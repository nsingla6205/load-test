package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/runtime/middleware"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func stubParseRegionAndZone() func() {
	orig := parseAndValidateRegionAndZone
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		if locationID == "invalid location!" {
			return "", "", &gcpgenserver.Error{Code: 400, Message: "Invalid location"}
		}
		return locationID, "", nil
	}
	return func() { parseAndValidateRegionAndZone = orig }
}

func stubCVPFetch(pools []gcpgenserver.BatchPoolV1beta, err error) func() {
	orig := fetchBatchPoolsFromCVPFn
	fetchBatchPoolsFromCVPFn = func(_ context.Context, _ []string, _ gcpgenserver.V1betaBatchListPoolsParams, fieldSet map[string]bool) ([]gcpgenserver.BatchPoolV1beta, error) {
		result := make([]gcpgenserver.BatchPoolV1beta, 0, len(pools))
		for _, pool := range pools {
			p := pool
			applyBatchPoolFieldSelection(&p, fieldSet, false)
			result = append(result, p)
		}
		return result, err
	}
	return func() { fetchBatchPoolsFromCVPFn = orig }
}

func stubBatchAuth(pass bool) func() {
	orig := batchAuthFn
	batchAuthFn = func(_ *http.Request) middleware.Responder {
		if pass {
			return nil
		}
		return middleware.ResponderFunc(func(rw http.ResponseWriter, p runtime.Producer) {})
	}
	return func() { batchAuthFn = orig }
}

// authContext returns a context with an http.Header value so getHTTPRequestFromContext
// returns a non-nil request, allowing stubBatchAuth to control the auth outcome.
func authContext() context.Context {
	return context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, http.Header{})
}

func makeVCPPool(uuid, name, state string) *models.Pool {
	return &models.Pool{
		BaseModel:      models.BaseModel{UUID: uuid, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:           name,
		State:          state,
		ServiceLevel:   "PREMIUM",
		SizeInBytes:    2199023255552,
		PoolAttributes: &models.PoolAttributes{PrimaryZone: "us-east4-a"},
	}
}

func makeCVPBatchPool(uuid, resourceId, state string) gcpgenserver.BatchPoolV1beta {
	return gcpgenserver.BatchPoolV1beta{
		PoolId:           gcpgenserver.NewOptNilString(uuid),
		ResourceId:       gcpgenserver.NewOptNilString(resourceId),
		StoragePoolState: gcpgenserver.NewOptNilBatchPoolV1betaStoragePoolState(gcpgenserver.BatchPoolV1betaStoragePoolState(state)),
	}
}

// ============================================================
// Auth Tests
// ============================================================

func TestV1betaBatchListPools_Auth(t *testing.T) {
	t.Run("InvalidJWT_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(false)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		logger := log.NewLogger()
		ctx := context.WithValue(context.Background(), utilsmiddleware.ContextSLoggerKey, logger)
		ctx = context.WithValue(ctx, utilsmiddleware.HeaderContextKey, http.Header{
			"Authorization": []string{"invalid-jwt-token"},
		})

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsUnauthorized)
		require.True(tt, ok, "expected Unauthorized response")
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
		assert.Equal(tt, "Authentication failure", unauthRes.Message)
	})

	t.Run("NilHTTPRequest_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := context.Background()

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsUnauthorized)
		require.True(tt, ok, "nil httpReq must return Unauthorized")
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
		assert.Equal(tt, "Authentication failure", unauthRes.Message)
	})

	t.Run("NonHTTPHeaderValue_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, "not-http-header")

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsUnauthorized)
		require.True(tt, ok, "non-http.Header context value must return Unauthorized")
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
		assert.Equal(tt, "Authentication failure", unauthRes.Message)
	})
}

// ============================================================
// Validation Tests
// ============================================================

func TestV1betaBatchListPools_Validation(t *testing.T) {
	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "invalid location!"}

		res, err := handler.V1betaBatchListPools(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListPoolsBadRequest)
		assert.True(tt, ok)
	})

	t.Run("NilPoolUUIDs_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: nil}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListPoolsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "poolUUIDs is required")
	})

	t.Run("EmptyPoolUUIDs_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListPoolsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "poolUUIDs is required")
	})

	t.Run("TooManyUUIDs_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		uuids := make([]string, 1001)
		for i := range uuids {
			uuids[i] = "uuid"
		}
		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: uuids}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListPoolsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "at most 1000")
	})
}

func TestV1betaBatchListPools_VCPOnly(t *testing.T) {
	t.Run("Success_WithFields", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		pool := makeVCPPool("vcp-pool-1", "vcp-pool", "READY")
		mockOrch.On("GetPoolsByUUIDs", ctx, []string{"vcp-pool-1"}, mock.Anything).
			Return([]*models.Pool{pool}, nil)

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"vcp-pool-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListPoolsFieldsItem{"resourceId", "storagePoolState"},
		}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Pools, 1)
		assert.False(tt, okRes.Pools[0].PoolId.Set, "poolId should not be returned unless requested")
		assert.Equal(tt, "vcp-pool", okRes.Pools[0].ResourceId.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStoragePoolState("READY"), okRes.Pools[0].StoragePoolState.Value)
		assert.False(tt, okRes.Pools[0].ServiceLevel.Set, "not requested")
	})

	t.Run("VCPFails_Returns500", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetPoolsByUUIDs", ctx, []string{"uuid-1"}, mock.Anything).
			Return(nil, errors.New("database error"))

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListPoolsInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("NoFieldsRequested_ReturnsOnlyPoolId", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		pool := makeVCPPool("pool-1", "my-pool", "READY")
		mockOrch.On("GetPoolsByUUIDs", mock.Anything, []string{"pool-1"}, mock.Anything).
			Return([]*models.Pool{pool}, nil)

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"pool-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Pools, 1)
		assert.Equal(tt, "pool-1", okRes.Pools[0].PoolId.Value)
		assert.False(tt, okRes.Pools[0].ResourceId.Set)
		assert.False(tt, okRes.Pools[0].StoragePoolState.Set)
	})

	t.Run("EmptyResult_ReturnsEmptyArray", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetPoolsByUUIDs", mock.Anything, []string{"nonexistent"}, mock.Anything).
			Return([]*models.Pool{}, nil)

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"nonexistent"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsOK)
		require.True(tt, ok)
		assert.Empty(tt, okRes.Pools)
	})
}

