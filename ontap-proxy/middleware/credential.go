package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	coreapiclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/coreapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	ontapproxyutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// Credential type constants
const (
	// CredentialTypeAdmin is used for admin operations (snaplock, EBR, litigation)
	// handled by ogen handlers which call SetupCredentialsForHandler() directly.
	CredentialTypeAdmin = "admin"

	// CredentialTypeExpertModeUser is used for passthrough operations via CredentialMiddleware.
	CredentialTypeExpertModeUser = "expertModeUser"

	// AdminUserName is the username used for admin credential type
	AdminUserName = "admin"
)

var (
	fetchCredentialsFunc = coreapi.FetchCredentials
	poolUriRegex         = "^/v1beta/projects/([^/]+)/locations/([^/]+)/pools/([^/]+)"
	smcOperationEnabled  = env.GetBool("SMC_OPERATION_ENABLED", false)
)

// adminCredentialPaths are normalized ONTAP paths (UUIDs as {uuid}) that require admin credentials.
var adminCredentialPaths = map[string]struct{}{
	// SnapMirror relationships
	"/api/snapmirror/relationships":                         {},
	"/api/snapmirror/relationships/{uuid}":                  {},
	"/api/snapmirror/relationships/{uuid}/transfers":        {},
	"/api/snapmirror/relationships/{uuid}/transfers/{uuid}": {},
	"/api/snapmirror/relationships/{uuid}/restore":          {},
	// Cluster licensing access token (SM-C)
	"/api/cluster/licensing/access_tokens": {},
	// SnapMirror object-store APIs
	"/api/snapmirror/object-stores/{uuid}":                                   {},
	"/api/snapmirror/object-stores/{uuid}/endpoints/{uuid}":                  {},
	"/api/snapmirror/object-stores/{uuid}/endpoints/{uuid}/snapshots":        {},
	"/api/snapmirror/object-stores/{uuid}/endpoints/{uuid}/snapshots/{uuid}": {},
}

func CredentialMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger := util.GetLogger(r.Context())

			// Passthrough routes use gcnvadmin by default. When SMC is enabled, use admin for
			// SnapMirror relationships, access-token, and object-store APIs.
			credentialType := CredentialTypeExpertModeUser
			if smcOperationEnabled && isAdminCredentialPath(ontapproxyutils.ExtractOntapPath(r.URL.Path)) {
				credentialType = CredentialTypeAdmin
			}
			poolDetails, err := extractPoolDetailsFromRequest(r, credentialType)
			if err != nil {
				logger.ErrorContext(r.Context(), "Failed to extract pool details", "error", err, "path", r.URL.Path)
				// Check if this is an IAM role validation error vs URI validation error
				if isIAMRoleValidationError(err) {
					handleCredentialError(w, err)
				} else {
					// URI validation error
					ontapproxyutils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid URI")
				}
				return
			}
			jwtToken := ExtractJWTFromRequest(r)
			ctx, err := SetupCredentialsCache(r.Context(), poolDetails, poolDetails.ProjectNumber, poolDetails.PoolID, poolDetails.UserName, jwtToken)
			if err != nil {
				logger.ErrorContext(r.Context(), "Failed to fetch and cache credentials", "error", err, "poolID", poolDetails.PoolID)
				handleCredentialError(w, err)
				return
			}
			// Add backend metrics context (same labels as top-level for correlation)
			ctx = AddBackendMetricsToContext(ctx, poolDetails.ProjectNumber, poolDetails.PoolID, normalizePath(r.URL.Path))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func fetchAndCacheCredentials(ctx context.Context, poolDetails *models.PoolDetails, cacheKey string, jwtToken string, logger log.Logger) error {
	if _, exists := cache.GetFromAuthDataCache(cacheKey); exists {
		logger.InfoContext(ctx, "Using cached auth data",
			"projectNumber", poolDetails.ProjectNumber,
			"poolID", poolDetails.PoolID,
			"accountName", poolDetails.AccountName,
			"userName", poolDetails.UserName,
			"cacheKey", cacheKey)
		return nil
	}

	logger.InfoContext(ctx, "Cache miss - fetching credentials from Core API",
		"projectNumber", poolDetails.ProjectNumber,
		"poolID", poolDetails.PoolID,
		"userName", poolDetails.UserName)

	credentials, err := fetchCredentialsFunc(ctx, poolDetails, jwtToken, logger)
	if err != nil {
		return fmt.Errorf("failed to fetch credentials: %w", err)
	}

	authData := authDataFromFetchedCredentials(poolDetails, credentials)

	cache.AddToAuthDataCache(cacheKey, authData)

	logger.InfoContext(ctx, "Credentials fetched from Core API and stored as AuthData",
		"poolID", authData.PoolID,
		"accountName", authData.AccountName,
		"authType", authData.AuthType,
		"cacheKey", cacheKey)

	return nil
}

