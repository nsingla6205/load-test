package vcm_workflows

import (
	"encoding/json"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	vcmclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm/vcm-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// RebuildVLMConfigWorkflow resolves the VLM configuration for a VCM deployment and writes it back to the config store.
func RebuildVLMConfigWorkflow(ctx *orchestrator.Context, params *commonparams.GetVLMConfigParams) error {
	logger := wfLogger(ctx)
	if params == nil {
		return fmt.Errorf("rebuild VLM config workflow: params missing")
	}
	logger.Info("workflow: starting VLM config rebuild", "deploymentId", params.DeploymentID)

	vlmConfigJSON, rebuildErr := buildVLMConfigJSON(ctx, params)
	if rebuildErr != nil {
		logger.Error("workflow: VLM config rebuild failed",
			"deploymentId", params.DeploymentID, "error", rebuildErr)
	}

	// Finalize on every outcome so the store's fetching flag is always cleared.
	var ca *activities.VLMConfigActivity
	finalizeReq := activities.FinalizeVLMConfigRequest{
		VLMConfigJSON: vlmConfigJSON,
		Failed:        rebuildErr != nil,
	}
	if err := orchestrator.ExecuteActivity(ctx, ca.FinalizeVLMConfig, finalizeReq).Get(ctx, nil); err != nil {
		logger.Error("workflow: finalize VLM config failed",
			"deploymentId", params.DeploymentID, "error", err)
		return err
	}

	if rebuildErr != nil {
		return rebuildErr
	}
	logger.Info("workflow: VLM config rebuild completed", "deploymentId", params.DeploymentID)
	return nil
}

// buildVLMConfigJSON resolves the VLM config via the GetVSAClusterDeployment child workflow and returns it as JSON.
func buildVLMConfigJSON(ctx *orchestrator.Context, params *commonparams.GetVLMConfigParams) (string, error) {
	var ca *activities.VLMConfigActivity
	if err := orchestrator.ExecuteActivity(ctx, ca.ValidateGetVLMConfigInputs, *params).Get(ctx, nil); err != nil {
		return "", fmt.Errorf("validate VLM config inputs: %w", err)
	}

	creds, err := resolveVLMConfigCredentials(ctx, params)
	if err != nil {
		return "", err
	}

	req := &vlm.GetVSAClusterDeploymentRequest{
		DeploymentID:     params.DeploymentID,
		CloudProvider:    env.Hyperscaler,
		ProjectID:        params.ServiceTenantProject,
		Region:           env.Region,
		Zone1:            params.Zone1,
		Zone2:            params.Zone2,
		MediatorZone:     params.MediatorZone,
		DeploymentType:   vlm.DeploymentTypeNonSharedHA,
		OntapCredentials: creds,
	}

	resp, err := vcmclient.InvokeGetClusterDeployment(ctx, req)
	if err != nil {
		return "", fmt.Errorf("VLM get cluster deployment: %w", err)
	}

	data, err := json.Marshal(resp.VLMConfig)
	if err != nil {
		return "", fmt.Errorf("marshal VLM config response: %w", err)
	}
	return string(data), nil
}

// resolveVLMConfigCredentials returns the ONTAP admin credentials for the VLM
// Get workflow, resolving either the certificate material or the admin password
// depending on the credential's auth type.
func resolveVLMConfigCredentials(ctx *orchestrator.Context, params *commonparams.GetVLMConfigParams) (vlm.OntapCredentials, error) {
	poolCreds := commonparams.DeriveOntapCredentials(params.OntapCredentials, params.CaURI)
	return resolveOntapCredentials(ctx, poolCreds)
}
