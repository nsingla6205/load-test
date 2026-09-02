package vcm

import (
	"context"
	"fmt"

	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

type JobActivity struct {
	Store vcmdb.VCMDatastore
}

// mapErrorToJobProgressParams maps err into ERROR job progress fields.
func mapErrorToJobProgressParams(params vcmdb.UpdateJobProgressParams, err error) vcmdb.UpdateJobProgressParams {
	params.State = vcmdb.JobStateError
	if err == nil {
		return params
	}
	trackingID, retriable, errorMessage := vsaerrors.ExtractJobFailureDetails(err)
	params.TrackingID = trackingID
	params.Retriable = retriable
	params.ErrorMessage = errorMessage
	return params
}

// JobProgressParams builds UpdateJobProgressParams from workflow state, error, and optional response payload.
func JobProgressParams(jobID string, state vcmdb.JobState, workflowErr error, responsePayload *string) vcmdb.UpdateJobProgressParams {
	params := vcmdb.UpdateJobProgressParams{JobID: jobID}
	switch {
	case workflowErr != nil:
		return mapErrorToJobProgressParams(params, workflowErr)
	case state != "":
		params.State = state
	default:
		params.State = vcmdb.JobStateDone
	}
	if responsePayload != nil && vcmdb.HasPersistableResponsePayload(*responsePayload) {
		params.ResponsePayload = *responsePayload
	}
	return params
}

// GetJob loads the durable job row so a workflow can verify preconditions
// (e.g. that the job is still in NEW before it starts provisioning).
func (a JobActivity) GetJob(ctx context.Context, jobID string) (*vcmdb.Job, error) {
	orchestrator.RecordHeartbeat(ctx, "Reading VCM job")
	if a.Store == nil {
		return nil, fmt.Errorf("vcm.JobActivity: job store is not configured")
	}
	job, err := a.Store.GetJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("get vcm job %q: %w", jobID, err)
	}
	return job, nil
}

// UpdateJobProgress persists durable VCM job progress from a go-workflows activity.
func (a JobActivity) UpdateJobProgress(ctx context.Context, params vcmdb.UpdateJobProgressParams) error {
	orchestrator.RecordHeartbeat(ctx, "Updating VCM job progress")
	if a.Store == nil {
		return fmt.Errorf("vcm.JobActivity: job store is not configured")
	}
	if params.State == "" && params.ErrorMessage != "" {
		params.State = vcmdb.JobStateError
	}
	if params.ErrorMessage != "" {
		util.GetLogger(ctx).ErrorContext(ctx, "VCM job failed", "jobId", params.JobID, "error", params.ErrorMessage)
	}
	return a.Store.UpdateJobProgress(ctx, params)
}
