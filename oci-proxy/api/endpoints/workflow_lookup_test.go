package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

const idempotentOPC = "11111111-2222-4333-8444-555555555555"

func stubWorkflowQuery(t *testing.T, fn func(ctx context.Context, c client.Client, workflowID, runID string) (workflowquery.Result, error)) {
	t.Helper()
	orig := workflowQueryFn
	workflowQueryFn = fn
	t.Cleanup(func() { workflowQueryFn = orig })
}

func TestLookupExistingWorkflow(t *testing.T) {
	t.Run("nil temporal client short-circuits to not found without querying", func(tt *testing.T) {
		called := false
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			called = true
			return workflowquery.Result{}, nil
		})
		h := &Handler{}
		got, err := h.lookupExistingWorkflow(context.Background(), idempotentOPC)
		assert.NoError(tt, err)
		assert.False(tt, got.Found)
		assert.False(tt, called)
	})

	t.Run("empty opc-request-id short-circuits to not found", func(tt *testing.T) {
		called := false
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			called = true
			return workflowquery.Result{}, nil
		})
		h := &Handler{TemporalClient: &mocks.Client{}}
		got, err := h.lookupExistingWorkflow(context.Background(), "")
		assert.NoError(tt, err)
		assert.False(tt, got.Found)
		assert.False(tt, called)
	})

	t.Run("returns found with live status when workflow exists", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(_ context.Context, _ client.Client, workflowID, _ string) (workflowquery.Result, error) {
			assert.Equal(tt, idempotentOPC, workflowID)
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		h := &Handler{TemporalClient: &mocks.Client{}}
		got, err := h.lookupExistingWorkflow(context.Background(), idempotentOPC)
		assert.NoError(tt, err)
		assert.True(tt, got.Found)
		assert.Equal(tt, workflowquery.WorkflowStatusInProgress, got.Status)
	})

	t.Run("temporal NotFound is reported as not found with nil error", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("workflow not found")
		})
		h := &Handler{TemporalClient: &mocks.Client{}}
		got, err := h.lookupExistingWorkflow(context.Background(), idempotentOPC)
		assert.NoError(tt, err)
		assert.False(tt, got.Found)
	})

	t.Run("non-NotFound error is propagated so caller can fail closed", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		h := &Handler{TemporalClient: &mocks.Client{}}
		got, err := h.lookupExistingWorkflow(context.Background(), idempotentOPC)
		assert.Error(tt, err)
		assert.False(tt, got.Found)
	})
}

func TestCreatePool_Idempotency(t *testing.T) {
	t.Run("replays existing workflow status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusCompleted}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := h.CreatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.OpcWorkRequestID)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusCompleted), acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "CreatePool", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			CreatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(&models.Pool{BaseModel: models.BaseModel{UUID: "pool-x", CreatedAt: time.Now(), UpdatedAt: time.Now()}}, idempotentOPC, nil)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := h.CreatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.CreatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), acc.Response.Status)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := h.CreatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.CreatePoolInternalServerError)
		assert.True(tt, ok)
		mockOrchestrator.AssertNotCalled(tt, "CreatePool", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces failure with reason instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "vlm deployment failed: quota exceeded"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := h.CreatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.CreatePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), conflict.Response.Status)
		assert.Equal(tt, "vlm deployment failed: quota exceeded", conflict.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "CreatePool", mock.Anything, mock.Anything)
	})

	t.Run("timed-out prior workflow surfaces failure with fallback reason when none recorded", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusTimedOut}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := h.CreatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.CreatePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusTimedOut), conflict.Response.Status)
		assert.Contains(tt, conflict.Response.ErrorMessage, "opc-work-request-id")
		mockOrchestrator.AssertNotCalled(tt, "CreatePool", mock.Anything, mock.Anything)
	})
}

func TestUpdatePool_Idempotency(t *testing.T) {
	const poolOCID = "ocid1.pool.oc1.iad.idempotent"
	const tenancy = "ocid1.tenancy.oc1..idempotent"

	t.Run("replays existing workflow status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		req := &ociserver.UpdatePoolRequest{DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true}}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "UpdatePool", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(&models.Pool{BaseModel: models.BaseModel{UUID: "pool-x"}}, idempotentOPC, nil)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		req := &ociserver.UpdatePoolRequest{DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true}}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.UpdatePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		req := &ociserver.UpdatePoolRequest{DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true}}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdatePoolInternalServerError)
		assert.True(tt, ok)
		mockOrchestrator.AssertNotCalled(tt, "UpdatePool", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces failure with reason instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "update failed: no shrink allowed"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		req := &ociserver.UpdatePoolRequest{DataEndpointCount: ociserver.OptInt64{Value: 4, Set: true}}
		params := ociserver.UpdatePoolParams{PoolOCID: poolOCID, TenancyOcid: tenancy}

		res, err := h.UpdatePool(contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.UpdatePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), conflict.Response.Status)
		assert.Equal(tt, "update failed: no shrink allowed", conflict.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "UpdatePool", mock.Anything, mock.Anything)
	})
}

