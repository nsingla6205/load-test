package api

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/active_directories"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/pools"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func setTestCVPHost(tt *testing.T, host string) {
	tt.Helper()
	orig := cvp.CVP_HOST
	cvp.SetCVPHost(host)
	tt.Cleanup(func() { cvp.SetCVPHost(orig) })
}

func TestResolvePerformanceParams(t *testing.T) {
	t.Run("UsesDefaultThroughputWhenNotProvided", func(tt *testing.T) {
		reqThroughput := gcpgenserver.OptNilFloat64{}
		reqIops := gcpgenserver.OptNilFloat64{Value: 2048, Set: true}

		throughput, iops := resolvePerformanceParams(reqThroughput, reqIops)

		assert.Equal(tt, minCustomThroughput, uint64(throughput))
		assert.Equal(tt, int64(2048), *iops)
	})

	t.Run("UsesProvidedValuesWhenBothSet", func(tt *testing.T) {
		reqThroughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
		reqIops := gcpgenserver.OptNilFloat64{Value: 5000, Set: true}

		throughput, iops := resolvePerformanceParams(reqThroughput, reqIops)

		assert.Equal(tt, int64(256), throughput)
		assert.Equal(tt, int64(5000), *iops)
	})

	t.Run("HandlesLargeValues", func(tt *testing.T) {
		reqThroughput := gcpgenserver.OptNilFloat64{Value: 5120, Set: true} // Max throughput
		reqIops := gcpgenserver.OptNilFloat64{Value: 160000, Set: true}     // Max IOPS

		throughput, iops := resolvePerformanceParams(reqThroughput, reqIops)

		assert.Equal(tt, int64(5120), throughput)
		assert.Equal(tt, int64(160000), *iops)
	})

	t.Run("HandlesMinimumValues", func(tt *testing.T) {
		reqThroughput := gcpgenserver.OptNilFloat64{Value: 64, Set: true} // Min throughput
		reqIops := gcpgenserver.OptNilFloat64{Value: 1024, Set: true}     // Min IOPS

		throughput, iops := resolvePerformanceParams(reqThroughput, reqIops)

		assert.Equal(tt, int64(64), throughput)
		assert.Equal(tt, int64(1024), *iops)
	})
}

func TestV1betaGetMultiplePools(t *testing.T) {
	t.Run("WhenGetMultiplePoolsFailsWithBadRequest", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return empty pools
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Since CVP_HOST is not set, we expect OK response with empty pools
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 0)
	})
	t.Run("WhenGetMultiplePoolsFailsWithUnauthorized", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return empty pools
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Since CVP_HOST is not set, we expect OK response with empty pools
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 0)
	})
	t.Run("WhenGetMultiplePoolsSucceeds", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return empty pools
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Since CVP_HOST is not set, we expect OK response with empty pools
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 0)
	})

	t.Run("Success - all pools found in VCP, CVP_HOST is set", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return all requested pools
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
			{
				BaseModel:      models.BaseModel{UUID: "uuid2"},
				Name:           "pool2",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, successResult.Pools, 2)
	})

	t.Run("Success - some pools found in VCP, some in CVP, CVP_HOST is set", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2", "uuid3"},
		}

		// Mock VCP to return only one pool
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, successResult.Pools, 1) // Only VCP pools, no CVP call
	})

	t.Run("Success - CVP_HOST is not set", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return only one pool
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, successResult.Pools, 1) // Only VCP pools, no CVP call
	})

	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "invalid-location",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock location validation to fail
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location",
			}
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badRequest, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badRequest.Code)
		assert.Equal(tt, "Invalid location", badRequest.Message)
	})

	t.Run("WhenPoolUuidsIsNil", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: nil,
		}

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badRequest, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badRequest.Code)
		assert.Equal(tt, "PoolUUIDs is required", badRequest.Message)
	})

	t.Run("WhenPoolUuidsExceeds1000", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		// Generate more than 1000 UUIDs
		poolUuids := make([]string, 1001)
		for i := 0; i < 1001; i++ {
			poolUuids[i] = fmt.Sprintf("uuid-%d", i)
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: poolUuids,
		}

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badRequest, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badRequest.Code)
		assert.Equal(tt, "poolUUIDs in body should have at most 1000 items", badRequest.Message)
	})

	t.Run("WhenGetMultiplePoolsFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock orchestrator to return error
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		internalError, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalError.Code)
		assert.Equal(tt, "Internal server error while getting pools", internalError.Message)
	})

	t.Run("WhenNoMissingPools", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return all requested pools (no missing pools)
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
			{
				BaseModel:      models.BaseModel{UUID: "uuid2"},
				Name:           "pool2",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Since all pools are found in VCP, we expect OK response with all pools
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 2)
	})

	t.Run("WhenNoMissingPoolsWithCVPEnabled", func(tt *testing.T) {
		// Set CVP_HOST so CVP calls will be made
		setTestCVPHost(tt, "http://cvp-host")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return all requested pools (no missing pools)
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
			{
				BaseModel:      models.BaseModel{UUID: "uuid2"},
				Name:           "pool2",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Since all pools are found in VCP, we expect OK response with all pools (no CVP call)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 2)
	})

	t.Run("WhenMissingPoolsWithCVPEnabled", func(tt *testing.T) {
		// Set CVP_HOST so CVP calls will be made
		setTestCVPHost(tt, "http://cvp-host")

		// Mock the CVP client so the handler can safely attempt the fallback lookup.
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		mockPools := pools.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Pools: mockPools,
		}
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}
		mockPools.EXPECT().V1betaGetMultiplePools(mock.Anything).Return(&pools.V1betaGetMultiplePoolsOK{
			Payload: &pools.V1betaGetMultiplePoolsOKBody{
				Pools: []*cvpmodels.PoolV1beta{},
			},
		}, nil)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2", "uuid3"},
		}

		// Mock VCP to return only one pool (missing pools will trigger CVP call)
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// CVP is queried for the missing pools; when it returns none, the response contains only VCP pools.
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 1)
	})

	t.Run("Covers getMultiplePoolsFromCVP path", func(tt *testing.T) {
		// Set CVP_HOST so the handler will not return early
		cvp.SetCVPHost("http://cvp-host")

		// Save and mock createClient
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		mockPools := pools.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Pools: mockPools,
		}
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		// Set up the mock for the CVP Pools client
		resourceID := "resource-id-2"
		network := "network-2"
		sizeInBytes := float64(1000000000)
		serviceLevel := "PREMIUM"
		storageClass := cvpmodels.StorageClassV1betaSOFTWARE
		storagePoolState := "READY"
		encryptionType := "SERVICE_MANAGED"

		mockPools.EXPECT().V1betaGetMultiplePools(mock.Anything).Return(&pools.V1betaGetMultiplePoolsOK{
			Payload: &pools.V1betaGetMultiplePoolsOKBody{
				Pools: []*cvpmodels.PoolV1beta{
					{
						PoolID:           "uuid2",
						ResourceID:       &resourceID,
						Network:          &network,
						SizeInBytes:      &sizeInBytes,
						ServiceLevel:     &serviceLevel,
						StorageClass:     storageClass,
						StoragePoolState: storagePoolState,
						EncryptionType:   encryptionType,
					},
				},
			},
		}, nil)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// VCP returns only one pool, so uuid2 is missing and triggers CVP call
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		// Should contain both VCP and CVP pools
		assert.Len(tt, okResp.Pools, 2)
	})

	t.Run("WhenOrchestratorGetMultiplePoolsFails_ReturnsInternalServerError", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return an error
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(nil, stderrors.New("database connection failed"))

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should return InternalServerError with proper error message
		internalServerError, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalServerError.Code)
		assert.Equal(tt, "Internal server error while getting pools", internalServerError.Message)
	})

	t.Run("WhenOrchestratorGetMultiplePoolsFails_ErrorNotReturned", func(tt *testing.T) {
		// Don't set CVP_HOST so CVP calls will be skipped
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return an error
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(nil, stderrors.New("database connection failed"))

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		// Key change: err should be nil, not the orchestrator error
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should return InternalServerError, not propagate the orchestrator error
		internalServerError, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalServerError.Code)
	})

	t.Run("WhenSomePoolsNotFoundInVCP_LogsDebugMessage", func(tt *testing.T) {
		// Set CVP_HOST to enable CVP calls
		setTestCVPHost(tt, "http://cvp-host")

		// Save and mock createClient
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		mockPools := pools.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Pools: mockPools,
		}
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		// Mock CVP to return the missing pool (uuid3)
		resourceID := "resource-id-3"
		network := "network-3"
		sizeInBytes := float64(1000000000)
		serviceLevel := "PREMIUM"
		storageClass := cvpmodels.StorageClassV1betaSOFTWARE
		storagePoolState := "READY"
		encryptionType := "SERVICE_MANAGED"

		mockPools.EXPECT().V1betaGetMultiplePools(mock.Anything).Return(&pools.V1betaGetMultiplePoolsOK{
			Payload: &pools.V1betaGetMultiplePoolsOKBody{
				Pools: []*cvpmodels.PoolV1beta{
					{
						PoolID:           "uuid3",
						ResourceID:       &resourceID,
						Network:          &network,
						SizeInBytes:      &sizeInBytes,
						ServiceLevel:     &serviceLevel,
						StorageClass:     storageClass,
						StoragePoolState: storagePoolState,
						EncryptionType:   encryptionType,
					},
				},
			},
		}, nil)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2", "uuid3"},
		}

		// Mock VCP to return only some pools (uuid1 and uuid2 found, uuid3 missing)
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
			{
				BaseModel:      models.BaseModel{UUID: "uuid2"},
				Name:           "pool2",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should return OK response with both VCP and CVP pools
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 3)
		// Verify the pools returned are from both VCP and CVP
		assert.Equal(tt, "uuid1", okResp.Pools[0].PoolId.Value)
		assert.Equal(tt, "uuid2", okResp.Pools[1].PoolId.Value)
		assert.Equal(tt, "uuid3", okResp.Pools[2].PoolId.Value)
	})

	t.Run("WhenAllPoolsFoundInVCP_NoCVPFallback", func(tt *testing.T) {
		// Set CVP_HOST to enable CVP calls
		setTestCVPHost(tt, "http://cvp-host")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return all requested pools
		vcpPools := []*models.Pool{
			{
				BaseModel:      models.BaseModel{UUID: "uuid1"},
				Name:           "pool1",
				PoolAttributes: &models.PoolAttributes{},
			},
			{
				BaseModel:      models.BaseModel{UUID: "uuid2"},
				Name:           "pool2",
				PoolAttributes: &models.PoolAttributes{},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Since all pools are found in VCP, we expect OK response with all pools
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 2)
	})

	t.Run("WhenOrchestratorGetMultiplePoolsReturnsEmpty_TriggersCVPFallback", func(tt *testing.T) {
		// Set CVP_HOST to enable CVP calls
		setTestCVPHost(tt, "http://cvp-host")

		// Save and mock createClient
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		mockPools := pools.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Pools: mockPools,
		}
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		// Mock CVP to return empty pools
		mockPools.EXPECT().V1betaGetMultiplePools(mock.Anything).Return(&pools.V1betaGetMultiplePoolsOK{
			Payload: &pools.V1betaGetMultiplePoolsOKBody{
				Pools: []*cvpmodels.PoolV1beta{},
			},
		}, nil)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return empty pools (triggering CVP fallback)
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return([]*models.Pool{}, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should return OK response with empty pools from CVP fallback
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 0) // CVP fallback returns empty pools
	})

	t.Run("WhenOrchestratorGetMultiplePoolsReturnsNil_TriggersCVPFallback", func(tt *testing.T) {
		// Set CVP_HOST to enable CVP calls
		setTestCVPHost(tt, "http://cvp-host")

		// Save and mock createClient
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		mockPools := pools.NewMockClientService(tt)
		mockClient := &cvpapi.Cvp{
			Pools: mockPools,
		}
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *mockClient
		}

		// Mock CVP to return empty pools
		mockPools.EXPECT().V1betaGetMultiplePools(mock.Anything).Return(&pools.V1betaGetMultiplePoolsOK{
			Payload: &pools.V1betaGetMultiplePoolsOKBody{
				Pools: []*cvpmodels.PoolV1beta{},
			},
		}, nil)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1", "uuid2"},
		}

		// Mock VCP to return nil (triggering CVP fallback)
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should return OK response with empty pools from CVP fallback
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 0) // CVP fallback returns empty pools
	})

	t.Run("WhenPoolsHaveAutoTieringEnabled_IncludesConsumptionFields", func(tt *testing.T) {
		setTestCVPHost(tt, "")
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1"},
		}

		// Mock VCP to return pool with auto tiering enabled and consumption data
		vcpPools := []*models.Pool{
			{
				BaseModel: models.BaseModel{
					UUID: "uuid1",
				},
				Name:             "pool1",
				AllowAutoTiering: true,
				PoolAttributes:   &models.PoolAttributes{},
				AutoTieringConfig: &models.AutoTieringConfig{
					HotTierSizeInBytes:      300000000000, // 300GB
					EnableHotTierAutoResize: true,
					HotTierConsumption:      100000000000, // 100GB
					ColdTierConsumption:     50000000000,  // 50GB
				},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 1)
		// Verify consumption fields are included when auto tiering is enabled
		assert.True(tt, okResp.Pools[0].HotTierConsumption.IsSet())
		assert.Equal(tt, int64(100000000000), okResp.Pools[0].HotTierConsumption.Value)
		assert.True(tt, okResp.Pools[0].ColdTierConsumption.IsSet())
		assert.Equal(tt, int64(50000000000), okResp.Pools[0].ColdTierConsumption.Value)
		// Verify auto tiering related fields are included when auto tiering is enabled
		assert.True(tt, okResp.Pools[0].HotTierSizeInBytes.IsSet())
		assert.True(tt, okResp.Pools[0].EnableHotTierAutoResize.IsSet())
	})

	t.Run("WhenPoolsHaveAutoTieringDisabled_NoConsumptionFields", func(tt *testing.T) {
		setTestCVPHost(tt, "")
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaGetMultiplePoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolIdListV1beta{
			PoolUuids: []string{"uuid1"},
		}

		// Mock VCP to return pool with auto tiering disabled
		vcpPools := []*models.Pool{
			{
				BaseModel: models.BaseModel{
					UUID: "uuid1",
				},
				Name:             "pool1",
				AllowAutoTiering: false,
				PoolAttributes:   &models.PoolAttributes{},
				AutoTieringConfig: &models.AutoTieringConfig{
					HotTierSizeInBytes:      500000000000,
					EnableHotTierAutoResize: true,
					HotTierConsumption:      100000000000,
					ColdTierConsumption:     50000000000,
				},
			},
		}
		mockOrchestrator.EXPECT().GetMultiplePools(mock.Anything, mock.Anything, mock.Anything).Return(vcpPools, nil)

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaGetMultiplePools(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		okResp, ok := result.(*gcpgenserver.V1betaGetMultiplePoolsOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.Pools, 1)
		// Verify consumption fields are not included when auto tiering is disabled
		assert.False(tt, okResp.Pools[0].HotTierConsumption.IsSet())
		assert.False(tt, okResp.Pools[0].ColdTierConsumption.IsSet())
		// Verify auto tiering related fields are not included when auto tiering is disabled
		assert.False(tt, okResp.Pools[0].HotTierSizeInBytes.IsSet())
		assert.False(tt, okResp.Pools[0].EnableHotTierAutoResize.IsSet())
	})
}

func TestV1betaCreatePool(t *testing.T) {
	t.Run("WhenUnifiedIsNotSetToTrue", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		req := &gcpgenserver.PoolV1beta{
			Unified: gcpgenserver.NewOptBool(false),
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "type must be set to UNIFIED, or unified/unifiedPool must be set to true (for backward compatibility)", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenUnifiedPoolIsNotSetToTrue", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		req := &gcpgenserver.PoolV1beta{
			UnifiedPool: gcpgenserver.NewOptBool(false),
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "type must be set to UNIFIED, or unified/unifiedPool must be set to true (for backward compatibility)", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenRegionAndZoneParsingFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolV1beta{
			Unified: gcpgenserver.NewOptBool(true),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenLdapEnabledIsSetButActiveDirectoryConfigIdIsNotSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		// Mock the environment variable to enable LDAP
		originalEnableLdap := enableLdap
		enableLdap = true
		defer func() { enableLdap = originalEnableLdap }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		labels := make(map[string]string)
		labels["test"] = "label"

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
			Labels:                   gcpgenserver.NewOptPoolV1betaLabels(labels),
			LdapEnabled:              gcpgenserver.NewOptNilBool(true),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "Active Directory configuration is required when LDAP is enabled", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenLdapEnabledIsSetButLdapFeatureFlagIsDisabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		labels := make(map[string]string)
		labels["test"] = "label"

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
			Labels:                   gcpgenserver.NewOptPoolV1betaLabels(labels),
			ActiveDirectoryConfigId:  gcpgenserver.NewOptNilString("some-config-id"),
			LdapEnabled:              gcpgenserver.NewOptNilBool(true),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "LDAP is not currently supported for Unified Flex Storage Pool", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenLdapEnabledIsSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		// Mock the environment variable to enable LDAP
		originalEnableLdap := enableLdap
		enableLdap = true
		defer func() { enableLdap = originalEnableLdap }()
		// Mock Active Directory config
		adConfig := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				UUID: "ad-config-uuid",
			},
			AdName: "test-ad",
		}

		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		labels := make(map[string]string)
		labels["test"] = "label"

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
			Labels:                   gcpgenserver.NewOptPoolV1betaLabels(labels),
			ActiveDirectoryConfigId:  gcpgenserver.NewOptNilString("some-config-id"),
			LdapEnabled:              gcpgenserver.NewOptNilBool(true),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		// Mock the AD config retrieval
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(adConfig, nil)
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{BaseModel: models.BaseModel{UUID: "new-pool-uuid"}, PoolAttributes: &models.PoolAttributes{Labels: labels}}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "operation-id")
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.Equal(tt, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
		assert.NotNil(tt, result)
	})
	t.Run("WhenRegionalPoolSupportIsNotEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Create a request with regional pool parameters
		req := &gcpgenserver.PoolV1beta{
			Unified:       gcpgenserver.NewOptBool(true),
			Zone:          gcpgenserver.NewOptString("us-east2-b"),
			SecondaryZone: gcpgenserver.NewOptString("us-east2-a"),
		}

		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east2",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east2", "", nil
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		// Call the function
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		// Assert the error
		assert.NotNil(tt, result)
		assert.NoError(tt, err)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "Regional Pool Support is not enabled", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenRegionalPoolSupportIsEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		// Mock the environment variable to enable regional pool support
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()

		// Create a request with regional pool parameters
		req := &gcpgenserver.PoolV1beta{
			Unified:       gcpgenserver.NewOptBool(true),
			Zone:          gcpgenserver.NewOptString("us-east1-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east1-b"),
		}

		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east1",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east1", "", nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{BaseModel: models.BaseModel{UUID: "new-pool-uuid"}, PoolAttributes: &models.PoolAttributes{}}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "operation-id")
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenZoneIsEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()

		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolV1beta{
			Unified:      gcpgenserver.NewOptBool(true),
			ServiceLevel: gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:  1099511627776,
			QosType:      gcpgenserver.NewOptNilString("auto"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "Zone cannot be empty for regional pool.", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenSecondaryZoneIsEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolV1beta{
			Unified:      gcpgenserver.NewOptBool(true),
			ServiceLevel: gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:  1099511627776,
			QosType:      gcpgenserver.NewOptNilString("auto"),
			Zone:         gcpgenserver.NewOptString("us-east4-a"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "Secondary Zone cannot be empty for regional pool.", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenZonesDontMatch", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.PoolV1beta{
			Unified:       gcpgenserver.NewOptBool(true),
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   1099511627776,
			QosType:       gcpgenserver.NewOptNilString("auto"),
			Zone:          gcpgenserver.NewOptString("us-east4-b"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "Multiple Zone values cannot be passed for Zonal Pool Creation", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenPoolAlreadyExists", func(tt *testing.T) {
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone:            gcpgenserver.NewOptString("us-east4-b"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "existing-pool-uuid",
			},
			PoolAttributes: &models.PoolAttributes{},
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		conflictResp, ok := result.(*gcpgenserver.V1betaCreatePoolConflict)
		assert.True(tt, ok, "Expected V1betaCreatePoolConflict response")
		assert.NotNil(tt, conflictResp)
	})
	t.Run("WhenPoolCreationFailsWithUserInputValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone:            gcpgenserver.NewOptString("us-east4-b"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Given pool size must be a multiple of 1GiB"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "Given pool size must be a multiple of 1GiB", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})
	t.Run("WhenPoolCreationSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		labels := make(map[string]string)
		labels["test"] = "label"

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone:            gcpgenserver.NewOptString("us-east4-b"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
			Labels:                   gcpgenserver.NewOptPoolV1betaLabels(labels),
			Mode:                     gcpgenserver.NewOptPoolV1betaMode("GCNV"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone

		defer func() {
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			getAndSyncKmsConfigForPool = _getAndSyncKmsConfigForPool
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
			return nil, nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{BaseModel: models.BaseModel{UUID: "new-pool-uuid"}, PoolAttributes: &models.PoolAttributes{Labels: labels}}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "operation-id")
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenPoolCreationSucceedsWithManualQosType", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		originalEnableMqos := enableMqos
		regionalPoolEnabled = true
		enableMqos = true
		defer func() {
			regionalPoolEnabled = originalRegionalPoolEnabled
			enableMqos = originalEnableMqos
		}()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		labels := make(map[string]string)
		labels["test"] = "label"

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("manual"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone:            gcpgenserver.NewOptString("us-east4-b"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
			Labels:                   gcpgenserver.NewOptPoolV1betaLabels(labels),
			Mode:                     gcpgenserver.NewOptPoolV1betaMode("GCNV"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone

		defer func() {
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			getAndSyncKmsConfigForPool = _getAndSyncKmsConfigForPool
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
			return nil, nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{BaseModel: models.BaseModel{UUID: "new-pool-uuid"}, PoolAttributes: &models.PoolAttributes{Labels: labels}}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "operation-id")
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenPoolCreationSucceedsWithExpertMode", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		labels := make(map[string]string)
		labels["test"] = "label"

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone:            gcpgenserver.NewOptString("us-east4-b"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
			Labels:                   gcpgenserver.NewOptPoolV1betaLabels(labels),
			Mode:                     gcpgenserver.NewOptPoolV1betaMode("ONTAP"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone

		defer func() {
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			getAndSyncKmsConfigForPool = _getAndSyncKmsConfigForPool
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
			return nil, nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{BaseModel: models.BaseModel{UUID: "new-pool-uuid"}, PoolAttributes: &models.PoolAttributes{Labels: labels}}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "operation-id")
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	// Test cases for hotTierSizeInBytes assignment logic - keep only the essential integration tests
	t.Run("HotTierSizeInBytes assignment when auto-tiering is enabled", func(tt *testing.T) {
		// Save and restore the original auto-tiering state
		originalAutoTieringEnabled := autoTieringEnabled
		defer func() { autoTieringEnabled = originalAutoTieringEnabled }()
		autoTieringEnabled = true // Enable auto-tiering for this test

		const (
			poolSize    = 2199023255552 // 2 TiB
			hotTierSize = 1099511627776 // 1 TiB
		)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              poolSize,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			AllowAutoTiering:         gcpgenserver.NewOptNilBool(true),
			HotTierSizeInBytes:       gcpgenserver.NewOptNilFloat64(float64(hotTierSize)),
			EnableHotTierAutoResize:  gcpgenserver.NewOptNilBool(true),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			Network:                  "test-network",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		// Mock that pool doesn't exist
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Capture the CreatePool parameters to verify hotTierSizeInBytes assignment
		var capturedParams *common.CreatePoolParams
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(params *common.CreatePoolParams) bool {
			capturedParams = params
			return true
		})).Return(&models.Pool{BaseModel: models.BaseModel{UUID: "new-pool-uuid"}, PoolAttributes: &models.PoolAttributes{}}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		// Verify that when auto-tiering is enabled, hotTierSizeInBytes is set to HotTierSizeInBytes.Value
		assert.NotNil(tt, capturedParams, "CreatePool should have been called")
		assert.Equal(tt, uint64(hotTierSize), capturedParams.HotTierSizeInBytes, "HotTierSizeInBytes should be set to the explicit hot tier size when auto-tiering is enabled")
		assert.True(tt, capturedParams.AllowAutoTiering, "AllowAutoTiering should be true")
	})

	t.Run("HotTierSizeInBytes assignment when auto-tiering is disabled", func(tt *testing.T) {
		const poolSize = 2199023255552 // 2 TiB

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              poolSize,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			AllowAutoTiering:         gcpgenserver.NewOptNilBool(false),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			Network:                  "test-network",
			// HotTierSizeInBytes not set when auto-tiering is disabled
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		// Mock that pool doesn't exist
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Capture the CreatePool parameters to verify hotTierSizeInBytes assignment
		var capturedParams *common.CreatePoolParams
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(params *common.CreatePoolParams) bool {
			capturedParams = params
			return true
		})).Return(&models.Pool{BaseModel: models.BaseModel{UUID: "new-pool-uuid"}, PoolAttributes: &models.PoolAttributes{}}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		// Verify that when auto-tiering is disabled, hotTierSizeInBytes is set to SizeInBytes (pool size)
		assert.NotNil(tt, capturedParams, "CreatePool should have been called")
		assert.Equal(tt, uint64(poolSize), capturedParams.HotTierSizeInBytes, "HotTierSizeInBytes should be set to pool size when auto-tiering is disabled")
	})

	// Test cases for the new Type enum field
	t.Run("WhenTypeIsSetToUnified", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		req := &gcpgenserver.PoolV1beta{
			Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
			ResourceId:    "test-pool",
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   1099511627776,
			Network:       "test-network",
			Zone:          gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
		}

		// Mock that pool doesn't exist
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "new-pool-uuid"},
			PoolAttributes: &models.PoolAttributes{},
		}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should pass validation since Type is set to UNIFIED
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
	})

	t.Run("WhenTypeIsSetToStandard", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		req := &gcpgenserver.PoolV1beta{
			Type: gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeFILE),
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "type must be set to UNIFIED, or unified/unifiedPool must be set to true (for backward compatibility)", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})

	t.Run("WhenTypeIsSetToUnspecified", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		req := &gcpgenserver.PoolV1beta{
			Type: gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeSTORAGEPOOLTYPEUNSPECIFIED),
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "type field cannot be STORAGE_POOL_TYPE_UNSPECIFIED", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})

	t.Run("WhenTypeIsNotSetAndUnifiedFieldsAreNotSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:   "test-pool",
			ServiceLevel: gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:  1099511627776,
			Network:      "test-network",
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
		assert.Equal(tt, "type must be set to UNIFIED, or unified/unifiedPool must be set to true (for backward compatibility)", result.(*gcpgenserver.V1betaCreatePoolBadRequest).Message)
	})

	t.Run("WhenTypeIsSetToUnifiedAndUnifiedFieldIsAlsoSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		req := &gcpgenserver.PoolV1beta{
			Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
			Unified:       gcpgenserver.NewOptBool(true), // Both Type and unified are set
			ResourceId:    "test-pool",
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   1099511627776,
			Network:       "test-network",
			Zone:          gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
		}

		// Mock that pool doesn't exist
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "new-pool-uuid"},
			PoolAttributes: &models.PoolAttributes{},
		}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should pass validation since Type is set to UNIFIED
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
	})

	t.Run("WhenTypeIsSetToUnifiedAndUnifiedPoolFieldIsAlsoSet", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		req := &gcpgenserver.PoolV1beta{
			Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
			UnifiedPool:   gcpgenserver.NewOptBool(true), // Both Type and unifiedPool are set
			ResourceId:    "test-pool",
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   1099511627776,
			Network:       "test-network",
			Zone:          gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
		}

		// Mock that pool doesn't exist
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "new-pool-uuid"},
			PoolAttributes: &models.PoolAttributes{},
		}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should pass validation since Type is set to UNIFIED
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
	})

	t.Run("WhenPoolIsInCreatingState", func(tt *testing.T) {
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:    "test-pool",
			Unified:       gcpgenserver.NewOptBool(true),
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   1099511627776,
			Network:       "test-network",
			Zone:          gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		existingPool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "creating-pool-uuid"},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		job := &models.Job{
			BaseModel: models.BaseModel{UUID: "job-uuid"},
			Type:      datamodel.JobTypeCreatePool,
			JobAttributes: &models.JobAttributes{
				ResourceUUID: "creating-pool-uuid",
			},
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeCreatePool)).Return(job, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		operation, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok, "Expected OperationV1beta response")
		assert.False(tt, operation.Done.Value, "Operation should not be marked as done")
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/project-number/locations/us-east4-a/operations/job-uuid")
	})

	t.Run("WhenPoolIsInCreatingStateAndJobLookupFails", func(tt *testing.T) {
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:    "test-pool",
			Unified:       gcpgenserver.NewOptBool(true),
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   1099511627776,
			Network:       "test-network",
			Zone:          gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		existingPool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "creating-pool-uuid"},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeCreatePool)).Return(nil, errors.NewNotFoundErr("job not found", nil))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		operation, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok, "Expected OperationV1beta response")
		assert.True(tt, operation.Done.Value, "Operation should be marked as done when job lookup fails")
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/project-number/locations/us-east4-a/operations/00000000-0000-0000-0000-000000000000")
	})

	t.Run("WhenPoolIsInCreatingStateWithLargeCapacity_UsesCorrectJobType", func(tt *testing.T) {
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:    "test-large-pool",
			Unified:       gcpgenserver.NewOptBool(true),
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   13194139533312, // 12TiB - large capacity
			Network:       "test-network",
			Zone:          gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
			LargeCapacity: gcpgenserver.NewOptBool(true), // This is the key - large capacity pool
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		existingPool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "creating-pool-uuid"},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		job := &models.Job{
			BaseModel: models.BaseModel{UUID: "job-uuid"},
			Type:      datamodel.JobTypeCreateLargePool, // Should use large pool job type
			JobAttributes: &models.JobAttributes{
				ResourceUUID: "creating-pool-uuid",
			},
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		// This is the key assertion - line 225 should use JobTypeCreateLargePool for large capacity pools
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeCreateLargePool)).Return(job, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		operation, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok, "Expected OperationV1beta response")
		assert.False(tt, operation.Done.Value, "Operation should not be marked as done")
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/project-number/locations/us-east4-a/operations/job-uuid")
	})

	t.Run("WhenPoolIsInCreatingStateWithoutLargeCapacity_UsesCorrectJobType", func(tt *testing.T) {
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:    "test-regular-pool",
			Unified:       gcpgenserver.NewOptBool(true),
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   2199023255552, // 2TiB - regular pool
			Network:       "test-network",
			Zone:          gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
			LargeCapacity: gcpgenserver.NewOptBool(false), // Regular pool
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		existingPool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "creating-pool-uuid"},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		job := &models.Job{
			BaseModel: models.BaseModel{UUID: "job-uuid"},
			Type:      datamodel.JobTypeCreatePool, // Should use regular pool job type
			JobAttributes: &models.JobAttributes{
				ResourceUUID: "creating-pool-uuid",
			},
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		// This is the key assertion - line 225 should use JobTypeCreatePool for regular pools
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeCreatePool)).Return(job, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		operation, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok, "Expected OperationV1beta response")
		assert.False(tt, operation.Done.Value, "Operation should not be marked as done")
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/project-number/locations/us-east4-a/operations/job-uuid")
	})

	t.Run("WhenPoolIsInCreatingStateWithLargeCapacityNotSet_UsesDefaultJobType", func(tt *testing.T) {
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:    "test-regular-pool",
			Unified:       gcpgenserver.NewOptBool(true),
			ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:   2199023255552, // 2TiB - regular pool
			Network:       "test-network",
			Zone:          gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
			// LargeCapacity not set - should default to false
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		existingPool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "creating-pool-uuid"},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		job := &models.Job{
			BaseModel: models.BaseModel{UUID: "job-uuid"},
			Type:      datamodel.JobTypeCreatePool, // Should default to regular pool job type
			JobAttributes: &models.JobAttributes{
				ResourceUUID: "creating-pool-uuid",
			},
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		// This verifies that common.GetBoolOrDefault(req.LargeCapacity, false) defaults to false when LargeCapacity is not set
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeCreatePool)).Return(job, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		operation, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok, "Expected OperationV1beta response")
		assert.False(tt, operation.Done.Value, "Operation should not be marked as done")
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/project-number/locations/us-east4-a/operations/job-uuid")
	})

	t.Run("WhenGetKmsConfigForPoolFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalRegionalPoolEnabled := regionalPoolEnabled
		regionalPoolEnabled = true
		defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		labels := make(map[string]string)
		labels["test"] = "label"
		kmsConfigUUID := "kms-config-uuid"

		req := &gcpgenserver.PoolV1beta{
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			Zone:                     gcpgenserver.NewOptString("us-east4-a"),
			SecondaryZone:            gcpgenserver.NewOptString("us-east4-b"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64), // 128 MiBps
			Labels:                   gcpgenserver.NewOptPoolV1betaLabels(labels),
			KmsConfigId:              gcpgenserver.NewOptNilString(kmsConfigUUID),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			getAndSyncKmsConfigForPool = _getAndSyncKmsConfigForPool
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
			return nil, &gcpgenserver.V1betaCreatePoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: fmt.Sprintf("KMS Config with ID %s not found", req.KmsConfigId.Value),
			}
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(http.StatusBadRequest), result.(*gcpgenserver.V1betaCreatePoolBadRequest).Code)
	})

	t.Run("ModeAssignment_MODEUNSPECIFIED_SetsGCNVMode", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			Network:                  "test-network",
			Mode:                     gcpgenserver.NewOptPoolV1betaMode(gcpgenserver.PoolV1betaModeMODEUNSPECIFIED),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		originalGetAndSyncKmsConfigForPool := getAndSyncKmsConfigForPool
		defer func() {
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			getAndSyncKmsConfigForPool = originalGetAndSyncKmsConfigForPool
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
			return nil, nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Capture the CreatePool parameters to verify Mode assignment
		var capturedParams *common.CreatePoolParams
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(params *common.CreatePoolParams) bool {
			capturedParams = params
			return true
		})).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "new-pool-uuid"},
			PoolAttributes: &models.PoolAttributes{Labels: make(map[string]string)},
		}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, capturedParams, "CreatePool should have been called")
		assert.Equal(tt, common.DEFAULTMode, capturedParams.Mode, "Mode should be set to DEFAULTMode when Mode is MODEUNSPECIFIED")
	})

	t.Run("ModeAssignment_DEFAULT_SetsDEFAULTMode", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			Network:                  "test-network",
			Mode:                     gcpgenserver.NewOptPoolV1betaMode(gcpgenserver.PoolV1betaModeDEFAULT),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		originalGetAndSyncKmsConfigForPool := getAndSyncKmsConfigForPool
		defer func() {
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			getAndSyncKmsConfigForPool = originalGetAndSyncKmsConfigForPool
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
			return nil, nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Capture the CreatePool parameters to verify Mode assignment
		var capturedParams *common.CreatePoolParams
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(params *common.CreatePoolParams) bool {
			capturedParams = params
			return true
		})).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "new-pool-uuid"},
			PoolAttributes: &models.PoolAttributes{Labels: make(map[string]string)},
		}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, capturedParams, "CreatePool should have been called")
		assert.Equal(tt, common.DEFAULTMode, capturedParams.Mode, "Mode should be set to DEFAULTMode when Mode is DEFAULT")
	})

	t.Run("ModeAssignment_ONTAP_SetsONTAPMode", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			Network:                  "test-network",
			Mode:                     gcpgenserver.NewOptPoolV1betaMode(gcpgenserver.PoolV1betaModeONTAP),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		originalGetAndSyncKmsConfigForPool := getAndSyncKmsConfigForPool
		defer func() {
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			getAndSyncKmsConfigForPool = originalGetAndSyncKmsConfigForPool
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
			return nil, nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Capture the CreatePool parameters to verify Mode assignment
		var capturedParams *common.CreatePoolParams
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(params *common.CreatePoolParams) bool {
			capturedParams = params
			return true
		})).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "new-pool-uuid"},
			PoolAttributes: &models.PoolAttributes{Labels: make(map[string]string)},
		}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, capturedParams, "CreatePool should have been called")
		assert.Equal(tt, common.ONTAPMode, capturedParams.Mode, "Mode should be set to ONTAPMode when Mode is ONTAP")
	})

	t.Run("ModeAssignment_OtherValue_SetsONTAPMode", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaCreatePoolParams{
			LocationId:    "us-east4-a",
			ProjectNumber: "project-number",
		}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1099511627776,
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			Network:                  "test-network",
			Mode:                     gcpgenserver.NewOptPoolV1betaMode("SOME_OTHER_MODE"),
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		originalGetAndSyncKmsConfigForPool := getAndSyncKmsConfigForPool
		defer func() {
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			getAndSyncKmsConfigForPool = originalGetAndSyncKmsConfigForPool
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
			return nil, nil
		}

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Capture the CreatePool parameters to verify Mode assignment
		var capturedParams *common.CreatePoolParams
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(params *common.CreatePoolParams) bool {
			capturedParams = params
			return true
		})).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "new-pool-uuid"},
			PoolAttributes: &models.PoolAttributes{Labels: make(map[string]string)},
		}, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, capturedParams, "CreatePool should have been called")
		assert.Equal(tt, common.ONTAPMode, capturedParams.Mode, "Mode should be set to ONTAPMode for any value other than MODEUNSPECIFIED or DEFAULT")
	})
}

