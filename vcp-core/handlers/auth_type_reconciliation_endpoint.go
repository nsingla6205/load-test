package api

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	authTypeReconciliationQueueFailedMessage  = "Failed to queue pool auth-type reconciliation"
	authTypeReconciliationStatusFailedMessage = "Failed to get pool auth-type reconciliation status"
)

// V1ReconcilePoolAuthType queues auth_type=1 to auth_type=2 reconciliation.
func (h *Handler) V1ReconcilePoolAuthType(
	ctx context.Context,
	req *oasgenserver.AuthTypeReconciliationRequestV1,
) (oasgenserver.V1ReconcilePoolAuthTypeRes, error) {
	if req == nil {
		return &oasgenserver.V1ReconcilePoolAuthTypeBadRequest{
			Code:    400,
			Message: "Request body is required",
		}, nil
	}
	poolUUIDs := make([]string, 0, len(req.PoolUuids))
	for _, poolUUID := range req.PoolUuids {
		poolUUIDs = append(poolUUIDs, poolUUID.String())
	}
	params := &commonparams.ReconcilePoolAuthTypeParams{
		SelectionType: string(req.SelectionType),
		FromAuthType:  int(req.FromAuthType),
		ToAuthType:    int(req.ToAuthType),
		PoolUUIDs:     poolUUIDs,
		BatchSize:     req.BatchSize.Value,
	}

	response, err := h.Orchestrator.ReconcilePoolAuthTypes(ctx, params)
	if err != nil {
		util.GetLogger(ctx).Error("Failed to queue pool auth-type reconciliation", "error", err)
		switch {
		case customerrors.IsBadRequestErr(err):
			return &oasgenserver.V1ReconcilePoolAuthTypeBadRequest{Code: 400, Message: err.Error()}, nil
		case customerrors.IsNotFoundErr(err):
			return &oasgenserver.V1ReconcilePoolAuthTypeNotFound{Code: 404, Message: err.Error()}, nil
		case customerrors.IsConflictErr(err):
			return &oasgenserver.V1ReconcilePoolAuthTypeConflict{Code: 409, Message: err.Error()}, nil
		case customerrors.IsNotImplementedYetErr(err):
			return &oasgenserver.V1ReconcilePoolAuthTypeNotImplemented{Code: 501, Message: err.Error()}, nil
		default:
			return &oasgenserver.V1ReconcilePoolAuthTypeInternalServerError{
				Code:    500,
				Message: authTypeReconciliationQueueFailedMessage,
			}, nil
		}
	}
	accepted, err := authTypeReconciliationResponseToAPI(response)
	if err != nil {
		util.GetLogger(ctx).Error(authTypeReconciliationQueueFailedMessage, "error", err)
		return &oasgenserver.V1ReconcilePoolAuthTypeInternalServerError{
			Code:    500,
			Message: authTypeReconciliationQueueFailedMessage,
		}, nil
	}
	return accepted, nil
}

// V1GetPoolAuthTypeReconciliationStatus returns aggregate per-pool status.
func (h *Handler) V1GetPoolAuthTypeReconciliationStatus(
	ctx context.Context,
	params oasgenserver.V1GetPoolAuthTypeReconciliationStatusParams,
) (oasgenserver.V1GetPoolAuthTypeReconciliationStatusRes, error) {
	status, err := h.Orchestrator.GetPoolAuthTypeReconciliationStatus(ctx, params.JobId.String())
	if err != nil {
		util.GetLogger(ctx).Error("Failed to get pool auth-type reconciliation status",
			"jobID", params.JobId.String(),
			"error", err,
		)
		switch {
		case customerrors.IsBadRequestErr(err):
			return &oasgenserver.V1GetPoolAuthTypeReconciliationStatusBadRequest{Code: 400, Message: err.Error()}, nil
		case customerrors.IsNotFoundErr(err):
			return &oasgenserver.V1GetPoolAuthTypeReconciliationStatusNotFound{Code: 404, Message: err.Error()}, nil
		case customerrors.IsNotImplementedYetErr(err):
			return &oasgenserver.V1GetPoolAuthTypeReconciliationStatusNotImplemented{Code: 501, Message: err.Error()}, nil
		default:
			return &oasgenserver.V1GetPoolAuthTypeReconciliationStatusInternalServerError{
				Code:    500,
				Message: authTypeReconciliationStatusFailedMessage,
			}, nil
		}
	}
	jobID, err := uuid.Parse(status.JobID)
	if err != nil {
		util.GetLogger(ctx).Error(authTypeReconciliationStatusFailedMessage, "error", err, "jobID", status.JobID)
		return &oasgenserver.V1GetPoolAuthTypeReconciliationStatusInternalServerError{
			Code:    500,
			Message: authTypeReconciliationStatusFailedMessage,
		}, nil
	}
	results, err := authTypeReconciliationPoolResultsToAPI(status.Results)
	if err != nil {
		util.GetLogger(ctx).Error(authTypeReconciliationStatusFailedMessage, "error", err, "jobID", status.JobID)
		return &oasgenserver.V1GetPoolAuthTypeReconciliationStatusInternalServerError{
			Code:    500,
			Message: authTypeReconciliationStatusFailedMessage,
		}, nil
	}
	return &oasgenserver.AuthTypeReconciliationStatusV1{
		JobId:           jobID,
		Status:          oasgenserver.AuthTypeReconciliationStatusV1Status(status.Status),
		TotalPools:      status.TotalPools,
		QueuedPools:     status.QueuedPools,
		ProcessingPools: status.ProcessingPools,
		SucceededPools:  status.SucceededPools,
		FailedPools:     status.FailedPools,
		SkippedPools:    status.SkippedPools,
		Results:         results,
	}, nil
}

func authTypeReconciliationResponseToAPI(
	response *models.AuthTypeReconciliationResponse,
) (*oasgenserver.AuthTypeReconciliationResponseV1, error) {
	jobID, err := uuid.Parse(response.JobID)
	if err != nil {
		return nil, fmt.Errorf("parse auth-type reconciliation job id: %w", err)
	}
	results, err := authTypeReconciliationPoolResultsToAPI(response.Results)
	if err != nil {
		return nil, err
	}
	return &oasgenserver.AuthTypeReconciliationResponseV1{
		JobId:        jobID,
		Status:       oasgenserver.AuthTypeReconciliationResponseV1Status(response.Status),
		TotalPools:   response.TotalPools,
		QueuedPools:  response.QueuedPools,
		SkippedPools: response.SkippedPools,
		Results:      results,
	}, nil
}

func authTypeReconciliationPoolResultsToAPI(
	results []models.AuthTypeReconciliationPoolResult,
) ([]oasgenserver.AuthTypeReconciliationPoolResultV1, error) {
	converted := make([]oasgenserver.AuthTypeReconciliationPoolResultV1, 0, len(results))
	for _, result := range results {
		poolUUID, err := uuid.Parse(result.PoolUUID)
		if err != nil {
			return nil, fmt.Errorf("parse auth-type reconciliation pool id %q: %w", result.PoolUUID, err)
		}
		convertedResult := oasgenserver.AuthTypeReconciliationPoolResultV1{
			PoolUuid: poolUUID,
			Status:   oasgenserver.AuthTypeReconciliationPoolResultV1Status(result.Status),
		}
		if result.JobID != "" {
			convertedResult.JobId = oasgenserver.NewOptString(result.JobID)
		}
		if result.Reason != "" {
			convertedResult.Reason = oasgenserver.NewOptString(result.Reason)
		}
		converted = append(converted, convertedResult)
	}
	return converted, nil
}
