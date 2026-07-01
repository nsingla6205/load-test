package api

import (
	"context"
	"strings"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// V1SetAccountHosts implements PUT /v1/account/{accountName}/hosts.
// Reconciles the desired pinned host set and starts async credential provisioning when needed.
func (h Handler) V1SetAccountHosts(ctx context.Context, req *oasgenserver.SetAccountHostsRequestV1, params oasgenserver.V1SetAccountHostsParams) (oasgenserver.V1SetAccountHostsRes, error) {
	logger := util.GetLogger(ctx)

	if req == nil {
		return &oasgenserver.V1SetAccountHostsBadRequest{
			Code:    400,
			Message: "request body is required",
		}, nil
	}
	if strings.TrimSpace(params.AccountName) == "" {
		return &oasgenserver.V1SetAccountHostsBadRequest{
			Code:    400,
			Message: "accountName path parameter is required",
		}, nil
	}

	hostUUIDs := req.HostUuids
	if hostUUIDs == nil {
		hostUUIDs = []string{}
	}

	onboardParams := &commonparams.PinAccountHostsParams{
		AccountName: strings.TrimSpace(params.AccountName),
		HostUUIDs:   append([]string(nil), hostUUIDs...),
	}

	response, err := h.Orchestrator.PinAccountHosts(ctx, onboardParams)
	if err != nil {
		if errors.IsNotImplementedYetErr(err) {
			return &oasgenserver.V1SetAccountHostsInternalServerError{
				Code:    500,
				Message: "Account host pinning is not implemented yet",
			}, nil
		}
		if customErr := asVCPError(err); customErr != nil {
			if customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
				return &oasgenserver.V1SetAccountHostsConflict{
					Code:    409,
					Message: customErr.GetMessage(),
				}, nil
			}
			if customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError) || customErr.IsError(vsaerrors.ErrAccountNotFound) {
				return &oasgenserver.V1SetAccountHostsNotFound{
					Code:    404,
					Message: customErr.GetMessage(),
				}, nil
			}
		}
		if errors.IsConflictErr(err) {
			return &oasgenserver.V1SetAccountHostsConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}
		if errors.IsNotFoundErr(err) {
			return &oasgenserver.V1SetAccountHostsNotFound{
				Code:    404,
				Message: err.Error(),
			}, nil
		}
		if errors.IsUserInputValidationErr(err) || errors.IsBadRequestErr(err) {
			return &oasgenserver.V1SetAccountHostsBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Errorf("Failed to set account hosts: %v", err)
		return &oasgenserver.V1SetAccountHostsInternalServerError{
			Code:    500,
			Message: "Failed to set account hosts",
		}, nil
	}

	return &oasgenserver.SetAccountHostsResponseV1{
		Status: oasgenserver.SetAccountHostsResponseV1Status(response.Status),
		JobId:  response.JobID,
	}, nil
}
