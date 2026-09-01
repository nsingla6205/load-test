package backgroundworkflows

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	temporalutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	aggregateTieringFullnessThresholdDefault = 0
	aggregateTieringFullnessThresholdFull    = 100
	poolAutoTierAutoResizeWFIDPlaceholder    = "Account_%d_Location_%s_Pool_%s_Ops_AutoTiering-HotTier-AutoResize"
)

var (
	ConsecutiveUpdatePoolTimeGapAllowedMinutes                 = env.GetFloat64("CONSECUTIVE_UPDATE_POOL_TIME_GAP_ALLOWED_MINUTES", 240)
	AutoTieringSyncActivityMaxAttempts                         = env.GetInt("AUTO_TIERING_SYNC_ACTIVITY_MAX_ATTEMPTS", 1)
	syncVSAAutoTieringWorkflowStartToCloseTimeoutSec           = env.GetUint64("SYNC_VSA_AUTO_TIERING_WORKFLOW_START_TO_CLOSE_TIMEOUT_SEC", 600)
	autoTieringPauseResumeWorkflowStartToCloseTimeoutSec       = env.GetUint64("AUTO_TIERING_PAUSE_RESUME_WORKFLOW_START_TO_CLOSE_TIMEOUT_SEC", 600)
	autoTieringHotTierAutoResizeWorkflowStartToCloseTimeoutSec = env.GetUint64("AUTO_TIERING_HOT_TIER_AUTO_RESIZE_WORKFLOW_START_TO_CLOSE_TIMEOUT_SEC", 600)
	syncVSAAutoTieringWorkflowHeartbeatTimeoutSec              = env.GetUint64("SYNC_VSA_AUTO_TIERING_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
	autoTieringPauseResumeWorkflowHeartbeatTimeoutSec          = env.GetUint64("AUTO_TIERING_PAUSE_RESUME_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
	autoTieringHotTierAutoResizeWorkflowHeartbeatTimeoutSec    = env.GetUint64("AUTO_TIERING_HOT_TIER_AUTO_RESIZE_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
	AutoTieringPauseResumeActivityMaxAttempts                  = env.GetInt("AUTO_TIERING_PAUSE_RESUME_ACTIVITY_MAX_ATTEMPTS", 3)
)

// SyncVSAAutoTieringWorkflow is a workflow that synchronizes auto-tiering for all pools.
func SyncVSAAutoTieringWorkflow(ctx workflow.Context) error {
	requestID := utils.RandomUUID()
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": workflow.GetInfo(ctx).WorkflowExecution.ID,
		// Adding a unique request ID for tracking purposes
		"requestID": requestID,
	})
	logger := util.GetLogger(ctx)
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(syncVSAAutoTieringWorkflowStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(syncVSAAutoTieringWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(AutoTieringSyncActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	autoTierActivity := &backgroundactivities.AutoTierSyncActivity{}
	commonActivities := &activities.CommonActivities{}

	filterStates := []string{datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateDegraded}
	var pools []*database.PoolIdentifier
	err = workflow.ExecuteActivity(ctx, commonActivities.ListPoolsUUID, filterStates).Get(ctx, &pools)
	if err != nil {
		logger.Errorf("ListPools activity failed with error: %v", err)
		return err
	}

	// Assuming the map format is -> map[poolUUID]map[consumptionType]consumptionValue, where consumptionType can be "hotTier" or "coldTier"
	var poolsConsumptionMap map[string]map[string]float64
	err = workflow.ExecuteActivity(ctx, autoTierActivity.FetchAndSavePoolsTieringInfo, pools).Get(ctx, &poolsConsumptionMap)
	if err != nil {
		logger.Errorf("Fetching auto-tier consumption from ontap activity failed with error: %v", err)
		return err
	}

	err = workflow.ExecuteActivity(ctx, autoTierActivity.UpdatePoolTieringConsumptionInDB, poolsConsumptionMap).Get(ctx, nil)
	if err != nil {
		// Log the error but continue processing. This is to ensure that even if the DB update fails,
		// we still attempt to pause/resume auto-tiering based on the fetched consumption data.
		// As for the update failure, it can be retried in the next scheduled run of this workflow.
		logger.Errorf("Saving auto-tier consumption in pool db activity failed with error: %v", err)
	}

	var segregatedPools map[string][]*database.PoolIdentifier
	err = workflow.ExecuteActivity(ctx, autoTierActivity.SegregatePools, pools, poolsConsumptionMap).Get(ctx, &segregatedPools)
	if err != nil {
		logger.Errorf("SegregatePools activity failed with error: %v", err)
		return err
	}

	pauseResumeWFFuturesList := make(map[string]struct {
		Future  workflow.ChildWorkflowFuture
		Context workflow.Context
	})

	// Iterate over the segregated pools and start child workflows to pause or resume auto-tiering.
	for operation, poolIdentifiers := range segregatedPools {
		if operation == backgroundactivities.PoolsToAutoResizeKey {
			// Auto-resize operations will be handled separately after this loop.
			continue
		}
		for _, pool := range poolIdentifiers {
			childWFCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				TaskQueue:             temporalutils.BackgroundTaskQueue,
				WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
				WorkflowTaskTimeout:   temporalutils.GetWorkflowGlobalTimeout(),
				// Since it's an independent pool level workflow, we can set the ParentClosePolicy to abandon.
				// This means that if the parent workflow is closed, this child workflow will continue to run.
				// And since we have a workflow timeout set, it will eventually complete or fail and not orphan out.
				ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
			})

			// Add extra logger fields to the child workflow context for better traceability.
			// This will help in identifying the parent workflow ID in the logs of the child workflow.
			// It is useful for debugging and tracing the execution flow of the workflows.
			childWFCtx = util.AddExtraLoggerFields(childWFCtx, map[string]interface{}{
				"parentWorkflowID": workflow.GetInfo(ctx).WorkflowExecution.ID,
			})

			// Execute the child workflow asynchronously to pause or resume auto-tiering for the pool.
			fut := workflow.ExecuteChildWorkflow(childWFCtx, AutoTieringPauseResumeWorkflow, pool, operation)
			// Store the future and context in the map with the pool UUID as the key.
			pauseResumeWFFuturesList[pool.UUID] = struct {
				Future  workflow.ChildWorkflowFuture
				Context workflow.Context
			}{
				Future:  fut,
				Context: childWFCtx,
			}
		}
	}

	// Wait for all child workflows to complete. This will ensure that we avoid any
	// race conditions on the pool resource state update.
	for poolID, wfFuture := range pauseResumeWFFuturesList {
		// Wait for each child workflow to complete.
		err = wfFuture.Future.Get(wfFuture.Context, nil)
		if err != nil {
			logger.Errorf("Child workflow for pool %s pause/resume failed with error: %v", poolID, err)
			// Continue processing other child workflows even if one fails.
			continue
		}
		logger.Infof("Child workflow for pool %s pause/resume completed successfully", poolID)
	}

	for _, pool := range segregatedPools[backgroundactivities.PoolsToAutoResizeKey] {
		location, err := utils.GetLocationFromVendorID(pool.VendorID)
		if err != nil {
			logger.Errorf("Failed to get location from vendor ID, error: %v", err)
			return err
		}
		workflowID := fmt.Sprintf(poolAutoTierAutoResizeWFIDPlaceholder, pool.AccountID, location, pool.UUID)

		var lastExecutionTime *time.Time
		err = workflow.ExecuteActivity(ctx, (&activities.WFLastExecutionActivity{}).GetWorkflowLastExecutionTime, workflowID).Get(ctx, &lastExecutionTime)
		if err != nil {
			logger.Errorf("Failed to get last workflow completion time for pool %s: %v", pool.Name, err)
			continue
		}

		// If the last completion time is within 4 hours, skip the auto-resize for this pool.
		// This completion time is fetched from the previous run of the auto-resize workflow.
		// This run could be a success or a failure run. We will skip running again in both cases.
		if workflow.Now(ctx).Sub(*lastExecutionTime).Minutes() <= ConsecutiveUpdatePoolTimeGapAllowedMinutes {
			logger.Infof("Skipping auto-resize for pool %s in location %s for account %d as last run was within 4 hours", pool.Name, location, pool.AccountID)
			continue
		}

		childWFCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			TaskQueue:             temporalutils.BackgroundTaskQueue,
			WorkflowID:            workflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
			WorkflowTaskTimeout:   temporalutils.GetWorkflowGlobalTimeout(),
			// Since it's an independent pool level workflow, we can set the ParentClosePolicy to
			// abandon. This means that if the parent workflow is closed, this child workflow will continue to run.
			// And since we have a workflow timeout set, it will eventually complete or fail and not orphan out.
			ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
		})
		logger.Infof("Starting workflow to auto-resize hot tier in pool %s with ID %s", pool.Name, pool.UUID)

		// Add extra logger fields to the child workflow context for better traceability.
		// This will help in identifying the parent workflow ID in the logs of the child workflow.
		// It is useful for debugging and tracing the execution flow of the workflows.
		childWFCtx = util.AddExtraLoggerFields(childWFCtx, map[string]interface{}{
			"parentWorkflowID":                      workflow.GetInfo(ctx).WorkflowExecution.ID,
			string(middleware.RequestCorrelationID): requestID,
		})

		// Execute the child workflow asynchronously to auto-resize hot tier for the pool and hydrating it to CCFE.
		workflow.ExecuteChildWorkflow(childWFCtx, AutoTieringHotTierAutoResizeWorkflow, pool)
	}

	// Sleep for a few seconds to ensure that the workflow does not complete immediately.
	// This is to ensure that the last workflow gets time to start.
	err = workflow.Sleep(ctx, 2*time.Second)
	if err != nil {
		return err
	}

	return nil
}

