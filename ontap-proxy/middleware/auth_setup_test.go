package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	coreapiclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestSetupCredentialsForHandler(t *testing.T) {
	t.Run("WhenValidParams_WithAdminCredential_ShouldSucceed", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		ctx := context.Background()

		resultCtx, err := SetupCredentialsForHandler(ctx, "1234", "test-pool", CredentialTypeAdmin)

		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)

		// Verify cache key is set in context
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, "1234:test-pool:admin", cacheKey)

		// Verify auth data is in cache
		authData, exists := cache.GetFromAuthDataCache(cacheKey)
		assert.True(t, exists)
		assert.Equal(t, "test-pool", authData.PoolID)

		// Cleanup
		cache.RemoveFromAuthDataCache(cacheKey)
	})

	t.Run("WhenValidParams_WithGcnvAdminCredential_ShouldSucceed", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		ctx := context.Background()

		resultCtx, err := SetupCredentialsForHandler(ctx, "5678", "another-pool", CredentialTypeExpertModeUser)

		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)

		// Verify cache key is set in context
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, "5678:another-pool:gadmin", cacheKey)

		// Cleanup
		cache.RemoveFromAuthDataCache(cacheKey)
	})

	t.Run("WhenCacheHit_ShouldUseCachedData", func(t *testing.T) {
		cacheKey := "cached-project:cached-pool:admin"
		testAuthData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			PoolID:   "cached-pool",
		}
		cache.AddToAuthDataCache(cacheKey, testAuthData)
		defer cache.RemoveFromAuthDataCache(cacheKey)

		ctx := context.Background()

		resultCtx, err := SetupCredentialsForHandler(ctx, "cached-project", "cached-pool", CredentialTypeAdmin)

		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)

		// Verify cache key is set correctly
		resultCacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, cacheKey, resultCacheKey)
	})

	t.Run("WhenFetchCredentialsFails_ShouldReturnError", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return nil, assert.AnError
		}

		ctx := context.Background()

		resultCtx, err := SetupCredentialsForHandler(ctx, "fail-project", "fail-pool", CredentialTypeAdmin)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch and cache credentials")
		// Context should still be returned (even if unchanged)
		assert.NotNil(t, resultCtx)
	})

	t.Run("WhenExpertModeUser_WithPrivilegedModeUser_ShouldMapToAdmin", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		headers := http.Header{}
		headers.Set("x-google-iam-role", "privOntap")
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, headers)

		// Note: This test verifies the code path for privileged mode user.
		// The actual mapping depends on IAM_ROLE_MAPPING_CONFIG environment variable.
		// If the config maps "privOntap" to "padmin" (PrivExpertModeUserSuffix), it should map to "admin".
		// Otherwise, it defaults to "gadmin" (ExpertModeUserSuffix).
		resultCtx, err := SetupCredentialsForHandler(ctx, "9999", "priv-pool", CredentialTypeExpertModeUser)

		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)

		// Verify cache key is set (actual value depends on IAM role mapping config)
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.NotEmpty(t, cacheKey)
		assert.Contains(t, cacheKey, "9999:priv-pool")

		// Cleanup
		cache.RemoveFromAuthDataCache(cacheKey)
	})

	t.Run("WhenExpertModeUser_WithoutHeaders_ShouldDefaultToGadmin", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		ctx := context.Background()

		resultCtx, err := SetupCredentialsForHandler(ctx, "8888", "default-pool", CredentialTypeExpertModeUser)

		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)

		// Verify cache key uses gadmin (default for expert mode)
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, "8888:default-pool:gadmin", cacheKey)

		// Cleanup
		cache.RemoveFromAuthDataCache(cacheKey)
	})

	t.Run("WhenExpertModeUser_WithValidationEnabled_ShouldReturnError", func(t *testing.T) {
		// This test explicitly sets iamRoleToUserValidationEnabled to true
		// Verifies the error case at lines 56-58 when validation is enabled
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		// Save original value and set to true
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		fetchCredentialsFunc = mockFetchCredentials

		// Create a context with empty headers (no IAM role)
		headers := http.Header{}
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, headers)

		resultCtx, err := SetupCredentialsForHandler(ctx, "validation-true", "test-pool", CredentialTypeExpertModeUser)

		// With validation enabled, determineUserNameFromRBAC should return error
		// SetupCredentialsForHandler should return ctx, err (lines 56-58)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to determine IAM role from context headers")
		assert.NotNil(t, resultCtx)
		// Verify context is returned even on error
		assert.Equal(t, ctx, resultCtx)
	})

	t.Run("WhenExpertModeUser_WithValidationDisabled_ShouldSucceed", func(t *testing.T) {
		// This test explicitly sets iamRoleToUserValidationEnabled to false
		// Verifies the success case when validation is disabled
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		// Save original value and set to false
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = false
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		fetchCredentialsFunc = mockFetchCredentials

		// Create a context with empty headers (no IAM role)
		headers := http.Header{}
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, headers)

		resultCtx, err := SetupCredentialsForHandler(ctx, "validation-false", "test-pool", CredentialTypeExpertModeUser)

		// With validation disabled, determineUserNameFromRBAC should return gadmin
		// SetupCredentialsForHandler should succeed
		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, "validation-false:test-pool:gadmin", cacheKey)
		cache.RemoveFromAuthDataCache(cacheKey)
	})

	t.Run("WhenExpertModeUser_WithHeadersButNoIAMRole_ValidationEnabled_ShouldReturnError", func(t *testing.T) {
		// This test explicitly sets iamRoleToUserValidationEnabled to true
		// Tests the error case when headers exist but no IAM role header is present
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		// Save original value and set to true
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		fetchCredentialsFunc = mockFetchCredentials

		headers := http.Header{}
		headers.Set("Authorization", "Bearer test-token")
		// No x-google-iam-role header - this will cause determineUserNameFromRBAC to return error
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, headers)

		resultCtx, err := SetupCredentialsForHandler(ctx, "validation-true-headers", "test-pool", CredentialTypeExpertModeUser)

		// With validation enabled, should return error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to determine IAM role from context headers")
		assert.NotNil(t, resultCtx)
		assert.Equal(t, ctx, resultCtx)
	})

	t.Run("WhenExpertModeUser_WithHeadersButNoIAMRole_ValidationDisabled_ShouldSucceed", func(t *testing.T) {
		// This test explicitly sets iamRoleToUserValidationEnabled to false
		// Tests the success case when headers exist but no IAM role header is present
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		// Save original value and set to false
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = false
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		fetchCredentialsFunc = mockFetchCredentials

		headers := http.Header{}
		headers.Set("Authorization", "Bearer test-token")
		// No x-google-iam-role header - should default to gadmin when validation disabled
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, headers)

		resultCtx, err := SetupCredentialsForHandler(ctx, "validation-false-headers", "test-pool", CredentialTypeExpertModeUser)

		// With validation disabled, should succeed with gadmin
		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, "validation-false-headers:test-pool:gadmin", cacheKey)
		cache.RemoveFromAuthDataCache(cacheKey)
	})

	t.Run("WhenExpertModeUser_WithNilHeaders_ValidationEnabled_ShouldReturnError", func(t *testing.T) {
		// Test when headers are explicitly nil in context (not just missing)
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		// Save original value and set to true
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = true
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		// Create context with explicitly nil headers
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, (http.Header)(nil))

		resultCtx, err := SetupCredentialsForHandler(ctx, "nil-headers-validation", "test-pool", CredentialTypeExpertModeUser)

		// With validation enabled and nil headers, should return error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to determine IAM role from context headers")
		assert.NotNil(t, resultCtx)
	})

	t.Run("WhenExpertModeUser_WithNilHeaders_ValidationDisabled_ShouldUseDefault", func(t *testing.T) {
		// Test when headers are explicitly nil in context (not just missing)
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		// Save original value and set to false
		originalValidationEnabled := iamRoleToUserValidationEnabled
		iamRoleToUserValidationEnabled = false
		defer func() { iamRoleToUserValidationEnabled = originalValidationEnabled }()

		fetchCredentialsFunc = mockFetchCredentials

		// Create context with explicitly nil headers
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, (http.Header)(nil))

		resultCtx, err := SetupCredentialsForHandler(ctx, "nil-headers-no-validation", "test-pool", CredentialTypeExpertModeUser)

		// With validation disabled and nil headers, should use default (gadmin)
		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, "nil-headers-no-validation:test-pool:gadmin", cacheKey)
		cache.RemoveFromAuthDataCache(cacheKey)
	})
}

