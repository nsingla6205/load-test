package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-faster/jx"
	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/active_directories"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/pools"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	regionalPoolEnabled                    = env.GetBool("REGIONAL_SUPPORT_ENABLED", false)
	minCustomThroughput                    = utils.MinCustomThroughput
	getAndSyncKmsConfigForPool             = _getAndSyncKmsConfigForPool
	enableLdap                             = env.GetBool("ENABLE_LDAP", false)
	blockUpdatePooltoATPool                = env.GetBool("BLOCK_UPDATE_POOL_TO_AT_POOL", false)
	enableMqos                             = env.GetBool("ENABLE_MQOS", true)
	enableVolumePerformanceGroupAssignment = env.GetBool("ENABLE_VOLUME_PERFORMANCE_GROUP_ASSIGNMENT", false)
	enableLargeCapacityPools               = env.GetBool("ENABLE_LARGE_CAPACITY_POOLS", true)
	enableTrueZonalLV                      = env.GetBool("ENABLE_TRUE_ZONAL_LV", false)
	enableTrueZonalGA                      = env.GetBool("ENABLE_TRUE_ZONAL_GA", false)
	trueZonalLVPoolMinSize                 = env.GetUint64("TRUE_ZONAL_LV_POOL_MIN_SIZE", 12*utils.TiBInBytes)
	trueZonalLV24NodePoolMinSize           = env.GetUint64("TRUE_ZONAL_LV_24_NODE_POOL_MIN_SIZE", 20*utils.TiBInBytes)
	enablePOPremiumPools                   = env.GetBool(env.EnvPOPremiumPoolsEnabled, false)
	enablePOCmek                           = env.GetBool(env.EnvPOCmekEnabled, false)
)

const (
	HTTP_BAD_REQUEST_CODE = 400
	maxRuneCount          = 63
	maxByteCount          = 128
)

func resolvePerformanceParams(reqThroughput gcpgenserver.OptNilFloat64, reqIops gcpgenserver.OptNilFloat64, isPO bool) (throughput int64, iops *int64) {
	// Set default throughput if not provided (non-PO pools only).
	if reqThroughput.IsSet() {
		throughput = int64(reqThroughput.Value)
	} else if !isPO {
		throughput = int64(minCustomThroughput)
	}

	// Set IOPS based on throughput if not provided, otherwise use provided value
	if reqIops.IsSet() {
		value := int64(reqIops.Value)
		iops = &value
	} else {
		// Leave IOPS as nil - orchestrator will calculate from throughput if needed
		iops = nil
	}

	return throughput, iops
}

// isPOCreateRequest reports whether the API request targets the PO pool path.
func isPOCreateRequest(req *gcpgenserver.PoolV1beta) bool {
	if req == nil {
		return false
	}
	mode := commonparams.DEFAULTMode
	if string(req.Mode.Value) == string(gcpgenserver.PoolV1betaModeONTAP) {
		mode = commonparams.ONTAPMode
	}
	return mode == commonparams.ONTAPMode && commonparams.IsPOServiceLevel(string(req.ServiceLevel))
}

// V1betaDescribePool handles the request to describe a pool.
func (h Handler) V1betaDescribePool(ctx context.Context, params gcpgenserver.V1betaDescribePoolParams) (gcpgenserver.V1betaDescribePoolRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	// Validate the location
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDescribePoolBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	pool, err := h.Orchestrator.DescribePool(ctx, params.PoolId, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("Pool not found", "uuid", params.PoolId)
			return &gcpgenserver.V1betaDescribePoolNotFound{
				Code:    404,
				Message: "Pool not found",
			}, nil
		}
		logger.Error("Failed to describe pool", "error", err.Error())
		return &gcpgenserver.V1betaDescribePoolInternalServerError{}, err
	}
	return convertToPoolV1BetaWithConsumption(pool), nil
}

// V1betaCreatePool handles the request to create a pool.
func (h Handler) V1betaCreatePool(ctx context.Context, req *gcpgenserver.PoolV1beta, params gcpgenserver.V1betaCreatePoolParams) (gcpgenserver.V1betaCreatePoolRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	region, zone, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaCreatePoolBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	isRegionalHA := zone == ""

	validateErr := validateCreatePoolParams(req, zone)
	if validateErr != nil {
		switch validateErr.Code {
		case http.StatusBadRequest:
			return &gcpgenserver.V1betaCreatePoolBadRequest{
				Code:    validateErr.Code,
				Message: validateErr.Message,
			}, nil
		default:
			return &gcpgenserver.V1betaCreatePoolInternalServerError{
				Code:    validateErr.Code,
				Message: validateErr.Message,
			}, nil
		}
	}

	vendorId := fmt.Sprintf("/projects/%v/locations/%v/pools/%s", params.ProjectNumber, params.LocationId, req.ResourceId)
	// Check if the pool already exists
	existingPool, err := h.Orchestrator.GetPoolByVendorID(ctx, vendorId, params.ProjectNumber)
	if err == nil {
		logger.Info("Pool already exists", "vendorId", vendorId)
		res, err2 := handleExistingPool(ctx, req, params, existingPool, h.Orchestrator)
		return res, err2
	} else if !errors.IsNotFoundErr(err) {
		logger.Error("Failed to check existing pool", "error", err.Error())
		return &gcpgenserver.V1betaCreatePoolInternalServerError{}, err
	}

	primaryZone := ""
	if !nillable.IsNilOrEmpty(&zone) {
		primaryZone = zone
	} else {
		primaryZone = req.Zone.Value
	}

	secondaryZone := ""
	if req.SecondaryZone.IsSet() {
		secondaryZone = req.SecondaryZone.Value
	}

	// Resolve performance parameters with defaults and auto-calculation
	isPOCreate := isPOCreateRequest(req)
	totalThroughput, totalIops := resolvePerformanceParams(req.TotalThroughputMibps, req.TotalIops, isPOCreate)

	largeCapacity := req.LargeCapacity.Value || isPOCreate

	isPreview := false
	isTrueZonal := !isPOCreate && isTrueZonal(isRegionalHA, largeCapacity, secondaryZone)
	if !enableTrueZonalGA && isTrueZonal {
		if msg := validateTrueZonalAutoTiering(req.AllowAutoTiering.IsSet(), req.AllowAutoTiering.Value, req.EnableHotTierAutoResize.IsSet(), req.EnableHotTierAutoResize.Value); msg != "" {
			return &gcpgenserver.V1betaCreatePoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: msg,
			}, nil
		}
		isPreview = true
	}

	hotTierSizeInBytes := uint64(req.SizeInBytes)
	if req.AllowAutoTiering.IsSet() && req.AllowAutoTiering.Value {
		hotTierSizeInBytes = uint64(req.HotTierSizeInBytes.Value)
	}
	qosType := req.QosType.Value
	if isPOCreate {
		qosType = ""
	}
	lvHAPairs := 0
	if isTrueZonal {
		if largeCapacity {
			lvHAPairs = int(resolveLvHAPairs(req.LargeCapacityConfig.Value))
			if msg := validateTrueZonalLVPoolSize(uint64(req.SizeInBytes), int64(lvHAPairs)); msg != "" {
				return &gcpgenserver.V1betaCreatePoolBadRequest{
					Code:    http.StatusBadRequest,
					Message: msg,
				}, nil
			}
		}
		secondaryZone = primaryZone
	}

	createPoolParams := &commonparams.CreatePoolParams{
		AccountName:             params.ProjectNumber,
		Region:                  region,
		PrimaryZone:             primaryZone,
		SecondaryZone:           secondaryZone,
		IsRegionalHA:            isRegionalHA,
		Name:                    req.ResourceId,
		Description:             req.Description.Value,
		VendorID:                vendorId,
		VendorSubNetID:          req.Network,
		ServiceLevel:            string(req.ServiceLevel),
		SizeInBytes:             uint64(req.SizeInBytes),
		QosType:                 qosType,
		AllowAutoTiering:        req.AllowAutoTiering.Value,
		HotTierSizeInBytes:      hotTierSizeInBytes,
		EnableHotTierAutoResize: req.EnableHotTierAutoResize.Value,
		CustomPerformanceParams: &commonparams.CustomPerformanceParams{ThroughputMibps: totalThroughput, Enabled: req.CustomPerformanceEnabled.Value, Iops: totalIops},
		LargeCapacity:           largeCapacity,
		XCorrelationID:          params.XCorrelationID.Value,
		IsTrueZonal:             isTrueZonal,
		IsPreview:               isPreview,
		LvHAPairs:               int64(lvHAPairs),
	}

	if string(req.Mode.Value) == string(gcpgenserver.PoolV1betaModeMODEUNSPECIFIED) || string(req.Mode.Value) == string(gcpgenserver.PoolV1betaModeDEFAULT) {
		createPoolParams.Mode = commonparams.DEFAULTMode
	} else {
		createPoolParams.Mode = commonparams.ONTAPMode
	}

	// PO (Performance Offering) pool fields: ONTAP mode + EXTREME|PREMIUM selects the PO path.
	if req.ClusterName.IsSet() {
		createPoolParams.ClusterName = req.ClusterName.Value
	}

	// Set AD related params
	adConfig, ifADExistsInVCP, adErr := getAndSyncAdConfigForPool(ctx, req.ActiveDirectoryConfigId, createPoolParams.AccountName, createPoolParams.Region, createPoolParams.XCorrelationID, h.Orchestrator)
	if adErr != nil {
		return adErr.toCreateResponse(), nil
	}
	createPoolParams.ADExistsInVCP = ifADExistsInVCP
	if adConfig != nil {
		createPoolParams.ActiveDirectoryId = adConfig.UUID
		createPoolParams.ActiveDirectory = adConfig
	}

	// Set LDAP enabled param
	if req.LdapEnabled.IsSet() {
		createPoolParams.LdapEnabled = req.LdapEnabled.Value
	} else {
		createPoolParams.LdapEnabled = false
	}

	// Set kms config related params if kms config is provided
	kmsConfig, errResp := getAndSyncKmsConfigForPool(ctx, req, createPoolParams, h.Orchestrator)
	if errResp != nil {
		return errResp, nil
	}
	if kmsConfig != nil {
		createPoolParams.KmsConfigId = kmsConfig.UUID
		createPoolParams.KmsConfigResourceID = kmsConfig.ResourceID
		createPoolParams.KmsConfig = kmsConfig
	}

	if req.Labels.IsSet() {
		jsonbLabels, err := validateLabels(req.Labels.Value)
		if err != nil {
			return &gcpgenserver.V1betaCreatePoolBadRequest{
				Code:    HTTP_BAD_REQUEST_CODE,
				Message: err.Error(),
			}, nil
		}
		createPoolParams.Labels = jsonbLabels
	}

	createPoolParams.TrialMode = newTrialModeParamsFromOpt(req.TrialMode)

	created, operationID, err := h.Orchestrator.CreatePool(ctx, createPoolParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaCreatePoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		}
		return &gcpgenserver.V1betaCreatePoolInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}
	resp, err := encodePoolV1(convertToPoolV1Beta(created))
	if err != nil {
		return nil, err
	}
	if operationID != "" {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{}, nil
}

