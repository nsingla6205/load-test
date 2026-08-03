package activities

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"github.com/xyproto/randomstring"
	"go.temporal.io/sdk/activity"
	"k8s.io/client-go/rest"
)

const (
	adcDeleteEndpointTemplate      = "%s/api/endpoints/%s/snapshots/%s"
	adcLogicalSizeEndpointTemplate = "%s/api/endpoints/%s"
	adcFileListEndpointTemplate    = "%s/api/endpoints/%s/snapshots/%s/files/%s"
)

var (
	RestHTTPClient       rest.HTTPClient = &http.Client{}
	GetStandardAuthToken                 = _getStandardAuthToken
)

type ADCActivity struct {
	SE database.Storage
}

type BackupDeleteAdcBody struct {
	Container      string `json:"container"`
	Port           int64  `json:"port"`
	AccessKey      string `json:"access_key"`
	SecretPassword string `json:"secret_password"`
	Server         string `json:"server"`
	ProviderType   string `json:"provider_type"`
}

type BackupDeleteAdcReq struct {
	ObjectStore BackupDeleteAdcBody `json:"object_store"`
}

// LogicalBytesResp represents the response from ADC for logical size calculation
type LogicalBytesResp struct {
	EndpointMetrics EndpointMetrics `json:"endpoint_metrics"`
}

// EndpointMetrics contains the logical size information
type EndpointMetrics struct {
	LogicalSize                uint64 `json:"logical_size"`
	CompressedBytesTransferred uint64 `json:"compressed_bytes_transferred"`
}

// LogicalBytesRespErr represents error response from ADC
type LogicalBytesRespErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// LogicalSizeResult represents the result of logical size calculation
type LogicalSizeResult struct {
	LogicalSize   uint64 `json:"logical_size"`
	OptimizedSize uint64 `json:"optimized_size"`
}

// DeployADCCloudRunService deploys the ADC service to Cloud Run
func (a *ADCActivity) DeployADCCloudRunService(ctx context.Context, params *hyperscalermodels.CloudRunServiceConfig) (*hyperscalermodels.CloudRunOperationResponse, error) {
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to get cloud service: %w", err))
	}

	response, err := cloudService.CreateCloudRunService(ctx, params)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to deploy Cloud Run service: %w", err))
	}

	return &hyperscalermodels.CloudRunOperationResponse{
		OperationName: response.OperationName,
		Status:        response.Status,
	}, nil
}

// GetADCServiceURL retrieves the URL of the deployed ADC Cloud Run service
func (a *ADCActivity) GetADCServiceURL(ctx context.Context, projectID, region, serviceName string) (string, error) {
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get cloud service: %w", err)
	}

	serviceURL, err := cloudService.GetCloudRunServiceURL(ctx, projectID, region, serviceName)
	if err != nil {
		return "", fmt.Errorf("failed to get Cloud Run service URL: %w", err)
	}

	return serviceURL, nil
}

// CleanupADCCloudRunService deletes the ADC Cloud Run service and returns the operation
func (a *ADCActivity) CleanupADCCloudRunService(ctx context.Context, projectID, region, serviceName string) (*hyperscalermodels.CloudRunOperationResponse, error) {
	activity.RecordHeartbeat(ctx, "started deleting ADC Cloud Run service")
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to get cloud service: %w", err))
	}

	response, err := cloudService.DeleteCloudRunService(ctx, projectID, region, serviceName)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to delete Cloud Run service: %w", err))
	}
	activity.RecordHeartbeat(ctx, "deleted ADC Cloud Run service")
	return &hyperscalermodels.CloudRunOperationResponse{
		OperationName: response.OperationName,
		Status:        response.Status,
	}, nil
}

