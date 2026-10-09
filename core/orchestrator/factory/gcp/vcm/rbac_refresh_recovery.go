package vcm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	gowfbackend "github.com/cschleiden/go-workflows/backend"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcm_workflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// RbacRefreshRecoveryHandler resumes an interrupted ONTAP_MODE_RBAC_REFRESH job
// after a process restart. It rebuilds the workflow input from the request
// payload persisted at first invocation and re-dispatches the refresh workflow
// under the deployment id (the workflow instance key), so a refresh reaches a
// terminal state instead of sitting in NEW/PROCESSING until the deadline.
type RbacRefreshRecoveryHandler struct {
	wfStarter GoWorkflowStarter
	logger    log.Logger
}

// NewRbacRefreshRecoveryHandler creates a recovery handler for RBAC refresh jobs.
func NewRbacRefreshRecoveryHandler(wfStarter GoWorkflowStarter, logger log.Logger) *RbacRefreshRecoveryHandler {
	return &RbacRefreshRecoveryHandler{wfStarter: wfStarter, logger: logger}
}

// Resume rebuilds the refresh workflow input from the persisted request payload
// and re-dispatches the refresh workflow. A workflow instance that is already
// running for this job id is treated as a successful no-op.
func (h *RbacRefreshRecoveryHandler) Resume(ctx context.Context, job *vcmdb.Job) error {
	if job == nil {
		return errors.New("rbac-refresh recovery: job is nil")
	}
	if job.Type != vcmdb.JobTypeOntapModeRbacRefresh {
		return fmt.Errorf("rbac-refresh recovery: unexpected job type %q for job %q", job.Type, job.ID)
	}
	if job.RequestPayload == "" {
		return fmt.Errorf("rbac-refresh recovery: job %q has no request payload to resume from", job.ID)
	}

	var vcm commonparams.VCMCreatePoolParams
	if err := json.Unmarshal([]byte(job.RequestPayload), &vcm); err != nil {
		return fmt.Errorf("rbac-refresh recovery: failed to unmarshal request payload for job %q: %w", job.ID, err)
	}
	vcm.Derived = vcm.DeriveInputs()

	params := &commonparams.RefreshRbacForPoolParams{
		PoolID:      vcm.DeploymentID,
		RbacFileURL: vcm.OntapModeRbacFileURL,
		VCM:         &vcm,
	}

	ctx = orchestrator.WithDeploymentID(ctx, vcm.DeploymentID)

	if h.logger != nil {
		h.logger.InfoContext(ctx, "resuming rbac-refresh job",
			"job_id", job.ID, "deployment_id", vcm.DeploymentID, "state", job.State)
	}

	if _, err := h.wfStarter.Start(ctx, vcm.DeploymentID, vcm_workflows.OntapModeRbacRefreshWorkflow, params); err != nil {
		if errors.Is(err, gowfbackend.ErrInstanceAlreadyExists) {
			if h.logger != nil {
				h.logger.InfoContext(ctx, "rbac-refresh job already running; skipping resume",
					"job_id", job.ID, "deployment_id", vcm.DeploymentID)
			}
			return nil
		}
		return fmt.Errorf("rbac-refresh recovery: re-dispatch RBAC refresh workflow for job %q: %w", job.ID, err)
	}
	return nil
}
