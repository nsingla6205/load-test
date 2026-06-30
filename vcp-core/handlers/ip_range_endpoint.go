package api

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// V1CreateIPRange creates a new IP range.
func (h Handler) V1CreateIPRange(ctx context.Context, req *oasgenserver.IPRangeV1, _ oasgenserver.V1CreateIPRangeParams) (oasgenserver.V1CreateIPRangeRes, error) {
	ir := convertFromIPRangeV1(req)
	if err := utils.ValidateIPRange(ir.StartIP, ir.EndIP, ir.Gateway, ir.Netmask); err != nil {
		return &oasgenserver.V1CreateIPRangeBadRequest{
			Message: fmt.Sprintf("Error creating IP range: %v", err),
			Code:    400,
		}, nil
	}

	created, err := h.Orchestrator.CreateIPRange(ctx, ir)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
				return &oasgenserver.V1CreateIPRangeConflict{
					Message: customErr.GetMessage(),
					Code:    409,
				}, nil
			}
		}
		return &oasgenserver.V1CreateIPRangeInternalServerError{
			Message: fmt.Sprintf("Error creating IP range: %v", err),
			Code:    500,
		}, nil
	}

	result := ipRangeToV1(created)
	return &result, nil
}

func convertFromIPRangeV1(req *oasgenserver.IPRangeV1) *datamodel.IPRange {
	ir := &datamodel.IPRange{}

	if req.StartIP.IsSet() {
		ir.StartIP = req.StartIP.Value
	}
	if req.EndIP.IsSet() {
		ir.EndIP = req.EndIP.Value
	}
	if req.Gateway.IsSet() {
		ir.Gateway = req.Gateway.Value
	}
	if req.Netmask.IsSet() {
		ir.Netmask = req.Netmask.Value
	}
	if req.RegionUUID.IsSet() {
		ir.RegionUUID = req.RegionUUID.Value
	}
	if req.AccountUUID.IsSet() {
		ir.AccountUUID = req.AccountUUID.Value
	}
	if req.Network.IsSet() {
		ir.Network = &req.Network.Value
	}
	if !req.LifType.Null {
		ir.LifType = string(req.LifType.Value)
	}

	return ir
}

// ipRangeToV1 converts a datamodel.IPRange to the Ogen response type.
func ipRangeToV1(ir *datamodel.IPRange) oasgenserver.IPRangeV1 {
	v := oasgenserver.IPRangeV1{
		UUID:        oasgenserver.NewOptString(ir.UUID),
		CreatedAt:   oasgenserver.NewOptDateTime(ir.CreatedAt),
		UpdatedAt:   oasgenserver.NewOptDateTime(ir.UpdatedAt),
		StartIP:     oasgenserver.NewOptString(ir.StartIP),
		EndIP:       oasgenserver.NewOptString(ir.EndIP),
		Gateway:     oasgenserver.NewOptString(ir.Gateway),
		Netmask:     oasgenserver.NewOptString(ir.Netmask),
		RegionUUID:  oasgenserver.NewOptString(ir.RegionUUID),
		RegionName:  oasgenserver.NewOptString(ir.RegionName),
		AccountUUID: oasgenserver.NewOptString(ir.AccountUUID),
		AccountName: oasgenserver.NewOptString(ir.AccountName),
	}

	if ir.Network != nil {
		v.Network = oasgenserver.NewOptString(*ir.Network)
	}
	if ir.LifType != "" {
		v.LifType = oasgenserver.NewOptNilIPRangeV1LifType(oasgenserver.IPRangeV1LifType(ir.LifType))
	}

	return v
}

// V1ListIPRanges returns IP ranges, by accountUUID and network.
func (h Handler) V1ListIPRanges(ctx context.Context, params oasgenserver.V1ListIPRangesParams) (oasgenserver.V1ListIPRangesRes, error) {
	var accountUUID, network *string
	if params.AccountUUID.IsSet() {
		accountUUID = &params.AccountUUID.Value
	}
	if params.Network.IsSet() {
		network = &params.Network.Value
	}

	list, err := h.Orchestrator.GetIPRanges(ctx, accountUUID, network)
	if err != nil {
		return &oasgenserver.V1ListIPRangesInternalServerError{
			Message: fmt.Sprintf("Error listing IP ranges: %v", err),
			Code:    500,
		}, nil
	}

	result := make(oasgenserver.V1ListIPRangesOKApplicationJSON, 0, len(list))
	for _, ir := range list {
		result = append(result, ipRangeToV1(ir))
	}
	return &result, nil
}

// V1DeleteIPRange deletes an IP range by UUID.
func (h Handler) V1DeleteIPRange(ctx context.Context, params oasgenserver.V1DeleteIPRangeParams) (oasgenserver.V1DeleteIPRangeRes, error) {
	deleted, err := h.Orchestrator.DeleteIPRange(ctx, params.IpRangeUuid)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
				return &oasgenserver.V1DeleteIPRangeNotFound{
					Message: "IP range not found",
					Code:    404,
				}, nil
			}
		}
		return &oasgenserver.V1DeleteIPRangeInternalServerError{
			Message: fmt.Sprintf("Error deleting IP range: %v", err),
			Code:    500,
		}, nil
	}

	result := ipRangeToV1(deleted)
	return &result, nil
}

// V1UpdateIPRange updates the network of an existing IP range.
func (h Handler) V1UpdateIPRange(ctx context.Context, req *oasgenserver.IPRangeUpdateV1, params oasgenserver.V1UpdateIPRangeParams) (oasgenserver.V1UpdateIPRangeRes, error) {
	ir := &datamodel.IPRange{}
	ir.UUID = params.IpRangeUuid
	ir.Network = &req.Network

	updated, err := h.Orchestrator.UpdateIPRangeNetwork(ctx, ir)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
				return &oasgenserver.V1UpdateIPRangeNotFound{
					Message: "IP range not found",
					Code:    404,
				}, nil
			}
		}
		return &oasgenserver.V1UpdateIPRangeInternalServerError{
			Message: fmt.Sprintf("Error updating IP range: %v", err),
			Code:    500,
		}, nil
	}

	result := ipRangeToV1(updated)
	return &result, nil
}
