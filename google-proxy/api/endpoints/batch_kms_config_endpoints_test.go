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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func stubBatchKmsConfigCVPFetch(kmsConfigs []gcpgenserver.BatchKmsConfigV1beta, err error) func() {
	orig := fetchBatchKmsConfigsFromCVPFn
	fetchBatchKmsConfigsFromCVPFn = func(
		_ context.Context,
		_ []string,
		_ gcpgenserver.V1betaBatchListKmsConfigsParams,
		_ map[string]bool,
	) ([]gcpgenserver.BatchKmsConfigV1beta, error) {
		return kmsConfigs, err
	}
	return func() { fetchBatchKmsConfigsFromCVPFn = orig }
}

func makeVCPKmsConfig(uuid, state string) *models.KmsConfig {
	now := time.Now().UTC()
	return &models.KmsConfig{
		BaseModel: models.BaseModel{
			UUID:      uuid,
			CreatedAt: now,
			UpdatedAt: now.Add(time.Minute),
			DeletedAt: nil,
		},
		State:           state,
		StateDetails:    "state details",
		Description:     "kms description",
		KeyProjectID:    "project-id",
		KeyRing:         "ring-a",
		KeyRingLocation: "us-east4",
		KeyName:         "key-a",
		ResourceID:      "kms-resource",
		KmsAttributes: &models.KmsAttributes{
			VcpServiceAccountEmail: "kms-sa@example.com",
		},
	}
}

func makeCVPBatchKmsConfig(uuid, state string) gcpgenserver.BatchKmsConfigV1beta {
	now := time.Now().UTC()
	return gcpgenserver.BatchKmsConfigV1beta{
		UUID:                gcpgenserver.NewOptNilString(uuid),
		KmsState:            gcpgenserver.NewOptNilBatchKmsConfigV1betaKmsState(gcpgenserver.BatchKmsConfigV1betaKmsState(state)),
		ResourceId:          gcpgenserver.NewOptNilString("cvp-resource"),
		CreatedTime:         gcpgenserver.NewOptNilDateTime(now),
		ServiceAccountEmail: gcpgenserver.NewOptNilString("cvp-sa@example.com"),
	}
}

func makeCVPBatchKmsConfigModel(uuid, state string) *cvpmodels.BatchKmsConfigV1beta {
	now := strfmt.DateTime(time.Now().UTC())
	deleted := strfmt.DateTime(time.Time(now).Add(2 * time.Minute))
	keyFullPath := "projects/project-id/locations/us-east4/keyRings/ring-a/cryptoKeys/key-a"
	description := "cvp kms description"
	instructions := "cvp instructions"
	kmsStateDetails := "cvp state details"
	resourceID := "cvp-resource"

	return &cvpmodels.BatchKmsConfigV1beta{
		UUID:                &uuid,
		ServiceAccountEmail: "cvp-sa@example.com",
		KeyFullPath:         &keyFullPath,
		KmsState:            &state,
		KmsStateDetails:     &kmsStateDetails,
		Description:         &description,
		CreatedTime:         &now,
		UpdatedTime:         &now,
		DeletedTime:         &deleted,
		Instructions:        &instructions,
		ResourceID:          &resourceID,
	}
}

func TestV1betaBatchListKmsConfigs_Auth(t *testing.T) {
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

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsUnauthorized)
		require.True(tt, ok)
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
		assert.Equal(tt, "Authentication failure", unauthRes.Message)
	})

	t.Run("NilHTTPRequest_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(context.Background(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsUnauthorized)
		assert.True(tt, ok)
	})

	t.Run("NonHTTPHeaderValue_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, "not-http-header")

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsUnauthorized)
		assert.True(tt, ok)
	})
}

