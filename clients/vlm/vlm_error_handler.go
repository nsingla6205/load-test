package vlm

import (
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/temporal"
)

// VLM error pattern constants used in handleStringBasedError for string-based
// error classification. Grouped by scope: vlmErrPattern* for shared/GCP
// patterns, ociVLMErrPattern* for OCI-specific safe-to-expose patterns.
const (
	// Shared VLM error patterns (GCP + OCI)
	vlmErrPatternNotFound                   = "notfound"
	vlmErrPatternDoesNotExistInZone         = "does not exist in zone"
	vlmErrPatternZoneResourcePoolExhausted  = "zone_resource_pool_exhausted"
	vlmErrPatternWithDetails                = "with_details"
	vlmErrPatternInsufficientResources      = "does not have enough resources available"
	vlmErrPatternVMInstance                 = "vm instance"
	vlmErrPatternUnavailableInThe           = "unavailable in the"
	vlmErrPatternBecauseOf                  = "because of"
	vlmErrPatternResourceOpRateExceeded     = "resource_operation_rate_exceeded"
	vlmErrPatternRateLimited                = "rate limited"
	vlmErrPatternNotReady                   = "not ready"
	vlmErrPatternConstraint                 = "constraint"
	vlmErrPatternViolated                   = "violated"
	vlmErrPatternCPUPlatform                = "cpu platform"
	vlmErrPatternMustMatch                  = "must match"
	vlmErrPatternServiceAccountAccessDenied = "service_account_access_denied"
	vlmErrPatternSourceMachineImage         = "sourcemachineimage"
	vlmErrPatternNotSupported               = "not supported"
	vlmErrPatternQuota                      = "quota"
	vlmErrPatternExceeded                   = "exceeded"
	vlmErrPatternRegion                     = "region"
	vlmErrPatternZone                       = "zone"
	vlmErrPatternPermissionDenied           = "permission denied"
	vlmErrPatternUnauthorized               = "unauthorized"
	vlmErrPatternResourceAlreadyExists      = "resource already exists"
	vlmErrPatternAlreadyExists              = "already exists"
	vlmErrPatternCPUPlatformMismatch        = "cpu platform mismatch"
	vlmErrPatternInvalidValueForField       = "invalid value for field"
	vlmErrPatternResourceNotFoundInZone     = "resource not found in zone"
	vlmErrPatternNotFoundInZone             = "not found in zone"

	// OCI-specific VLM error patterns (safe-to-expose, message passthrough)
	ociVLMErrPatternLimitExceeded           = "limitexceeded"
	ociVLMErrPatternDailyResourceLimit      = "daily resource creation limit"
	ociVLMErrPatternRunCommonConfigAPI      = "runcommonconfigapinodeworkflow"
	ociVLMErrPatternUpdateDeploymentAttrs   = "updatedeploymentattributesworkflow"
	ociVLMErrPatternGetLicenseKey           = "getlicensekeyactivityoci"
	ociVLMErrPatternAttachBlockVolume       = "attachblockvolumeactivity"
	ociVLMErrPatternOCPUs                   = "ocpus"
	ociVLMErrPatternMustBeBetween           = "must be between"
	ociVLMErrPatternSVMDoesntExist          = "svm doesn't exist"
	ociVLMErrPatternSPConfig                = "spconfig"
	ociVLMErrPatternDiskSizeBetween         = "disk size must be between"
	ociVLMErrPatternPerDiskSizeBetween      = "per disk size must be between"
	ociVLMErrPatternCreateAggregatesHAW     = "createaggregatesforhawworkflow"
	ociVLMErrPatternCreateAggregatesHA      = "createaggregatesforhaworkflow"
	ociVLMErrPatternCannotReassignPrimaryIP = "cannot reassign primary private ip"
	ociVLMErrPatternEntryDoesntExist        = "entry doesn't exist"
	ociVLMErrPatternSVMSvmsPath             = "/svm/svms"
	ociVLMErrPatternClusterCreationFailed   = "cluster creation failed"
	ociVLMErrPatternCanceledError           = "cancelederror"
	ociVLMErrPatternScaleOntapCluster       = "scale ontap cluster"
	ociVLMErrPatternVMProvisioningWorkflow  = "vmprovisioningworkflow"
	ociVLMErrPatternStartVMWorkflow         = "startvmworkflow"
	ociVLMErrPatternSecondaryIPsForLIFs     = "secondary ips for lifs are not enabled"
	ociVLMErrPatternUserCertificate         = "user_certificate"
)

// VLMErrorHandler handles VLM-specific errors and converts them to user-facing errors
type VLMErrorHandler struct {
	logger log.Logger
}