func handleExistingPool(ctx context.Context, req *gcpgenserver.PoolV1beta, params gcpgenserver.V1betaCreatePoolParams, existingPool *models.Pool, orchestrator factory.OrchestratorFactory) (gcpgenserver.V1betaCreatePoolRes, error) {
	logger := util.GetLogger(ctx)
	if existingPool.State != datamodel.LifeCycleStateCreating {
		// Pool exists and is not in creating state, return 409 Conflict
		return &gcpgenserver.V1betaCreatePoolConflict{
			Code:    409,
			Message: fmt.Sprintf("Pool with resource_id '%s' already exists", req.ResourceId),
		}, nil
	} else {
		resp, err := encodePoolV1(convertToPoolV1Beta(existingPool))
		if err != nil {
			logger.Error("Failed to encode existing pool response", "error", err.Error())
			return &gcpgenserver.V1betaCreatePoolInternalServerError{}, err
		}
		// Pool is in creating state, find the existing job and return same operation
		poolCategory := models.GetPoolCategory(common.GetBoolOrDefault(req.LargeCapacity, false))
		jobType := string(models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationCreate, poolCategory))
		job, jobErr := orchestrator.GetJobByResourceUUID(ctx, existingPool.UUID, jobType)
		if jobErr != nil {
			logger.Error("Failed to find job for creating pool", "poolUUID", existingPool.UUID, "error", jobErr.Error())
			// Return the pool response even if job lookup fails
			return &gcpgenserver.OperationV1beta{
				Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, uuid.UUID{}.String())), // Dummy operation ID
				Response: resp,
				Done:     gcpgenserver.NewOptBool(true), // Mark as done since we can't track the job
			}, nil
		}
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(job.State == datamodel.JobsStateDONE || job.State == datamodel.JobsStateERROR), // Done if job is in DONE or ERROR state
		}, nil
	}
}

// V1betaDeletePool handles the request to delete a pool.
func (h Handler) V1betaDeletePool(ctx context.Context, params gcpgenserver.V1betaDeletePoolParams) (gcpgenserver.V1betaDeletePoolRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	// Validate the location
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDeletePoolBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	// Check if the pool exists
	existingPool, err := h.Orchestrator.DescribePool(ctx, params.PoolId, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("Pool not found", "uuid", params.PoolId)
			return &gcpgenserver.V1betaDeletePoolNotFound{
				Code:    404,
				Message: "Pool not found",
			}, nil
		} else {
			logger.Error("Failed to check existing pool", "error", err.Error())
			return &gcpgenserver.V1betaDeletePoolInternalServerError{}, err
		}
	}

	dummyOperationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, uuid.UUID{}.String())
	if existingPool != nil {
		switch existingPool.State {
		case datamodel.LifeCycleStateDeleting:
			log := util.GetLogger(ctx)
			poolCategory := models.GetPoolCategory(existingPool.LargeCapacity)
			job, jobErr := h.Orchestrator.GetJobByResourceUUID(ctx, existingPool.UUID, string(models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationDelete, poolCategory)))
			if jobErr != nil {
				log.Error("Failed to find job for deleting pool", "poolUUID", existingPool.UUID, "error", jobErr.Error())
				// Return the pool response even if job lookup fails
				return &gcpgenserver.OperationV1beta{
					Name: gcpgenserver.NewOptString(dummyOperationID), // Dummy operation ID
					Done: gcpgenserver.NewOptBool(true),               // Mark as done since we can't find the job
				}, nil
			}
			operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)
			return &gcpgenserver.OperationV1beta{
				Name: gcpgenserver.NewOptString(operationID),
				Done: gcpgenserver.NewOptBool(job.State == datamodel.JobsStateDONE || job.State == datamodel.JobsStateERROR), // Done if job is in DONE or ERROR state
			}, nil
		case datamodel.LifeCycleStateCreating:
			if params.XCorrelationID.IsSet() && params.XCorrelationID.Value != "" {
				log := util.GetLogger(ctx)
				poolCategory := models.GetPoolCategory(existingPool.LargeCapacity)
				deleteJobType := string(models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationDelete, poolCategory))
				job, jobErr := h.Orchestrator.GetJobByResourceUUID(ctx, existingPool.UUID, deleteJobType)
				if jobErr == nil && job != nil {
					// Checking if correlation ID matches - return existing job for idempotency
					if job.CorrelationID == params.XCorrelationID.Value {
						log.Infof("Found existing delete job %s for pool %s in CREATING state with matching correlation ID %s (cleanup case), returning existing job UUID",
							job.UUID, existingPool.UUID, params.XCorrelationID.Value)
						operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)
						return &gcpgenserver.OperationV1beta{
							Name: gcpgenserver.NewOptString(operationID),
							Done: gcpgenserver.NewOptBool(job.State == datamodel.JobsStateDONE || job.State == datamodel.JobsStateERROR),
						}, nil
					}
				}
			}
		case datamodel.LifeCycleStateUpdating:
			msg := "Error deleting pool - Pool is already transitioning between states"
			return &gcpgenserver.V1betaDeletePoolConflict{
				Code:    409,
				Message: msg,
			}, nil
		}
	}

	if existingPool != nil && existingPool.DeletedAt != nil {
		return &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(dummyOperationID),
			Done: gcpgenserver.NewOptBool(true),
		}, nil
	}
	deletePoolParams := &commonparams.DeletePoolParams{
		AccountName: params.ProjectNumber,
		PoolID:      params.PoolId,
	}
	// Delete the pool
	deleted, operationID, err := h.Orchestrator.DeletePool(ctx, deletePoolParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("Pool not found", "uuid", params.PoolId)
			return &gcpgenserver.OperationV1beta{
				Name: gcpgenserver.NewOptString(dummyOperationID),
				Done: gcpgenserver.NewOptBool(true),
			}, nil
		}
		if errors.IsBadRequestErr(err) {
			logger.Info("Pool has volume", "uuid", params.PoolId)
			return &gcpgenserver.V1betaDeletePoolConflict{
				Code:    400,
				Message: "Pool has active volumes",
			}, nil
		}
		if errors.IsConflictErr(err) {
			logger.Info("Pool is in transition state", "uuid", params.PoolId)
			return &gcpgenserver.V1betaDeletePoolConflict{
				Code:    409,
				Message: "Error deleting pool - Pool is already transitioning between states",
			}, nil
		}
		logger.Error("Failed to delete pool", "error", err.Error())
		return &gcpgenserver.V1betaDeletePoolInternalServerError{}, err
	}
	resp, err := encodePoolV1(convertToPoolV1Beta(deleted))
	if err != nil {
		return nil, err
	}
	if deleted.State == datamodel.LifeCycleStateDeleting || deleted.State == datamodel.LifeCycleStateCreating {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}

	logger.Info("Pool deleted successfully", "PoolID", params.PoolId)
	return &gcpgenserver.V1betaDeletePoolNoContent{}, nil
}

// V1betaGetMultiplePools handles the request to get multiple pools.
func (h Handler) V1betaGetMultiplePools(ctx context.Context, req *gcpgenserver.PoolIdListV1beta, params gcpgenserver.V1betaGetMultiplePoolsParams) (gcpgenserver.V1betaGetMultiplePoolsRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	// Validate the location first
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaGetMultiplePoolsBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req.PoolUuids == nil {
		return &gcpgenserver.V1betaGetMultiplePoolsBadRequest{
			Code:    400,
			Message: "PoolUUIDs is required",
		}, nil
	}

	if len(req.PoolUuids) > 1000 {
		return &gcpgenserver.V1betaGetMultiplePoolsBadRequest{
			Code:    float64(400),
			Message: "poolUUIDs in body should have at most 1000 items",
		}, nil
	}

	// Query VCP first
	poolsModelVCP, err := h.Orchestrator.GetMultiplePools(ctx, params.ProjectNumber, req.PoolUuids)
	if err != nil {
		logger.Error("Failed to get multiple pools", "error", err.Error())
		return &gcpgenserver.V1betaGetMultiplePoolsInternalServerError{
			Code:    500,
			Message: "Internal server error while getting pools",
		}, nil
	}

	poolsVCP := make([]gcpgenserver.PoolV1beta, 0, len(req.PoolUuids))
	foundPoolUUIDs := make(map[string]struct{}, len(poolsModelVCP))
	for _, pool := range poolsModelVCP {
		response := convertToPoolV1BetaWithConsumption(pool)
		poolsVCP = append(poolsVCP, *response)
		foundPoolUUIDs[pool.UUID] = struct{}{}
	}

	// If all pools are found in VCP, just return them.
	if len(req.PoolUuids) == len(poolsVCP) {
		logger.Info("All pools found in VCP", "pools", poolsVCP)
		return &gcpgenserver.V1betaGetMultiplePoolsOK{
			Pools: poolsVCP,
		}, nil
	}

	// Only call CVP if CVP_HOST is set.
	// logger.Info("DEBUG: CVP_HOST value in handler", "cvpHost", cvpHost, "os.Getenv", os.Getenv("CVP_HOST"))
	if cvp.CVP_HOST == "" {
		logger.Info("CVP_HOST environment variable is not set, skipping CVP call", "foundPools", len(poolsVCP), "requestedPools", len(req.PoolUuids))
		return &gcpgenserver.V1betaGetMultiplePoolsOK{
			Pools: poolsVCP,
		}, nil
	}

	// Figure out which pools are missing and need to be fetched from CVP
	missingPoolUUIDs := helper.FindMissingUUIDs(req.PoolUuids, foundPoolUUIDs)

	// If no pools are missing (e.g. due to duplicates in request), we don't need to call CVP
	if len(missingPoolUUIDs) == 0 {
		return &gcpgenserver.V1betaGetMultiplePoolsOK{
			Pools: poolsVCP,
		}, nil
	}

	logger.Debug("Some pools not found in VCP, fetching from CVP", "missingPools", missingPoolUUIDs)
	return getMultiplePoolsFromCVP(ctx, missingPoolUUIDs, params, poolsVCP)
}

