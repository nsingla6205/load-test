package backgroundactivities

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

// ExecuteWorkflowSequentiallyFunc is a function type for executing workflows sequentially
type ExecuteWorkflowSequentiallyFunc func(
	temporal client.Client,
	ctx context.Context,
	sequenceWfOptions client.StartWorkflowOptions,
	wfFunction interface{},
	wfOptions workflow.ChildWorkflowOptions,
	wfArgs ...interface{},
) error

// ExecuteWorkflowSequentially is a variable that holds the function to execute workflows sequentially.
// This is set by RegisterWorkflowExecutor to avoid circular dependencies.
// It can also be mocked in tests.
var ExecuteWorkflowSequentially ExecuteWorkflowSequentiallyFunc

// RegisterWorkflowExecutor sets the function for executing workflows sequentially.
// This is called by the workflows package to register its implementation.
func RegisterWorkflowExecutor(fn ExecuteWorkflowSequentiallyFunc) {
	ExecuteWorkflowSequentially = fn
}

// ControlWorkflowActivity provides activities for executing workflows through control workflows
type ControlWorkflowActivity struct{}

// Pool operation labels used in structured logs. They must stay in sync with the
// labels emitted by the per-pool workflows so a single query returns the queue
// activity and the workflow it started.
const (
	poolOperationCertificateRotation = "certificate_rotation"
	poolOperationCAReconciliation    = "ca_reconciliation"
	poolOperationPasswordRotation    = "password_rotation"
)

func poolOperationQueueLogger(
	ctx context.Context,
	correlationID string,
	poolUUID string,
	operation string,
) log.Logger {
	fields := log.Fields{}
	if existing, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		for key, value := range existing {
			fields[key] = value
		}
	}
	operationFields := log.Fields{
		string(middleware.RequestCorrelationID): correlationID,
		"poolUUID":                              poolUUID,
		"poolOperation":                         operation,
		"workflowID":                            correlationID,
	}
	for key, value := range operationFields {
		fields[key] = value
	}
	return log.NewLogger().With(fields)
}

// ExecutePoolCertificateRotationSequentially executes certificate rotation for a pool through the control workflow.
// This ensures only one workflow runs at a time on the pool, preventing race conditions.
func (a *ControlWorkflowActivity) ExecutePoolCertificateRotationSequentially(ctx context.Context, poolUUID string, workflowTimeout time.Duration) error {
	childWorkflowID := fmt.Sprintf("PoolCertRotation_%s_%d", poolUUID, time.Now().UnixNano())
	logger := poolOperationQueueLogger(ctx, childWorkflowID, poolUUID, poolOperationCertificateRotation)
	logger.Info("Queueing pool certificate rotation")

	// Get temporal client
	temporalClient, err := workflowengine.FetchTemporalClient()
	if err != nil {
		logger.Errorf("Failed to get Temporal client: %v", err)
		return err
	}

	// Generate control workflow ID based on pool UUID
	// All pool operations for this pool will use the same control workflow ID
	controlWorkflowID := fmt.Sprintf(common.PoolOperationsSeq, poolUUID)

	logger.Infof("Using control workflow ID: %s for pool: %s", controlWorkflowID, poolUUID)
	logger.Infof("Certificate rotation workflow ID: %s", childWorkflowID)

	// Execute the certificate rotation workflow through the control workflow
	// This ensures sequential execution with other pool operations
	err = ExecuteWorkflowSequentially(
		temporalClient,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		common.RotatePoolCertificateWorkflowName,
		workflow.ChildWorkflowOptions{
			TaskQueue:          workflowengine.CustomerTaskQueue,
			WorkflowID:         childWorkflowID,
			WorkflowRunTimeout: workflowTimeout,
		},
		poolUUID,
	)

	if err != nil {
		logger.Errorf("Failed to execute certificate rotation sequentially for pool %s: %v", poolUUID, err)
		return err
	}

	logger.Infof("Successfully queued certificate rotation for pool: %s", poolUUID)
	return nil
}

