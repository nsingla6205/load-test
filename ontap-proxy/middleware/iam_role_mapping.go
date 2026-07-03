package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	// IAMRoleHeader is the HTTP header name for IAM role
	IAMRoleHeader = "x-google-iam-role"

	ManageSnaplockRole   = "netapp.ontap.manageSnaplock"
	PrivilegedDeleteRole = "netapp.ontap.privilegedDelete"
)

var (
	// iamRoleMappingConfig holds the cached IAM role to RBAC user mapping
	// Key: IAM role (from header), Value: RBAC username
	// After initialization, this map is read-only, so concurrent reads are safe
	iamRoleMappingConfig map[string]string
	// once ensures LoadIAMRoleMappingConfig initialization happens only once
	once sync.Once

	iamRoleToUserValidationEnabled = env.GetBool("IAM_ROLE_TO_USER_VALIDATION_ENABLED", false)
	iamRoleToUserMappingConfig     = env.GetString("IAM_ROLE_TO_USER_MAPPING_CONFIG", "")
)

// LoadIAMRoleMappingConfig loads the IAM role mapping configuration from environment variables
// Format: {"ontap": "gcnvadmin", "privOntap": "admin"}
// Key: IAM role (from header), Value: RBAC username
// This function is thread-safe and uses sync.Once to ensure initialization happens only once
func LoadIAMRoleMappingConfig() map[string]string {
	once.Do(func() {
		if iamRoleToUserMappingConfig == "" {
			// Return empty map if not configured
			iamRoleMappingConfig = make(map[string]string)
			return
		}

		var config map[string]string
		if err := json.Unmarshal([]byte(iamRoleToUserMappingConfig), &config); err != nil {
			// If parsing fails, use empty map
			iamRoleMappingConfig = make(map[string]string)
			return
		}

		iamRoleMappingConfig = config
	})
	// After initialization, the map is read-only, so concurrent reads are safe
	return iamRoleMappingConfig
}

// GetIAMRoleFromHeader extracts the IAM role from the request header
func GetIAMRoleFromHeader(req *http.Request) string {
	iamRoleHeader := req.Header.Get(IAMRoleHeader)
	logIAMRoleHeader(req.Context(), iamRoleHeader)
	if iamRoleHeader == "" {
		return ""
	}

	return strings.TrimSpace(iamRoleHeader)
}

// logIAMRoleHeader logs the x-google-iam-role header value received from the
// upstream load balancer (CLH) at INFO level, so we can verify whether the
// header actually reaches ontap-proxy.
func logIAMRoleHeader(ctx context.Context, iamRole string) {
	util.GetLogger(ctx).InfoContext(ctx, "ontap-proxy received x-google-iam-role header from CLH",
		"header", IAMRoleHeader,
		"present", iamRole != "",
		"value", iamRole,
	)
}

// MapIAMRoleToRBACUser maps an IAM role to the corresponding RBAC user
func MapIAMRoleToRBACUser(ctx context.Context, iamRole string) string {
	logger := util.GetLogger(ctx)
	config := LoadIAMRoleMappingConfig()

	// Look up the IAM role in the config map
	if rbacUser, exists := config[iamRole]; exists && rbacUser != "" {
		logger.DebugContext(ctx, "Mapping IAM role to RBAC user from config", "iamRole", iamRole, "rbacUser", rbacUser)
		return rbacUser
	}

	if iamRoleToUserValidationEnabled {
		// If validation is enabled and no match found, log error
		logger.ErrorContext(ctx, "IAM role not found in mapping config", "iamRole", iamRole)
		return ""
	}
	// Default to gcnvadmin if no match (backward compatibility)
	logger.DebugContext(ctx, "No IAM role match in config, defaulting to gcnvadmin", "iamRole", iamRole)
	return env.ExpertModeUserSuffix
}

// GetRBACUserFromRequest determines the RBAC user from the request
func GetRBACUserFromRequest(ctx context.Context, req *http.Request) string {
	iamRole := GetIAMRoleFromHeader(req)
	if iamRole == "" {
		// No IAM role header, return empty string
		return ""
	}
	return MapIAMRoleToRBACUser(ctx, iamRole)
}

// GetIAMRoleFromContext returns the IAM role from headers stored in context by auth middleware.
// Use this in ogen handlers to validate x-google-iam-role.
func GetIAMRoleFromContext(ctx context.Context) string {
	headers, ok := ctx.Value(utilsmiddleware.HeaderContextKey).(http.Header)
	if !ok || headers == nil {
		logIAMRoleHeader(ctx, "")
		return ""
	}
	iamRole := headers.Get(IAMRoleHeader)
	logIAMRoleHeader(ctx, iamRole)
	return strings.TrimSpace(iamRole)
}

// IsIAMRoleHeaderSnaplockExistInContext returns true if the IAM role from context equals role.
func IsIAMRoleHeaderSnaplockExistInContext(ctx context.Context, role string) bool {
	return GetIAMRoleFromContext(ctx) == role
}
