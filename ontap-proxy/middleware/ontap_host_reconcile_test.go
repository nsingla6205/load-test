package middleware

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coreapiclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	ontapproxyutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestCoreFetchErrorToProxyHTTP(t *testing.T) {
	fe := func(code int, msg string) *ontapproxyutils.HTTPError {
		return &ontapproxyutils.HTTPError{Status: code, Message: msg}
	}
	t.Run("WhenCoreReturns404_ShouldPreserve404", func(t *testing.T) {
		assert.Equal(t, 404, coreFetchErrorToProxyHTTP(fe(404, "Pool not found")).Status)
	})
	t.Run("WhenCoreReturns400Deleting_ShouldPreserve400", func(t *testing.T) {
		assert.Equal(t, 400, coreFetchErrorToProxyHTTP(fe(400, "Pool is in deleting state")).Status)
	})
	t.Run("WhenCoreReturns400Creating_ShouldPreserve400", func(t *testing.T) {
		assert.Equal(t, 400, coreFetchErrorToProxyHTTP(fe(400, "Pool is in creating state")).Status)
	})
	t.Run("WhenCoreReturns500_ShouldMapTo502", func(t *testing.T) {
		assert.Equal(t, 502, coreFetchErrorToProxyHTTP(fe(500, "Internal server error")).Status)
	})
	t.Run("WhenCoreReturns503_ShouldPreserve503", func(t *testing.T) {
		assert.Equal(t, 503, coreFetchErrorToProxyHTTP(fe(503, "Service unavailable")).Status)
	})
	t.Run("WhenErrorIsOpaque_ShouldMapTo502WithHostNotFoundMessage", func(t *testing.T) {
		out := coreFetchErrorToProxyHTTP(fmt.Errorf("opaque"))
		assert.Equal(t, 502, out.Status)
		assert.Equal(t, "ONTAP cluster host not found", out.Message)
	})
}

func TestReconcilePoolCredentialsAfterHostLookupFailure(t *testing.T) {
	orig := fetchCredentialsFunc
	defer func() { fetchCredentialsFunc = orig }()

	// Round-trip: same shape as generateCacheKey(projectNumber, poolID, userName).
	cacheKey := generateCacheKey("1111", "pool-uuid", "gadmin")
	cache.AddToAuthDataCache(cacheKey, &models.AuthData{
		PoolID:      "pool-uuid",
		AccountName: "1111",
		AuthType:    models.USERNAME_PWD,
		Username:    "u",
		Password:    "p",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "gone.example.com"},
		},
	})
	t.Cleanup(func() { cache.RemoveFromAuthDataCache(cacheKey) })

	t.Run("WhenReconcileSucceeds_ShouldRepopulateCache", func(t *testing.T) {
		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			assert.Equal(t, "pool-uuid", poolDetails.PoolID)
			assert.Equal(t, "gadmin", poolDetails.UserName)
			assert.Equal(t, "", jwtToken)
			return &coreapiclient.OntapCredentialsV1{
				AuthType: coreapiclient.NewOptInt(1),
				Username: coreapiclient.NewOptString("u2"),
				Password: coreapiclient.NewOptString("p2"),
				OntapEndpoints: []coreapiclient.OntapEndpoint{
					{IP: "10.0.0.1", DNS: "fresh.example.com"},
				},
			}, nil
		}

		err := ReconcilePoolCredentialsAfterHostLookupFailure(context.Background(), cacheKey)
		require.NoError(t, err)

		cached, ok := cache.GetFromAuthDataCache(cacheKey)
		require.True(t, ok)
		assert.Equal(t, "fresh.example.com", cached.OntapEndpoints[0].DNS)
	})

	t.Run("WhenCoreReturnsServiceUnavailable_ShouldMapTo503", func(t *testing.T) {
		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return nil, &ontapproxyutils.HTTPError{Status: http.StatusServiceUnavailable, Message: "Service unavailable"}
		}
		err := ReconcilePoolCredentialsAfterHostLookupFailure(context.Background(), cacheKey)
		var pe *ProxyHTTPError
		require.Error(t, err)
		require.ErrorAs(t, err, &pe)
		assert.Equal(t, http.StatusServiceUnavailable, pe.Status)
	})

	t.Run("WhenReconcileInvokesCore_ShouldPassEmptyJWT", func(t *testing.T) {
		fetchCredentialsFunc = orig
		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			assert.Equal(t, "", jwtToken)
			return &coreapiclient.OntapCredentialsV1{
				AuthType: coreapiclient.NewOptInt(1),
				Username: coreapiclient.NewOptString("u3"),
				Password: coreapiclient.NewOptString("p3"),
				OntapEndpoints: []coreapiclient.OntapEndpoint{
					{IP: "10.0.0.2", DNS: "ctx-jwt.example.com"},
				},
			}, nil
		}
		err := ReconcilePoolCredentialsAfterHostLookupFailure(context.Background(), cacheKey)
		require.NoError(t, err)
		cached, ok := cache.GetFromAuthDataCache(cacheKey)
		require.True(t, ok)
		assert.Equal(t, "ctx-jwt.example.com", cached.OntapEndpoints[0].DNS)
	})

	t.Run("WhenCacheKeyIsInvalid_ShouldReturnBadGateway", func(t *testing.T) {
		err := ReconcilePoolCredentialsAfterHostLookupFailure(context.Background(), "bad-key")
		var pe *ProxyHTTPError
		require.ErrorAs(t, err, &pe)
		assert.Equal(t, http.StatusBadGateway, pe.Status)
		assert.Equal(t, "ONTAP cluster host not found", pe.Message)
	})
}

