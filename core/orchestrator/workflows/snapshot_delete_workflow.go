package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type snapshotDeleteWorkflow struct {
	BaseWorkflow
}

// Enforcing the WorkflowInterface on snapshotDeleteWorkflow
var _ WorkflowInterface = &snapshotDeleteWorkflow{}

// DeleteSnapshotWorkflow Delete Snapshot Workflow process snapshot related requests from a customer.
func DeleteSnapshotWorkflow(ctx workflow.Context, params *common.DeleteSnapshotParams, snapshot *datamodel.Snapshot) (gcpgenserver.V1betaDescribeSnapshotRes, error) {
	logger := util.GetLogger(ctx)
	snapshotWf := new(snapshotDeleteWorkflow)
	err := snapshotWf.Setup(ctx, params)
	if err != nil {
		logger.Infof("Snapshot Delete workflow setup executed with error: %v", err)
		return nil, err
	}

	if err = snapshotWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, err
	}

	snapshotWf.Status = WorkflowStatusRunning
	err = snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		logger.Infof("Update job status for snapshot executed with error: %v", err)
		return nil, err
	}
	_, customErr := snapshotWf.Run(ctx, snapshot)
	if customErr != nil {
		logger.Infof("Snapshot delete workflow run executed with error: %v", customErr)
		snapshotWf.Status = WorkflowStatusFailed
		jobUpdateErr := snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if jobUpdateErr != nil {
			logger.Errorf("Failed to update job status to Done with error for DeleteSnapshotWorkflow: %v", jobUpdateErr)
			return nil, jobUpdateErr
		}
		return nil, customErr
	}
	snapshotWf.Status = WorkflowStatusCompleted
	err = snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		logger.Errorf("Failed to update job status to Done for DeleteSnapshotWorkflow: %v", err)
	}
	logger.Debug("Delete Snapshot workflow completed successfully")
	return nil, nil
}

func (wf *snapshotDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteSnapshotParams := input.(*common.DeleteSnapshotParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteSnapshotParams.AccountName
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *snapshotDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := util.GetLogger(ctx)
	snapshot := args[0].(*datamodel.Snapshot)
	deleteActivity := &activities.SnapshotDeleteActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(snapshotStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(snapshotHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	dbSnapshot := snapshot
	logger.Infof("Starting the snapshot deletion workflow for snapshot: %s", dbSnapshot.Name)

	// Cancellation handling uses the original snapshot state to decide whether an in-progress
	// create workflow needs to be cancelled before we proceed with deletion.
	poolActivity := &activities.PoolActivity{}
	cancellationActivity := &activities.CancellationActivity{}
	commonActivity := &activities.CommonActivities{}
	ackTimeout, forceTimeout := common.GetCancellationTimeouts("SNAPSHOT")
	if cancelErr := common.HandleCancellationForCreatingResource(ctx, wf.Logger,
		common.HandleCancellationForCreatingResourceParams{
			ResourceUUID:               dbSnapshot.UUID,
			ResourceState:              dbSnapshot.State,
			CreateJobType:              datamodel.JobTypeCreateSnapshot,
			SignalName:                 CancelSnapshotSignalName,
			CancellationAckTimeout:     ackTimeout,
			ForceTerminationAckTimeout: forceTimeout,
		},
		poolActivity.GetCreateJobByResourceUUID,
		cancellationActivity,
		commonActivity,
	); cancelErr != nil {
		wf.Logger.Warnf("Error handling cancellation: %v, proceeding with deletion", cancelErr)
	}

	// Default compensation: restore snapshot to READY (ONTAP snapshot still exists).
	// Switched to ERROR after DeleteSnapshotInONTAP succeeds — see below.
	dbSnapshot.State = datamodel.LifeCycleStateREADY
	dbSnapshot.StateDetails = datamodel.LifeCycleStateAvailableDetails

	rollbackManager := common.NewRollbackManager()
	rollbackManager.AddActivity(deleteActivity.UpdateDeleteSnapshotDetails, dbSnapshot)

	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	hasExternalUUID := dbSnapshot.GetExternalUUID() != ""
	var dbNodes []*datamodel.Node
	if err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &dbSnapshot.Volume.PoolID).Get(ctx, &dbNodes); err != nil {
		return nil, ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   dbSnapshot.Volume.Pool.DeploymentName,
		OntapCredentials: dbSnapshot.Volume.Pool.PoolCredentials,
	})

	if hasExternalUUID {
		if err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteSnapshotInONTAP, &dbSnapshot, &node).Get(ctx, nil); err != nil {
			return nil, ConvertToVSAError(err)
		}
		// ONTAP snapshot is now gone. Restoring to READY would create a phantom resource in the
		// DB pointing to a non-existent ONTAP snapshot. Switch compensation to ERROR so the DB
		// reflects the inconsistency and operators know reconciliation is required.
		dbSnapshot.State = datamodel.LifeCycleStateError
		dbSnapshot.StateDetails = datamodel.LifeCycleStateDeletionErrorDetails
	}

	if err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteSnapshot, &dbSnapshot).Get(ctx, nil); err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}
