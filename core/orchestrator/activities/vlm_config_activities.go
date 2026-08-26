package activities

import (
	"context"
	"fmt"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
)

// VLMConfigStore writes the VLM config rebuild outcome back to the config store.
type VLMConfigStore interface {
	SetVLMConfig(ctx context.Context, jsonStr string) error
	ClearVLMConfigFetching(ctx context.Context) error
}

// VLMConfigActivity holds the store used by the VLM config rebuild activities.
type VLMConfigActivity struct {
	Store VLMConfigStore
}

// ValidateGetVLMConfigInputs verifies the rebuild params carry the required fields.
func (a *VLMConfigActivity) ValidateGetVLMConfigInputs(ctx context.Context, params commonparams.GetVLMConfigParams) error {
	logger := util.GetLogger(ctx)

	var missing []string
	if params.DeploymentID == "" {
		missing = append(missing, "deploymentId")
	}
	if params.ServiceTenantProject == "" {
		missing = append(missing, "serviceTenantProject")
	}
	if params.Zone1 == "" {
		missing = append(missing, "zone1")
	}
	if params.Zone2 == "" {
		missing = append(missing, "zone2")
	}
	if params.MediatorZone == "" {
		missing = append(missing, "mediatorZone")
	}
	if len(missing) > 0 {
		return apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
				fmt.Errorf("VLM config rebuild: missing required inputs: %v", missing)), true)
	}

	cred := commonparams.SelectOntapCredential(params.OntapCredentials)
	if cred.SecretID == "" {
		return apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
				fmt.Errorf("VLM config rebuild: no ONTAP admin credential with a password secret id")), true)
	}

	logger.Info("validated VLM config rebuild inputs",
		"deploymentId", params.DeploymentID, "serviceTenantProject", params.ServiceTenantProject)
	return nil
}

// FinalizeVLMConfigRequest carries the rebuild workflow outcome.
type FinalizeVLMConfigRequest struct {
	VLMConfigJSON string
	Failed        bool
}

// FinalizeVLMConfig stores the resolved VLM config, or clears the fetching flag on failure.
func (a *VLMConfigActivity) FinalizeVLMConfig(ctx context.Context, req FinalizeVLMConfigRequest) error {
	logger := util.GetLogger(ctx)
	if a.Store == nil {
		return fmt.Errorf("vcm config activity: store not configured")
	}

	if req.Failed {
		logger.Info("VLM config rebuild failed; clearing fetching flag")
		if err := a.Store.ClearVLMConfigFetching(ctx); err != nil {
			return fmt.Errorf("clear VLM config fetching: %w", err)
		}
		return nil
	}

	logger.Info("VLM config rebuild succeeded; storing VLM config",
		"vlmConfigBytes", len(req.VLMConfigJSON))
	if err := a.Store.SetVLMConfig(ctx, req.VLMConfigJSON); err != nil {
		return fmt.Errorf("store VLM config: %w", err)
	}
	return nil
}