func (a *ADCActivity) CreateServiceAccount(ctx context.Context, projectID string, saAccountID string, saDisplayName string) (*hyperscalermodels.ServiceAccount, error) {
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	createReq := &hyperscalermodels.CreateServiceAccountRequest{
		AccountId: saAccountID,
		ServiceAccount: &hyperscalermodels.ServiceAccount{
			DisplayName: saDisplayName,
		},
	}

	sa, err := cloudService.CreateServiceAccount(createReq, projectID, saDisplayName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return sa, nil
}

// AttachRolesToServiceAccount attach roles to service account
func (a *ADCActivity) AttachRolesToServiceAccount(ctx context.Context, projectID string, serviceAccountEmail string, roles []string) error {
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	err = cloudService.AttachOrUpdateRolesForServiceAccounts(roles, serviceAccountEmail, projectID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

// IsServiceAccountCreated check if service account is created
func (a *ADCActivity) IsServiceAccountCreated(ctx context.Context, saEmail string) (bool, error) {
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	sa, err := cloudService.GetServiceAccountByEmail(saEmail)
	if err != nil {
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return sa != nil, nil
}

func (a *ADCActivity) DeleteSA(ctx context.Context, projectID string, saAccountID string) error {
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger := cloudService.GetLogger()
	saEmail := utils.ConstructServiceAccountEmail(saAccountID, projectID)
	logger.Infof("Deleting service account %s in project %s", saEmail, projectID)
	err = cloudService.DeleteServiceAccount(projectID, saEmail)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// RemoveRolesFromServiceAccount removes specified roles from a service account
func (a *ADCActivity) RemoveRolesFromServiceAccount(ctx context.Context, projectID string, saAccountID string, roles []string) error {
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cloud service: %w", err)
	}

	saEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", saAccountID, projectID)
	logger := util.GetLogger(ctx)
	logger.Infof("Removing roles from service account %s", saEmail)

	err = cloudService.RemoveRolesFromServiceAccounts(roles, saEmail, projectID)
	if err != nil {
		logger.Errorf("Failed to remove roles from service account %s: %v", saEmail, err)
		return fmt.Errorf("failed to remove roles from service account: %w", err)
	}

	logger.Infof("Successfully removed roles from service account %s", saEmail)
	return nil
}

// InitialDeleteRequestWithCloudRun initiates delete request using Cloud Run ADC service
func (a *ADCActivity) InitialDeleteRequestWithCloudRun(ctx context.Context, adcParams *common.ADCParams, serviceURL string) (*common.ADCResponse, error) {
	logger := util.GetLogger(ctx)
	reqBody, err := ConvertADCParamsToRequest(adcParams)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to convert ADC params to request: %w", err))
	}
	reqBodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to marshal request body: %w", err))
	}

	// Use Cloud Run service URL instead of Kubernetes service URL
	initialURL := fmt.Sprintf(adcDeleteEndpointTemplate,
		serviceURL, adcParams.DestEndpointUUID, adcParams.SnapshotUUID)

	req, err := http.NewRequest("DELETE", initialURL, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		logger.Errorf("Failed to create request: %v", err)
		return nil, err
	}

	// Generate identity token for the Cloud Run service
	identityToken, err := GetStandardAuthToken(ctx, serviceURL)
	if err != nil {
		logger.Errorf("Failed to get identity token: %v", err)
		return nil, err
	}

	// Add proper headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/hal+json")
	req.Header.Set("Authorization", "Bearer "+identityToken)

	resp, err := RestHTTPClient.Do(req)
	if err != nil && (resp == nil || resp.StatusCode != http.StatusTemporaryRedirect) {
		logger.Errorf("ADC delete request error: %v", err)
		return nil, err
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			err = resp.Body.Close()
			if err != nil {
				logger.Error("failed to close response body", "error", err)
			}
		}
	}()

	// Check for error status codes
	if resp.StatusCode >= 400 {
		if resp.StatusCode == 404 {
			return &common.ADCResponse{StatusCode: resp.StatusCode}, nil
		}
		logger.Errorf("ADC delete request failed with status code: %d", resp.StatusCode)
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("ADC delete request failed with status code: %d", resp.StatusCode))
	}

	redirectURL := resp.Header.Get("Location")

	return &common.ADCResponse{
		StatusCode:  resp.StatusCode,
		RedirectURL: redirectURL,
	}, nil
}