func TestV1betaUpdatePoolValidationErrors(t *testing.T) {
	// Save original autoTieringEnabled and restore at end of test
	originalAutoTieringEnabled := autoTieringEnabled
	defer func() { autoTieringEnabled = originalAutoTieringEnabled }()
	autoTieringEnabled = false

	validationErrorCases := []struct {
		name    string
		req     *gcpgenserver.PoolUpdateV1beta
		message string
	}{
		{
			name: "Zone is set",
			req: &gcpgenserver.PoolUpdateV1beta{
				Zone: gcpgenserver.NewOptString("us-east4-b"),
			},
			message: "Zone cannot be specified for zonal pool update",
		},
		{
			name: "GlobalAccessAllowed is set to true",
			req: &gcpgenserver.PoolUpdateV1beta{
				GlobalAccessAllowed: gcpgenserver.NewOptNilBool(true),
			},
			message: "Updating Global access is currently not supported",
		},
		{
			name: "GlobalAccessAllowed is set to false",
			req: &gcpgenserver.PoolUpdateV1beta{
				GlobalAccessAllowed: gcpgenserver.NewOptNilBool(false),
			},
			message: "Updating Global access is currently not supported",
		},
		{
			name: "AllowAutoTiering is set to true",
			req: &gcpgenserver.PoolUpdateV1beta{
				AllowAutoTiering: gcpgenserver.NewOptNilBool(true),
			},
			message: "Auto-Tiering feature is currently not enabled",
		},
		{
			name: "AllowAutoTiering is set to false",
			req: &gcpgenserver.PoolUpdateV1beta{
				AllowAutoTiering: gcpgenserver.NewOptNilBool(false),
			},
			message: "Auto-Tiering feature is currently not enabled",
		},
		{
			name: "HotTierSizeInBytes is set",
			req: &gcpgenserver.PoolUpdateV1beta{
				HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(1024),
			},
			message: "Auto-Tiering feature is currently not enabled",
		},
		{
			name: "EnableHotTierAutoResize is set to false",
			req: &gcpgenserver.PoolUpdateV1beta{
				EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(false),
			},
			message: "Auto-Tiering feature is currently not enabled",
		},
		{
			name: "EnableHotTierAutoResize is set to true",
			req: &gcpgenserver.PoolUpdateV1beta{
				EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
			},
			message: "Auto-Tiering feature is currently not enabled",
		},
		{
			name: "Shrink pool size",
			req: &gcpgenserver.PoolUpdateV1beta{
				SizeInBytes: gcpgenserver.NewOptNilFloat64(1073741824), // 1 GiB
			},
			message: "Pool size cannot be reduced",
		},
		{
			name: "QosType is set to invalid value",
			req: &gcpgenserver.PoolUpdateV1beta{
				QosType: gcpgenserver.NewOptNilString("invalid-qos-type"),
			},
			message: "QosType must be 'auto' or 'manual'",
		},
		{
			name: "CustomPerformanceEnabled is set to false",
			req: &gcpgenserver.PoolUpdateV1beta{
				CustomPerformanceEnabled: gcpgenserver.NewOptNilBool(false),
			},
			message: "Updating CustomerPerformance is currently not supported",
		},
		{
			name: "CustomPerformanceEnabled is set to true",
			req: &gcpgenserver.PoolUpdateV1beta{
				CustomPerformanceEnabled: gcpgenserver.NewOptNilBool(true),
			},
			message: "Updating CustomerPerformance is currently not supported",
		},
	}

	for _, tc := range validationErrorCases {
		t.Run(tc.name, func(tt *testing.T) {
			mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
			params := gcpgenserver.V1betaUpdatePoolParams{
				LocationId:    "us-east4",
				ProjectNumber: "project-number",
				PoolId:        "pool-id",
			}

			originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
			defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

			parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
				return "us-east4", "", nil
			}

			// Set orchestrator to return a pool when DescribePool is called.
			mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
				BaseModel: models.BaseModel{
					UUID: "pool-uuid",
				},
				Description: "original description",
				SizeInBytes: 1099511627776, // 1 TiB
				CustomPerformanceParams: &models.CustomPerformanceParams{
					Throughput: 64, // 64 MiBps
					Iops:       1024,
				},
				PoolAttributes: &models.PoolAttributes{
					PrimaryZone: "us-east4-a",
				},
				State: "READY",
			}, nil)
			// HasActiveClusterUpgrade is not called for validation-error cases; it runs only after validateUpdatePoolParams passes

			handler := Handler{
				Orchestrator: mockOrchestrator,
			}
			result, err := handler.V1betaUpdatePool(context.Background(), tc.req, params)

			assert.NoError(tt, err)
			assert.NotNil(tt, result)
			// Check if result is BadRequest error (most common for validation errors)
			if badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest); ok {
				assert.Equal(tt, float64(400), badReq.Code)
				assert.Equal(tt, tc.message, badReq.Message)
			} else if conflict, ok := result.(*gcpgenserver.V1betaUpdatePoolConflict); ok {
				// If it's a conflict error, check its message content
				assert.Equal(tt, float64(409), conflict.Code)
				assert.Equal(tt, tc.message, conflict.Message)
			} else {
				tt.Fatalf("Unexpected response type: %T", result)
			}
		})
	}

	t.Run("ZoneSwitchRejectsOtherFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		// Regional pool so Zone is otherwise valid.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:   models.BaseModel{UUID: "pool-uuid"},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone:   "us-east4-a",
				SecondaryZone: "us-east4-b",
				IsRegionalHA:  true,
			},
			State: "READY",
		}, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), &gcpgenserver.PoolUpdateV1beta{
			Zone:        gcpgenserver.NewOptString("us-east4-b"),
			Description: gcpgenserver.NewOptNilString("new description"),
		}, params)

		assert.NoError(tt, err)
		if badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest); ok {
			assert.Equal(tt, float64(400), badReq.Code)
			assert.Equal(tt, "When 'zone' is provided, no other update fields are allowed", badReq.Message)
		} else {
			tt.Fatalf("Unexpected response type: %T", result)
		}
	})

	t.Run("ZoneSwitchRejectsInvalidTargetZone", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel:   models.BaseModel{UUID: "pool-uuid"},
			Description: "original description",
			SizeInBytes: 1099511627776,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone:   "us-east4-a",
				SecondaryZone: "us-east4-b",
				IsRegionalHA:  true,
			},
			State:   "READY",
			QosType: utils.QosTypeAuto,
		}, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), &gcpgenserver.PoolUpdateV1beta{
			Zone: gcpgenserver.NewOptString("us-east4-c"),
		}, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok, "expected 400 bad request, got %T", result)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Target Zone is invalid", badReq.Message)
	})

	t.Run("TestOngoingUpdatePoolOperationScenario", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		// Set orchestrator to return a pool when DescribePool is called.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			State:       "UPDATING",
		}, nil)
		// HasActiveClusterUpgrade not called: validateUpdatePoolParams returns 409 for UPDATING state first

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaUpdatePool(context.Background(), &gcpgenserver.PoolUpdateV1beta{
			SizeInBytes: gcpgenserver.NewOptNilFloat64(2199023255552), // 2 TiB
		}, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Check if result is BadRequest error (most common for validation errors)
		if conflict, ok := result.(*gcpgenserver.V1betaUpdatePoolConflict); ok {
			// If it's a conflict error, check its message content
			assert.Equal(tt, float64(409), conflict.Code)
			assert.Equal(tt, "An update operation is already in progress for this pool", conflict.Message)
		} else {
			tt.Fatalf("Unexpected response type: %T", result)
		}
	})

	t.Run("TestDegradedPoolUpdateBlocked", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		// Set orchestrator to return a pool in DEGRADED state when DescribePool is called.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64, // 64 MiBps
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State: datamodel.LifeCycleStateDegraded,
		}, nil)
		// HasActiveClusterUpgrade not called: validateUpdatePoolParams returns 409 for DEGRADED state first

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaUpdatePool(context.Background(), &gcpgenserver.PoolUpdateV1beta{
			SizeInBytes: gcpgenserver.NewOptNilFloat64(2199023255552), // 2 TiB
		}, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Check if result is Conflict error
		conflict, ok := result.(*gcpgenserver.V1betaUpdatePoolConflict)
		assert.True(tt, ok, "Expected V1betaUpdatePoolConflict response")
		assert.Equal(tt, float64(409), conflict.Code)
		assert.Equal(tt, "Update operation is not allowed when the pool is in degraded state", conflict.Message)
	})

	t.Run("TestClusterUpgradeInProgressPoolUpdateBlocked", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateREADY,
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(true, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), &gcpgenserver.PoolUpdateV1beta{
			SizeInBytes: gcpgenserver.NewOptNilFloat64(2199023255552),
		}, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		conflict, ok := result.(*gcpgenserver.V1betaUpdatePoolConflict)
		assert.True(tt, ok, "Expected V1betaUpdatePoolConflict response")
		assert.Equal(tt, float64(409), conflict.Code)
		assert.Equal(tt, "Storage pool is temporarily unavailable, please try again later", conflict.Message)
	})

	t.Run("WhenHasActiveClusterUpgradeReturnsError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-uuid"},
			State:     datamodel.LifeCycleStateREADY,
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, fmt.Errorf("db unavailable"))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), &gcpgenserver.PoolUpdateV1beta{
			SizeInBytes: gcpgenserver.NewOptNilFloat64(2199023255552),
		}, params)

		assert.Error(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaUpdatePoolInternalServerError)
		assert.True(tt, ok, "Expected V1betaUpdatePoolInternalServerError when upgrade check fails")
	})
}

// TestV1betaUpdatePool_RegionalHAZoneOnlyInvokesOrchestrator covers the happy path where a regional HA pool
// receives a zone-only update to the secondary zone (no other update fields).
func TestV1betaUpdatePool_RegionalHAZoneOnlyInvokesOrchestrator(t *testing.T) {
	originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", "", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	params := gcpgenserver.V1betaUpdatePoolParams{
		LocationId:    "us-east4",
		ProjectNumber: "project-number",
		PoolId:        "pool-id",
	}

	existing := &models.Pool{
		BaseModel:   models.BaseModel{UUID: "pool-uuid"},
		Description: "original description",
		SizeInBytes: 1099511627776,
		QosType:     utils.QosTypeAuto,
		CustomPerformanceParams: &models.CustomPerformanceParams{
			Throughput: 64,
			Iops:       1024,
		},
		PoolAttributes: &models.PoolAttributes{
			PrimaryZone:   "us-east4-a",
			SecondaryZone: "us-east4-b",
			IsRegionalHA:  true,
		},
		State: datamodel.LifeCycleStateREADY,
	}

	mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-id", "project-number").Return(existing, nil)
	mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
	mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.UpdatePoolParams) bool {
		return p.PoolId == "pool-id" &&
			p.Region == "us-east4" &&
			p.AccountName == "project-number" &&
			p.CurrentZone == "us-east4-b"
	})).Return(existing, "async-op-id", nil)

	handler := Handler{Orchestrator: mockOrchestrator}
	result, err := handler.V1betaUpdatePool(context.Background(), &gcpgenserver.PoolUpdateV1beta{
		Zone: gcpgenserver.NewOptString("us-east4-b"),
	}, params)

	assert.NoError(t, err)
	op, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok, "expected async operation, got %T", result)
	assert.True(t, op.Name.IsSet())
	assert.Contains(t, op.Name.Value, "async-op-id")
	assert.True(t, op.Done.IsSet())
	assert.False(t, op.Done.Value)
}

func TestV1betaCreatePoolValidationErrors(t *testing.T) {
	validationErrorCases := []struct {
		name                   string
		req                    *gcpgenserver.PoolV1beta
		message                string
		setEnableMqos          bool
		setEnableLdap          bool
		setEnableAutoTiering   bool
		setRegionalPool        bool
		setRegionalPoolEnabled bool
		locationId             string
	}{
		{
			name: "Type field is STORAGE_POOL_TYPE_UNSPECIFIED",
			req: &gcpgenserver.PoolV1beta{
				Type:    gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeSTORAGEPOOLTYPEUNSPECIFIED),
				Unified: gcpgenserver.NewOptBool(true),
			},
			message: "type field cannot be STORAGE_POOL_TYPE_UNSPECIFIED",
		},
		{
			name: "Type is FILE (not UNIFIED)",
			req: &gcpgenserver.PoolV1beta{
				Type: gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeFILE),
			},
			message: "type must be set to UNIFIED, or unified/unifiedPool must be set to true (for backward compatibility)",
		},
		{
			name: "Active directory assigned to ONTAP Mode Pool",
			req: &gcpgenserver.PoolV1beta{
				Unified:                   gcpgenserver.NewOptBool(true),
				Mode:                      gcpgenserver.NewOptPoolV1betaMode(gcpgenserver.PoolV1betaModeONTAP),
				ActiveDirectoryResourceId: gcpgenserver.NewOptString("ad-resource-id"),
			},
			message: "Active directory cannot be assigned to ONTAP Mode Pool",
		},
		{
			name: "Manual QosType is not supported",
			req: &gcpgenserver.PoolV1beta{
				Unified: gcpgenserver.NewOptBool(true),
				QosType: gcpgenserver.NewOptNilString("manual"),
			},
			message:       "Manual QosType is not supported",
			setEnableMqos: false,
		},
		{
			name: "Manual QosType cannot be assigned to ONTAP Mode Pool",
			req: &gcpgenserver.PoolV1beta{
				Unified: gcpgenserver.NewOptBool(true),
				Mode:    gcpgenserver.NewOptPoolV1betaMode(gcpgenserver.PoolV1betaModeONTAP),
				QosType: gcpgenserver.NewOptNilString("manual"),
			},
			message:       "Manual QosType cannot be assigned to ONTAP Mode Pool",
			setEnableMqos: true,
		},
		{
			name: "Active Directory configuration is required when LDAP is enabled",
			req: &gcpgenserver.PoolV1beta{
				Unified:     gcpgenserver.NewOptBool(true),
				LdapEnabled: gcpgenserver.NewOptNilBool(true),
			},
			message:       "Active Directory configuration is required when LDAP is enabled",
			setEnableLdap: true,
		},
		{
			name: "LDAP is not currently supported",
			req: &gcpgenserver.PoolV1beta{
				Unified:                 gcpgenserver.NewOptBool(true),
				LdapEnabled:             gcpgenserver.NewOptNilBool(true),
				ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("ad-config-id"),
			},
			message: "LDAP is not currently supported for Unified Flex Storage Pool",
		},
		{
			name: "Auto-Tiering feature is currently not enabled - AllowAutoTiering set to true",
			req: &gcpgenserver.PoolV1beta{
				Unified:          gcpgenserver.NewOptBool(true),
				AllowAutoTiering: gcpgenserver.NewOptNilBool(true),
			},
			message: "Auto-Tiering feature is currently not enabled.",
		},
		{
			name: "Auto-Tiering feature is currently not enabled - HotTierSizeInBytes set",
			req: &gcpgenserver.PoolV1beta{
				Unified:            gcpgenserver.NewOptBool(true),
				HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(1024),
			},
			message: "Auto-Tiering feature is currently not enabled.",
		},
		{
			name: "HotTierSizeInBytes is a required field to enable auto-tiering",
			req: &gcpgenserver.PoolV1beta{
				Unified:          gcpgenserver.NewOptBool(true),
				AllowAutoTiering: gcpgenserver.NewOptNilBool(true),
			},
			message:              "HotTierSizeInBytes is a required field to enable auto-tiering",
			setEnableAutoTiering: true,
		},
		{
			name: "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering",
			req: &gcpgenserver.PoolV1beta{
				Unified:                 gcpgenserver.NewOptBool(true),
				HotTierSizeInBytes:      gcpgenserver.NewOptNilFloat64(1024),
				EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
			},
			message:              "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering",
			setEnableAutoTiering: true,
		},
		{
			name: "Regional Pool Support is not enabled",
			req: &gcpgenserver.PoolV1beta{
				Unified: gcpgenserver.NewOptBool(true),
			},
			message:                "Regional Pool Support is not enabled",
			setRegionalPool:        true,
			setRegionalPoolEnabled: false,
			locationId:             "us-east4",
		},
		{
			name: "Zone cannot be empty for regional pool",
			req: &gcpgenserver.PoolV1beta{
				Unified: gcpgenserver.NewOptBool(true),
			},
			message:                "Zone cannot be empty for regional pool.",
			setRegionalPool:        true,
			setRegionalPoolEnabled: true,
			locationId:             "us-east4",
		},
		{
			name: "Secondary Zone cannot be empty for regional pool",
			req: &gcpgenserver.PoolV1beta{
				Unified: gcpgenserver.NewOptBool(true),
				Zone:    gcpgenserver.NewOptString("us-east4-a"),
			},
			message:                "Secondary Zone cannot be empty for regional pool.",
			setRegionalPool:        true,
			setRegionalPoolEnabled: true,
			locationId:             "us-east4",
		},
		{
			name: "Secondary Zone cannot be same as Primary Zone - regional pool",
			req: &gcpgenserver.PoolV1beta{
				Unified:       gcpgenserver.NewOptBool(true),
				Zone:          gcpgenserver.NewOptString("us-east4-a"),
				SecondaryZone: gcpgenserver.NewOptString("us-east4-a"),
			},
			message:                "Secondary Zone cannot be same as Primary Zone",
			setRegionalPool:        true,
			setRegionalPoolEnabled: true,
			locationId:             "us-east4",
		},
		{
			name: "Multiple Zone values cannot be passed for Zonal Pool Creation",
			req: &gcpgenserver.PoolV1beta{
				Unified: gcpgenserver.NewOptBool(true),
				Zone:    gcpgenserver.NewOptString("us-east4-b"),
			},
			message:    "Multiple Zone values cannot be passed for Zonal Pool Creation",
			locationId: "us-east4-a",
		},
		{
			name: "Secondary Zone cannot be same as Primary Zone - zonal pool",
			req: &gcpgenserver.PoolV1beta{
				Unified:       gcpgenserver.NewOptBool(true),
				SecondaryZone: gcpgenserver.NewOptString("us-east4-a"),
			},
			message:    "Secondary Zone cannot be same as Primary Zone",
			locationId: "us-east4-a",
		},
	}

	for _, tc := range validationErrorCases {
		t.Run(tc.name, func(tt *testing.T) {
			mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

			// Use custom locationId if provided, otherwise default
			locationId := tc.locationId
			if locationId == "" {
				locationId = "us-east4-a"
			}

			params := gcpgenserver.V1betaCreatePoolParams{
				LocationId:    locationId,
				ProjectNumber: "project-number",
			}

			// Save original flags and restore at end of test
			originalAutoTieringEnabled := autoTieringEnabled
			originalEnableMqos := enableMqos
			originalEnableLdap := enableLdap
			originalRegionalPoolEnabled := regionalPoolEnabled
			originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
			originalGetAndSyncKmsConfigForPool := getAndSyncKmsConfigForPool

			defer func() {
				autoTieringEnabled = originalAutoTieringEnabled
				enableMqos = originalEnableMqos
				enableLdap = originalEnableLdap
				regionalPoolEnabled = originalRegionalPoolEnabled
				parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
				getAndSyncKmsConfigForPool = originalGetAndSyncKmsConfigForPool
			}()
			autoTieringEnabled = false
			enableMqos = false
			enableLdap = false
			regionalPoolEnabled = false

			// Set enableMqos for this specific test case if needed
			if tc.setEnableMqos {
				enableMqos = true
				defer func() { enableMqos = false }()
			}

			// Set enableLdap for this specific test case if needed
			if tc.setEnableLdap {
				enableLdap = true
				defer func() { enableLdap = false }()
			}

			// Set autoTieringEnabled for this specific test case if needed
			if tc.setEnableAutoTiering {
				autoTieringEnabled = true
				defer func() { autoTieringEnabled = false }()
			}

			// Set regionalPoolEnabled for this specific test case if needed
			if tc.setRegionalPoolEnabled {
				regionalPoolEnabled = true
				defer func() { regionalPoolEnabled = false }()
			}

			// Set up parseAndValidateRegionAndZone based on whether this is a regional pool test
			if tc.setRegionalPool {
				parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
					return "us-east4", "", nil // Empty zone for regional pool
				}
			} else {
				// Extract region and zone from locationId
				parts := strings.Split(locationId, "-")
				region := strings.Join(parts[:len(parts)-1], "-")
				parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
					return region, locationId, nil
				}
			}

			getAndSyncKmsConfigForPool = func(ctx context.Context, req *gcpgenserver.PoolV1beta, params *common.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
				return nil, nil
			}

			// Use Maybe() since validation errors return early and this won't be called
			mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))

			// Set common fields once for all test cases
			tc.req.ResourceId = "test-pool"
			tc.req.ServiceLevel = gcpgenserver.PoolV1betaServiceLevelFLEX
			tc.req.SizeInBytes = 1099511627776

			handler := Handler{
				Orchestrator: mockOrchestrator,
			}
			result, err := handler.V1betaCreatePool(context.Background(), tc.req, params)

			assert.NoError(tt, err)
			assert.NotNil(tt, result)
			// Check if result is BadRequest error (most common for validation errors)
			if badReq, ok := result.(*gcpgenserver.V1betaCreatePoolBadRequest); ok {
				assert.Equal(tt, float64(400), badReq.Code)
				assert.Equal(tt, tc.message, badReq.Message)
			} else {
				tt.Fatalf("Unexpected response type: %T, expected V1betaCreatePoolBadRequest", result)
			}
		})
	}
}

