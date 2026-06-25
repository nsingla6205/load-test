package vlm

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	temporalUtils "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var NewVSAClientWorkflowManager = _newVSAClientWorkflowManager

var (
	VSALifecycleManagerQueuePrefix = env.GetString("VSA_LIFECYCLE_MANAGER_QUEUE_PREFIX", "vsa-lifecycle-manager")
	// ExtractedOntapVersion is to be used for determining vlm task queue version
	ExtractedOntapVersion             = utils.ExtractOntapVersion(env.GetString("ONTAP_VERSION_DETAILS", "9.17.1P2"))
	VSALifecycleManagerQueue          = fmt.Sprintf("%s-%s", VSALifecycleManagerQueuePrefix, ExtractedOntapVersion)
	IsIntegrationTest                 = env.GetBool("INTEGRATION_TEST", false)
	VlmWorkflowStartToCloseTimeout    = env.GetString("VLMWORKFLOW_START_TO_CLOSE_WORKFLOW_TIMEOUT", "20m")
	VlmWorkflowRetryInterval          = env.GetString("VLMWORKFLOW_RETRY_INTERVAL", "1m")
	VlmWorkflowRetryMaxAttempts       = env.GetInt("VLMWORKFLOW_RETRY_MAX_ATTEMPTS", 3)
	VlmWorkflowRetryMaxInterval       = env.GetString("VLMWORKFLOW_RETRY_MAX_INTERVAL", "5m")
	VlmWorkflowRetryBackoff           = env.GetString("VLMWORKFLOW_RETRY_BACKOFF_COEFFICIENT", "2.0")
	MinLvHAPair                       = env.GetInt("NUMBER_OF_HA_PAIRS_LARGE_CAPACITY", 6) // Minimum HA pairs to trigger large capacity workflow time logic
	CreateVSAClusterLargeCapacityTime = time.Duration(env.GetInt("VLM_CREATE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES_LV", 45)) * time.Minute

	// RetryErrorPatterns Configurable error patterns that trigger delete and retry operations
	RetryErrorPatterns = getRetryErrorPatterns()
)

const accountIDLabelKey = "account_id"

const VLMCloudProvider = "gcp"
const AccountName = "AccountName"
const expertMode = "exp-mode"

// getRetryErrorPatterns returns the list of error patterns that trigger delete and retry operations
func getRetryErrorPatterns() []string {
	// Try to get from environment variable first
	patternsStr := env.GetString("VLM_RETRY_ERROR_PATTERNS", "")
	if patternsStr == "" {
		// No patterns configured, return empty slice
		return []string{}
	}

	// Parse comma-separated string
	patterns := strings.Split(patternsStr, ",")
	// Trim whitespace from each pattern
	for i, pattern := range patterns {
		patterns[i] = strings.TrimSpace(pattern)
	}
	return patterns
}

type WorkflowRetryPolicy struct {
	InitialInterval     time.Duration
	BackoffCoefficient  float64
	MaximumInterval     time.Duration
	MaximumAttempts     int
	StartToCloseTimeout time.Duration
}

type VlmWorkflowClient interface {
	CreateVSAClusterDeployment(ctx workflow.Context, createVSAClusterDeploymentRequest *CreateVSAClusterDeploymentRequest, taskQueue string) (*CreateVSAClusterDeploymentResponse, error)
	CreateVSASVM(ctx workflow.Context, createSVMRequest *CreateSVMRequest) (*CreateSVMResponse, error)
	DeleteVSASVM(ctx workflow.Context, deleteSVMRequest *DeleteSVMRequest) (*DeleteSVMResponse, error)
	ModifyVSASVMWorkflow(ctx workflow.Context, req *ModifySVMRequest) (*ModifySVMResponse, error)
	DeleteVSAClusterDeployment(ctx workflow.Context, deleteVSAClusterDeploymentRequest *DeleteVSAClusterDeploymentRequest, ontapVersion string) error
	UpdateVSAClusterDeployment(ctx workflow.Context, updateVSAClusterDeploymentRequest *UpdateVSAClusterDeploymentRequest, ontapVersion string) (*UpdateVSAClusterDeploymentResponse, error)
	ValidateClusterHealth(ctx workflow.Context, validateClusterHealthRequest *ValidateClusterHealthRequest) error
	ClusterPowerOp(ctx workflow.Context, clusterPowerOpRequest *ClusterPowerOpReq) error
	UpgradeVSAClusterDeploymentWorkflow(ctx workflow.Context, req *UpdateVSAClusterDeploymentRequest) (*UpgradeVSAClusterDeploymentResponse, error)
	UpgradeVSAMediatorWorkflow(ctx workflow.Context, req *UpdateMediatorRequest) (*UpdateMediatorResponse, error)
	UpdateLicenseWorkflow(ctx workflow.Context, req *UpdateLicenseRequest) error
	GetClusterZiZsDetails(ctx workflow.Context, req *GetResourceInfoReq) (*GetResourceInfoResp, error)
	CreateVSAExpertModeUser(ctx workflow.Context, createVSAExpertModeUserRequest *OntapExpertModeUserConfig) (OntapExpertModeUserResponse, error)
	ZoneSwitch(ctx workflow.Context, req *ZoneSwitchRequest) (*ZoneSwitchResponse, error)
	RotateFabricPoolKeys(ctx workflow.Context, req *RotateFabricPoolKeysRequest) (*RotateFabricPoolKeysResponse, error)
}

