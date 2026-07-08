package api

import (
	"context"

	"github.com/google/uuid"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// PoolCreateV1 provisions the ONTAP pool via the VCM orchestrator + VLM.
func (h Handler) PoolCreateV1(ctx context.Context, req *vcmserver.PoolCreateV1) (vcmserver.PoolCreateV1Res, error) {
	// TODO: implement PoolCreateV1. Returns the success response with a stub
	// operation until the provisioning workflow is wired up.
	return &vcmserver.OperationResponse{
		OperationID: uuid.NewString(),
		Done:        true,
	}, nil
}

// PoolGetV1 returns the current state of a pool.
func (h Handler) PoolGetV1(ctx context.Context, params vcmserver.PoolGetV1Params) (vcmserver.PoolGetV1Res, error) {
	// TODO: implement PoolGetV1
	return &vcmserver.PoolGetV1NotImplemented{
		Code:    501,
		Message: "GetPool TODO: implementation pending method decision",
	}, nil
}
