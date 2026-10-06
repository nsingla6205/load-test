package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestV1ReconcilePoolAuthType(t *testing.T) {
	poolUUID := uuid.New()
	jobUUID := uuid.New()
	childWorkflowID := jobUUID.String() + "-pool-" + poolUUID.String()
	orchestrator := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(orchestrator)
	orchestrator.On(
		"ReconcilePoolAuthTypes",
		mock.Anything,
		mock.MatchedBy(func(params *commonparams.ReconcilePoolAuthTypeParams) bool {
			return params.SelectionType == "poolList" &&
				params.FromAuthType == 1 &&
				params.ToAuthType == 2 &&
				len(params.PoolUUIDs) == 1 &&
				params.PoolUUIDs[0] == poolUUID.String() &&
				params.BatchSize == 25
		}),
	).Return(&models.AuthTypeReconciliationResponse{
		JobID:        jobUUID.String(),
		Status:       "PROCESSING",
		TotalPools:   1,
		QueuedPools:  1,
		SkippedPools: 0,
		Results: []models.AuthTypeReconciliationPoolResult{{
			PoolUUID: poolUUID.String(),
			Status:   "QUEUED",
			JobID:    childWorkflowID,
		}},
	}, nil).Once()

	response, err := handler.V1ReconcilePoolAuthType(
		context.Background(),
		&oasgenserver.AuthTypeReconciliationRequestV1{
			SelectionType: oasgenserver.AuthTypeReconciliationRequestV1SelectionTypePoolList,
			FromAuthType:  oasgenserver.AuthTypeReconciliationRequestV1FromAuthType1,
			ToAuthType:    oasgenserver.AuthTypeReconciliationRequestV1ToAuthType2,
			PoolUuids:     []uuid.UUID{poolUUID},
			BatchSize:     oasgenserver.NewOptInt(25),
		},
	)

	require.NoError(t, err)
	accepted, ok := response.(*oasgenserver.AuthTypeReconciliationResponseV1)
	require.True(t, ok)
	require.Equal(t, jobUUID, accepted.JobId)
	require.Equal(t, 1, accepted.QueuedPools)
	require.Equal(t, oasgenserver.AuthTypeReconciliationResponseV1StatusPROCESSING, accepted.Status)
	require.Equal(t, childWorkflowID, accepted.Results[0].JobId.Value)
}

func TestV1ReconcilePoolAuthType_NotImplemented(t *testing.T) {
	orchestrator := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(orchestrator)
	orchestrator.On(
		"ReconcilePoolAuthTypes",
		mock.Anything,
		mock.Anything,
	).Return(
		(*models.AuthTypeReconciliationResponse)(nil),
		customerrors.NewNotImplementedYetErr(),
	).Once()

	response, err := handler.V1ReconcilePoolAuthType(
		context.Background(),
		&oasgenserver.AuthTypeReconciliationRequestV1{
			SelectionType: oasgenserver.AuthTypeReconciliationRequestV1SelectionTypeAll,
			FromAuthType:  oasgenserver.AuthTypeReconciliationRequestV1FromAuthType1,
			ToAuthType:    oasgenserver.AuthTypeReconciliationRequestV1ToAuthType2,
		},
	)

	require.NoError(t, err)
	_, ok := response.(*oasgenserver.V1ReconcilePoolAuthTypeNotImplemented)
	require.True(t, ok)
}

func TestV1ReconcilePoolAuthType_NilRequest(t *testing.T) {
	handler := NewHandler(factory.NewMockOrchestratorFactory(t))

	response, err := handler.V1ReconcilePoolAuthType(context.Background(), nil)

	require.NoError(t, err)
	badRequest, ok := response.(*oasgenserver.V1ReconcilePoolAuthTypeBadRequest)
	require.True(t, ok)
	require.Equal(t, "Request body is required", badRequest.Message)
}