type VSAClientWorkflowManager struct {
}

func _newVSAClientWorkflowManager() VlmWorkflowClient {
	if IsIntegrationTest {
		return &VSAClientWorkflowManagerMock{}
	}
	return &VSAClientWorkflowManager{}
}

// GetVLMWorkerQueue returns the VLM worker queue name based on the account and ONTAP version
func GetVLMWorkerQueue(logger log.Logger, account string) string {
	ontapVersion := utils.GetOntapVersionBasedOnAllowlisting(account)
	return fmt.Sprintf("%s-%s", VSALifecycleManagerQueuePrefix, utils.ExtractOntapVersion(ontapVersion))
}
func (vlmManager *VSAClientWorkflowManager) CreateVSAExpertModeUser(ctx workflow.Context, createVSAExpertModeUserRequest *OntapExpertModeUserConfig) (OntapExpertModeUserResponse, error) {
	ontapExpertModeUserResponse := OntapExpertModeUserResponse{}
	logger := util.GetLogger(ctx)
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return ontapExpertModeUserResponse, err
	}
	accountId := createVSAExpertModeUserRequest.VLMConfig.Deployment.Labels[accountIDLabelKey]

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[CreateVSAExpertModeUserWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            createVSAExpertModeUserRequest.VLMConfig.Deployment.DeploymentID + expertMode,
		TaskQueue:             GetVLMWorkerQueue(logger, accountId),           // As VLM workflows are executed in a VSALifecycleManagerQueue queue
		WaitForCancellation:   true,                                           // The parent workflow waits until the child workflow is fully canceled (it finishes whatever it needs to do after being canceled).
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE, // Allows reuse of workflow ID
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return ontapExpertModeUserResponse, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, createVSAExpertModeUserRequest.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, CreateVSAExpertModeUserWorkflowName, createVSAExpertModeUserRequest).Get(childWorkflowContxt, &ontapExpertModeUserResponse)

	if err != nil {
		logger.Error("Failed to create expertModeUser", "error", err)
		vlmErrorHandler := NewVLMErrorHandlerWithLogger(logger)
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return ontapExpertModeUserResponse, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return ontapExpertModeUserResponse, nil
}