// NewVLMErrorHandler creates a new VLM error handler
func NewVLMErrorHandler() *VLMErrorHandler {
	return &VLMErrorHandler{
		logger: log.NewLogger(),
	}
}

// NewVLMErrorHandlerWithLogger creates a new VLM error handler with a custom logger
func NewVLMErrorHandlerWithLogger(logger log.Logger) *VLMErrorHandler {
	return &VLMErrorHandler{
		logger: logger,
	}
}

// HandleVLMError processes VLM errors and converts them to appropriate VCP errors
func (h *VLMErrorHandler) HandleVLMError(err error) error {
	if err == nil {
		return nil
	}

	// Log the incoming error for debugging
	h.logger.Debug("Processing VLM error",
		"error", err.Error(),
		"error_type", fmt.Sprintf("%T", err))

	// First, check if it's a child workflow execution error (retry scenario)
	var childWorkflowErr *temporal.ChildWorkflowExecutionError
	if errors.As(err, &childWorkflowErr) {
		h.logger.Info("Detected child workflow execution error (retry scenario)",
			"workflow_type", childWorkflowErr.WorkflowType(),
			"workflow_id", childWorkflowErr.WorkflowID(),
			"run_id", childWorkflowErr.RunID())

		// Try to extract the actual error from the cause
		// For child workflow errors, we need to check if there's an underlying application error
		var appErr *temporal.ApplicationError
		if errors.As(childWorkflowErr, &appErr) {
			h.logger.Debug("Found application error in child workflow, processing it",
				"app_error_type", appErr.Type(),
				"app_error_message", appErr.Message())

			// Check if the cause is already a VLM error (avoid double processing)
			if appErr.Type() == ErrorTypeVLMClientError {
				h.logger.Info("Cause is already a VLM error, processing directly")
				return h.HandleVLMError(appErr)
			}

			// Process the application error to extract VLM client error details
			return h.HandleVLMError(appErr)
		}

		// If no cause, return generic workflow error with retry context
		return errors.NewVCPError(errors.ErrVLMWorkflowError,
			fmt.Errorf("workflow %s failed after retries: %s",
				childWorkflowErr.WorkflowType(), childWorkflowErr.Error()))
	}

	// Check if it's a temporal application error
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		h.logger.Warn("Non-temporal error received from VLM",
			"error", err.Error(),
			"error_type", fmt.Sprintf("%T", err))
		return h.handleNonTemporalError(err)
	}

	// Check if it's a VLM client error
	if appErr.Type() == ErrorTypeVLMClientError {
		var vlmClientErr VLMClientError
		if appErr.HasDetails() && appErr.Details(&vlmClientErr) == nil {
			h.logger.Info("Successfully extracted VLM client error details",
				"http_code", vlmClientErr.HttpCode,
				"code", vlmClientErr.Code,
				"message", vlmClientErr.Message,
				"component", vlmClientErr.Component,
				"external", vlmClientErr.External,
				"retryable", vlmClientErr.Retryable,
				"causes_count", len(vlmClientErr.Cause))
			return h.convertVLMClientErrorToVCPError(vlmClientErr, err)
		}
		h.logger.Warn("Failed to extract VLM client error details",
			"app_error_type", appErr.Type(),
			"has_details", appErr.HasDetails(),
			"app_error_message", appErr.Message())
	}

	// If it's not a VLM client error, return as generic VLM workflow error
	h.logger.Info("Non-VLM temporal error received",
		"app_error_type", appErr.Type(),
		"app_error_message", appErr.Message())
	return errors.NewVCPError(errors.ErrVLMWorkflowError, err)
}

// handleNonTemporalError handles non-temporal errors with fallback logic
func (h *VLMErrorHandler) handleNonTemporalError(err error) error {
	// Try to classify the error based on its message
	errMsg := strings.ToLower(err.Error())

	// Check for common error patterns
	if h.isConflictError(errMsg) {
		h.logger.Info("Classified as resource-already-exists conflict", "error", err.Error())
		return errors.NewVCPError(errors.ErrGCPResourceAlreadyExistsError, err)
	}

	if h.isRetryableError(errMsg) {
		h.logger.Info("Classified as retryable error", "error", err.Error())
		return errors.NewVCPError(errors.ErrVLMWorkflowError, err)
	}

	if h.isQuotaError(errMsg) {
		h.logger.Info("Classified as quota error", "error", err.Error())
		return errors.NewVCPError(errors.ErrVLMQuotaExceededGeneral, err)
	}

	if h.isPermissionError(errMsg) {
		h.logger.Info("Classified as permission error", "error", err.Error())
		return errors.NewVCPError(errors.ErrVLMInsufficientPermissions, err)
	}

	// Default to generic VLM workflow error
	return errors.NewVCPError(errors.ErrVLMWorkflowError, err)
}