func TestV1ReconcilePoolAuthType_ErrorMappings(t *testing.T) {
	tests := []struct {
		name       string
		returned   error
		assertType func(t *testing.T, response oasgenserver.V1ReconcilePoolAuthTypeRes)
	}{
		{
			name:     "bad request",
			returned: customerrors.NewBadRequestErr("invalid request"),
			assertType: func(t *testing.T, response oasgenserver.V1ReconcilePoolAuthTypeRes) {
				_, ok := response.(*oasgenserver.V1ReconcilePoolAuthTypeBadRequest)
				require.True(t, ok)
			},
		},
		{
			name:     "not found",
			returned: customerrors.NewNotFoundErr("pool", nil),
			assertType: func(t *testing.T, response oasgenserver.V1ReconcilePoolAuthTypeRes) {
				_, ok := response.(*oasgenserver.V1ReconcilePoolAuthTypeNotFound)
				require.True(t, ok)
			},
		},
		{
			name:     "conflict",
			returned: customerrors.NewConflictErr("reconciliation already running"),
			assertType: func(t *testing.T, response oasgenserver.V1ReconcilePoolAuthTypeRes) {
				_, ok := response.(*oasgenserver.V1ReconcilePoolAuthTypeConflict)
				require.True(t, ok)
			},
		},
		{
			name:     "internal error",
			returned: errors.New("database unavailable"),
			assertType: func(t *testing.T, response oasgenserver.V1ReconcilePoolAuthTypeRes) {
				internal, ok := response.(*oasgenserver.V1ReconcilePoolAuthTypeInternalServerError)
				require.True(t, ok)
				require.Equal(t, authTypeReconciliationQueueFailedMessage, internal.Message)
				require.NotContains(t, internal.Message, "database unavailable")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			orchestrator := factory.NewMockOrchestratorFactory(t)
			handler := NewHandler(orchestrator)
			orchestrator.On(
				"ReconcilePoolAuthTypes",
				mock.Anything,
				mock.Anything,
			).Return((*models.AuthTypeReconciliationResponse)(nil), test.returned).Once()

			response, err := handler.V1ReconcilePoolAuthType(
				context.Background(),
				validAuthTypeReconciliationRequest(),
			)

			require.NoError(t, err)
			test.assertType(t, response)
		})
	}
}

func TestV1GetPoolAuthTypeReconciliationStatus(t *testing.T) {
	jobUUID := uuid.New()
	poolUUID := uuid.New()
	childWorkflowID := jobUUID.String() + "-pool-" + poolUUID.String()
	orchestrator := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(orchestrator)
	orchestrator.On(
		"GetPoolAuthTypeReconciliationStatus",
		mock.Anything,
		jobUUID.String(),
	).Return(&models.AuthTypeReconciliationStatus{
		JobID:          jobUUID.String(),
		Status:         "PARTIAL",
		TotalPools:     2,
		SucceededPools: 1,
		FailedPools:    1,
		Results: []models.AuthTypeReconciliationPoolResult{{
			PoolUUID: poolUUID.String(),
			Status:   "DONE",
			JobID:    childWorkflowID,
		}},
	}, nil).Once()

	response, err := handler.V1GetPoolAuthTypeReconciliationStatus(
		context.Background(),
		oasgenserver.V1GetPoolAuthTypeReconciliationStatusParams{JobId: jobUUID},
	)

	require.NoError(t, err)
	status, ok := response.(*oasgenserver.AuthTypeReconciliationStatusV1)
	require.True(t, ok)
	require.Equal(t, oasgenserver.AuthTypeReconciliationStatusV1StatusPARTIAL, status.Status)
	require.Equal(t, 1, status.SucceededPools)
	require.Equal(t, 1, status.FailedPools)
	require.Equal(t, childWorkflowID, status.Results[0].JobId.Value)
}

func TestGetPoolAuthTypeReconciliationStatusRoute(t *testing.T) {
	t.Run("accepts job ID as query parameter", func(t *testing.T) {
		jobUUID := uuid.New()
		orchestrator := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(orchestrator)
		orchestrator.On(
			"GetPoolAuthTypeReconciliationStatus",
			mock.Anything,
			jobUUID.String(),
		).Return(&models.AuthTypeReconciliationStatus{
			JobID:  jobUUID.String(),
			Status: "DONE",
		}, nil).Once()

		server, err := oasgenserver.NewServer(handler)
		require.NoError(t, err)
		request := httptest.NewRequest(
			http.MethodGet,
			"/v1/pools/reconcileAuthType?jobId="+jobUUID.String(),
			nil,
		)
		recorder := httptest.NewRecorder()

		server.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		orchestrator.AssertExpectations(t)
	})

	t.Run("rejects missing job ID query parameter", func(t *testing.T) {
		server, err := oasgenserver.NewServer(&Handler{})
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodGet, "/v1/pools/reconcileAuthType", nil)
		recorder := httptest.NewRecorder()

		server.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	})
}

