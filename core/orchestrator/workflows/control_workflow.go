package workflows

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

var (
	SequenceWorkflowTimeout = env.GetString("SEQUENCE_WORKFLOW_TIMEOUT_MINUTES", "120m")

	// Feature flag: when true, the new ContinueAsNew-based sequencer is used.
	sequenceResilienceEnabled = env.GetBool("SEQUENCE_WORKFLOW_RESILIENCE_ENABLED", true)

	sequenceIdleTimeout = time.Duration(env.GetInt("SEQUENCE_IDLE_TIMEOUT_SECONDS", 3)) * time.Second
)

const (
	// VolumeCreateDeleteSnapshotDeleteSeq is a placeholder used for sequence workflow instance that runs all
	// volume CREATE & DELETE operation and snapshot DELETE calls for a specific pool sequentially.
	VolumeCreateDeleteSnapshotDeleteSeq = "Account_%d_Location_%s_Pool_%s_Ops_Volume-CD-Snapshot-D"

	// PoolDataSubnetCreateDelete is a placeholder used for sequence workflow instance that runs all
	// subnet create and delete operations for a specific account and VPC sequentially.
	PoolDataSubnetCreateDelete = common.PoolDataSubnetCreateDelete

	// PoolOperationsSeq is a placeholder used for sequence workflow instance that runs all
	// pool-level operations (certificate rotation, password rotation, updates, deletes, etc.) sequentially.
	// This ensures only one workflow operates on a pool at a time, preventing race conditions.
	PoolOperationsSeq = common.PoolOperationsSeq

	// Signal is the name of the signal used to call sequential workflows.
	Signal = common.Signal

	// Workflow name constants for use with control workflows
	// These are used to reference workflows by name when executing them sequentially
	RotatePoolCertificateWorkflowName = common.RotatePoolCertificateWorkflowName
	RotatePoolPasswordWorkflowName    = common.RotatePoolPasswordWorkflowName
)

// SignalWorkflowParams holds the parameters for the child workflow to be executed upon receiving a signal.
type SignalWorkflowParams struct {
	Function string
	Args     []interface{}
	Options  workflow.ChildWorkflowOptions
}

// SequenceWorkflowState is the serializable state passed across ContinueAsNew boundaries.
// It carries the pending signal queue so no signals are lost during rotation.
type SequenceWorkflowState struct {
	PendingQueue []SignalWorkflowParams `json:"pending_queue"`
	ChildrenRun  int                    `json:"children_run"`
	RunStartedAt time.Time              `json:"run_started_at"`
}

// SequenceWorkflowInput is the entry argument for the resilient SequenceWorkflow.
// On first invocation (via SignalWithStart) it will be zero-value; on ContinueAsNew
// it carries the state snapshot from the previous run.
type SequenceWorkflowInput struct {
	State *SequenceWorkflowState `json:"state,omitempty"`
}

// SequenceWorkflow is the legacy workflow that listens for signals and executes child workflows sequentially.
// It does NOT use ContinueAsNew. Used when SEQUENCE_WORKFLOW_RESILIENCE_ENABLED=false.
func SequenceWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	exitFlag := false
	signalChan := workflow.GetSignalChannel(ctx, Signal)

	// This timer is used to check if the workflow should exit, if sitting idle.
	timeout := workflow.NewTimer(ctx, sequenceIdleTimeout)

	for {
		selector := workflow.NewSelector(ctx)

		selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
			var signalWf SignalWorkflowParams
			c.Receive(ctx, &signalWf)

			ctx = workflow.WithChildOptions(ctx, signalWf.Options)
			if err := workflow.ExecuteChildWorkflow(ctx, signalWf.Function, signalWf.Args...).Get(ctx, nil); err != nil {
				logger.Error("Failed to execute child workflow", "error", err)
				return
			}
		})

		selector.AddFuture(timeout, func(f workflow.Future) {
			exitFlag = true
		})

		selector.Select(ctx)
		if exitFlag {
			// If the exit flag is set, we check if there are any pending signals.
			// The Selector selects any case randomly if multiple cases are ready.
			// Hence, we need to check if there are any pending signals before exiting to handle the random case.
			if selector.HasPending() {
				continue
			}
			// Exit the workflow.
			// This will ensure the workflow is not continuously waiting for signals.
			// This will also stop the workflow from exceeding the maximum event history size.
			logger.Info("Current value reached threshold, exiting workflow")
			break
		}
	}

	return nil
}