func TestV1betaBatchListPools_Parallel(t *testing.T) {
	t.Run("BothSucceed_CombinesResults", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		cvpPool := makeCVPBatchPool("sde-pool-1", "sde-pool", "READY")
		restoreCVP := stubCVPFetch([]gcpgenserver.BatchPoolV1beta{cvpPool}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpPool := makeVCPPool("vcp-pool-1", "vcp-pool", "READY")
		mockOrch.On("GetPoolsByUUIDs", mock.Anything, []string{"vcp-pool-1", "sde-pool-1"}, mock.Anything).
			Return([]*models.Pool{vcpPool}, nil)

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"vcp-pool-1", "sde-pool-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListPoolsFieldsItem{"resourceId", "storagePoolState"},
		}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Pools, 2)

		resourceIDs := map[string]bool{}
		for _, p := range okRes.Pools {
			assert.False(tt, p.PoolId.Set, "poolId should not be returned unless requested")
			resourceIDs[p.ResourceId.Value] = true
		}
		assert.True(tt, resourceIDs["vcp-pool"], "VCP pool should be in results")
		assert.True(tt, resourceIDs["sde-pool"], "SDE pool should be in results")
	})

	t.Run("VCPFails_SDESucceeds_ReturnsSDEOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		cvpPool := makeCVPBatchPool("sde-pool-1", "sde-pool", "READY")
		restoreCVP := stubCVPFetch([]gcpgenserver.BatchPoolV1beta{cvpPool}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetPoolsByUUIDs", mock.Anything, []string{"uuid-1"}, mock.Anything).
			Return(nil, errors.New("VCP database error"))

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Pools, 1)
		assert.Equal(tt, "sde-pool-1", okRes.Pools[0].PoolId.Value, "poolId remains when no fields are requested")
	})

	t.Run("VCPSucceeds_SDEFails_ReturnsVCPOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubCVPFetch(nil, errors.New("CVP timeout"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpPool := makeVCPPool("vcp-pool-1", "vcp-pool", "READY")
		mockOrch.On("GetPoolsByUUIDs", mock.Anything, []string{"vcp-pool-1"}, mock.Anything).
			Return([]*models.Pool{vcpPool}, nil)

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"vcp-pool-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListPoolsFieldsItem{"resourceId"},
		}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Pools, 1)
		assert.False(tt, okRes.Pools[0].PoolId.Set, "poolId should not be returned unless requested")
		assert.Equal(tt, "vcp-pool", okRes.Pools[0].ResourceId.Value)
	})

	t.Run("BothFail_Returns500", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubCVPFetch(nil, errors.New("CVP down"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetPoolsByUUIDs", mock.Anything, []string{"uuid-1"}, mock.Anything).
			Return(nil, errors.New("VCP down"))

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListPoolsInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("BothReturnEmpty_ReturnsEmptyArray", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubCVPFetch([]gcpgenserver.BatchPoolV1beta{}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetPoolsByUUIDs", mock.Anything, []string{"nonexistent"}, mock.Anything).
			Return([]*models.Pool{}, nil)

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"nonexistent"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsOK)
		require.True(tt, ok)
		assert.Empty(tt, okRes.Pools)
	})

	t.Run("MultiplePools_FromBothSystems", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		cvpPools := []gcpgenserver.BatchPoolV1beta{
			makeCVPBatchPool("sde-1", "sde-pool-1", "READY"),
			makeCVPBatchPool("sde-2", "sde-pool-2", "CREATING"),
		}
		restoreCVP := stubCVPFetch(cvpPools, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpPools := []*models.Pool{
			makeVCPPool("vcp-1", "vcp-pool-1", "READY"),
			makeVCPPool("vcp-2", "vcp-pool-2", "UPDATING"),
			makeVCPPool("vcp-3", "vcp-pool-3", "DELETING"),
		}
		mockOrch.On("GetPoolsByUUIDs", mock.Anything, []string{"vcp-1", "vcp-2", "vcp-3", "sde-1", "sde-2"}, mock.Anything).
			Return(vcpPools, nil)

		req := &gcpgenserver.BatchPoolUUIDListV1beta{PoolUUIDs: []string{"vcp-1", "vcp-2", "vcp-3", "sde-1", "sde-2"}}
		params := gcpgenserver.V1betaBatchListPoolsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListPoolsFieldsItem{"resourceId"},
		}

		res, err := handler.V1betaBatchListPools(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListPoolsOK)
		require.True(tt, ok)
		assert.Len(tt, okRes.Pools, 5, "3 VCP + 2 SDE pools")
	})
}

// ============================================================
// buildFieldSet Tests
// ============================================================

func TestBuildFieldSet(t *testing.T) {
	t.Run("NilFields_ReturnsNil", func(tt *testing.T) {
		result := buildFieldSet(nil)
		assert.Nil(tt, result)
	})

	t.Run("EmptyFields_ReturnsNil", func(tt *testing.T) {
		result := buildFieldSet([]gcpgenserver.V1betaBatchListPoolsFieldsItem{})
		assert.Nil(tt, result)
	})

	t.Run("WithFields_ReturnsMap", func(tt *testing.T) {
		fields := []gcpgenserver.V1betaBatchListPoolsFieldsItem{"resourceId", "storagePoolState", "sizeInBytes"}
		result := buildFieldSet(fields)
		require.NotNil(tt, result)
		assert.True(tt, result["resourceId"])
		assert.True(tt, result["storagePoolState"])
		assert.True(tt, result["sizeInBytes"])
		assert.False(tt, result["description"])
	})
}

// ============================================================
// convertPoolToBatchPool Tests
// ============================================================

