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
	hydrationEnabled = env.GetBool("GCP_HYDRATE_ENABLED", true)
)

const (
	replicationQuotaRuleError = "Operation was successful but quota rule sync between source and destination failed"
)

// isResumeQuotaRuleFailure checks if the error is a resume quota rule failure error
func isResumeQuotaRuleFailure(err *vsaerrors.CustomError) bool {
	if err == nil {
		return false
	}
	return err.TrackingID == vsaerrors.ErrResumeReplicationQuotaRuleFailure
}

type ReplicationResumeWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &ReplicationResumeWorkflow{}

func ResumeReplicationWorkflow(ctx workflow.Context, params *commonparams.ResumeReplicationParams, event *replication.ResumeReplicationEvent) (*vsa.VolumeReplication, error) {
	repWf := new(ReplicationResumeWorkflow)
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
		logger.Info("Resume Volume Replication workflow run executed with error", "error", customErr)
		// Check if this is a quota rule failure (partial success case) and quotaRuleSync is enabled
		if quotaRuleSync && isResumeQuotaRuleFailure(customErr) {
			// Quota rule sync failed but replication resume succeeded - treat as partial success
			logger.Warn("Resume replication succeeded but quota rule operations failed")
			repWf.Status = workflows.WorkflowStatusCompleted
			// Use vsaerrors.NewVCPError so it's recognized as CustomError in UpdateJobStatus
			quotaRuleErr := vsaerrors.NewVCPError(
				vsaerrors.ErrResumeReplicationQuotaRuleFailure,
				errors.New(replicationQuotaRuleError),
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

func (wf *ReplicationResumeWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	resumeReplicationParams := input.(*commonparams.ResumeReplicationParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = resumeReplicationParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ReplicationResumeWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	event := args[0].(*replication.ResumeReplicationEvent)
	params := args[1].(*commonparams.ResumeReplicationParams)
	replicationActivity := &replicationActivities.ResumeVolumeReplicationActivity{}
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
	ao1.RetryPolicy.MaximumAttempts = int32(ReplicationJobsRetryMaxAttempts)

	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	replicationResult := replication.ResumeReplicationResult{
		SrcProjectNumber: &event.SourceProjectNumber,
		DstProjectNumber: &event.DestinationProjectNumber,
		Event:            event,
		DbVolReplication: event.ReplicationModel,
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.SetHybridReplicationVariablesResume, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSrcBasePathResume, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathResume, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedSrcTokenResume, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedDstTokenResume, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if !replicationResult.IsSrcForHybridReplication {
		err = workflow.ExecuteActivity(ctx, replicationActivity.VerifyDstVolume, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.ResizeVolumeIfNeeded, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DescribeRemoteJobResume, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.ResumeReplicationOnDestination, &replicationResult, params).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobResume, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.MountReplicationAfterResume, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	} else {
		err = workflow.ExecuteActivity(ctx, replicationActivity.HandleHybridReplicationResumeWhenGcnvIsSrc, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Quota rules dehydration after mount
	if quotaRuleSync && !replicationResult.IsHybridReplicationVolume {
		logger := workflow.GetLogger(ctx)

		// List source quota rules via VCP API
		var sourceQuotaRules []*datamodel.QuotaRule
		err = workflow.ExecuteActivity(ctx, replicationActivity.ListQuotaRulesOnSourceResume, &replicationResult).Get(ctx, &sourceQuotaRules)
		if err != nil {
			logger.Warn("Resume replication succeeded but listing source quota rules failed", "error", err)
			return nil, vsaerrors.NewVCPError(
				vsaerrors.ErrResumeReplicationQuotaRuleFailure,
				errors.New(replicationQuotaRuleError),
			)
		}
		replicationResult.SourceQuotaRules = sourceQuotaRules

		// List quota rules on destination volume via VCP API
		var destinationQuotaRules []*datamodel.QuotaRule
		err = workflow.ExecuteActivity(ctx, replicationActivity.ListQuotaRulesOnDestinationResume, &replicationResult).Get(ctx, &destinationQuotaRules)
		if err != nil {
			logger.Warn("Resume replication succeeded but listing destination quota rules failed", "error", err)
			return nil, vsaerrors.NewVCPError(
				vsaerrors.ErrResumeReplicationQuotaRuleFailure,
				errors.New(replicationQuotaRuleError),
			)
		}
		replicationResult.DestinationQuotaRules = destinationQuotaRules

		// Dehydrate (notify CCFE about deletions) quota rules if hydration is enabled
		// Note: Dehydration failure does not cause resume failure - we do best-effort recovery and continue
		var dehydratedQuotaRules []*datamodel.QuotaRule
		if hydrationEnabled && replicationResult.DestinationQuotaRules != nil && len(replicationResult.DestinationQuotaRules) > 0 {
			// Dehydrate quota rules and capture the list of successfully dehydrated ones
			err = workflow.ExecuteActivity(ctx, replicationActivity.DehydrateQuotaRulesResume,
				replicationResult.DestinationQuotaRules,
				replicationResult.DstVolume.ResourceId,
				replicationResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
				*replicationResult.DstProjectNumber,
			).Get(ctx, &dehydratedQuotaRules)
			if err != nil {
				logger.Warn("Resume replication succeeded but dehydrating quota rules failed", "error", err)
				return nil, vsaerrors.NewVCPError(
					vsaerrors.ErrResumeReplicationQuotaRuleFailure,
					errors.New(replicationQuotaRuleError),
				)
			}

			// Check if this is a partial failure (only SOME quota rules were dehydrated, not all)
			// Partial failure: clear source and keep only dehydrated rules for recovery re-sync
			// Full success: keep both source and destination as-is for normal sync
			if len(dehydratedQuotaRules) > 0 && (len(dehydratedQuotaRules) < len(replicationResult.DestinationQuotaRules)) {
				logger.Warn("Partial dehydration failure. Entering recovery mode.",
					"dehydrated", len(dehydratedQuotaRules), "total", len(replicationResult.DestinationQuotaRules))
				replicationResult.SourceQuotaRules = nil
				replicationResult.DestinationQuotaRules = dehydratedQuotaRules
			}
		}

		// Sync source quota rules to destination via API (returns quota rules for hydration)
		err = workflow.ExecuteActivity(ctx, replicationActivity.AddSrcQuotaRulesToDstDB, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			logger.Warn("Resume replication succeeded but syncing quota rules to destination failed", "error", err)
			return nil, vsaerrors.NewVCPError(
				vsaerrors.ErrResumeReplicationQuotaRuleFailure,
				errors.New(replicationQuotaRuleError),
			)
		}

		// Hydrate quota rules to CCFE if hydration is enabled
		if hydrationEnabled && replicationResult.DestinationQuotaRules != nil && len(replicationResult.DestinationQuotaRules) > 0 {
			err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateQuotaRulesResume,
				replicationResult.DestinationQuotaRules,
				replicationResult.DstVolume.ResourceId,
				replicationResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
				*replicationResult.DstProjectNumber,
			).Get(ctx, nil)
			if err != nil {
				logger.Warn("Resume replication succeeded but hydrating quota rules failed", "error", err)
				return nil, vsaerrors.NewVCPError(
					vsaerrors.ErrResumeReplicationQuotaRuleFailure,
					errors.New(replicationQuotaRuleError),
				)
			}
		}
	}

	return nil, workflows.ConvertToVSAError(err)
}
