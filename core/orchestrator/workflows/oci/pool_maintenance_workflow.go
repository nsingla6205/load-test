package oci

import (
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type ociExecutePoolMaintenanceWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &ociExecutePoolMaintenanceWorkflow{}

func OCIExecutePoolMaintenanceWorkflow(ctx workflow.Context, params *common.PoolMaintenanceParams, pool *datamodel.Pool) error {
	start := workflow.Now(ctx)
	wf := new(ociExecutePoolMaintenanceWorkflow)
	log := util.GetLogger(ctx)
	if err := wf.Setup(ctx, params); err != nil {
		emitDuration(ctx, wfExecuteMaintenance, queueCustomer, start)
		return workflows.ConvertToVSAError(err)
	}

	wf.Status = workflows.WorkflowStatusRunning
	_, customErr := wf.Run(ctx, params, pool)
	if customErr != nil {
		log.Error("OCIExecutePoolMaintenanceWorkflow failed", "error", customErr)
		wf.Status = workflows.WorkflowStatusFailed
		emitDuration(ctx, wfExecuteMaintenance, queueCustomer, start)
		return workflows.ConvertToVSAError(customErr)
	}

	wf.Status = workflows.WorkflowStatusCompleted
	emitDuration(ctx, wfExecuteMaintenance, queueCustomer, start)
	return nil
}

func (wf *ociExecutePoolMaintenanceWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params, ok := input.(*common.PoolMaintenanceParams)
	if !ok || params == nil {
		return fmt.Errorf("OCIExecutePoolMaintenanceWorkflow.Setup: invalid params (expected *PoolMaintenanceParams)")
	}
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
	wf.Status = workflows.WorkflowStatusCreated

	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": wf.ID,
		"customerID": wf.CustomerID,
		"poolOCID":   params.PoolOCID,
	})
	wf.Logger = util.GetLogger(ctx)

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ociExecutePoolMaintenanceWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	if len(args) < 2 {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIExecutePoolMaintenanceWorkflow.Run: expected 2 args, got %d", len(args)))
	}
	params, ok := args[0].(*common.PoolMaintenanceParams)
	if !ok {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIExecutePoolMaintenanceWorkflow.Run: args[0] has unexpected type %T, want *common.PoolMaintenanceParams", args[0]))
	}
	if params == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCIExecutePoolMaintenanceWorkflow.Run: args[0] (*common.PoolMaintenanceParams) must not be nil")))
	}
	pool, ok := args[1].(*datamodel.Pool)
	if !ok {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIExecutePoolMaintenanceWorkflow.Run: args[1] has unexpected type %T, want *datamodel.Pool", args[1]))
	}
	if pool == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCIExecutePoolMaintenanceWorkflow.Run: args[1] (*datamodel.Pool) must not be nil")))
	}

	logger := wf.Logger
	if logger == nil {
		logger = util.GetLogger(ctx)
	}
	logger.Info("OCIExecutePoolMaintenanceWorkflow starting",
		"poolOCID", params.PoolOCID,
		"poolUUID", pool.UUID,
		"operationType", params.OperationType,
		"instanceCount", len(params.InstanceOCIDs),
	)

	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: workflows.RbacActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        workflows.RbacActivityInitialBackoff,
			BackoffCoefficient:     2.0,
			MaximumInterval:        workflows.RbacActivityMaxInterval,
			MaximumAttempts:        int32(workflows.RbacActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	})

	poolActivity := &activities.PoolActivity{}

	rollbackManager := common.NewRollbackManager()
	rollbackManager.AddActivity(poolActivity.ErroredPool, pool)
	var runErr error
	defer func() {
		if runErr != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackCtx := workflow.WithActivityOptions(disconnectedCtx, workflow.ActivityOptions{
				StartToCloseTimeout: workflows.RbacActivityStartToClose,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:        workflows.RbacActivityInitialBackoff,
					BackoffCoefficient:     2.0,
					MaximumInterval:        workflows.RbacActivityMaxInterval,
					MaximumAttempts:        int32(workflows.RbacActivityMaxAttempts),
					NonRetryableErrorTypes: []string{"PanicError"},
				},
			})
			rollbackManager.ExecuteRollback(rollbackCtx, runErr)
		}
	}()

	var currentVlmConfig *vlm.VLMConfig
	if runErr = workflow.ExecuteActivity(actCtx, poolActivity.ParseVlmConfig, pool).Get(actCtx, &currentVlmConfig); runErr != nil {
		logger.Error("Failed to parse stored VLM config for pool maintenance",
			"poolUUID", pool.UUID, "error", runErr)
		emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageParseVlmConfig, resultFailure)
		return nil, workflows.ConvertToVSAError(runErr)
	}
	emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageParseVlmConfig, resultSuccess)

	nodeTargets := buildMaintenanceTargets(currentVlmConfig, params.InstanceOCIDs)
	nodeMaintenanceRequested := len(nodeTargets) > 0
	mediatorRequested := requestIncludesMediator(currentVlmConfig, params.InstanceOCIDs)
	if !nodeMaintenanceRequested && !mediatorRequested {
		runErr = vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("OCIExecutePoolMaintenanceWorkflow: no maintenance targets resolved for pool %q from the requested instance OCIDs", pool.UUID))
		logger.Error("No maintenance targets resolved from requested instance OCIDs",
			"poolUUID", pool.UUID, "instanceOCIDs", params.InstanceOCIDs)
		emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageResolveTargets, resultFailure)
		return nil, workflows.ConvertToVSAError(runErr)
	}
	emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageResolveTargets, resultSuccess)

	ontapCreds := &vlm.OntapCredentials{}
	if runErr = workflow.ExecuteActivity(actCtx, poolActivity.GetOnTapCredentialsForOCI, pool).Get(actCtx, ontapCreds); runErr != nil {
		logger.Error("Failed to fetch ONTAP admin credentials for pool maintenance",
			"poolUUID", pool.UUID, "error", runErr)
		emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageOntapCredentials, resultFailure)
		return nil, workflows.ConvertToVSAError(runErr)
	}
	emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageOntapCredentials, resultSuccess)

	vsaClient := workflows.GetNewVSAClientWorkflowManager()

	// For non_shared_ha deployments the mediator maintenance/recovery must run
	// before instance maintenance, but only when the request actually targets the
	// mediator instance. non_shared_ha always has exactly one HA pair.
	if currentVlmConfig.Deployment.DeploymentType == vlm.DeploymentTypeNonSharedHA && mediatorRequested {
		mediatorReq := &vlm.MediatorMaintenanceRequest{
			VLMConfig:        *currentVlmConfig,
			OntapCredentials: *ontapCreds,
			HAPairIndices:    oneBasedMediatorHAPairIndices(currentVlmConfig, params.InstanceOCIDs),
		}
		logger.Info("Dispatching mediator maintenance before instance maintenance (non_shared_ha)",
			"poolUUID", pool.UUID,
			"haPairIndices", mediatorReq.HAPairIndices,
			"nodeTargetCount", len(nodeTargets),
		)

		var updatedVlmConfig *vlm.VLMConfig
		if params.OperationType == common.MaintenanceOperationRecreate {
			updatedVlmConfig, runErr = vsaClient.VSAMediatorITCWorkflow(ctx, mediatorReq)
		} else {
			updatedVlmConfig, runErr = vsaClient.VSAMediatorMaintenanceWorkflow(ctx, mediatorReq)
		}
		if runErr != nil {
			logger.Error("VLM-side mediator maintenance failed",
				"poolUUID", pool.UUID, "error", runErr)
			emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageMediatorMaintenance, resultFailure)
			return nil, workflows.ConvertToVSAError(runErr)
		}
		if updatedVlmConfig == nil {
			runErr = vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError,
				fmt.Errorf("OCIExecutePoolMaintenanceWorkflow: VLM returned nil config from mediator maintenance for pool %q", pool.UUID))
			logger.Error("VLM-side mediator maintenance returned nil config", "poolUUID", pool.UUID)
			emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageMediatorMaintenance, resultFailure)
			return nil, workflows.ConvertToVSAError(runErr)
		}
		currentVlmConfig = updatedVlmConfig
		if runErr = workflow.ExecuteActivity(actCtx, poolActivity.SavePoolVLMConfig, pool, *currentVlmConfig).Get(actCtx, nil); runErr != nil {
			logger.Error("Failed to persist VLM config after mediator maintenance",
				"poolUUID", pool.UUID, "error", runErr)
			emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageMediatorMaintenance, resultFailure)
			return nil, workflows.ConvertToVSAError(runErr)
		}
		if nodeMaintenanceRequested {
			nodeTargets = buildMaintenanceTargets(currentVlmConfig, params.InstanceOCIDs)
			if len(nodeTargets) == 0 {
				runErr = vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError,
					fmt.Errorf("OCIExecutePoolMaintenanceWorkflow: mediator maintenance response no longer contains requested node instances for pool %q", pool.UUID))
				logger.Error("Requested node instances missing from mediator maintenance response",
					"poolUUID", pool.UUID,
					"instanceOCIDs", params.InstanceOCIDs)
				emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageMediatorMaintenance, resultFailure)
				return nil, workflows.ConvertToVSAError(runErr)
			}
		}
		emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageMediatorMaintenance, resultSuccess)
	} else if currentVlmConfig.Deployment.DeploymentType == vlm.DeploymentTypeNonSharedHA {
		logger.Info("Skipping mediator maintenance; request does not target the mediator instance (non_shared_ha)",
			"poolUUID", pool.UUID,
			"instanceOCIDs", params.InstanceOCIDs,
		)
	}

	completedBatches := make([]int, 0)
	if len(nodeTargets) == 0 {
		logger.Info("Skipping instance MaintenanceWorkflow; request has no node instance targets",
			"poolUUID", pool.UUID,
			"instanceOCIDs", params.InstanceOCIDs,
			"mediatorRequested", mediatorRequested,
		)
	} else {
		batchInput := activities.CalculateBatchPlanActivityInput{
			NumHAPairs:                  len(currentVlmConfig.Cloud.HAPairs),
			HAPairIndices:               oneBasedHAPairIndices(nodeTargets),
			ParallelNumberOfNodesForITC: parallelNumberOfNodesForITCOCI,
		}
		var batchPlan *activities.CalculateBatchPlanActivityOutput
		if runErr = workflow.ExecuteActivity(actCtx, poolActivity.CalculateBatchPlanForUpdate, batchInput).Get(actCtx, &batchPlan); runErr != nil {
			logger.Error("Failed to calculate pool maintenance batch plan",
				"poolUUID", pool.UUID, "error", runErr)
			emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageBatchPlan, resultFailure)
			return nil, workflows.ConvertToVSAError(runErr)
		}
		emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageBatchPlan, resultSuccess)
		logger.Info("Pool maintenance batch plan calculated",
			"poolUUID", pool.UUID,
			"numHAPairs", batchPlan.NumHAPairs,
			"batchSize", batchPlan.BatchSize,
			"numWorkflowCalls", batchPlan.NumWorkflowCalls,
			"batchIndices", batchPlan.BatchIndices,
		)

		completedBatches = make([]int, 0, batchPlan.NumWorkflowCalls)
		for batchNum := 0; batchNum < batchPlan.NumWorkflowCalls; batchNum++ {
			batchIndices := batchPlan.BatchIndices[batchNum]
			batchTargets := filterTargetsByHAPairIndices(nodeTargets, batchIndices)
			if len(batchTargets) == 0 {
				continue
			}

			vlmReq := &vlm.MaintenanceRequest{
				VLMConfig:        *currentVlmConfig,
				OntapCredentials: *ontapCreds,
				MaintenanceType:  string(params.OperationType),
				Targets:          batchTargets,
			}
			logger.Info("Dispatching pool maintenance batch",
				"poolUUID", pool.UUID,
				"batchNumber", batchNum+1,
				"totalBatches", batchPlan.NumWorkflowCalls,
				"haPairIndices", batchIndices,
			)

			var vlmResp *vlm.MaintenanceResponse
			vlmResp, runErr = vsaClient.MaintenanceWorkflow(ctx, vlmReq)
			if runErr != nil {
				logger.Error("VLM-side pool maintenance batch failed",
					"poolUUID", pool.UUID,
					"batchNumber", batchNum+1,
					"haPairIndices", batchIndices,
					"completedBatches", completedBatches,
					"error", runErr)
				emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageVLMMaintenance, resultFailure)
				return nil, workflows.ConvertToVSAError(runErr)
			}
			if vlmResp == nil {
				runErr = vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError,
					fmt.Errorf("OCIExecutePoolMaintenanceWorkflow: VLM returned nil response for pool %q batch %d", pool.UUID, batchNum+1))
				logger.Error("VLM-side pool maintenance returned nil response",
					"poolUUID", pool.UUID, "batchNumber", batchNum+1)
				emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageVLMMaintenance, resultFailure)
				return nil, workflows.ConvertToVSAError(runErr)
			}
			if failures := failedMaintenanceResults(vlmResp.Results); len(failures) > 0 {
				runErr = vsaerrors.NewVCPError(vsaerrors.ErrVLMWorkflowError,
					fmt.Errorf("OCIExecutePoolMaintenanceWorkflow: pool %q maintenance failed in batch %d for node(s): %s", pool.UUID, batchNum+1, strings.Join(failures, "; ")))
				logger.Error("Pool maintenance batch reported node-level failures",
					"poolUUID", pool.UUID, "batchNumber", batchNum+1, "failures", failures)
				emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageVLMMaintenance, resultFailure)
				return nil, workflows.ConvertToVSAError(runErr)
			}
			currentVlmConfig = &vlmResp.VLMConfig
			completedBatches = append(completedBatches, batchNum+1)
		}
	}
	if runErr = workflow.ExecuteActivity(actCtx, poolActivity.CompletePoolMaintenance, pool, *currentVlmConfig).Get(actCtx, nil); runErr != nil {
		logger.Error("Failed to persist VLM config and complete pool maintenance",
			"poolUUID", pool.UUID, "completedBatches", completedBatches, "error", runErr)
		emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageVLMMaintenance, resultFailure)
		return nil, workflows.ConvertToVSAError(runErr)
	}
	emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageVLMMaintenance, resultSuccess)
	emitStage(ctx, wfExecuteMaintenance, queueCustomer, stageMarkReady, resultSuccess)

	logger.Info("OCI pool maintenance completed",
		"poolOCID", params.PoolOCID,
		"poolUUID", pool.UUID,
		"operationType", params.OperationType,
	)
	return fmt.Sprintf("pool maintenance executed for pool %s", pool.UUID), nil
}

