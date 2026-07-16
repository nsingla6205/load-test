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

const testPoolOCID = "ocid1.pool.oc1..test"

func defaultUpgradePoolParams() ociserver.UpgradePoolParams {
	return ociserver.UpgradePoolParams{
		PoolOCID:    testPoolOCID,
		TenancyOcid: defaultTestTenancyOCID,
	}
}

func validUpgradePoolRequest() *ociserver.UpgradePoolRequest {
	return &ociserver.UpgradePoolRequest{
		TargetOntapVersion: "9.15.1",
		VsaImagePath:       "/n/namespace/b/bucket/o/image.tgz",
	}
}

func TestUpgradePool_MissingOPCRequestID(t *testing.T) {
	h := Handler{}
	res, err := h.UpgradePool(context.Background(), validUpgradePoolRequest(), defaultUpgradePoolParams())
	assert.NoError(t, err)
	bad, ok := res.(*ociserver.UpgradePoolBadRequest)
	assert.True(t, ok)
	assert.Equal(t, invalidOPCRequestID, bad.Response.ErrorMessage)
	assert.Equal(t, testPoolOCID, bad.Response.PoolOCID)
	assert.Equal(t, string(workflowquery.WorkflowStatusFailed), bad.Response.Status)
}

func TestUpgradePool_EmptyTargetOntapVersion(t *testing.T) {
	req := validUpgradePoolRequest()
	req.TargetOntapVersion = ""
	h := Handler{}
	res, err := h.UpgradePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultUpgradePoolParams())
	assert.NoError(t, err)
	bad, ok := res.(*ociserver.UpgradePoolBadRequest)
	assert.True(t, ok)
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t, "targetOntapVersion is required", bad.Response.ErrorMessage)
}

func TestUpgradePool_EmptyVsaImagePath(t *testing.T) {
	req := validUpgradePoolRequest()
	req.VsaImagePath = ""
	h := Handler{}
	res, err := h.UpgradePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultUpgradePoolParams())
	assert.NoError(t, err)
	bad, ok := res.(*ociserver.UpgradePoolBadRequest)
	assert.True(t, ok)
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t, "vsaImagePath is required", bad.Response.ErrorMessage)
}

func TestUpgradePool_InvalidMediatorImageOcid(t *testing.T) {
	req := validUpgradePoolRequest()
	req.MediatorImageOcid = ociserver.OptString{Value: "not-a-valid-ocid", Set: true}
	h := Handler{}
	res, err := h.UpgradePool(contextWithOpcRequestID(nil, defaultTestOPC), req, defaultUpgradePoolParams())
	assert.NoError(t, err)
	bad, ok := res.(*ociserver.UpgradePoolBadRequest)
	assert.True(t, ok)
	assert.Equal(t, defaultTestOPC, bad.OpcWorkRequestID)
	assert.Equal(t, "mediatorImageOcid is not a valid OCID", bad.Response.ErrorMessage)
}

func TestUpgradePool_OrchestratorErrors(t *testing.T) {
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
			err:          utilserrors.NewBadRequestErr("invalid upgrade parameters"),
			expectType:   "BadRequest",
			expectErrMsg: "invalid upgrade parameters",
		},
		{
			name:         "Conflict returns 409",
			err:          utilserrors.NewConflictErr("upgrade already in progress"),
			expectType:   "Conflict",
			expectErrMsg: "upgrade already in progress",
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
			mockOrchestrator.EXPECT().UpgradeCluster(mock.Anything, mock.Anything).Return(nil, "", tt.err)
			h := Handler{Orchestrator: mockOrchestrator}
			ctx := contextWithOpcRequestID(nil, defaultTestOPC)

			res, err := h.UpgradePool(ctx, validUpgradePoolRequest(), defaultUpgradePoolParams())
			assert.NoError(t, err)

			switch tt.expectType {
			case "NotFound":
				resp, ok := res.(*ociserver.UpgradePoolNotFound)
				assert.True(t, ok)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Contains(t, resp.Response.ErrorMessage, tt.expectErrMsg)
			case "BadRequest":
				resp, ok := res.(*ociserver.UpgradePoolBadRequest)
				assert.True(t, ok)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Equal(t, tt.expectErrMsg, resp.Response.ErrorMessage)
			case "Conflict":
				resp, ok := res.(*ociserver.UpgradePoolConflict)
				assert.True(t, ok)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Contains(t, resp.Response.ErrorMessage, tt.expectErrMsg)
			case "InternalServerError":
				resp, ok := res.(*ociserver.UpgradePoolInternalServerError)
				assert.True(t, ok)
				assert.Equal(t, defaultTestOPC, resp.OpcWorkRequestID)
				assert.Equal(t, testPoolOCID, resp.Response.PoolOCID)
				assert.Equal(t, tt.expectErrMsg, resp.Response.ErrorMessage)
			}

			assert.Equal(t, string(workflowquery.WorkflowStatusFailed), getUpgradePoolErrorStatus(res, tt.expectType))
		})
	}
}

func getUpgradePoolErrorStatus(res ociserver.UpgradePoolRes, typ string) string {
	switch typ {
	case "NotFound":
		return res.(*ociserver.UpgradePoolNotFound).Response.Status
	case "BadRequest":
		return res.(*ociserver.UpgradePoolBadRequest).Response.Status
	case "Conflict":
		return res.(*ociserver.UpgradePoolConflict).Response.Status
	case "InternalServerError":
		return res.(*ociserver.UpgradePoolInternalServerError).Response.Status
	}
	return ""
}