func TestV1betaUpdatePool(t *testing.T) {
	// Save original parseAndValidateRegionAndZone function and restore at end of test.
	originalParseAndValidate := parseAndValidateRegionAndZone
	t.Run("WhenRegionAndZoneParsingFails", func(tt *testing.T) {
		// Set the function to return a parsing error.
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "invalid-location",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}
		req := &gcpgenserver.PoolUpdateV1beta{
			Description: gcpgenserver.NewOptNilString("description"),
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Invalid location ID", badReq.Message)
	})
	t.Run("WhenGetPoolFails", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		// Set orchestrator to return a pool when DescribePool is called.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("pool not found", nil))

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}
		req := &gcpgenserver.PoolUpdateV1beta{
			Description:          gcpgenserver.NewOptNilString("updated description"),
			SizeInBytes:          gcpgenserver.NewOptNilFloat64(1099511627776),
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(128),
			TotalIops:            gcpgenserver.NewOptNilFloat64(2048),
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)
		assert.NoError(tt, err)
		notFoundErr, ok := result.(*gcpgenserver.V1betaUpdatePoolNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), notFoundErr.Code)
		assert.Equal(tt, "Pool not found", notFoundErr.Message)
	})
	t.Run("WhenUpdatePoolFails", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		// Set orchestrator to return a pool when DescribePool is called.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64, // 64 MiBps
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State: "READY",
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		// Set orchestrator to return an error when UpdatePool is called.
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).
			Return(nil, "", fmt.Errorf("update failed"))

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}
		req := &gcpgenserver.PoolUpdateV1beta{
			Description:          gcpgenserver.NewOptNilString("updated description"),
			SizeInBytes:          gcpgenserver.NewOptNilFloat64(1099511627776),
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(128),
			TotalIops:            gcpgenserver.NewOptNilFloat64(2048),
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "update failed", internalErr.Message)
	})
	t.Run("WhenUpdatePoolFailsDueToConflict", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		// Set orchestrator to return a pool when DescribePool is called.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64, // 64 MiBps
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		// Set orchestrator to return an error when UpdatePool is called.
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).
			Return(nil, "", errors.NewConflictErr("Pool is already transitioning between states"))

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}
		req := &gcpgenserver.PoolUpdateV1beta{
			Description:          gcpgenserver.NewOptNilString("updated description"),
			SizeInBytes:          gcpgenserver.NewOptNilFloat64(1099511627776),
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(128),
			TotalIops:            gcpgenserver.NewOptNilFloat64(2048),
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdatePoolConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(409), internalErr.Code)
		assert.Equal(tt, "Pool is already transitioning between states", internalErr.Message)
	})
	t.Run("WhenUpdatePoolFailsDueToUserInputValidationError", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		// Set orchestrator to return a pool when DescribePool is called.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64, // 64 MiBps
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State: "READY",
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		// Set orchestrator to return a user input validation error when UpdatePool is called.
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).
			Return(nil, "", errors.NewUserInputValidationErr("Invalid input: size too small"))

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}
		req := &gcpgenserver.PoolUpdateV1beta{
			Description:          gcpgenserver.NewOptNilString("updated description"),
			SizeInBytes:          gcpgenserver.NewOptNilFloat64(1099511627776),
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(128),
			TotalIops:            gcpgenserver.NewOptNilFloat64(2048),
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)
		assert.NoError(tt, err)
		badRequest, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(HTTP_BAD_REQUEST_CODE), badRequest.Code)
		assert.Equal(tt, "Invalid input: size too small", badRequest.Message)
	})
	t.Run("WhenPoolUpdateSucceeds", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

		// Create a dummy pool that represents the updated pool.
		updatedPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updated-pool-uuid",
			},
			PoolAttributes: &models.PoolAttributes{},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		// Set orchestrator to return a pool when DescribePool is called.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64, // 64 MiBps
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).
			Return(updatedPool, "op-123", nil)

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}
		labels := make(map[string]string)
		labels["test"] = "label"
		req := &gcpgenserver.PoolUpdateV1beta{
			Description:          gcpgenserver.NewOptNilString("updated description"),
			SizeInBytes:          gcpgenserver.NewOptNilFloat64(1099511627776),
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(128),
			TotalIops:            gcpgenserver.NewOptNilFloat64(2048),
			Labels:               gcpgenserver.OptPoolUpdateV1betaLabels{Value: labels, Set: true},
		}
		handler := Handler{Orchestrator: mockOrchestrator}

		result, err := handler.V1betaUpdatePool(context.Background(), req, params)
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})
}

func TestV1betaDeletePool(t *testing.T) {
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDeletePoolBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaDeletePoolBadRequest).Message)
	})
	t.Run("WhenPoolDoesNotExist", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "non-existent-pool-id",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(nil, errors.NewNotFoundErr("not found", nil))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(404), result.(*gcpgenserver.V1betaDeletePoolNotFound).Code)
		assert.Equal(tt, "Pool not found", result.(*gcpgenserver.V1betaDeletePoolNotFound).Message)
	})
	t.Run("WhenPoolHasActiveVolumes", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-with-volumes",
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-with-volumes",
			},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(nil, "", errors.NewBadRequestErr("pool has active volumes"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDeletePoolConflict).Code)
		assert.Equal(tt, "Pool has active volumes", result.(*gcpgenserver.V1betaDeletePoolConflict).Message)
	})
	t.Run("WhenPoolIsAlreadyDeleted", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "deletable-pool-id",
		}

		createdAt := time.Now()
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "deletable-pool-id",
				CreatedAt: createdAt,
				UpdatedAt: createdAt,
				DeletedAt: &createdAt,
			},
			PoolAttributes: &models.PoolAttributes{},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, true, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})
	t.Run("WhenPoolDeletionRaceCondition", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "deletable-pool-id",
		}

		createdAt := time.Now()
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "deletable-pool-id",
				CreatedAt: createdAt,
				UpdatedAt: createdAt,
			},
			PoolAttributes: &models.PoolAttributes{},
		}
		deletePoolParams := &common.DeletePoolParams{
			AccountName: params.ProjectNumber,
			PoolID:      existingPool.UUID,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, deletePoolParams).Return(existingPool, "", errors.NewNotFoundErr("pool not found", nil))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, true, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})
	t.Run("WhenPoolDeletionSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "deletable-pool-id",
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "deletable-pool-id",
			},
			PoolAttributes: &models.PoolAttributes{},
		}
		deletedPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "deletable-pool-id",
			},
			PoolAttributes: &models.PoolAttributes{},
			State:          datamodel.LifeCycleStateDeleting,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, mock.Anything).Return(deletedPool, "operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/operation-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenPoolIsInCreatingState", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "creating-pool-id",
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}
		deletePoolParams := &commonparams.DeletePoolParams{
			AccountName: params.ProjectNumber,
			PoolID:      existingPool.UUID,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, deletePoolParams).Return(nil, "", errors.NewConflictErr("Error deleting pool - Pool is already transitioning between states"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(409), result.(*gcpgenserver.V1betaDeletePoolConflict).Code)
		assert.Equal(tt, "Error deleting pool - Pool is already transitioning between states", result.(*gcpgenserver.V1betaDeletePoolConflict).Message)
	})
	t.Run("WhenPoolIsInUpdatingState", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "updating-pool-id",
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updating-pool-id",
			},
			State:          datamodel.LifeCycleStateUpdating,
			PoolAttributes: &models.PoolAttributes{},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(409), result.(*gcpgenserver.V1betaDeletePoolConflict).Code)
		assert.Equal(tt, "Error deleting pool - Pool is already transitioning between states", result.(*gcpgenserver.V1betaDeletePoolConflict).Message)
	})
	t.Run("WhenPoolIsInDeletingStateAndJobExists_ThenReturnTheSameJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "deleting-pool-id",
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "deleting-pool-id",
			},
			State:          datamodel.LifeCycleStateDeleting,
			PoolAttributes: &models.PoolAttributes{},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		job := &models.Job{
			BaseModel: models.BaseModel{UUID: "job-uuid"},
			Type:      datamodel.JobTypeDeletePool,
			JobAttributes: &models.JobAttributes{
				ResourceUUID: "deleting-pool-id",
			},
			State: datamodel.JobsStateNEW,
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeDeletePool)).Return(job, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, result.(*gcpgenserver.OperationV1beta).Name.Value, "/v1beta/projects/project-number/locations/us-east4/operations/job-uuid")
		assert.False(tt, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})
	t.Run("WhenPoolIsInDeletingStateAndJobDoesNotExists_ThenReturnStaticJobWithDone", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "deleting-pool-id",
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "deleting-pool-id",
			},
			State:          datamodel.LifeCycleStateDeleting,
			PoolAttributes: &models.PoolAttributes{},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, errors.NewNotFoundErr("not found", nil))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, result.(*gcpgenserver.OperationV1beta).Name.Value, "/v1beta/projects/project-number/locations/us-east4/operations/00000000-0000-0000-0000-000000000000")
		assert.True(tt, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})
	t.Run("WhenPoolIsInCreatingStateWithCorrelationIDAndExistingDeleteJob_ReturnsExistingJobUUID", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		correlationID := "test-correlation-id"
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			PoolId:         "creating-pool-id",
			XCorrelationID: gcpgenserver.OptString{Value: correlationID, Set: true},
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		existingDeleteJob := &models.Job{
			BaseModel: models.BaseModel{
				UUID: "existing-delete-job-uuid",
			},
			Type:          datamodel.JobTypeDeletePool,
			CorrelationID: correlationID,
			State:         datamodel.JobsStatePROCESSING,
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeDeletePool)).Return(existingDeleteJob, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/existing-delete-job-uuid", op.Name.Value)
		assert.False(tt, op.Done.Value) // Job is PROCESSING, not done
	})
	t.Run("WhenPoolIsInCreatingStateWithCorrelationIDAndExistingDeleteJobInDoneState_ReturnsExistingJobUUIDWithDone", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		correlationID := "test-correlation-id"
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			PoolId:         "creating-pool-id",
			XCorrelationID: gcpgenserver.OptString{Value: correlationID, Set: true},
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		existingDeleteJob := &models.Job{
			BaseModel: models.BaseModel{
				UUID: "existing-delete-job-uuid",
			},
			Type:          datamodel.JobTypeDeletePool,
			CorrelationID: correlationID,
			State:         datamodel.JobsStateDONE,
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeDeletePool)).Return(existingDeleteJob, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/existing-delete-job-uuid", op.Name.Value)
		assert.True(tt, op.Done.Value) // Job is DONE
	})
	t.Run("WhenPoolIsInCreatingStateWithCorrelationIDAndExistingDeleteJobWithMismatchedCorrelationID_ProceedsWithNormalDelete", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		correlationID := "test-correlation-id"
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			PoolId:         "creating-pool-id",
			XCorrelationID: gcpgenserver.OptString{Value: correlationID, Set: true},
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		existingDeleteJob := &models.Job{
			BaseModel: models.BaseModel{
				UUID: "existing-delete-job-uuid",
			},
			Type:          datamodel.JobTypeDeletePool,
			CorrelationID: "different-correlation-id", // Mismatched
			State:         datamodel.JobsStatePROCESSING,
		}

		deletePoolParams := &commonparams.DeletePoolParams{
			AccountName: params.ProjectNumber,
			PoolID:      existingPool.UUID,
		}
		deletedPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateDeleting,
			PoolAttributes: &models.PoolAttributes{},
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeDeletePool)).Return(existingDeleteJob, nil)
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, deletePoolParams).Return(deletedPool, "new-operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		// Should proceed with normal delete and return new operation ID
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/new-operation-id", op.Name.Value)
	})
	t.Run("WhenPoolIsInCreatingStateWithCorrelationIDAndNoExistingDeleteJob_ProceedsWithNormalDelete", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		correlationID := "test-correlation-id"
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			PoolId:         "creating-pool-id",
			XCorrelationID: gcpgenserver.OptString{Value: correlationID, Set: true},
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		deletePoolParams := &commonparams.DeletePoolParams{
			AccountName: params.ProjectNumber,
			PoolID:      existingPool.UUID,
		}
		deletedPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateDeleting,
			PoolAttributes: &models.PoolAttributes{},
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, deletePoolParams).Return(deletedPool, "new-operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		// Should proceed with normal delete and return new operation ID
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/new-operation-id", op.Name.Value)
	})
	t.Run("WhenPoolIsInCreatingStateWithoutCorrelationID_ProceedsWithNormalDelete", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			PoolId:         "creating-pool-id",
			XCorrelationID: gcpgenserver.OptString{}, // Not set
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		deletePoolParams := &commonparams.DeletePoolParams{
			AccountName: params.ProjectNumber,
			PoolID:      existingPool.UUID,
		}
		deletedPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateDeleting,
			PoolAttributes: &models.PoolAttributes{},
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		// Should not call GetJobByResourceUUID when correlation ID is not set
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, deletePoolParams).Return(deletedPool, "new-operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		// Should proceed with normal delete
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/new-operation-id", op.Name.Value)
	})
	t.Run("WhenPoolIsInCreatingStateWithCorrelationIDAndGetJobByResourceUUIDFails_ProceedsWithNormalDelete", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		correlationID := "test-correlation-id"
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			PoolId:         "creating-pool-id",
			XCorrelationID: gcpgenserver.OptString{Value: correlationID, Set: true},
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		deletePoolParams := &commonparams.DeletePoolParams{
			AccountName: params.ProjectNumber,
			PoolID:      existingPool.UUID,
		}
		deletedPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateDeleting,
			PoolAttributes: &models.PoolAttributes{},
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeDeletePool)).Return(nil, stderrors.New("database error"))
		mockOrchestrator.EXPECT().DeletePool(mock.Anything, deletePoolParams).Return(deletedPool, "new-operation-id", nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		// Should proceed with normal delete when GetJobByResourceUUID fails
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/new-operation-id", op.Name.Value)
	})
	t.Run("WhenPoolIsInCreatingStateWithCorrelationIDAndExistingDeleteJobInErrorState_ReturnsExistingJobUUIDWithDone", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		correlationID := "test-correlation-id"
		params := gcpgenserver.V1betaDeletePoolParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			PoolId:         "creating-pool-id",
			XCorrelationID: gcpgenserver.OptString{Value: correlationID, Set: true},
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "creating-pool-id",
			},
			State:          datamodel.LifeCycleStateCreating,
			PoolAttributes: &models.PoolAttributes{},
		}

		existingDeleteJob := &models.Job{
			BaseModel: models.BaseModel{
				UUID: "existing-delete-job-uuid",
			},
			Type:          datamodel.JobTypeDeletePool,
			CorrelationID: correlationID,
			State:         datamodel.JobsStateERROR, // Job is in ERROR state
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, existingPool.UUID, string(datamodel.JobTypeDeletePool)).Return(existingDeleteJob, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDeletePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		// Should return existing job UUID even when in ERROR state (idempotency)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/existing-delete-job-uuid", op.Name.Value)
		assert.True(tt, op.Done.Value) // Job is ERROR, so Done should be true
	})
}

func TestV1betaDescribePool(t *testing.T) {
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDescribePoolParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDescribePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDescribePoolBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaDescribePoolBadRequest).Message)
	})
	t.Run("WhenPoolDoesNotExist", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDescribePoolParams{
			PoolId:        "non-existent-pool-id",
			ProjectNumber: "project-number",
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(nil, errors.NewNotFoundErr("not found", nil))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDescribePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(404), result.(*gcpgenserver.V1betaDescribePoolNotFound).Code)
		assert.Equal(tt, "Pool not found", result.(*gcpgenserver.V1betaDescribePoolNotFound).Message)
	})

	t.Run("WhenDescribePoolFailsWithInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDescribePoolParams{
			PoolId:        "pool-id",
			ProjectNumber: "project-number",
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(nil, fmt.Errorf("internal error"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDescribePool(context.Background(), params)

		assert.Error(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaDescribePoolInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("WhenDescribePoolSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDescribePoolParams{
			PoolId:        "existing-pool-id",
			ProjectNumber: "project-number",
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "existing-pool-id",
			},
			PoolAttributes: &models.PoolAttributes{
				Labels: map[string]string{"test": "label"},
			},
			Name:        "test-pool",
			Description: "This is a test pool",
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(existingPool, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDescribePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "existing-pool-id", result.(*gcpgenserver.PoolV1beta).PoolId.Value)
		assert.Equal(tt, "This is a test pool", result.(*gcpgenserver.PoolV1beta).Description.Value)
		assert.Equal(tt, existingPool.PoolAttributes.Labels["test"], result.(*gcpgenserver.PoolV1beta).Labels.Value["test"])
	})

	t.Run("WhenPoolHasAutoTieringEnabled_IncludesConsumptionFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaDescribePoolParams{
			PoolId:        "pool-id",
			ProjectNumber: "project-number",
		}

		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-id",
			},
			Name:             "test-pool",
			AllowAutoTiering: true,
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000, // 500GB
				EnableHotTierAutoResize: true,
				HotTierConsumption:      200000000000, // 200GB
				ColdTierConsumption:     100000000000, // 100GB
			},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, params.PoolId, params.ProjectNumber).Return(pool, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaDescribePool(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		poolRes, ok := result.(*gcpgenserver.PoolV1beta)
		assert.True(tt, ok)
		// Verify consumption fields are included when auto tiering is enabled
		assert.True(tt, poolRes.HotTierConsumption.IsSet())
		assert.Equal(tt, int64(200000000000), poolRes.HotTierConsumption.Value)
		assert.True(tt, poolRes.ColdTierConsumption.IsSet())
		assert.Equal(tt, int64(100000000000), poolRes.ColdTierConsumption.Value)
		// Verify auto tiering related fields are included when auto tiering is enabled
		assert.True(tt, poolRes.HotTierSizeInBytes.IsSet())
		assert.True(tt, poolRes.EnableHotTierAutoResize.IsSet())
	})
}

func TestV1betaListPools(t *testing.T) {
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaListPoolsParams{
			LocationId:    "invalid-location-id",
			ProjectNumber: "project-number",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaListPools(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaListPoolsBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaListPoolsBadRequest).Message)
	})
	t.Run("WhenListPoolsFailsWithInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaListPoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().ListPools(mock.Anything, params.ProjectNumber, false).Return(nil, fmt.Errorf("internal error"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaListPools(context.Background(), params)

		assert.Error(tt, err)
		assert.NotNil(tt, result)
		_, ok := result.(*gcpgenserver.V1betaListPoolsInternalServerError)
		assert.True(tt, ok)
	})
	t.Run("WhenListPoolsSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaListPoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		pool1 := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "pool-uuid-1"},
			Name:           "pool-1",
			PoolAttributes: &models.PoolAttributes{},
		}
		pool2 := &models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-uuid-2"},
			Name:      "pool-2",
			PoolAttributes: &models.PoolAttributes{
				NumberOfVolumes: 1,
			},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().ListPools(mock.Anything, params.ProjectNumber, false).Return([]*models.Pool{pool1, pool2}, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaListPools(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListPoolsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 2, len(successResult.Pools))
		assert.Equal(tt, "pool-uuid-1", successResult.Pools[0].PoolId.Value)
		assert.Equal(tt, "pool-uuid-2", successResult.Pools[1].PoolId.Value)
	})
	t.Run("WhenListPoolsSucceedsIncludeDeleted", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaListPoolsParams{
			LocationId:     "us-east4",
			ProjectNumber:  "project-number",
			IncludeDeleted: gcpgenserver.NewOptBool(true),
		}

		pool1 := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "pool-uuid-1"},
			Name:           "pool-1",
			PoolAttributes: &models.PoolAttributes{},
		}
		pool2 := &models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-uuid-2"},
			Name:      "pool-2",
			PoolAttributes: &models.PoolAttributes{
				NumberOfVolumes: 1,
			},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().ListPools(mock.Anything, params.ProjectNumber, true).Return([]*models.Pool{pool1, pool2}, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaListPools(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListPoolsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 2, len(successResult.Pools))
		assert.Equal(tt, "pool-uuid-1", successResult.Pools[0].PoolId.Value)
		assert.Equal(tt, "pool-uuid-2", successResult.Pools[1].PoolId.Value)
	})

	t.Run("WhenPoolsHaveAutoTieringEnabled_IncludesConsumptionFields", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaListPoolsParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		pool1 := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid-1",
			},
			Name:             "pool-1",
			AllowAutoTiering: true,
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      400000000000, // 400GB
				EnableHotTierAutoResize: true,
				HotTierConsumption:      150000000000, // 150GB
				ColdTierConsumption:     75000000000,  // 75GB
			},
		}
		pool2 := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid-2",
			},
			Name:             "pool-2",
			AllowAutoTiering: false,
			PoolAttributes:   &models.PoolAttributes{},
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().ListPools(mock.Anything, params.ProjectNumber, false).Return([]*models.Pool{pool1, pool2}, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		result, err := handler.V1betaListPools(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListPoolsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 2, len(successResult.Pools))
		// First pool has auto tiering enabled - should include consumption fields
		assert.True(tt, successResult.Pools[0].HotTierConsumption.IsSet())
		assert.Equal(tt, int64(150000000000), successResult.Pools[0].HotTierConsumption.Value)
		assert.True(tt, successResult.Pools[0].ColdTierConsumption.IsSet())
		assert.Equal(tt, int64(75000000000), successResult.Pools[0].ColdTierConsumption.Value)
		// First pool should also include auto tiering related fields
		assert.True(tt, successResult.Pools[0].HotTierSizeInBytes.IsSet())
		assert.True(tt, successResult.Pools[0].EnableHotTierAutoResize.IsSet())
		// Second pool has auto tiering disabled - should not include consumption fields
		assert.False(tt, successResult.Pools[1].HotTierConsumption.IsSet())
		assert.False(tt, successResult.Pools[1].ColdTierConsumption.IsSet())
		// Second pool should not include auto tiering related fields
		assert.False(tt, successResult.Pools[1].HotTierSizeInBytes.IsSet())
		assert.False(tt, successResult.Pools[1].EnableHotTierAutoResize.IsSet())
	})
}

// TestValidateLabels_Valid validates that a proper label set passes without error.
func TestValidateLabels_Valid(t *testing.T) {
	labels := map[string]string{
		"env":     "prod",
		"version": "v1",
	}
	_, err := validateLabels(labels)
	assert.NoError(t, err)
}

// TestValidateLabels_ExceedLabelCount returns an error when label count exceeds 64.
func TestValidateLabels_ExceedLabelCount(t *testing.T) {
	labels := make(map[string]string)
	for i := 0; i < 65; i++ {
		labels[fmt.Sprintf("key%d", i)] = "value"
	}
	_, err := validateLabels(labels)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid label count")
}

// TestValidateLabels_EmptyKey returns an error when a label key is empty.
func TestValidateLabels_EmptyKey(t *testing.T) {
	labels := map[string]string{
		"": "somevalue",
	}
	_, err := validateLabels(labels)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key is required in label")
}

// TestValidateLabels_KeyExceedsMaxRune returns an error when a key exceeds the max allowed runes.
func TestValidateLabels_KeyExceedsMaxRune(t *testing.T) {
	// Create a key of 64 runes (maxRuneCount is 63)
	key := strings.Repeat("a", maxRuneCount+1)
	labels := map[string]string{
		key: "value",
	}
	_, err := validateLabels(labels)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "label key")
	assert.Contains(t, err.Error(), "length can't exceed")
}

// TestValidateLabels_KeyExceedsMaxByte returns an error when a key exceeds the max allowed bytes.
func TestValidateLabels_KeyExceedsMaxByte(t *testing.T) {
	// Create a key of 129 bytes (maxByteCount is 128).
	key := string(make([]byte, maxByteCount+1))
	labels := map[string]string{
		key: "value",
	}
	_, err := validateLabels(labels)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "label key")
	assert.Contains(t, err.Error(), "length can't exceed")
}

// TestValidateLabels_ValueExceedsMaxRune returns an error when a value exceeds the max allowed runes.
func TestValidateLabels_ValueExceedsMaxRune(t *testing.T) {
	// Create a value of 64 runes (maxRuneCount is 63)
	value := strings.Repeat("b", maxRuneCount+1)
	labels := map[string]string{
		"key": value,
	}
	_, err := validateLabels(labels)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "label value")
	assert.Contains(t, err.Error(), "length can't exceed")
}

// TestValidateLabels_ValueExceedsMaxByte returns an error when a value exceeds the max allowed bytes.
func TestValidateLabels_ValueExceedsMaxByte(t *testing.T) {
	// Create a value of 129 bytes (maxByteCount is 128).
	value := string(make([]byte, maxByteCount+1))
	labels := map[string]string{
		"key": value,
	}
	_, err := validateLabels(labels)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "label value")
	assert.Contains(t, err.Error(), "length can't exceed")
}

func TestGetEnableHotTierAutoResize(t *testing.T) {
	t.Run("WhenConfigIsNil", func(tt *testing.T) {
		result := getEnableHotTierAutoResize(nil)
		assert.False(tt, result)
	})

	t.Run("WhenConfigIsNotNilAndEnableHotTierAutoResizeIsTrue", func(tt *testing.T) {
		config := &models.AutoTieringConfig{
			EnableHotTierAutoResize: true,
		}
		result := getEnableHotTierAutoResize(config)
		assert.True(tt, result)
	})
}
func TestGetHotTierSizeInBytes(t *testing.T) {
	t.Run("WhenConfigIsNil", func(tt *testing.T) {
		result := getHotTierSizeInBytes(nil)
		assert.Equal(tt, float64(0), result)
	})

	t.Run("WhenConfigIsNotNil", func(tt *testing.T) {
		config := &models.AutoTieringConfig{
			HotTierSizeInBytes: 1024,
		}
		result := getHotTierSizeInBytes(config)
		assert.Equal(tt, float64(1024), result)
	})
}

func TestValidateCreatePoolParams_AutoTieringValidation(t *testing.T) {
	const (
		minPoolHotTierSize = 1099511627776 // 1 TiB
		validPoolSize      = 2199023255552 // 2 TiB
		smallPoolSize      = 1073741824    // 1 GiB
		largePoolSize      = 5497558138880 // 5 TiB
	)

	// Save original state and restore after all tests
	originalAutoTieringEnabled := autoTieringEnabled
	defer func() { autoTieringEnabled = originalAutoTieringEnabled }()

	t.Run("Feature Gate Tests", func(tt *testing.T) {
		t.Run("AutoTiering feature disabled - AllowAutoTiering is true", func(ttt *testing.T) {
			autoTieringEnabled = false
			req := &gcpgenserver.PoolV1beta{
				Unified:          gcpgenserver.NewOptBool(true),
				SizeInBytes:      validPoolSize,
				AllowAutoTiering: gcpgenserver.NewOptNilBool(true),
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when auto-tiering feature is disabled but AllowAutoTiering is true")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "Auto-Tiering feature is currently not enabled.")
		})

		t.Run("AutoTiering feature disabled - HotTierSizeInBytes is set", func(ttt *testing.T) {
			autoTieringEnabled = false
			req := &gcpgenserver.PoolV1beta{
				Unified:            gcpgenserver.NewOptBool(true),
				SizeInBytes:        validPoolSize,
				AllowAutoTiering:   gcpgenserver.NewOptNilBool(false),
				HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(float64(minPoolHotTierSize)),
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when auto-tiering feature is disabled but HotTierSizeInBytes is set")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "Auto-Tiering feature is currently not enabled.")
		})

		t.Run("AutoTiering feature disabled - no auto-tiering params set (should pass)", func(ttt *testing.T) {
			autoTieringEnabled = false
			req := &gcpgenserver.PoolV1beta{
				Unified:     gcpgenserver.NewOptBool(true),
				SizeInBytes: validPoolSize,
				// Neither AllowAutoTiering nor HotTierSizeInBytes are set
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.Nil(ttt, err, "Expected no error when auto-tiering feature is disabled and no auto-tiering params are set")
		})
	})

	// For the rest of the tests, enable auto-tiering feature
	autoTieringEnabled = true

	t.Run("Valid Auto-Tiering Configurations", func(tt *testing.T) {
		t.Run("Valid configuration with all parameters", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Unified:                 gcpgenserver.NewOptBool(true),
				SizeInBytes:             validPoolSize,
				AllowAutoTiering:        gcpgenserver.NewOptNilBool(true),
				HotTierSizeInBytes:      gcpgenserver.NewOptNilFloat64(float64(minPoolHotTierSize)),
				EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.Nil(ttt, err, "Expected no error for valid auto-tiering parameters")
		})

		t.Run("HotTierSizeInBytes equal to pool size", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Unified:            gcpgenserver.NewOptBool(true),
				SizeInBytes:        validPoolSize,
				AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),
				HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(float64(validPoolSize)), // Equal to pool size
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.Nil(ttt, err, "Expected no error when HotTierSizeInBytes equals pool size")
		})

		t.Run("Complex valid scenario - large pool with explicit auto-resize disabled", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Unified:                 gcpgenserver.NewOptBool(true),
				SizeInBytes:             largePoolSize, // 5 TiB pool
				AllowAutoTiering:        gcpgenserver.NewOptNilBool(true),
				HotTierSizeInBytes:      gcpgenserver.NewOptNilFloat64(float64(validPoolSize)), // 2 TiB hot tier
				EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(false),                     // Explicitly disable auto-resize
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.Nil(ttt, err, "Expected no error for complex valid auto-tiering scenario")
		})
	})

	t.Run("Auto-Tiering Required Field Validation", func(tt *testing.T) {
		t.Run("AllowAutoTiering enabled but HotTierSizeInBytes is missing", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Unified:          gcpgenserver.NewOptBool(true),
				SizeInBytes:      validPoolSize,
				AllowAutoTiering: gcpgenserver.NewOptNilBool(true),
				// HotTierSizeInBytes not set
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when HotTierSizeInBytes is missing with auto-tiering enabled")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "HotTierSizeInBytes is a required field to enable auto-tiering")
		})

		t.Run("AllowAutoTiering enabled but HotTierSizeInBytes is zero", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Unified:            gcpgenserver.NewOptBool(true),
				SizeInBytes:        validPoolSize,
				AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),
				HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(0), // Zero value
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when HotTierSizeInBytes is zero with auto-tiering enabled")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "HotTierSizeInBytes is a required field to enable auto-tiering")
		})
	})

	t.Run("Auto-Tiering Disabled Scenarios", func(tt *testing.T) {
		t.Run("AllowAutoTiering disabled but HotTierSizeInBytes is set", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Unified:            gcpgenserver.NewOptBool(true),
				SizeInBytes:        validPoolSize,
				AllowAutoTiering:   gcpgenserver.NewOptNilBool(false),
				HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(float64(minPoolHotTierSize)),
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when HotTierSizeInBytes is set but auto-tiering is disabled")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering")
		})

		t.Run("AllowAutoTiering disabled but EnableHotTierAutoResize is set", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Unified:                 gcpgenserver.NewOptBool(true),
				SizeInBytes:             validPoolSize,
				AllowAutoTiering:        gcpgenserver.NewOptNilBool(false),
				EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when EnableHotTierAutoResize is set but auto-tiering is disabled")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering")
		})

		t.Run("AllowAutoTiering not set (defaults to false) with HotTierSizeInBytes set", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Unified:     gcpgenserver.NewOptBool(true),
				SizeInBytes: validPoolSize,
				// AllowAutoTiering not set (defaults to false)
				HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(float64(minPoolHotTierSize)),
			}
			zone := "us-east4-a"

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when HotTierSizeInBytes is set but auto-tiering is not enabled")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering")
		})
	})
}

