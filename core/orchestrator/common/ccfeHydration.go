package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	errs "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/httphelpers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	VolumeCreate                            = _hydrateVolumeCreate
	VolumeDelete                            = _hydrateVolumeDelete
	BatchHydrateCreatedSnapshots            = _batchHydrateCreatedSnapshots
	BatchHydrateDeletedSnapshots            = _batchHydrateDeletedSnapshots
	HydrateCreatedBackups                   = _hydrateCreatedBackups
	HydrateDeletedBackups                   = _hydrateDeletedBackups
	HydrateCreatedBackupVaults              = _hydrateCreatedBackupVaults
	HydrateDeletedBackupVaults              = _hydrateDeletedBackupVaults
	HydrateUpdatedPool                      = _hydrateUpdatedPool
	HydrateUpdatedVolume                    = _hydrateUpdatedVolume
	HydrateBackupVaultCrossRegionNetappUuid = _hydrateBackupVaultCrossRegionNetappUuid
	MapStateToGcpState                      = _mapStateToGcpState
	HydrateFlexCacheState                   = _hydrateFlexCacheState
	HydrateReplicationState                 = _hydrateReplicationState
	HydrateReplicationStateAndType          = _hydrateReplicationStateAndType
	ReplicationDelete                       = _hydrateReplicationDelete
	ReplicationCreate                       = _hydrateReplicationCreate
	HydrateQuotaRuleDelete                  = _hydrateQuotaRuleDelete
	HydrateQuotaRulesDelete                 = _hydrateQuotaRulesDelete
	HydrateQuotaRuleCreate                  = _hydrateQuotaRuleCreate
	GetQuotaLimit                           = _getQuotaLimit
	createHydrateCreateObject               = _createHydrateCreateObject
	hydrateToCffe                           = _hydrateToCffe
	doHydrateToCffe                         = _doHydrateToCffe
	getQuotaLimitsForResource               = _getQuotaLimitsForResource
	HydrateRetryErrors                      = []int{409, 429, 500, 503, 504}
	baseUri                                 = env.GetString("GCP_HYDRATE_BASE_URL", "")
	batchSize                               = env.GetInt("GCP_HYDRATE_BATCH_SIZE", 10)
	ApiHydrateMaxRetries                    = max(1, env.GetInt("API_HYDRATE_MAX_RETRIES", 10))
	ApiHydrateRetryDelay                    = time.Duration(env.GetInt("API_HYDRATE_RETRY_DELAY", 5)) * time.Second
	jsonMarshal                             = json.Marshal
	httpNewRequest                          = http.NewRequest
	httpClient                              *http.Client
	httpClientDo                            = httpClient.Do
	ioReadAll                               = io.ReadAll
	jsonUnmarshal                           = json.Unmarshal
	apiIdleTimeout                          = env.GetUint("API_HYDRATE_IDLE_TIMEOUT", 8)
	stringConvAtoi                          = strconv.Atoi
)

type ContextKey int
type ResourceType string
type QuotaType string

const (
	Create                                       = "batchCreate"
	Delete                                       = "batchDelete"
	ResourceTypeVolume              ResourceType = "VOLUME"
	ResourceTypeReplication         ResourceType = "REPLICATION"
	ResourceTypeInRegionReplication ResourceType = "IN_REGION_REPLICATION"
	FlexVolumesPerRegion            QuotaType    = "FLEX_VOLUMES_PER_REGION"
	FlexReplicationVolumesLimit     QuotaType    = "FLEX_REPLICATED_VOLUMES_PER_REGION"
	FlexIRRReplicationVolumesLimit  QuotaType    = "FLEX_UNIFIED_IN_REGION_REPLICATED_VOLUMES_PER_REGION"
	ResourceQuotaTypeEmpty          QuotaType    = ""
	CorrelationContextKey           ContextKey   = iota
	CorrelationIDName               string       = log.RequestCorrelationID
	BackupAssetType                              = "storage.googleapis.com/Bucket"

	// LifeCycle state in Google
	deletedGcp = "DISABLED"
	defaultGcp = "STATE_UNSPECIFIED"
)

