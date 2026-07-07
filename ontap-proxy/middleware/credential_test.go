package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	coreapiclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

var mockFetchCredentials = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
	return &coreapiclient.OntapCredentialsV1{
		AuthType: coreapiclient.NewOptInt(2),
		SecretID: coreapiclient.NewOptString("test-secret"),
		Password: coreapiclient.NewOptString("test-password"),
		Username: coreapiclient.NewOptString("test-user"),
		OntapEndpoints: []coreapiclient.OntapEndpoint{
			{IP: "1.2.3.4", DNS: "test.example.com"},
		},
	}, nil
}

func TestCredentialMiddleware(t *testing.T) {
	t.Run("WhenValidRequest_ShouldSucceed", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes", nil)
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := CredentialMiddleware()
		middleware(nextHandler).ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("WhenInvalidURI_ShouldReturn400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/invalid/path", nil)
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware := CredentialMiddleware()
		middleware(nextHandler).ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid URI")
	})

	t.Run("WhenFetchCredentialsFails_ShouldHandleError", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return nil, fmt.Errorf("pool not found")
		}

		// Use a different pool ID to avoid cache hits from previous tests
		req := httptest.NewRequest("GET", "/v1beta/projects/9999/locations/us-central1/pools/error-pool/ontap/api/storage/volumes", nil)
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware := CredentialMiddleware()
		middleware(nextHandler).ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "Pool not found")
	})

	t.Run("WhenIAMRoleValidationFails_ShouldReturn401", func(t *testing.T) {
		// Test that IAM role validation errors return 401 Unauthorized, not 400 Bad Request
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		// Valid URI but no IAM role header - should trigger IAM role validation error
		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/test-pool/ontap/api/storage/volumes", nil)
		// No x-google-iam-role header set
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware := CredentialMiddleware()
		middleware(nextHandler).ServeHTTP(w, req)

		// Should return 401 Unauthorized, not 400 Bad Request
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Unauthorized")
		assert.Contains(t, w.Body.String(), "Unable to determine IAM role")
		// Should NOT contain "Invalid URI"
		assert.NotContains(t, w.Body.String(), "Invalid URI")
	})

	t.Run("WhenIAMRoleValidationFails_WithEmptyHeader_ShouldReturn401", func(t *testing.T) {
		// Test that IAM role validation errors return 401 Unauthorized even with empty header
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		// Valid URI but empty IAM role header
		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/test-pool/ontap/api/storage/volumes", nil)
		req.Header.Set("x-google-iam-role", "")
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware := CredentialMiddleware()
		middleware(nextHandler).ServeHTTP(w, req)

		// Should return 401 Unauthorized, not 400 Bad Request
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Unauthorized")
		assert.Contains(t, w.Body.String(), "Unable to determine IAM role")
		// Should NOT contain "Invalid URI"
		assert.NotContains(t, w.Body.String(), "Invalid URI")
	})
}