// CheckDeleteStatusWithCloudRun checks delete status using Cloud Run ADC service
func (a *ADCActivity) CheckDeleteStatusWithCloudRun(ctx context.Context, params *common.ADCParams, serviceURL, redirectURL string) (*common.ADCResponse, error) {
	activity.RecordHeartbeat(ctx, "started checking ADC delete status")
	logger := util.GetLogger(ctx)
	if redirectURL == "" {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("missing redirect URL"))
	}
	reqBody, err := ConvertADCParamsToRequest(params)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to convert ADC params to request: %w", err))
	}
	reqBodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to marshal request body: %w", err))
	}

	// Use Cloud Run service URL for async status check
	asyncURL := fmt.Sprintf("%s%s", serviceURL, redirectURL)
	logger.Debug(fmt.Sprintf("Follow async delete URL: %s", asyncURL))

	// Create the HTTP request
	req, err := http.NewRequest("DELETE", asyncURL, bytes.NewBuffer(reqBodyBytes))
	if err != nil {
		logger.Errorf("Failed to create request: %v", err)
		return nil, err
	}

	// Generate identity token for the Cloud Run service
	identityToken, err := GetStandardAuthToken(ctx, serviceURL)
	if err != nil {
		logger.Errorf("Failed to get identity token: %v", err)
		return nil, err
	}

	// Add proper headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/hal+json")
	req.Header.Set("Authorization", "Bearer "+identityToken)

	resp, err := RestHTTPClient.Do(req)
	if err != nil && (resp == nil || resp.StatusCode != http.StatusTemporaryRedirect) {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("ADC status request error: %w", err))
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			err = resp.Body.Close()
			if err != nil {
				logger.Error("failed to close response body", "error", err)
			}
		}
	}()

	// Check for error status codes
	if resp.StatusCode >= 400 {
		logger.Errorf("ADC delete status request failed with status code: %d", resp.StatusCode)
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("ADC delete status request failed with status code: %d", resp.StatusCode))
	}

	newRedirectURL := resp.Header.Get("Location")
	activity.RecordHeartbeat(ctx, "completed checking ADC delete status")
	return &common.ADCResponse{
		StatusCode:  resp.StatusCode,
		RedirectURL: newRedirectURL,
	}, nil
}

// CheckOperationStatus checks the status of a Cloud Run operation
func (a *ADCActivity) CheckOperationStatus(ctx context.Context, operationName string) (bool, error) {
	activity.RecordHeartbeat(ctx, "started checking status of a Cloud Run operation")
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get cloud service: %w", err)
	}

	isReady, err := cloudService.CheckOperationStatus(ctx, operationName)
	if err != nil {
		return false, fmt.Errorf("failed to check operation status: %w", err)
	}
	activity.RecordHeartbeat(ctx, "completed checking status of a Cloud Run operation")
	return isReady, nil
}

// CreateHmacKeys creates HMAC keys for the service account and returns encoded keys
func (a *ADCActivity) CreateHmacKeys(ctx context.Context, params *common.HmacKeyCreateParams) (*common.HmacKeys, error) {
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to get cloud service: %w", err))
	}

	accessKey, secretKey, err := cloudService.CreateHmacKey(params.ProjectNumber, params.ServiceAccount)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to create HMAC key: %w", err))
	}
	if accessKey == nil || secretKey == nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("accessKey or secretKey is nil"))
	}

	// Encode the keys to avoid storing sensitive data in Temporal DB
	encodedAccessKey := base64.StdEncoding.EncodeToString([]byte(*accessKey))
	encodedSecretKey := base64.StdEncoding.EncodeToString([]byte(*secretKey))

	return &common.HmacKeys{
		AccessKey: encodedAccessKey,
		SecretKey: encodedSecretKey,
	}, nil
}

// GenerateResourceTimestamp generates a deterministic timestamp for resource naming
// This ensures consistency across workflow replays
func (a *ADCActivity) GenerateResourceTimestamp(ctx context.Context) (string, error) {
	// Use current time - this will be consistent across replays within the same activity execution
	timestamp := time.Now().Format("20060102150405") // YYYYMMDDHHMMSS format
	return timestamp + randomstring.HumanFriendlyEnglishString(4), nil
}

// ConvertADCParamsToRequest converts the ADC parameters to the request friendly structure.
func ConvertADCParamsToRequest(adcParams *common.ADCParams) (BackupDeleteAdcReq, error) {
	accessKeyBytes, err := base64.StdEncoding.DecodeString(adcParams.AccessKey)
	if err != nil {
		return BackupDeleteAdcReq{}, fmt.Errorf("failed to decode access key: %w", err)
	}

	// Decode the secret key
	secretKeyBytes, err := base64.StdEncoding.DecodeString(adcParams.SecretKey)
	if err != nil {
		return BackupDeleteAdcReq{}, fmt.Errorf("failed to decode secret key: %w", err)
	}
	return BackupDeleteAdcReq{
		ObjectStore: BackupDeleteAdcBody{
			Container:      adcParams.BucketName,
			Port:           adcParams.Port,
			AccessKey:      string(accessKeyBytes),
			SecretPassword: string(secretKeyBytes),
			Server:         adcParams.ServerURL,
			ProviderType:   adcParams.ProvideType,
		},
	}, nil
}

