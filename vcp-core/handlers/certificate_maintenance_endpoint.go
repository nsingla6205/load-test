package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// V1ReconcilePoolCertificateAuthority starts Helm-CA reconciliation for the selected pools.
func (h Handler) V1ReconcilePoolCertificateAuthority(
	ctx context.Context,
	req *oasgenserver.ReconcilePoolCertificateAuthorityRequestV1,
) (oasgenserver.V1ReconcilePoolCertificateAuthorityRes, error) {
	params, err := buildPoolCertificateMaintenanceParams(
		req.SelectionType,
		req.PoolId,
		req.PoolIds,
	)
	if err != nil {
		return &oasgenserver.V1ReconcilePoolCertificateAuthorityBadRequest{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		}, nil
	}
	if h.Orchestrator == nil {
		return &oasgenserver.V1ReconcilePoolCertificateAuthorityInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "certificate maintenance service is unavailable",
		}, nil
	}
	workflowID, jobID, err := h.Orchestrator.StartVsaCAReconciliation(ctx, params)
	if err != nil {
		util.GetLogger(ctx).Error("Failed to start VSA CA reconciliation", "error", err)
		return &oasgenserver.V1ReconcilePoolCertificateAuthorityInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}
	return certificateMaintenanceResponse(jobID, workflowID, req.SelectionType, params.PoolUUIDs), nil
}

// V1GetPoolCertificateAuthorityReconciliationStatus returns parent and per-pool progress
// for a CA reconciliation job.
func (h Handler) V1GetPoolCertificateAuthorityReconciliationStatus(
	ctx context.Context,
	params oasgenserver.V1GetPoolCertificateAuthorityReconciliationStatusParams,
) (oasgenserver.V1GetPoolCertificateAuthorityReconciliationStatusRes, error) {
	status, statusErr := h.certificateMaintenanceStatus(
		ctx,
		params.JobId,
		coremodels.CertificateMaintenanceOperationCAReconciliation,
	)
	if statusErr != nil {
		switch statusErr.code {
		case http.StatusNotFound:
			return &oasgenserver.V1GetPoolCertificateAuthorityReconciliationStatusNotFound{
				Code: float64(statusErr.code), Message: statusErr.message,
			}, nil
		default:
			return &oasgenserver.V1GetPoolCertificateAuthorityReconciliationStatusInternalServerError{
				Code: float64(statusErr.code), Message: statusErr.message,
			}, nil
		}
	}
	return status, nil
}

// V1GetPoolCertificateRotationStatus returns parent and per-pool progress for a forced
// certificate rotation job.
func (h Handler) V1GetPoolCertificateRotationStatus(
	ctx context.Context,
	params oasgenserver.V1GetPoolCertificateRotationStatusParams,
) (oasgenserver.V1GetPoolCertificateRotationStatusRes, error) {
	status, statusErr := h.certificateMaintenanceStatus(
		ctx,
		params.JobId,
		coremodels.CertificateMaintenanceOperationForcedRotation,
	)
	if statusErr != nil {
		switch statusErr.code {
		case http.StatusNotFound:
			return &oasgenserver.V1GetPoolCertificateRotationStatusNotFound{
				Code: float64(statusErr.code), Message: statusErr.message,
			}, nil
		default:
			return &oasgenserver.V1GetPoolCertificateRotationStatusInternalServerError{
				Code: float64(statusErr.code), Message: statusErr.message,
			}, nil
		}
	}
	return status, nil
}

// certificateMaintenanceStatusError carries the HTTP status both certificate maintenance
// status handlers must map onto their own generated response types.
type certificateMaintenanceStatusError struct {
	code    int
	message string
}

// certificateMaintenanceStatus loads job progress and rejects jobs belonging to the other
// certificate maintenance operation, so each status route only reports its own jobs.
func (h Handler) certificateMaintenanceStatus(
	ctx context.Context,
	jobID uuid.UUID,
	operation coremodels.CertificateMaintenanceOperation,
) (*oasgenserver.PoolCertificateMaintenanceStatusResponseV1, *certificateMaintenanceStatusError) {
	if h.Orchestrator == nil {
		return nil, &certificateMaintenanceStatusError{
			code:    http.StatusInternalServerError,
			message: "certificate maintenance service is unavailable",
		}
	}
	progress, err := h.Orchestrator.GetCertificateMaintenanceStatus(ctx, jobID.String())
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return nil, &certificateMaintenanceStatusError{
				code:    http.StatusNotFound,
				message: fmt.Sprintf("certificate maintenance job %s not found", jobID),
			}
		}
		util.GetLogger(ctx).Error("Failed to get certificate maintenance status", "jobId", jobID, "error", err)
		return nil, &certificateMaintenanceStatusError{
			code:    http.StatusInternalServerError,
			message: err.Error(),
		}
	}
	if progress.Operation != operation {
		return nil, &certificateMaintenanceStatusError{
			code:    http.StatusNotFound,
			message: fmt.Sprintf("job %s is a %s job, not %s", jobID, progress.Operation, operation),
		}
	}
	pools := make([]oasgenserver.PoolCertificateMaintenancePoolStatusV1, 0, len(progress.Pools))
	for _, pool := range progress.Pools {
		poolID, parseErr := uuid.Parse(pool.PoolID)
		if parseErr != nil {
			return nil, &certificateMaintenanceStatusError{
				code:    http.StatusInternalServerError,
				message: fmt.Sprintf("invalid stored pool UUID %q", pool.PoolID),
			}
		}
		childJobID, parseErr := uuid.Parse(pool.JobID)
		if parseErr != nil {
			return nil, &certificateMaintenanceStatusError{
				code:    http.StatusInternalServerError,
				message: fmt.Sprintf("invalid stored job UUID %q", pool.JobID),
			}
		}
		pools = append(pools, oasgenserver.PoolCertificateMaintenancePoolStatusV1{
			PoolId: poolID, JobId: childJobID, WorkflowId: pool.WorkflowID,
			Status: oasgenserver.PoolCertificateMaintenancePoolStatusV1Status(pool.Status),
			Error:  oasgenserver.NewOptString(pool.Error),
		})
	}
	return &oasgenserver.PoolCertificateMaintenanceStatusResponseV1{
		JobId:           jobID,
		WorkflowId:      progress.WorkflowID,
		Operation:       oasgenserver.PoolCertificateMaintenanceStatusResponseV1Operation(progress.Operation),
		Status:          oasgenserver.PoolCertificateMaintenanceStatusResponseV1Status(progress.Status),
		SelectionType:   oasgenserver.PoolCertificateMaintenanceSelectionV1(progress.SelectionType),
		DesiredCaUri:    oasgenserver.NewOptString(progress.DesiredCAURI),
		TotalPools:      progress.TotalPools,
		CompletedPools:  progress.CompletedPools,
		FailedPools:     progress.FailedPools,
		InProgressPools: progress.InProgressPools,
		Pools:           pools,
	}, nil
}

