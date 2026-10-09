package replicationWorkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type reverseHybridReplicationWorkflow struct {
	baseHybridReplicationWorkflow
}

var (
	_ workflows.WorkflowInterface = &reverseHybridReplicationWorkflow{}
)

// ReverseHybridReplicationWorkflow handles reverse operations for hybrid replications
func ReverseHybridReplicationWorkflow(ctx workflow.Context, params *commonparams.ReverseAndResumeReplicationParams, event *replication.ReverseReplicationEvent) error {
	log := workflow.GetLogger(ctx)
	volumeRepWf := new(reverseHybridReplicationWorkflow)

	err := volumeRepWf.Setup(ctx, params)
	if err != nil {
		log.Error("Failed to setup ReverseHybridReplicationWorkflow", "error", err)
		return err
	}

	if err = volumeRepWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return err
	}

	volumeRepWf.Status = workflows.WorkflowStatusRunning
	err = volumeRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		volumeRepWf.Status = workflows.WorkflowStatusFailed
		err = volumeRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return err
	}
	_, customErr := volumeRepWf.Run(ctx, event, params)
	if customErr != nil {
		volumeRepWf.Status = workflows.WorkflowStatusFailed
		_ = volumeRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return customErr
	}
	volumeRepWf.Status = workflows.WorkflowStatusCompleted
	err = volumeRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return err
}

func (wf *reverseHybridReplicationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	reverseReplicationParams := input.(*commonparams.ReverseAndResumeReplicationParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = reverseReplicationParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *reverseHybridReplicationWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	event := args[0].(*replication.ReverseReplicationEvent)
	params := args[1].(*commonparams.ReverseAndResumeReplicationParams)
	replicationActivity := &replicationActivities.ReverseHybridReplicationActivity{}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(workflows.StartToCloseTimeoutForReplicationActivities) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     workflows.BackoffCoefficientForReplicationActivities,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"NonRetryableErr", "PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Initialize result
	reverseResult := replication.ReverseHybridReplicationResult{
		Event:            event,
		DbVolReplication: event.ReplicationModel,
		DstProjectNumber: &event.DestinationProjectNumber,
		SrcProjectNumber: &event.SourceProjectNumber,
	}

	// Set hybrid replication variables (validate and set flags)
	err = workflow.ExecuteActivity(ctx, replicationActivity.SetHybridReplicationVariablesReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Get node provider first (needed for cluster activities)
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetNodeProviderForHybridReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Phase 2: Check Cluster Peer Health (activity)
	err = workflow.ExecuteActivity(ctx, replicationActivity.CheckClusterPeerHealthForHybridReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Phase 3: Main Workflow
	// 1. Update RBAC Role
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateRbacRoleForHybridReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// 2. Generate and Update Replication with Reverse Commands
	err = workflow.ExecuteActivity(ctx, replicationActivity.GenerateReverseCommandsForHybridReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationWithReverseCommandsForHybridReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// 3. Create job for child workflow and start appropriate workflow based on IsSrcForHybridReplication
	var pollJob datamodel.Job
	if reverseResult.IsSrcForHybridReplication {
		// For source hybrid replication, use fallback workflow
		err = workflow.ExecuteActivity(ctx, replicationActivity.CreateJobForHybridReverse, &reverseResult, string(datamodel.JobTypeReverseHybridReplicationFallbackInternal)).Get(ctx, &pollJob)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Start ReverseHybridFallbackReplicationWorkflow and verify it started
		childCtx1 := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowID:            pollJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
			ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_ABANDON,
		})

		childWorkflowFuture := workflow.ExecuteChildWorkflow(
			childCtx1,
			ReverseHybridFallbackReplicationWorkflow,
			params,
			reverseResult,
		)

		// Don't wait for completion - just verify it started
		var childWE workflow.Execution
		err = childWorkflowFuture.GetChildWorkflowExecution().Get(ctx, &childWE)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	} else {
		// For destination hybrid replication, use poll workflow
		err = workflow.ExecuteActivity(ctx, replicationActivity.CreateJobForHybridReverse, &reverseResult, string(datamodel.JobTypeReverseHybridReplicationInternal)).Get(ctx, &pollJob)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Start Background Polling Workflow (asynchronous, no wait).
		// Use the poll-specific timeout (default 150 min) so the workflow outlives the
		// ListSnapmirrorDestinations retry window (120 retries × 1 min cap = 120 min upper bound)
		// plus Phase 2 cleanup activities (~10 min); see VSCP-6543.
		childCtx1 := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowID:            pollJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetReverseHybridReplicationPollWorkflowTimeout(),
			ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_ABANDON,
		})

		childWorkflowFuture := workflow.ExecuteChildWorkflow(
			childCtx1,
			ReverseHybridReplicationPollWorkflow,
			&reverseResult,
		)

		// Don't wait for completion - just verify it started
		var childWE workflow.Execution
		err = childWorkflowFuture.GetChildWorkflowExecution().Get(ctx, &childWE)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// 4. Return - workflow completes
	return nil, nil
}