func (vlmManager *VSAClientWorkflowManager) CreateVSAClusterDeployment(ctx workflow.Context, createVSAClusterDeploymentRequest *CreateVSAClusterDeploymentRequest, taskQueue string) (*CreateVSAClusterDeploymentResponse, error) {
	logger := util.GetLogger(ctx)

	if taskQueue == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("taskQueue cannot be empty")))
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	accountID := createVSAClusterDeploymentRequest.VLMConfig.Deployment.Labels[accountIDLabelKey]

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[CreateVSAClusterDeploymentWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	if createVSAClusterDeploymentRequest.VLMConfig.Deployment.NumHAPair >= MinLvHAPair {
		workflowExecutionTimeout = CreateVSAClusterLargeCapacityTime
	}
	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID, // This ensures that each child workflow has a unique identifier, even if the same Deployment ID is used across different zones
		TaskQueue:             taskQueue,                                                           // As VLM workflows are executed in a VSALifecycleManagerQueue queue
		WaitForCancellation:   true,                                                                // The parent workflow waits until the child workflow is fully canceled (it finishes whatever it needs to do after being canceled).
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,          // Allows reuse only if the previous execution did not complete successfully (e.g., failed, timed out, terminated, or cancelled)
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	createVSAClusterDeploymentResponse := &CreateVSAClusterDeploymentResponse{}

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, CreateVSAClusterDeploymentWorkflowName, createVSAClusterDeploymentRequest).Get(childWorkflowContxt, &createVSAClusterDeploymentResponse)

	if err != nil {
		logger.Error("Failed to create VSA cluster", "error", err)

		// Check if error contains configured strings that require delete and retry
		if len(RetryErrorPatterns) > 0 {
			shouldRetry := checkRetryError(logger, err)

			if shouldRetry {
				logger.Info("Detected configured error pattern, attempting delete and retry",
					"deploymentID", createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID)

				// Delete existing deployment
				deleteRequest := &DeleteVSAClusterDeploymentRequest{
					DeploymentID:  createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID,
					ProjectID:     createVSAClusterDeploymentRequest.VLMConfig.Deployment.GCPConfig.ProjectID,
					CloudProvider: VLMCloudProvider,
				}

				ontapVersion := utils.ExtractOntapVersion(utils.GetOntapVersionBasedOnAllowlisting(accountID))

				deleteErr := vlmManager.DeleteVSAClusterDeployment(ctx, deleteRequest, ontapVersion)
				if deleteErr == nil {
					logger.Info("Successfully deleted deployment, retrying creation",
						"deploymentID", createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID)

					// Retry creation
					// using new retryChildWorkflowContxt to ensure a new child workflow is created with a unique ID
					// using the same Deployment ID with the same parent workflow would also work as the previous execution failed
					// but since we couldn't reproduce the issue reliably, using a new child workflow ID for retry to be safe
					retryChildWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
						WorkflowID:            createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID + "-1", // This ensures that each child workflow has a unique identifier, even if the same Deployment ID is used across different zones
						TaskQueue:             taskQueue,                                                                  // As VLM workflows are executed in a VSALifecycleManagerQueue queue
						WaitForCancellation:   true,                                                                       // The parent workflow waits until the child workflow is fully canceled (it finishes whatever it needs to do after being canceled).
						WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,                 // Allows reuse only if the previous execution did not complete successfully (e.g., failed, timed out, terminated, or cancelled)
						RetryPolicy: &temporal.RetryPolicy{
							InitialInterval:    retryPolicy.InitialInterval,
							BackoffCoefficient: retryPolicy.BackoffCoefficient,
							MaximumInterval:    retryPolicy.MaximumInterval,
							MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
						},
						WorkflowExecutionTimeout: workflowExecutionTimeout,
					})
					// Add correlation and deployment IDs to context
					retryChildWorkflowContxt = workflow.WithValue(retryChildWorkflowContxt, CorrelationIDKey, correlationID)
					retryChildWorkflowContxt = workflow.WithValue(retryChildWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)
					retryChildWorkflowContxt = workflow.WithValue(retryChildWorkflowContxt, DeploymentIDKey, createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID)

					retryErr := workflow.ExecuteChildWorkflow(retryChildWorkflowContxt, CreateVSAClusterDeploymentWorkflowName, createVSAClusterDeploymentRequest).Get(retryChildWorkflowContxt, &createVSAClusterDeploymentResponse)
					if retryErr == nil {
						logger.Info("Successfully created VSA cluster after retry",
							"deploymentID", createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID)
						return createVSAClusterDeploymentResponse, nil
					}
				} else {
					logger.Warn("Failed to delete existing deployment during retry, continuing with normal error handling",
						"deploymentID", createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID,
						"deleteError", deleteErr)
				}
			}
		}

		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandlerWithLogger(logger)

		handledErr := vlmErrorHandler.HandleVLMError(err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return createVSAClusterDeploymentResponse, nil
}

func (vlmManager *VSAClientWorkflowManager) CreateVSASVM(ctx workflow.Context, createSVMRequest *CreateSVMRequest) (*CreateSVMResponse, error) {
	logger := util.GetLogger(ctx)

	if createSVMRequest == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("createSVMRequest cannot be nil")))
	}

	if createSVMRequest.VLMConfig.Deployment.DeploymentID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("deployment ID is required to create SVM")))
	}

	if createSVMRequest.Name == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("SVM name is required to create SVM")))
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	accountID := createSVMRequest.VLMConfig.Deployment.Labels[accountIDLabelKey]

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[CreateVSASVMWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             GetVLMWorkerQueue(logger, accountID),
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	createSVMResponse := &CreateSVMResponse{}

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, createSVMRequest.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, CreateVSASVMWorkflowName, createSVMRequest).Get(childWorkflowContxt, &createSVMResponse)
	if err != nil {
		logger.Error("Failed to create SVM", "error", err)
		if strings.Contains(err.Error(), "already exists and is in use by a different VM") {
			return nil, nil
		}
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return createSVMResponse, nil
}