// dedicatedFetchFunc returns a fetch stub whose credentials carry the given
// service level, so SetupCredentialsCache picks the namespace from the
// response exactly as it does in production.
func dedicatedFetchFunc(serviceLevel string) func(context.Context, *models.PoolDetails, string, log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
	return func(_ context.Context, _ *models.PoolDetails, _ string, _ log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
		return &coreapiclient.OntapCredentialsV1{
			AuthType:       coreapiclient.NewOptInt(models.USERNAME_PWD_SEC_MGR),
			SecretID:       coreapiclient.NewOptString("acct-host-secret"),
			Username:       coreapiclient.NewOptString("account-host-user"),
			OntapEndpoints: []coreapiclient.OntapEndpoint{{IP: "10.0.0.1", DNS: "10.0.0.1"}},
			ServiceLevel:   coreapiclient.NewOptString(serviceLevel),
		}, nil
	}
}

// TestSetupCredentialsCache_DedicatedVsStandard covers the rule that decides
// which cache key a request lands on:
//   - clusterName header present -> hardware:{proj}:{pool}:{cluster}
//   - clusterName header absent  -> {proj}:{pool}:{user}
func TestSetupCredentialsCache_DedicatedVsStandard(t *testing.T) {
	const projectNumber = "proj-1"
	const poolID = "pool-uuid"
	const userName = "gadmin"

	tests := []struct {
		name        string
		clusterName string // value of the x-netapp-cluster-name header
		wantKey     string
	}{
		{
			name:        "HeaderPresent_UsesDedicatedCache",
			clusterName: "cluster-b",
			wantKey:     "hardware:proj-1:pool-uuid:gadmin:cluster-b",
		},
		{
			name:        "HeaderAbsent_UsesStandardCache",
			clusterName: "",
			wantKey:     "proj-1:pool-uuid:gadmin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalFetchCredentials := fetchCredentialsFunc
			defer func() { fetchCredentialsFunc = originalFetchCredentials }()
			fetchCredentialsFunc = dedicatedFetchFunc("STANDARD")

			poolDetails := &models.PoolDetails{
				ProjectNumber: projectNumber,
				PoolID:        poolID,
				AccountName:   projectNumber,
				UserName:      userName,
				ClusterName:   tt.clusterName,
			}

			resultCtx, err := SetupCredentialsCache(context.Background(), poolDetails, projectNumber, poolID, userName, "jwt")
			assert.NoError(t, err)

			cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
			defer cache.RemoveFromAuthDataCache(cacheKey)
			assert.Equal(t, tt.wantKey, cacheKey)

			authData, exists := cache.GetFromAuthDataCache(cacheKey)
			assert.True(t, exists)
			assert.Equal(t, "account-host-user", authData.Username)
			assert.Equal(t, "acct-host-secret", authData.SecretID)
		})
	}
}

