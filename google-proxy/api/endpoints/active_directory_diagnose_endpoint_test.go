package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/pools"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// withADDiagnosticEnabled flips the package-level feature flag for the duration
// of a test.
func withADDiagnosticEnabled(enabled bool) func() {
	orig := adDiagnosticEnabled
	adDiagnosticEnabled = enabled
	return func() { adDiagnosticEnabled = orig }
}

func diagnoseParams() gcpgenserver.V1betaDirectoryServiceDiagnosisParams {
	return gcpgenserver.V1betaDirectoryServiceDiagnosisParams{
		PoolId:         "pool-uuid",
		ProjectNumber:  "pn",
		LocationId:     "loc",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
}

func validDiagnoseReq() *gcpgenserver.PoolConnectivityCheckV1beta {
	return &gcpgenserver.PoolConnectivityCheckV1beta{
		Action:       gcpgenserver.PoolConnectivityCheckV1betaActionConnectivityCheck,
		ResourceType: gcpgenserver.PoolConnectivityCheckV1betaResourceTypeAD,
	}
}

func TestV1betaDirectoryServiceDiagnosis_FlagDisabled(t *testing.T) {
	defer withADDiagnosticEnabled(false)()

	handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	res, err := handler.V1betaDirectoryServiceDiagnosis(context.Background(), validDiagnoseReq(), diagnoseParams())
	require.NoError(t, err)
	br, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest)
	require.True(t, ok)
	assert.Equal(t, adDiagnosisDisabledMessage, br.Message)
}

func TestV1betaDirectoryServiceDiagnosis_NilRequest(t *testing.T) {
	defer withADDiagnosticEnabled(true)()

	handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	res, err := handler.V1betaDirectoryServiceDiagnosis(context.Background(), nil, diagnoseParams())
	require.NoError(t, err)
	br, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest)
	require.True(t, ok)
	assert.Equal(t, "request body is required", br.Message)
}

func TestV1betaDirectoryServiceDiagnosis_WrongAction(t *testing.T) {
	defer withADDiagnosticEnabled(true)()

	handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	req := validDiagnoseReq()
	req.Action = "somethingElse"
	res, err := handler.V1betaDirectoryServiceDiagnosis(context.Background(), req, diagnoseParams())
	require.NoError(t, err)
	_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest)
	require.True(t, ok)
}

func TestV1betaDirectoryServiceDiagnosis_LDAPNotSupported(t *testing.T) {
	defer withADDiagnosticEnabled(true)()

	handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	req := validDiagnoseReq()
	req.ResourceType = gcpgenserver.PoolConnectivityCheckV1betaResourceTypeLDAP
	res, err := handler.V1betaDirectoryServiceDiagnosis(context.Background(), req, diagnoseParams())
	require.NoError(t, err)
	br, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest)
	require.True(t, ok)
	assert.Equal(t, adDiagnosisLDAPNotSupported, br.Message)
}

func TestV1betaDirectoryServiceDiagnosis_PoolNotFoundNoCVP(t *testing.T) {
	defer withADDiagnosticEnabled(true)()
	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = origHost }()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("DescribePool", mock.Anything, "pool-uuid", "pn").
		Return((*coremodels.Pool)(nil), customerrors.NewNotFoundErr("Pool", nil))
	handler := Handler{Orchestrator: mockOrch}

	res, err := handler.V1betaDirectoryServiceDiagnosis(context.Background(), validDiagnoseReq(), diagnoseParams())
	require.NoError(t, err)
	nf, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisNotFound)
	require.True(t, ok)
	assert.Contains(t, nf.Message, "pool-uuid")
}

func TestV1betaDirectoryServiceDiagnosis_DescribePoolGenericError(t *testing.T) {
	defer withADDiagnosticEnabled(true)()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("DescribePool", mock.Anything, "pool-uuid", "pn").
		Return((*coremodels.Pool)(nil), errors.New("db down"))
	handler := Handler{Orchestrator: mockOrch}

	res, err := handler.V1betaDirectoryServiceDiagnosis(context.Background(), validDiagnoseReq(), diagnoseParams())
	require.NoError(t, err)
	_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError)
	require.True(t, ok)
}

