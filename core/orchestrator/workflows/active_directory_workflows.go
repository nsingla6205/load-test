package workflows

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	CancelActiveDirectorySignalName = "cancel-active-directory-creation"
)

type ActiveDirectoryCreateWorkflow struct {
	BaseWorkflow
}

func CreateActiveDirectoryWorkflow(
	ctx workflow.Context,
	params *common.CreateActiveDirectoryParams,
	adRecord *datamodel.ActiveDirectory,
) (interface{}, error) {
	log := util.GetLogger(ctx)
	activeDirectoryWf := new(ActiveDirectoryCreateWorkflow)

	err := activeDirectoryWf.Setup(ctx, params)
	if err != nil {
		log.Errorf("Failed to setup ActiveDirectoryCreateWorkflow: %v", err)
		return nil, err
	}

	activeDirectoryWf.Status = WorkflowStatusRunning
	err = activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Processing for ActiveDirectoryCreateWorkflow: %v", err)
		return nil, err
	}

	_, customErr := activeDirectoryWf.Run(ctx, params, adRecord)
	if customErr != nil {
		log.Errorf("ActiveDirectoryCreateWorkflow completed with error: %v", customErr)
		activeDirectoryWf.Status = WorkflowStatusFailed
		err2 := activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Errorf("Failed to update job status to Done with error for ActiveDirectoryCreateWorkflow: %v", err2)
			return nil, err2
		}
		return nil, customErr
	}

	activeDirectoryWf.Status = WorkflowStatusCompleted
	err = activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Done for ActiveDirectoryCreateWorkflow: %v", err)
	}
	return nil, err
}

func (wf *ActiveDirectoryCreateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createAdParams := input.(*common.CreateActiveDirectoryParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createAdParams.AccountId
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": wf.ID,
		"customerID": wf.CustomerID,
	})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ActiveDirectoryCreateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := util.GetLogger(ctx)
	activeDirectoryActivity := &active_directory_activities.ActiveDirectoryCreateActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	params := args[0].(*common.CreateActiveDirectoryParams)
	adRecord := args[1].(*datamodel.ActiveDirectory)

	rollbackManager := common.NewRollbackManager()
	rollbackManager.AddActivity(activeDirectoryActivity.RollbackActiveDirectory, adRecord)
	defer func() {
		// Trigger the rollback only if there was an error, and we are not in SDE mode
		if err != nil && (cvp.CVP_HOST == "") {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	if cvp.CVP_HOST == "" {
		logger.Info("CVP_HOST environment variable is not set, creating AD in VCP")
		err = workflow.ExecuteActivity(
			ctx,
			activeDirectoryActivity.CreateVcpActiveDirectory,
			params,
			adRecord,
		).Get(ctx, nil)
	} else {
		ctx, err = FetchAndSetAuthToken(ctx, params.AccountId, logger)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(
			ctx,
			activeDirectoryActivity.CreateSdeActiveDirectory,
			params,
		).Get(ctx, nil)
	}

	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}

type ActiveDirectoryDeleteWorkflow struct {
	BaseWorkflow
}

func DeleteActiveDirectoryWorkflow(ctx workflow.Context, params *common.DeleteActiveDirectoryParams) (interface{}, error) {
	log := util.GetLogger(ctx)
	activeDirectoryWf := new(ActiveDirectoryDeleteWorkflow)

	err := activeDirectoryWf.Setup(ctx, params)
	if err != nil {
		log.Errorf("Failed to setup ActiveDirectoryDeleteWorkflow: %v", err)
		return nil, err
	}
	if err = activeDirectoryWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, err
	}

	activeDirectoryWf.Status = WorkflowStatusRunning
	err = activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Processing for ActiveDirectoryDeleteWorkflow: %v", err)
		return nil, err
	}

	_, customErr := activeDirectoryWf.Run(ctx, params)
	if customErr != nil {
		log.Errorf("ActiveDirectoryDeleteWorkflow completed with error: %v", customErr)
		activeDirectoryWf.Status = WorkflowStatusFailed
		err2 := activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Errorf("Failed to update job status to Done with error for ActiveDirectoryDeleteWorkflow: %v", err2)
			return nil, err2
		}
		return nil, customErr
	}

	activeDirectoryWf.Status = WorkflowStatusCompleted
	err = activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Done for ActiveDirectoryDeleteWorkflow: %v", err)
	}
	return nil, err
}