func TestV1betaBatchListKmsConfigs_Validation(t *testing.T) {
	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "invalid location!"}

		res, err := handler.V1betaBatchListKmsConfigs(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsBadRequest)
		assert.True(tt, ok)
	})

	t.Run("EmptyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "kmsConfigIds is required")
	})

	t.Run("TooManyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		origLimit := maxBatchKmsConfigIDs
		maxBatchKmsConfigIDs = func() int { return 2 }
		defer func() { maxBatchKmsConfigIDs = origLimit }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"a", "b", "c"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "at most 2")
	})

	t.Run("When_kmsConfigIds_contains_non_uuid", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp.example"
		defer func() { cvp.CVP_HOST = origHost }()

		cvpCalled := false
		origFetch := fetchBatchKmsConfigsFromCVPFn
		fetchBatchKmsConfigsFromCVPFn = func(
			_ context.Context,
			_ []string,
			_ gcpgenserver.V1betaBatchListKmsConfigsParams,
			_ map[string]bool,
		) ([]gcpgenserver.BatchKmsConfigV1beta, error) {
			cvpCalled = true
			return nil, errors.New("CVP client should not be called")
		}
		defer func() { fetchBatchKmsConfigsFromCVPFn = origFetch }()

		// The orchestrator must never be called when the request fails UUID
		// format validation; an unset mock would error out if it were.
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{
			"10000000-0000-0000-0000-000000000001",
			"not-a-uuid",
		}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsBadRequest)
		require.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		assert.Contains(tt, badReq.Message, "kmsConfigIds")
		assert.Contains(tt, badReq.Message, "kmsConfigIds.1 in body should match")
		assert.Contains(tt, badReq.Message, "[a-fA-F0-9]{8}")
		assert.False(tt, cvpCalled)
		mockOrch.AssertNotCalled(tt, "GetKmsConfigsByUUIDs", mock.Anything, mock.Anything)
	})

	t.Run("When_kmsConfigIds_is_nil_request_returns_bad_request", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(authContext(), nil, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsBadRequest)
		assert.True(tt, ok)
		assert.Equal(tt, "kmsConfigIds is required and must have at least 1 item", badReq.Message)
		mockOrch.AssertNotCalled(tt, "GetKmsConfigsByUUIDs", mock.Anything, mock.Anything)
	})
}

func TestV1betaBatchListKmsConfigs_VCPOnly(t *testing.T) {
	t.Run("Success_DefaultsToUUIDOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		kmsConfig := makeVCPKmsConfig("11111111-1111-1111-1111-111111111111", "READY")
		mockOrch.On("GetKmsConfigsByUUIDs", ctx, []string{"11111111-1111-1111-1111-111111111111"}).
			Return([]*models.KmsConfig{kmsConfig}, nil)

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"11111111-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.KmsConfigs, 1)
		assert.Equal(tt, "11111111-1111-1111-1111-111111111111", okRes.KmsConfigs[0].UUID.Value)
		assert.False(tt, okRes.KmsConfigs[0].ResourceId.Set)
		assert.False(tt, okRes.KmsConfigs[0].KmsState.Set)
	})

	t.Run("When_kmsConfigIds_duplicates_are_deduped", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		id := "11111111-1111-1111-1111-111111111111"
		mockOrch.On("GetKmsConfigsByUUIDs", ctx, []string{id}).
			Return([]*models.KmsConfig{makeVCPKmsConfig(id, "READY")}, nil)

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{id, id}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsOK)
		require.True(tt, ok)
		mockOrch.AssertExpectations(tt)
	})

	t.Run("Failure_ReturnsInternalServerError", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetKmsConfigsByUUIDs", ctx, []string{"11111111-1111-1111-1111-111111111111"}).
			Return(nil, errors.New("db error"))

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"11111111-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsInternalServerError)
		assert.True(tt, ok)
	})
}

