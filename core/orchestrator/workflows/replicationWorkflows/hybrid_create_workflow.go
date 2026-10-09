package replicationWorkflows

import (
	"errors"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	clusterPeerTimeout  = env.GetDuration("CLUSTER_PEER_TIMEOUT", 60*time.Minute)
	clusterPeerInterval = env.GetDuration("CLUSTER_PEER_INTERVAL", 15*time.Second)
)

type baseHybridReplicationWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

type createHybridReplicationWorkflow struct {
	baseHybridReplicationWorkflow
}

var (
	_ workflows.WorkflowInterface = &createHybridReplicationWorkflow{}
	_ workflows.WorkflowInterface = &createEstablishPeeringWorkflow{}
	_ workflows.WorkflowInterface = &createInternalEstablishWorkflow{}
)

// CreateHybridReplicationWorkflow Workflow process volume replication related request from a customer.
func CreateHybridReplicationWorkflow(ctx workflow.Context, params *common.CreateVolumeParams, volume *datamodel.Volume) (gcpgenserver.V1betaDescribeVolumeRes, error) {
	log := workflow.GetLogger(ctx)
	volumeWf := new(createHybridReplicationWorkflow)

	err := volumeWf.Setup(ctx, params)
	if err != nil {
		log.Error("Failed to setup CreateVolumeWorkflow", "error", err)
		return nil, err
	}
	volumeWf.Status = workflows.WorkflowStatusRunning
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		volumeWf.Status = workflows.WorkflowStatusFailed
		err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return nil, err
	}
	_, customErr := volumeWf.Run(ctx, volume, params)
	if customErr != nil {
		volumeWf.Status = workflows.WorkflowStatusFailed
		err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, err
	}
	volumeWf.Status = workflows.WorkflowStatusCompleted
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

func (wf *createHybridReplicationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createPoolParams := input.(*common.CreateVolumeParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createPoolParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *createHybridReplicationWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	dbVolume := args[0].(*datamodel.Volume)
	createVolumeParams := args[1].(*common.CreateVolumeParams)
	replicationActivity := &replicationActivities.HybridReplicationActivity{}
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
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}

	ctx = workflow.WithActivityOptions(ctx, ao)
	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(ReplicationJobsRetryMaxAttempts)

	replicationResult := replication.CreateHybridReplicationResult{
		DestinationVolume:           dbVolume,
		DestinationRegion:           createVolumeParams.Region,
		DestinationZone:             createVolumeParams.Zone,
		DestinationProjectNumber:    createVolumeParams.AccountName,
		HybridReplicationParameters: createVolumeParams.HybridReplicationParameters,
	}

	var createdJob datamodel.Job

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstSignedTokenForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	var childWorkflowResult gcpgenserver.V1betaDescribeVolumeRes
	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateJobForHybridReplication, &replicationResult, string(datamodel.JobTypeCreateVolume)).Get(ctx, &createdJob)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             workflowengine.CustomerTaskQueue,
		WorkflowID:            createdJob.WorkflowID,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
	})

	err = workflow.ExecuteChildWorkflow(childCtx, workflows.CreateVolumeWorkflow, createVolumeParams, dbVolume).Get(ctx, &childWorkflowResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	replicationResult.JobId = &createdJob.UUID
	if createdJob.CorrelationID != "" {
		replicationResult.CorrelationID = &createdJob.CorrelationID
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.DescribeJobForHybridReplicationWorkflow, &replicationResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetNodeForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if createdJob.RequestID != "" {
		replicationResult.RequestID = &createdJob.RequestID
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateNodesForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateLocalHybridReplicationRow, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateVolumeReplicationForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	var establishPeeringJob datamodel.Job

	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateJobForHybridReplication, &replicationResult, string(datamodel.JobTypeHybridReplicationEstablishPeering)).Get(ctx, &establishPeeringJob)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             workflowengine.CustomerTaskQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowID:            establishPeeringJob.WorkflowID,
	})

	childWorkflowFuture := workflow.ExecuteChildWorkflow(
		ctx,
		EstablishPeeringWorkflow,
		replicationResult,
		dbVolume,
	)
	var childWE workflow.Execution
	err = childWorkflowFuture.GetChildWorkflowExecution().Get(ctx, &childWE)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	return nil, nil
}

