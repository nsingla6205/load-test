package replicationWorkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	CancelReplicationSignalName = "cancel-replication-creation"
)

var (
	ReplicationJobsRetryMaxAttempts = env.GetInt("REPLICATION_JOBS_RETRY_MAX_ATTEMPTS", 10)
	quotaRuleSync                   = env.GetBool("QUOTA_RULE_SYNC", true)
)

type createVolumeReplicationWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

// Enforcing the WorkflowInterface on createVolumeReplicationWorkflow
var _ workflows.WorkflowInterface = &createVolumeReplicationWorkflow{}

// CreateVolumeReplicationWorkflow Workflow process volume replication related request from a customer.
func CreateVolumeReplicationWorkflow(ctx workflow.Context, params *common.CreateVolumeReplicationParams, volumeRep *datamodel.VolumeReplication, event *replication.CreateReplicationEvent) (gcpgenserver.V1betaDescribeVolumeRes, error) {
	volumeRepWf := new(createVolumeReplicationWorkflow)
	err := volumeRepWf.Setup(ctx, params)
	if err != nil {
		return nil, err
	}
	if err = volumeRepWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	volumeRepWf.Status = workflows.WorkflowStatusRunning
	err = volumeRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		volumeRepWf.Status = workflows.WorkflowStatusFailed
		err = volumeRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return nil, err
	}
	_, customErr := volumeRepWf.Run(ctx, volumeRep, event)
	if customErr != nil {
		volumeRepWf.Status = workflows.WorkflowStatusFailed
		err = volumeRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, err
	}
	volumeRepWf.Status = workflows.WorkflowStatusCompleted
	err = volumeRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

func (wf *createVolumeReplicationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createReplicationParams := input.(*common.CreateVolumeReplicationParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createReplicationParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *createVolumeReplicationWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	volumeReplication := args[0].(*datamodel.VolumeReplication)
	event := args[1].(*replication.CreateReplicationEvent)
	replicationActivity := &replicationActivities.VolumeReplicationCreateActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(workflows.StartToCloseTimeoutForReplicationActivities) * time.Second,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     workflows.BackoffCoefficientForReplicationActivities,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(ReplicationJobsRetryMaxAttempts)

	ctx1 := workflow.WithActivityOptions(ctx, ao1)
	dbVolumeRep := volumeReplication

	replicationResult := replication.CreateReplicationResult{
		SrcProjectNumber: &event.SourceProjectNumber,
		DstProjectNumber: &event.DestinationProjectNumber,
		Event:            event,
		DbVolReplication: dbVolumeRep,
	}

	cancellationHandler := common.NewWorkflowCancellationHandler(ctx, CancelReplicationSignalName, volumeReplication.UUID, "replication")
	rollbackManager := common.NewRollbackManager()

	updateReplicationStateToError := func(disconnectedCtx workflow.Context) error {
		volumeReplication.State = datamodel.LifeCycleStateError
		volumeReplication.StateDetails = datamodel.LifeCycleStateCreationErrorDetails
		err2 := workflow.ExecuteActivity(disconnectedCtx, replicationActivity.UpdateReplicationState, *volumeReplication).Get(disconnectedCtx, nil)
		if err2 != nil {
			log.Error("Failed to update replication state in DB to error", "error", err2)
		}
		return err2
	}

	// Defer function to mark the database entry in error state if any error occurs
	defer func() {
		common.ExecuteDeferredCleanup(ctx, cancellationHandler, rollbackManager, err, log, "replication", volumeReplication.UUID,
			updateReplicationStateToError,
			func(disconnectedCtx workflow.Context, cancelErr error) {
				_ = updateReplicationStateToError(disconnectedCtx)
			},
			nil) // shouldRollbackOnError
	}()

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	var dbNodes []*datamodel.Node

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSrcBasePath, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePath, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedSrcToken, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedDstToken, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &replicationResult.Event.SourcePool.ID).Get(ctx, &dbNodes)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	srcNode := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   replicationResult.Event.SourcePool.DeploymentName,
		OntapCredentials: replicationResult.Event.SourcePool.PoolCredentials,
	})

	replicationResult.SrcNode = srcNode

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSourceInterclusterLifs, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDestinationPoolDetails, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateSnapmirrorFirewall, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.PollSnapmirrorFirewallOperation, &replicationResult).Get(ctx1, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateClusterPeering, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.AcceptClusterPeering, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJob, &replicationResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	dbVolumeRep.StateDetails = "Cluster Peered"
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationState, dbVolumeRep).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateDestinationVolume, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateDestinationVolumeDetails, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJob, &replicationResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateDestinationVolume, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	dbVolumeRep.StateDetails = "Remote volume created"
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationState, dbVolumeRep).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetVolumeSVMNames, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateReplicationOnDestination, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateDestinationVolumeReplicationDetails, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.AcceptSvmPeer, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJob, &replicationResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationDetails, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.MountReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	if quotaRuleSync {
		// Fetch source quota rules from database
		var sourceQuotaRules []*datamodel.QuotaRule
		err = workflow.ExecuteActivity(ctx, replicationActivity.ListQuotaRulesLocal, &replicationResult).Get(ctx, &sourceQuotaRules)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		replicationResult.SourceQuotaRules = sourceQuotaRules

		// Sync quota rules from source to destination
		// This activity now returns the quota rules in the response, so we don't need to call ListQuotaRulesOnDestination
		err = workflow.ExecuteActivity(ctx, replicationActivity.CreateQuotaRulesOnDestination, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Hydrate quota rules to CCFE if quota rules exist
		if len(replicationResult.DestinationQuotaRules) > 0 {
			err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateQuotaRules,
				replicationResult.DestinationQuotaRules,
				replicationResult.DstVolume.ResourceId,
				replicationResult.Event.DestinationLocationID,
				*replicationResult.DstProjectNumber,
			).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}
		}
	}

	return nil, workflows.ConvertToVSAError(err)
}