// ExecutePoolForcedCertificateRotationSequentially queues an on-demand certificate rotation
// for a single pool through the per-pool control workflow, bypassing the remaining-lifetime
// eligibility check. It serializes against every other pool operation exactly like the
// scheduled path does.
func (a *ControlWorkflowActivity) ExecutePoolForcedCertificateRotationSequentially(
	ctx context.Context,
	poolUUID string,
	workflowTimeout time.Duration,
) error {
	return a.ExecuteTrackedPoolForcedCertificateRotationSequentially(
		ctx,
		poolUUID,
		fmt.Sprintf("%d", time.Now().UnixNano()),
		"",
		workflowTimeout,
	)
}

// ExecuteTrackedPoolForcedCertificateRotationSequentially queues a tracked rotation.
func (a *ControlWorkflowActivity) ExecuteTrackedPoolForcedCertificateRotationSequentially(
	ctx context.Context,
	poolUUID, transitionWorkflowID, childJobID string,
	workflowTimeout time.Duration,
) error {
	activity.RecordHeartbeat(ctx, "Queueing forced certificate rotation")
	err := executeTrackedPoolForcedCertificateRotationSequentially(
		ctx,
		poolUUID,
		transitionWorkflowID,
		childJobID,
		workflowTimeout,
		workflowengine.FetchTemporalClient,
		ExecuteWorkflowSequentially,
	)
	if err == nil {
		activity.RecordHeartbeat(ctx, "Forced certificate rotation queued")
	}
	return err
}

func executePoolForcedCertificateRotationSequentially(
	ctx context.Context,
	poolUUID string,
	workflowTimeout time.Duration,
	fetchTemporalClient func() (client.Client, error),
	executeSequentially ExecuteWorkflowSequentiallyFunc,
) error {
	return executeTrackedPoolForcedCertificateRotationSequentially(
		ctx,
		poolUUID,
		fmt.Sprintf("%d", time.Now().UnixNano()),
		"",
		workflowTimeout,
		fetchTemporalClient,
		executeSequentially,
	)
}

func executeTrackedPoolForcedCertificateRotationSequentially(
	ctx context.Context,
	poolUUID string,
	transitionWorkflowID string,
	childJobID string,
	workflowTimeout time.Duration,
	fetchTemporalClient func() (client.Client, error),
	executeSequentially ExecuteWorkflowSequentiallyFunc,
) error {
	childWorkflowID := fmt.Sprintf("PoolForcedCertRotation_%s_%s", poolUUID, transitionWorkflowID)
	logger := poolOperationQueueLogger(ctx, childWorkflowID, poolUUID, poolOperationCertificateRotation)
	logger.Info("Queueing forced pool certificate rotation")
	temporalClient, err := fetchTemporalClient()
	if err != nil {
		return fmt.Errorf("get Temporal client: %w", err)
	}

	controlWorkflowID := fmt.Sprintf(common.PoolOperationsSeq, poolUUID)
	workflowName := common.ForceRotatePoolCertificateWorkflowName
	workflowArgs := []interface{}{poolUUID}
	if childJobID != "" {
		workflowName = common.TrackedForceRotatePoolCertificateWorkflowName
		workflowArgs = append(workflowArgs, childJobID)
	}
	if err := executeSequentially(
		temporalClient,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		workflowName,
		workflow.ChildWorkflowOptions{
			TaskQueue:          workflowengine.CustomerTaskQueue,
			WorkflowID:         childWorkflowID,
			WorkflowRunTimeout: workflowTimeout,
		},
		workflowArgs...,
	); err != nil {
		logger.Errorf("Failed to execute forced certificate rotation sequentially for pool %s: %v", poolUUID, err)
		return err
	}
	logger.Infof("Successfully queued forced certificate rotation for pool: %s", poolUUID)
	return nil
}

// ExecutePoolCAReconciliationSequentially queues explicit desired-CA reconciliation
// through the shared per-pool control workflow.
func (a *ControlWorkflowActivity) ExecutePoolCAReconciliationSequentially(
	ctx context.Context,
	poolUUID string,
	desiredCAURI string,
	transitionWorkflowID string,
	workflowTimeout time.Duration,
) error {
	return a.ExecuteTrackedPoolCAReconciliationSequentially(
		ctx,
		poolUUID,
		desiredCAURI,
		transitionWorkflowID,
		"",
		workflowTimeout,
	)
}