func authDataFromFetchedCredentials(poolDetails *models.PoolDetails, credentials *coreapiclient.OntapCredentialsV1) *models.AuthData {
	return &models.AuthData{
		AuthType:       credentials.AuthType.Value,
		SecretID:       getStringValue(credentials.SecretID),
		CertificateID:  getStringValue(credentials.CertificateID),
		Password:       getStringValue(credentials.Password),
		PoolID:         poolDetails.PoolID,
		AccountName:    poolDetails.AccountName,
		Username:       getStringValue(credentials.Username),
		OntapEndpoints: convertOntapEndpoints(credentials.OntapEndpoints),
		CaURI:          getStringValue(credentials.CaURI),
		ServiceLevel:   getStringValue(credentials.ServiceLevel),
		ClusterName:    poolDetails.ClusterName,
		AccountUUID:    getStringValue(credentials.AccountUUID),
		DeploymentName: getStringValue(credentials.DeploymentName),
	}
}

func handleCredentialError(w http.ResponseWriter, err error) {
	var fe *ontapproxyutils.HTTPError
	if errors.As(err, &fe) {
		ontapproxyutils.WriteErrorResponse(w, fe.Status, fe.Message)
		return
	}

	errorMsg := err.Error()

	switch {
	case contains(errorMsg, "pool not found"):
		ontapproxyutils.WriteErrorResponse(w, http.StatusNotFound, "Pool not found")
	case contains(errorMsg, "deleting state"):
		ontapproxyutils.WriteErrorResponse(w, http.StatusBadRequest, "Pool is in deleting state")
	case contains(errorMsg, "creating state"):
		ontapproxyutils.WriteErrorResponse(w, http.StatusBadRequest, "Pool is in creating state")
	case contains(errorMsg, "invalid pool details"):
		ontapproxyutils.WriteErrorResponse(w, http.StatusBadRequest, "Invalid pool details")
	case contains(errorMsg, "unable to determine IAM role from context headers"):
		// IAM role validation error - missing or invalid IAM role header
		ontapproxyutils.WriteErrorResponse(w, http.StatusUnauthorized, "Unauthorized: Unable to determine IAM role from request headers")
	case contains(errorMsg, "unauthorized access"):
		ontapproxyutils.WriteErrorResponse(w, http.StatusUnauthorized, "Unauthorized access")
	case contains(errorMsg, "forbidden access"):
		ontapproxyutils.WriteErrorResponse(w, http.StatusForbidden, "Forbidden access")
	case contains(errorMsg, "internal server error"):
		ontapproxyutils.WriteErrorResponse(w, http.StatusInternalServerError, "Internal server error")
	case contains(errorMsg, "core API call failed"):
		ontapproxyutils.WriteErrorResponse(w, http.StatusServiceUnavailable, "Service unavailable")
	default:
		ontapproxyutils.WriteErrorResponse(w, http.StatusInternalServerError, "Internal server error")
	}
}

// isIAMRoleValidationError checks if the error is related to IAM role validation
// (missing IAM role header) rather than URI validation
func isIAMRoleValidationError(err error) bool {
	if err == nil {
		return false
	}
	errorMsg := err.Error()
	return contains(errorMsg, "unable to determine IAM role from context headers")
}

func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func generateCacheKey(projectNumber, poolID, userName string) string {
	return fmt.Sprintf("%s:%s:%s", projectNumber, poolID, userName)
}

// dedicatedCacheKeyPrefix namespaces credentials resolved via the dedicated-capacity
// account-host flow so they never collide with standard pool credentials.
const dedicatedCacheKeyPrefix = "hardware"

// generateDedicatedCacheKey builds the dedicated-namespace cache key of the form
// dedicated:{projectNumber}:{poolID}:{clusterName}. clusterName may be empty when the
// request omitted the x-netapp-cluster-name header (the pool's own cluster is used).
func generateDedicatedCacheKey(cacheKey string, clusterName string) string {
	return fmt.Sprintf("%s:%s:%s", dedicatedCacheKeyPrefix, cacheKey, clusterName)
}

// isDedicatedCacheKey reports whether a cache key belongs to the dedicated namespace.
func isDedicatedCacheKey(cacheKey string) bool {
	return strings.HasPrefix(cacheKey, dedicatedCacheKeyPrefix+":")
}

// ClusterNameHeader is the request header carrying the external ONTAP cluster name
// for dedicated-capacity pools.
const ClusterNameHeader = "x-netapp-cluster-name"

// ExtractClusterNameFromRequest reads the x-netapp-cluster-name header from the request.
func ExtractClusterNameFromRequest(req *http.Request) string {
	return strings.TrimSpace(req.Header.Get(ClusterNameHeader))
}

// ExtractClusterNameFromContext reads the x-netapp-cluster-name header from the
// header map stored on context by auth.AuthMiddleware (used by ogen handlers).
func ExtractClusterNameFromContext(ctx context.Context) string {
	headers, ok := ctx.Value(utilsmiddleware.HeaderContextKey).(http.Header)
	if !ok || headers == nil {
		return ""
	}
	return strings.TrimSpace(headers.Get(ClusterNameHeader))
}

