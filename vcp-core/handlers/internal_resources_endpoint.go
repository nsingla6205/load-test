package api

import (
	"context"

	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// V1GetInternalResource serves the read-only ops inventory get.
func (h Handler) V1GetInternalResource(ctx context.Context, req *oasgenserver.OpsGetResourceRequestV1, params oasgenserver.V1GetInternalResourceParams) (oasgenserver.V1GetInternalResourceRes, error) {
	if h.OpsInventory == nil {
		return &oasgenserver.V1GetInternalResourceNotImplemented{Code: 501, Message: "ops inventory API is not configured"}, nil
	}
	return h.OpsInventory.Get(ctx, req, params)
}

// V1ListInternalResources serves the read-only ops inventory list.
func (h Handler) V1ListInternalResources(ctx context.Context, req *oasgenserver.OpsListResourcesRequestV1, params oasgenserver.V1ListInternalResourcesParams) (oasgenserver.V1ListInternalResourcesRes, error) {
	if h.OpsInventory == nil {
		return &oasgenserver.V1ListInternalResourcesNotImplemented{Code: 501, Message: "ops inventory API is not configured"}, nil
	}
	return h.OpsInventory.List(ctx, req, params)
}
