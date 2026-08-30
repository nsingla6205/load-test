package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	ociSvmNamePrefix         = "oci-svm-"
	ociSvmNameMaxTotalLength = 40
)

func ociSvmNameMaxSuffixLength(prefix string) int {
	return ociSvmNameMaxTotalLength - len(prefix)
}

func errMsgSvmNamePrefix(prefix string) string {
	return fmt.Sprintf("name must start with %s", prefix)
}

func errMsgSvmNameTooShort(prefix string) string {
	return fmt.Sprintf("name must be longer than the %s prefix", prefix)
}

func errMsgSvmNameTooLong(maxTotal int) string {
	return fmt.Sprintf("name must be at most %d characters", maxTotal)
}

func generateSvmResourceName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ociSvmNamePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	return trimmed
}

func validateSvmResourceName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, ociSvmNamePrefix) {
		return errMsgSvmNamePrefix(ociSvmNamePrefix)
	}
	if len(trimmed) <= len(ociSvmNamePrefix) {
		return errMsgSvmNameTooShort(ociSvmNamePrefix)
	}
	if len(trimmed) > ociSvmNameMaxTotalLength {
		return errMsgSvmNameTooLong(ociSvmNameMaxTotalLength)
	}
	return ""
}

// validateRequiredOCIDs checks that none of the provided name/value pairs are empty.
// Returns the error message for the first empty value, or "" if all are present.
func validateRequiredOCIDs(fields ...struct{ name, value string }) string {
	for _, f := range fields {
		if f.value == "" {
			return f.name + " is required"
		}
	}
	return ""
}

// validateOCIDFormats checks that all provided values conform to the OCI OCID format.
// Returns the error message for the first invalid value, or "" if all pass.
func validateOCIDFormats(fields ...struct{ name, value string }) string {
	for _, f := range fields {
		if !isValidOCID(f.value) {
			return f.name + " is not a valid OCID"
		}
	}
	return ""
}

// svmErrorResponse builds the SvmOperationErrorResponse body shared by all SVM
// error returns. status defaults to workflowquery.WorkflowStatusFailed.
func svmErrorResponse(svmOCID, errorMessage string) ociserver.SvmOperationErrorResponse {
	return ociserver.SvmOperationErrorResponse{
		Status:       string(workflowquery.WorkflowStatusFailed),
		SvmOCID:      svmOCID,
		ErrorMessage: errorMessage,
	}
}

func svmOperationErrorHeaders(opcWorkRequestID, svmOCID, errorMessage string) ociserver.SvmOperationErrorResponseHeaders {
	return ociserver.SvmOperationErrorResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response:         svmErrorResponse(svmOCID, errorMessage),
	}
}

// svmOrchestratorErrorMessage returns the client-facing message for an orchestrator error.
func svmOrchestratorErrorMessage(err error) string {
	if err == nil {
		return "Internal server error"
	}
	if utilserrors.IsConflictErr(err) {
		if unwrapped := errors.Unwrap(err); unwrapped != nil {
			return unwrapped.Error()
		}
	}
	if utilserrors.IsUserInputValidationErr(err) || utilserrors.IsBadRequestErr(err) ||
		utilserrors.IsNotFoundErr(err) || utilserrors.IsConflictErr(err) {
		return err.Error()
	}
	return "Internal server error"
}

func updateSvmBadRequest(opcWorkRequestID, svmOCID, errorMessage string) ociserver.UpdateSvmRes {
	bad := ociserver.UpdateSvmBadRequest(svmOperationErrorHeaders(opcWorkRequestID, svmOCID, errorMessage))
	return &bad
}

func mapUpdateSvmOrchestratorError(ctx context.Context, err error, opcWorkRequestID, svmOCID string) ociserver.UpdateSvmRes {
	hdr := svmOperationErrorHeaders(opcWorkRequestID, svmOCID, svmOrchestratorErrorMessage(err))
	switch {
	case utilserrors.IsUserInputValidationErr(err) || utilserrors.IsBadRequestErr(err):
		bad := ociserver.UpdateSvmBadRequest(hdr)
		return &bad
	case utilserrors.IsNotFoundErr(err):
		notFound := ociserver.UpdateSvmNotFound(hdr)
		return &notFound
	case utilserrors.IsConflictErr(err):
		conflict := ociserver.UpdateSvmConflict(hdr)
		return &conflict
	default:
		util.GetLogger(ctx).Error("UpdateSvm orchestrator error", "svmOCID", svmOCID, "error", err)
		internal := ociserver.UpdateSvmInternalServerError(hdr)
		return &internal
	}
}