func TestFetchAndCacheCredentials(t *testing.T) {
	t.Run("WhenCacheHit_ShouldReturnCachedData", func(t *testing.T) {
		poolDetails := &models.PoolDetails{
			PoolID:        "test-pool",
			AccountName:   "test-account",
			UserName:      "test-user",
			ProjectNumber: "test-project",
		}

		ctx := context.Background()
		cacheKey := "test-project:test-pool:test-user"

		testAuthData := &models.AuthData{
			AuthType: 2,
			PoolID:   "test-pool",
		}
		cache.AddToAuthDataCache(cacheKey, testAuthData)
		defer cache.RemoveFromAuthDataCache(cacheKey)

		logger := &log.MockLogger{}
		logger.On("InfoContext", mock.Anything, "Using cached auth data", "projectNumber", "test-project", "poolID", "test-pool", "accountName", "test-account", "userName", "test-user", "cacheKey", "test-project:test-pool:test-user").Return()

		err := fetchAndCacheCredentials(ctx, poolDetails, cacheKey, "test-jwt-token", logger)

		assert.NoError(t, err)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCacheMiss_ShouldFetchFromAPI", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		poolDetails := &models.PoolDetails{
			PoolID:        "test-pool",
			AccountName:   "test-account",
			UserName:      "test-user",
			ProjectNumber: "test-project",
		}

		ctx := context.Background()
		cacheKey := "test-project:test-pool:test-user"
		defer cache.RemoveFromAuthDataCache(cacheKey)

		logger := &log.MockLogger{}
		logger.On("InfoContext", mock.Anything, "Cache miss - fetching credentials from Core API", "projectNumber", "test-project", "poolID", "test-pool", "userName", "test-user").Return()
		logger.On("InfoContext", mock.Anything, "Credentials fetched from Core API and stored as AuthData", "poolID", "test-pool", "accountName", "test-account", "authType", 2, "cacheKey", "test-project:test-pool:test-user").Return()

		err := fetchAndCacheCredentials(ctx, poolDetails, cacheKey, "test-jwt-token", logger)

		assert.NoError(t, err)
		logger.AssertExpectations(t)

		cachedData, exists := cache.GetFromAuthDataCache(cacheKey)
		assert.True(t, exists)
		assert.Equal(t, "test-pool", cachedData.PoolID)
		assert.Equal(t, "test-account", cachedData.AccountName)
		assert.Equal(t, "test-user", cachedData.Username)
	})
}

func TestExtractPoolDetailsFromRequest(t *testing.T) {
	t.Run("WhenValidURI_WithGcnvAdminCredential_ValidationEnabled_ShouldReturnError", func(t *testing.T) {
		// Test with validation enabled - should return error when no IAM role header
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes", nil)

		poolDetails, err := extractPoolDetailsFromRequest(req, CredentialTypeExpertModeUser)

		// Should return error when validation is enabled and no IAM role
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to determine IAM role from context headers")
		assert.Nil(t, poolDetails)
	})

	t.Run("WhenValidURI_WithGcnvAdminCredential_ValidationDisabled_ShouldExtractDetails", func(t *testing.T) {
		// Test with validation disabled - should succeed with gadmin
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = false
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes", nil)

		poolDetails, err := extractPoolDetailsFromRequest(req, CredentialTypeExpertModeUser)

		// Should succeed when validation is disabled
		assert.NoError(t, err)
		assert.NotNil(t, poolDetails)
		assert.Equal(t, "1234", poolDetails.ProjectNumber)
		assert.Equal(t, "my-pool", poolDetails.PoolID)
		assert.Equal(t, "1234", poolDetails.AccountName)
		// Should return gadmin when validation disabled
		assert.Equal(t, "gadmin", poolDetails.UserName)
	})

	t.Run("WhenValidURI_WithAdminCredential_ShouldExtractDetailsWithAdminUser", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/snaplock/file/vol-uuid/path/to/file", nil)

		poolDetails, err := extractPoolDetailsFromRequest(req, CredentialTypeAdmin)

		assert.NoError(t, err)
		assert.Equal(t, "1234", poolDetails.ProjectNumber)
		assert.Equal(t, "my-pool", poolDetails.PoolID)
		assert.Equal(t, "1234", poolDetails.AccountName)
		assert.Equal(t, AdminUserName, poolDetails.UserName)
	})

	t.Run("WhenInvalidURI_ShouldReturnError", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/invalid/path", nil)

		poolDetails, err := extractPoolDetailsFromRequest(req, CredentialTypeExpertModeUser)

		assert.Error(t, err)
		assert.Nil(t, poolDetails)
		assert.Contains(t, err.Error(), "pool URI should match format")
	})

	t.Run("WhenValidURI_WithPrivilegedModeUser_ShouldMapToAdmin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes", nil)
		req.Header.Set("x-google-iam-role", "privOntap")

		poolDetails, err := extractPoolDetailsFromRequest(req, CredentialTypeExpertModeUser)

		assert.NoError(t, err)
		assert.NotNil(t, poolDetails)
		assert.NotEmpty(t, poolDetails.UserName)
	})
}

