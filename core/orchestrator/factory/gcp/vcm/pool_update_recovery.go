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
)

// UpdatePoolRecoveryHandler resumes an interrupted POOL_UPDATE job after a
// process restart. It rebuilds the workflow input from the request payload
// persisted at first invocation and re-dispatches the pool update workflow
// under the job's own id, so an in-flight COMPUTE or SCALE change reaches a
// terminal state instead of sitting in NEW/PROCESSING until the deadline.
type UpdatePoolRecoveryHandler struct {
	wfStarter GoWorkflowStarter
	logger    log.Logger
}

// NewUpdatePoolRecoveryHandler creates a recovery handler for update-pool jobs.
func NewUpdatePoolRecoveryHandler(wfStarter GoWorkflowStarter, logger log.Logger) *UpdatePoolRecoveryHandler {
	return &UpdatePoolRecoveryHandler{wfStarter: wfStarter, logger: logger}
}

// Resume rebuilds the update-pool workflow input from the persisted request
// payload and re-dispatches the pool update workflow. A workflow instance that
// is already running for this job id is treated as a successful no-op.
func (h *UpdatePoolRecoveryHandler) Resume(ctx context.Context, job *vcmdb.Job) error {
	if job == nil {
		return errors.New("update-pool recovery: job is nil")
	}
	if job.Type != vcmdb.JobTypePoolUpdate {
		return fmt.Errorf("update-pool recovery: unexpected job type %q for job %q", job.Type, job.ID)
	}
	if job.RequestPayload == "" {
		return fmt.Errorf("update-pool recovery: job %q has no request payload to resume from", job.ID)
	}

	var vcm commonparams.VCMUpdatePoolParams
	if err := json.Unmarshal([]byte(job.RequestPayload), &vcm); err != nil {
		return fmt.Errorf("update-pool recovery: failed to unmarshal request payload for job %q: %w", job.ID, err)
	}
	// Recompute derived inputs so they stay consistent with the persisted
	// request rather than trusting whatever was serialized.
	vcm.Derived = vcm.DeriveInputs()

	params := &commonparams.UpdatePoolParams{VCM: &vcm}

	if h.logger != nil {
		h.logger.InfoContext(ctx, "resuming update-pool job",
			"job_id", job.ID, "deployment_id", vcm.DeploymentID, "state", job.State,
			"change_type", vcm.ChangeType, "update_mask", vcm.UpdateMask)
	}

	if _, err := h.wfStarter.Start(ctx, job.ID, vcm_workflows.PoolUpdateWorkflow, params); err != nil {
		if errors.Is(err, gowfbackend.ErrInstanceAlreadyExists) {
			// A workflow run is already in flight for this job id; nothing to do.
			if h.logger != nil {
				h.logger.InfoContext(ctx, "update-pool job already running; skipping resume",
					"job_id", job.ID, "deployment_id", vcm.DeploymentID)
			}
			return nil
		}
		return fmt.Errorf("update-pool recovery: re-dispatch pool update workflow for job %q: %w", job.ID, err)
	}
	return nil
}
