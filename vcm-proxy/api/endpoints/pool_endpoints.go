package api

import (
	"context"

	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// PoolCreateV1 accepts a pool create request without provisioning it yet.
func (h Handler) PoolCreateV1(ctx context.Context, req *vcmserver.PoolRequestV1) (vcmserver.PoolCreateV1Res, error) {
	return &vcmserver.PoolCreateV1OK{}, nil
}
