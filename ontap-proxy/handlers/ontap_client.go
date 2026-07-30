// Package handlers provides custom API handlers for the ONTAP proxy.
// These handlers translate incoming API requests to different ONTAP APIs
// and transform responses back to expected formats.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/reverseproxy"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// BackendMetricsRecorder records backend request/duration/error metrics. Production uses middleware; tests can inject a mock.
type BackendMetricsRecorder interface {
	RecordBackendRequest(ctx context.Context, method, projectID, poolID, path string, statusCode int)
	RecordBackendDuration(ctx context.Context, durationSec float64, method, projectID, poolID, path string, statusCode int)
	RecordBackendError(ctx context.Context, method, projectID, poolID, path, errorCode string)
}

// OntapClient is the interface for ONTAP client operations used by endpoints.
// Implemented by *RestOntapClient; allows substituting mocks in tests.
type OntapClient interface {
	GetVolume(ctx context.Context, volumeUUID string) (*VolumeInfo, error)
	ExecuteCLI(ctx context.Context, command, privilege string) (*CLIResponse, error)
	ExecuteAPI(ctx context.Context, method, apiPath string, body []byte) (respBody []byte, statusCode int, err error)
	ListVolumesWithSvm(ctx context.Context, maxRecords int) ([]VolumeInfo, error)
}

// SVMInfo represents a storage virtual machine returned by GET /api/svm/svms.
type SVMInfo struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

// svmListResponse is the ONTAP REST response for GET /api/svm/svms (list).
type svmListResponse struct {
	Records []SVMInfo `json:"records"`
}

// AggregateInfo represents a storage aggregate returned by GET /api/storage/aggregates.
// Fields map to ONTAP REST: name, uuid, state, volume_count, space.block_storage.{size,available,used},
// and space.total_provisioned_space.
type AggregateInfo struct {
	Name        string             `json:"name"`
	UUID        string             `json:"uuid"`
	State       string             `json:"state"`
	VolumeCount int64              `json:"volume_count"`
	Space       AggregateSpaceInfo `json:"space"`
}

// AggregateSpaceInfo holds aggregate space fields from the ONTAP REST space sub-object.
type AggregateSpaceInfo struct {
	BlockStorage          AggregateBlockStorageSpace `json:"block_storage"`
	TotalProvisionedSpace int64                      `json:"total_provisioned_space"`
}

// AggregateBlockStorageSpace holds aggregate block-storage space metrics from ONTAP REST.
type AggregateBlockStorageSpace struct {
	Size      int64 `json:"size"`
	Available int64 `json:"available"`
	Used      int64 `json:"used"`
}

// aggregateListResponse is the ONTAP REST response for GET /api/storage/aggregates (list).
type aggregateListResponse struct {
	Records []AggregateInfo `json:"records"`
}

// RestOntapClient provides methods to interact with ONTAP REST APIs.
// It reuses the connection pool from the reverse proxy for efficiency.
type RestOntapClient struct {
	httpClient      *http.Client
	endpoint        string
	authData        *models.AuthData
	backendRecorder BackendMetricsRecorder // nil: use middleware; set in tests to mock and avoid global metrics state
}

// VolumeInfo represents the volume information retrieved from ONTAP.
// Used to get volume name and SVM name needed for CLI commands.
type VolumeInfo struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	SVM  struct {
		Name string `json:"name"`
		UUID string `json:"uuid"`
	} `json:"svm"`
}

// CLIRequest represents the request body for POST /api/private/cli
type CLIRequest struct {
	Input     string `json:"input"`
	Privilege string `json:"privilege"`
}

// CLIResponse represents the response from POST /api/private/cli
type CLIResponse struct {
	Output string `json:"output"`
}

// OntapErrorResponse represents the error response from ONTAP REST API
type OntapErrorResponse struct {
	Error *OntapError `json:"error,omitempty"`
}

// OntapError represents the error details from ONTAP
type OntapError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// CLIErrorResponse represents the full error response from ONTAP private CLI
type CLIErrorResponse struct {
	Error  *OntapError `json:"error,omitempty"`
	Output string      `json:"output,omitempty"`
}

// OntapCLIError is a custom error type that preserves ONTAP error details
type OntapCLIError struct {
	StatusCode int
	Code       string
	Message    string
	Output     string
}

func (e *OntapCLIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (code: %s)", e.Message, e.Code)
	}
	return e.Message
}