// SequenceWorkflowV2 is the resilient version with ContinueAsNew support.
// Used when SEQUENCE_WORKFLOW_RESILIENCE_ENABLED=true. It drains signals into an in-memory
// queue, processes them one at a time, and rotates before hitting Temporal's history limit.
func SequenceWorkflowV2(ctx workflow.Context, input SequenceWorkflowInput) error {
	logger := workflow.GetLogger(ctx)

	state := initSequenceState(ctx, input)
	signalChan := workflow.GetSignalChannel(ctx, Signal)

	for {
		// Drain new signals and rotate before processing when thresholds are met.
		// This prevents signal bursts from growing history past the server limit
		// while a long-running child workflow is still executing.
		drainSignalsIntoQueue(signalChan, &state.PendingQueue)
		if shouldContinueAsNewBeforeProcess(ctx, state) {
			logger.Info("ContinueAsNew triggered",
				"childrenRun", state.ChildrenRun,
				"pendingQueueDepth", len(state.PendingQueue),
				"historyLength", workflow.GetInfo(ctx).GetCurrentHistoryLength(),
			)
			return workflow.NewContinueAsNewError(ctx, SequenceWorkflowV2, SequenceWorkflowInput{State: &state})
		}

		// 1. Process queued work
		if len(state.PendingQueue) > 0 {
			item := state.PendingQueue[0]
			state.PendingQueue = state.PendingQueue[1:]

			childCtx := workflow.WithChildOptions(ctx, item.Options)
			if err := workflow.ExecuteChildWorkflow(childCtx, item.Function, item.Args...).Get(childCtx, nil); err != nil {
				logger.Error("Child workflow failed, continuing queue", "function", item.Function, "error", err)
			}
			state.ChildrenRun++

			// Check if rotation is needed after each child completes
			if shouldContinueAsNew(ctx, state) {
				drainSignalsIntoQueue(signalChan, &state.PendingQueue)
				logger.Info("ContinueAsNew triggered",
					"childrenRun", state.ChildrenRun,
					"pendingQueueDepth", len(state.PendingQueue),
					"historyLength", workflow.GetInfo(ctx).GetCurrentHistoryLength(),
				)
				return workflow.NewContinueAsNewError(ctx, SequenceWorkflowV2, SequenceWorkflowInput{State: &state})
			}

			// Drain any new signals that arrived while child was executing
			drainSignalsIntoQueue(signalChan, &state.PendingQueue)
			continue
		}

		// 2. No pending work — wait for new signal or idle exit
		selector := workflow.NewSelector(ctx)
		gotSignal := false

		selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, _ bool) {
			var params SignalWorkflowParams
			c.Receive(ctx, &params)
			state.PendingQueue = append(state.PendingQueue, params)
			gotSignal = true
		})

		idleTimer := workflow.NewTimer(ctx, sequenceIdleTimeout)
		selector.AddFuture(idleTimer, func(workflow.Future) {})

		selector.Select(ctx)

		// Select picks randomly when the signal channel and idle timer are both ready,
		// so drain before evaluating the exit condition.
		drainSignalsIntoQueue(signalChan, &state.PendingQueue)

		if !gotSignal && len(state.PendingQueue) == 0 && !selector.HasPending() {
			logger.Info("Idle timeout reached, exiting workflow cleanly",
				"childrenRun", state.ChildrenRun,
			)
			break
		}
	}

	return nil
}

// initSequenceState restores state from CAN input or initializes fresh state.
func initSequenceState(ctx workflow.Context, input SequenceWorkflowInput) SequenceWorkflowState {
	if input.State != nil {
		input.State.ChildrenRun = 0
		input.State.RunStartedAt = workflow.Now(ctx)
		return *input.State
	}
	return SequenceWorkflowState{
		PendingQueue: nil,
		ChildrenRun:  0,
		RunStartedAt: workflow.Now(ctx),
	}
}

// drainSignalsIntoQueue drains all buffered signals from the channel into the pending queue.
// Uses ReceiveAsync which is non-blocking — returns immediately when channel is empty.
func drainSignalsIntoQueue(ch workflow.ReceiveChannel, queue *[]SignalWorkflowParams) {
	for {
		var params SignalWorkflowParams
		if !ch.ReceiveAsync(&params) {
			break
		}
		*queue = append(*queue, params)
	}
}

// shouldContinueAsNewBeforeProcess checks if Temporal suggests rotating before starting
// the next child. Pending-queue depth is intentionally excluded here so a signal burst
// is drained and at least one child can run before queue-depth rotation.
//
// When a run starts via ContinueAsNew, the carried pending-queue payload alone can push
// history size past Temporal's suggest threshold before any child executes. In that case
// we must still dispatch at least one queued child; otherwise the workflow CAN-loops
// forever without making progress.
func shouldContinueAsNewBeforeProcess(ctx workflow.Context, state SequenceWorkflowState) bool {
	if len(state.PendingQueue) > 0 && state.ChildrenRun == 0 {
		return false
	}
	return workflow.GetInfo(ctx).GetContinueAsNewSuggested()
}

