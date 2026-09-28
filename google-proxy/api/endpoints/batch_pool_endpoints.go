package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-openapi/runtime/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var fetchBatchPoolsFromCVPFn = fetchBatchPoolsFromCVP // replaceable for testing
var batchAuthFn = func(req *http.Request) middleware.Responder {
	return auth.BatchAuthenticatedGCP(req, func() middleware.Responder { return nil })
}

func (h Handler) V1betaBatchListPools(ctx context.Context, req *gcpgenserver.BatchPoolUUIDListV1beta, params gcpgenserver.V1betaBatchListPoolsParams) (gcpgenserver.V1betaBatchListPoolsRes, error) {
	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListPoolsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}
	responder := batchAuthFn(httpReq)
	if responder != nil {
		return &gcpgenserver.V1betaBatchListPoolsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBatchListPoolsBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if len(req.PoolUuids) == 0 {
		return &gcpgenserver.V1betaBatchListPoolsBadRequest{
			Code:    http.StatusBadRequest,
			Message: "poolUuids is required and must have at least 1 item",
		}, nil
	}

	if len(req.PoolUuids) > env.MaxBatchPoolUUIDs {
		return &gcpgenserver.V1betaBatchListPoolsBadRequest{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("poolUuids in body should have at most %d items", env.MaxBatchPoolUUIDs),
		}, nil
	}

	fieldSet := buildFieldSet(params.Fields)

	if cvp.CVP_HOST == "" {
		return h.batchListPoolsVCPOnly(ctx, req.PoolUuids, fieldSet)
	}

	return h.batchListPoolsParallel(ctx, req.PoolUuids, params, fieldSet)
}

func (h Handler) batchListPoolsVCPOnly(ctx context.Context, poolUUIDs []string, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListPoolsRes, error) {
	logger := util.GetLogger(ctx)

	pools, err := h.Orchestrator.GetPoolsByUUIDs(ctx, poolUUIDs, commonparams.PoolFetchOptionsFromFields(fieldSet))
	if err != nil {
		logger.Error("Failed to get pools by UUIDs from VCP", "error", err.Error())
		return &gcpgenserver.V1betaBatchListPoolsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting pools",
		}, nil
	}

	vcpBatchPools := make([]gcpgenserver.BatchPoolV1beta, 0, len(pools))
	for _, pool := range pools {
		vcpBatchPools = append(vcpBatchPools, convertPoolToBatchPool(pool, fieldSet))
	}

	return &gcpgenserver.V1betaBatchListPoolsOK{Pools: vcpBatchPools}, nil
}

func (h Handler) batchListPoolsParallel(ctx context.Context, poolUUIDs []string, params gcpgenserver.V1betaBatchListPoolsParams, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListPoolsRes, error) {
	logger := util.GetLogger(ctx)

	var (
		vcpPools []*models.Pool
		vcpErr   error
		cvpPools []gcpgenserver.BatchPoolV1beta
		cvpErr   error
		wg       sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		vcpPools, vcpErr = h.Orchestrator.GetPoolsByUUIDs(ctx, poolUUIDs, commonparams.PoolFetchOptionsFromFields(fieldSet))
	}()

	go func() {
		defer wg.Done()
		cvpPools, cvpErr = fetchBatchPoolsFromCVPFn(ctx, poolUUIDs, params, fieldSet)
	}()

	wg.Wait()

	if vcpErr != nil && cvpErr != nil {
		logger.Error("Both VCP and CVP batch pool queries failed",
			"vcpError", vcpErr.Error(), "cvpError", cvpErr.Error())
		return &gcpgenserver.V1betaBatchListPoolsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting pools from both systems",
		}, nil
	}

	var vcpBatchPools []gcpgenserver.BatchPoolV1beta
	if vcpErr != nil {
		logger.Warn("VCP batch pool query failed, returning SDE results only", "error", vcpErr.Error())
	} else {
		vcpBatchPools = make([]gcpgenserver.BatchPoolV1beta, 0, len(vcpPools))
		for _, pool := range vcpPools {
			vcpBatchPools = append(vcpBatchPools, convertPoolToBatchPool(pool, fieldSet))
		}
	}

	if cvpErr != nil {
		logger.Warn("CVP batch pool query failed, returning VCP results only", "error", cvpErr.Error())
	}

	allPools := append(vcpBatchPools, cvpPools...)
	return &gcpgenserver.V1betaBatchListPoolsOK{Pools: allPools}, nil
}