// TestReconcilePoolCredentialsAfterHostLookupFailure_DedicatedCacheKey is a
// regression test for a bug where reconciling a dedicated-capacity (hardware:...)
// cache key merged userName and clusterName into a single ClusterName value and
// dropped UserName entirely, causing Core to reject the reconcile request with an
// unknown cluster name (e.g. "gadmin:rohitj1-vsim48" instead of "rohitj1-vsim48").
func TestReconcilePoolCredentialsAfterHostLookupFailure_DedicatedCacheKey(t *testing.T) {
	orig := fetchCredentialsFunc
	defer func() { fetchCredentialsFunc = orig }()

	cacheKey := generateDedicatedCacheKey(generateCacheKey("531265380079", "pool-uuid", "gadmin"), "rohitj1-vsim48")
	cache.AddToAuthDataCache(cacheKey, &models.AuthData{
		PoolID:      "pool-uuid",
		AccountName: "531265380079",
		AuthType:    models.USER_CERTIFICATE,
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "gone.example.com"},
		},
	})
	t.Cleanup(func() { cache.RemoveFromAuthDataCache(cacheKey) })

	fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
		assert.Equal(t, "531265380079", poolDetails.ProjectNumber)
		assert.Equal(t, "pool-uuid", poolDetails.PoolID)
		assert.Equal(t, "gadmin", poolDetails.UserName)
		assert.Equal(t, "rohitj1-vsim48", poolDetails.ClusterName)
		return &coreapiclient.OntapCredentialsV1{
			AuthType: coreapiclient.NewOptInt(1),
			OntapEndpoints: []coreapiclient.OntapEndpoint{
				{IP: "10.0.0.1", DNS: "fresh.example.com"},
			},
		}, nil
	}

	err := ReconcilePoolCredentialsAfterHostLookupFailure(context.Background(), cacheKey)
	require.NoError(t, err)

	cached, ok := cache.GetFromAuthDataCache(cacheKey)
	require.True(t, ok)
	assert.Equal(t, "fresh.example.com", cached.OntapEndpoints[0].DNS)
}

