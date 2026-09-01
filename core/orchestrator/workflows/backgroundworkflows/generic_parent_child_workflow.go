package backgroundworkflows

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	temporalutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	backgroundWorkflowStartToCloseTimeoutSec = env.GetUint64("BACKGROUND_WORKFLOW_START_TO_CLOSE_TIMEOUT_SEC", 300)
	backgroundWorkflowHeartbeatTimeoutSec    = env.GetUint64("BACKGROUND_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 150)
)

// getWorkflowName extracts the name of the workflow function from its pointer.
// This is used to ensure that the workflow name is correctly identified when starting child workflows.
func getWorkflowName(fnx interface{}) string {
	// It uses reflection to get the function concrete value pointer, passes it to FuncForPC to get the
	// function name. It returns the name in the below format -
	// github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows.<function_name>
	// Then, we split it to get the last part, which is the actual function name.
	strs := strings.Split(runtime.FuncForPC(reflect.ValueOf(fnx).Pointer()).Name(), ".")
	return strs[len(strs)-1]
}

// GenericParentWorkflowConfig contains configuration for the generic parent workflow
type GenericParentWorkflowConfig struct {
	WorkflowName          string
	BatchSize             int
	ChildWorkflowTimeout  time.Duration
	GetTotalCountActivity interface{}
	ChildWorkflowFunc     interface{}
}

// GenericChildWorkflowConfig contains configuration for the generic child workflow
type GenericChildWorkflowConfig struct {
	WorkflowName            string
	ActivityBatchSize       int
	MaxConcurrentActivities int
	ActivityTimeoutMinutes  int
	ListDataActivity        interface{}
	ProcessBatchActivity    interface{}
}

// GenericChildWorkflowResult represents the result of a generic child workflow
type GenericChildWorkflowResult struct {
	WorkflowID          string
	TotalItemsProcessed int
	SuccessfulItems     int
	FailedItems         int
	Error               string
	FailedResourceNames []string
}

// GenericParentWorkflowResult represents the result of a generic parent workflow
type GenericParentWorkflowResult struct {
	TotalItemsProcessed int
	TotalSuccessful     int
	TotalFailed         int
	ChildResults        []*GenericChildWorkflowResult
}

// GenericParentWorkflow is a reusable parent workflow that orchestrates multiple child workflows
func GenericParentWorkflow(ctx workflow.Context, config GenericParentWorkflowConfig) (*GenericParentWorkflowResult, error) {
	requestID := temporalutils.GeneratedUUID(ctx)
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": workflow.GetInfo(ctx).WorkflowExecution.ID,
		"requestID":  requestID,
	})
	logger := util.GetLogger(ctx)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	// Activity options for master workflow activities
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(backgroundWorkflowStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(backgroundWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Get total count first
	var totalCount int
	err = workflow.ExecuteActivity(ctx, config.GetTotalCountActivity).Get(ctx, &totalCount)
	if err != nil {
		logger.Error("Failed to get total count", "Error", err)
		return nil, err
	}
	logger.Infof("Starting Parent workflow '%s' -> Processing %d total items", config.WorkflowName, totalCount)

	// Number of child workflows needed
	numChildWorkflows := (totalCount + config.BatchSize - 1) / config.BatchSize

	// Create child workflows with controlled concurrency
	// Store both future and expected limit for each child workflow
	type childWorkflowInfo struct {
		future workflow.ChildWorkflowFuture
		limit  int
	}
	childFutures := make(map[string]childWorkflowInfo)
	var allResults []*GenericChildWorkflowResult

	// Start all child workflows immediately with controlled concurrency
	logger.Infof("Parent workflow '%s' -> Starting all %d child workflows", config.WorkflowName, numChildWorkflows)

	// Extract the workflow name from the child workflow function
	// This is required so Temporal can properly identify the workflow type
	childWorkflowName := getWorkflowName(config.ChildWorkflowFunc)

	// Start child workflows with chunked data fetching
	for i := 0; i < numChildWorkflows; i++ {
		offset := i * config.BatchSize
		limit := config.BatchSize
		if offset+limit > totalCount {
			limit = totalCount - offset
		}

		childWorkflowTimeout := config.ChildWorkflowTimeout
		if childWorkflowTimeout == 0 {
			childWorkflowTimeout = 10 * time.Minute
		}
		// Using deterministic childWorkflowID based on offset and limit to ensure replay determinism
		childWorkflowID := fmt.Sprintf("%s-child-%d-%d", workflow.GetInfo(ctx).WorkflowExecution.ID, offset, limit)
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:         childWorkflowID,
			WorkflowRunTimeout: childWorkflowTimeout,
		})

		// Pass offset and limit to child workflow for chunked fetching
		// Pass the workflow name as a string so Temporal can identify the workflow type
		future := workflow.ExecuteChildWorkflow(childCtx, childWorkflowName, offset, limit)
		childFutures[childWorkflowID] = childWorkflowInfo{
			future: future,
			limit:  limit,
		}
	}

	// Wait for all child workflows to complete
	for childWorkflowID, childInfo := range childFutures {
		var result *GenericChildWorkflowResult
		err = childInfo.future.Get(ctx, &result)

		if err != nil {
			logger.Errorf("Parent workflow '%s' -> Child workflow '%s' failed. Error: %v", config.WorkflowName, childWorkflowID, err)
			// Continue with other workflows even if one fails
			allResults = append(allResults, &GenericChildWorkflowResult{
				WorkflowID:          childWorkflowID,
				Error:               err.Error(),
				TotalItemsProcessed: childInfo.limit,
				FailedItems:         childInfo.limit,
			})
		} else {
			allResults = append(allResults, result)
			if result != nil {
				logger.Infof("Parent workflow '%s' -> Child workflow '%s' completed. %d items processed, %d successful, %d failed", config.WorkflowName, childWorkflowID, result.TotalItemsProcessed, result.SuccessfulItems, result.FailedItems)
			}
		}
	}

	// Aggregate results
	totalProcessed := 0
	totalSuccessful := 0
	totalFailed := 0

	for _, result := range allResults {
		if result != nil {
			totalProcessed += result.TotalItemsProcessed
			totalSuccessful += result.SuccessfulItems
			totalFailed += result.FailedItems
		}
	}
	logger.Infof("Parent workflow '%s' completed. -> Total items processed: %d, successful: %d, failed: %d", config.WorkflowName, totalProcessed, totalSuccessful, totalFailed)

	return &GenericParentWorkflowResult{
		TotalItemsProcessed: totalProcessed,
		TotalSuccessful:     totalSuccessful,
		TotalFailed:         totalFailed,
		ChildResults:        allResults,
	}, nil
}