func TestValidatePoolUri(t *testing.T) {
	t.Run("WhenValidURI_ShouldReturnNil", func(t *testing.T) {
		uri := "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes"
		err := validatePoolUri(uri)
		assert.NoError(t, err)
	})

	t.Run("WhenInvalidFormat_ShouldReturnError", func(t *testing.T) {
		uri := "/invalid/path"
		err := validatePoolUri(uri)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "pool URI should match format")
	})

	t.Run("WhenURIMissingPoolID_ShouldReturnError", func(t *testing.T) {
		// URI that's missing the pool_id part - fails regex check
		uri := "/v1beta/projects/1234/locations/us-central1/pools"
		err := validatePoolUri(uri)
		assert.Error(t, err)
		// The regex check happens first and will catch this
		assert.Contains(t, err.Error(), "pool URI should match format")
	})
}

func TestHandleCredentialError(t *testing.T) {
	t.Run("WhenPoolNotFound_ShouldReturn404", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("pool not found"))

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "Pool not found")
	})

	t.Run("WhenInvalidPoolDetails_ShouldReturn400", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("invalid pool details"))

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid pool details")
	})

	t.Run("WhenIAMRoleValidationError_ShouldReturn401", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("unable to determine IAM role from context headers"))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Unauthorized")
		assert.Contains(t, w.Body.String(), "Unable to determine IAM role")
	})

	t.Run("WhenUnauthorizedAccess_ShouldReturn401", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("unauthorized access"))

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Unauthorized access")
	})

	t.Run("WhenForbiddenAccess_ShouldReturn403", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("forbidden access"))

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "Forbidden access")
	})

	t.Run("WhenInternalServerError_ShouldReturn500", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("internal server error"))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "Internal server error")
	})

	t.Run("WhenCoreAPICallFailed_ShouldReturn503", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("core API call failed"))

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.Contains(t, w.Body.String(), "Service unavailable")
	})

	t.Run("WhenUnknownError_ShouldReturn500", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("unknown error"))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "Internal server error")
	})
	t.Run("WhenPoolInCreatingState_ShouldReturn409", func(t *testing.T) {
		w := httptest.NewRecorder()

		handleCredentialError(w, fmt.Errorf("pool is in creating state: Pool is in creating state"))

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Pool is in creating state")
	})
}

func TestIsIAMRoleValidationError(t *testing.T) {
	t.Run("WhenErrorIsIAMRoleValidation_ShouldReturnTrue", func(t *testing.T) {
		err := fmt.Errorf("unable to determine IAM role from context headers")
		result := isIAMRoleValidationError(err)
		assert.True(t, result)
	})

	t.Run("WhenErrorContainsIAMRoleValidationMessage_ShouldReturnTrue", func(t *testing.T) {
		err := fmt.Errorf("some prefix: unable to determine IAM role from context headers: some suffix")
		result := isIAMRoleValidationError(err)
		assert.True(t, result)
	})

	t.Run("WhenErrorIsURIValidation_ShouldReturnFalse", func(t *testing.T) {
		err := fmt.Errorf("pool URI should match format: /v1beta/projects/project_number/locations/location_id/pools/pool_id")
		result := isIAMRoleValidationError(err)
		assert.False(t, result)
	})

	t.Run("WhenErrorIsGeneric_ShouldReturnFalse", func(t *testing.T) {
		err := fmt.Errorf("some other error")
		result := isIAMRoleValidationError(err)
		assert.False(t, result)
	})

	t.Run("WhenErrorIsNil_ShouldReturnFalse", func(t *testing.T) {
		result := isIAMRoleValidationError(nil)
		assert.False(t, result)
	})

	t.Run("WhenErrorIsEmpty_ShouldReturnFalse", func(t *testing.T) {
		err := fmt.Errorf("")
		result := isIAMRoleValidationError(err)
		assert.False(t, result)
	})
}