// NewOntapClientFromContext creates an OntapClient using auth data from the request context.
// The auth data is set by CredentialMiddleware and cached in the auth data cache.
// Returns an error if auth data is not found in the context.
func NewOntapClientFromContext(ctx context.Context) (OntapClient, error) {
	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return nil, fmt.Errorf("no auth data cache key found in context")
	}

	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return nil, fmt.Errorf("auth data not found in cache for key: %s", cacheKey)
	}

	// Get a pooled HTTP client from the global connection pool
	client, endpoint, err := reverseproxy.GetGlobalConnectionPool().GetClient(ctx, authData)
	if err != nil {
		return nil, fmt.Errorf("failed to get pooled client: %w", err)
	}

	return &RestOntapClient{
		httpClient: client,
		endpoint:   endpoint,
		authData:   authData,
	}, nil
}

// volumeListResponse is the ONTAP REST response for GET /api/storage/volumes (list).
type volumeListResponse struct {
	Records []VolumeInfo `json:"records"`
}

// VolumeInventoryAggregate is the aggregate reference embedded in VolumeInventoryInfo.
type VolumeInventoryAggregate struct {
	Name string `json:"name"`
}

// VolumeInventoryInfo is returned by ListVolumesForInventory.
// It extends basic volume identity with space usage and aggregate membership,
// required to compute per-aggregate used space in the inventory response.
type VolumeInventoryInfo struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	SVM  struct {
		Name string `json:"name"`
		UUID string `json:"uuid"`
	} `json:"svm"`
	Space struct {
		// Size is the provisioned size of the volume in bytes.
		Size int64 `json:"size"`
	} `json:"space"`
	// Aggregates lists the aggregate(s) backing this volume (one for FlexVol, many for FlexGroup).
	Aggregates []VolumeInventoryAggregate `json:"aggregates"`
}

// volumeInventoryListResponse is the ONTAP REST response envelope for ListVolumesForInventory.
type volumeInventoryListResponse struct {
	Records []VolumeInventoryInfo `json:"records"`
}

// ListVolumesWithSvm returns volumes with name, uuid, and svm.name for discovery.
// When authData.AccountName is set the query is scoped to volumes whose SVM
// name matches "svm_{accountName}_*", restricting results to project-scoped SVMs.
// Used to run CLI "snaplock legal-hold show" per volume when listing litigations.
func (c *RestOntapClient) ListVolumesWithSvm(ctx context.Context, maxRecords int) ([]VolumeInfo, error) {
	if maxRecords <= 0 {
		maxRecords = 1000
	}
	urlStr := fmt.Sprintf("https://%s/api/storage/volumes?fields=name,uuid,svm.name&max_records=%d", c.endpoint, maxRecords)
	if c.authData.AccountName != "" {
		urlStr += "&svm.name=svm_" + c.authData.AccountName + "_*"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("volume list request failed: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ONTAP error (status %d): %s", resp.StatusCode, string(body))
	}
	var list volumeListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("failed to parse volume list: %w", err)
	}
	return list.Records, nil
}

// ListVolumesForInventory fetches volumes with name, uuid, svm, space.size, and aggregates
// via GET /api/storage/volumes. Results are used by the inventory endpoint to compute
// per-aggregate used space (sum of volume sizes) and to populate the SVM/volume tree.
// When authData.AccountName is set the query is scoped to project-scoped SVMs.
func (c *RestOntapClient) ListVolumesForInventory(ctx context.Context, maxRecords int) ([]VolumeInventoryInfo, error) {
	if maxRecords <= 0 {
		maxRecords = 10000
	}
	urlStr := fmt.Sprintf(
		"https://%s/api/storage/volumes?fields=name,uuid,svm.name,svm.uuid,space.size,aggregates.name&max_records=%d",
		c.endpoint, maxRecords,
	)
	if c.authData.AccountName != "" {
		urlStr += "&svm.name=svm_" + c.authData.AccountName + "_*"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	c.recordBackendMetrics(ctx, http.MethodGet, start, resp, err)
	if err != nil {
		return nil, fmt.Errorf("volume inventory list request failed: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ONTAP error (status %d): %s", resp.StatusCode, string(body))
	}
	var list volumeInventoryListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("failed to parse volume inventory list: %w", err)
	}
	return list.Records, nil
}

// ListSVMs returns SVMs on the cluster via GET /api/svm/svms.
// When authData.AccountName is set the query is narrowed to SVMs whose names
// match the pattern "svm_{accountName}_*", which is the naming convention used
// for project-scoped SVMs. When AccountName is empty all SVMs are returned.
func (c *RestOntapClient) ListSVMs(ctx context.Context) ([]SVMInfo, error) {
	urlStr := fmt.Sprintf("https://%s/api/svm/svms?fields=name,uuid&max_records=1000", c.endpoint)
	if c.authData.AccountName != "" {
		urlStr += "&name=svm_" + c.authData.AccountName + "_*"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	c.recordBackendMetrics(ctx, http.MethodGet, start, resp, err)
	if err != nil {
		return nil, fmt.Errorf("SVM list request failed: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ONTAP error (status %d): %s", resp.StatusCode, string(body))
	}
	var list svmListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("failed to parse SVM list: %w", err)
	}
	return list.Records, nil
}

