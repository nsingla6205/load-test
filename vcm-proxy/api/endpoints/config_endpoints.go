package api

import (
	"context"

	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// SetConfigV1 stores the VCM common configuration in memory and returns 200 immediately.
func (h Handler) SetConfigV1(ctx context.Context, req *vcmserver.SetConfigRequestV1) error {
	if h.ConfigStore != nil {
		h.ConfigStore.Apply(ctx, req)
	}
	return nil
}