func (vlmManager *VSAClientWorkflowManager) DeleteVSASVM(ctx workflow.Context, deleteSVMRequest *DeleteSVMRequest) (*DeleteSVMResponse, error) {
	logger := util.GetLogger(ctx)

	if deleteSVMRequest == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("deleteSVMRequest cannot be nil")))
	}

	if deleteSVMRequest.VLMConfig.Deployment.DeploymentID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("deployment ID is required to delete SVM")))
	}

	if deleteSVMRequest.Name == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("SVM name is required to delete SVM")))
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	accountID := deleteSVMRequest.VLMConfig.Deployment.Labels[accountIDLabelKey]

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[DeleteVSASVMWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             GetVLMWorkerQueue(logger, accountID),
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	deleteSVMResponse := &DeleteSVMResponse{}

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, deleteSVMRequest.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, DeleteVSASVMWorkflowName, deleteSVMRequest).Get(childWorkflowContxt, &deleteSVMResponse)
	if err != nil {
		logger.Error("Failed to delete SVM", "error", err)
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return deleteSVMResponse, nil
}

func (vlmManager *VSAClientWorkflowManager) ModifyVSASVMWorkflow(ctx workflow.Context, req *ModifySVMRequest) (*ModifySVMResponse, error) {
	logger := util.GetLogger(ctx)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	accountID := req.VLMConfig.Deployment.Labels[accountIDLabelKey]

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[ModifyVSASVMWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             GetVLMWorkerQueue(logger, accountID),
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	modifySVMResponse := &ModifySVMResponse{}

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, req.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, ModifyVSASVMWorkflowName, req).Get(childWorkflowContxt, &modifySVMResponse)
	if err != nil {
		logger.Error("Failed to modify SVM", "error", err)
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return modifySVMResponse, nil
}

func (vlmManager *VSAClientWorkflowManager) DeleteVSAClusterDeployment(ctx workflow.Context, deleteVSAClusterDeploymentRequest *DeleteVSAClusterDeploymentRequest, ontapVersion string) error {
	logger := util.GetLogger(ctx)

	if deleteVSAClusterDeploymentRequest.ProjectID == "" {
		logger.Warnf("Skipping VSA cluster deployment,cannot delete VSA cluster deployment without project ID")
		return nil
	}

	if deleteVSAClusterDeploymentRequest.DeploymentID == "" {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("deployment ID is required to delete pool")))
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return err
	}

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[DeleteVSAClusterDeploymentWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}
	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             VSALifecycleManagerQueuePrefix + "-" + ontapVersion,
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, deleteVSAClusterDeploymentRequest.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, DeleteVSAClusterDeploymentWorkflowName, deleteVSAClusterDeploymentRequest).Get(childWorkflowContxt, nil)
	if err != nil {
		logger.Error("Failed to delete VSA cluster", "error", err)
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return nil
}

func (vlmManager *VSAClientWorkflowManager) UpdateVSAClusterDeployment(ctx workflow.Context, updateVSAClusterDeploymentRequest *UpdateVSAClusterDeploymentRequest, ontapVersion string) (*UpdateVSAClusterDeploymentResponse, error) {
	logger := util.GetLogger(ctx)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[UpdateVSAClusterDeploymentWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}
	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             VSALifecycleManagerQueuePrefix + "-" + ontapVersion,
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	updateVSAClusterDeploymentResponse := &UpdateVSAClusterDeploymentResponse{}

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, updateVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, UpdateVSAClusterDeploymentWorkflowName, updateVSAClusterDeploymentRequest).Get(childWorkflowContxt, &updateVSAClusterDeploymentResponse)

	if err != nil {
		logger.Error("Failed to update VSA cluster", "error", err)
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return updateVSAClusterDeploymentResponse, nil
}

