package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

const (
	ociExecutePoolMaintenanceWorkflowType = "OCIExecutePoolMaintenanceWorkflow"
)

func (o *OCIOrchestrator) StartPoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ExecutePoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (string, error) {
	return o.dispatchPoolMaintenance(ctx, params, ociExecutePoolMaintenanceWorkflowType)
}

func (o *OCIOrchestrator) FinishPoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) dispatchPoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams, workflowType string) (string, error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	temporal := o.temporal

	if params == nil {
		return "", customerrors.NewBadRequestErr("PoolMaintenanceParams is required")
	}

	account, err := common.GetAccount(ctx, se, params.AccountName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			accountName := params.AccountName
			return "", customerrors.NewNotFoundErr("account", &accountName)
		}
		logger.Error("Failed to load account for pool maintenance",
			"accountName", params.AccountName, "poolOCID", params.PoolOCID, "error", err)
		return "", err
	}

	conditions := [][]interface{}{{"pool_external_identifier = ?", params.PoolOCID}}
	conditions = append(conditions, []interface{}{"account_id = ?", account.ID})
	poolView, err := se.GetPoolByName(ctx, conditions)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			poolOCID := params.PoolOCID
			return "", customerrors.NewNotFoundErr("pool", &poolOCID)
		}
		return "", err
	}

	if err = validateInstancesBelongToPool(poolView.VLMConfig, params.InstanceOCIDs); err != nil {
		logger.Error("pool maintenance instance membership validation failed, instances do not belong to the pool",
			"poolOCID", params.PoolOCID, "error", err)
		return "", err
	}

	workflowID := resolveWorkflowID(params.WorkflowID)
	resume := poolView.State == datamodel.LifeCycleStateUpdating && isCrashResume(poolView.WorkflowID, workflowID)

	if !resume {
		if err = validatePoolMaintenanceState(poolView.State, params.PoolOCID); err != nil {
			logger.Error("pool is not in a state that allows maintenance",
				"poolOCID", params.PoolOCID, "state", poolView.State, "error", err)
			return "", err
		}
		if err = o.validateNoActiveClusterUpgrade(ctx, poolView.UUID, params.PoolOCID); err != nil {
			logger.Error("active cluster upgrade in progress; cannot start pool maintenance",
				"poolOCID", params.PoolOCID, "error", err)
			return "", err
		}
	} else if err = o.validateNoActiveClusterUpgrade(ctx, poolView.UUID, params.PoolOCID); err != nil {
		logger.Error("active cluster upgrade in progress during pool maintenance resume",
			"poolOCID", params.PoolOCID, "error", err)
		pool := database.ConvertPoolViewToPool(poolView)
		if _, rbErr := se.ErroredResource(ctx, pool, err.Error()); rbErr != nil {
			logger.Error("Failed to transition pool to ERROR after upgrade block on resume",
				"poolOCID", params.PoolOCID, "error", rbErr)
		}
		return "", err
	}

	pool := database.ConvertPoolViewToPool(poolView)
	pool.WorkflowID = workflowID
	params.WorkflowID = workflowID

	if !resume {
		pool, err = se.UpdatingPool(ctx, pool)
		if err != nil {
			logger.Error("Failed to transition pool to UPDATING for maintenance",
				"poolOCID", params.PoolOCID, "error", err)
			return "", err
		}
	}

	defer func() {
		if err != nil {
			if _, rbErr := se.ErroredResource(ctx, pool, err.Error()); rbErr != nil {
				logger.Error("Failed to rollback pool to ERROR state after maintenance dispatch failure",
					"poolOCID", params.PoolOCID, "error", rbErr)
			}
		}
	}()

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		workflowType,
		nil,
		params,
		pool,
	)
	if err != nil {
		if isWorkflowAlreadyStarted(err) {
			logger.Info("OCI pool maintenance workflow already started; returning idempotent success",
				"workflowID", workflowID, "poolOCID", params.PoolOCID, "workflowType", workflowType)
			err = nil
			return workflowID, nil
		}
		logger.Error("Failed to start OCI pool maintenance workflow",
			"workflowID", workflowID, "poolOCID", params.PoolOCID, "workflowType", workflowType, "error", err)
		return "", err
	}

	logger.Info("OCI pool maintenance workflow started",
		"workflowID", workflowID, "poolOCID", params.PoolOCID, "workflowType", workflowType)
	return workflowID, nil
}

func validatePoolMaintenanceState(state, poolOCID string) error {
	switch state {
	case datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateError:
		return nil
	case datamodel.LifeCycleStateDeleted:
		return customerrors.NewBadRequestErr(fmt.Sprintf("pool %s is deleted and cannot undergo maintenance", poolOCID))
	case datamodel.LifeCycleStateUpdating,
		datamodel.LifeCycleStateCreating,
		datamodel.LifeCycleStateDeleting,
		datamodel.LifeCycleStatePreparing,
		datamodel.LifeCycleStateMigrating:
		return customerrors.NewConflictErr(fmt.Sprintf("pool maintenance cannot start: an operation is already in progress (state=%s)", state))
	default:
		return customerrors.NewConflictErr(fmt.Sprintf("pool maintenance cannot start in state %s, must be READY or ERROR after a failed maintenance", state))
	}
}

func validateInstancesBelongToPool(vlmConfigJSON string, instanceOCIDs []string) error {
	if strings.TrimSpace(vlmConfigJSON) == "" {
		return customerrors.NewNotReadyErr("vlm config is not available for the pool")
	}

	var cfg vlm.VLMConfig
	if err := json.Unmarshal([]byte(vlmConfigJSON), &cfg); err != nil {
		return customerrors.NewNotReadyErr(fmt.Sprintf("failed to unmarshal vlm config: %v", err))
	}

	known := make(map[string]struct{})
	for _, pair := range cfg.Cloud.HAPairs {
		for _, vm := range []vlm.VMConfig{pair.VM1, pair.VM2, pair.Mediator} {
			if id := strings.TrimSpace(vm.VMInstanceID); id != "" {
				known[id] = struct{}{}
			}
		}
	}

	for _, ocid := range instanceOCIDs {
		if _, ok := known[strings.TrimSpace(ocid)]; !ok {
			return customerrors.NewNotFoundErr("instance", &ocid)
		}
	}
	return nil
}