// convertVLMClientErrorToVCPError converts vlm.VLMClientError to appropriate VCP error
func (h *VLMErrorHandler) convertVLMClientErrorToVCPError(vlmErr VLMClientError, originalErr error) error {
	// Log the VLM client error details
	h.logger.Info("Processing VLM client error",
		"http_code", vlmErr.HttpCode,
		"code", vlmErr.Code,
		"component", vlmErr.Component,
		"external", vlmErr.External,
		"retryable", vlmErr.Retryable,
		"message", vlmErr.Message,
		"causes", vlmErr.Cause)

	mappedError := h.mapVLMErrorToUserFacingError(vlmErr, originalErr)

	// If the error is marked as external, we should propagate it to the customer
	if vlmErr.External {
		h.logger.Info("External VLM error - propagating to customer",
			"http_code", vlmErr.HttpCode,
			"code", vlmErr.Code,
			"message", vlmErr.Message)
		return mappedError
	}

	// For internal errors, log them but still return the mapped error
	// This preserves the actual error message while indicating it's internal
	h.logger.Warn("Internal VLM error - preserving error message for debugging",
		"http_code", vlmErr.HttpCode,
		"code", vlmErr.Code,
		"message", vlmErr.Message,
		"causes", vlmErr.Cause)

	return mappedError
}

// mapVLMErrorToUserFacingError maps VLM error codes to user-facing VCP errors
func (h *VLMErrorHandler) mapVLMErrorToUserFacingError(vlmErr VLMClientError, originalErr error) error {
	h.logger.Debug("Mapping VLM error to user-facing error",
		"http_code", vlmErr.HttpCode,
		"vlm_code", vlmErr.Code,
		"message", vlmErr.Message,
		"causes", vlmErr.Cause)

	// PRIORITY 1: Map based on VLM error codes first (most specific)
	if vlmErr.Code != "" {
		switch vlmErr.Code {
		case "QUOTA_EXCEEDED":
			return h.handleQuotaError(vlmErr, originalErr)
		case "ZONE_RESOURCE_POOL_EXHAUSTED":
			return h.handleResourceExhaustionError(vlmErr, originalErr)
		case "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS":
			return h.handleResourceExhaustionWithDetailsError(vlmErr, originalErr)
		case "RESOURCE_OPERATION_RATE_EXCEEDED":
			return h.handleRateLimitError(vlmErr, originalErr)
		case "SERVICE_ACCOUNT_ACCESS_DENIED":
			return errors.NewVCPError(errors.ErrVLMServiceAccountAccessDenied, originalErr)
		case "RESOURCE_NOT_READY":
			return errors.NewVCPError(errors.ErrVLMResourceNotReady, originalErr)
		case "PROJECT_CONSTRAINT_VIOLATED":
			return errors.NewVCPError(errors.ErrVLMProjectConstraintViolated, originalErr)
		case "CPU_PLATFORM_MISMATCH":
			return errors.NewVCPError(errors.ErrVLMCPUPlatformMismatch, originalErr)
		case "INVALID_MACHINE_IMAGE_UPDATE":
			return errors.NewVCPError(errors.ErrVLMInvalidMachineImageUpdate, originalErr)
		case "CLOUD_OPERATION_FAILED":
			return h.handleCloudOperationFailed(vlmErr, originalErr)
		case "CLOUD_VM_OFFLINE":
			return h.handleCloudOperationFailed(vlmErr, originalErr)
		}
	}

	// PRIORITY 2: String-based pattern matching (medium specificity)
	if vlmErr.Message != "" || len(vlmErr.Cause) > 0 {
		return h.handleStringBasedError(vlmErr, originalErr)
	}

	// PRIORITY 3: Map based on HTTP status codes (least specific, fallback)
	switch vlmErr.HttpCode {
	case 429: // Too Many Requests
		return h.handleQuotaOrRateLimitError(vlmErr, originalErr)
	case 403: // Forbidden
		return h.handlePermissionError(vlmErr, originalErr)
	case 409: // Conflict
		return h.handleConflictError(vlmErr, originalErr)
	case 400: // Bad Request
		return h.handleBadRequestError(vlmErr, originalErr)
	case 404: // Not Found
		return h.handleNotFoundError(vlmErr, originalErr)
	case 401: // Unauthorized
		return h.handleUnauthorizedError(vlmErr, originalErr)
	case 500, 502, 503, 504: // Server errors
		return h.handleServerError(vlmErr, originalErr)
	}

	// Final fallback: generic VLM workflow error
	return errors.NewVCPError(errors.ErrVLMWorkflowError, originalErr)
}