// ExecuteTrackedPoolCAReconciliationSequentially queues tracked CA reconciliation.
func (a *ControlWorkflowActivity) ExecuteTrackedPoolCAReconciliationSequentially(
	ctx context.Context,
	poolUUID string,
	desiredCAURI string,
	transitionWorkflowID string,
	childJobID string,
	workflowTimeout time.Duration,
) error {
	activity.RecordHeartbeat(ctx, "Queueing explicit desired-CA reconciliation")
	err := executeTrackedPoolCAReconciliationSequentially(
		ctx,
		poolUUID,
		desiredCAURI,
		transitionWorkflowID,
		childJobID,
		workflowTimeout,
		workflowengine.FetchTemporalClient,
		ExecuteWorkflowSequentially,
	)
	if err == nil {
		activity.RecordHeartbeat(ctx, "Explicit desired-CA reconciliation queued")
	}
	return err
}

func executePoolCAReconciliationSequentially(
	ctx context.Context,
	poolUUID string,
	desiredCAURI string,
	transitionWorkflowID string,
	workflowTimeout time.Duration,
	fetchTemporalClient func() (client.Client, error),
	executeSequentially ExecuteWorkflowSequentiallyFunc,
) error {
	return executeTrackedPoolCAReconciliationSequentially(
		ctx,
		poolUUID,
		desiredCAURI,
		transitionWorkflowID,
		"",
		workflowTimeout,
		fetchTemporalClient,
		executeSequentially,
	)
}

func executeTrackedPoolCAReconciliationSequentially(
	ctx context.Context,
	poolUUID string,
	desiredCAURI string,
	transitionWorkflowID string,
	childJobID string,
	workflowTimeout time.Duration,
	fetchTemporalClient func() (client.Client, error),
	executeSequentially ExecuteWorkflowSequentiallyFunc,
) error {
	childWorkflowID := fmt.Sprintf("PoolCAReconciliation_%s_%s", poolUUID, transitionWorkflowID)
	logger := poolOperationQueueLogger(ctx, childWorkflowID, poolUUID, poolOperationCAReconciliation)
	logger.Info("Queueing pool CA reconciliation", "desiredCAURI", desiredCAURI)
	temporalClient, err := fetchTemporalClient()
	if err != nil {
		return fmt.Errorf("get Temporal client: %w", err)
	}

	controlWorkflowID := fmt.Sprintf(common.PoolOperationsSeq, poolUUID)
	workflowName := common.ReconcilePoolCAWorkflowName
	workflowArgs := []interface{}{poolUUID, desiredCAURI}
	if childJobID != "" {
		workflowName = common.TrackedReconcilePoolCAWorkflowName
		workflowArgs = append(workflowArgs, childJobID)
	}
	if err := executeSequentially(
		temporalClient,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		workflowName,
		workflow.ChildWorkflowOptions{
			TaskQueue:          workflowengine.CustomerTaskQueue,
			WorkflowID:         childWorkflowID,
			WorkflowRunTimeout: workflowTimeout,
		},
		workflowArgs...,
	); err != nil {
		logger.Errorf("Failed to execute CA reconciliation sequentially for pool %s: %v", poolUUID, err)
		return err
	}
	logger.Infof("Successfully queued CA reconciliation for pool: %s", poolUUID)
	return nil
}

// ExecutePoolPasswordRotationSequentially executes password rotation for a pool through the control workflow.
// This ensures only one workflow runs at a time on the pool, preventing race conditions.
func (a *ControlWorkflowActivity) ExecutePoolPasswordRotationSequentially(ctx context.Context, poolUUID string, workflowTimeout time.Duration) error {
	childWorkflowID := fmt.Sprintf("PoolPasswordRotation_%s_%d", poolUUID, time.Now().UnixNano())
	logger := poolOperationQueueLogger(ctx, childWorkflowID, poolUUID, poolOperationPasswordRotation)
	logger.Info("Queueing pool password rotation")

	// Get temporal client
	temporalClient, err := workflowengine.FetchTemporalClient()
	if err != nil {
		logger.Errorf("Failed to get Temporal client: %v", err)
		return err
	}

	// Generate control workflow ID based on pool UUID
	controlWorkflowID := fmt.Sprintf(common.PoolOperationsSeq, poolUUID)

	logger.Infof("Using control workflow ID: %s for pool: %s", controlWorkflowID, poolUUID)
	logger.Infof("Password rotation workflow ID: %s", childWorkflowID)

	// Execute the password rotation workflow through the control workflow
	err = ExecuteWorkflowSequentially(
		temporalClient,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		common.RotatePoolPasswordWorkflowName,
		workflow.ChildWorkflowOptions{
			TaskQueue:          workflowengine.CustomerTaskQueue,
			WorkflowID:         childWorkflowID,
			WorkflowRunTimeout: workflowTimeout,
		},
		poolUUID,
	)

	if err != nil {
		logger.Errorf("Failed to execute password rotation sequentially for pool %s: %v", poolUUID, err)
		return err
	}

	logger.Infof("Successfully queued password rotation for pool: %s", poolUUID)
	return nil
}