func TestUpgradePool_Success(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().UpgradeCluster(mock.Anything, mock.Anything).Return(nil, "wf-upgrade-123", nil)
	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(nil, defaultTestOPC)

	res, err := h.UpgradePool(ctx, validUpgradePoolRequest(), defaultUpgradePoolParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.UpgradePoolAcceptedResponseHeaders)
	assert.True(t, ok, "response should be *ociserver.UpgradePoolAcceptedResponseHeaders")
	assert.Equal(t, defaultTestOPC, accepted.OpcWorkRequestID)
	assert.Equal(t, string(workflowquery.WorkflowStatusInProgress), accepted.Response.Status)
	assert.Equal(t, "wf-upgrade-123", accepted.Response.WorkflowId)
	assert.Equal(t, testPoolOCID, accepted.Response.PoolOCID)
}

func TestUpgradePool_ForwardsOptionalFields(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	mockOrchestrator.EXPECT().
		UpgradeCluster(mock.Anything, mock.MatchedBy(func(p *commonparams.UpgradeClusterParams) bool {
			return p.ForceUpgrade == true &&
				p.SkipUpdateRBAC == true &&
				p.PoolOCID == testPoolOCID &&
				p.AccountName == defaultTestTenancyOCID &&
				p.TargetOntapVersion == "9.15.1" &&
				p.VSAImagePath == "/n/namespace/b/bucket/o/image.tgz" &&
				p.MediatorBuildImage == "ocid1.image.oc1.iad.test-mediator" &&
				p.WorkflowID == defaultTestOPC
		})).
		Return(nil, "wf-opts-123", nil)

	h := Handler{Orchestrator: mockOrchestrator}
	ctx := contextWithOpcRequestID(nil, defaultTestOPC)
	req := validUpgradePoolRequest()
	req.ForceUpgrade = ociserver.OptBool{Value: true, Set: true}
	req.SkipUpdateRBAC = ociserver.OptBool{Value: true, Set: true}
	req.MediatorImageOcid = ociserver.OptString{Value: "ocid1.image.oc1.iad.test-mediator", Set: true}

	res, err := h.UpgradePool(ctx, req, defaultUpgradePoolParams())
	assert.NoError(t, err)

	accepted, ok := res.(*ociserver.UpgradePoolAcceptedResponseHeaders)
	assert.True(t, ok)
	assert.Equal(t, "wf-opts-123", accepted.Response.WorkflowId)
}

func TestUpgradePool_Idempotency(t *testing.T) {
	t.Run("replays existing workflow status without invoking orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusInProgress}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := upgradePoolIdem(&h, contextWithOpcRequestID(nil, idempotentOPC), validUpgradePoolRequest(), defaultUpgradePoolParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.UpgradePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.OpcWorkRequestID)
		assert.Equal(tt, idempotentOPC, acc.Response.WorkflowId)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), acc.Response.Status)
		assert.Equal(tt, testPoolOCID, acc.Response.PoolOCID)
		mockOrchestrator.AssertNotCalled(tt, "UpgradeCluster", mock.Anything, mock.Anything)
	})

	t.Run("fresh request passes opc-request-id as workflow id to orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, serviceerror.NewNotFound("not found")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().
			UpgradeCluster(mock.Anything, mock.MatchedBy(func(p *commonparams.UpgradeClusterParams) bool {
				return p != nil && p.WorkflowID == idempotentOPC
			})).
			Return(nil, idempotentOPC, nil)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := upgradePoolIdem(&h, contextWithOpcRequestID(nil, idempotentOPC), validUpgradePoolRequest(), defaultUpgradePoolParams())
		assert.NoError(tt, err)
		acc, ok := res.(*ociserver.UpgradePoolAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, acc.OpcWorkRequestID)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), acc.Response.Status)
	})

	t.Run("fails closed with 500 when lookup errors and does not invoke orchestrator", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{}, errors.New("temporal unavailable")
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := upgradePoolIdem(&h, contextWithOpcRequestID(nil, idempotentOPC), validUpgradePoolRequest(), defaultUpgradePoolParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.UpgradePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, resp.OpcWorkRequestID)
		assert.Equal(tt, "Internal server error", resp.Response.ErrorMessage)
		mockOrchestrator.AssertNotCalled(tt, "UpgradeCluster", mock.Anything, mock.Anything)
	})

	t.Run("terminally-failed prior workflow surfaces failure with reason instead of 202", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{
				Status: workflowquery.WorkflowStatusFailed,
				Error:  &workflowquery.WorkflowError{Message: "ONTAP upgrade failed: timeout"},
			}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := upgradePoolIdem(&h, contextWithOpcRequestID(nil, idempotentOPC), validUpgradePoolRequest(), defaultUpgradePoolParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.UpgradePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, resp.OpcWorkRequestID)
		assert.Equal(tt, "ONTAP upgrade failed: timeout", resp.Response.ErrorMessage)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "UpgradeCluster", mock.Anything, mock.Anything)
	})

	t.Run("timed-out prior workflow surfaces failure with fallback reason", func(tt *testing.T) {
		stubWorkflowQuery(tt, func(context.Context, client.Client, string, string) (workflowquery.Result, error) {
			return workflowquery.Result{Status: workflowquery.WorkflowStatusTimedOut}, nil
		})
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		h := Handler{Orchestrator: mockOrchestrator, TemporalClient: &mocks.Client{}}

		res, err := upgradePoolIdem(&h, contextWithOpcRequestID(nil, idempotentOPC), validUpgradePoolRequest(), defaultUpgradePoolParams())
		assert.NoError(tt, err)
		resp, ok := res.(*ociserver.UpgradePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, idempotentOPC, resp.OpcWorkRequestID)
		assert.Contains(tt, resp.Response.ErrorMessage, "previous request with this opc-work-request-id failed")
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), resp.Response.Status)
		mockOrchestrator.AssertNotCalled(tt, "UpgradeCluster", mock.Anything, mock.Anything)
	})
}