func init() {
	ctx := context.Background()
	logger := util.GetLogger(ctx)
	httpTransportClone := http.DefaultTransport.(*http.Transport).Clone()
	if apiIdleTimeout > 0 {
		httpTransportClone.IdleConnTimeout = time.Second * ((time.Duration)(apiIdleTimeout))
	} else {
		httpTransportClone.DisableKeepAlives = true
	}
	httpClient := &http.Client{}
	loggingRoundTripper := httphelpers.GetLoggingRoundTripper("HYDRATE", logger, httpTransportClone)
	httpClient.Transport = loggingRoundTripper
	httpClientDo = httpClient.Do
}

type ccfeSuccessResponseObject struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type ccfeErrorResponseObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func _hydrateVolumeCreate(ctx context.Context, logger log.Logger, volume models.VolumeHydrateObject, location string, projectId string, token string) error {
	request := models.Request{
		Volume: &volume,
	}
	hydrateVolume := createHydrateCreateObject(request)
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/resources:%s", baseUri, projectId, location, Create)
	logger.Infof("Hydrating volume create to callbackApi, volume: %+v", volume)
	err := hydrateToCffe(ctx, logger, hydrateVolume, url, http.MethodPost, token)
	return err
}

func _hydrateVolumeDelete(ctx context.Context, logger log.Logger, volumeResourceID string, region string, projectId string, token string) error {
	nameArray := make([]string, 1)
	nameArray[0] = "volumes/" + volumeResourceID
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/resources:%s", baseUri, projectId, region, Delete)
	logger.Infof("Hydrating volume delete to callbackApi, volumeId: %s", volumeResourceID)
	err := hydrateToCffe(ctx, logger, models.GcpHydrateDelete{Names: nameArray}, url, http.MethodPost, token)
	return err
}

// _batchHydrateCreatedSnapshots hydrates created snapshots in batches to CCFE.
func _batchHydrateCreatedSnapshots(ctx context.Context, logger log.Logger, resources []models.Request, currVolumeName string, location string, projectId string, token string) error {
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s/resources:%s", baseUri, projectId, location, currVolumeName, Create)
	var err error
	batch := 0
	var requestArr []models.Request
	var uuids, resourceType string
	for i, resource := range resources {
		requestArr = append(requestArr, resource)
		batch++
		if batch == batchSize || i == len(resources)-1 {
			err := hydrateToCffe(ctx, logger, models.GcpHydrateCreate{Requests: requestArr}, url, http.MethodPost, token)
			uuids, resourceType = getAllUUIDs(requestArr)
			if err != nil {
				logger.ErrorContext(ctx, "Created Snapshot Hydration failed for this batch", "UUID's", uuids, "resourceType", resourceType, "Error", err, "VolumeName", currVolumeName)
			}
			// Reset batch and requestArr after processing the batch
			batch = 0
			requestArr = []models.Request{}
		}
	}
	logger.Infof("Successfully Hydrated snapshot create to callbackApi with the volume name %s", currVolumeName)
	return err
}

// getAllUUIDs returns all the UUIDs present in a gcp_http Request and also sends which resource type it belongs to
func getAllUUIDs(requestArr []models.Request) (string, string) {
	allUuids := ""
	if len(requestArr) == 0 {
		return allUuids, "" // Handle empty input gracefully
	}

	if requestArr[0].Snapshot != nil && requestArr[0].Snapshot.ResourceId != "" {
		for _, req := range requestArr {
			allUuids = allUuids + ", " + req.Snapshot.SnapshotId
		}
		return allUuids, "snapshot"
	}
	return allUuids, ""
}

