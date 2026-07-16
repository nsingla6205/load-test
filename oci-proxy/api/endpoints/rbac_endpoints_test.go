package api

import (
	"context"
	"errors"
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

func defaultRbacRefreshPoolParams() ociserver.RbacRefreshPoolParams {
	return ociserver.RbacRefreshPoolParams{
		PoolOCID:    testPoolOCID,
		TenancyOcid: defaultTestTenancyOCID,
	}
}

func TestRbacRefreshPool_MissingOPCRequestID(t *testing.T) {
	h := Handler{}
	res, err := h.RbacRefreshPool(context.Background(), ociserver.OptRbacRefreshRequest{}, defaultRbacRefreshPoolParams())
	assert.NoError(t, err)
	bad, ok := res.(*ociserver.RbacRefreshPoolBadRequest)
	assert.True(t, ok)
	assert.Equal(t, invalidOPCRequestID, bad.Response.ErrorMessage)
	assert.Equal(t, testPoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
}

func TestRbacRefreshPool_OrchestratorErrors(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectType   string
		expectErrMsg string
	}{
		{
			name:         "NotFound returns 404",
			err:          utilserrors.NewNotFoundErr("pool", nil),
			expectType:   "NotFound",
			expectErrMsg: "pool not found",
		},
		{
			name:         "BadRequest returns 400",
			err:          utilserrors.NewBadRequestErr("pool OCID is required"),
			expectType:   "BadRequest",
			expectErrMsg: "pool OCID is required",
		},
		{
			name:         "generic error returns 500",
			err:          assert.AnError,
			expectType:   "InternalServerError",
			expectErrMsg: "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockOrchestrator := factory.NewMockOrchestratorFactory(t)
			mockOrchestrator.EXPECT().UpdateRbacForPoolById(mock.Anything, mock.Anything).Return("", tt.err)
			h := Handler{Orchestrator: mockOrchestrator}
			ctx := contextWithOpcRequestID(nil, defaultTestOPC)

			res, err := h.RbacRefreshPool(ctx, ociserver.OptRbacRefreshRequest{}, defaultRbacRefreshPoolParams())
			assert.NoError(t, err)

			switch tt.expectType {
			case "NotFound":
				resp, ok := res.(*ociserver.RbacRefreshPoolNotFound)
				assert.True(t, ok)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Contains(t, resp.Response.ErrorMessage, tt.expectErrMsg)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			case "BadRequest":
				resp, ok := res.(*ociserver.RbacRefreshPoolBadRequest)
				assert.True(t, ok)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Equal(t, tt.expectErrMsg, resp.Response.ErrorMessage)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			case "InternalServerError":
				resp, ok := res.(*ociserver.RbacRefreshPoolInternalServerError)
				assert.True(t, ok)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Equal(t, tt.expectErrMsg, resp.Response.ErrorMessage)
				assert.Equal(t, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
			}
		})
	}
}

func TestRbacRefreshPool_SuccessWithoutRbacFilePath(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().UpdateRbacForPoolById(mock.Anything, mock.Anything).Return("wf-rbac-123", nil)
	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(nil, defaultTestOPC)

	res, err := h.RbacRefreshPool(ctx, ociserver.OptRbacRefreshRequest{}, defaultRbacRefreshPoolParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.RbacRefreshAcceptedResponseHeaders)
	assert.True(t, ok, "response should be *ociserver.RbacRefreshAcceptedResponseHeaders")
	assert.Equal(t, defaultTestOPC, accepted.OpcWorkRequestID)
	assert.Equal(t, string(workflowquery.WorkflowStatusInProgress), accepted.Response.Status)
	assert.Equal(t, "wf-rbac-123", accepted.Response.WorkflowId)
	assert.Equal(t, testPoolOCID, accepted.Response.PoolOCID)
}

func TestRbacRefreshPool_SuccessWithRbacFilePath(t *testing.T) {
	rbacURL := "https://objectstorage.us-phoenix-1.oraclecloud.com/n/ns/b/bucket/o/rbac.yaml"
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		UpdateRbacForPoolById(mock.Anything, mock.MatchedBy(func(p *commonparams.RefreshRbacForPoolParams) bool {
			return p.PoolOCID == testPoolOCID &&
				p.AccountName == defaultTestTenancyOCID &&
				p.RbacFileURL == rbacURL
		})).
		Return("wf-rbac-456", nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(nil, defaultTestOPC)
	req := ociserver.OptRbacRefreshRequest{
		Value: ociserver.RbacRefreshRequest{
			RbacFilePath: ociserver.OptString{Value: rbacURL, Set: true},
		},
		Set: true,
	}

	res, err := h.RbacRefreshPool(ctx, req, defaultRbacRefreshPoolParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.RbacRefreshAcceptedResponseHeaders)
	assert.True(t, ok)
	assert.Equal(t, "wf-rbac-456", accepted.Response.WorkflowId)
	assert.Equal(t, testPoolOCID, accepted.Response.PoolOCID)
}

func TestRbacRefreshPool_Idempotency(t *testing.T) {
	t.Run("replays existing workflow status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rbacRefreshPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptRbacRefreshRequest{}, defaultRbacRefreshPoolParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.RbacRefreshAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *RbacRefreshAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), acc.Response.Status)
		assert.Equal(tt, testPoolOCID, acc.Response.PoolOCID)
		mockOrchestrator.AssertNotCalled(tt, "UpdateRbacForPoolById", mock.Anything, mock.Anything)
	})

	t.Run("fresh request forwards to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdateRbacForPoolById(mock.Anything, mock.Anything).Return("wf-rbac-123", nil)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rbacRefreshPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptRbacRefreshRequest{}, defaultRbacRefreshPoolParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.RbacRefreshAcceptedResponseHeaders)
		assert.True(tt, ok, "expected *RbacRefreshAcceptedResponseHeaders, got %T", res)
		assert.Equal(tt, "wf-rbac-123", acc.Response.WorkflowId)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rbacRefreshPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptRbacRefreshRequest{}, defaultRbacRefreshPoolParams())
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.RbacRefreshPoolInternalServerError)
		assert.True(tt, ok, "expected *RbacRefreshPoolInternalServerError, got %T", res)
		mockOrchestrator.AssertNotCalled(tt, "UpdateRbacForPoolById", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces 409 instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "rbac refresh failed: file not accessible"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rbacRefreshPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptRbacRefreshRequest{}, defaultRbacRefreshPoolParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.RbacRefreshPoolConflict)
		assert.True(tt, ok, "expected *RbacRefreshPoolConflict, got %T", res)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		assert.Equal(tt, "rbac refresh failed: file not accessible", resp.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "UpdateRbacForPoolById", mock.Anything, mock.Anything)
	})
}
