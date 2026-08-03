package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	maintErrMsgNotImplemented = "operation is not yet implemented"

	maintErrMsgMissingOpcWorkRequestID = "opcWorkRequestId is required"
	maintErrMsgMissingRequestBody      = "request body is required"
	maintErrMsgInvalidPoolOCID         = "poolOCID %q must be a valid OCID"
	maintErrMsgInvalidTenancyOcid      = "tenancyOcid %q must be a valid OCID"
	maintErrMsgInvalidInstanceOcids    = "instanceOcids must be a non-empty list of unique OCIDs, each prefixed with %q"
	maintErrMsgInvalidOperationType    = "operationType %q must be one of reboot_migrate, recreate"

	instanceOCIDPrefix = "ocid1.instance."
)

type maintErrKind int

const (
	maintErrBadRequest maintErrKind = iota
	maintErrNotFound
	maintErrConflict
	maintErrPrecondition
	maintErrUnprocessable
	maintErrInternal
)

func newMaintenanceErrorBody(poolOCID, errorMessage string) ociserver.PoolOperationErrorResponse {
	return ociserver.PoolOperationErrorResponse{
		Status:       string(workflowquery.WorkflowStatusFailed),
		PoolOCID:     poolOCID,
		ErrorMessage: errorMessage,
	}
}

func newMaintenanceAccepted(opcWorkRequestID, workflowID string, status ociserver.MaintenanceAcceptedResponseStatus) *ociserver.MaintenanceAcceptedResponseHeaders {
	return &ociserver.MaintenanceAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.MaintenanceAcceptedResponse{
			Status:     status,
			WorkflowId: ociserver.NewOptString(workflowID),
		},
	}
}

func isValidInstanceOCID(s string) bool {
	return isValidOCID(s) && strings.HasPrefix(s, instanceOCIDPrefix)
}

func isValidMaintenanceOperationType(op string) bool {
	return commonparams.MaintenanceOperationType(strings.TrimSpace(op)).IsValid()
}

func normalizeInstanceOcids(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

func validateMaintenanceRequest(req *ociserver.MaintenanceRequest, poolOCID, tenancyOCID string, instanceOcids []string) error {
	if !isValidOCID(poolOCID) {
		return fmt.Errorf(maintErrMsgInvalidPoolOCID, poolOCID)
	}
	if !isValidOCID(tenancyOCID) {
		return fmt.Errorf(maintErrMsgInvalidTenancyOcid, tenancyOCID)
	}
	if req == nil {
		return errors.New(maintErrMsgMissingRequestBody)
	}
	if !validInstanceOcids(instanceOcids) {
		return fmt.Errorf(maintErrMsgInvalidInstanceOcids, instanceOCIDPrefix)
	}
	if !isValidMaintenanceOperationType(string(req.OperationType)) {
		return fmt.Errorf(maintErrMsgInvalidOperationType, req.OperationType)
	}
	return nil
}

func validateStartMaintenanceRequest(req *ociserver.StartMaintenanceRequest, poolOCID, tenancyOCID string, instanceOcids []string) error {
	if !isValidOCID(poolOCID) {
		return fmt.Errorf(maintErrMsgInvalidPoolOCID, poolOCID)
	}
	if !isValidOCID(tenancyOCID) {
		return fmt.Errorf(maintErrMsgInvalidTenancyOcid, tenancyOCID)
	}
	if req == nil {
		return errors.New(maintErrMsgMissingRequestBody)
	}
	if !validInstanceOcids(instanceOcids) {
		return fmt.Errorf(maintErrMsgInvalidInstanceOcids, instanceOCIDPrefix)
	}
	return nil
}

func validInstanceOcids(instanceOcids []string) bool {
	if len(instanceOcids) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(instanceOcids))
	for _, ocid := range instanceOcids {
		if !isValidInstanceOCID(ocid) {
			return false
		}
		if _, dup := seen[ocid]; dup {
			return false
		}
		seen[ocid] = struct{}{}
	}
	return true
}