// ExecutePoolAuthTypeReconciliationForDesiredCASequentially queues explicit-CA
// auth reconciliation through the per-pool sequencer.
func (a *ControlWorkflowActivity) ExecutePoolAuthTypeReconciliationForDesiredCASequentially(
	ctx context.Context,
	poolUUID, desiredCAURI, transitionWorkflowID string,
	workflowTimeout time.Duration,
) error {
	activity.RecordHeartbeat(ctx, "Queueing explicit desired-CA auth reconciliation")
	err := executePoolAuthTypeReconciliationForDesiredCASequentially(
		ctx,
		poolUUID,
		desiredCAURI,
		transitionWorkflowID,
		workflowTimeout,
		workflowengine.FetchTemporalClient,
		ExecuteWorkflowSequentially,
	)
	if err == nil {
		activity.RecordHeartbeat(ctx, "Explicit desired-CA auth reconciliation queued")
	}
	return err
}

func executePoolAuthTypeReconciliationForDesiredCASequentially(
	ctx context.Context,
	poolUUID, desiredCAURI, transitionWorkflowID string,
	workflowTimeout time.Duration,
	fetchTemporalClient func() (client.Client, error),
	executeSequentially ExecuteWorkflowSequentiallyFunc,
) error {
	childWorkflowID := AuthTypeReconciliationOperationWorkflowID(poolUUID, transitionWorkflowID)
	logger := poolOperationQueueLogger(ctx, childWorkflowID, poolUUID, "auth_type_1_to_2_reconciliation")
	logger.Info("Queueing pool AuthType 1 to 2 reconciliation", "desiredCAURI", desiredCAURI)
	temporalClient, err := fetchTemporalClient()
	if err != nil {
		return fmt.Errorf("get Temporal client: %w", err)
	}
	return executeSequentially(
		temporalClient,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        fmt.Sprintf(common.PoolOperationsSeq, poolUUID),
		},
		common.ReconcilePoolAuthTypeForDesiredCAWorkflowName,
		workflow.ChildWorkflowOptions{
			TaskQueue:          workflowengine.CustomerTaskQueue,
			WorkflowID:         childWorkflowID,
			WorkflowRunTimeout: workflowTimeout,
		},
		poolUUID,
		desiredCAURI,
	)
}

// AuthTypeReconciliationOperationWorkflowID returns the deterministic workflow
// ID used for one API-selected pool reconciliation.
func AuthTypeReconciliationOperationWorkflowID(poolUUID, requestWorkflowID string) string {
	return fmt.Sprintf("PoolAuthTypeCAReconciliation_%s_%s", poolUUID, requestWorkflowID)
}

// WaitForAuthTypeReconciliationOperation waits for the sequenced per-pool
// workflow to finish.
func (a *ControlWorkflowActivity) WaitForAuthTypeReconciliationOperation(
	ctx context.Context,
	workflowID string,
) error {
	return waitForAuthTypeReconciliationOperation(
		ctx,
		workflowID,
		workflowengine.FetchTemporalClient,
		0,
	)
}

func waitForAuthTypeReconciliationOperation(
	ctx context.Context,
	workflowID string,
	fetchTemporalClient func() (client.Client, error),
	retryDelay time.Duration,
) error {
	temporalClient, err := fetchTemporalClient()
	if err != nil {
		return fmt.Errorf("get Temporal client: %w", err)
	}
	if retryDelay <= 0 {
		retryDelay = 2 * time.Second
	}
	for {
		err = temporalClient.GetWorkflow(ctx, workflowID, "").Get(ctx, nil)
		var notFound *serviceerror.NotFound
		if !errors.As(err, &notFound) {
			return err
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
