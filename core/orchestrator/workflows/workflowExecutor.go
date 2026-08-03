package workflows

import (
	"context"
	"fmt"
	"strings"
	"time"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

var (
	// TemporalWorkflowMaxRetries and TemporalWorkflowRetryDelay are the defaults for
	// ExecuteWorkflowWithRetry. They are vars (not consts) so unit tests can override
	// them — same pattern as cvp.ApiCvpRetryDelay.
	TemporalWorkflowMaxRetries = 3
	TemporalWorkflowRetryDelay = 2 * time.Second
)

// WorkflowExecutor provides centralized workflow execution for VSA Control Plane operations
type WorkflowExecutor struct {
	temporal client.Client
	logger   log.Logger
}

// NewWorkflowExecutor creates a new workflow executor instance
func NewWorkflowExecutor(temporal client.Client, logger log.Logger) *WorkflowExecutor {
	return &WorkflowExecutor{
		temporal: temporal,
		logger:   logger,
	}
}

// SequentialWorkflowOptions contains configuration for sequential workflow execution
type SequentialWorkflowOptions struct {
	ControlWorkflowID  string
	ChildWorkflowID    string
	TaskQueue          string
	EnableRetry        bool
	MaxRetries         int
	RetryDelay         time.Duration
	WorkflowRunTimeout *time.Duration // Optional: if nil, uses global timeout
}

// DefaultSequentialWorkflowOptions returns default options for sequential workflow execution
func DefaultSequentialWorkflowOptions(controlWorkflowID, childWorkflowID string) *SequentialWorkflowOptions {
	return &SequentialWorkflowOptions{
		ControlWorkflowID: controlWorkflowID,
		ChildWorkflowID:   childWorkflowID,
		TaskQueue:         workflowengine.CustomerTaskQueue,
		EnableRetry:       true,
		MaxRetries:        TemporalWorkflowMaxRetries,
		RetryDelay:        TemporalWorkflowRetryDelay,
	}
}

// ExecuteSequentialWorkflow executes a workflow using the sequential pattern with retry logic
func (we *WorkflowExecutor) ExecuteSequentialWorkflow(
	ctx context.Context,
	options *SequentialWorkflowOptions,
	workflowFunc interface{},
	args ...interface{},
) error {
	if options.EnableRetry {
		return we.executeWithRetry(ctx, options, workflowFunc, args...)
	}

	return we.executeSingle(ctx, options, workflowFunc, args...)
}

// ExecuteWorkflow executes a workflow using the standard pattern (non-sequential)
func (we *WorkflowExecutor) ExecuteWorkflow(
	ctx context.Context,
	workflowID string,
	taskQueue string,
	workflowFunc interface{},
	workflowRunTimeout *time.Duration,
	args ...interface{},
) error {
	return we.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, workflowRunTimeout, nil, args...)
}

// ExecuteWorkflowWithRetry executes standard workflow with retry logic for transient failures
// workflowIDReusePolicy is optional - if nil, defaults to WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
func (we *WorkflowExecutor) ExecuteWorkflowWithRetry(
	ctx context.Context,
	workflowID string,
	taskQueue string,
	workflowFunc interface{},
	workflowRunTimeout *time.Duration,
	workflowIDReusePolicy *enums.WorkflowIdReusePolicy,
	args ...interface{},
) error {
	var lastErr error

	for attempt := 1; attempt <= TemporalWorkflowMaxRetries; attempt++ {
		err := we.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, workflowRunTimeout, workflowIDReusePolicy, args...)

		if err == nil {
			if attempt > 1 {
				we.logger.Info("Standard workflow execution succeeded after retry",
					"attempt", attempt,
					"workflowID", workflowID,
					"taskQueue", taskQueue)
			}
			return nil
		}

		lastErr = err

		// Check if this is a retryable error
		if !we.isRetryableError(err) {
			we.logger.Error("Non-retryable standard workflow execution error",
				"attempt", attempt,
				"workflowID", workflowID,
				"taskQueue", taskQueue,
				"error", err)
			return err
		}

		if attempt < TemporalWorkflowMaxRetries {
			we.logger.Warn("Standard workflow execution failed, retrying",
				"attempt", attempt,
				"maxRetries", TemporalWorkflowMaxRetries,
				"retryAfter", TemporalWorkflowRetryDelay,
				"workflowID", workflowID,
				"taskQueue", taskQueue,
				"error", err)

			// Context-aware sleep that respects cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(TemporalWorkflowRetryDelay):
				// Continue to next attempt
			}
		}
	}

	we.logger.Error("Standard workflow execution failed after all retry attempts",
		"attempts", TemporalWorkflowMaxRetries,
		"workflowID", workflowID,
		"taskQueue", taskQueue,
		"finalError", lastErr)

	return fmt.Errorf("standard workflow execution failed after %d attempts: %w",
		TemporalWorkflowMaxRetries, lastErr)
}