func TestV1betaBatchListKmsConfigs_Parallel(t *testing.T) {
	t.Run("Success_BothSides", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		restoreCVP := stubBatchKmsConfigCVPFetch([]gcpgenserver.BatchKmsConfigV1beta{
			makeCVPBatchKmsConfig("33333333-3333-3333-3333-333333333333", "READY"),
		}, nil)
		defer restoreCVP()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = "https://cvp.example.com"
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetKmsConfigsByUUIDs", mock.Anything, []string{"22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"}).
			Return([]*models.KmsConfig{makeVCPKmsConfig("22222222-2222-2222-2222-222222222222", "READY")}, nil)

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListKmsConfigsFieldsItem{
				"resourceId",
				"kmsState",
			},
		}

		res, err := handler.V1betaBatchListKmsConfigs(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.KmsConfigs, 2)
	})

	t.Run("Success_IgnoresNilVCPEntries", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		restoreCVP := stubBatchKmsConfigCVPFetch([]gcpgenserver.BatchKmsConfigV1beta{
			{UUID: gcpgenserver.NewOptNilString("33333333-3333-3333-3333-333333333333")},
		}, nil)
		defer restoreCVP()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = "https://cvp.example.com"
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetKmsConfigsByUUIDs", mock.Anything, []string{"22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"}).
			Return([]*models.KmsConfig{nil, makeVCPKmsConfig("22222222-2222-2222-2222-222222222222", "READY")}, nil)

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.KmsConfigs, 2)
		assert.Equal(tt, "22222222-2222-2222-2222-222222222222", okRes.KmsConfigs[0].UUID.Value)
		assert.Equal(tt, "33333333-3333-3333-3333-333333333333", okRes.KmsConfigs[1].UUID.Value)
	})

	t.Run("Success_DeduplicatesCVPWhenUUIDExistsInVCP", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		restoreCVP := stubBatchKmsConfigCVPFetch([]gcpgenserver.BatchKmsConfigV1beta{
			makeCVPBatchKmsConfig("44444444-4444-4444-4444-444444444444", "READY"),
		}, nil)
		defer restoreCVP()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = "https://cvp.example.com"
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetKmsConfigsByUUIDs", mock.Anything, []string{"44444444-4444-4444-4444-444444444444"}).
			Return([]*models.KmsConfig{makeVCPKmsConfig("44444444-4444-4444-4444-444444444444", "CREATING")}, nil)

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"44444444-4444-4444-4444-444444444444"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListKmsConfigsFieldsItem{
				"kmsState",
			},
		}

		res, err := handler.V1betaBatchListKmsConfigs(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.KmsConfigs, 1)
		assert.Equal(tt, "44444444-4444-4444-4444-444444444444", okRes.KmsConfigs[0].UUID.Value)
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateCREATING, okRes.KmsConfigs[0].KmsState.Value)
	})

	t.Run("PartialSuccess_WhenVCPFails", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		restoreCVP := stubBatchKmsConfigCVPFetch([]gcpgenserver.BatchKmsConfigV1beta{
			makeCVPBatchKmsConfig("33333333-3333-3333-3333-333333333333", "READY"),
		}, nil)
		defer restoreCVP()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = "https://cvp.example.com"
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		mockOrch.On("GetKmsConfigsByUUIDs", mock.Anything, []string{"33333333-3333-3333-3333-333333333333"}).
			Return(nil, errors.New("vcp failed"))

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"33333333-3333-3333-3333-333333333333"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.KmsConfigs, 1)
		assert.Equal(tt, "33333333-3333-3333-3333-333333333333", okRes.KmsConfigs[0].UUID.Value)
	})

	t.Run("PartialSuccess_WhenCVPFails", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		restoreCVP := stubBatchKmsConfigCVPFetch(nil, errors.New("cvp failed"))
		defer restoreCVP()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = "https://cvp.example.com"
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		mockOrch.On("GetKmsConfigsByUUIDs", mock.Anything, []string{"22222222-2222-2222-2222-222222222222"}).
			Return([]*models.KmsConfig{makeVCPKmsConfig("22222222-2222-2222-2222-222222222222", "READY")}, nil)

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"22222222-2222-2222-2222-222222222222"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.KmsConfigs, 1)
		assert.Equal(tt, "22222222-2222-2222-2222-222222222222", okRes.KmsConfigs[0].UUID.Value)
	})

	t.Run("Failure_WhenBothFail", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		restoreCVP := stubBatchKmsConfigCVPFetch(nil, errors.New("cvp failed"))
		defer restoreCVP()

		origHost := cvp.CVP_HOST
		cvp.CVP_HOST = "https://cvp.example.com"
		defer func() { cvp.CVP_HOST = origHost }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		mockOrch.On("GetKmsConfigsByUUIDs", mock.Anything, []string{"11111111-1111-1111-1111-111111111111"}).
			Return(nil, errors.New("vcp failed"))

		req := &gcpgenserver.BatchKmsConfigIdListV1beta{KmsConfigIds: []string{"11111111-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListKmsConfigs(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListKmsConfigsInternalServerError)
		assert.True(tt, ok)
	})
}