// validateSvmAdminPasswordRef validates an OCI Vault secret reference for SVM admin password.
// Returns the parsed version and an empty error message on success.
func validateSvmAdminPasswordRef(adminPassword ociserver.OCIOCIDVersionRef) (version int64, errMsg string) {
	if strings.TrimSpace(adminPassword.Ocid) == "" {
		return 0, "svmAdminPassword.ocid is required"
	}
	if !isValidOCID(adminPassword.Ocid) {
		return 0, "svmAdminPassword.ocid is not a valid OCID"
	}
	trimmedVersion := strings.TrimSpace(adminPassword.Version)
	if trimmedVersion == "" {
		return 0, "svmAdminPassword.version is required"
	}
	parsedVersion, err := strconv.ParseInt(trimmedVersion, 10, 64)
	if err != nil {
		return 0, "svmAdminPassword.version must be a valid integer"
	}
	if parsedVersion < 1 {
		return 0, "svmAdminPassword.version must be greater than or equal to 1"
	}
	return parsedVersion, ""
}

// CreateSvmByPool implements generated createSvmByPool operation.
func (h *Handler) CreateSvmByPool(ctx context.Context, req *ociserver.CreateSvmRequest, params ociserver.CreateSvmByPoolParams) (ociserver.CreateSvmByPoolRes, error) {
	logger := util.GetLogger(ctx)

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-work-request-id in context", "error", err)
		return &ociserver.CreateSvmByPoolBadRequest{
			OpcWorkRequestID: uuid.NewString(),
			Response:         svmErrorResponse(req.SvmOCID, invalidOPCRequestID),
		}, nil
	}

	poolOCID := strings.TrimSpace(params.PoolOCID)
	svmOCID := strings.TrimSpace(req.SvmOCID)
	svmName := ""
	if name, ok := req.Name.Get(); ok {
		svmName = strings.TrimSpace(name)
	}
	tenancyOCID := strings.TrimSpace(params.TenancyOcid)

	if msg := validateRequiredOCIDs(
		struct{ name, value string }{"poolOCID path parameter", poolOCID},
		struct{ name, value string }{"svmOCID", svmOCID},
		struct{ name, value string }{"Tenancy-Ocid", tenancyOCID},
	); msg != "" {
		return &ociserver.CreateSvmByPoolBadRequest{
			OpcWorkRequestID: opcWorkRequestID,
			Response:         svmErrorResponse(svmOCID, msg),
		}, nil
	}

	if msg := validateOCIDFormats(
		struct{ name, value string }{"poolOCID", poolOCID},
		struct{ name, value string }{"svmOCID", svmOCID},
		struct{ name, value string }{"Tenancy-Ocid", tenancyOCID},
	); msg != "" {
		return &ociserver.CreateSvmByPoolBadRequest{
			OpcWorkRequestID: opcWorkRequestID,
			Response:         svmErrorResponse(svmOCID, msg),
		}, nil
	}

	if msg := validateSvmResourceName(svmName); msg != "" {
		return &ociserver.CreateSvmByPoolBadRequest{
			OpcWorkRequestID: opcWorkRequestID,
			Response:         svmErrorResponse(svmOCID, msg),
		}, nil
	}

	name := generateSvmResourceName(svmName)
	logger.Info("Resolved SVM resource name for create", "svmOCID", svmOCID, "inputName", svmName, "svmName", name)

	createSvmParams := &commonparams.CreateSvmParams{
		AccountName:           tenancyOCID,
		PoolOCID:              poolOCID,
		SvmExternalIdentifier: svmOCID,
		Name:                  name,
		Ips:                   req.Ips,
		WorkflowID:            opcWorkRequestID,
	}
	if adminPassword, ok := req.SvmAdminPassword.Get(); ok && strings.TrimSpace(adminPassword.Ocid) != "" {
		svmAdminPasswordVersion, errMsg := validateSvmAdminPasswordRef(adminPassword)
		if errMsg != "" {
			return &ociserver.CreateSvmByPoolBadRequest{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, errMsg),
			}, nil
		}
		createSvmParams.SvmAdminPassword = &commonparams.OciAdminPassword{
			Ocid:    strings.TrimSpace(adminPassword.Ocid),
			Version: svmAdminPasswordVersion,
		}
	}

	workflowID, err := h.Orchestrator.CreateSvm(ctx, createSvmParams)
	if err != nil {
		if utilserrors.IsUserInputValidationErr(err) || utilserrors.IsBadRequestErr(err) {
			return &ociserver.CreateSvmByPoolBadRequest{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, err.Error()),
			}, nil
		}
		if utilserrors.IsNotFoundErr(err) {
			return &ociserver.CreateSvmByPoolNotFound{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, err.Error()),
			}, nil
		}
		if utilserrors.IsConflictErr(err) {
			msg := err.Error()
			if unwrapped := errors.Unwrap(err); unwrapped != nil {
				msg = unwrapped.Error()
			}
			return &ociserver.CreateSvmByPoolConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, msg),
			}, nil
		}
		logger.Error("CreateSvm orchestrator error", "error", err)
		return &ociserver.CreateSvmByPoolInternalServerError{
			OpcWorkRequestID: opcWorkRequestID,
			Response:         svmErrorResponse(svmOCID, "Internal server error"),
		}, nil
	}

	return newCreateSvmAccepted(opcWorkRequestID, svmOCID, workflowID, workflowquery.WorkflowStatusInProgress), nil
}