func fetchBatchPoolsFromCVP(
	ctx context.Context,
	poolUUIDs []string,
	params gcpgenserver.V1betaBatchListPoolsParams,
	fieldSet map[string]bool,
) ([]gcpgenserver.BatchPoolV1beta, error) {
	cvpClient := cvpClientFromContext(ctx)

	cvpParams := cvpBatch.NewV1betaBatchListPoolsParamsWithContext(ctx)
	cvpParams.SetBody(&cvpmodels.PoolIDListV1beta{
		PoolUUIDs: poolUUIDs,
	})
	applyBatchCvpListCommonParams(cvpParams, params.LocationId, batchListFieldStrings(params.Fields), params.XCorrelationID)

	cviResponse, err := cvpClient.Batch.V1betaBatchListPools(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list pools failed: %w", err)
	}

	var result []gcpgenserver.BatchPoolV1beta
	if cviResponse != nil && cviResponse.Payload != nil && cviResponse.Payload.Pools != nil {
		for _, cvpPool := range cviResponse.Payload.Pools {
			if cvpPool != nil {
				bp := convertCVPBatchPoolToGCPBatchPool(cvpPool)
				applyBatchPoolFieldSelection(&bp, fieldSet, false)
				result = append(result, bp)
			}
		}
	}

	return result, nil
}

func buildFieldSet(fields []gcpgenserver.V1betaBatchListPoolsFieldsItem) map[string]bool {
	return utils.BuildFieldSet(fields)
}

