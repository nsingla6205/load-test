package replicationWorkflows

import (
	"errors"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	ReverseReplicationJobsRetryMaxAttempts = env.GetInt("REPLICATION_JOBS_RETRY_MAX_ATTEMPTS", 10)
)

const (
	reverseResumeQuotaRuleError = "Operation was successful but quota rule sync between source and destination failed"
)

type ReverseReplicationWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &ReverseReplicationWorkflow{}

func ReverseAndResumeVolumeReplicationWorkflow(ctx workflow.Context, params *commonparams.ReverseAndResumeReplicationParams, event *replication.ReverseReplicationEvent) (*vsa.VolumeReplication, error) {
	repWf := new(ReverseReplicationWorkflow)
	err := repWf.Setup(ctx, params)
	if err != nil {
		return nil, err
	}
	repWf.Status = workflows.WorkflowStatusRunning
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return nil, err
	}
	_, customErr := repWf.Run(ctx, event, params)
	if customErr != nil {
		logger := workflow.GetLogger(ctx)
		logger.Info("Reverse Resume Volume Replication workflow run executed with error", "error", customErr)
		// Check if this is a quota rule failure (partial success case) and quotaRuleSync is enabled
		if quotaRuleSync && isReverseResumeQuotaRuleFailure(customErr) {
			// Quota rule sync failed but reverse resume succeeded - treat as partial success
			logger.Warn("Reverse resume succeeded but quota rule operations failed")
			repWf.Status = workflows.WorkflowStatusCompleted
			// Use vsaerrors.NewVCPError so it's recognized as CustomError in UpdateJobStatus
			quotaRuleErr := vsaerrors.NewVCPError(
				vsaerrors.ErrReverseResumeReplicationQuotaRuleFailure,
				errors.New(reverseResumeQuotaRuleError),
			)
			err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), quotaRuleErr)
			return nil, err
		}
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, err
	}
	repWf.Status = workflows.WorkflowStatusCompleted
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

// isReverseResumeQuotaRuleFailure checks if the error is a reverse resume quota rule failure error
func isReverseResumeQuotaRuleFailure(err *vsaerrors.CustomError) bool {
	if err == nil {
		return false
	}
	return err.TrackingID == vsaerrors.ErrReverseResumeReplicationQuotaRuleFailure
}

