package api

import (
	"context"
	"time"

	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// PoolCreateV1 persists a durable pool-create job and accepts the request.
func (h Handler) PoolCreateV1(ctx context.Context, req *vcmserver.PoolRequestV1) (vcmserver.PoolCreateV1Res, error) {
	if h.ConfigStore == nil {
		return &vcmserver.PoolCreateV1InternalServerError{
			Code:    500,
			Message: "VCM config store is not configured",
		}, nil
	}
	if req == nil || req.RequestId == "" || req.Pool.DeploymentId == "" {
		return &vcmserver.PoolCreateV1BadRequest{
			Code:    400,
			Message: "requestId and pool.deploymentId are required",
		}, nil
	}
	if req.Deadline.IsZero() || !req.Deadline.UTC().After(time.Now().UTC()) {
		return &vcmserver.PoolCreateV1BadRequest{
			Code:    400,
			Message: "deadline must be a future UTC timestamp",
		}, nil
	}

	job := &vcmdb.Job{
		ID:         req.RequestId,
		ResourceID: req.Pool.DeploymentId,
		Type:       string(datamodel.JobTypeCreatePool),
		State:      vcmdb.JobStateNew,
		DeadlineAt: req.Deadline.UTC(),
	}
	if err := h.ConfigStore.CreateJob(ctx, job); err != nil {
		return &vcmserver.PoolCreateV1InternalServerError{
			Code:    500,
			Message: "Failed to persist pool creation job",
		}, nil
	}

	// The provisioning workflow will set CurrentStep via UpdateJobProgress
	// as each workflow step executes.
	return &vcmserver.PoolCreateV1OK{}, nil
}
