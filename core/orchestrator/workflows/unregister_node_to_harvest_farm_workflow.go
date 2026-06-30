package workflows

import (
	"errors"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type unRegisterNodeFromHarvestFarmParams struct {
	PoolID            int64
	CustomerProjectID string
	TenantProjectID   string
}

type unRegisterNodeFromHarvestFarmWorkflow struct {
	BaseWorkflow
	SE database.Storage
}

// Enforcing the WorkflowInterface on unRegisterNodeToHarvestFarmWorkflow
var _ WorkflowInterface = &unRegisterNodeFromHarvestFarmWorkflow{}

// UnRegisterNodeFromHarvestFarmWorkflow is a Temporal workflow that un-registers a node to the Harvest farm
func UnRegisterNodeFromHarvestFarmWorkflow(ctx workflow.Context, params *unRegisterNodeFromHarvestFarmParams) error {
	wf := new(unRegisterNodeFromHarvestFarmWorkflow)
	err := wf.Setup(ctx, params)
	if err != nil {
		return err
	}
	wf.Status = WorkflowStatusRunning
	_, err = wf.Run(ctx, params)
	if e, ok := err.(*vsaerrors.CustomError); ok && e != nil {
		wf.Status = WorkflowStatusFailed
		return err
	}
	wf.Status = WorkflowStatusCompleted
	return nil
}

func (wf *unRegisterNodeFromHarvestFarmWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	workflowInput, ok := input.(*unRegisterNodeFromHarvestFarmParams)
	if !ok {
		return errors.New("unable to type cast input as unRegisterNodeFromHarvestFarmParams")
	}
	wf.CustomerID = workflowInput.CustomerProjectID
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger
	return nil
}

func (wf *unRegisterNodeFromHarvestFarmWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	input := args[0].(*unRegisterNodeFromHarvestFarmParams)
	unRegisterActivity := &activities.UnRegisterNodeFromHarvestActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	// Set activity options with heartbeat timeout
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: activities.UnRegisterNodeFromHarvestFarmNonRetryableErrors,
		},
	}
	activityParams := &activities.UnRegisterNodeFromHarvestActivityParams{
		PoolID:            input.PoolID,
		CustomerProjectID: input.CustomerProjectID,
		TenantProjectID:   input.TenantProjectID,
	}

	defer func() {
		if err != nil {
			_ = workflow.ExecuteActivity(ctx, unRegisterActivity.AlertHarvestUnRegisterFailure, err.Error()).Get(ctx, nil)
		}
	}()

	ctx = workflow.WithActivityOptions(ctx, ao)

	// Create a separate context with shorter heartbeat timeout for DB operations
	dbHbCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)

	var dbNodes []*datamodel.Node
	// 1. Validate and Get Nodes from DB which are in Deleted state (DB operation - uses dbHbCtx)
	err = workflow.ExecuteActivity(dbHbCtx, unRegisterActivity.ValidateAndGetNodes, activityParams).Get(dbHbCtx, &dbNodes)
	if err != nil {
		var appErr *temporal.ApplicationError
		if errors.As(err, &appErr) && appErr.NonRetryable() && appErr.Type() == activities.UnRegisterNodesInfoNotAvailable {
			wf.Logger.Infof("no nodes available to perform unregister from harvest farm")
			return nil, nil
		}
		return nil, ConvertToVSAError(err)
	}

	// If No Nodes exist, i.e., pollers are already deleted from harvest farm hence mark the wf as success
	if len(dbNodes) == 0 {
		return nil, nil
	}
	// Update Nodes info to activity params
	activityParams.Nodes = dbNodes

	var nodeGroupMap []*datamodel.NodeNodeGroupMap
	// 2. Get node and harvest mapping details which are not in soft deleted state (DB operation - uses dbHbCtx)
	err = workflow.ExecuteActivity(dbHbCtx, unRegisterActivity.GetNodeGroupMapping, activityParams).Get(dbHbCtx, &nodeGroupMap)
	if err != nil {
		var appErr *temporal.ApplicationError
		if errors.As(err, &appErr) && appErr.NonRetryable() && appErr.Type() == activities.UnRegisterNodeGroupMapNotAvailable {
			wf.Logger.Infof("no nodeGroupMap available to perform unregister from harvest farm")
			return nil, nil
		}
		return nil, ConvertToVSAError(err)
	}

	// If No nodeGroupMap exists, i.e., pollers are already deleted from harvest farm hence mark the wf as success
	if len(nodeGroupMap) == 0 {
		return nil, nil
	}
	// Update NodeLeaseMap info to activity params
	activityParams.NodeGroupsMap = nodeGroupMap

	// 3. Delete NodeGroupMapping from DB i.e., soft delete (DB operation - uses dbHbCtx)
	err = workflow.ExecuteActivity(dbHbCtx, unRegisterActivity.DeleteNodeGroupMapping, activityParams).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// 4. Delete node pollers from harvest farm (GCS bucket or harvest pod PVC)
	err = workflow.ExecuteActivity(ctx, unRegisterActivity.DeletePollersFromHarvestFarm, activityParams).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// 5. Validate and delete kubernetes lease information from DB (DB operation - uses dbHbCtx)
	err = workflow.ExecuteActivity(dbHbCtx, unRegisterActivity.ValidateAndReleaseLease, activityParams).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}
