package backgroundworkflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	middlewarelog "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	authTypeReconciliationParentTimeout           = time.Hour
	authTypeReconciliationJobActivityStartToClose = 30 * time.Second
	authTypeReconciliationJobActivityMaxAttempts  = 3
	authTypeReconciliationJobActivityMaxInterval  = 10 * time.Second
	// authTypeReconciliationRunTimeoutSlack must stay above the worst-case job
	// persist retry budget. WorkflowRunTimeout is enforced by the server and does
	// not run the in-workflow failure defer.
	authTypeReconciliationRunTimeoutSlack = 5 * time.Minute
)

// AuthTypeReconciliationWorkflowRunTimeout is the server-side run timeout for
// the on-demand parent workflow. It is longer than the in-workflow parent timer
// so the final job-status write can finish after that timer fires.
func AuthTypeReconciliationWorkflowRunTimeout(parentTimeout time.Duration) time.Duration {
	if parentTimeout <= 0 {
		parentTimeout = authTypeReconciliationParentTimeout
	}
	return parentTimeout + authTypeReconciliationRunTimeoutSlack
}

func authTypeReconciliationJobPersistBudget() time.Duration {
	budget := time.Duration(authTypeReconciliationJobActivityMaxAttempts) * authTypeReconciliationJobActivityStartToClose
	interval := time.Second
	for attempt := 1; attempt < authTypeReconciliationJobActivityMaxAttempts; attempt++ {
		budget += interval
		interval *= 2
		if interval > authTypeReconciliationJobActivityMaxInterval {
			interval = authTypeReconciliationJobActivityMaxInterval
		}
	}
	return budget
}

// ReconcilePoolAuthTypeAPIWorkflowParams carries one API-selected pool.
type ReconcilePoolAuthTypeAPIWorkflowParams struct {
	PoolUUID         string        `json:"poolUuid"`
	DesiredCAURI     string        `json:"desiredCaUri"`
	OperationTimeout time.Duration `json:"operationTimeout"`
}

// ReconcilePoolAuthTypesAPIWorkflowParams carries the complete on-demand request.
type ReconcilePoolAuthTypesAPIWorkflowParams struct {
	JobID            string                                  `json:"jobId"`
	BatchSize        int                                     `json:"batchSize"`
	OperationTimeout time.Duration                           `json:"operationTimeout"`
	ParentTimeout    time.Duration                           `json:"parentTimeout"`
	JobPayload       models.AuthTypeReconciliationJobPayload `json:"jobPayload"`
}

// ReconcilePoolAuthTypeForDesiredCAWorkflow reconciles one password-auth pool
// against the desired CA captured in this workflow's immutable input.
func ReconcilePoolAuthTypeForDesiredCAWorkflow(
	ctx workflow.Context,
	poolUUID, desiredCAURI string,
) (workflowErr error) {
	ctx, logger := withPoolOperationLogging(ctx, poolUUID, poolOperationAuthReconciliation)
	logger.Info("Pool AuthType 1 to 2 reconciliation started", "desiredCAURI", desiredCAURI)
	defer func() {
		if workflowErr != nil {
			logger.Error("Pool AuthType 1 to 2 reconciliation finished", "outcome", "failure", "error", workflowErr)
			return
		}
		logger.Info("Pool AuthType 1 to 2 reconciliation finished", "outcome", "success")
	}()

	retryPolicy, err := workflows.PopulateRotationRetryPolicyParams()
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	})
	reconcileActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	logger.Info("Promoting demoted pool credentials from AuthType 1 to AuthType 2", "desiredCAURI", desiredCAURI)
	return workflow.ExecuteActivity(
		ctx,
		reconcileActivity.ReconcileAuthType1PoolForDesiredCA,
		poolUUID,
		desiredCAURI,
	).Get(ctx, nil)
}

