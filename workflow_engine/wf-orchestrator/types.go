package orchestrator

import "time"

// WorkflowExecution identifies a specific workflow execution.
type WorkflowExecution struct {
	ID    string
	RunID string
}

// WorkflowInfo holds metadata about the current workflow execution,
// mirroring the subset of workflow.Info that the codebase actually uses.
type WorkflowInfo struct {
	WorkflowExecutionTimeout time.Duration
	WorkflowStartTime        time.Time
	WorkflowExecution        WorkflowExecution
	// Attempt is the current workflow execution attempt, starting at 1.
	Attempt int32
}

// RetryPolicy is an engine-agnostic retry configuration that maps 1-to-1
// with temporal.RetryPolicy fields used in this codebase.
type RetryPolicy struct {
	InitialInterval    time.Duration
	BackoffCoefficient float64
	MaximumInterval    time.Duration
	MaximumAttempts    int32
}

// ActivityOptions mirrors workflow.ActivityOptions for engine-agnostic usage.
type ActivityOptions struct {
	ScheduleToCloseTimeout time.Duration
	ActivityID             string
	HeartbeatTimeout       time.Duration
	RetryPolicy            *RetryPolicy
}

// ChildWorkflowOptions mirrors workflow.ChildWorkflowOptions for engine-agnostic usage.
type ChildWorkflowOptions struct {
	WorkflowExecutionTimeout time.Duration
	WorkflowID               string
	RetryPolicy              *RetryPolicy
}