func TestCredentialMiddleware_ErrorDistinction(t *testing.T) {
	t.Run("WhenURIValidationError_ShouldReturn400", func(t *testing.T) {
		// Test that URI validation errors still return 400, not 401
		req := httptest.NewRequest("GET", "/invalid/path", nil)
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware := CredentialMiddleware()
		middleware(nextHandler).ServeHTTP(w, req)

		// Should return 400 Bad Request for URI validation errors
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid URI")
		// Should NOT contain IAM role error message
		assert.NotContains(t, w.Body.String(), "Unable to determine IAM role")
	})

	t.Run("WhenIAMRoleValidationError_ShouldReturn401", func(t *testing.T) {
		// Test that IAM role validation errors return 401, not 400
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		// Valid URI but no IAM role header
		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/test-pool/ontap/api/storage/volumes", nil)
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware := CredentialMiddleware()
		middleware(nextHandler).ServeHTTP(w, req)

		// Should return 401 Unauthorized for IAM role validation errors
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Unauthorized")
		assert.Contains(t, w.Body.String(), "Unable to determine IAM role")
		// Should NOT contain "Invalid URI"
		assert.NotContains(t, w.Body.String(), "Invalid URI")
	})

	t.Run("WhenValidationDisabled_WithNoIAMRole_ShouldSucceed", func(t *testing.T) {
		// Test that when validation is disabled, missing IAM role doesn't cause error
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = false
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		// Valid URI but no IAM role header - should succeed when validation disabled
		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/test-pool/ontap/api/storage/volumes", nil)
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		middleware := CredentialMiddleware()
		middleware(nextHandler).ServeHTTP(w, req)

		// Should succeed (200 OK) when validation is disabled
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestCredentialMiddleware_AdminPathUsesAdminCredentials(t *testing.T) {
	// Verify that when the request path is an admin path (exact normalized match),
	// the middleware selects admin credentials (poolDetails.UserName == admin).
	originalFetch := fetchCredentialsFunc
	defer func() { fetchCredentialsFunc = originalFetch }()

	var capturedPoolDetails *models.PoolDetails
	fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
		capturedPoolDetails = poolDetails
		return mockFetchCredentials(ctx, poolDetails, jwtToken, logger)
	}

	t.Run("admin path selects admin user when SMC enabled", func(t *testing.T) {
		old := smcOperationEnabled
		smcOperationEnabled = true
		defer func() { smcOperationEnabled = old }()

		capturedPoolDetails = nil
		req := httptest.NewRequest("GET", "/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/snapmirror/relationships", nil)
		w := httptest.NewRecorder()
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		CredentialMiddleware()(next).ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, capturedPoolDetails, "fetchCredentials should have been called")
		assert.Equal(t, AdminUserName, capturedPoolDetails.UserName, "admin path should use admin credentials when SMC enabled")
	})

	t.Run("admin path uses expert user when SMC disabled", func(t *testing.T) {
		old := smcOperationEnabled
		smcOperationEnabled = false
		defer func() { smcOperationEnabled = old }()

		capturedPoolDetails = nil
		req := httptest.NewRequest("GET", "/v1beta/projects/9997/locations/us-central1/pools/smc-off-pool/ontap/api/snapmirror/relationships", nil)
		w := httptest.NewRecorder()
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		CredentialMiddleware()(next).ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, capturedPoolDetails, "fetchCredentials should have been called")
		assert.NotEqual(t, AdminUserName, capturedPoolDetails.UserName, "admin path should use expert user when SMC disabled")
	})

	t.Run("non-admin path does not select admin user", func(t *testing.T) {
		// SMC can be true or false; non-admin path should never get admin credentials
		old := smcOperationEnabled
		smcOperationEnabled = true
		defer func() { smcOperationEnabled = old }()

		capturedPoolDetails = nil
		// Use a unique project/pool to avoid cache hit from other tests so fetchCredentials is called
		req := httptest.NewRequest("GET", "/v1beta/projects/9998/locations/us-central1/pools/non-admin-pool/ontap/api/storage/volumes", nil)
		w := httptest.NewRecorder()
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
		CredentialMiddleware()(next).ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, capturedPoolDetails, "fetchCredentials should have been called")
		assert.NotEqual(t, AdminUserName, capturedPoolDetails.UserName, "non-admin path should not use admin credentials")
	})
}