func maintenanceStatus(op ociserver.MaintenanceRequestOperationType) ociserver.MaintenanceAcceptedResponseStatus {
	switch op {
	case ociserver.MaintenanceRequestOperationTypeRebootMigrate:
		return ociserver.MaintenanceAcceptedResponseStatusRebootMigrateInProgress
	case ociserver.MaintenanceRequestOperationTypeRecreate:
		return ociserver.MaintenanceAcceptedResponseStatusRecreateInProgress
	default:
		return ociserver.MaintenanceAcceptedResponseStatusRebootMigrateInProgress
	}
}

func mapMaintenanceErrorKind(err error) maintErrKind {
	switch {
	case utilserrors.IsUserInputValidationErr(err), utilserrors.IsBadRequestErr(err):
		return maintErrBadRequest
	case utilserrors.IsNotFoundErr(err):
		return maintErrNotFound
	case utilserrors.IsConflictErr(err):
		return maintErrConflict
	case utilserrors.IsNotReadyErr(err):
		return maintErrPrecondition
	case utilserrors.IsNotSupportedErr(err):
		return maintErrUnprocessable
	default:
		return maintErrInternal
	}
}

func (h *Handler) StartMaintenance(ctx context.Context, _ *ociserver.StartMaintenanceRequest, params ociserver.StartMaintenanceParams) (ociserver.StartMaintenanceRes, error) {
	logger := util.GetLogger(ctx)
	logger.Info("StartMaintenance is not yet implemented")

	// TODO: once VLM supports this, we should call startMaintenanceImpl here.
	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		opcWorkRequestID = uuid.NewString()
	}
	return &ociserver.StartMaintenanceDefaultErrorStatusCodeWithHeaders{
		StatusCode:       http.StatusNotImplemented,
		OpcWorkRequestID: opcWorkRequestID,
		Response:         newMaintenanceErrorBody(strings.TrimSpace(params.PoolOCID), maintErrMsgNotImplemented),
	}, nil
}

func (h *Handler) startMaintenanceImpl(ctx context.Context, req *ociserver.StartMaintenanceRequest, params ociserver.StartMaintenanceParams) (ociserver.StartMaintenanceRes, error) {
	logger := util.GetLogger(ctx)

	poolOCID := strings.TrimSpace(params.PoolOCID)
	tenancyOCID := strings.TrimSpace(params.TenancyOcid)

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-request-id or opc-work-request-id in context", "error", err)
		return startMaintenanceErrorResponse(uuid.NewString(), maintErrBadRequest,
			newMaintenanceErrorBody(poolOCID, maintErrMsgMissingOpcWorkRequestID)), nil
	}

	var instanceOcids []string
	if req != nil {
		instanceOcids = normalizeInstanceOcids(req.InstanceOcids)
	}

	if err := validateStartMaintenanceRequest(req, poolOCID, tenancyOCID, instanceOcids); err != nil {
		logger.Error("start maintenance request validation failed", "poolOCID", poolOCID, "error", err.Error())
		return startMaintenanceErrorResponse(opcWorkRequestID, maintErrBadRequest,
			newMaintenanceErrorBody(poolOCID, err.Error())), nil
	}

	maintParams := &commonparams.PoolMaintenanceParams{
		AccountName:         tenancyOCID,
		PoolOCID:            poolOCID,
		InstanceOCIDs:       instanceOcids,
		DisableAutoGiveBack: req.DisableAutoGiveBack.Or(false),
		WorkflowID:          opcWorkRequestID,
	}

	workflowID, err := h.Orchestrator.StartPoolMaintenance(ctx, maintParams)
	if err != nil {
		kind := mapMaintenanceErrorKind(err)
		logger.Error("start maintenance orchestrator error", "poolOCID", poolOCID, "error", err.Error())
		return startMaintenanceErrorResponse(opcWorkRequestID, kind, newMaintenanceErrorBody(poolOCID, err.Error())), nil
	}

	return &ociserver.MaintenanceAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.MaintenanceAcceptedResponse{
			Status:     ociserver.MaintenanceAcceptedResponseStatusTakeoverInProgress,
			WorkflowId: ociserver.NewOptString(workflowID),
		},
	}, nil
}