// CalculateLogicalBytesAndOptimizedBytes calculates logical bytes and optimized bytes from ADC service
func (a *ADCActivity) CalculateLogicalBytesAndOptimizedBytes(ctx context.Context, adcParams *common.ADCParams, serviceURL string) (*LogicalSizeResult, error) {
	logger := util.GetLogger(ctx)

	// Create ADC request for logical size calculation
	req, err := a.CreateADCGetRequestForLogicalSize(adcParams, serviceURL)
	if err != nil {
		logger.Errorf("Failed to create ADC request for logical size: %v", err)
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to create ADC request: %w", err))
	}

	// Generate identity token for the Cloud Run service
	identityToken, err := GetStandardAuthToken(ctx, serviceURL)
	if err != nil {
		logger.Errorf("Failed to get identity token: %v", err)
		return nil, err
	}

	// Add proper headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/hal+json")
	req.Header.Set("Authorization", "Bearer "+identityToken)

	// Make HTTP request to ADC service
	resp, err := RestHTTPClient.Do(req)
	if err != nil {
		logger.Errorf("HTTP request failed in CalculateLogicalBytesAndOptimizedBytes: %v", err)
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("HTTP request failed: %w", err))
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			err = resp.Body.Close()
			if err != nil {
				logger.Error("failed to close response body", "error", err)
			}
		}
	}()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Errorf("Failed to read response body: %v", err)
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to read response body: %w", err))
	}

	// Parse response based on status code
	status := resp.StatusCode
	if status == http.StatusOK {
		var logicalBytesResp LogicalBytesResp
		err = json.Unmarshal(body, &logicalBytesResp)
		if err != nil {
			logger.Errorf("Failed to unmarshal logical bytes response: %v", err)
			return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to unmarshal response: %w", err))
		}

		logger.Infof("Successfully retrieved logical size: %d, compressed bytes: %d",
			logicalBytesResp.EndpointMetrics.LogicalSize,
			logicalBytesResp.EndpointMetrics.CompressedBytesTransferred)

		return &LogicalSizeResult{
			LogicalSize:   logicalBytesResp.EndpointMetrics.LogicalSize,
			OptimizedSize: logicalBytesResp.EndpointMetrics.CompressedBytesTransferred,
		}, nil
	} else {
		var logicalBytesRespErr LogicalBytesRespErr
		err = json.Unmarshal(body, &logicalBytesRespErr)
		if err != nil {
			logger.Errorf("Failed to unmarshal error response: %v", err)
			return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to unmarshal error response: %w", err))
		}

		logger.Errorf("ADC logical size calculation failed with status %d: %s", status, logicalBytesRespErr.Message)
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("ADC logical size calculation failed with status %d: %s", status, logicalBytesRespErr.Message))
	}
}

// CreateADCGetRequestForLogicalSize creates HTTP request for ADC logical size calculation
func (a *ADCActivity) CreateADCGetRequestForLogicalSize(adcParams *common.ADCParams, serviceURL string) (*http.Request, error) {
	// Create URL for logical size endpoint
	adcURL := fmt.Sprintf(adcLogicalSizeEndpointTemplate, serviceURL, adcParams.DestEndpointUUID)

	// Create HTTP request
	req, err := http.NewRequest("GET", adcURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Decode HMAC keys
	accessKeyBytes, err := base64.StdEncoding.DecodeString(adcParams.AccessKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode access key: %w", err)
	}

	secretKeyBytes, err := base64.StdEncoding.DecodeString(adcParams.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode secret key: %w", err)
	}

	// Add headers for ADC authentication
	req.Header.Add("access_key", string(accessKeyBytes))
	req.Header.Add("secret_password", string(secretKeyBytes))
	req.Header.Add("port", fmt.Sprintf("%d", adcParams.Port))
	req.Header.Add("container", adcParams.BucketName)
	req.Header.Add("server", adcParams.ServerURL)
	req.Header.Add("provider_type", adcParams.ProvideType)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")

	return req, nil
}

// GetStandardAuthToken fetches a standard Google Cloud identity token for authentication
func _getStandardAuthToken(ctx context.Context, audience string) (string, error) {
	logger := util.GetLogger(ctx)

	// Get cloud service to use the wrapper method
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get cloud service: %w", err)
	}

	token, err := cloudService.GetIdentityToken(ctx, audience)
	if err != nil {
		logger.Errorf("Failed to get identity token: %v", err)
		return "", fmt.Errorf("failed to get identity token: %w", err)
	}

	return token, nil
}