// handleServerError handles server-side errors (500, 502, 503, 504)
func (h *VLMErrorHandler) handleServerError(vlmErr VLMClientError, originalErr error) error {
	h.logger.Warn("Server error from VLM",
		"http_code", vlmErr.HttpCode,
		"message", vlmErr.Message,
		"retryable", vlmErr.Retryable)

	// Check if there's a specific VLM error code that we can handle
	if vlmErr.Code != "" {
		switch vlmErr.Code {
		case "CLOUD_OPERATION_FAILED":
			// Handle cloud operation failures with detailed error information
			return h.handleCloudOperationFailed(vlmErr, originalErr)
		}
	}

	// Always prioritize cause information over generic original error
	if len(vlmErr.Cause) > 0 {
		primaryCause := vlmErr.Cause[0]
		h.logger.Info("Using detailed cause information for server error",
			"cause", primaryCause)

		// If the error is marked as retryable, return a retryable error with cause
		if vlmErr.Retryable {
			return errors.NewVCPError(errors.ErrVLMWorkflowError,
				fmt.Errorf("server error: %s", primaryCause))
		}

		// For non-retryable server errors, return with cause information
		return errors.NewVCPError(errors.ErrVLMWorkflowError,
			fmt.Errorf("server error: %s", primaryCause))
	}

	// Fallback to original error if no cause information
	if vlmErr.Retryable {
		return errors.NewVCPError(errors.ErrVLMWorkflowError, originalErr)
	}
	return errors.NewVCPError(errors.ErrVLMWorkflowError, originalErr)
}

// handleQuotaOrRateLimitError handles quota and rate limit errors
func (h *VLMErrorHandler) handleQuotaOrRateLimitError(vlmErr VLMClientError, originalErr error) error {
	message := strings.ToLower(vlmErr.Message)

	h.logger.Info("Processing quota/rate limit error",
		"http_code", vlmErr.HttpCode,
		"message", vlmErr.Message)

	// Check for quota exceeded patterns
	if strings.Contains(message, "quota") && strings.Contains(message, "exceeded") {
		if strings.Contains(message, "region") {
			return errors.NewVCPError(errors.ErrVLMQuotaExceededRegional, originalErr)
		}
		if strings.Contains(message, "zone") {
			return errors.NewVCPError(errors.ErrVLMQuotaExceededZonal, originalErr)
		}
		return errors.NewVCPError(errors.ErrVLMQuotaExceededGeneral, originalErr)
	}

	// Check for rate limit patterns
	if strings.Contains(message, "rate limit") || strings.Contains(message, "rate exceeded") {
		if strings.Contains(message, "disk") || strings.Contains(message, "provisioned") {
			return errors.NewVCPError(errors.ErrVLMDiskRateLimited, originalErr)
		}
		return errors.NewVCPError(errors.ErrVLMRateLimitExceeded, originalErr)
	}

	// Default quota/rate limit error
	return errors.NewVCPError(errors.ErrVLMQuotaExceededGeneral, originalErr)
}

// handlePermissionError handles permission-related errors
func (h *VLMErrorHandler) handlePermissionError(vlmErr VLMClientError, originalErr error) error {
	message := strings.ToLower(vlmErr.Message)

	h.logger.Info("Processing permission error",
		"http_code", vlmErr.HttpCode,
		"message", vlmErr.Message)

	if strings.Contains(message, "service_account_access_denied") {
		return errors.NewVCPError(errors.ErrVLMServiceAccountAccessDenied, originalErr)
	}

	if strings.Contains(message, "required") && strings.Contains(message, "permission") {
		return errors.NewVCPError(errors.ErrVLMInsufficientPermissions, originalErr)
	}

	return errors.NewVCPError(errors.ErrVLMInsufficientPermissions, originalErr)
}

// handleConflictError handles conflict errors (like resource already exists)
func (h *VLMErrorHandler) handleConflictError(vlmErr VLMClientError, originalErr error) error {
	message := strings.ToLower(vlmErr.Message)

	h.logger.Info("Processing conflict error",
		"http_code", vlmErr.HttpCode,
		"message", vlmErr.Message)

	if strings.Contains(message, "already exists") {
		return errors.NewVCPError(errors.ErrGCPResourceAlreadyExistsError, originalErr)
	}

	return errors.NewVCPError(errors.ErrGCPResourceAlreadyExistsError, originalErr)
}

