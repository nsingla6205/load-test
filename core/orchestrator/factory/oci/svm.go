package oci

import (
	"context"
	"errors"
	"strings"
	"time"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonfactory "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

// CreateSvm starts an async workflow to create an SVM in the pool identified by PoolOCID.
// Returns the workflow ID as operationID/workRequestId for polling. Before creating, validates cluster state/capacity,
// SVM name (convention + uniqueness), and IP requirements.
func (o *OCIOrchestrator) CreateSvm(ctx context.Context, params *commonparams.CreateSvmParams) (operationID string, err error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	temporal := o.temporal

	if err := validateCreateSvmParams(params); err != nil {
		return "", err
	}

	account, err := commonfactory.GetOrCreateAccount(ctx, se, strings.TrimSpace(params.AccountName))
	if err != nil {
		logger.Error("Failed to get or create account for SVM create", "accountName", params.AccountName, "error", err)
		return "", err
	}

	// Default protocols for data LIF calculation (iSCSI enabled by default)
	if !params.EnableIscsi && !params.EnableNfs {
		params.EnableIscsi = true
	}

	deploymentName := GenerateDeploymentNameFromOCID(params.PoolOCID)
	conditions := [][]interface{}{
		{"deployment_name = ?", deploymentName},
		{"account_id = ?", account.ID},
	}
	poolView, err := se.GetPoolByName(ctx, conditions)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", customerrors.NewNotFoundErr("pool not found for PoolOCID", nil)
		}
		return "", err
	}
	pool := database.ConvertPoolViewToPool(poolView)

	svmExternalIdentifier := strings.TrimSpace(params.SvmExternalIdentifier)
	workflowID := resolveWorkflowID(params.WorkflowID)

	exists, err := se.SvmExistsByExternalIdentifier(ctx, svmExternalIdentifier, pool.AccountID)
	if err != nil {
		logger.Error("Failed to check for existing SVM by OCID", "svmOCID", params.SvmExternalIdentifier, "error", err)
		return "", err
	}

	var preallocatedSvm *datamodel.Svm
	if exists {
		existingSvm, getErr := se.GetSvmByExternalIdentifier(ctx, svmExternalIdentifier, pool.AccountID)
		if getErr != nil {
			logger.Error("Failed to load existing SVM by OCID", "svmOCID", params.SvmExternalIdentifier, "error", getErr)
			return "", getErr
		}
		if !(existingSvm.State == datamodel.LifeCycleStateCreating && isCrashResume(existingSvm.WorkflowID, workflowID)) {
			return "", customerrors.NewConflictErr("svmOCID already exists")
		}
		preallocatedSvm = existingSvm

		if upgradeErr := o.validatePoolNotUnderUpgrade(ctx, pool.UUID, params.PoolOCID, "SVM creation"); upgradeErr != nil {
			_ = se.ErroredSVM(ctx, preallocatedSvm, datamodel.LifeCycleStateCreationErrorDetails)
			return "", upgradeErr
		}

		logger.Info("Resuming in-flight CreateSVM after crash", "svmUUID", preallocatedSvm.UUID, "svmOCID", preallocatedSvm.SvmExternalIdentifier, "workflowID", workflowID)
	} else {
		if err := o.validatePoolNotUnderUpgrade(ctx, pool.UUID, params.PoolOCID, "SVM creation"); err != nil {
			return "", err
		}

		logger.Info("No existing SVM with same OCID, proceeding to create",
			"svmOCID", params.SvmExternalIdentifier, "poolOCID", params.PoolOCID)

		if err = validateCreateSvm(ctx, se, params, pool); err != nil {
			return "", err
		}

		svmRow := &datamodel.Svm{
			Name:                  params.Name,
			SvmExternalIdentifier: svmExternalIdentifier,
			AccountID:             pool.AccountID,
			PoolID:                pool.ID,
			WorkflowID:            workflowID,
		}
		preallocatedSvm, err = se.CreateSvmInCreatingState(ctx, svmRow)
		if err != nil {
			logger.Error("Failed to pre-allocate SVM in CREATING state", "svmOCID", params.SvmExternalIdentifier, "error", err)
			return "", err
		}
		logger.Info("SVM pre-allocated in CREATING state", "svmUUID", preallocatedSvm.UUID, "svmOCID", preallocatedSvm.SvmExternalIdentifier)
	}

	defer func() {
		if err != nil && preallocatedSvm != nil {
			compCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if compErr := se.ErroredSVM(compCtx, preallocatedSvm, datamodel.LifeCycleStateCreationErrorDetails); compErr != nil {
				logger.Error("Failed to mark pre-allocated SVM as ERROR after orchestrator-level failure",
					"svmUUID", preallocatedSvm.UUID, "compensationError", compErr)
			}
		}
	}()

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCICreateSVMWorkflow,
		nil, // use default workflow run timeout
		params,
		pool,
		preallocatedSvm,
	)
	if err != nil {
		if isWorkflowAlreadyStarted(err) {
			logger.Info("CreateSVM workflow already started; returning idempotent success", "workflowID", workflowID, "svmOCID", params.SvmExternalIdentifier)
			err = nil
			return workflowID, nil
		}
		logger.Error("Failed to start CreateSVM workflow", "workflowID", workflowID, "error", err)
		return "", err
	}
	// Return workflow ID so caller can use generic workRequest tracking API.
	return workflowID, nil
}