func TestIsAdminCredentialPath(t *testing.T) {
	t.Run("SnapMirror relationships use admin (normalized paths)", func(t *testing.T) {
		assert.True(t, isAdminCredentialPath("/api/snapmirror/relationships"))
		assert.True(t, isAdminCredentialPath("/api/snapmirror/relationships/{uuid}"))
		assert.True(t, isAdminCredentialPath("/api/snapmirror/relationships/{uuid}/transfers"))
		assert.True(t, isAdminCredentialPath("/api/snapmirror/relationships/{uuid}/transfers/{uuid}"))
		assert.True(t, isAdminCredentialPath("/api/snapmirror/relationships/{uuid}/restore"))
	})
	t.Run("access_tokens uses admin", func(t *testing.T) {
		assert.True(t, isAdminCredentialPath("/api/cluster/licensing/access_tokens"))
	})
	t.Run("object-stores use admin (normalized paths)", func(t *testing.T) {
		assert.True(t, isAdminCredentialPath("/api/snapmirror/object-stores/{uuid}"))
		assert.True(t, isAdminCredentialPath("/api/snapmirror/object-stores/{uuid}/endpoints/{uuid}"))
		assert.True(t, isAdminCredentialPath("/api/snapmirror/object-stores/{uuid}/endpoints/{uuid}/snapshots"))
		assert.True(t, isAdminCredentialPath("/api/snapmirror/object-stores/{uuid}/endpoints/{uuid}/snapshots/{uuid}"))
	})
	t.Run("other paths do not use admin", func(t *testing.T) {
		assert.False(t, isAdminCredentialPath("/api/storage/volumes"))
		assert.False(t, isAdminCredentialPath("/api/snapmirror/policies"))
		assert.False(t, isAdminCredentialPath("/api/other/snapmirror/relationships"))
		assert.False(t, isAdminCredentialPath("/api/cluster/licensing/licenses"))
		assert.False(t, isAdminCredentialPath(""))
	})
}

func TestGenerateCacheKey(t *testing.T) {
	t.Run("WhenGcnvAdminCredential_ShouldGenerateKey", func(t *testing.T) {
		key := generateCacheKey("project1", "pool1", "gcnvadmin")
		assert.Equal(t, "project1:pool1:gcnvadmin", key)
	})

	t.Run("WhenAdminCredential_ShouldGenerateKey", func(t *testing.T) {
		key := generateCacheKey("project1", "pool1", "admin")
		assert.Equal(t, "project1:pool1:admin", key)
	})
}

func TestParseAuthDataCacheKey(t *testing.T) {
	t.Run("roundtrip with generateCacheKey", func(t *testing.T) {
		key := generateCacheKey("531265380079", "77b35f7a-0985-1ffb-bcab-d076d35e689f", "gcnvadmin")
		pn, pid, un, err := parseAuthDataCacheKey(key)
		require.NoError(t, err)
		assert.Equal(t, "531265380079", pn)
		assert.Equal(t, "77b35f7a-0985-1ffb-bcab-d076d35e689f", pid)
		assert.Equal(t, "gcnvadmin", un)
	})
	t.Run("invalid key", func(t *testing.T) {
		_, _, _, err := parseAuthDataCacheKey("bad")
		assert.Error(t, err)
	})
}