func TestConvertPoolToBatchPool(t *testing.T) {
	t.Run("NoFields_ReturnsOnlyPoolId", func(tt *testing.T) {
		pool := makeVCPPool("pool-1", "test-pool", "READY")
		bp := convertPoolToBatchPool(pool, nil)
		assert.Equal(tt, "pool-1", bp.PoolId.Value)
		assert.False(tt, bp.StoragePoolState.Set)
		assert.False(tt, bp.SizeInBytes.Set)
	})

	t.Run("BasicFields", func(tt *testing.T) {
		fieldSet := map[string]bool{
			"resourceId": true, "storagePoolState": true, "storagePoolStateDetails": true,
			"sizeInBytes": true, "serviceLevel": true, "description": true,
		}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p1"}, Name: "my-pool", State: "READY",
			StateDetails: "Available", ServiceLevel: "PREMIUM", SizeInBytes: 1000,
			Description: "desc", PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, "my-pool", bp.ResourceId.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStoragePoolStateREADY, bp.StoragePoolState.Value)
		assert.Equal(tt, "Available", bp.StoragePoolStateDetails.Value)
		assert.Equal(tt, float64(1000), bp.SizeInBytes.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaServiceLevelPREMIUM, bp.ServiceLevel.Value)
		assert.Equal(tt, "desc", bp.Description.Value)
	})

	t.Run("TimestampFields", func(tt *testing.T) {
		now := time.Now()
		deleted := now.Add(-time.Hour)
		fieldSet := map[string]bool{"createdAt": true, "updatedAt": true, "deletedAt": true}
		pool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "p1", CreatedAt: now, UpdatedAt: now, DeletedAt: &deleted},
			PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.CreatedAt.Set)
		assert.True(tt, bp.UpdatedAt.Set)
		assert.True(tt, bp.DeletedAt.Set)
	})

	t.Run("DeletedAtNil_AppearsAsNull", func(tt *testing.T) {
		fieldSet := map[string]bool{"deletedAt": true}
		pool := &models.Pool{BaseModel: models.BaseModel{UUID: "p1"}, PoolAttributes: &models.PoolAttributes{}}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.DeletedAt.Set, "requested field should be set")
		assert.True(tt, bp.DeletedAt.Null, "nil deletedAt should be null")
	})

	t.Run("PoolAttributes", func(tt *testing.T) {
		fieldSet := map[string]bool{
			"allocatedBytes": true, "numberOfVolumes": true, "zone": true,
			"secondaryZone": true, "ldapEnabled": true, "labels": true,
		}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p1"},
			PoolAttributes: &models.PoolAttributes{
				AllocatedBytes: 500, NumberOfVolumes: 10, PrimaryZone: "us-east4-a",
				SecondaryZone: "us-east4-b", IsRegionalHA: true, LdapEnabled: true,
				Labels: map[string]string{"env": "prod"},
			},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, float64(500), bp.AllocatedBytes.Value)
		assert.Equal(tt, int32(10), bp.NumberOfVolumes.Value)
		assert.Equal(tt, "us-east4-a", bp.Zone.Value)
		assert.Equal(tt, "us-east4-b", bp.SecondaryZone.Value)
		assert.True(tt, bp.LdapEnabled.Value)
		assert.True(tt, bp.Labels.Set)
	})

	t.Run("SecondaryZone_NotRegionalHA_AppearsAsNull", func(tt *testing.T) {
		fieldSet := map[string]bool{"secondaryZone": true}
		pool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "p1"},
			PoolAttributes: &models.PoolAttributes{SecondaryZone: "us-east4-b", IsRegionalHA: false},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.SecondaryZone.Set, "requested field should be set")
		assert.True(tt, bp.SecondaryZone.Null, "non-regional pool secondaryZone should be null")
	})

	t.Run("KmsConfig", func(tt *testing.T) {
		fieldSet := map[string]bool{"kmsConfigId": true, "kmsConfigResourceId": true, "encryptionType": true}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p1"},
			KmsConfig: &models.KmsConfig{
				BaseModel: models.BaseModel{UUID: "kms-1"}, KeyProjectID: "proj",
				KeyRing: "ring", KeyRingLocation: "loc", KeyName: "key",
			},
			PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, "kms-1", bp.KmsConfigId.Value)
		assert.True(tt, bp.KmsConfigResourceId.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaEncryptionType("CLOUD_KMS"), bp.EncryptionType.Value)
	})

	t.Run("NoKmsConfig_ServiceManaged", func(tt *testing.T) {
		fieldSet := map[string]bool{"encryptionType": true}
		pool := &models.Pool{BaseModel: models.BaseModel{UUID: "p1"}, PoolAttributes: &models.PoolAttributes{}}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaEncryptionType("SERVICE_MANAGED"), bp.EncryptionType.Value)
	})

	t.Run("ActiveDirectory", func(tt *testing.T) {
		fieldSet := map[string]bool{"activeDirectoryConfigId": true, "activeDirectoryResourceId": true}
		pool := &models.Pool{
			BaseModel:               models.BaseModel{UUID: "p1"},
			AccountName:             "test-project",
			ActiveDirectoryConfigId: "ad-1", ActiveDirectoryResourceId: "ad-res",
			PoolAttributes: &models.PoolAttributes{PrimaryZone: "us-east4-a"},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, "ad-1", bp.ActiveDirectoryConfigId.Value)
		assert.Equal(tt, "projects/test-project/locations/us-east4/activeDirectories/ad-res", bp.ActiveDirectoryResourceId.Value)
	})

	t.Run("ActiveDirectory_Empty_AppearsAsNull", func(tt *testing.T) {
		fieldSet := map[string]bool{"activeDirectoryConfigId": true, "activeDirectoryResourceId": true}
		pool := &models.Pool{BaseModel: models.BaseModel{UUID: "p1"}, PoolAttributes: &models.PoolAttributes{}}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.ActiveDirectoryConfigId.Set, "requested field should be set")
		assert.True(tt, bp.ActiveDirectoryConfigId.Null, "empty AD config should be null")
		assert.True(tt, bp.ActiveDirectoryResourceId.Set, "requested field should be set")
		assert.True(tt, bp.ActiveDirectoryResourceId.Null, "empty AD resource should be null")
	})

	t.Run("CustomPerformance", func(tt *testing.T) {
		fieldSet := map[string]bool{
			"customPerformanceEnabled": true, "totalThroughputMibps": true,
			"totalIops": true, "availableThroughputMibps": true,
		}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p1"},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled: true, Throughput: 256, Iops: 4096,
			},
			TotalThroughputMibps: 100, TotalIops: 1600,
			PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.CustomPerformanceEnabled.Value)
		assert.Equal(tt, float64(256), bp.TotalThroughputMibps.Value)
		assert.Equal(tt, float64(4096), bp.TotalIops.Value)
	})

	t.Run("NoCustomPerformance_UsesPoolDefaults", func(tt *testing.T) {
		fieldSet := map[string]bool{"totalThroughputMibps": true, "totalIops": true}
		pool := &models.Pool{
			BaseModel:            models.BaseModel{UUID: "p1"},
			TotalThroughputMibps: 128, TotalIops: 2048,
			PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, float64(128), bp.TotalThroughputMibps.Value)
		assert.Equal(tt, float64(2048), bp.TotalIops.Value)
	})

	t.Run("AutoTiering", func(tt *testing.T) {
		fieldSet := map[string]bool{
			"allowAutoTiering": true, "hotTierSizeInBytes": true,
			"enableHotTierAutoResize": true, "coldTierConsumption": true, "hotTierConsumption": true,
		}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p1"}, AllowAutoTiering: true,
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes: 1000, EnableHotTierAutoResize: true,
				ColdTierConsumption: 500, HotTierConsumption: 300,
			},
			PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.AllowAutoTiering.Value)
		assert.Equal(tt, float64(1000), bp.HotTierSizeInBytes.Value)
		assert.True(tt, bp.EnableHotTierAutoResize.Value)
		assert.Equal(tt, int64(500), bp.ColdTierConsumption.Value)
		assert.Equal(tt, int64(300), bp.HotTierConsumption.Value)
	})

	t.Run("AutoTiering_Disabled_AppearsAsNull", func(tt *testing.T) {
		fieldSet := map[string]bool{"hotTierSizeInBytes": true, "coldTierConsumption": true}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p1"}, AllowAutoTiering: false,
			PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.HotTierSizeInBytes.Set, "requested field should be set")
		assert.True(tt, bp.HotTierSizeInBytes.Null, "disabled auto-tiering should be null")
		assert.True(tt, bp.ColdTierConsumption.Set, "requested field should be set")
		assert.True(tt, bp.ColdTierConsumption.Null, "disabled auto-tiering should be null")
	})

	t.Run("StaticFields", func(tt *testing.T) {
		fieldSet := map[string]bool{
			"storageClass": true, "network": true, "region": true, "globalAccessAllowed": true,
			"billingLabels": true, "qosType": true, "satisfies_pzi": true, "satisfies_pzs": true,
			"unifiedPool": true, "type": true, "largeCapacity": true, "mode": true,
			"managedPool": true, "isHyperdiskAvailable": true,
		}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p1"}, Region: "us-east4",
			VendorSubNetID: "projects/123/global/networks/net", QosType: "auto",
			SatisfiesPzi: true, SatisfiesPzs: false, APIAccessMode: "DEFAULT",
			PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, "us-east4", bp.Region.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStorageClassHARDWARE, bp.StorageClass.Value)
		assert.Equal(tt, "projects/123/global/networks/net", bp.Network.Value)
		assert.Equal(tt, "auto", bp.QosType.Value)
		assert.True(tt, bp.SatisfiesPzi.Value)
		assert.False(tt, bp.SatisfiesPzs.Value)
		assert.True(tt, bp.BillingLabels.Set)
		assert.True(tt, bp.BillingLabels.Null)
		assert.True(tt, bp.GlobalAccessAllowed.Set)
		assert.True(tt, bp.GlobalAccessAllowed.Null)
		assert.True(tt, bp.UnifiedPool.Set)
		assert.True(tt, bp.UnifiedPool.Value)
		assert.True(tt, bp.Type.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaTypeUNIFIED, bp.Type.Value)
		assert.True(tt, bp.LargeCapacity.Set)
		assert.False(tt, bp.LargeCapacity.Value)
		assert.True(tt, bp.Mode.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaModeDEFAULT, bp.Mode.Value)
		assert.True(tt, bp.ManagedPool.Set)
		assert.True(tt, bp.ManagedPool.Value)
		assert.True(tt, bp.IsHyperdiskAvailable.Set)
		assert.False(tt, bp.IsHyperdiskAvailable.Value)
	})

	t.Run("IsHyperdiskAvailable_FlexPool_IsTrue", func(tt *testing.T) {
		fieldSet := map[string]bool{"isHyperdiskAvailable": true}
		pool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "p-flex"},
			ServiceLevel:   string(gcpgenserver.BatchPoolV1betaServiceLevelFLEX),
			PoolAttributes: &models.PoolAttributes{},
		}

		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.IsHyperdiskAvailable.Set)
		assert.True(tt, bp.IsHyperdiskAvailable.Value)
	})

	t.Run("StorageClass_FlexPool_IsSoftware", func(tt *testing.T) {
		fieldSet := map[string]bool{"storageClass": true}
		pool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "p-flex-storage"},
			ServiceLevel:   string(gcpgenserver.BatchPoolV1betaServiceLevelFLEX),
			PoolAttributes: &models.PoolAttributes{},
		}

		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.StorageClass.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStorageClassSOFTWARE, bp.StorageClass.Value)
	})

	t.Run("POPool_OmitsTypeAndUnifiedPool", func(tt *testing.T) {
		fieldSet := map[string]bool{"type": true, "unifiedPool": true, "storageClass": true, "mode": true}
		pool := &models.Pool{
			BaseModel:      models.BaseModel{UUID: "po-pool"},
			ServiceLevel:   string(gcpgenserver.BatchPoolV1betaServiceLevelEXTREME),
			APIAccessMode:  common.ONTAPMode,
			PoolAttributes: &models.PoolAttributes{},
		}

		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStorageClassHARDWARE, bp.StorageClass.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaModeONTAP, bp.Mode.Value)
		assert.False(tt, bp.Type.Set, "PO pools should omit type")
		assert.False(tt, bp.UnifiedPool.Set, "PO pools should omit unifiedPool")
	})

	t.Run("FullPayload_AllExpectedFieldsFromVCP", func(tt *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		deleted := now.Add(-time.Hour)
		fieldSet := map[string]bool{
			"poolId": true, "activeDirectoryConfigId": true, "activeDirectoryResourceId": true,
			"kmsConfigId": true, "kmsConfigResourceId": true, "network": true,
			"resourceId": true, "serviceLevel": true, "qosType": true, "sizeInBytes": true,
			"allocatedBytes": true, "totalThroughputMibps": true, "availableThroughputMibps": true,
			"numberOfVolumes": true, "storagePoolState": true, "storagePoolStateDetails": true,
			"createdAt": true, "updatedAt": true, "deletedAt": true, "storageClass": true,
			"description": true, "ldapEnabled": true, "encryptionType": true,
			"zone": true, "secondaryZone": true, "region": true, "globalAccessAllowed": true,
			"labels": true, "billingLabels": true, "allowAutoTiering": true,
			"hotTierSizeInBytes": true, "enableHotTierAutoResize": true,
			"satisfies_pzs": true, "satisfies_pzi": true, "assetLocationMetadata": true,
			"customPerformanceEnabled": true, "totalIops": true, "type": true,
			"unifiedPool": true, "largeCapacity": true, "coldTierConsumption": true,
			"hotTierConsumption": true, "mode": true, "managedPool": true,
			"isHyperdiskAvailable": true,
		}
		pool := &models.Pool{
			BaseModel:               models.BaseModel{UUID: "pool-full", CreatedAt: now, UpdatedAt: now, DeletedAt: &deleted},
			Name:                    "pool-name",
			Description:             "pool description",
			State:                   "READY",
			StateDetails:            "Available for use",
			ServiceLevel:            string(gcpgenserver.BatchPoolV1betaServiceLevelFLEX),
			SizeInBytes:             2199023255552,
			AccountName:             "project-123",
			Region:                  "",
			TotalThroughputMibps:    128,
			UtilizedThroughputMibps: 16,
			TotalIops:               2048,
			AllowAutoTiering:        true,
			VendorSubNetID:          "projects/123/global/networks/net",
			QosType:                 "auto",
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone:     "us-east4-a",
				SecondaryZone:   "us-east4-b",
				AllocatedBytes:  500,
				NumberOfVolumes: 10,
				IsRegionalHA:    true,
				Labels:          map[string]string{"env": "prod"},
				LdapEnabled:     true,
			},
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 256,
				Iops:       4096,
			},
			AutoTieringConfig: &models.AutoTieringConfig{
				HotTierSizeInBytes:      1000,
				EnableHotTierAutoResize: true,
				HotTierConsumption:      300,
				ColdTierConsumption:     500,
			},
			LargeCapacity:             true,
			KmsConfig:                 &models.KmsConfig{BaseModel: models.BaseModel{UUID: "kms-1"}, KeyProjectID: "proj", KeyRing: "ring", KeyRingLocation: "loc", KeyName: "key"},
			SatisfiesPzi:              true,
			SatisfiesPzs:              false,
			AssetMetadata:             &models.AssetMetadata{ChildAssets: []models.ChildAsset{{AssetType: "disk", AssetNames: []string{"disk-1"}}}},
			ActiveDirectoryConfigId:   "ad-1",
			ActiveDirectoryResourceId: "ad-res",
			APIAccessMode:             string(gcpgenserver.BatchPoolV1betaModeDEFAULT),
		}

		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.Equal(tt, "pool-full", bp.PoolId.Value)
		assert.Equal(tt, "ad-1", bp.ActiveDirectoryConfigId.Value)
		assert.Equal(tt, "projects/project-123/locations/us-east4/activeDirectories/ad-res", bp.ActiveDirectoryResourceId.Value)
		assert.Equal(tt, "kms-1", bp.KmsConfigId.Value)
		assert.Equal(tt, "projects/proj/locations/loc/keyRings/ring/cryptoKeys/key", bp.KmsConfigResourceId.Value)
		assert.Equal(tt, "projects/123/global/networks/net", bp.Network.Value)
		assert.Equal(tt, "pool-name", bp.ResourceId.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaServiceLevelFLEX, bp.ServiceLevel.Value)
		assert.Equal(tt, "auto", bp.QosType.Value)
		assert.Equal(tt, float64(2199023255552), bp.SizeInBytes.Value)
		assert.Equal(tt, float64(500), bp.AllocatedBytes.Value)
		assert.Equal(tt, float64(256), bp.TotalThroughputMibps.Value)
		assert.Equal(tt, float64(240), bp.AvailableThroughputMibps.Value)
		assert.Equal(tt, int32(10), bp.NumberOfVolumes.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStoragePoolStateREADY, bp.StoragePoolState.Value)
		assert.Equal(tt, "Available for use", bp.StoragePoolStateDetails.Value)
		assert.Equal(tt, now, bp.CreatedAt.Value)
		assert.Equal(tt, now, bp.UpdatedAt.Value)
		assert.Equal(tt, deleted, bp.DeletedAt.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStorageClassSOFTWARE, bp.StorageClass.Value)
		assert.Equal(tt, "pool description", bp.Description.Value)
		assert.True(tt, bp.LdapEnabled.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaEncryptionTypeCLOUDKMS, bp.EncryptionType.Value)
		assert.Equal(tt, "us-east4-a", bp.Zone.Value)
		assert.Equal(tt, "us-east4-b", bp.SecondaryZone.Value)
		assert.Equal(tt, "us-east4", bp.Region.Value)
		assert.True(tt, bp.GlobalAccessAllowed.Set)
		assert.True(tt, bp.GlobalAccessAllowed.Null)
		assert.Equal(tt, "prod", bp.Labels.Value["env"])
		assert.True(tt, bp.BillingLabels.Set)
		assert.True(tt, bp.BillingLabels.Null)
		assert.True(tt, bp.AllowAutoTiering.Value)
		assert.Equal(tt, float64(1000), bp.HotTierSizeInBytes.Value)
		assert.True(tt, bp.EnableHotTierAutoResize.Value)
		assert.False(tt, bp.SatisfiesPzs.Value)
		assert.True(tt, bp.SatisfiesPzi.Value)
		assert.True(tt, bp.AssetLocationMetadata.Set)
		require.Len(tt, bp.AssetLocationMetadata.Value.ChildAssets.Value, 1)
		assert.Equal(tt, "disk", bp.AssetLocationMetadata.Value.ChildAssets.Value[0].AssetType.Value)
		assert.True(tt, bp.CustomPerformanceEnabled.Value)
		assert.Equal(tt, float64(4096), bp.TotalIops.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaTypeUNIFIED, bp.Type.Value)
		assert.True(tt, bp.UnifiedPool.Value)
		assert.True(tt, bp.LargeCapacity.Value)
		assert.Equal(tt, int64(300), bp.HotTierConsumption.Value)
		assert.Equal(tt, int64(500), bp.ColdTierConsumption.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaModeDEFAULT, bp.Mode.Value)
		assert.True(tt, bp.ManagedPool.Value)
		assert.True(tt, bp.IsHyperdiskAvailable.Value)
	})

	t.Run("EmptyStringFields_AppearAsNull", func(tt *testing.T) {
		fieldSet := map[string]bool{
			"description": true, "serviceLevel": true, "storagePoolState": true,
			"storagePoolStateDetails": true, "network": true, "qosType": true, "mode": true,
		}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p-empty"},
		}

		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.Description.Set)
		assert.True(tt, bp.Description.Null)
		assert.True(tt, bp.ServiceLevel.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaServiceLevelSERVICELEVELUNSPECIFIED, bp.ServiceLevel.Value)
		assert.True(tt, bp.StoragePoolState.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStoragePoolStateSTATEUNSPECIFIED, bp.StoragePoolState.Value)
		assert.True(tt, bp.StoragePoolStateDetails.Set)
		assert.True(tt, bp.StoragePoolStateDetails.Null)
		assert.True(tt, bp.Network.Set)
		assert.True(tt, bp.Network.Null)
		assert.True(tt, bp.QosType.Set)
		assert.True(tt, bp.QosType.Null)
		assert.True(tt, bp.Mode.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaModeMODEUNSPECIFIED, bp.Mode.Value)
	})

	t.Run("AssetLocationMetadata", func(tt *testing.T) {
		fieldSet := map[string]bool{"assetLocationMetadata": true}
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "p1"},
			AssetMetadata: &models.AssetMetadata{
				ChildAssets: []models.ChildAsset{
					{AssetType: "disk", AssetNames: []string{"disk-1", "disk-2"}},
				},
			},
			PoolAttributes: &models.PoolAttributes{},
		}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.AssetLocationMetadata.Set)
		assert.True(tt, bp.AssetLocationMetadata.Value.ChildAssets.Set)
		require.Len(tt, bp.AssetLocationMetadata.Value.ChildAssets.Value, 1)
		assert.Equal(tt, "disk", bp.AssetLocationMetadata.Value.ChildAssets.Value[0].AssetType.Value)
		assert.Equal(tt, []string{"disk-1", "disk-2"}, bp.AssetLocationMetadata.Value.ChildAssets.Value[0].AssetNames)
	})

	t.Run("AssetLocationMetadata_Nil_AppearsAsNull", func(tt *testing.T) {
		fieldSet := map[string]bool{"assetLocationMetadata": true}
		pool := &models.Pool{BaseModel: models.BaseModel{UUID: "p1"}, PoolAttributes: &models.PoolAttributes{}}
		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.AssetLocationMetadata.Set, "requested field should be set")
		assert.True(tt, bp.AssetLocationMetadata.Null, "nil asset metadata should be null")
	})

	t.Run("NullBranches_WhenOptionalDataMissing", func(tt *testing.T) {
		fieldSet := map[string]bool{
			"allocatedBytes": true, "numberOfVolumes": true, "kmsConfigId": true,
			"kmsConfigResourceId": true, "zone": true, "region": true, "globalAccessAllowed": true,
			"labels": true, "billingLabels": true, "ldapEnabled": true, "enableHotTierAutoResize": true,
			"hotTierConsumption": true, "availableThroughputMibps": true, "type": true,
			"unifiedPool": true, "storageClass": true, "largeCapacity": true,
			"customPerformanceEnabled": true, "managedPool": true, "isHyperdiskAvailable": true,
		}
		pool := &models.Pool{
			BaseModel:               models.BaseModel{UUID: "p-null"},
			TotalThroughputMibps:    100,
			UtilizedThroughputMibps: 25,
		}

		bp := convertPoolToBatchPool(pool, fieldSet)
		assert.True(tt, bp.AllocatedBytes.Set)
		assert.True(tt, bp.AllocatedBytes.Null)
		assert.True(tt, bp.NumberOfVolumes.Set)
		assert.True(tt, bp.NumberOfVolumes.Null)
		assert.True(tt, bp.KmsConfigId.Set)
		assert.True(tt, bp.KmsConfigId.Null)
		assert.True(tt, bp.KmsConfigResourceId.Set)
		assert.True(tt, bp.KmsConfigResourceId.Null)
		assert.True(tt, bp.Region.Set)
		assert.True(tt, bp.Region.Null)
		assert.True(tt, bp.Zone.Set)
		assert.True(tt, bp.Zone.Null)
		assert.True(tt, bp.GlobalAccessAllowed.Set)
		assert.True(tt, bp.GlobalAccessAllowed.Null)
		assert.True(tt, bp.StorageClass.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStorageClassHARDWARE, bp.StorageClass.Value)
		assert.True(tt, bp.Type.Set)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaTypeUNIFIED, bp.Type.Value)
		assert.True(tt, bp.UnifiedPool.Set)
		assert.True(tt, bp.UnifiedPool.Value)
		assert.True(tt, bp.LargeCapacity.Set)
		assert.False(tt, bp.LargeCapacity.Value)
		assert.True(tt, bp.Labels.Set)
		assert.True(tt, bp.Labels.Null)
		assert.True(tt, bp.BillingLabels.Set)
		assert.True(tt, bp.BillingLabels.Null)
		assert.True(tt, bp.LdapEnabled.Set)
		assert.True(tt, bp.LdapEnabled.Null)
		assert.True(tt, bp.CustomPerformanceEnabled.Set)
		assert.False(tt, bp.CustomPerformanceEnabled.Value)
		assert.True(tt, bp.EnableHotTierAutoResize.Set)
		assert.True(tt, bp.EnableHotTierAutoResize.Null)
		assert.True(tt, bp.HotTierConsumption.Set)
		assert.True(tt, bp.HotTierConsumption.Null)
		assert.True(tt, bp.AvailableThroughputMibps.Set)
		assert.Equal(tt, float64(75), bp.AvailableThroughputMibps.Value)
		assert.True(tt, bp.ManagedPool.Set)
		assert.True(tt, bp.ManagedPool.Value)
		assert.True(tt, bp.IsHyperdiskAvailable.Set)
		assert.False(tt, bp.IsHyperdiskAvailable.Value)
	})
}