// handleBadRequestError handles bad request errors
func (h *VLMErrorHandler) handleBadRequestError(vlmErr VLMClientError, originalErr error) error {
	message := strings.ToLower(vlmErr.Message)

	h.logger.Info("Processing bad request error",
		"http_code", vlmErr.HttpCode,
		"message", vlmErr.Message)

	if strings.Contains(message, "cpu platform") {
		return errors.NewVCPError(errors.ErrVLMCPUPlatformMismatch, originalErr)
	}

	if strings.Contains(message, "sourcemachineimage") && strings.Contains(message, "not supported") {
		return errors.NewVCPError(errors.ErrVLMInvalidMachineImageUpdate, originalErr)
	}

	return errors.NewVCPError(errors.ErrBadRequest, originalErr)
}

// handleNotFoundError handles not found errors
func (h *VLMErrorHandler) handleNotFoundError(vlmErr VLMClientError, originalErr error) error {
	message := strings.ToLower(vlmErr.Message)

	h.logger.Info("Processing not found error",
		"http_code", vlmErr.HttpCode,
		"message", vlmErr.Message)

	if strings.Contains(message, "notfound") || strings.Contains(message, "does not exist in zone") {
		return errors.NewVCPError(errors.ErrVLMResourceNotAvailableInZone, originalErr)
	}

	return errors.NewVCPError(errors.ErrVLMResourceNotAvailableInZone, originalErr)
}

// handleUnauthorizedError handles unauthorized errors
func (h *VLMErrorHandler) handleUnauthorizedError(vlmErr VLMClientError, originalErr error) error {
	h.logger.Info("Processing unauthorized error",
		"http_code", vlmErr.HttpCode,
		"message", vlmErr.Message)

	return errors.NewVCPError(errors.ErrVLMInsufficientPermissions, originalErr)
}

// handleQuotaError handles quota exceeded errors
func (h *VLMErrorHandler) handleQuotaError(vlmErr VLMClientError, originalErr error) error {
	message := strings.ToLower(vlmErr.Message)

	h.logger.Info("Processing quota error",
		"code", vlmErr.Code,
		"message", vlmErr.Message)

	if strings.Contains(message, "region") {
		return errors.NewVCPError(errors.ErrVLMQuotaExceededRegional, originalErr)
	}
	if strings.Contains(message, "zone") {
		return errors.NewVCPError(errors.ErrVLMQuotaExceededZonal, originalErr)
	}
	return errors.NewVCPError(errors.ErrVLMQuotaExceededGeneral, originalErr)
}

// handleResourceExhaustionError handles resource exhaustion errors
func (h *VLMErrorHandler) handleResourceExhaustionError(vlmErr VLMClientError, originalErr error) error {
	h.logger.Info("Processing resource exhaustion error",
		"code", vlmErr.Code,
		"message", vlmErr.Message)

	return errors.NewVCPError(errors.ErrVLMZoneResourcePoolExhausted, originalErr)
}

// handleResourceExhaustionWithDetailsError handles resource exhaustion with details errors
func (h *VLMErrorHandler) handleResourceExhaustionWithDetailsError(vlmErr VLMClientError, originalErr error) error {
	h.logger.Info("Processing resource exhaustion with details error",
		"code", vlmErr.Code,
		"message", vlmErr.Message)

	return errors.NewVCPError(errors.ErrVLMZoneResourcePoolExhaustedWithDetails, originalErr)
}

// handleRateLimitError handles rate limit errors
func (h *VLMErrorHandler) handleRateLimitError(vlmErr VLMClientError, originalErr error) error {
	message := strings.ToLower(vlmErr.Message)

	h.logger.Info("Processing rate limit error",
		"code", vlmErr.Code,
		"message", vlmErr.Message)

	if strings.Contains(message, "disk") || strings.Contains(message, "provisioned") {
		return errors.NewVCPError(errors.ErrVLMDiskRateLimited, originalErr)
	}
	return errors.NewVCPError(errors.ErrVLMRateLimitExceeded, originalErr)
}

