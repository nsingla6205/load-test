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

// CreatePoolRecoveryHandler resumes an interrupted POOL_CREATE job after a
// process restart. It rebuilds the workflow input from the request payload
// persisted at first invocation and re-dispatches the pool workflow under the
// job's own id. Pool update has its own dedicated recovery handler.
type CreatePoolRecoveryHandler struct {
	wfStarter GoWorkflowStarter
	logger    log.Logger
}

// NewCreatePoolRecoveryHandler creates a recovery handler for create-pool jobs.
func NewCreatePoolRecoveryHandler(wfStarter GoWorkflowStarter, logger log.Logger) *CreatePoolRecoveryHandler {
	return &CreatePoolRecoveryHandler{wfStarter: wfStarter, logger: logger}
}

// Resume rebuilds the create-pool workflow input from the persisted request
// payload and re-dispatches the pool workflow. A workflow instance that is
// already running for this job id is treated as a successful no-op.
func (h *CreatePoolRecoveryHandler) Resume(ctx context.Context, job *vcmdb.Job) error {
	if job == nil {
		return errors.New("create-pool recovery: job is nil")
	}
	if job.Type != vcmdb.JobTypePoolCreate {
		return fmt.Errorf("create-pool recovery: unexpected job type %q for job %q", job.Type, job.ID)
	}
	if job.RequestPayload == "" {
		return fmt.Errorf("create-pool recovery: job %q has no request payload to resume from", job.ID)
	}

	var vcm commonparams.VCMCreatePoolParams
	if err := json.Unmarshal([]byte(job.RequestPayload), &vcm); err != nil {
		return fmt.Errorf("create-pool recovery: failed to unmarshal request payload for job %q: %w", job.ID, err)
	}
	// Recompute derived inputs so they stay consistent with the persisted
	// request rather than trusting whatever was serialized.
	vcm.Derived = vcm.DeriveInputs()

	params := &commonparams.CreatePoolParams{VCM: &vcm}

	ctx = orchestrator.WithDeploymentID(ctx, vcm.DeploymentID)

	if h.logger != nil {
		h.logger.InfoContext(ctx, "resuming create-pool job",
			"job_id", job.ID, "deployment_id", vcm.DeploymentID, "state", job.State)
	}

	if _, err := h.wfStarter.Start(ctx, job.ID, vcm_workflows.PoolWorkflow, params); err != nil {
		if errors.Is(err, gowfbackend.ErrInstanceAlreadyExists) {
			// A workflow run is already in flight for this job id; nothing to do.
			if h.logger != nil {
				h.logger.InfoContext(ctx, "create-pool job already running; skipping resume",
					"job_id", job.ID, "deployment_id", vcm.DeploymentID)
			}
			return nil
		}
		return fmt.Errorf("create-pool recovery: re-dispatch pool workflow for job %q: %w", job.ID, err)
	}
	return nil
}