// ReconcilePoolAuthTypeAPIWorkflow queues one API-selected pool through the
// normal pool-operation sequencer and waits for that operation to finish. The
// operation timeout intentionally covers both sequencer queue time and runtime.
func ReconcilePoolAuthTypeAPIWorkflow(
	ctx workflow.Context,
	params ReconcilePoolAuthTypeAPIWorkflowParams,
) (workflowErr error) {
	ctx, _ = withPoolOperationLogging(ctx, params.PoolUUID, poolOperationAuthReconciliation)
	if params.PoolUUID == "" || params.DesiredCAURI == "" {
		return temporal.NewNonRetryableApplicationError(
			"pool UUID and desired CA URI are required for API auth reconciliation",
			"InvalidArgument",
			nil,
		)
	}

	operationTimeout := params.OperationTimeout
	if operationTimeout <= 0 {
		operationTimeout = 20 * time.Minute
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: operationTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        time.Second,
			BackoffCoefficient:     2,
			MaximumInterval:        30 * time.Second,
			MaximumAttempts:        10,
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	})

	requestWorkflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	if err := workflow.ExecuteActivity(
		ctx,
		controlActivity.ExecutePoolAuthTypeReconciliationForDesiredCASequentially,
		params.PoolUUID,
		params.DesiredCAURI,
		requestWorkflowID,
		operationTimeout,
	).Get(ctx, nil); err != nil {
		return fmt.Errorf("queue pool %s auth reconciliation: %w", params.PoolUUID, err)
	}

	operationWorkflowID := backgroundactivities.AuthTypeReconciliationOperationWorkflowID(
		params.PoolUUID,
		requestWorkflowID,
	)
	if err := workflow.ExecuteActivity(
		ctx,
		controlActivity.WaitForAuthTypeReconciliationOperation,
		operationWorkflowID,
	).Get(ctx, nil); err != nil {
		return fmt.Errorf("pool %s auth reconciliation: %w", params.PoolUUID, err)
	}
	return nil
}

// ReconcilePoolAuthTypesAPIWorkflow owns on-demand reconciliation dispatch,
// bounded concurrency, per-pool results, and the aggregate parent job.
func ReconcilePoolAuthTypesAPIWorkflow(
	ctx workflow.Context,
	params ReconcilePoolAuthTypesAPIWorkflowParams,
) (workflowErr error) {
	if params.JobID == "" || params.BatchSize <= 0 {
		return temporal.NewNonRetryableApplicationError(
			"job ID and positive batch size are required",
			"InvalidArgument",
			nil,
		)
	}

	commonActivity := &activities.CommonActivities{}
	logger := util.GetLogger(ctx)
	recordedOutcome := false
	defer func() {
		if workflowErr != nil && !recordedOutcome {
			recordAuthReconciliationJobFailure(ctx, logger, commonActivity, params.JobID, workflowErr)
		}
	}()
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: authTypeReconciliationJobActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    authTypeReconciliationJobActivityMaxInterval,
			MaximumAttempts:    authTypeReconciliationJobActivityMaxAttempts,
		},
	})
	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: params.JobID},
		State:     string(datamodel.JobsStatePROCESSING),
	}
	if err := workflow.ExecuteActivity(ctx, commonActivity.UpdateJobStatus, job).Get(ctx, nil); err != nil {
		return fmt.Errorf("mark auth reconciliation job %s processing: %w", params.JobID, err)
	}

	results := append([]models.AuthTypeReconciliationPoolResult(nil), params.JobPayload.Results...)
	poolIndexes := make([]int, 0, len(results))
	for index := range results {
		if results[index].Status == "QUEUED" {
			poolIndexes = append(poolIndexes, index)
		}
	}

	parentTimeout := params.ParentTimeout
	if parentTimeout <= 0 {
		parentTimeout = authTypeReconciliationParentTimeout
	}
	operationTimeout := params.OperationTimeout
	if operationTimeout <= 0 {
		operationTimeout = 20 * time.Minute
	}
	parentTimer := workflow.NewTimer(ctx, parentTimeout)
	timedOut := false

	for start := 0; start < len(poolIndexes) && !timedOut; start += params.BatchSize {
		end := start + params.BatchSize
		if end > len(poolIndexes) {
			end = len(poolIndexes)
		}
		children := make([]authReconciliationChildExecution, 0, end-start)
		for _, resultIndex := range poolIndexes[start:end] {
			result := &results[resultIndex]
			result.Status = "PROCESSING"
			childID := fmt.Sprintf("%s-pool-%s", params.JobID, result.PoolUUID)
			result.JobID = childID
			childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				WorkflowID:         childID,
				WorkflowRunTimeout: operationTimeout,
				ParentClosePolicy:  enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
			})
			children = append(children, authReconciliationChildExecution{
				resultIndex: resultIndex,
				future: workflow.ExecuteChildWorkflow(
					childCtx,
					ReconcilePoolAuthTypeAPIWorkflow,
					ReconcilePoolAuthTypeAPIWorkflowParams{
						PoolUUID:         result.PoolUUID,
						DesiredCAURI:     params.JobPayload.DesiredCAURI,
						OperationTimeout: operationTimeout,
					},
				),
			})
		}
		params.JobPayload.Results = results
		if err := persistAuthTypeReconciliationResults(ctx, commonActivity, params); err != nil {
			return err
		}
		pending := len(children)
		selector := workflow.NewSelector(ctx)
		for _, child := range children {
			resultIndex := child.resultIndex
			selector.AddFuture(child.future, func(future workflow.Future) {
				recordAuthReconciliationChildResult(results, resultIndex, future.Get(ctx, nil))
				pending--
			})
		}
		selector.AddFuture(parentTimer, func(workflow.Future) {
			timedOut = true
		})
		for pending > 0 && !timedOut {
			selector.Select(ctx)
		}
		if timedOut {
			break
		}
		params.JobPayload.Results = results
		if err := persistAuthTypeReconciliationResults(ctx, commonActivity, params); err != nil {
			return err
		}
	}

	if timedOut {
		for index := range results {
			if results[index].Status == "QUEUED" || results[index].Status == "PROCESSING" {
				results[index].Status = "ERROR"
				results[index].Reason = "parent auth-type reconciliation timed out"
			}
		}
	}
	params.JobPayload.Results = results
	if err := persistAuthTypeReconciliationResults(ctx, commonActivity, params); err != nil {
		return err
	}

	failed := 0
	for _, result := range results {
		switch result.Status {
		case "ERROR":
			failed++
		}
	}
	job.State = string(datamodel.JobsStateDONE)
	job.ErrorDetails = ""
	if failed > 0 {
		job.State = string(datamodel.JobsStateERROR)
		job.ErrorDetails = fmt.Sprintf("%d pool reconciliations failed", failed)
	}
	if err := workflow.ExecuteActivity(ctx, commonActivity.UpdateJobStatus, job).Get(ctx, nil); err != nil {
		return fmt.Errorf("complete auth reconciliation job %s: %w", params.JobID, err)
	}
	recordedOutcome = true
	if timedOut {
		return temporal.NewApplicationError("auth-type reconciliation parent timed out", "Timeout")
	}
	return nil
}