func (wf *ReverseReplicationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
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

func (wf *ReverseReplicationWorkflow) Run(ctx workflow.Context, args ...interface{}) (result interface{}, customErr *vsaerrors.CustomError) {
	event := args[0].(*replication.ReverseReplicationEvent)
	params := args[1].(*commonparams.ReverseAndResumeReplicationParams)
	replicationActivity := &replicationActivities.ReverseVolumeReplicationActivity{}
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
	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(ReverseReplicationJobsRetryMaxAttempts)

	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	reverseResult := replication.ReverseReplicationResult{
		Event:            event,
		SrcProjectNumber: &event.SourceProjectNumber,
		DstProjectNumber: &event.DestinationProjectNumber,
		DbVolReplication: event.ReplicationModel,
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSrcBasePathReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedSrcTokenReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedDstTokenReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.VerifyNewDstVolume, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.ResizeNewDstVolumeIfNeeded, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobOnSrc, &reverseResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	rollbackManager := commonparams.NewRollbackManager()
	defer func() {
		if customErr != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, customErr)
		}
	}()

	err = workflow.ExecuteActivity(ctx, replicationActivity.ReverseAndResumeReplication, &reverseResult, params).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	rollbackManager.AddActivity(replicationActivity.DeleteNewReplicationOnSrc, &reverseResult)

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobOnSrc, &reverseResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Update the database on the current source (which becomes the new destination after reverse)
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateVolumeReplicationAttributesSrc, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobOnSrc, &reverseResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(replicationActivity.RevertVolumeReplicationAttributesSrc, &reverseResult)

	// Update the database on the current destination (which becomes the new source after reverse)
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateVolumeReplicationAttributesDst, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobOnDst, &reverseResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.MountReplicationAfterReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Quota rules synchronization after mount
	if quotaRuleSync && reverseResult.DbVolReplication.HybridReplicationAttributes == nil {
		logger := workflow.GetLogger(ctx)

		// List new source quota rules (current destination)
		var newSourceQuotaRules []*datamodel.QuotaRule
		err = workflow.ExecuteActivity(ctx, replicationActivity.ListQuotaRulesOnNewSourceReverse, &reverseResult).Get(ctx, &newSourceQuotaRules)
		if err != nil {
			logger.Warn("Reverse resume succeeded but listing new source quota rules failed", "error", err)
			return nil, vsaerrors.NewVCPError(
				vsaerrors.ErrReverseResumeReplicationQuotaRuleFailure,
				errors.New(reverseResumeQuotaRuleError),
			)
		}
		reverseResult.SourceQuotaRules = newSourceQuotaRules

		// List new destination quota rules (current source)
		var newDestinationQuotaRules []*datamodel.QuotaRule
		err = workflow.ExecuteActivity(ctx, replicationActivity.ListQuotaRulesOnNewDestinationReverse, &reverseResult).Get(ctx, &newDestinationQuotaRules)
		if err != nil {
			logger.Warn("Reverse resume succeeded but listing new destination quota rules failed", "error", err)
			return nil, vsaerrors.NewVCPError(
				vsaerrors.ErrReverseResumeReplicationQuotaRuleFailure,
				errors.New(reverseResumeQuotaRuleError),
			)
		}
		reverseResult.DestinationQuotaRules = newDestinationQuotaRules

		// Dehydrate quota rules if hydration enabled
		var dehydratedQuotaRules []*datamodel.QuotaRule
		if hydrationEnabled && reverseResult.DestinationQuotaRules != nil && len(reverseResult.DestinationQuotaRules) > 0 {
			err = workflow.ExecuteActivity(ctx, replicationActivity.DehydrateQuotaRulesReverse,
				reverseResult.DestinationQuotaRules,
				reverseResult.NewDstVolume.ResourceId,
				reverseResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
				*reverseResult.SrcProjectNumber,
			).Get(ctx, &dehydratedQuotaRules)
			if err != nil {
				logger.Warn("Reverse resume succeeded but dehydrating quota rules failed", "error", err)
				return nil, vsaerrors.NewVCPError(
					vsaerrors.ErrReverseResumeReplicationQuotaRuleFailure,
					errors.New(reverseResumeQuotaRuleError),
				)
			}

			// Check if this is a partial failure (only SOME quota rules were dehydrated, not all)
			// Partial failure: clear source and keep only dehydrated rules for recovery re-sync
			// Full success: keep both source and destination as-is for normal sync
			if len(dehydratedQuotaRules) > 0 && (len(dehydratedQuotaRules) < len(reverseResult.DestinationQuotaRules)) {
				logger.Warn("Partial dehydration failure. Entering recovery mode.",
					"dehydrated", len(dehydratedQuotaRules), "total", len(reverseResult.DestinationQuotaRules))
				reverseResult.SourceQuotaRules = nil
				reverseResult.DestinationQuotaRules = dehydratedQuotaRules
			}
		}

		// Sync quota rules to new destination
		err = workflow.ExecuteActivity(ctx, replicationActivity.AddNewSrcQuotaRulesToNewDstDBReverse, &reverseResult).Get(ctx, &reverseResult)
		if err != nil {
			logger.Warn("Reverse resume succeeded but syncing quota rules failed", "error", err)
			return nil, vsaerrors.NewVCPError(
				vsaerrors.ErrReverseResumeReplicationQuotaRuleFailure,
				errors.New(reverseResumeQuotaRuleError),
			)
		}

		// Hydrate quota rules to CCFE if enabled
		if hydrationEnabled && reverseResult.DestinationQuotaRules != nil && len(reverseResult.DestinationQuotaRules) > 0 {
			err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateQuotaRulesReverse,
				reverseResult.DestinationQuotaRules,
				reverseResult.NewDstVolume.ResourceId,
				reverseResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
				*reverseResult.SrcProjectNumber,
			).Get(ctx, nil)
			if err != nil {
				logger.Warn("Reverse resume succeeded but hydrating quota rules failed", "error", err)
				return nil, vsaerrors.NewVCPError(
					vsaerrors.ErrReverseResumeReplicationQuotaRuleFailure,
					errors.New(reverseResumeQuotaRuleError),
				)
			}
		}
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.CleanupOldReplication, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}
