package replicationWorkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type internalSnapshotDeleteWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

// Enforcing the WorkflowInterface on snapshotDeleteWorkflow
var _ workflows.WorkflowInterface = &internalSnapshotDeleteWorkflow{}

// DeleteInternalSnapshotWorkflow Delete Snapshot Workflow process snapshot related requests from a customer.
func DeleteInternalSnapshotWorkflow(ctx workflow.Context, params *common.SnapshotsInternalDeleteParams) (gcpgenserver.V1betaDescribeSnapshotRes, error) {
	logger := workflow.GetLogger(ctx)
	snapshotWf := new(internalSnapshotDeleteWorkflow)
	err := snapshotWf.Setup(ctx, params)
	if err != nil {
		logger.Error("Snapshot Delete workflow setup executed with error", "error", err)
		return nil, err
	}
	snapshotWf.Status = workflows.WorkflowStatusRunning
	err = snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		logger.Error("Update job status for snapshot executed with error", "error", err)
		snapshotWf.Status = workflows.WorkflowStatusFailed
		err = snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return nil, err
	}
	_, customErr := snapshotWf.Run(ctx, params)
	if customErr != nil {
		logger.Error("Snapshot delete workflow run executed with error", "error", customErr)
		snapshotWf.Status = workflows.WorkflowStatusFailed
		err = snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, err
	}
	logger.Info("Snapshot workflow completed successfully")
	snapshotWf.Status = workflows.WorkflowStatusCompleted
	err = snapshotWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

func (wf *internalSnapshotDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteSnapshotParams := input.(*common.SnapshotsInternalDeleteParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteSnapshotParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *internalSnapshotDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	params := args[0].(*common.SnapshotsInternalDeleteParams)
	replicationActivity := &replicationActivities.InternalSnapshotsDeleteActivity{}
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

	logger.Info("Starting the snapshot deletion workflow for snapshots")
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetNodeFromDB, &params).Get(ctx, &params)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            params.Nodes,
		DeploymentName:   params.Volume.Pool.DeploymentName,
		OntapCredentials: params.Volume.Pool.PoolCredentials,
	})

	err = workflow.ExecuteActivity(ctx, replicationActivity.ListSnapshotInONTAP, &params, &node).Get(ctx, &params)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteSnapshotsInONTAP, &params, &node).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.ListSnapshotFromDB, &params).Get(ctx, &params)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.DehydrateSnapshots, &params).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateSnapshotRecordInDB, &params).Get(ctx, nil)

	return nil, workflows.ConvertToVSAError(err)
}
