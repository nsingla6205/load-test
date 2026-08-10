package api

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// V1ListInterclusterLifs returns intercluster LIFs filtered by optional account and IP range UUIDs.
func (h Handler) V1ListInterclusterLifs(ctx context.Context, params oasgenserver.V1ListInterclusterLifsParams) (oasgenserver.V1ListInterclusterLifsRes, error) {
	var accountUUID, ipRangeUUID *string
	if params.AccountUUID.IsSet() {
		accountUUID = &params.AccountUUID.Value
	}
	if params.IpRangeUUID.IsSet() {
		ipRangeUUID = &params.IpRangeUUID.Value
	}

	list, err := h.Orchestrator.GetInterclusterLifs(ctx, accountUUID, ipRangeUUID)
	if err != nil {
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) || customErr.IsError(vsaerrors.ErrAccountNotFound) {
				return &oasgenserver.V1ListInterclusterLifsNotFound{
					Message: customErr.GetMessage(),
					Code:    404,
				}, nil
			}
		}
		return &oasgenserver.V1ListInterclusterLifsInternalServerError{
			Message: fmt.Sprintf("Error listing intercluster LIFs: %v", err),
			Code:    500,
		}, nil
	}

	result := make(oasgenserver.V1ListInterclusterLifsOKApplicationJSON, 0, len(list))
	for _, lif := range list {
		result = append(result, interclusterLifToV1(lif))
	}
	return &result, nil
}

func interclusterLifToV1(lif *datamodel.Lif) oasgenserver.InterclusterLifV1 {
	v := oasgenserver.InterclusterLifV1{
		UUID:      oasgenserver.NewOptString(lif.UUID),
		CreatedAt: oasgenserver.NewOptDateTime(lif.CreatedAt),
		UpdatedAt: oasgenserver.NewOptDateTime(lif.UpdatedAt),
		Name:      oasgenserver.NewOptString(lif.Name),
		IpAddress: oasgenserver.NewOptString(lif.IPAddress),
		Netmask:   oasgenserver.NewOptString(lif.SubnetMask),
	}
	if lif.AccountUUID != "" {
		v.AccountUUID = oasgenserver.NewOptString(lif.AccountUUID)
	}
	if lif.AccountName != "" {
		v.AccountName = oasgenserver.NewOptString(lif.AccountName)
	}
	if lif.NodeUUID != "" {
		v.NodeUUID = oasgenserver.NewOptString(lif.NodeUUID)
	}
	if lif.HostUUID != "" {
		v.HostUUID = oasgenserver.NewOptString(lif.HostUUID)
	}
	if lif.IPRangeUUID != "" {
		v.IpRangeUUID = oasgenserver.NewOptString(lif.IPRangeUUID)
	}
	return v
}
