package api

import (
	"context"
	"errors"
	"testing"
	"time"

	ogenmw "github.com/ogen-go/ogen/middleware"
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

// runIdempotencyMiddleware drives a request through the shared idempotency
// middleware the same way the ogen server does: the middleware performs the
// opc-work-request-id lookup and either short-circuits with a replay response
// or calls next (which invokes the handler). It returns the resolved response
// (middleware.Response.Type) so tests can assert on the concrete ogen type.
func runIdempotencyMiddleware(h *Handler, ctx context.Context, op ociserver.OperationName, body any, params ogenmw.Parameters, next func(context.Context) (any, error)) (any, error) {
	resp, err := h.IdempotencyMiddleware()(
		ogenmw.Request{Context: ctx, OperationName: op, Body: body, Params: params},
		func(r ogenmw.Request) (ogenmw.Response, error) {
			res, hErr := next(r.Context)
			return ogenmw.Response{Type: res}, hErr
		},
	)
	return resp.Type, err
}

// pathParams builds a middleware.Parameters map with the given path parameters
// (name/value pairs) so the middleware can extract resource OCIDs for its
// response, mirroring what the ogen server populates for path-based operations.
func pathParams(pairs ...string) ogenmw.Parameters {
	p := ogenmw.Parameters{}
	for i := 0; i+1 < len(pairs); i += 2 {
		p[ogenmw.ParameterKey{Name: pairs[i], In: "path"}] = pairs[i+1]
	}
	return p
}

func createPoolIdem(h *Handler, ctx context.Context, req *ociserver.CreatePoolRequest, params ociserver.CreatePoolParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.CreatePoolOperation, req, nil, func(c context.Context) (any, error) {
		return h.CreatePool(c, req, params)
	})
}

func updatePoolIdem(h *Handler, ctx context.Context, req *ociserver.UpdatePoolRequest, params ociserver.UpdatePoolParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.UpdatePoolOperation, req, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.UpdatePool(c, req, params)
	})
}

func deletePoolIdem(h *Handler, ctx context.Context, params ociserver.DeletePoolParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.DeletePoolOperation, nil, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.DeletePool(c, params)
	})
}

func createSvmIdem(h *Handler, ctx context.Context, req *ociserver.CreateSvmRequest, params ociserver.CreateSvmByPoolParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.CreateSvmByPoolOperation, req, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.CreateSvmByPool(c, req, params)
	})
}

func updateSvmIdem(h *Handler, ctx context.Context, req *ociserver.UpdateSvmRequest, params ociserver.UpdateSvmParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.UpdateSvmOperation, req, pathParams("poolOCID", params.PoolOCID, "svmOCID", params.SvmOCID), func(c context.Context) (any, error) {
		return h.UpdateSvm(c, req, params)
	})
}

func deleteSvmIdem(h *Handler, ctx context.Context, req ociserver.OptDeleteSvmReq, params ociserver.DeleteSvmParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.DeleteSvmOperation, nil, pathParams("poolOCID", params.PoolOCID, "svmOCID", params.SvmOCID), func(c context.Context) (any, error) {
		return h.DeleteSvm(c, req, params)
	})
}

func upgradePoolIdem(h *Handler, ctx context.Context, req *ociserver.UpgradePoolRequest, params ociserver.UpgradePoolParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.UpgradePoolOperation, req, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.UpgradePool(c, req, params)
	})
}

func rotateFabricPoolKeysIdem(h *Handler, ctx context.Context, req *ociserver.RotateFabricPoolKeysRequest, params ociserver.RotateFabricPoolKeysParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.RotateFabricPoolKeysOperation, req, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.RotateFabricPoolKeys(c, req, params)
	})
}

func rbacRefreshPoolIdem(h *Handler, ctx context.Context, req ociserver.OptRbacRefreshRequest, params ociserver.RbacRefreshPoolParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.RbacRefreshPoolOperation, req, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.RbacRefreshPool(c, req, params)
	})
}

func startMaintenanceIdem(h *Handler, ctx context.Context, req *ociserver.StartMaintenanceRequest, params ociserver.StartMaintenanceParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.StartMaintenanceOperation, req, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.StartMaintenance(c, req, params)
	})
}

func finishMaintenanceIdem(h *Handler, ctx context.Context, req *ociserver.FinishMaintenanceRequest, params ociserver.FinishMaintenanceParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.FinishMaintenanceOperation, req, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.FinishMaintenance(c, req, params)
	})
}