func TestTryReconcileHostLookupErrorIfApplicable(t *testing.T) {
	orig := fetchCredentialsFunc
	defer func() { fetchCredentialsFunc = orig }()

	t.Run("WhenErrorIsNotHostLookup_ShouldReturnOriginalError", func(t *testing.T) {
		err := fmt.Errorf("connection refused")
		got := TryReconcileHostLookupErrorIfApplicable(context.Background(), err)
		assert.Equal(t, err, got)
	})

	t.Run("WhenNoSuchHostAndCacheKeyMissing_ShouldReturnOriginalError", func(t *testing.T) {
		err := fmt.Errorf("no such host")
		got := TryReconcileHostLookupErrorIfApplicable(context.Background(), err)
		assert.ErrorIs(t, got, err)
	})

	t.Run("WhenErrorIsNil_ShouldReturnNil", func(t *testing.T) {
		assert.Nil(t, TryReconcileHostLookupErrorIfApplicable(context.Background(), nil))
	})

	t.Run("WhenHostLookupAndCacheEntryMissing_ShouldStillAttemptReconcile", func(t *testing.T) {
		cacheKey := "reconcile-missing:pool-uuid:gadmin"
		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return nil, &ontapproxyutils.HTTPError{Status: 400, Message: "Pool is in deleting state"}
		}
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		inErr := fmt.Errorf("lookup bad.example.com: no such host")
		out := TryReconcileHostLookupErrorIfApplicable(ctx, inErr)
		var pe *ProxyHTTPError
		require.ErrorAs(t, out, &pe)
		assert.Equal(t, 400, pe.Status)
		assert.Contains(t, pe.Message, "deleting")
	})

	t.Run("WhenHostLookupAndCoreReturnsOpaqueError_ShouldMapToBadGateway", func(t *testing.T) {
		cacheKey := "reconcile-non-proxy:pool-uuid:gadmin"
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "reconcile-non-proxy",
			PoolID:      "pool-uuid",
		})
		t.Cleanup(func() { cache.RemoveFromAuthDataCache(cacheKey) })
		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return nil, fmt.Errorf("plain core failure")
		}
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		out := TryReconcileHostLookupErrorIfApplicable(ctx, fmt.Errorf("lookup bad.example.com: no such host"))
		var pe *ProxyHTTPError
		require.ErrorAs(t, out, &pe)
		assert.Equal(t, http.StatusBadGateway, pe.Status)
		assert.Equal(t, "ONTAP cluster host not found", pe.Message)
	})

	t.Run("WhenNoSuchHostAndCoreReturnsTypedHTTPError_ShouldReturnProxyHTTPError", func(t *testing.T) {
		cacheKey := "reconcile-try:pool-uuid:gadmin"
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "reconcile-try",
			PoolID:      "pool-uuid",
		})
		t.Cleanup(func() { cache.RemoveFromAuthDataCache(cacheKey) })

		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return nil, &ontapproxyutils.HTTPError{Status: 400, Message: "Pool is in deleting state"}
		}
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		inErr := fmt.Errorf("dial tcp: lookup bad.example.com: no such host")
		out := TryReconcileHostLookupErrorIfApplicable(ctx, inErr)
		var pe *ProxyHTTPError
		require.ErrorAs(t, out, &pe)
		assert.Equal(t, 400, pe.Status)
		assert.Contains(t, pe.Message, "deleting")
	})

	t.Run("WhenNoSuchHostAndReconcileSucceeds_ShouldReturnOriginalError", func(t *testing.T) {
		cacheKey := "reconcile-ok:pool-uuid:gadmin"
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "reconcile-ok",
			PoolID:      "pool-uuid",
			AuthType:    models.USERNAME_PWD,
			Username:    "u",
			Password:    "p",
			OntapEndpoints: []models.OntapEndpoint{
				{DNS: "old.example.com"},
			},
		})
		t.Cleanup(func() { cache.RemoveFromAuthDataCache(cacheKey) })
		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return &coreapiclient.OntapCredentialsV1{
				AuthType:       coreapiclient.NewOptInt(1),
				Username:       coreapiclient.NewOptString("u"),
				Password:       coreapiclient.NewOptString("p"),
				OntapEndpoints: []coreapiclient.OntapEndpoint{{DNS: "new.example.com"}},
			}, nil
		}
		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		inErr := fmt.Errorf("no such host")
		out := TryReconcileHostLookupErrorIfApplicable(ctx, inErr)
		assert.Equal(t, inErr, out)
	})

	t.Run("WhenContextDeadlineExceededAndCoreReturnsTypedHTTPError_ShouldReturnProxyHTTPError", func(t *testing.T) {
		cacheKey := "reconcile-timeout:pool-uuid:gadmin"
		cache.AddToAuthDataCache(cacheKey, &models.AuthData{
			AccountName: "reconcile-timeout",
			PoolID:      "pool-uuid",
		})
		t.Cleanup(func() { cache.RemoveFromAuthDataCache(cacheKey) })

		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return nil, &ontapproxyutils.HTTPError{Status: 400, Message: "Pool is in deleting state"}
		}

		ctx := context.WithValue(context.Background(), models.AuthDataKey, cacheKey)
		out := TryReconcileHostLookupErrorIfApplicable(ctx, context.DeadlineExceeded)

		var pe *ProxyHTTPError
		require.ErrorAs(t, out, &pe)
		assert.Equal(t, http.StatusBadRequest, pe.Status)
		assert.Contains(t, pe.Message, "deleting")
	})
}
