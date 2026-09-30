package gowfbackend

import (
	"context"
	"errors"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/workflow"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// WorkflowStarter launches go-workflows instances from outside the worker.
type WorkflowStarter struct {
	client *cclient.Client
}

// NewWorkflowStarter creates a WorkflowStarter for the given client.
func NewWorkflowStarter(client *cclient.Client) *WorkflowStarter {
	return &WorkflowStarter{client: client}
}

// WorkflowStarter returns a WorkflowStarter for this runner.
func (r *Runner) WorkflowStarter() *WorkflowStarter {
	return NewWorkflowStarter(r.client)
}

// Start creates a new workflow instance from a registered workflow function.
func (s *WorkflowStarter) Start(ctx context.Context, instanceID string, workflowFn interface{}, args ...any) (*workflow.Instance, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("gowfbackend: WorkflowStarter not configured")
	}
	workflowName := FuncName(workflowFn)
	if workflowName == "" {
		return nil, errors.New("gowfbackend: workflowFn must be a registered workflow function")
	}
	// CreateWorkflowInstance runs the registered context propagators against
	// this context, and they read the correlation id from a different key than
	// the HTTP middleware writes it to. Bridge it here so every dispatch site
	// gets correlation propagation without having to remember to do it.
	ctx = orchestrator.WithRequestContext(ctx)
	return s.client.CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: instanceID,
	}, workflowName, args...)
}
