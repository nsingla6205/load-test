package api

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
)

const testResetWorkflowID = "wf-reset-pw-001"

// setEnableAdminTempPassword overrides the package-level enableAdminTempPassword
// flag for the duration of a test and restores it on cleanup, so tests do not
// depend on process env and cannot leak state across cases.
func setEnableAdminTempPassword(t *testing.T, enabled bool) {
	t.Helper()
	orig := enableAdminTempPassword
	enableAdminTempPassword = enabled
	t.Cleanup(func() { enableAdminTempPassword = orig })
}

// setOciAdminTempPasswordExpiryHours overrides the package-level expiry window
// for the duration of a test and restores it on cleanup.
func setOciAdminTempPasswordExpiryHours(t *testing.T, hours int) {
	t.Helper()
	orig := ociAdminTempPasswordExpiryHours
	ociAdminTempPasswordExpiryHours = hours
	t.Cleanup(func() { ociAdminTempPasswordExpiryHours = orig })
}

func defaultResetPasswordParams() ociserver.ResetPasswordParams {
	return ociserver.ResetPasswordParams{
		PoolOCID:    testPoolOCID,
		TenancyOcid: defaultTestTenancyOCID,
	}
}

func resetPasswordRequestWith(tempPassword string) ociserver.OptResetPasswordRequest {
	return ociserver.NewOptResetPasswordRequest(ociserver.ResetPasswordRequest{
		TempPassword: ociserver.NewOptString(tempPassword),
	})
}