func TestPoolDetailsFromCacheKey(t *testing.T) {
	t.Run("StandardKey_RebuildsPoolDetails", func(t *testing.T) {
		key := generateCacheKey("proj-1", "pool-1", "gadmin")
		pd, err := poolDetailsFromCacheKey(key)
		require.NoError(t, err)
		require.NotNil(t, pd)
		assert.Equal(t, "proj-1", pd.ProjectNumber)
		assert.Equal(t, "pool-1", pd.PoolID)
		assert.Equal(t, "proj-1", pd.AccountName)
		assert.Equal(t, "gadmin", pd.UserName)
	})

	t.Run("DedicatedKey_RebuildsPoolDetailsWithClusterName", func(t *testing.T) {
		key := generateDedicatedCacheKey("proj-1:pool-1", "cluster-a")
		pd, err := poolDetailsFromCacheKey(key)
		require.NoError(t, err)
		require.NotNil(t, pd)
		assert.Equal(t, "proj-1", pd.ProjectNumber)
		assert.Equal(t, "pool-1", pd.PoolID)
		assert.Equal(t, "proj-1", pd.AccountName)
		assert.Equal(t, "cluster-a", pd.ClusterName)
	})

	t.Run("DedicatedKey_WithEmptyClusterName_StillValid", func(t *testing.T) {
		key := generateDedicatedCacheKey("proj-1:pool-1", "")
		pd, err := poolDetailsFromCacheKey(key)
		require.NoError(t, err)
		require.NotNil(t, pd)
		assert.Equal(t, "proj-1", pd.ProjectNumber)
		assert.Equal(t, "pool-1", pd.PoolID)
		assert.Equal(t, "", pd.ClusterName)
	})

	t.Run("MalformedDedicatedKey_ReturnsError", func(t *testing.T) {
		// dedicated-namespaced key with too few segments.
		pd, err := poolDetailsFromCacheKey("hardware:proj-1:pool-1")
		require.Error(t, err)
		assert.Nil(t, pd)
		assert.Contains(t, err.Error(), "invalid hardware auth cache key")
	})
}

func TestGetStringValue(t *testing.T) {
	t.Run("WhenValueIsSet_ShouldReturnValue", func(t *testing.T) {
		opt := coreapiclient.NewOptString("test-value")
		result := getStringValue(opt)
		assert.Equal(t, "test-value", result)
	})

	t.Run("WhenValueIsNotSet_ShouldReturnEmpty", func(t *testing.T) {
		opt := coreapiclient.OptString{}
		result := getStringValue(opt)
		assert.Equal(t, "", result)
	})
}

func TestConvertOntapEndpoints(t *testing.T) {
	t.Run("WhenEndpointsProvided_ShouldConvert", func(t *testing.T) {
		apiEndpoints := []coreapiclient.OntapEndpoint{
			{IP: "1.2.3.4", DNS: "test1.example.com"},
			{IP: "5.6.7.8", DNS: "test2.example.com"},
		}

		endpoints := convertOntapEndpoints(apiEndpoints)

		assert.Len(t, endpoints, 2)
		assert.Equal(t, "1.2.3.4", endpoints[0].IP)
		assert.Equal(t, "test1.example.com", endpoints[0].DNS)
		assert.Equal(t, "5.6.7.8", endpoints[1].IP)
		assert.Equal(t, "test2.example.com", endpoints[1].DNS)
	})

	t.Run("WhenEmptyEndpoints_ShouldReturnEmpty", func(t *testing.T) {
		apiEndpoints := []coreapiclient.OntapEndpoint{}

		endpoints := convertOntapEndpoints(apiEndpoints)

		assert.Len(t, endpoints, 0)
	})

	t.Run("WhenNilEndpoints_ShouldReturnEmpty", func(t *testing.T) {
		var apiEndpoints []coreapiclient.OntapEndpoint

		endpoints := convertOntapEndpoints(apiEndpoints)

		assert.Len(t, endpoints, 0)
	})
}