func (wf *ActiveDirectoryDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteAdParams := input.(*common.DeleteActiveDirectoryParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteAdParams.ProjectNumber
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": wf.ID,
		"customerID": wf.CustomerID,
	})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ActiveDirectoryDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := util.GetLogger(ctx)
	activeDirectoryActivity := &active_directory_activities.ActiveDirectoryDeleteActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	params := args[0].(*common.DeleteActiveDirectoryParams)

	logger.Info("Starting Active Directory delete workflow", "active_directory_uuid", params.ActiveDirectoryUUID)

	var checkResult active_directory_activities.CheckDeletionAllowedResult
	err = workflow.ExecuteActivity(
		ctx,
		activeDirectoryActivity.CheckDeletionAllowed,
		params,
	).Get(ctx, &checkResult)

	// Handle the check result
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to check if deletion is allowed: %v", err))
		return nil, ConvertToVSAError(err)
	}

	if !checkResult.DeletionAllowed {
		// AD found at VCP but deletion not allowed (SVMs using it)
		err1 := customerrors.Errorf("active directory deletion is not allowed - ad is in use")
		logger.Error(err1.Error())
		return nil, ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrActiveDirectoryDeleteErrorDueToInUseByPool, err1))
	}

	// Step 1: Check if SDE is enabled (CVP_HOST is set)
	if cvp.CVP_HOST != "" {
		logger.Debug("SDE is enabled")

		ctx, err = FetchAndSetAuthToken(ctx, params.ProjectNumber, logger)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Step 2: Check if AD can be deleted at VCP (check existence and SVM associations)
		if !checkResult.ADExists {
			// AD not found at VCP - trigger delete at SDE
			logger.Info("AD not found at VCP, attempting deletion at SDE")
			err = workflow.ExecuteActivity(
				ctx,
				activeDirectoryActivity.DeleteSdeActiveDirectory,
				params,
			).Get(ctx, nil)
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to delete Active Directory from SDE: %v", err))
				return nil, ConvertToVSAError(err)
			}
			logger.Info("Successfully completed Active Directory delete workflow (not found at VCP, deleted from SDE if present)")
			return nil, nil
		}

		// AD found at VCP and deletion is allowed
		logger.Info("AD found at VCP and deletion is allowed, deleting from both SDE and VCP")

		// Delete from SDE first
		err = workflow.ExecuteActivity(
			ctx,
			activeDirectoryActivity.DeleteSdeActiveDirectory,
			params,
		).Get(ctx, nil)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to delete Active Directory from SDE: %v", err))
			return nil, ConvertToVSAError(err)
		}

		// Then delete from VCP
		err = workflow.ExecuteActivity(
			ctx,
			activeDirectoryActivity.DeleteVcpActiveDirectory,
			params,
		).Get(ctx, nil)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to delete Active Directory from VCP: %v", err))
			return nil, ConvertToVSAError(err)
		}

		logger.Info("Successfully completed Active Directory delete workflow (deleted from both SDE and VCP)")
		return nil, nil
	}

	// SDE is disabled - only delete from VCP
	logger.Info("Deleting AD from VCP only")

	// Delete from VCP
	err = workflow.ExecuteActivity(
		ctx,
		activeDirectoryActivity.DeleteVcpActiveDirectory,
		params,
	).Get(ctx, nil)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to delete Active Directory from VCP: %v", err))
		return nil, ConvertToVSAError(err)
	}

	logger.Info("Successfully completed Active Directory delete workflow (deleted from VCP)")
	return nil, nil
}

type ActiveDirectoryUpdateWorkflow struct {
	BaseWorkflow
}

func UpdateActiveDirectoryWorkflow(
	ctx workflow.Context,
	params *common.UpdateActiveDirectoryParams,
	adRecord *models.ActiveDirectory,
) (interface{}, error) {
	log := util.GetLogger(ctx)
	activeDirectoryWf := new(ActiveDirectoryUpdateWorkflow)

	err := activeDirectoryWf.Setup(ctx, params)
	if err != nil {
		log.Errorf("Failed to setup ActiveDirectoryUpdateWorkflow: %v", err)
		return nil, err
	}

	activeDirectoryWf.Status = WorkflowStatusRunning
	err = activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Processing for ActiveDirectoryUpdateWorkflow: %v", err)
		return nil, err
	}

	_, customErr := activeDirectoryWf.Run(ctx, params, adRecord)
	if customErr != nil {
		log.Errorf("ActiveDirectoryUpdateWorkflow completed with error: %v", customErr)
		activeDirectoryWf.Status = WorkflowStatusFailed
		err2 := activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			log.Errorf("Failed to update job status to Done with error for ActiveDirectoryUpdateWorkflow: %v", err2)
			return nil, err2
		}
		return nil, customErr
	}

	activeDirectoryWf.Status = WorkflowStatusCompleted
	err = activeDirectoryWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Done for ActiveDirectoryUpdateWorkflow: %v", err)
	}
	return nil, err
}

