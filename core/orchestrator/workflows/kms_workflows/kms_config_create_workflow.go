package kms_workflows

import (
	"time"

	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type createKmsConfigWorkflow struct {
	workflows.BaseWorkflow
}

// Enforcing the WorkflowInterface on createKmsConfigWorkflow
var _ workflows.WorkflowInterface = &createKmsConfigWorkflow{}

var (
	cvpMaxPollTimeout                    = env.GetUint64("CVP_JOB_POLL_TIMEOUT_MIN", 10)
	cvpPollInterval                      = env.GetUint64("CVP_JOB_POLL_INTERVAL_SEC", 30)
	kmsCreateActivityStartToCloseTimeout = env.GetDuration("KMS_CREATE_ACTIVITY_START_TO_CLOSE_TIMEOUT", 5*time.Minute)
	kmsCreateActivityHeartbeatTimeout    = env.GetDuration("KMS_CREATE_ACTIVITY_HEARTBEAT_TIMEOUT", 4*time.Minute)
)

const (
	CancelKmsConfigSignalName = "cancel-kms-config-creation"
)

// CreateKmsConfigWorkflow KMS config Workflow process pool related requests from a customer.
func CreateKmsConfigWorkflow(ctx workflow.Context, params *common.CreateKmsConfigParams, kmsConfig *datamodel.KmsConfig) (interface{}, error) {
	kmsConfigWorkflow := new(createKmsConfigWorkflow)
	err := kmsConfigWorkflow.Setup(ctx, params)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	if err = kmsConfigWorkflow.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	kmsConfigWorkflow.Status = workflows.WorkflowStatusRunning
	err = kmsConfigWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	_, customErr := kmsConfigWorkflow.Run(ctx, params, kmsConfig)

	if customErr != nil {
		kmsConfigWorkflow.Status = workflows.WorkflowStatusFailed
		sdeJobUpdateErr := kmsConfigWorkflow.updateSdeJobStatus(ctx, params, datamodel.JobsStateERROR, customErr)
		vcpJobUpdateerr := kmsConfigWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if sdeJobUpdateErr != nil || vcpJobUpdateerr != nil {
			return nil, workflows.ConvertToVSAError(vsaerrors.Combine(sdeJobUpdateErr, vcpJobUpdateerr, customErr))
		}
		return nil, customErr
	}

	kmsConfigWorkflow.Status = workflows.WorkflowStatusCompleted
	sdeJobUpdateErr := kmsConfigWorkflow.updateSdeJobStatus(ctx, params, datamodel.JobsStateDONE, nil)
	vcpJobUpdateerr := kmsConfigWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if sdeJobUpdateErr != nil || vcpJobUpdateerr != nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.Combine(sdeJobUpdateErr, vcpJobUpdateerr))
	}
	return nil, nil
}

func (kmsConfigWorkflow *createKmsConfigWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createKmsConfigParams := input.(*common.CreateKmsConfigParams)
	info := workflow.GetInfo(ctx)
	kmsConfigWorkflow.ID = info.WorkflowExecution.ID
	kmsConfigWorkflow.CustomerID = createKmsConfigParams.AccountName
	kmsConfigWorkflow.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": kmsConfigWorkflow.ID, "customerID": kmsConfigWorkflow.CustomerID})
	logger := util.GetLogger(ctx)
	kmsConfigWorkflow.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         kmsConfigWorkflow.ID,
			Status:     kmsConfigWorkflow.Status,
			CustomerID: kmsConfigWorkflow.CustomerID,
		}, nil
	})
}