// _batchHydrateDeletedSnapshots hydrates deleted snapshots in batches to CCFE.
func _batchHydrateDeletedSnapshots(ctx context.Context, logger log.Logger, hydrateSnapshot []models.Request, currVolumeName string, region string, projectId string, token string) error {
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s/resources:%s", baseUri, projectId, region, currVolumeName, Delete)
	var err error
	batch := 0
	var requestArr []models.Request
	var uuids, resourceType string
	for i, resource := range hydrateSnapshot {
		requestArr = append(requestArr, resource)
		batch++
		if batch == batchSize || i == len(hydrateSnapshot)-1 {
			uuids, resourceType = getAllUUIDs(requestArr)
			resource := convertDeleteResource(requestArr)
			if len(resource.Names) == 0 {
				logger.ErrorContext(ctx, "Deleted Snapshot Hydration failed for this account as the request batch has no snapshot names",
					"UUID's", uuids,
					"resourceType", resourceType,
					"Error", "No snapshot names found in the request batch. Cannot proceed with deletion.",
					"VolumeName", currVolumeName)
				batch = 0
				requestArr = []models.Request{}
				continue
			}
			err = hydrateToCffe(ctx, logger, resource, url, http.MethodPost, token)
			if err != nil {
				logger.ErrorContext(ctx, "Deleted Snapshot Hydration failed for this batch", "UUID's", uuids, "resourceType", resourceType, "Error", err, "VolumeName", currVolumeName)
			}
			batch = 0
			requestArr = []models.Request{}
		}
	}
	logger.Infof("Successfully Hydrated snapshot delete to callbackApi with the volume name %s", currVolumeName)
	return err
}

// _hydrateCreatedBackups hydrates created backups to CCFE.
func _hydrateCreatedBackups(ctx context.Context, logger log.Logger, resources []models.Request, backupVaultName string, location string, projectId string, token string) error {
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/backupVaults/%s/resources:%s", baseUri, projectId, location, backupVaultName, Create)
	err := hydrateToCffe(ctx, logger, models.GcpHydrateCreate{Requests: resources}, url, http.MethodPost, token)
	if err != nil {
		logger.Errorf("Created Backup Hydration failed for backupVault %s with error %v", backupVaultName, err)
		return err
	}
	logger.Infof("Successfully hydrated created backups to CCFE for the backupVault %s", backupVaultName)
	return nil
}

// _hydrateDeletedBackups hydrates deleted backups to CCFE.
func _hydrateDeletedBackups(ctx context.Context, logger log.Logger, names []string, backupVaultName string, location string, projectId string, token string) error {
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/backupVaults/%s/resources:%s", baseUri, projectId, location, backupVaultName, Delete)
	err := hydrateToCffe(ctx, logger, models.GcpHydrateDelete{Names: names}, url, http.MethodPost, token)
	if err != nil {
		logger.Errorf("Deleted Backup Hydration failed for backupVault %s with error %v", backupVaultName, err)
		return err
	}
	logger.Infof("Successfully hydrated deleted backups to CCFE for the backupVault %s", backupVaultName)
	return nil
}

// _hydrateCreatedBackupVaults hydrates created backup vaults to CCFE.
func _hydrateCreatedBackupVaults(ctx context.Context, logger log.Logger, resources []models.Request, backupVaultName string, location string, projectId string, token string) error {
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/resources:%s", baseUri, projectId, location, Create)
	err := hydrateToCffe(ctx, logger, models.GcpHydrateCreate{Requests: resources}, url, http.MethodPost, token)
	if err != nil {
		logger.Errorf("Created Backup Vault Hydration failed for backupVault %s with error %v", backupVaultName, err)
		return err
	}
	logger.Infof("Successfully hydrated created backup vaults to CCFE for the backupVault %s", backupVaultName)
	return nil
}

// _hydrateDeletedBackupVaults hydrates deleted backup vaults to CCFE.
func _hydrateDeletedBackupVaults(ctx context.Context, logger log.Logger, names []string, backupVaultName string, location string, projectId string, token string) error {
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/resources:%s", baseUri, projectId, location, Delete)
	err := hydrateToCffe(ctx, logger, models.GcpHydrateDelete{Names: names}, url, http.MethodPost, token)
	if err != nil {
		logger.Errorf("Deleted Backup Vault Hydration failed for backupVault %s with error %v", backupVaultName, err)
		return err
	}
	logger.Infof("Successfully hydrated deleted backup vaults to CCFE for the backupVault %s", backupVaultName)
	return nil
}