func (vlmManager *VSAClientWorkflowManager) UpgradeVSAClusterDeploymentWorkflow(ctx workflow.Context, req *UpdateVSAClusterDeploymentRequest) (*UpgradeVSAClusterDeploymentResponse, error) {
	logger := util.GetLogger(ctx)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return &UpgradeVSAClusterDeploymentResponse{}, err
	}

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[UpgradeVSAClusterDeploymentWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}
	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             VSALifecycleManagerQueuePrefix + "-" + ExtractedOntapVersion,
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	upgradeVSAClusterDeploymentResponse := &UpgradeVSAClusterDeploymentResponse{}

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return &UpgradeVSAClusterDeploymentResponse{}, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, req.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, UpgradeVSAClusterDeploymentWorkflowName, req).Get(childWorkflowContxt, &upgradeVSAClusterDeploymentResponse)

	if err != nil {
		logger.Error("Failed to upgrade VSA cluster", "error", err)
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return &UpgradeVSAClusterDeploymentResponse{}, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return upgradeVSAClusterDeploymentResponse, nil
}

// UpgradeVSAMediatorWorkflow upgrades a VSA mediator
func (vlmManager *VSAClientWorkflowManager) UpgradeVSAMediatorWorkflow(ctx workflow.Context, req *UpdateMediatorRequest) (*UpdateMediatorResponse, error) {
	logger := util.GetLogger(ctx)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[UpdateVSAMediatorWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}
	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             VSALifecycleManagerQueuePrefix + "-" + ExtractedOntapVersion,
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	upgradeVSAMediatorResponse := &UpdateMediatorResponse{}

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, req.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, UpdateVSAMediatorWorkflowName, req).Get(childWorkflowContxt, upgradeVSAMediatorResponse)

	if err != nil {
		logger.Error("Failed to upgrade VSA mediator", "error", err)
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return &UpdateMediatorResponse{}, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return upgradeVSAMediatorResponse, nil
}

func (vlmManager *VSAClientWorkflowManager) GetClusterZiZsDetails(ctx workflow.Context, req *GetResourceInfoReq) (*GetResourceInfoResp, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Starting GetClusterZiZsDetails workflow", "projectID", req.ProjectID, "deploymentID", req.DeploymentID)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	// Extract account ID from project ID (assuming project ID contains account info)
	// If project ID doesn't contain account info, we'll use a default account
	var accountId string
	if ctx.Value(AccountName) != nil {
		accountId = ctx.Value(AccountName).(string)
	}

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[GetClusterZiZsDetailsWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             GetVLMWorkerQueue(logger, accountId),
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		// Generate fallback correlation ID if not present
		correlationID = fmt.Sprintf("deployment_%s_time_%s", req.DeploymentID, time.Now().Format("20060102150405"))
		logger.Info("Generated fallback correlation ID", "correlationID", correlationID)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, req.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)	

	var resp GetResourceInfoResp
	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, GetClusterZiZsDetailsWorkflowName, req).Get(childWorkflowContxt, &resp)
	if err != nil {
		logger.Error("Failed to execute GetClusterZiZsDetails workflow", "error", err)
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	logger.Info("GetClusterZiZsDetails workflow completed successfully",
		"projectID", req.ProjectID,
		"deploymentID", req.DeploymentID)

	return &resp, nil
}

func PopulateRetryPolicyParams() (*WorkflowRetryPolicy, error) {
	activityStartToCloseTimeout, err := time.ParseDuration(VlmWorkflowStartToCloseTimeout)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}

	activityRetryInterval, err := time.ParseDuration(VlmWorkflowRetryInterval)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}

	activityRetryMaxAttempts := VlmWorkflowRetryMaxAttempts

	activityRetryMaxInterval, err := time.ParseDuration(VlmWorkflowRetryMaxInterval)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}

	activityRetryBackoff, err := strconv.ParseFloat(VlmWorkflowRetryBackoff, 64)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}

	return &WorkflowRetryPolicy{
		InitialInterval:     activityRetryInterval,
		StartToCloseTimeout: activityStartToCloseTimeout,
		BackoffCoefficient:  activityRetryBackoff,
		MaximumInterval:     activityRetryMaxInterval,
		MaximumAttempts:     activityRetryMaxAttempts,
	}, nil
}

