package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	orchestratorMocks "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	utilsErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

const (
	testBatchPoolID1 = "bc816c73-a338-a283-1f7f-48b32edcb305"
	testBatchPoolID2 = "c7b35aad-f1c7-86ac-df97-a214c6607deb"
)

func TestV1BatchUpgradeClusters_PoolList_ReturnsAcceptedResponse(t *testing.T) {
	mockOrch := &orchestratorMocks.MockOrchestratorFactory{}
	handler := &Handler{Orchestrator: mockOrch}
	mockResponse := &models.BatchClusterUpgradeResponse{
		BatchUpgradeID: "batch-uuid-2",
		TotalClusters:  2,
		SelectedClusters: []models.SelectedCluster{
			{ClusterID: "pool-1", PoolID: "pool-1", CurrentVersion: "9.18.1P1", Priority: 1},
			{ClusterID: "pool-2", PoolID: "pool-2", CurrentVersion: "9.18.1P1", Priority: 2},
		},
		Status: models.UpgradeStatusPending,
		JobID:  "batch-uuid-2",
	}
	mockOrch.On("BatchUpgradeClusters", mock.Anything, mock.MatchedBy(func(params *commonparams.BatchUpgradeClusterParams) bool {
		return params.SelectionType == "poolList" &&
			assert.ObjectsAreEqual([]string{testBatchPoolID1, testBatchPoolID2}, params.PoolIDs) &&
			params.FailureThreshold == 0
	})).Return(mockResponse, "batch-uuid-2", nil)

	result, err := handler.V1BatchUpgradeClusters(context.Background(), &oasgenserver.BatchClusterUpgradeRequestV1{
		SelectionType: oasgenserver.BatchClusterUpgradeRequestV1SelectionTypePoolList,
		PoolIds:       []string{testBatchPoolID1, testBatchPoolID2},
	})

	require.NoError(t, err)
	response, ok := result.(*oasgenserver.BatchClusterUpgradeResponseV1)
	require.True(t, ok)
	assert.Equal(t, "batch-uuid-2", response.BatchUpgradeId)
	assert.Equal(t, 2, response.TotalClusters)
	assert.Len(t, response.SelectedClusters, 2)
	assert.Equal(t, oasgenserver.BatchClusterUpgradeResponseV1StatusPENDING, response.Status)
	mockOrch.AssertExpectations(t)
}

func TestV1BatchUpgradeClusters_Percentage_PassesFilters(t *testing.T) {
	mockOrch := &orchestratorMocks.MockOrchestratorFactory{}
	handler := &Handler{Orchestrator: mockOrch}
	mockResponse := &models.BatchClusterUpgradeResponse{
		BatchUpgradeID: "batch-uuid-3",
		TotalClusters:  1,
		Status:         models.UpgradeStatusPending,
		JobID:          "batch-uuid-3",
	}
	mockOrch.On("BatchUpgradeClusters", mock.Anything, mock.MatchedBy(func(params *commonparams.BatchUpgradeClusterParams) bool {
		return params.SelectionType == "percentage" &&
			params.Percentage == 50 &&
			params.Filters != nil &&
			assert.ObjectsAreEqual([]string{"us-central1-a"}, params.Filters.Zones)
	})).Return(mockResponse, "batch-uuid-3", nil)

	result, err := handler.V1BatchUpgradeClusters(context.Background(), &oasgenserver.BatchClusterUpgradeRequestV1{
		SelectionType: oasgenserver.BatchClusterUpgradeRequestV1SelectionTypePercentage,
		Percentage:    oasgenserver.NewOptInt(50),
		Filters: oasgenserver.NewOptBatchClusterFiltersV1(oasgenserver.BatchClusterFiltersV1{
			Zones:         []string{"us-central1-a"},
			ClusterStates: []string{"READY"},
		}),
	})

	require.NoError(t, err)
	_, ok := result.(*oasgenserver.BatchClusterUpgradeResponseV1)
	assert.True(t, ok)
	mockOrch.AssertExpectations(t)
}