func (kmsConfigWorkflow *createKmsConfigWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.CreateKmsConfigParams)
	kmsConfig := args[1].(*datamodel.KmsConfig)
	kmsConfigActivity := &kms_activities.KmsConfigActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: kmsCreateActivityStartToCloseTimeout,
		HeartbeatTimeout:    kmsCreateActivityHeartbeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}

	ctx = workflow.WithActivityOptions(ctx, ao)
	cancellationHandler := common.NewWorkflowCancellationHandler(ctx, CancelKmsConfigSignalName, kmsConfig.UUID, "kms-config")

	rollbackManager := common.NewRollbackManager()
	defer func() {
		common.ExecuteDeferredCleanup(ctx, cancellationHandler, rollbackManager, err, kmsConfigWorkflow.Logger, "kms config", kmsConfig.UUID,
			func(disconnectedCtx workflow.Context) error {
				rollbackManager.AddActivity(kmsConfigActivity.FailedKmsConfigCreateActivity, kmsConfig, err.Error(), params.LocationID)
				return nil
			},
			func(disconnectedCtx workflow.Context, cancelErr error) {
				rollbackManager.AddActivity(kmsConfigActivity.FailedKmsConfigCreateActivity, kmsConfig, "kms config creation cancelled by delete request", params.LocationID)
			},
			nil) // shouldRollbackOnError
	}()

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	isVCPCreated := kmsConfig.KmsAttributes != nil && kmsConfig.KmsAttributes.IsVCPCreated()

	// VCP-created config path: create GCP service account directly in CMEK global project.
	// SDE-created config path: get JWT, poll SDE operation, describe SDE config, update attributes.
	if isVCPCreated {
		// Create GCP service account in the CMEK global project
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CreateGCPServiceAccountActivity, kmsConfig).Get(ctx, kmsConfig)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Enable the service account in case it was disabled by a previous delete flow
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.EnableGCPServiceAccountActivity, kmsConfig).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	} else {
		jwtToken := ""
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.GetSignedTokenActivity, params.ProjectNumber).Get(ctx, &jwtToken)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, jwtToken)

		pollingOptions := workflow.ActivityOptions{
			StartToCloseTimeout: time.Duration(cvpMaxPollTimeout) * time.Minute,
			HeartbeatTimeout:    kmsCreateActivityHeartbeatTimeout,
			RetryPolicy: &temporal.RetryPolicy{
				BackoffCoefficient:     retryPolicy.BackoffCoefficient,
				InitialInterval:        time.Duration(cvpPollInterval) * time.Second,
				NonRetryableErrorTypes: []string{"PanicError"},
			},
		}
		pollingCtx := workflow.WithActivityOptions(ctx, pollingOptions)

		pollKmsConfigParams := &common.PollKmsConfigParams{
			OperationUri:   params.OperationUri,
			OperationDone:  params.OperationDone,
			ProjectNumber:  params.ProjectNumber,
			LocationID:     params.LocationID,
			XCorrelationID: params.XCorrelationID,
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(pollingCtx, kmsConfigActivity.PollKmsConfigOperationActivity, pollKmsConfigParams).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		getKmsConfigParams := &common.GetKmsConfigParams{
			UUID:          params.UUID,
			LocationID:    params.LocationID,
			ProjectNumber: params.ProjectNumber,
		}
		var cvpKmsConfig cvpmodels.KmsConfigV1beta
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.DescribeSDEKmsConfigurationActivity, getKmsConfigParams).Get(ctx, &cvpKmsConfig)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		kmsConfig.KmsAttributes.SdeKmsConfigUUID = cvpKmsConfig.UUID
		kmsConfig.KmsAttributes.SdeServiceAccountEmail = cvpKmsConfig.ServiceAccountEmail
		kmsConfig.KmsAttributes.Instructions = cvpKmsConfig.Instructions
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.UpdateKmsConfigAttributesActivity, kmsConfig, kmsConfig.KmsAttributes).Get(ctx, kmsConfig)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Create the service account key for the KMS configuration (shared)
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CreateVSAKmsConfigSAKeyActivity, kmsConfig).Get(ctx, kmsConfig)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Grant roles — only needed for SDE-created configs (impersonation from SDE SA to VCP SA).
	if !isVCPCreated {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.GrantRoleActivity, kmsConfig).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Mark KMS config as created (shared)
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CreatedKmsConfigActivity, kmsConfig).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	return kmsConfig, nil
}

func (kmsConfigWorkflow *createKmsConfigWorkflow) RevertCreateKmsConfigWorkflow(ctx workflow.Context) error {
	// Implement the revert logic for kms config workflows
	// This might involve rolling back any changes made during the workflow execution
	return nil
}

func (kmsConfigWorkflow *createKmsConfigWorkflow) updateSdeJobStatus(ctx workflow.Context, params *common.CreateKmsConfigParams, status datamodel.JobState, customErr *vsaerrors.CustomError) error {
	if params == nil || params.SdeJobUUID == "" {
		return nil
	}

	sdeJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: params.SdeJobUUID},
		State:     string(status),
	}

	if customErr != nil {
		sdeJob.TrackingID = customErr.TrackingID
		if customErr.OriginalErr != nil {
			sdeJob.ErrorDetails = customErr.OriginalErr.Error()
		}
	}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return workflows.ConvertToVSAError(err)
	}

	commonActivity := activities.CommonActivities{}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})

	return workflow.ExecuteActivity(ctx, commonActivity.UpdateJobStatus, sdeJob).Get(ctx, nil)
}
