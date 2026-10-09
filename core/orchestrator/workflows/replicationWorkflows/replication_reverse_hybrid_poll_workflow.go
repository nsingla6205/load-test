package replicationWorkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	// reverseHybridReplicationMaxRetries caps the ListSnapmirrorDestinations activity retries.
	reverseHybridReplicationMaxRetries = env.GetInt("REVERSE_HYBRID_REPLICATION_MAX_RETRIES", 120)
	// remoteResyncPollMaxInterval uses a dedicated env var so the reverse-hybrid customer-command
	// wait window can be tuned independently from the shared RETRY_MAX_INTERVAL knob.
	remoteResyncPollMaxInterval = env.GetString("REVERSE_HYBRID_REPLICATION_POLL_MAX_INTERVAL", "1m")
)

type reverseHybridReplicationPollWorkflow struct {
	baseHybridReplicationWorkflow
}

// ReverseHybridReplicationPollWorkflow polls snapmirror destinations and updates replication state
func ReverseHybridReplicationPollWorkflow(ctx workflow.Context, result *replication.ReverseHybridReplicationResult) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting ReverseHybridReplicationPollWorkflow", "replicationUUID", result.DbVolReplication.UUID)

	pollWf := new(reverseHybridReplicationPollWorkflow)

	err := pollWf.Setup(ctx, nil)
	if err != nil {
		logger.Error("Failed to setup ReverseHybridReplicationPollWorkflow", "error", err)
		return err
	}
	pollWf.Status = workflows.WorkflowStatusRunning
	err = pollWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		pollWf.Status = workflows.WorkflowStatusFailed
		err = pollWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return err
	}
	_, workflowErr := pollWf.Run(ctx, result)
	if workflowErr != nil {
		pollWf.Status = workflows.WorkflowStatusFailed
		err2 := pollWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), workflowErr)
		if err2 != nil {
			workflow.GetLogger(ctx).Error("Failed to update job status", "error", err2)
		}
		return workflowErr
	}
	pollWf.Status = workflows.WorkflowStatusCompleted
	err2 := pollWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		workflow.GetLogger(ctx).Error("Failed to update job status", "error", err2)
		return err2
	}
	return nil
}

func (wf *reverseHybridReplicationPollWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:     wf.ID,
			Status: wf.Status,
		}, nil
	})
}

func (wf *reverseHybridReplicationPollWorkflow) Run(ctx workflow.Context, args ...interface{}) (_ interface{}, customErr *vsaerrors.CustomError) {
	result := args[0].(*replication.ReverseHybridReplicationResult)
	logger := workflow.GetLogger(ctx)
	logger.Info("Running ReverseHybridReplicationPollWorkflow", "replicationUUID", result.DbVolReplication.UUID)

	defer func() {
		if customErr != nil && result != nil && result.DbVolReplication != nil {
			replicationActivity := &replicationActivities.ReverseHybridReplicationActivity{}
			if updateErr := workflow.ExecuteActivity(ctx, replicationActivity.SetReplicationToErrorForReverseHybrid, result.DbVolReplication, customErr.Error(), result.IsSrcForHybridReplication).Get(ctx, nil); updateErr != nil {
				logger.Error("Failed to update volume replication state in DB to error", "error", updateErr)
			}
		}
	}()

	replicationActivity := &replicationActivities.ReverseHybridReplicationActivity{}
	updateAttrActivity := &replicationActivities.UpdateVolumeReplicationAttributesActivity{}

	// Configure activity retry policy for polling
	// Use a longer timeout and more retries since we're waiting for the destination to appear
	retryPolicy, policyErr := workflows.PopulateRetryPolicyParams()
	if policyErr != nil {
		return nil, workflows.ConvertToVSAError(policyErr)
	}
	maxAttempts := int32(reverseHybridReplicationMaxRetries)
	activityRetryMaxInterval, parseError := time.ParseDuration(remoteResyncPollMaxInterval)
	if parseError != nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, parseError))
	}

	// Create activity options with retry policy for ListSnapmirrorDestinations
	// This activity will retry automatically when destination is not found
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(workflows.StartToCloseTimeoutForReplicationActivities) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     workflows.BackoffCoefficientForReplicationActivities,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"NonRetryableErr", "PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	listDestinationsAO := activityOptions
	listDestinationsAO.RetryPolicy.MaximumAttempts = int32(maxAttempts)
	listDestinationsAO.RetryPolicy.MaximumInterval = activityRetryMaxInterval
	listDestinationsCtx := workflow.WithActivityOptions(ctx, listDestinationsAO)

	describeRemoteJobAO := activityOptions
	describeRemoteJobAO.RetryPolicy.MaximumAttempts = int32(ReplicationJobsRetryMaxAttempts)
	ctx1 := workflow.WithActivityOptions(ctx, describeRemoteJobAO)

	// 1. List snapmirror destinations - this will retry automatically until found or max attempts
	// Activity returns error when destination not found, triggering retry
	// Activity returns success when destination is found
	var err error
	err = workflow.ExecuteActivity(listDestinationsCtx, replicationActivity.ListSnapmirrorDestinationsForHybridReverse, result).Get(listDestinationsCtx, result)
	if err != nil {
		logger.Warn("Failed to find snapmirror destination after max retries", "replicationUUID", result.DbVolReplication.UUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	logger.Info("Successfully found snapmirror destination", "replicationUUID", result.DbVolReplication.UUID)

	// 2. Hydrate replication state and type for reverse hybrid replication
	err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateReplicationSateAndTypeForReverseHybridReplication, result).Get(ctx, result)
	if err != nil {
		logger.Error("Failed to hydrate replication state and type", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	// 3. Update replication state based on snapmirror status
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationStateForHybridReverse, result).Get(ctx, result)
	if err != nil {
		logger.Error("Failed to update replication state", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	// 4. Update replication attributes on destination (new source after reverse)
	// Get destination base path
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathForHybridReverse, result).Get(ctx, result)
	if err != nil {
		logger.Error("Failed to get destination base path", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	// Get destination JWT token
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedDstTokenForHybridReverse, result).Get(ctx, result)
	if err != nil {
		logger.Error("Failed to get destination JWT token", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	updateAttrResult := &replication.UpdateVolumeReplicationAttributesResult{
		Event: &replication.UpdateVolumeReplicationAttributesEvent{
			UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
				VolumeReplicationId: result.DbVolReplication.ReplicationAttributes.DestinationReplicationUUID,
			},
		},
	}
	err = workflow.ExecuteActivity(ctx, updateAttrActivity.UpdateSrcVolumeReplication, &updateAttrResult).Get(ctx, &updateAttrResult)
	if err != nil {
		logger.Error("Failed to update volume replication attributes", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	// 5. Cleanup old replication on destination (new source after reverse)
	err = workflow.ExecuteActivity(ctx, replicationActivity.CleanupOldReplicationForHybridReverse, result).Get(ctx, result)
	if err != nil {
		logger.Error("Failed to cleanup old replication", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	// 6. Describe remote job for cleanup operation
	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobOnDstForHybridReverse, result).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to describe remote job", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}
