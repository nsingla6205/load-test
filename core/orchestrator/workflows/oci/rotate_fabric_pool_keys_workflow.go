package oci

import (
	"fmt"

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

type ociRotateFabricPoolKeysWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &ociRotateFabricPoolKeysWorkflow{}

func OCIRotateFabricPoolKeysWorkflow(ctx workflow.Context, params *common.RotateFabricPoolKeysParams, pool *datamodel.Pool) error {
	wf := new(ociRotateFabricPoolKeysWorkflow)
	log := util.GetLogger(ctx)
	if err := wf.Setup(ctx, params); err != nil {
		return workflows.ConvertToVSAError(err)
	}

	wf.Status = workflows.WorkflowStatusRunning
	_, customErr := wf.Run(ctx, params, pool)
	if customErr != nil {
		log.Error("OCIRotateFabricPoolKeysWorkflow failed", "error", customErr)
		wf.Status = workflows.WorkflowStatusFailed
		return workflows.ConvertToVSAError(customErr)
	}

	wf.Status = workflows.WorkflowStatusCompleted
	return nil
}

func (wf *ociRotateFabricPoolKeysWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params, ok := input.(*common.RotateFabricPoolKeysParams)
	if !ok || params == nil {
		return fmt.Errorf("OCIRotateFabricPoolKeysWorkflow.Setup: invalid params (expected *RotateFabricPoolKeysParams)")
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

func (wf *ociRotateFabricPoolKeysWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	if len(args) < 2 {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIRotateFabricPoolKeysWorkflow.Run: expected 2 args, got %d", len(args)))
	}
	params, ok := args[0].(*common.RotateFabricPoolKeysParams)
	if !ok {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIRotateFabricPoolKeysWorkflow.Run: args[0] has unexpected type %T, want *common.RotateFabricPoolKeysParams", args[0]))
	}
	if params == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCIRotateFabricPoolKeysWorkflow.Run: args[0] (*common.RotateFabricPoolKeysParams) must not be nil")))
	}
	pool, ok := args[1].(*datamodel.Pool)
	if !ok {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIRotateFabricPoolKeysWorkflow.Run: args[1] has unexpected type %T, want *datamodel.Pool", args[1]))
	}
	if pool == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCIRotateFabricPoolKeysWorkflow.Run: args[1] (*datamodel.Pool) must not be nil")))
	}

	newSecretOCID := params.NewSecretOCID
	newSecretVersion := params.NewSecretVersion

	logger := wf.Logger
	if logger == nil {
		logger = util.GetLogger(ctx)
	}
	logger.Info("OCIRotateFabricPoolKeysWorkflow starting",
		"poolOCID", params.PoolOCID,
		"poolUUID", pool.UUID,
		"newSecretOCID", newSecretOCID,
		"newSecretVersion", newSecretVersion,
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

	ontapVersion := workflows.ResolveVLMWorkerVersion(pool)
	if ontapVersion == "" {
		runErr = vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
			fmt.Errorf("could not resolve a VLM worker version to route OCI fabric pool key rotation; buildInfo=%+v", pool.BuildInfo))
		logger.Error("OCI fabric pool key rotation stopped: could not resolve a VLM worker version",
			"poolOCID", pool.PoolExternalIdentifier, "buildInfo", pool.BuildInfo, "error", runErr)
		return nil, workflows.ConvertToVSAError(runErr)
	}

	var currentVlmConfig *vlm.VLMConfig
	if runErr = workflow.ExecuteActivity(actCtx, poolActivity.ParseVlmConfig, pool).Get(actCtx, &currentVlmConfig); runErr != nil {
		logger.Error("Failed to parse stored VLM config for fabric pool key rotation",
			"poolUUID", pool.UUID, "error", runErr)
		return nil, workflows.ConvertToVSAError(runErr)
	}

	ontapCreds := &vlm.OntapCredentials{}
	if runErr = workflow.ExecuteActivity(actCtx, poolActivity.GetOnTapCredentialsForOCI, pool).Get(actCtx, ontapCreds); runErr != nil {
		logger.Error("Failed to fetch ONTAP admin credentials for fabric pool key rotation",
			"poolUUID", pool.UUID, "error", runErr)
		return nil, workflows.ConvertToVSAError(runErr)
	}

	if runErr = workflow.ExecuteActivity(
		actCtx, poolActivity.ValidateOCIFabricPoolSecret, newSecretOCID, newSecretVersion,
	).Get(actCtx, nil); runErr != nil {
		logger.Error("New fabric pool secret failed validation",
			"poolOCID", pool.PoolExternalIdentifier,
			"newSecretOCID", newSecretOCID,
			"newSecretVersion", newSecretVersion,
			"error", runErr)
		return nil, workflows.ConvertToVSAError(runErr)
	}

	vsaClient := workflows.GetNewVSAClientWorkflowManager()
	vlmReq := &vlm.RotateFabricPoolKeysRequest{
		VLMConfig:        *currentVlmConfig,
		NewSecretOcid:    newSecretOCID,
		NewSecretVersion: newSecretVersion,
		OntapCredentials: *ontapCreds,
	}
	logger.Info("Routing fabric pool key rotation to the pool's deployed VLM worker",
		"poolOCID", pool.PoolExternalIdentifier,
		"ontapVersion", ontapVersion,
	)
	var vlmResp *vlm.RotateFabricPoolKeysResponse
	vlmResp, runErr = vsaClient.RotateFabricPoolKeys(ctx, vlmReq, ontapVersion)
	if runErr != nil {
		logger.Error("VLM-side fabric pool key rotation failed",
			"poolUUID", pool.UUID,
			"newSecretOCID", newSecretOCID,
			"error", runErr)
		return nil, handleActivityError(logger, runErr, "RotateFabricPoolKeys", pool.UUID)
	}
	if vlmResp == nil {
		runErr = vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError,
			fmt.Errorf("OCIRotateFabricPoolKeysWorkflow: VLM returned nil response for pool %q", pool.UUID))
		logger.Error("VLM-side fabric pool key rotation returned nil response",
			"poolUUID", pool.UUID, "newSecretOCID", newSecretOCID)
		return nil, workflows.ConvertToVSAError(runErr)
	}

	if runErr = workflow.ExecuteActivity(
		actCtx, poolActivity.CreatedPool, pool, &vlmResp.VLMConfig,
	).Get(actCtx, nil); runErr != nil {
		logger.Error("Failed to persist rotated fabric pool secret and transition pool to READY",
			"poolUUID", pool.UUID,
			"newSecretOCID", newSecretOCID,
			"error", runErr)
		return nil, workflows.ConvertToVSAError(runErr)
	}

	logger.Info("OCI fabric pool key rotation completed",
		"poolOCID", params.PoolOCID,
		"poolUUID", pool.UUID,
		"newSecretOCID", newSecretOCID,
		"newSecretVersion", newSecretVersion,
	)
	return fmt.Sprintf("fabric pool keys rotated for pool %s", pool.UUID), nil
}