func TestEnsureRequestedFieldsPresent(t *testing.T) {
	t.Run("SetsRequestedUnsetFieldsToNull", func(tt *testing.T) {
		fields := map[string]bool{
			"poolId": true, "activeDirectoryConfigId": true, "activeDirectoryResourceId": true,
			"kmsConfigId": true, "kmsConfigResourceId": true, "network": true,
			"resourceId": true, "serviceLevel": true, "qosType": true, "sizeInBytes": true,
			"allocatedBytes": true, "totalThroughputMibps": true, "availableThroughputMibps": true,
			"numberOfVolumes": true, "storagePoolState": true, "storagePoolStateDetails": true,
			"createdAt": true, "updatedAt": true, "deletedAt": true, "storageClass": true,
			"description": true, "ldapEnabled": true, "encryptionType": true,
			"zone": true, "secondaryZone": true, "region": true, "globalAccessAllowed": true,
			"labels": true, "billingLabels": true, "allowAutoTiering": true,
			"hotTierSizeInBytes": true, "enableHotTierAutoResize": true,
			"satisfies_pzs": true, "satisfies_pzi": true, "assetLocationMetadata": true,
			"customPerformanceEnabled": true, "totalIops": true, "type": true,
			"unifiedPool": true, "largeCapacity": true, "coldTierConsumption": true,
			"hotTierConsumption": true, "mode": true, "managedPool": true,
			"isHyperdiskAvailable": true,
		}
		bp := gcpgenserver.BatchPoolV1beta{
			PoolId: gcpgenserver.NewOptNilString("pool-1"),
		}

		ensureRequestedFieldsPresent(&bp, fields, false)

		assert.True(tt, bp.PoolId.Value == "pool-1")
		assert.True(tt, bp.CreatedAt.Null)
		assert.True(tt, bp.UpdatedAt.Null)
		assert.True(tt, bp.DeletedAt.Null)
		assert.True(tt, bp.ResourceId.Null)
		assert.True(tt, bp.Description.Null)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaServiceLevelSERVICELEVELUNSPECIFIED, bp.ServiceLevel.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStoragePoolStateSTATEUNSPECIFIED, bp.StoragePoolState.Value)
		assert.True(tt, bp.StoragePoolStateDetails.Null)
		assert.True(tt, bp.SizeInBytes.Null)
		assert.True(tt, bp.AllocatedBytes.Null)
		assert.True(tt, bp.NumberOfVolumes.Null)
		assert.True(tt, bp.KmsConfigId.Null)
		assert.True(tt, bp.KmsConfigResourceId.Null)
		assert.True(tt, bp.ActiveDirectoryConfigId.Null)
		assert.True(tt, bp.ActiveDirectoryResourceId.Null)
		assert.True(tt, bp.EncryptionType.Null)
		assert.True(tt, bp.Region.Null)
		assert.True(tt, bp.Zone.Null)
		assert.True(tt, bp.SecondaryZone.Null)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStorageClassSTORAGECLASSUNSPECIFIED, bp.StorageClass.Value)
		assert.True(tt, bp.Network.Null)
		assert.True(tt, bp.LdapEnabled.Null)
		assert.True(tt, bp.GlobalAccessAllowed.Null)
		assert.True(tt, bp.Labels.Null)
		assert.True(tt, bp.BillingLabels.Null)
		assert.True(tt, bp.AllowAutoTiering.Null)
		assert.True(tt, bp.SatisfiesPzs.Null)
		assert.True(tt, bp.SatisfiesPzi.Null)
		assert.True(tt, bp.CustomPerformanceEnabled.Null)
		assert.True(tt, bp.TotalThroughputMibps.Null)
		assert.True(tt, bp.TotalIops.Null)
		assert.True(tt, bp.HotTierSizeInBytes.Null)
		assert.True(tt, bp.EnableHotTierAutoResize.Null)
		assert.True(tt, bp.QosType.Null)
		assert.True(tt, bp.ColdTierConsumption.Null)
		assert.True(tt, bp.HotTierConsumption.Null)
		assert.True(tt, bp.AvailableThroughputMibps.Null)
		assert.True(tt, bp.AssetLocationMetadata.Null)
		assert.True(tt, bp.UnifiedPool.Null)
		assert.True(tt, bp.LargeCapacity.Null)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaTypeSTORAGEPOOLTYPEUNSPECIFIED, bp.Type.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaModeMODEUNSPECIFIED, bp.Mode.Value)
		assert.True(tt, bp.ManagedPool.Null)
		assert.True(tt, bp.IsHyperdiskAvailable.Null)
	})
}

