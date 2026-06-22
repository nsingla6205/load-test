package gowfbackend

import (
	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// WrapWorkflow0 wraps a workflow that takes no extra parameters and returns
// (response, error) so it accepts cschleidenwf.Context for direct
// registration with cschleiden's typed APIs (e.g. when callers prefer not
// to go through GowfRegistrar). Mirrors temporalbackend.WrapWorkflow0.
func WrapWorkflow0[Resp any](fn func(*orchestrator.Context) (Resp, error)) func(gowf.Context) (Resp, error) {
	return func(ctx gowf.Context) (Resp, error) {
		return fn(NewGoWorkflowsContext(ctx))
	}
}

// WrapWorkflow0Err wraps a workflow that takes no extra parameters and
// returns only error.
func WrapWorkflow0Err(fn func(*orchestrator.Context) error) func(gowf.Context) error {
	return func(ctx gowf.Context) error {
		return fn(NewGoWorkflowsContext(ctx))
	}
}

// WrapWorkflow1 wraps a workflow that takes a single request parameter and
// returns (response, error).
func WrapWorkflow1[Req any, Resp any](fn func(*orchestrator.Context, Req) (Resp, error)) func(gowf.Context, Req) (Resp, error) {
	return func(ctx gowf.Context, req Req) (Resp, error) {
		return fn(NewGoWorkflowsContext(ctx), req)
	}
}

// WrapWorkflow1Err wraps a workflow that takes a single request parameter and
// returns only error.
func WrapWorkflow1Err[Req any](fn func(*orchestrator.Context, Req) error) func(gowf.Context, Req) error {
	return func(ctx gowf.Context, req Req) error {
		return fn(NewGoWorkflowsContext(ctx), req)
	}
}

// WrapWorkflow2 wraps a workflow that takes two request parameters and
// returns (response, error).
func WrapWorkflow2[Req1 any, Req2 any, Resp any](fn func(*orchestrator.Context, Req1, Req2) (Resp, error)) func(gowf.Context, Req1, Req2) (Resp, error) {
	return func(ctx gowf.Context, r1 Req1, r2 Req2) (Resp, error) {
		return fn(NewGoWorkflowsContext(ctx), r1, r2)
	}
}

// WrapWorkflow2Err wraps a workflow that takes two request parameters and
// returns only error.
func WrapWorkflow2Err[Req1 any, Req2 any](fn func(*orchestrator.Context, Req1, Req2) error) func(gowf.Context, Req1, Req2) error {
	return func(ctx gowf.Context, r1 Req1, r2 Req2) error {
		return fn(NewGoWorkflowsContext(ctx), r1, r2)
	}
}