type createEstablishPeeringWorkflow struct {
	baseHybridReplicationWorkflow
}

// EstablishPeeringWorkflow creates a establish peering workflow
func EstablishPeeringWorkflow(ctx workflow.Context, replicationResult replication.CreateHybridReplicationResult, volume *datamodel.Volume) error {
	createEstablishPeeringWF := new(createEstablishPeeringWorkflow)

	err := createEstablishPeeringWF.Setup(ctx, nil)
	if err != nil {
		return err
	}
	createEstablishPeeringWF.Status = workflows.WorkflowStatusRunning
	err = createEstablishPeeringWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		createEstablishPeeringWF.Status = workflows.WorkflowStatusFailed
		err = createEstablishPeeringWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return err
	}
	_, workflowErr := createEstablishPeeringWF.Run(ctx, replicationResult, volume)
	if workflowErr != nil {
		createEstablishPeeringWF.Status = workflows.WorkflowStatusFailed
		err2 := createEstablishPeeringWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), workflowErr)
		if err2 != nil {
			workflow.GetLogger(ctx).Error("Failed to update job status", "error", err2)
		}
		return workflowErr
	}
	createEstablishPeeringWF.Status = workflows.WorkflowStatusCompleted
	err2 := createEstablishPeeringWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		workflow.GetLogger(ctx).Error("Failed to update job status", "error", err2)
		return err2
	}
	return nil
}

func (wf *createEstablishPeeringWorkflow) Setup(ctx workflow.Context, input interface{}) error {
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

func (wf *createEstablishPeeringWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	replicationResult := args[0].(replication.CreateHybridReplicationResult)
	dbVolume := args[1].(*datamodel.Volume)
	replicationActivity := &replicationActivities.HybridReplicationActivity{}
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
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rollbackManager := common.NewRollbackManager()
	defer func() {
		if err != nil {
			updateStateDetailsAndCode(&replicationResult, workflows.ConvertToVSAError(err))
			err2 := workflow.ExecuteActivity(ctx, replicationActivity.UpdateClusterPeerDetailsOnErrorActivity, &replicationResult).Get(ctx, nil)
			if err2 != nil {
				log.Error("Failed to update cache parameters in DB", "error", err2)
			}
			err3 := workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationRowDetailsOnErrorActivity, &replicationResult).Get(ctx, nil)
			if err3 != nil {
				log.Error("Failed to update cache parameters in DB", "error", err3)
			}
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	if replicationResult.DstBasePath == nil {
		err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}
	if replicationResult.DstJwtToken == nil {
		err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstSignedTokenForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if replicationResult.NodeProvider == nil {
		err = workflow.ExecuteActivity(ctx, replicationActivity.GetNodeForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		err = workflow.ExecuteActivity(ctx, replicationActivity.CreateNodesForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetOrCreateClusterPeerForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateClusterPeeringInReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetOrCreateClusterPeerInOntapForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	var internalEstablishJob datamodel.Job

	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateJobForHybridReplication, &replicationResult, string(datamodel.JobTypeHybridReplicationInternalEstablish)).Get(ctx, &internalEstablishJob)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             workflowengine.CustomerTaskQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowRunTimeout:    *workflowengine.GetRevertVolumeWorkflowTimeout(),
		ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowID:            internalEstablishJob.WorkflowID,
	})
	childWorkflowFuture1 := workflow.ExecuteChildWorkflow(
		ctx,
		InternalEstablishWorkflow,
		replicationResult,
		dbVolume,
	)
	var childWE1 workflow.Execution
	err = childWorkflowFuture1.GetChildWorkflowExecution().Get(ctx, &childWE1)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	return nil, nil
}

type createInternalEstablishWorkflow struct {
	baseHybridReplicationWorkflow
}

func InternalEstablishWorkflow(ctx workflow.Context, replicationResult replication.CreateHybridReplicationResult, volume *datamodel.Volume) error {
	createInternalEstablishWF := new(createInternalEstablishWorkflow)
	err := createInternalEstablishWF.Setup(ctx, nil)
	if err != nil {
		return err
	}
	createInternalEstablishWF.Status = workflows.WorkflowStatusRunning
	err = createInternalEstablishWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		createInternalEstablishWF.Status = workflows.WorkflowStatusFailed
		err = createInternalEstablishWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return err
	}
	_, workflowErr := createInternalEstablishWF.Run(ctx, replicationResult, volume)
	if workflowErr != nil {
		createInternalEstablishWF.Status = workflows.WorkflowStatusFailed
		err2 := createInternalEstablishWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), workflowErr)
		if err2 != nil {
			workflow.GetLogger(ctx).Error("Failed to update job status", "error", err2)
		}
		return workflowErr
	}
	createInternalEstablishWF.Status = workflows.WorkflowStatusCompleted
	err2 := createInternalEstablishWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		workflow.GetLogger(ctx).Error("Failed to update job status", "error", err2)
		return err2
	}
	return nil
}

