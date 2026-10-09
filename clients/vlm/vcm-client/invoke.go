// Package vcmclient invokes the VLM workflows as child workflows from the VCM
// pool-provisioning workflow.
package vcmclient

import (
	"errors"

	vlm "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
)

// expertModeWorkflowIDSuffix distinguishes the expert-mode user child workflow
// from the other VLM children dispatched for the same deployment. The leading
// "-" matches the other children's convention ("-create-cluster",
// "-update-cluster", "-get-cluster") and keeps the deployment id a readable
// prefix rather than running straight into the suffix.
const expertModeWorkflowIDSuffix = "-exp-mode"

// wfLogger resolves the engine-neutral workflow logger from the orchestrator
// context, so log lines carry the workflow's correlation/deployment fields
// instead of coming from a package-global logger. Both workflow backends
// register a logger factory in init(), so at workflow runtime this always
// resolves; the error path is unreachable and panics to surface the
// misconfiguration immediately (mirroring the backend context helpers).
func wfLogger(ctx *orchestrator.Context) orchestrator.UnifiedLogger {
	logger, err := orchestrator.WorkflowLoggerFromContext(ctx.BackendCtx())
	if err != nil {
		panic("vcmclient: no workflow logger factory registered: " + err.Error())
	}
	return logger
}

// toRetryPolicy maps the VLM workflow retry policy onto the orchestrator one.
func toRetryPolicy(rp *vlm.WorkflowRetryPolicy) *orchestrator.RetryPolicy {
	return &orchestrator.RetryPolicy{
		InitialInterval:    rp.InitialInterval,
		BackoffCoefficient: rp.BackoffCoefficient,
		MaximumInterval:    rp.MaximumInterval,
		MaximumAttempts:    int32(rp.MaximumAttempts),
	}
}

// InvokeCreateClusterDeployment runs the VLM CreateVSAClusterDeploymentWorkflow
// as a child workflow.
func InvokeCreateClusterDeployment(ctx *orchestrator.Context, req *vlm.CreateVSAClusterDeploymentRequest) (*vlm.CreateVSAClusterDeploymentResponse, error) {
	retryPolicy, err := vlm.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	workflowExecutionTimeout := vlm.WorkflowExecutionTimeoutMap[vlm.CreateVSAClusterDeploymentWorkflowName]
	if req.VLMConfig.Deployment.NumHAPair >= vlm.MinLvHAPair {
		workflowExecutionTimeout = vlm.CreateVSAClusterLargeCapacityTime
	}

	childCtx := ctx.WithChildWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowID:               req.VLMConfig.Deployment.DeploymentID + "-create-cluster",
		WorkflowExecutionTimeout: workflowExecutionTimeout,
		RetryPolicy:              toRetryPolicy(retryPolicy),
	})
	childCtx = orchestrator.WithValue(childCtx, vlm.DeploymentIDKey, req.VLMConfig.Deployment.DeploymentID)

	logger := wfLogger(childCtx)
	logger.Info("dispatching VLM child workflow",
		"workflowName", vlm.CreateVSAClusterDeploymentWorkflowName,
		"workflowID", req.VLMConfig.Deployment.DeploymentID,
		"executionTimeout", workflowExecutionTimeout.String())
	resp := &vlm.CreateVSAClusterDeploymentResponse{}
	if err := orchestrator.ExecuteChildWorkflow(childCtx, vlm.CreateVSAClusterDeploymentWorkflowName, req).Get(childCtx, resp); err != nil {
		logger.Error("VLM create-cluster child workflow failed",
			"workflowID", req.VLMConfig.Deployment.DeploymentID, "error", err.Error())
		handledErr := vlm.NewVLMErrorHandler().HandleVLMError(err)
		return nil, apperror.WrapAsAppError(handledErr, false)
	}
	return resp, nil
}