// _hydrateBackupVaultCrossRegionNetappUuid PATCHes a destination backup vault in CCFE
// to set the netapp_uuid field for cross-project cross-region backup.
// backupVaultResourceID may be a short vault name or a full resource path
// (projects/.../locations/.../backupVaults/{name}); only the vault name is used in the URL
// because project and location are already path segments.
func _hydrateBackupVaultCrossRegionNetappUuid(ctx context.Context, logger log.Logger, backupVaultResourceID, crossRegionNetappUuid, location, projectId, token string) error {
	request := &models.BackupVaultCrossRegionNetappUuidRequest{
		NetappUuid: crossRegionNetappUuid,
	}
	vaultName := path.Base(strings.TrimSpace(backupVaultResourceID))
	// path.Base returns "." for empty/whitespace input and "/" for "/", which would
	// produce an invalid CCFE URL and risk PATCHing the wrong resource.
	if vaultName == "" || vaultName == "." || vaultName == "/" {
		return fmt.Errorf("invalid backup vault resource id %q: expected a vault name or full resource path", backupVaultResourceID)
	}
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/backupVaults/%s?update_mask=netapp_uuid", baseUri, projectId, location, vaultName)
	logger.Infof("Hydrating backup vault netapp_uuid to CCFE, backupVaultResourceID: %s, vaultName: %s", backupVaultResourceID, vaultName)
	err := hydrateToCffe(ctx, logger, request, url, http.MethodPatch, token)
	return err
}

// convertDeleteResource converts a slice of requests into a GCP-compatible delete resource object.
func convertDeleteResource(requestArr []models.Request) models.GcpHydrateDelete {
	if len(requestArr) == 0 {
		return models.GcpHydrateDelete{}
	}

	if requestArr[0].Snapshot != nil && requestArr[0].Snapshot.ResourceId != "" {
		return mapToGcpBulkSnapshotDelete(requestArr)
	}
	return models.GcpHydrateDelete{}
}

// mapToGcpBulkSnapshotDelete maps a slice of requests to a GCP-compatible bulk snapshot deletion request.
func mapToGcpBulkSnapshotDelete(reqArray []models.Request) models.GcpHydrateDelete {
	nameArr := []string{} // Initialize as an empty slice
	for _, req := range reqArray {
		if req.Snapshot != nil && req.Snapshot.ResourceId != "" {
			nameArr = append(nameArr, "snapshots/"+utils.RenameSnapshotName(req.Snapshot.ResourceId))
		}
	}
	return models.GcpHydrateDelete{Names: nameArr}
}

func _hydrateFlexCacheState(ctx context.Context, logger log.Logger, region, projectId, volumeResourceID, cacheState, state, token string) error {
	request := &models.FlexCacheVolumeUpdateMaskRequest{
		CacheState: models.FlexCacheVolumeHydrateCacheState(cacheState),
		State:      models.FlexCacheVolumeHydrateState(state),
	}

	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s?update_mask=cacheState,state", baseUri, projectId, region, volumeResourceID)
	logger.Infof("Hydrating FlexCache state to callbackApi, resourceId:: %+v", volumeResourceID)
	err := hydrateToCffe(ctx, logger, request, url, http.MethodPatch, token)

	return err
}

func _hydrateReplicationState(ctx context.Context, logger log.Logger, region string, projectId string, volumeResourceID string, replicationId string, state models.VolumeReplicationHydrateState, token string) error {
	request := &models.VolumeReplicationUpdateMaskRequest{
		State: state,
	}
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s/replications/%s?update_mask=state", baseUri, projectId, region, volumeResourceID, replicationId)
	logger.Infof("Hydrating replication state to callbackApi, replicationId:: %+v", replicationId)
	err := hydrateToCffe(ctx, logger, request, url, http.MethodPatch, token)
	return err
}