// convertPoolToBatchPool converts a VCP pool to BatchPoolV1beta.
// When fieldSet is nil (no fields requested), only poolId is returned.
// When fieldSet is provided, only requested fields are populated; missing values are null.
func convertPoolToBatchPool(pool *models.Pool, fieldSet map[string]bool) gcpgenserver.BatchPoolV1beta {
	bp := gcpgenserver.BatchPoolV1beta{
		PoolId: gcpgenserver.NewOptNilString(pool.UUID),
	}

	if fieldSet == nil {
		return bp
	}

	bp.CreatedAt = gcpgenserver.NewOptNilDateTime(pool.CreatedAt)
	bp.UpdatedAt = gcpgenserver.NewOptNilDateTime(pool.UpdatedAt)
	if pool.DeletedAt != nil {
		bp.DeletedAt = gcpgenserver.NewOptNilDateTime(*pool.DeletedAt)
	}
	if pool.Name != "" {
		bp.ResourceId = gcpgenserver.NewOptNilString(pool.Name)
	}
	if pool.Description != "" {
		bp.Description = gcpgenserver.NewOptNilString(pool.Description)
	}
	if pool.VendorSubNetID != "" {
		bp.Network = gcpgenserver.NewOptNilString(pool.VendorSubNetID)
	}
	bp.SizeInBytes = gcpgenserver.NewOptNilFloat64(float64(pool.SizeInBytes))

	var throughputValue float64
	var iopsValue int64
	customPerformanceEnabled := false
	if pool.CustomPerformanceParams != nil && pool.CustomPerformanceParams.Enabled {
		customPerformanceEnabled = true
		throughputValue = pool.CustomPerformanceParams.Throughput
		iopsValue = pool.CustomPerformanceParams.Iops
	} else {
		throughputValue = pool.TotalThroughputMibps
		iopsValue = pool.TotalIops
	}
	bp.TotalThroughputMibps = gcpgenserver.NewOptNilFloat64(throughputValue)
	bp.AvailableThroughputMibps = gcpgenserver.NewOptNilFloat64(throughputValue - pool.UtilizedThroughputMibps)
	bp.TotalIops = gcpgenserver.NewOptNilFloat64(float64(iopsValue))

	if pool.State != "" {
		bp.StoragePoolState = gcpgenserver.NewOptNilBatchPoolV1betaStoragePoolState(
			gcpgenserver.BatchPoolV1betaStoragePoolState(pool.State))
	}
	if pool.StateDetails != "" {
		bp.StoragePoolStateDetails = gcpgenserver.NewOptNilString(pool.StateDetails)
	}
	if pool.ServiceLevel != "" {
		bp.ServiceLevel = gcpgenserver.NewOptNilBatchPoolV1betaServiceLevel(
			gcpgenserver.BatchPoolV1betaServiceLevel(pool.ServiceLevel))
	}
	if pool.QosType != "" {
		bp.QosType = gcpgenserver.NewOptNilString(pool.QosType)
	}

	// Unified type and customPerformanceEnabled apply to VSA flex pools only; PO pools omit them.
	if !commonparams.IsPOModelPool(pool) {
		bp.CustomPerformanceEnabled = gcpgenserver.NewOptNilBool(customPerformanceEnabled)
		bp.Type = gcpgenserver.NewOptNilBatchPoolV1betaType(gcpgenserver.BatchPoolV1betaTypeUNIFIED)
		bp.UnifiedPool = gcpgenserver.NewOptNilBool(true)
	}
	bp.ManagedPool = gcpgenserver.NewOptNilBool(true)
	bp.IsHyperdiskAvailable = gcpgenserver.NewOptNilBool(
		pool.ServiceLevel == string(gcpgenserver.BatchPoolV1betaServiceLevelFLEX),
	)
	storageClass := gcpgenserver.BatchPoolV1betaStorageClassHARDWARE
	if pool.ServiceLevel == string(gcpgenserver.BatchPoolV1betaServiceLevelFLEX) {
		storageClass = gcpgenserver.BatchPoolV1betaStorageClassSOFTWARE
	}
	bp.StorageClass = gcpgenserver.NewOptNilBatchPoolV1betaStorageClass(storageClass)
	bp.AllowAutoTiering = gcpgenserver.NewOptNilBool(pool.AllowAutoTiering)
	bp.LargeCapacity = gcpgenserver.NewOptNilBool(pool.LargeCapacity)
	bp.SatisfiesPzs = gcpgenserver.NewOptNilBool(pool.SatisfiesPzs)
	bp.SatisfiesPzi = gcpgenserver.NewOptNilBool(pool.SatisfiesPzi)
	if pool.APIAccessMode != "" {
		bp.Mode = gcpgenserver.NewOptNilBatchPoolV1betaMode(gcpgenserver.BatchPoolV1betaMode(pool.APIAccessMode))
	}

	if pool.PoolAttributes != nil {
		bp.AllocatedBytes = gcpgenserver.NewOptNilFloat64(pool.PoolAttributes.AllocatedBytes)
		bp.NumberOfVolumes = gcpgenserver.NewOptNilInt32(int32(pool.PoolAttributes.NumberOfVolumes))
		bp.LdapEnabled = gcpgenserver.NewOptNilBool(pool.PoolAttributes.LdapEnabled)
		if pool.PoolAttributes.PrimaryZone != "" {
			bp.Zone = gcpgenserver.NewOptNilString(pool.PoolAttributes.PrimaryZone)
		}
		if pool.PoolAttributes.IsRegionalHA && pool.PoolAttributes.SecondaryZone != "" {
			bp.SecondaryZone = gcpgenserver.NewOptNilString(pool.PoolAttributes.SecondaryZone)
		}
		if pool.PoolAttributes.Labels != nil {
			labels := gcpgenserver.BatchPoolV1betaLabels{}
			for key, value := range pool.PoolAttributes.Labels {
				labels[key] = value
			}
			bp.Labels = gcpgenserver.NewOptNilBatchPoolV1betaLabels(labels)
		}
	}
	derivedRegion := pool.Region
	if pool.PoolAttributes != nil && pool.PoolAttributes.PrimaryZone != "" {
		if region, _, err := utils.ParseRegionAndZone(pool.PoolAttributes.PrimaryZone); err == nil {
			derivedRegion = region
		}
	}
	if derivedRegion != "" {
		bp.Region = gcpgenserver.NewOptNilString(derivedRegion)
	}

	if pool.ActiveDirectoryConfigId != "" && derivedRegion != "" {
		bp.ActiveDirectoryConfigId = gcpgenserver.NewOptNilString(pool.ActiveDirectoryConfigId)
		bp.ActiveDirectoryResourceId = gcpgenserver.NewOptNilString(fmt.Sprintf(
			"projects/%s/locations/%s/activeDirectories/%s", pool.AccountName, derivedRegion, pool.ActiveDirectoryResourceId))
	}

	kmsConfigID := ""
	if pool.KmsConfig != nil {
		kmsConfigID = pool.KmsConfig.UUID
		if kmsConfigID != "" {
			bp.KmsConfigId = gcpgenserver.NewOptNilString(kmsConfigID)
		}
		bp.KmsConfigResourceId = gcpgenserver.NewOptNilString(utils.ParsedKeyFullPathResource{
			ProjectID: pool.KmsConfig.KeyProjectID,
			KeyRing:   pool.KmsConfig.KeyRing,
			Location:  pool.KmsConfig.KeyRingLocation,
			CryptoKey: pool.KmsConfig.KeyName,
		}.String())
	}
	if enc := utils.GetEncryptionType(&kmsConfigID); enc != "" {
		bp.EncryptionType = gcpgenserver.NewOptNilBatchPoolV1betaEncryptionType(
			gcpgenserver.BatchPoolV1betaEncryptionType(enc))
	}

	if pool.AssetMetadata != nil {
		childAssets := make([]gcpgenserver.ChildAsset, 0, len(pool.AssetMetadata.ChildAssets))
		for _, asset := range pool.AssetMetadata.ChildAssets {
			childAssets = append(childAssets, gcpgenserver.ChildAsset{
				AssetType:  gcpgenserver.NewOptString(asset.AssetType),
				AssetNames: asset.AssetNames,
			})
		}
		bp.AssetLocationMetadata = gcpgenserver.NewOptNilBatchPoolV1betaAssetLocationMetadata(
			gcpgenserver.BatchPoolV1betaAssetLocationMetadata{
				ChildAssets: gcpgenserver.NewOptNilChildAssetArray(childAssets),
			},
		)
	}

	if pool.AllowAutoTiering && pool.AutoTieringConfig != nil {
		bp.HotTierSizeInBytes = gcpgenserver.NewOptNilFloat64(float64(pool.AutoTieringConfig.HotTierSizeInBytes))
		bp.EnableHotTierAutoResize = gcpgenserver.NewOptNilBool(pool.AutoTieringConfig.EnableHotTierAutoResize)
		bp.HotTierConsumption = gcpgenserver.NewOptNilInt64(pool.AutoTieringConfig.HotTierConsumption)
		bp.ColdTierConsumption = gcpgenserver.NewOptNilInt64(pool.AutoTieringConfig.ColdTierConsumption)
	}

	applyBatchPoolFieldSelection(&bp, fieldSet, commonparams.IsPOModelPool(pool))

	return bp
}

