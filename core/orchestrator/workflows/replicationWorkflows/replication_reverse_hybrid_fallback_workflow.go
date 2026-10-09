package replicationWorkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
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

type reverseHybridFallbackReplicationWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &reverseHybridFallbackReplicationWorkflow{}

func ReverseHybridFallbackReplicationWorkflow(ctx workflow.Context, params *commonparams.ReverseAndResumeReplicationParams, hybridReverseResult *replication.ReverseHybridReplicationResult) (*vsa.VolumeReplication, error) {
	repWf := new(reverseHybridFallbackReplicationWorkflow)
	err := repWf.Setup(ctx, params)
	if err != nil {
		return nil, err
	}

	if err = repWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, err
	}

	repWf.Status = workflows.WorkflowStatusRunning
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		_ = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return nil, err
	}
	_, customErr := repWf.Run(ctx, hybridReverseResult, params)
	if customErr != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		_ = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, customErr
	}
	repWf.Status = workflows.WorkflowStatusCompleted
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

func (wf *reverseHybridFallbackReplicationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
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

func (wf *reverseHybridFallbackReplicationWorkflow) Run(ctx workflow.Context, args ...interface{}) (_ interface{}, customErr *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	result := args[0].(*replication.ReverseHybridReplicationResult)
	params := args[1].(*commonparams.ReverseAndResumeReplicationParams)
	replicationActivity := &replicationActivities.ReverseVolumeReplicationActivity{}
	reverseHybridActivity := &replicationActivities.ReverseHybridReplicationActivity{}
	updateAttrActivity := &replicationActivities.UpdateVolumeReplicationAttributesActivity{}

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

	rollbackManager := commonparams.NewRollbackManager()
	defer func() {
		if customErr != nil {
			if result != nil && result.DbVolReplication != nil {
				if updateErr := workflow.ExecuteActivity(ctx, reverseHybridActivity.SetReplicationToErrorForReverseHybrid, result.DbVolReplication, customErr.Error(), result.IsSrcForHybridReplication).Get(ctx, nil); updateErr != nil {
					logger.Error("Failed to update volume replication state in DB to error", "error", updateErr)
				}
			}
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, customErr)
		}
	}()

	reverseResult := replication.ReverseReplicationResult{
		Event:            result.Event,
		SrcProjectNumber: &result.Event.SourceProjectNumber,
		DstProjectNumber: &result.Event.DestinationProjectNumber,
		DbVolReplication: result.Event.ReplicationModel,
		NodeProvider:     result.NodeProvider,
	}

	// GetSrcBasePathReverse
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSrcBasePathReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// GetSignedSrcTokenReverse
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedSrcTokenReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// ReverseAndResumeReplication
	err = workflow.ExecuteActivity(ctx, replicationActivity.ReverseAndResumeReplication, &reverseResult, params).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	rollbackManager.AddActivity(replicationActivity.DeleteNewReplicationOnSrc, &reverseResult)

	// DescribeRemoteJobOnSrc (after ReverseAndResumeReplication)
	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobOnSrc, &reverseResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// HydrateReplicationSateAndTypeForReverseFallbackHybridReplication
	err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateReplicationSateAndTypeForReverseFallbackHybridReplication, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Get the original replication attributes before reversal
	originalAttrs := result.Event.ReplicationModel.ReplicationAttributes
	updateRequest := replicationActivities.ConvertToReversedAttributesForHybridRep(originalAttrs)
	// UpdateVolumeReplicationAttributes
	updateAttrResult := &replication.UpdateVolumeReplicationAttributesResult{
		Event: &replication.UpdateVolumeReplicationAttributesEvent{
			UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
				VolumeReplicationId:       result.DbVolReplication.ReplicationAttributes.SourceReplicationUUID,
				VolumeReplicationInternal: updateRequest,
			},
		},
	}
	err = workflow.ExecuteActivity(ctx, updateAttrActivity.GetSnapmirrorDetailsFromOntap, &updateAttrResult).Get(ctx, &updateAttrResult)
	if err != nil {
		logger.Error("Failed to update volume replication attributes", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, updateAttrActivity.UpdateDstVolumeReplication, &updateAttrResult).Get(ctx, &updateAttrResult)
	if err != nil {
		logger.Error("Failed to update volume replication attributes", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	// SetVolumeReplicationStatusToOnpremReplication
	err = workflow.ExecuteActivity(ctx, replicationActivity.SetVolumeReplicationStatusToOnpremReplication, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// ReleaseReplicationOnSrc
	err = workflow.ExecuteActivity(ctx, replicationActivity.ReleaseReplicationOnOldSrc, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// MountReplicationAfterReverse
	err = workflow.ExecuteActivity(ctx, replicationActivity.MountReplicationAfterReverse, &reverseResult).Get(ctx, &reverseResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}