func _hydrateReplicationStateAndType(ctx context.Context, logger log.Logger, region string, projectId string, volumeResourceID string, replicationId string, state models.VolumeReplicationHydrateState, hybridReplicationType datamodel.HybridReplicationParametersReplicationType, token string) error {
	request := &models.VolumeReplicationUpdateMaskRequest{
		State:                 state,
		HybridReplicationType: hybridReplicationType,
	}
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s/replications/%s?update_mask=state,hybrid_replication_type", baseUri, projectId, region, volumeResourceID, replicationId)
	logger.Infof("Hydrating replication state & type to callbackApi, replicationId:: %+v", replicationId)
	err := hydrateToCffe(ctx, logger, request, url, http.MethodPatch, token)
	return err
}

func _hydrateReplicationDelete(ctx context.Context, logger log.Logger, replicationResourceId string, volumeResourceID string, region string, projectId string, token string) error {
	nameArray := make([]string, 1)
	nameArray[0] = "replications/" + replicationResourceId
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s/resources:%s", baseUri, projectId, region, volumeResourceID, Delete)
	logger.Infof("Hydrating replication delete to callbackApi, replicationId: %+v, url: %s", replicationResourceId, url)
	err := hydrateToCffe(ctx, logger, models.GcpHydrateDelete{Names: nameArray}, url, http.MethodPost, token)
	return err
}

func _hydrateQuotaRuleDelete(ctx context.Context, logger log.Logger, quotaRuleId string, volumeId string, region string, projectId string, token string) error {
	// Backward compatibility wrapper - calls the batched version with a single quota rule
	return _hydrateQuotaRulesDelete(ctx, logger, []string{quotaRuleId}, volumeId, region, projectId, token)
}

func _hydrateQuotaRulesDelete(ctx context.Context, logger log.Logger, quotaRuleNames []string, volumeId string, region string, projectId string, token string) error {
	// Batch size 1: process one quota rule at a time for individual error handling
	for _, quotaRuleName := range quotaRuleNames {
		formattedName := "quotaRules/" + quotaRuleName
		batchedNames := []string{formattedName}
		url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s/resources:%s", baseUri, projectId, region, volumeId, Delete)
		logger.Infof("Hydrating quota rule delete to callbackApi, QuotaRuleName: %s", quotaRuleName)
		err := hydrateToCffe(ctx, logger, models.GcpHydrateDelete{Names: batchedNames}, url, http.MethodPost, token)
		if err != nil {
			return err
		}
	}
	return nil
}

func _createHydrateCreateObject(request models.Request) models.GcpHydrateCreate {
	requestArr := make([]models.Request, 1)
	requestArr[0] = request
	requestBody := models.GcpHydrateCreate{Requests: requestArr}
	return requestBody
}

func _hydrateToCffe(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
	var err error
	retryErr := utils.RetrierOnCodes(logger, func() error {
		err = doHydrateToCffe(ctx, logger, v, url, method, token)
		return err
	}, HydrateRetryErrors, ApiHydrateMaxRetries, ApiHydrateRetryDelay)

	if retryErr != nil {
		return retryErr
	}
	return err
}

func _doHydrateToCffe(ctx context.Context, logger log.Logger, v any, url string, method string, token string) error {
	bodyBytes, err := jsonMarshal(v)
	if err != nil {
		return errs.NewVCPError(errs.ErrFailedToMarshalJson, err)
	}

	req, err := httpNewRequest(method, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return errs.NewVCPError(errs.ErrFailedToCreateHTTP, err)
	}

	req.Header.Add("Authorization", "Bearer "+token)
	req.Header.Add("Content-Type", "application/json")

	if ctxCorrId, ok := ctx.Value(CorrelationContextKey).(string); ok {
		req.Header.Set(CorrelationIDName, ctxCorrId)
	} else {
		logger.Warn("x-correlation-id not present in context for CCFE hydration request")
	}

	res, err := httpClientDo(req)
	if err != nil {
		return errs.NewVCPError(errs.ErrFailedToExecuteHTTP, err)
	}
	defer func(res *http.Response) {
		err := res.Body.Close()
		if err != nil {
			logger.Error("error in response body close: ", err)
		}
	}(res)

	if res.StatusCode != 200 {
		responseBody, err := ioReadAll(res.Body)
		if err != nil {
			return errs.NewVCPError(errs.ErrFailedToReadResponse, err)
		}
		var ccfeRespStruct models.CcfeErrorResponseObject
		err = jsonUnmarshal(responseBody, &ccfeRespStruct)
		if err != nil || ccfeRespStruct.Error == nil {
			return errs.NewVCPError(errs.ErrFailedToUnmarshalCCFE, err)
		}
		return &errs.CustomError{
			HttpCode: &ccfeRespStruct.Error.Code,
			Message:  ccfeRespStruct.Error.Message,
		}
	}
	logger.Info("Hydration successfully completed")
	return nil
}

