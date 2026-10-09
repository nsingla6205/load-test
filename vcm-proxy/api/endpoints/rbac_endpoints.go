package api

import (
	"context"
	"errors"
	"fmt"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	weutil "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ClusterRefreshRbacV1 re-applies the ONTAP-mode RBAC role from the requested
// file via the VCM orchestrator, which owns the durable job lifecycle. VCM does
// not gate on cluster mode or validate the RBAC pair; CLH owns both. Returns the
// long-running Operation (AIP-151) with done=false.
func (h Handler) ClusterRefreshRbacV1(
	ctx context.Context,
	req *vcmserver.OntapModeRbacV1,
	queryParams vcmserver.ClusterRefreshRbacV1Params,
) (vcmserver.ClusterRefreshRbacV1Res, error) {
	logger := weutil.GetLogger(ctx)

	if req == nil {
		return clusterRefreshRbacBadRequest("request body is required", false), nil
	}

	requestID, deadline, err := parseRequestIDAndTimeout(queryParams.RequestId, queryParams.Timeout)
	if err != nil {
		return clusterRefreshRbacBadRequest(err.Error(), false), nil
	}

	params, err := h.ConfigStore.RefreshRbacParams(requestID, deadline, *req)
	if err != nil {
		// Retriable: CLH can re-seed the missing configuration and try again
		// with the same requestId.
		logger.WarnContext(ctx, "refresh-rbac rejected: VCM configuration is incomplete",
			"requestId", requestID, "error", err)
		return clusterRefreshRbacBadRequest(err.Error(), true), nil
	}
	deploymentID := params.VCM.DeploymentID

	logger.InfoContext(ctx, "received refresh-rbac request",
		"deploymentId", deploymentID, "requestId", requestID, "deadline", deadline,
		"rbacFileUrl", params.VCM.OntapModeRbacFileURL)

	operationID, err := h.Orchestrator.UpdateRbacForPoolById(ctx, params)
	if err != nil {
		logger.ErrorContext(ctx, "refresh-rbac orchestrator error",
			"requestId", requestID, "deploymentId", deploymentID, "error", err)
		message := "Internal error occurred"
		// On a conflict the orchestrator returns the operation id of the refresh
		// already in progress; surface it so CLH knows which operation to wait on.
		if conflictErr(err) && operationID != "" {
			message = fmt.Sprintf("RBAC refresh is already in progress for this deployment (operation %s), please wait for it to complete", operationID)
		}
		return &vcmserver.ClusterRefreshRbacV1InternalServerError{
			Code:      500,
			Message:   message,
			Retriable: vcmserver.NewOptBool(true),
		}, nil
	}
	if operationID == "" {
		operationID = requestID
	}

	return &vcmserver.OperationV1{
		Name: "/v1/operations/" + operationID,
		Done: false,
	}, nil
}

// conflictErr reports whether err is a VCP resource-state-conflict error, which
// the orchestrator returns when a refresh is already in progress for the
// deployment.
func conflictErr(err error) bool {
	var ce *vsaerrors.CustomError
	return errors.As(err, &ce) && ce.IsError(vsaerrors.ErrResourceStateConflictError)
}

func clusterRefreshRbacBadRequest(message string, retriable bool) *vcmserver.ClusterRefreshRbacV1BadRequest {
	return &vcmserver.ClusterRefreshRbacV1BadRequest{
		Code:      400,
		Message:   message,
		Retriable: vcmserver.NewOptBool(retriable),
	}
}