func TestExtractJWTFromContext(t *testing.T) {
	t.Run("WhenHeadersInContext_ShouldReturnAuthorizationHeader", func(t *testing.T) {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer test-jwt-token")

		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, headers)

		token := ExtractJWTFromContext(ctx)

		assert.Equal(t, "Bearer test-jwt-token", token)
	})

	t.Run("WhenNoHeadersInContext_ShouldReturnEmpty", func(t *testing.T) {
		ctx := context.Background()

		token := ExtractJWTFromContext(ctx)

		assert.Equal(t, "", token)
	})

	t.Run("WhenHeadersAreNil_ShouldReturnEmpty", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, (http.Header)(nil))

		token := ExtractJWTFromContext(ctx)

		assert.Equal(t, "", token)
	})

	t.Run("WhenAuthorizationHeaderIsEmpty_ShouldReturnEmpty", func(t *testing.T) {
		headers := http.Header{}
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, headers)

		token := ExtractJWTFromContext(ctx)

		assert.Equal(t, "", token)
	})

	t.Run("WhenWrongTypeInContext_ShouldReturnEmpty", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, "not-headers")

		token := ExtractJWTFromContext(ctx)

		assert.Equal(t, "", token)
	})
}

func TestSetupCredentialsCache(t *testing.T) {
	t.Run("WhenValidParams_ShouldSucceed", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		poolDetails := &models.PoolDetails{
			ProjectNumber: "test-project",
			PoolID:        "test-pool",
			AccountName:   "test-project",
			UserName:      "admin",
		}

		ctx := context.Background()
		jwtToken := "test-jwt-token"

		resultCtx, err := SetupCredentialsCache(ctx, poolDetails, "test-project", "test-pool", "admin", jwtToken)

		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)

		// Verify cache key is set in context
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, "test-project:test-pool:admin", cacheKey)

		// Verify auth data is in cache
		authData, exists := cache.GetFromAuthDataCache(cacheKey)
		assert.True(t, exists)
		assert.Equal(t, "test-pool", authData.PoolID)

		// Cleanup
		cache.RemoveFromAuthDataCache(cacheKey)
	})

	t.Run("WhenCacheHit_ShouldUseCachedData", func(t *testing.T) {
		cacheKey := "cached-project:cached-pool:gadmin"
		testAuthData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			PoolID:   "cached-pool",
		}
		cache.AddToAuthDataCache(cacheKey, testAuthData)
		defer cache.RemoveFromAuthDataCache(cacheKey)

		poolDetails := &models.PoolDetails{
			ProjectNumber: "cached-project",
			PoolID:        "cached-pool",
			AccountName:   "cached-project",
			UserName:      "gadmin",
		}

		ctx := context.Background()
		jwtToken := "test-jwt-token"

		resultCtx, err := SetupCredentialsCache(ctx, poolDetails, "cached-project", "cached-pool", "gadmin", jwtToken)

		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)

		// Verify cache key is set correctly
		resultCacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, cacheKey, resultCacheKey)
	})

	t.Run("WhenFetchCredentialsFails_ShouldReturnError", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			return nil, assert.AnError
		}

		poolDetails := &models.PoolDetails{
			ProjectNumber: "fail-project",
			PoolID:        "fail-pool",
			AccountName:   "fail-project",
			UserName:      "admin",
		}

		ctx := context.Background()
		jwtToken := "test-jwt-token"

		resultCtx, err := SetupCredentialsCache(ctx, poolDetails, "fail-project", "fail-pool", "admin", jwtToken)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to fetch and cache credentials")
		assert.Contains(t, err.Error(), "fail-pool")
		// Context should still be returned (even if unchanged)
		assert.NotNil(t, resultCtx)
	})

	t.Run("WhenDedicatedCacheHit_ShouldUseDedicatedCachedData", func(t *testing.T) {
		const projectNumber = "dedicated-project"
		const poolID = "dedicated-pool"
		const userName = "gadmin"
		const clusterName = "cluster-x"

		dedicatedKey := generateDedicatedCacheKey(generateCacheKey(projectNumber, poolID, userName), clusterName)
		cache.AddToAuthDataCache(dedicatedKey, &models.AuthData{
			AuthType:     models.USERNAME_PWD_SEC_MGR,
			PoolID:       poolID,
			ServiceLevel: "STANDARD",
			ClusterName:  clusterName,
		})
		defer cache.RemoveFromAuthDataCache(dedicatedKey)

		// fetchCredentialsFunc must NOT be called on a dedicated cache hit.
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()
		fetchCredentialsFunc = func(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapiclient.OntapCredentialsV1, error) {
			t.Fatalf("fetchCredentialsFunc should not be called on dedicated cache hit")
			return nil, nil
		}

		poolDetails := &models.PoolDetails{
			ProjectNumber: projectNumber,
			PoolID:        poolID,
			AccountName:   projectNumber,
			UserName:      userName,
			ClusterName:   clusterName,
		}

		resultCtx, err := SetupCredentialsCache(context.Background(), poolDetails, projectNumber, poolID, userName, "jwt")

		assert.NoError(t, err)
		assert.Equal(t, dedicatedKey, cache.GetAuthDataKeyFromContext(resultCtx))
	})

	t.Run("WhenEmptyJWTToken_ShouldStillSucceed", func(t *testing.T) {
		originalFetchCredentials := fetchCredentialsFunc
		defer func() { fetchCredentialsFunc = originalFetchCredentials }()

		fetchCredentialsFunc = mockFetchCredentials

		poolDetails := &models.PoolDetails{
			ProjectNumber: "empty-jwt-project",
			PoolID:        "empty-jwt-pool",
			AccountName:   "empty-jwt-project",
			UserName:      "admin",
		}

		ctx := context.Background()
		jwtToken := ""

		resultCtx, err := SetupCredentialsCache(ctx, poolDetails, "empty-jwt-project", "empty-jwt-pool", "admin", jwtToken)

		assert.NoError(t, err)
		assert.NotNil(t, resultCtx)

		// Verify cache key is set in context
		cacheKey := cache.GetAuthDataKeyFromContext(resultCtx)
		assert.Equal(t, "empty-jwt-project:empty-jwt-pool:admin", cacheKey)

		// Cleanup
		cache.RemoveFromAuthDataCache(cacheKey)
	})
}