func _hydrateReplicationCreate(ctx context.Context, logger log.Logger, replication models.ReplicationHydrateObject, region string, projectId string, volumeResourceID string, token string) error {
	request := models.Request{
		Replication: &replication,
	}
	hydrateReplication := createHydrateCreateObject(request)
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s/resources:%s", baseUri, projectId, region, volumeResourceID, Create)
	logger.Infof("Hydrating replication create to callbackApi, replication: %+v", replication)
	err := hydrateToCffe(ctx, logger, hydrateReplication, url, http.MethodPost, token)
	return err
}

func _hydrateQuotaRuleCreate(ctx context.Context, logger log.Logger, quotaRule models.QuotaRuleHydrateObject, volumeResourceID string, location string, projectId string, token string) error {
	request := models.Request{
		QuotaRule: &quotaRule,
	}
	hydrateQuotaRule := createHydrateCreateObject(request)
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s/resources:%s", baseUri, projectId, location, volumeResourceID, Create)
	logger.Infof("Hydrating quotaRule create to callbackApi, quotaRule: %+v", quotaRule)
	err := hydrateToCffe(ctx, logger, hydrateQuotaRule, url, http.MethodPost, token)
	return err
}

func _getQuotaLimit(ctx context.Context, logger log.Logger, region string, projectId string, token string, resourceType ResourceType) (int, error) {
	quotaType := getResourceQuotaType(resourceType)
	logger.Infof("Calling get Quota type ,quotaType : %+v", quotaType)
	destVolumeQuota, err := getQuotaLimitsForResource(ctx, projectId, region, quotaType, token, logger)
	if err != nil {
		logger.Errorf("Error when hydrating replication: %v", err)
		return 0, err
	}
	return destVolumeQuota, nil
}

// GetQuotaLimitsForResource calls google callback API and returns the limit
func _getQuotaLimitsForResource(ctx context.Context, projectId string, region string, quotaType QuotaType, token string, logger log.Logger) (int, error) {
	url := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s:getQuotaLimit?type=%s", baseUri, projectId, region, quotaType)

	req, err := httpNewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, errs.NewVCPError(errs.ErrFailedToExecuteHTTP, err)
	}

	req.Header.Add("Authorization", "Bearer "+token)
	req.Header.Add("Content-Type", "application/json")

	if ctxCorrId, ok := ctx.Value(CorrelationContextKey).(string); ok {
		req.Header.Set(CorrelationIDName, ctxCorrId)
	} else {
		logger.Warn("x-correlation-id not present in context for CCFE hydration request")
	}

	res, err := httpClientDo(req)
	if err != nil {
		return 0, errs.NewVCPError(errs.ErrFailedToExecuteHTTP, err)
	}
	defer func(res *http.Response) {
		err := res.Body.Close()
		if err != nil {
			logger.Error("error in response body close: ", err)
		}
	}(res)

	responseBody, err := ioReadAll(res.Body)
	if err != nil {
		return 0, errs.NewVCPError(errs.ErrFailedToReadResponse, err)
	}
	if res.StatusCode == 200 {
		var quotaObject ccfeSuccessResponseObject
		err = jsonUnmarshal(responseBody, &quotaObject)
		if err != nil {
			return 0, errs.NewVCPError(errs.ErrFailedToUnmarshalCCFE, err)
		}
		quota, err := stringConvAtoi(quotaObject.Value)
		if err != nil {
			return 0, errs.NewVCPError(errs.ErrFailedToReadQuota, err)
		}
		return quota, nil
	} else {
		var errorObject ccfeErrorResponseObject
		err = jsonUnmarshal(responseBody, &errorObject)
		if err != nil {
			return 0, errs.NewVCPError(errs.ErrFailedToUnmarshalCCFE, err)
		}
		return 0, &errs.CustomError{
			OriginalErr: errs.WrapAsTemporalApplicationError(err),
			Message:     errorObject.Message,
			HttpCode:    &errorObject.Code,
		}
	}
}