func buildMaintenanceTargets(cfg *vlm.VLMConfig, instanceOCIDs []string) []vlm.MaintenanceTarget {
	if cfg == nil || len(instanceOCIDs) == 0 {
		return nil
	}
	requested := requestedInstanceSet(instanceOCIDs)

	var targets []vlm.MaintenanceTarget
	for i, pair := range cfg.Cloud.HAPairs {
		var nodeNames []string
		for _, vm := range []vlm.VMConfig{pair.VM1, pair.VM2} {
			instanceID := strings.TrimSpace(vm.VMInstanceID)
			if instanceID == "" {
				continue
			}
			if _, ok := requested[instanceID]; ok {
				nodeNames = append(nodeNames, vm.Name)
			}
		}
		if len(nodeNames) > 0 {
			targets = append(targets, vlm.MaintenanceTarget{
				HaPairIndex: i,
				NodeNames:   nodeNames,
			})
		}
	}
	return targets
}

func requestIncludesMediator(cfg *vlm.VLMConfig, instanceOCIDs []string) bool {
	return len(oneBasedMediatorHAPairIndices(cfg, instanceOCIDs)) > 0
}

func oneBasedMediatorHAPairIndices(cfg *vlm.VLMConfig, instanceOCIDs []string) []int {
	if cfg == nil || len(instanceOCIDs) == 0 {
		return nil
	}
	requested := requestedInstanceSet(instanceOCIDs)
	var indices []int
	for i, pair := range cfg.Cloud.HAPairs {
		mediatorID := strings.TrimSpace(pair.Mediator.VMInstanceID)
		if mediatorID == "" {
			continue
		}
		if _, ok := requested[mediatorID]; ok {
			indices = append(indices, i+1)
		}
	}
	return indices
}