// InvokeUpdateClusterDeployment runs the VLM UpdateVSAClusterDeploymentWorkflow
// as a child workflow. COMPUTE sets NewInstanceType and keeps current SPConfig;
// SCALE applies the target SPConfig and leaves NewInstanceType empty.
func InvokeUpdateClusterDeployment(ctx *orchestrator.Context, req *vlm.UpdateVSAClusterDeploymentRequest) (*vlm.UpdateVSAClusterDeploymentResponse, error) {
	retryPolicy, err := vlm.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	deploymentID := req.VLMConfig.Deployment.DeploymentID
	workflowExecutionTimeout := vlm.WorkflowExecutionTimeoutMap[vlm.UpdateVSAClusterDeploymentWorkflowName]
	if req.NumHAPair >= vlm.MinLvHAPair {
		workflowExecutionTimeout = vlm.CreateVSAClusterLargeCapacityTime
	}

	childCtx := ctx.WithChildWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowID:               deploymentID + "-update-cluster",
		WorkflowExecutionTimeout: workflowExecutionTimeout,
		RetryPolicy:              toRetryPolicy(retryPolicy),
	})
	childCtx = orchestrator.WithValue(childCtx, vlm.DeploymentIDKey, deploymentID)

	logger := wfLogger(childCtx)
	logger.Info("dispatching VLM child workflow",
		"workflowName", vlm.UpdateVSAClusterDeploymentWorkflowName,
		"workflowID", deploymentID+"-update-cluster",
		"newInstanceType", req.NewInstanceType,
		"executionTimeout", workflowExecutionTimeout.String())
	resp := &vlm.UpdateVSAClusterDeploymentResponse{}
	if err := orchestrator.ExecuteChildWorkflow(childCtx, vlm.UpdateVSAClusterDeploymentWorkflowName, req).Get(childCtx, resp); err != nil {
		logger.Error("VLM update-cluster child workflow failed",
			"workflowID", deploymentID+"-update-cluster", "error", err.Error())
		handledErr := vlm.NewVLMErrorHandler().HandleVLMError(err)
		return nil, apperror.WrapAsAppError(handledErr, false)
	}
	return resp, nil
}

// InvokeCreateSVM runs the VLM CreateVSASVMWorkflow as a child workflow.
func InvokeCreateSVM(ctx *orchestrator.Context, req *vlm.CreateSVMRequest) (*vlm.CreateSVMResponse, error) {
	if req.VLMConfig.Deployment.DeploymentID == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("deployment ID is required to create SVM")), true)
	}
	if req.Name == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, errors.New("SVM name is required to create SVM")), true)
	}

	retryPolicy, err := vlm.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	childCtx := ctx.WithChildWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowExecutionTimeout: vlm.WorkflowExecutionTimeoutMap[vlm.CreateVSASVMWorkflowName],
		RetryPolicy:              toRetryPolicy(retryPolicy),
	})
	childCtx = orchestrator.WithValue(childCtx, vlm.DeploymentIDKey, req.VLMConfig.Deployment.DeploymentID)

	resp := &vlm.CreateSVMResponse{}
	if err := orchestrator.ExecuteChildWorkflow(childCtx, vlm.CreateVSASVMWorkflowName, req).Get(childCtx, resp); err != nil {
		wfLogger(childCtx).Error("Failed to create SVM", "error", err.Error())
		handledErr := vlm.NewVLMErrorHandler().HandleVLMError(err)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(handledErr, &customErr) && customErr.IsError(vsaerrors.ErrGCPResourceAlreadyExistsError) {
			return nil, nil
		}
		return nil, apperror.WrapAsAppError(handledErr, false)
	}
	return resp, nil
}

// InvokeCreateExpertModeUser runs the VLM expert-mode user workflow as a child
// workflow of VCM pool creation. The child workflow ID is keyed on the
// deployment, so only one such child can be active per cluster.
func InvokeCreateExpertModeUser(ctx *orchestrator.Context, req *vlm.OntapExpertModeUserConfig) (*vlm.OntapExpertModeUserResponse, error) {
	if req == nil || req.VLMConfig.Deployment.DeploymentID == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
				errors.New("deployment ID is required to create ONTAP mode user")), true)
	}
	return invokeExpertModeUser(ctx, req.VLMConfig.Deployment.DeploymentID+expertModeWorkflowIDSuffix, req)
}

