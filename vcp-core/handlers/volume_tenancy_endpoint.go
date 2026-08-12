package api

import (
	"context"
	"strconv"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// V1GetAllVolumeTenancy returns a paginated expert-mode volume tenancy snapshot for one cluster.
func (h Handler) V1GetAllVolumeTenancy(ctx context.Context, params oasgenserver.V1GetAllVolumeTenancyParams) (oasgenserver.V1GetAllVolumeTenancyRes, error) {
	if params.ClusterName == "" {
		return volumeTenancyBadRequest(vsaerrors.ErrMissingRequiredInputError, "clusterName is required"), nil
	}

	cursorID := int64(0)
	if params.Cursor.IsSet() && params.Cursor.Value != "" {
		parsed, err := strconv.ParseInt(params.Cursor.Value, 10, 64)
		if err != nil || parsed < 0 {
			return volumeTenancyBadRequest(vsaerrors.ErrInputValidationError, "invalid cursor"), nil
		}
		cursorID = parsed
	}

	limit := commonparams.DefaultExpertModeVolumeTenancyPageLimit
	if params.Limit.IsSet() && params.Limit.Value > 0 {
		limit = params.Limit.Value
	}
	limit = commonparams.NormalizeExpertModeVolumeTenancyPageLimit(limit)

	items, nextCursor, err := h.Orchestrator.ListExpertModeVolumeTenancy(ctx, commonparams.ListExpertModeVolumeTenancyParams{
		ClusterName: params.ClusterName,
		CursorID:    cursorID,
		Limit:       limit,
	})
	if err != nil {
		return mapVolumeTenancyListError(err), nil
	}

	responseItems := make([]oasgenserver.VolumeTenancyV1, 0, len(items))
	for _, item := range items {
		responseItems = append(responseItems, convertExpertModeVolumeTenancyToOAS(item))
	}

	response := &oasgenserver.V1GetAllVolumeTenancyOK{Items: responseItems}
	if nextCursor != "" {
		response.SetNextCursor(oasgenserver.NewOptString(nextCursor))
	}
	return response, nil
}

func convertExpertModeVolumeTenancyToOAS(item commonparams.ExpertModeVolumeTenancy) oasgenserver.VolumeTenancyV1 {
	out := oasgenserver.VolumeTenancyV1{
		UUID:     item.UUID,
		Svm:      item.SVM,
		Volume:   item.Volume,
		Project:  item.Project,
		PoolName: item.PoolName,
	}
	if item.TenantProject != "" {
		out.SetTenantProject(oasgenserver.NewOptString(item.TenantProject))
	}
	if item.DeploymentName != "" {
		out.SetDeploymentName(oasgenserver.NewOptString(item.DeploymentName))
	}
	return out
}

func volumeTenancyBadRequest(trackingID int, message string) *oasgenserver.V1GetAllVolumeTenancyBadRequest {
	if message == "" {
		message = vsaerrors.GetErrorMessageByTrackingID(trackingID).Message
	}
	return &oasgenserver.V1GetAllVolumeTenancyBadRequest{
		Message: message,
		Code:    float64(trackingID),
	}
}

func mapVolumeTenancyListError(err error) oasgenserver.V1GetAllVolumeTenancyRes {
	if utilserrors.IsNotImplementedYetErr(err) {
		return &oasgenserver.V1GetAllVolumeTenancyNotImplemented{
			Message: "Expert-mode volume tenancy listing is not supported for this provider",
			Code:    float64(commonparams.HttpStatusNotImplemented),
		}
	}

	var customErr *vsaerrors.CustomError
	if vsaerrors.As(err, &customErr) {
		message := customErr.GetMessage()
		if message == "" {
			message = vsaerrors.GetErrorMessageByTrackingID(customErr.TrackingID).Message
		}

		switch {
		case customErr.IsError(vsaerrors.ErrMissingRequiredInputError),
			customErr.IsError(vsaerrors.ErrInputValidationError):
			return volumeTenancyBadRequest(customErr.TrackingID, message)
		case customErr.IsError(vsaerrors.ErrDatabaseDataReadError):
			return &oasgenserver.V1GetAllVolumeTenancyInternalServerError{
				Message: message,
				Code:    float64(customErr.TrackingID),
			}
		}

		if hasCode, httpCode := customErr.GetHttpCode(); hasCode {
			switch httpCode {
			case commonparams.HTTPStatusBadRequest:
				return volumeTenancyBadRequest(customErr.TrackingID, message)
			}
		}

		return &oasgenserver.V1GetAllVolumeTenancyInternalServerError{
			Message: message,
			Code:    float64(customErr.TrackingID),
		}
	}

	return &oasgenserver.V1GetAllVolumeTenancyInternalServerError{
		Message: vsaerrors.DefaultErrorMessage,
		Code:    float64(vsaerrors.ErrInternalServerError),
	}
}