func convertCVPBatchPoolToGCPBatchPool(p *cvpmodels.BatchPoolV1beta) gcpgenserver.BatchPoolV1beta {
	bp := gcpgenserver.BatchPoolV1beta{}

	if p.PoolID != nil {
		bp.PoolId = gcpgenserver.NewOptNilString(*p.PoolID)
	}

	if p.CreatedAt != nil {
		bp.CreatedAt = gcpgenserver.NewOptNilDateTime(time.Time(*p.CreatedAt))
	}
	if p.UpdatedAt != nil {
		bp.UpdatedAt = gcpgenserver.NewOptNilDateTime(time.Time(*p.UpdatedAt))
	}
	if p.DeletedAt != nil {
		bp.DeletedAt = gcpgenserver.NewOptNilDateTime(time.Time(*p.DeletedAt))
	}
	if p.ResourceID != nil {
		bp.ResourceId = gcpgenserver.NewOptNilString(*p.ResourceID)
	}
	if p.Description != nil {
		bp.Description = gcpgenserver.NewOptNilString(*p.Description)
	}
	if p.ServiceLevel != nil {
		bp.ServiceLevel = gcpgenserver.NewOptNilBatchPoolV1betaServiceLevel(
			gcpgenserver.BatchPoolV1betaServiceLevel(*p.ServiceLevel))
	}
	if p.StoragePoolState != nil {
		bp.StoragePoolState = gcpgenserver.NewOptNilBatchPoolV1betaStoragePoolState(
			gcpgenserver.BatchPoolV1betaStoragePoolState(*p.StoragePoolState))
	}
	if p.StoragePoolStateDetails != nil {
		bp.StoragePoolStateDetails = gcpgenserver.NewOptNilString(*p.StoragePoolStateDetails)
	}
	if p.SizeInBytes != nil {
		bp.SizeInBytes = gcpgenserver.NewOptNilFloat64(*p.SizeInBytes)
	}
	if p.AllocatedBytes != nil {
		bp.AllocatedBytes = gcpgenserver.NewOptNilFloat64(*p.AllocatedBytes)
	}
	if p.NumberOfVolumes != nil {
		bp.NumberOfVolumes = gcpgenserver.NewOptNilInt32(int32(*p.NumberOfVolumes))
	}
	if p.KmsConfigID != nil {
		bp.KmsConfigId = gcpgenserver.NewOptNilString(*p.KmsConfigID)
	}
	if p.KmsConfigResourceID != nil {
		bp.KmsConfigResourceId = gcpgenserver.NewOptNilString(*p.KmsConfigResourceID)
	}
	if p.ActiveDirectoryConfigID != nil {
		bp.ActiveDirectoryConfigId = gcpgenserver.NewOptNilString(*p.ActiveDirectoryConfigID)
	}
	if p.ActiveDirectoryResourceID != nil {
		bp.ActiveDirectoryResourceId = gcpgenserver.NewOptNilString(*p.ActiveDirectoryResourceID)
	}
	if p.EncryptionType != nil {
		bp.EncryptionType = gcpgenserver.NewOptNilBatchPoolV1betaEncryptionType(
			gcpgenserver.BatchPoolV1betaEncryptionType(*p.EncryptionType))
	}
	if p.Zone != nil {
		bp.Zone = gcpgenserver.NewOptNilString(*p.Zone)
	}
	if p.SecondaryZone != nil {
		bp.SecondaryZone = gcpgenserver.NewOptNilString(*p.SecondaryZone)
	}
	if p.Region != nil {
		bp.Region = gcpgenserver.NewOptNilString(*p.Region)
	}
	if p.StorageClass != nil {
		bp.StorageClass = gcpgenserver.NewOptNilBatchPoolV1betaStorageClass(
			gcpgenserver.BatchPoolV1betaStorageClass(*p.StorageClass))
	}
	if p.Network != nil {
		bp.Network = gcpgenserver.NewOptNilString(*p.Network)
	}
	if p.LdapEnabled != nil {
		bp.LdapEnabled = gcpgenserver.NewOptNilBool(*p.LdapEnabled)
	}
	if p.GlobalAccessAllowed != nil {
		bp.GlobalAccessAllowed = gcpgenserver.NewOptNilBool(*p.GlobalAccessAllowed)
	}
	if p.Labels != nil {
		labels := gcpgenserver.BatchPoolV1betaLabels{}
		for k, v := range p.Labels {
			labels[k] = v
		}
		bp.Labels = gcpgenserver.NewOptNilBatchPoolV1betaLabels(labels)
	}
	if p.BillingLabels != nil {
		billingLabels := gcpgenserver.BatchPoolV1betaBillingLabels{}
		for k, v := range p.BillingLabels {
			billingLabels[k] = v
		}
		bp.BillingLabels = gcpgenserver.NewOptNilBatchPoolV1betaBillingLabels(billingLabels)
	}
	if p.AllowAutoTiering != nil {
		bp.AllowAutoTiering = gcpgenserver.NewOptNilBool(*p.AllowAutoTiering)
	}
	if p.SatisfiesPzs != nil {
		bp.SatisfiesPzs = gcpgenserver.NewOptNilBool(*p.SatisfiesPzs)
	}
	if p.SatisfiesPzi != nil {
		bp.SatisfiesPzi = gcpgenserver.NewOptNilBool(*p.SatisfiesPzi)
	}
	if p.CustomPerformanceEnabled != nil {
		bp.CustomPerformanceEnabled = gcpgenserver.NewOptNilBool(*p.CustomPerformanceEnabled)
	}
	if p.TotalThroughputMibps != nil {
		bp.TotalThroughputMibps = gcpgenserver.NewOptNilFloat64(*p.TotalThroughputMibps)
	}
	if p.TotalIops != nil {
		bp.TotalIops = gcpgenserver.NewOptNilFloat64(*p.TotalIops)
	}
	if p.HotTierSizeInBytes != nil {
		bp.HotTierSizeInBytes = gcpgenserver.NewOptNilFloat64(*p.HotTierSizeInBytes)
	}
	if p.EnableHotTierAutoResize != nil {
		bp.EnableHotTierAutoResize = gcpgenserver.NewOptNilBool(*p.EnableHotTierAutoResize)
	}
	if p.QosType != nil {
		bp.QosType = gcpgenserver.NewOptNilString(*p.QosType)
	}
	if p.ColdTierConsumption != nil {
		bp.ColdTierConsumption = gcpgenserver.NewOptNilInt64(*p.ColdTierConsumption)
	}
	if p.HotTierConsumption != nil {
		bp.HotTierConsumption = gcpgenserver.NewOptNilInt64(*p.HotTierConsumption)
	}
	if p.AvailableThroughputMibps != nil {
		bp.AvailableThroughputMibps = gcpgenserver.NewOptNilFloat64(*p.AvailableThroughputMibps)
	}
	if p.UnifiedPool != nil {
		bp.UnifiedPool = gcpgenserver.NewOptNilBool(*p.UnifiedPool)
	}
	if p.LargeCapacity != nil {
		bp.LargeCapacity = gcpgenserver.NewOptNilBool(*p.LargeCapacity)
	}
	if p.Type != nil {
		bp.Type = gcpgenserver.NewOptNilBatchPoolV1betaType(gcpgenserver.BatchPoolV1betaType(*p.Type))
	}
	if p.Mode != nil {
		bp.Mode = gcpgenserver.NewOptNilBatchPoolV1betaMode(gcpgenserver.BatchPoolV1betaMode(*p.Mode))
	}
	if p.ManagedPool != nil {
		bp.ManagedPool = gcpgenserver.NewOptNilBool(*p.ManagedPool)
	}
	if p.IsHyperdiskAvailable != nil {
		bp.IsHyperdiskAvailable = gcpgenserver.NewOptNilBool(*p.IsHyperdiskAvailable)
	}
	if p.AssetLocationMetadata != nil {
		var childAssets []gcpgenserver.ChildAsset
		for _, asset := range p.AssetLocationMetadata.ChildAssets {
			if asset != nil {
				childAssets = append(childAssets, gcpgenserver.ChildAsset{
					AssetType:  gcpgenserver.NewOptString(asset.AssetType),
					AssetNames: asset.AssetNames,
				})
			}
		}
		bp.AssetLocationMetadata = gcpgenserver.NewOptNilBatchPoolV1betaAssetLocationMetadata(
			gcpgenserver.BatchPoolV1betaAssetLocationMetadata{
				ChildAssets: gcpgenserver.NewOptNilChildAssetArray(childAssets),
			})
	}

	return bp
}

