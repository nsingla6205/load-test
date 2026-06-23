package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	testSvmOCID     = "ocid1.svm.oc1..svm"
	testTenancyOCID = "ocid1.tenancy.oc1..tenancy"
)

// validSvmAdminPassword returns a fixed valid OCIOCIDVersionRef for tests that
// must pass svmAdminPassword validation to reach the orchestrator path.
func validSvmAdminPassword() ociserver.OCIOCIDVersionRef {
	return ociserver.OCIOCIDVersionRef{Ocid: "ocid1.vaultsecret.oc1..test", Version: "1"}
}

// validCreateParams returns CreateSvmByPoolParams with valid OCID-format test values.
func validCreateParams() ociserver.CreateSvmByPoolParams {
	return ociserver.CreateSvmByPoolParams{PoolOCID: testPoolOCID, TenancyOcid: testTenancyOCID}
}

func TestCreateSvmByPool(t *testing.T) {
	t.Run("returns 202 with workflowId and svmOCID and auto-generated name", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreateSvm(mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, p *commonparams.CreateSvmParams) (string, error) {
				assert.Equal(tt, "ocid1.pool.oc1..pool", p.PoolOCID)
				assert.Equal(tt, "ocid1.svm.oc1..svm", p.SvmExternalIdentifier)
				assert.True(tt, strings.HasPrefix(p.Name, "oci-svm-"), "expected auto-generated name with prefix %q, got %q", "oci-svm-", p.Name)
				assert.Equal(tt, []string{"10.0.0.10"}, p.Ips)
				assert.Equal(tt, "ocid1.tenancy.oc1..tenancy", p.AccountName)
				return "wf-123", nil
			},
		)
		h := &Handler{Orchestrator: mockOrchestrator}

		req := &ociserver.CreateSvmRequest{
			SvmOCID:          "ocid1.svm.oc1..svm",
			Ips:              []string{"10.0.0.10"},
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{Ocid: "ocid1.secret.oc1..pwd", Version: "1"},
		}
		pparams := ociserver.CreateSvmByPoolParams{PoolOCID: "ocid1.pool.oc1..pool", TenancyOcid: "ocid1.tenancy.oc1..tenancy"}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, pparams)
		assert.NoError(tt, err)

		headers, ok := res.(*ociserver.CreateSvmAcceptedResponseHeaders)
		assert.True(tt, ok, "response should be *ociserver.CreateSvmAcceptedResponseHeaders")
		assert.Equal(tt, defaultTestOPC, headers.OpcRequestID)
		createRes := headers.Response
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), createRes.Status)
		assert.Equal(tt, "wf-123", createRes.WorkflowId)
		assert.Equal(tt, "ocid1.svm.oc1..svm", createRes.SvmOCID)
	})

	t.Run("returns 400 when opc-request-id is missing", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.CreateSvmRequest{SvmOCID: "svm"}

		res, err := h.CreateSvmByPool(context.Background(), req, ociserver.CreateSvmByPoolParams{PoolOCID: "pool"})
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreateSvmByPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), badReq.Response.Status)
		assert.Equal(tt, invalidOPCRequestID, badReq.Response.ErrorMessage)
		assert.Equal(tt, "svm", badReq.Response.SvmOCID)
	})

	t.Run("returns 400 when svmAdminPassword.version is not a valid integer", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.CreateSvmRequest{
			SvmOCID: testSvmOCID,
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{
				Ocid:    "ocid1.vaultsecret.oc1..abc",
				Version: "not-a-number",
			},
		}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreateSvmByPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "svmAdminPassword.version must be a valid integer", badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when svmAdminPassword.version is less than 1", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.CreateSvmRequest{
			SvmOCID: testSvmOCID,
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{
				Ocid:    "ocid1.vaultsecret.oc1..abc",
				Version: "0",
			},
		}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreateSvmByPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "svmAdminPassword.version must be greater than or equal to 1", badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when poolOCID is empty", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.CreateSvmRequest{SvmOCID: "svm"}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, ociserver.CreateSvmByPoolParams{PoolOCID: "  "})
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreateSvmByPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "poolOCID path parameter is required", badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when svmOCID is empty", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.CreateSvmRequest{SvmOCID: "  "}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, ociserver.CreateSvmByPoolParams{PoolOCID: "pool"})
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreateSvmByPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "svmOCID is required", badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when Tenancy-Ocid is empty", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.CreateSvmRequest{SvmOCID: "svm"}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, ociserver.CreateSvmByPoolParams{PoolOCID: "pool", TenancyOcid: "  "})
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreateSvmByPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "Tenancy-Ocid is required", badReq.Response.ErrorMessage)
	})

	t.Run("returns 202 with valid svmAdminPassword", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreateSvm(mock.Anything, mock.MatchedBy(func(p *commonparams.CreateSvmParams) bool {
			return strings.HasPrefix(p.Name, "oci-svm-") &&
				p.SvmAdminPassword != nil &&
				p.SvmAdminPassword.Ocid == "ocid1.vaultsecret.oc1..abc" &&
				p.SvmAdminPassword.Version == 3
		})).Return("wf-pwd", nil)
		h := &Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.CreateSvmRequest{
			SvmOCID: testSvmOCID,
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{
				Ocid:    "ocid1.vaultsecret.oc1..abc",
				Version: "3",
			},
		}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.CreateSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, headers.OpcRequestID)
		assert.Equal(tt, "wf-pwd", headers.Response.WorkflowId)
	})

	t.Run("returns 400 when orchestrator returns user input validation error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreateSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewUserInputValidationErr("invalid root volume style"))
		h := &Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.CreateSvmRequest{SvmOCID: testSvmOCID, SvmAdminPassword: validSvmAdminPassword()}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreateSvmByPoolBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "invalid root volume style")
	})

	t.Run("returns 400 when orchestrator returns bad request error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreateSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewBadRequestErr("too many IPs"))
		h := &Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.CreateSvmRequest{SvmOCID: testSvmOCID, SvmAdminPassword: validSvmAdminPassword()}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.CreateSvmByPoolBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "too many IPs")
	})

	t.Run("returns 409 with unwrapped conflict message", func(tt *testing.T) {
		inner := utilserrors.NewConflictErr("inner detail")
		wrapped := fmt.Errorf("outer: %w", inner)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreateSvm(mock.Anything, mock.Anything).Return("", wrapped)
		h := &Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.CreateSvmRequest{SvmOCID: testSvmOCID, SvmAdminPassword: validSvmAdminPassword()}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.CreateSvmByPoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, inner.Error(), conflict.Response.ErrorMessage)
	})

	t.Run("returns 404 when orchestrator returns not found", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreateSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewNotFoundErr("pool", nil))
		h := &Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.CreateSvmRequest{SvmOCID: testSvmOCID, SvmAdminPassword: validSvmAdminPassword()}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		notFound, ok := res.(*ociserver.CreateSvmByPoolNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), notFound.Response.Status)
	})

	t.Run("returns 409 when orchestrator returns conflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreateSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewConflictErr("svmOCID already exists"))
		h := &Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.CreateSvmRequest{SvmOCID: testSvmOCID, SvmAdminPassword: validSvmAdminPassword()}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.CreateSvmByPoolConflict)
		assert.True(tt, ok)
		assert.Contains(tt, conflict.Response.ErrorMessage, "svmOCID already exists")
	})

	t.Run("returns 500 for generic orchestrator error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().CreateSvm(mock.Anything, mock.Anything).Return("", errors.New("boom"))
		h := &Handler{Orchestrator: mockOrchestrator}
		req := &ociserver.CreateSvmRequest{SvmOCID: testSvmOCID, SvmAdminPassword: validSvmAdminPassword()}

		res, err := h.CreateSvmByPool(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validCreateParams())
		assert.NoError(tt, err)
		serverErr, ok := res.(*ociserver.CreateSvmByPoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, "Internal server error", serverErr.Response.ErrorMessage)
	})
}

