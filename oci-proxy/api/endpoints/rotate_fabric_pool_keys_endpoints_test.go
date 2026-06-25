package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
)

const (
	testSecretOCID       = "ocid1.vaultsecret.oc1.iad.testsecret"
	testRotateWorkflowID = "wf-rotate-fpk-001"
)

func defaultRotateFabricPoolKeysParams() ociserver.RotateFabricPoolKeysParams {
	return ociserver.RotateFabricPoolKeysParams{
		PoolOCID:    testPoolOCID,
		TenancyOcid: defaultTestTenancyOCID,
	}
}

func validRotateFabricPoolKeysRequest() *ociserver.RotateFabricPoolKeysRequest {
	return &ociserver.RotateFabricPoolKeysRequest{
		SecretOCID: testSecretOCID,
	}
}

func TestRotateFabricPoolKeys_MissingOPCRequestID(t *testing.T) {
	h := Handler{}
	res, err := h.RotateFabricPoolKeys(context.Background(), validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.RotateFabricPoolKeysBadRequest)
	assert.True(t, ok)
	assert.Equal(t, invalidOPCRequestID, bad.Response.ErrorMessage)
	assert.Equal(t, testPoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
}

// TestRotateFabricPoolKeys_RequestValidation exercises every branch of
// validateRotateFabricPoolKeysRequest. The orchestrator is intentionally
// omitted: validation must fail before the handler reaches the orchestrator,
// so any orchestrator call from these cases would indicate a regression
// (and would panic via the nil Handler.Orchestrator dereference).
func TestRotateFabricPoolKeys_RequestValidation(t *testing.T) {
	type tc struct {
		name       string
		mutate     func(req *ociserver.RotateFabricPoolKeysRequest, params *ociserver.RotateFabricPoolKeysParams)
		nilRequest bool
		expectErr  string
	}
	cases := []tc{
		{
			name:       "nil body",
			nilRequest: true,
			expectErr:  errMsgRotateFabricPoolKeysBodyEmpty,
		},
		{
			name: "empty poolOCID",
			mutate: func(_ *ociserver.RotateFabricPoolKeysRequest, params *ociserver.RotateFabricPoolKeysParams) {
				params.PoolOCID = ""
			},
			expectErr: errMsgEmptyPoolOCID,
		},
		{
			name: "invalid poolOCID",
			mutate: func(_ *ociserver.RotateFabricPoolKeysRequest, params *ociserver.RotateFabricPoolKeysParams) {
				params.PoolOCID = "not-an-ocid"
			},
			expectErr: errMsgInvalidPoolOCID,
		},
		{
			name: "empty tenancy",
			mutate: func(_ *ociserver.RotateFabricPoolKeysRequest, params *ociserver.RotateFabricPoolKeysParams) {
				params.TenancyOcid = ""
			},
			expectErr: errMsgEmptyTenancyOcid,
		},
		{
			name: "invalid tenancy",
			mutate: func(_ *ociserver.RotateFabricPoolKeysRequest, params *ociserver.RotateFabricPoolKeysParams) {
				params.TenancyOcid = "not-an-ocid"
			},
			expectErr: errMsgInvalidTenancyOcid,
		},
		{
			name: "empty secretOCID",
			mutate: func(req *ociserver.RotateFabricPoolKeysRequest, _ *ociserver.RotateFabricPoolKeysParams) {
				req.SecretOCID = ""
			},
			expectErr: errMsgEmptySecretOCID,
		},
		{
			name: "secretOCID whitespace-only",
			mutate: func(req *ociserver.RotateFabricPoolKeysRequest, _ *ociserver.RotateFabricPoolKeysParams) {
				req.SecretOCID = "   "
			},
			expectErr: errMsgEmptySecretOCID,
		},
		{
			name: "invalid secretOCID",
			mutate: func(req *ociserver.RotateFabricPoolKeysRequest, _ *ociserver.RotateFabricPoolKeysParams) {
				req.SecretOCID = "not-an-ocid"
			},
			expectErr: errMsgInvalidSecretOCID,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := Handler{}
			ctx := contextWithOpcRequestID(nil, defaultTestOPC)

			var req *ociserver.RotateFabricPoolKeysRequest
			if !c.nilRequest {
				req = validRotateFabricPoolKeysRequest()
			}
			params := defaultRotateFabricPoolKeysParams()
			if c.mutate != nil {
				c.mutate(req, &params)
			}

			res, err := h.RotateFabricPoolKeys(ctx, req, params)
			assert.NoError(t, err)

			bad, ok := res.(*ociserver.RotateFabricPoolKeysBadRequest)
			assert.True(t, ok, "expected *RotateFabricPoolKeysBadRequest, got %T", res)
			assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
			assert.Equal(t, c.expectErr, bad.Response.ErrorMessage)
			assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
		})
	}
}