// handleCloudOperationFailed handles cloud operation failures with detailed error information
func (h *VLMErrorHandler) handleCloudOperationFailed(vlmErr VLMClientError, originalErr error) error {
	h.logger.Info("Processing cloud operation failed error",
		"http_code", vlmErr.HttpCode,
		"code", vlmErr.Code,
		"message", vlmErr.Message,
		"causes", vlmErr.Cause)

	// Check if there are specific causes that provide more detailed error information
	if len(vlmErr.Cause) > 0 {
		// Use the first cause as it usually contains the most specific error
		primaryCause := vlmErr.Cause[0]
		h.logger.Debug("Primary cause of cloud operation failure",
			"cause", primaryCause)

		// Check for specific error patterns in the cause
		primaryCauseLower := strings.ToLower(primaryCause)

		// VM creation failures
		if strings.Contains(primaryCauseLower, "failed to create vm") {
			if strings.Contains(primaryCauseLower, "unable to get image") {
				return errors.NewVCPError(errors.ErrVLMResourceNotReady,
					fmt.Errorf("VM creation failed: %s", primaryCause))
			}
			if strings.Contains(primaryCauseLower, "quota") {
				return errors.NewVCPError(errors.ErrVLMQuotaExceededGeneral,
					fmt.Errorf("VM creation failed: %s", primaryCause))
			}
			if strings.Contains(primaryCauseLower, "permission") || strings.Contains(primaryCauseLower, "access denied") {
				return errors.NewVCPError(errors.ErrVLMInsufficientPermissions,
					fmt.Errorf("VM creation failed: %s", primaryCause))
			}
			// Generic VM creation failure
			return errors.NewVCPError(errors.ErrVLMWorkflowError,
				fmt.Errorf("VM creation failed: %s", primaryCause))
		}

		// Handle "unable to get image" specifically (from your screenshot)
		if strings.Contains(primaryCauseLower, "unable to get image") {
			return errors.NewVCPError(errors.ErrVLMResourceNotReady,
				fmt.Errorf("Image retrieval failed: %s", primaryCause))
		}

		// Network-related failures
		if strings.Contains(primaryCauseLower, "network") || strings.Contains(primaryCauseLower, "subnet") {
			return errors.NewVCPError(errors.ErrVLMResourceNotAvailableInZone,
				fmt.Errorf("Network configuration failed: %s", primaryCause))
		}

		// Storage-related failures
		if strings.Contains(primaryCauseLower, "disk") || strings.Contains(primaryCauseLower, "storage") {
			if strings.Contains(primaryCauseLower, "quota") {
				return errors.NewVCPError(errors.ErrVLMQuotaExceededGeneral,
					fmt.Errorf("Storage operation failed: %s", primaryCause))
			}
			return errors.NewVCPError(errors.ErrVLMResourceNotReady,
				fmt.Errorf("Storage operation failed: %s", primaryCause))
		}

		if strings.Contains(primaryCauseLower, "is not running") {
			return errors.NewVCPError(errors.ErrVLMCloudVMOffline,
				fmt.Errorf("Cloud VM is not running: %s", primaryCause))
		}

		// Return a more specific error with the cause information
		return errors.NewVCPError(errors.ErrVLMWorkflowError,
			fmt.Errorf("Cloud operation failed: %s", primaryCause))
	}

	// If no specific causes, use the main message
	return errors.NewVCPError(errors.ErrVLMWorkflowError,
		fmt.Errorf("Cloud operation failed: %s", vlmErr.Message))
}

