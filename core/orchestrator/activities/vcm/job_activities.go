package vcm

import (
	"context"
	"fmt"

	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

type JobActivity struct {
	Store vcmdb.VCMDatastore
}

// mapErrorToJobProgressParams sets state to ERROR and fills tracking_id, error_message,
// and retriable from err, using CustomError fields when err wraps one.
func mapErrorToJobProgressParams(params vcmdb.UpdateJobProgressParams, err error) vcmdb.UpdateJobProgressParams {
	params.State = vcmdb.JobStateError
	if err == nil {
		return params
	}
	var customError *vsaerrors.CustomError
	if vsaerrors.As(err, &customError) {
		params.TrackingID = customError.TrackingID
		params.Retriable = customError.Retriable
		if customError.OriginalErr != nil {
			params.ErrorMessage = customError.OriginalErr.Error()
		} else {
			params.ErrorMessage = customError.Message
		}
		return params
	}
	params.TrackingID = 0
	params.ErrorMessage = err.Error()
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
	return a.Store.UpdateJobProgress(ctx, params)
}
