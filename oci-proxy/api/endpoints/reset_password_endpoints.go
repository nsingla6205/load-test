package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ociAdminTempPasswordLength is the length of the temporary password generated
// for the ociadmin user when the caller does not supply one. Twelve characters
// with mixed classes satisfies ONTAP's default password complexity policy.
const ociAdminTempPasswordLength = 12

// defaultMaxTempPasswordGenerationAttempts is the fallback retry cap when
// OCI_ADMIN_TEMP_PASSWORD_GENERATION_ATTEMPTS is unset or misconfigured.
const defaultMaxTempPasswordGenerationAttempts = 7

// defaultOciAdminTempPasswordExpiryHours is the fallback expiry window (in
// hours) for the temporary ociadmin password when
// OCI_ADMIN_TEMP_PASSWORD_EXPIRY_HOURS is unset or misconfigured.
const defaultOciAdminTempPasswordExpiryHours = 24

// maxTempPasswordGenerationAttempts bounds how many times ResetPassword regenerates
// a temporary password when the random draw fails validateOciAdminTempPassword.
// utils.GenerateStrongPassword guarantees length and character classes but not the
// "no run of identical characters" rule enforced on caller-supplied passwords, so a
// rare draw (e.g. one containing "aaa") can fail validation. Read once at startup
// from OCI_ADMIN_TEMP_PASSWORD_GENERATION_ATTEMPTS (default 7). A non-positive
// value falls back to the default so generation never runs unbounded or not at all.
var maxTempPasswordGenerationAttempts = resolveMaxTempPasswordGenerationAttempts()

// ociAdminTempPasswordExpiryHours is the number of hours after which the
// temporary ociadmin password is considered expired. Read once at startup from
// OCI_ADMIN_TEMP_PASSWORD_EXPIRY_HOURS (default 24) so the value reported to the
// caller stays configurable. A non-positive value falls back to the default so
// the response never advertises an already-expired password.
var ociAdminTempPasswordExpiryHours = resolveOciAdminTempPasswordExpiryHours()

// resolveMaxTempPasswordGenerationAttempts reads and validates the configured
// retry cap, falling back to the default when unset or non-positive.
func resolveMaxTempPasswordGenerationAttempts() int {
	attempts := env.GetInt("OCI_ADMIN_TEMP_PASSWORD_GENERATION_ATTEMPTS", defaultMaxTempPasswordGenerationAttempts)
	if attempts <= 0 {
		return defaultMaxTempPasswordGenerationAttempts
	}
	return attempts
}

// resolveOciAdminTempPasswordExpiryHours reads and validates the configured
// expiry window, falling back to the default when unset or non-positive.
func resolveOciAdminTempPasswordExpiryHours() int {
	hours := env.GetInt("OCI_ADMIN_TEMP_PASSWORD_EXPIRY_HOURS", defaultOciAdminTempPasswordExpiryHours)
	if hours <= 0 {
		return defaultOciAdminTempPasswordExpiryHours
	}
	return hours
}

