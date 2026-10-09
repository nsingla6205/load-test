package vcm_workflows

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	vcmclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm/vcm-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

const (
	// jobStepGetOntapClusterName is the durable job step recorded while the
	// cluster name is read from ONTAP.
	jobStepGetOntapClusterName = "get-ontap-cluster-name"
	// jobStepRefreshOntapModeRbac is the durable job step recorded while VLM
	// re-applies the ONTAP-mode RBAC role.
	jobStepRefreshOntapModeRbac = "refresh-ontap-mode-rbac"
)

// rbacRefreshWorkflow holds the refresh request plus the durable job identity.
type rbacRefreshWorkflow struct {
	// baseWorkflow carries the durable job id and state helpers.
	baseWorkflow

	req    *commonparams.VCMCreatePoolParams
	inputs *commonparams.VCMDerivedPoolInputs

	// currentStep is the last entered step, recorded on terminal ERROR.
	currentStep string
}

// rbacRefreshOperationResponse is the terminal operation response of a refresh.
type rbacRefreshOperationResponse struct {
	OntapModeRbac rbacRefreshOperationRbac `json:"ontapModeRbac"`
}

type rbacRefreshOperationRbac struct {
	RbacFileURL      string `json:"rbacFileUrl"`
	RbacFileCheckSum string `json:"rbacFileCheckSum"`
}

// OntapModeRbacRefreshWorkflow re-applies the ONTAP-mode user's RBAC role
// through the VLM expert-mode user workflow with IsUpdate set, and drives the
// durable job through NEW -> PROCESSING -> DONE/ERROR. The job row is created
// (in NEW) by the orchestrator factory before the workflow starts.
//
// VCM forwards the RBAC pair unchanged and applies no gating of its own: CLH
// only invokes the refresh for ONTAP-mode clusters, and VLM verifies the file
// against the checksum before applying it.
func OntapModeRbacRefreshWorkflow(ctx *orchestrator.Context, params *commonparams.RefreshRbacForPoolParams) (*commonparams.VCMPoolWorkflowResult, error) {
	if params == nil || params.VCM == nil {
		return nil, fmt.Errorf("rbac refresh workflow: VCM params missing from request")
	}
	inputs := params.VCM.Derived
	if inputs == nil {
		inputs = params.VCM.DeriveInputs()
	}

	wf := &rbacRefreshWorkflow{req: params.VCM, inputs: inputs}
	wf.ID = params.VCM.RequestID

	logger := wfLogger(ctx)
	logger.Info("workflow: starting ONTAP mode RBAC refresh",
		"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "deadline", wf.req.Deadline,
		"rbacFileUrl", wf.req.OntapModeRbacFileURL)

	wf.Status = workflowStatusRunning
	wf.currentStep = jobStepStarting
	if err := checkDeadline(ctx, wf.req.Deadline, "mark-processing"); err != nil {
		return wf.fail(ctx, err)
	}
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateProcessing, jobStepStarting, nil, nil); err != nil {
		logger.Error("workflow: failed to mark job PROCESSING",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
		return nil, err
	}

	appliedChecksum, err := wf.refresh(ctx)
	if err != nil {
		return wf.fail(ctx, err)
	}

	donePayload, marshalErr := rbacRefreshOperationResponsePayload(wf.req.OntapModeRbacFileURL, appliedChecksum)
	if marshalErr != nil {
		logger.Warn("workflow: failed to marshal RBAC refresh operation response; marking job DONE without response payload",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", marshalErr)
		donePayload = nil
	}
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateDone, jobStepCompleted, nil, donePayload); err != nil {
		logger.Error("workflow: failed to mark job DONE",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
		return nil, err
	}
	wf.Status = workflowStatusCompleted

	return &commonparams.VCMPoolWorkflowResult{
		DeploymentID: wf.req.DeploymentID,
		Status:       "READY",
		Message:      "ONTAP mode RBAC refresh workflow completed",
	}, nil
}

// refresh resolves the credentials, reads the live VLM config and the ONTAP
// cluster name, and invokes the VLM expert-mode user workflow in update mode.
// It returns the checksum VLM reports for the applied file.
//
// The ONTAP-mode credential is resolved the same way pool create resolves it so
// VLM receives the same username: on the certificate path that name is the
// certificate's common name, and VLM's add-user step would create a new account
// under any other name. VLM discards the password itself when IsUpdate is set.
func (wf *rbacRefreshWorkflow) refresh(ctx *orchestrator.Context) (string, error) {
	logger := wfLogger(ctx)
	deploymentID := wf.req.DeploymentID

	adminCreds, err := resolveOntapCredentials(ctx, wf.inputs.OntapCredentials)
	if err != nil {
		logger.Error("workflow: resolve ONTAP admin password failed",
			"deploymentName", deploymentID, "error", err)
		return "", err
	}

	username, authenticationType, modeCreds, err := resolveOntapModeCredentials(ctx, wf.inputs.OntapModeCredentials)
	if err != nil {
		logger.Error("workflow: resolve ONTAP mode credentials failed",
			"deploymentName", deploymentID, "error", err)
		return "", err
	}

	if err := checkDeadline(ctx, wf.req.Deadline, jobStepGetCluster); err != nil {
		return "", err
	}
	wf.setStep(ctx, jobStepGetCluster)
	getCluster := func(c *orchestrator.Context, r *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
		return vcmclient.InvokeRefreshGetClusterDeployment(c, wf.req.RequestID, r)
	}
	vlmCfg, err := getLiveVLMConfig(ctx, getCluster, wf.req, wf.inputs, adminCreds)
	if err != nil {
		return "", err
	}

	// VLM owns RBAC roles by the admin SVM (cluster) name; without it VLM falls
	// back to the bare deployment ID and ONTAP rejects the role lookup with
	// "SVM does not exist".
	if err := checkDeadline(ctx, wf.req.Deadline, jobStepGetOntapClusterName); err != nil {
		return "", err
	}
	wf.setStep(ctx, jobStepGetOntapClusterName)
	clusterName, err := wf.getOntapClusterName(ctx, vlmCfg)
	if err != nil {
		logger.Error("workflow: get ONTAP cluster name failed",
			"deploymentName", deploymentID, "error", err)
		return "", err
	}
	vlmCfg.VsaCluster.ClusterName = clusterName

	if err := checkDeadline(ctx, wf.req.Deadline, jobStepRefreshOntapModeRbac); err != nil {
		return "", err
	}
	wf.setStep(ctx, jobStepRefreshOntapModeRbac)
	logger.Info("workflow: invoking VLM ONTAP mode user child workflow in update mode",
		"deploymentName", deploymentID,
		"clusterName", clusterName,
		"username", username,
		"authenticationType", authenticationType)

	resp, err := vcmclient.InvokeRefreshExpertModeUser(ctx, wf.req.RequestID, &vlm.OntapExpertModeUserConfig{
		VLMConfig:                 vlmCfg,
		OntapCredentials:          adminCreds,
		ExpertModeUserCredentials: modeCreds,
		Username:                  username,
		AuthenticationType:        authenticationType,
		RbacFileURL:               wf.req.OntapModeRbacFileURL,
		RbacFileChecksum:          wf.req.OntapModeRbacChecksum,
		IsUpdate:                  true,
	})
	if err != nil {
		logger.Error("workflow: VLM ONTAP mode RBAC refresh failed",
			"deploymentName", deploymentID, "username", username, "error", err)
		return "", fmt.Errorf("VLM ONTAP mode RBAC refresh: %w", err)
	}

	appliedChecksum := ""
	if resp != nil {
		appliedChecksum = resp.RbacFileChecksum
	}
	logger.Info("workflow: VLM ONTAP mode RBAC refreshed",
		"deploymentName", deploymentID, "username", username, "rbacFileChecksum", appliedChecksum)
	return appliedChecksum, nil
}

// getOntapClusterName reads the cluster name from ONTAP over the node mgmt LIF,
// using the same SNI and node credentials as pool create's QoS step.
func (wf *rbacRefreshWorkflow) getOntapClusterName(ctx *orchestrator.Context, vlmCfg vlm.VLMConfig) (string, error) {
	var ca *activities.ClusterActivity

	mgmtIP := nodeMgmtIPFromVLMConfig(vlmCfg)
	if mgmtIP == "" {
		return "", errors.New("no node management IP in VLM cluster config")
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes: []*datamodel.Node{
			{EndpointAddress: mgmtIP, HostDNSName: mgmtIP},
		},
		DeploymentName:   wf.req.DeploymentID,
		OntapCredentials: nodeCredentialsFromInputs(wf.inputs),
		ServerName:       vsa.OntapSNIServerName(wf.req.DeploymentID),
	})

	var clusterName string
	if err := orchestrator.ExecuteActivity(ctx, ca.GetOntapClusterName, node).Get(ctx, &clusterName); err != nil {
		return "", fmt.Errorf("get ONTAP cluster name: %w", err)
	}
	if clusterName == "" {
		return "", errors.New("ONTAP returned an empty cluster name")
	}
	return clusterName, nil
}