func TestV1GetPoolAuthTypeReconciliationStatus_ErrorMappings(t *testing.T) {
	tests := []struct {
		name       string
		returned   error
		assertType func(t *testing.T, response oasgenserver.V1GetPoolAuthTypeReconciliationStatusRes)
	}{
		{
			name:     "bad request",
			returned: customerrors.NewBadRequestErr("invalid job ID"),
			assertType: func(t *testing.T, response oasgenserver.V1GetPoolAuthTypeReconciliationStatusRes) {
				_, ok := response.(*oasgenserver.V1GetPoolAuthTypeReconciliationStatusBadRequest)
				require.True(t, ok)
			},
		},
		{
			name:     "not found",
			returned: customerrors.NewNotFoundErr("job", nil),
			assertType: func(t *testing.T, response oasgenserver.V1GetPoolAuthTypeReconciliationStatusRes) {
				_, ok := response.(*oasgenserver.V1GetPoolAuthTypeReconciliationStatusNotFound)
				require.True(t, ok)
			},
		},
		{
			name:     "not implemented",
			returned: customerrors.NewNotImplementedYetErr(),
			assertType: func(t *testing.T, response oasgenserver.V1GetPoolAuthTypeReconciliationStatusRes) {
				_, ok := response.(*oasgenserver.V1GetPoolAuthTypeReconciliationStatusNotImplemented)
				require.True(t, ok)
			},
		},
		{
			name:     "internal error",
			returned: errors.New("database unavailable"),
			assertType: func(t *testing.T, response oasgenserver.V1GetPoolAuthTypeReconciliationStatusRes) {
				internal, ok := response.(*oasgenserver.V1GetPoolAuthTypeReconciliationStatusInternalServerError)
				require.True(t, ok)
				require.Equal(t, authTypeReconciliationStatusFailedMessage, internal.Message)
				require.NotContains(t, internal.Message, "database unavailable")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jobID := uuid.New()
			orchestrator := factory.NewMockOrchestratorFactory(t)
			handler := NewHandler(orchestrator)
			orchestrator.On(
				"GetPoolAuthTypeReconciliationStatus",
				mock.Anything,
				jobID.String(),
			).Return((*models.AuthTypeReconciliationStatus)(nil), test.returned).Once()

			response, err := handler.V1GetPoolAuthTypeReconciliationStatus(
				context.Background(),
				oasgenserver.V1GetPoolAuthTypeReconciliationStatusParams{JobId: jobID},
			)

			require.NoError(t, err)
			test.assertType(t, response)
		})
	}
}

func TestV1ReconcilePoolAuthType_InvalidStoredPoolUUID(t *testing.T) {
	orchestrator := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(orchestrator)
	orchestrator.On("ReconcilePoolAuthTypes", mock.Anything, mock.Anything).Return(&models.AuthTypeReconciliationResponse{
		JobID:  uuid.NewString(),
		Status: "PROCESSING",
		Results: []models.AuthTypeReconciliationPoolResult{{
			PoolUUID: "not-a-uuid",
			Status:   "QUEUED",
		}},
	}, nil).Once()

	response, err := handler.V1ReconcilePoolAuthType(context.Background(), validAuthTypeReconciliationRequest())

	require.NoError(t, err)
	internal, ok := response.(*oasgenserver.V1ReconcilePoolAuthTypeInternalServerError)
	require.True(t, ok)
	require.Equal(t, authTypeReconciliationQueueFailedMessage, internal.Message)
	require.NotContains(t, internal.Message, "not-a-uuid")
}

func TestV1GetPoolAuthTypeReconciliationStatus_InvalidStoredPoolUUID(t *testing.T) {
	jobUUID := uuid.New()
	orchestrator := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(orchestrator)
	orchestrator.On("GetPoolAuthTypeReconciliationStatus", mock.Anything, jobUUID.String()).Return(&models.AuthTypeReconciliationStatus{
		JobID:  jobUUID.String(),
		Status: "DONE",
		Results: []models.AuthTypeReconciliationPoolResult{{
			PoolUUID: "not-a-uuid",
			Status:   "DONE",
		}},
	}, nil).Once()

	response, err := handler.V1GetPoolAuthTypeReconciliationStatus(
		context.Background(),
		oasgenserver.V1GetPoolAuthTypeReconciliationStatusParams{JobId: jobUUID},
	)

	require.NoError(t, err)
	internal, ok := response.(*oasgenserver.V1GetPoolAuthTypeReconciliationStatusInternalServerError)
	require.True(t, ok)
	require.Equal(t, authTypeReconciliationStatusFailedMessage, internal.Message)
	require.NotContains(t, internal.Message, "not-a-uuid")
}