func (wf *ActiveDirectoryUpdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	updateAdParams := input.(*common.UpdateActiveDirectoryParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = updateAdParams.AccountId
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": wf.ID,
		"customerID": wf.CustomerID,
	})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ActiveDirectoryUpdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := util.GetLogger(ctx)
	activeDirectoryActivity := &active_directory_activities.ActiveDirectoryUpdateActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	if len(args) < 2 {
		return nil, ConvertToVSAError(vsaerrors.New("insufficient arguments provided to workflow"))
	}
	params := args[0].(*common.UpdateActiveDirectoryParams)
	oldAd := args[1].(*models.ActiveDirectory)

	if cvp.CVP_HOST == "" {
		logger.Info("CVP_HOST environment variable is not set, Updating AD in VCP only")
		err = wf.handleVcpUpdate(ctx, activeDirectoryActivity, params, oldAd)
		if err != nil {
			logger.Errorf("Failed to update Active Directory in VCP: %v", err)
			return nil, ConvertToVSAError(err)
		}
	} else {
		logger.Info("CVP_HOST environment variable is set, Updating AD in SDE first, then VCP (if applicable)")

		ctx, err = FetchAndSetAuthToken(ctx, params.AccountId, logger)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		var sdeResult *cvpModels.OperationV1beta
		// Trigger SDE update first
		err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.UpdateSdeActiveDirectory, params).Get(ctx, &sdeResult)
		if err != nil {
			logger.Errorf("Failed to update Active Directory in SDE: %v", err)
			return nil, ConvertToVSAError(err)
		}

		if sdeResult == nil {
			logger.Errorf("Failed to update Active Directory in SDE: SDE result is nil")
			return nil, ConvertToVSAError(vsaerrors.New("SDE Result is nil"))
		}

		// Prepare to Poll the SDE update status
		pollingOptions := workflow.ActivityOptions{
			StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:        retryPolicy.InitialInterval,
				BackoffCoefficient:     retryPolicy.BackoffCoefficient,
				MaximumInterval:        retryPolicy.MaximumInterval,
				MaximumAttempts:        int32(maxRetryAttemptsForSDEPollJob),
				NonRetryableErrorTypes: []string{"NonRetryableError", "PanicError"},
			},
		}
		pollingCtx := workflow.WithActivityOptions(ctx, pollingOptions)

		// Poll the SDE Update Operation until completion
		err = workflow.ExecuteActivity(pollingCtx, activeDirectoryActivity.PollSdeUpdateActivity, params, sdeResult).Get(pollingCtx, nil)
		if err != nil {
			logger.Errorf("SDE polling failed: %v, Skipping next steps", err)
			return nil, ConvertToVSAError(err)
		}
		logger.Info("SDE AD Update operation completed successfully")

		// Only proceed to VCP update if SDE update succeeded
		logger.Info("SDE update successful, proceeding with VCP update")
		// In SDE flow, VCP AD Db record is not marked to updating earlier, thus marking as updating here.
		err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.MarkVcpAdToUpdatingActivity, params, oldAd).Get(ctx, nil)
		if err != nil {
			logger.Errorf("Failed to mark Active Directory in VCP to Updating State: %v", err)
			return nil, ConvertToVSAError(err)
		}

		err = wf.handleVcpUpdate(ctx, activeDirectoryActivity, params, oldAd)
		if err != nil {
			logger.Errorf("Failed to update Active Directory in VCP: %v", err)
			return nil, ConvertToVSAError(err)
		}
	}

	return nil, nil
}

