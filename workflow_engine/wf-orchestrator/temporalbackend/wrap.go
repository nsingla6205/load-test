package temporalbackend

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/workflow"
)

// WrapWorkflow1 wraps a workflow function that takes a single request parameter
// and returns (response, error) so it accepts workflow.Context for Temporal registration.
func WrapWorkflow1[Req any, Resp any](fn func(*orchestrator.Context, Req) (Resp, error)) func(workflow.Context, Req) (Resp, error) {
	return func(ctx workflow.Context, req Req) (Resp, error) {
		orchCtx := NewTemporalContext(ctx)
		return fn(orchCtx, req)
	}
}

// WrapWorkflow0 wraps a workflow function that takes no extra parameters
// and returns (response, error) so it accepts workflow.Context for Temporal registration.
func WrapWorkflow0[Resp any](fn func(*orchestrator.Context) (Resp, error)) func(workflow.Context) (Resp, error) {
	return func(ctx workflow.Context) (Resp, error) {
		orchCtx := NewTemporalContext(ctx)
		return fn(orchCtx)
	}
}

// WrapWorkflow1Err wraps a workflow function that takes a single request parameter
// and returns only error so it accepts workflow.Context for Temporal registration.
func WrapWorkflow1Err[Req any](fn func(*orchestrator.Context, Req) error) func(workflow.Context, Req) error {
	return func(ctx workflow.Context, req Req) error {
		orchCtx := NewTemporalContext(ctx)
		return fn(orchCtx, req)
	}
}

// WrapWorkflow0Err wraps a workflow function that takes no extra parameters
// and returns only error so it accepts workflow.Context for Temporal registration.
func WrapWorkflow0Err(fn func(*orchestrator.Context) error) func(workflow.Context) error {
	return func(ctx workflow.Context) error {
		orchCtx := NewTemporalContext(ctx)
		return fn(orchCtx)
	}
}

// WrapWorkflow2 wraps a workflow function that takes two request parameters
// and returns (response, error).
func WrapWorkflow2[Req1 any, Req2 any, Resp any](fn func(*orchestrator.Context, Req1, Req2) (Resp, error)) func(workflow.Context, Req1, Req2) (Resp, error) {
	return func(ctx workflow.Context, r1 Req1, r2 Req2) (Resp, error) {
		orchCtx := NewTemporalContext(ctx)
		return fn(orchCtx, r1, r2)
	}
}

// WrapWorkflow2Err wraps a workflow function that takes two request parameters
// and returns only error.
func WrapWorkflow2Err[Req1 any, Req2 any](fn func(*orchestrator.Context, Req1, Req2) error) func(workflow.Context, Req1, Req2) error {
	return func(ctx workflow.Context, r1 Req1, r2 Req2) error {
		orchCtx := NewTemporalContext(ctx)
		return fn(orchCtx, r1, r2)
	}
}