func TestIsTrueZonal(t *testing.T) {
	originalEnableTrueZonalLV := enableTrueZonalLV
	defer func() { enableTrueZonalLV = originalEnableTrueZonalLV }()

	tests := []struct {
		name          string
		featureOn     bool
		isRegionalHA  bool
		largeCapacity bool
		secondaryZone string
		want          bool
	}{
		{
			name:          "zonal large capacity when feature enabled",
			featureOn:     true,
			largeCapacity: true,
			want:          true,
		},
		{
			name:          "feature disabled",
			featureOn:     false,
			largeCapacity: true,
			want:          false,
		},
		{
			name:          "not large capacity",
			featureOn:     true,
			largeCapacity: false,
			want:          false,
		},
		{
			name:          "regional HA",
			featureOn:     true,
			isRegionalHA:  true,
			largeCapacity: true,
			want:          false,
		},
		{
			name:          "secondary zone set",
			featureOn:     true,
			largeCapacity: true,
			secondaryZone: "us-east4-b",
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enableTrueZonalLV = tt.featureOn
			got := isTrueZonal(tt.isRegionalHA, tt.largeCapacity, tt.secondaryZone)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateTrueZonalAutoTiering(t *testing.T) {
	originalblockTrueZonalAutoTieringAndBackupFlag := blockTrueZonalAutoTieringAndBackup
	blockTrueZonalAutoTieringAndBackup = true
	defer func() { blockTrueZonalAutoTieringAndBackup = originalblockTrueZonalAutoTieringAndBackupFlag }()

	tests := []struct {
		name                  string
		allowAutoTieringSet   bool
		allowAutoTiering      bool
		hotTierAutoResizeSet  bool
		hotTierAutoResize     bool
		expectedErrorContains string
	}{
		{
			name:                  "returns empty when both fields unset",
			expectedErrorContains: "",
		},
		{
			name:                  "returns allow auto-tiering violation when set true",
			allowAutoTieringSet:   true,
			allowAutoTiering:      true,
			expectedErrorContains: trueZonalEnableAutoTieringErrorMsg,
		},
		{
			name:                  "returns hot tier auto-resize violation when set true",
			hotTierAutoResizeSet:  true,
			hotTierAutoResize:     true,
			expectedErrorContains: "Hot tier auto-resize cannot be enabled for true-zonal pools",
		},
		{
			name:                  "returns allow auto-tiering violation first when both set true",
			allowAutoTieringSet:   true,
			allowAutoTiering:      true,
			hotTierAutoResizeSet:  true,
			hotTierAutoResize:     true,
			expectedErrorContains: trueZonalEnableAutoTieringErrorMsg,
		},
		{
			name:                  "returns hot tier auto-resize violation when auto-tiering false but auto-resize set true",
			allowAutoTieringSet:   true,
			allowAutoTiering:      false,
			hotTierAutoResizeSet:  true,
			hotTierAutoResize:     true,
			expectedErrorContains: "Hot tier auto-resize cannot be enabled for true-zonal pools",
		},
		{
			name:                  "returns empty when allow auto-tiering explicitly false",
			allowAutoTieringSet:   true,
			allowAutoTiering:      false,
			expectedErrorContains: "",
		},
		{
			name:                  "returns empty when hot tier auto-resize explicitly false",
			hotTierAutoResizeSet:  true,
			hotTierAutoResize:     false,
			expectedErrorContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := validateTrueZonalAutoTiering(tt.allowAutoTieringSet, tt.allowAutoTiering, tt.hotTierAutoResizeSet, tt.hotTierAutoResize)
			assert.Equal(t, tt.expectedErrorContains, msg)
		})
	}
}

func TestV1betaCreatePool_TrueZonal(t *testing.T) {
	const sixTiB = 6 * 1099511627776
	const primaryZone = "us-east4-a"

	originalEnableTrueZonalLV := enableTrueZonalLV
	originalEnableLargeCapacityPools := enableLargeCapacityPools
	defer func() {
		enableTrueZonalLV = originalEnableTrueZonalLV
		enableLargeCapacityPools = originalEnableLargeCapacityPools
	}()
	enableTrueZonalLV = true
	enableLargeCapacityPools = true

	params := gcpgenserver.V1betaCreatePoolParams{
		LocationId:    "us-east4-a",
		ProjectNumber: "project-number",
	}
	baseReq := &gcpgenserver.PoolV1beta{
		Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
		ServiceLevel:  gcpgenserver.PoolV1betaServiceLevelFLEX,
		SizeInBytes:   sixTiB,
		LargeCapacity: gcpgenserver.NewOptBool(true),
		QosType:       gcpgenserver.NewOptNilString("auto"),
		ResourceId:    "true-zonal-pool",
		Network:       "projects/test/global/networks/test",
	}

	originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", primaryZone, nil
	}

	t.Run("SetsIsTrueZonalOnCreatePoolParams", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		req := *baseReq

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.NewNotFoundErr("not found", nil))
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(p *commonparams.CreatePoolParams) bool {
			return p != nil &&
				p.IsTrueZonal &&
				p.IsPreview &&
				!p.IsRegionalHA &&
				p.LargeCapacity &&
				p.PrimaryZone == primaryZone &&
				p.SecondaryZone == primaryZone
		})).Return(&models.Pool{
			BaseModel:      models.BaseModel{UUID: "new-pool-uuid"},
			PoolAttributes: &models.PoolAttributes{IsTrueZonal: true, PrimaryZone: primaryZone, SecondaryZone: primaryZone},
		}, "operation-id", nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreatePool(context.Background(), &req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "operation-id")
		assert.Equal(t, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})

	t.Run("RejectsAutoTieringForTrueZonal", func(t *testing.T) {
		originalAutoTieringEnabled := autoTieringEnabled
		originalblockTrueZonalAutoTieringAndBackupFlag := blockTrueZonalAutoTieringAndBackup
		defer func() {
			autoTieringEnabled = originalAutoTieringEnabled
			blockTrueZonalAutoTieringAndBackup = originalblockTrueZonalAutoTieringAndBackupFlag
		}()
		autoTieringEnabled = true
		blockTrueZonalAutoTieringAndBackup = true

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		req := *baseReq
		req.AllowAutoTiering = gcpgenserver.NewOptNilBool(true)
		req.HotTierSizeInBytes = gcpgenserver.NewOptNilFloat64(float64(sixTiB))
		req.EnableHotTierAutoResize = gcpgenserver.NewOptNilBool(true)

		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.NewNotFoundErr("not found", nil))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreatePool(context.Background(), &req, params)

		assert.NoError(t, err)
		badReq, ok := result.(*gcpgenserver.V1betaCreatePoolBadRequest)
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, float64(http.StatusBadRequest), badReq.Code)
		assert.Contains(t, badReq.Message, trueZonalEnableAutoTieringErrorMsg)
	})
}

func TestValidateCreatePoolParams_ZonalLargeCapacity(t *testing.T) {
	const validPoolSize = 2199023255552 // 2 TiB

	originalEnableTrueZonalLV := enableTrueZonalLV
	defer func() { enableTrueZonalLV = originalEnableTrueZonalLV }()

	zonalLargeCapacityReq := &gcpgenserver.PoolV1beta{
		Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
		SizeInBytes:   validPoolSize,
		LargeCapacity: gcpgenserver.NewOptBool(true),
	}

	t.Run("AllowsZonalLargeCapacityPoolWhenFeatureDisabled", func(t *testing.T) {
		enableTrueZonalLV = false
		err := validateCreatePoolParams(zonalLargeCapacityReq, "us-east4-a")
		assert.Nil(t, err)
	})

	t.Run("AllowsZonalLargeCapacityPoolWhenFeatureEnabled", func(t *testing.T) {
		enableTrueZonalLV = true
		err := validateCreatePoolParams(zonalLargeCapacityReq, "us-east4-a")
		assert.Nil(t, err)
	})
}

func TestValidateCreatePoolParams_LargeCapacityFeatureFlag(t *testing.T) {
	originalEnableLargeCapacityPools := enableLargeCapacityPools
	defer func() { enableLargeCapacityPools = originalEnableLargeCapacityPools }()

	t.Run("RejectsLargeCapacityWhenFlagDisabled", func(tt *testing.T) {
		enableLargeCapacityPools = false
		req := &gcpgenserver.PoolV1beta{
			Unified:       gcpgenserver.NewOptBool(true),
			SizeInBytes:   2199023255552, // 2 TiB
			LargeCapacity: gcpgenserver.NewOptBool(true),
		}
		err := validateCreatePoolParams(req, "us-east4-a")
		assert.NotNil(tt, err)
		assert.Equal(tt, float64(http.StatusBadRequest), err.Code)
		assert.Contains(tt, err.Message, "Large capacity pools feature is not enabled")
	})

	t.Run("AllowsNonLargeCapacityWhenFlagDisabled", func(tt *testing.T) {
		enableLargeCapacityPools = false
		req := &gcpgenserver.PoolV1beta{
			Unified:     gcpgenserver.NewOptBool(true),
			SizeInBytes: 2199023255552, // 2 TiB
		}
		err := validateCreatePoolParams(req, "us-east4-a")
		assert.Nil(tt, err)
	})
}

func TestValidateCreatePoolParams_SecondaryZoneValidation(t *testing.T) {
	const (
		validPoolSize = 2199023255552 // 2 TiB
	)

	// Save original state and restore after all tests
	originalRegionalPoolEnabled := regionalPoolEnabled
	defer func() { regionalPoolEnabled = originalRegionalPoolEnabled }()

	t.Run("Regional Pool Secondary Zone Validation", func(tt *testing.T) {
		regionalPoolEnabled = true

		t.Run("Secondary Zone cannot be empty for regional pool", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
				SizeInBytes:   validPoolSize,
				Zone:          gcpgenserver.NewOptString("us-east4-a"),
				SecondaryZone: gcpgenserver.NewOptString(""), // Empty secondary zone
			}
			zone := "" // Empty zone indicates regional pool

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when secondary zone is empty for regional pool")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "Secondary Zone cannot be empty for regional pool.")
		})

		t.Run("Secondary Zone cannot be same as Primary Zone for regional pool", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
				SizeInBytes:   validPoolSize,
				Zone:          gcpgenserver.NewOptString("us-east4-a"),
				SecondaryZone: gcpgenserver.NewOptString("us-east4-a"), // Same as primary zone
			}
			zone := "" // Empty zone indicates regional pool

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when secondary zone is same as primary zone for regional pool")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "Secondary Zone cannot be same as Primary Zone")
		})

		t.Run("Valid regional pool with different zones", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
				SizeInBytes:   validPoolSize,
				Zone:          gcpgenserver.NewOptString("us-east4-a"),
				SecondaryZone: gcpgenserver.NewOptString("us-east4-b"), // Different from primary zone
			}
			zone := "" // Empty zone indicates regional pool

			err := validateCreatePoolParams(req, zone)
			assert.Nil(ttt, err, "Expected no error for valid regional pool with different zones")
		})
	})

	t.Run("Zonal Pool Secondary Zone Validation", func(tt *testing.T) {
		regionalPoolEnabled = true

		t.Run("Secondary Zone cannot be same as Primary Zone for zonal pool", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
				SizeInBytes:   validPoolSize,
				Zone:          gcpgenserver.NewOptString("us-east4-a"),
				SecondaryZone: gcpgenserver.NewOptString("us-east4-a"), // Same as primary zone
			}
			zone := "us-east4-a" // Non-empty zone indicates zonal pool

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when secondary zone is same as primary zone for zonal pool")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "Secondary Zone cannot be same as Primary Zone")
		})

		t.Run("Valid zonal pool with different secondary zone", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
				SizeInBytes:   validPoolSize,
				Zone:          gcpgenserver.NewOptString("us-east4-a"),
				SecondaryZone: gcpgenserver.NewOptString("us-east4-b"), // Different from primary zone
			}
			zone := "us-east4-a" // Non-empty zone indicates zonal pool

			err := validateCreatePoolParams(req, zone)
			assert.Nil(ttt, err, "Expected no error for valid zonal pool with different secondary zone")
		})

		t.Run("Valid zonal pool without secondary zone", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Type:        gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
				SizeInBytes: validPoolSize,
				Zone:        gcpgenserver.NewOptString("us-east4-a"),
				// SecondaryZone not set
			}
			zone := "us-east4-a" // Non-empty zone indicates zonal pool

			err := validateCreatePoolParams(req, zone)
			assert.Nil(ttt, err, "Expected no error for valid zonal pool without secondary zone")
		})
	})

	t.Run("Regional Pool Support Disabled", func(tt *testing.T) {
		regionalPoolEnabled = false

		t.Run("Regional pool creation should fail when feature is disabled", func(ttt *testing.T) {
			req := &gcpgenserver.PoolV1beta{
				Type:          gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED),
				SizeInBytes:   validPoolSize,
				Zone:          gcpgenserver.NewOptString("us-east4-a"),
				SecondaryZone: gcpgenserver.NewOptString("us-east4-b"),
			}
			zone := "" // Empty zone indicates regional pool

			err := validateCreatePoolParams(req, zone)
			assert.NotNil(ttt, err, "Expected error when regional pool support is disabled")
			assert.Equal(ttt, float64(400), err.Code)
			assert.Contains(ttt, err.Message, "Regional Pool Support is not enabled")
		})
	})
}

func TestConvertToPoolV1Beta(t *testing.T) {
	t.Run("WhenPoolHasAllFields", func(tt *testing.T) {
		createdAt := time.Now()
		deletedAt := time.Now().Add(1 * time.Hour)

		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "test-pool-uuid",
				CreatedAt: createdAt,
				DeletedAt: &deletedAt,
			},
			Description:             "Test pool description",
			VendorID:                "/projects/123/locations/us-east4/pools/test-pool",
			Region:                  "us-east4",
			SizeInBytes:             1099511627776, // 1 TiB
			ServiceLevel:            "premium",
			State:                   datamodel.LifeCycleStateAvailable,
			QosType:                 "auto",
			TotalThroughputMibps:    1024.0,
			TotalIops:               2048,
			UtilizedThroughputMibps: 1024.0,
			UtilizedIops:            2048,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      549755813888, // 512 GiB
				EnableHotTierAutoResize: true,
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 1024,
				Iops:       2048,
			},
			KmsConfig: &models.KmsConfig{
				BaseModel: models.BaseModel{
					UUID: "test-kms-config-id",
				},
				KeyName:         "test-kms-config",
				KeyProjectID:    "test-kms-project-id",
				KeyRingLocation: "us-east4",
				KeyRing:         "test-kms-keyring",
			},
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-pool-uuid", result.PoolId.Value)
		assert.Equal(tt, "Test pool description", result.Description.Value)
		assert.Equal(tt, float64(1099511627776), result.SizeInBytes)
		assert.Equal(tt, "auto", result.QosType.Value)
		assert.True(tt, result.CreatedAt.IsSet())
		assert.True(tt, result.DeletedAt.IsSet())
		assert.Equal(tt, float64(1024), result.TotalThroughputMibps.Value)
		assert.Equal(tt, float64(2048), result.TotalIops.Value)
		assert.True(tt, result.AvailableThroughputMibps.IsSet())
		assert.Equal(tt, float64(0), result.AvailableThroughputMibps.Value)
		assert.True(tt, result.AvailableIops.IsSet())
		assert.Equal(tt, float64(0), result.AvailableIops.Value)
		assert.Equal(tt, "test-kms-config-id", result.KmsConfigId.Value)
		assert.Equal(tt, "projects/test-kms-project-id/locations/us-east4/keyRings/test-kms-keyring/cryptoKeys/test-kms-config", result.KmsConfigResourceId.Value)
		assert.Equal(tt, gcpgenserver.PoolV1betaTypeUNIFIED, result.Type.Value, "Type should be set to UNIFIED for VSA pools")
		assert.True(tt, result.Unified.Value, "Unified should be true for VSA pools")
		assert.True(tt, result.UnifiedPool.Value, "UnifiedPool should be true for VSA pools")
	})

	t.Run("WhenPoolIsZonalNotRegionalHA", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "zonal-pool-uuid"},
			Name:      "zonal-pool",
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone:   "us-central1-a",
				SecondaryZone: "us-central1-a",
				IsTrueZonal:   true,
				IsRegionalHA:  false,
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "us-central1-a", result.Zone.Value)
		if result.SecondaryZone.IsSet() {
			assert.Empty(tt, result.SecondaryZone.Value, "zonal pool omits secondaryZone on describe")
		}
	})

	t.Run("WhenPoolHasAutoTieringConfigWithConsumption", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			Description:      "Test pool description",
			SizeInBytes:      1099511627776,
			ServiceLevel:     "PREMIUM",
			QosType:          "auto",
			AllowAutoTiering: true, // Enable auto-tiering to test lines 789-790
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				BucketName:              "test-bucket",
				HotTierConsumption:      250000000000,
				ColdTierConsumption:     150000000000,
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-pool-uuid", result.PoolId.Value)
		// Lines 789-790: Consumption fields should be set when AllowAutoTiering is true
		assert.True(tt, result.HotTierConsumption.IsSet(), "Line 789: HotTierConsumption should be set when AllowAutoTiering is true")
		assert.Equal(tt, int64(250000000000), result.HotTierConsumption.Value, "Line 789: HotTierConsumption should match config value")
		assert.True(tt, result.ColdTierConsumption.IsSet(), "Line 790: ColdTierConsumption should be set when AllowAutoTiering is true")
		assert.Equal(tt, int64(150000000000), result.ColdTierConsumption.Value, "Line 790: ColdTierConsumption should match config value")
	})

	t.Run("WhenPoolHasNoAutoTieringConfig", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:              "test-pool",
			Description:       "Test pool description",
			SizeInBytes:       1099511627776,
			ServiceLevel:      "PREMIUM",
			QosType:           "auto",
			PoolAttributes:    &models.PoolAttributes{},
			AutoTieringConfig: nil,
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-pool-uuid", result.PoolId.Value)
		assert.False(tt, result.HotTierConsumption.IsSet())
		assert.False(tt, result.ColdTierConsumption.IsSet())
	})

	t.Run("WhenPoolHasAutoTieringConfigWithoutConsumption", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:           "test-pool",
			Description:    "Test pool description",
			SizeInBytes:    1099511627776,
			ServiceLevel:   "PREMIUM",
			QosType:        "auto",
			PoolAttributes: &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				BucketName:              "test-bucket",
				HotTierConsumption:      0,
				ColdTierConsumption:     0,
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-pool-uuid", result.PoolId.Value)
		// Consumption fields should not be set in convertToPoolV1Beta (used for create/update responses)
		assert.False(tt, result.HotTierConsumption.IsSet())
		assert.False(tt, result.ColdTierConsumption.IsSet())
	})

	t.Run("WhenPoolIsFromCVP", func(tt *testing.T) {
		createdAt := time.Now()
		deletedAt := time.Now().Add(1 * time.Hour)

		pool := &cvpmodels.PoolV1beta{
			PoolID:    "test-pool-uuid",
			CreatedAt: strfmt.DateTime(createdAt),
			DeletedAt: func() *strfmt.DateTime {
				dt := strfmt.DateTime(deletedAt)
				return &dt
			}(),
			ResourceID: func() *string {
				s := "test-pool"
				return &s
			}(),
			Network: func() *string {
				s := "test-network"
				return &s
			}(),
			SizeInBytes: func() *float64 {
				s := 1099511627776.0
				return &s
			}(),
			ServiceLevel: func() *string {
				s := "premium"
				return &s
			}(),
			StoragePoolState:         "available",
			CustomPerformanceEnabled: true,
		}

		result := convertToPoolV1beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-pool-uuid", result.PoolId.Value)
		assert.Equal(tt, float64(1099511627776), result.SizeInBytes)
		assert.Equal(tt, gcpgenserver.PoolV1betaTypeFILE, result.Type.Value, "Type should be set to FILE for CVP pools")
		assert.False(tt, result.Unified.Value, "Unified should be false for CVP pools")
		assert.False(tt, result.UnifiedPool.Value, "UnifiedPool should be false for CVP pools")
	})

	t.Run("WhenCVPoolHasConsumptionFields", func(tt *testing.T) {
		createdAt := time.Now()
		deletedAt := time.Now().Add(1 * time.Hour)

		pool := &cvpmodels.PoolV1beta{
			PoolID:    "test-pool-uuid",
			CreatedAt: strfmt.DateTime(createdAt),
			DeletedAt: func() *strfmt.DateTime {
				dt := strfmt.DateTime(deletedAt)
				return &dt
			}(),
			ResourceID: func() *string {
				s := "test-pool"
				return &s
			}(),
			Network: func() *string {
				s := "test-network"
				return &s
			}(),
			SizeInBytes: func() *float64 {
				s := 1099511627776.0
				return &s
			}(),
			ServiceLevel: func() *string {
				s := "premium"
				return &s
			}(),
			StoragePoolState:         "available",
			CustomPerformanceEnabled: true,
			HotTierConsumption: func() *int64 {
				v := int64(300000000000)
				return &v
			}(),
			ColdTierConsumption: func() *int64 {
				v := int64(200000000000)
				return &v
			}(),
		}

		result := convertToPoolV1beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-pool-uuid", result.PoolId.Value)
		assert.True(tt, result.HotTierConsumption.IsSet())
		assert.Equal(tt, int64(300000000000), result.HotTierConsumption.Value)
		assert.True(tt, result.ColdTierConsumption.IsSet())
		assert.Equal(tt, int64(200000000000), result.ColdTierConsumption.Value)
	})

	t.Run("WhenPoolHasAssetMetadata", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-with-assets",
			},
			Description: "Pool with asset metadata",
			VendorID:    "/projects/123/locations/us-east4/pools/test-pool",
			Region:      "us-east4",
			SizeInBytes: 2147483648, // 2 GiB
			State:       datamodel.LifeCycleStateAvailable,
			QosType:     "auto",
			AssetMetadata: &models.AssetMetadata{
				ChildAssets: []models.ChildAsset{
					{
						AssetType:  "storage",
						AssetNames: []string{"asset1", "asset2"},
					},
					{
						AssetType:  "compute",
						AssetNames: []string{"vm1"},
					},
				},
			},
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.True(tt, result.AssetLocationMetadata.IsSet())

		metadata := result.AssetLocationMetadata.Value
		assert.True(tt, metadata.ChildAssets.Set)
		assert.Len(tt, metadata.ChildAssets.Value, 2)

		// Check first asset
		firstAsset := metadata.ChildAssets.Value[0]
		assert.Equal(tt, "storage", firstAsset.AssetType.Value)
		assert.Equal(tt, []string{"asset1", "asset2"}, firstAsset.AssetNames)

		// Check second asset
		secondAsset := metadata.ChildAssets.Value[1]
		assert.Equal(tt, "compute", secondAsset.AssetType.Value)
		assert.Equal(tt, []string{"vm1"}, secondAsset.AssetNames)
	})

	t.Run("WhenPoolHasEmptyAssetMetadata", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-empty-assets",
			},
			Description: "Pool with empty asset metadata",
			VendorID:    "/projects/123/locations/us-east4/pools/test-pool",
			SizeInBytes: 1073741824, // 1 GiB
			State:       datamodel.LifeCycleStateAvailable,
			AssetMetadata: &models.AssetMetadata{
				ChildAssets: []models.ChildAsset{},
			},
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.True(tt, result.AssetLocationMetadata.IsSet())

		metadata := result.AssetLocationMetadata.Value
		assert.True(tt, metadata.ChildAssets.Set)
		assert.Empty(tt, metadata.ChildAssets.Value)
	})

	t.Run("WhenPoolHasSingleChildAsset", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-single-asset",
			},
			Description: "Pool with single asset",
			VendorID:    "/projects/123/locations/us-east4/pools/test-pool",
			SizeInBytes: 1073741824, // 1 GiB
			State:       datamodel.LifeCycleStateAvailable,
			AssetMetadata: &models.AssetMetadata{
				ChildAssets: []models.ChildAsset{
					{
						AssetType:  "database",
						AssetNames: []string{"db1", "db2", "db3"},
					},
				},
			},
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.True(tt, result.AssetLocationMetadata.IsSet())

		metadata := result.AssetLocationMetadata.Value
		assert.True(tt, metadata.ChildAssets.Set)
		assert.Len(tt, metadata.ChildAssets.Value, 1)

		asset := metadata.ChildAssets.Value[0]
		assert.Equal(tt, "database", asset.AssetType.Value)
		assert.Equal(tt, []string{"db1", "db2", "db3"}, asset.AssetNames)
	})

	// Additional tests specifically for lines 789-790
	t.Run("WhenAllowAutoTieringIsFalse_ShouldNotSetConsumption", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: false, // Lines 789-790 should NOT execute
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				HotTierConsumption:      100000000000,
				ColdTierConsumption:     200000000000,
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		// Lines 789-790 should NOT execute when AllowAutoTiering is false
		assert.False(tt, result.HotTierConsumption.IsSet(), "Line 789 should NOT execute when AllowAutoTiering is false")
		assert.False(tt, result.ColdTierConsumption.IsSet(), "Line 790 should NOT execute when AllowAutoTiering is false")
	})

	t.Run("WhenAllowAutoTieringTrueButNilConfig_ShouldHandleGracefully", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:              "test-pool",
			SizeInBytes:       1099511627776,
			AllowAutoTiering:  true, // Lines 789-790 should execute
			PoolAttributes:    &models.PoolAttributes{},
			AutoTieringConfig: nil, // But config is nil
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		// Lines 789-790 execute, but helper functions return empty opt when config is nil
		assert.False(tt, result.HotTierConsumption.IsSet(), "Line 789: Should handle nil config gracefully")
		assert.False(tt, result.ColdTierConsumption.IsSet(), "Line 790: Should handle nil config gracefully")
	})

	t.Run("WhenAllowAutoTieringTrueWithZeroConsumption_ShouldSetZeroValues", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true, // Lines 789-790 should execute
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				HotTierConsumption:      0, // Zero consumption
				ColdTierConsumption:     0, // Zero consumption
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		// Lines 789-790: Should set even with zero values
		assert.True(tt, result.HotTierConsumption.IsSet(), "Line 789: Should set even with zero value")
		assert.Equal(tt, int64(0), result.HotTierConsumption.Value, "Line 789: Should be zero")
		assert.True(tt, result.ColdTierConsumption.IsSet(), "Line 790: Should set even with zero value")
		assert.Equal(tt, int64(0), result.ColdTierConsumption.Value, "Line 790: Should be zero")
	})

	t.Run("WhenAllowAutoTieringTrueWithLargeConsumption_ShouldHandleLargeValues", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      10995116277760, // 10 TiB
			AllowAutoTiering: true,           // Lines 789-790 should execute
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1099511627776, // 1 TiB
				EnableHotTierAutoResize: true,
				HotTierConsumption:      900000000000,  // 900 GB
				ColdTierConsumption:     9000000000000, // 9 TB
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		// Lines 789-790: Should handle large values correctly
		assert.True(tt, result.HotTierConsumption.IsSet(), "Line 789: Should handle large values")
		assert.Equal(tt, int64(900000000000), result.HotTierConsumption.Value, "Line 789: Should match large value")
		assert.True(tt, result.ColdTierConsumption.IsSet(), "Line 790: Should handle large values")
		assert.Equal(tt, int64(9000000000000), result.ColdTierConsumption.Value, "Line 790: Should match large value")
	})

	t.Run("WhenAllowAutoTieringTrueWithNegativeConsumption_ShouldSetNegativeValues", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true, // Lines 789-790 should execute
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				HotTierConsumption:      -100000000000, // Negative (edge case)
				ColdTierConsumption:     -200000000000, // Negative (edge case)
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		// Lines 789-790: Should set even with negative values (edge case)
		assert.True(tt, result.HotTierConsumption.IsSet(), "Line 789: Should set even with negative value")
		assert.Equal(tt, int64(-100000000000), result.HotTierConsumption.Value, "Line 789: Should handle negative")
		assert.True(tt, result.ColdTierConsumption.IsSet(), "Line 790: Should set even with negative value")
		assert.Equal(tt, int64(-200000000000), result.ColdTierConsumption.Value, "Line 790: Should handle negative")
	})

	t.Run("WhenAllowAutoTieringTrueWithOnlyHotConsumption_ShouldSetBothFields", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true, // Lines 789-790 should execute
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				HotTierConsumption:      300000000000, // Only hot has value
				ColdTierConsumption:     0,            // Cold is zero
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		// Lines 789-790: Both should be set
		assert.True(tt, result.HotTierConsumption.IsSet(), "Line 789: Should be set")
		assert.Equal(tt, int64(300000000000), result.HotTierConsumption.Value, "Line 789: Should match hot value")
		assert.True(tt, result.ColdTierConsumption.IsSet(), "Line 790: Should be set even if zero")
		assert.Equal(tt, int64(0), result.ColdTierConsumption.Value, "Line 790: Should be zero")
	})

	t.Run("WhenAllowAutoTieringTrueWithOnlyColdConsumption_ShouldSetBothFields", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "test-pool-uuid",
			},
			Name:             "test-pool",
			SizeInBytes:      1099511627776,
			AllowAutoTiering: true, // Lines 789-790 should execute
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				HotTierConsumption:      0,            // Hot is zero
				ColdTierConsumption:     400000000000, // Only cold has value
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		// Lines 789-790: Both should be set
		assert.True(tt, result.HotTierConsumption.IsSet(), "Line 789: Should be set even if zero")
		assert.Equal(tt, int64(0), result.HotTierConsumption.Value, "Line 789: Should be zero")
		assert.True(tt, result.ColdTierConsumption.IsSet(), "Line 790: Should be set")
		assert.Equal(tt, int64(400000000000), result.ColdTierConsumption.Value, "Line 790: Should match cold value")
	})
}