// ExecuteWorkflowSingle performs a single standard workflow execution attempt
// workflowIDReusePolicy is optional - if nil, defaults to WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
func (we *WorkflowExecutor) ExecuteWorkflowSingle(
	ctx context.Context,
	workflowID string,
	taskQueue string,
	workflowFunc interface{},
	workflowRunTimeout *time.Duration,
	workflowIDReusePolicy *enums.WorkflowIdReusePolicy,
	args ...interface{},
) error {
	timeout := workflowengine.GetWorkflowGlobalTimeout()
	if workflowRunTimeout != nil {
		timeout = *workflowRunTimeout
	}

	// Default to REJECT_DUPLICATE for backwards compatibility
	reusePolicy := enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	if workflowIDReusePolicy != nil {
		reusePolicy = *workflowIDReusePolicy
	}

	options := client.StartWorkflowOptions{
		TaskQueue:             taskQueue,
		ID:                    workflowID,
		WorkflowIDReusePolicy: reusePolicy,
		WorkflowRunTimeout:    timeout,
	}

	_, err := we.temporal.ExecuteWorkflow(ctx, options, workflowFunc, args...)
	if err != nil {
		we.logger.Error("Failed to execute standard workflow",
			"workflowID", workflowID,
			"taskQueue", taskQueue,
			"error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

// executeWithRetry executes workflow with retry logic for transient failures
func (we *WorkflowExecutor) executeWithRetry(
	ctx context.Context,
	options *SequentialWorkflowOptions,
	workflowFunc interface{},
	args ...interface{},
) error {
	var lastErr error

	for attempt := 1; attempt <= options.MaxRetries; attempt++ {
		err := we.executeSingle(ctx, options, workflowFunc, args...)

		if err == nil {
			if attempt > 1 {
				we.logger.Info("Workflow execution succeeded after retry",
					"attempt", attempt,
					"controlWorkflowID", options.ControlWorkflowID,
					"childWorkflowID", options.ChildWorkflowID)
			}
			return nil
		}

		lastErr = err

		// Check if this is a retryable error
		if !we.isRetryableError(err) {
			we.logger.Error("Non-retryable workflow execution error",
				"attempt", attempt,
				"controlWorkflowID", options.ControlWorkflowID,
				"childWorkflowID", options.ChildWorkflowID,
				"error", err)
			return err
		}

		if attempt < options.MaxRetries {
			we.logger.Warn("Workflow execution failed, retrying",
				"attempt", attempt,
				"maxRetries", options.MaxRetries,
				"retryAfter", options.RetryDelay,
				"controlWorkflowID", options.ControlWorkflowID,
				"childWorkflowID", options.ChildWorkflowID,
				"error", err)

			// Use context-aware sleep that respects cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(options.RetryDelay):
				// Continue to next attempt
			}
		}
	}

	we.logger.Error("Workflow execution failed after all retry attempts",
		"attempts", options.MaxRetries,
		"controlWorkflowID", options.ControlWorkflowID,
		"childWorkflowID", options.ChildWorkflowID,
		"finalError", lastErr)

	return fmt.Errorf("workflow execution failed after %d attempts: %w", options.MaxRetries, lastErr)
}

// executeSingle performs a single workflow execution attempt
func (we *WorkflowExecutor) executeSingle(
	ctx context.Context,
	options *SequentialWorkflowOptions,
	workflowFunc interface{},
	args ...interface{},
) error {
	timeout := workflowengine.GetWorkflowGlobalTimeout()
	if options.WorkflowRunTimeout != nil {
		timeout = *options.WorkflowRunTimeout
	}
	return ExecuteWorkflowSequentially(
		we.temporal,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: options.TaskQueue,
			ID:        options.ControlWorkflowID,
		},
		workflowFunc,
		workflow.ChildWorkflowOptions{
			TaskQueue:             options.TaskQueue,
			WorkflowID:            options.ChildWorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    timeout,
		},
		args...,
	)
}

// isRetryableError determines if a Temporal workflow error is retryable
// following VSA Control Plane error categorization patterns
func (we *WorkflowExecutor) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for context deadline exceeded (retryable)
	if vsaerrors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Check for Temporal-specific retryable errors
	var (
		workflowAlreadyStartedError *serviceerror.WorkflowExecutionAlreadyStarted
		resourceExhaustedError      *serviceerror.ResourceExhausted
		unavailableError            *serviceerror.Unavailable
		deadlineExceededError       *serviceerror.DeadlineExceeded
	)

	switch {
	case vsaerrors.As(err, &workflowAlreadyStartedError):
		// Workflow already started - not retryable for creation workflows
		return false
	case vsaerrors.As(err, &resourceExhaustedError):
		// Temporal service overloaded - retryable
		return true
	case vsaerrors.As(err, &unavailableError):
		// Temporal service unavailable - retryable
		return true
	case vsaerrors.As(err, &deadlineExceededError):
		// Timeout - retryable
		return true
	default:
		// Check for network/connection errors
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "connection refused") ||
			strings.Contains(errMsg, "connection reset") ||
			strings.Contains(errMsg, "timeout") ||
			strings.Contains(errMsg, "unavailable") ||
			strings.Contains(errMsg, "deadline exceeded") {
			return true
		}
		return false
	}
}

// GenerateControlWorkflowID creates a standardized control workflow ID
// following VSA Control Plane naming conventions
func GenerateControlWorkflowID(accountID int64, location, poolName string) string {
	return fmt.Sprintf(VolumeCreateDeleteSnapshotDeleteSeq, accountID, location, poolName)
}