func TestV1BatchUpgradeClusters_InvalidSelectionCriteria_ReturnsBadRequest(t *testing.T) {
	tests := []struct {
		name    string
		request *oasgenserver.BatchClusterUpgradeRequestV1
		message string
	}{
		{
			name: "percentage omitted",
			request: &oasgenserver.BatchClusterUpgradeRequestV1{
				SelectionType: oasgenserver.BatchClusterUpgradeRequestV1SelectionTypePercentage,
			},
			message: "Percentage must be between 1 and 100",
		},
		{
			name: "pool IDs omitted",
			request: &oasgenserver.BatchClusterUpgradeRequestV1{
				SelectionType: oasgenserver.BatchClusterUpgradeRequestV1SelectionTypePoolList,
			},
			message: "Pool IDs are required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &Handler{}
			result, err := handler.V1BatchUpgradeClusters(context.Background(), test.request)

			require.NoError(t, err)
			response, ok := result.(*oasgenserver.V1BatchUpgradeClustersBadRequest)
			require.True(t, ok)
			assert.Contains(t, response.Message, test.message)
		})
	}
}

func TestV1BatchUpgradeClusters_OrchestratorErrors_ReturnMappedResponses(t *testing.T) {
	tests := []struct {
		name         string
		orchestrator error
		assertResult func(t *testing.T, result oasgenserver.V1BatchUpgradeClustersRes)
	}{
		{
			name:         "bad request",
			orchestrator: utilsErrors.NewBadRequestErr("No eligible clusters"),
			assertResult: func(t *testing.T, result oasgenserver.V1BatchUpgradeClustersRes) {
				_, ok := result.(*oasgenserver.V1BatchUpgradeClustersBadRequest)
				assert.True(t, ok)
			},
		},
		{
			name:         "not found",
			orchestrator: utilsErrors.NewNotFoundErr("clusters", nil),
			assertResult: func(t *testing.T, result oasgenserver.V1BatchUpgradeClustersRes) {
				_, ok := result.(*oasgenserver.V1BatchUpgradeClustersNotFound)
				assert.True(t, ok)
			},
		},
		{
			name:         "conflict",
			orchestrator: utilsErrors.NewConflictErr("pool already has an active upgrade"),
			assertResult: func(t *testing.T, result oasgenserver.V1BatchUpgradeClustersRes) {
				response, ok := result.(*oasgenserver.V1BatchUpgradeClustersConflict)
				require.True(t, ok)
				assert.Equal(t, float64(409), response.Code)
				assert.Contains(t, response.Message, "active upgrade")
			},
		},
		{
			name:         "internal",
			orchestrator: assert.AnError,
			assertResult: func(t *testing.T, result oasgenserver.V1BatchUpgradeClustersRes) {
				_, ok := result.(*oasgenserver.V1BatchUpgradeClustersInternalServerError)
				assert.True(t, ok)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockOrch := &orchestratorMocks.MockOrchestratorFactory{}
			handler := &Handler{Orchestrator: mockOrch}
			mockOrch.On("BatchUpgradeClusters", mock.Anything, mock.Anything).
				Return(nil, "", test.orchestrator)

			result, err := handler.V1BatchUpgradeClusters(context.Background(), &oasgenserver.BatchClusterUpgradeRequestV1{
				SelectionType: oasgenserver.BatchClusterUpgradeRequestV1SelectionTypePoolList,
				PoolIds:       []string{testBatchPoolID1},
			})

			require.NoError(t, err)
			test.assertResult(t, result)
			mockOrch.AssertExpectations(t)
		})
	}
}

