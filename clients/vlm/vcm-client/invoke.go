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