func validateFinishMaintenanceRequest(req *ociserver.FinishMaintenanceRequest, poolOCID, tenancyOCID string, instanceOcids []string) error {
	if !isValidOCID(poolOCID) {
		return fmt.Errorf(maintErrMsgInvalidPoolOCID, poolOCID)
	}
	if !isValidOCID(tenancyOCID) {
		return fmt.Errorf(maintErrMsgInvalidTenancyOcid, tenancyOCID)
	}
	if req == nil {
		return errors.New(maintErrMsgMissingRequestBody)
	}
	if !validInstanceOcids(instanceOcids) {
		return fmt.Errorf(maintErrMsgInvalidInstanceOcids, instanceOCIDPrefix)
	}
	return nil
}

func (h *Handler) FinishMaintenance(ctx context.Context, _ *ociserver.FinishMaintenanceRequest, params ociserver.FinishMaintenanceParams) (ociserver.FinishMaintenanceRes, error) {
	logger := util.GetLogger(ctx)
	logger.Info("FinishMaintenance is not yet implemented")

	// TODO: once VLM supports this, we should call finishMaintenanceImpl here.
	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		opcWorkRequestID = uuid.NewString()
	}
	return &ociserver.FinishMaintenanceDefaultErrorStatusCodeWithHeaders{
		StatusCode:       http.StatusNotImplemented,
		OpcWorkRequestID: opcWorkRequestID,
		Response:         newMaintenanceErrorBody(strings.TrimSpace(params.PoolOCID), maintErrMsgNotImplemented),
	}, nil
}

func (h *Handler) finishMaintenanceImpl(ctx context.Context, req *ociserver.FinishMaintenanceRequest, params ociserver.FinishMaintenanceParams) (ociserver.FinishMaintenanceRes, error) {
	logger := util.GetLogger(ctx)

	poolOCID := strings.TrimSpace(params.PoolOCID)
	tenancyOCID := strings.TrimSpace(params.TenancyOcid)

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-request-id or opc-work-request-id in context", "error", err)
		return finishMaintenanceErrorResponse(uuid.NewString(), maintErrBadRequest,
			newMaintenanceErrorBody(poolOCID, maintErrMsgMissingOpcWorkRequestID)), nil
	}

	var instanceOcids []string
	if req != nil {
		instanceOcids = normalizeInstanceOcids(req.InstanceOcids)
	}

	if err := validateFinishMaintenanceRequest(req, poolOCID, tenancyOCID, instanceOcids); err != nil {
		logger.Error("finish maintenance request validation failed", "poolOCID", poolOCID, "error", err.Error())
		return finishMaintenanceErrorResponse(opcWorkRequestID, maintErrBadRequest,
			newMaintenanceErrorBody(poolOCID, err.Error())), nil
	}

	maintParams := &commonparams.PoolMaintenanceParams{
		AccountName:   tenancyOCID,
		PoolOCID:      poolOCID,
		InstanceOCIDs: instanceOcids,
		WorkflowID:    opcWorkRequestID,
	}

	workflowID, err := h.Orchestrator.FinishPoolMaintenance(ctx, maintParams)
	if err != nil {
		kind := mapMaintenanceErrorKind(err)
		logger.Error("finish maintenance orchestrator error", "poolOCID", poolOCID, "error", err.Error())
		return finishMaintenanceErrorResponse(opcWorkRequestID, kind, newMaintenanceErrorBody(poolOCID, err.Error())), nil
	}

	return &ociserver.MaintenanceAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.MaintenanceAcceptedResponse{
			Status:     ociserver.MaintenanceAcceptedResponseStatusGivebackInProgress,
			WorkflowId: ociserver.NewOptString(workflowID),
		},
	}, nil
}