func TestV1betaDirectoryServiceDiagnosis_Success(t *testing.T) {
	defer withADDiagnosticEnabled(true)()
	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = origHost }()

	ad := &coremodels.ActiveDirectory{
		BaseModel: coremodels.BaseModel{UUID: "ad-uuid", UpdatedAt: time.Now().UTC()},
		State:     "READY",
	}
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("DescribePool", mock.Anything, "pool-uuid", "pn").Return(&coremodels.Pool{}, nil)
	mockOrch.On("DiagnoseActiveDirectory", mock.Anything, mock.Anything).Return(ad, "job-uuid", nil)
	handler := Handler{Orchestrator: mockOrch}

	res, err := handler.V1betaDirectoryServiceDiagnosis(context.Background(), validDiagnoseReq(), diagnoseParams())
	require.NoError(t, err)
	op, ok := res.(*gcpgenserver.OperationV1beta)
	require.True(t, ok)
	assert.Contains(t, op.Name.Value, "job-uuid")
	assert.False(t, op.Done.Value)
	assert.NotNil(t, op.Response)
}

func TestV1betaDirectoryServiceDiagnosis_OrchestratorErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		assertRes func(t *testing.T, res gcpgenserver.V1betaDirectoryServiceDiagnosisRes)
	}{
		{
			name: "bad request",
			err:  customerrors.NewBadRequestErr("invalid"),
			assertRes: func(t *testing.T, res gcpgenserver.V1betaDirectoryServiceDiagnosisRes) {
				_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest)
				assert.True(t, ok)
			},
		},
		{
			name: "not found",
			err:  customerrors.NewNotFoundErr("AD", nil),
			assertRes: func(t *testing.T, res gcpgenserver.V1betaDirectoryServiceDiagnosisRes) {
				_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisNotFound)
				assert.True(t, ok)
			},
		},
		{
			name: "internal",
			err:  errors.New("boom"),
			assertRes: func(t *testing.T, res gcpgenserver.V1betaDirectoryServiceDiagnosisRes) {
				_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError)
				assert.True(t, ok)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer withADDiagnosticEnabled(true)()
			origHost := cvp.CVP_HOST
			cvp.CVP_HOST = ""
			defer func() { cvp.CVP_HOST = origHost }()

			mockOrch := factory.NewMockOrchestratorFactory(t)
			mockOrch.On("DescribePool", mock.Anything, "pool-uuid", "pn").Return(&coremodels.Pool{}, nil)
			mockOrch.On("DiagnoseActiveDirectory", mock.Anything, mock.Anything).
				Return((*coremodels.ActiveDirectory)(nil), "", tc.err)
			handler := Handler{Orchestrator: mockOrch}

			res, err := handler.V1betaDirectoryServiceDiagnosis(context.Background(), validDiagnoseReq(), diagnoseParams())
			require.NoError(t, err)
			tc.assertRes(t, res)
		})
	}
}

func TestBuildDirectoryServiceDiagnosisV1(t *testing.T) {
	t.Run("uses UpdatedAt and lowercases state", func(t *testing.T) {
		updated := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		ad := &coremodels.ActiveDirectory{
			BaseModel: coremodels.BaseModel{UUID: "ad-uuid", UpdatedAt: updated},
			State:     "READY",
		}
		got := buildDirectoryServiceDiagnosisV1("pool-uuid", ad, "job-uuid")
		assert.Equal(t, "pool-uuid", got.PoolID)
		assert.Equal(t, "ad-uuid", got.ActiveDirectoryID)
		assert.Equal(t, "ready", got.LifeCycleState)
		require.Len(t, got.Jobs, 1)
		assert.Equal(t, "job-uuid", got.Jobs[0].JobID)
		assert.Equal(t, diagnoseDirectoryServicesAction, got.Jobs[0].Action)
		assert.Equal(t, diagnoseJobObjectType, got.Jobs[0].ObjectType)
		assert.Equal(t, diagnoseJobStateOngoing, got.Jobs[0].State)
		assert.Equal(t, "ad-uuid", got.Jobs[0].ObjectID)
	})

	t.Run("falls back to CreatedAt then now when timestamps zero", func(t *testing.T) {
		ad := &coremodels.ActiveDirectory{BaseModel: coremodels.BaseModel{UUID: "ad-uuid"}, State: "creating"}
		got := buildDirectoryServiceDiagnosisV1("pool-uuid", ad, "job-uuid")
		require.Len(t, got.Jobs, 1)
		assert.NotEmpty(t, got.Jobs[0].Created)
	})
}