// ensureRequestedFieldsPresent sets any requested field that is still absent to null.
// This guarantees all requested fields appear in the JSON response.
func ensureRequestedFieldsPresent(bp *gcpgenserver.BatchPoolV1beta, fieldSet map[string]bool, isPOPool bool) {
	if fieldSet == nil {
		return
	}
	if fieldSet["poolId"] && !bp.PoolId.Set {
		bp.PoolId.SetToNull()
	}
	if fieldSet["activeDirectoryConfigId"] && !bp.ActiveDirectoryConfigId.Set {
		bp.ActiveDirectoryConfigId.SetToNull()
	}
	if fieldSet["activeDirectoryResourceId"] && !bp.ActiveDirectoryResourceId.Set {
		bp.ActiveDirectoryResourceId.SetToNull()
	}
	if fieldSet["kmsConfigId"] && !bp.KmsConfigId.Set {
		bp.KmsConfigId.SetToNull()
	}
	if fieldSet["kmsConfigResourceId"] && !bp.KmsConfigResourceId.Set {
		bp.KmsConfigResourceId.SetToNull()
	}
	if fieldSet["network"] && !bp.Network.Set {
		bp.Network.SetToNull()
	}
	if fieldSet["resourceId"] && !bp.ResourceId.Set {
		bp.ResourceId.SetToNull()
	}
	if fieldSet["serviceLevel"] && !bp.ServiceLevel.Set {
		bp.ServiceLevel = gcpgenserver.NewOptNilBatchPoolV1betaServiceLevel(
			gcpgenserver.BatchPoolV1betaServiceLevelSERVICELEVELUNSPECIFIED)
	}
	if fieldSet["qosType"] && !bp.QosType.Set {
		bp.QosType.SetToNull()
	}
	if fieldSet["sizeInBytes"] && !bp.SizeInBytes.Set {
		bp.SizeInBytes.SetToNull()
	}
	if fieldSet["allocatedBytes"] && !bp.AllocatedBytes.Set {
		bp.AllocatedBytes.SetToNull()
	}
	if fieldSet["totalThroughputMibps"] && !bp.TotalThroughputMibps.Set {
		bp.TotalThroughputMibps.SetToNull()
	}
	if fieldSet["availableThroughputMibps"] && !bp.AvailableThroughputMibps.Set {
		bp.AvailableThroughputMibps.SetToNull()
	}
	if fieldSet["numberOfVolumes"] && !bp.NumberOfVolumes.Set {
		bp.NumberOfVolumes.SetToNull()
	}
	if fieldSet["storagePoolState"] && !bp.StoragePoolState.Set {
		bp.StoragePoolState = gcpgenserver.NewOptNilBatchPoolV1betaStoragePoolState(
			gcpgenserver.BatchPoolV1betaStoragePoolStateSTATEUNSPECIFIED)
	}
	if fieldSet["storagePoolStateDetails"] && !bp.StoragePoolStateDetails.Set {
		bp.StoragePoolStateDetails.SetToNull()
	}
	if fieldSet["createdAt"] && !bp.CreatedAt.Set {
		bp.CreatedAt.SetToNull()
	}
	if fieldSet["updatedAt"] && !bp.UpdatedAt.Set {
		bp.UpdatedAt.SetToNull()
	}
	if fieldSet["deletedAt"] && !bp.DeletedAt.Set {
		bp.DeletedAt.SetToNull()
	}
	if fieldSet["storageClass"] && !bp.StorageClass.Set {
		bp.StorageClass = gcpgenserver.NewOptNilBatchPoolV1betaStorageClass(
			gcpgenserver.BatchPoolV1betaStorageClassSTORAGECLASSUNSPECIFIED)
	}
	if fieldSet["description"] && !bp.Description.Set {
		bp.Description.SetToNull()
	}
	if fieldSet["ldapEnabled"] && !bp.LdapEnabled.Set {
		bp.LdapEnabled.SetToNull()
	}
	if fieldSet["encryptionType"] && !bp.EncryptionType.Set {
		bp.EncryptionType.SetToNull()
	}
	if fieldSet["zone"] && !bp.Zone.Set {
		bp.Zone.SetToNull()
	}
	if fieldSet["secondaryZone"] && !bp.SecondaryZone.Set {
		bp.SecondaryZone.SetToNull()
	}
	if fieldSet["region"] && !bp.Region.Set {
		bp.Region.SetToNull()
	}
	if fieldSet["globalAccessAllowed"] && !bp.GlobalAccessAllowed.Set {
		bp.GlobalAccessAllowed.SetToNull()
	}
	if fieldSet["labels"] && !bp.Labels.Set {
		bp.Labels.SetToNull()
	}
	if fieldSet["billingLabels"] && !bp.BillingLabels.Set {
		bp.BillingLabels.SetToNull()
	}
	if fieldSet["allowAutoTiering"] && !bp.AllowAutoTiering.Set {
		bp.AllowAutoTiering.SetToNull()
	}
	if fieldSet["hotTierSizeInBytes"] && !bp.HotTierSizeInBytes.Set {
		bp.HotTierSizeInBytes.SetToNull()
	}
	if fieldSet["enableHotTierAutoResize"] && !bp.EnableHotTierAutoResize.Set {
		bp.EnableHotTierAutoResize.SetToNull()
	}
	if fieldSet["satisfies_pzi"] && !bp.SatisfiesPzi.Set {
		bp.SatisfiesPzi.SetToNull()
	}
	if fieldSet["satisfies_pzs"] && !bp.SatisfiesPzs.Set {
		bp.SatisfiesPzs.SetToNull()
	}
	if fieldSet["assetLocationMetadata"] && !bp.AssetLocationMetadata.Set {
		bp.AssetLocationMetadata.SetToNull()
	}
	if !isPOPool && fieldSet["customPerformanceEnabled"] && !bp.CustomPerformanceEnabled.Set {
		bp.CustomPerformanceEnabled.SetToNull()
	}
	if fieldSet["totalIops"] && !bp.TotalIops.Set {
		bp.TotalIops.SetToNull()
	}
	if !isPOPool && fieldSet["type"] && !bp.Type.Set {
		bp.Type = gcpgenserver.NewOptNilBatchPoolV1betaType(
			gcpgenserver.BatchPoolV1betaTypeSTORAGEPOOLTYPEUNSPECIFIED)
	}
	if !isPOPool && fieldSet["unifiedPool"] && !bp.UnifiedPool.Set {
		bp.UnifiedPool.SetToNull()
	}
	if fieldSet["largeCapacity"] && !bp.LargeCapacity.Set {
		bp.LargeCapacity.SetToNull()
	}
	if fieldSet["hotTierConsumption"] && !bp.HotTierConsumption.Set {
		bp.HotTierConsumption.SetToNull()
	}
	if fieldSet["coldTierConsumption"] && !bp.ColdTierConsumption.Set {
		bp.ColdTierConsumption.SetToNull()
	}
	if fieldSet["mode"] && !bp.Mode.Set {
		bp.Mode = gcpgenserver.NewOptNilBatchPoolV1betaMode(
			gcpgenserver.BatchPoolV1betaModeMODEUNSPECIFIED)
	}
	if fieldSet["managedPool"] && !bp.ManagedPool.Set {
		bp.ManagedPool.SetToNull()
	}
	if fieldSet["isHyperdiskAvailable"] && !bp.IsHyperdiskAvailable.Set {
		bp.IsHyperdiskAvailable.SetToNull()
	}
}

