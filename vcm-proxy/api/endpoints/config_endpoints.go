package api

import (
	"context"

	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// SetConfigV1 stores the VCM common configuration in memory and returns 200 immediately.
// The correlation id on the request is bound to the logger by the logging
// middleware, so it is not read from params here.
func (h Handler) SetConfigV1(ctx context.Context, req *vcmserver.SetConfigRequestV1, _ vcmserver.SetConfigV1Params) error {
	if h.ConfigStore != nil {
		h.ConfigStore.Apply(ctx, req)
	}
	return nil
}