// ListAggregates returns aggregates on the cluster via GET /api/storage/aggregates.
// Aggregates are fetched for all nodes on the cluster regardless of the account scope.
func (c *RestOntapClient) ListAggregates(ctx context.Context) ([]AggregateInfo, error) {
	urlStr := fmt.Sprintf("https://%s/api/storage/aggregates?fields=name,uuid,state,volume_count,space.block_storage.size,space.block_storage.available,space.block_storage.used,space.total_provisioned_space&max_records=1000", c.endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create aggregate list request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	c.recordBackendMetrics(ctx, http.MethodGet, start, resp, err)
	if err != nil {
		return nil, fmt.Errorf("aggregate list request failed: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ONTAP error (status %d): %s", resp.StatusCode, string(body))
	}
	var list aggregateListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("failed to parse aggregate list: %w", err)
	}
	return list.Records, nil
}

// recordBackendMetrics records backend request/duration/error metrics for ogen-handled API calls.
// Uses the same metrics as the reverse proxy (ontap_proxy_backend_*). Call after each httpClient.Do.
// Best-effort: we never panic or fail the request; if recording panics we log and continue.
func (c *RestOntapClient) recordBackendMetrics(ctx context.Context, method string, start time.Time, resp *http.Response, err error) {
	defer func() {
		if r := recover(); r != nil {
			util.GetLogger(ctx).ErrorContext(ctx, "Backend metrics recording panicked; request still succeeded", "panic", r)
		}
	}()
	projectID, poolID, path := middleware.GetBackendMetricsFromContext(ctx)
	duration := time.Since(start).Seconds()
	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}
	if c.backendRecorder != nil {
		c.backendRecorder.RecordBackendRequest(ctx, method, projectID, poolID, path, statusCode)
		c.backendRecorder.RecordBackendDuration(ctx, duration, method, projectID, poolID, path, statusCode)
		if code := middleware.BackendErrorCodeForMetric(statusCode, err); code != "" {
			c.backendRecorder.RecordBackendError(ctx, method, projectID, poolID, path, code)
		}
		return
	}
	middleware.RecordBackendRequest(ctx, method, projectID, poolID, path, statusCode)
	middleware.RecordBackendDuration(ctx, duration, method, projectID, poolID, path, statusCode)
	if code := middleware.BackendErrorCodeForMetric(statusCode, err); code != "" {
		middleware.RecordBackendError(ctx, method, projectID, poolID, path, code)
	}
}

// GetVolume retrieves volume information by UUID from ONTAP.
// Returns volume name and SVM name needed for CLI commands.
func (c *RestOntapClient) GetVolume(ctx context.Context, volumeUUID string) (*VolumeInfo, error) {
	logger := util.GetLogger(ctx)

	url := fmt.Sprintf("https://%s/api/storage/volumes/%s?fields=name,svm.name,svm.uuid", c.endpoint, volumeUUID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)
	req.Header.Set("Accept", "application/json")

	logger.DebugContext(ctx, "Fetching volume info from ONTAP",
		"volumeUUID", volumeUUID,
		"endpoint", c.endpoint,
	)

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	c.recordBackendMetrics(ctx, http.MethodGet, start, resp, err)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("volume not found: %s", volumeUUID)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ONTAP error (status %d): %s", resp.StatusCode, string(body))
	}

	var volumeInfo VolumeInfo
	if err := json.NewDecoder(resp.Body).Decode(&volumeInfo); err != nil {
		return nil, fmt.Errorf("failed to parse volume response: %w", err)
	}

	logger.DebugContext(ctx, "Retrieved volume info",
		"volumeUUID", volumeUUID,
		"volumeName", volumeInfo.Name,
		"svmName", volumeInfo.SVM.Name,
	)

	return &volumeInfo, nil
}