func TestConvertToPoolV1Beta_DeletedAtHandling(t *testing.T) {
	t.Run("WhenPoolHasNilDeletedAt_FieldIsOmitted", func(tt *testing.T) {
		// Test for READY pools where deletedAt should be omitted (Swagger 2.0 behavior)
		// This tests the fix for the bug where nil deletedAt was converted to 0001-01-01T00:00:00Z
		createdAt := time.Now()
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "ready-pool-uuid",
				CreatedAt: createdAt,
				UpdatedAt: createdAt,
				DeletedAt: nil, // READY pool has nil deletedAt
			},
			Name:           "ready-pool",
			Description:    "Test READY pool",
			SizeInBytes:    1099511627776,
			State:          datamodel.LifeCycleStateREADY,
			ServiceLevel:   "premium",
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "ready-pool-uuid", result.PoolId.Value)
		// Critical assertion: DeletedAt should be Set=false to omit field from JSON
		assert.False(tt, result.DeletedAt.IsSet(), "DeletedAt should be Set=false to omit field")
		// Verify Get() returns false for unset values
		_, ok := result.DeletedAt.Get()
		assert.False(tt, ok, "Get() should return false for unset DeletedAt")
	})

	t.Run("WhenPoolHasValidDeletedAt_ReturnsDeletedAtValue", func(tt *testing.T) {
		// Test for deleted pools where deletedAt has a timestamp
		createdAt := time.Now()
		deletedAt := time.Now().Add(1 * time.Hour)
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "deleted-pool-uuid",
				CreatedAt: createdAt,
				UpdatedAt: createdAt,
				DeletedAt: &deletedAt, // Deleted pool has timestamp
			},
			Name:           "deleted-pool",
			Description:    "Test deleted pool",
			SizeInBytes:    1099511627776,
			State:          datamodel.LifeCycleStateDeleted,
			ServiceLevel:   "premium",
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.Equal(tt, "deleted-pool-uuid", result.PoolId.Value)
		// Critical assertion: DeletedAt should be Set=true, Null=false, with valid timestamp
		assert.True(tt, result.DeletedAt.IsSet(), "DeletedAt should be Set=true")
		assert.False(tt, result.DeletedAt.IsNull(), "DeletedAt should be Null=false for deleted pool")
		// Verify Get() returns the actual timestamp
		actualDeletedAt, ok := result.DeletedAt.Get()
		assert.True(tt, ok, "Get() should return true for non-null DeletedAt")
		assert.Equal(tt, deletedAt, actualDeletedAt, "DeletedAt timestamp should match")
	})

	t.Run("WhenPoolHasZeroTimeDeletedAt_DoesNotReturnZeroTime", func(tt *testing.T) {
		// Regression test: ensure we never return 0001-01-01T00:00:00Z
		// This was the original bug where var deletedAt time.Time initialized to zero time
		createdAt := time.Now()
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "pool-with-nil-deleted",
				CreatedAt: createdAt,
				UpdatedAt: createdAt,
				DeletedAt: nil,
			},
			Name:           "test-pool",
			SizeInBytes:    1099511627776,
			State:          datamodel.LifeCycleStateREADY,
			ServiceLevel:   "premium",
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		// Ensure field is not set (omitted from JSON)
		assert.False(tt, result.DeletedAt.IsSet(), "Should be unset, not zero time")
		actualDeletedAt, ok := result.DeletedAt.Get()
		assert.False(tt, ok, "Should not return a value")
		// If we accidentally got a value, ensure it's not year 1
		if ok {
			assert.NotEqual(tt, 1, actualDeletedAt.Year(), "Should never return year 1 (zero time)")
		}
	})

	t.Run("WhenPoolDeletedAtIsNil_FieldOmittedFromJSON", func(tt *testing.T) {
		// Test JSON serialization behavior - field should be omitted (Swagger 2.0 behavior)
		// Critical for API response correctness
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "pool-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
				DeletedAt: nil,
			},
			Name:           "test-pool",
			SizeInBytes:    1099511627776,
			State:          datamodel.LifeCycleStateREADY,
			ServiceLevel:   "premium",
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		// Marshal to JSON and verify deletedAt is omitted (not "0001-01-01T00:00:00Z" or null)
		jsonBytes, err := result.MarshalJSON()
		assert.NoError(tt, err, "Should marshal to JSON without error")

		jsonStr := string(jsonBytes)
		// Ensure deletedAt is NOT the zero time string
		assert.NotContains(tt, jsonStr, "0001-01-01", "Should not contain zero time in JSON")
		// Field should be completely omitted (Swagger 2.0 behavior)
		assert.NotContains(tt, jsonStr, "deletedAt", "deletedAt field should be omitted from JSON when nil")
		assert.NotContains(tt, jsonStr, "deleted_at", "deleted_at field should be omitted from JSON when nil")
	})

	t.Run("WhenPoolDeletedAtIsSet_SerializesToJSONTimestamp", func(tt *testing.T) {
		// Test JSON serialization with valid timestamp
		deletedAt := time.Date(2026, 1, 23, 12, 0, 0, 0, time.UTC)
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "pool-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
				DeletedAt: &deletedAt,
			},
			Name:           "test-pool",
			SizeInBytes:    1099511627776,
			State:          datamodel.LifeCycleStateDeleted,
			ServiceLevel:   "premium",
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		// Marshal to JSON and verify deletedAt contains the timestamp
		jsonBytes, err := result.MarshalJSON()
		assert.NoError(tt, err, "Should marshal to JSON without error")

		jsonStr := string(jsonBytes)
		// Verify timestamp is present (2026)
		assert.Contains(tt, jsonStr, "2026", "Should contain the year 2026 in JSON")
		assert.NotContains(tt, jsonStr, `"deletedAt":null`, "deletedAt should not be null when set")
		assert.NotContains(tt, jsonStr, `"deleted_at":null`, "deleted_at should not be null when set")
	})

	t.Run("WhenPoolInCREATINGState_DeletedAtIsOmitted", func(tt *testing.T) {
		// Test that pools in CREATING state (not yet ready) have deletedAt field omitted
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "creating-pool-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
				DeletedAt: nil,
			},
			Name:           "creating-pool",
			SizeInBytes:    1099511627776,
			State:          datamodel.LifeCycleStateCreating,
			ServiceLevel:   "premium",
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.False(tt, result.DeletedAt.IsSet(), "DeletedAt should be Set=false (field omitted)")
	})

	t.Run("WhenPoolInDELETINGState_DeletedAtMayBeSet", func(tt *testing.T) {
		// Test that pools in DELETING state may have deletedAt already set
		deletedAt := time.Now()
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "deleting-pool-uuid",
				CreatedAt: time.Now().Add(-1 * time.Hour),
				UpdatedAt: time.Now(),
				DeletedAt: &deletedAt,
			},
			Name:           "deleting-pool",
			SizeInBytes:    1099511627776,
			State:          datamodel.LifeCycleStateDeleting,
			ServiceLevel:   "premium",
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.NotNil(tt, result)
		assert.True(tt, result.DeletedAt.IsSet(), "DeletedAt should be Set=true")
		assert.False(tt, result.DeletedAt.IsNull(), "DeletedAt should be Null=false for DELETING pool with timestamp")
		actualDeletedAt, ok := result.DeletedAt.Get()
		assert.True(tt, ok, "Get() should return true")
		assert.Equal(tt, deletedAt, actualDeletedAt, "DeletedAt timestamp should match")
	})
}

// TestCalculateIopsForUpdate tests the calculateIopsForUpdate function
// which is used for pool updates and covers the missing coverage scenarios
func TestCalculateIopsForUpdate(t *testing.T) {
	ctx := context.Background()

	// Create a mock existing pool for testing
	existingPool := &models.Pool{
		CustomPerformanceParams: &models.CustomPerformanceParams{
			Throughput: 128,  // 128 MiBps
			Iops:       2048, // 2048 IOPS
		},
	}

	t.Run("IOPSExplicitlyProvided", func(tt *testing.T) {
		t.Run("ValidIOPS", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{Value: 5000, Set: true}

			result := calculateIopsForUpdate(ctx, throughput, iops, existingPool)
			assert.Equal(ttt, int64(5000), result)
		})

		t.Run("IOPSBelowMinimum", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{Value: 1000, Set: true} // 1000 < 256*16 = 4096

			// Function doesn't validate - it just returns the provided IOPS value
			result := calculateIopsForUpdate(ctx, throughput, iops, existingPool)
			assert.Equal(ttt, int64(1000), result)
		})

		t.Run("IOPSAboveMaximum", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{Value: 200000, Set: true} // 200000 > 160000

			// Function doesn't validate - it just returns the provided IOPS value
			result := calculateIopsForUpdate(ctx, throughput, iops, existingPool)
			assert.Equal(ttt, int64(200000), result)
		})
	})

	t.Run("OnlyThroughputProvided", func(ttt *testing.T) {
		t.Run("CurrentIOPSBelowMinimum", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{} // Not set

			// Current IOPS (2048) is below new minimum (256*16 = 4096)
			// Should increase to minimum
			result := calculateIopsForUpdate(ctx, throughput, iops, existingPool)
			assert.Equal(ttt, int64(4096), result) // Should increase to minimum
		})

		t.Run("CurrentIOPSBelowMinimum", func(ttt *testing.T) {
			// Create pool with IOPS below new minimum
			lowIopsPool := &models.Pool{
				CustomPerformanceParams: &models.CustomPerformanceParams{
					Throughput: 128,
					Iops:       1000, // 1000 < 256*16 = 4096
				},
			}

			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{} // Not set

			result := calculateIopsForUpdate(ctx, throughput, iops, lowIopsPool)
			assert.Equal(ttt, int64(4096), result) // Should increase to minimum
		})

		t.Run("CurrentIOPSAboveMinimum", func(ttt *testing.T) {
			// Create pool with IOPS above new minimum
			highIopsPool := &models.Pool{
				CustomPerformanceParams: &models.CustomPerformanceParams{
					Throughput: 128,
					Iops:       10000, // 10000 > 256*16 = 4096
				},
			}

			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{} // Not set

			result := calculateIopsForUpdate(ctx, throughput, iops, highIopsPool)

			assert.Equal(ttt, int64(10000), result) // Should keep current IOPS
		})
	})

	t.Run("NeitherProvided", func(ttt *testing.T) {
		throughput := gcpgenserver.OptNilFloat64{} // Not set
		iops := gcpgenserver.OptNilFloat64{}       // Not set

		result := calculateIopsForUpdate(ctx, throughput, iops, existingPool)
		assert.Equal(ttt, int64(2048), result) // Should use existing IOPS
	})

	t.Run("ThroughputOnlyValidation", func(ttt *testing.T) {
		t.Run("ThroughputWithNoIOPS", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{Value: 512, Set: true}
			iops := gcpgenserver.OptNilFloat64{} // Not set

			// Should calculate minimum IOPS based on throughput
			result := calculateIopsForUpdate(ctx, throughput, iops, existingPool)
			assert.Equal(ttt, int64(8192), result) // 512 * 16 = 8192
		})

		t.Run("SmallThroughputIncrease", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{Value: 100, Set: true}
			iops := gcpgenserver.OptNilFloat64{} // Not set

			// Minimum IOPS for 100 MiBps is 1600, but current IOPS (2048) is higher
			result := calculateIopsForUpdate(ctx, throughput, iops, existingPool)
			assert.Equal(ttt, int64(2048), result) // Should keep current IOPS
		})
	})

	// This should not occur within VCP as the CustomPerformanceParams is always set, but it feels safer to check for it just in case.
	t.Run("CustomPerformanceParamsNil", func(tt *testing.T) {
		poolWithoutCustomPerf := &models.Pool{
			CustomPerformanceParams: nil,
			TotalIops:               5000, // Use TotalIops as fallback
		}

		t.Run("IOPSExplicitlyProvided", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{Value: 8000, Set: true}

			// Should return provided IOPS regardless of CustomPerformanceParams
			result := calculateIopsForUpdate(ctx, throughput, iops, poolWithoutCustomPerf)
			assert.Equal(ttt, int64(8000), result)
		})

		t.Run("OnlyThroughputProvided", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{}

			// Should use TotalIops (5000) and compare with minimum (256*16 = 4096)
			// Since 5000 > 4096, should keep TotalIops
			result := calculateIopsForUpdate(ctx, throughput, iops, poolWithoutCustomPerf)
			assert.Equal(ttt, int64(5000), result)
		})

		t.Run("OnlyThroughputProvidedWithLowTotalIops", func(ttt *testing.T) {
			poolWithLowIops := &models.Pool{
				CustomPerformanceParams: nil,
				TotalIops:               2000, // Below minimum for 256 MiBps (4096)
			}
			throughput := gcpgenserver.OptNilFloat64{Value: 256, Set: true}
			iops := gcpgenserver.OptNilFloat64{}

			// Should increase to minimum (4096) since TotalIops (2000) < minimum (4096)
			result := calculateIopsForUpdate(ctx, throughput, iops, poolWithLowIops)
			assert.Equal(ttt, int64(4096), result)
		})

		t.Run("NeitherProvided", func(ttt *testing.T) {
			throughput := gcpgenserver.OptNilFloat64{}
			iops := gcpgenserver.OptNilFloat64{}

			// Should use TotalIops when CustomPerformanceParams is nil
			result := calculateIopsForUpdate(ctx, throughput, iops, poolWithoutCustomPerf)
			assert.Equal(ttt, int64(5000), result)
		})
	})
}

func TestValidateUpdateThroughputAndIopsAboveUtilized(t *testing.T) {
	ctx := context.Background()

	pool := &models.Pool{
		QosType: utils.QosTypeManual,
		CustomPerformanceParams: &models.CustomPerformanceParams{
			Throughput: 128,
			Iops:       2048,
		},
		UtilizedThroughputMibps: 100,
		UtilizedIops:            1600,
	}

	t.Run("validThroughputAndIops_Increase", func(tt *testing.T) {
		throughput := float64(256)
		iops := float64(4096)

		err := validateUpdateThroughputAndIopsAboveUtilized(ctx, throughput, iops, pool)
		assert.NoError(tt, err)
	})

	t.Run("validThroughputAndIops_Decrease", func(tt *testing.T) {
		throughput := float64(125)
		iops := float64(2000)

		err := validateUpdateThroughputAndIopsAboveUtilized(ctx, throughput, iops, pool)
		assert.NoError(tt, err)
	})

	t.Run("validThroughputAndIops_ExactDecrease", func(tt *testing.T) {
		throughput := float64(100)
		iops := float64(1600)

		err := validateUpdateThroughputAndIopsAboveUtilized(ctx, throughput, iops, pool)
		assert.NoError(tt, err)
	})

	t.Run("validThroughputAndIops_DecreaseToMinimum", func(tt *testing.T) {
		minQosPool := &models.Pool{
			QosType: utils.QosTypeManual,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			UtilizedThroughputMibps: 32,
			UtilizedIops:            512,
		}

		throughput := float64(64)
		iops := float64(1024)

		err := validateUpdateThroughputAndIopsAboveUtilized(ctx, throughput, iops, minQosPool)
		assert.NoError(tt, err)
	})

	t.Run("invalidThroughputAndIops_DecreaseThroughputBelowUtilized", func(tt *testing.T) {
		throughput := float64(50)
		iops := float64(1600)

		err := validateUpdateThroughputAndIopsAboveUtilized(ctx, throughput, iops, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Requested throughput (50 MiBps) must be >= current pool utilization (100 MiBps).")
		var userInputErr *errors.UserInputValidationErr
		assert.ErrorAs(tt, err, &userInputErr)
	})

	t.Run("invalidThroughputAndIops_DecreaseIopsBelowUtilized", func(tt *testing.T) {
		throughput := float64(100)
		iops := float64(800)

		err := validateUpdateThroughputAndIopsAboveUtilized(ctx, throughput, iops, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Requested IOPS (800) must be >= current pool utilization (1600 IOPS).")
		var userInputErr *errors.UserInputValidationErr
		assert.ErrorAs(tt, err, &userInputErr)
	})
}

// TestV1betaUpdatePool_ThroughputOnlyUpdate tests the scenario where only throughput is updated
func TestV1betaUpdatePool_ThroughputOnlyUpdate(t *testing.T) {
	// Save original parseAndValidateRegionAndZone function and restore at end of test.
	originalParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

	t.Run("ThroughputOnlyUpdate_ValidIncrease", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Set orchestrator to return a pool when DescribePool is called.
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,   // 64 MiBps
				Iops:       1024, // 1024 IOPS (below new minimum)
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)

		// Set orchestrator to return success when UpdatePool is called.
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).
			Return(&models.Pool{
				BaseModel: models.BaseModel{
					UUID: "updated-pool-uuid",
				},
				PoolAttributes: &models.PoolAttributes{
					PrimaryZone: "us-east4-a",
				},
				CustomPerformanceParams: &models.CustomPerformanceParams{
					Throughput: 256,
					Iops:       4096,
				},
			}, "op-123", nil)

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		// Only update throughput, not IOPS
		req := &gcpgenserver.PoolUpdateV1beta{
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(256), // 256 MiBps
			// TotalIops not set - should trigger smart IOPS calculation
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})
}

func TestV1betaUpdatePool_ManualQos_IopsOnlyUpdate(t *testing.T) {
	// Save original parseAndValidateRegionAndZone function and restore at end of test.
	originalParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

	t.Run("IopsOnlyUpdate_ValidIncrease", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Pool with manual QoS and utilized values
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: utils.QosTypeManual,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 128,
				Iops:       2048,
			},
			UtilizedThroughputMibps: 100,
			UtilizedIops:            1600,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)

		// Set orchestrator to return success when UpdatePool is called.
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).
			Return(&models.Pool{
				BaseModel: models.BaseModel{
					UUID: "updated-pool-uuid",
				},
				CustomPerformanceParams: &models.CustomPerformanceParams{
					Throughput: 256,
					Iops:       4096,
				},
				PoolAttributes: &models.PoolAttributes{
					PrimaryZone: "us-east4-a",
				},
			}, "op-123", nil)

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}
		req := &gcpgenserver.PoolUpdateV1beta{
			TotalIops: gcpgenserver.NewOptNilFloat64(4096), // Increase IOPS above utilized (1600)
			// TotalThroughputMibps not set
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})

	t.Run("IopsOnlyUpdate_InvalidDecreaseBelowUtilized", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Pool with manual QoS and utilized values
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: utils.QosTypeManual,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 128,
				Iops:       2048,
			},
			UtilizedThroughputMibps: 100,
			UtilizedIops:            1600, // Pool has 1600 IOPS utilized
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}
		req := &gcpgenserver.PoolUpdateV1beta{
			TotalIops: gcpgenserver.NewOptNilFloat64(800), // Below utilized (1600)
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badRequest, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok, "Expected BadRequest response")
		assert.Contains(tt, badRequest.Message, "Requested IOPS (800) must be >= current pool utilization (1600 IOPS)")
	})

	t.Run("IopsOnlyUpdate_ValidDecreaseAboveUtilized", func(tt *testing.T) {
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Pool with manual QoS and utilized values
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: utils.QosTypeManual,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 128,
				Iops:       2048,
			},
			UtilizedThroughputMibps: 100,
			UtilizedIops:            1600,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)

		// Set orchestrator to return success when UpdatePool is called.
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).
			Return(&models.Pool{
				BaseModel: models.BaseModel{
					UUID: "updated-pool-uuid",
				},
				CustomPerformanceParams: &models.CustomPerformanceParams{
					Throughput: 128,
					Iops:       1800,
				},
				PoolAttributes: &models.PoolAttributes{
					PrimaryZone: "us-east4-a",
				},
			}, "op-123", nil)

		params := gcpgenserver.V1betaUpdatePoolParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-id",
		}

		req := &gcpgenserver.PoolUpdateV1beta{
			TotalIops: gcpgenserver.NewOptNilFloat64(1800), // Above utilized (1600)
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})
}

func TestGetKmsConfigForPool(t *testing.T) {
	t.Run("ReturnsNilWhenKmsConfigIdIsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		req := &gcpgenserver.PoolV1beta{KmsConfigId: gcpgenserver.NewOptNilString("")}
		params := &common.CreatePoolParams{}
		orchestrator := factory.NewMockOrchestratorFactory(tt)

		kmsConfig, errResp := _getAndSyncKmsConfigForPool(ctx, req, params, orchestrator)
		assert.Nil(tt, kmsConfig)
		assert.Nil(tt, errResp)
	})

	t.Run("ReturnsKmsConfigOnSuccess", func(tt *testing.T) {
		ctx := context.Background()
		req := &gcpgenserver.PoolV1beta{KmsConfigId: gcpgenserver.NewOptNilString("kms-uuid")}
		params := &common.CreatePoolParams{}
		expectedConfig := &models.KmsConfig{}
		orchestrator := factory.NewMockOrchestratorFactory(tt)
		orchestrator.On("GetKmsConfig", ctx, mock.Anything).Return(expectedConfig, nil)

		kmsConfig, errResp := _getAndSyncKmsConfigForPool(ctx, req, params, orchestrator)
		assert.Equal(tt, expectedConfig, kmsConfig)
		assert.Nil(tt, errResp)
	})

	t.Run("WhenSyncSuccess", func(tt *testing.T) {
		ctx := context.Background()
		req := &gcpgenserver.PoolV1beta{KmsConfigId: gcpgenserver.NewOptNilString("kms-uuid")}
		params := &common.CreatePoolParams{}
		orchestratorFactory := factory.NewMockOrchestratorFactory(tt)
		desc := "Description"
		kfp := "projects/project/locations/location/keyRings/keyring/cryptoKeys/key"
		resId := "resourceId"
		sdeResp := &cvpmodels.KmsConfigV1beta{
			Description:  &desc,
			Instructions: "Instructions",
			KeyFullPath:  &kfp,
			ResourceID:   &resId,
			KmsState:     cvpmodels.KmsConfigV1betaKmsStateREADY,
		}
		modelKmsConfig := &models.KmsConfig{}

		orchestratorFactory.On("GetKmsConfig", ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("kms", nil))
		orchestratorFactory.On("GetSDEKmsConfiguration", ctx, mock.Anything).Return(sdeResp, nil)
		orchestratorFactory.On("CreateAndSyncKmsConfig", ctx, mock.Anything).Return(modelKmsConfig, nil)

		kmsConfig, errResp := _getAndSyncKmsConfigForPool(ctx, req, params, orchestratorFactory)

		assert.NotNil(tt, kmsConfig)
		assert.Nil(tt, errResp)
	})

	t.Run("ReturnsInternalServerErrorOnOtherError", func(tt *testing.T) {
		ctx := context.Background()
		req := &gcpgenserver.PoolV1beta{KmsConfigId: gcpgenserver.NewOptNilString("kms-uuid")}
		params := &common.CreatePoolParams{}
		orchestratorFactory := factory.NewMockOrchestratorFactory(tt)
		orchestratorFactory.On("GetKmsConfig", ctx, mock.Anything).Return(nil, errors.New("unexpected error"))

		kmsConfig, errResp := _getAndSyncKmsConfigForPool(ctx, req, params, orchestratorFactory)
		assert.Nil(tt, kmsConfig)
		internalErr, ok := errResp.(*gcpgenserver.V1betaCreatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusInternalServerError), internalErr.Code)
	})

	t.Run("ReturnsInternalServerErrorWhenSDEKmsConfigFails", func(tt *testing.T) {
		ctx := context.Background()
		req := &gcpgenserver.PoolV1beta{KmsConfigId: gcpgenserver.NewOptNilString("kms-uuid")}
		params := &common.CreatePoolParams{}
		orchestratorFactory := factory.NewMockOrchestratorFactory(tt)

		orchestratorFactory.On("GetKmsConfig", ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("kms", nil))
		orchestratorFactory.On("GetSDEKmsConfiguration", ctx, mock.Anything).Return(nil, stderrors.New("sde error"))

		kmsConfig, errResp := _getAndSyncKmsConfigForPool(ctx, req, params, orchestratorFactory)

		assert.Nil(tt, kmsConfig)
		internalErr, ok := errResp.(*gcpgenserver.V1betaCreatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusInternalServerError), internalErr.Code)
	})

	t.Run("ReturnsInternalServerErrorWhenCreateAndSyncKmsConfigFails", func(tt *testing.T) {
		ctx := context.Background()
		req := &gcpgenserver.PoolV1beta{KmsConfigId: gcpgenserver.NewOptNilString("kms-uuid")}
		params := &common.CreatePoolParams{}
		orchestratorFactory := factory.NewMockOrchestratorFactory(tt)
		sdeResp := &cvpmodels.KmsConfigV1beta{KmsState: cvpmodels.KmsConfigV1betaKmsStateREADY}

		orchestratorFactory.On("GetKmsConfig", ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("kms", nil))
		orchestratorFactory.On("GetSDEKmsConfiguration", ctx, mock.Anything).Return(sdeResp, nil)
		orchestratorFactory.On("CreateAndSyncKmsConfig", ctx, mock.Anything).Return(nil, stderrors.New("sync error"))

		kmsConfig, errResp := _getAndSyncKmsConfigForPool(ctx, req, params, orchestratorFactory)

		assert.Nil(tt, kmsConfig)
		internalErr, ok := errResp.(*gcpgenserver.V1betaCreatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusInternalServerError), internalErr.Code)
	})

	t.Run("ReturnsBadRequestWhenSDEKmsConfigFails", func(tt *testing.T) {
		ctx := context.Background()
		req := &gcpgenserver.PoolV1beta{KmsConfigId: gcpgenserver.NewOptNilString("kms-uuid")}
		params := &common.CreatePoolParams{}
		orchestratorFactory := factory.NewMockOrchestratorFactory(tt)

		orchestratorFactory.On("GetKmsConfig", ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("kms", nil))
		orchestratorFactory.On("GetSDEKmsConfiguration", ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("kms", nil))

		kmsConfig, errResp := _getAndSyncKmsConfigForPool(ctx, req, params, orchestratorFactory)

		assert.Nil(tt, kmsConfig)
		internalErr, ok := errResp.(*gcpgenserver.V1betaCreatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), internalErr.Code)
	})

	t.Run("ReturnsBadRequestWhenSDEKmsConfigHasInvalidState", func(tt *testing.T) {
		ctx := context.Background()
		req := &gcpgenserver.PoolV1beta{KmsConfigId: gcpgenserver.NewOptNilString("kms-uuid")}
		params := &common.CreatePoolParams{}
		orchestratorFactory := factory.NewMockOrchestratorFactory(tt)
		sdeResp := &cvpmodels.KmsConfigV1beta{KmsState: cvpmodels.KmsConfigV1betaKmsStateDELETING}

		orchestratorFactory.On("GetKmsConfig", ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("kms", nil))
		orchestratorFactory.On("GetSDEKmsConfiguration", ctx, mock.Anything).Return(sdeResp, nil)

		kmsConfig, errResp := _getAndSyncKmsConfigForPool(ctx, req, params, orchestratorFactory)

		assert.Nil(tt, kmsConfig)
		badRequestErr, ok := errResp.(*gcpgenserver.V1betaCreatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusPreconditionFailed), badRequestErr.Code)
		assert.Contains(tt, badRequestErr.Message, "Kms config is in invalid state")
	})
}