func TestExtractJWTFromRequest(t *testing.T) {
	t.Run("WhenBearerToken_ShouldReturnFullHeader", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("authorization", "Bearer test-jwt-token")

		token := ExtractJWTFromRequest(req)

		assert.Equal(t, "Bearer test-jwt-token", token)
	})

	t.Run("WhenNoBearerPrefix_ShouldReturnOriginalHeader", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("authorization", "test-jwt-token")

		token := ExtractJWTFromRequest(req)

		assert.Equal(t, "test-jwt-token", token)
	})

	t.Run("WhenNoAuthorizationHeader_ShouldReturnEmpty", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)

		token := ExtractJWTFromRequest(req)

		assert.Equal(t, "", token)
	})

	t.Run("WhenEmptyAuthorizationHeader_ShouldReturnEmpty", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("authorization", "")

		token := ExtractJWTFromRequest(req)

		assert.Equal(t, "", token)
	})
}

func TestDetermineUserNameFromRBAC(t *testing.T) {
	// Helper function to set IAM role mapping config and reset once
	setIAMRoleMappingConfig := func(t *testing.T, configJSON string) func() {
		// Save original values
		originalConfig := iamRoleToUserMappingConfig
		originalMappingConfig := iamRoleMappingConfig

		// Set new config
		iamRoleToUserMappingConfig = configJSON

		// Reset once to allow re-initialization
		// Note: We reset sync.Once in tests to allow re-initialization with different configs
		// This is test-only code and necessary for proper test isolation
		// nolint:copylocks // Test-only: resetting sync.Once to allow re-initialization
		once = sync.Once{}

		// Load the new config
		LoadIAMRoleMappingConfig()

		// Return cleanup function
		return func() {
			iamRoleToUserMappingConfig = originalConfig
			iamRoleMappingConfig = originalMappingConfig
			// Reset once again to allow other tests to re-initialize if needed
			// nolint:copylocks // Test-only: resetting sync.Once to allow re-initialization
			once = sync.Once{}
		}
	}

	t.Run("WhenEmptyRoleUserName_WithValidationEnabled_ShouldReturnError", func(t *testing.T) {
		// Test case "" with iamRoleToUserValidationEnabled = true
		// This covers: case "" -> if iamRoleToUserValidationEnabled -> return error
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		req := httptest.NewRequest("GET", "/test", nil)
		// No IAM role header, so GetRBACUserFromRequest returns ""
		ctx := context.Background()

		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to determine IAM role from context headers")
		assert.Equal(t, "", userName)
	})

	t.Run("WhenEmptyRoleUserName_WithValidationDisabled_ShouldReturnGadmin", func(t *testing.T) {
		// Test case "" with iamRoleToUserValidationEnabled = false
		// This covers: case "" -> return env.ExpertModeUserSuffix
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = false
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		req := httptest.NewRequest("GET", "/test", nil)
		// No IAM role header, so GetRBACUserFromRequest returns ""
		ctx := context.Background()

		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, env.ExpertModeUserSuffix, userName)
		assert.Equal(t, "gadmin", userName)
	})

	t.Run("WhenRoleUserNameIsPrivExpertModeUserSuffix_ShouldReturnAdmin", func(t *testing.T) {
		// Test case env.PrivExpertModeUserSuffix (padmin)
		// This covers: case env.PrivExpertModeUserSuffix -> return AdminUserName
		// Set up config to map a role to "padmin"
		cleanup := setIAMRoleMappingConfig(t, `{"testPrivRole": "padmin"}`)
		defer cleanup()

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("x-google-iam-role", "testPrivRole")
		ctx := context.Background()

		// Verify GetRBACUserFromRequest returns "padmin"
		rbacUser := GetRBACUserFromRequest(ctx, req)
		assert.Equal(t, "padmin", rbacUser)

		// Test determineUserNameFromRBAC
		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, AdminUserName, userName)
		assert.Equal(t, "admin", userName)
	})

	t.Run("WhenRoleUserNameIsCustomValue_ShouldReturnRoleUserName", func(t *testing.T) {
		// Test default case - when roleUserName is not empty and not env.PrivExpertModeUserSuffix
		// This covers: default -> return roleUserName
		// Set up config to map a role to a custom user (not padmin)
		cleanup := setIAMRoleMappingConfig(t, `{"testCustomRole": "customuser"}`)
		defer cleanup()

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("x-google-iam-role", "testCustomRole")
		ctx := context.Background()

		// Verify GetRBACUserFromRequest returns "customuser"
		rbacUser := GetRBACUserFromRequest(ctx, req)
		assert.Equal(t, "customuser", rbacUser)

		// Test determineUserNameFromRBAC - should return the custom user
		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, rbacUser, userName)
		assert.Equal(t, "customuser", userName)
	})

	t.Run("WhenRoleUserNameIsGadmin_ShouldReturnGadmin", func(t *testing.T) {
		// Test default case with gadmin
		// This covers: default -> return roleUserName (when roleUserName is "gadmin")
		// Set up config to map a role to "gadmin"
		cleanup := setIAMRoleMappingConfig(t, `{"testGadminRole": "gadmin"}`)
		defer cleanup()

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("x-google-iam-role", "testGadminRole")
		ctx := context.Background()

		// Verify GetRBACUserFromRequest returns "gadmin"
		rbacUser := GetRBACUserFromRequest(ctx, req)
		assert.Equal(t, "gadmin", rbacUser)

		// Test determineUserNameFromRBAC - should return "gadmin"
		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, rbacUser, userName)
		assert.Equal(t, "gadmin", userName)
	})

	t.Run("WhenIAMRoleMapsToPadmin_WithConfig_ShouldReturnAdmin", func(t *testing.T) {
		// Test with privOntap role mapped to padmin
		// This covers: case env.PrivExpertModeUserSuffix -> return AdminUserName
		cleanup := setIAMRoleMappingConfig(t, `{"privOntap": "padmin"}`)
		defer cleanup()

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("x-google-iam-role", "privOntap")
		ctx := context.Background()

		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, "admin", userName)
	})

	t.Run("WhenIAMRoleMapsToGadmin_WithConfig_ShouldReturnGadmin", func(t *testing.T) {
		// Test with ontap role mapped to gadmin
		// This covers: default -> return roleUserName (when roleUserName is "gadmin")
		cleanup := setIAMRoleMappingConfig(t, `{"ontap": "gadmin"}`)
		defer cleanup()

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("x-google-iam-role", "ontap")
		ctx := context.Background()

		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, "gadmin", userName)
	})

	t.Run("WhenIAMRoleNotInConfig_ShouldReturnGadmin", func(t *testing.T) {
		// Test with empty config - unknown role should default to gadmin
		// This covers: default -> return roleUserName (when MapIAMRoleToRBACUser defaults to gadmin)
		cleanup := setIAMRoleMappingConfig(t, `{}`)
		defer cleanup()

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("x-google-iam-role", "unknownRole")
		ctx := context.Background()

		// MapIAMRoleToRBACUser defaults to gadmin when role not in config
		rbacUser := GetRBACUserFromRequest(ctx, req)
		assert.Equal(t, "gadmin", rbacUser)

		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.NoError(t, err)
		assert.Equal(t, "gadmin", userName)
	})

	t.Run("WhenEmptyIAMRoleHeader_WithValidationEnabled_ShouldReturnError", func(t *testing.T) {
		// Test case "" with empty header and validation enabled
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("x-google-iam-role", "")
		ctx := context.Background()

		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to determine IAM role from context headers")
		assert.Equal(t, "", userName)
	})

	t.Run("WhenIAMRoleHeaderIsWhitespace_WithValidationEnabled_ShouldReturnError", func(t *testing.T) {
		// Test case "" with whitespace header and validation enabled
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("x-google-iam-role", "   ")
		ctx := context.Background()

		userName, err := determineUserNameFromRBAC(ctx, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to determine IAM role from context headers")
		assert.Equal(t, "", userName)
	})
}