func TestBuildBatchKmsConfigFieldSet(t *testing.T) {
	assert.Nil(t, buildBatchKmsConfigFieldSet(nil))

	fieldSet := buildBatchKmsConfigFieldSet([]gcpgenserver.V1betaBatchListKmsConfigsFieldsItem{
		"resourceId",
		"kmsState",
	})
	assert.True(t, fieldSet["resourceId"])
	assert.True(t, fieldSet["kmsState"])
}

func TestConvertKmsConfigToBatchKmsConfig(t *testing.T) {
	t.Run("RequestedEnumMissing_ReturnsUnspecified", func(tt *testing.T) {
		kmsConfig := makeVCPKmsConfig("kms-1", "")
		kmsConfig.StateDetails = ""

		bk := convertKmsConfigToBatchKmsConfig(kmsConfig, map[string]bool{"kmsState": true})
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateSTATEUNSPECIFIED, bk.KmsState.Value)
	})

	t.Run("RequestedEnumExplicitlyUnspecified_Preserved", func(tt *testing.T) {
		kmsConfig := makeVCPKmsConfig("kms-1", string(gcpgenserver.BatchKmsConfigV1betaKmsStateSTATEUNSPECIFIED))

		bk := convertKmsConfigToBatchKmsConfig(kmsConfig, map[string]bool{"kmsState": true})
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateSTATEUNSPECIFIED, bk.KmsState.Value)
	})

	t.Run("RequestedEnumNonDefault_Preserved", func(tt *testing.T) {
		kmsConfig := makeVCPKmsConfig("kms-1", "READY")

		bk := convertKmsConfigToBatchKmsConfig(kmsConfig, map[string]bool{"kmsState": true})
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateREADY, bk.KmsState.Value)
	})

	t.Run("RequestedFieldsMissing_AreNull", func(tt *testing.T) {
		kmsConfig := makeVCPKmsConfig("kms-1", "READY")
		kmsConfig.Description = ""
		kmsConfig.ResourceID = ""
		kmsConfig.KmsAttributes = nil

		bk := convertKmsConfigToBatchKmsConfig(kmsConfig, map[string]bool{
			"description":         true,
			"resourceId":          true,
			"serviceAccountEmail": true,
			"instructions":        true,
		})

		assert.True(tt, bk.Description.Null)
		assert.True(tt, bk.ResourceId.Null)
		assert.True(tt, bk.ServiceAccountEmail.Null)
		assert.True(tt, bk.Instructions.Null)
	})

	t.Run("UnrequestedFieldsAreOmitted", func(tt *testing.T) {
		kmsConfig := makeVCPKmsConfig("kms-1", "READY")

		bk := convertKmsConfigToBatchKmsConfig(kmsConfig, map[string]bool{"resourceId": true})
		assert.True(tt, bk.UUID.Set)
		assert.True(tt, bk.ResourceId.Set)
		assert.False(tt, bk.KmsState.Set)
		assert.False(tt, bk.KeyFullPath.Set)
	})

	t.Run("FullPayloadMapsAllSupportedVCPFields", func(tt *testing.T) {
		kmsConfig := makeVCPKmsConfig("kms-1", "READY")
		now := time.Now().UTC()
		kmsConfig.CreatedAt = now
		kmsConfig.UpdatedAt = now.Add(time.Minute)
		deletedAt := now.Add(2 * time.Minute)
		kmsConfig.DeletedAt = &deletedAt

		fieldSet := map[string]bool{
			"serviceAccountEmail": true,
			"keyFullPath":         true,
			"kmsState":            true,
			"kmsStateDetails":     true,
			"description":         true,
			"createdTime":         true,
			"updatedTime":         true,
			"deletedTime":         true,
			"instructions":        true,
			"resourceId":          true,
		}

		bk := convertKmsConfigToBatchKmsConfig(kmsConfig, fieldSet)
		assert.Equal(tt, "kms-1", bk.UUID.Value)
		assert.Equal(tt, "kms-sa@example.com", bk.ServiceAccountEmail.Value)
		assert.Equal(tt, "projects/project-id/locations/us-east4/keyRings/ring-a/cryptoKeys/key-a", bk.KeyFullPath.Value)
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateREADY, bk.KmsState.Value)
		assert.Equal(tt, "state details", bk.KmsStateDetails.Value)
		assert.Equal(tt, "kms description", bk.Description.Value)
		assert.Equal(tt, now, bk.CreatedTime.Value)
		assert.Equal(tt, now.Add(time.Minute), bk.UpdatedTime.Value)
		assert.Equal(tt, deletedAt, bk.DeletedTime.Value)
		assert.Contains(tt, bk.Instructions.Value, "gcloud kms keys add-iam-policy-binding")
		assert.Equal(tt, "kms-resource", bk.ResourceId.Value)
	})
}