// ExecuteCLI executes an ONTAP CLI command via the private CLI endpoint.
// The command is sent as POST /api/private/cli with the specified privilege level.
func (c *RestOntapClient) ExecuteCLI(ctx context.Context, command, privilege string) (*CLIResponse, error) {
	logger := util.GetLogger(ctx)

	url := fmt.Sprintf("https://%s/api/private/cli", c.endpoint)

	reqBody := CLIRequest{
		Input:     command,
		Privilege: privilege,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal CLI request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create CLI request: %w", err)
	}

	c.setAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Log the command (sanitized - no credentials in command)
	logger.InfoContext(ctx, "Executing ONTAP CLI command",
		"endpoint", c.endpoint,
		"privilege", privilege,
		// Note: Be careful not to log sensitive commands
	)

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	c.recordBackendMetrics(ctx, http.MethodPost, start, resp, err)
	if err != nil {
		return nil, fmt.Errorf("CLI request failed: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read CLI response: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Try to parse as structured ONTAP error response
		var cliError CLIErrorResponse
		if jsonErr := json.Unmarshal(body, &cliError); jsonErr == nil && cliError.Error != nil {
			return nil, &OntapCLIError{
				StatusCode: resp.StatusCode,
				Code:       cliError.Error.Code,
				Message:    cliError.Error.Message,
				Output:     cliError.Output,
			}
		}
		// Fallback to generic error
		return nil, &OntapCLIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
		}
	}

	var cliResp CLIResponse
	if err := json.Unmarshal(body, &cliResp); err != nil {
		// Some CLI commands return plain text, not JSON
		// In that case, treat the body as the output
		cliResp.Output = string(body)
	}

	logger.DebugContext(ctx, "CLI command executed successfully")

	return &cliResp, nil
}

// ExecuteAPI sends an HTTP request to the given ONTAP API path.
// method is the HTTP method (GET, POST, PATCH, DELETE, etc.). For GET/DELETE body may be nil; for POST/PATCH body is the request body.
// Returns the response body, status code, and an error only for transport/read failures.
// For HTTP 4xx/5xx, err is nil and the caller should interpret statusCode and body.
func (c *RestOntapClient) ExecuteAPI(ctx context.Context, method, apiPath string, body []byte) (respBody []byte, statusCode int, err error) {
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	url := fmt.Sprintf("https://%s%s", c.endpoint, apiPath)
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	c.recordBackendMetrics(ctx, method, start, resp, err)
	if err != nil {
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

// SnaplockLitigationBeginRequest is the body for ONTAP POST /api/storage/snaplock/litigations
type SnaplockLitigationBeginRequest struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Volume struct {
		UUID string `json:"uuid,omitempty"`
		Name string `json:"name,omitempty"`
	} `json:"volume"`
}

// SnaplockLitigationGetResponse matches api.yaml SnaplockLitigationResponse (id, name, path, svm, volume, operations).
type SnaplockLitigationGetResponse struct {
	ID         string                               `json:"id"`
	Name       string                               `json:"name"`
	Path       string                               `json:"path"`
	Svm        *SnaplockLitigationGetResponseSvm    `json:"svm,omitempty"`
	Volume     *SnaplockLitigationGetResponseVol    `json:"volume,omitempty"`
	Operations []SnaplockLitigationGetOperationItem `json:"operations,omitempty"`
}

// SnaplockLitigationGetOperationItem matches one item of SnaplockLegalHoldOperationResponse for JSON.
type SnaplockLitigationGetOperationItem struct {
	ID                int    `json:"id"`
	State             string `json:"state"`
	Path              string `json:"path,omitempty"`
	Type              string `json:"type,omitempty"`
	NumFilesProcessed string `json:"num_files_processed,omitempty"`
	NumFilesFailed    string `json:"num_files_failed,omitempty"`
	NumFilesSkipped   string `json:"num_files_skipped,omitempty"`
	NumInodesIgnored  string `json:"num_inodes_ignored,omitempty"`
}

type SnaplockLitigationGetResponseSvm struct {
	Name string `json:"name,omitempty"`
	UUID string `json:"uuid,omitempty"`
}

type SnaplockLitigationGetResponseVol struct {
	Name string `json:"name,omitempty"`
	UUID string `json:"uuid,omitempty"`
}

// SnaplockOperationGetResponse is the JSON shape for SnaplockLitigationOperationGet (id, state, path, type, num_files_*, status_details).
type SnaplockOperationGetResponse struct {
	ID                int    `json:"id"`
	State             string `json:"state"`
	Path              string `json:"path"`
	Type              string `json:"type"`
	NumFilesProcessed string `json:"num_files_processed,omitempty"`
	NumFilesFailed    string `json:"num_files_failed,omitempty"`
	NumFilesSkipped   string `json:"num_files_skipped,omitempty"`
	NumInodesIgnored  string `json:"num_inodes_ignored,omitempty"`
	StatusDetails     string `json:"status_details,omitempty"`
}

// setAuthHeaders sets the appropriate authentication headers on the request
// based on the auth type (certificate or basic auth).
func (c *RestOntapClient) setAuthHeaders(req *http.Request) {
	// Certificate auth is handled by the TLS transport, no header needed
	if c.authData.AuthType == models.USER_CERTIFICATE {
		return
	}

	// For basic auth, set the Authorization header
	if c.authData.Username != "" && c.authData.Password != "" {
		req.SetBasicAuth(c.authData.Username, c.authData.Password)
	}
}
