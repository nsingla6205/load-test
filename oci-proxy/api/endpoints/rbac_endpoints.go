package api

import (
	"context"

	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

func (h *Handler) RbacRefreshPool(ctx context.Context, req ociserver.OptRbacRefreshRequest, params ociserver.RbacRefreshPoolParams) (ociserver.RbacRefreshPoolRes, error) {
	logger := util.GetLogger(ctx)
	poolOCID := params.PoolOCID

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		return newRbacRefreshBadRequest(uuid.NewString(), poolOCID, invalidOPCRequestID), nil
	}

	var rbacFilePath string
	if req.IsSet() && req.Value.RbacFilePath.IsSet() {
		rbacFilePath = req.Value.RbacFilePath.Value
	}

	refreshParams := &commonparams.RefreshRbacForPoolParams{
		PoolOCID:    poolOCID,
		AccountName: params.TenancyOcid,
		RbacFileURL: rbacFilePath,
	}

	workflowID, err := h.Orchestrator.UpdateRbacForPoolById(ctx, refreshParams)
	if err != nil {
		logger.Error("Failed to trigger RBAC refresh", "poolOCID", poolOCID, "error", err.Error())
		return mapRbacRefreshError(opcWorkRequestID, poolOCID, err), nil
	}

	return &ociserver.RbacRefreshAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.RbacRefreshAcceptedResponse{
			Status:     string(workflowquery.WorkflowStatusInProgress),
			WorkflowId: workflowID,
			PoolOCID:   poolOCID,
		},
	}, nil
}

func newRbacRefreshBadRequest(opcWorkRequestID, poolOCID, errMsg string) *ociserver.RbacRefreshPoolBadRequest {
	return &ociserver.RbacRefreshPoolBadRequest{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.PoolOperationErrorResponse{
			Status:       string(workflowquery.WorkflowStatusFailed),
			PoolOCID:     poolOCID,
			ErrorMessage: errMsg,
		},
	}
}

func mapRbacRefreshError(opcWorkRequestID, poolOCID string, err error) ociserver.RbacRefreshPoolRes {
	if utilserrors.IsNotFoundErr(err) {
		return &ociserver.RbacRefreshPoolNotFound{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolOCID,
				ErrorMessage: err.Error(),
			},
		}
	}
	if utilserrors.IsBadRequestErr(err) {
		return newRbacRefreshBadRequest(opcWorkRequestID, poolOCID, err.Error())
	}
	return &ociserver.RbacRefreshPoolInternalServerError{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.PoolOperationErrorResponse{
			Status:       string(workflowquery.WorkflowStatusFailed),
			PoolOCID:     poolOCID,
			ErrorMessage: "Internal server error",
		},
	}
}