// GetFileInodeNumbers gets inode numbers and sizes for a list of file paths using ADC service
// Based on cloud-backup-service ADC API: GET /api/endpoints/{endpoint_uuid}/snapshots/{snapshot_uuid}/files/{encoded_file_path}
// The response is ADCFileListResponse with a records array containing File objects with inode numbers and sizes
func (a *ADCActivity) GetFileInodeNumbers(ctx context.Context, adcParams *common.ADCParams, serviceURL string, filePaths []string) (map[string]*FileInodeAndSize, error) {
	logger := util.GetLogger(ctx)
	fileInodeSizeMap := make(map[string]*FileInodeAndSize)

	// Generate identity token for the Cloud Run service
	identityToken, err := GetStandardAuthToken(ctx, serviceURL)
	if err != nil {
		logger.Errorf("Failed to get identity token: %v", err)
		return nil, err
	}

	// Decode HMAC keys once for all requests
	accessKeyBytes, err := base64.StdEncoding.DecodeString(adcParams.AccessKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode access key: %w", err)
	}

	secretKeyBytes, err := base64.StdEncoding.DecodeString(adcParams.SecretKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode secret key: %w", err)
	}

	// Loop through each file path and get its inode number and size
	for _, filePath := range filePaths {
		// Create ADC request for getting file information (including inode and size)
		// API endpoint: GET /api/endpoints/{endpoint_uuid}/snapshots/{snapshot_uuid}/files/{encoded_file_path}
		// Encode the file path (replaces "." with "%2E" and "/" with "%2F")
		encodedFilePath := url.QueryEscape(filePath)
		adcFileListEndpoint := fmt.Sprintf(adcFileListEndpointTemplate, serviceURL, adcParams.DestEndpointUUID, adcParams.SnapshotUUID, encodedFilePath)

		req, err := http.NewRequest("GET", adcFileListEndpoint, nil)
		if err != nil {
			logger.Errorf("Failed to create HTTP request for file list: %v", err)
			return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to create HTTP request: %w", err))
		}

		// Add headers for ADC authentication (matching FrameAdcGetRequest pattern from cloud-backup-service)
		req.Header.Add("access_key", string(accessKeyBytes))
		req.Header.Add("secret_password", string(secretKeyBytes))
		req.Header.Add("port", fmt.Sprintf("%d", adcParams.Port))
		req.Header.Add("container", adcParams.BucketName)
		req.Header.Add("server", adcParams.ServerURL)
		req.Header.Add("provider_type", adcParams.ProvideType)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+identityToken)

		// Make HTTP request to ADC service
		resp, err := RestHTTPClient.Do(req)
		if err != nil {
			logger.Errorf("HTTP request failed in GetFileInodeNumbers for file %s: %v", filePath, err)
			// Continue with other files even if one fails
			continue
		}

		// Read response body
		body, err := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if closeErr != nil {
			logger.Error("failed to close response body", "error", closeErr)
		}

		if err != nil {
			logger.Errorf("Failed to read response body for file %s: %v", filePath, err)
			continue
		}

		// Parse response based on status code
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusTemporaryRedirect {
			// Response structure matches ADCFileListResponse from cloud-backup-service
			var adcFileListResponse struct {
				Files []struct {
					Inode    int    `json:"inode"`
					Size     int    `json:"size"`
					Filename string `json:"filename"`
				} `json:"records"`
				EndOfList  bool `json:"end-of-list"`
				NumRecords int  `json:"num-records"`
			}
			if err := json.Unmarshal(body, &adcFileListResponse); err != nil {
				logger.Errorf("Failed to parse ADC file list response for file %s: %v, body: %s", filePath, err, string(body))
				continue
			}

			// Check if response contains exactly one file (not a directory)
			if adcFileListResponse.NumRecords != 1 || len(adcFileListResponse.Files) != 1 {
				logger.Warnf("Expected exactly one file record for %s, but got %d records. This may be a directory.", filePath, adcFileListResponse.NumRecords)
				continue
			}

			// Extract inode number and size from the first (and only) file record
			fileRecord := adcFileListResponse.Files[0]
			inodeNumber := strconv.Itoa(fileRecord.Inode)
			if inodeNumber == "0" {
				logger.Warnf("Invalid inode number (0) returned for file %s", filePath)
				continue
			}
			fileInodeSizeMap[filePath] = &FileInodeAndSize{
				Inode: inodeNumber,
				Size:  int64(fileRecord.Size),
			}
			logger.Debugf("Successfully retrieved inode number %s and size %d for file %s", inodeNumber, fileRecord.Size, filePath)
		} else if resp.StatusCode == http.StatusNotFound {
			logger.Warnf("File not found in backup: %s (status: %d)", filePath, resp.StatusCode)
		} else if resp.StatusCode == http.StatusTooManyRequests {
			logger.Warnf("Too many requests for file %s, will retry (status: %d)", filePath, resp.StatusCode)
			// Could implement retry logic here if needed
		} else {
			logger.Warnf("Failed to get inode number for file %s, status code: %d, body: %s", filePath, resp.StatusCode, string(body))
		}
	}

	if len(fileInodeSizeMap) < len(filePaths) {
		logger.Warnf("Successfully retrieved inode numbers for %d out of %d files", len(fileInodeSizeMap), len(filePaths))
	}

	return fileInodeSizeMap, nil
}

