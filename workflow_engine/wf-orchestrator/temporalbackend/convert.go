package temporalbackend

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func toTemporalRetryPolicy(rp *orchestrator.RetryPolicy) *temporal.RetryPolicy {
	if rp == nil {
		return nil
	}
	return &temporal.RetryPolicy{
		InitialInterval:    rp.InitialInterval,
		BackoffCoefficient: rp.BackoffCoefficient,
		MaximumInterval:    rp.MaximumInterval,
		MaximumAttempts:    rp.MaximumAttempts,
	}
}

func toTemporalActivityOptions(opts orchestrator.ActivityOptions) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		ScheduleToCloseTimeout: opts.ScheduleToCloseTimeout,
		ActivityID:             opts.ActivityID,
		HeartbeatTimeout:       opts.HeartbeatTimeout,
		RetryPolicy:            toTemporalRetryPolicy(opts.RetryPolicy),
	}
}

func toTemporalChildOptions(opts orchestrator.ChildWorkflowOptions) workflow.ChildWorkflowOptions {
	return workflow.ChildWorkflowOptions{
		WorkflowExecutionTimeout: opts.WorkflowExecutionTimeout,
		WorkflowID:               opts.WorkflowID,
		ParentClosePolicy:        enums.PARENT_CLOSE_POLICY_TERMINATE,
		RetryPolicy:              toTemporalRetryPolicy(opts.RetryPolicy),
	}
}
