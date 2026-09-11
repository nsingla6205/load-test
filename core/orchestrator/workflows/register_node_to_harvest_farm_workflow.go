package workflows

import (
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	harvestRegisterRetryStateChangeID = "harvestRegisterRetryState"
	harvestRegisterRetryStateVersion  = workflow.Version(1)
)

// RegisterNodeToHarvestFarmWorkflowInput holds input parameters for the workflow
type RegisterNodeToHarvestFarmWorkflowInput struct {
	PoolID            int64
	MaxNodesPerGroup  int
	CustomerProjectID string
	TenantProjectID   string
	PoolUUID          string
	AccountID         int64
	DeploymentName    string
	PoolName          string
	IsRegionalHA      bool
}

type registerNodeToHarvestFarmWorkflow struct {
	BaseWorkflow
	SE database.Storage
}

var (
	harvestHost             = env.GetString("HARVEST_HOST", "harvest-farm-service.vcp.svc.cluster.local:3000")
	harvestRestProtocol     = env.GetString("HARVEST_REST_PROTOCOL", "http")
	harvestPodUploadURL     = fmt.Sprintf("%s://%s/config/upload", harvestRestProtocol, harvestHost)
	harvestPollerBucketName = strings.TrimSpace(env.GetString("POLLER_REBALANCE_BUCKET_NAME", ""))
)

func registerHarvestUploadDestination() string {
	if harvestPollerBucketName != "" {
		return "gs://" + harvestPollerBucketName
	}
	return harvestPodUploadURL
}

// Enforcing the WorkflowInterface on registerNodeToHarvestFarmWorkflow
var _ WorkflowInterface = &registerNodeToHarvestFarmWorkflow{}

// RegisterNodeToHarvestFarmWorkflow is a Temporal workflow that registers a node to the Harvest farm
func RegisterNodeToHarvestFarmWorkflow(ctx workflow.Context, input RegisterNodeToHarvestFarmWorkflowInput) error {
	wf := new(registerNodeToHarvestFarmWorkflow)
	err := wf.Setup(ctx, input)
	if err != nil {
		return err
	}
	wf.Status = WorkflowStatusRunning
	// Optionally update the job status here if needed
	_, customErr := wf.Run(ctx, input)
	if customErr != nil {
		wf.Status = WorkflowStatusFailed
		return customErr
	}
	wf.Status = WorkflowStatusCompleted
	return nil
}

func (wf *registerNodeToHarvestFarmWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	workflowInput := input.(RegisterNodeToHarvestFarmWorkflowInput)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = workflowInput.CustomerProjectID // Set if you have a customer/account field
	wf.Status = WorkflowStatusCreated
	return nil
}

func (wf *registerNodeToHarvestFarmWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	input := args[0].(RegisterNodeToHarvestFarmWorkflowInput)
	registerActivity := &activities.RegisterNodeToHarvestFarmActivity{}
	var err error
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	// Set activity options
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"not enough nodes found for pool",
				"node group assignment returned insufficient mappings for pool",
				"node1 and node2 must be different nodes",
				"failed to fetch node group details from nodeGroup Map table",
				"PanicError",
			},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	retryStateVersion := workflow.GetVersion(ctx, harvestRegisterRetryStateChangeID, workflow.DefaultVersion, harvestRegisterRetryStateVersion)

	// Create a separate context with shorter heartbeat timeout for DB operations
	dbHbCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)

	defer func() {
		if err != nil {
			_ = workflow.ExecuteActivity(ctx, registerActivity.AlertHarvestRegisterFailure, err.Error()).Get(ctx, nil)
		}
	}()

	// mappings tracks node_node_group_maps rows once they exist (after step 1). The deferred mark
	// activity persists harvest_retry_state.create on exit when version >= 1.
	var mappings []*datamodel.NodeNodeGroupMap
	var uploadedNodeIDs map[int64]struct{}
	if retryStateVersion >= harvestRegisterRetryStateVersion {
		defer func() {
			cleanupMappings := mappings
			if len(cleanupMappings) == 0 && err != nil {
				disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
				cleanupDbCtx := workflow.WithHeartbeatTimeout(disconnectedCtx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)
				var loadedMappings []*datamodel.NodeNodeGroupMap
				if loadErr := workflow.ExecuteActivity(cleanupDbCtx, registerActivity.LoadActiveHarvestMappingsForPool, input.PoolID).Get(cleanupDbCtx, &loadedMappings); loadErr != nil {
					workflow.GetLogger(cleanupDbCtx).Warn("failed to load harvest mappings for create retry state", "error", loadErr)
				} else {
					cleanupMappings = loadedMappings
				}
			}
			if len(cleanupMappings) == 0 {
				return
			}
			retryInput := activities.MarkCreateRetryStateInput{
				Mappings:        cleanupMappings,
				Success:         err == nil,
				UploadedNodeIDs: uploadedNodeIDs,
			}
			if err != nil {
				retryInput.Message = err.Error()
			}
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			cleanupDbCtx := workflow.WithHeartbeatTimeout(disconnectedCtx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)
			if markErr := workflow.ExecuteActivity(cleanupDbCtx, registerActivity.MarkCreateRetryState, retryInput).Get(cleanupDbCtx, nil); markErr != nil {
				workflow.GetLogger(cleanupDbCtx).Warn("failed to persist harvest create retry state", "error", markErr)
			}
		}()
	}

	// 1. Register nodes to Harvest farm
	var nodeMappings []*datamodel.NodeNodeGroupMap
	err = workflow.ExecuteActivity(dbHbCtx, registerActivity.RegisterNodeToHarvestFarm,
		activities.RegisterNodeToHarvestFarmInput{
			PoolID:            input.PoolID,
			MaxNodesPerGroup:  input.MaxNodesPerGroup,
			CustomerProjectID: input.CustomerProjectID,
			TenantProjectID:   input.TenantProjectID,
			DeploymentName:    input.DeploymentName,
			PoolName:          input.PoolName,
			IsRegionalHA:      input.IsRegionalHA,
		}).Get(dbHbCtx, &nodeMappings)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	mappings = nodeMappings

	// 2. Validate and create kubernetes lease if required.
	var updatedNodeMappings []*datamodel.NodeNodeGroupMap
	err = workflow.ExecuteActivity(ctx, registerActivity.ValidateAndCreateKubernetesLease, nodeMappings).Get(dbHbCtx, &updatedNodeMappings)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	mappings = updatedNodeMappings

	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	baseUploadInput := activities.UploadHarvestTemplateInput{
		UploadURL: registerHarvestUploadDestination(),
		PoolUUID:  input.PoolUUID,
		AccountID: input.AccountID,
	}

	// DefaultVersion keeps the historical single batch upload so in-flight workflows replay
	// deterministically; version 1 splits uploads per node to track partial success in retry state.
	if retryStateVersion >= harvestRegisterRetryStateVersion {
		uploadedNodeIDs = make(map[int64]struct{})
		for _, mapping := range updatedNodeMappings {
			singleUploadInput := baseUploadInput
			singleUploadInput.NodeMappings = []*datamodel.NodeNodeGroupMap{mapping}

			err = workflow.ExecuteActivity(ctx, uploadActivity.UploadHarvestTemplate, singleUploadInput).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			uploadedNodeIDs[mapping.NodeID] = struct{}{}
		}
	} else {
		batchUploadInput := baseUploadInput
		batchUploadInput.NodeMappings = updatedNodeMappings
		err = workflow.ExecuteActivity(ctx, uploadActivity.UploadHarvestTemplate, batchUploadInput).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	return updatedNodeMappings, nil
}