func requestedInstanceSet(instanceOCIDs []string) map[string]struct{} {
	requested := make(map[string]struct{}, len(instanceOCIDs))
	for _, id := range instanceOCIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			requested[trimmed] = struct{}{}
		}
	}
	return requested
}

func oneBasedHAPairIndices(targets []vlm.MaintenanceTarget) []int {
	indices := make([]int, 0, len(targets))
	for _, t := range targets {
		indices = append(indices, t.HaPairIndex+1)
	}
	return indices
}

func filterTargetsByHAPairIndices(targets []vlm.MaintenanceTarget, oneBasedIndices []int) []vlm.MaintenanceTarget {
	want := make(map[int]struct{}, len(oneBasedIndices))
	for _, idx := range oneBasedIndices {
		want[idx-1] = struct{}{}
	}
	out := make([]vlm.MaintenanceTarget, 0, len(oneBasedIndices))
	for _, t := range targets {
		if _, ok := want[t.HaPairIndex]; ok {
			out = append(out, t)
		}
	}
	return out
}

func failedMaintenanceResults(results []vlm.PairMaintenanceResult) []string {
	var failures []string
	for _, r := range results {
		if !r.Succeeded {
			failures = append(failures, fmt.Sprintf("%s (ha_pair-%d): %s", r.NodeName, r.HaPairIndex, r.Error))
		}
	}
	return failures
}