func applyBatchPoolFieldSelection(bp *gcpgenserver.BatchPoolV1beta, fieldSet map[string]bool, isPOPool bool) {
	if fieldSet == nil {
		bp.ActiveDirectoryConfigId.Reset()
		bp.ActiveDirectoryResourceId.Reset()
		bp.KmsConfigId.Reset()
		bp.KmsConfigResourceId.Reset()
		bp.Network.Reset()
		bp.ResourceId.Reset()
		bp.ServiceLevel.Reset()
		bp.QosType.Reset()
		bp.SizeInBytes.Reset()
		bp.AllocatedBytes.Reset()
		bp.TotalThroughputMibps.Reset()
		bp.AvailableThroughputMibps.Reset()
		bp.NumberOfVolumes.Reset()
		bp.StoragePoolState.Reset()
		bp.StoragePoolStateDetails.Reset()
		bp.CreatedAt.Reset()
		bp.UpdatedAt.Reset()
		bp.DeletedAt.Reset()
		bp.StorageClass.Reset()
		bp.Description.Reset()
		bp.LdapEnabled.Reset()
		bp.EncryptionType.Reset()
		bp.Zone.Reset()
		bp.SecondaryZone.Reset()
		bp.Region.Reset()
		bp.GlobalAccessAllowed.Reset()
		bp.Labels.Reset()
		bp.BillingLabels.Reset()
		bp.AllowAutoTiering.Reset()
		bp.HotTierSizeInBytes.Reset()
		bp.EnableHotTierAutoResize.Reset()
		bp.SatisfiesPzi.Reset()
		bp.SatisfiesPzs.Reset()
		bp.AssetLocationMetadata.Reset()
		bp.CustomPerformanceEnabled.Reset()
		bp.TotalIops.Reset()
		bp.Type.Reset()
		bp.UnifiedPool.Reset()
		bp.LargeCapacity.Reset()
		bp.HotTierConsumption.Reset()
		bp.ColdTierConsumption.Reset()
		bp.Mode.Reset()
		bp.ManagedPool.Reset()
		bp.IsHyperdiskAvailable.Reset()
		return
	}

	if !fieldSet["poolId"] {
		bp.PoolId.Reset()
	}
	if !fieldSet["activeDirectoryConfigId"] {
		bp.ActiveDirectoryConfigId.Reset()
	}
	if !fieldSet["activeDirectoryResourceId"] {
		bp.ActiveDirectoryResourceId.Reset()
	}
	if !fieldSet["kmsConfigId"] {
		bp.KmsConfigId.Reset()
	}
	if !fieldSet["kmsConfigResourceId"] {
		bp.KmsConfigResourceId.Reset()
	}
	if !fieldSet["network"] {
		bp.Network.Reset()
	}
	if !fieldSet["resourceId"] {
		bp.ResourceId.Reset()
	}
	if !fieldSet["serviceLevel"] {
		bp.ServiceLevel.Reset()
	}
	if !fieldSet["qosType"] {
		bp.QosType.Reset()
	}
	if !fieldSet["sizeInBytes"] {
		bp.SizeInBytes.Reset()
	}
	if !fieldSet["allocatedBytes"] {
		bp.AllocatedBytes.Reset()
	}
	if !fieldSet["totalThroughputMibps"] {
		bp.TotalThroughputMibps.Reset()
	}
	if !fieldSet["availableThroughputMibps"] {
		bp.AvailableThroughputMibps.Reset()
	}
	if !fieldSet["numberOfVolumes"] {
		bp.NumberOfVolumes.Reset()
	}
	if !fieldSet["storagePoolState"] {
		bp.StoragePoolState.Reset()
	}
	if !fieldSet["storagePoolStateDetails"] {
		bp.StoragePoolStateDetails.Reset()
	}
	if !fieldSet["createdAt"] {
		bp.CreatedAt.Reset()
	}
	if !fieldSet["updatedAt"] {
		bp.UpdatedAt.Reset()
	}
	if !fieldSet["deletedAt"] {
		bp.DeletedAt.Reset()
	}
	if !fieldSet["storageClass"] {
		bp.StorageClass.Reset()
	}
	if !fieldSet["description"] {
		bp.Description.Reset()
	}
	if !fieldSet["ldapEnabled"] {
		bp.LdapEnabled.Reset()
	}
	if !fieldSet["encryptionType"] {
		bp.EncryptionType.Reset()
	}
	if !fieldSet["zone"] {
		bp.Zone.Reset()
	}
	if !fieldSet["secondaryZone"] {
		bp.SecondaryZone.Reset()
	}
	if !fieldSet["region"] {
		bp.Region.Reset()
	}
	if !fieldSet["globalAccessAllowed"] {
		bp.GlobalAccessAllowed.Reset()
	}
	if !fieldSet["labels"] {
		bp.Labels.Reset()
	}
	if !fieldSet["billingLabels"] {
		bp.BillingLabels.Reset()
	}
	if !fieldSet["allowAutoTiering"] {
		bp.AllowAutoTiering.Reset()
	}
	if !fieldSet["hotTierSizeInBytes"] {
		bp.HotTierSizeInBytes.Reset()
	}
	if !fieldSet["enableHotTierAutoResize"] {
		bp.EnableHotTierAutoResize.Reset()
	}
	if !fieldSet["satisfies_pzi"] {
		bp.SatisfiesPzi.Reset()
	}
	if !fieldSet["satisfies_pzs"] {
		bp.SatisfiesPzs.Reset()
	}
	if !fieldSet["assetLocationMetadata"] {
		bp.AssetLocationMetadata.Reset()
	}
	if !fieldSet["customPerformanceEnabled"] {
		bp.CustomPerformanceEnabled.Reset()
	}
	if !fieldSet["totalIops"] {
		bp.TotalIops.Reset()
	}
	if !fieldSet["type"] {
		bp.Type.Reset()
	}
	if !fieldSet["unifiedPool"] {
		bp.UnifiedPool.Reset()
	}
	if !fieldSet["largeCapacity"] {
		bp.LargeCapacity.Reset()
	}
	if !fieldSet["hotTierConsumption"] {
		bp.HotTierConsumption.Reset()
	}
	if !fieldSet["coldTierConsumption"] {
		bp.ColdTierConsumption.Reset()
	}
	if !fieldSet["mode"] {
		bp.Mode.Reset()
	}
	if !fieldSet["managedPool"] {
		bp.ManagedPool.Reset()
	}
	if !fieldSet["isHyperdiskAvailable"] {
		bp.IsHyperdiskAvailable.Reset()
	}

	ensureRequestedFieldsPresent(bp, fieldSet, isPOPool)
}

func getHTTPRequestFromContext(ctx context.Context) *http.Request {
	headers := ctx.Value(utilsmiddleware.HeaderContextKey)
	if headers == nil {
		return nil
	}
	httpHeaders, ok := headers.(http.Header)
	if !ok {
		return nil
	}
	req := &http.Request{Header: httpHeaders}
	req = req.WithContext(ctx)
	return req
}