// TestRotateFabricPoolKeys_NormalizesInputs verifies trim happens before
// validation (matches other pool endpoints) so accidental whitespace in
// caller-supplied OCIDs does not produce spurious 400s.
func TestRotateFabricPoolKeys_NormalizesInputs(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		RotateFabricPoolKeys(mock.Anything, mock.MatchedBy(func(p *commonparams.RotateFabricPoolKeysParams) bool {
			return p.PoolOCID == testPoolOCID &&
				p.AccountName == defaultTestTenancyOCID &&
				p.NewSecretOCID == testSecretOCID
		})).
		Return(testRotateWorkflowID, false, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(nil, defaultTestOPC)
	req := &ociserver.RotateFabricPoolKeysRequest{
		SecretOCID: "  " + testSecretOCID + "  ",
	}
	params := ociserver.RotateFabricPoolKeysParams{
		PoolOCID:    "  " + testPoolOCID + "  ",
		TenancyOcid: "  " + defaultTestTenancyOCID + "  ",
	}

	res, err := h.RotateFabricPoolKeys(ctx, req, params)
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.RotateFabricPoolKeysAcceptedResponseHeaders)
	assert.True(t, ok)
	assert.Equal(t, defaultTestOPC, accepted.OpcWorkRequestID)
	assert.Equal(t, ociserver.RotateFabricPoolKeysAcceptedResponseStatusInProgress, accepted.Response.Status)
}

