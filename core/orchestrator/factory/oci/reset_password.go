package oci

import (
	"context"
	"fmt"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ResetAdminPasswordForPool resolves the pool by OCID and dispatches the OCI
// reset-password workflow, which sets and then expires the OCI expert-mode
// admin (ociadmin) password on the pool's ONTAP cluster.
//
// This targets the expert-mode user only; it does not touch VCP-managed pool
// credentials or the pool lifecycle state, so (unlike UpdatePool /
// RotateFabricPoolKeys) the pool is not transitioned to UPDATING.
func (o *OCIOrchestrator) ResetAdminPasswordForPool(ctx context.Context, params *commonparams.ResetOciAdminPasswordParams) (string, error) {
	logger := util.GetLogger(ctx)

	if params == nil {
		return "", customerrors.NewBadRequestErr("ResetOciAdminPasswordParams is required")
	}

	pool, err := getPoolByOCID(ctx, o.storage, params.PoolOCID, params.AccountName)
	if err != nil {
		return "", err
	}

	// Only allow the reset on pools that are not being deleted. Resetting the
	// ociadmin password on a DELETING/DELETED pool is pointless and could race
	// with teardown of the ONTAP cluster.
	if pool.State == datamodel.LifeCycleStateDeleting || pool.State == datamodel.LifeCycleStateDeleted {
		logger.Warn("Rejecting reset password request for deleted pool", "poolOCID", params.PoolOCID, "state", pool.State)
		return "", customerrors.NewBadRequestErr(fmt.Sprintf("cannot reset password for pool %s in state %s", params.PoolOCID, pool.State))
	}

	workflowID := resolveWorkflowID(params.WorkflowID)

	wfParams := &commonparams.ResetOciAdminPasswordParams{
		PoolOCID:     params.PoolOCID,
		AccountName:  params.AccountName,
		TempPassword: params.TempPassword,
		WorkflowID:   workflowID,
	}

	workflowExecutor := workflows.NewWorkflowExecutor(o.temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCIResetOciAdminPasswordWorkflow,
		workflowengine.GetResetOciAdminPasswordWorkflowRunTimeout(),
		wfParams,
		pool,
	)
	if err != nil {
		logger.Error("Reset OCI admin password workflow dispatch failed", "workflowID", workflowID, "poolOCID", params.PoolOCID, "error", err)
		return "", fmt.Errorf("failed to dispatch reset password workflow for pool %s: %w", params.PoolOCID, err)
	}

	logger.Info("Reset OCI admin password workflow dispatched", "workflowID", workflowID, "poolOCID", params.PoolOCID, "poolUUID", pool.UUID)
	return workflowID, nil
}
