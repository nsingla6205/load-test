package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconstants "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/utils/constants"
)

// OperationGetV1 returns the durable status of a VCM operation.
func (h Handler) OperationGetV1(ctx context.Context, params vcmserver.OperationGetV1Params) (vcmserver.OperationGetV1Res, error) {
	if err := uuid.Validate(params.OperationId); err != nil {
		return &vcmserver.OperationGetV1BadRequest{
			Code:    400,
			Message: "Invalid operation ID",
		}, nil
	}
	if h.ConfigStore == nil {
		return &vcmserver.OperationGetV1InternalServerError{
			Code:    500,
			Message: "VCM config store is not configured",
		}, nil
	}

	job, err := h.ConfigStore.GetJob(ctx, params.OperationId)
	if errors.Is(err, vcmdb.ErrJobNotFound) {
		return &vcmserver.OperationGetV1NotFound{
			Code:    404,
			Message: vcmconstants.OperationNotFound,
		}, nil
	}
	if err != nil {
		return &vcmserver.OperationGetV1InternalServerError{
			Code:    500,
			Message: "An internal error occurred",
		}, nil
	}

	response := &vcmserver.OperationV1{
		Name:     "/v1/operations/" + job.ID,
		Done:     job.IsTerminal(),
		Metadata: operationMetadataFromJob(job),
	}
	switch job.State {
	case vcmdb.JobStateNew, vcmdb.JobStateProcessing:
		return response, nil
	case vcmdb.JobStateDone:
		response.Response = vcmserver.NewOptOperationV1Response(vcmserver.OperationV1Response{})
		return response, nil
	case vcmdb.JobStateError:
		errorDetails := vsaerrors.GetErrorMessageByTrackingID(job.TrackingID)
		errorCode := http.StatusInternalServerError
		if errorDetails.HttpCode != nil {
			errorCode = *errorDetails.HttpCode
		}
		errorMessage := job.ErrorMessage
		if errorMessage == "" {
			errorMessage = errorDetails.Message
		}
		response.Error = vcmserver.NewOptErrorResponseV1(vcmserver.ErrorResponseV1{
			Code:      float64(errorCode),
			Message:   errorMessage,
			Retriable: vcmserver.NewOptBool(job.Retriable),
		})
		return response, nil
	default:
		return &vcmserver.OperationGetV1InternalServerError{
			Code:    500,
			Message: "Operation has an invalid state",
		}, nil
	}
}

func operationMetadataFromJob(job *vcmdb.Job) vcmserver.OptOperationMetadataV1 {
	meta := vcmserver.OperationMetadataV1{}
	if job.CurrentStep != "" {
		meta.CurrentStep = vcmserver.NewOptString(job.CurrentStep)
	}
	if !job.CreatedAt.IsZero() {
		meta.CreateTime = vcmserver.NewOptDateTime(job.CreatedAt.UTC())
	}
	if job.IsTerminal() && !job.UpdatedAt.IsZero() {
		meta.EndTime = vcmserver.NewOptDateTime(job.UpdatedAt.UTC())
	}
	return vcmserver.NewOptOperationMetadataV1(meta)
}