func TestFormatDiagnosisJobCreated(t *testing.T) {
	tm := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	assert.Equal(t, "2026-03-04T05:06:07Z", formatDiagnosisJobCreated(tm))
}

func TestEncodeDirectoryServiceDiagnosisV1(t *testing.T) {
	raw, err := encodeDirectoryServiceDiagnosisV1(directoryServiceDiagnosisV1{
		PoolID:         "pool-uuid",
		LifeCycleState: "ready",
	})
	require.NoError(t, err)
	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.Equal(t, "pool-uuid", decoded["poolId"])
}

func TestMapDiagnoseErrorToResponse(t *testing.T) {
	t.Run("bad request", func(t *testing.T) {
		res := mapDiagnoseErrorToResponse(customerrors.NewBadRequestErr("invalid"))
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest)
		assert.True(t, ok)
	})
	t.Run("user input validation", func(t *testing.T) {
		res := mapDiagnoseErrorToResponse(customerrors.NewUserInputValidationErr("invalid"))
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest)
		assert.True(t, ok)
	})
	t.Run("not found", func(t *testing.T) {
		res := mapDiagnoseErrorToResponse(customerrors.NewNotFoundErr("AD", nil))
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisNotFound)
		assert.True(t, ok)
	})
	t.Run("internal", func(t *testing.T) {
		res := mapDiagnoseErrorToResponse(errors.New("boom"))
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError)
		assert.True(t, ok)
	})
}

func TestMapCVPDiagnoseErrorToResponse(t *testing.T) {
	cvpErr := func(code float64, msg string) *cvpModels.Error {
		return &cvpModels.Error{Code: code, Message: msg}
	}

	t.Run("bad request", func(t *testing.T) {
		res := mapCVPDiagnoseErrorToResponse(&pools.V1betaDirectoryServiceDiagnosisBadRequest{Payload: cvpErr(400, "bad")})
		br, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest)
		require.True(t, ok)
		assert.Equal(t, "bad", br.Message)
		assert.Equal(t, float64(400), br.Code)
	})
	t.Run("not found", func(t *testing.T) {
		res := mapCVPDiagnoseErrorToResponse(&pools.V1betaDirectoryServiceDiagnosisNotFound{Payload: cvpErr(404, "nf")})
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisNotFound)
		assert.True(t, ok)
	})
	t.Run("conflict", func(t *testing.T) {
		res := mapCVPDiagnoseErrorToResponse(&pools.V1betaDirectoryServiceDiagnosisConflict{Payload: cvpErr(409, "conflict")})
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisConflict)
		assert.True(t, ok)
	})
	t.Run("unprocessable entity", func(t *testing.T) {
		res := mapCVPDiagnoseErrorToResponse(&pools.V1betaDirectoryServiceDiagnosisUnprocessableEntity{Payload: cvpErr(422, "unproc")})
		sc, ok := res.(*gcpgenserver.ErrorStatusCode)
		require.True(t, ok)
		assert.Equal(t, http.StatusUnprocessableEntity, sc.StatusCode)
	})
	t.Run("unauthorized", func(t *testing.T) {
		res := mapCVPDiagnoseErrorToResponse(&pools.V1betaDirectoryServiceDiagnosisUnauthorized{Payload: cvpErr(401, "unauth")})
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisUnauthorized)
		assert.True(t, ok)
	})
	t.Run("forbidden", func(t *testing.T) {
		res := mapCVPDiagnoseErrorToResponse(&pools.V1betaDirectoryServiceDiagnosisForbidden{Payload: cvpErr(403, "forbidden")})
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisForbidden)
		assert.True(t, ok)
	})
	t.Run("too many requests", func(t *testing.T) {
		res := mapCVPDiagnoseErrorToResponse(&pools.V1betaDirectoryServiceDiagnosisTooManyRequests{Payload: cvpErr(429, "rate")})
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisTooManyRequests)
		assert.True(t, ok)
	})
	t.Run("default", func(t *testing.T) {
		res := mapCVPDiagnoseErrorToResponse(errors.New("unknown"))
		_, ok := res.(*gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError)
		assert.True(t, ok)
	})
}