func TestApplyBatchPoolFieldSelection(t *testing.T) {
	t.Run("WithRequestedFields_ReturnsOnlyRequestedFields", func(tt *testing.T) {
		bp := gcpgenserver.BatchPoolV1beta{
			PoolId:           gcpgenserver.NewOptNilString("pool-1"),
			ResourceId:       gcpgenserver.NewOptNilString("my-pool"),
			ServiceLevel:     gcpgenserver.NewOptNilBatchPoolV1betaServiceLevel(gcpgenserver.BatchPoolV1betaServiceLevelPREMIUM),
			StoragePoolState: gcpgenserver.NewOptNilBatchPoolV1betaStoragePoolState(gcpgenserver.BatchPoolV1betaStoragePoolStateREADY),
		}

		applyBatchPoolFieldSelection(&bp, map[string]bool{"resourceId": true}, false)

		assert.False(tt, bp.PoolId.Set)
		assert.True(tt, bp.ResourceId.Set)
		assert.Equal(tt, "my-pool", bp.ResourceId.Value)
		assert.False(tt, bp.ServiceLevel.Set)
		assert.False(tt, bp.StoragePoolState.Set)
	})

	t.Run("WithRequestedMissingField_SetsNull", func(tt *testing.T) {
		bp := gcpgenserver.BatchPoolV1beta{
			PoolId: gcpgenserver.NewOptNilString("pool-1"),
		}

		applyBatchPoolFieldSelection(&bp, map[string]bool{"resourceId": true}, false)

		assert.False(tt, bp.PoolId.Set)
		assert.True(tt, bp.ResourceId.Set)
		assert.True(tt, bp.ResourceId.Null)
	})

	t.Run("WithoutFields_KeepsOnlyPoolId", func(tt *testing.T) {
		bp := gcpgenserver.BatchPoolV1beta{
			PoolId:           gcpgenserver.NewOptNilString("pool-1"),
			ResourceId:       gcpgenserver.NewOptNilString("my-pool"),
			StoragePoolState: gcpgenserver.NewOptNilBatchPoolV1betaStoragePoolState(gcpgenserver.BatchPoolV1betaStoragePoolStateREADY),
		}

		applyBatchPoolFieldSelection(&bp, nil, false)

		assert.True(tt, bp.PoolId.Set)
		assert.Equal(tt, "pool-1", bp.PoolId.Value)
		assert.False(tt, bp.ResourceId.Set)
		assert.False(tt, bp.StoragePoolState.Set)
	})
}

