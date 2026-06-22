package gowfbackend

import (
	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// toGowfRetryOptions maps orchestrator.RetryPolicy onto cschleiden's
// RetryOptions. Field-by-field:
//
//	InitialInterval     -> FirstRetryInterval
//	BackoffCoefficient  -> BackoffCoefficient
//	MaximumInterval     -> MaxRetryInterval
//	MaximumAttempts     -> MaxAttempts (int32 -> int)
//
// A nil retry policy yields cschleiden's DefaultRetryOptions so callers that
// did not set a policy still get sensible retry behaviour rather than the
// zero-value (which would mean no retries at all).
func toGowfRetryOptions(rp *orchestrator.RetryPolicy) gowf.RetryOptions {
	if rp == nil {
		return gowf.DefaultRetryOptions
	}
	return gowf.RetryOptions{
		MaxAttempts:        int(rp.MaximumAttempts),
		FirstRetryInterval: rp.InitialInterval,
		MaxRetryInterval:   rp.MaximumInterval,
		BackoffCoefficient: rp.BackoffCoefficient,
	}
}

// toGowfActivityOptions maps orchestrator.ActivityOptions onto cschleiden's
// ActivityOptions. cschleiden has no analog for ActivityID or HeartbeatTimeout,
// so those fields are dropped (documented in package-level comments). The
// orchestrator ScheduleToCloseTimeout maps to RetryTimeout, which is the
// closest cschleiden equivalent: an upper bound on the total time spent
// across retries.
func toGowfActivityOptions(opts orchestrator.ActivityOptions) gowf.ActivityOptions {
	retry := toGowfRetryOptions(opts.RetryPolicy)
	if opts.ScheduleToCloseTimeout > 0 {
		retry.RetryTimeout = opts.ScheduleToCloseTimeout
	}
	return gowf.ActivityOptions{
		RetryOptions: retry,
	}
}

// toGowfSubWorkflowOptions maps orchestrator.ChildWorkflowOptions onto
// cschleiden's SubWorkflowOptions. cschleiden has no
// WorkflowExecutionTimeout knob and always terminates children with the
// parent (semantically equivalent to Temporal's PARENT_CLOSE_POLICY_TERMINATE,
// which is what temporalbackend hard-codes).
func toGowfSubWorkflowOptions(opts orchestrator.ChildWorkflowOptions) gowf.SubWorkflowOptions {
	retry := toGowfRetryOptions(opts.RetryPolicy)
	if opts.WorkflowExecutionTimeout > 0 {
		retry.RetryTimeout = opts.WorkflowExecutionTimeout
	}
	return gowf.SubWorkflowOptions{
		InstanceID:   opts.WorkflowID,
		RetryOptions: retry,
	}
}