// UpdateSvm starts an async workflow to update an SVM (e.g. admin password rotation).
// if the SVM is already UPDATING with the same workflow ID,
// we skip validation and re-register the workflow with same workflow ID.
func (o *OCIOrchestrator) UpdateSvm(ctx context.Context, params *commonparams.UpdateSvmParams) (operationID string, err error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	temporal := o.temporal

	if err := validateUpdateSvmParams(params); err != nil {
		return "", err
	}

	account, err := commonfactory.GetAccount(ctx, se, strings.TrimSpace(params.AccountName))
	if err != nil {
		logger.Error("Failed to get account for SVM update", "accountName", params.AccountName, "error", err)
		return "", err
	}

	poolConditions := [][]interface{}{
		{"pool_external_identifier = ?", strings.TrimSpace(params.PoolOCID)},
		{"account_id = ?", account.ID},
	}
	poolView, err := se.GetPoolByName(ctx, poolConditions)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			return "", customerrors.NewNotFoundErr("pool not found for PoolOCID", nil)
		}
		return "", err
	}

	targetSvm, err := se.GetSvmByExternalIdentifier(ctx, strings.TrimSpace(params.SvmExternalIdentifier), account.ID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return "", customerrors.NewNotFoundErr("svm not found for svmOCID", nil)
		}
		return "", err
	}
	if targetSvm.PoolID != poolView.ID {
		return "", customerrors.NewNotFoundErr("svm not found for svmOCID", nil)
	}

	if err := validatePoolStateForSvmUpdate(poolView.State); err != nil {
		return "", err
	}

	workflowID := resolveWorkflowID(params.WorkflowID)
	resume := targetSvm.State == datamodel.LifeCycleStateUpdating && isCrashResume(targetSvm.WorkflowID, workflowID)

	if !resume {
		if err := o.validatePoolNotUnderUpgrade(ctx, poolView.UUID, params.PoolOCID, "SVM update"); err != nil {
			return "", err
		}
		if err := validateSvmUpdateState(targetSvm); err != nil {
			return "", err
		}
	} else {
		if upgradeErr := o.validatePoolNotUnderUpgrade(ctx, poolView.UUID, params.PoolOCID, "SVM update"); upgradeErr != nil {
			_ = se.ErroredSVM(ctx, targetSvm, datamodel.LifeCycleStateUpdateErrorDetails)
			return "", upgradeErr
		}
	}

	previousState := targetSvm.State
	previousStateDetails := targetSvm.StateDetails
	pool := database.ConvertPoolViewToPool(poolView)

	if !resume {
		targetSvm.WorkflowID = workflowID
		err = se.UpdateSvmState(ctx, targetSvm,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails,
			[]interface{}{"state IN ?", []string{datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateError}},
		)
		if err != nil {
			if customerrors.IsConflictErr(err) {
				return "", err
			}
			logger.Error("Failed to transition SVM to UPDATING", "svmOCID", params.SvmExternalIdentifier, "error", err)
			return "", err
		}
		logger.Info("SVM transitioned to UPDATING", "svmUUID", targetSvm.UUID, "svmOCID", params.SvmExternalIdentifier)
	} else {
		logger.Info("Resuming UpdateSVM", "svmUUID", targetSvm.UUID, "svmOCID", params.SvmExternalIdentifier, "workflowID", workflowID)
	}

	defer func() {
		if err != nil && targetSvm != nil {
			compCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if compErr := se.UpdateSvmState(compCtx, targetSvm, previousState, previousStateDetails); compErr != nil {
				logger.Error("Failed to revert SVM to previous state",
					"svmUUID", targetSvm.UUID, "previousState", previousState, "compensationError", compErr)
			}
		}
	}()

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCIUpdateSVMWorkflow,
		nil,
		params,
		targetSvm,
		pool,
	)
	if err != nil {
		if isWorkflowAlreadyStarted(err) {
			logger.Info("UpdateSVM workflow already started; returning idempotent success", "workflowID", workflowID, "svmOCID", params.SvmExternalIdentifier)
			err = nil
			return workflowID, nil
		}
		logger.Error("Failed to start UpdateSVM workflow", "workflowID", workflowID, "error", err)
		return "", err
	}
	logger.Info("OCIUpdateSVMWorkflow started", "workflowID", workflowID, "svmOCID", params.SvmExternalIdentifier)
	return workflowID, nil
}