// checkRetryError checks if the error message or cause contains any of the configured retry error patterns
func checkRetryError(logger log.Logger, err error) bool {
	if err == nil {
		return false
	}

	// Check the main error message
	errMsg := strings.ToLower(err.Error())
	for _, pattern := range RetryErrorPatterns {
		if strings.Contains(errMsg, strings.ToLower(pattern)) {
			// log the matched pattern and error message for debugging
			logger.Info("Matched retry error pattern", "pattern", pattern, "error", errMsg)
			return true
		}
	}

	// Check if it's a temporal application error and extract cause
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		// Check if it's a VLM client error with cause information
		if appErr.Type() == "VLMClientError" {
			var vlmClientErr VLMClientError
			if appErr.HasDetails() && appErr.Details(&vlmClientErr) == nil {
				// Check each cause string
				for _, cause := range vlmClientErr.Cause {
					causeStr := strings.ToLower(cause)
					for _, pattern := range RetryErrorPatterns {
						if strings.Contains(causeStr, strings.ToLower(pattern)) {
							logger.Info("Matched retry error pattern in cause", "pattern", pattern, "cause", causeStr)
							return true
						}
					}
				}
			}
		}
	}

	return false
}

func (vlmManager *VSAClientWorkflowManager) ValidateClusterHealth(ctx workflow.Context, validateClusterHealthRequest *ValidateClusterHealthRequest) error {
	logger := util.GetLogger(ctx)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return err
	}

	accountID := validateClusterHealthRequest.VLMConfig.Deployment.Labels[accountIDLabelKey]

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[ClusterHealthCheckWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowID := fmt.Sprintf("%s-health-check-%d-%s", validateClusterHealthRequest.VLMConfig.Deployment.DeploymentID, workflow.Now(ctx).UnixNano(), workflow.GetInfo(ctx).WorkflowExecution.ID)
	logger.Info("Creating ValidateClusterHealth child workflow", "deploymentID", validateClusterHealthRequest.VLMConfig.Deployment.DeploymentID, "childWorkflowID", childWorkflowID, "accountID", accountID)

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            childWorkflowID,
		TaskQueue:             GetVLMWorkerQueue(logger, accountID),
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, validateClusterHealthRequest.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, ClusterHealthCheckWorkflowName, validateClusterHealthRequest).Get(childWorkflowContxt, nil)
	if err != nil {
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return nil
}

func (vlmManager *VSAClientWorkflowManager) ClusterPowerOp(ctx workflow.Context, clusterPowerOpRequest *ClusterPowerOpReq) error {
	logger := util.GetLogger(ctx)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return err
	}

	accountID := clusterPowerOpRequest.VLMConfig.Deployment.Labels[accountIDLabelKey]

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[ClusterPowerCycleWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowID := fmt.Sprintf("%s-power-op-%d-%s", clusterPowerOpRequest.VLMConfig.Deployment.DeploymentID, workflow.Now(ctx).UnixNano(), workflow.GetInfo(ctx).WorkflowExecution.ID)
	logger.Info("Creating ClusterPowerOp child workflow", "deploymentID", clusterPowerOpRequest.VLMConfig.Deployment.DeploymentID, "operation", clusterPowerOpRequest.Operation, "childWorkflowID", childWorkflowID, "accountID", accountID)

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            childWorkflowID,
		TaskQueue:             GetVLMWorkerQueue(logger, accountID),
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, clusterPowerOpRequest.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, ClusterPowerCycleWorkflowName, clusterPowerOpRequest).Get(childWorkflowContxt, nil)
	if err != nil {
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	return nil
}

// UpdateLicenseWorkflow updates the ONTAP license for a VSA cluster
func (vlmManager *VSAClientWorkflowManager) UpdateLicenseWorkflow(ctx workflow.Context, req *UpdateLicenseRequest) error {
	logger := util.GetLogger(ctx)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return err
	}

	// Get workflow execution timeout for UpdateLicense workflow
	workflowExecutionTimeout, exists := WorkflowExecutionTimeoutMap[UpdateLicenseWorkflowName]
	if !exists {
		workflowExecutionTimeout = WorkflowExecutionTimeoutMap["DefaultWorkflowExecutionTimeout"]
	}

	// Generate child workflow ID
	childWorkflowID := fmt.Sprintf("update-license-%s-%d", req.VSAManagementIP, time.Now().Unix())

	logger.Info("Creating UpdateLicense child workflow", "vsaManagementIP", req.VSAManagementIP, "childWorkflowID", childWorkflowID)

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            childWorkflowID,
		TaskQueue:             VSALifecycleManagerQueue,
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	// Add correlation ID to context if available
	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
	} else {
		opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
		childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
		childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)
	}

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, UpdateLicenseWorkflowName, req).Get(childWorkflowContxt, nil)
	if err != nil {
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	logger.Info("UpdateLicense child workflow completed successfully", "vsaManagementIP", req.VSAManagementIP)
	return nil
}

