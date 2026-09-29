package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
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

	// Everything the workflow needs beyond the shape - cluster identity, zones,
	// projects, networking and ONTAP credentials - comes from the configuration
	// CLH pushed through POST /v1/config rather than from this request.
	params, err := h.ConfigStore.CreatePoolParams(requestID, deadline, req.ClusterShape)
	if err != nil {
		// Retriable: CLH can re-seed the missing configuration and try again
		// with the same requestId.
		logger.WarnContext(ctx, "create-cluster rejected: VCM configuration is incomplete",
			"requestId", requestID, "error", err)
		return clusterCreateBadRequest(err.Error(), true), nil
	}
	deploymentID := params.VCM.DeploymentID

	logger.InfoContext(ctx, "received create-cluster request",
		"deploymentId", deploymentID, "requestId", requestID, "timeoutDeadline", deadline)

	_, operationID, err := h.Orchestrator.CreatePool(ctx, params)
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

// ClusterUpdateV1 validates the update-cluster request, resolves the
// configuration-owned inputs from memory, and updates the ONTAP cluster via
// the VCM orchestrator, which owns the durable job lifecycle. Returns the
// long-running Operation (AIP-151) with done=false; clients poll
// GET /v1/operations/{operationId} using the operation ID (== requestId).
func (h Handler) ClusterUpdateV1(
	ctx context.Context,
	req *vcmserver.ClusterV1,
	queryParams vcmserver.ClusterUpdateV1Params,
) (vcmserver.ClusterUpdateV1Res, error) {
	logger := weutil.GetLogger(ctx)

	if req == nil {
		return clusterUpdateBadRequest("request body is required", false), nil
	}

	requestID, deadline, err := parseRequestIDAndTimeout(queryParams.RequestId, queryParams.Timeout)
	if err != nil {
		return clusterUpdateBadRequest(err.Error(), false), nil
	}

	if queryParams.UpdateMask == "" {
		return clusterUpdateBadRequest("updateMask is required", false), nil
	}
	if err := validateClusterUpdateMask(queryParams.UpdateMask); err != nil {
		return clusterUpdateBadRequest(err.Error(), false), nil
	}
	if err := validateProvisionedShapeGroup(req.ClusterShape); err != nil {
		return clusterUpdateBadRequest(err.Error(), false), nil
	}
	if err := validateMaskedClusterShape(queryParams.UpdateMask, req.ClusterShape); err != nil {
		return clusterUpdateBadRequest(err.Error(), false), nil
	}
	if err := validateClusterChangeType(queryParams.ChangeType.Value); err != nil {
		return clusterUpdateBadRequest(err.Error(), false), nil
	}

	changeType := string(queryParams.ChangeType.Value)

	// The provisioned performance fields form one complete target shape. They
	// are never backfilled from VLM SPConfig, whose values describe the
	// physical disk layout rather than the customer-requested shape.
	updateParams, err := h.ConfigStore.UpdatePoolParams(requestID, deadline, req.ClusterShape, queryParams.UpdateMask, changeType)
	if err != nil {
		logger.WarnContext(ctx, "update-cluster rejected: VCM configuration is incomplete",
			"requestId", requestID, "error", err)
		return clusterUpdateBadRequest(err.Error(), true), nil
	}
	deploymentID := updateParams.VCM.DeploymentID

	logger.InfoContext(ctx, "received update-cluster request",
		"deploymentId", deploymentID, "requestId", requestID, "deadline", deadline,
		"updateMask", queryParams.UpdateMask, "changeType", changeType)

	_, operationID, err := h.Orchestrator.UpdatePool(ctx, updateParams)
	if err != nil {
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
			message := customErr.Error()
			if customErr.OriginalErr != nil {
				message = customErr.OriginalErr.Error()
			}
			logger.WarnContext(ctx, "update-cluster duplicate request rejected",
				"requestId", requestID, "deploymentId", deploymentID, "error", err)
			return clusterUpdateConflict(message), nil
		}
		if vsaerrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrResourceNotFound) {
			message := customErr.Error()
			if customErr.OriginalErr != nil {
				message = customErr.OriginalErr.Error()
			}
			logger.WarnContext(ctx, "update-cluster rejected: cluster not found",
				"requestId", requestID, "deploymentId", deploymentID, "error", err)
			return clusterUpdateNotFound(message), nil
		}
		logger.ErrorContext(ctx, "update-cluster orchestrator error", "error", err)
		return &vcmserver.ClusterUpdateV1InternalServerError{
			Code:      500,
			Message:   "Internal error occurred",
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

func validateClusterUpdateMask(updateMask string) error {
	provisionedShapePaths := make(map[string]struct{}, 3)
	for _, path := range strings.Split(updateMask, ",") {
		switch path {
		// Supported clusterShape paths for PATCH
		case "clusterShape.provisionedIops",
			"clusterShape.provisionedSizeInBytes",
			"clusterShape.provisionedThroughputMibps":
			provisionedShapePaths[path] = struct{}{}
			continue
		case "clusterShape",
			"clusterShape.autoTieringConfig",
			"clusterShape.autoTieringConfig.autoTierBucketName",
			"clusterShape.autoTieringConfig.hotTierSizeInBytes",
			"clusterShape.infrastructureSpec",
			"clusterShape.infrastructureSpec.dataDiskCount",
			"clusterShape.infrastructureSpec.dataDiskType",
			"clusterShape.infrastructureSpec.haPairCount",
			"clusterShape.infrastructureSpec.mediatorDiskType",
			"clusterShape.infrastructureSpec.preferredVsaInstanceFamily",
			"clusterShape.infrastructureSpec.systemDiskType",
			"clusterShape.infrastructureSpec.vsaInstanceType",
			"clusterShape.kmsConfig",
			"clusterShape.kmsConfig.keyName",
			"clusterShape.kmsConfig.keyProjectId",
			"clusterShape.kmsConfig.keyRingLocation",
			"clusterShape.kmsConfig.keyRingName",
			"clusterShape.kmsConfig.saPasswordLocation",
			"clusterShape.mode",
			"clusterShape.ontapModeRbac",
			"clusterShape.ontapModeRbac.rbacFileCheckSum",
			"clusterShape.ontapModeRbac.rbacFileURL",
			"clusterShape.qosType",
			"clusterShape.regionIdentifier",
			"clusterShape.userIops",
			"clusterShape.userSizeInBytes",
			"clusterShape.userThroughputMibps":
			return fmt.Errorf("updateMask field path %q is not supported", path)
		default:
			return fmt.Errorf("invalid updateMask field path %q", path)
		}
	}
	if len(provisionedShapePaths) != 0 && len(provisionedShapePaths) != 3 {
		return fmt.Errorf("updateMask must include provisionedIops, provisionedThroughputMibps, and provisionedSizeInBytes together")
	}
	return nil
}

// validateProvisionedShapeGroup requires the customer-requested performance
// fields to be either all present or all absent. A partial shape cannot be
// completed from VLM SPConfig because it contains amplified disk values.
func validateProvisionedShapeGroup(shape vcmserver.ClusterShapeV1) error {
	setCount := 0
	for _, value := range []vcmserver.OptInt64{
		shape.ProvisionedIops,
		shape.ProvisionedThroughputMibps,
		shape.ProvisionedSizeInBytes,
	} {
		if value.IsSet() {
			setCount++
		}
	}
	if setCount != 0 && setCount != 3 {
		return fmt.Errorf("provisionedIops, provisionedThroughputMibps, and provisionedSizeInBytes must be provided together")
	}
	return nil
}

// validateMaskedClusterShape rejects an update whose mask names a performance
// field the request body does not carry a usable value for. Without this the
// omitted field would reach the workflow as 0 and be dispatched to VMRS as a
// request to shrink the pool to nothing.
func validateMaskedClusterShape(updateMask string, shape vcmserver.ClusterShapeV1) error {
	for _, path := range strings.Split(updateMask, ",") {
		var value vcmserver.OptInt64
		switch path {
		case common.VCMUpdateMaskProvisionedIops:
			value = shape.ProvisionedIops
		case common.VCMUpdateMaskProvisionedSizeInBytes:
			value = shape.ProvisionedSizeInBytes
		case common.VCMUpdateMaskProvisionedThroughputMibps:
			value = shape.ProvisionedThroughputMibps
		default:
			continue
		}
		if !value.IsSet() {
			return fmt.Errorf("updateMask names %q but the field is not set in clusterShape", path)
		}
		if value.Value <= 0 {
			return fmt.Errorf("updateMask names %q but its value %d is not greater than zero", path, value.Value)
		}
	}
	return nil
}

func validateClusterChangeType(changeType vcmserver.ChangeTypeV1) error {
	switch changeType {
	case vcmserver.ChangeTypeV1COMPUTE, vcmserver.ChangeTypeV1SCALE:
		return nil
	default:
		return fmt.Errorf("changeType %q is not supported", changeType)
	}
}

func clusterUpdateBadRequest(message string, retriable bool) *vcmserver.ClusterUpdateV1BadRequest {
	return &vcmserver.ClusterUpdateV1BadRequest{
		Code:      400,
		Message:   message,
		Retriable: vcmserver.NewOptBool(retriable),
	}
}

func clusterUpdateConflict(message string) *vcmserver.ClusterUpdateV1Conflict {
	return &vcmserver.ClusterUpdateV1Conflict{
		Code:      409,
		Message:   message,
		Retriable: vcmserver.NewOptBool(false),
	}
}

func clusterUpdateNotFound(message string) *vcmserver.ClusterUpdateV1NotFound {
	return &vcmserver.ClusterUpdateV1NotFound{
		Code:      404,
		Message:   message,
		Retriable: vcmserver.NewOptBool(false),
	}
}