// V1ForcePoolCertificateRotation starts on-demand forced certificate rotation.
func (h Handler) V1ForcePoolCertificateRotation(
	ctx context.Context,
	req *oasgenserver.ForcePoolCertificateRotationRequestV1,
) (oasgenserver.V1ForcePoolCertificateRotationRes, error) {
	params, err := buildPoolCertificateMaintenanceParams(
		req.SelectionType,
		req.PoolId,
		req.PoolIds,
	)
	if err != nil {
		return &oasgenserver.V1ForcePoolCertificateRotationBadRequest{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
		}, nil
	}
	if h.Orchestrator == nil {
		return &oasgenserver.V1ForcePoolCertificateRotationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "certificate maintenance service is unavailable",
		}, nil
	}
	workflowID, jobID, err := h.Orchestrator.StartForcedVsaCertificateRotation(ctx, params)
	if err != nil {
		util.GetLogger(ctx).Error("Failed to start forced VSA certificate rotation", "error", err)
		return &oasgenserver.V1ForcePoolCertificateRotationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}
	return certificateMaintenanceResponse(jobID, workflowID, req.SelectionType, params.PoolUUIDs), nil
}

func buildPoolCertificateMaintenanceParams(
	selection oasgenserver.PoolCertificateMaintenanceSelectionV1,
	poolID oasgenserver.OptUUID,
	poolIDs []uuid.UUID,
) (*commonparams.PoolCertificateMaintenanceParams, error) {
	params := &commonparams.PoolCertificateMaintenanceParams{
		SelectionType: commonparams.PoolCertificateMaintenanceSelection(selection),
	}
	selectedPoolID, hasPoolID := poolID.Get()
	switch selection {
	case oasgenserver.PoolCertificateMaintenanceSelectionV1All:
		if hasPoolID || len(poolIDs) > 0 {
			return nil, fmt.Errorf("poolId and poolIds must be omitted when selectionType is all")
		}
	case oasgenserver.PoolCertificateMaintenanceSelectionV1Pool:
		if !hasPoolID {
			return nil, fmt.Errorf("poolId is required when selectionType is pool")
		}
		if len(poolIDs) > 0 {
			return nil, fmt.Errorf("poolIds must be omitted when selectionType is pool")
		}
		params.PoolUUIDs = []string{selectedPoolID.String()}
	case oasgenserver.PoolCertificateMaintenanceSelectionV1PoolList:
		if hasPoolID {
			return nil, fmt.Errorf("poolId must be omitted when selectionType is poolList")
		}
		if len(poolIDs) == 0 {
			return nil, fmt.Errorf("poolIds is required when selectionType is poolList")
		}
		params.PoolUUIDs = make([]string, len(poolIDs))
		for index, id := range poolIDs {
			params.PoolUUIDs[index] = id.String()
		}
	default:
		return nil, fmt.Errorf("unsupported selectionType %q", selection)
	}
	return params, nil
}

func certificateMaintenanceResponse(
	jobID string,
	workflowID string,
	selection oasgenserver.PoolCertificateMaintenanceSelectionV1,
	poolUUIDs []string,
) *oasgenserver.PoolCertificateMaintenanceResponseV1 {
	poolIDs := make([]uuid.UUID, 0, len(poolUUIDs))
	for _, poolUUID := range poolUUIDs {
		id, err := uuid.Parse(poolUUID)
		if err == nil {
			poolIDs = append(poolIDs, id)
		}
	}
	return &oasgenserver.PoolCertificateMaintenanceResponseV1{
		JobId:         uuid.MustParse(jobID),
		WorkflowId:    workflowID,
		SelectionType: selection,
		PoolIds:       poolIDs,
	}
}
