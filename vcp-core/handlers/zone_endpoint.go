package api

import (
	"context"
	"fmt"

	ht "github.com/ogen-go/ogen/http"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// V1ListZones returns all active zones.
func (h Handler) V1ListZones(ctx context.Context, _ oasgenserver.V1ListZonesParams) (oasgenserver.V1ListZonesRes, error) {
	zones, err := h.Orchestrator.ListZones(ctx)
	if err != nil {
		return &oasgenserver.V1ListZonesInternalServerError{
			Message: fmt.Sprintf("Error listing zones: %v", err),
			Code:    500,
		}, nil
	}

	regions, err := h.Orchestrator.ListRegions(ctx)
	if err != nil {
		return &oasgenserver.V1ListZonesInternalServerError{
			Message: fmt.Sprintf("Error listing zones: %v", err),
			Code:    500,
		}, nil
	}
	regionsByID := make(map[int64]*datamodel.Region, len(regions))
	for _, region := range regions {
		regionsByID[region.ID] = region
	}

	result := make(oasgenserver.V1ListZonesOKApplicationJSON, 0, len(zones))
	for _, zone := range zones {
		var region *datamodel.Region
		if zone.RegionID.Valid {
			region = regionsByID[zone.RegionID.Int64]
		}
		result = append(result, zoneToV1(zone, region))
	}
	return &result, nil
}

// V1CreateZone creates a new zone.
func (h Handler) V1CreateZone(ctx context.Context, req *oasgenserver.ZoneV1, _ oasgenserver.V1CreateZoneParams) (oasgenserver.V1CreateZoneRes, error) {
	zone := &datamodel.Zone{
		Name: req.Name,
	}
	if req.Timezone.IsSet() {
		zone.Timezone = req.Timezone.Value
	}

	createdZone, err := h.Orchestrator.CreateZone(ctx, zone, req.RegionUUID)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
				return &oasgenserver.V1CreateZoneConflict{
					Message: customErr.GetMessage(),
					Code:    409,
				}, nil
			}
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
				return &oasgenserver.V1CreateZoneBadRequest{
					Message: customErr.GetMessage(),
					Code:    400,
				}, nil
			}
		}
		return &oasgenserver.V1CreateZoneInternalServerError{
			Message: fmt.Sprintf("Error creating zone: %v", err),
			Code:    500,
		}, nil
	}

	region, err := h.regionByUUID(ctx, req.RegionUUID)
	if err != nil {
		return &oasgenserver.V1CreateZoneInternalServerError{
			Message: fmt.Sprintf("Error creating zone: %v", err),
			Code:    500,
		}, nil
	}

	result := zoneToV1(createdZone, region)
	return &result, nil
}

// V1UpdateZone updates an existing zone by UUID.
func (h Handler) V1UpdateZone(ctx context.Context, req *oasgenserver.ZoneV1, params oasgenserver.V1UpdateZoneParams) (oasgenserver.V1UpdateZoneRes, error) {
	zone := &datamodel.Zone{}
	zone.UUID = params.UUID
	zone.Name = req.Name
	if req.Timezone.IsSet() {
		zone.Timezone = req.Timezone.Value
	}

	updatedRegion, err := h.Orchestrator.UpdateZone(ctx, zone, req.RegionUUID)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
				return &oasgenserver.V1UpdateZoneNotFound{
					Message: "Zone not found",
					Code:    404,
				}, nil
			}
			if customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
				return &oasgenserver.V1UpdateZoneBadRequest{
					Message: customErr.GetMessage(),
					Code:    400,
				}, nil
			}
		}
		return &oasgenserver.V1UpdateZoneInternalServerError{
			Message: fmt.Sprintf("Error updating zone: %v", err),
			Code:    500,
		}, nil
	}

	region, err := h.regionByUUID(ctx, req.RegionUUID)
	if err != nil {
		return &oasgenserver.V1UpdateZoneInternalServerError{
			Message: fmt.Sprintf("Error updating zone: %v", err),
			Code:    500,
		}, nil
	}

	result := zoneToV1(updatedRegion, region)
	return &result, nil
}

// V1DeleteZone deletes a zone by UUID.
func (h Handler) V1DeleteZone(ctx context.Context, _ oasgenserver.V1DeleteZoneParams) (oasgenserver.V1DeleteZoneRes, error) {
	var r oasgenserver.V1DeleteZoneRes
	return r, ht.ErrNotImplemented
}

// regionByUUID loads a single region by its UUID. Region data is no longer
// embedded on datamodel.Zone, so callers fetch it explicitly when building responses.
func (h Handler) regionByUUID(ctx context.Context, uuid string) (*datamodel.Region, error) {
	regions, err := h.Orchestrator.ListRegions(ctx)
	if err != nil {
		return nil, err
	}
	for _, region := range regions {
		if region.UUID == uuid {
			return region, nil
		}
	}
	return nil, nil
}

func zoneToV1(zone *datamodel.Zone, region *datamodel.Region) oasgenserver.ZoneV1 {
	v := oasgenserver.ZoneV1{
		UUID:      oasgenserver.NewOptString(zone.UUID),
		CreatedAt: oasgenserver.NewOptDateTime(zone.CreatedAt),
		UpdatedAt: oasgenserver.NewOptDateTime(zone.UpdatedAt),
		Name:      zone.Name,
		Timezone:  oasgenserver.NewOptString(zone.Timezone),
	}

	if region != nil {
		v.RegionUUID = region.UUID
		v.RegionName = oasgenserver.NewOptString(region.Name)
	}

	return v
}
