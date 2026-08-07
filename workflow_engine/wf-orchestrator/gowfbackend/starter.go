package gowfbackend

import (
	"context"
	"errors"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/cschleiden/go-workflows/workflow"
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
	return s.client.CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: instanceID,
	}, workflowName, args...)
}