// AutoTieringPauseResumeWorkflow is a workflow that pauses or resumes auto-tiering for a specific pool.
func AutoTieringPauseResumeWorkflow(ctx workflow.Context, poolIdentifier database.PoolIdentifier, operation string) error {
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": workflow.GetInfo(ctx).WorkflowExecution.ID,
		"customerID": poolIdentifier.AccountID,
	})
	logger := util.GetLogger(ctx)
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(autoTieringPauseResumeWorkflowStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(autoTieringPauseResumeWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(AutoTieringPauseResumeActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	autoTierActivity := &backgroundactivities.AutoTierSyncActivity{}
	syncSnapshotActivity := &backgroundactivities.SyncSnapshotActivity{}

	if operation == backgroundactivities.PoolsToPauseKey {
		logger.Infof("Starting child workflow to pause auto-tiering in pool %s with ID %s", poolIdentifier.Name, poolIdentifier.UUID)
	} else {
		logger.Infof("Starting child workflow to resume auto-tiering in pool %s with ID %s", poolIdentifier.Name, poolIdentifier.UUID)
	}

	var pool *datamodel.Pool
	err = workflow.ExecuteActivity(ctx, syncSnapshotActivity.FetchPoolByUUID, poolIdentifier.UUID, poolIdentifier.AccountID).Get(ctx, &pool)
	if err != nil {
		logger.Errorf("FetchPoolByUUID activity execution failed for pool %s. Error: %v", poolIdentifier.Name, err)
		return err
	}

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &pool.ID).Get(ctx, &dbNodes)
	if err != nil {
		return err
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	tieringFullnessThreshold := aggregateTieringFullnessThresholdDefault
	if operation == backgroundactivities.PoolsToPauseKey {
		pool.AutoTieringConfig.TieringStatus = datamodel.TieringStatusPaused
		// If auto tiering is paused, we set the auto-tiering threshold to 100%
		tieringFullnessThreshold = aggregateTieringFullnessThresholdFull
	} else {
		pool.AutoTieringConfig.TieringStatus = datamodel.TieringStatusResumed
	}

	err = workflow.ExecuteActivity(ctx, autoTierActivity.ToggleHotTierBypassModeForPoolVolumes, pool).Get(ctx, nil)
	if err != nil {
		// Logging the error and marking tiering status with partial enum, so that
		// it can be retried in the next sync.
		logger.Errorf("Failed to execute ToggleHotTierBypassModeForPoolVolumes for pool %s: %v", pool.Name, err)
		if operation == backgroundactivities.PoolsToPauseKey {
			pool.AutoTieringConfig.TieringStatus = datamodel.TieringStatusPartiallyPaused
		} else {
			pool.AutoTieringConfig.TieringStatus = datamodel.TieringStatusPartiallyResumed
		}
	}

	poolActivity := &activities.PoolActivity{}
	currentVlmConfig := &vlm.VLMConfig{}
	err = workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, pool).Get(ctx, &currentVlmConfig)
	if err != nil {
		return workflows.ConvertToVSAError(err)
	}

	aggrNames := make([]string, 0)
	for _, aggrName := range currentVlmConfig.DataAggr {
		aggrNames = append(aggrNames, aggrName.Name)
	}
	err = workflow.ExecuteActivity(ctx, autoTierActivity.UpdateAggregatesInOntap, node, tieringFullnessThreshold, aggrNames).Get(ctx, nil)
	if err != nil {
		logger.Errorf("Failed to execute UpdateAggregatesInOntap for pool %s: %v", pool.Name, err)
		if operation == backgroundactivities.PoolsToPauseKey {
			pool.AutoTieringConfig.TieringStatus = datamodel.TieringStatusPartiallyPaused
		} else {
			pool.AutoTieringConfig.TieringStatus = datamodel.TieringStatusPartiallyResumed
		}
	} else {
		pool.AutoTieringConfig.TieringFullnessThreshold = int64(tieringFullnessThreshold)
	}

	err = workflow.ExecuteActivity(ctx, autoTierActivity.UpdatePoolTieringThresholdAndStatus, pool.UUID, pool.AutoTieringConfig.TieringFullnessThreshold, pool.AutoTieringConfig.TieringStatus).Get(ctx, nil)
	if err != nil {
		return err
	}

	if operation == backgroundactivities.PoolsToPauseKey {
		logger.Infof("Successfully finished child workflow to pause auto-tiering in pool %s with ID %s", poolIdentifier.Name, poolIdentifier.UUID)
	} else {
		logger.Infof("Successfully finished child workflow to resume auto-tiering in pool %s with ID %s", poolIdentifier.Name, poolIdentifier.UUID)
	}

	return nil
}

type autoTieringHotTierAutoResizeWorkflow struct {
	completionTime time.Time
}

// AutoTieringHotTierAutoResizeWorkflow is a workflow that handles the auto-resize of the hot tier for a specific pool.
func AutoTieringHotTierAutoResizeWorkflow(ctx workflow.Context, pool *database.PoolIdentifier) error {
	wf := new(autoTieringHotTierAutoResizeWorkflow)
	if err := wf.Setup(ctx, pool); err != nil {
		return err
	}
	_, err := wf.Run(ctx, pool)
	if err != nil {
		wf.completionTime = workflow.Now(ctx)
		return err
	}
	wf.completionTime = workflow.Now(ctx)
	return nil
}

func (wf *autoTieringHotTierAutoResizeWorkflow) Setup(ctx workflow.Context, pool *database.PoolIdentifier) error {
	info := workflow.GetInfo(ctx)
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": info.WorkflowExecution.ID, "customerID": pool.AccountID})
	wf.completionTime = workflow.Now(ctx)
	return workflow.SetQueryHandler(ctx, workflows.StatusQueryName, func() (*time.Time, error) {
		return &wf.completionTime, nil
	})
}

func (wf *autoTieringHotTierAutoResizeWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, error) {
	poolIdentifier := args[0].(*database.PoolIdentifier)
	logger := util.GetLogger(ctx)
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(autoTieringHotTierAutoResizeWorkflowStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(autoTieringHotTierAutoResizeWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(AutoTieringSyncActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	commonActivities := activities.CommonActivities{}
	syncSnapshotActivity := &backgroundactivities.SyncSnapshotActivity{}
	poolActivity := &activities.PoolActivity{}

	var pool *datamodel.Pool
	err = workflow.ExecuteActivity(ctx, syncSnapshotActivity.FetchPoolByUUID, poolIdentifier.UUID, poolIdentifier.AccountID).Get(ctx, &pool)
	if err != nil {
		logger.Errorf("FetchPoolByUUID activity execution failed for pool %s. Error: %v", poolIdentifier.Name, err)
		return nil, err
	}

	job := &datamodel.Job{
		Type:         string(datamodel.JobTypeUpdatePool),
		State:        string(datamodel.JobsStateNEW),
		IsAdminJob:   true,
		ResourceName: pool.UUID,
		AccountID:    sql.NullInt64{Int64: pool.AccountID, Valid: true},
	}

	poolRegionParts := strings.Split(pool.VendorID, "-")[:len(strings.Split(pool.VendorID, "-"))-1]
	poolRegion := strings.Join(poolRegionParts, "-")

	// We are increasing the hot tier size by 10%. The result is in round off GiB.
	newHotTierSizeInBytes := uint64(int64(float64(pool.AutoTieringConfig.HotTierSizeInBytes)*(1+0.01*backgroundactivities.AutoTierHotTierAutoResizeIncreasePercent)/(1<<30))) * (1 << 30)

	poolUpdateParams := &common.UpdatePoolParams{
		HotTierSizeInBytes:        newHotTierSizeInBytes,
		AutoResizeTriggeredUpdate: true,
		// Copying below fields as it is since they are required internally within the update pool workflow.
		AllowAutoTiering:        pool.AllowAutoTiering,
		EnableHotTierAutoResize: pool.AutoTieringConfig.EnableHotTierAutoResize,
		AccountName:             pool.Account.Name,
		PoolId:                  pool.UUID,
		SizeInBytes:             uint64(pool.SizeInBytes),
		TotalThroughputMibps:    pool.PoolAttributes.ThroughputMibps,
		TotalIops:               &pool.PoolAttributes.Iops,
		Description:             pool.Description,
		Region:                  poolRegion,
	}

	var createdJob *datamodel.Job
	err = workflow.ExecuteActivity(ctx, commonActivities.CreateJob, job).Get(ctx, &createdJob)
	if err != nil {
		return nil, err
	}

	err = workflow.ExecuteActivity(ctx, poolActivity.UpdatingPool, pool).Get(ctx, nil)
	if err != nil {
		return nil, err
	}

	updatePoolRunTimeout := temporalutils.GetUpdatePoolWorkflowRunTimeout(pool.LargeCapacity, true)
	childWFCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            createdJob.WorkflowID,
		TaskQueue:             temporalutils.BackgroundTaskQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowTaskTimeout:   temporalutils.GetWorkflowGlobalTimeout(),
		WorkflowRunTimeout:    *updatePoolRunTimeout,
		ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
	})
	err = workflow.ExecuteChildWorkflow(childWFCtx, workflows.UpdatePoolWorkflow, poolUpdateParams, pool).Get(ctx, nil)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