// fail records a terminal ERROR on the durable job and returns that error.
func (wf *rbacRefreshWorkflow) fail(ctx *orchestrator.Context, err error) (*commonparams.VCMPoolWorkflowResult, error) {
	logger := wfLogger(ctx)
	logger.Error("workflow: ONTAP mode RBAC refresh failed",
		"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
	wf.Status = workflowStatusFailed
	// Marking the job ERROR is best-effort: a failed update is logged but must
	// not replace the original cause, so the real refresh failure is always the
	// returned error (same best-effort pattern as setStep).
	if uerr := wf.UpdateJobStatus(ctx, vcmdb.JobStateError, wf.currentStep, err, nil); uerr != nil {
		logger.Error("workflow: failed to mark job ERROR",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", uerr)
	}
	return nil, err
}

// setStep updates the durable job's current_step (best-effort; a failed update
// is logged but does not abort the refresh).
func (wf *rbacRefreshWorkflow) setStep(ctx *orchestrator.Context, step string) {
	wf.currentStep = step
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateProcessing, step, nil, nil); err != nil {
		wfLogger(ctx).Warn("workflow: failed to update job step",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "step", step, "error", err)
	}
}

// rbacRefreshOperationResponsePayload marshals the refresh operation response:
// the requested file URL and the checksum VLM reports for the applied file.
func rbacRefreshOperationResponsePayload(rbacFileURL, appliedChecksum string) (*string, error) {
	data, err := json.Marshal(rbacRefreshOperationResponse{
		OntapModeRbac: rbacRefreshOperationRbac{
			RbacFileURL:      rbacFileURL,
			RbacFileCheckSum: appliedChecksum,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal RBAC refresh operation response: %w", err)
	}
	payload := string(data)
	return &payload, nil
}
