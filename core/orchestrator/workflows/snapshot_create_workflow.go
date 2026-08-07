package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	snapshotStartToCloseTimeoutSec = env.GetUint64("SNAPSHOT_START_TO_CLOSE_TIMEOUT_SEC", 300)
	snapshotHeartbeatTimeoutSec    = env.GetUint64("SNAPSHOT_HEARTBEAT_TIMEOUT_SEC", 150)
)

const (
	CancelSnapshotSignalName = "cancel-snapshot-creation"
)

type snapshotCreateWorkflow struct {
	BaseWorkflow
	SE database.Storage
}

var _ WorkflowInterface = &snapshotCreateWorkflow{}

// CreateSnapshotWorkflow processes snapshot creation requests.
func CreateSnapshotWorkflow(ctx workflow.Context, params *common.CreateSnapshotParams, snapshot *datamodel.Snapshot) (gcpgenserver.V1betaCreateSnapshotRes, error) {
	logger := util.GetLogger(ctx)
	snapshotWf := new(snapshotCreateWorkflow)
	err := snapshotWf.Setup(ctx, params)
	if err != nil {
		logger.Infof("Snapshot workflow setup executed with error: %v", err)
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
		logger.Infof("Snapshot workflow run executed with error: %v", customErr)
		snapshotWf.Status = WorkflowStatusFailed
		jobUpdateErr := snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if jobUpdateErr != nil {
			logger.Errorf("Failed to update job status to ERROR for CreateSnapshotWorkflow: %v", jobUpdateErr)
			return nil, jobUpdateErr
		}
		return nil, customErr
	}

	snapshotWf.Status = WorkflowStatusCompleted
	err = snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		logger.Errorf("Failed to update job status to Done for CreateSnapshotWorkflow: %v", err)
	}
	logger.Debug("Create Snapshot workflow completed successfully")
	return nil, nil
}

// Setup initializes the workflow with the necessary parameters and registers a status query handler.
func (wf *snapshotCreateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createSnapshotParams := input.(*common.CreateSnapshotParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createSnapshotParams.AccountName
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

// Run executes the snapshot creation workflow steps.
//
// The rollback manager is pre-loaded with UpdateSnapshotDetails(error) so any failure
// automatically marks the DB record as failed. After CreateSnapshotInONTAP succeeds,
// DeleteSnapshotInONTAP is also registered (LIFO: ONTAP delete runs first, then DB error write).
func (wf *snapshotCreateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	snapshot := args[0].(*datamodel.Snapshot)
	logger := util.GetLogger(ctx)
	snapshotActivity := &activities.SnapshotCreateActivity{}
	deleteActivity := &activities.SnapshotDeleteActivity{}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
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
	ctx = workflow.WithActivityOptions(ctx, ao)

	dbSnapshot := snapshot
	snapshotDescription := dbSnapshot.Description
	dbSnapshot.Description = wf.ID // Job UUID sent as the ONTAP snapshot comment for end-to-end traceability.

	logger.Infof("Starting the snapshot creation workflow for snapshot: %s", dbSnapshot.Name)

	cancellationHandler := common.NewWorkflowCancellationHandler(ctx, CancelSnapshotSignalName, dbSnapshot.UUID, "snapshot")

	rollbackManager := common.NewRollbackManager()
	// Pre-register error state write. On any failure the rollback manager marks the snapshot
	// as error/soft-deleted in the DB. Description is restored in the defer before serialization.
	rollbackManager.AddActivity(snapshotActivity.UpdateSnapshotDetails, dbSnapshot, nil)

	defer func() {
		if err != nil {
			dbSnapshot.Description = snapshotDescription
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		err = cancelErr
		return nil, ConvertToVSAError(err)
	}

	var dbNodes []*datamodel.Node
	if err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &dbSnapshot.Volume.PoolID).Get(ctx, &dbNodes); err != nil {
		return nil, ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   dbSnapshot.Volume.Pool.DeploymentName,
		OntapCredentials: dbSnapshot.Volume.Pool.PoolCredentials,
	})

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		err = cancelErr
		return nil, ConvertToVSAError(err)
	}

	var snapshotCreateResponse *vsa.SnapshotProviderResponse
	if err = workflow.ExecuteActivity(ctx, snapshotActivity.CreateSnapshotInONTAP, &dbSnapshot, &node).Get(ctx, &snapshotCreateResponse); err != nil {
		logger.Errorf("Failed to create snapshot in ONTAP: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// ONTAP side effect exists — register ONTAP deletion rollback (LIFO: runs before DB error write).
	dbSnapshot.Description = snapshotDescription
	if snapshotCreateResponse != nil && dbSnapshot.SnapshotAttributes != nil {
		dbSnapshot.SnapshotAttributes.ExternalUUID = snapshotCreateResponse.ExternalUUID
	}
	rollbackManager.AddActivity(deleteActivity.DeleteSnapshotInONTAP, dbSnapshot, &node)

	if err = workflow.ExecuteActivity(ctx, snapshotActivity.UpdateSnapshotDetails, dbSnapshot, snapshotCreateResponse).Get(ctx, nil); err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}
