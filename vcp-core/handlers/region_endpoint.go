package api

import (
	"context"
	"fmt"

	ht "github.com/ogen-go/ogen/http"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func (h Handler) V1ListRegions(ctx context.Context, _ oasgenserver.V1ListRegionsParams) (oasgenserver.V1ListRegionsRes, error) {
	regions, err := h.Orchestrator.ListRegions(ctx)
	if err != nil {
		return &oasgenserver.V1ListRegionsInternalServerError{
			Message: fmt.Sprintf("Error listing regions: %v", err),
			Code:    500,
		}, nil
	}

	result := make(oasgenserver.V1ListRegionsOKApplicationJSON, 0, len(regions))
	for _, region := range regions {
		result = append(result, regionToV1(region))
	}
	return &result, nil
}

// V1CreateRegion creates a new region.
func (h Handler) V1CreateRegion(ctx context.Context, req *oasgenserver.RegionV1, _ oasgenserver.V1CreateRegionParams) (oasgenserver.V1CreateRegionRes, error) {
	region := &datamodel.Region{
		Name: req.Name,
	}

	created, err := h.Orchestrator.CreateRegion(ctx, region)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
				return &oasgenserver.V1CreateRegionConflict{
					Message: customErr.GetMessage(),
					Code:    409,
				}, nil
			}
		}
		return &oasgenserver.V1CreateRegionInternalServerError{
			Message: fmt.Sprintf("Error creating region: %v", err),
			Code:    500,
		}, nil
	}

	result := regionToV1(created)
	return &result, nil
}

// V1UpdateRegion updates an existing region by UUID.
func (h Handler) V1UpdateRegion(ctx context.Context, req *oasgenserver.RegionV1, params oasgenserver.V1UpdateRegionParams) (oasgenserver.V1UpdateRegionRes, error) {
	region := &datamodel.Region{}
	region.UUID = params.UUID
	region.Name = req.Name

	updated, _, err := h.Orchestrator.UpdateRegion(ctx, region)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
				return &oasgenserver.V1UpdateRegionNotFound{
					Message: "Region not found",
					Code:    404,
				}, nil
			}
			if customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
				return &oasgenserver.V1UpdateRegionBadRequest{
					Message: customErr.GetMessage(),
					Code:    400,
				}, nil
			}
		}
		return &oasgenserver.V1UpdateRegionInternalServerError{
			Message: fmt.Sprintf("Error updating region: %v", err),
			Code:    500,
		}, nil
	}

	result := regionToV1(updated)
	return &result, nil
}

// V1DeleteRegion deletes a region by UUID.
func (h Handler) V1DeleteRegion(ctx context.Context, _ oasgenserver.V1DeleteRegionParams) (oasgenserver.V1DeleteRegionRes, error) {
	var r oasgenserver.V1DeleteRegionRes
	return r, ht.ErrNotImplemented
}

func regionToV1(region *datamodel.Region) oasgenserver.RegionV1 {
	return oasgenserver.RegionV1{
		UUID:      oasgenserver.NewOptString(region.UUID),
		CreatedAt: oasgenserver.NewOptDateTime(region.CreatedAt),
		UpdatedAt: oasgenserver.NewOptDateTime(region.UpdatedAt),
		Name:      region.Name,
	}
}