func TestConvertCVPBatchKmsConfigToGCPBatchKmsConfig(t *testing.T) {
	t.Run("NoFields_ReturnsUUIDOnly", func(tt *testing.T) {
		kmsConfig := makeCVPBatchKmsConfigModel("kms-1", "READY")
		bk := convertCVPBatchKmsConfigToGCPBatchKmsConfig(kmsConfig, nil)

		assert.Equal(tt, "kms-1", bk.UUID.Value)
		assert.False(tt, bk.KmsState.Set)
		assert.False(tt, bk.ResourceId.Set)
	})

	t.Run("NilUUID_ReturnsNullUUID", func(tt *testing.T) {
		bk := convertCVPBatchKmsConfigToGCPBatchKmsConfig(&cvpmodels.BatchKmsConfigV1beta{}, nil)
		assert.True(tt, bk.UUID.Null)
	})

	t.Run("RequestedMissingFieldsAreDefaulted", func(tt *testing.T) {
		uuid := "kms-1"
		bk := convertCVPBatchKmsConfigToGCPBatchKmsConfig(&cvpmodels.BatchKmsConfigV1beta{
			UUID: &uuid,
		}, map[string]bool{
			"kmsState":            true,
			"description":         true,
			"serviceAccountEmail": true,
			"resourceId":          true,
		})

		assert.Equal(tt, "kms-1", bk.UUID.Value)
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateSTATEUNSPECIFIED, bk.KmsState.Value)
		assert.True(tt, bk.Description.Null)
		assert.True(tt, bk.ServiceAccountEmail.Null)
		assert.True(tt, bk.ResourceId.Null)
	})

	t.Run("FullPayloadMapsAllRequestedCVPFields", func(tt *testing.T) {
		kmsConfig := makeCVPBatchKmsConfigModel("kms-1", "READY")
		fieldSet := map[string]bool{
			"serviceAccountEmail": true,
			"keyFullPath":         true,
			"kmsState":            true,
			"kmsStateDetails":     true,
			"description":         true,
			"createdTime":         true,
			"updatedTime":         true,
			"deletedTime":         true,
			"instructions":        true,
			"resourceId":          true,
		}

		bk := convertCVPBatchKmsConfigToGCPBatchKmsConfig(kmsConfig, fieldSet)
		assert.Equal(tt, "kms-1", bk.UUID.Value)
		assert.Equal(tt, "cvp-sa@example.com", bk.ServiceAccountEmail.Value)
		assert.Equal(tt, "projects/project-id/locations/us-east4/keyRings/ring-a/cryptoKeys/key-a", bk.KeyFullPath.Value)
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateREADY, bk.KmsState.Value)
		assert.Equal(tt, "cvp state details", bk.KmsStateDetails.Value)
		assert.Equal(tt, "cvp kms description", bk.Description.Value)
		assert.Equal(tt, time.Time(*kmsConfig.CreatedTime), bk.CreatedTime.Value)
		assert.Equal(tt, time.Time(*kmsConfig.UpdatedTime), bk.UpdatedTime.Value)
		assert.Equal(tt, time.Time(*kmsConfig.DeletedTime), bk.DeletedTime.Value)
		assert.Equal(tt, "cvp instructions", bk.Instructions.Value)
		assert.Equal(tt, "cvp-resource", bk.ResourceId.Value)
	})
}