// handleStringBasedError processes string-based error patterns using both message and cause
func (h *VLMErrorHandler) handleStringBasedError(vlmErr VLMClientError, originalErr error) error {
	// Prioritize cause information over message for more detailed error matching
	var errorStr string
	if len(vlmErr.Cause) > 0 {
		errorStr = strings.ToLower(vlmErr.Cause[0])
		h.logger.Debug("Using cause information for string-based error matching",
			"cause", vlmErr.Cause[0])
	} else if vlmErr.Message != "" {
		errorStr = strings.ToLower(vlmErr.Message)
		h.logger.Debug("Using message for string-based error matching",
			"message", vlmErr.Message)
	} else {
		// No error information available
		h.logger.Warn("No error message or cause available for string-based matching")
		return errors.NewVCPError(errors.ErrVLMWorkflowError, originalErr)
	}

	h.logger.Debug("Processing string-based error",
		"error_string", errorStr)

	// Resource exhaustion/stockout patterns
	if strings.Contains(errorStr, vlmErrPatternNotFound) || strings.Contains(errorStr, vlmErrPatternDoesNotExistInZone) {
		return errors.NewVCPError(errors.ErrVLMResourceNotAvailableInZone, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	if strings.Contains(errorStr, vlmErrPatternZoneResourcePoolExhausted) {
		if strings.Contains(errorStr, vlmErrPatternWithDetails) {
			return errors.NewVCPError(errors.ErrVLMZoneResourcePoolExhaustedWithDetails, h.appendCauseToOriginalError(originalErr, vlmErr))
		}
		return errors.NewVCPError(errors.ErrVLMZoneResourcePoolExhausted, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	if strings.Contains(errorStr, vlmErrPatternInsufficientResources) {
		return errors.NewVCPError(errors.ErrVLMInsufficientResourcesInZone, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// VM type unavailable patterns
	if strings.Contains(errorStr, vlmErrPatternVMInstance) && strings.Contains(errorStr, vlmErrPatternUnavailableInThe) {
		if strings.Contains(errorStr, vlmErrPatternBecauseOf) {
			return errors.NewVCPError(errors.ErrVLMVMTypeUnavailableWithReason, h.appendCauseToOriginalError(originalErr, vlmErr))
		}
		return errors.NewVCPError(errors.ErrVLMVMTypeUnavailableInZone, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Rate limit patterns
	if strings.Contains(errorStr, vlmErrPatternResourceOpRateExceeded) {
		return errors.NewVCPError(errors.ErrVLMRateLimitExceeded, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	if strings.Contains(errorStr, vlmErrPatternRateLimited) {
		return errors.NewVCPError(errors.ErrVLMDiskRateLimited, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Resource not ready patterns
	if strings.Contains(errorStr, vlmErrPatternNotReady) {
		return errors.NewVCPError(errors.ErrVLMResourceNotReady, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Project constraint patterns
	if strings.Contains(errorStr, vlmErrPatternConstraint) && strings.Contains(errorStr, vlmErrPatternViolated) {
		return errors.NewVCPError(errors.ErrVLMProjectConstraintViolated, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// CPU platform mismatch patterns
	if strings.Contains(errorStr, vlmErrPatternCPUPlatform) && strings.Contains(errorStr, vlmErrPatternMustMatch) {
		return errors.NewVCPError(errors.ErrVLMCPUPlatformMismatch, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Service account access denied patterns
	if strings.Contains(errorStr, vlmErrPatternServiceAccountAccessDenied) {
		return errors.NewVCPError(errors.ErrVLMServiceAccountAccessDenied, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Machine image update patterns
	if strings.Contains(errorStr, vlmErrPatternSourceMachineImage) && strings.Contains(errorStr, vlmErrPatternNotSupported) {
		return errors.NewVCPError(errors.ErrVLMInvalidMachineImageUpdate, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Quota patterns (fallback)
	if strings.Contains(errorStr, vlmErrPatternQuota) && strings.Contains(errorStr, vlmErrPatternExceeded) {
		if strings.Contains(errorStr, vlmErrPatternRegion) {
			return errors.NewVCPError(errors.ErrVLMQuotaExceededRegional, h.appendCauseToOriginalError(originalErr, vlmErr))
		}
		if strings.Contains(errorStr, vlmErrPatternZone) {
			return errors.NewVCPError(errors.ErrVLMQuotaExceededZonal, h.appendCauseToOriginalError(originalErr, vlmErr))
		}
		return errors.NewVCPError(errors.ErrVLMQuotaExceededGeneral, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Permission/authorization patterns
	if strings.Contains(errorStr, vlmErrPatternPermissionDenied) || strings.Contains(errorStr, vlmErrPatternUnauthorized) {
		return errors.NewVCPError(errors.ErrVLMInsufficientPermissions, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Conflict patterns (resource already exists, in use, etc.)
	if strings.Contains(errorStr, vlmErrPatternResourceAlreadyExists) || strings.Contains(errorStr, vlmErrPatternAlreadyExists) {
		return errors.NewVCPError(errors.ErrGCPResourceAlreadyExistsError, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Bad request patterns (CPU platform mismatch, invalid machine image, etc.)
	if strings.Contains(errorStr, vlmErrPatternCPUPlatformMismatch) {
		return errors.NewVCPError(errors.ErrVLMCPUPlatformMismatch, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	if strings.Contains(errorStr, vlmErrPatternInvalidValueForField) && strings.Contains(errorStr, vlmErrPatternNotSupported) {
		return errors.NewVCPError(errors.ErrVLMInvalidMachineImageUpdate, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// Not found patterns
	if strings.Contains(errorStr, vlmErrPatternResourceNotFoundInZone) || strings.Contains(errorStr, vlmErrPatternNotFoundInZone) {
		return errors.NewVCPError(errors.ErrVLMResourceNotAvailableInZone, h.appendCauseToOriginalError(originalErr, vlmErr))
	}

	// OCI-specific VLM error patterns — pass VLM cause/message through to the user
	// instead of masking with the generic "An error occurred during VLM workflow execution" text.
	if strings.Contains(errorStr, ociVLMErrPatternLimitExceeded) && strings.Contains(errorStr, ociVLMErrPatternDailyResourceLimit) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternRunCommonConfigAPI) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternUpdateDeploymentAttrs) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternGetLicenseKey) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternAttachBlockVolume) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternOCPUs) && strings.Contains(errorStr, ociVLMErrPatternMustBeBetween) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternSVMDoesntExist) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternSPConfig) || strings.Contains(errorStr, ociVLMErrPatternDiskSizeBetween) || strings.Contains(errorStr, ociVLMErrPatternPerDiskSizeBetween) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternCreateAggregatesHAW) || strings.Contains(errorStr, ociVLMErrPatternCreateAggregatesHA) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternCannotReassignPrimaryIP) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternEntryDoesntExist) && strings.Contains(errorStr, ociVLMErrPatternSVMSvmsPath) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternClusterCreationFailed) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternCanceledError) && strings.Contains(errorStr, ociVLMErrPatternScaleOntapCluster) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternVMProvisioningWorkflow) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternStartVMWorkflow) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternSecondaryIPsForLIFs) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}
	if strings.Contains(errorStr, ociVLMErrPatternUserCertificate) && strings.Contains(errorStr, vlmErrPatternNotSupported) {
		return h.newVCPErrorWithVLMCause(errors.ErrVLMWorkflowError, vlmErr, originalErr)
	}

	// Default to generic VLM workflow error
	h.logger.Warn("No specific error pattern matched, using generic VLM workflow error",
		"error_string", errorStr)
	return errors.NewVCPError(errors.ErrVLMWorkflowError, originalErr)
}

// newVCPErrorWithVLMCause creates a VCPError that uses the actual VLM
// cause/message as the user-facing text instead of the static errors.json
// message. The tracking ID still drives http_code and retriable semantics.
func (h *VLMErrorHandler) newVCPErrorWithVLMCause(trackingID int, vlmErr VLMClientError, originalErr error) error {
	vcpErr := errors.NewVCPError(trackingID, h.appendCauseToOriginalError(originalErr, vlmErr))
	if len(vlmErr.Cause) > 0 {
		vcpErr.Message = vlmErr.Cause[0]
	} else if vlmErr.Message != "" {
		vcpErr.Message = vlmErr.Message
	}
	return vcpErr
}

// appendCauseToOriginalError appends the cause error message to the original error message
func (h *VLMErrorHandler) appendCauseToOriginalError(originalErr error, vlmErr VLMClientError) error {
	// If we have cause information, append it to the original error
	if len(vlmErr.Cause) > 0 {
		cause := vlmErr.Cause[0]
		return fmt.Errorf("%s: %s", originalErr.Error(), cause)
	}

	// Return original error if no cause
	return originalErr
}

// Helper methods for error classification

// isConflictError checks if an error message indicates a resource-already-exists
// conflict (for example an SVM that already exists). Mirrors the "already exists"
// classification used on the temporal path so non-temporal errors (such as those
// surfaced across the go-workflows child-workflow boundary, where the concrete
// error type is flattened to a message string) map to the same tracking ID.
func (h *VLMErrorHandler) isConflictError(errorMsg string) bool {
	conflictPatterns := []string{
		vlmErrPatternResourceAlreadyExists,
		vlmErrPatternAlreadyExists,
	}

	for _, pattern := range conflictPatterns {
		if strings.Contains(errorMsg, pattern) {
			return true
		}
	}
	return false
}

// isRetryableError checks if an error message indicates a retryable error
func (h *VLMErrorHandler) isRetryableError(errorMsg string) bool {
	retryablePatterns := []string{
		"timeout",
		"temporary",
		"retry",
		"unavailable",
		"busy",
		"overloaded",
		"rate limit",
		"throttle",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errorMsg, pattern) {
			return true
		}
	}
	return false
}

// isQuotaError checks if an error message indicates a quota error
func (h *VLMErrorHandler) isQuotaError(errorMsg string) bool {
	quotaPatterns := []string{
		"quota",
		"limit exceeded",
		"resource exhausted",
	}

	for _, pattern := range quotaPatterns {
		if strings.Contains(errorMsg, pattern) {
			return true
		}
	}
	return false
}

// isPermissionError checks if an error message indicates a permission error
func (h *VLMErrorHandler) isPermissionError(errorMsg string) bool {
	permissionPatterns := []string{
		"permission",
		"forbidden",
		"unauthorized",
		"access denied",
		"insufficient",
	}

	for _, pattern := range permissionPatterns {
		if strings.Contains(errorMsg, pattern) {
			return true
		}
	}
	return false
}
