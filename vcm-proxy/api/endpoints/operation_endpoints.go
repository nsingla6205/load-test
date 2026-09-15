package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

const (
	hostGroupWorkflowTimeout = 10 * time.Minute
	volumeWorkflowTimeout    = 10 * time.Minute
)

func getOperationID() string {
	return uuid.NewString()
}

func getOperationDeadline(timeout time.Duration) time.Time {
	return time.Now().UTC().Add(timeout)
}

func getHostGroupOperationDeadline() time.Time {
	return getOperationDeadline(hostGroupWorkflowTimeout)
}

func getVolumeOperationDeadline() time.Time {
	return getOperationDeadline(volumeWorkflowTimeout)
}

// describeVCMOperationSurfaceJobErrorDetails reports whether the public operation GET
// should use job.ErrorMessage as error.message (vs catalog text from errors.json).
func describeVCMOperationSurfaceJobErrorDetails(job *vcmdb.Job) bool {
	if job == nil {
		return false
	}
	if job.ErrorMessage != "" &&
		(job.TrackingID == vsaerrors.ErrLargeVolumeBackupRestoreValidation ||
			job.TrackingID == vsaerrors.ErrRestoreVolumeValidation ||
			job.TrackingID == vsaerrors.ErrSFRFilesMissing ||
			job.TrackingID == vsaerrors.ErrSnapshotNotAllowedForVolume ||
			job.TrackingID == vsaerrors.ErrKMSMigrationSdeClientError) {
		return true
	}
	// ROTATE_CMEK_BACKUPS only — not all ErrGCPResourceProvisionError (3003) jobs.
	return job.Type == string(datamodel.JobTypeRotateCmekBackups) && job.ErrorMessage != ""
}

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
			Message: datamodel.OperationNotFound,
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
		if opResponse, ok := operationResponseFromJob(job); ok {
			response.Response = vcmserver.NewOptOperationV1Response(opResponse)
		}
		return response, nil
	case vcmdb.JobStateDone:
		if opResponse, ok := operationResponseFromJob(job); ok {
			response.Response = vcmserver.NewOptOperationV1Response(opResponse)
		}
		return response, nil
	case vcmdb.JobStateError:
		errMsg := vsaerrors.GetErrorMessageByTrackingID(job.TrackingID)
		detailedErrorMessage := errMsg.Message
		if describeVCMOperationSurfaceJobErrorDetails(job) {
			detailedErrorMessage = job.ErrorMessage
		}
		errorCode := http.StatusInternalServerError
		if errMsg.HttpCode != nil {
			errorCode = *errMsg.HttpCode
		}
		response.Error = vcmserver.NewOptErrorResponseV1(vcmserver.ErrorResponseV1{
			Code:      float64(errorCode),
			Message:   detailedErrorMessage,
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

func operationResponseFromJob(job *vcmdb.Job) (vcmserver.OperationV1Response, bool) {
	if job == nil || !vcmdb.HasPersistablePayload(job.ResponsePayload) {
		return nil, false
	}
	opResponse := make(vcmserver.OperationV1Response)
	if err := json.Unmarshal([]byte(job.ResponsePayload), &opResponse); err != nil {
		return nil, false
	}
	if len(opResponse) == 0 {
		return nil, false
	}
	return opResponse, true
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
