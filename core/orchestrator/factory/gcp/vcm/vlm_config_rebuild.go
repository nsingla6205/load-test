package vcm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcm_workflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
)

// vlmConfigRebuildInstanceSuffix is the per-deployment rebuild workflow instance id suffix.
const vlmConfigRebuildInstanceSuffix = "-vlmcfg-rebuild-"

// VLMConfigRebuildDispatcher starts the VLM config rebuild workflow via the GoWorkflowStarter.
type VLMConfigRebuildDispatcher struct {
	wfStarter GoWorkflowStarter
}

// NewVLMConfigRebuildDispatcher builds a dispatcher backed by the given workflow starter.
func NewVLMConfigRebuildDispatcher(wfStarter GoWorkflowStarter) *VLMConfigRebuildDispatcher {
	return &VLMConfigRebuildDispatcher{wfStarter: wfStarter}
}

// StartRebuildVLMConfig schedules RebuildVLMConfigWorkflow, treating an existing instance as a no-op.
func (d *VLMConfigRebuildDispatcher) StartRebuildVLMConfig(ctx context.Context, params *commonparams.GetVLMConfigParams) error {
	if params == nil {
		return errors.New("start rebuild VLM config: params missing")
	}
	if params.DeploymentID == "" {
		return errors.New("start rebuild VLM config: deploymentId missing")
	}
	// Keep the config fingerprint for traceability and add a unique attempt ID
	// so a terminally failed workflow does not block a later retry. The config
	// store prevents concurrent attempts while a rebuild is in flight.
	instanceID := params.DeploymentID + vlmConfigRebuildInstanceSuffix +
		rebuildConfigFingerprint(params) + "-" + uuid.NewString()
	_, err := d.wfStarter.Start(ctx, instanceID, vcm_workflows.RebuildVLMConfigWorkflow, params)
	switch {
	case err == nil, errors.Is(err, cbackend.ErrInstanceAlreadyExists):
		return nil
	default:
		return err
	}
}

// rebuildConfigFingerprint returns a short, stable hash of the mutable rebuild
// inputs (zones and ONTAP credentials) for workflow traceability. Stable
// identity fields (deployment id, tenant project, CA URI) are excluded; the
// deployment id is already the instance-id prefix.
func rebuildConfigFingerprint(params *commonparams.GetVLMConfigParams) string {
	parts := []string{
		params.Zone1,
		params.Zone2,
		params.MediatorZone,
	}
	for _, c := range params.OntapCredentials {
		parts = append(parts, c.Type, c.SecretID, c.CertificateID)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])[:12]
}