func TestDeleteSvm(t *testing.T) {
	t.Run("returns 202 on success", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeleteSvm(mock.Anything, mock.Anything).Return("wf-del-1", nil)
		h := &Handler{Orchestrator: mockOrchestrator}

		params := ociserver.DeleteSvmParams{SvmOCID: "ocid1.svm", PoolOCID: "ocid1.pool", TenancyOcid: "tenancy1"}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.DeleteSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, headers.OpcRequestID)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), headers.Response.Status)
		assert.Equal(tt, "wf-del-1", headers.Response.WorkflowId)
		assert.Equal(tt, "ocid1.svm", headers.Response.SvmOCID)
	})

	t.Run("returns 400 when opc-request-id missing", func(tt *testing.T) {
		h := &Handler{}
		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "pool", TenancyOcid: "t"}
		res, err := h.DeleteSvm(context.Background(), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.DeleteSvmBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, invalidOPCRequestID, badReq.Response.ErrorMessage)
		assert.Equal(tt, "svm", badReq.Response.SvmOCID)
	})

	t.Run("returns 400 when svmOCID empty", func(tt *testing.T) {
		h := &Handler{}
		params := ociserver.DeleteSvmParams{SvmOCID: "", PoolOCID: "pool", TenancyOcid: "t"}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.DeleteSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "svmOCID")
	})

	t.Run("returns 400 when poolOCID empty", func(tt *testing.T) {
		h := &Handler{}
		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "", TenancyOcid: "t"}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.DeleteSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "poolOCID")
	})

	t.Run("returns 400 when tenancy empty", func(tt *testing.T) {
		h := &Handler{}
		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "pool", TenancyOcid: ""}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.DeleteSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "Tenancy-Ocid")
	})

	t.Run("passes force flag from request body", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeleteSvm(mock.Anything, mock.MatchedBy(func(p *commonparams.DeleteSvmParams) bool {
			return p.Force == true
		})).Return("wf-force", nil)
		h := &Handler{Orchestrator: mockOrchestrator}

		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "pool", TenancyOcid: "t"}
		req := ociserver.NewOptDeleteSvmReq(ociserver.DeleteSvmReq{Force: ociserver.NewOptBool(true)})
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, params)
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.DeleteSvmAcceptedResponseHeaders)
		assert.True(tt, ok)
		assert.Equal(tt, defaultTestOPC, headers.OpcRequestID)
		assert.Equal(tt, "wf-force", headers.Response.WorkflowId)
	})

	t.Run("returns 400 on bad request", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeleteSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewBadRequestErr("cannot delete"))
		h := &Handler{Orchestrator: mockOrchestrator}

		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "pool", TenancyOcid: "t"}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.DeleteSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "cannot delete")
	})

	t.Run("returns 409 with unwrapped conflict message", func(tt *testing.T) {
		inner := utilserrors.NewConflictErr("inner reason")
		wrapped := fmt.Errorf("wrap: %w", inner)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeleteSvm(mock.Anything, mock.Anything).Return("", wrapped)
		h := &Handler{Orchestrator: mockOrchestrator}

		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "pool", TenancyOcid: "t"}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.DeleteSvmConflict)
		assert.True(tt, ok)
		assert.Equal(tt, inner.Error(), conflict.Response.ErrorMessage)
	})

	t.Run("returns 404 on not found", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeleteSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewNotFoundErr("svm", nil))
		h := &Handler{Orchestrator: mockOrchestrator}

		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "pool", TenancyOcid: "t"}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.DeleteSvmNotFound)
		assert.True(tt, ok)
	})

	t.Run("returns 409 on conflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeleteSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewConflictErr("already deleting"))
		h := &Handler{Orchestrator: mockOrchestrator}

		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "pool", TenancyOcid: "t"}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		_, ok := res.(*ociserver.DeleteSvmConflict)
		assert.True(tt, ok)
	})

	t.Run("returns 500 on generic error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DeleteSvm(mock.Anything, mock.Anything).Return("", errors.New("boom"))
		h := &Handler{Orchestrator: mockOrchestrator}

		params := ociserver.DeleteSvmParams{SvmOCID: "svm", PoolOCID: "pool", TenancyOcid: "t"}
		res, err := h.DeleteSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), ociserver.OptDeleteSvmReq{}, params)
		assert.NoError(tt, err)
		serverErr, ok := res.(*ociserver.DeleteSvmInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, "Internal server error", serverErr.Response.ErrorMessage)
	})
}