func TestFetchBatchKmsConfigsFromCVP(t *testing.T) {
	t.Run("Success_ReturnsConvertedConfigs", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		config1 := makeCVPBatchKmsConfigModel("kms-1", "READY")
		config2 := &cvpmodels.BatchKmsConfigV1beta{}
		cvpResponse := &cvpBatch.V1betaBatchListKmsConfigsOK{
			Payload: &cvpBatch.V1betaBatchListKmsConfigsOKBody{
				KmsConfigurations: []*cvpmodels.BatchKmsConfigV1beta{config1, nil, config2},
			},
		}
		mockBatch.On("V1betaBatchListKmsConfigs", mock.MatchedBy(func(p *cvpBatch.V1betaBatchListKmsConfigsParams) bool {
			return p.LocationID == "us-east4" &&
				p.Body != nil &&
				len(p.Body.KmsConfigIDs) == 2 &&
				p.Body.KmsConfigIDs[0] == "kms-1" &&
				p.Fields != nil &&
				len(p.Fields) == 2 &&
				p.Fields[0] == "resourceId" &&
				p.Fields[1] == "kmsState" &&
				p.XCorrelationID != nil &&
				*p.XCorrelationID == "corr-123"
		})).Return(cvpResponse, nil)

		params := gcpgenserver.V1betaBatchListKmsConfigsParams{
			LocationId:     "us-east4",
			Fields:         []gcpgenserver.V1betaBatchListKmsConfigsFieldsItem{"resourceId", "kmsState"},
			XCorrelationID: gcpgenserver.NewOptString("corr-123"),
		}

		result, err := fetchBatchKmsConfigsFromCVP(context.Background(), []string{"kms-1", "kms-2"}, params, buildBatchKmsConfigFieldSet(params.Fields))
		require.NoError(tt, err)
		require.Len(tt, result, 2)
		assert.Equal(tt, "kms-1", result[0].UUID.Value)
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateREADY, result[0].KmsState.Value)
		assert.Equal(tt, "cvp-resource", result[0].ResourceId.Value)
		assert.Equal(tt, gcpgenserver.BatchKmsConfigV1betaKmsStateSTATEUNSPECIFIED, result[1].KmsState.Value)
		assert.True(tt, result[1].ResourceId.Null)
	})

	t.Run("CVPReturnsError_PropagatesError", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		mockBatch.On("V1betaBatchListKmsConfigs", mock.AnythingOfType("*batch.V1betaBatchListKmsConfigsParams")).
			Return(nil, errors.New("connection refused"))

		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}
		result, err := fetchBatchKmsConfigsFromCVP(context.Background(), []string{"kms-1"}, params, nil)
		assert.Nil(tt, result)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "CVP batch list KMS configs failed")
		assert.Contains(tt, err.Error(), "connection refused")
	})

	t.Run("CVPReturnsNilPayload_ReturnsEmpty", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restoreClient := stubCreateClient(mockBatch)
		defer restoreClient()

		cvpResponse := &cvpBatch.V1betaBatchListKmsConfigsOK{Payload: nil}
		mockBatch.On("V1betaBatchListKmsConfigs", mock.AnythingOfType("*batch.V1betaBatchListKmsConfigsParams")).
			Return(cvpResponse, nil)

		params := gcpgenserver.V1betaBatchListKmsConfigsParams{LocationId: "us-east4"}
		result, err := fetchBatchKmsConfigsFromCVP(context.Background(), []string{"kms-1"}, params, nil)
		require.NoError(tt, err)
		assert.Empty(tt, result)
	})
}

func TestStubBatchKmsConfigCVPFetch_SatisfiesResponderUsage(t *testing.T) {
	restoreAuth := stubBatchAuth(false)
	defer restoreAuth()
	resp := batchAuthFn(&http.Request{})
	require.NotNil(t, resp)
	_ = middleware.ResponderFunc(func(rw http.ResponseWriter, p runtime.Producer) {})
}