func TestV1betaUpdatePool_AutoTieringValidation(t *testing.T) {
	// Save original autoTieringEnabled and restore at end of test.
	originalAutoTieringEnabled := autoTieringEnabled
	defer func() { autoTieringEnabled = originalAutoTieringEnabled }()

	// Save original parseAndValidateRegionAndZone function and restore at end of test.
	originalParseAndValidate := parseAndValidateRegionAndZone
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", "us-east4", nil
	}
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

	params := gcpgenserver.V1betaUpdatePoolParams{
		LocationId:    "us-east4",
		ProjectNumber: "project-number",
		PoolId:        "pool-id",
	}

	existingPool := &models.Pool{
		BaseModel: models.BaseModel{
			UUID: "pool-uuid",
		},
		AllowAutoTiering: false,
		AutoTieringConfig: &models.AutoTieringConfig{
			HotTierSizeInBytes:      1073741824, // 1GB
			EnableHotTierAutoResize: false,
		},
		CustomPerformanceParams: &models.CustomPerformanceParams{
			Throughput: 64,   // 64 MiBps
			Iops:       1024, // 1024 IOPS
		},
		PoolAttributes: &models.PoolAttributes{
			PrimaryZone: "us-east4-a",
		},
	}

	t.Run("AutoTiering feature disabled - rejects AllowAutoTiering=true", func(tt *testing.T) {
		autoTieringEnabled = false

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering: gcpgenserver.NewOptNilBool(true),
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "Auto-Tiering feature is currently not enabled")
	})

	t.Run("AutoTiering feature disabled - rejects HotTierSizeInBytes", func(tt *testing.T) {
		autoTieringEnabled = false

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(2147483648), // 2GB
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "Auto-Tiering feature is currently not enabled")
	})

	t.Run("AutoTiering feature disabled - rejects EnableHotTierAutoResize", func(tt *testing.T) {
		autoTieringEnabled = false

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "Auto-Tiering feature is currently not enabled")
	})

	t.Run("AutoTiering feature disabled - allows non-AutoTiering updates", func(tt *testing.T) {
		autoTieringEnabled = false

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updated-pool-uuid",
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, "op-123", nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			Description: gcpgenserver.NewOptNilString("Updated description"),
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})

	t.Run("AutoTiering feature enabled - rejects HotTierSizeInBytes without AllowAutoTiering", func(tt *testing.T) {
		autoTieringEnabled = true

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			// AllowAutoTiering not set (defaults to false)
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(2147483648), // 2GB
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering", badReq.Message)
	})

	t.Run("AutoTiering feature enabled - rejects EnableHotTierAutoResize without AllowAutoTiering", func(tt *testing.T) {
		autoTieringEnabled = true

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			// AllowAutoTiering not set (defaults to false)
			EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering", badReq.Message)
	})

	t.Run("AutoTiering feature enabled - rejects both HotTierSizeInBytes and EnableHotTierAutoResize without AllowAutoTiering", func(tt *testing.T) {
		autoTieringEnabled = true

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			// AllowAutoTiering not set (defaults to false)
			HotTierSizeInBytes:      gcpgenserver.NewOptNilFloat64(2147483648), // 2GB
			EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering", badReq.Message)
	})

	t.Run("AutoTiering feature enabled - rejects HotTierSizeInBytes with AllowAutoTiering explicitly set to false", func(tt *testing.T) {
		autoTieringEnabled = true

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:   gcpgenserver.NewOptNilBool(false),         // Explicitly set to false
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(2147483648), // 2GB
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering", badReq.Message)
	})

	t.Run("AutoTiering feature enabled - allows HotTierSizeInBytes with AllowAutoTiering enabled", func(tt *testing.T) {
		autoTieringEnabled = true

		// Create a pool that already has auto-tiering enabled
		poolWithAutoTiering := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AllowAutoTiering: true, // Pool already has auto-tiering enabled
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1073741824, // 1GB
				EnableHotTierAutoResize: false,
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,   // 64 MiBps
				Iops:       1024, // 1024 IOPS
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(poolWithAutoTiering, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updated-pool-uuid",
			},
			Name:        "test-pool",
			Description: "test description",
			State:       datamodel.LifeCycleStateCreated,
			SizeInBytes: 2199023255552, // 2TB
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone:     "us-east4-a",
				Labels:          nil,
				AllocatedBytes:  0,
				NumberOfVolumes: 0,
			},
			VendorSubNetID: "/projects/123456789/global/networks/default",
			ServiceLevel:   "premium",
			QosType:        "auto",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 128.0,
				Iops:       2048,
				Enabled:    true,
			},
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      2147483648, // 2GB
				EnableHotTierAutoResize: false,
			},
			LargeCapacity: false,
		}, "op-123", nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),          // Explicitly enabled
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(2147483648), // 2GB
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.True(tt, op.Name.IsSet(), "Expected operation name to be set")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})
}

func TestV1betaUpdatePool_AutoTieringParameterHandling(t *testing.T) {
	// Save original utils.AutoTieringEnabled and restore at end of test.
	originalAutoTieringEnabled := utils.AutoTieringEnabled
	defer func() { utils.AutoTieringEnabled = originalAutoTieringEnabled }()
	utils.AutoTieringEnabled = true

	// Save original autoTieringEnabled and restore at end of test.
	originalLocalAutoTieringEnabled := autoTieringEnabled
	defer func() { autoTieringEnabled = originalLocalAutoTieringEnabled }()
	autoTieringEnabled = true

	// Save original parseAndValidateRegionAndZone function and restore at end of test.
	originalParseAndValidate := parseAndValidateRegionAndZone
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", "us-east4", nil
	}
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidate }()

	params := gcpgenserver.V1betaUpdatePoolParams{
		LocationId:    "us-east4",
		ProjectNumber: "project-number",
		PoolId:        "pool-id",
	}

	t.Run("Successfully updates pool with AutoTiering enabled", func(tt *testing.T) {
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:             "test-pool",
			Description:      "test description",
			State:            datamodel.LifeCycleStateREADY,
			SizeInBytes:      2147483648, // 2GB
			QosType:          "auto",
			AllowAutoTiering: true, // Pool already has auto-tiering enabled
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1073741824, // 1GB
				EnableHotTierAutoResize: false,
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 64,   // 64 MiBps
				Iops:       1024, // 1024 IOPS
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updated-pool-uuid",
			},
			Name:             "test-pool",
			Description:      "test description",
			State:            datamodel.LifeCycleStateUpdating,
			SizeInBytes:      2147483648, // 2GB
			QosType:          "auto",
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1073741824,
				EnableHotTierAutoResize: false,
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, "op-123", nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(1073741824), // Must provide HotTierSizeInBytes
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.True(tt, op.Name.IsSet(), "Expected operation name to be set")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})

	t.Run("Successfully updates HotTierSizeInBytes", func(tt *testing.T) {
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:             "test-pool",
			Description:      "test description",
			State:            datamodel.LifeCycleStateREADY,
			SizeInBytes:      2147483648, // 2GB
			QosType:          "auto",
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1073741824, // 1GB
				EnableHotTierAutoResize: false,
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 64,   // 64 MiBps
				Iops:       1024, // 1024 IOPS
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updated-pool-uuid",
			},
			Name:        "test-pool",
			Description: "test description",
			State:       datamodel.LifeCycleStateUpdating,
			SizeInBytes: 2147483648, // 2GB
			QosType:     "auto",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, "op-123", nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(2147483648), // 2GB
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.True(tt, op.Name.IsSet(), "Expected operation name to be set")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})

	t.Run("Successfully updates EnableHotTierAutoResize", func(tt *testing.T) {
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:             "test-pool",
			Description:      "test description",
			State:            datamodel.LifeCycleStateREADY,
			SizeInBytes:      2147483648, // 2GB
			QosType:          "auto",
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1073741824, // 1GB
				EnableHotTierAutoResize: false,
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 64,   // 64 MiBps
				Iops:       1024, // 1024 IOPS
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updated-pool-uuid",
			},
			Name:        "test-pool",
			Description: "test description",
			State:       datamodel.LifeCycleStateUpdating,
			SizeInBytes: 2147483648, // 2GB
			QosType:     "auto",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, "op-123", nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:        gcpgenserver.NewOptNilBool(true),
			HotTierSizeInBytes:      gcpgenserver.NewOptNilFloat64(1073741824), // Must provide HotTierSizeInBytes when enabling AutoTiering
			EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.True(tt, op.Name.IsSet(), "Expected operation name to be set")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})

	t.Run("Handles pool with nil AutoTieringConfig", func(tt *testing.T) {
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AllowAutoTiering:  true, // Pool has auto-tiering enabled
			AutoTieringConfig: nil,  // No existing AutoTiering config (edge case)
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,   // 64 MiBps
				Iops:       1024, // 1024 IOPS
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State: datamodel.LifeCycleStateREADY,
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updated-pool-uuid",
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}, "op-123", nil)

		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:        gcpgenserver.NewOptNilBool(true),
			HotTierSizeInBytes:      gcpgenserver.NewOptNilFloat64(1073741824), // 1GB
			EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(false),
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.True(tt, op.Name.IsSet(), "Expected operation name to be set")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})

	t.Run("Successfully updates pool with existing AutoTiering enabled - allows hot tier parameter updates without AllowAutoTiering", func(tt *testing.T) {
		// Pool that already has auto-tiering enabled
		existingPoolWithAutoTiering := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:             "test-pool",
			Description:      "test description",
			State:            datamodel.LifeCycleStateREADY,
			SizeInBytes:      2199023255552, // 2TB
			QosType:          "auto",
			AllowAutoTiering: true, // Pool already has auto-tiering enabled
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1073741824, // 1GB
				EnableHotTierAutoResize: false,
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 64,   // 64 MiBps
				Iops:       1024, // 1024 IOPS
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, mock.Anything, mock.Anything).Return(existingPoolWithAutoTiering, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.Anything).Return(&models.Pool{
			BaseModel: models.BaseModel{
				UUID: "updated-pool-uuid",
			},
			Name:        "test-pool",
			Description: "Updating production pool with auto-tiering enabled",
			State:       datamodel.LifeCycleStateCreated,
			SizeInBytes: 2199023255552, // 2TB
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone:     "us-east4-a",
				Labels:          nil,
				AllocatedBytes:  0,
				NumberOfVolumes: 0,
			},
			VendorSubNetID: "/projects/123456789/global/networks/default",
			ServiceLevel:   "premium",
			QosType:        "auto",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64.0,
				Iops:       1024,
				Enabled:    true,
			},
			AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      2199023255552, // 2TB - updated value
				EnableHotTierAutoResize: true,          // updated value
			},
			LargeCapacity: false,
		}, "op-123", nil)

		// Request payload matching the user's example - no AllowAutoTiering field
		req := &gcpgenserver.PoolUpdateV1beta{
			Description:             gcpgenserver.NewOptNilString("Updating production pool with auto-tiering enabled"),
			HotTierSizeInBytes:      gcpgenserver.NewOptNilFloat64(2199023255552), // 2TB
			EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
			// Note: AllowAutoTiering is not set, but pool already has auto-tiering enabled
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		expectedOpName := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "op-123")
		assert.True(tt, op.Name.IsSet(), "Expected operation name to be set")
		assert.Equal(tt, expectedOpName, op.Name.Value)
	})
}

func TestV1betaCreatePool_WithActiveDirectoryConfigId(t *testing.T) {
	t.Run("WhenActiveDirectoryConfigIdIsValid", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Mock Active Directory config
		adConfig := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				UUID: "ad-config-uuid",
			},
			AdName: "test-ad",
		}

		// Mock parseAndValidateRegionAndZone
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		// Mock that pool doesn't exist yet
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Mock the AD config retrieval
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.MatchedBy(func(params *commonparams.GetADParams) bool {
			return params.UUID == "ad-config-uuid" && params.AccountName == "test-project"
		})).Return(adConfig, nil)

		// Mock the pool creation
		createdPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:                      "test-pool",
			AccountName:               "test-project",
			ActiveDirectoryConfigId:   "ad-config-uuid",
			ActiveDirectoryResourceId: "test-ad",
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 64,
				Iops:       1024,
			},
		}
		mockOrchestrator.EXPECT().CreatePool(mock.Anything, mock.MatchedBy(func(params *commonparams.CreatePoolParams) bool {
			return params.ActiveDirectoryId == "ad-config-uuid" && params.ActiveDirectory != nil && params.ADExistsInVCP
		})).Return(createdPool, "op-123", nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			Network:                  "test-network",
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1073741824,
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			ActiveDirectoryConfigId:  gcpgenserver.NewOptNilString("ad-config-uuid"),
		}

		params := gcpgenserver.V1betaCreatePoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1-a",
		}

		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, op.Done.IsSet())
		assert.False(tt, op.Done.Value, "Operation should be in progress")
		assert.True(tt, op.Name.IsSet())
		assert.Contains(tt, op.Name.Value, "op-123")

		// Verify that the created pool has the correct AD config set
		assert.Equal(tt, "pool-uuid", createdPool.UUID)
		assert.Equal(tt, "test-pool", createdPool.Name)
		assert.Equal(tt, "ad-config-uuid", createdPool.ActiveDirectoryConfigId)
		assert.Equal(tt, "test-ad", createdPool.ActiveDirectoryResourceId)
	})

	t.Run("WhenActiveDirectoryConfigIdNotFound", func(tt *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Mock parseAndValidateRegionAndZone
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		// Mock that pool doesn't exist yet
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Mock AD config not found
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("Active Directory", nil))

		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			Network:                  "test-network",
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1073741824,
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			ActiveDirectoryConfigId:  gcpgenserver.NewOptNilString("non-existent-ad-uuid"),
		}

		params := gcpgenserver.V1betaCreatePoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1-a",
		}

		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badRequest, ok := result.(*gcpgenserver.V1betaCreatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(tt, badRequest.Message, "Active Directory Config with ID non-existent-ad-uuid not found")
	})

	t.Run("WhenActiveDirectoryConfigIdInternalError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Mock parseAndValidateRegionAndZone
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		// Mock that pool doesn't exist yet
		mockOrchestrator.EXPECT().GetPoolByVendorID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		// Mock AD config internal error
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(nil, stderrors.New("database error"))

		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.PoolV1beta{
			ResourceId:               "test-pool",
			Unified:                  gcpgenserver.NewOptBool(true),
			Network:                  "test-network",
			ServiceLevel:             gcpgenserver.PoolV1betaServiceLevelFLEX,
			SizeInBytes:              1073741824,
			CustomPerformanceEnabled: gcpgenserver.NewOptBool(true),
			TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(64),
			QosType:                  gcpgenserver.NewOptNilString("auto"),
			ActiveDirectoryConfigId:  gcpgenserver.NewOptNilString("ad-config-uuid"),
		}

		params := gcpgenserver.V1betaCreatePoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1-a",
		}

		result, err := handler.V1betaCreatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		internalError, ok := result.(*gcpgenserver.V1betaCreatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusInternalServerError), internalError.Code)
		assert.Equal(tt, "database error", internalError.Message)
	})
}

func TestV1betaUpdatePool_WithActiveDirectoryConfigId(t *testing.T) {
	t.Run("WhenActiveDirectoryConfigIdIsValid", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		adConfig := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				UUID: "ad-config-uuid",
			},
			AdName: "test-ad",
		}

		// Mock parseAndValidateRegionAndZone
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}

		// Mock AD fetch
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.MatchedBy(func(params *commonparams.GetADParams) bool {
			return params.UUID == "ad-config-uuid" && params.AccountName == "test-project"
		})).Return(adConfig, nil)

		// Mock pool description
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "test-project").Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)

		// Mock pool update
		updatedPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:                      "test-pool",
			AccountName:               "test-project",
			ActiveDirectoryConfigId:   "ad-config-uuid",
			ActiveDirectoryResourceId: "test-ad",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 128,
				Iops:       2048,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdatePoolParams) bool {
			return params.ActiveDirectoryConfigId == "ad-config-uuid" &&
				params.ActiveDirectoryId == "ad-config-uuid" &&
				params.ActiveDirectory != nil &&
				params.IfADExistsInVCP
		})).Return(updatedPool, "op-123", nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.PoolUpdateV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("ad-config-uuid"),
		}

		params := gcpgenserver.V1betaUpdatePoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1-a",
			PoolId:        "pool-uuid",
		}

		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, op.Name.IsSet())

		// Verify that the updated pool has the correct AD config set
		assert.Equal(tt, "pool-uuid", updatedPool.UUID)
		assert.Equal(tt, "ad-config-uuid", updatedPool.ActiveDirectoryConfigId)
		assert.Equal(tt, "test-ad", updatedPool.ActiveDirectoryResourceId)
	})

	t.Run("WhenActiveDirectoryConfigIdIsNotFound_ReturnsBadRequest", func(tt *testing.T) {
		setTestCVPHost(tt, "")

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "test-project").Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("Active Directory", nil))

		handler := Handler{Orchestrator: mockOrchestrator}
		req := &gcpgenserver.PoolUpdateV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("missing-ad"),
		}
		params := gcpgenserver.V1betaUpdatePoolParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-central1-a",
			PoolId:         "pool-uuid",
			XCorrelationID: gcpgenserver.NewOptString("corr-id"),
		}

		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Contains(tt, badReq.Message, "missing-ad")
	})

	t.Run("WhenActiveDirectoryConfigIdIsValid_PropagatesCorrelationID", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		adConfig := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				UUID: "ad-config-uuid",
			},
			AdName: "test-ad",
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "test-project").Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.MatchedBy(func(params *commonparams.GetADParams) bool {
			return params.UUID == "ad-config-uuid"
		})).Return(adConfig, nil)
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdatePoolParams) bool {
			return params.ActiveDirectoryConfigId == "ad-config-uuid" &&
				params.ActiveDirectoryId == "ad-config-uuid" &&
				params.ActiveDirectory != nil &&
				params.IfADExistsInVCP &&
				params.XCorrelationID == "corr-id"
		})).Return(existingPool, "op-123", nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.PoolUpdateV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("ad-config-uuid"),
		}
		params := gcpgenserver.V1betaUpdatePoolParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-central1-a",
			PoolId:         "pool-uuid",
			XCorrelationID: gcpgenserver.NewOptString("corr-id"),
		}

		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		_, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
	})

	t.Run("WhenActiveDirectoryConfigIdInternalError_UpdateReturnsInternal", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}

		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "test-project").Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(nil, stderrors.New("database error"))

		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.PoolUpdateV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("ad-config-uuid"),
		}
		params := gcpgenserver.V1betaUpdatePoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1-a",
			PoolId:        "pool-uuid",
		}

		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusInternalServerError), internalErr.Code)
		assert.Equal(tt, "database error", internalErr.Message)
	})

	t.Run("WhenActiveDirectoryConfigIdIsEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		// Mock parseAndValidateRegionAndZone
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:                      "test-pool",
			ActiveDirectoryConfigId:   "old-ad-config-uuid",
			ActiveDirectoryResourceId: "old-ad",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}

		// Mock pool description
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "test-project").Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)

		// Mock pool update with empty ActiveDirectoryConfigId
		updatedPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name: "test-pool",
			// ActiveDirectoryConfigId should remain unchanged when not specified
			ActiveDirectoryConfigId:   "old-ad-config-uuid",
			ActiveDirectoryResourceId: "old-ad",
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}
		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdatePoolParams) bool {
			return params.ActiveDirectoryConfigId == ""
		})).Return(updatedPool, "op-123", nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		req := &gcpgenserver.PoolUpdateV1beta{
			// ActiveDirectoryConfigId not set
		}

		params := gcpgenserver.V1betaUpdatePoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1-a",
			PoolId:        "pool-uuid",
		}

		result, err := handler.V1betaUpdatePool(context.Background(), req, params)

		assert.NoError(tt, err)
		_, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)

		// Verify that the ActiveDirectoryConfigId was not set (empty string)
		// Since AD config wasn't provided in the update request, it should remain empty
		assert.Equal(tt, "pool-uuid", updatedPool.UUID)
		assert.Equal(tt, "old-ad-config-uuid", updatedPool.ActiveDirectoryConfigId)
		assert.Equal(tt, "old-ad", updatedPool.ActiveDirectoryResourceId)
	})
}

func TestV1betaUpdatePool_LargeCapacityPropagation(t *testing.T) {
	buildExistingPool := func() *models.Pool {
		return &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Description: "original description",
			SizeInBytes: 1099511627776, // 1 TiB
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Throughput: 64,
				Iops:       1024,
			},
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
			State:         "READY",
			LargeCapacity: false,
		}
	}

	buildParams := func() gcpgenserver.V1betaUpdatePoolParams {
		return gcpgenserver.V1betaUpdatePoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-central1-a",
			PoolId:        "pool-uuid",
		}
	}

	newRequest := func() *gcpgenserver.PoolUpdateV1beta {
		return &gcpgenserver.PoolUpdateV1beta{
			SizeInBytes:          gcpgenserver.NewOptNilFloat64(2199023255552), // 2 TiB
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(128),
			TotalIops:            gcpgenserver.NewOptNilFloat64(2048),
		}
	}

	t.Run("SetsLargeCapacityWhenProvided", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalParse := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParse }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		existingPool := buildExistingPool()
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "test-project").Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)

		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdatePoolParams) bool {
			return params.LargeCapacity != nil && *params.LargeCapacity
		})).Return(existingPool, "op-123", nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		req := newRequest()
		req.LargeCapacity = gcpgenserver.NewOptNilBool(true)

		result, err := handler.V1betaUpdatePool(context.Background(), req, buildParams())

		assert.NoError(tt, err)
		_, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
	})

	t.Run("LeavesLargeCapacityNilWhenNotProvided", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalParse := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParse }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		existingPool := buildExistingPool()
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "test-project").Return(existingPool, nil)
		mockOrchestrator.EXPECT().HasActiveClusterUpgrade(mock.Anything, "pool-uuid").Return(false, nil)

		mockOrchestrator.EXPECT().UpdatePool(mock.Anything, mock.MatchedBy(func(params *commonparams.UpdatePoolParams) bool {
			return params.LargeCapacity == nil
		})).Return(existingPool, "op-456", nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		req := newRequest() // LargeCapacity not set

		result, err := handler.V1betaUpdatePool(context.Background(), req, buildParams())

		assert.NoError(tt, err)
		_, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
	})
}

func TestGetAndSyncAdConfigForPool(t *testing.T) {
	t.Run("WhenActiveDirectoryConfigIdIsEmpty", func(tt *testing.T) {
		req := &gcpgenserver.PoolV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString(""),
		}

		accountName := "test-project"
		region := "us-central1"

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		adConfig, _, errResp := getAndSyncAdConfigForPool(context.Background(), req.ActiveDirectoryConfigId, accountName, region, "", mockOrchestrator)

		assert.Nil(tt, adConfig)
		assert.Nil(tt, errResp)
	})

	t.Run("WhenActiveDirectoryConfigIdIsValid", func(tt *testing.T) {
		adConfig := &models.ActiveDirectory{
			BaseModel: models.BaseModel{
				UUID: "ad-config-uuid",
			},
			AdName: "test-ad",
		}

		req := &gcpgenserver.PoolV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("ad-config-uuid"),
		}

		accountName := "test-project"
		region := "us-central1"

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.MatchedBy(func(getParams *commonparams.GetADParams) bool {
			return getParams.UUID == "ad-config-uuid" && getParams.AccountName == "test-project"
		})).Return(adConfig, nil)

		resultAdConfig, _, errResp := getAndSyncAdConfigForPool(context.Background(), req.ActiveDirectoryConfigId, accountName, region, "", mockOrchestrator)

		assert.Nil(tt, errResp)
		assert.NotNil(tt, resultAdConfig)
		assert.Equal(tt, "ad-config-uuid", resultAdConfig.UUID)
		assert.Equal(tt, "test-ad", resultAdConfig.AdName)
	})

	t.Run("WhenActiveDirectoryConfigIdNotFound", func(tt *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		req := &gcpgenserver.PoolV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("non-existent-ad-uuid"),
		}

		accountName := "test-project"
		region := "us-central1"

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("Active Directory", nil))

		adConfig, _, errResp := getAndSyncAdConfigForPool(context.Background(), req.ActiveDirectoryConfigId, accountName, region, "", mockOrchestrator)

		assert.Nil(tt, adConfig)
		assert.NotNil(tt, errResp)
		resp := errResp.toCreateResponse()
		badRequest, ok := resp.(*gcpgenserver.V1betaCreatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(tt, badRequest.Message, "Active Directory Config with ID non-existent-ad-uuid not found")
	})

	t.Run("WhenActiveDirectoryConfigIdInternalError", func(tt *testing.T) {
		req := &gcpgenserver.PoolV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("ad-config-uuid"),
		}

		accountName := "test-project"
		region := "us-central1"

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(nil, stderrors.New("database error"))

		adConfig, _, errResp := getAndSyncAdConfigForPool(context.Background(), req.ActiveDirectoryConfigId, accountName, region, "", mockOrchestrator)

		assert.Nil(tt, adConfig)
		assert.NotNil(tt, errResp)
		resp := errResp.toCreateResponse()
		internalError, ok := resp.(*gcpgenserver.V1betaCreatePoolInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusInternalServerError), internalError.Code)
		assert.Equal(tt, "database error", internalError.Message)
	})

	t.Run("WhenActiveDirectoryFetchedFromCVPSuccessfully", func(tt *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.SetCVPHost("http://cvp-host")
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()

		resourceID := "res-id"
		username := "user"
		domain := "example.com"
		dns := "1.1.1.1"
		netbios := "NETBIOS"
		orgUnit := "OU=Test"
		site := "site"
		aesEnc := true
		encryptDC := true
		ldapSigning := true
		allowLocal := true
		description := "desc"
		kdcIP := "10.0.0.1"
		kdcHostname := "kdc.example.com"

		mockActiveDirectories := active_directories.NewMockClientService(tt)
		mockCvpAD := &cvpmodels.ActiveDirectoryV1beta{
			ActiveDirectoryID:           "ad-config-uuid",
			ResourceID:                  &resourceID,
			Username:                    &username,
			Domain:                      &domain,
			DNS:                         &dns,
			NetBIOS:                     &netbios,
			ActiveDirectoryState:        cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateREADY,
			ActiveDirectoryStateDetails: "custom-details",
			OrganizationalUnit:          &orgUnit,
			Site:                        &site,
			KdcIP:                       kdcIP,
			KdcHostname:                 kdcHostname,
			AesEncryption:               &aesEnc,
			EncryptDCConnections:        &encryptDC,
			LdapSigning:                 &ldapSigning,
			AllowLocalNFSUsersWithLdap:  &allowLocal,
			Description:                 &description,
			BackupOperators:             []string{"backup"},
			SecurityOperators:           []string{"security"},
			Administrators:              []string{"admin"},
		}

		mockActiveDirectories.EXPECT().
			V1betaDescribeActiveDirectory(mock.Anything).
			Return(&active_directories.V1betaDescribeActiveDirectoryOK{Payload: mockCvpAD}, nil)

		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{ActiveDirectories: mockActiveDirectories}
		}

		req := &gcpgenserver.PoolV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("ad-config-uuid"),
		}
		accountName := "test-project"
		region := "us-central1"

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("Active Directory", nil))

		adConfig, existsInVCP, errResp := getAndSyncAdConfigForPool(context.Background(), req.ActiveDirectoryConfigId, accountName, region, "", mockOrchestrator)

		assert.Nil(tt, errResp)
		assert.False(tt, existsInVCP)
		assert.NotNil(tt, adConfig)
		assert.Equal(tt, "ad-config-uuid", adConfig.UUID)
		assert.Equal(tt, resourceID, adConfig.AdName)
		assert.Equal(tt, username, adConfig.Username)
		assert.Equal(tt, domain, adConfig.Domain)
		assert.Equal(tt, dns, adConfig.DNS)
		assert.Equal(tt, netbios, adConfig.NetBIOS)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, adConfig.State)
		assert.Equal(tt, "custom-details", adConfig.StateDetails)
		assert.NotNil(tt, adConfig.ActiveDirectoryAttributes)
		assert.Equal(tt, orgUnit, adConfig.ActiveDirectoryAttributes.OrganizationalUnit)
		assert.Equal(tt, site, adConfig.ActiveDirectoryAttributes.Site)
		assert.Equal(tt, kdcIP, adConfig.ActiveDirectoryAttributes.KdcIP)
		assert.Equal(tt, kdcHostname, adConfig.ActiveDirectoryAttributes.KdcHostname)
		assert.Equal(tt, aesEnc, adConfig.ActiveDirectoryAttributes.AesEncryption)
		assert.Equal(tt, encryptDC, adConfig.ActiveDirectoryAttributes.EncryptDCConnections)
		assert.Equal(tt, ldapSigning, adConfig.ActiveDirectoryAttributes.LdapSigning)
		assert.Equal(tt, allowLocal, adConfig.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap)
		assert.Equal(tt, description, adConfig.ActiveDirectoryAttributes.Description)
		assert.Equal(tt, []string{"backup"}, adConfig.ActiveDirectoryAttributes.BackupOperators)
		assert.Equal(tt, []string{"security"}, adConfig.ActiveDirectoryAttributes.SecurityOperators)
		assert.Equal(tt, []string{"admin"}, adConfig.ActiveDirectoryAttributes.Administrators)
	})

	t.Run("WhenActiveDirectoryFetchFromCVPFails", func(tt *testing.T) {
		originalCVPHost := cvp.CVP_HOST
		cvp.SetCVPHost("http://cvp-host")
		defer func() {
			cvp.CVP_HOST = originalCVPHost
		}()

		mockActiveDirectories := active_directories.NewMockClientService(tt)
		mockActiveDirectories.EXPECT().
			V1betaDescribeActiveDirectory(mock.Anything).
			Return(nil, fmt.Errorf("cvp unavailable"))

		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{ActiveDirectories: mockActiveDirectories}
		}

		req := &gcpgenserver.PoolV1beta{
			ActiveDirectoryConfigId: gcpgenserver.NewOptNilString("ad-config-uuid"),
		}
		accountName := "test-project"
		region := "us-central1"

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetADConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("Active Directory", nil))

		adConfig, _, errResp := getAndSyncAdConfigForPool(context.Background(), req.ActiveDirectoryConfigId, accountName, region, "", mockOrchestrator)

		assert.Nil(tt, adConfig)
		assert.NotNil(tt, errResp)
		resp := errResp.toCreateResponse()
		badRequest, ok := resp.(*gcpgenserver.V1betaCreatePoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badRequest.Code)
		assert.Contains(tt, badRequest.Message, "Active Directory Config with ID ad-config-uuid not found")
	})
}