func getResourceQuotaType(resourceType ResourceType) QuotaType {
	switch resourceType {
	case ResourceTypeVolume:
		return FlexVolumesPerRegion
	case ResourceTypeReplication:
		return FlexReplicationVolumesLimit
	case ResourceTypeInRegionReplication:
		return FlexIRRReplicationVolumesLimit
	}
	return ResourceQuotaTypeEmpty
}

// _mapStateToGcpState maps a local state string to its corresponding GCP-compatible state string.
func _mapStateToGcpState(state string) string {
	switch state {
	case datamodel.LifeCycleStateDeleted:
		return deletedGcp
	case datamodel.LifeCycleStateAvailable:
		return datamodel.LifeCycleStateREADY
	case "":
		return defaultGcp
	default:
		return state
	}
}

func _hydrateUpdatedPool(ctx context.Context, poolHydrateObj models.PoolHydrateObject, token string) error {
	logger := util.GetLogger(ctx)
	updateMask := "state"
	updatePoolPayload := models.PoolUpdateCCFERequest{State: poolHydrateObj.State}
	if poolHydrateObj.HotTierSizeGib > 0 {
		// TODO: Need to confirm on the update mask value for hot_tier_size_gib as per VCP
		updateMask = updateMask + ",hot_tier_size_gib"
		updatePoolPayload.HotTierSizeGib = poolHydrateObj.HotTierSizeGib
	}
	fullUrl := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/storagePools/%s?update_mask=%s", baseUri, poolHydrateObj.OwnerID, poolHydrateObj.Region, poolHydrateObj.Name, updateMask)
	err := hydrateToCffe(ctx, logger, updatePoolPayload, fullUrl, http.MethodPatch, token)
	if err != nil {
		logger.Errorf("Failed to hydrate updated pool to CCFE, poolID: %s, error: %v", poolHydrateObj.Name, err)
		return err
	}
	return nil
}

func _hydrateUpdatedVolume(ctx context.Context, volumeHydrateObj models.VolumeUpdateCCFERequest, region, projectId, volumeResourceID, token string) error {
	logger := util.GetLogger(ctx)
	fullURL := fmt.Sprintf("%s/v1internal/projects/%s/locations/%s/volumes/%s?update_mask=cloneDetails", baseUri, projectId, region, volumeResourceID)
	err := hydrateToCffe(ctx, logger, volumeHydrateObj, fullURL, http.MethodPatch, token)
	if err != nil {
		logger.Errorf("Failed to hydrate updated volume to CCFE, volumeID: %s, error: %v", volumeResourceID, err)
		return err
	}
	return nil
}