func (vlmManager *VSAClientWorkflowManager) ZoneSwitch(ctx workflow.Context, req *ZoneSwitchRequest) (*ZoneSwitchResponse, error) {
	logger := util.GetLogger(ctx)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[ZoneSwitchWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowContxt := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             VSALifecycleManagerQueuePrefix + "-" + ExtractedOntapVersion,
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	zoneSwitchResponse := &ZoneSwitchResponse{}

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Add correlation and deployment IDs to context
	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, CorrelationIDKey, correlationID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, DeploymentIDKey, req.VLMConfig.Deployment.DeploymentID)
	childWorkflowContxt = workflow.WithValue(childWorkflowContxt, OpcWorkRequestIDKey, opcWorkRequestID)

	err = workflow.ExecuteChildWorkflow(childWorkflowContxt, ZoneSwitchWorkflowName, req).Get(childWorkflowContxt, &zoneSwitchResponse)
	if err != nil {
		logger.Error("Failed to Zone Switch: ", "error", err)
		// Handle VLM-specific errors and convert them to user-facing errors
		vlmErrorHandler := NewVLMErrorHandler()
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	logger.Info("ZoneSwitch workflow completed successfully")
	return zoneSwitchResponse, nil
}

func (vlmManager *VSAClientWorkflowManager) RotateFabricPoolKeys(ctx workflow.Context, req *RotateFabricPoolKeysRequest) (*RotateFabricPoolKeysResponse, error) {
	logger := util.GetLogger(ctx)

	if req == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("RotateFabricPoolKeysRequest cannot be nil")))
	}
	if req.NewSecretOcid == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("NewSecretOcid is required to rotate fabric pool keys")))
	}
	if req.VLMConfig.Deployment.DeploymentID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("deployment ID is required to rotate fabric pool keys")))
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	accountID := req.VLMConfig.Deployment.Labels[accountIDLabelKey]

	workflowExecutionTimeout := temporalUtils.GetWorkflowGlobalTimeout()
	if timeout, ok := WorkflowExecutionTimeoutMap[RotateFabricPoolKeysWorkflowName]; ok {
		workflowExecutionTimeout = timeout
	}

	childWorkflowID := fmt.Sprintf("%s-rotate-fabric-pool-keys-%d-%s",
		req.VLMConfig.Deployment.DeploymentID, workflow.Now(ctx).UnixNano(), workflow.GetInfo(ctx).WorkflowExecution.ID)
	logger.Info("Creating RotateFabricPoolKeys child workflow",
		"deploymentID", req.VLMConfig.Deployment.DeploymentID,
		"childWorkflowID", childWorkflowID,
		"accountID", accountID,
		"newSecretOcid", req.NewSecretOcid)

	childWorkflowCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            childWorkflowID,
		TaskQueue:             GetVLMWorkerQueue(logger, accountID),
		WaitForCancellation:   true,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
		WorkflowExecutionTimeout: workflowExecutionTimeout,
	})

	correlationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx)
	if err != nil {
		logger.Error("Failed to get correlation ID from workflow context logger fields", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	opcWorkRequestID, _ := utils.GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx)
	childWorkflowCtx = workflow.WithValue(childWorkflowCtx, CorrelationIDKey, correlationID)
	childWorkflowCtx = workflow.WithValue(childWorkflowCtx, DeploymentIDKey, req.VLMConfig.Deployment.DeploymentID)
	childWorkflowCtx = workflow.WithValue(childWorkflowCtx, OpcWorkRequestIDKey, opcWorkRequestID)

	rotateResp := &RotateFabricPoolKeysResponse{}
	if err = workflow.ExecuteChildWorkflow(childWorkflowCtx, RotateFabricPoolKeysWorkflowName, req).Get(childWorkflowCtx, rotateResp); err != nil {
		logger.Error("Failed to rotate fabric pool keys", "error", err)
		vlmErrorHandler := NewVLMErrorHandlerWithLogger(logger)
		handledErr := vlmErrorHandler.HandleVLMError(err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(handledErr)
	}

	logger.Info("RotateFabricPoolKeys child workflow completed successfully",
		"deploymentID", req.VLMConfig.Deployment.DeploymentID,
		"childWorkflowID", childWorkflowID)
	return rotateResp, nil
}