func TestAuthTypeReconciliationResponseToAPI(t *testing.T) {
	jobID := uuid.New()
	poolID := uuid.New()
	tests := []struct {
		name     string
		response *models.AuthTypeReconciliationResponse
		wantErr  string
	}{
		{
			name: "valid job and pool ids",
			response: &models.AuthTypeReconciliationResponse{
				JobID:        jobID.String(),
				Status:       "PROCESSING",
				TotalPools:   1,
				QueuedPools:  1,
				SkippedPools: 0,
				Results: []models.AuthTypeReconciliationPoolResult{{
					PoolUUID: poolID.String(),
					Status:   "QUEUED",
					JobID:    jobID.String() + "-pool-" + poolID.String(),
					Reason:   "waiting",
				}},
			},
		},
		{
			name: "invalid job id",
			response: &models.AuthTypeReconciliationResponse{
				JobID:  "not-a-uuid",
				Status: "PROCESSING",
			},
			wantErr: "parse auth-type reconciliation job id",
		},
		{
			name: "invalid pool id",
			response: &models.AuthTypeReconciliationResponse{
				JobID:  jobID.String(),
				Status: "PROCESSING",
				Results: []models.AuthTypeReconciliationPoolResult{{
					PoolUUID: "not-a-uuid",
					Status:   "QUEUED",
				}},
			},
			wantErr: `parse auth-type reconciliation pool id "not-a-uuid"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converted, err := authTypeReconciliationResponseToAPI(test.response)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				require.Nil(t, converted)
				return
			}
			require.NoError(t, err)
			require.Equal(t, jobID, converted.JobId)
			require.Equal(t, oasgenserver.AuthTypeReconciliationResponseV1StatusPROCESSING, converted.Status)
			require.Equal(t, poolID, converted.Results[0].PoolUuid)
			require.Equal(t, jobID.String()+"-pool-"+poolID.String(), converted.Results[0].JobId.Value)
			require.Equal(t, "waiting", converted.Results[0].Reason.Value)
		})
	}
}

func TestV1ReconcilePoolAuthType_InvalidStoredJobID(t *testing.T) {
	orchestrator := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(orchestrator)
	orchestrator.On("ReconcilePoolAuthTypes", mock.Anything, mock.Anything).Return(&models.AuthTypeReconciliationResponse{
		JobID:  "not-a-uuid",
		Status: "PROCESSING",
	}, nil).Once()

	response, err := handler.V1ReconcilePoolAuthType(context.Background(), validAuthTypeReconciliationRequest())

	require.NoError(t, err)
	internal, ok := response.(*oasgenserver.V1ReconcilePoolAuthTypeInternalServerError)
	require.True(t, ok)
	require.Equal(t, float64(500), internal.Code)
	require.Equal(t, authTypeReconciliationQueueFailedMessage, internal.Message)
	require.NotContains(t, internal.Message, "not-a-uuid")
}

func TestV1GetPoolAuthTypeReconciliationStatus_InvalidStoredJobID(t *testing.T) {
	jobUUID := uuid.New()
	orchestrator := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(orchestrator)
	orchestrator.On("GetPoolAuthTypeReconciliationStatus", mock.Anything, jobUUID.String()).Return(&models.AuthTypeReconciliationStatus{
		JobID:  "not-a-uuid",
		Status: "DONE",
	}, nil).Once()

	response, err := handler.V1GetPoolAuthTypeReconciliationStatus(
		context.Background(),
		oasgenserver.V1GetPoolAuthTypeReconciliationStatusParams{JobId: jobUUID},
	)

	require.NoError(t, err)
	internal, ok := response.(*oasgenserver.V1GetPoolAuthTypeReconciliationStatusInternalServerError)
	require.True(t, ok)
	require.Equal(t, float64(500), internal.Code)
	require.Equal(t, authTypeReconciliationStatusFailedMessage, internal.Message)
	require.NotContains(t, internal.Message, "not-a-uuid")
}

func validAuthTypeReconciliationRequest() *oasgenserver.AuthTypeReconciliationRequestV1 {
	return &oasgenserver.AuthTypeReconciliationRequestV1{
		SelectionType: oasgenserver.AuthTypeReconciliationRequestV1SelectionTypeAll,
		FromAuthType:  oasgenserver.AuthTypeReconciliationRequestV1FromAuthType1,
		ToAuthType:    oasgenserver.AuthTypeReconciliationRequestV1ToAuthType2,
	}
}