func getMultiplePoolsFromCVP(ctx context.Context, missingPoolUUIDs []string, params gcpgenserver.V1betaGetMultiplePoolsParams, vcpPools []gcpgenserver.PoolV1beta) (gcpgenserver.V1betaGetMultiplePoolsRes, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)

	getMultiplePoolsParams := &pools.V1betaGetMultiplePoolsParams{
		LocationID:    params.LocationId,
		ProjectNumber: params.ProjectNumber,
		Body: &cvpmodels.PoolIDListV1beta{
			PoolUUIDs: missingPoolUUIDs,
		},
	}
	resp, err := cvpClient.Pools.V1betaGetMultiplePools(getMultiplePoolsParams)
	if err != nil {
		switch e := err.(type) {
		case *pools.V1betaGetMultiplePoolsBadRequest:
			return &gcpgenserver.V1betaGetMultiplePoolsBadRequest{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *pools.V1betaGetMultiplePoolsUnauthorized:
			return &gcpgenserver.V1betaGetMultiplePoolsUnauthorized{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *pools.V1betaGetMultiplePoolsForbidden:
			return &gcpgenserver.V1betaGetMultiplePoolsForbidden{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *pools.V1betaGetMultiplePoolsNotFound:
			return &gcpgenserver.V1betaGetMultiplePoolsNotFound{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *pools.V1betaGetMultiplePoolsInternalServerError:
			return &gcpgenserver.V1betaGetMultiplePoolsInternalServerError{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		}
	}

	var cvpPools []gcpgenserver.PoolV1beta
	if resp != nil && resp.Payload != nil && resp.Payload.Pools != nil {
		cvpPools = append(cvpPools, convertToPoolsV1beta(resp.Payload.Pools)...)
	}

	// Combine VCP and CVP pools
	allPools := append(vcpPools, cvpPools...)
	return &gcpgenserver.V1betaGetMultiplePoolsOK{
		Pools: allPools,
	}, nil
}

// V1betaListPools handles the request to list pools.
func (h Handler) V1betaListPools(ctx context.Context, params gcpgenserver.V1betaListPoolsParams) (gcpgenserver.V1betaListPoolsRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	// Validate the location
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaListPoolsBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	includeDeleted := false
	if params.IncludeDeleted.IsSet() {
		includeDeleted = params.IncludeDeleted.Value
	}

	poolList, err := h.Orchestrator.ListPools(ctx, params.ProjectNumber, includeDeleted)
	if err != nil {
		return &gcpgenserver.V1betaListPoolsInternalServerError{}, err
	}

	logger.Info("Pools found", "pools", poolList)
	return &gcpgenserver.V1betaListPoolsOK{
		Pools: convertToPoolsV1Beta(poolList),
	}, nil
}

// V1betaGetBackupConfigsForPool handles the request to get backup configurations for a pool.
func (h Handler) V1betaGetBackupConfigsForPool(ctx context.Context, params gcpgenserver.V1betaGetBackupConfigsForPoolParams) (gcpgenserver.V1betaGetBackupConfigsForPoolRes, error) {
	logger := util.GetLogger(ctx)
	if !ExpertModeBackupEnabled {
		msg := "Backup for ONTAP mode pools is currently not enabled."
		logger.Error(msg)
		return &gcpgenserver.V1betaGetBackupConfigsForPoolBadRequest{
			Code:    400,
			Message: msg,
		}, nil
	}
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	// Validate the location
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaGetBackupConfigsForPoolBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	// Get backup configs for the pool
	backupConfigs, err := h.Orchestrator.GetBackupConfigsForPool(ctx, params.PoolId, params.ProjectNumber, params.LocationId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("Pool not found", "poolId", params.PoolId)
			return &gcpgenserver.V1betaGetBackupConfigsForPoolNotFound{
				Code:    404,
				Message: "Pool not found",
			}, nil
		}
		if errors.IsBadRequestErr(err) {
			return &gcpgenserver.V1betaGetBackupConfigsForPoolBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to get backup configs for pool", "poolId", params.PoolId, "error", err.Error())
		return &gcpgenserver.V1betaGetBackupConfigsForPoolInternalServerError{}, err
	}

	// Convert to API response format
	responseConfigs := make([]gcpgenserver.VolumeBackupConfigV1beta, 0, len(backupConfigs))
	for _, config := range backupConfigs {
		apiConfig := gcpgenserver.VolumeBackupConfigV1beta{
			VolumeId: gcpgenserver.NewOptString(config.VolumeResourceID),
		}

		if config.BackupVaultPath != nil || config.BackupPolicyPath != nil || config.ScheduledBackupEnabled != nil || config.BackupChainBytes != nil {
			bc := gcpgenserver.BackupConfigV1beta{}
			if config.BackupVaultPath != nil {
				bc.BackupVaultId = gcpgenserver.NewOptNilString(*config.BackupVaultPath)
			}
			if config.BackupPolicyPath != nil {
				bc.BackupPolicyId = gcpgenserver.NewOptNilString(*config.BackupPolicyPath)
			}
			if config.ScheduledBackupEnabled != nil {
				bc.ScheduledBackupEnabled = gcpgenserver.NewOptNilBool(*config.ScheduledBackupEnabled)
			}
			if config.BackupChainBytes != nil {
				bc.BackupChainBytes = gcpgenserver.NewOptNilInt64(*config.BackupChainBytes)
			}
			apiConfig.BackupConfig = gcpgenserver.NewOptBackupConfigV1beta(bc)
		}

		responseConfigs = append(responseConfigs, apiConfig)
	}

	return &gcpgenserver.V1betaGetBackupConfigsForPoolOK{
		BackupConfigs: responseConfigs,
	}, nil
}

// V1betaUpdatePool handles the request to update a pool.
func (h Handler) V1betaUpdatePool(ctx context.Context, req *gcpgenserver.PoolUpdateV1beta, params gcpgenserver.V1betaUpdatePoolParams) (gcpgenserver.V1betaUpdatePoolRes, error) {
	logger := util.GetLogger(ctx)

	region, zone, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	existingPool, err := h.Orchestrator.DescribePool(ctx, params.PoolId, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("Pool not found", "uuid", params.PoolId)
			return &gcpgenserver.V1betaUpdatePoolNotFound{
				Code:    404,
				Message: "Pool not found",
			}, nil
		}
		logger.Error("Failed to describe pool", "error", err.Error())
		return &gcpgenserver.V1betaUpdatePoolInternalServerError{}, err
	}

	// Validate request shape and pool state first so malformed requests get 4xx (400/409) without calling the upgrade backend
	validateErr := validateUpdatePoolParams(req, existingPool)
	if validateErr != nil {
		return validateErr, nil
	}

	// Block pool update when cluster upgrade is in progress (same as degraded mode); runs after validation so request-shape errors stay 400
	hasUpgrade, err := h.Orchestrator.HasActiveClusterUpgrade(ctx, existingPool.UUID)
	if err != nil {
		logger.Error("Failed to check cluster upgrade status", "error", err.Error())
		return &gcpgenserver.V1betaUpdatePoolInternalServerError{}, err
	}
	if hasUpgrade {
		return &gcpgenserver.V1betaUpdatePoolConflict{
			Code:    http.StatusConflict,
			Message: "Storage pool is temporarily unavailable, please try again later",
		}, nil
	}

	param := &commonparams.UpdatePoolParams{
		AccountName: params.ProjectNumber,
		Region:      region,
		CurrentZone: zone,
		PoolId:      params.PoolId,
	}

	isPOPoolUpdate := commonparams.IsPOModelPool(existingPool)

	if req.LargeCapacity.IsSet() {
		param.LargeCapacity = nillable.GetBoolPtr(req.LargeCapacity.Or(false))
	}

	if !isPOPoolUpdate {
		// IOPS Handling: When only throughput is changed (IOPS not provided in request):
		// - If current IOPS > (new throughput * 16): Keep current IOPS unchanged
		// - If current IOPS < (new throughput * 16): Increase IOPS to minimum (throughput * 16)
		//
		// This ensures that customers only changing throughput don't lose their higher IOPS
		// while maintaining the minimum IOPS requirement for the new throughput level.
		// Always validate and calculate IOPS - handles all cases including validation
		calculatedIops := calculateIopsForUpdate(ctx, req.TotalThroughputMibps, req.TotalIops, existingPool)

		// Validate if user is updating throughput/qos (either TotalThroughputMibps or TotalIops is set)
		// This blocks QoS reductions below what's utilized by child volumes
		if existingPool.QosType == utils.QosTypeManual && (req.TotalThroughputMibps.IsSet() || req.TotalIops.IsSet()) {
			// Use requested throughput if set, otherwise use existing pool throughput
			var throughputToValidate float64
			if req.TotalThroughputMibps.IsSet() {
				throughputToValidate = req.TotalThroughputMibps.Value
			} else {
				if existingPool.CustomPerformanceParams != nil {
					throughputToValidate = float64(existingPool.CustomPerformanceParams.Throughput)
				} else {
					throughputToValidate = float64(existingPool.TotalThroughputMibps)
				}
			}

			validateErr2 := validateUpdateThroughputAndIopsAboveUtilized(
				ctx,
				throughputToValidate,
				float64(calculatedIops),
				existingPool)
			if validateErr2 != nil {
				return &gcpgenserver.V1betaUpdatePoolBadRequest{
					Code:    http.StatusBadRequest,
					Message: validateErr2.Error(),
				}, nil
			}
		}
		param.TotalIops = &calculatedIops
	}

	if req.Description.IsSet() {
		param.Description = req.Description.Value
	} else {
		param.Description = existingPool.Description
	}

	if req.QosType.IsSet() {
		param.QosType = req.QosType.Value
	} else {
		param.QosType = existingPool.QosType
	}

	if req.SizeInBytes.IsSet() {
		param.SizeInBytes = uint64(req.SizeInBytes.Value)
	} else {
		param.SizeInBytes = existingPool.SizeInBytes
	}

	if req.TotalThroughputMibps.IsSet() {
		param.TotalThroughputMibps = int64(req.TotalThroughputMibps.Value)
	} else if existingPool.CustomPerformanceParams != nil {
		param.TotalThroughputMibps = int64(existingPool.CustomPerformanceParams.Throughput)
	} else {
		param.TotalThroughputMibps = int64(existingPool.TotalThroughputMibps)
	}

	if req.Zone.IsSet() {
		param.CurrentZone = req.Zone.Value
	} else if existingPool.PoolAttributes != nil && existingPool.PoolAttributes.PrimaryZone != "" {
		param.CurrentZone = existingPool.PoolAttributes.PrimaryZone
	}

	if req.Labels.IsSet() {
		jsonbLabels, err := validateLabels(req.Labels.Value)
		if err != nil {
			return &gcpgenserver.V1betaUpdatePoolBadRequest{
				Code:    HTTP_BAD_REQUEST_CODE,
				Message: err.Error(),
			}, nil
		}
		param.Labels = jsonbLabels
	}

	// AutoTiering parameter handling
	if req.AllowAutoTiering.IsSet() {
		param.AllowAutoTiering = req.AllowAutoTiering.Value
	} else {
		param.AllowAutoTiering = existingPool.AllowAutoTiering
	}

	if req.HotTierSizeInBytes.IsSet() {
		param.HotTierSizeInBytes = uint64(req.HotTierSizeInBytes.Value)
	} else if existingPool.AutoTieringConfig != nil {
		param.HotTierSizeInBytes = uint64(existingPool.AutoTieringConfig.HotTierSizeInBytes)
	}

	if req.EnableHotTierAutoResize.IsSet() {
		param.EnableHotTierAutoResize = req.EnableHotTierAutoResize.Value
	} else if existingPool.AutoTieringConfig != nil {
		param.EnableHotTierAutoResize = existingPool.AutoTieringConfig.EnableHotTierAutoResize
	}

	if req.ActiveDirectoryConfigId.IsSet() {
		adConfig, ifADExistsInVCP, adErr := getAndSyncAdConfigForPool(ctx, req.ActiveDirectoryConfigId, params.ProjectNumber, params.LocationId, params.XCorrelationID.Value, h.Orchestrator)
		if adErr != nil {
			return adErr.toUpdateResponse(), nil
		}
		param.ActiveDirectoryConfigId = req.ActiveDirectoryConfigId.Value
		param.ActiveDirectoryId = req.ActiveDirectoryConfigId.Value
		param.ActiveDirectory = adConfig
		param.IfADExistsInVCP = ifADExistsInVCP
		if params.XCorrelationID.IsSet() {
			param.XCorrelationID = params.XCorrelationID.Value
		}
	}

	if isPOPoolUpdate {
		param.QosType = existingPool.QosType
		param.TotalThroughputMibps = int64(existingPool.TotalThroughputMibps)
		if existingPool.CustomPerformanceParams != nil {
			param.TotalThroughputMibps = int64(existingPool.CustomPerformanceParams.Throughput)
		}
		existingIops := existingPool.TotalIops
		if existingPool.CustomPerformanceParams != nil {
			existingIops = existingPool.CustomPerformanceParams.Iops
		}
		param.TotalIops = &existingIops
	}

	updatedPool, operationID, err := h.Orchestrator.UpdatePool(ctx, param)
	if err != nil {
		logger.Error("Failed to update pool", "error", err.Error())
		if errors.IsUserInputValidationErr(err) || errors.IsBadRequestErr(err) {
			return &gcpgenserver.V1betaUpdatePoolBadRequest{
				Code:    HTTP_BAD_REQUEST_CODE,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaUpdatePoolConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		return &gcpgenserver.V1betaUpdatePoolInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	resp, err := encodePoolV1(convertToPoolV1Beta(updatedPool))
	if err != nil {
		return nil, err
	}
	if operationID != "" {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{}, nil
}

func convertToPoolsV1Beta(pools []*models.Pool) []gcpgenserver.PoolV1beta {
	poolsV1Beta := make([]gcpgenserver.PoolV1beta, len(pools))
	for i, pool := range pools {
		poolsV1Beta[i] = *convertToPoolV1BetaWithConsumption(pool)
	}
	return poolsV1Beta
}

// getLdapEnabled safely extracts LdapEnabled from PoolAttributes
func getLdapEnabled(pool *models.Pool) bool {
	if pool.PoolAttributes != nil {
		return pool.PoolAttributes.LdapEnabled
	}
	return false
}

func convertToPoolV1Beta(pool *models.Pool) *gcpgenserver.PoolV1beta {
	var deletedAt gcpgenserver.OptNilDateTime
	if pool.DeletedAt != nil {
		deletedAt = gcpgenserver.NewOptNilDateTime(*pool.DeletedAt)
	}

	var throughputValue float64
	var iops int64
	customPerformanceEnabled := false
	if (pool.CustomPerformanceParams != nil) && (pool.CustomPerformanceParams.Enabled) {
		customPerformanceEnabled = pool.CustomPerformanceParams.Enabled
		throughputValue = pool.CustomPerformanceParams.Throughput
		iops = pool.CustomPerformanceParams.Iops
	} else {
		throughputValue = pool.TotalThroughputMibps
		iops = pool.TotalIops
	}

	labels := gcpgenserver.PoolV1betaLabels{}
	if pool.PoolAttributes.Labels != nil {
		for key, value := range pool.PoolAttributes.Labels {
			labels[key] = value
		}
	}
	secondaryZone := ""
	if pool.PoolAttributes.IsRegionalHA {
		secondaryZone = pool.PoolAttributes.SecondaryZone
	}

	// PO (Performance Offering) pools are provisioned on HARDWARE capacity.
	// Derive storage class from service level; it is never persisted.
	storageClass := commonparams.DerivePOStorageClass(pool.ServiceLevel)

	poolV1beta := &gcpgenserver.PoolV1beta{
		PoolId:                   gcpgenserver.NewOptString(pool.UUID),
		CreatedAt:                gcpgenserver.NewOptDateTime(pool.CreatedAt),
		UpdatedAt:                gcpgenserver.NewOptDateTime(pool.UpdatedAt),
		DeletedAt:                deletedAt,
		ResourceId:               pool.Name,
		Description:              gcpgenserver.NewOptNilString(pool.Description),
		Network:                  pool.VendorSubNetID,
		SizeInBytes:              float64(pool.SizeInBytes),
		TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(throughputValue),
		AvailableThroughputMibps: gcpgenserver.NewOptNilFloat64(throughputValue - pool.UtilizedThroughputMibps),
		TotalIops:                gcpgenserver.NewOptNilFloat64(float64(iops)),
		AvailableIops:            gcpgenserver.NewOptNilFloat64(float64(iops) - float64(pool.UtilizedIops)),
		StoragePoolState:         gcpgenserver.NewOptPoolV1betaStoragePoolState(gcpgenserver.PoolV1betaStoragePoolState(pool.State)),
		StoragePoolStateDetails:  gcpgenserver.NewOptString(pool.StateDetails),
		ServiceLevel:             gcpgenserver.PoolV1betaServiceLevel(pool.ServiceLevel),
		QosType:                  gcpgenserver.NewOptNilString(pool.QosType),
		StorageClass:             gcpgenserver.NewOptStorageClassV1beta(gcpgenserver.StorageClassV1beta(storageClass)),
		AllowAutoTiering:         gcpgenserver.NewOptNilBool(pool.AllowAutoTiering),
		AllocatedBytes:           gcpgenserver.NewOptNilFloat64(pool.PoolAttributes.AllocatedBytes),
		NumberOfVolumes:          gcpgenserver.NewOptNilInt32(int32(pool.PoolAttributes.NumberOfVolumes)),
		Zone:                     gcpgenserver.NewOptString(pool.PoolAttributes.PrimaryZone),
		SecondaryZone:            gcpgenserver.NewOptString(secondaryZone),
		Labels:                   gcpgenserver.NewOptPoolV1betaLabels(labels),
		LargeCapacity:            gcpgenserver.NewOptBool(pool.LargeCapacity),
		SatisfiesPzs:             gcpgenserver.NewOptNilBool(pool.SatisfiesPzs),
		SatisfiesPzi:             gcpgenserver.NewOptNilBool(pool.SatisfiesPzi),
		Mode:                     gcpgenserver.NewOptPoolV1betaMode(gcpgenserver.PoolV1betaMode(pool.APIAccessMode)),
		LdapEnabled:              gcpgenserver.NewOptNilBool(getLdapEnabled(pool)),
	}

	if !commonparams.IsPOModelPool(pool) {
		poolV1beta.CustomPerformanceEnabled = gcpgenserver.NewOptBool(customPerformanceEnabled)
		poolV1beta.Type = gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeUNIFIED)
		poolV1beta.UnifiedPool = gcpgenserver.NewOptBool(true)
		poolV1beta.Unified = gcpgenserver.NewOptBool(true)
	}

	if commonparams.IsPOModelPool(pool) {
		if pool.ClusterDetails != nil && pool.ClusterDetails.ExternalName != "" {
			poolV1beta.ClusterName = gcpgenserver.NewOptString(pool.ClusterDetails.ExternalName)
		}
		if pool.HostUUID != "" {
			poolV1beta.HostUuid = gcpgenserver.NewOptString(pool.HostUUID)
		}
		if throughputValue == 0 {
			poolV1beta.TotalThroughputMibps = gcpgenserver.OptNilFloat64{}
			poolV1beta.AvailableThroughputMibps = gcpgenserver.OptNilFloat64{}
		}
		if iops == 0 {
			poolV1beta.TotalIops = gcpgenserver.OptNilFloat64{}
			poolV1beta.AvailableIops = gcpgenserver.OptNilFloat64{}
		}
		if pool.PoolAttributes.PrimaryZone == "" {
			poolV1beta.Zone = gcpgenserver.OptString{}
		}
		if secondaryZone == "" {
			poolV1beta.SecondaryZone = gcpgenserver.OptString{}
		}
		if pool.QosType == "" {
			poolV1beta.QosType = gcpgenserver.OptNilString{}
		}
	}

	if pool.ActiveDirectoryConfigId != "" {
		region, _, err := utils.ParseRegionAndZone(pool.PoolAttributes.PrimaryZone)
		if err == nil {
			poolV1beta.ActiveDirectoryConfigId = gcpgenserver.NewOptNilString(pool.ActiveDirectoryConfigId)
			poolV1beta.ActiveDirectoryResourceId = gcpgenserver.NewOptString(fmt.Sprintf(
				"projects/%s/locations/%s/activeDirectories/%s", pool.AccountName, region, pool.ActiveDirectoryResourceId))
		}
	}

	kmsConfigId := ""
	if pool.KmsConfig != nil {
		poolV1beta.KmsConfigId = gcpgenserver.NewOptNilString(pool.KmsConfig.UUID)
		poolV1beta.KmsConfigResourceId = gcpgenserver.NewOptString(utils.ParsedKeyFullPathResource{ProjectID: pool.KmsConfig.KeyProjectID,
			KeyRing: pool.KmsConfig.KeyRing, Location: pool.KmsConfig.KeyRingLocation, CryptoKey: pool.KmsConfig.KeyName}.String())
		kmsConfigId = pool.KmsConfig.UUID
	}
	poolV1beta.EncryptionType = gcpgenserver.NewOptPoolV1betaEncryptionType(gcpgenserver.PoolV1betaEncryptionType(utils.GetEncryptionType(&kmsConfigId)))
	var assetLocationMetadata gcpgenserver.PoolV1betaAssetLocationMetadata
	if pool.AssetMetadata != nil {
		var assets []gcpgenserver.ChildAsset
		inChildAssets := pool.AssetMetadata.ChildAssets
		for _, asset := range inChildAssets {
			var childAsset gcpgenserver.ChildAsset
			childAsset.AssetType = gcpgenserver.NewOptString(asset.AssetType)
			childAsset.AssetNames = asset.AssetNames
			assets = append(assets, childAsset)
		}
		assetLocationMetadata = gcpgenserver.PoolV1betaAssetLocationMetadata{
			ChildAssets: gcpgenserver.OptNilChildAssetArray{Value: assets, Set: true},
		}
		poolV1beta.AssetLocationMetadata = gcpgenserver.NewOptNilPoolV1betaAssetLocationMetadata(assetLocationMetadata)
	}

	// Only include auto tiering fields if auto tiering is enabled
	if pool.AllowAutoTiering {
		poolV1beta.HotTierSizeInBytes = gcpgenserver.NewOptNilFloat64(getHotTierSizeInBytes(pool.AutoTieringConfig))
		poolV1beta.EnableHotTierAutoResize = gcpgenserver.NewOptNilBool(getEnableHotTierAutoResize(pool.AutoTieringConfig))
		poolV1beta.HotTierConsumption = getHotTierConsumptionOpt(pool.AutoTieringConfig)
		poolV1beta.ColdTierConsumption = getColdTierConsumptionOpt(pool.AutoTieringConfig)
	}

	if pool.PoolAttributes != nil {
		if config, ok := largeCapacityConfigFromLvHAPairs(pool.PoolAttributes.LvHAPairs); ok {
			poolV1beta.LargeCapacityConfig = gcpgenserver.NewOptNilPoolV1betaLargeCapacityConfig(config)
		}
	}

	return poolV1beta
}

// encodePoolV1 encodes a PoolV1 struct to JSON.
func encodePoolV1(pool *gcpgenserver.PoolV1beta) (jx.Raw, error) {
	data, err := json.Marshal(pool)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// encodeBackupConfigV1 encodes a BackupConfigV1beta struct to JSON.
func encodeBackupConfigV1(bc *gcpgenserver.BackupConfigV1beta) (jx.Raw, error) {
	data, err := json.Marshal(bc)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// convertDataProtectionToBackupConfigV1beta converts a DataProtection datamodel to a BackupConfigV1beta GCP schema type.
func convertDataProtectionToBackupConfigV1beta(dp *datamodel.DataProtection) *gcpgenserver.BackupConfigV1beta {
	bc := &gcpgenserver.BackupConfigV1beta{}
	if dp.BackupVaultID != "" {
		bc.BackupVaultId = gcpgenserver.NewOptNilString(dp.BackupVaultID)
	}
	if dp.BackupPolicyID != "" {
		bc.BackupPolicyId = gcpgenserver.NewOptNilString(dp.BackupPolicyID)
	}
	if dp.ScheduledBackupEnabled != nil {
		bc.ScheduledBackupEnabled = gcpgenserver.NewOptNilBool(*dp.ScheduledBackupEnabled)
	}
	return bc
}

func convertToPoolsV1beta(pools []*cvpmodels.PoolV1beta) []gcpgenserver.PoolV1beta {
	poolsV1Beta := make([]gcpgenserver.PoolV1beta, len(pools))
	for i, pool := range pools {
		poolsV1Beta[i] = *convertToPoolV1beta(pool)
	}
	return poolsV1Beta
}

func convertToPoolV1beta(pool *cvpmodels.PoolV1beta) *gcpgenserver.PoolV1beta {
	var assetLocationMetadata gcpgenserver.PoolV1betaAssetLocationMetadata
	if pool.AssetLocationMetadata != nil {
		var assets []gcpgenserver.ChildAsset
		inChildAssets := pool.AssetLocationMetadata.ChildAssets
		for _, asset := range inChildAssets {
			var cvpAsset gcpgenserver.ChildAsset
			cvpAsset.AssetType = gcpgenserver.NewOptString(asset.AssetType)
			cvpAsset.AssetNames = asset.AssetNames
			assets = append(assets, cvpAsset)
		}
		assetLocationMetadata = gcpgenserver.PoolV1betaAssetLocationMetadata{
			ChildAssets: gcpgenserver.OptNilChildAssetArray{Value: assets},
		}
	}
	return &gcpgenserver.PoolV1beta{
		PoolId:                    gcpgenserver.NewOptString(pool.PoolID),
		CreatedAt:                 gcpgenserver.NewOptDateTime(time.Time(pool.CreatedAt)),
		UpdatedAt:                 gcpgenserver.NewOptDateTime(time.Time(pool.UpdatedAt)),
		DeletedAt:                 utils.SafeTime(pool.DeletedAt),
		ResourceId:                *pool.ResourceID,
		Network:                   *pool.Network,
		AllocatedBytes:            utils.SafeFloat64(pool.AllocatedBytes),
		SizeInBytes:               *pool.SizeInBytes,
		TotalThroughputMibps:      utils.SafeFloat64(pool.TotalThroughputMibps),
		AvailableThroughputMibps:  utils.SafeFloat64(pool.AvailableThroughputMibps),
		ServiceLevel:              gcpgenserver.PoolV1betaServiceLevel(*pool.ServiceLevel),
		TotalIops:                 utils.SafeFloat64(pool.TotalIops),
		CustomPerformanceEnabled:  gcpgenserver.NewOptBool(pool.CustomPerformanceEnabled),
		Zone:                      gcpgenserver.NewOptString(pool.Zone),
		StorageClass:              gcpgenserver.NewOptStorageClassV1beta(gcpgenserver.StorageClassV1beta(pool.StorageClass)),
		StoragePoolState:          gcpgenserver.NewOptPoolV1betaStoragePoolState(gcpgenserver.PoolV1betaStoragePoolState(pool.StoragePoolState)),
		NumberOfVolumes:           utils.SafeInt64ToInt32(pool.NumberOfVolumes),
		StoragePoolStateDetails:   gcpgenserver.NewOptString(pool.StateDetails),
		Description:               utils.SafeString(pool.Description),
		AllowAutoTiering:          utils.SafeBool(pool.AllowAutoTiering),
		HotTierSizeInBytes:        utils.SafeFloat64(pool.HotTierSizeInBytes),
		EnableHotTierAutoResize:   utils.SafeBool(pool.EnableHotTierAutoResize),
		KmsConfigId:               utils.SafeString(pool.KmsConfigID),
		KmsConfigResourceId:       gcpgenserver.NewOptString(pool.KmsConfigResourceID),
		ActiveDirectoryConfigId:   utils.SafeString(pool.ActiveDirectoryConfigID),
		ActiveDirectoryResourceId: gcpgenserver.NewOptString(pool.ActiveDirectoryResourceID),
		LdapEnabled:               utils.SafeBool(pool.LdapEnabled),
		EncryptionType:            gcpgenserver.NewOptPoolV1betaEncryptionType(gcpgenserver.PoolV1betaEncryptionType(pool.EncryptionType)),
		GlobalAccessAllowed:       utils.SafeBool(pool.GlobalAccessAllowed),
		Labels:                    gcpgenserver.NewOptPoolV1betaLabels(pool.Labels),
		SecondaryZone:             gcpgenserver.NewOptString(pool.SecondaryZone),
		QosType:                   utils.SafeString(pool.QosType),
		SatisfiesPzi:              utils.SafeBool(pool.SatisfiesPzi),
		SatisfiesPzs:              utils.SafeBool(pool.SatisfiesPzs),
		AssetLocationMetadata:     gcpgenserver.NewOptNilPoolV1betaAssetLocationMetadata(assetLocationMetadata),
		// Unified Pool is set false for SDE pools
		Type:                gcpgenserver.NewOptPoolV1betaType(gcpgenserver.PoolV1betaTypeFILE),
		UnifiedPool:         gcpgenserver.NewOptBool(false),
		Unified:             gcpgenserver.NewOptBool(false),
		HotTierConsumption:  utils.SafeInt64(pool.HotTierConsumption),
		ColdTierConsumption: utils.SafeInt64(pool.ColdTierConsumption),
	}
}

// Helper functions for AutoTieringConfig field access
func getHotTierSizeInBytes(config *models.AutoTieringConfig) float64 {
	if config == nil {
		return 0
	}
	return float64(config.HotTierSizeInBytes)
}

func getEnableHotTierAutoResize(config *models.AutoTieringConfig) bool {
	if config == nil {
		return false
	}
	return config.EnableHotTierAutoResize
}

func validateTrueZonalAutoTiering(allowAutoTieringSet, allowAutoTiering, hotTierAutoResizeSet, hotTierAutoResize bool) string {
	if blockTrueZonalAutoTieringAndBackup && allowAutoTieringSet && allowAutoTiering {
		return trueZonalEnableAutoTieringErrorMsg
	}
	if hotTierAutoResizeSet && hotTierAutoResize {
		return "Hot tier auto-resize cannot be enabled for true-zonal pools"
	}
	return ""
}

func largeCapacityConfigFromLvHAPairs(lvHAPairs int64) (gcpgenserver.PoolV1betaLargeCapacityConfig, bool) {
	switch lvHAPairs {
	case commonparams.LvHAPairsNodeCount12:
		return gcpgenserver.PoolV1betaLargeCapacityConfigNODECOUNT12, true
	case commonparams.LvHAPairsNodeCount24:
		return gcpgenserver.PoolV1betaLargeCapacityConfigNODECOUNT24, true
	default:
		return "", false
	}
}

func resolveLvHAPairs(largeCapacityConfig gcpgenserver.PoolV1betaLargeCapacityConfig) int64 {
	if !enableTrueZonalGA {
		return 0
	}
	if largeCapacityConfig == gcpgenserver.PoolV1betaLargeCapacityConfigNODECOUNT24 {
		return commonparams.LvHAPairsNodeCount24
	}
	return commonparams.LvHAPairsNodeCount12
}

func validateTrueZonalLVPoolSize(sizeInBytes uint64, lvHAPairs int64) string {
	minSize := trueZonalLVPoolMinSize
	if lvHAPairs == commonparams.LvHAPairsNodeCount24 {
		minSize = trueZonalLV24NodePoolMinSize
	} else if enableTrueZonalGA {
		minSize = trueZonalLVPoolMinSize / 2
	}
	if sizeInBytes < minSize {
		return fmt.Sprintf("Pool size must be at least %d bytes for true-zonal large-capacity pools", minSize)
	}
	return ""
}

func convertToPoolV1BetaWithConsumption(pool *models.Pool) *gcpgenserver.PoolV1beta {
	result := convertToPoolV1Beta(pool)
	// Add consumption fields only if auto tiering is enabled
	if pool.AllowAutoTiering {
		result.HotTierConsumption = getHotTierConsumptionOpt(pool.AutoTieringConfig)
		result.ColdTierConsumption = getColdTierConsumptionOpt(pool.AutoTieringConfig)
	}
	return result
}

func getHotTierConsumptionOpt(config *models.AutoTieringConfig) gcpgenserver.OptNilInt64 {
	if config == nil {
		return gcpgenserver.OptNilInt64{}
	}
	return gcpgenserver.NewOptNilInt64(config.HotTierConsumption)
}

func getColdTierConsumptionOpt(config *models.AutoTieringConfig) gcpgenserver.OptNilInt64 {
	if config == nil {
		return gcpgenserver.OptNilInt64{}
	}
	return gcpgenserver.NewOptNilInt64(config.ColdTierConsumption)
}

// validateCreatePoolParams validates the parameters for creating a pool.
// Non-PO pools must be Unified Flex Storage Pools; PO pools are exempt from that requirement.
func validateCreatePoolParams(req *gcpgenserver.PoolV1beta, zone string) *gcpgenserver.Error {
	if req.Type.IsSet() && req.Type.Value == gcpgenserver.PoolV1betaTypeSTORAGEPOOLTYPEUNSPECIFIED {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "type field cannot be STORAGE_POOL_TYPE_UNSPECIFIED",
		}
	}

	if !isPOCreateRequest(req) {
		// Check the new Type field first, then fall back to unified/unifiedPool fields for backward compatibility
		isUnified := false

		if req.Type.IsSet() {
			switch req.Type.Value {
			case gcpgenserver.PoolV1betaTypeUNIFIED:
				isUnified = true
			case gcpgenserver.PoolV1betaTypeFILE:
				isUnified = false
			}
		} else if req.Unified.IsSet() {
			isUnified = req.Unified.Value
		} else if req.UnifiedPool.IsSet() {
			isUnified = req.UnifiedPool.Value
		}

		if !isUnified {
			return &gcpgenserver.Error{
				Code:    http.StatusBadRequest,
				Message: "type must be set to UNIFIED, or unified/unifiedPool must be set to true (for backward compatibility)",
			}
		}
	}

	if req.LargeCapacity.IsSet() && req.LargeCapacity.Value && !enableLargeCapacityPools {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "Large capacity pools feature is not enabled",
		}
	}

	if _, ok := req.LargeCapacityConfig.Get(); ok && nillable.IsNilOrEmpty(&zone) {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "Large capacity config is not supported for regional pools",
		}
	}

	if config, ok := req.LargeCapacityConfig.Get(); ok &&
		config != gcpgenserver.PoolV1betaLargeCapacityConfigNODECOUNT12 &&
		config != gcpgenserver.PoolV1betaLargeCapacityConfigNODECOUNT24 && enableTrueZonalGA {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "Large capacity config must be set to NODE_COUNT_12 or NODE_COUNT_24",
		}
	}
	if _, ok := req.LargeCapacityConfig.Get(); ok && req.LargeCapacity.IsSet() && !req.LargeCapacity.Value && enableTrueZonalGA {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "Large capacity should be set to true when large capacity config is set",
		}
	}

	if isPOCreateRequest(req) &&
		string(req.ServiceLevel) == commonparams.ServiceLevelPremium &&
		!enablePOPremiumPools {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: commonparams.POValidationPremiumDisabledMsg,
		}
	}

	if isPOCreateRequest(req) {
		if !nillable.IsNilOrEmpty(&zone) {
			return &gcpgenserver.Error{
				Code:    http.StatusBadRequest,
				Message: commonparams.POValidationRegionalOnlyMsg,
			}
		}
		if req.ActiveDirectoryConfigId.Value != "" || req.ActiveDirectoryResourceId.Value != "" {
			return &gcpgenserver.Error{
				Code:    http.StatusBadRequest,
				Message: commonparams.POValidationActiveDirectoryMsg,
			}
		}
		if req.LdapEnabled.IsSet() && req.LdapEnabled.Value {
			return &gcpgenserver.Error{
				Code:    http.StatusBadRequest,
				Message: commonparams.POValidationLdapEnabledMsg,
			}
		}
		if req.KmsConfigId.Value != "" && !enablePOCmek {
			return &gcpgenserver.Error{
				Code:    http.StatusBadRequest,
				Message: commonparams.POValidationCmekMsg,
			}
		}
	}

	if req.Mode.Value == gcpgenserver.PoolV1betaModeONTAP && req.ActiveDirectoryResourceId.Value != "" {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "Active directory cannot be assigned to ONTAP Mode Pool",
		}
	}

	if req.QosType.IsSet() && req.QosType.Value == utils.QosTypeManual && !enableMqos {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "Manual QosType is not supported",
		}
	}

	if req.LdapEnabled.IsSet() && req.LdapEnabled.Value && req.ActiveDirectoryConfigId.Value == "" {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "Active Directory configuration is required when LDAP is enabled",
		}
	}

	if req.LdapEnabled.IsSet() && req.LdapEnabled.Value && !enableLdap {
		return &gcpgenserver.Error{
			Code:    http.StatusBadRequest,
			Message: "LDAP is not currently supported for Unified Flex Storage Pool",
		}
	}

	if !isPOCreateRequest(req) {
		if nillable.IsNilOrEmpty(&zone) {
			if !regionalPoolEnabled {
				return &gcpgenserver.Error{
					Code:    http.StatusBadRequest,
					Message: "Regional Pool Support is not enabled",
				}
			}

			if !req.Zone.IsSet() || req.Zone.Value == "" {
				return &gcpgenserver.Error{
					Code:    http.StatusBadRequest,
					Message: "Zone cannot be empty for regional pool.",
				}
			}

			if !req.SecondaryZone.IsSet() || req.SecondaryZone.Value == "" {
				return &gcpgenserver.Error{
					Code:    http.StatusBadRequest,
					Message: "Secondary Zone cannot be empty for regional pool.",
				}
			}
			if req.SecondaryZone.Value == req.Zone.Value {
				return &gcpgenserver.Error{
					Code:    http.StatusBadRequest,
					Message: "Secondary Zone cannot be same as Primary Zone",
				}
			}
		} else {
			if req.Zone.IsSet() && req.Zone.Value != "" && req.Zone.Value != zone {
				return &gcpgenserver.Error{
					Code:    http.StatusBadRequest,
					Message: "Multiple Zone values cannot be passed for Zonal Pool Creation",
				}
			}
			if req.SecondaryZone.IsSet() && req.SecondaryZone.Value != "" && req.SecondaryZone.Value == zone {
				return &gcpgenserver.Error{
					Code:    http.StatusBadRequest,
					Message: "Secondary Zone cannot be same as Primary Zone",
				}
			}
		}
	}

	// Validate auto-tiering parameters
	if !autoTieringEnabled && ((req.AllowAutoTiering.IsSet() && req.AllowAutoTiering.Value) || (req.HotTierSizeInBytes.IsSet() && req.HotTierSizeInBytes.Value > 0)) {
		return &gcpgenserver.Error{
			Code:    HTTP_BAD_REQUEST_CODE,
			Message: "Auto-Tiering feature is currently not enabled.",
		}
	}

	if req.AllowAutoTiering.IsSet() && req.AllowAutoTiering.Value {
		// 1. HotTierSizeInBytes is required when auto-tiering is enabled (existence check only)
		if !req.HotTierSizeInBytes.IsSet() || req.HotTierSizeInBytes.Value == 0 {
			return &gcpgenserver.Error{
				Code:    HTTP_BAD_REQUEST_CODE,
				Message: "HotTierSizeInBytes is a required field to enable auto-tiering",
			}
		}
		// Note: All numerical validations (size comparisons, min/max checks) moved to orchestrator
	}

	// Auto-tiering params cannot be set without enabling AllowAutoTiering
	allowAutoTieringValue := false
	if req.AllowAutoTiering.IsSet() {
		allowAutoTieringValue = req.AllowAutoTiering.Value
	}
	if !allowAutoTieringValue && ((req.HotTierSizeInBytes.IsSet() && req.HotTierSizeInBytes.Value > 0) || (req.EnableHotTierAutoResize.IsSet() && req.EnableHotTierAutoResize.Value)) {
		return &gcpgenserver.Error{
			Code:    HTTP_BAD_REQUEST_CODE,
			Message: "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering",
		}
	}

	return nil
}

