package vcm_workflows

import (
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// Internal workflow status labels, informational only.
const (
	workflowStatusRunning   = "RUNNING"
	workflowStatusCompleted = "COMPLETED"
	workflowStatusFailed    = "FAILED"
)

// baseWorkflow carries the durable job identity and status plus the shared
// job-state helpers embedded by VCM workflows.
type baseWorkflow struct {
	// ID is the durable job id (equals the request id).
	ID string
	// Status is the in-memory workflow status.
	Status string
}

// EnsureJobState asserts the durable job is in the expected state before the
// workflow proceeds.
func (bw *baseWorkflow) EnsureJobState(ctx *orchestrator.Context, expected vcmdb.JobState) error {
	if bw.ID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError,
			errors.New("job id cannot be empty"))
	}

	ja := &activities.JobActivity{}
	var job *vcmdb.Job
	if err := orchestrator.ExecuteActivity(ctx, ja.GetJob, bw.ID).Get(ctx, &job); err != nil {
		return err
	}

	if job == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
			fmt.Errorf("job %s not found", bw.ID))
	}

	if job.State != expected {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("job %s is in state %s; expected %s", bw.ID, job.State, expected))
	}

	return nil
}

// UpdateJobStatus persists job state, step, error details, and an optional terminal response payload.
func (bw *baseWorkflow) UpdateJobStatus(ctx *orchestrator.Context, state vcmdb.JobState, step string, cause error, responsePayload *string) error {
	if bw.ID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError,
			errors.New("job id cannot be empty"))
	}

	params := vcmdb.UpdateJobProgressParams{
		JobID: bw.ID,
		Step:  step,
		State: state,
	}
	if responsePayload != nil && vcmdb.HasPersistablePayload(*responsePayload) {
		params.ResponsePayload = *responsePayload
	}
	if cause != nil {
		params.TrackingID, params.ErrorMessage, params.Retriable = jobErrorFields(cause)
	}

	ja := &activities.JobActivity{}
	return orchestrator.ExecuteActivity(ctx, ja.UpdateJobProgress, params).Get(ctx, nil)
}
