package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	orchestratorMocks "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utilsErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// Test V1UpgradeCluster function
func TestV1UpgradeCluster_Success(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
		Metadata:           oasgenserver.NewOptClusterUpgradeRequestV1Metadata(map[string]string{"key": "value"}),
	}

	// Create test parameters
	params := oasgenserver.V1UpgradeClusterParams{
		ClusterId: "test-cluster-id",
	}

	// Create expected orchestrator parameters
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
		Metadata:           map[string]string{"key": "value"},
	}

	// Create mock response
	mockResponse := &models.ClusterUpgradeResponse{
		ClusterID: "test-cluster-id",
		Status:    models.UpgradeStatusPending,
		JobID:     "test-job-id",
		CreatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	// Set up mock expectation
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(mockResponse, "test-job-id", nil)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1UpgradeCluster(ctx, req, params)

	// Assert success
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to success response
	successResponse, ok := result.(*oasgenserver.ClusterUpgradeResponseV1)
	assert.True(t, ok)
	assert.Equal(t, "test-cluster-id", successResponse.ClusterId)
	assert.Equal(t, oasgenserver.ClusterUpgradeResponseV1StatusPENDING, successResponse.Status)
	assert.Equal(t, "test-job-id", successResponse.JobId)
	assert.Equal(t, time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), successResponse.CreatedAt)
	assert.Equal(t, time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), successResponse.UpdatedAt)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1UpgradeCluster_NotFoundError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
		Metadata:           oasgenserver.NewOptClusterUpgradeRequestV1Metadata(map[string]string{"key": "value"}),
	}

	// Create test parameters
	params := oasgenserver.V1UpgradeClusterParams{
		ClusterId: "test-cluster-id",
	}

	// Create expected orchestrator parameters
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
		Metadata:           map[string]string{"key": "value"},
	}

	// Set up mock to return not found error
	clusterId := params.ClusterId
	mockError := utilsErrors.NewNotFoundErr("Cluster", &clusterId)
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(nil, "", mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1UpgradeCluster(ctx, req, params)

	// Assert success with not found error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to not found response
	notFoundResponse, ok := result.(*oasgenserver.V1UpgradeClusterNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(404), notFoundResponse.Code)
	assert.Contains(t, notFoundResponse.Message, "Cluster not found: test-cluster-id")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1UpgradeCluster_BadRequestError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
		Metadata:           oasgenserver.NewOptClusterUpgradeRequestV1Metadata(map[string]string{"key": "value"}),
	}

	// Create test parameters
	params := oasgenserver.V1UpgradeClusterParams{
		ClusterId: "test-cluster-id",
	}

	// Create expected orchestrator parameters
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
		Metadata:           map[string]string{"key": "value"},
	}

	// Set up mock to return bad request error
	mockError := errors.New("bad request")
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(nil, "", mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1UpgradeCluster(ctx, req, params)

	// Assert success with bad request error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to bad request response
	badRequestResponse, ok := result.(*oasgenserver.V1UpgradeClusterBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequestResponse.Code)
	assert.Contains(t, badRequestResponse.Message, "bad request")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1UpgradeCluster_ConflictError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
		Metadata:           oasgenserver.NewOptClusterUpgradeRequestV1Metadata(map[string]string{"key": "value"}),
	}

	// Create test parameters
	params := oasgenserver.V1UpgradeClusterParams{
		ClusterId: "test-cluster-id",
	}

	// Create expected orchestrator parameters
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
		Metadata:           map[string]string{"key": "value"},
	}

	// Set up mock to return conflict error
	mockError := errors.New("conflict")
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(nil, "", mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1UpgradeCluster(ctx, req, params)

	// Assert success with conflict error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to conflict response
	conflictResponse, ok := result.(*oasgenserver.V1UpgradeClusterConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), conflictResponse.Code)
	assert.Equal(t, "conflict", conflictResponse.Message)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1UpgradeCluster_ClusterStateConflictError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
		Metadata:           oasgenserver.NewOptClusterUpgradeRequestV1Metadata(map[string]string{"key": "value"}),
	}

	// Create test parameters
	params := oasgenserver.V1UpgradeClusterParams{
		ClusterId: "test-cluster-id",
	}

	// Create expected orchestrator parameters
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
		Metadata:           map[string]string{"key": "value"},
	}

	// Set up mock to return cluster state error as BadRequestErr (should return 409)
	mockError := utilsErrors.NewBadRequestErr("Cluster must be in READY or DISABLED state for upgrade. Current state: UPDATING")
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(nil, "", mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1UpgradeCluster(ctx, req, params)

	// Assert success with conflict error response (409)
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to conflict response
	conflictResponse, ok := result.(*oasgenserver.V1UpgradeClusterConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), conflictResponse.Code)
	assert.Contains(t, conflictResponse.Message, "Cluster must be in READY or DISABLED state for upgrade")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1UpgradeCluster_ZoneSwitchConflictError(t *testing.T) {
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}
	handler := Handler{Orchestrator: mockOrchestrator}

	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
	}
	params := oasgenserver.V1UpgradeClusterParams{ClusterId: "test-cluster-id"}
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
	}

	mockError := utilsErrors.NewConflictErr("Cluster upgrade is not allowed when the pool is switching/switched to a different primary zone.")
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(nil, "", mockError)

	result, err := handler.V1UpgradeCluster(context.Background(), req, params)

	assert.NoError(t, err)
	conflictResponse, ok := result.(*oasgenserver.V1UpgradeClusterConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), conflictResponse.Code)
	assert.Contains(t, conflictResponse.Message, "switching/switched")
	mockOrchestrator.AssertExpectations(t)
}

