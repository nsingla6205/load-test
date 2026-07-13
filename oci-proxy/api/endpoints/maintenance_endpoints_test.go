package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

const (
	testInstanceOCID1   = "ocid1.instance.oc1.iad.aaaaaaaainstanceone"
	testInstanceOCID2   = "ocid1.instance.oc1.iad.aaaaaaaainstancetwo"
	testMaintWorkflowID = "wf-maint-001"
)

func defaultStartMaintenanceParams() ociserver.StartMaintenanceParams {
	return ociserver.StartMaintenanceParams{
		PoolOCID:    testPoolOCID,
		TenancyOcid: defaultTestTenancyOCID,
	}
}

func defaultExecuteMaintenanceParams() ociserver.ExecuteMaintenanceParams {
	return ociserver.ExecuteMaintenanceParams{
		PoolOCID:    testPoolOCID,
		TenancyOcid: defaultTestTenancyOCID,
	}
}

func validMaintenanceRequest() *ociserver.MaintenanceRequest {
	return &ociserver.MaintenanceRequest{
		InstanceOcids: []string{testInstanceOCID1, testInstanceOCID2},
		OperationType: ociserver.MaintenanceRequestOperationTypeRebootMigrate,
	}
}

func validStartMaintenanceRequest() *ociserver.StartMaintenanceRequest {
	return &ociserver.StartMaintenanceRequest{
		InstanceOcids: []string{testInstanceOCID1, testInstanceOCID2},
	}
}

func TestStartMaintenance_MissingOPCRequestID(t *testing.T) {
	h := Handler{}
	res, err := h.startMaintenanceImpl(context.Background(), validStartMaintenanceRequest(), defaultStartMaintenanceParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.StartMaintenanceBadRequest)
	assert.True(t, ok, "expected *StartMaintenanceBadRequest, got %T", res)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
	assert.Equal(t, maintErrMsgMissingOpcWorkRequestID, bad.Response.ErrorMessage)
}

func TestStartMaintenance_RequestValidation(t *testing.T) {
	cases := []struct {
		name           string
		mutate         func(req *ociserver.StartMaintenanceRequest, params *ociserver.StartMaintenanceParams)
		nilRequest     bool
		expectErrParts []string
	}{
		{
			name: "invalid poolOCID",
			mutate: func(_ *ociserver.StartMaintenanceRequest, params *ociserver.StartMaintenanceParams) {
				params.PoolOCID = "not-an-ocid"
			},
			expectErrParts: []string{"poolOCID", "not-an-ocid"},
		},
		{
			name: "invalid tenancy",
			mutate: func(_ *ociserver.StartMaintenanceRequest, params *ociserver.StartMaintenanceParams) {
				params.TenancyOcid = "not-an-ocid"
			},
			expectErrParts: []string{"tenancyOcid", "not-an-ocid"},
		},
		{
			name: "empty instanceOcids",
			mutate: func(req *ociserver.StartMaintenanceRequest, _ *ociserver.StartMaintenanceParams) {
				req.InstanceOcids = nil
			},
			expectErrParts: []string{"instanceOcids"},
		},
		{
			name: "duplicate instanceOcids",
			mutate: func(req *ociserver.StartMaintenanceRequest, _ *ociserver.StartMaintenanceParams) {
				req.InstanceOcids = []string{testInstanceOCID1, testInstanceOCID1}
			},
			expectErrParts: []string{"instanceOcids"},
		},
		{
			name: "non-instance instanceOcids",
			mutate: func(req *ociserver.StartMaintenanceRequest, _ *ociserver.StartMaintenanceParams) {
				req.InstanceOcids = []string{testPoolOCID}
			},
			expectErrParts: []string{"instanceOcids"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := Handler{}
			ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

			req := validStartMaintenanceRequest()
			if c.nilRequest {
				req = nil
			}
			params := defaultStartMaintenanceParams()
			if c.mutate != nil {
				c.mutate(req, &params)
			}

			res, err := h.startMaintenanceImpl(ctx, req, params)
			assert.NoError(t, err)

			bad, ok := res.(*ociserver.StartMaintenanceBadRequest)
			assert.True(t, ok, "expected *StartMaintenanceBadRequest, got %T", res)
			assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
			assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
			assert.NotEmpty(t, bad.Response.ErrorMessage)
			for _, part := range c.expectErrParts {
				assert.Contains(t, bad.Response.ErrorMessage, part)
			}
		})
	}
}