// isTrueZonal determines whether a new pool should use true-zonal deployment. GA enables
// true-zonal for every zonal pool without a secondary zone.
func isTrueZonal(isRegionalHA, largeCapacity bool, secondaryZone string) bool {
	zonal := !isRegionalHA && secondaryZone == ""
	return zonal && (enableTrueZonalGA || (largeCapacity && enableTrueZonalLV))
}

// validateUpdatePoolParams validates the parameters for updating a pool.
// We currently only allow updating the description, size, total throughput, and total IOPS.
func validateUpdatePoolParams(req *gcpgenserver.PoolUpdateV1beta, existingPool *models.Pool) gcpgenserver.V1betaUpdatePoolRes {
	if existingPool.State == datamodel.LifeCycleStateUpdating {
		return &gcpgenserver.V1betaUpdatePoolConflict{
			Code:    http.StatusConflict,
			Message: "An update operation is already in progress for this pool",
		}
	}

	if existingPool.State == datamodel.LifeCycleStateDegraded {
		return &gcpgenserver.V1betaUpdatePoolConflict{
			Code:    http.StatusConflict,
			Message: "Update operation is not allowed when the pool is in degraded state",
		}
	}

	if commonparams.IsPOModelPool(existingPool) {
		return validatePOUpdatePoolParams(req, existingPool)
	}

	pa := existingPool.PoolAttributes

	// Zone switching is a mutually exclusive update operation: when zone is provided, no other fields may be set.
	// This avoids ambiguous behavior (e.g., changing QoS/size and switching zones in the same request).
	if req.Zone.IsSet() && pa != nil && pa.IsRegionalHA &&
		(req.Description.IsSet() ||
			req.QosType.IsSet() ||
			req.SizeInBytes.IsSet() ||
			req.TotalThroughputMibps.IsSet() ||
			req.TotalIops.IsSet() ||
			req.Labels.IsSet() ||
			req.AllowAutoTiering.IsSet() ||
			req.HotTierSizeInBytes.IsSet() ||
			req.EnableHotTierAutoResize.IsSet() ||
			req.ActiveDirectoryConfigId.IsSet() ||
			req.GlobalAccessAllowed.IsSet() ||
			req.CustomPerformanceEnabled.IsSet() ||
			req.LargeCapacity.IsSet()) {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "When 'zone' is provided, no other update fields are allowed",
		}
	}

	if req.Zone.IsSet() && pa != nil && !pa.IsRegionalHA {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Zone cannot be specified for zonal pool update",
		}
	}

	if req.Zone.IsSet() && pa != nil && pa.IsRegionalHA && req.Zone.Value != pa.PrimaryZone && req.Zone.Value != pa.SecondaryZone {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Target Zone is invalid",
		}
	}

	if !req.Zone.IsSet() && pa != nil {
		switch pa.ZoneSwitchState {
		case datamodel.ZoneSwitching:
			return &gcpgenserver.V1betaUpdatePoolConflict{
				Code:    http.StatusConflict,
				Message: "Update operation is not allowed when the pool is switching to a different primary zone.",
			}
		case datamodel.ZoneSwitched:
			if req.SizeInBytes.IsSet() || req.TotalThroughputMibps.IsSet() || req.TotalIops.IsSet() {
				return &gcpgenserver.V1betaUpdatePoolConflict{
					Code:    http.StatusConflict,
					Message: "Pool size, throughput and iops update is not supported while the storage pool is operating in a switched-over zone. Switch the pool back to its primary zone before retrying.",
				}
			}
		}
	}

	if req.GlobalAccessAllowed.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Updating Global access is currently not supported",
		}
	}

	// Feature flag validation
	if !autoTieringEnabled && (req.AllowAutoTiering.IsSet() ||
		(req.HotTierSizeInBytes.IsSet() && req.HotTierSizeInBytes.Value > 0) ||
		(req.EnableHotTierAutoResize.IsSet())) {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Auto-Tiering feature is currently not enabled",
		}
	}

	// HotTierSizeInBytes is required when enabling auto-tiering
	if req.AllowAutoTiering.IsSet() && req.AllowAutoTiering.Value {
		// Validate enabling Auto-Tiering env variable if blockUpdatePooltoATPool is true
		if !existingPool.AllowAutoTiering && blockUpdatePooltoATPool {
			return &gcpgenserver.V1betaUpdatePoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: "Enabling Auto-Tiering on a non-AT pool is not supported currently",
			}
		}
		if !req.HotTierSizeInBytes.IsSet() || req.HotTierSizeInBytes.Value == 0 {
			return &gcpgenserver.V1betaUpdatePoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: "HotTierSizeInBytes is required when enabling auto-tiering",
			}
		}
	}

	// AutoTiering parameter validation - HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering
	// However, if the pool already has auto-tiering enabled, these parameters can be updated directly
	allowAutoTieringValue := false
	if req.AllowAutoTiering.IsSet() {
		allowAutoTieringValue = req.AllowAutoTiering.Value
	}

	// Check if pool already has auto-tiering enabled
	poolHasAutoTiering := existingPool.AllowAutoTiering

	// Only validate if auto-tiering is not already enabled on the pool
	if !poolHasAutoTiering && !allowAutoTieringValue && ((req.HotTierSizeInBytes.IsSet() && req.HotTierSizeInBytes.Value > 0) || req.EnableHotTierAutoResize.IsSet()) {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "HotTierSizeInBytes and EnableHotTierAutoResize cannot be set without enabling AllowAutoTiering",
		}
	}

	if models.IsTrueZonalPool(existingPool) && !enableTrueZonalGA {
		if req.SizeInBytes.IsSet() && req.SizeInBytes.Value != float64(existingPool.SizeInBytes) {
			return &gcpgenserver.V1betaUpdatePoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: "Pool size cannot be updated for true-zonal pools",
			}
		}
		if msg := validateTrueZonalAutoTiering(req.AllowAutoTiering.IsSet(), req.AllowAutoTiering.Value, req.EnableHotTierAutoResize.IsSet(), req.EnableHotTierAutoResize.Value); msg != "" {
			return &gcpgenserver.V1betaUpdatePoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: msg,
			}
		}
		if req.HotTierSizeInBytes.IsSet() {
			currentHotTier := float64(0)
			if existingPool.AutoTieringConfig != nil {
				currentHotTier = float64(existingPool.AutoTieringConfig.HotTierSizeInBytes)
			}
			if req.HotTierSizeInBytes.Value != currentHotTier {
				return &gcpgenserver.V1betaUpdatePoolBadRequest{
					Code:    http.StatusBadRequest,
					Message: "Hot tier size cannot be updated for true-zonal pools",
				}
			}
		}
	}

	// We do not allow pool size to be reduced (non-PO pools).
	if !commonparams.IsPOModelPool(existingPool) && req.SizeInBytes.IsSet() && req.SizeInBytes.Value < float64(existingPool.SizeInBytes) {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Pool size cannot be reduced",
		}
	}

	if existingPool.APIAccessMode == commonparams.ONTAPMode && req.QosType.IsSet() && req.QosType.Value != existingPool.QosType {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "QosType cannot be modified for an ONTAP mode pool",
		}
	}

	// Allow qosType transition (auto <-> manual); workflow handles the transition.
	// Only validate that value is a valid enum when set.
	if req.QosType.IsSet() && req.QosType.Value != utils.QosTypeAuto && req.QosType.Value != utils.QosTypeManual {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "QosType must be 'auto' or 'manual'",
		}
	}
	// Only allow transitioning to manual when ENABLE_MQOS (enableMqos) is true.
	if req.QosType.IsSet() && req.QosType.Value == utils.QosTypeManual && !enableMqos {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Manual QosType is not supported",
		}
	}

	if req.CustomPerformanceEnabled.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Updating CustomerPerformance is currently not supported",
		}
	}

	return nil
}