// ============================================================
// convertCVPBatchPoolToGCPBatchPool Tests
// ============================================================

func TestConvertCVPBatchPoolToGCPBatchPool(t *testing.T) {
	t.Run("AllCoreFields", func(tt *testing.T) {
		now := strfmt.DateTime(time.Now())
		size := float64(2199023255552)
		allocated := float64(1000000000000)
		numVolumes := int32(5)
		kmsID := "kms-uuid"
		kmsRes := "projects/p1/locations/l1/keyRings/r1/cryptoKeys/k1"
		adID := "ad-uuid"
		adRes := "ad-resource"
		poolID := "pool-1"
		resourceID := "my-pool"
		region := "us-east4"
		zone := "us-east4-a"
		network := "projects/123/global/networks/mynet"
		ldap := true
		allowAT := true
		throughput := float64(256)
		iops := float64(4096)
		managed := true
		billingLabels := map[string]string{"cost-center": "123"}
		poolType := "UNIFIED"

		desc := "desc"
		customPerfEnabled := true
		cvpPool := &cvpmodels.BatchPoolV1beta{
			PoolID: &poolID, CreatedAt: &now, UpdatedAt: &now,
			ResourceID: &resourceID, Description: &desc, CustomPerformanceEnabled: &customPerfEnabled, ServiceLevel: swag.String("EXTREME"),
			StoragePoolState: swag.String("READY"), StoragePoolStateDetails: swag.String("Available"),
			SizeInBytes: &size, AllocatedBytes: &allocated, NumberOfVolumes: &numVolumes,
			KmsConfigID: &kmsID, KmsConfigResourceID: &kmsRes,
			ActiveDirectoryConfigID: &adID, ActiveDirectoryResourceID: &adRes,
			EncryptionType: swag.String("CLOUD_KMS"), Region: &region, Zone: &zone, Network: &network,
			LdapEnabled: &ldap, AllowAutoTiering: &allowAT,
			TotalThroughputMibps: &throughput, TotalIops: &iops,
			ManagedPool: &managed, UnifiedPool: swag.Bool(false), Type: &poolType, BillingLabels: billingLabels,
		}

		result := convertCVPBatchPoolToGCPBatchPool(cvpPool)

		assert.Equal(tt, "pool-1", result.PoolId.Value)
		assert.True(tt, result.CreatedAt.Set)
		assert.True(tt, result.UpdatedAt.Set)
		assert.Equal(tt, "my-pool", result.ResourceId.Value)
		assert.Equal(tt, "desc", result.Description.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaServiceLevelEXTREME, result.ServiceLevel.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaStoragePoolStateREADY, result.StoragePoolState.Value)
		assert.Equal(tt, "Available", result.StoragePoolStateDetails.Value)
		assert.Equal(tt, size, result.SizeInBytes.Value)
		assert.Equal(tt, allocated, result.AllocatedBytes.Value)
		assert.Equal(tt, int32(5), result.NumberOfVolumes.Value)
		assert.Equal(tt, "kms-uuid", result.KmsConfigId.Value)
		assert.Equal(tt, kmsRes, result.KmsConfigResourceId.Value)
		assert.Equal(tt, "ad-uuid", result.ActiveDirectoryConfigId.Value)
		assert.Equal(tt, "ad-resource", result.ActiveDirectoryResourceId.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaEncryptionType("CLOUD_KMS"), result.EncryptionType.Value)
		assert.Equal(tt, region, result.Region.Value)
		assert.Equal(tt, zone, result.Zone.Value)
		assert.Equal(tt, network, result.Network.Value)
		assert.True(tt, result.LdapEnabled.Value)
		assert.True(tt, result.AllowAutoTiering.Value)
		assert.Equal(tt, throughput, result.TotalThroughputMibps.Value)
		assert.Equal(tt, iops, result.TotalIops.Value)
		assert.True(tt, result.BillingLabels.Set)
		assert.Equal(tt, "123", result.BillingLabels.Value["cost-center"])
		assert.True(tt, result.ManagedPool.Value)
		assert.False(tt, result.UnifiedPool.Value)
		assert.Equal(tt, gcpgenserver.BatchPoolV1betaTypeUNIFIED, result.Type.Value)
	})

	t.Run("MinimalFields_OnlyPoolId", func(tt *testing.T) {
		poolID := "minimal-pool"
		cvpPool := &cvpmodels.BatchPoolV1beta{PoolID: &poolID}
		result := convertCVPBatchPoolToGCPBatchPool(cvpPool)

		assert.Equal(tt, "minimal-pool", result.PoolId.Value)
		assert.False(tt, result.CreatedAt.Set)
		assert.False(tt, result.UpdatedAt.Set)
		assert.False(tt, result.DeletedAt.Set)
		assert.False(tt, result.ResourceId.Set)
		assert.False(tt, result.AllocatedBytes.Set)
		assert.False(tt, result.NumberOfVolumes.Set)
		assert.False(tt, result.KmsConfigId.Set)
		assert.False(tt, result.EncryptionType.Set)
		assert.False(tt, result.Region.Set)
		assert.False(tt, result.Zone.Set)
		assert.False(tt, result.Description.Set)
		assert.False(tt, result.CustomPerformanceEnabled.Set)
		assert.False(tt, result.Type.Set)
		assert.False(tt, result.ManagedPool.Set)
	})

	t.Run("WithDeletedAt", func(tt *testing.T) {
		now := strfmt.DateTime(time.Now())
		poolID := "del-pool"
		cvpPool := &cvpmodels.BatchPoolV1beta{PoolID: &poolID, DeletedAt: &now}
		result := convertCVPBatchPoolToGCPBatchPool(cvpPool)
		assert.True(tt, result.DeletedAt.Set)
	})

	t.Run("WithLabelsAndBillingLabels", func(tt *testing.T) {
		poolID := "label-pool"
		cvpPool := &cvpmodels.BatchPoolV1beta{
			PoolID:        &poolID,
			Labels:        map[string]string{"env": "prod", "team": "storage"},
			BillingLabels: map[string]string{"cost-center": "123"},
		}
		result := convertCVPBatchPoolToGCPBatchPool(cvpPool)
		assert.True(tt, result.Labels.Set)
		assert.True(tt, result.BillingLabels.Set)
	})

	t.Run("WithAssetLocationMetadata", func(tt *testing.T) {
		poolID := "asset-pool"
		cvpPool := &cvpmodels.BatchPoolV1beta{
			PoolID: &poolID,
			AssetLocationMetadata: &cvpmodels.BatchPoolV1betaAssetLocationMetadata{
				ChildAssets: []*cvpmodels.ChildAsset{
					{AssetType: "disk", AssetNames: []string{"disk-1"}},
					nil,
				},
			},
		}
		result := convertCVPBatchPoolToGCPBatchPool(cvpPool)
		assert.True(tt, result.AssetLocationMetadata.Set)
		assert.True(tt, result.AssetLocationMetadata.Value.ChildAssets.Set)
		assert.Len(tt, result.AssetLocationMetadata.Value.ChildAssets.Value, 1)
	})
}