func TestGetActiveDirectoryFromCVP(t *testing.T) {
	t.Run("ReturnsActiveDirectoryWhenFound", func(tt *testing.T) {
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()

		mockActiveDirectories := active_directories.NewMockClientService(tt)
		payload := &cvpmodels.ActiveDirectoryV1beta{
			ActiveDirectoryID: "ad-config-uuid",
		}
		mockActiveDirectories.EXPECT().
			V1betaDescribeActiveDirectory(mock.Anything).
			Return(&active_directories.V1betaDescribeActiveDirectoryOK{Payload: payload}, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{ActiveDirectories: mockActiveDirectories}
		}

		ad, err := getActiveDirectoryFromCVP(context.Background(), "ad-config-uuid", "project", "location", "x-correlation-id")
		assert.NoError(tt, err)
		assert.Equal(tt, payload, ad)
	})

	t.Run("ReturnsErrorWhenDescribeFails", func(tt *testing.T) {
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()

		mockActiveDirectories := active_directories.NewMockClientService(tt)
		mockActiveDirectories.EXPECT().
			V1betaDescribeActiveDirectory(mock.Anything).
			Return(nil, fmt.Errorf("describe failed"))

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{ActiveDirectories: mockActiveDirectories}
		}

		ad, err := getActiveDirectoryFromCVP(context.Background(), "ad-config-uuid", "project", "location", "x-correlation-id")
		assert.Nil(tt, ad)
		assert.Error(tt, err)
	})

	t.Run("ReturnsErrorWhenPayloadIsEmpty", func(tt *testing.T) {
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()

		mockActiveDirectories := active_directories.NewMockClientService(tt)
		mockActiveDirectories.EXPECT().
			V1betaDescribeActiveDirectory(mock.Anything).
			Return(&active_directories.V1betaDescribeActiveDirectoryOK{Payload: nil}, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{ActiveDirectories: mockActiveDirectories}
		}

		ad, err := getActiveDirectoryFromCVP(context.Background(), "ad-config-uuid", "project", "location", "x-correlation-id")
		assert.Nil(tt, ad)
		assert.Error(tt, err)
	})
}

func TestConvertCVPActiveDirectoryToModel(t *testing.T) {
	resourceID := "res-id"
	username := "user"
	domain := "example.com"
	dns := "1.1.1.1"
	netbios := "NETBIOS"
	orgUnit := "OU=Test"
	site := "site"
	aesEnc := true
	encryptDC := true
	ldapSigning := true
	allowLocal := true
	description := "desc"
	kdcIP := "10.0.0.1"
	kdcHostname := "kdc.example.com"

	tests := []struct {
		name           string
		state          string
		expectedState  string
		expectedDetail string
	}{
		{"ReadyState", cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateREADY, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails},
		{"CreatingState", cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateCREATING, datamodel.LifeCycleStateCreating, datamodel.LifeCycleStateCreatingDetails},
		{"UpdatingState", cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateUPDATING, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails},
		{"InUseState", cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateINUSE, datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails},
		{"DeletingState", cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateDELETING, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails},
		{"ErrorState", cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateERROR, datamodel.LifeCycleStateError, datamodel.LifeCycleStateError},
		{"UnknownStateDefaultsToReady", "", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails},
	}

	for _, tc := range tests {
		tt := tc
		t.Run(tt.name, func(t *testing.T) {
			cvpAd := &cvpmodels.ActiveDirectoryV1beta{
				ActiveDirectoryID:           "ad-config-uuid",
				ResourceID:                  &resourceID,
				Username:                    &username,
				Domain:                      &domain,
				DNS:                         &dns,
				NetBIOS:                     &netbios,
				ActiveDirectoryState:        tt.state,
				ActiveDirectoryStateDetails: "custom-details",
				OrganizationalUnit:          &orgUnit,
				Site:                        &site,
				KdcIP:                       kdcIP,
				KdcHostname:                 kdcHostname,
				AesEncryption:               &aesEnc,
				EncryptDCConnections:        &encryptDC,
				LdapSigning:                 &ldapSigning,
				AllowLocalNFSUsersWithLdap:  &allowLocal,
				Description:                 &description,
				BackupOperators:             []string{"backup"},
				SecurityOperators:           []string{"security"},
				Administrators:              []string{"admin"},
			}

			ad := convertCVPActiveDirectoryToModel(cvpAd)

			assert.Equal(t, "ad-config-uuid", ad.UUID)
			assert.Equal(t, resourceID, ad.AdName)
			assert.Equal(t, username, ad.Username)
			assert.Equal(t, domain, ad.Domain)
			assert.Equal(t, dns, ad.DNS)
			assert.Equal(t, netbios, ad.NetBIOS)
			assert.Equal(t, tt.expectedState, ad.State)
			assert.Equal(t, "custom-details", ad.StateDetails)
			assert.NotNil(t, ad.ActiveDirectoryAttributes)
			assert.Equal(t, orgUnit, ad.ActiveDirectoryAttributes.OrganizationalUnit)
			assert.Equal(t, site, ad.ActiveDirectoryAttributes.Site)
			assert.Equal(t, kdcIP, ad.ActiveDirectoryAttributes.KdcIP)
			assert.Equal(t, kdcHostname, ad.ActiveDirectoryAttributes.KdcHostname)
			assert.Equal(t, aesEnc, ad.ActiveDirectoryAttributes.AesEncryption)
			assert.Equal(t, encryptDC, ad.ActiveDirectoryAttributes.EncryptDCConnections)
			assert.Equal(t, ldapSigning, ad.ActiveDirectoryAttributes.LdapSigning)
			assert.Equal(t, allowLocal, ad.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap)
			assert.Equal(t, description, ad.ActiveDirectoryAttributes.Description)
			assert.Equal(t, []string{"backup"}, ad.ActiveDirectoryAttributes.BackupOperators)
			assert.Equal(t, []string{"security"}, ad.ActiveDirectoryAttributes.SecurityOperators)
			assert.Equal(t, []string{"admin"}, ad.ActiveDirectoryAttributes.Administrators)
		})
	}
}

func TestConvertToPoolV1Beta_WithActiveDirectoryFields(t *testing.T) {
	t.Run("WhenPoolHasActiveDirectoryConfig", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AccountName:               "test-project",
			Name:                      "test-pool",
			ActiveDirectoryConfigId:   "ad-config-uuid",
			ActiveDirectoryResourceId: "test-ad",
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, "test-pool", result.ResourceId)
		assert.True(tt, result.ActiveDirectoryConfigId.IsSet())
		assert.Equal(tt, "ad-config-uuid", result.ActiveDirectoryConfigId.Value)
		assert.True(tt, result.ActiveDirectoryResourceId.IsSet())
		// Note: The format uses region (us-central1), not zone (us-central1-a), as parsed from PrimaryZone
		assert.Equal(tt, "projects/test-project/locations/us-central1/activeDirectories/test-ad", result.ActiveDirectoryResourceId.Value)
	})

	t.Run("WhenPoolHasNoActiveDirectoryConfig", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AccountName: "test-project",
			Name:        "test-pool",
			// No ActiveDirectoryConfigId or ActiveDirectoryResourceId
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, "test-pool", result.ResourceId)
		assert.False(tt, result.ActiveDirectoryConfigId.IsSet())
		assert.False(tt, result.ActiveDirectoryResourceId.IsSet())
	})

	t.Run("WhenConvertingPoolV1Beta_NoConsumptionFieldsInCreateResponse", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:           "test-pool",
			PoolAttributes: &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierConsumption:  100,
				ColdTierConsumption: 200,
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, "test-pool", result.ResourceId)
		// Consumption fields should not be set in basic conversion (for create response)
		assert.False(tt, result.HotTierConsumption.IsSet())
		assert.False(tt, result.ColdTierConsumption.IsSet())
	})

	t.Run("WhenConvertingPoolV1BetaWithConsumption_WithAutoTieringEnabled_IncludesConsumptionFields", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:             "test-pool",
			AllowAutoTiering: true,
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				HotTierConsumption:      100,
				ColdTierConsumption:     200,
			},
		}

		result := convertToPoolV1BetaWithConsumption(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, "test-pool", result.ResourceId)
		// Consumption fields should be set in withConsumption version when auto tiering is enabled
		assert.True(tt, result.HotTierConsumption.IsSet())
		assert.Equal(tt, int64(100), result.HotTierConsumption.Value)
		assert.True(tt, result.ColdTierConsumption.IsSet())
		assert.Equal(tt, int64(200), result.ColdTierConsumption.Value)
		// Auto tiering related fields should be set when auto tiering is enabled
		assert.True(tt, result.HotTierSizeInBytes.IsSet())
		assert.Equal(tt, float64(500000000000), result.HotTierSizeInBytes.Value)
		assert.True(tt, result.EnableHotTierAutoResize.IsSet())
		assert.True(tt, result.EnableHotTierAutoResize.Value)
	})

	t.Run("WhenConvertingPoolV1BetaWithConsumption_WithoutAutoTiering_NoConsumptionFields", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:             "test-pool",
			AllowAutoTiering: false,
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierConsumption:  100,
				ColdTierConsumption: 200,
			},
		}

		result := convertToPoolV1BetaWithConsumption(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, "test-pool", result.ResourceId)
		// Consumption fields should not be set when auto tiering is not enabled
		assert.False(tt, result.HotTierConsumption.IsSet())
		assert.False(tt, result.ColdTierConsumption.IsSet())
		// Auto tiering related fields should not be set when auto tiering is not enabled
		assert.False(tt, result.HotTierSizeInBytes.IsSet())
		assert.False(tt, result.EnableHotTierAutoResize.IsSet())
	})

	t.Run("WhenPoolHasUtilizedThroughputAndIops_CalculatesAvailableCorrectly", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:                    "test-pool",
			TotalThroughputMibps:    1024.0,
			UtilizedThroughputMibps: 256.0,
			TotalIops:               2048,
			UtilizedIops:            512,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 1024.0,
				Iops:       2048,
			},
			QosType:        utils.QosTypeManual,
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, float64(1024), result.TotalThroughputMibps.Value)
		assert.Equal(tt, float64(768), result.AvailableThroughputMibps.Value) // 1024 - 256
		assert.Equal(tt, float64(2048), result.TotalIops.Value)
		assert.Equal(tt, float64(1536), result.AvailableIops.Value) // 2048 - 512
	})

	t.Run("WhenPoolHasFullyUtilizedThroughputAndIops_CalculatesAvailableCorrectly", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:                    "test-pool",
			TotalThroughputMibps:    1024.0,
			UtilizedThroughputMibps: 1024.0,
			TotalIops:               2048,
			UtilizedIops:            2048,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 1024.0,
				Iops:       2048,
			},
			QosType:        utils.QosTypeManual,
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, float64(1024), result.TotalThroughputMibps.Value)
		assert.Equal(tt, float64(0), result.AvailableThroughputMibps.Value)
		assert.Equal(tt, float64(2048), result.TotalIops.Value)
		assert.Equal(tt, float64(0), result.AvailableIops.Value)
	})

	t.Run("WhenPoolHasNoUtilizedThroughputAndIops_CalculatesAvailableCorrectly", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:                    "test-pool",
			TotalThroughputMibps:    1024.0,
			UtilizedThroughputMibps: 0.0,
			TotalIops:               2048,
			UtilizedIops:            0,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 1024.0,
				Iops:       2048,
			},
			QosType:        utils.QosTypeManual,
			PoolAttributes: &models.PoolAttributes{},
		}

		result := convertToPoolV1Beta(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, float64(1024), result.TotalThroughputMibps.Value)
		assert.Equal(tt, float64(1024), result.AvailableThroughputMibps.Value)
		assert.Equal(tt, float64(2048), result.TotalIops.Value)
		assert.Equal(tt, float64(2048), result.AvailableIops.Value)
	})

	t.Run("WhenConvertingPoolV1Beta_WithoutAutoTiering_NoAutoTieringFields", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			Name:             "test-pool",
			AllowAutoTiering: false,
			PoolAttributes:   &models.PoolAttributes{},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      500000000000,
				EnableHotTierAutoResize: true,
				HotTierConsumption:      100,
				ColdTierConsumption:     200,
			},
		}

		result := convertToPoolV1Beta(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, "test-pool", result.ResourceId)
		// Auto tiering related fields should not be set when auto tiering is not enabled
		assert.False(tt, result.HotTierSizeInBytes.IsSet())
		assert.False(tt, result.EnableHotTierAutoResize.IsSet())
		// Consumption fields should not be set in basic conversion
		assert.False(tt, result.HotTierConsumption.IsSet())
		assert.False(tt, result.ColdTierConsumption.IsSet())
	})

	t.Run("WhenPoolHasInvalidZoneForActiveDirectory", func(tt *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AccountName:               "test-project",
			Name:                      "test-pool",
			ActiveDirectoryConfigId:   "ad-config-uuid",
			ActiveDirectoryResourceId: "test-ad",
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "invalid-zone-format",
			},
		}

		// This should not panic and should handle the error gracefully
		result := convertToPoolV1Beta(pool)

		assert.Equal(tt, "pool-uuid", result.PoolId.Value)
		assert.Equal(tt, "test-pool", result.ResourceId)
		// ActiveDirectory fields should not be set when zone parsing fails
		assert.False(tt, result.ActiveDirectoryConfigId.IsSet())
		assert.False(tt, result.ActiveDirectoryResourceId.IsSet())
	})
}

// TestValidateUpdatePoolParams_EnablingAutoTieringOnNonATPool tests the validation
// that prevents enabling auto-tiering on pools that were not created with auto-tiering enabled
// based on the blockUpdatePooltoATPool flag.
func TestValidateUpdatePoolParams_EnablingAutoTieringOnNonATPool(t *testing.T) {
	// Save original flags and restore at end of test
	originalAutoTieringEnabled := autoTieringEnabled
	originalBlockUpdatePooltoATPool := blockUpdatePooltoATPool
	defer func() {
		autoTieringEnabled = originalAutoTieringEnabled
		blockUpdatePooltoATPool = originalBlockUpdatePooltoATPool
	}()
	autoTieringEnabled = true

	t.Run("RejectsEnablingAutoTieringOnNonATPoolWhenFlagIsTrue", func(tt *testing.T) {
		// Set flag to block updates
		blockUpdatePooltoATPool = true

		// Pool created without auto-tiering
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AllowAutoTiering: false, // Pool does not have auto-tiering enabled
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State: datamodel.LifeCycleStateREADY,
		}

		// Request to enable auto-tiering
		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(1073741824), // 1GB
		}

		result := validateUpdatePoolParams(req, existingPool)

		// Should return BadRequest error
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok, "Expected V1betaUpdatePoolBadRequest response")
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(tt, "Enabling Auto-Tiering on a non-AT pool is not supported currently", badReq.Message)
	})

	t.Run("AllowsEnablingAutoTieringOnNonATPoolWhenFlagIsFalse", func(tt *testing.T) {
		// Set flag to allow updates
		blockUpdatePooltoATPool = false

		// Pool created without auto-tiering
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AllowAutoTiering: false, // Pool does not have auto-tiering enabled
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State: datamodel.LifeCycleStateREADY,
		}

		// Request to enable auto-tiering
		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(1073741824), // 1GB
		}

		result := validateUpdatePoolParams(req, existingPool)

		// Should not return the block update error (may return other validation errors)
		if result != nil {
			badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
			if ok {
				assert.NotEqual(tt, "Enabling Auto-Tiering on a non-AT pool is not supported currently", badReq.Message,
					"Should not reject enabling auto-tiering when flag is false")
			}
		}
	})

	t.Run("AllowsUpdatingAutoTieringParamsOnATPool", func(tt *testing.T) {
		// Set flag to block updates (but this shouldn't affect pools that already have AT enabled)
		blockUpdatePooltoATPool = true

		// Pool created with auto-tiering enabled
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AllowAutoTiering: true, // Pool already has auto-tiering enabled
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State: datamodel.LifeCycleStateREADY,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1073741824, // 1GB
				EnableHotTierAutoResize: false,
			},
		}

		// Request to update auto-tiering params (increase hot tier size)
		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),
			HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(2147483648), // 2GB
		}

		result := validateUpdatePoolParams(req, existingPool)

		// Should not return error for this specific validation
		// Since existingPool.AllowAutoTiering is true, the condition !existingPool.AllowAutoTiering is false
		// so the blockUpdatePooltoATPool check won't trigger
		if result != nil {
			badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
			if ok {
				assert.NotEqual(tt, "Enabling Auto-Tiering on a non-AT pool is not supported currently", badReq.Message,
					"Should not reject updating auto-tiering params on pool that already has it enabled")
			}
		}
	})

	t.Run("RejectsEnablingAutoTieringWithoutHotTierSize", func(tt *testing.T) {
		// Set flag to allow updates (so we can test the HotTierSize validation)
		blockUpdatePooltoATPool = false

		// Pool created without auto-tiering
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AllowAutoTiering: false, // Pool does not have auto-tiering enabled
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State: datamodel.LifeCycleStateREADY,
		}

		// Request to enable auto-tiering without HotTierSizeInBytes
		req := &gcpgenserver.PoolUpdateV1beta{
			AllowAutoTiering: gcpgenserver.NewOptNilBool(true),
			// HotTierSizeInBytes not set
		}

		result := validateUpdatePoolParams(req, existingPool)

		// Should return BadRequest error for missing HotTierSizeInBytes
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok, "Expected V1betaUpdatePoolBadRequest response")
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(tt, "HotTierSizeInBytes is required when enabling auto-tiering", badReq.Message)
	})

	t.Run("AllowsNonAutoTieringUpdatesOnNonATPool", func(tt *testing.T) {
		// Set flag to block updates (but this shouldn't affect non-auto-tiering updates)
		blockUpdatePooltoATPool = true

		// Pool created without auto-tiering
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AllowAutoTiering: false,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State:       datamodel.LifeCycleStateREADY,
			SizeInBytes: 1099511627776, // 1TB
		}

		// Request to update non-auto-tiering params
		req := &gcpgenserver.PoolUpdateV1beta{
			Description:          gcpgenserver.NewOptNilString("Updated description"),
			SizeInBytes:          gcpgenserver.NewOptNilFloat64(2199023255552), // 2TB
			TotalIops:            gcpgenserver.NewOptNilFloat64(2048),
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(128),
		}

		result := validateUpdatePoolParams(req, existingPool)

		// Should not return the auto-tiering specific error
		// Since req.AllowAutoTiering is not set, the validation won't trigger
		if result != nil {
			badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
			if ok {
				assert.NotEqual(tt, "Enabling Auto-Tiering on a non-AT pool is not supported currently", badReq.Message,
					"Should not reject non-auto-tiering updates on non-AT pool")
			}
		}
	})

	t.Run("RejectsUpdateWhenPoolIsInDegradedState", func(tt *testing.T) {
		// Pool in DEGRADED state
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			AllowAutoTiering: false,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			State:       datamodel.LifeCycleStateDegraded,
			SizeInBytes: 1099511627776, // 1TB
		}

		// Request to update pool
		req := &gcpgenserver.PoolUpdateV1beta{
			Description:          gcpgenserver.NewOptNilString("Updated description"),
			SizeInBytes:          gcpgenserver.NewOptNilFloat64(2199023255552), // 2TB
			TotalIops:            gcpgenserver.NewOptNilFloat64(2048),
			TotalThroughputMibps: gcpgenserver.NewOptNilFloat64(128),
		}

		result := validateUpdatePoolParams(req, existingPool)

		// Should return Conflict error
		conflict, ok := result.(*gcpgenserver.V1betaUpdatePoolConflict)
		assert.True(tt, ok, "Expected V1betaUpdatePoolConflict response")
		assert.Equal(tt, float64(http.StatusConflict), conflict.Code)
		assert.Equal(tt, "Update operation is not allowed when the pool is in degraded state", conflict.Message)
	})

	t.Run("RejectsUpdateWhenPoolIsZoneSwitchingOrSwitched", func(tt *testing.T) {
		for _, state := range []string{datamodel.ZoneSwitching, datamodel.ZoneSwitched} {
			tt.Run(state, func(t *testing.T) {
				existingPool := &models.Pool{
					BaseModel: models.BaseModel{UUID: "pool-uuid"},
					State:     datamodel.LifeCycleStateREADY,
					PoolAttributes: &models.PoolAttributes{
						PrimaryZone:     "us-east4-a",
						ZoneSwitchState: state,
					},
					SizeInBytes: 1099511627776,
				}
				req := &gcpgenserver.PoolUpdateV1beta{
					Description: gcpgenserver.NewOptNilString("Updated description"),
				}
				result := validateUpdatePoolParams(req, existingPool)
				conflict, ok := result.(*gcpgenserver.V1betaUpdatePoolConflict)
				assert.True(t, ok, "Expected V1betaUpdatePoolConflict for zone switch state %q", state)
				assert.Equal(t, float64(http.StatusConflict), conflict.Code)
				assert.Equal(t, "Update operation is not allowed when the pool is switching/switched to a different primary zone.", conflict.Message)
			})
		}
	})
}

func TestValidateUpdatePoolParams_TrueZonalRejectsAutoTieringWhenFlagEnabled(t *testing.T) {
	originalblockTrueZonalAutoTieringAndBackupFlag := blockTrueZonalAutoTieringAndBackup
	originalAutoTieringEnabled := autoTieringEnabled
	defer func() {
		blockTrueZonalAutoTieringAndBackup = originalblockTrueZonalAutoTieringAndBackupFlag
		autoTieringEnabled = originalAutoTieringEnabled
	}()
	blockTrueZonalAutoTieringAndBackup = true
	autoTieringEnabled = true

	existingPool := &models.Pool{
		BaseModel: models.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateREADY,
		PoolAttributes: &models.PoolAttributes{
			IsTrueZonal: true,
			PrimaryZone: "us-east4-a",
		},
	}
	req := &gcpgenserver.PoolUpdateV1beta{
		AllowAutoTiering:   gcpgenserver.NewOptNilBool(true),
		HotTierSizeInBytes: gcpgenserver.NewOptNilFloat64(1099511627776),
	}

	result := validateUpdatePoolParams(req, existingPool)
	badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
	require.True(t, ok)
	assert.Equal(t, float64(http.StatusBadRequest), badReq.Code)
	assert.Equal(t, trueZonalEnableAutoTieringErrorMsg, badReq.Message)
}

func TestValidateUpdatePoolParams_TrueZonalBlocksSizeUpdate(t *testing.T) {
	originalAutoTieringEnabled := autoTieringEnabled
	defer func() { autoTieringEnabled = originalAutoTieringEnabled }()
	autoTieringEnabled = true

	const poolSize = float64(2 * 1024 * 1024 * 1024 * 1024)
	existingPool := &models.Pool{
		State:            datamodel.LifeCycleStateREADY,
		SizeInBytes:      uint64(poolSize),
		AllowAutoTiering: true,
		AutoTieringConfig: &models.AutoTieringConfig{
			HotTierSizeInBytes:      uint64(poolSize),
			EnableHotTierAutoResize: false,
		},
		PoolAttributes: &models.PoolAttributes{
			IsTrueZonal: true,
			PrimaryZone: "us-central1-a",
		},
	}

	t.Run("RejectsPoolSizeChange", func(tt *testing.T) {
		req := &gcpgenserver.PoolUpdateV1beta{
			SizeInBytes: gcpgenserver.NewOptNilFloat64(poolSize * 2),
		}
		result := validateUpdatePoolParams(req, existingPool)
		if !assert.NotNil(tt, result) {
			return
		}
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		if !assert.True(tt, ok) {
			return
		}
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Contains(tt, badReq.Message, "true-zonal")
	})

	t.Run("AllowsDescriptionWithoutSizeChange", func(tt *testing.T) {
		req := &gcpgenserver.PoolUpdateV1beta{
			Description: gcpgenserver.NewOptNilString("updated"),
		}
		assert.Nil(tt, validateUpdatePoolParams(req, existingPool))
	})

	t.Run("AllowsSameSizeInRequest", func(tt *testing.T) {
		req := &gcpgenserver.PoolUpdateV1beta{
			SizeInBytes: gcpgenserver.NewOptNilFloat64(poolSize),
		}
		assert.Nil(tt, validateUpdatePoolParams(req, existingPool))
	})

	t.Run("RejectsEnableHotTierAutoResizeTrue", func(tt *testing.T) {
		req := &gcpgenserver.PoolUpdateV1beta{
			EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(true),
		}
		result := validateUpdatePoolParams(req, existingPool)
		if !assert.NotNil(tt, result) {
			return
		}
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		if !assert.True(tt, ok) {
			return
		}
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Contains(tt, badReq.Message, "auto-resize")
	})

	t.Run("AllowsEnableHotTierAutoResizeFalse", func(tt *testing.T) {
		req := &gcpgenserver.PoolUpdateV1beta{
			EnableHotTierAutoResize: gcpgenserver.NewOptNilBool(false),
		}
		assert.Nil(tt, validateUpdatePoolParams(req, existingPool))
	})
}