func TestBatchUpgradeRoute_OmittedFailureThreshold_DefaultsToZero(t *testing.T) {
	mockOrch := &orchestratorMocks.MockOrchestratorFactory{}
	handler := &Handler{Orchestrator: mockOrch}
	mockResponse := &models.BatchClusterUpgradeResponse{
		BatchUpgradeID: "batch-uuid-4",
		TotalClusters:  2,
		Status:         models.UpgradeStatusPending,
		JobID:          "batch-uuid-4",
	}
	mockOrch.On("BatchUpgradeClusters", mock.Anything, mock.MatchedBy(func(params *commonparams.BatchUpgradeClusterParams) bool {
		return params.FailureThreshold == 0 && params.MaxConcurrency == 5
	})).Return(mockResponse, "batch-uuid-4", nil)

	server, err := oasgenserver.NewServer(handler)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/v1/clusters/batchUpgrade", bytes.NewBufferString(`{
		"selectionType": "poolList",
		"poolIds": ["bc816c73-a338-a283-1f7f-48b32edcb305", "c7b35aad-f1c7-86ac-df97-a214c6607deb"]
	}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())
	mockOrch.AssertExpectations(t)
}

func TestBatchUpgradeRoute_PoolIDWithTrailingWhitespace_ReturnsBadRequest(t *testing.T) {
	server, err := oasgenserver.NewServer(&Handler{})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/v1/clusters/batchUpgrade", bytes.NewBufferString(`{
		"selectionType": "poolList",
		"poolIds": ["bc816c73-a338-a283-1f7f-48b32edcb305 "]
	}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "leading or trailing whitespace is not allowed")
}

func TestValidateBatchUpgradePoolIDs_ReturnsAllIssuesWithoutArrayIndexes(t *testing.T) {
	duplicatePoolID := "6ad0e18f-cf83-41d4-942f-da16e837cfa7"
	poolIDs := []string{
		"not-a-uuid",
		"bc816c73-a338-a283-1f7f-48b32edcb305 ",
		"bc816c73-a338-a283-1f7f-48b32edcb305123",
		testBatchPoolID1,
		testBatchPoolID2,
		duplicatePoolID,
		duplicatePoolID,
		"65fdf383-86d5-47f3-886f-c004231271a6",
		"07c42205-b327-4be4-bc32-d87db224a640",
		"8a58ac92-3109-4685-91b6-20da5bc8aa70",
	}

	err := validateBatchUpgradePoolIDs(poolIDs)

	require.Error(t, err)
	assert.ErrorContains(t, err, `"not-a-uuid": must be a valid UUID`)
	assert.ErrorContains(t, err, `"bc816c73-a338-a283-1f7f-48b32edcb305 ": leading or trailing whitespace is not allowed`)
	assert.ErrorContains(t, err, `"bc816c73-a338-a283-1f7f-48b32edcb305123": must be a valid UUID`)
	assert.ErrorContains(t, err, `"`+duplicatePoolID+`": duplicate pool ID`)
	assert.NotContains(t, err.Error(), "poolIds[")
}

func TestV1GetBatchUpgradeStatus_ReturnsProgress(t *testing.T) {
	mockOrch := &orchestratorMocks.MockOrchestratorFactory{}
	handler := &Handler{Orchestrator: mockOrch}
	mockProgress := &models.UpgradeProgress{
		JobID:  "batch-1",
		Status: models.UpgradeStatusInProgress,
		Clusters: []models.ClusterUpgradeStatus{
			{ClusterID: "c1", Status: "COMPLETED"},
			{ClusterID: "c2", Status: "IN_PROGRESS"},
		},
	}
	mockOrch.On("GetBatchUpgradeStatus", mock.Anything, "batch-1").Return(mockProgress, nil)

	result, err := handler.V1GetBatchUpgradeStatus(context.Background(), oasgenserver.V1GetBatchUpgradeStatusParams{
		BatchUpgradeId: "batch-1",
	})

	require.NoError(t, err)
	response, ok := result.(*oasgenserver.UpgradeProgressV1)
	require.True(t, ok)
	assert.Equal(t, "batch-1", response.JobId)
	assert.Len(t, response.Clusters, 2)
	mockOrch.AssertExpectations(t)
}