func executeMaintenanceIdem(h *Handler, ctx context.Context, req *ociserver.MaintenanceRequest, params ociserver.ExecuteMaintenanceParams) (any, error) {
	return runIdempotencyMiddleware(h, ctx, ociserver.ExecuteMaintenanceOperation, req, pathParams("poolOCID", params.PoolOCID), func(c context.Context) (any, error) {
		return h.ExecuteMaintenance(c, req, params)
	})
}

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

		res, err := createPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
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

		res, err := createPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
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

		res, err := createPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
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

		res, err := createPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
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

		res, err := createPoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), validCreatePoolRequest(), defaultCreatePoolParams())
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

		res, err := updatePoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
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

		res, err := updatePoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
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

		res, err := updatePoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
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

		res, err := updatePoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
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

		res, err := deletePoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), params)
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

		res, err := deletePoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), params)
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

		res, err := deletePoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), params)
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

		res, err := deletePoolIdem(&h, contextWithOpcRequestID(context.Background(), idempotentOPC), params)
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

		res, err := createSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
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

		res, err := createSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
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

		res, err := createSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
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

		res, err := createSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), req, params)
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

		res, err := deleteSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptDeleteSvmReq{}, params)
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

		res, err := deleteSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptDeleteSvmReq{}, params)
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

		res, err := deleteSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptDeleteSvmReq{}, params)
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

		res, err := deleteSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.DeleteSvmConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), conflict.Response.Status)
		assert.Equal(tt, "svm delete failed: still mounted", conflict.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "DeleteSvm", mock.Anything, mock.Anything)
	})
}

func TestUpdateSvm_Idempotency(t *testing.T) {
	updateParams := func() ociserver.UpdateSvmParams {
		return ociserver.UpdateSvmParams{SvmOCID: "ocid1.svm.oc1..a", PoolOCID: "ocid1.pool.oc1..a", TenancyOcid: "ocid1.tenancy.oc1..a"}
	}
	updateReq := func() *ociserver.UpdateSvmRequest {
		return &ociserver.UpdateSvmRequest{SvmAdminPassword: validSvmAdminPassword()}
	}

	t.Run("replays existing workflow status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := updateSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), updateReq(), updateParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.UpdateSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), acc.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "UpdateSvm", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			UpdateSvm(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdateSvmParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(idempotentOPC, nil)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := updateSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), updateReq(), updateParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.UpdateSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := updateSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), updateReq(), updateParams())
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.UpdateSvmInternalServerError)
		assert.True(tt, ok)
		mockOrchestrator.AssertNotCalled(tt, "UpdateSvm", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces failure with reason instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "svm update failed: invalid admin password"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := updateSvmIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), updateReq(), updateParams())
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.UpdateSvmConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), conflict.Response.Status)
		assert.Equal(tt, "svm update failed: invalid admin password", conflict.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "UpdateSvm", mock.Anything, mock.Anything)
	})
}

func TestRotateFabricPoolKeys_Idempotency(t *testing.T) {
	t.Run("replays existing in-progress workflow without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rotateFabricPoolKeysIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.RotateFabricPoolKeysAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.OpcWorkRequestID)
		assert.Equal(tt, testPoolOCID, acc.Response.PoolOCID)
		assert.Equal(tt, ociserver.RotateFabricPoolKeysAcceptedResponseStatusInProgress, acc.Response.Status)
		assert.True(tt, acc.Response.WorkflowId.IsSet())
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId.Value)
		mockOrchestrator.AssertNotCalled(tt, "RotateFabricPoolKeys", mock.Anything, mock.Anything)
	})

	t.Run("replays completed workflow with no_change status", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusCompleted}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rotateFabricPoolKeysIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.RotateFabricPoolKeysAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.OpcWorkRequestID)
		assert.Equal(tt, testPoolOCID, acc.Response.PoolOCID)
		assert.Equal(tt, ociserver.RotateFabricPoolKeysAcceptedResponseStatusNoChange, acc.Response.Status)
		assert.True(tt, acc.Response.WorkflowId.IsSet())
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId.Value)
		mockOrchestrator.AssertNotCalled(tt, "RotateFabricPoolKeys", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			RotateFabricPoolKeys(mock.Anything, mock.MatchedBy(func(p *commonparams.RotateFabricPoolKeysParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(idempotentOPC, nil)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rotateFabricPoolKeysIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.RotateFabricPoolKeysAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId.Value)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rotateFabricPoolKeysIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.RotateFabricPoolKeysInternalServerError)
		assert.True(tt, ok)
		mockOrchestrator.AssertNotCalled(tt, "RotateFabricPoolKeys", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces failure with reason instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "key rotation failed: secret not accessible"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := &Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := rotateFabricPoolKeysIdem(h, contextWithOpcRequestID(context.Background(), idempotentOPC), validRotateFabricPoolKeysRequest(), defaultRotateFabricPoolKeysParams())
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.RotateFabricPoolKeysConflict)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), conflict.Response.Status)
		assert.Equal(tt, "key rotation failed: secret not accessible", conflict.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "RotateFabricPoolKeys", mock.Anything, mock.Anything)
	})
}