func (h *Handler) ExecuteMaintenance(ctx context.Context, req *ociserver.MaintenanceRequest, params ociserver.ExecuteMaintenanceParams) (ociserver.ExecuteMaintenanceRes, error) {
	logger := util.GetLogger(ctx)

	poolOCID := strings.TrimSpace(params.PoolOCID)
	tenancyOCID := strings.TrimSpace(params.TenancyOcid)

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-request-id or opc-work-request-id in context", "error", err)
		return executeMaintenanceErrorResponse(uuid.NewString(), maintErrBadRequest,
			newMaintenanceErrorBody(poolOCID, maintErrMsgMissingOpcWorkRequestID)), nil
	}

	var instanceOcids []string
	if req != nil {
		instanceOcids = normalizeInstanceOcids(req.InstanceOcids)
	}

	if err := validateMaintenanceRequest(req, poolOCID, tenancyOCID, instanceOcids); err != nil {
		logger.Error("execute maintenance request validation failed", "poolOCID", poolOCID, "error", err.Error())
		return executeMaintenanceErrorResponse(opcWorkRequestID, maintErrBadRequest,
			newMaintenanceErrorBody(poolOCID, err.Error())), nil
	}

	maintParams := &commonparams.PoolMaintenanceParams{
		AccountName:   tenancyOCID,
		PoolOCID:      poolOCID,
		InstanceOCIDs: instanceOcids,
		OperationType: commonparams.MaintenanceOperationType(req.OperationType),
		WorkflowID:    opcWorkRequestID,
	}

	workflowID, err := h.Orchestrator.ExecutePoolMaintenance(ctx, maintParams)
	if err != nil {
		kind := mapMaintenanceErrorKind(err)
		logger.Error("execute maintenance orchestrator error", "poolOCID", poolOCID, "error", err.Error())
		return executeMaintenanceErrorResponse(opcWorkRequestID, kind, newMaintenanceErrorBody(poolOCID, err.Error())), nil
	}

	return &ociserver.MaintenanceAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.MaintenanceAcceptedResponse{
			Status:     maintenanceStatus(req.OperationType),
			WorkflowId: ociserver.NewOptString(workflowID),
		},
	}, nil
}

func startMaintenanceErrorResponse(opcWorkRequestID string, kind maintErrKind, body ociserver.PoolOperationErrorResponse) ociserver.StartMaintenanceRes {
	headers := ociserver.PoolOperationErrorResponseHeaders{OpcWorkRequestID: opcWorkRequestID, Response: body}
	switch kind {
	case maintErrNotFound:
		return (*ociserver.StartMaintenanceNotFound)(&headers)
	case maintErrConflict:
		return (*ociserver.StartMaintenanceConflict)(&headers)
	case maintErrPrecondition:
		return (*ociserver.StartMaintenancePreconditionFailed)(&headers)
	case maintErrUnprocessable:
		return (*ociserver.StartMaintenanceUnprocessableEntity)(&headers)
	case maintErrInternal:
		return (*ociserver.StartMaintenanceInternalServerError)(&headers)
	default:
		return (*ociserver.StartMaintenanceBadRequest)(&headers)
	}
}

func executeMaintenanceErrorResponse(opcWorkRequestID string, kind maintErrKind, body ociserver.PoolOperationErrorResponse) ociserver.ExecuteMaintenanceRes {
	headers := ociserver.PoolOperationErrorResponseHeaders{OpcWorkRequestID: opcWorkRequestID, Response: body}
	switch kind {
	case maintErrNotFound:
		return (*ociserver.ExecuteMaintenanceNotFound)(&headers)
	case maintErrConflict:
		return (*ociserver.ExecuteMaintenanceConflict)(&headers)
	case maintErrPrecondition:
		return (*ociserver.ExecuteMaintenancePreconditionFailed)(&headers)
	case maintErrUnprocessable:
		return (*ociserver.ExecuteMaintenanceUnprocessableEntity)(&headers)
	case maintErrInternal:
		return (*ociserver.ExecuteMaintenanceInternalServerError)(&headers)
	default:
		return (*ociserver.ExecuteMaintenanceBadRequest)(&headers)
	}
}

func finishMaintenanceErrorResponse(opcWorkRequestID string, kind maintErrKind, body ociserver.PoolOperationErrorResponse) ociserver.FinishMaintenanceRes {
	headers := ociserver.PoolOperationErrorResponseHeaders{OpcWorkRequestID: opcWorkRequestID, Response: body}
	switch kind {
	case maintErrNotFound:
		return (*ociserver.FinishMaintenanceNotFound)(&headers)
	case maintErrConflict:
		return (*ociserver.FinishMaintenanceConflict)(&headers)
	case maintErrPrecondition:
		return (*ociserver.FinishMaintenancePreconditionFailed)(&headers)
	case maintErrUnprocessable:
		return (*ociserver.FinishMaintenanceUnprocessableEntity)(&headers)
	case maintErrInternal:
		return (*ociserver.FinishMaintenanceInternalServerError)(&headers)
	default:
		return (*ociserver.FinishMaintenanceBadRequest)(&headers)
	}
}
