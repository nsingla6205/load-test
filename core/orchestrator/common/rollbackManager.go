package common

import (
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/workflow"
)

var executeActivity = workflow.ExecuteActivity
var executeChildWorkflow = workflow.ExecuteChildWorkflow

type actionType int

const (
	rollbackActivityType actionType = iota
	rollbackWorkflowType
)

type rollbackAction struct {
	fn         interface{}
	args       []interface{}
	actionType actionType
	taskQueue  string
}

// RollbackManager manages rollback activities and workflows.
type RollbackManager struct {
	rollbacks []rollbackAction
}

// NewRollbackManager creates a new RollbackManager.
func NewRollbackManager() *RollbackManager {
	return &RollbackManager{
		rollbacks: []rollbackAction{},
	}
}

// ClearAll removes all registered rollbacks.
func (rm *RollbackManager) ClearAll() {
	rm.rollbacks = rm.rollbacks[:0]
}

// AddActivity adds a rollback activity function and its arguments to the manager.
func (rm *RollbackManager) AddActivity(activity interface{}, args ...interface{}) {
	rm.rollbacks = append(rm.rollbacks, rollbackAction{
		fn:         activity,
		args:       args,
		actionType: rollbackActivityType,
	})
}

// AddWorkflowWithTaskQueue adds a rollback workflow to a specific task queue.
func (rm *RollbackManager) AddWorkflow(taskQueue string, workflowFn interface{}, args ...interface{}) {
	rm.addWorkflowWithTaskQueue(taskQueue, workflowFn, args...)
}

// addWorkflowWithTaskQueue is an internal helper to append the rollback workflow.
func (rm *RollbackManager) addWorkflowWithTaskQueue(taskQueue string, workflowFn interface{}, args ...interface{}) {
	rm.rollbacks = append(rm.rollbacks, rollbackAction{
		fn:         workflowFn,
		taskQueue:  taskQueue,
		args:       args,
		actionType: rollbackWorkflowType,
	})
}

// ExecuteRollback executes all rollback activities and workflows in LIFO order.
// It logs errors if any rollback step fails.
func (rm *RollbackManager) ExecuteRollback(ctx workflow.Context, err error) {
	logger := util.GetLogger(ctx)

	for i := len(rm.rollbacks) - 1; i >= 0; i-- {
		r := rm.rollbacks[i]

		errorMessage := vsaerrors.ExtractCustomerFacingErrorMessage(ctx, err)
		r.args = append(r.args, errorMessage)

		switch r.actionType {
		case rollbackActivityType:
			fut := executeActivity(ctx, r.fn, r.args...)
			if errComp := fut.Get(ctx, nil); errComp != nil {
				err = errComp
				logger.Errorf("Error executing rollback fn, err: %v", err)
			}
		case rollbackWorkflowType:
			wfTimeout := temporal.GetWorkflowGlobalTimeout()
			// If the workflow function is a VLM workflow, fetch the specific timeout and correlation ID
			if fnName, ok := r.fn.(string); ok && strings.HasPrefix(fnName, "vlm.") {
				var correlationID string
				wfTimeout, correlationID, err = fetchVLMTimeoutAndCorrelationID(ctx, fnName)
				if err != nil {
					logger.Warnf("Error fetching VLM timeouts for rollback, err: %v", err)
				}
				ctx = workflow.WithValue(ctx, vlm.CorrelationIDKey, correlationID)
			}
			options := workflow.ChildWorkflowOptions{
				TaskQueue:                r.taskQueue,
				WorkflowExecutionTimeout: wfTimeout,
			}
			ctxChild := workflow.WithChildOptions(ctx, options)
			fut := executeChildWorkflow(ctxChild, r.fn, r.args...)
			if errComp := fut.Get(ctxChild, nil); errComp != nil {
				err = errComp
				logger.Errorf("Error executing rollback workflow, err: %v", err)
			}
		}
	}
}

func fetchVLMTimeoutAndCorrelationID(ctx workflow.Context, fnName string) (time.Duration, string, error) {
	wfTimeout := temporal.GetWorkflowGlobalTimeout()
	if timeout, ok := vlm.WorkflowExecutionTimeoutMap[fnName]; ok {
		wfTimeout = timeout
		correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
		if err != nil {
			return 0, "", err
		}

		return wfTimeout, correlationID, nil
	}

	return wfTimeout, "", nil
}