// GenericChildWorkflow is a reusable child workflow that processes data in batches
func GenericChildWorkflow(ctx workflow.Context, offset, limit int, config GenericChildWorkflowConfig) (*GenericChildWorkflowResult, error) {
	// Get parent request ID from context if available
	parentRequestID := ""
	if parentCtx := workflow.GetInfo(ctx).ParentWorkflowExecution; parentCtx != nil {
		parentRequestID = parentCtx.ID
	}
	if parentRequestID == "" {
		parentRequestID = workflow.GetInfo(ctx).WorkflowExecution.ID
	}

	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": workflow.GetInfo(ctx).WorkflowExecution.ID,
		"offset":     offset,
		"limit":      limit,
		"requestID":  parentRequestID,
	})
	logger := util.GetLogger(ctx)
	logger.Infof("Starting child workflow '%s' for items %d to %d", config.WorkflowName, offset, offset+limit-1)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return &GenericChildWorkflowResult{
			WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID,
			Error:      err.Error(),
		}, err
	}

	// Activity options for child workflow activities
	activityTimeout := retryPolicy.StartToCloseTimeout
	if config.ActivityTimeoutMinutes > 0 {
		activityTimeout = time.Duration(config.ActivityTimeoutMinutes) * time.Minute
	}
	defaultActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: activityTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:        1, // No retries by default
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions)

	// Retry options specifically for ListDataActivity
	listDataRetryOptions := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(backgroundWorkflowStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(backgroundWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctxWithListDataRetry := workflow.WithActivityOptions(ctx, listDataRetryOptions)

	// Fetch all assigned data in one database call using generic approach
	var totalItemsProcessed int
	var allResults []interface{}
	var fetchedData interface{}

	err = workflow.ExecuteActivity(ctxWithListDataRetry, config.ListDataActivity, offset, limit).Get(ctx, &fetchedData)
	if err != nil {
		errorMsg := fmt.Sprintf("ListData activity failed. Error: %v, Offset: %d, Limit: %d", err, offset, limit)
		logger.Errorf("Child workflow '%s' -> %s", config.WorkflowName, errorMsg)
		return &GenericChildWorkflowResult{
			WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID,
			Error:      err.Error(),
		}, err
	}

	// Process the fetched data generically
	totalItemsProcessed, allResults = processGenericData(ctx, fetchedData, config, logger)
	logger.Debugf("Child workflow '%s' -> fetched %d items in one call, processing with max %d concurrent activities", config.WorkflowName, totalItemsProcessed, config.MaxConcurrentActivities)

	// Aggregate results from all completed activities
	totalSuccessCount := 0
	totalFailureCount := 0
	var allFailedResourceNames []string
	var allFailedResourceErrors []backgroundactivities.ParentChildWorkflowError

	for _, result := range allResults {
		if resultMap, ok := result.(map[string]interface{}); ok {
			if val, exists := resultMap["Successful"]; exists {
				if successful, ok := val.(float64); ok {
					totalSuccessCount += int(successful)
				}
			}
			if val, exists := resultMap["Failed"]; exists {
				if failed, ok := val.(float64); ok {
					totalFailureCount += int(failed)
				}
			}
			// Collect failed resource names
			if val, exists := resultMap["FailedResourceNames"]; exists {
				if failedResourceNames, ok := val.([]interface{}); ok {
					for _, resourceName := range failedResourceNames {
						if name, ok := resourceName.(string); ok {
							allFailedResourceNames = append(allFailedResourceNames, name)
						}
					}
				}
			}
			// Collect failed resource errors (for logging, not returned in workflow result)
			if val, exists := resultMap["FailedResourceErrors"]; exists {
				if failedResourceErrors, ok := val.([]interface{}); ok {
					for _, resourceError := range failedResourceErrors {
						if resourceErrorMap, ok := resourceError.(map[string]interface{}); ok {
							resourceName, _ := resourceErrorMap["ResourceName"].(string)
							errorMsg, _ := resourceErrorMap["Error"].(string)
							allFailedResourceErrors = append(allFailedResourceErrors, backgroundactivities.ParentChildWorkflowError{
								ResourceName: resourceName,
								Error:        errorMsg,
							})
						}
					}
				}
			}
		} else {
			logger.Warnf("Child Workflow '%s' -> Unexpected result type: %T", config.WorkflowName, result)
		}
	}
	if len(allFailedResourceErrors) > 0 {
		logger.Errorf("Child workflow '%s' -> All failed resource errors: %+v", config.WorkflowName, allFailedResourceErrors)
	}
	logger.Infof("Child workflow '%s' completed -> Total items processed: %d, successful: %d, failed: %d", config.WorkflowName, totalItemsProcessed, totalSuccessCount, totalFailureCount)

	return &GenericChildWorkflowResult{
		WorkflowID:          workflow.GetInfo(ctx).WorkflowExecution.ID,
		TotalItemsProcessed: totalItemsProcessed,
		SuccessfulItems:     totalSuccessCount,
		FailedItems:         totalFailureCount,
		FailedResourceNames: allFailedResourceNames,
	}, nil
}

// processGenericData processes any slice type generically
func processGenericData(ctx workflow.Context, fetchedData interface{}, config GenericChildWorkflowConfig, logger log.Logger) (int, []interface{}) {
	var allResults []interface{}

	items := reflect.ValueOf(fetchedData)
	if items.Kind() != reflect.Slice {
		errorMsg := fmt.Sprintf("Child Workflow '%s'-> expected slice, got %T", config.WorkflowName, fetchedData)
		logger.Error(errorMsg)
		return 0, allResults
	}

	totalItems := items.Len()
	currentIndex := 0
	batchNumber := 0

	// Process items in controlled batches to limit RPS
	for currentIndex < totalItems {
		// Process a batch of activities with controlled concurrency
		var batchFutures []workflow.Future
		var batchDataBatches [][]interface{}

		// Start up to MaxConcurrentActivities activities
		for i := 0; i < config.MaxConcurrentActivities && currentIndex < totalItems; i++ {
			batchSize := config.ActivityBatchSize
			if currentIndex+batchSize > totalItems {
				batchSize = totalItems - currentIndex
			}

			// Extract a batch of items from the fetched list
			dataBatch := make([]interface{}, batchSize)
			for j := 0; j < batchSize; j++ {
				dataBatch[j] = items.Index(currentIndex + j).Interface()
			}
			batchDataBatches = append(batchDataBatches, dataBatch)
			batchNumber++
			logger.Infof("Child Workflow '%s'-> Batch %d with %d items (index: %d to %d)", config.WorkflowName, batchNumber, len(dataBatch), currentIndex, currentIndex+len(dataBatch)-1)

			// Process this batch immediately
			future := workflow.ExecuteActivity(ctx, config.ProcessBatchActivity, dataBatch)
			batchFutures = append(batchFutures, future)

			currentIndex += len(dataBatch)
		}

		// Wait for current batch of activities to complete
		if len(batchFutures) > 0 {
			for i, future := range batchFutures {
				dataBatch := batchDataBatches[i]
				var batchResult interface{}
				err := future.Get(ctx, &batchResult)

				if err != nil {
					errorMsg := fmt.Sprintf("Child Workflow '%s' -> Batch %d failed: %v", config.WorkflowName, i, err)
					logger.Error(errorMsg)
					allResults = append(allResults, map[string]interface{}{
						"error":      err.Error(),
						"batch_size": len(dataBatch),
						"Successful": float64(0),
						"Failed":     float64(len(dataBatch)),
					})
				} else {
					allResults = append(allResults, batchResult)
				}
			}
		}
	}
	return totalItems, allResults
}