// ResetPassword resets the OCI expert-mode admin (ociadmin) password on a single
// pool. If the caller supplies tempPassword it is used verbatim; otherwise a
// strong 12-character password is generated. The operation is asynchronous: it
// dispatches a workflow and returns 202 with the workflow id, the temporary
// password, and the UTC timestamp at which that password expires
// (now + OCI_ADMIN_TEMP_PASSWORD_EXPIRY_HOURS) so the caller can hand both to the
// operator.
func (h *Handler) ResetPassword(ctx context.Context, req ociserver.OptResetPasswordRequest, params ociserver.ResetPasswordParams) (ociserver.ResetPasswordRes, error) {
	logger := util.GetLogger(ctx)
	poolOCID := params.PoolOCID

	opcWorkRequestID, err := opcWorkRequestIDFromContext(ctx)
	if err != nil {
		return newResetPasswordBadRequest(uuid.NewString(), poolOCID, invalidOPCRequestID), nil
	}

	// Gate the operation on the ENABLE_ADMIN_TEMP_PASSWORD feature flag. When the
	// feature is off, reject up front before generating a password or dispatching
	// any workflow, so the proxy and worker stay consistent.
	if !enableAdminTempPassword {
		logger.Warn("Rejecting reset password request: feature disabled", "poolOCID", poolOCID)
		return newResetPasswordBadRequest(opcWorkRequestID, poolOCID, errMsgAdminTempPasswordDisabled), nil
	}

	tempPassword := ""
	if req.IsSet() && req.Value.TempPassword.IsSet() {
		tempPassword = strings.TrimSpace(req.Value.TempPassword.Value)
	}
	if tempPassword == "" {
		generated, genErr := generateCompliantOciAdminTempPassword()
		if genErr != nil {
			logger.Error("Failed to generate temporary password", "poolOCID", poolOCID, "error", genErr.Error())
			return newResetPasswordInternalServerError(opcWorkRequestID, poolOCID), nil
		}
		tempPassword = generated
	} else if err := validateOciAdminTempPassword(tempPassword, ociExpertModeUsername); err != nil {
		// Enforce the same complexity policy as the create-pool ociAdminTempPassword.
		// The error message never echoes the password value.
		return newResetPasswordBadRequest(opcWorkRequestID, poolOCID, err.Error()), nil
	}

	resetParams := &commonparams.ResetOciAdminPasswordParams{
		PoolOCID:     poolOCID,
		AccountName:  params.TenancyOcid,
		TempPassword: tempPassword,
		WorkflowID:   opcWorkRequestID,
	}

	workflowID, err := h.Orchestrator.ResetAdminPasswordForPool(ctx, resetParams)
	if err != nil {
		logger.Error("Failed to trigger OCI admin password reset", "poolOCID", poolOCID, "error", err.Error())
		return mapResetPasswordError(opcWorkRequestID, poolOCID, err), nil
	}

	// The temporary password expires at the current UTC time plus the
	// configured window. This is a best-effort hint for the caller; the actual
	// expiry is enforced on the ONTAP cluster by the reset-password workflow.
	passwordExpiresAt := time.Now().UTC().Add(time.Duration(ociAdminTempPasswordExpiryHours) * time.Hour)

	return &ociserver.ResetPasswordAcceptedResponseHeaders{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.ResetPasswordAcceptedResponse{
			Status:            string(workflowquery.WorkflowStatusInProgress),
			WorkflowId:        workflowID,
			PoolOCID:          poolOCID,
			TempPassword:      tempPassword,
			PasswordExpiresAt: ociserver.NewOptDateTime(passwordExpiresAt),
		},
	}, nil
}

// generateCompliantOciAdminTempPassword returns a generated temporary admin
// password that satisfies validateOciAdminTempPassword — the same complexity
// policy enforced on caller-supplied passwords. utils.GenerateStrongPassword does
// not preclude runs of identical characters, so a rare draw can fail the validator;
// this regenerates up to maxTempPasswordGenerationAttempts times and returns the
// first compliant password. If a genuine generation error occurs it is returned
// immediately; if every attempt produces a non-compliant password (astronomically
// unlikely, indicative of a broken RNG) the last validation error is wrapped and
// returned so the caller surfaces a 500 rather than forwarding a bad password.
func generateCompliantOciAdminTempPassword() (string, error) {
	var lastValidationErr error
	for attempt := 0; attempt < maxTempPasswordGenerationAttempts; attempt++ {
		candidate, err := utils.GenerateStrongPassword(ociAdminTempPasswordLength)
		if err != nil {
			return "", err
		}
		if validationErr := validateOciAdminTempPassword(candidate, ociExpertModeUsername); validationErr != nil {
			lastValidationErr = validationErr
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("failed to generate a compliant temporary password after %d attempts: %w",
		maxTempPasswordGenerationAttempts, lastValidationErr)
}

func newResetPasswordBadRequest(opcWorkRequestID, poolOCID, errMsg string) *ociserver.ResetPasswordBadRequest {
	return &ociserver.ResetPasswordBadRequest{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.PoolOperationErrorResponse{
			Status:       string(workflowquery.WorkflowStatusFailed),
			PoolOCID:     poolOCID,
			ErrorMessage: errMsg,
		},
	}
}

func newResetPasswordInternalServerError(opcWorkRequestID, poolOCID string) *ociserver.ResetPasswordInternalServerError {
	return &ociserver.ResetPasswordInternalServerError{
		OpcWorkRequestID: opcWorkRequestID,
		Response: ociserver.PoolOperationErrorResponse{
			Status:       string(workflowquery.WorkflowStatusFailed),
			PoolOCID:     poolOCID,
			ErrorMessage: "Internal server error",
		},
	}
}

func mapResetPasswordError(opcWorkRequestID, poolOCID string, err error) ociserver.ResetPasswordRes {
	if utilserrors.IsNotFoundErr(err) {
		return &ociserver.ResetPasswordNotFound{
			OpcWorkRequestID: opcWorkRequestID,
			Response: ociserver.PoolOperationErrorResponse{
				Status:       string(workflowquery.WorkflowStatusFailed),
				PoolOCID:     poolOCID,
				ErrorMessage: err.Error(),
			},
		}
	}
	if utilserrors.IsBadRequestErr(err) {
		return newResetPasswordBadRequest(opcWorkRequestID, poolOCID, err.Error())
	}
	return newResetPasswordInternalServerError(opcWorkRequestID, poolOCID)
}
