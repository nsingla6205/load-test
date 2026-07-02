// Package oci contains OCI-specific Temporal workflows. This file implements
// the OCI cluster upgrade workflow (VSA and mediator).
package oci

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	common "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ociParallelNumberOfNodesForITC controls batch size for large-pool upgrades.
var ociParallelNumberOfNodesForITC = env.GetInt("OCI_PARALLEL_NUMBER_OF_NODES_FOR_ITC", 4)

// OCIClusterUpgradeWorkflowParams is the input to the OCI cluster upgrade workflow.
type OCIClusterUpgradeWorkflowParams struct {
	JobID             string            `json:"jobId"`
	ClusterID         string            `json:"clusterId"`
	AccountName       string            `json:"accountName"`
	Pool              *datamodel.Pool   `json:"pool"`
	TargetVersion     string            `json:"targetVersion"`
	CurrentVersion    string            `json:"currentVersion"`
	VSAImagePath      string            `json:"vsaImagePath"`
	MediatorImageOcid string            `json:"mediatorImageOcid,omitempty"`
	ForceUpgrade      bool              `json:"forceUpgrade"`
	SkipUpdateRBAC    bool              `json:"skipUpdateRBAC"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// ociUpgradeContext carries mutable state across upgrade phases.
type ociUpgradeContext struct {
	Params               *OCIClusterUpgradeWorkflowParams
	Pool                 *datamodel.Pool
	Credentials          *vlm.OntapCredentials
	CurrentVlmConfig     *vlm.VLMConfig
	VlmClient            vlm.VlmWorkflowClient
	ClusterWasDisabled   bool
	NeedsVSAUpgrade      bool
	NeedsMediatorUpgrade bool
}

// ociUpgradeResult captures the outcome of the upgrade phase.
type ociUpgradeResult struct {
	VSAUpgradeResponse      *vlm.UpgradeVSAClusterDeploymentResponse
	MediatorUpgradeResponse *vlm.UpdateMediatorResponse
	FinalVlmConfig          *vlm.VLMConfig
	Success                 bool
	Error                   error
}

type ociClusterUpgradeWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &ociClusterUpgradeWorkflow{}

// OCIClusterUpgradeWorkflow is the Temporal entry point for OCI pool upgrades.
func OCIClusterUpgradeWorkflow(ctx workflow.Context, params *OCIClusterUpgradeWorkflowParams) error {
	wf := new(ociClusterUpgradeWorkflow)
	if err := wf.Setup(ctx, params); err != nil {
		wf.markJobFailed(ctx, params, err)
		return err
	}
	wf.Status = workflows.WorkflowStatusRunning

	if err := wf.updateClusterUpgradeJobStatus(ctx, params.JobID, string(models.UpgradeStatusInProgress), ""); err != nil {
		return err
	}

	_, customErr := wf.Run(ctx, params)
	if customErr != nil {
		wf.Status = workflows.WorkflowStatusFailed
		_ = wf.updateClusterUpgradeJobStatus(ctx, params.JobID, string(models.UpgradeStatusFailed), customErr.Error())
		return customErr
	}

	wf.Status = workflows.WorkflowStatusCompleted
	if err := wf.updateClusterUpgradeJobStatus(ctx, params.JobID, string(models.UpgradeStatusCompleted), ""); err != nil {
		wf.Logger.Error("Failed to mark job COMPLETED but upgrade succeeded; treating as success", "jobID", params.JobID, "error", err)
	}
	return nil
}

// markJobFailed tries to mark the cluster upgrade as failed when the setup itself fails before
// even executing the workflow OCIClusterUpgradeWorkflow
func (wf *ociClusterUpgradeWorkflow) markJobFailed(ctx workflow.Context, params *OCIClusterUpgradeWorkflowParams, cause error) {
	if params == nil || params.JobID == "" {
		return
	}
	clusterUpgradeActivity := &activities.ClusterUpgradeActivity{}
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})
	_ = workflow.ExecuteActivity(actCtx, clusterUpgradeActivity.UpdateClusterUpgradeJobStatusActivity,
		params.JobID, string(models.UpgradeStatusFailed), cause.Error()).Get(actCtx, nil)
}

// Setup initializes logger fields and registers the Temporal "status" query handler.
func (wf *ociClusterUpgradeWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params, ok := input.(*OCIClusterUpgradeWorkflowParams)
	if !ok || params == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError,
			errors.New("OCIClusterUpgradeWorkflow.Setup: invalid params"))
	}
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
	wf.Status = workflows.WorkflowStatusCreated

	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID":  wf.ID,
		"customerID":  wf.CustomerID,
		"clusterID":   params.ClusterID,
		"hyperscaler": "oci",
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

// updateClusterUpgradeJobStatus persists a status change for the upgrade job via a Temporal activity.
func (wf *ociClusterUpgradeWorkflow) updateClusterUpgradeJobStatus(ctx workflow.Context, jobID, status, errorMessage string) error {
	clusterUpgradeActivity := &activities.ClusterUpgradeActivity{}
	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})
	err := workflow.ExecuteActivity(actCtx, clusterUpgradeActivity.UpdateClusterUpgradeJobStatusActivity, jobID, status, errorMessage).Get(actCtx, nil)
	if err != nil {
		wf.Logger.Error("Failed to update cluster upgrade job status", "jobID", jobID, "status", status, "error", err)
		return err
	}
	return nil
}

// Run executes pre-upgrade → upgrade → post-upgrade phases sequentially.
func (wf *ociClusterUpgradeWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	var params *OCIClusterUpgradeWorkflowParams
	if len(args) > 0 {
		params, _ = args[0].(*OCIClusterUpgradeWorkflowParams)
	}
	if params == nil {
		return nil, workflows.ConvertToVSAError(errors.New("OCIClusterUpgradeWorkflow.Run: missing or invalid params"))
	}

	wf.Logger.Info("Starting cluster upgrade workflow",
		"targetVersion", params.TargetVersion, "currentVersion", params.CurrentVersion)

	ctx, upgradeCtx, customErr := wf.preUpgradePhase(ctx, params)
	if customErr != nil {
		wf.Logger.Error("Pre-upgrade phase failed", "error", customErr)
		wf.cleanupAfterPreUpgradeFailure(ctx, upgradeCtx)
		return nil, customErr
	}

	upgradeRes, customErr := wf.upgradePhase(ctx, upgradeCtx)
	if customErr != nil {
		wf.Logger.Error("Upgrade phase failed", "error", customErr)
		if cleanupErr := wf.postUpgradePhase(ctx, upgradeCtx, upgradeRes, customErr); cleanupErr != nil {
			wf.Logger.Error("Post-upgrade cleanup failed after upgrade error", "error", cleanupErr)
		}
		return nil, customErr
	}

	if cleanupErr := wf.postUpgradePhase(ctx, upgradeCtx, upgradeRes, nil); cleanupErr != nil {
		wf.Logger.Error("Post-upgrade phase failed", "error", cleanupErr)
	}

	wf.Logger.Info("Cluster upgrade workflow completed successfully")
	return params, nil
}

// cleanupAfterPreUpgradeFailure powers off a disabled cluster that was powered
// on during pre-upgrade but failed before reaching the upgrade phase.
func (wf *ociClusterUpgradeWorkflow) cleanupAfterPreUpgradeFailure(
	ctx workflow.Context, upgradeCtx *ociUpgradeContext,
) {
	if upgradeCtx == nil || !upgradeCtx.ClusterWasDisabled {
		return
	}
	wf.Logger.Info("Powering off cluster after pre-upgrade failure (was originally disabled)")
	powerOffRequest := &vlm.ClusterPowerOpReq{
		VLMConfig:        *upgradeCtx.CurrentVlmConfig,
		OntapCredentials: *upgradeCtx.Credentials,
		Operation:        vlm.ClusterPowerOff,
	}
	if err := upgradeCtx.VlmClient.ClusterPowerOp(ctx, powerOffRequest); err != nil {
		wf.Logger.Error("Failed to power off cluster after pre-upgrade failure", "error", err)
	} else {
		wf.Logger.Info("Cluster powered off successfully after pre-upgrade failure")
	}
}

// preUpgradePhase fetches credentials, loads VLM config, powers on if DISABLED,
// and determines whether the upgrade is actually needed.
func (wf *ociClusterUpgradeWorkflow) preUpgradePhase(
	ctx workflow.Context, params *OCIClusterUpgradeWorkflowParams,
) (workflow.Context, *ociUpgradeContext, *vsaerrors.CustomError) {
	wf.Logger.Info("Starting pre-upgrade phase")

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return ctx, nil, workflows.ConvertToVSAError(err)
	}
	startToClose, err := time.ParseDuration(getOCIUpgradeStartToCloseTimeout(params.Pool))
	if err != nil {
		return ctx, nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: startToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	poolActivities := &activities.PoolActivity{}
	credentials := &vlm.OntapCredentials{}
	if err := workflow.ExecuteActivity(ctx, poolActivities.GetOnTapCredentialsForOCI, params.Pool).Get(ctx, credentials); err != nil {
		wf.Logger.Error("Failed to fetch ONTAP credentials from OCI Vault", "error", err)
		return ctx, nil, workflows.ConvertToVSAError(err)
	}

	currentVlmConfig := &vlm.VLMConfig{}
	if err := json.Unmarshal([]byte(params.Pool.VLMConfig), currentVlmConfig); err != nil {
		return ctx, nil, workflows.ConvertToVSAError(err)
	}

	vlmClient := workflows.GetNewVSAClientWorkflowManager()

	clusterWasDisabled := false
	if params.Pool.State == datamodel.LifeCycleStateDisabled {
		wf.Logger.Info("Cluster is DISABLED, powering on for upgrade")
		clusterWasDisabled = true

		powerOnRequest := &vlm.ClusterPowerOpReq{
			VLMConfig:        *currentVlmConfig,
			OntapCredentials: *credentials,
			Operation:        vlm.ClusterPowerOn,
		}
		if err := vlmClient.ClusterPowerOp(ctx, powerOnRequest); err != nil {
			wf.Logger.Error("Failed to power on cluster", "error", err)
			return ctx, nil, workflows.ConvertToVSAError(err)
		}
		wf.Logger.Info("Cluster powered on successfully")
	}

	needsVSAUpgrade := true
	if params.Pool.BuildInfo != nil {
		if !params.ForceUpgrade && params.Pool.BuildInfo.OntapVersion == params.TargetVersion {
			needsVSAUpgrade = false
			wf.Logger.Info("VSA already up to date",
				"currentOntapVersion", params.Pool.BuildInfo.OntapVersion,
				"targetVersion", params.TargetVersion)
		}
	}

	needsMediatorUpgrade := false
	if params.MediatorImageOcid != "" {
		if currentVlmConfig.Deployment.DeploymentType == vlm.DeploymentTypeSharedHA {
			wf.Logger.Info("Skipping mediator upgrade — shared HA deployments do not have a dedicated mediator",
				"deploymentType", currentVlmConfig.Deployment.DeploymentType)
		} else {
			needsMediatorUpgrade = true
			if params.Pool.BuildInfo != nil {
				if !params.ForceUpgrade && params.Pool.BuildInfo.MediatorBuildImage == params.MediatorImageOcid {
					needsMediatorUpgrade = false
					wf.Logger.Info("Mediator already up to date",
						"currentBuildImage", params.Pool.BuildInfo.MediatorBuildImage)
				}
			}
		}
	}

	upgradeCtx := &ociUpgradeContext{
		Params:               params,
		Pool:                 params.Pool,
		Credentials:          credentials,
		CurrentVlmConfig:     currentVlmConfig,
		VlmClient:            vlmClient,
		ClusterWasDisabled:   clusterWasDisabled,
		NeedsVSAUpgrade:      needsVSAUpgrade,
		NeedsMediatorUpgrade: needsMediatorUpgrade,
	}

	wf.Logger.Info("Pre-upgrade phase completed",
		"needsVSAUpgrade", needsVSAUpgrade,
		"needsMediatorUpgrade", needsMediatorUpgrade,
		"clusterWasDisabled", clusterWasDisabled)
	return ctx, upgradeCtx, nil
}

// upgradePhase runs mediator upgrade (if needed) then VSA upgrade via VLM, persisting BuildInfo on success.
func (wf *ociClusterUpgradeWorkflow) upgradePhase(
	ctx workflow.Context, upgradeCtx *ociUpgradeContext,
) (*ociUpgradeResult, *vsaerrors.CustomError) {
	wf.Logger.Info("Starting upgrade phase")

	res := &ociUpgradeResult{Success: false}

	if upgradeCtx.NeedsMediatorUpgrade {
		mediatorResp, customErr := wf.executeMediatorUpgrade(ctx, upgradeCtx)
		if customErr != nil {
			res.Error = customErr
			return res, customErr
		}
		if mediatorResp != nil {
			upgradeCtx.CurrentVlmConfig = &mediatorResp.VLMConfig
			res.MediatorUpgradeResponse = mediatorResp
		}
		wf.Logger.Info("Mediator upgrade step completed, proceeding to VSA upgrade")
	} else {
		wf.Logger.Info("Skipping mediator upgrade — not needed or mediatorImageOcid not provided")
	}

	if !upgradeCtx.NeedsVSAUpgrade {
		wf.Logger.Info("Skipping VSA cluster deployment upgrade — already up to date")
		res.FinalVlmConfig = upgradeCtx.CurrentVlmConfig
		res.Success = true
		return res, nil
	}

	vsaResponse, customErr := wf.executeVSAUpgrade(ctx, upgradeCtx)
	if customErr != nil {
		res.Error = customErr
		return res, customErr
	}

	upgradeCtx.CurrentVlmConfig = &vsaResponse.VLMConfig
	res.VSAUpgradeResponse = vsaResponse

	if err := wf.updateOntapVersionAfterUpgrade(ctx, upgradeCtx, vsaResponse.OntapVersion); err != nil {
		wf.Logger.Error("Failed to update ONTAP version for pool build_info but cluster upgrade succeeded", "error", err)
	}

	res.FinalVlmConfig = upgradeCtx.CurrentVlmConfig
	res.Success = true

	wf.Logger.Info("Upgrade phase completed")
	return res, nil
}

// executeMediatorUpgrade upgrades the VSA mediator via VLM for OCI.
func (wf *ociClusterUpgradeWorkflow) executeMediatorUpgrade(
	ctx workflow.Context, upgradeCtx *ociUpgradeContext,
) (*vlm.UpdateMediatorResponse, *vsaerrors.CustomError) {
	wf.Logger.Info("Starting mediator upgrade via VLM",
		"mediatorImageOcid", upgradeCtx.Params.MediatorImageOcid)

	req := &vlm.UpdateMediatorRequest{
		VLMConfig:        *upgradeCtx.CurrentVlmConfig,
		OntapCredentials: *upgradeCtx.Credentials,
		MediatorProviderConfig: &vlm.OCIMediatorProviderConfig{
			MediatorImageOcid: upgradeCtx.Params.MediatorImageOcid,
		},
	}

	resp, err := upgradeCtx.VlmClient.UpgradeVSAMediatorWorkflow(ctx, req)
	if err != nil {
		wf.Logger.Error("Mediator upgrade failed", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVLMWorkflowError, err)
	}

	wf.Logger.Info("Mediator upgrade completed successfully",
		"mediatorImageOcid", upgradeCtx.Params.MediatorImageOcid)
	return resp, nil
}

// executeVSAUpgrade dispatches the VLM upgrade — batch mode for large pools, single-shot otherwise.
func (wf *ociClusterUpgradeWorkflow) executeVSAUpgrade(
	ctx workflow.Context, upgradeCtx *ociUpgradeContext,
) (*vlm.UpgradeVSAClusterDeploymentResponse, *vsaerrors.CustomError) {
	poolActivities := &activities.PoolActivity{}

	if upgradeCtx.Pool.LargeCapacity {
		batchInput := &activities.CalculateBatchPlanActivityInput{
			NumHAPairs:                  upgradeCtx.CurrentVlmConfig.Deployment.NumHAPair,
			ParallelNumberOfNodesForITC: ociParallelNumberOfNodesForITC,
		}
		var batchPlan *activities.CalculateBatchPlanActivityOutput
		if err := workflow.ExecuteActivity(ctx, poolActivities.CalculateBatchPlanForUpdate, batchInput).Get(ctx, &batchPlan); err != nil {
			wf.Logger.Error("Failed to calculate batch plan for large pool upgrade", "error", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrVLMWorkflowError, err)
		}

		vsaResponse, err := wf.executeClusterUpgradeBatchUpdates(ctx, batchPlan, upgradeCtx)
		if err != nil {
			wf.Logger.Error("Large pool VSA upgrade failed", "error", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrVLMWorkflowError, err)
		}
		wf.Logger.Info("VSA cluster deployment upgrade completed (batch)",
			"ontapVersion", vsaResponse.OntapVersion)
		return vsaResponse, nil
	}

	upgradeRequest := &vlm.UpdateVSAClusterDeploymentRequest{}
	if err := wf.prepareClusterUpgradeRequestActivity(ctx, upgradeRequest, upgradeCtx.Params,
		*upgradeCtx.CurrentVlmConfig, *upgradeCtx.Credentials); err != nil {
		wf.Logger.Error("Failed to prepare cluster upgrade request", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVLMWorkflowError, err)
	}

	wf.Logger.Info("Starting VSA cluster deployment upgrade",
		"targetVersion", upgradeCtx.Params.TargetVersion)
	vsaResponse, err := upgradeCtx.VlmClient.UpgradeVSAClusterDeploymentWorkflow(ctx, upgradeRequest)
	if err != nil {
		wf.Logger.Error("VSA cluster deployment upgrade failed", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVLMWorkflowError, err)
	}
	wf.Logger.Info("VSA cluster deployment upgrade completed",
		"ontapVersion", vsaResponse.OntapVersion)
	return vsaResponse, nil
}

// persistBuildInfoAndVLMConfig writes updated build info and VLM config to the pool (best-effort).
func (wf *ociClusterUpgradeWorkflow) persistBuildInfoAndVLMConfig(
	ctx workflow.Context, upgradeCtx *ociUpgradeContext,
	buildInfo *datamodel.PoolBuildInfo, step string,
) {
	vlmConfigBytes, err := json.Marshal(upgradeCtx.CurrentVlmConfig)
	if err != nil {
		wf.Logger.Error("Failed to marshal VLM config after upgrade step", "step", step, "error", err)
		return
	}
	updates := map[string]interface{}{
		"build_info": buildInfo,
		"vlm_config": string(vlmConfigBytes),
	}
	poolActivities := &activities.PoolActivity{}
	if updateErr := workflow.ExecuteActivity(ctx, poolActivities.UpdatePoolFields,
		upgradeCtx.Pool.UUID, updates).Get(ctx, nil); updateErr != nil {
		wf.Logger.Error("Failed to persist build info after upgrade step", "step", step, "error", updateErr)
	} else {
		wf.Logger.Info("Persisted build info after upgrade step", "step", step)
	}
}

// postUpgradePhase refreshes licenses on success and powers off if originally DISABLED (best-effort).
func (wf *ociClusterUpgradeWorkflow) postUpgradePhase(
	ctx workflow.Context, upgradeCtx *ociUpgradeContext,
	upgradeRes *ociUpgradeResult, _ *vsaerrors.CustomError,
) *vsaerrors.CustomError {
	wf.Logger.Info("Starting post-upgrade phase", "upgradeSuccess", upgradeRes.Success)

	if upgradeRes.Success {
		ontapVersion := upgradeCtx.Params.TargetVersion
		if upgradeRes.VSAUpgradeResponse != nil && upgradeRes.VSAUpgradeResponse.OntapVersion != "" {
			ontapVersion = upgradeRes.VSAUpgradeResponse.OntapVersion
		}

		buildInfo := &datamodel.PoolBuildInfo{
			VSABuildImage:  upgradeRes.FinalVlmConfig.Deployment.Images.VSAImageName,
			OntapVersion:   ontapVersion,
			BuildTimestamp: workflow.Now(ctx),
		}
		if buildInfo.VSABuildImage == "" && upgradeCtx.Pool.BuildInfo != nil {
			buildInfo.VSABuildImage = upgradeCtx.Pool.BuildInfo.VSABuildImage
		}
		if upgradeRes.FinalVlmConfig.Deployment.Images.MediatorImageName != "" {
			buildInfo.MediatorBuildImage = upgradeRes.FinalVlmConfig.Deployment.Images.MediatorImageName
		} else if upgradeCtx.Pool.BuildInfo != nil {
			buildInfo.MediatorBuildImage = upgradeCtx.Pool.BuildInfo.MediatorBuildImage
		}
		wf.persistBuildInfoAndVLMConfig(ctx, upgradeCtx, buildInfo, "post-upgrade")

		if err := wf.updateLicense(ctx, upgradeCtx.Params, upgradeRes.FinalVlmConfig,
			upgradeCtx.Credentials, upgradeCtx.VlmClient); err != nil {
			wf.Logger.Error("Failed to update license after upgrade", "error", err)
		}

		if !upgradeCtx.Params.SkipUpdateRBAC {
			wf.refreshRbacForPool(ctx, upgradeCtx)
		} else {
			wf.Logger.Info("Skipping RBAC refresh, skipUpdateRBAC is set", "poolUUID", upgradeCtx.Pool.UUID)
		}
	}

	if upgradeCtx.ClusterWasDisabled {
		wf.Logger.Info("Powering off cluster after upgrade (was originally disabled)")
		vlmConfig := upgradeCtx.CurrentVlmConfig
		if upgradeRes.FinalVlmConfig != nil {
			vlmConfig = upgradeRes.FinalVlmConfig
		}
		powerOffRequest := &vlm.ClusterPowerOpReq{
			VLMConfig:        *vlmConfig,
			OntapCredentials: *upgradeCtx.Credentials,
			Operation:        vlm.ClusterPowerOff,
		}
		if err := upgradeCtx.VlmClient.ClusterPowerOp(ctx, powerOffRequest); err != nil {
			wf.Logger.Error("Failed to power off cluster after upgrade", "error", err)
		} else {
			wf.Logger.Info("Cluster powered off successfully")
		}
	}
	wf.Logger.Info("Post-upgrade phase completed")
	return nil
}

// executeClusterUpgradeBatchUpdates upgrades a large pool one HA-pair batch at a time,
// carrying forward the VLM config from each batch to the next.
func (wf *ociClusterUpgradeWorkflow) executeClusterUpgradeBatchUpdates(
	ctx workflow.Context,
	batchPlan *activities.CalculateBatchPlanActivityOutput,
	upgradeCtx *ociUpgradeContext,
) (*vlm.UpgradeVSAClusterDeploymentResponse, error) {
	currentConfig := upgradeCtx.CurrentVlmConfig
	var lastResponse *vlm.UpgradeVSAClusterDeploymentResponse
	completedBatches := make([]int, 0, batchPlan.NumWorkflowCalls)

	for batchNum := 0; batchNum < batchPlan.NumWorkflowCalls; batchNum++ {
		batchIndices := batchPlan.BatchIndices[batchNum]

		upgradeRequest := &vlm.UpdateVSAClusterDeploymentRequest{}
		if err := wf.prepareClusterUpgradeRequestActivity(ctx, upgradeRequest, upgradeCtx.Params,
			*currentConfig, *upgradeCtx.Credentials); err != nil {
			return nil, fmt.Errorf("failed to prepare cluster upgrade request for batch %d: %w", batchNum+1, err)
		}
		upgradeRequest.HAPairIndices = batchIndices

		wf.Logger.Info("Starting large pool cluster upgrade batch",
			"batchNumber", batchNum+1, "totalBatches", batchPlan.NumWorkflowCalls,
			"indices", batchIndices, "totalHAPairs", batchPlan.NumHAPairs,
			"batchSize", batchPlan.BatchSize)

		response, err := upgradeCtx.VlmClient.UpgradeVSAClusterDeploymentWorkflow(ctx, upgradeRequest)
		if err != nil {
			wf.Logger.Error("Large pool cluster upgrade failed",
				"batchNumber", batchNum+1, "totalBatches", batchPlan.NumWorkflowCalls,
				"completedBatches", completedBatches, "error", err)
			return nil, err
		}

		completedBatches = append(completedBatches, batchNum+1)
		currentConfig = &response.VLMConfig
		lastResponse = response

		wf.Logger.Info("Completed large pool cluster upgrade batch",
			"batchNumber", batchNum+1, "totalBatches", batchPlan.NumWorkflowCalls,
			"indices", batchIndices)
	}

	if lastResponse == nil {
		return nil, fmt.Errorf("no cluster upgrade response produced from batch execution")
	}

	upgradeCtx.CurrentVlmConfig = currentConfig
	return lastResponse, nil
}

// prepareClusterUpgradeRequestActivity builds the VLM upgrade request.
// Generates an OCI PAR URL from vsaImagePath and sets it as OntapUpgradeImagePath.
func (wf *ociClusterUpgradeWorkflow) prepareClusterUpgradeRequestActivity(
	ctx workflow.Context,
	upgradeRequest *vlm.UpdateVSAClusterDeploymentRequest,
	params *OCIClusterUpgradeWorkflowParams,
	currentVlmConfig vlm.VLMConfig,
	credentials vlm.OntapCredentials,
) error {
	upgradeRequest.VLMConfig = currentVlmConfig

	commonActivity := &activities.CommonActivities{}
	var parURL string
	if err := workflow.ExecuteActivity(ctx, commonActivity.GenerateVSAOCIPARActivity,
		params.VSAImagePath).Get(ctx, &parURL); err != nil {
		return fmt.Errorf("failed to generate OCI PAR for VSA image %s: %w", params.VSAImagePath, err)
	}

	upgradeRequest.OntapUpgrade = vlm.OntapUpgradeConfig{
		OntapUpgradeTargetImageVersion: params.TargetVersion,
		OntapUpgradeImagePath:          parURL,
		SkipOntapImageVersionMatch:     env.SkipOntapImageVersionMatch || params.ForceUpgrade,
		RunPreUpgrade:                  true,
		VSAImageName:                   currentVlmConfig.Deployment.Images.VSAImageName,
	}
	upgradeRequest.OntapCredentials = credentials
	// -1 signals VLM to skip auto-tier threshold update; valid values are 0..100.
	upgradeRequest.AutoTierThreshold = -1
	return nil
}

// updateOntapVersionAfterUpgrade persists the VLM-reported ONTAP version to pool.cluster_details.
func (wf *ociClusterUpgradeWorkflow) updateOntapVersionAfterUpgrade(
	ctx workflow.Context, upgradeCtx *ociUpgradeContext, ontapVersion string,
) error {
	wf.Logger.Info("Updating pool cluster_details with ONTAP version", "ontapVersion", ontapVersion)

	clusterDetail := upgradeCtx.Pool.ClusterDetails
	clusterDetail.OntapVersion = ontapVersion

	poolActivities := &activities.PoolActivity{}
	updates := map[string]interface{}{
		"cluster_details": clusterDetail,
	}
	if err := workflow.ExecuteActivity(ctx, poolActivities.UpdatePoolFields,
		upgradeCtx.Pool.UUID, updates).Get(ctx, nil); err != nil {
		return fmt.Errorf("failed to update pool cluster_details with ONTAP version: %w", err)
	}

	wf.Logger.Info("Updated pool cluster_details with ONTAP version", "ontapVersion", ontapVersion)
	return nil
}

// updateLicense refreshes ONTAP licenses on all node-mgmt LIFs. Per-node failures are non-fatal.
func (wf *ociClusterUpgradeWorkflow) updateLicense(
	ctx workflow.Context,
	params *OCIClusterUpgradeWorkflowParams,
	currentVlmConfig *vlm.VLMConfig,
	credentials *vlm.OntapCredentials,
	vlmClient vlm.VlmWorkflowClient,
) error {
	var nodeMgmtIPs []string
	for haPairIndex, haPair := range currentVlmConfig.Cloud.HAPairs {
		if lif, ok := haPair.VM1.SystemLIFs[vlm.LIFTypeNodeMgmt]; ok {
			nodeMgmtIPs = append(nodeMgmtIPs, lif.IP)
			wf.Logger.Info("Found VM1 node management IP",
				"haPairIndex", haPairIndex, "nodeMgmtIP", lif.IP)
		}
		if lif, ok := haPair.VM2.SystemLIFs[vlm.LIFTypeNodeMgmt]; ok {
			nodeMgmtIPs = append(nodeMgmtIPs, lif.IP)
			wf.Logger.Info("Found VM2 node management IP",
				"haPairIndex", haPairIndex, "nodeMgmtIP", lif.IP)
		}
	}

	if len(nodeMgmtIPs) == 0 {
		wf.Logger.Warn("No node management IPs in VLM config; skipping license update")
		return nil
	}

	for _, ip := range nodeMgmtIPs {
		req := &vlm.UpdateLicenseRequest{
			OntapLicense: vlm.OntapLicense{
				SecretUri: []string{secretURI},
			},
			OntapCredentials: *credentials,
			VSAManagementIP:  ip,
			Provider:         vlm.OCICloud,
		}
		wf.Logger.Info("Updating ONTAP license for node", "nodeMgmtIP", ip)
		if err := vlmClient.UpdateLicenseWorkflow(ctx, req); err != nil {
			wf.Logger.Error("Failed to update license for node", "nodeMgmtIP", ip, "error", err)
			continue
		}
		wf.Logger.Info("License updated successfully for node", "nodeMgmtIP", ip)
	}

	wf.Logger.Info("License update process completed", "totalNodes", len(nodeMgmtIPs))
	return nil
}

// refreshRbacForPool delegates to OCIRefreshRbacForPoolWorkflow as a child workflow.
// Failure is logged but does not fail the upgrade.
//
// The child workflow ID embeds the parent RunID so each parent retry/replay starts
// a fresh child execution. Without this, a second attempt would collide with the
// terminal-state child from the previous attempt (causing USE_EXISTING_WORKFLOW
// conflicts depending on Temporal's WorkflowIDReusePolicy).
func (wf *ociClusterUpgradeWorkflow) refreshRbacForPool(
	ctx workflow.Context, upgradeCtx *ociUpgradeContext,
) {
	rbacParams := &common.RefreshRbacForPoolParams{
		PoolOCID:    upgradeCtx.Pool.PoolExternalIdentifier,
		AccountName: upgradeCtx.Params.AccountName,
	}

	parentRunID := workflow.GetInfo(ctx).WorkflowExecution.RunID
	childWorkflowID := fmt.Sprintf("%s-rbac-%s", wf.ID, parentRunID)
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            childWorkflowID,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	})

	err := workflow.ExecuteChildWorkflow(childCtx, OCIRefreshRbacForPoolWorkflow, rbacParams, upgradeCtx.Pool).Get(childCtx, nil)
	if err != nil {
		wf.Logger.Error("RBAC refresh child workflow failed", "poolUUID", upgradeCtx.Pool.UUID, "childWorkflowID", childWorkflowID, "error", err)
		return
	}

	wf.Logger.Info("RBAC role applied", "poolUUID", upgradeCtx.Pool.UUID, "childWorkflowID", childWorkflowID)
}

// getOCIUpgradeStartToCloseTimeout returns the activity timeout (longer for large pools).
func getOCIUpgradeStartToCloseTimeout(pool *datamodel.Pool) string {
	if pool != nil && pool.LargeCapacity {
		return workflows.StartToCloseTimeoutUpgradeLV
	}
	return workflows.StartToCloseTimeoutUpgrade
}