// validateSvmDeletionState ensures the SVM is in a state that allows deletion.
func validateSvmDeletionState(svm *datamodel.Svm) error {
	switch svm.State {
	case datamodel.LifeCycleStateDeleted:
		return customerrors.NewNotFoundErr("svm deleted already", nil)
	case datamodel.LifeCycleStateDeleting:
		return customerrors.NewConflictErr("SVM delete is already in progress")
	case datamodel.LifeCycleStateCreating:
		return customerrors.NewConflictErr("SVM cannot be deleted while creation is in progress")
	}
	return nil
}

// DeleteSvm starts an async workflow to delete an SVM identified by SvmID.
// Returns the workflow ID so the caller can track the operation via workRequest polling.
func (o *OCIOrchestrator) DeleteSvm(ctx context.Context, params *commonparams.DeleteSvmParams) (operationID string, err error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	temporal := o.temporal

	if err := validateDeleteSvmParams(params); err != nil {
		return "", err
	}

	account, err := commonfactory.GetOrCreateAccount(ctx, se, strings.TrimSpace(params.AccountName))
	if err != nil {
		return "", err
	}

	deploymentName := GenerateDeploymentNameFromOCID(strings.TrimSpace(params.PoolOCID))
	poolConditions := [][]interface{}{
		{"deployment_name = ?", deploymentName},
		{"account_id = ?", account.ID},
	}
	poolView, err := se.GetPoolByName(ctx, poolConditions)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", customerrors.NewNotFoundErr("pool not found for PoolOCID", nil)
		}
		return "", err
	}

	targetSvm, err := se.GetSvmByExternalIdentifier(ctx, strings.TrimSpace(params.SvmID), account.ID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return "", customerrors.NewNotFoundErr("svm not found for svmOCID", nil)
		}
		return "", err
	}
	if targetSvm.PoolID != poolView.ID {
		return "", customerrors.NewNotFoundErr("svm not found for svmOCID", nil)
	}

	workflowID := resolveWorkflowID(params.WorkflowID)
	resume := targetSvm.State == datamodel.LifeCycleStateDeleting && isCrashResume(targetSvm.WorkflowID, workflowID)

	if !resume {
		if err := o.validatePoolNotUnderUpgrade(ctx, poolView.UUID, params.PoolOCID, "SVM deletion"); err != nil {
			return "", err
		}
		if err := validateSvmDeletionState(targetSvm); err != nil {
			return "", err
		}
	} else {
		if upgradeErr := o.validatePoolNotUnderUpgrade(ctx, poolView.UUID, params.PoolOCID, "SVM deletion"); upgradeErr != nil {
			_ = se.ErroredSVM(ctx, targetSvm, datamodel.LifeCycleStateDeletionErrorDetails)
			return "", upgradeErr
		}
	}

	pool := database.ConvertPoolViewToPool(poolView)

	deletingSvm := targetSvm
	if !resume {
		targetSvm.WorkflowID = workflowID
		deletingSvm, err = se.TransitionSvmToDeleting(ctx, targetSvm)
		if err != nil {
			if customerrors.IsConflictErr(err) {
				return "", err
			}
			logger.Error("Failed to transition SVM to DELETING", "svmOCID", params.SvmID, "error", err)
			return "", err
		}
		logger.Info("SVM transitioned to DELETING", "svmUUID", deletingSvm.UUID, "svmOCID", params.SvmID)
	} else {
		logger.Info("Resuming in-flight DeleteSVM after crash", "svmUUID", deletingSvm.UUID, "svmOCID", params.SvmID, "workflowID", workflowID)
	}

	defer func() {
		if err != nil && deletingSvm != nil {
			compCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if compErr := se.ErroredSVM(compCtx, deletingSvm, datamodel.LifeCycleStateDeletionErrorDetails); compErr != nil {
				logger.Error("Failed to mark SVM as ERROR after orchestrator-level delete failure",
					"svmUUID", deletingSvm.UUID, "compensationError", compErr)
			}
		}
	}()

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCIDeleteSVMWorkflow,
		nil,
		params,
		deletingSvm,
		pool,
	)
	if err != nil {
		if isWorkflowAlreadyStarted(err) {
			logger.Info("DeleteSVM workflow already started; returning idempotent success", "workflowID", workflowID, "svmOCID", params.SvmID)
			err = nil
			return workflowID, nil
		}
		logger.Error("Failed to start DeleteSVM workflow", "workflowID", workflowID, "error", err)
		return "", err
	}
	logger.Info("OCIDeleteSVMWorkflow started", "workflowID", workflowID, "svmOCID", params.SvmID)
	return workflowID, nil
}