// parseAuthDataCacheKey is the inverse of generateCacheKey. Normal requests derive
// pool identity from the request URI via extractPoolDetailsFromRequest; RoundTrip
// only has the cache key in context (after Director may have rewritten the path), so
// reconcile uses this split to rebuild PoolDetails for Core.
func parseAuthDataCacheKey(cacheKey string) (projectNumber, poolID, userName string, err error) {
	parts := strings.SplitN(cacheKey, ":", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid auth cache key")
	}
	return parts[0], parts[1], parts[2], nil
}

// poolDetailsFromCacheKey rebuilds PoolDetails from a cache key for credential
// reconcile. It understands both the standard namespace
// ({projectNumber}:{poolID}:{userName}) and the dedicated namespace
// (hardware:{projectNumber}:{poolID}:{userName}:{clusterName}, see
// generateDedicatedCacheKey).
func poolDetailsFromCacheKey(cacheKey string) (*models.PoolDetails, error) {
	if isDedicatedCacheKey(cacheKey) {
		// hardware:{projectNumber}:{poolID}:{userName}:{clusterName}
		parts := strings.SplitN(cacheKey, ":", 5)
		if len(parts) != 5 {
			return nil, fmt.Errorf("invalid hardware auth cache key")
		}
		return &models.PoolDetails{
			ProjectNumber: parts[1],
			PoolID:        parts[2],
			AccountName:   parts[1],
			UserName:      parts[3],
			ClusterName:   parts[4],
		}, nil
	}

	projectNumber, poolID, userName, err := parseAuthDataCacheKey(cacheKey)
	if err != nil {
		return nil, err
	}
	return &models.PoolDetails{
		ProjectNumber: projectNumber,
		PoolID:        poolID,
		AccountName:   projectNumber,
		UserName:      userName,
	}, nil
}

func getStringValue(opt coreapiclient.OptString) string {
	if opt.IsSet() {
		return opt.Value
	}
	return ""
}

func convertOntapEndpoints(apiEndpoints []coreapiclient.OntapEndpoint) []models.OntapEndpoint {
	var endpoints []models.OntapEndpoint
	for _, apiEndpoint := range apiEndpoints {
		endpoints = append(endpoints, models.OntapEndpoint{
			IP:  apiEndpoint.IP,
			DNS: apiEndpoint.DNS,
		})
	}
	return endpoints
}

// determineUserNameFromRBAC determines the username based on RBAC user role from the request
func determineUserNameFromRBAC(ctx context.Context, req *http.Request) (string, error) {
	roleUserName := GetRBACUserFromRequest(ctx, req)
	switch roleUserName {
	case "":
		if iamRoleToUserValidationEnabled {
			// Headers not available in context, but validation is enabled
			return "", fmt.Errorf("unable to determine IAM role from context headers")
		}
		return env.ExpertModeUserSuffix, nil
	case env.PrivExpertModeUserSuffix:
		// Privileged Mode User map to Admin for now
		return AdminUserName, nil
	default:
		return roleUserName, nil
	}
}

// isAdminCredentialPath returns true if the normalized ONTAP path exactly matches an entry in adminCredentialPaths.
func isAdminCredentialPath(ontapPath string) bool {
	_, ok := adminCredentialPaths[ontapPath]
	return ok
}

func extractPoolDetailsFromRequest(req *http.Request, credentialType string) (*models.PoolDetails, error) {
	uri := req.URL.Path

	err := validatePoolUri(uri)
	if err != nil {
		return nil, err
	}

	uriSlice := strings.Split(uri, "/")
	projectNumber := uriSlice[3]
	_ = uriSlice[5]
	poolID := uriSlice[7]

	accountName := projectNumber

	var userName string
	if credentialType == CredentialTypeAdmin {
		userName = AdminUserName
	} else {
		userName, err = determineUserNameFromRBAC(req.Context(), req)
		if err != nil {
			return nil, err
		}
	}

	return &models.PoolDetails{
		ProjectNumber: projectNumber,
		PoolID:        poolID,
		AccountName:   accountName,
		UserName:      userName,
		ClusterName:   ExtractClusterNameFromRequest(req),
	}, nil
}

func validatePoolUri(uri string) error {
	compiledRegex := regexp.MustCompile(poolUriRegex)
	valid := compiledRegex.MatchString(uri)
	if !valid {
		return fmt.Errorf("pool URI should match format: /v1beta/projects/project_number/locations/location_id/pools/pool_id, got: %s", uri)
	}

	uriList := strings.Split(uri, "/")
	if len(uriList) < 8 {
		return fmt.Errorf("pool URI should have at least 8 path segments, got: %s", uri)
	}

	return nil
}

// ExtractJWTFromRequest extracts the JWT token from the request Authorization header.
// Returns the full Authorization header value (including "Bearer " prefix if present).
// Used by CredentialMiddleware for passthrough routes.
func ExtractJWTFromRequest(req *http.Request) string {
	return req.Header.Get("Authorization")
}

// ExtractJWTFromContext extracts the JWT token from context.
// The headers are stored in context by auth.AuthMiddleware.
// Used by SetupCredentialsForHandler for ogen handlers.
func ExtractJWTFromContext(ctx context.Context) string {
	headers, ok := ctx.Value(utilsmiddleware.HeaderContextKey).(http.Header)
	if !ok || headers == nil {
		return ""
	}
	return headers.Get("Authorization")
}