type authReconciliationChildExecution struct {
	resultIndex int
	future      workflow.ChildWorkflowFuture
}

func recordAuthReconciliationChildResult(
	results []models.AuthTypeReconciliationPoolResult,
	resultIndex int,
	childErr error,
) {
	if childErr != nil {
		results[resultIndex].Status = "ERROR"
		results[resultIndex].Reason = childErr.Error()
		return
	}
	results[resultIndex].Status = "DONE"
	results[resultIndex].Reason = ""
}

func persistAuthTypeReconciliationResults(
	ctx workflow.Context,
	commonActivity *activities.CommonActivities,
	params ReconcilePoolAuthTypesAPIWorkflowParams,
) error {
	attributes := &datamodel.JobAttributes{PayloadAttributes: params.JobPayload.Attributes()}
	if err := workflow.ExecuteActivity(
		ctx,
		commonActivity.UpdateJobAttributes,
		params.JobID,
		attributes,
	).Get(ctx, nil); err != nil {
		return fmt.Errorf("persist auth reconciliation job %s results: %w", params.JobID, err)
	}
	return nil
}

func recordAuthReconciliationJobFailure(
	ctx workflow.Context,
	logger middlewarelog.Logger,
	commonActivity *activities.CommonActivities,
	jobID string,
	failure error,
) {
	if jobID == "" || failure == nil {
		return
	}
	disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
	job := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{UUID: jobID},
		State:        string(datamodel.JobsStateERROR),
		ErrorDetails: failure.Error(),
	}
	if updateErr := workflow.ExecuteActivity(
		disconnectedCtx,
		commonActivity.UpdateJobStatus,
		job,
	).Get(disconnectedCtx, nil); updateErr != nil {
		logger.Error("Failed to record auth reconciliation error",
			"jobID", jobID,
			"error", updateErr,
		)
	}
}