func TestDeletePool_Idempotency(t *testing.T) {
	t.Run("replays existing workflow status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1.iad.del", TenancyOcid: "ocid1.tenancy.oc1..del"}

		res, err := h.DeletePool(contextWithOpcRequestID(context.Background(), idempotentOPC), params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.DeletePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "DeletePool", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			DeletePool(mock.Anything, mock.MatchedBy(func(p *commonparams.DeletePoolParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(&models.Pool{BaseModel: models.BaseModel{UUID: "pool-x"}, State: "DELETING"}, idempotentOPC, nil)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1.iad.del", TenancyOcid: "ocid1.tenancy.oc1..del"}

		res, err := h.DeletePool(contextWithOpcRequestID(context.Background(), idempotentOPC), params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.DeletePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1.iad.del", TenancyOcid: "ocid1.tenancy.oc1..del"}

		res, err := h.DeletePool(contextWithOpcRequestID(context.Background(), idempotentOPC), params)
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.DeletePoolInternalServerError)
		assert.True(tt, ok)
		mockOrchestrator.AssertNotCalled(tt, "DeletePool", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces failure with reason instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "delete failed: pool has volumes"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		params := ociserver.DeletePoolParams{PoolOCID: "ocid1.pool.oc1.iad.del", TenancyOcid: "ocid1.tenancy.oc1..del"}

		res, err := h.DeletePool(contextWithOpcRequestID(context.Background(), idempotentOPC), params)
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.DeletePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), conflict.Response.Status)
		assert.Equal(tt, "delete failed: pool has volumes", conflict.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "DeletePool", mock.Anything, mock.Anything)
	})
}

func TestCreateSvm_Idempotency(t *testing.T) {
	t.Run("replays existing workflow status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusCompleted}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		req := &ociserver.CreateSvmRequest{SvmOCID: "ocid1.svm.oc1..a", SvmAdminPassword: validSvmAdminPassword()}
		params := ociserver.CreateSvmByPoolParams{PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.CreateSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusCompleted), acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "CreateSvm", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			CreateSvm(mock.Anything, mock.MatchedBy(func(p *commonparams.CreateSvmParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(idempotentOPC, nil)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		req := &ociserver.CreateSvmRequest{SvmOCID: "ocid1.svm.oc1..a", SvmAdminPassword: validSvmAdminPassword()}
		params := ociserver.CreateSvmByPoolParams{PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.CreateSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		req := &ociserver.CreateSvmRequest{SvmOCID: "ocid1.svm.oc1..a", SvmAdminPassword: validSvmAdminPassword()}
		params := ociserver.CreateSvmByPoolParams{PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.CreateSvmByPoolInternalServerError)
		assert.True(tt, ok)
		mockOrchestrator.AssertNotCalled(tt, "CreateSvm", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces failure with reason instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "svm create failed: lif conflict"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		req := &ociserver.CreateSvmRequest{SvmOCID: "ocid1.svm.oc1..a", SvmAdminPassword: validSvmAdminPassword()}
		params := ociserver.CreateSvmByPoolParams{PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.CreateSvmByPoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), conflict.Response.Status)
		assert.Equal(tt, "svm create failed: lif conflict", conflict.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "CreateSvm", mock.Anything, mock.Anything)
	})
}

func TestDeleteSvm_Idempotency(t *testing.T) {
	t.Run("replays existing workflow status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		params := ociserver.DeleteSvmParams{SvmOCID: "ocid1.svm.oc1..a", PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}

		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.DeleteSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "DeleteSvm", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			DeleteSvm(mock.Anything, mock.MatchedBy(func(p *commonparams.DeleteSvmParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(idempotentOPC, nil)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		params := ociserver.DeleteSvmParams{SvmOCID: "ocid1.svm.oc1..a", PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}

		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.DeleteSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		params := ociserver.DeleteSvmParams{SvmOCID: "ocid1.svm.oc1..a", PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}

		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.DeleteSvmInternalServerError)
		assert.True(tt, ok)
		mockOrchestrator.AssertNotCalled(tt, "DeleteSvm", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces failure with reason instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "svm delete failed: still mounted"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}
		params := ociserver.DeleteSvmParams{SvmOCID: "ocid1.svm.oc1..a", PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}

		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.DeleteSvmConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), conflict.Response.Status)
		assert.Equal(tt, "svm delete failed: still mounted", conflict.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "DeleteSvm", mock.Anything, mock.Anything)
	})
}