// ============================================================
// fetchBatchPoolsFromCVP Tests
// ============================================================

func stubCreateClient(mockBatch *cvpBatch.MockClientService) func() {
	orig := createClient
	createClient = func(_ log.Logger, _ string) cvpapi.Cvp {
		return cvpapi.Cvp{Batch: mockBatch}
	}
	return func() { createClient = orig }
}

func TestFetchBatchPoolsFromCVP(t *testing.T) {
	t.Run("Success_ReturnsPools", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		size := float64(1000)
		resourceID := "my-pool"
		poolID1 := "sde-pool-1"
		poolID2 := "sde-pool-2"
		cvpResponse := &cvpBatch.V1betaBatchListPoolsOK{
			Payload: &cvpBatch.V1betaBatchListPoolsOKBody{
				Pools: []*cvpmodels.BatchPoolV1beta{
					{PoolID: &poolID1, ResourceID: &resourceID, SizeInBytes: &size},
					{PoolID: &poolID2},
				},
			},
		}
		mockBatch.On("V1betaBatchListPools", mock.AnythingOfType("*batch.V1betaBatchListPoolsParams")).
			Return(cvpResponse, nil)

		ctx := context.Background()
		params := gcpgenserver.V1betaBatchListPoolsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListPoolsFieldsItem{"resourceId", "sizeInBytes"},
		}

		result, err := fetchBatchPoolsFromCVP(ctx, []string{"sde-pool-1", "sde-pool-2"}, params, buildFieldSet(params.Fields))
		require.NoError(tt, err)
		require.Len(tt, result, 2)
		assert.False(tt, result[0].PoolId.Set)
		assert.Equal(tt, "my-pool", result[0].ResourceId.Value)
		assert.False(tt, result[1].PoolId.Set)
		assert.True(tt, result[1].ResourceId.Set)
		assert.True(tt, result[1].ResourceId.Null)
	})

	t.Run("Success_WithCorrelationID", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		poolID := "pool-1"
		cvpResponse := &cvpBatch.V1betaBatchListPoolsOK{
			Payload: &cvpBatch.V1betaBatchListPoolsOKBody{
				Pools: []*cvpmodels.BatchPoolV1beta{{PoolID: &poolID}},
			},
		}
		mockBatch.On("V1betaBatchListPools", mock.MatchedBy(func(p *cvpBatch.V1betaBatchListPoolsParams) bool {
			return p.XCorrelationID != nil && *p.XCorrelationID == "corr-123"
		})).Return(cvpResponse, nil)

		ctx := context.Background()
		params := gcpgenserver.V1betaBatchListPoolsParams{
			LocationId:     "us-east4",
			XCorrelationID: gcpgenserver.NewOptString("corr-123"),
		}

		result, err := fetchBatchPoolsFromCVP(ctx, []string{"pool-1"}, params, nil)
		require.NoError(tt, err)
		assert.Len(tt, result, 1)
	})

	t.Run("Success_NoFields_NoCorrelationID", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		poolID := "pool-1"
		cvpResponse := &cvpBatch.V1betaBatchListPoolsOK{
			Payload: &cvpBatch.V1betaBatchListPoolsOKBody{
				Pools: []*cvpmodels.BatchPoolV1beta{{PoolID: &poolID}},
			},
		}
		mockBatch.On("V1betaBatchListPools", mock.AnythingOfType("*batch.V1betaBatchListPoolsParams")).
			Return(cvpResponse, nil)

		ctx := context.Background()
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		result, err := fetchBatchPoolsFromCVP(ctx, []string{"pool-1"}, params, nil)
		require.NoError(tt, err)
		assert.Len(tt, result, 1)
	})

	t.Run("CVPReturnsError_PropagatesError", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		mockBatch.On("V1betaBatchListPools", mock.AnythingOfType("*batch.V1betaBatchListPoolsParams")).
			Return(nil, errors.New("connection refused"))

		ctx := context.Background()
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		result, err := fetchBatchPoolsFromCVP(ctx, []string{"pool-1"}, params, nil)
		assert.Nil(tt, result)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "CVP batch list pools failed")
		assert.Contains(tt, err.Error(), "connection refused")
	})

	t.Run("CVPReturnsNilPayload_ReturnsEmpty", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		cvpResponse := &cvpBatch.V1betaBatchListPoolsOK{Payload: nil}
		mockBatch.On("V1betaBatchListPools", mock.AnythingOfType("*batch.V1betaBatchListPoolsParams")).
			Return(cvpResponse, nil)

		ctx := context.Background()
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		result, err := fetchBatchPoolsFromCVP(ctx, []string{"pool-1"}, params, nil)
		require.NoError(tt, err)
		assert.Empty(tt, result)
	})

	t.Run("CVPReturnsNilPoolsInPayload_ReturnsEmpty", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		cvpResponse := &cvpBatch.V1betaBatchListPoolsOK{
			Payload: &cvpBatch.V1betaBatchListPoolsOKBody{Pools: nil},
		}
		mockBatch.On("V1betaBatchListPools", mock.AnythingOfType("*batch.V1betaBatchListPoolsParams")).
			Return(cvpResponse, nil)

		ctx := context.Background()
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		result, err := fetchBatchPoolsFromCVP(ctx, []string{"pool-1"}, params, nil)
		require.NoError(tt, err)
		assert.Empty(tt, result)
	})

	t.Run("CVPReturnsNilPoolInArray_SkipsNil", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		poolID1 := "pool-1"
		poolID3 := "pool-3"
		cvpResponse := &cvpBatch.V1betaBatchListPoolsOK{
			Payload: &cvpBatch.V1betaBatchListPoolsOKBody{
				Pools: []*cvpmodels.BatchPoolV1beta{
					{PoolID: &poolID1},
					nil,
					{PoolID: &poolID3},
				},
			},
		}
		mockBatch.On("V1betaBatchListPools", mock.AnythingOfType("*batch.V1betaBatchListPoolsParams")).
			Return(cvpResponse, nil)

		ctx := context.Background()
		params := gcpgenserver.V1betaBatchListPoolsParams{LocationId: "us-east4"}

		result, err := fetchBatchPoolsFromCVP(ctx, []string{"pool-1", "pool-2", "pool-3"}, params, nil)
		require.NoError(tt, err)
		require.Len(tt, result, 2, "nil pool should be skipped")
		assert.Equal(tt, "pool-1", result[0].PoolId.Value)
		assert.Equal(tt, "pool-3", result[1].PoolId.Value)
	})
}