func validatePOUpdatePoolParams(req *gcpgenserver.PoolUpdateV1beta, existingPool *models.Pool) gcpgenserver.V1betaUpdatePoolRes {
	if req == nil {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "invalid PO pool update request",
		}
	}
	if req.AllowAutoTiering.IsSet() || req.HotTierSizeInBytes.IsSet() || req.EnableHotTierAutoResize.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: commonparams.POValidationAutoTieringMsg,
		}
	}
	if req.QosType.IsSet() || req.CustomPerformanceEnabled.IsSet() ||
		req.TotalThroughputMibps.IsSet() || req.TotalIops.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: commonparams.POValidationManualQoSMsg,
		}
	}
	if req.Zone.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: commonparams.POValidationZoneUpdateMsg,
		}
	}
	if req.LargeCapacity.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: commonparams.POValidationLargeCapacityUpdateMsg,
		}
	}
	if req.GlobalAccessAllowed.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Updating Global access is currently not supported",
		}
	}
	if req.ActiveDirectoryConfigId.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: commonparams.POValidationActiveDirectoryMsg,
		}
	}
	if !req.Description.IsSet() && !req.Labels.IsSet() && !req.SizeInBytes.IsSet() {
		return &gcpgenserver.V1betaUpdatePoolBadRequest{
			Code:    http.StatusBadRequest,
			Message: "At least one of description, labels, or sizeInBytes must be provided for PO pool update",
		}
	}
	if req.Labels.IsSet() {
		if _, err := validateLabels(req.Labels.Value); err != nil {
			return &gcpgenserver.V1betaUpdatePoolBadRequest{
				Code:    HTTP_BAD_REQUEST_CODE,
				Message: err.Error(),
			}
		}
	}
	return nil
}

