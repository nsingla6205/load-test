package api

import (
	"context"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	weutil "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ClusterCreateV1 validates the create-cluster request, resolves the
// configuration-owned inputs from memory, and provisions the ONTAP cluster via
// the VCM orchestrator, which owns the durable job lifecycle. Returns the
// long-running Operation (AIP-151) with done=false; clients poll
// GET /v1/operations/{operationId} using the operation ID (== requestId).
func (h Handler) ClusterCreateV1(ctx context.Context, req *vcmserver.ClusterV1, queryParams vcmserver.ClusterCreateV1Params) (vcmserver.ClusterCreateV1Res, error) {
	logger := weutil.GetLogger(ctx)

	if req == nil {
		return clusterCreateBadRequest("request body is required", false), nil
	}

	requestID, deadline, err := parseRequestIDAndTimeout(queryParams.RequestId, queryParams.Timeout)
	if err != nil {
		return clusterCreateBadRequest(err.Error(), false), nil
	}

	if h.ConfigStore == nil {
		logger.ErrorContext(ctx, "create-cluster rejected: VCM config store is not configured",
			"requestId", requestID)
		return &vcmserver.ClusterCreateV1InternalServerError{
			Code:    500,
			Message: "VCM config store is not configured",
		}, nil
	}

	// Everything the workflow needs beyond the shape - cluster identity, zones,
	// projects, networking and ONTAP credentials - comes from the configuration
	// CLH pushed through POST /v1/config rather than from this request.
	poolParams, err := h.ConfigStore.CreatePoolParams(requestID, deadline, req.ClusterShape)
	if err != nil {
		// Retriable: CLH can re-seed the missing configuration and try again
		// with the same requestId.
		logger.WarnContext(ctx, "create-cluster rejected: VCM configuration is incomplete",
			"requestId", requestID, "error", err)
		return clusterCreateBadRequest(err.Error(), true), nil
	}
	deploymentID := poolParams.VCM.DeploymentID

	logger.InfoContext(ctx, "received create-cluster request",
		"deploymentId", deploymentID, "requestId", requestID, "timeoutDeadline", deadline)

	_, operationID, err := h.Orchestrator.CreatePool(ctx, poolParams)
	if err != nil {
		// A duplicate requestId (an operation already exists for this
		// idempotency key) is a client conflict, not a retriable server error:
		// reject it with a clear, non-retriable message instead of returning a
		// 202 the caller would poll against a stale operation.
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
			message := customErr.Error()
			if customErr.OriginalErr != nil {
				message = customErr.OriginalErr.Error()
			}
			logger.WarnContext(ctx, "create-cluster duplicate request rejected",
				"requestId", requestID, "deploymentId", deploymentID, "error", err)
			return clusterCreateConflict(message), nil
		}
		logger.ErrorContext(ctx, "create-cluster orchestrator error", "error", err)
		return &vcmserver.ClusterCreateV1InternalServerError{
			Code:      500,
			Message:   "Internal error occurred",
			Retriable: vcmserver.NewOptBool(true),
		}, nil
	}

	// Prefer requestId as the operation ID; fall back to the dispatcher ID.
	if operationID == "" {
		operationID = requestID
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + operationID,
		Done: false,
	}, nil
}

func clusterCreateBadRequest(message string, retriable bool) *vcmserver.ClusterCreateV1BadRequest {
	return &vcmserver.ClusterCreateV1BadRequest{
		Code:      400,
		Message:   message,
		Retriable: vcmserver.NewOptBool(retriable),
	}
}

// clusterCreateConflict rejects a duplicate requestId (an operation already
// exists for this idempotency key) with a non-retriable 409 Conflict.
func clusterCreateConflict(message string) *vcmserver.ClusterCreateV1Conflict {
	return &vcmserver.ClusterCreateV1Conflict{
		Code:      409,
		Message:   message,
		Retriable: vcmserver.NewOptBool(false),
	}
}
