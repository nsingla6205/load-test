package api

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	errMsgEmptySecretOCID               = "secretOCID must not be empty"
	errMsgInvalidSecretOCID             = "secretOCID must be a valid OCID"
	errMsgRotateFabricPoolKeysBodyEmpty = "request body is required"
)

func normalizeRotateFabricPoolKeysRequest(req *ociserver.RotateFabricPoolKeysRequest, params *ociserver.RotateFabricPoolKeysParams) {
	if params != nil {
		params.TenancyOcid = strings.TrimSpace(params.TenancyOcid)
		params.PoolOCID = strings.TrimSpace(params.PoolOCID)
	}
	if req == nil {
		return
	}
	req.SecretOCID = strings.TrimSpace(req.SecretOCID)
}

func validateRotateFabricPoolKeysRequest(req *ociserver.RotateFabricPoolKeysRequest, params *ociserver.RotateFabricPoolKeysParams) string {
	if req == nil {
		return errMsgRotateFabricPoolKeysBodyEmpty
	}
	normalizeRotateFabricPoolKeysRequest(req, params)

	if params.PoolOCID == "" {
		return errMsgEmptyPoolOCID
	}
	if !isValidOCID(params.PoolOCID) {
		return errMsgInvalidPoolOCID
	}
	if params.TenancyOcid == "" {
		return errMsgEmptyTenancyOcid
	}
	if !isValidOCID(params.TenancyOcid) {
		return errMsgInvalidTenancyOcid
	}
	if req.SecretOCID == "" {
		return errMsgEmptySecretOCID
	}
	if !isValidOCID(req.SecretOCID) {
		return errMsgInvalidSecretOCID
	}
	return ""
}

func (h *Handler) RotateFabricPoolKeys(ctx context.Context, req *ociserver.RotateFabricPoolKeysRequest, params ociserver.RotateFabricPoolKeysParams) (ociserver.RotateFabricPoolKeysRes, error) {
	logger := util.GetLogger(ctx)

	normalizeRotateFabricPoolKeysRequest(req, &params)
	poolOCID := params.PoolOCID

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-work-request-id in context", "error", err)
		return newRotateFabricPoolKeysBadRequest(uuid.NewString(), poolOCID, invalidOPCRequestID), nil
	}

	if errMsg := validateRotateFabricPoolKeysRequest(req, &params); errMsg != "" {
		logger.Error("RotateFabricPoolKeys request validation failed", "error", errMsg)
		return newRotateFabricPoolKeysBadRequest(opcWorkRequestID, poolOCID, errMsg), nil
	}

	rotateParams := &commonparams.RotateFabricPoolKeysParams{
		AccountName:   params.TenancyOcid,
		PoolOCID:      poolOCID,
		NewSecretOCID: req.SecretOCID,
	}

	workflowID, noChange, err := h.Orchestrator.RotateFabricPoolKeys(ctx, rotateParams)
	if err != nil {
		logger.Error("RotateFabricPoolKeys orchestrator error",
			"poolOCID", poolOCID,
			"error", err.Error(),
		)
		return mapRotateFabricPoolKeysError(opcWorkRequestID, poolOCID, err), nil
	}

	resp := ociserver.RotateFabricPoolKeysAcceptedResponse{
		Status:   ociserver.RotateFabricPoolKeysAcceptedResponseStatusInProgress,
		PoolOCID: poolOCID,
	}
	if noChange {
		resp.Status = ociserver.RotateFabricPoolKeysAcceptedResponseStatusNoChange
	} else {
		resp.WorkflowId = ociserver.NewOptString(workflowID)
	}

	return &ociserver.RotateFabricPoolKeysAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response:         resp,
	}, nil
}

func newRotateFabricPoolKeysBadRequest(opcWorkRequestID, poolOCID, errMsg string) *ociserver.RotateFabricPoolKeysBadRequest {
	return &ociserver.RotateFabricPoolKeysBadRequest{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.PoolOperationErrorResponse{
			Status:       string(workflowquery.WorkflowStatusFailed),
			PoolOCID:     poolOCID,
			ErrorMessage: errMsg,
		},
	}
}

func mapRotateFabricPoolKeysError(opcWorkRequestID, poolOCID string, err error) ociserver.RotateFabricPoolKeysRes {
	switch {
	case utilserrors.IsUserInputValidationErr(err), utilserrors.IsBadRequestErr(err):
		return newRotateFabricPoolKeysBadRequest(opcWorkRequestID, poolOCID, err.Error())
	case utilserrors.IsNotFoundErr(err):
		return &ociserver.RotateFabricPoolKeysNotFound{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolOCID,
				ErrorMessage: err.Error(),
			},
		}
	case utilserrors.IsConflictErr(err):
		msg := err.Error()
		if unwrapped := errors.Unwrap(err); unwrapped != nil {
			msg = unwrapped.Error()
		}
		return &ociserver.RotateFabricPoolKeysConflict{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolOCID,
				ErrorMessage: msg,
			},
		}
	default:
		return &ociserver.RotateFabricPoolKeysInternalServerError{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolOCID,
				ErrorMessage: "Internal server error",
			},
		}
	}
}