func TestRotateFabricPoolKeys_SuccessWithoutVersion(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		RotateFabricPoolKeys(mock.Anything, mock.MatchedBy(func(p *commonparams.RotateFabricPoolKeysParams) bool {
			return p.PoolOCID == testPoolOCID &&
				p.AccountName == defaultTestTenancyOCID &&
				p.NewSecretOCID == testSecretOCID
		})).
		Return(testRotateWorkflowID, false, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(nil, defaultTestOPC)

	res, err := h.RotateFabricPoolKeys(ctx, validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.RotateFabricPoolKeysAcceptedResponseHeaders)
	assert.True(t, ok, "expected *RotateFabricPoolKeysAcceptedResponseHeaders, got %T", res)
	assert.Equal(t, defaultTestOPC, accepted.OpcWorkRequestID)
	assert.Equal(t, ociserver.RotateFabricPoolKeysAcceptedResponseStatusInProgress, accepted.Response.Status)
	assert.Equal(t, testPoolOCID, accepted.Response.PoolOCID)
	assert.True(t, accepted.Response.WorkflowId.IsSet())
	assert.Equal(t, testRotateWorkflowID, accepted.Response.WorkflowId.Value)
}

// TestRotateFabricPoolKeys_NoChangeShortCircuit covers the design's
// same-secret short-circuit: when the orchestrator reports noChange=true,
// the handler must return 202 with status=no_change and an unset
// WorkflowId (not the empty string). Callers branch on Status alone, but
// emitting an empty workflowId would violate the OpenAPI contract.
func TestRotateFabricPoolKeys_NoChangeShortCircuit(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		RotateFabricPoolKeys(mock.Anything, mock.Anything).
		Return("", true, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(nil, defaultTestOPC)

	res, err := h.RotateFabricPoolKeys(ctx, validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.RotateFabricPoolKeysAcceptedResponseHeaders)
	assert.True(t, ok, "expected *RotateFabricPoolKeysAcceptedResponseHeaders, got %T", res)
	assert.Equal(t, defaultTestOPC, accepted.OpcWorkRequestID)
	assert.Equal(t, ociserver.RotateFabricPoolKeysAcceptedResponseStatusNoChange, accepted.Response.Status)
	assert.Equal(t, testPoolOCID, accepted.Response.PoolOCID)
	assert.False(t, accepted.Response.WorkflowId.IsSet(),
		"WorkflowId must be unset when status is no_change; OptString.IsSet=false omits it from the JSON body")
}

func TestRotateFabricPoolKeys_OrchestratorErrors(t *testing.T) {
	tests := []struct {
		name            string
		orchestratorErr error
		assertResponse  func(t *testing.T, res ociserver.RotateFabricPoolKeysRes)
	}{
		{
			name:            "NotFound returns 404",
			orchestratorErr: utilserrors.NewNotFoundErr("pool", nil),
			assertResponse: func(t *testing.T, res ociserver.RotateFabricPoolKeysRes) {
				resp, ok := res.(*ociserver.RotateFabricPoolKeysNotFound)
				assert.True(t, ok, "expected *RotateFabricPoolKeysNotFound, got %T", res)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
				assert.Contains(t, resp.Response.ErrorMessage, "pool not found")
			},
		},
		{
			name:            "BadRequest returns 400",
			orchestratorErr: utilserrors.NewBadRequestErr("PoolOCID is required"),
			assertResponse: func(t *testing.T, res ociserver.RotateFabricPoolKeysRes) {
				resp, ok := res.(*ociserver.RotateFabricPoolKeysBadRequest)
				assert.True(t, ok, "expected *RotateFabricPoolKeysBadRequest, got %T", res)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Equal(t, "PoolOCID is required", resp.Response.ErrorMessage)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "UserInputValidation returns 400",
			orchestratorErr: utilserrors.NewUserInputValidationErr("payload shape invalid"),
			assertResponse: func(t *testing.T, res ociserver.RotateFabricPoolKeysRes) {
				resp, ok := res.(*ociserver.RotateFabricPoolKeysBadRequest)
				assert.True(t, ok, "expected *RotateFabricPoolKeysBadRequest, got %T", res)
				assert.Contains(t, resp.Response.ErrorMessage, "payload shape invalid")
			},
		},
		{
			name:            "Conflict returns 409",
			orchestratorErr: utilserrors.NewConflictErr("pool is in UPDATING state"),
			assertResponse: func(t *testing.T, res ociserver.RotateFabricPoolKeysRes) {
				resp, ok := res.(*ociserver.RotateFabricPoolKeysConflict)
				assert.True(t, ok, "expected *RotateFabricPoolKeysConflict, got %T", res)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Contains(t, resp.Response.ErrorMessage, "pool is in UPDATING state")
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "NotImplementedYet falls through to 500 with generic message",
			orchestratorErr: utilserrors.NewNotImplementedYetErr(),
			assertResponse: func(t *testing.T, res ociserver.RotateFabricPoolKeysRes) {
				resp, ok := res.(*ociserver.RotateFabricPoolKeysInternalServerError)
				assert.True(t, ok, "expected *RotateFabricPoolKeysInternalServerError, got %T", res)
				assert.Equal(t, "Internal server error", resp.Response.ErrorMessage,
					"internal details must not leak; map handler returns a generic message")
			},
		},
		{
			name:            "generic error returns 500 with generic message",
			orchestratorErr: assert.AnError,
			assertResponse: func(t *testing.T, res ociserver.RotateFabricPoolKeysRes) {
				resp, ok := res.(*ociserver.RotateFabricPoolKeysInternalServerError)
				assert.True(t, ok, "expected *RotateFabricPoolKeysInternalServerError, got %T", res)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Equal(t, "Internal server error", resp.Response.ErrorMessage)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockOrchestrator := factory.NewMockOrchestratorFactory(t)
			mockOrchestrator.EXPECT().
				RotateFabricPoolKeys(mock.Anything, mock.Anything).
				Return("", false, tt.orchestratorErr)

			h := Handler{Orchestrator: mockOrchestrator}
			ctx := contextWithOpcRequestID(nil, defaultTestOPC)

			res, err := h.RotateFabricPoolKeys(ctx, validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())
			assert.NoError(t, err)
			tt.assertResponse(t, res)
		})
	}
}
