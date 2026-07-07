package middleware

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// SetupCredentialsForHandler sets up credentials for an ogen handler.
// Unlike CredentialMiddleware, this takes explicit parameters instead of
// extracting from request URL. This allows ogen handlers to call it directly
// with typed parameters from the OpenAPI spec.
//
// The JWT token is automatically extracted from context (set by auth.AuthMiddleware).
//
// Parameters:
//   - ctx: Request context (must have headers set by auth.AuthMiddleware)
//   - projectNumber: From path parameter (ogen params.ProjectNumber)
//   - poolID: From path parameter (ogen params.PoolId)
//   - credentialType: CredentialTypeAdmin or CredentialTypeExpertModeUser
//
// Returns context with AuthDataKey set, ready for NewOntapClientFromContext().
func SetupCredentialsForHandler(
	ctx context.Context,
	projectNumber string,
	poolID string,
	credentialType string,
) (context.Context, error) {
	logger := util.GetLogger(ctx)
	// Get JWT token from context (set by auth.AuthMiddleware)
	jwtToken := ExtractJWTFromContext(ctx)

	// Determine username based on IAM role mapping or credential type
	// Note: For SetupCredentialsForHandler, we need to extract headers from context
	// Since we don't have direct access to the request, we fall back to credential type
	var userName string
	if credentialType == CredentialTypeAdmin {
		userName = AdminUserName
	} else {
		req := &http.Request{}
		var err error
		// Try to get IAM role from context headers if available
		headers, ok := ctx.Value(utilsmiddleware.HeaderContextKey).(http.Header)
		if ok && headers != nil {
			// Create a temporary request to use determineUserNameFromRBAC
			// This is a workaround since SetupCredentialsForHandler doesn't have direct request access
			req.Header = headers
			req = req.WithContext(ctx)
			userName, err = determineUserNameFromRBAC(ctx, req)
			if err != nil {
				// Check if this is an IAM role validation error
				if isIAMRoleValidationError(err) {
					logger.ErrorContext(ctx, "IAM role validation failed", "error", err, "poolID", poolID, "projectNumber", projectNumber)
				} else {
					logger.ErrorContext(ctx, "Failed to determine user name from RBAC", "error", err, "poolID", poolID, "projectNumber", projectNumber)
				}
				return ctx, err
			}
		} else {
			// Headers not available in context
			if iamRoleToUserValidationEnabled {
				// Headers not available in context, but validation is enabled
				return ctx, fmt.Errorf("unable to determine IAM role from context headers")
			}
			// Headers not available and validation disabled - use default
			userName = env.ExpertModeUserSuffix
		}
	}
	poolDetails := &models.PoolDetails{
		ProjectNumber: projectNumber,
		PoolID:        poolID,
		AccountName:   projectNumber,
		UserName:      userName,
		ClusterName:   ExtractClusterNameFromContext(ctx),
	}

	return SetupCredentialsCache(ctx, poolDetails, projectNumber, poolID, userName, jwtToken)
}

// SetupCredentialsCache resolves and caches ONTAP credentials for a request.
//
// The cache key is determined solely by request parameters:
//   - header present (clusterName != ""):  dedicated:{projectNumber}:{poolID}:{userName}:{clusterName}
//   - header absent:                       {projectNumber}:{poolID}:{userName}
//
// This means one cache lookup per request. On a miss the credential is fetched
// from Core and stored under the same key.
func SetupCredentialsCache(ctx context.Context, poolDetails *models.PoolDetails, projectNumber, poolID, userName, jwtToken string) (context.Context, error) {
	logger := util.GetLogger(ctx)

	cacheKey := generateCacheKey(projectNumber, poolID, userName)
	standardCacheKey := cacheKey // Save the standard cache key for logging
	if poolDetails.ClusterName != "" {
		cacheKey = generateDedicatedCacheKey(cacheKey, poolDetails.ClusterName)
	}

	if _, exists := cache.GetFromAuthDataCache(cacheKey); exists {
		logger.DebugContext(ctx, "Using cached auth data", "poolID", poolID, "cacheKey", cacheKey)
		return context.WithValue(ctx, models.AuthDataKey, cacheKey), nil
	}

	// Cache miss: fetch once from Core and store under the same key.
	credentials, err := fetchCredentialsFunc(ctx, poolDetails, jwtToken, logger)
	if err != nil {
		return ctx, fmt.Errorf("failed to fetch and cache credentials pooluuid: %s, error: %v", poolDetails.PoolID, err)
	}
	authData := authDataFromFetchedCredentials(poolDetails, credentials)

	if authData.ServiceLevel == models.ServiceLevelFLEX {
		cacheKey = standardCacheKey
	}

	cache.AddToAuthDataCache(cacheKey, authData)

	logger.DebugContext(ctx, "Credentials setup for handler",
		"poolID", poolID,
		"projectNumber", projectNumber,
		"cacheKey", cacheKey,
		"serviceLevel", authData.ServiceLevel)
	return context.WithValue(ctx, models.AuthDataKey, cacheKey), nil
}