// calculateIopsForUpdate calculates IOPS for pool updates
// It ensures IOPS meets minimum requirements for both new and existing throughput
func calculateIopsForUpdate(ctx context.Context, throughput gcpgenserver.OptNilFloat64, iops gcpgenserver.OptNilFloat64, existingPool *models.Pool) int64 {
	// Case 1: IOPS explicitly provided - validate against throughput requirements
	if iops.IsSet() {
		// Return as it is since the calculation is done in the orchestrator
		return int64(iops.Value)
	} else if throughput.IsSet() {
		// Case 2: Only throughput is provided - smart IOPS calculation
		var currentIops int64
		if existingPool.CustomPerformanceParams != nil {
			currentIops = existingPool.CustomPerformanceParams.Iops
		} else {
			currentIops = existingPool.TotalIops
		}
		newThroughput := int64(throughput.Value)
		minimumIopsInt := newThroughput * 16

		logger := util.GetLogger(ctx)
		logger.Info("IOPS calculation",
			"newThroughput", newThroughput,
			"currentIops", currentIops,
			"minimumIops", minimumIopsInt)

		if currentIops > minimumIopsInt {
			// Current IOPS is already above minimum, keep it as is
			logger.Info("Keeping current IOPS (above minimum)", "finalIops", currentIops)
			return currentIops
		} else {
			// Current IOPS is below minimum, increase to minimum
			logger.Info("Increasing IOPS to minimum", "finalIops", minimumIopsInt)
			return minimumIopsInt
		}
	} else {
		// Case 3: Neither throughput nor IOPS provided - use existing IOPS
		if existingPool.CustomPerformanceParams != nil {
			return existingPool.CustomPerformanceParams.Iops
		}
		return existingPool.TotalIops
	}
}