func (wf *ActiveDirectoryUpdateWorkflow) handleVcpUpdate(
	ctx workflow.Context,
	activeDirectoryActivity *active_directory_activities.ActiveDirectoryUpdateActivity,
	params *common.UpdateActiveDirectoryParams,
	oldAd *models.ActiveDirectory,
) error {
	logger := util.GetLogger(ctx)

	rollbackManager := common.NewRollbackManager()
	rollbackManager.AddActivity(activeDirectoryActivity.MarkVcpAdToErrorActivity, params, oldAd)

	var err error
	defer func() {
		// Trigger the rollback only if there was an error
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	// Change ID to be populated both at AD table and pool table
	vcpActiveDirectoryChangeId := utils.RandomUUID()

	// Push updates to pool and SVMs first using a separate workflow
	logger.Info("Pushing updates to pool and SVMs before VCP DB AD update")
	err = PushAdUpdatesToSVMWorkflow(ctx, oldAd, params, vcpActiveDirectoryChangeId)
	if err != nil {
		logger.Errorf("Failed to push Active Directory updates to pool and SVMs: %v", err)
		return err
	}

	// Get state based on active SVMs using this AD (same as Create Volume flow: IN_USE when in use, READY when not)
	adActivity := &active_directory_activities.ActiveDirectoryActivity{}
	var stateResult common.ActiveDirectoryStateResult
	err = workflow.ExecuteActivity(ctx, adActivity.GetActiveDirectoryStateFromSVMUsage, oldAd.ID).Get(ctx, &stateResult)
	if err != nil {
		logger.Errorf("Failed to get Active Directory state from SVM usage: %v", err)
		return err
	}

	// Then update VCP with the computed state
	logger.Info("Updating VCP Active Directory")
	err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.UpdateVcpActiveDirectory, params, oldAd, vcpActiveDirectoryChangeId, stateResult.State, stateResult.StateDetails).Get(ctx, nil)
	if err != nil {
		logger.Errorf("Failed to update Active Directory in VCP: %v", err)
		return err
	}

	return nil
}

func PushAdUpdatesToSVMWorkflow(ctx workflow.Context, oldAd *models.ActiveDirectory, params *common.UpdateActiveDirectoryParams, adChangeId string) error {
	log := util.GetLogger(ctx)
	activeDirectoryActivity := &active_directory_activities.ActiveDirectoryActivity{}

	// Validate inputs
	if oldAd == nil {
		log.Error("Active Directory is nil in PushAdUpdatesToSVMWorkflow")
		return vsaerrors.New("Active Directory is nil")
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return ConvertToVSAError(err)
	}

	// Set activity options
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 1: Fetch SVMs associated with the Active Directory
	var svms []*datamodel.Svm
	err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.GetSvmsForAd, oldAd.ID).Get(ctx, &svms)
	if err != nil {
		log.Errorf("Failed to fetch svms for Active Directory %s: %v", oldAd.AdName, err)
		return err
	}

	// Step 2: Process SVMs in batches of 10
	if len(svms) == 0 {
		log.Info("No SVMs found for Active Directory, skipping downstream updates")
		return nil
	}

	// Step 3: Generate update parameters
	var updateAdCredentialsForSvmParams *vsa.UpdateActiveDirectoryCredentialsParams
	err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.GenerateUpdateAdCredentialsParams, oldAd, params).Get(ctx, &updateAdCredentialsForSvmParams)
	if err != nil {
		log.Error("Failed to get active directory during PushAdUpdatesToSVMWorkflow with error: ", err)
		return ConvertToVSAError(err)
	}

	log.Infof("Processing %d SVMs in batches of 10", len(svms))
	batchSize := 10
	for i := 0; i < len(svms); i += batchSize {
		end := i + batchSize
		if end > len(svms) {
			end = len(svms)
		}

		svmBatch := svms[i:end]
		log.Infof("Processing SVM batch %d-%d", i+1, end)

		// Process each pool in the current batch
		for _, svm := range svmBatch {
			// Fetch Pool
			var pool datamodel.Pool
			err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetPoolBySvmPoolId, svm.PoolID).Get(ctx, &pool)
			if err != nil {
				return ConvertToVSAError(err)
			}

			// Step 3.1: Fetch Node details for this pool
			var dbNodes []*datamodel.Node
			err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, pool.ID).Get(ctx, &dbNodes)
			if err != nil {
				return ConvertToVSAError(err)
			}

			node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
				Nodes:            dbNodes,
				DeploymentName:   pool.DeploymentName,
				OntapCredentials: pool.PoolCredentials,
			})

			var cifs ontapRest.CifsService
			err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.GetCifsService, node, svm.Name, svm.SvmDetails.ExternalUUID).Get(ctx, &cifs)
			if err != nil {
				return ConvertToVSAError(err)
			}

			// Step 3.2: Update AD credentials for this SVM
			err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.UpdateAdCredentialsForSvm, node, updateAdCredentialsForSvmParams, svm.Name, svm.SvmDetails.ExternalUUID, cifs).Get(ctx, nil)
			if err != nil {
				return ConvertToVSAError(err)
			}

			// Step 3.3: Propagate Change ID to Pool
			err = workflow.ExecuteActivity(ctx, activeDirectoryActivity.PropagateAdChangeIdToPool, pool, adChangeId).Get(ctx, nil)
			if err != nil {
				return ConvertToVSAError(err)
			}
		}
	}

	log.Info("Successfully pushed Active Directory updates to all pools and SVMs")
	return nil
}