func newCreateSvmAccepted(opcWorkRequestID, svmOCID, workflowID string, status workflowquery.WorkflowStatus) *ociserver.CreateSvmAcceptedResponseHeaders {
	return &ociserver.CreateSvmAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.CreateSvmAcceptedResponse{
			Status:     string(status),
			WorkflowId: workflowID,
			SvmOCID:    svmOCID,
		},
	}
}

// DeleteSvm implements generated deleteSvm operation.
func (h *Handler) DeleteSvm(ctx context.Context, req ociserver.OptDeleteSvmReq, params ociserver.DeleteSvmParams) (ociserver.DeleteSvmRes, error) {
	logger := util.GetLogger(ctx)

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-work-request-id in context", "error", err)
		return &ociserver.DeleteSvmBadRequest{
			OpcWorkRequestID: uuid.NewString(),
			Response:         svmErrorResponse(params.SvmOCID, invalidOPCRequestID),
		}, nil
	}

	svmOCID := strings.TrimSpace(params.SvmOCID)
	poolOCID := strings.TrimSpace(params.PoolOCID)
	tenancyOCID := strings.TrimSpace(params.TenancyOcid)

	if msg := validateRequiredOCIDs(
		struct{ name, value string }{"svmOCID", svmOCID},
		struct{ name, value string }{"poolOCID", poolOCID},
		struct{ name, value string }{"Tenancy-Ocid", tenancyOCID},
	); msg != "" {
		return &ociserver.DeleteSvmBadRequest{
			OpcWorkRequestID: opcWorkRequestID,
			Response:         svmErrorResponse(svmOCID, msg),
		}, nil
	}

	force := false
	if req.IsSet() {
		force = req.Value.Force.Or(false)
	}

	workflowID, err := h.Orchestrator.DeleteSvm(ctx, &commonparams.DeleteSvmParams{
		PoolOCID:    poolOCID,
		AccountName: tenancyOCID,
		SvmID:       svmOCID,
		Force:       force,
		WorkflowID:  opcWorkRequestID,
	})
	if err != nil {
		if utilserrors.IsUserInputValidationErr(err) || utilserrors.IsBadRequestErr(err) {
			return &ociserver.DeleteSvmBadRequest{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, err.Error()),
			}, nil
		}
		if utilserrors.IsNotFoundErr(err) {
			logger.Info("SVM not found for delete", "svmOCID", svmOCID)
			return &ociserver.DeleteSvmNotFound{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, err.Error()),
			}, nil
		}
		if utilserrors.IsConflictErr(err) {
			msg := err.Error()
			if unwrapped := errors.Unwrap(err); unwrapped != nil {
				msg = unwrapped.Error()
			}
			return &ociserver.DeleteSvmConflict{
				OpcWorkRequestID: opcWorkRequestID,
				Response:         svmErrorResponse(svmOCID, msg),
			}, nil
		}
		logger.Error("DeleteSvm orchestrator error", "svmOCID", svmOCID, "error", err)
		return &ociserver.DeleteSvmInternalServerError{
			OpcWorkRequestID: opcWorkRequestID,
			Response:         svmErrorResponse(svmOCID, "Internal server error"),
		}, nil
	}

	return newDeleteSvmAccepted(opcWorkRequestID, svmOCID, workflowID, workflowquery.WorkflowStatusInProgress), nil
}