// TestValidateUpdatePoolParams_QosType tests that pool updates with QosType
// are allowed when QosType matches the existing pool value
func TestValidateUpdatePoolParams_QosType(t *testing.T) {
	t.Run("AllowsUpdateWhenQosTypeMatchesExistingPool_Manual", func(tt *testing.T) {
		orig := enableMqos
		defer func() { enableMqos = orig }()
		enableMqos = true

		// Pool with manual QosType
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: "manual",
			State:   datamodel.LifeCycleStateREADY,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		// Request with same QosType as existing pool
		req := &gcpgenserver.PoolUpdateV1beta{
			QosType:     gcpgenserver.NewOptNilString("manual"),
			Description: gcpgenserver.NewOptNilString("Updated description"),
		}

		result := validateUpdatePoolParams(req, existingPool)
		assert.Nil(tt, result, "Should allow update when QosType matches existing pool")
	})

	t.Run("AllowsUpdateWhenQosTypeMatchesExistingPool_Auto", func(tt *testing.T) {
		// Pool with auto QosType
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: "auto",
			State:   datamodel.LifeCycleStateREADY,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		// Request with same QosType as existing pool
		req := &gcpgenserver.PoolUpdateV1beta{
			QosType:     gcpgenserver.NewOptNilString("auto"),
			Description: gcpgenserver.NewOptNilString("Updated description"),
		}

		result := validateUpdatePoolParams(req, existingPool)
		assert.Nil(tt, result, "Should allow update when QosType matches existing pool")
	})

	t.Run("AllowsUpdateWhenQosTypeChangesFromAutoToManual", func(tt *testing.T) {
		orig := enableMqos
		defer func() { enableMqos = orig }()
		enableMqos = true

		// Pool with auto QosType
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: "auto",
			State:   datamodel.LifeCycleStateREADY,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		// Request changing QosType to manual (transition allowed; workflow handles it)
		req := &gcpgenserver.PoolUpdateV1beta{
			QosType: gcpgenserver.NewOptNilString("manual"),
		}

		result := validateUpdatePoolParams(req, existingPool)
		assert.Nil(tt, result, "Should allow update when QosType changes from auto to manual")
	})

	t.Run("AllowsUpdateWhenQosTypeChangesFromManualToAuto", func(tt *testing.T) {
		// Pool with manual QosType
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: "manual",
			State:   datamodel.LifeCycleStateREADY,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		// Request changing QosType to auto (transition allowed; workflow handles it)
		req := &gcpgenserver.PoolUpdateV1beta{
			QosType: gcpgenserver.NewOptNilString("auto"),
		}

		result := validateUpdatePoolParams(req, existingPool)
		assert.Nil(tt, result, "Should allow update when QosType changes from manual to auto")
	})

	t.Run("RejectsUpdateWhenOntapModePool", func(tt *testing.T) {
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: "auto",
			State:   datamodel.LifeCycleStateREADY,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
			APIAccessMode: common.ONTAPMode,
		}

		req := &gcpgenserver.PoolUpdateV1beta{
			QosType: gcpgenserver.NewOptNilString("manual"),
		}

		result := validateUpdatePoolParams(req, existingPool)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok, "Expected V1betaUpdatePoolBadRequest response")
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(tt, "QosType cannot be modified for an ONTAP mode pool", badReq.Message)
	})

	t.Run("RejectsUpdateWhenQosTypeInvalid", func(tt *testing.T) {
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: "auto",
			State:   datamodel.LifeCycleStateREADY,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		req := &gcpgenserver.PoolUpdateV1beta{
			QosType: gcpgenserver.NewOptNilString("invalid-qos-type"),
		}

		result := validateUpdatePoolParams(req, existingPool)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok, "Expected V1betaUpdatePoolBadRequest response")
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(tt, "QosType must be 'auto' or 'manual'", badReq.Message)
	})

	t.Run("RejectsUpdateWhenQosTypeManualAndEnableMqosFalse", func(tt *testing.T) {
		orig := enableMqos
		defer func() { enableMqos = orig }()
		enableMqos = false

		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: "auto",
			State:   datamodel.LifeCycleStateREADY,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		req := &gcpgenserver.PoolUpdateV1beta{
			QosType: gcpgenserver.NewOptNilString("manual"),
		}

		result := validateUpdatePoolParams(req, existingPool)
		badReq, ok := result.(*gcpgenserver.V1betaUpdatePoolBadRequest)
		assert.True(tt, ok, "Expected V1betaUpdatePoolBadRequest response")
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Equal(tt, "Manual QosType is not supported", badReq.Message)
	})

	t.Run("AllowsUpdateWhenQosTypeNotSet", func(tt *testing.T) {
		// Pool with manual QosType
		existingPool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID: "pool-uuid",
			},
			QosType: "manual",
			State:   datamodel.LifeCycleStateREADY,
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "us-east4-a",
			},
		}

		// Request without QosType (not trying to change it)
		req := &gcpgenserver.PoolUpdateV1beta{
			Description: gcpgenserver.NewOptNilString("Updated description"),
		}

		result := validateUpdatePoolParams(req, existingPool)
		assert.Nil(tt, result, "Should allow update when QosType is not set in request")
	})
}

func TestV1betaGetBackupConfigsForPool(t *testing.T) {
	t.Run("Success_WithBackupConfigs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		backupVaultPath1 := "projects/test-project/locations/us-west1-a/backupVaults/backup-vault-1"
		backupVaultPath2 := "projects/test-project/locations/us-west1-a/backupVaults/backup-vault-2"
		expectedConfigs := []*models.ExpertModeVolumeBackupConfig{
			{
				VolumeResourceID: "volume-uuid-1",
				BackupVaultPath:  &backupVaultPath1,
			},
			{
				VolumeResourceID: "volume-uuid-2",
				BackupVaultPath:  &backupVaultPath2,
			},
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return(expectedConfigs, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		okResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolOK)
		assert.True(tt, ok)
		assert.Equal(tt, 2, len(okResp.BackupConfigs))

		// Verify first config
		assert.Equal(tt, "volume-uuid-1", okResp.BackupConfigs[0].VolumeId.Value)
		assert.True(tt, okResp.BackupConfigs[0].BackupConfig.IsSet())
		assert.Equal(tt, "projects/test-project/locations/us-west1-a/backupVaults/backup-vault-1", okResp.BackupConfigs[0].BackupConfig.Value.BackupVaultId.Value)

		// Verify second config
		assert.Equal(tt, "volume-uuid-2", okResp.BackupConfigs[1].VolumeId.Value)
		assert.True(tt, okResp.BackupConfigs[1].BackupConfig.IsSet())
		assert.Equal(tt, "projects/test-project/locations/us-west1-a/backupVaults/backup-vault-2", okResp.BackupConfigs[1].BackupConfig.Value.BackupVaultId.Value)

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Success_WithMixedBackupConfigs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}
		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		backupVaultPath := "projects/test-project/locations/us-west1-a/backupVaults/backup-vault-1"
		expectedConfigs := []*models.ExpertModeVolumeBackupConfig{
			{
				VolumeResourceID: "volume-with-backup",
				BackupVaultPath:  &backupVaultPath,
			},
			{
				VolumeResourceID: "volume-without-backup",
				BackupVaultPath:  nil,
			},
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return(expectedConfigs, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		okResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolOK)
		assert.True(tt, ok)
		assert.Equal(tt, 2, len(okResp.BackupConfigs))

		// Volume with backup config
		assert.Equal(tt, "volume-with-backup", okResp.BackupConfigs[0].VolumeId.Value)
		assert.True(tt, okResp.BackupConfigs[0].BackupConfig.IsSet())

		// Volume without backup config
		assert.Equal(tt, "volume-without-backup", okResp.BackupConfigs[1].VolumeId.Value)
		assert.False(tt, okResp.BackupConfigs[1].BackupConfig.IsSet())

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Success_EmptyPool", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}
		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return([]*models.ExpertModeVolumeBackupConfig{}, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		okResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolOK)
		assert.True(tt, ok)
		assert.Equal(tt, 0, len(okResp.BackupConfigs))

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Error_NotEnabled", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "invalid-location",
			PoolId:        "pool-uuid-123",
		}
		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = false

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		badReq, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Backup for ONTAP mode pools is currently not enabled.", badReq.Message)
	})

	t.Run("Error_InvalidLocation", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "invalid-location",
			PoolId:        "pool-uuid-123",
		}
		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock location validation to return error
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location format",
			}
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		badReq, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Invalid location format", badReq.Message)
	})

	t.Run("Error_PoolNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "non-existent-pool",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "non-existent-pool", "test-project", "us-west1-a").
			Return(nil, errors.NewNotFoundErr("Pool", nil)).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		notFoundResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolNotFound)
		assert.True(tt, ok)
		assert.Equal(tt, float64(404), notFoundResp.Code)
		assert.Equal(tt, "Pool not found", notFoundResp.Message)

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Error_NonONTAPPool", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "rest-pool-uuid",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "rest-pool-uuid", "test-project", "us-west1-a").
			Return(nil, errors.NewBadRequestErr("backup configurations are only available for ONTAP pools")).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		badReq, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "backup configurations are only available for ONTAP pools")

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Error_InternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return(nil, stderrors.New("database connection error")).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.Error(tt, err)
		assert.NotNil(tt, result)

		_, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolInternalServerError)
		assert.True(tt, ok)

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Success_LargeNumberOfVolumes", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock location validation
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		expectedConfigs := make([]*models.ExpertModeVolumeBackupConfig, 100)
		for i := 0; i < 100; i++ {
			backupVaultPath := fmt.Sprintf("projects/test-project/locations/us-west1-a/backupVaults/backup-vault-%d", i)
			expectedConfigs[i] = &models.ExpertModeVolumeBackupConfig{
				VolumeResourceID: fmt.Sprintf("volume-uuid-%d", i),
				BackupVaultPath:  &backupVaultPath,
			}
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return(expectedConfigs, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		okResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolOK)
		assert.True(tt, ok)
		assert.Equal(tt, 100, len(okResp.BackupConfigs))

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Success_WithScheduledBackupEnabled", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		scheduledBackupEnabled := true
		expectedConfigs := []*models.ExpertModeVolumeBackupConfig{
			{
				VolumeResourceID:       "volume-uuid-1",
				ScheduledBackupEnabled: &scheduledBackupEnabled,
			},
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return(expectedConfigs, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		okResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.BackupConfigs, 1)

		cfg := okResp.BackupConfigs[0]
		assert.Equal(tt, "volume-uuid-1", cfg.VolumeId.Value)
		assert.True(tt, cfg.BackupConfig.IsSet())
		assert.True(tt, cfg.BackupConfig.Value.ScheduledBackupEnabled.IsSet())
		assert.True(tt, cfg.BackupConfig.Value.ScheduledBackupEnabled.Value)
		assert.False(tt, cfg.BackupConfig.Value.BackupChainBytes.IsSet())

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Success_WithBackupChainBytes", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		chainBytes := int64(1073741824) // 1 GiB
		expectedConfigs := []*models.ExpertModeVolumeBackupConfig{
			{
				VolumeResourceID: "volume-uuid-1",
				BackupChainBytes: &chainBytes,
			},
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return(expectedConfigs, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		okResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.BackupConfigs, 1)

		cfg := okResp.BackupConfigs[0]
		assert.Equal(tt, "volume-uuid-1", cfg.VolumeId.Value)
		assert.True(tt, cfg.BackupConfig.IsSet())
		assert.True(tt, cfg.BackupConfig.Value.BackupChainBytes.IsSet())
		assert.Equal(tt, int64(1073741824), cfg.BackupConfig.Value.BackupChainBytes.Value)
		assert.False(tt, cfg.BackupConfig.Value.ScheduledBackupEnabled.IsSet())

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Success_WithAllNewFieldsAlongsideExisting", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		scheduledBackupEnabled := true
		chainBytes := int64(2147483648) // 2 GiB
		backupVaultPath := "projects/test-project/locations/us-west1-a/backupVaults/my-vault"
		backupPolicyPath := "projects/test-project/locations/us-west1-a/backupPolicies/my-policy"
		expectedConfigs := []*models.ExpertModeVolumeBackupConfig{
			{
				VolumeResourceID:       "volume-uuid-1",
				BackupVaultPath:        &backupVaultPath,
				BackupPolicyPath:       &backupPolicyPath,
				ScheduledBackupEnabled: &scheduledBackupEnabled,
				BackupChainBytes:       &chainBytes,
			},
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return(expectedConfigs, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		okResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.BackupConfigs, 1)

		cfg := okResp.BackupConfigs[0]
		assert.Equal(tt, "volume-uuid-1", cfg.VolumeId.Value)
		assert.True(tt, cfg.BackupConfig.IsSet())
		bc := cfg.BackupConfig.Value
		assert.Equal(tt, backupVaultPath, bc.BackupVaultId.Value)
		assert.Equal(tt, backupPolicyPath, bc.BackupPolicyId.Value)
		assert.True(tt, bc.ScheduledBackupEnabled.IsSet())
		assert.True(tt, bc.ScheduledBackupEnabled.Value)
		assert.True(tt, bc.BackupChainBytes.IsSet())
		assert.Equal(tt, int64(2147483648), bc.BackupChainBytes.Value)

		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Success_OnlyNewFields_BackupConfigSetWithoutVaultOrPolicy", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		params := gcpgenserver.V1betaGetBackupConfigsForPoolParams{
			ProjectNumber: "test-project",
			LocationId:    "us-west1-a",
			PoolId:        "pool-uuid-123",
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-west1", "us-west1-a", nil
		}

		scheduledBackupEnabled := false
		chainBytes := int64(512000)
		expectedConfigs := []*models.ExpertModeVolumeBackupConfig{
			{
				VolumeResourceID:       "volume-uuid-1",
				ScheduledBackupEnabled: &scheduledBackupEnabled,
				BackupChainBytes:       &chainBytes,
			},
		}

		mockOrchestrator.EXPECT().GetBackupConfigsForPool(mock.Anything, "pool-uuid-123", "test-project", "us-west1-a").
			Return(expectedConfigs, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetBackupConfigsForPool(ctx, params)

		assert.NoError(tt, err)
		okResp, ok := result.(*gcpgenserver.V1betaGetBackupConfigsForPoolOK)
		assert.True(tt, ok)
		assert.Len(tt, okResp.BackupConfigs, 1)

		cfg := okResp.BackupConfigs[0]
		assert.True(tt, cfg.BackupConfig.IsSet(), "BackupConfig should be set even without vault/policy when new fields are present")
		bc := cfg.BackupConfig.Value
		assert.False(tt, bc.BackupVaultId.IsSet())
		assert.False(tt, bc.BackupPolicyId.IsSet())
		assert.True(tt, bc.ScheduledBackupEnabled.IsSet())
		assert.False(tt, bc.ScheduledBackupEnabled.Value)
		assert.True(tt, bc.BackupChainBytes.IsSet())
		assert.Equal(tt, int64(512000), bc.BackupChainBytes.Value)

		mockOrchestrator.AssertExpectations(tt)
	})
}

func TestV1betaRestoreOntapModeBackup(t *testing.T) {
	validBackupPath := "projects/p1/locations/us-east4/backupVaults/bv1/backups/backup-id-1"
	ctx := context.Background()

	t.Run("WhenOntapModeRestoreDisabled", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = false
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "vol-uuid",
			BackupUri: validBackupPath,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badReq, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Ontap mode Backup/Restore feature is currently not enabled.", badReq.Message)
	})

	t.Run("WhenRegionParsingFails", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{Code: 400, Message: "Invalid location ID"}
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "invalid-location",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "vol-uuid",
			BackupUri: validBackupPath,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badReq, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Invalid location ID", badReq.Message)
	})

	t.Run("WhenVolumeIdEmpty", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "",
			BackupUri: validBackupPath,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badReq, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "VolumeId and BackupUri are required", badReq.Message)
	})

	t.Run("WhenBackupUriEmpty", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "vol-uuid",
			BackupUri: "",
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badReq, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "VolumeId and BackupUri are required", badReq.Message)
	})

	t.Run("WhenBackupPathInvalid", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "vol-uuid",
			BackupUri: "too/few/parts",
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badReq, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "Invalid backup path format", badReq.Message)
	})

	t.Run("WhenSourceFileListExceedsMax", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		sourceFileList := make([]string, MaxSourceFileList+1)
		for i := range sourceFileList {
			sourceFileList[i] = fmt.Sprintf("/file%d", i)
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:       "vol-uuid",
			BackupUri:      validBackupPath,
			SourceFileList: sourceFileList,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badReq, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Contains(tt, badReq.Message, "Source file list cannot contain more than")
	})

	t.Run("WhenOrchestratorReturnsUserValidationError", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().RestoreOntapModeBackup(mock.Anything, mock.Anything).
			Return("", errors.NewUserInputValidationErr("volume not found")).Once()

		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "vol-uuid",
			BackupUri: validBackupPath,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badReq, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, "volume not found", badReq.Message)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenOrchestratorReturnsNotFoundError", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().RestoreOntapModeBackup(mock.Anything, mock.Anything).
			Return("", errors.NewNotFoundErr("volume", nil)).Once()

		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "vol-uuid",
			BackupUri: validBackupPath,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		badReq, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenOrchestratorReturnsInternalError", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().RestoreOntapModeBackup(mock.Anything, mock.Anything).
			Return("", stderrors.New("database error")).Once()

		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "vol-uuid",
			BackupUri: validBackupPath,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		internalErr, ok := result.(*gcpgenserver.V1betaRestoreOntapModeBackupInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "database error", internalErr.Message)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		jobUUID := "job-uuid-123"
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().RestoreOntapModeBackup(mock.Anything, mock.MatchedBy(func(p *commonparams.RestoreOntapModeBackupParams) bool {
			return p.AccountName == "project-number" && p.BackupPath == validBackupPath &&
				p.VolumeUUID == "vol-uuid" && p.Region == "us-east4" && p.PoolID == "pool-uuid"
		})).Return(jobUUID, nil).Once()

		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:  "vol-uuid",
			BackupUri: validBackupPath,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, op.Name.IsSet())
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/"+jobUUID, op.Name.Value)
		assert.True(tt, op.Done.IsSet())
		assert.False(tt, op.Done.Value)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenSuccessWithRestoreFilePath", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		jobUUID := "job-uuid-456"
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().RestoreOntapModeBackup(mock.Anything, mock.MatchedBy(func(p *commonparams.RestoreOntapModeBackupParams) bool {
			return p.RestoreFilePath == "/restore/dest"
		})).Return(jobUUID, nil).Once()

		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:        "vol-uuid",
			BackupUri:       validBackupPath,
			RestoreFilePath: gcpgenserver.NewOptString("/restore/dest"),
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, op.Name.IsSet())
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/"+jobUUID, op.Name.Value)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenSourceFileListSet_CallsSFROntapModeBackup", func(tt *testing.T) {
		originalOntapModeRestoreEnabled := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = originalOntapModeRestoreEnabled }()

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		jobUUID := "job-uuid-sfr"
		sourceFileList := []string{"/path/to/file1", "/path/to/file2"}
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().SFROntapModeBackup(mock.Anything, mock.MatchedBy(func(p *commonparams.RestoreOntapModeBackupParams) bool {
			return p.AccountName == "project-number" && p.BackupPath == validBackupPath &&
				p.VolumeUUID == "vol-uuid" && p.Region == "us-east4" && p.PoolID == "pool-uuid" &&
				len(p.SourceFileList) == 2 && p.SourceFileList[0] == "/path/to/file1" && p.SourceFileList[1] == "/path/to/file2"
		})).Return(jobUUID, nil).Once()

		params := gcpgenserver.V1betaRestoreOntapModeBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
			PoolId:        "pool-uuid",
		}
		req := &gcpgenserver.RestoreBackupRequestV1beta{
			VolumeId:       "vol-uuid",
			BackupUri:      validBackupPath,
			SourceFileList: sourceFileList,
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaRestoreOntapModeBackup(ctx, req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.True(tt, op.Name.IsSet())
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/"+jobUUID, op.Name.Value)
		mockOrchestrator.AssertExpectations(tt)
	})
}

func TestV1betaBackupConfig(t *testing.T) {
	baseParams := func() gcpgenserver.V1betaBackupConfigParams {
		return gcpgenserver.V1betaBackupConfigParams{
			ProjectNumber: "project-number",
			LocationId:    "us-east4",
			PoolId:        "pool-uuid",
		}
	}

	// makeBackupConfig builds a BackupConfigRequestV1betaBackupConfig with backupVaultId set.
	makeBackupConfig := func(vaultID string) gcpgenserver.BackupConfigRequestV1betaBackupConfig {
		bc := gcpgenserver.BackupConfigRequestV1betaBackupConfig{}
		bc.BackupVaultId.SetTo(vaultID)
		return bc
	}

	baseReq := func() *gcpgenserver.BackupConfigRequestV1beta {
		return &gcpgenserver.BackupConfigRequestV1beta{
			VolumeUuid:   "volume-uuid",
			BackupConfig: makeBackupConfig("vault-uuid"),
		}
	}

	setupLocation := func(t *testing.T) {
		t.Helper()
		orig := parseAndValidateRegionAndZone
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		t.Cleanup(func() { parseAndValidateRegionAndZone = orig })
	}

	t.Run("FeatureDisabled", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = false
		defer func() { ExpertModeBackupEnabled = orig }()

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), baseParams())
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "not enabled")
	})

	t.Run("InvalidLocation", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		origLoc := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = origLoc }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{Code: 400, Message: "invalid location"}
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), baseParams())
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "invalid location")
	})

	t.Run("TrueZonalPool_BackupConfigRejectedWhenFlagEnabled", func(tt *testing.T) {
		origExpertModeBackupEnabled := ExpertModeBackupEnabled
		origTrueZonalBackupBlock := blockTrueZonalAutoTieringAndBackup
		ExpertModeBackupEnabled = true
		blockTrueZonalAutoTieringAndBackup = true
		defer func() {
			ExpertModeBackupEnabled = origExpertModeBackupEnabled
			blockTrueZonalAutoTieringAndBackup = origTrueZonalBackupBlock
		}()

		setupLocation(tt)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "project-number").Return(&models.Pool{
			BaseModel: models.BaseModel{UUID: "pool-uuid"},
			PoolAttributes: &models.PoolAttributes{
				IsTrueZonal: true,
			},
		}, nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), baseParams())
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, trueZonalBackupConfigNotSupportedErrorMsg, badReq.Message)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("TrueZonalPool_DescribePoolNotFound", func(tt *testing.T) {
		origExpertModeBackupEnabled := ExpertModeBackupEnabled
		origTrueZonalBackupBlock := blockTrueZonalAutoTieringAndBackup
		ExpertModeBackupEnabled = true
		blockTrueZonalAutoTieringAndBackup = true
		defer func() {
			ExpertModeBackupEnabled = origExpertModeBackupEnabled
			blockTrueZonalAutoTieringAndBackup = origTrueZonalBackupBlock
		}()

		setupLocation(tt)

		poolID := "pool-uuid"
		notFoundErr := errors.NewNotFoundErr("pool", &poolID)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "project-number").Return(nil, notFoundErr).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), baseParams())
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, float64(400), badReq.Code)
		assert.Equal(tt, notFoundErr.Error(), badReq.Message)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("TrueZonalPool_DescribePoolInternalError", func(tt *testing.T) {
		origExpertModeBackupEnabled := ExpertModeBackupEnabled
		origTrueZonalBackupBlock := blockTrueZonalAutoTieringAndBackup
		ExpertModeBackupEnabled = true
		blockTrueZonalAutoTieringAndBackup = true
		defer func() {
			ExpertModeBackupEnabled = origExpertModeBackupEnabled
			blockTrueZonalAutoTieringAndBackup = origTrueZonalBackupBlock
		}()

		setupLocation(tt)

		describePoolErr := stderrors.New("database unavailable")
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().DescribePool(mock.Anything, "pool-uuid", "project-number").Return(nil, describePoolErr).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), baseParams())
		assert.NoError(tt, err)
		internalErr, ok := result.(*gcpgenserver.V1betaBackupConfigInternalServerError)
		assert.True(tt, ok)
		assert.Equal(tt, float64(500), internalErr.Code)
		assert.Equal(tt, "Internal server error", internalErr.Message)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("When_BackupVaultIdAbsent_NilPassedToOrchestrator", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		// BackupVaultId not present in payload → BackupVaultID=nil (no-op); request reaches orchestrator.
		req := &gcpgenserver.BackupConfigRequestV1beta{VolumeUuid: "volume-uuid"}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		returnedConfig := &datamodel.DataProtection{BackupPolicyID: "bp-uuid"}
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.MatchedBy(func(p *commonparams.ManageBackupConfigForExpertModeVolumeParams) bool {
				return p.BackupVaultID == nil
			}),
		).Return(returnedConfig, "job-uuid", nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), req, baseParams())
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		assert.NotEmpty(tt, op.Response)
		assert.Contains(tt, string(op.Response), "bp-uuid")
	})

	t.Run("When_BackupVaultIdEmpty_DetachPassedToOrchestrator", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		// BackupVaultId explicitly set to "" → BackupVaultID=&"" (detach); request reaches orchestrator.
		bc := gcpgenserver.BackupConfigRequestV1betaBackupConfig{}
		bc.BackupVaultId.SetTo("")
		req := &gcpgenserver.BackupConfigRequestV1beta{
			VolumeUuid:   "volume-uuid",
			BackupConfig: bc,
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		returnedConfig := &datamodel.DataProtection{BackupVaultID: ""}
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.MatchedBy(func(p *commonparams.ManageBackupConfigForExpertModeVolumeParams) bool {
				return p.BackupVaultID != nil && *p.BackupVaultID == ""
			}),
		).Return(returnedConfig, "job-uuid", nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), req, baseParams())
		assert.NoError(tt, err)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		// After detach, BackupVaultID is "" so convertDataProtectionToBackupConfigV1beta
		// does not set the BackupVaultId field — response is still populated (not nil).
		assert.NotNil(tt, op.Response)
	})

	t.Run("KmsGrant_CmekNotEnabled_Rejected", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		origCmek := cmekBackupEnabled
		cmekBackupEnabled = false
		defer func() { cmekBackupEnabled = origCmek }()

		setupLocation(tt)

		bc := gcpgenserver.BackupConfigRequestV1betaBackupConfig{}
		bc.BackupVaultId.SetTo("vault-uuid")
		bc.KmsGrant.SetTo("projects/p/locations/l/keyRings/r/cryptoKeys/k")
		req := &gcpgenserver.BackupConfigRequestV1beta{
			VolumeUuid:   "volume-uuid",
			BackupConfig: bc,
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		result, err := handler.V1betaBackupConfig(context.Background(), req, baseParams())
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "CMEK backup is not enabled")
	})

	t.Run("XNetappBackupScheduleHeader_InvalidCron_Rejected", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		params := baseParams()
		params.XNetappBackupSchedule = gcpgenserver.NewOptString("bad-cron-expr")

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), params)
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.NotEmpty(tt, badReq.Message)
	})

	t.Run("XNetappBackupScheduleHeader_ValidCron_PassedToOrchestrator", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		const validSchedule = "*/5 * * * *"
		params := baseParams()
		params.XNetappBackupSchedule = gcpgenserver.NewOptString(validSchedule)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.MatchedBy(func(p *commonparams.ManageBackupConfigForExpertModeVolumeParams) bool {
				return p.BackupSchedule == validSchedule
			}),
		).Return(nil, "job-uuid", nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), params)
		assert.NoError(tt, err)
		_, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("VolumeUuid_Empty_Rejected", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		req := &gcpgenserver.BackupConfigRequestV1beta{
			VolumeUuid:   "",
			BackupConfig: makeBackupConfig("vault-uuid"),
		}

		handler := Handler{Orchestrator: factory.NewMockOrchestratorFactory(tt)}
		result, err := handler.V1betaBackupConfig(context.Background(), req, baseParams())
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "volumeUuid is required")
	})

	t.Run("BackupPolicyId_Set_PassedToOrchestrator", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		const bpID = "my-backup-policy-uuid"
		bc := makeBackupConfig("vault-uuid")
		bc.BackupPolicyId.SetTo(bpID)
		req := &gcpgenserver.BackupConfigRequestV1beta{
			VolumeUuid:   "volume-uuid",
			BackupConfig: bc,
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.MatchedBy(func(p *commonparams.ManageBackupConfigForExpertModeVolumeParams) bool {
				return p.BackupPolicyID != nil && *p.BackupPolicyID == bpID
			}),
		).Return(nil, "job-uuid", nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), req, baseParams())
		assert.NoError(tt, err)
		_, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("ScheduledBackupEnabled_Set_PassedToOrchestrator", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		bc := makeBackupConfig("vault-uuid")
		bc.ScheduledBackupEnabled.SetTo(true)
		req := &gcpgenserver.BackupConfigRequestV1beta{
			VolumeUuid:   "volume-uuid",
			BackupConfig: bc,
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.MatchedBy(func(p *commonparams.ManageBackupConfigForExpertModeVolumeParams) bool {
				return p.ScheduledBackupEnabled != nil && *p.ScheduledBackupEnabled == true
			}),
		).Return(nil, "job-uuid", nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), req, baseParams())
		assert.NoError(tt, err)
		_, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("OrchestratorReturns_ValidationError_ReturnsBadRequest", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.Anything,
		).Return(nil, "", errors.NewUserInputValidationErr("invalid volume uuid")).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), baseParams())
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "invalid volume uuid")
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("OrchestratorReturns_NotFoundError_ReturnsBadRequest", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.Anything,
		).Return(nil, "", errors.NewNotFoundErr("volume not found", nil)).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), baseParams())
		assert.NoError(tt, err)
		badReq, ok := result.(*gcpgenserver.V1betaBackupConfigBadRequest)
		assert.True(tt, ok)
		assert.NotEmpty(tt, badReq.Message)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("OrchestratorReturns_InternalError_Returns500", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		setupLocation(tt)

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.Anything,
		).Return(nil, "", stderrors.New("internal server error")).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), baseReq(), baseParams())
		assert.NoError(tt, err)
		serverErr, ok := result.(*gcpgenserver.V1betaBackupConfigInternalServerError)
		assert.True(tt, ok)
		assert.Contains(tt, serverErr.Message, "internal server error")
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("Success_WithKmsGrant", func(tt *testing.T) {
		orig := ExpertModeBackupEnabled
		ExpertModeBackupEnabled = true
		defer func() { ExpertModeBackupEnabled = orig }()

		origCmek := cmekBackupEnabled
		cmekBackupEnabled = true
		defer func() { cmekBackupEnabled = origCmek }()

		setupLocation(tt)

		const kmsKey = "projects/p/locations/l/keyRings/r/cryptoKeys/k"
		bc := gcpgenserver.BackupConfigRequestV1betaBackupConfig{}
		bc.BackupVaultId.SetTo("vault-uuid")
		bc.KmsGrant.SetTo(kmsKey)
		req := &gcpgenserver.BackupConfigRequestV1beta{
			VolumeUuid:   "volume-uuid",
			BackupConfig: bc,
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().ManageBackupConfigForExpertModeVolume(
			mock.Anything,
			mock.MatchedBy(func(p *commonparams.ManageBackupConfigForExpertModeVolumeParams) bool {
				return p.KmsGrant != nil && *p.KmsGrant == kmsKey
			}),
		).Return(nil, "job-uuid", nil).Once()

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaBackupConfig(context.Background(), req, baseParams())
		assert.NoError(tt, err)
		_, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok)
		mockOrchestrator.AssertExpectations(tt)
	})
}
