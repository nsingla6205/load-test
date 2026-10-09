package replicationWorkflows

import (
	"errors"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Note: quotaRuleSyncEnabled and isQuotaRuleFailure are defined in replication_internal_stop_workflow.go

type ReplicationStopWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &ReplicationStopWorkflow{}

func StopReplicationWorkflow(ctx workflow.Context, params *commonparams.StopReplicationParams, event *replication.StopReplicationEvent) (*vsa.VolumeReplication, error) {
	repWf := new(ReplicationStopWorkflow)
	err := repWf.Setup(ctx, params)
	if err != nil {
		return nil, err
	}
	logger := workflow.GetLogger(ctx)
	repWf.Status = workflows.WorkflowStatusRunning
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return nil, err
	}
	_, customErr := repWf.Run(ctx, event)
	if customErr != nil {
		// Check if this is a quota rule failure (partial success case) and quotaRuleSync is enabled
		if quotaRuleSyncEnabled && isQuotaRuleFailure(customErr) {
			logger.Warn("Stop replication succeeded but quota rule sync failed on destination, marking workflow as completed")
			repWf.Status = workflows.WorkflowStatusCompleted
			// Use vsaerrors.NewVCPError so it's recognized as CustomError in UpdateJobStatus
			quotaRuleErr := vsaerrors.NewVCPError(
				vsaerrors.ErrBreakReplicationQuotaRuleFailure,
				errors.New(datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure),
			)
			err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), quotaRuleErr)
			return nil, err
		}

		// For all other errors, mark workflow as failed
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, err
	}
	repWf.Status = workflows.WorkflowStatusCompleted
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

func (wf *ReplicationStopWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	stopReplicationParams := input.(*commonparams.StopReplicationParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = stopReplicationParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ReplicationStopWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	event := args[0].(*replication.StopReplicationEvent)
	replicationActivity := &replicationActivities.StopVolumeReplicationActivity{}
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
			NonRetryableErrorTypes: []string{"NonRetryableError", "PanicError"},
		},
	}
	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(ReplicationJobsRetryMaxAttempts)

	ctx = workflow.WithActivityOptions(ctx, ao)
	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	replicationResult := replication.StopReplicationResult{
		SrcProjectNumber: &event.SourceProjectNumber,
		DstProjectNumber: &event.DestinationProjectNumber,
		Event:            event,
		DbVolReplication: event.ReplicationModel,
		CorrelationID:    event.CommonReplicationEventParams.XCorrelationID,
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.SetHybridReplicationVariablesStop, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSrcBasePathStop, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathStop, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedSrcTokenStop, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedDstTokenStop, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if !replicationResult.IsSrcForHybridReplication {
		err = workflow.ExecuteActivity(ctx, replicationActivity.StopReplicationOnDestination, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeDestJobStop, &replicationResult).Get(ctx, nil)
		if err != nil {
			// Check if this is a quota rule failure from internal stop
			// Check error message directly before conversion
			if quotaRuleSyncEnabled && strings.Contains(err.Error(), datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure) {
				// Return quota-specific error to be handled specially by error handler
				// This allows the workflow to be marked as completed (partial success) instead of failed
				// (Same pattern as internal stop workflow)
				return nil, workflows.ConvertToVSAError(
					vsaerrors.NewVCPError(
						vsaerrors.ErrBreakReplicationQuotaRuleFailure,
						vsaerrors.New(datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure),
					),
				)
			}
			return nil, workflows.ConvertToVSAError(err)
		}
	} else {
		err = workflow.ExecuteActivity(ctx, replicationActivity.HandleHybridReplicationStopWhenGcnvIsSrc, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	return nil, workflows.ConvertToVSAError(err)
}