func newDeleteSvmAccepted(opcWorkRequestID, svmOCID, workflowID string, status workflowquery.WorkflowStatus) *ociserver.DeleteSvmAcceptedResponseHeaders {
	return &ociserver.DeleteSvmAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.DeleteSvmAcceptedResponse{
			Status:     string(status),
			WorkflowId: workflowID,
			SvmOCID:    svmOCID,
		},
	}
}

// UpdateSvm implements generated updateSvm operation.
func (h *Handler) UpdateSvm(ctx context.Context, req *ociserver.UpdateSvmRequest, params ociserver.UpdateSvmParams) (ociserver.UpdateSvmRes, error) {
	logger := util.GetLogger(ctx)

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		logger.Error("missing opc-work-request-id in context", "error", err)
		return updateSvmBadRequest(uuid.NewString(), params.SvmOCID, invalidOPCRequestID), nil
	}

	poolOCID := strings.TrimSpace(params.PoolOCID)
	svmOCID := strings.TrimSpace(params.SvmOCID)
	tenancyOCID := strings.TrimSpace(params.TenancyOcid)

	if msg := validateRequiredOCIDs(
		struct{ name, value string }{"poolOCID path parameter", poolOCID},
		struct{ name, value string }{"svmOCID path parameter", svmOCID},
		struct{ name, value string }{"Tenancy-Ocid", tenancyOCID},
	); msg != "" {
		return updateSvmBadRequest(opcWorkRequestID, svmOCID, msg), nil
	}

	if msg := validateOCIDFormats(
		struct{ name, value string }{"poolOCID", poolOCID},
		struct{ name, value string }{"svmOCID", svmOCID},
		struct{ name, value string }{"Tenancy-Ocid", tenancyOCID},
	); msg != "" {
		return updateSvmBadRequest(opcWorkRequestID, svmOCID, msg), nil
	}

	svmAdminPasswordVersion, errMsg := validateSvmAdminPasswordRef(req.SvmAdminPassword)
	if errMsg != "" {
		return updateSvmBadRequest(opcWorkRequestID, svmOCID, errMsg), nil
	}

	workflowID, err := h.Orchestrator.UpdateSvm(ctx, &commonparams.UpdateSvmParams{
		PoolOCID:              poolOCID,
		AccountName:           tenancyOCID,
		SvmExternalIdentifier: svmOCID,
		SvmAdminPassword: &commonparams.OciAdminPassword{
			Ocid:    strings.TrimSpace(req.SvmAdminPassword.Ocid),
			Version: svmAdminPasswordVersion,
		},
		WorkflowID: opcWorkRequestID,
	})
	if err != nil {
		return mapUpdateSvmOrchestratorError(ctx, err, opcWorkRequestID, svmOCID), nil
	}

	return newUpdateSvmAccepted(opcWorkRequestID, svmOCID, workflowID, workflowquery.WorkflowStatusInProgress), nil
}

func newUpdateSvmAccepted(opcWorkRequestID, svmOCID, workflowID string, status workflowquery.WorkflowStatus) *ociserver.UpdateSvmAcceptedResponseHeaders {
	return &ociserver.UpdateSvmAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.UpdateSvmAcceptedResponse{
			Status:     string(status),
			WorkflowId: workflowID,
			SvmOCID:    svmOCID,
		},
	}
}