type LogicalSizeUpdateResult struct {
	OldSize        int64
	NewSize        int64
	LatestBackupID int64
}

func (a *ADCActivity) FetchLogicalSizeAndUpdateActivity(ctx context.Context, volumeUUID string, endpointUUID string, adcParams *common.ADCParams, serviceURL string) (*LogicalSizeUpdateResult, error) {
	logger := util.GetLogger(ctx)

	if adcParams == nil || adcParams.DestEndpointUUID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("endpoint UUID is required for logical size update on volume %s", volumeUUID))
	}
	endpointUUID = adcParams.DestEndpointUUID

	// Fetch logical size from ADC
	logicalSizeResult, err := a.CalculateLogicalBytesAndOptimizedBytes(ctx, adcParams, serviceURL)
	if err != nil {
		logger.Errorf("Failed to calculate logical size from ADC: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	latestPerEndpoint, err := a.SE.GetLatestBackupsPerEndpointByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get latest backups per endpoint for volume %s: %v", volumeUUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var target *datamodel.Backup
	for _, b := range latestPerEndpoint {
		if b != nil && b.Attributes != nil && b.Attributes.EndpointUUID == endpointUUID {
			target = b
			break
		}
	}
	if target == nil {
		logger.Errorf("FetchLogicalSizeAndUpdateActivity: no AVAILABLE latest backup for endpoint %s on volume %s", endpointUUID, volumeUUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("no latest backup for endpoint %s on volume %s", endpointUUID, volumeUUID))
	}

	// Capture the old size before the overwrite so the caller can compute the drop.
	oldSize := target.LatestLogicalBackupSize
	newSize := int64(logicalSizeResult.LogicalSize)

	err = a.SE.UpdateBackupFields(ctx, target.UUID, map[string]interface{}{
		"latest_logical_backup_size": newSize,
	})
	if err != nil {
		logger.Errorf("Failed to update latest backup logical size for endpoint %s: %v", endpointUUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Keep backup_chain_histories in sync for deleted-volume / detached-vault (ADC) deletes.
	// Inherit consumer/deployment metadata from the prior active row when present.
	if histErr := a.SE.UpdateBackupChainHistory(ctx, volumeUUID, endpointUUID, newSize); histErr != nil {
		logger.Warnf("Ledger: Failed to update backup chain history for volume %s endpoint %s after ADC size refresh: %v",
			volumeUUID, endpointUUID, histErr)
		// Don't fail the activity if history update fails; backup size was already updated.
	}

	return &LogicalSizeUpdateResult{
		OldSize:        oldSize,
		NewSize:        newSize,
		LatestBackupID: target.ID,
	}, nil
}