// validateUpdateThroughputAndIopsAboveUtilized validates that the requested throughput and IOPS are at least the existing pool's usage (utilized by volumes in the pool)
func validateUpdateThroughputAndIopsAboveUtilized(ctx context.Context, throughput float64, iops float64, existingPool *models.Pool) error {
	if throughput < existingPool.UtilizedThroughputMibps {
		return errors.NewUserInputValidationErr(fmt.Sprintf(
			"Requested throughput (%.0f MiBps) must be >= current pool utilization (%.0f MiBps).",
			throughput, existingPool.UtilizedThroughputMibps,
		))
	}
	if iops < float64(existingPool.UtilizedIops) {
		return errors.NewUserInputValidationErr(fmt.Sprintf(
			"Requested IOPS (%.0f) must be >= current pool utilization (%.0f IOPS).",
			iops, float64(existingPool.UtilizedIops),
		))
	}
	return nil
}

// validateLabels will loop through the label map and validate labels according to Google requirements
func validateLabels(labels map[string]string) (*datamodel.JSONB, error) {
	_, err := json.Marshal(labels)
	if err != nil {
		return nil, errors.NewUserInputValidationErr("unable to marshal labels")
	}

	if len(labels) > 64 {
		return nil, errors.NewUserInputValidationErr("invalid label count")
	}

	jsonbLabels := make(datamodel.JSONB)
	for k, v := range labels {
		if len(k) == 0 {
			return nil, errors.NewUserInputValidationErr("key is required in label")
		}
		if len(strings.Split(k, "")) > maxRuneCount {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("label key '%s' is too long (length can't exceed %d characters)", k, maxRuneCount))
		}
		if len(k) > maxByteCount {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("label key '%s' is too long (encoded length can't exceed %d bytes)", k, maxByteCount))
		}
		if len(strings.Split(v, "")) > maxRuneCount {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("label value '%s' is too long (length can't exceed %d characters)", v, maxRuneCount))
		}
		if len(v) > maxByteCount {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("label value '%s' is too long (encoded length can't exceed %d bytes)", v, maxByteCount))
		}
		jsonbLabels[k] = v
	}
	return &jsonbLabels, nil
}

func _getAndSyncKmsConfigForPool(ctx context.Context, req *gcpgenserver.PoolV1beta, params *commonparams.CreatePoolParams, orchestratorInterface factory.OrchestratorFactory) (*models.KmsConfig, gcpgenserver.V1betaCreatePoolRes) {
	if req.KmsConfigId.Value == "" {
		return nil, nil
	}
	getKmsConfigParams := &commonparams.GetKmsConfigParams{
		UUID:          req.KmsConfigId.Value,
		AccountName:   params.AccountName,
		LocationID:    params.Region,
		ProjectNumber: params.AccountName,
	}
	kmsConfig, err := orchestratorInterface.GetKmsConfig(ctx, getKmsConfigParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			// try to find the kms config in SDE, it's possible that the user created the KMS config in SDE
			sdeKmsConfig, err := orchestratorInterface.GetSDEKmsConfiguration(ctx, getKmsConfigParams)
			if err != nil {
				if errors.IsNotFoundErr(err) {
					return nil, &gcpgenserver.V1betaCreatePoolBadRequest{
						Code:    http.StatusBadRequest,
						Message: fmt.Sprintf("KMS Config with ID %s not found", req.KmsConfigId.Value),
					}
				}
				return nil, &gcpgenserver.V1betaCreatePoolInternalServerError{
					Code:    http.StatusInternalServerError,
					Message: err.Error(),
				}
			}

			// make sure kms config is in ready or in use state then only create the kms config in VCP
			switch sdeKmsConfig.KmsState {
			case datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateInUse:
			default:
				return nil, &gcpgenserver.V1betaCreatePoolBadRequest{
					Code:    http.StatusPreconditionFailed,
					Message: fmt.Sprintf("Kms config is in invalid state %s", sdeKmsConfig.KmsState),
				}
			}

			// create and sync the KMS configuration with the SDE KMS configuration in VCP
			createKmsConfigParams := kms_activities.ConvertToCreateKmsConfigParams(sdeKmsConfig, params)
			kmsConfig, err := orchestratorInterface.CreateAndSyncKmsConfig(ctx, createKmsConfigParams)
			if err != nil {
				return nil, &gcpgenserver.V1betaCreatePoolInternalServerError{
					Code:    http.StatusInternalServerError,
					Message: err.Error(),
				}
			}
			return kmsConfig, nil
		}
		return nil, &gcpgenserver.V1betaCreatePoolInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
	}
	return kmsConfig, nil
}

// adSyncError captures AD sync failures and can be mapped to different API responses.
type adSyncError struct {
	kind    adSyncErrorKind
	code    float64
	message string
}

type adSyncErrorKind int

const (
	adSyncErrorKindBadRequest adSyncErrorKind = iota
	adSyncErrorKindInternal
)

func (e *adSyncError) toCreateResponse() gcpgenserver.V1betaCreatePoolRes {
	switch e.kind {
	case adSyncErrorKindBadRequest:
		return &gcpgenserver.V1betaCreatePoolBadRequest{Code: e.code, Message: e.message}
	default:
		return &gcpgenserver.V1betaCreatePoolInternalServerError{Code: e.code, Message: e.message}
	}
}

func (e *adSyncError) toUpdateResponse() gcpgenserver.V1betaUpdatePoolRes {
	switch e.kind {
	case adSyncErrorKindBadRequest:
		return &gcpgenserver.V1betaUpdatePoolBadRequest{Code: e.code, Message: e.message}
	default:
		return &gcpgenserver.V1betaUpdatePoolInternalServerError{Code: e.code, Message: e.message}
	}
}

func newAdSyncBadRequest(code float64, message string) *adSyncError {
	return &adSyncError{kind: adSyncErrorKindBadRequest, code: code, message: message}
}

func newAdSyncInternal(code float64, message string) *adSyncError {
	return &adSyncError{kind: adSyncErrorKindInternal, code: code, message: message}
}

func getAndSyncAdConfigForPool(ctx context.Context, activeDirectoryConfigId gcpgenserver.OptNilString, accountName, region, xCorrelationID string, orchestrator factory.OrchestratorFactory) (*models.ActiveDirectory, bool, *adSyncError) {
	log := util.GetLogger(ctx)
	ifADExistsInVCP := false
	if activeDirectoryConfigId.Value == "" {
		return nil, ifADExistsInVCP, nil
	}
	activeDirectoryUUID := activeDirectoryConfigId.Value

	getADParams := &commonparams.GetADParams{
		UUID:          activeDirectoryUUID,
		AccountName:   accountName,
		LocationID:    region,
		ProjectNumber: accountName,
	}
	adConfig, err := orchestrator.GetADConfig(ctx, getADParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			log.Warnf("Active Directory config with ID %s not found in VCP, trying CVP", activeDirectoryUUID)

			// Try to fetch AD from CVP if CVP_HOST is set
			if cvp.CVP_HOST != "" {
				cvpAd, cvpErr := getActiveDirectoryFromCVP(ctx, activeDirectoryUUID, accountName, region, xCorrelationID)
				if cvpErr != nil {
					log.Errorf("Failed to fetch Active Directory from CVP: %v", cvpErr)
					return nil, ifADExistsInVCP, newAdSyncBadRequest(http.StatusBadRequest, fmt.Sprintf("Active Directory Config with ID %s not found", activeDirectoryUUID))
				}
				if cvpAd != nil {
					log.Infof("Active Directory found in CVP, syncing to VCP, adUUID: %s", activeDirectoryUUID)
					// Convert CVP AD to models.ActiveDirectory and return
					adConfig = convertCVPActiveDirectoryToModel(cvpAd)
					return adConfig, ifADExistsInVCP, nil
				}
			}

			return nil, ifADExistsInVCP, newAdSyncBadRequest(http.StatusBadRequest, fmt.Sprintf("Active Directory Config with ID %s not found", activeDirectoryUUID))
		}
		return nil, ifADExistsInVCP, newAdSyncInternal(http.StatusInternalServerError, err.Error())
	}
	ifADExistsInVCP = true
	return adConfig, ifADExistsInVCP, nil
}

// getActiveDirectoryFromCVP retrieves Active Directory from CVP
func getActiveDirectoryFromCVP(ctx context.Context, adConfigID, projectNumber, locationID, XCorrelationID string) (*cvpmodels.ActiveDirectoryV1beta, error) {
	logger := util.GetLogger(ctx)

	// Get JWT token from context
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	if jwtToken == "" {
		logger.Warn("JWT token not found in context for CVP AD lookup", "adConfigID", adConfigID)
		// Continue without JWT token - CVP client might handle this
	}

	// Create CVP client
	cvpClient := createClient(logger, jwtToken)

	// Describe the Active Directory from CVP
	describeADParams := &active_directories.V1betaDescribeActiveDirectoryParams{
		Context:           ctx,
		ActiveDirectoryID: adConfigID,
		ProjectNumber:     projectNumber,
		LocationID:        locationID,
		XCorrelationID:    &XCorrelationID,
	}

	adResp, err := cvpClient.ActiveDirectories.V1betaDescribeActiveDirectory(describeADParams)
	if err != nil {
		logger.Error("Failed to describe Active Directory from CVP", "adConfigID", adConfigID, "error", err)
		return nil, fmt.Errorf("failed to describe Active Directory from CVP: %w", err)
	}

	if adResp == nil || adResp.Payload == nil {
		logger.Error("Empty response from CVP describe Active Directory", "adConfigID", adConfigID)
		return nil, fmt.Errorf("empty response from CVP describe Active Directory")
	}

	return adResp.Payload, nil
}

