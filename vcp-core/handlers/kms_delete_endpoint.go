package api

import (
	"context"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// V1DeleteGcpKmsConfig starts the VCP delete for a CMEK policy.
// A missing or soft-deleted row is returned as 404.
func (h *Handler) V1DeleteGcpKmsConfig(ctx context.Context, params oasgenserver.V1DeleteGcpKmsConfigParams) (oasgenserver.V1DeleteGcpKmsConfigRes, error) {
	kmsConfig, job, err := h.Orchestrator.DeleteKmsConfigCore(ctx, params.KmsConfigId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &oasgenserver.V1DeleteGcpKmsConfigNotFound{
				Code:    http.StatusNotFound,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &oasgenserver.V1DeleteGcpKmsConfigConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		}
		return &oasgenserver.V1DeleteGcpKmsConfigInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}

	return convertKmsConfigToApiResponse(kmsConfig, job), nil
}