func TestResetPassword_MissingOPCRequestID(t *testing.T) {
	h := Handler{}
	res, err := h.ResetPassword(context.Background(), resetPasswordRequestWith("Netapp1234"), defaultResetPasswordParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.ResetPasswordBadRequest)
	assert.True(t, ok, "expected *ResetPasswordBadRequest, got %T", res)
	assert.Equal(t, invalidOPCRequestID, bad.Response.ErrorMessage)
	assert.Equal(t, testPoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
}

// TestResetPassword_FeatureDisabled verifies the ENABLE_ADMIN_TEMP_PASSWORD gate:
// when the feature is off the handler rejects with 400 and never touches the
// orchestrator (a call would panic via the nil Orchestrator).
func TestResetPassword_FeatureDisabled(t *testing.T) {
	setEnableAdminTempPassword(t, false)

	h := Handler{}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	res, err := h.ResetPassword(ctx, resetPasswordRequestWith("Netapp1234"), defaultResetPasswordParams())
	assert.NoError(t, err)

	bad, ok := res.(*ociserver.ResetPasswordBadRequest)
	assert.True(t, ok, "expected *ResetPasswordBadRequest, got %T", res)
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t, errMsgAdminTempPasswordDisabled, bad.Response.ErrorMessage)
	assert.Equal(t, testPoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
}

// TestResetPassword_SuppliedPassword verifies a caller-supplied, policy-compliant
// password is forwarded verbatim and echoed back in the 202 response.
func TestResetPassword_SuppliedPassword(t *testing.T) {
	setEnableAdminTempPassword(t, true)
	setOciAdminTempPasswordExpiryHours(t, 24)
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		ResetAdminPasswordForPool(mock.Anything, mock.MatchedBy(func(p *commonparams.ResetOciAdminPasswordParams) bool {
			return p != nil &&
				p.PoolOCID == testPoolOCID &&
				p.AccountName == defaultTestTenancyOCID &&
				p.TempPassword == "Netapp1234"
		})).
		Return(testResetWorkflowID, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	before := time.Now().UTC()
	res, err := h.ResetPassword(ctx, resetPasswordRequestWith("Netapp1234"), defaultResetPasswordParams())
	after := time.Now().UTC()
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.ResetPasswordAcceptedResponseHeaders)
	assert.True(t, ok, "expected *ResetPasswordAcceptedResponseHeaders, got %T", res)
	assert.Equal(t, defaultTestOPC, accepted.OpcWorkRequestID)
	assert.Equal(t, string(workflowquery.WorkflowStatusInProgress), accepted.Response.Status)
	assert.Equal(t, testPoolOCID, accepted.Response.PoolOCID)
	assert.Equal(t, testResetWorkflowID, accepted.Response.WorkflowId)
	assert.Equal(t, "Netapp1234", accepted.Response.TempPassword)
	// passwordExpiresAt is optional but must be populated with now (UTC) +
	// configured window, within the small window spanning the call, in UTC.
	assert.True(t, accepted.Response.PasswordExpiresAt.Set, "passwordExpiresAt must be set")
	expiresAt := accepted.Response.PasswordExpiresAt.Value
	assert.Equal(t, time.UTC, expiresAt.Location())
	assert.False(t, expiresAt.Before(before.Add(24*time.Hour)))
	assert.False(t, expiresAt.After(after.Add(24*time.Hour)))
}

// TestResetPassword_ExpiryConfigurable verifies the reported passwordExpiresAt
// honors the configured OCI_ADMIN_TEMP_PASSWORD_EXPIRY_HOURS window.
func TestResetPassword_ExpiryConfigurable(t *testing.T) {
	setEnableAdminTempPassword(t, true)
	setOciAdminTempPasswordExpiryHours(t, 48)
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		ResetAdminPasswordForPool(mock.Anything, mock.Anything).
		Return(testResetWorkflowID, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	before := time.Now().UTC()
	res, err := h.ResetPassword(ctx, resetPasswordRequestWith("Netapp1234"), defaultResetPasswordParams())
	after := time.Now().UTC()
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.ResetPasswordAcceptedResponseHeaders)
	assert.True(t, ok, "expected *ResetPasswordAcceptedResponseHeaders, got %T", res)
	assert.True(t, accepted.Response.PasswordExpiresAt.Set, "passwordExpiresAt must be set")
	expiresAt := accepted.Response.PasswordExpiresAt.Value
	assert.False(t, expiresAt.Before(before.Add(48*time.Hour)))
	assert.False(t, expiresAt.After(after.Add(48*time.Hour)))
}

// TestResolveOciAdminTempPasswordExpiryHours verifies the env-backed resolver
// falls back to the default for unset/non-positive values.
func TestResolveOciAdminTempPasswordExpiryHours(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		setEnv   bool
		want     int
	}{
		{name: "unset uses default", setEnv: false, want: defaultOciAdminTempPasswordExpiryHours},
		{name: "custom positive value", setEnv: true, envValue: "72", want: 72},
		{name: "zero falls back to default", setEnv: true, envValue: "0", want: defaultOciAdminTempPasswordExpiryHours},
		{name: "negative falls back to default", setEnv: true, envValue: "-5", want: defaultOciAdminTempPasswordExpiryHours},
		{name: "non-numeric falls back to default", setEnv: true, envValue: "abc", want: defaultOciAdminTempPasswordExpiryHours},
	}

	const envKey = "OCI_ADMIN_TEMP_PASSWORD_EXPIRY_HOURS"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(envKey, tt.envValue)
			} else {
				// Ensure the var is unset for this case, restoring it afterwards.
				if orig, ok := os.LookupEnv(envKey); ok {
					t.Cleanup(func() { _ = os.Setenv(envKey, orig) })
				}
				assert.NoError(t, os.Unsetenv(envKey))
			}
			assert.Equal(t, tt.want, resolveOciAdminTempPasswordExpiryHours())
		})
	}
}