// shouldContinueAsNew determines if the workflow should rotate via ContinueAsNew.
func shouldContinueAsNew(ctx workflow.Context, state SequenceWorkflowState) bool {
	return shouldContinueAsNewBeforeProcess(ctx, state)
}

// ExecuteWorkflowSequentially sends a signal to the sequence workflow to execute a child workflow sequentially.
// If the sequence workflow is not running, it starts a new instance with the provided options and signals it.
//
//   - temporal: Temporal client instance.
//
//   - ctx: Go context.
//
//   - sequenceWfOptions: Workflow start options for the sequence workflow.
//     This includes options like WorkflowID, TaskQueue, WorkflowIDReusePolicy.
//     Sequence WorkflowID needs to have resource and its operations that we want to sequence post-fixed in below format
//
//     (Sequence-Uniqueness-ID)_Ops_(Resource1-C/U/D)-(Resource2-C/U/D)-...-(ResourceN-C/U/D)
//     , where C=CREATE, U=UPDATE, D=DELETE.
//
//   - wfFunction: The child workflow function to execute.
//
//   - wfOptions: Workflow options for the child workflow.
//     This includes options like TaskQueue, WorkflowID, and WorkflowIDReusePolicy.
//
//   - wfArgs: Arguments to pass to the child workflow function.
//
// Returns an error if the signal or workflow start fails.
func ExecuteWorkflowSequentially(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
	return ExecuteWorkflowSeq(temporal, ctx, sequenceWfOptions, wfFunction, wfOptions, wfArgs...)
}

// ExecuteWorkflowSeq is a variable that holds the function to execute workflows sequentially.
// This allows for easier mocking in tests.
var ExecuteWorkflowSeq = _executeWorkflowSequentially

func _executeWorkflowSequentially(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
	if err := validateWorkflowParams(sequenceWfOptions.ID, wfOptions); err != nil {
		return customerrors.New(fmt.Sprintf("Invalid parameters for sequence workflow execution, error: %v", err))
	}

	// Defaulting to customer task queue for the workflows, if not provided.
	if wfOptions.TaskQueue == "" {
		wfOptions.TaskQueue = workflowengine.CustomerTaskQueue
	}
	if sequenceWfOptions.TaskQueue == "" {
		sequenceWfOptions.TaskQueue = workflowengine.CustomerTaskQueue
	}

	// This will ensure that the control workflow does not run indefinitely and block the sequence workflow.
	var parseErr error
	sequenceWfOptions.WorkflowRunTimeout, parseErr = time.ParseDuration(SequenceWorkflowTimeout)
	if parseErr != nil {
		sequenceWfOptions.WorkflowRunTimeout = workflowengine.GetWorkflowGlobalTimeout()
	}

	// SignalWithStartWorkflow is used to signal the sequence workflow if running.
	// If the sequence workflow is not running, it will start a new instance with the provided options and signal it.
	var err error
	if sequenceResilienceEnabled {
		_, err = temporal.SignalWithStartWorkflow(
			ctx,
			sequenceWfOptions.ID,
			Signal,
			SignalWorkflowParams{
				Function: getWorkflowName(wfFunction),
				Args:     wfArgs,
				Options:  wfOptions,
			},
			sequenceWfOptions,
			SequenceWorkflowV2,
			SequenceWorkflowInput{},
		)
	} else {
		_, err = temporal.SignalWithStartWorkflow(
			ctx,
			sequenceWfOptions.ID,
			Signal,
			SignalWorkflowParams{
				Function: getWorkflowName(wfFunction),
				Args:     wfArgs,
				Options:  wfOptions,
			},
			sequenceWfOptions,
			SequenceWorkflow,
		)
	}
	if err != nil {
		return err
	}

	return nil
}

// getWorkflowName extracts the name of the workflow function from its pointer or returns the string if already a string.
// This is used to ensure that the workflow name is correctly identified when starting workflows.
func getWorkflowName(fnx interface{}) string {
	// If fnx is already a string, return it directly
	if name, ok := fnx.(string); ok {
		return name
	}

	// Otherwise, use reflection to get the function name from the function pointer
	// It uses reflection to get the function concrete value pointer, passes it to FuncForPC to get the
	// function name. It returns the name in the below format -
	// github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows.<function_name>
	// Then, we split it to get the last part, which is the actual function name.
	strs := strings.Split(runtime.FuncForPC(reflect.ValueOf(fnx).Pointer()).Name(), ".")
	return strs[len(strs)-1]
}

func validateWorkflowParams(sequenceWorkflowID string, wfOptions workflow.ChildWorkflowOptions) error {
	if sequenceWorkflowID == "" {
		return customerrors.New("sequence workflow ID cannot be empty")
	}
	if wfOptions.WorkflowID == "" {
		return customerrors.New("execution workflow ID cannot be empty")
	}

	return nil
}