func (wf *createInternalEstablishWorkflow) Setup(ctx workflow.Context, input interface{}) error {
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

func (wf *createInternalEstablishWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	replicationResult := args[0].(replication.CreateHybridReplicationResult)
	replicationActivity := &replicationActivities.HybridReplicationActivity{}
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
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(ReplicationJobsRetryMaxAttempts)

	rollbackManager := common.NewRollbackManager()
	defer func() {
		if err != nil {
			updateStateDetailsAndCode(&replicationResult, workflows.ConvertToVSAError(err))
			err4 := workflow.ExecuteActivity(ctx, replicationActivity.UpdateSVMPeerOnErrorActivity, &replicationResult).Get(ctx, nil)
			if err4 != nil {
				log.Error("Failed to update cache parameters in DB", "error", err4)
			}
			err2 := workflow.ExecuteActivity(ctx, replicationActivity.UpdateClusterPeerDetailsOnErrorActivity, &replicationResult).Get(ctx, nil)
			if err2 != nil {
				log.Error("Failed to update cache parameters in DB", "error", err2)
			}

			err3 := workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationRowDetailsOnErrorActivity, &replicationResult).Get(ctx, nil)
			if err3 != nil {
				log.Error("Failed to update cache parameters in DB", "error", err3)
			}
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()
	err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateReplicationStateForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	waitCtx := getWaitContext(ctx, replicationResult.ClusterPeeringRow)
	err = workflow.ExecuteActivity(waitCtx, replicationActivity.WaitForClusterPeerActivityForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		if temporal.IsTimeoutError(err) {
			err = vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrClusterPeerTimeout, err))
		}
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.SetClusterPeeringStatusToPeeredForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateReplicationStateForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.SetVolumeReplicationPeeringStatusToPendingSVMPeering, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateSVMPeerInOntapForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.SetVolumeReplicationSVMPeeringDetails, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(waitCtx, replicationActivity.WaitForSVMPeerForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		if temporal.IsTimeoutError(err) {
			err = vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrSVMPeerTimeout, err))
		}
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.SetSVMPeeringToPeered, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetReplicationForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.CleanupReplicationIfNeeded, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.CreateHybridVolumeReplicationInternal, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateHybridVolumeReplicationDetailsAndSetPeeringStatusToPeered, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.HydrateReplicationStateForHybridReplication, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.MountReplicationAfterHybridReplicationCreate, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	return nil, workflows.ConvertToVSAError(err)
}