// TestResetPassword_GeneratesWhenEmpty verifies that when no password is
// supplied the handler generates one, forwards it to the orchestrator, and
// echoes the same generated value back.
func TestResetPassword_GeneratesWhenEmpty(t *testing.T) {
	setEnableAdminTempPassword(t, true)
	var forwarded string
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		ResetAdminPasswordForPool(mock.Anything, mock.MatchedBy(func(p *commonparams.ResetOciAdminPasswordParams) bool {
			if p == nil || p.TempPassword == "" {
				return false
			}
			forwarded = p.TempPassword
			return true
		})).
		Return(testResetWorkflowID, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	// Empty request body -> generate.
	res, err := h.ResetPassword(ctx, ociserver.OptResetPasswordRequest{}, defaultResetPasswordParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.ResetPasswordAcceptedResponseHeaders)
	assert.True(t, ok, "expected *ResetPasswordAcceptedResponseHeaders, got %T", res)
	assert.NotEmpty(t, accepted.Response.TempPassword, "a generated password must be returned")
	assert.Equal(t, forwarded, accepted.Response.TempPassword,
		"the generated password echoed to the caller must match the one dispatched")
	// The generated password must itself satisfy the complexity policy.
	assert.NoError(t, validateOciAdminTempPassword(accepted.Response.TempPassword, ociExpertModeUsername))
}

// TestResetPassword_InvalidSuppliedPassword ensures a non-compliant supplied
// password is rejected with 400 before the orchestrator is ever called (the
// nil Orchestrator would panic on any call).
func TestResetPassword_InvalidSuppliedPassword(t *testing.T) {
	setEnableAdminTempPassword(t, true)
	h := Handler{}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	res, err := h.ResetPassword(ctx, resetPasswordRequestWith("Ab1"), defaultResetPasswordParams())
	assert.NoError(t, err)

	bad, ok := res.(*ociserver.ResetPasswordBadRequest)
	assert.True(t, ok, "expected *ResetPasswordBadRequest, got %T", res)
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t,
		fmt.Sprintf(errMsgTempPasswordLength, ociAdminTempPasswordMinLen, ociAdminTempPasswordMaxLen),
		bad.Response.ErrorMessage)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
}

func TestResetPassword_OrchestratorErrors(t *testing.T) {
	tests := []struct {
		name            string
		orchestratorErr error
		assertResponse  func(t *testing.T, res ociserver.ResetPasswordRes)
	}{
		{
			name:            "NotFound returns 404",
			orchestratorErr: utilserrors.NewNotFoundErr("pool", nil),
			assertResponse: func(t *testing.T, res ociserver.ResetPasswordRes) {
				resp, ok := res.(*ociserver.ResetPasswordNotFound)
				assert.True(t, ok, "expected *ResetPasswordNotFound, got %T", res)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "BadRequest returns 400",
			orchestratorErr: utilserrors.NewBadRequestErr("cannot reset password for pool in state DELETING"),
			assertResponse: func(t *testing.T, res ociserver.ResetPasswordRes) {
				resp, ok := res.(*ociserver.ResetPasswordBadRequest)
				assert.True(t, ok, "expected *ResetPasswordBadRequest, got %T", res)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Contains(t, resp.Response.ErrorMessage, "DELETING")
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "generic error returns 500 with generic message",
			orchestratorErr: assert.AnError,
			assertResponse: func(t *testing.T, res ociserver.ResetPasswordRes) {
				resp, ok := res.(*ociserver.ResetPasswordInternalServerError)
				assert.True(t, ok, "expected *ResetPasswordInternalServerError, got %T", res)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, "Internal server error", resp.Response.ErrorMessage,
					"internal details must not leak")
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnableAdminTempPassword(t, true)
			mockOrchestrator := factory.NewMockOrchestratorFactory(t)
			mockOrchestrator.EXPECT().
				ResetAdminPasswordForPool(mock.Anything, mock.Anything).
				Return("", tt.orchestratorErr)

			h := Handler{Orchestrator: mockOrchestrator}
			ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

			res, err := h.ResetPassword(ctx, resetPasswordRequestWith("Netapp1234"), defaultResetPasswordParams())
			assert.NoError(t, err)
			tt.assertResponse(t, res)
		})
	}
}