// InvokeRefreshExpertModeUser runs the VLM expert-mode user workflow as a child
// workflow of a VCM RBAC refresh. The child workflow ID is keyed on the refresh
// request ID rather than the deployment, so refreshes with different request
// IDs run independently; whether to issue them concurrently is the caller's
// decision.
func InvokeRefreshExpertModeUser(ctx *orchestrator.Context, requestID string, req *vlm.OntapExpertModeUserConfig) (*vlm.OntapExpertModeUserResponse, error) {
	if req == nil || req.VLMConfig.Deployment.DeploymentID == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
				errors.New("deployment ID is required to refresh ONTAP mode user RBAC")), true)
	}
	if requestID == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
				errors.New("request ID is required to refresh ONTAP mode user RBAC")), true)
	}
	return invokeExpertModeUser(ctx, requestID+expertModeWorkflowIDSuffix, req)
}

func invokeExpertModeUser(ctx *orchestrator.Context, workflowID string, req *vlm.OntapExpertModeUserConfig) (*vlm.OntapExpertModeUserResponse, error) {
	retryPolicy, err := vlm.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	deploymentID := req.VLMConfig.Deployment.DeploymentID
	workflowExecutionTimeout := vlm.WorkflowExecutionTimeoutMap[vlm.CreateVSAExpertModeUserWorkflowName]
	childCtx := ctx.WithChildWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowID:               workflowID,
		WorkflowExecutionTimeout: workflowExecutionTimeout,
		RetryPolicy:              toRetryPolicy(retryPolicy),
	})
	childCtx = orchestrator.WithValue(childCtx, vlm.DeploymentIDKey, deploymentID)

	logger := wfLogger(childCtx)
	logger.Info("dispatching VLM child workflow",
		"workflowName", vlm.CreateVSAExpertModeUserWorkflowName,
		"workflowID", workflowID,
		"executionTimeout", workflowExecutionTimeout.String())

	resp := &vlm.OntapExpertModeUserResponse{}
	if err := orchestrator.ExecuteChildWorkflow(childCtx, vlm.CreateVSAExpertModeUserWorkflowName, req).Get(childCtx, resp); err != nil {
		logger.Error("VLM ONTAP mode user child workflow failed",
			"workflowID", workflowID, "error", err.Error())
		handledErr := vlm.NewVLMErrorHandler().HandleVLMError(err)
		return nil, apperror.WrapAsAppError(handledErr, false)
	}
	return resp, nil
}

// InvokeGetClusterDeployment runs the VLM GetVSAClusterDeployment workflow as a
// child workflow to resolve the current VLM configuration for a deployment.
func InvokeGetClusterDeployment(ctx *orchestrator.Context, req *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	return invokeGetClusterDeployment(ctx, req.DeploymentID+getClusterWorkflowIDSuffix, req)
}

// InvokeRefreshGetClusterDeployment is InvokeGetClusterDeployment for an RBAC
// refresh. Its child workflow ID is keyed on the refresh request ID, so
// refreshes the caller runs concurrently do not collide on one child.
func InvokeRefreshGetClusterDeployment(ctx *orchestrator.Context, requestID string, req *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	if requestID == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
				errors.New("request ID is required to read the cluster for an RBAC refresh")), true)
	}
	return invokeGetClusterDeployment(ctx, requestID+getClusterWorkflowIDSuffix, req)
}

const getClusterWorkflowIDSuffix = "-get-cluster"

func invokeGetClusterDeployment(ctx *orchestrator.Context, workflowID string, req *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	retryPolicy, err := vlm.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	childCtx := ctx.WithChildWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowID:               workflowID,
		WorkflowExecutionTimeout: vlm.GetVSAClusterDeploymentWorkflowExecutionTimeout,
		RetryPolicy:              toRetryPolicy(retryPolicy),
	})
	childCtx = orchestrator.WithValue(childCtx, vlm.DeploymentIDKey, req.DeploymentID)

	logger := wfLogger(childCtx)
	logger.Info("dispatching VLM child workflow",
		"workflowName", vlm.GetVSAClusterDeploymentWorkflowName,
		"workflowID", workflowID)
	resp := &vlm.GetVSAClusterDeploymentResponse{}
	if err := orchestrator.ExecuteChildWorkflow(childCtx, vlm.GetVSAClusterDeploymentWorkflowName, req).Get(childCtx, resp); err != nil {
		logger.Error("VLM get-cluster child workflow failed",
			"workflowID", workflowID, "error", err.Error())
		handledErr := vlm.NewVLMErrorHandler().HandleVLMError(err)
		return nil, apperror.WrapAsAppError(handledErr, false)
	}
	return resp, nil
}