func TestStartMaintenance_Success(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		StartPoolMaintenance(mock.Anything, mock.MatchedBy(func(p *commonparams.PoolMaintenanceParams) bool {
			return p.PoolOCID == testPoolOCID &&
				p.AccountName == defaultTestTenancyOCID &&
				!p.DisableAutoGiveBack &&
				len(p.InstanceOCIDs) == 2 &&
				p.WorkflowID == defaultTestOPC
		})).
		Return(testMaintWorkflowID, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	res, err := h.startMaintenanceImpl(ctx, validStartMaintenanceRequest(), defaultStartMaintenanceParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
	assert.True(t, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
	assert.Equal(t, defaultTestOPC, accepted.OpcWorkRequestID)
	assert.Equal(t, ociserver.MaintenanceAcceptedResponseStatusTakeoverInProgress, accepted.Response.Status)
	assert.True(t, accepted.Response.WorkflowId.IsSet())
	assert.Equal(t, testMaintWorkflowID, accepted.Response.WorkflowId.Value)
}

func TestStartMaintenance_DisableAutoGiveBackPassthrough(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		StartPoolMaintenance(mock.Anything, mock.MatchedBy(func(p *commonparams.PoolMaintenanceParams) bool {
			return p.DisableAutoGiveBack
		})).
		Return(testMaintWorkflowID, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	req := validStartMaintenanceRequest()
	req.DisableAutoGiveBack = ociserver.NewOptBool(true)

	res, err := h.startMaintenanceImpl(ctx, req, defaultStartMaintenanceParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
	assert.True(t, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
	assert.Equal(t, ociserver.MaintenanceAcceptedResponseStatusTakeoverInProgress, accepted.Response.Status)
}

func TestStartMaintenance_OrchestratorErrors(t *testing.T) {
	tests := []struct {
		name            string
		orchestratorErr error
		assertResponse  func(t *testing.T, res ociserver.StartMaintenanceRes)
	}{
		{
			name:            "NotFound returns 404",
			orchestratorErr: utilserrors.NewNotFoundErr("pool", nil),
			assertResponse: func(t *testing.T, res ociserver.StartMaintenanceRes) {
				resp, ok := res.(*ociserver.StartMaintenanceNotFound)
				assert.True(t, ok, "expected *StartMaintenanceNotFound, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "Conflict returns 409",
			orchestratorErr: utilserrors.NewConflictErr("operation in progress"),
			assertResponse: func(t *testing.T, res ociserver.StartMaintenanceRes) {
				resp, ok := res.(*ociserver.StartMaintenanceConflict)
				assert.True(t, ok, "expected *StartMaintenanceConflict, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "NotReady returns 412",
			orchestratorErr: utilserrors.NewNotReadyErr("prechecks failed"),
			assertResponse: func(t *testing.T, res ociserver.StartMaintenanceRes) {
				resp, ok := res.(*ociserver.StartMaintenancePreconditionFailed)
				assert.True(t, ok, "expected *StartMaintenancePreconditionFailed, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "NotSupported returns 422",
			orchestratorErr: utilserrors.NewNotSupportedErr(),
			assertResponse: func(t *testing.T, res ociserver.StartMaintenanceRes) {
				resp, ok := res.(*ociserver.StartMaintenanceUnprocessableEntity)
				assert.True(t, ok, "expected *StartMaintenanceUnprocessableEntity, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "BadRequest returns 400",
			orchestratorErr: utilserrors.NewBadRequestErr("bad params"),
			assertResponse: func(t *testing.T, res ociserver.StartMaintenanceRes) {
				resp, ok := res.(*ociserver.StartMaintenanceBadRequest)
				assert.True(t, ok, "expected *StartMaintenanceBadRequest, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "generic error returns 500",
			orchestratorErr: assert.AnError,
			assertResponse: func(t *testing.T, res ociserver.StartMaintenanceRes) {
				resp, ok := res.(*ociserver.StartMaintenanceInternalServerError)
				assert.True(t, ok, "expected *StartMaintenanceInternalServerError, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockOrchestrator := factory.NewMockOrchestratorFactory(t)
			mockOrchestrator.EXPECT().
				StartPoolMaintenance(mock.Anything, mock.Anything).
				Return("", tt.orchestratorErr)

			h := Handler{Orchestrator: mockOrchestrator}
			ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

			res, err := h.startMaintenanceImpl(ctx, validStartMaintenanceRequest(), defaultStartMaintenanceParams())
			assert.NoError(t, err)
			tt.assertResponse(t, res)
		})
	}
}

func TestExecuteMaintenance_Success(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		ExecutePoolMaintenance(mock.Anything, mock.MatchedBy(func(p *commonparams.PoolMaintenanceParams) bool {
			return p.PoolOCID == testPoolOCID && p.OperationType == commonparams.MaintenanceOperationReboot
		})).
		Return(testMaintWorkflowID, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	req := validMaintenanceRequest()
	req.OperationType = ociserver.MaintenanceRequestOperationTypeReboot

	res, err := h.ExecuteMaintenance(ctx, req, defaultExecuteMaintenanceParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
	assert.True(t, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
	assert.Equal(t, ociserver.MaintenanceAcceptedResponseStatusRebootInProgress, accepted.Response.Status)
	assert.Equal(t, testMaintWorkflowID, accepted.Response.WorkflowId.Value)
}

func TestExecuteMaintenance_ITCStatus(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		ExecutePoolMaintenance(mock.Anything, mock.MatchedBy(func(p *commonparams.PoolMaintenanceParams) bool {
			return p.OperationType == commonparams.MaintenanceOperationITC
		})).
		Return(testMaintWorkflowID, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	req := validMaintenanceRequest()
	req.OperationType = ociserver.MaintenanceRequestOperationTypeItc

	res, err := h.ExecuteMaintenance(ctx, req, defaultExecuteMaintenanceParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
	assert.True(t, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
	assert.Equal(t, ociserver.MaintenanceAcceptedResponseStatusItcInProgress, accepted.Response.Status)
}

func TestExecuteMaintenance_MissingOPCRequestID(t *testing.T) {
	h := Handler{}
	res, err := h.ExecuteMaintenance(context.Background(), validMaintenanceRequest(), defaultExecuteMaintenanceParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.ExecuteMaintenanceBadRequest)
	assert.True(t, ok, "expected *ExecuteMaintenanceBadRequest, got %T", res)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
	assert.Equal(t, maintErrMsgMissingOpcWorkRequestID, bad.Response.ErrorMessage)
}

func TestExecuteMaintenance_RequestValidation(t *testing.T) {
	cases := []struct {
		name           string
		mutate         func(req *ociserver.MaintenanceRequest, params *ociserver.ExecuteMaintenanceParams)
		expectErrParts []string
	}{
		{
			name: "invalid poolOCID",
			mutate: func(_ *ociserver.MaintenanceRequest, params *ociserver.ExecuteMaintenanceParams) {
				params.PoolOCID = "not-an-ocid"
			},
			expectErrParts: []string{"poolOCID", "not-an-ocid"},
		},
		{
			name: "invalid tenancy",
			mutate: func(_ *ociserver.MaintenanceRequest, params *ociserver.ExecuteMaintenanceParams) {
				params.TenancyOcid = "not-an-ocid"
			},
			expectErrParts: []string{"tenancyOcid", "not-an-ocid"},
		},
		{
			name: "empty instanceOcids",
			mutate: func(req *ociserver.MaintenanceRequest, _ *ociserver.ExecuteMaintenanceParams) {
				req.InstanceOcids = nil
			},
			expectErrParts: []string{"instanceOcids"},
		},
		{
			name: "duplicate instanceOcids",
			mutate: func(req *ociserver.MaintenanceRequest, _ *ociserver.ExecuteMaintenanceParams) {
				req.InstanceOcids = []string{testInstanceOCID1, testInstanceOCID1}
			},
			expectErrParts: []string{"instanceOcids"},
		},
		{
			name: "non-instance instanceOcids",
			mutate: func(req *ociserver.MaintenanceRequest, _ *ociserver.ExecuteMaintenanceParams) {
				req.InstanceOcids = []string{testPoolOCID}
			},
			expectErrParts: []string{"instanceOcids"},
		},
		{
			name: "invalid operationType",
			mutate: func(req *ociserver.MaintenanceRequest, _ *ociserver.ExecuteMaintenanceParams) {
				req.OperationType = ociserver.MaintenanceRequestOperationType("bogus")
			},
			expectErrParts: []string{"operationType", "bogus"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := Handler{}
			ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

			req := validMaintenanceRequest()
			params := defaultExecuteMaintenanceParams()
			if c.mutate != nil {
				c.mutate(req, &params)
			}

			res, err := h.ExecuteMaintenance(ctx, req, params)
			assert.NoError(t, err)

			bad, ok := res.(*ociserver.ExecuteMaintenanceBadRequest)
			assert.True(t, ok, "expected *ExecuteMaintenanceBadRequest, got %T", res)
			assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
			assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
			assert.NotEmpty(t, bad.Response.ErrorMessage)
			for _, part := range c.expectErrParts {
				assert.Contains(t, bad.Response.ErrorMessage, part)
			}
		})
	}
}

func TestExecuteMaintenance_OrchestratorErrors(t *testing.T) {
	tests := []struct {
		name            string
		orchestratorErr error
		assertResponse  func(t *testing.T, res ociserver.ExecuteMaintenanceRes)
	}{
		{
			name:            "NotFound returns 404",
			orchestratorErr: utilserrors.NewNotFoundErr("pool", nil),
			assertResponse: func(t *testing.T, res ociserver.ExecuteMaintenanceRes) {
				resp, ok := res.(*ociserver.ExecuteMaintenanceNotFound)
				assert.True(t, ok, "expected *ExecuteMaintenanceNotFound, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
				assert.NotEmpty(t, resp.Response.ErrorMessage)
			},
		},
		{
			name:            "Conflict returns 409",
			orchestratorErr: utilserrors.NewConflictErr("operation in progress"),
			assertResponse: func(t *testing.T, res ociserver.ExecuteMaintenanceRes) {
				resp, ok := res.(*ociserver.ExecuteMaintenanceConflict)
				assert.True(t, ok, "expected *ExecuteMaintenanceConflict, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "NotReady returns 412",
			orchestratorErr: utilserrors.NewNotReadyErr("prechecks failed"),
			assertResponse: func(t *testing.T, res ociserver.ExecuteMaintenanceRes) {
				resp, ok := res.(*ociserver.ExecuteMaintenancePreconditionFailed)
				assert.True(t, ok, "expected *ExecuteMaintenancePreconditionFailed, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "NotSupported returns 422",
			orchestratorErr: utilserrors.NewNotSupportedErr(),
			assertResponse: func(t *testing.T, res ociserver.ExecuteMaintenanceRes) {
				resp, ok := res.(*ociserver.ExecuteMaintenanceUnprocessableEntity)
				assert.True(t, ok, "expected *ExecuteMaintenanceUnprocessableEntity, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "generic error returns 500",
			orchestratorErr: assert.AnError,
			assertResponse: func(t *testing.T, res ociserver.ExecuteMaintenanceRes) {
				resp, ok := res.(*ociserver.ExecuteMaintenanceInternalServerError)
				assert.True(t, ok, "expected *ExecuteMaintenanceInternalServerError, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockOrchestrator := factory.NewMockOrchestratorFactory(t)
			mockOrchestrator.EXPECT().
				ExecutePoolMaintenance(mock.Anything, mock.Anything).
				Return("", tt.orchestratorErr)

			h := Handler{Orchestrator: mockOrchestrator}
			ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

			res, err := h.ExecuteMaintenance(ctx, validMaintenanceRequest(), defaultExecuteMaintenanceParams())
			assert.NoError(t, err)
			tt.assertResponse(t, res)
		})
	}
}

func defaultFinishMaintenanceParams() ociserver.FinishMaintenanceParams {
	return ociserver.FinishMaintenanceParams{
		PoolOCID:    testPoolOCID,
		TenancyOcid: defaultTestTenancyOCID,
	}
}

func validFinishMaintenanceRequest() *ociserver.FinishMaintenanceRequest {
	return &ociserver.FinishMaintenanceRequest{
		InstanceOcids: []string{testInstanceOCID1, testInstanceOCID2},
	}
}

func TestFinishMaintenance_MissingOPCRequestID(t *testing.T) {
	h := Handler{}
	res, err := h.finishMaintenanceImpl(context.Background(), validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())

	assert.NoError(t, err)
	bad, ok := res.(*ociserver.FinishMaintenanceBadRequest)
	assert.True(t, ok, "expected *FinishMaintenanceBadRequest, got %T", res)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
	assert.Equal(t, maintErrMsgMissingOpcWorkRequestID, bad.Response.ErrorMessage)
}

// TestFinishMaintenance_RequestValidation exercises the finish-specific
// validator. Unlike start/execute, finish has no operationType, so there is no
// operationType failure case here.
func TestFinishMaintenance_RequestValidation(t *testing.T) {
	cases := []struct {
		name           string
		mutate         func(req *ociserver.FinishMaintenanceRequest, params *ociserver.FinishMaintenanceParams)
		nilRequest     bool
		expectErrParts []string
	}{
		{
			name: "invalid poolOCID",
			mutate: func(_ *ociserver.FinishMaintenanceRequest, params *ociserver.FinishMaintenanceParams) {
				params.PoolOCID = "not-an-ocid"
			},
			expectErrParts: []string{"poolOCID", "not-an-ocid"},
		},
		{
			name: "invalid tenancy",
			mutate: func(_ *ociserver.FinishMaintenanceRequest, params *ociserver.FinishMaintenanceParams) {
				params.TenancyOcid = "not-an-ocid"
			},
			expectErrParts: []string{"tenancyOcid", "not-an-ocid"},
		},
		{
			name:           "nil body",
			nilRequest:     true,
			expectErrParts: []string{"request body"},
		},
		{
			name: "empty instanceOcids",
			mutate: func(req *ociserver.FinishMaintenanceRequest, _ *ociserver.FinishMaintenanceParams) {
				req.InstanceOcids = nil
			},
			expectErrParts: []string{"instanceOcids"},
		},
		{
			name: "duplicate instanceOcids",
			mutate: func(req *ociserver.FinishMaintenanceRequest, _ *ociserver.FinishMaintenanceParams) {
				req.InstanceOcids = []string{testInstanceOCID1, testInstanceOCID1}
			},
			expectErrParts: []string{"instanceOcids"},
		},
		{
			name: "non-instance instanceOcids",
			mutate: func(req *ociserver.FinishMaintenanceRequest, _ *ociserver.FinishMaintenanceParams) {
				req.InstanceOcids = []string{testPoolOCID}
			},
			expectErrParts: []string{"instanceOcids"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := Handler{}
			ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

			req := validFinishMaintenanceRequest()
			if c.nilRequest {
				req = nil
			}
			params := defaultFinishMaintenanceParams()
			if c.mutate != nil {
				c.mutate(req, &params)
			}

			res, err := h.finishMaintenanceImpl(ctx, req, params)
			assert.NoError(t, err)

			bad, ok := res.(*ociserver.FinishMaintenanceBadRequest)
			assert.True(t, ok, "expected *FinishMaintenanceBadRequest, got %T", res)
			assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
			assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
			assert.NotEmpty(t, bad.Response.ErrorMessage)
			for _, part := range c.expectErrParts {
				assert.Contains(t, bad.Response.ErrorMessage, part)
			}
		})
	}
}

func TestFinishMaintenance_Success(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		FinishPoolMaintenance(mock.Anything, mock.MatchedBy(func(p *commonparams.PoolMaintenanceParams) bool {
			return p.PoolOCID == testPoolOCID &&
				p.AccountName == defaultTestTenancyOCID &&
				len(p.InstanceOCIDs) == 2 &&
				p.WorkflowID == defaultTestOPC
		})).
		Return(testMaintWorkflowID, nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	res, err := h.finishMaintenanceImpl(ctx, validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
	assert.True(t, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
	assert.Equal(t, defaultTestOPC, accepted.OpcWorkRequestID)
	assert.Equal(t, ociserver.MaintenanceAcceptedResponseStatusGivebackInProgress, accepted.Response.Status)
	assert.True(t, accepted.Response.WorkflowId.IsSet())
	assert.Equal(t, testMaintWorkflowID, accepted.Response.WorkflowId.Value)
}

func TestFinishMaintenance_OrchestratorErrors(t *testing.T) {
	tests := []struct {
		name            string
		orchestratorErr error
		assertResponse  func(t *testing.T, res ociserver.FinishMaintenanceRes)
	}{
		{
			name:            "NotFound returns 404",
			orchestratorErr: utilserrors.NewNotFoundErr("pool", nil),
			assertResponse: func(t *testing.T, res ociserver.FinishMaintenanceRes) {
				resp, ok := res.(*ociserver.FinishMaintenanceNotFound)
				assert.True(t, ok, "expected *FinishMaintenanceNotFound, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "Conflict returns 409",
			orchestratorErr: utilserrors.NewConflictErr("operation in progress"),
			assertResponse: func(t *testing.T, res ociserver.FinishMaintenanceRes) {
				resp, ok := res.(*ociserver.FinishMaintenanceConflict)
				assert.True(t, ok, "expected *FinishMaintenanceConflict, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "NotReady returns 412",
			orchestratorErr: utilserrors.NewNotReadyErr("prechecks failed"),
			assertResponse: func(t *testing.T, res ociserver.FinishMaintenanceRes) {
				resp, ok := res.(*ociserver.FinishMaintenancePreconditionFailed)
				assert.True(t, ok, "expected *FinishMaintenancePreconditionFailed, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "NotSupported returns 422",
			orchestratorErr: utilserrors.NewNotSupportedErr(),
			assertResponse: func(t *testing.T, res ociserver.FinishMaintenanceRes) {
				resp, ok := res.(*ociserver.FinishMaintenanceUnprocessableEntity)
				assert.True(t, ok, "expected *FinishMaintenanceUnprocessableEntity, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "NotImplementedYet falls through to 500 (giveback workflow pending)",
			orchestratorErr: utilserrors.NewNotImplementedYetErr(),
			assertResponse: func(t *testing.T, res ociserver.FinishMaintenanceRes) {
				resp, ok := res.(*ociserver.FinishMaintenanceInternalServerError)
				assert.True(t, ok, "expected *FinishMaintenanceInternalServerError, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
		{
			name:            "generic error returns 500",
			orchestratorErr: assert.AnError,
			assertResponse: func(t *testing.T, res ociserver.FinishMaintenanceRes) {
				resp, ok := res.(*ociserver.FinishMaintenanceInternalServerError)
				assert.True(t, ok, "expected *FinishMaintenanceInternalServerError, got %T", res)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockOrchestrator := factory.NewMockOrchestratorFactory(t)
			mockOrchestrator.EXPECT().
				FinishPoolMaintenance(mock.Anything, mock.Anything).
				Return("", tt.orchestratorErr)

			h := Handler{Orchestrator: mockOrchestrator}
			ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

			res, err := h.finishMaintenanceImpl(ctx, validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())
			assert.NoError(t, err)
			tt.assertResponse(t, res)
		})
	}
}

func TestStartMaintenance_NotImplemented(t *testing.T) {
	h := Handler{}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	res, err := h.StartMaintenance(ctx, validStartMaintenanceRequest(), defaultStartMaintenanceParams())
	assert.NoError(t, err)

	resp, ok := res.(*ociserver.StartMaintenanceDefaultErrorStatusCodeWithHeaders)
	assert.True(t, ok, "expected *StartMaintenanceDefaultErrorStatusCodeWithHeaders, got %T", res)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
	assert.Equal(t, maintErrMsgNotImplemented, resp.Response.ErrorMessage)
}

func TestFinishMaintenance_NotImplemented(t *testing.T) {
	h := Handler{}
	ctx := contextWithOpcRequestID(context.Background(), defaultTestOPC)

	res, err := h.FinishMaintenance(ctx, validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())
	assert.NoError(t, err)

	resp, ok := res.(*ociserver.FinishMaintenanceDefaultErrorStatusCodeWithHeaders)
	assert.True(t, ok, "expected *FinishMaintenanceDefaultErrorStatusCodeWithHeaders, got %T", res)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
	assert.Equal(t, maintErrMsgNotImplemented, resp.Response.ErrorMessage)
}

func TestExecuteMaintenance_Idempotency(t *testing.T) {
	t.Run("replays existing workflow without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.ExecuteMaintenance(ctx, validMaintenanceRequest(), defaultExecuteMaintenanceParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, idempotentOPC, acc.OpcWorkRequestID)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId.Value)
		assert.Equal(tt, ociserver.MaintenanceAcceptedResponseStatusRebootMigrateInProgress, acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "ExecutePoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("completed prior workflow returns completed status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusCompleted}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.ExecuteMaintenance(ctx, validMaintenanceRequest(), defaultExecuteMaintenanceParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, ociserver.MaintenanceAcceptedResponseStatusCompleted, acc.Response.Status)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId.Value)
		mockOrchestrator.AssertNotCalled(tt, "ExecutePoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			ExecutePoolMaintenance(mock.Anything, mock.MatchedBy(func(p *commonparams.PoolMaintenanceParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(idempotentOPC, nil)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.ExecuteMaintenance(ctx, validMaintenanceRequest(), defaultExecuteMaintenanceParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId.Value)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.ExecuteMaintenance(ctx, validMaintenanceRequest(), defaultExecuteMaintenanceParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.ExecuteMaintenanceInternalServerError)
		assert.True(tt, ok, "expected *ExecuteMaintenanceInternalServerError, got %T", res)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "ExecutePoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces conflict instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "maintenance failed"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.ExecuteMaintenance(ctx, validMaintenanceRequest(), defaultExecuteMaintenanceParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.ExecuteMaintenanceConflict)
		assert.True(tt, ok, "expected *ExecuteMaintenanceConflict, got %T", res)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "ExecutePoolMaintenance", mock.Anything, mock.Anything)
	})
}

func TestStartMaintenance_Idempotency(t *testing.T) {
	t.Run("replays existing workflow without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.startMaintenanceImpl(ctx, validStartMaintenanceRequest(), defaultStartMaintenanceParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId.Value)
		mockOrchestrator.AssertNotCalled(tt, "StartPoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("completed prior workflow returns completed status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusCompleted}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.startMaintenanceImpl(ctx, validStartMaintenanceRequest(), defaultStartMaintenanceParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, ociserver.MaintenanceAcceptedResponseStatusCompleted, acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "StartPoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.startMaintenanceImpl(ctx, validStartMaintenanceRequest(), defaultStartMaintenanceParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.StartMaintenanceInternalServerError)
		assert.True(tt, ok, "expected *StartMaintenanceInternalServerError, got %T", res)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "StartPoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces conflict instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusTimedOut}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.startMaintenanceImpl(ctx, validStartMaintenanceRequest(), defaultStartMaintenanceParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.StartMaintenanceConflict)
		assert.True(tt, ok, "expected *StartMaintenanceConflict, got %T", res)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "StartPoolMaintenance", mock.Anything, mock.Anything)
	})
}

func TestFinishMaintenance_Idempotency(t *testing.T) {
	t.Run("replays existing workflow without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.finishMaintenanceImpl(ctx, validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId.Value)
		assert.Equal(tt, ociserver.MaintenanceAcceptedResponseStatusGivebackInProgress, acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "FinishPoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("completed prior workflow returns completed status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusCompleted}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.finishMaintenanceImpl(ctx, validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.MaintenanceAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *MaintenanceAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, ociserver.MaintenanceAcceptedResponseStatusCompleted, acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "FinishPoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.finishMaintenanceImpl(ctx, validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.FinishMaintenanceInternalServerError)
		assert.True(tt, ok, "expected *FinishMaintenanceInternalServerError, got %T", res)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "FinishPoolMaintenance", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces conflict instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusFailed}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		ctx := contextWithOpcRequestID(context.Background(), idempotentOPC)

		res, err := h.finishMaintenanceImpl(ctx, validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.FinishMaintenanceConflict)
		assert.True(tt, ok, "expected *FinishMaintenanceConflict, got %T", res)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "FinishPoolMaintenance", mock.Anything, mock.Anything)
	})
}

func TestMaintenanceStatus_DefaultsToRebootInProgress(t *testing.T) {
	status := maintenanceStatus(ociserver.MaintenanceRequestOperationType("unknown"))
	assert.Equal(t, ociserver.MaintenanceAcceptedResponseStatusRebootInProgress, status)
}

func TestValidateMaintenanceRequest_NilBody(t *testing.T) {
	err := validateMaintenanceRequest(nil, testPoolOCID, defaultTestTenancyOCID, []string{testInstanceOCID1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), maintErrMsgMissingRequestBody)
}

func TestValidateStartMaintenanceRequest_NilBody(t *testing.T) {
	err := validateStartMaintenanceRequest(nil, testPoolOCID, defaultTestTenancyOCID, []string{testInstanceOCID1})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), maintErrMsgMissingRequestBody)
}

func TestStartMaintenance_NotImplemented_MissingOPCFallback(t *testing.T) {
	h := Handler{}

	res, err := h.StartMaintenance(context.Background(), validStartMaintenanceRequest(), defaultStartMaintenanceParams())
	assert.NoError(t, err)

	resp, ok := res.(*ociserver.StartMaintenanceDefaultErrorStatusCodeWithHeaders)
	assert.True(t, ok, "expected *StartMaintenanceDefaultErrorStatusCodeWithHeaders, got %T", res)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	assert.NotEmpty(t, resp.OpcWorkRequestID)
}

func TestFinishMaintenance_NotImplemented_MissingOPCFallback(t *testing.T) {
	h := Handler{}

	res, err := h.FinishMaintenance(context.Background(), validFinishMaintenanceRequest(), defaultFinishMaintenanceParams())
	assert.NoError(t, err)

	resp, ok := res.(*ociserver.FinishMaintenanceDefaultErrorStatusCodeWithHeaders)
	assert.True(t, ok, "expected *FinishMaintenanceDefaultErrorStatusCodeWithHeaders, got %T", res)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	assert.NotEmpty(t, resp.OpcWorkRequestID)
}