func updateStateDetailsAndCode(result *replication.CreateHybridReplicationResult, err error) {
	var vsaErr *vsaerrors.CustomError
	if errors.As(err, &vsaErr) {
		switch vsaErr.TrackingID {
		case vsaerrors.ErrClusterPeerNotAvailable:
			result.DbVolReplication.HybridReplicationAttributes.StateDetailsCode = models.ClusterPeeringExpiredCode
			result.DbVolReplication.HybridReplicationAttributes.StatusDetails = datamodel.ClusterPeeringExpired
		case vsaerrors.ErrClusterPeerTimeout:
			result.DbVolReplication.HybridReplicationAttributes.StateDetailsCode = models.ClusterPeeringExpiredCode
			result.DbVolReplication.HybridReplicationAttributes.StatusDetails = datamodel.ClusterPeeringExpired
		case vsaerrors.ErrorCreateClusterPeerCVISourceClusterUnreachable:
			result.DbVolReplication.HybridReplicationAttributes.StateDetailsCode = models.SourceClusterUnreachableCode
			result.DbVolReplication.HybridReplicationAttributes.StatusDetails = datamodel.ClusterPeeringSourceUnreachable
		case vsaerrors.ErrSVMPeerTimeout:
			result.DbVolReplication.HybridReplicationAttributes.StateDetailsCode = models.SVMPeeringExpiredCode
			result.DbVolReplication.HybridReplicationAttributes.StatusDetails = datamodel.SVMPeeringExpired
		case vsaerrors.ErrSVMPeerNotAvailable:
			result.DbVolReplication.HybridReplicationAttributes.StateDetailsCode = models.ErrorDuringSVMPeeringCode
			result.DbVolReplication.HybridReplicationAttributes.StatusDetails = datamodel.ErrorDuringSVMPeering
		case vsaerrors.ErrClusterPeerError:
			result.DbVolReplication.HybridReplicationAttributes.StateDetailsCode = models.ErrorDuringClusterPeerCode
			result.DbVolReplication.HybridReplicationAttributes.StatusDetails = datamodel.ErrorDuringClusterPeer
		default:
			result.DbVolReplication.HybridReplicationAttributes.StateDetailsCode = models.DefaultCode
			result.DbVolReplication.HybridReplicationAttributes.StatusDetails = getOriginalErrorCause(err)
		}
	}
}

// getOriginalErrorCause extracts the root cause of an error by unwrapping wrapped errors
func getOriginalErrorCause(err error) string {
	if err == nil {
		return ""
	}

	// Keep unwrapping until we find the root cause
	for {
		// Check if this error has an Unwrap method (for wrapped errors)
		if unwrapped := errors.Unwrap(err); unwrapped != nil {
			err = unwrapped
			continue
		}

		// Check if this error implements the Cause method (for pkg/errors style wrapping)
		if causer, ok := err.(interface{ Cause() error }); ok {
			if cause := causer.Cause(); cause != nil {
				err = cause
				continue
			}
		}

		// No more unwrapping possible, return the current error message
		break
	}

	return err.Error()
}

func getWaitContext(ctx workflow.Context, clusterPeeringRow *datamodel.ClusterPeerings) workflow.Context {
	expiry := clusterPeerTimeout
	if clusterPeeringRow.ClusterPeeringAttributes.ExpiryTime != nil && clusterPeeringRow.ClusterPeeringAttributes.ExpiryTime.After(time.Now()) {
		expiry = time.Until(*clusterPeeringRow.ClusterPeeringAttributes.ExpiryTime)
	}
	interval := clusterPeerInterval
	activityOptions := workflow.ActivityOptions{
		// Total timeout for the activity
		ScheduleToCloseTimeout: expiry,
		// Timeout for a single activity execution
		StartToCloseTimeout: interval,
		// With an interval of 15s it will take ~10 mins to reach the maximum interval of 45s
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    interval,
			BackoffCoefficient: 1.05,
			MaximumInterval:    interval * 3,
		},
	}
	return workflow.WithActivityOptions(ctx, activityOptions)
}
