package kms_workflows

import (
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	SdeKmsJobRetryMaxAttempts = env.GetInt("SDE_KMS_JOBS_RETRY_MAX_ATTEMPTS", 10)
)

type deleteKmsConfigWorkflow struct {
	workflows.BaseWorkflow
}

// Enforcing the WorkflowInterface on kmsDeleteWorkflow
var _ workflows.WorkflowInterface = &deleteKmsConfigWorkflow{}

// DeleteKmsConfigWorkflow process kms config delete request from a customer.
func DeleteKmsConfigWorkflow(ctx workflow.Context, kmsConfig *datamodel.KmsConfig, params *commonparams.DeleteKmsConfigParams) (gcpgenserver.V1betaDeleteKmsConfigurationRes, error) {
	kmsConfigWf := new(deleteKmsConfigWorkflow)
	err := kmsConfigWf.Setup(ctx, params)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	if err = kmsConfigWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	kmsConfigWf.Status = workflows.WorkflowStatusRunning
	err = kmsConfigWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	_, customErr := kmsConfigWf.Run(ctx, kmsConfig, params)
	if customErr != nil {
		kmsConfigWf.Status = workflows.WorkflowStatusFailed
		err = kmsConfigWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, workflows.ConvertToVSAError(err)
	}
	kmsConfigWf.Status = workflows.WorkflowStatusCompleted
	err = kmsConfigWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	return nil, nil
}

func (wf *deleteKmsConfigWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteParams := input.(*commonparams.DeleteKmsConfigParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	logger := util.GetLogger(ctx)
	wf.Logger = logger.With(log.Fields{
		"workflowID": wf.ID,
		"customerID": wf.CustomerID,
	})

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *deleteKmsConfigWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	kmsConfig := args[0].(*datamodel.KmsConfig)
	params := args[1].(*commonparams.DeleteKmsConfigParams)
	deleteActivity := &kms_activities.KmsConfigActivity{}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	defaultActivityOpts := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}

	ctx = workflow.WithActivityOptions(ctx, defaultActivityOpts)

	// Handle cancellation if kms config is in CREATING state
	cancellationActivity := &activities.CancellationActivity{}
	commonActivity := &activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	ackTimeout, forceTimeout := commonparams.GetCancellationTimeouts("KMS_CONFIG")
	if cancelErr := commonparams.HandleCancellationForCreatingResource(ctx, wf.Logger,
		commonparams.HandleCancellationForCreatingResourceParams{
			ResourceUUID:               kmsConfig.UUID,
			ResourceState:              kmsConfig.State,
			CreateJobType:              datamodel.JobTypeCreateKmsConfig,
			SignalName:                 CancelKmsConfigSignalName,
			CancellationAckTimeout:     ackTimeout,
			ForceTerminationAckTimeout: forceTimeout,
		},
		poolActivity.GetCreateJobByResourceUUID,
		cancellationActivity,
		commonActivity,
	); cancelErr != nil {
		wf.Logger.Warnf("Error handling cancellation: %v, proceeding with normal delete", cancelErr)
	}
	defer func() {
		if err != nil && kmsConfig.UUID != "" {
			rollbackCtx, _ := workflow.NewDisconnectedContext(ctx)
			targetState, targetStateDetails := resolveDeleteKmsConfigFailureState(rollbackCtx, err, kmsConfig)
			if restoreErr := workflow.ExecuteActivity(rollbackCtx, deleteActivity.UpdateKmsConfigState, kmsConfig, targetState, targetStateDetails).Get(rollbackCtx, nil); restoreErr != nil {
				util.GetLogger(ctx).Errorf("Failed to restore KMS config state: %v", restoreErr)
			}
		}
	}()

	if !utils.ShouldUseVCPForExistingKMS(kmsConfig) {
		jwtToken := ""
		err = workflow.ExecuteActivity(ctx, deleteActivity.GetSignedTokenActivity, params.AccountName).Get(ctx, &jwtToken)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, jwtToken)

		sdeJobRetryOpts := defaultActivityOpts
		sdeJobRetryOpts.RetryPolicy.MaximumAttempts = int32(SdeKmsJobRetryMaxAttempts)
		ctx1 := workflow.WithActivityOptions(ctx, sdeJobRetryOpts)

		var sdeJobId string
		err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteSDEKmsConfig, kmsConfig, params).Get(ctx, &sdeJobId)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, deleteActivity.DescribeSDEDeleteJob, &sdeJobId, params).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Shared path: disable service account in DB + soft-delete KMS config
	if kmsConfig.UUID != "" {
		err = workflow.ExecuteActivity(ctx, deleteActivity.DisableKmsServiceAccount, kmsConfig).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, deleteActivity.DeleteKmsConfig, kmsConfig, params).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}
	return nil, nil
}

func getDeleteKmsConfigRollbackState(ctx workflow.Context) (string, string, error) {
	commonActivity := &activities.CommonActivities{}
	var job datamodel.Job
	if err := workflow.ExecuteActivity(ctx, commonActivity.GetJob, workflow.GetInfo(ctx).WorkflowExecution.ID).Get(ctx, &job); err != nil {
		return "", "", err
	}
	if job.JobAttributes == nil {
		return "", "", nil
	}
	return job.JobAttributes.PreviousState, job.JobAttributes.PreviousStateDetails, nil
}

func resolveDeleteKmsConfigFailureState(ctx workflow.Context, workflowErr error, kmsConfig *datamodel.KmsConfig) (string, string) {
	if !shouldRestorePreviousStateOnDeleteFailure(workflowErr, kmsConfig) {
		return datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails
	}

	previousState, previousStateDetails, rollbackErr := getDeleteKmsConfigRollbackState(ctx)
	if rollbackErr != nil {
		util.GetLogger(ctx).Errorf("Failed to load rollback state for KMS config delete: %v", rollbackErr)
		return datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails
	}
	if previousState == "" {
		util.GetLogger(ctx).Warnf("Previous KMS config state missing for failed delete, moving config to ERROR: %s", kmsConfig.UUID)
		return datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails
	}

	return previousState, previousStateDetails
}

func shouldRestorePreviousStateOnDeleteFailure(workflowErr error, kmsConfig *datamodel.KmsConfig) bool {
	if workflowErr == nil || kmsConfig == nil || utils.ShouldUseVCPForExistingKMS(kmsConfig) {
		return false
	}

	customErr := vsaerrors.ExtractCustomError(workflowErr)
	hasHTTPCode, httpCode := customErr.GetHttpCode()
	return hasHTTPCode && httpCode == http.StatusConflict
}