func TestV1UpgradeCluster_ForbiddenError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
		Metadata:           oasgenserver.NewOptClusterUpgradeRequestV1Metadata(map[string]string{"key": "value"}),
	}

	// Create test parameters
	params := oasgenserver.V1UpgradeClusterParams{
		ClusterId: "test-cluster-id",
	}

	// Create expected orchestrator parameters
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
		Metadata:           map[string]string{"key": "value"},
	}

	// Set up mock to return forbidden error
	mockError := errors.New("forbidden")
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(nil, "", mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1UpgradeCluster(ctx, req, params)

	// Assert success with forbidden error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to forbidden response
	forbiddenResponse, ok := result.(*oasgenserver.V1UpgradeClusterForbidden)
	assert.True(t, ok)
	assert.Equal(t, float64(403), forbiddenResponse.Code)
	assert.Equal(t, "forbidden", forbiddenResponse.Message)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1UpgradeCluster_ServiceUnavailableError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
		Metadata:           oasgenserver.NewOptClusterUpgradeRequestV1Metadata(map[string]string{"key": "value"}),
	}

	// Create test parameters
	params := oasgenserver.V1UpgradeClusterParams{
		ClusterId: "test-cluster-id",
	}

	// Create expected orchestrator parameters
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
		Metadata:           map[string]string{"key": "value"},
	}

	// Set up mock to return service unavailable error
	mockError := errors.New("Failed to retrieve cluster information")
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(nil, "", mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1UpgradeCluster(ctx, req, params)

	// Assert success with service unavailable error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to service unavailable response
	serviceUnavailableResponse, ok := result.(*oasgenserver.V1UpgradeClusterInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(503), serviceUnavailableResponse.Code)
	assert.Contains(t, serviceUnavailableResponse.Message, "Service temporarily unavailable")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1UpgradeCluster_InternalServerError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ClusterUpgradeRequestV1{
		VsaBuildImage:      oasgenserver.NewOptString("vsa-image:latest"),
		MediatorBuildImage: oasgenserver.NewOptString("mediator-image:latest"),
		ForceUpgrade:       oasgenserver.NewOptBool(true),
		Metadata:           oasgenserver.NewOptClusterUpgradeRequestV1Metadata(map[string]string{"key": "value"}),
	}

	// Create test parameters
	params := oasgenserver.V1UpgradeClusterParams{
		ClusterId: "test-cluster-id",
	}

	// Create expected orchestrator parameters
	expectedParams := &commonparams.UpgradeClusterParams{
		ClusterID:          "test-cluster-id",
		VSABuildImage:      "vsa-image:latest",
		MediatorBuildImage: "mediator-image:latest",
		ForceUpgrade:       true,
		Metadata:           map[string]string{"key": "value"},
	}

	// Set up mock to return generic error
	mockError := errors.New("generic error")
	mockOrchestrator.On("UpgradeCluster", mock.Anything, expectedParams).Return(nil, "", mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1UpgradeCluster(ctx, req, params)

	// Assert success with internal server error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to internal server error response
	internalServerErrorResponse, ok := result.(*oasgenserver.V1UpgradeClusterInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), internalServerErrorResponse.Code)
	assert.Contains(t, internalServerErrorResponse.Message, "Upgrade operation failed")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

// Test V1GetClusterUpgradeStatus function
func TestV1GetClusterUpgradeStatus_Success(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1GetClusterUpgradeStatusParams{
		JobId: "test-job-id",
	}

	// Create mock response
	startTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	endTime := time.Date(2023, 1, 1, 13, 0, 0, 0, time.UTC)
	mockProgress := &models.UpgradeProgress{
		JobID:  "test-job-id",
		Status: models.UpgradeStatusInProgress,
		Clusters: []models.ClusterUpgradeStatus{
			{
				ClusterID:   "test-cluster-id",
				Status:      "IN_PROGRESS",
				StartTime:   &startTime,
				EndTime:     &endTime,
				CurrentStep: "upgrading",
			},
		},
		Errors: []models.UpgradeError{
			{
				Code:      "UPGRADE_ERROR",
				Message:   "test error",
				Type:      "validation",
				Retryable: true,
				ClusterID: "test-cluster-id",
			},
		},
		Warnings: []string{"test warning"},
	}

	// Set up mock expectation
	mockOrchestrator.On("GetClusterUpgradeStatus", mock.Anything, "test-job-id").Return(mockProgress, nil)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1GetClusterUpgradeStatus(ctx, params)

	// Assert success
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to success response
	successResponse, ok := result.(*oasgenserver.UpgradeProgressV1)
	assert.True(t, ok)
	assert.Equal(t, "test-job-id", successResponse.JobId)
	assert.Equal(t, oasgenserver.UpgradeProgressV1StatusINPROGRESS, successResponse.Status)
	assert.Len(t, successResponse.Clusters, 1)
	assert.Len(t, successResponse.Errors, 1)
	assert.Len(t, successResponse.Warnings, 1)

	// Verify cluster status
	cluster := successResponse.Clusters[0]
	assert.Equal(t, "test-cluster-id", cluster.ClusterId)
	assert.Equal(t, oasgenserver.ClusterUpgradeStatusV1StatusINPROGRESS, cluster.Status)
	assert.True(t, cluster.StartTime.Set)
	assert.Equal(t, startTime, cluster.StartTime.Value)
	assert.True(t, cluster.EndTime.Set)
	assert.Equal(t, endTime, cluster.EndTime.Value)
	assert.True(t, cluster.CurrentStep.Set)
	assert.Equal(t, "upgrading", cluster.CurrentStep.Value)

	// Verify error
	error := successResponse.Errors[0]
	assert.Equal(t, "UPGRADE_ERROR", error.Code)
	assert.Equal(t, "test error", error.Message)
	assert.Equal(t, "validation", error.Type)
	assert.True(t, error.Retryable)
	assert.True(t, error.ClusterId.Set)
	assert.Equal(t, "test-cluster-id", error.ClusterId.Value)

	// Verify warning
	assert.Equal(t, "test warning", successResponse.Warnings[0])

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1GetClusterUpgradeStatus_NotFoundError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1GetClusterUpgradeStatusParams{
		JobId: "test-job-id",
	}

	// Set up mock to return not found error
	mockError := errors.New("record not found")
	mockOrchestrator.On("GetClusterUpgradeStatus", mock.Anything, "test-job-id").Return(nil, mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1GetClusterUpgradeStatus(ctx, params)

	// Assert success with not found error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to not found response
	notFoundResponse, ok := result.(*oasgenserver.V1GetClusterUpgradeStatusNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(404), notFoundResponse.Code)
	assert.Contains(t, notFoundResponse.Message, "Upgrade job not found: test-job-id")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1GetClusterUpgradeStatus_BadRequestError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1GetClusterUpgradeStatusParams{
		JobId: "test-job-id",
	}

	// Set up mock to return bad request error
	mockError := errors.New("bad request")
	mockOrchestrator.On("GetClusterUpgradeStatus", mock.Anything, "test-job-id").Return(nil, mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1GetClusterUpgradeStatus(ctx, params)

	// Assert success with bad request error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to bad request response
	badRequestResponse, ok := result.(*oasgenserver.V1GetClusterUpgradeStatusBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequestResponse.Code)
	assert.Contains(t, badRequestResponse.Message, "Invalid request")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1GetClusterUpgradeStatus_InternalServerError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1GetClusterUpgradeStatusParams{
		JobId: "test-job-id",
	}

	// Set up mock to return generic error
	mockError := errors.New("generic error")
	mockOrchestrator.On("GetClusterUpgradeStatus", mock.Anything, "test-job-id").Return(nil, mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1GetClusterUpgradeStatus(ctx, params)

	// Assert success with internal server error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to internal server error response
	internalServerErrorResponse, ok := result.(*oasgenserver.V1GetClusterUpgradeStatusInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), internalServerErrorResponse.Code)
	assert.Contains(t, internalServerErrorResponse.Message, "Failed to get upgrade status")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

// Test convertClusterStatusesToAPI function
func TestConvertClusterStatusesToAPI(t *testing.T) {
	t.Run("Nil clusters", func(t *testing.T) {
		result := convertClusterStatusesToAPI(nil)
		assert.Nil(t, result)
	})

	t.Run("Empty clusters", func(t *testing.T) {
		result := convertClusterStatusesToAPI([]models.ClusterUpgradeStatus{})
		assert.NotNil(t, result)
		assert.Len(t, result, 0)
	})

	t.Run("Valid clusters", func(t *testing.T) {
		startTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
		endTime := time.Date(2023, 1, 1, 13, 0, 0, 0, time.UTC)
		clusters := []models.ClusterUpgradeStatus{
			{
				ClusterID:   "cluster-1",
				Status:      "IN_PROGRESS",
				StartTime:   &startTime,
				EndTime:     &endTime,
				CurrentStep: "upgrading",
			},
			{
				ClusterID:   "cluster-2",
				Status:      "COMPLETED",
				StartTime:   nil,
				EndTime:     nil,
				CurrentStep: "",
			},
		}

		result := convertClusterStatusesToAPI(clusters)
		assert.NotNil(t, result)
		assert.Len(t, result, 2)

		// Verify first cluster
		cluster1 := result[0]
		assert.Equal(t, "cluster-1", cluster1.ClusterId)
		assert.Equal(t, oasgenserver.ClusterUpgradeStatusV1StatusINPROGRESS, cluster1.Status)
		assert.True(t, cluster1.StartTime.Set)
		assert.Equal(t, startTime, cluster1.StartTime.Value)
		assert.True(t, cluster1.EndTime.Set)
		assert.Equal(t, endTime, cluster1.EndTime.Value)
		assert.True(t, cluster1.CurrentStep.Set)
		assert.Equal(t, "upgrading", cluster1.CurrentStep.Value)

		// Verify second cluster
		cluster2 := result[1]
		assert.Equal(t, "cluster-2", cluster2.ClusterId)
		assert.Equal(t, oasgenserver.ClusterUpgradeStatusV1StatusCOMPLETED, cluster2.Status)
		assert.False(t, cluster2.StartTime.Set)
		assert.False(t, cluster2.EndTime.Set)
		assert.False(t, cluster2.CurrentStep.Set)
	})
}

// Test convertUpgradeErrorsToAPI function
func TestConvertUpgradeErrorsToAPI(t *testing.T) {
	t.Run("Nil errors", func(t *testing.T) {
		result := convertUpgradeErrorsToAPI(nil)
		assert.Nil(t, result)
	})

	t.Run("Empty errors", func(t *testing.T) {
		result := convertUpgradeErrorsToAPI([]models.UpgradeError{})
		assert.NotNil(t, result)
		assert.Len(t, result, 0)
	})

	t.Run("Valid errors", func(t *testing.T) {
		errors := []models.UpgradeError{
			{
				Code:      "ERROR_1",
				Message:   "First error",
				Type:      "validation",
				Retryable: true,
				ClusterID: "cluster-1",
			},
			{
				Code:      "ERROR_2",
				Message:   "Second error",
				Type:      "system",
				Retryable: false,
				ClusterID: "",
			},
		}

		result := convertUpgradeErrorsToAPI(errors)
		assert.NotNil(t, result)
		assert.Len(t, result, 2)

		// Verify first error
		error1 := result[0]
		assert.Equal(t, "ERROR_1", error1.Code)
		assert.Equal(t, "First error", error1.Message)
		assert.Equal(t, "validation", error1.Type)
		assert.True(t, error1.Retryable)
		assert.True(t, error1.ClusterId.Set)
		assert.Equal(t, "cluster-1", error1.ClusterId.Value)

		// Verify second error
		error2 := result[1]
		assert.Equal(t, "ERROR_2", error2.Code)
		assert.Equal(t, "Second error", error2.Message)
		assert.Equal(t, "system", error2.Type)
		assert.False(t, error2.Retryable)
		assert.False(t, error2.ClusterId.Set)
	})
}

// Test error handling patterns
func TestErrorHandlingPattern(t *testing.T) {
	t.Run("Not Found Error Detection", func(t *testing.T) {
		err := errors.New("record not found")

		// Test the string matching logic used in the handler
		assert.True(t, strings.Contains(err.Error(), "not found"))
		assert.True(t, strings.Contains(err.Error(), "record not found"))
	})

	t.Run("Bad Request Error Detection", func(t *testing.T) {
		err := errors.New("bad request")

		// Test the string matching logic used in the handler
		assert.True(t, strings.Contains(err.Error(), "bad request"))
		assert.False(t, strings.Contains(err.Error(), "invalid request"))
	})

	t.Run("Service Unavailable Error Detection", func(t *testing.T) {
		err := errors.New("Failed to retrieve cluster information")

		// Test the string matching logic used in the handler
		assert.False(t, strings.Contains(err.Error(), "unavailable"))
		assert.True(t, strings.Contains(err.Error(), "Failed to retrieve cluster information"))
	})

	t.Run("Conflict Error Detection", func(t *testing.T) {
		err := errors.New("conflict")

		// Test the string matching logic used in the handler
		assert.True(t, strings.Contains(err.Error(), "conflict"))
	})

	t.Run("Forbidden Error Detection", func(t *testing.T) {
		err := errors.New("forbidden")

		// Test the string matching logic used in the handler
		assert.True(t, strings.Contains(err.Error(), "forbidden"))
	})
}

// Test V1ListImageVersions function
func TestV1ListImageVersions_Success(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1ListImageVersionsParams{}

	// Create mock response
	mockResponse := &models.ListAvailableVersionsResponse{
		Current: "9.17.1P1",
		Versions: []models.AvailableVersion{
			{
				OntapVersion: "9.17.1P1",
				VSAImagePath: "gcr.io/vsa-image:9.17.1",
				VSAName:      "vsa-9.17.1",
				MediatorName: "mediator-9.17.1",
				IsCurrent:    true,
				IsActive:     true,
			},
			{
				OntapVersion: "9.16.1",
				VSAImagePath: "gcr.io/vsa-image:9.16.1",
				VSAName:      "vsa-9.16.1",
				MediatorName: "mediator-9.16.1",
				IsCurrent:    false,
				IsActive:     true,
			},
		},
	}

	// Set up mock expectation
	mockOrchestrator.On("ListAvailableVersions", mock.Anything).Return(mockResponse, nil)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1ListImageVersions(ctx, params)

	// Assert success
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to success response
	successResponse, ok := result.(*oasgenserver.ListAvailableVersionsResponseV1)
	assert.True(t, ok)
	assert.Equal(t, "9.17.1P1", successResponse.Current)
	assert.Len(t, successResponse.Versions, 2)

	// Verify first version
	assert.Equal(t, "9.17.1P1", successResponse.Versions[0].OntapVersion)
	assert.Equal(t, "gcr.io/vsa-image:9.17.1", successResponse.Versions[0].VsaImagePath)
	assert.Equal(t, "vsa-9.17.1", successResponse.Versions[0].VsaName)
	assert.Equal(t, "mediator-9.17.1", successResponse.Versions[0].MediatorName)
	assert.True(t, successResponse.Versions[0].IsCurrent)
	assert.True(t, successResponse.Versions[0].IsActive)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1ListImageVersions_InternalServerError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1ListImageVersionsParams{}

	// Set up mock to return error
	mockError := errors.New("database error")
	mockOrchestrator.On("ListAvailableVersions", mock.Anything).Return(nil, mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1ListImageVersions(ctx, params)

	// Assert success with internal server error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to internal server error response
	internalServerErrorResponse, ok := result.(*oasgenserver.V1ListImageVersionsInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), internalServerErrorResponse.Code)
	assert.Equal(t, "Failed to retrieve available versions", internalServerErrorResponse.Message)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

// Test V1CreateImageVersion function
func TestV1CreateImageVersion_Success(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "9.17.1P1",
		VsaImagePath: "gcr.io/vsa-image:9.17.1",
		VsaName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
		IsActive:     true,
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Create mock response
	mockCreatedVersion := &datamodel.ImageVersion{
		BaseModel: datamodel.BaseModel{
			UUID: "test-uuid",
		},
		OntapVersion: "9.17.1P1",
		VSAImagePath: "gcr.io/vsa-image:9.17.1",
		VSAName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
		IsActive:     true,
	}

	// Set up mock expectation
	mockOrchestrator.On("CreateImageVersion", mock.Anything, "9.17.1P1", "gcr.io/vsa-image:9.17.1", "vsa-9.17.1", "mediator-9.17.1", true).Return(mockCreatedVersion, nil)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to success response
	successResponse, ok := result.(*oasgenserver.AvailableVersionV1)
	assert.True(t, ok)
	assert.Equal(t, "9.17.1P1", successResponse.OntapVersion)
	assert.Equal(t, "gcr.io/vsa-image:9.17.1", successResponse.VsaImagePath)
	assert.Equal(t, "vsa-9.17.1", successResponse.VsaName)
	assert.Equal(t, "mediator-9.17.1", successResponse.MediatorName)
	assert.False(t, successResponse.IsCurrent) // Newly created versions are never current
	assert.True(t, successResponse.IsActive)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1CreateImageVersion_MissingOntapVersion(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request with missing ontapVersion
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "",
		VsaImagePath: "gcr.io/vsa-image:9.17.1",
		VsaName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success with bad request error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to bad request response
	badRequestResponse, ok := result.(*oasgenserver.V1CreateImageVersionBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequestResponse.Code)
	assert.Equal(t, "ontapVersion is required", badRequestResponse.Message)
}

func TestV1CreateImageVersion_MissingVsaImagePath(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request with missing vsaImagePath
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "9.17.1P1",
		VsaImagePath: "",
		VsaName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success with bad request error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to bad request response
	badRequestResponse, ok := result.(*oasgenserver.V1CreateImageVersionBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequestResponse.Code)
	assert.Equal(t, "vsaImagePath is required", badRequestResponse.Message)
}

func TestV1CreateImageVersion_MissingVsaName(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request with missing vsaName
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "9.17.1P1",
		VsaImagePath: "gcr.io/vsa-image:9.17.1",
		VsaName:      "",
		MediatorName: "mediator-9.17.1",
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success with bad request error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to bad request response
	badRequestResponse, ok := result.(*oasgenserver.V1CreateImageVersionBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequestResponse.Code)
	assert.Equal(t, "vsaName is required", badRequestResponse.Message)
}

func TestV1CreateImageVersion_MissingMediatorName(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request with missing mediatorName
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "9.17.1P1",
		VsaImagePath: "gcr.io/vsa-image:9.17.1",
		VsaName:      "vsa-9.17.1",
		MediatorName: "",
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success with bad request error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to bad request response
	badRequestResponse, ok := result.(*oasgenserver.V1CreateImageVersionBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequestResponse.Code)
	assert.Equal(t, "mediatorName is required", badRequestResponse.Message)
}

func TestV1CreateImageVersion_ConflictError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "9.17.1P1",
		VsaImagePath: "gcr.io/vsa-image:9.17.1",
		VsaName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
		IsActive:     true,
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Set up mock to return conflict error
	mockError := errors.New("already exists")
	mockOrchestrator.On("CreateImageVersion", mock.Anything, "9.17.1P1", "gcr.io/vsa-image:9.17.1", "vsa-9.17.1", "mediator-9.17.1", true).Return(nil, mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success with conflict error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to conflict response
	conflictResponse, ok := result.(*oasgenserver.V1CreateImageVersionConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), conflictResponse.Code)
	assert.Contains(t, conflictResponse.Message, "Image version with ONTAP version '9.17.1P1' already exists")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1CreateImageVersion_BadRequestError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "9.17.1P1",
		VsaImagePath: "gcr.io/vsa-image:9.17.1",
		VsaName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
		IsActive:     true,
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Set up mock to return bad request error
	mockError := errors.New("bad request")
	mockOrchestrator.On("CreateImageVersion", mock.Anything, "9.17.1P1", "gcr.io/vsa-image:9.17.1", "vsa-9.17.1", "mediator-9.17.1", true).Return(nil, mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success with bad request error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to bad request response
	badRequestResponse, ok := result.(*oasgenserver.V1CreateImageVersionBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequestResponse.Code)
	assert.Equal(t, "bad request", badRequestResponse.Message)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1CreateImageVersion_InternalServerError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "9.17.1P1",
		VsaImagePath: "gcr.io/vsa-image:9.17.1",
		VsaName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
		IsActive:     true,
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Set up mock to return generic error
	mockError := errors.New("generic error")
	mockOrchestrator.On("CreateImageVersion", mock.Anything, "9.17.1P1", "gcr.io/vsa-image:9.17.1", "vsa-9.17.1", "mediator-9.17.1", true).Return(nil, mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success with internal server error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to internal server error response
	internalServerErrorResponse, ok := result.(*oasgenserver.V1CreateImageVersionInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), internalServerErrorResponse.Code)
	assert.Equal(t, "Failed to create image version", internalServerErrorResponse.Message)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1CreateImageVersion_DefaultIsActive(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test request without isActive (should default to false)
	req := &oasgenserver.ImageVersionCreateRequestV1{
		OntapVersion: "9.17.1P1",
		VsaImagePath: "gcr.io/vsa-image:9.17.1",
		VsaName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
		IsActive:     false, // Default value
	}

	// Create test parameters
	params := oasgenserver.V1CreateImageVersionParams{}

	// Create mock response
	mockCreatedVersion := &datamodel.ImageVersion{
		BaseModel: datamodel.BaseModel{
			UUID: "test-uuid",
		},
		OntapVersion: "9.17.1P1",
		VSAImagePath: "gcr.io/vsa-image:9.17.1",
		VSAName:      "vsa-9.17.1",
		MediatorName: "mediator-9.17.1",
		IsActive:     false, // Default value
	}

	// Set up mock expectation - isActive should be false (default)
	mockOrchestrator.On("CreateImageVersion", mock.Anything, "9.17.1P1", "gcr.io/vsa-image:9.17.1", "vsa-9.17.1", "mediator-9.17.1", false).Return(mockCreatedVersion, nil)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1CreateImageVersion(ctx, req, params)

	// Assert success
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

// Test V1DeleteImageVersion function
func TestV1DeleteImageVersion_Success(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1DeleteImageVersionParams{
		OntapVersion: "9.17.1P1",
	}

	// Set up mock expectation
	mockOrchestrator.On("DeleteImageVersion", mock.Anything, "9.17.1P1").Return(nil)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1DeleteImageVersion(ctx, params)

	// Assert success
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to no content response
	noContentResponse, ok := result.(*oasgenserver.V1DeleteImageVersionNoContent)
	assert.True(t, ok)
	assert.NotNil(t, noContentResponse)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1DeleteImageVersion_NotFoundError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1DeleteImageVersionParams{
		OntapVersion: "9.17.1P1",
	}

	// Set up mock to return not found error
	ontapVersion := params.OntapVersion
	mockError := utilsErrors.NewNotFoundErr("ImageVersion", &ontapVersion)
	mockOrchestrator.On("DeleteImageVersion", mock.Anything, "9.17.1P1").Return(mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1DeleteImageVersion(ctx, params)

	// Assert success with not found error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to not found response
	notFoundResponse, ok := result.(*oasgenserver.V1DeleteImageVersionNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(404), notFoundResponse.Code)
	assert.Contains(t, notFoundResponse.Message, "Image version with ONTAP version '9.17.1P1' not found")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1DeleteImageVersion_NotFoundErrorString(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1DeleteImageVersionParams{
		OntapVersion: "9.17.1P1",
	}

	// Set up mock to return not found error (string match)
	mockError := errors.New("not found")
	mockOrchestrator.On("DeleteImageVersion", mock.Anything, "9.17.1P1").Return(mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1DeleteImageVersion(ctx, params)

	// Assert success with not found error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to not found response
	notFoundResponse, ok := result.(*oasgenserver.V1DeleteImageVersionNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(404), notFoundResponse.Code)
	assert.Contains(t, notFoundResponse.Message, "Image version with ONTAP version '9.17.1P1' not found")

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1DeleteImageVersion_BadRequestError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1DeleteImageVersionParams{
		OntapVersion: "9.17.1P1",
	}

	// Set up mock to return bad request error
	mockError := errors.New("bad request")
	mockOrchestrator.On("DeleteImageVersion", mock.Anything, "9.17.1P1").Return(mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1DeleteImageVersion(ctx, params)

	// Assert success with bad request error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to bad request response
	badRequestResponse, ok := result.(*oasgenserver.V1DeleteImageVersionBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequestResponse.Code)
	assert.Equal(t, "bad request", badRequestResponse.Message)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}

func TestV1DeleteImageVersion_InternalServerError(t *testing.T) {
	// Create mock orchestrator
	mockOrchestrator := &orchestratorMocks.MockOrchestratorFactory{}

	// Create handler
	handler := Handler{
		Orchestrator: mockOrchestrator,
	}

	// Create test parameters
	params := oasgenserver.V1DeleteImageVersionParams{
		OntapVersion: "9.17.1P1",
	}

	// Set up mock to return generic error
	mockError := errors.New("generic error")
	mockOrchestrator.On("DeleteImageVersion", mock.Anything, "9.17.1P1").Return(mockError)

	// Call the handler
	ctx := context.Background()
	result, err := handler.V1DeleteImageVersion(ctx, params)

	// Assert success with internal server error response
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Cast to internal server error response
	internalServerErrorResponse, ok := result.(*oasgenserver.V1DeleteImageVersionInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), internalServerErrorResponse.Code)
	assert.Equal(t, "Failed to delete image version", internalServerErrorResponse.Message)

	// Verify mock was called correctly
	mockOrchestrator.AssertExpectations(t)
}
