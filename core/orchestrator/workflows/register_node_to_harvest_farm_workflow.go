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

	// Create a separate context with shorter heartbeat timeout for DB operations
	dbHbCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)

	defer func() {
		if err != nil {
			_ = workflow.ExecuteActivity(ctx, registerActivity.AlertHarvestRegisterFailure, err.Error()).Get(ctx, nil)
		}
	}()

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

	// 2. Validate and create kubernetes lease if required.
	var updatedNodeMappings []*datamodel.NodeNodeGroupMap
	err = workflow.ExecuteActivity(ctx, registerActivity.ValidateAndCreateKubernetesLease, nodeMappings).Get(dbHbCtx, &updatedNodeMappings)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Render and upload a Harvest template for each node mapping (sequentially)
	uploadInput := activities.UploadHarvestTemplateInput{
		NodeMappings: updatedNodeMappings,
		UploadURL:    registerHarvestUploadDestination(),
		PoolUUID:     input.PoolUUID,
		AccountID:    input.AccountID,
	}

	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	err = workflow.ExecuteActivity(ctx, uploadActivity.UploadHarvestTemplate, uploadInput).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	return updatedNodeMappings, nil
}