// ConvertToGCPHydrateBackupCreateRequests converts a slice of Backup objects to GCP hydrate create requests.
// mode should be models.BackupHydrationModeONTAP for ONTAP (expert mode) pools, or models.BackupHydrationModeDefault otherwise.
// sourceStoragePool is the full GCP resource path of the source storage pool
// (e.g. "projects/{project}/locations/{region}/storagePools/{name}").
// It is only included in the result when mode is BackupHydrationModeONTAP and the value is non-empty.
// Returns a slice of Request objects.
func ConvertToGCPHydrateBackupCreateRequests(backups []*datamodel.Backup, mode string, sourceStoragePool string) []models.Request {
	var requests []models.Request
	for _, backup := range backups {
		sourceVolume := utils.GetSourceVolumePathFromBackup(backup)
		if backup.Attributes != nil && backup.Attributes.IsExpertModeBackup && backup.Attributes.VolumeName != "" {
			// Keep the full path format but replace the volume name segment with the ONTAP volume UUID.
			if parts := strings.Split(sourceVolume, "/"); len(parts) > 0 && parts[len(parts)-1] == backup.Attributes.VolumeName {
				parts[len(parts)-1] = backup.VolumeUUID
				sourceVolume = strings.Join(parts, "/")
			}
		}
		volumeUsageInBytes := uint64(backup.SizeInBytes)

		var protocols []string
		if backup.Attributes != nil {
			protocols = backup.Attributes.Protocols
		}

		request := models.Request{Backup: &models.HydrateBackup{
			ResourceId:       backup.Name,
			BackupId:         backup.UUID,
			VolumeUsageBytes: &volumeUsageInBytes,
			SourceVolume:     sourceVolume,
			SourceVolumeDetails: &models.SourceVolumeDetails{
				VolumeProtocols: protocols,
			},
			Mode: mode,
		}}

		if mode == models.BackupHydrationModeONTAP && sourceStoragePool != "" {
			request.Backup.SourceStoragePool = sourceStoragePool
		}

		if backup.Attributes != nil && backup.Attributes.BucketName != "" {
			assetLocationMetadata := getOrCreateAssetLocationMetadata(request.Backup)
			assetLocationMetadata.ChildAssets = append(assetLocationMetadata.ChildAssets, &models.ChildAsset{
				AssetType:  BackupAssetType,
				AssetNames: []string{fmt.Sprintf("//storage.googleapis.com/%s", backup.Attributes.BucketName)},
			})
		}

		requests = append(requests, request)
	}
	return requests
}

// getOrCreateAssetLocationMetadata safely gets or creates AssetLocationMetadata
// Returns the existing instance if it exists, or creates a new one if it's nil
func getOrCreateAssetLocationMetadata(backup *models.HydrateBackup) *models.AssetLocationMetadata {
	if backup.AssetLocationMetadata == nil {
		backup.AssetLocationMetadata = &models.AssetLocationMetadata{
			ChildAssets: []*models.ChildAsset{},
		}
	}
	return backup.AssetLocationMetadata
}

// ConvertToGCPHydrateBackupDeleteRequests converts a slice of Backup objects to a slice of backup names for deletion.
// Returns a slice of strings.
func ConvertToGCPHydrateBackupDeleteRequests(backups []*datamodel.Backup) []string {
	var names []string
	for _, backup := range backups {
		names = append(names, fmt.Sprintf("backups/%s", backup.Name))
	}
	return names
}

// ConvertToGCPHydrateBackupVaultCreateRequests converts a slice of BackupVault objects to GCP hydrate create requests.
// Returns a slice of Request objects, each containing a HydrateBackupVault with ResourceId, BackupVaultId,
// BackupVaultType, and BackupRegion populated from the corresponding BackupVault.
func ConvertToGCPHydrateBackupVaultCreateRequests(backupVaults []*datamodel.BackupVault) []models.Request {
	var requests []models.Request
	for _, backupVault := range backupVaults {
		request := models.Request{BackupVault: &models.HydrateBackupVault{
			ResourceId:      backupVault.Name,
			BackupVaultId:   backupVault.UUID,
			BackupVaultType: backupVault.BackupVaultType,
			BackupRegion:    *backupVault.BackupRegionName,
		}}
		if backupVault.CmekAttributes != nil {
			request.BackupVault.BackupsKmsKey = backupVault.CmekAttributes.BackupsPrimaryKeyVersion
			request.BackupVault.KmsConfigResourcePath = backupVault.CmekAttributes.KmsConfigResourcePath
		}
		requests = append(requests, request)
	}
	return requests
}

// ConvertToGCPHydrateBackupVaultDeleteRequests converts a slice of BackupVault objects to a slice of backup vault names for deletion.
// Returns a slice of strings, each formatted as "backupVaults/{name}" where {name} is the BackupVault's Name field.
func ConvertToGCPHydrateBackupVaultDeleteRequests(backupVaults []*datamodel.BackupVault) []string {
	var names []string
	for _, backupVault := range backupVaults {
		names = append(names, fmt.Sprintf("backupVaults/%s", backupVault.Name))
	}
	return names
}