// convertCVPActiveDirectoryToModel converts CVP ActiveDirectoryV1beta to models.ActiveDirectory
func convertCVPActiveDirectoryToModel(cvpAd *cvpmodels.ActiveDirectoryV1beta) *models.ActiveDirectory {
	ad := &models.ActiveDirectory{
		BaseModel: models.BaseModel{
			UUID: cvpAd.ActiveDirectoryID,
		},
	}

	if cvpAd.ResourceID != nil {
		ad.AdName = *cvpAd.ResourceID
	}
	if cvpAd.Username != nil {
		ad.Username = *cvpAd.Username
	}
	if cvpAd.Domain != nil {
		ad.Domain = *cvpAd.Domain
	}
	if cvpAd.DNS != nil {
		ad.DNS = *cvpAd.DNS
	}
	if cvpAd.NetBIOS != nil {
		ad.NetBIOS = *cvpAd.NetBIOS
	}

	// Convert state
	switch cvpAd.ActiveDirectoryState {
	case cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateREADY:
		ad.State = datamodel.LifeCycleStateREADY
		ad.StateDetails = datamodel.LifeCycleStateReadyDetails
	case cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateCREATING:
		ad.State = datamodel.LifeCycleStateCreating
		ad.StateDetails = datamodel.LifeCycleStateCreatingDetails
	case cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateUPDATING:
		ad.State = datamodel.LifeCycleStateUpdating
		ad.StateDetails = datamodel.LifeCycleStateUpdatingDetails
	case cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateINUSE:
		ad.State = datamodel.LifeCycleStateInUse
		ad.StateDetails = datamodel.LifeCycleStateInUseDetails
	case cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateDELETING:
		ad.State = datamodel.LifeCycleStateDeleting
		ad.StateDetails = datamodel.LifeCycleStateDeletingDetails
	case cvpmodels.ActiveDirectoryV1betaActiveDirectoryStateERROR:
		ad.State = datamodel.LifeCycleStateError
		ad.StateDetails = datamodel.LifeCycleStateError
	default:
		ad.State = datamodel.LifeCycleStateREADY
		ad.StateDetails = datamodel.LifeCycleStateReadyDetails
	}

	if cvpAd.ActiveDirectoryStateDetails != "" {
		ad.StateDetails = cvpAd.ActiveDirectoryStateDetails
	}

	// Convert attributes
	adAttributes := &models.ActiveDirectoryAttributes{}
	if cvpAd.OrganizationalUnit != nil {
		adAttributes.OrganizationalUnit = *cvpAd.OrganizationalUnit
	}
	if cvpAd.Site != nil {
		adAttributes.Site = *cvpAd.Site
	}
	if cvpAd.KdcIP != "" {
		adAttributes.KdcIP = cvpAd.KdcIP
	}
	if cvpAd.KdcHostname != "" {
		adAttributes.KdcHostname = cvpAd.KdcHostname
	}
	if cvpAd.AesEncryption != nil {
		adAttributes.AesEncryption = *cvpAd.AesEncryption
	}
	if cvpAd.EncryptDCConnections != nil {
		adAttributes.EncryptDCConnections = *cvpAd.EncryptDCConnections
	}
	if cvpAd.LdapSigning != nil {
		adAttributes.LdapSigning = *cvpAd.LdapSigning
	}
	if cvpAd.AllowLocalNFSUsersWithLdap != nil {
		adAttributes.AllowLocalNFSUsersWithLdap = *cvpAd.AllowLocalNFSUsersWithLdap
	}
	if cvpAd.Description != nil {
		adAttributes.Description = *cvpAd.Description
	}

	// Convert user groups
	if len(cvpAd.BackupOperators) > 0 {
		adAttributes.BackupOperators = cvpAd.BackupOperators
	}
	if len(cvpAd.SecurityOperators) > 0 {
		adAttributes.SecurityOperators = cvpAd.SecurityOperators
	}
	if len(cvpAd.Administrators) > 0 {
		adAttributes.Administrators = cvpAd.Administrators
	}

	ad.ActiveDirectoryAttributes = adAttributes

	return ad
}

// V1betaRestoreOntapModeBackup restores a volume from backup (full-volume or file-level) for ontap mode.
func (h Handler) V1betaRestoreOntapModeBackup(ctx context.Context, req *gcpgenserver.RestoreBackupRequestV1beta, params gcpgenserver.V1betaRestoreOntapModeBackupParams) (gcpgenserver.V1betaRestoreOntapModeBackupRes, error) {
	logger := util.GetLogger(ctx)
	// LocationId comes from path parameters
	locationId := params.LocationId
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, locationId, nil)

	if !ExpertModeBackupEnabled {
		return &gcpgenserver.V1betaRestoreOntapModeBackupBadRequest{
			Code:    400,
			Message: "Ontap mode Backup/Restore feature is currently not enabled.",
		}, nil
	}

	region, _, parsingErr := parseAndValidateRegionAndZone(locationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaRestoreOntapModeBackupBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req.VolumeId == "" || req.BackupUri == "" {
		return &gcpgenserver.V1betaRestoreOntapModeBackupBadRequest{
			Code:    400,
			Message: "VolumeId and BackupUri are required",
		}, nil
	}

	backupPath := req.BackupUri
	components := strings.Split(backupPath, "/")
	if len(components) < MaxBackupPathComponents {
		return &gcpgenserver.V1betaRestoreOntapModeBackupBadRequest{
			Code:    400,
			Message: "Invalid backup path format",
		}, nil
	}

	if len(req.SourceFileList) > 0 {
		if len(req.SourceFileList) > MaxSourceFileList {
			return &gcpgenserver.V1betaRestoreOntapModeBackupBadRequest{
				Code:    400,
				Message: fmt.Sprintf("Source file list cannot contain more than %d files", MaxSourceFileList),
			}, nil
		}
	}
	var restoreFilePath string
	if req.RestoreFilePath.IsSet() {
		restoreFilePath = req.RestoreFilePath.Value
	}

	restoreParams := &commonparams.RestoreOntapModeBackupParams{
		AccountName:     params.ProjectNumber,
		BackupPath:      backupPath,
		SourceFileList:  req.SourceFileList,
		RestoreFilePath: restoreFilePath,
		VolumeUUID:      req.VolumeId,
		Region:          region,
		PoolID:          params.PoolId,
	}

	var jobUUID string
	var restoreErr error
	if len(req.SourceFileList) > 0 {
		jobUUID, restoreErr = h.Orchestrator.SFROntapModeBackup(ctx, restoreParams)
	} else {
		jobUUID, restoreErr = h.Orchestrator.RestoreOntapModeBackup(ctx, restoreParams)
	}
	if restoreErr != nil {
		if errors.IsUserInputValidationErr(restoreErr) || errors.IsNotFoundErr(restoreErr) {
			return &gcpgenserver.V1betaRestoreOntapModeBackupBadRequest{
				Code:    400,
				Message: restoreErr.Error(),
			}, nil
		}
		logger.Error("Failed to restore files from backup", "error", restoreErr.Error())
		return &gcpgenserver.V1betaRestoreOntapModeBackupInternalServerError{
			Code:    500,
			Message: restoreErr.Error(),
		}, nil
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + locationId + "/operations/" + jobUUID
	return &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(operationID),
		Done: gcpgenserver.NewOptBool(false),
	}, nil
}

// V1betaBackupConfig handles the request to attach a backup vault (and optionally a backup policy)
// to an expert mode volume.
func (h Handler) V1betaBackupConfig(ctx context.Context, req *gcpgenserver.BackupConfigRequestV1beta, params gcpgenserver.V1betaBackupConfigParams) (gcpgenserver.V1betaBackupConfigRes, error) {
	logger := util.GetLogger(ctx)
	locationId := params.LocationId
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, locationId, nil)

	if !ExpertModeBackupEnabled {
		return &gcpgenserver.V1betaBackupConfigBadRequest{
			Code:    400,
			Message: "Expert mode backup feature is currently not enabled.",
		}, nil
	}

	region, _, parsingErr := parseAndValidateRegionAndZone(locationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBackupConfigBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req.VolumeUuid == "" {
		return &gcpgenserver.V1betaBackupConfigBadRequest{
			Code:    400,
			Message: "volumeUuid is required",
		}, nil
	}

	reqBackupConfig := req.BackupConfig
	if blockTrueZonalAutoTieringAndBackup {
		pool, err := h.Orchestrator.DescribePool(ctx, params.PoolId, params.ProjectNumber)
		if err != nil {
			if errors.IsNotFoundErr(err) {
				return &gcpgenserver.V1betaBackupConfigBadRequest{
					Code:    400,
					Message: err.Error(),
				}, nil
			}
			logger.Error("Failed to describe pool for expert mode backup config", "poolID", params.PoolId, "error", err.Error())
			return &gcpgenserver.V1betaBackupConfigInternalServerError{
				Code:    500,
				Message: "Internal server error",
			}, nil
		}
		if models.BlockForTrueZonalPreview(pool, blockTrueZonalAutoTieringAndBackup) {
			if expertModeBackupConfigHasSetFields(reqBackupConfig) {
				return &gcpgenserver.V1betaBackupConfigBadRequest{
					Code:    400,
					Message: trueZonalBackupConfigNotSupportedErrorMsg,
				}, nil
			}
		}
	}

	// Patch semantics for all BackupConfig fields:
	//   null / absent → nil       (no-op: preserve existing value in DB)
	//   ""            → &""       (explicit detach/clear)
	//   "value"       → &"value"  (attach/set)
	var backupVaultID *string
	if reqBackupConfig.BackupVaultId.IsSet() && !reqBackupConfig.BackupVaultId.IsNull() {
		v := reqBackupConfig.BackupVaultId.Value
		backupVaultID = &v
	}

	var backupPolicyID *string
	if reqBackupConfig.BackupPolicyId.IsSet() && !reqBackupConfig.BackupPolicyId.IsNull() {
		v := reqBackupConfig.BackupPolicyId.Value
		backupPolicyID = &v
	}

	var scheduledBackupEnabled *bool
	if reqBackupConfig.ScheduledBackupEnabled.IsSet() && !reqBackupConfig.ScheduledBackupEnabled.IsNull() {
		v := reqBackupConfig.ScheduledBackupEnabled.Value
		scheduledBackupEnabled = &v
	}

	var kmsGrant *string
	if reqBackupConfig.KmsGrant.IsSet() && !reqBackupConfig.KmsGrant.IsNull() {
		if reqBackupConfig.KmsGrant.Value != "" {
			if !cmekBackupEnabled {
				return &gcpgenserver.V1betaBackupConfigBadRequest{
					Code:    400,
					Message: "CMEK backup is not enabled",
				}, nil
			}
		}
		v := reqBackupConfig.KmsGrant.Value
		kmsGrant = &v
	}

	var backupSchedule string
	if params.XNetappBackupSchedule.IsSet() {
		backupSchedule = params.XNetappBackupSchedule.Value
		if err := validateBackupScheduleCron(backupSchedule); err != nil {
			return &gcpgenserver.V1betaBackupConfigBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
	}

	manageParams := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
		AccountName:            params.ProjectNumber,
		PoolUUID:               params.PoolId,
		VolumeUUID:             req.VolumeUuid,
		BackupVaultID:          backupVaultID,
		BackupPolicyID:         backupPolicyID,
		ScheduledBackupEnabled: scheduledBackupEnabled,
		KmsGrant:               kmsGrant,
		BackupSchedule:         backupSchedule,
		Region:                 region,
	}

	backupConfig, jobUUID, err := h.Orchestrator.ManageBackupConfigForExpertModeVolume(ctx, manageParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaBackupConfigBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to manage backup config for expert mode volume", "error", err.Error())
		return &gcpgenserver.V1betaBackupConfigInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + locationId + "/operations/" + jobUUID
	op := &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(operationID),
		Done: gcpgenserver.NewOptBool(false),
	}

	if backupConfig != nil {
		backupConfigV1beta := convertDataProtectionToBackupConfigV1beta(backupConfig)
		if resp, encErr := encodeBackupConfigV1(backupConfigV1beta); encErr != nil {
			logger.Error("Failed to encode backup config response", "error", encErr.Error())
		} else {
			op.Response = resp
		}
	}

	return op, nil
}

func expertModeBackupConfigHasSetFields(cfg gcpgenserver.BackupConfigRequestV1betaBackupConfig) bool {
	return cfg.BackupVaultId.IsSet() || cfg.BackupPolicyId.IsSet() || cfg.ScheduledBackupEnabled.IsSet() || cfg.KmsGrant.IsSet()
}