func TestUpdateSvm(t *testing.T) {
	validUpdateReq := func() *ociserver.UpdateSvmRequest {
		return &ociserver.UpdateSvmRequest{
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{Ocid: "ocid1.vaultsecret.oc1..abc", Version: "1"},
		}
	}
	validUpdateParams := func() ociserver.UpdateSvmParams {
		return ociserver.UpdateSvmParams{PoolOCID: "ocid1.pool.oc1..pool", SvmOCID: "ocid1.svm.oc1..svm", TenancyOcid: "ocid1.tenancy.oc1..tenancy"}
	}

	t.Run("returns 202 with workflowId and svmOCID", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdateSvm(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdateSvmParams) bool {
			return p.PoolOCID == "ocid1.pool.oc1..pool" &&
				p.SvmExternalIdentifier == "ocid1.svm.oc1..svm" &&
				p.AccountName == "ocid1.tenancy.oc1..tenancy" &&
				p.SvmAdminPassword != nil &&
				p.SvmAdminPassword.Ocid == "ocid1.vaultsecret.oc1..abc" &&
				p.SvmAdminPassword.Version == 1
		})).Return("wf-update-1", nil)
		h := &Handler{Orchestrator: mockOrchestrator}

		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), validUpdateParams())
		assert.NoError(tt, err)
		headers, ok := res.(*ociserver.UpdateSvmAcceptedResponseHeaders)
		assert.True(tt, ok, "response should be *ociserver.UpdateSvmAcceptedResponseHeaders")
		assert.Equal(tt, defaultTestOPC, headers.OpcRequestID)
		assert.Equal(tt, string(workflowquery.WorkflowStatusInProgress), headers.Response.Status)
		assert.Equal(tt, "wf-update-1", headers.Response.WorkflowId)
		assert.Equal(tt, "ocid1.svm.oc1..svm", headers.Response.SvmOCID)
	})

	t.Run("returns 400 when opc-request-id is missing", func(tt *testing.T) {
		h := &Handler{}
		res, err := h.UpdateSvm(context.Background(), validUpdateReq(), validUpdateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), badReq.Response.Status)
		assert.Equal(tt, invalidOPCRequestID, badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when poolOCID is empty", func(tt *testing.T) {
		h := &Handler{}
		params := validUpdateParams()
		params.PoolOCID = "  "
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "poolOCID path parameter")
	})

	t.Run("returns 400 when svmOCID is empty", func(tt *testing.T) {
		h := &Handler{}
		params := validUpdateParams()
		params.SvmOCID = "  "
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "svmOCID path parameter")
	})

	t.Run("returns 400 when Tenancy-Ocid is empty", func(tt *testing.T) {
		h := &Handler{}
		params := validUpdateParams()
		params.TenancyOcid = "  "
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "Tenancy-Ocid")
	})

	t.Run("returns 400 when poolOCID is not a valid OCID format", func(tt *testing.T) {
		h := &Handler{}
		params := validUpdateParams()
		params.PoolOCID = "not-an-ocid"
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "poolOCID")
		assert.Contains(tt, badReq.Response.ErrorMessage, "not a valid OCID")
	})

	t.Run("returns 400 when svmOCID is not a valid OCID format", func(tt *testing.T) {
		h := &Handler{}
		params := validUpdateParams()
		params.SvmOCID = "invalid-format"
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "svmOCID")
		assert.Contains(tt, badReq.Response.ErrorMessage, "not a valid OCID")
	})

	t.Run("returns 400 when Tenancy-Ocid is not a valid OCID format", func(tt *testing.T) {
		h := &Handler{}
		params := validUpdateParams()
		params.TenancyOcid = "just-a-string"
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), params)
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "Tenancy-Ocid")
		assert.Contains(tt, badReq.Response.ErrorMessage, "not a valid OCID")
	})

	t.Run("returns 400 when svmAdminPassword.ocid is empty", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.UpdateSvmRequest{
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{Ocid: "  ", Version: "1"},
		}
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validUpdateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "svmAdminPassword.ocid is required", badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when svmAdminPassword.ocid is not a valid OCID", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.UpdateSvmRequest{
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{Ocid: "not-an-ocid", Version: "1"},
		}
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validUpdateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "svmAdminPassword.ocid is not a valid OCID", badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when svmAdminPassword.version is not a valid integer", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.UpdateSvmRequest{
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{Ocid: "ocid1.vaultsecret.oc1..abc", Version: "not-a-number"},
		}
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validUpdateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "svmAdminPassword.version must be a valid integer", badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when svmAdminPassword.version is less than 1", func(tt *testing.T) {
		h := &Handler{}
		req := &ociserver.UpdateSvmRequest{
			SvmAdminPassword: ociserver.OCIOCIDVersionRef{Ocid: "ocid1.vaultsecret.oc1..abc", Version: "0"},
		}
		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), req, validUpdateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "svmAdminPassword.version must be greater than or equal to 1", badReq.Response.ErrorMessage)
	})

	t.Run("returns 400 when orchestrator returns user input validation error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdateSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewUserInputValidationErr("bad input"))
		h := &Handler{Orchestrator: mockOrchestrator}

		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), validUpdateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "bad input")
	})

	t.Run("returns 400 when orchestrator returns bad request error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdateSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewBadRequestErr("invalid param"))
		h := &Handler{Orchestrator: mockOrchestrator}

		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), validUpdateParams())
		assert.NoError(tt, err)
		badReq, ok := res.(*ociserver.UpdateSvmBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Response.ErrorMessage, "invalid param")
	})

	t.Run("returns 404 when orchestrator returns not found", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdateSvm(mock.Anything, mock.Anything).Return("", utilserrors.NewNotFoundErr("svm", nil))
		h := &Handler{Orchestrator: mockOrchestrator}

		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), validUpdateParams())
		assert.NoError(tt, err)
		notFound, ok := res.(*ociserver.UpdateSvmNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, string(workflowquery.WorkflowStatusFailed), notFound.Response.Status)
	})

	t.Run("returns 409 with unwrapped conflict message", func(tt *testing.T) {
		inner := utilserrors.NewConflictErr("inner detail")
		wrapped := fmt.Errorf("outer: %w", inner)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdateSvm(mock.Anything, mock.Anything).Return("", wrapped)
		h := &Handler{Orchestrator: mockOrchestrator}

		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), validUpdateParams())
		assert.NoError(tt, err)
		conflict, ok := res.(*ociserver.UpdateSvmConflict)
		assert.True(tt, ok)
		assert.Equal(tt, inner.Error(), conflict.Response.ErrorMessage)
	})

	t.Run("returns 500 for generic orchestrator error", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().UpdateSvm(mock.Anything, mock.Anything).Return("", errors.New("boom"))
		h := &Handler{Orchestrator: mockOrchestrator}

		res, err := h.UpdateSvm(contextWithOpcRequestID(context.Background(), defaultTestOPC), validUpdateReq(), validUpdateParams())
		assert.NoError(tt, err)
		serverErr, ok := res.(*ociserver.UpdateSvmInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, "Internal server error", serverErr.Response.ErrorMessage)
	})
}
