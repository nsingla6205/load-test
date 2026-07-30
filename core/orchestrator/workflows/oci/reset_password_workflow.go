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

type ociResetAdminPasswordForPoolWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &ociResetAdminPasswordForPoolWorkflow{}

// OCIResetOciAdminPasswordWorkflow resets the OCI expert-mode admin (ociadmin)
// password on a single pool's ONTAP cluster. It sets the password to the
// caller-supplied/generated temporary value and then expires it, forcing a
// change on next login.
//
// The temporary password travels in params.TempPassword. It is a plaintext
// secret and must never be logged.
func OCIResetOciAdminPasswordWorkflow(ctx workflow.Context, params *common.ResetOciAdminPasswordParams, pool *datamodel.Pool) error {
	wf := new(ociResetAdminPasswordForPoolWorkflow)
	log := util.GetLogger(ctx)
	if err := wf.Setup(ctx, params); err != nil {
		return workflows.ConvertToVSAError(err)
	}

	wf.Status = workflows.WorkflowStatusRunning
	_, customErr := wf.Run(ctx, params, pool)
	if customErr != nil {
		log.Error("error in OCIResetOciAdminPasswordWorkflow", "error", customErr)
		wf.Status = workflows.WorkflowStatusFailed
		return workflows.ConvertToVSAError(customErr)
	}

	wf.Status = workflows.WorkflowStatusCompleted
	return nil
}

func (wf *ociResetAdminPasswordForPoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params, ok := input.(*common.ResetOciAdminPasswordParams)
	if !ok || params == nil {
		return fmt.Errorf("OCIResetOciAdminPasswordWorkflow.Setup: invalid params (expected *ResetOciAdminPasswordParams)")
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

func (wf *ociResetAdminPasswordForPoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	var params *common.ResetOciAdminPasswordParams
	var pool *datamodel.Pool
	if len(args) >= 2 {
		params, _ = args[0].(*common.ResetOciAdminPasswordParams)
		pool, _ = args[1].(*datamodel.Pool)
	}
	if params == nil || pool == nil {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIResetOciAdminPasswordWorkflow.Run: missing or invalid params/pool"))
	}
	if params.TempPassword == "" {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("temporary password is required to reset the OCI admin password for pool %q", pool.UUID)))
	}
	if pool.PoolCredentials == nil {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("pool credentials are required to reset the OCI admin password for pool %q", pool.UUID))
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: workflows.RbacActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        workflows.RbacActivityInitialBackoff,
			BackoffCoefficient:     2.0,
			MaximumInterval:        workflows.RbacActivityMaxInterval,
			MaximumAttempts:        int32(workflows.RbacActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})

	poolActivity := &activities.PoolActivity{}

	var vlmConfig *vlm.VLMConfig
	if err := workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, pool).Get(ctx, &vlmConfig); err != nil {
		wf.Logger.Error("Failed to parse VLM config", "poolUUID", pool.UUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	clusterName := vlmConfig.VsaCluster.ClusterName
	if clusterName == "" {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
			fmt.Errorf("cluster name is empty in VLM config for pool %q", pool.UUID)))
	}

	// Set and expire are separate activities so a failed (idempotent) expire is
	// retried on its own and never replays the non-idempotent set, which ONTAP
	// would reject as a password reuse. Expire runs only after the set succeeds.
	if err := workflow.ExecuteActivity(ctx, poolActivity.SetOciAdminPasswordOnVSA,
		pool, clusterName, ociExpertModeUsername, params.TempPassword).Get(ctx, nil); err != nil {
		wf.Logger.Error("Failed to set OCI admin password on ONTAP", "poolUUID", pool.UUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	if err := workflow.ExecuteActivity(ctx, poolActivity.ExpireOciAdminPasswordOnVSA,
		pool, clusterName, ociExpertModeUsername).Get(ctx, nil); err != nil {
		wf.Logger.Error("Failed to expire OCI admin password on ONTAP", "poolUUID", pool.UUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	// Persist the temporary-credential state so a reconciler can detect resets
	// that are never consumed within the allowed window. The password itself is
	// never stored; only the active flag and creation timestamp are recorded.
	if err := workflow.ExecuteActivity(ctx, poolActivity.RecordTempPasswordState, pool).Get(ctx, nil); err != nil {
		wf.Logger.Error("Failed to record temporary password state", "poolUUID", pool.UUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	wf.Logger.Info("OCI admin password reset completed", "poolOCID", params.PoolOCID, "poolUUID", pool.UUID, "cluster", clusterName)
	return fmt.Sprintf("reset ociadmin password for pool %s", pool.UUID), nil
}
