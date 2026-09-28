package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	log "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	maxBatchVolumeUUIDs        = env.GetInt("MAX_BATCH_VOLUME_UUIDS", 1000)
	fetchBatchVolumesFromCVPFn = fetchBatchVolumesFromCVP
	batchVolumeFieldNames      = []string{
		"resourceId",
		"created",
		"creationToken",
		"poolId",
		"kmsConfigId",
		"kmsConfigResourceId",
		"network",
		"activeDirectoryConfigId",
		"activeDirectoryResourceId",
		"serviceLevel",
		"securityStyle",
		"usedBytes",
		"quotaInBytes",
		"throughputMibps",
		"coldTierSizeGib",
		"snapReserve",
		"snapshotDirectory",
		"volumeState",
		"volumeStateDetails",
		"isDataProtection",
		"inReplication",
		"snapshotPolicy",
		"storageClass",
		"exportPolicy",
		"backupConfig",
		"tieringPolicy",
		"blockProperties",
		"blockDevices",
		"protocols",
		"restrictedActions",
		"smbSettings",
		"mountPoints",
		"labels",
		"kerberosEnabled",
		"ldapEnabled",
		"unixPermissions",
		"encryptionType",
		"description",
		"zone",
		"multipleEndpoints",
		"largeCapacity",
		"secondaryZone",
		"dedicatedCapacity",
		"largeVolumeConstituentCount",
		"cacheParameters",
		"hotTierSizeGib",
		"cloneDetails",
		"region",
	}
)

func (h Handler) V1betaBatchListVolumes(ctx context.Context, req *gcpgenserver.BatchVolumeUUIDListV1beta, params gcpgenserver.V1betaBatchListVolumesParams) (gcpgenserver.V1betaBatchListVolumesRes, error) {
	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListVolumesUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBatchListVolumesBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if len(req.VolumeUuids) == 0 {
		return &gcpgenserver.V1betaBatchListVolumesBadRequest{
			Code:    http.StatusBadRequest,
			Message: "volumeUuids is required and must have at least 1 item",
		}, nil
	}

	if len(req.VolumeUuids) > maxBatchVolumeUUIDs {
		return &gcpgenserver.V1betaBatchListVolumesBadRequest{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("volumeUuids in body should have at most %d items", maxBatchVolumeUUIDs),
		}, nil
	}

	fieldSet := buildBatchVolumeFieldSet(params.Fields)

	if cvp.CVP_HOST == "" {
		return h.batchListVolumesVCPOnly(ctx, req.VolumeUuids, fieldSet)
	}

	return h.batchListVolumesParallel(ctx, req.VolumeUuids, params, fieldSet)
}

func (h Handler) batchListVolumesVCPOnly(ctx context.Context, volumeUUIDs []string, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListVolumesRes, error) {
	logger := util.GetLogger(ctx)

	volumes, err := h.Orchestrator.GetVolumesByUUIDs(ctx, volumeUUIDs, commonparams.VolumeFetchOptionsFromFields(fieldSet))
	if err != nil {
		logger.Error("Failed to get volumes by UUIDs from VCP", "error", err.Error())
		return &gcpgenserver.V1betaBatchListVolumesInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting volumes",
		}, nil
	}

	vcpBatchVolumes := make([]gcpgenserver.BatchVolumeV1beta, 0, len(volumes))
	for _, volume := range volumes {
		converted := convertModelToVCPVolume(volume)
		if converted == nil {
			continue
		}
		updateVCPVolumeBaseForBatch(volume, converted)
		batchVolume, convErr := convertVolumeModelToBatchVolume(logger, *converted, fieldSet)
		if convErr != nil {
			logger.Error("Failed to convert VCP volume to batch volume", "volumeID", volume.UUID, "error", convErr.Error())
			continue
		}
		vcpBatchVolumes = append(vcpBatchVolumes, batchVolume)
	}

	return &gcpgenserver.V1betaBatchListVolumesOK{Volumes: vcpBatchVolumes}, nil
}

func (h Handler) batchListVolumesParallel(ctx context.Context, volumeUUIDs []string, params gcpgenserver.V1betaBatchListVolumesParams, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListVolumesRes, error) {
	logger := util.GetLogger(ctx)

	var (
		vcpVolumes []*models.Volume
		vcpErr     error
		cvpVolumes []gcpgenserver.BatchVolumeV1beta
		cvpErr     error
		wg         sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		vcpVolumes, vcpErr = h.Orchestrator.GetVolumesByUUIDs(ctx, volumeUUIDs, commonparams.VolumeFetchOptionsFromFields(fieldSet))
	}()

	go func() {
		defer wg.Done()
		cvpVolumes, cvpErr = fetchBatchVolumesFromCVPFn(ctx, volumeUUIDs, params, fieldSet)
	}()

	wg.Wait()

	if vcpErr != nil && cvpErr != nil {
		logger.Error("Both VCP and CVP batch volume queries failed", "vcpError", vcpErr.Error(), "cvpError", cvpErr.Error())
		return &gcpgenserver.V1betaBatchListVolumesInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting volumes from both systems",
		}, nil
	}

	var vcpBatchVolumes []gcpgenserver.BatchVolumeV1beta
	if vcpErr != nil {
		logger.Warn("VCP batch volume query failed, returning CVP results only", "error", vcpErr.Error())
	} else {
		vcpBatchVolumes = make([]gcpgenserver.BatchVolumeV1beta, 0, len(vcpVolumes))
		for _, volume := range vcpVolumes {
			converted := convertModelToVCPVolume(volume)
			if converted == nil {
				continue
			}
			updateVCPVolumeBaseForBatch(volume, converted)
			batchVolume, convErr := convertVolumeModelToBatchVolume(logger, *converted, fieldSet)
			if convErr != nil {
				logger.Error("Failed to convert VCP volume to batch volume", "volumeID", volume.UUID, "error", convErr.Error())
				continue
			}
			vcpBatchVolumes = append(vcpBatchVolumes, batchVolume)
		}
	}

	if cvpErr != nil {
		logger.Warn("CVP batch volume query failed, returning VCP results only", "error", cvpErr.Error())
	}

	allVolumes := append(vcpBatchVolumes, cvpVolumes...)
	return &gcpgenserver.V1betaBatchListVolumesOK{Volumes: allVolumes}, nil
}

func fetchBatchVolumesFromCVP(
	ctx context.Context,
	volumeUUIDs []string,
	params gcpgenserver.V1betaBatchListVolumesParams,
	fieldSet map[string]bool,
) ([]gcpgenserver.BatchVolumeV1beta, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createCVPClient(logger, jwtToken)

	var fields []string
	for _, f := range params.Fields {
		fields = append(fields, string(f))
	}

	cvpParams := cvpBatch.NewV1betaBatchListVolumesParamsWithContext(ctx)
	cvpParams.SetLocationID(params.LocationId)
	cvpParams.SetBody(&cvpmodels.VolumeIDListV1beta{
		VolumeUUIDs: volumeUUIDs,
	})
	if len(fields) > 0 {
		cvpParams.SetFields(fields)
	}
	if params.XCorrelationID.IsSet() {
		correlationID := params.XCorrelationID.Value
		cvpParams.SetXCorrelationID(&correlationID)
	}

	cvpResponse, err := cvpClient.Batch.V1betaBatchListVolumes(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list volumes failed: %w", err)
	}

	result := make([]gcpgenserver.BatchVolumeV1beta, 0)
	if cvpResponse != nil && cvpResponse.Payload != nil && cvpResponse.Payload.Volumes != nil {
		for _, cvpVolume := range cvpResponse.Payload.Volumes {
			if cvpVolume == nil {
				continue
			}
			batchVolume, convErr := convertCVPBatchVolumeToGCPBatchVolume(logger, cvpVolume, fieldSet)
			if convErr != nil {
				logger.Error("Failed to convert CVP batch volume", "volumeID", cvpVolume.VolumeID, "error", convErr.Error())
				continue
			}
			result = append(result, batchVolume)
		}
	}

	return result, nil
}

func buildBatchVolumeFieldSet(fields []gcpgenserver.V1betaBatchListVolumesFieldsItem) map[string]bool {
	return utils.BuildFieldSet(fields)
}

func convertVolumeModelToBatchVolume(logger log.Logger, v gcpgenserver.VolumeV1beta, fieldSet map[string]bool) (gcpgenserver.BatchVolumeV1beta, error) {
	bv := gcpgenserver.BatchVolumeV1beta{VolumeId: v.VolumeId.Value}
	if fieldSet == nil {
		return bv, nil
	}

	for field := range fieldSet {
		applyRequestedBatchVolumeField(logger, &bv, v, field)
	}
	return bv, nil
}

func updateVCPVolumeBaseForBatch(volume *models.Volume, base *gcpgenserver.VolumeV1beta) {
	if volume.SecondaryZone != "" {
		base.SecondaryZone = gcpgenserver.NewOptNilString(volume.SecondaryZone)
	}
}

func applyRequestedBatchVolumeField(logger log.Logger, bv *gcpgenserver.BatchVolumeV1beta, v gcpgenserver.VolumeV1beta, field string) {
	switch field {
	case "resourceId":
		if v.ResourceId != "" {
			bv.ResourceId = gcpgenserver.NewOptNilString(v.ResourceId)
		} else {
			bv.ResourceId.SetToNull()
		}
	case "created":
		if v.Created.IsSet() {
			bv.Created = gcpgenserver.NewOptNilDateTime(v.Created.Value)
		} else {
			bv.Created.SetToNull()
		}
	case "creationToken":
		if v.CreationToken.IsSet() {
			bv.CreationToken = gcpgenserver.NewOptNilString(v.CreationToken.Value)
		} else {
			bv.CreationToken.SetToNull()
		}
	case "poolId":
		if poolID, ok := v.PoolId.Get(); ok {
			bv.PoolId = gcpgenserver.NewOptNilString(poolID)
		} else {
			bv.PoolId.SetToNull()
		}
	case "kmsConfigId":
		if kmsConfigID, ok := v.KmsConfigId.Get(); ok {
			bv.KmsConfigId = gcpgenserver.NewOptNilString(kmsConfigID)
		} else {
			bv.KmsConfigId.SetToNull()
		}
	case "kmsConfigResourceId":
		if kmsConfigResourceID, ok := v.KmsConfigResourceId.Get(); ok {
			bv.KmsConfigResourceId = gcpgenserver.NewOptNilString(kmsConfigResourceID)
		} else {
			bv.KmsConfigResourceId.SetToNull()
		}
	case "network":
		if v.Network.IsSet() {
			bv.Network = gcpgenserver.NewOptNilString(v.Network.Value)
		} else {
			bv.Network.SetToNull()
		}
	case "activeDirectoryConfigId":
		if activeDirectoryConfigID, ok := v.ActiveDirectoryConfigId.Get(); ok {
			bv.ActiveDirectoryConfigId = gcpgenserver.NewOptNilString(activeDirectoryConfigID)
		} else {
			bv.ActiveDirectoryConfigId.SetToNull()
		}
	case "activeDirectoryResourceId":
		if activeDirectoryResourceID, ok := v.ActiveDirectoryResourceId.Get(); ok {
			bv.ActiveDirectoryResourceId = gcpgenserver.NewOptNilString(activeDirectoryResourceID)
		} else {
			bv.ActiveDirectoryResourceId.SetToNull()
		}
	case "serviceLevel":
		if v.ServiceLevel.IsSet() {
			bv.ServiceLevel = gcpgenserver.NewOptNilBatchVolumeV1betaServiceLevel(gcpgenserver.BatchVolumeV1betaServiceLevel(v.ServiceLevel.Value))
		} else {
			bv.ServiceLevel = gcpgenserver.NewOptNilBatchVolumeV1betaServiceLevel(gcpgenserver.BatchVolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED)
		}
	case "securityStyle":
		if v.SecurityStyle.IsSet() {
			bv.SecurityStyle = gcpgenserver.NewOptNilBatchVolumeV1betaSecurityStyle(gcpgenserver.BatchVolumeV1betaSecurityStyle(v.SecurityStyle.Value))
		} else {
			bv.SecurityStyle = gcpgenserver.NewOptNilBatchVolumeV1betaSecurityStyle(gcpgenserver.BatchVolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED)
		}
	case "usedBytes":
		if usedBytes, ok := v.UsedBytes.Get(); ok {
			bv.UsedBytes = gcpgenserver.NewOptNilFloat64(usedBytes)
		} else {
			bv.UsedBytes.SetToNull()
		}
	case "quotaInBytes":
		if v.QuotaInBytes.IsSet() {
			bv.QuotaInBytes = gcpgenserver.NewOptNilFloat64(v.QuotaInBytes.Value)
		} else {
			bv.QuotaInBytes.SetToNull()
		}
	case "throughputMibps":
		if throughputMibps, ok := v.ThroughputMibps.Get(); ok {
			bv.ThroughputMibps = gcpgenserver.NewOptNilFloat64(throughputMibps)
		} else {
			bv.ThroughputMibps.SetToNull()
		}
	case "coldTierSizeGib":
		if coldTierSizeGib, ok := v.ColdTierSizeGib.Get(); ok {
			bv.ColdTierSizeGib = gcpgenserver.NewOptNilFloat64(coldTierSizeGib)
		} else {
			bv.ColdTierSizeGib.SetToNull()
		}
	case "snapReserve":
		if v.SnapReserve.IsSet() {
			bv.SnapReserve = gcpgenserver.NewOptNilFloat64(v.SnapReserve.Value)
		} else {
			bv.SnapReserve.SetToNull()
		}
	case "snapshotDirectory":
		if v.SnapshotDirectory.IsSet() {
			bv.SnapshotDirectory = gcpgenserver.NewOptNilBool(v.SnapshotDirectory.Value)
		} else {
			bv.SnapshotDirectory.SetToNull()
		}
	case "volumeState":
		if v.VolumeState.IsSet() {
			bv.VolumeState = gcpgenserver.NewOptNilBatchVolumeV1betaVolumeState(gcpgenserver.BatchVolumeV1betaVolumeState(v.VolumeState.Value))
		} else {
			bv.VolumeState = gcpgenserver.NewOptNilBatchVolumeV1betaVolumeState(gcpgenserver.BatchVolumeV1betaVolumeStateSTATEUNSPECIFIED)
		}
	case "volumeStateDetails":
		if v.VolumeStateDetails.IsSet() {
			bv.VolumeStateDetails = gcpgenserver.NewOptNilString(v.VolumeStateDetails.Value)
		} else {
			bv.VolumeStateDetails.SetToNull()
		}
	case "isDataProtection":
		if v.IsDataProtection.IsSet() {
			bv.IsDataProtection = gcpgenserver.NewOptNilBool(v.IsDataProtection.Value)
		} else {
			bv.IsDataProtection.SetToNull()
		}
	case "inReplication":
		if v.InReplication.IsSet() {
			bv.InReplication = gcpgenserver.NewOptNilBool(v.InReplication.Value)
		} else {
			bv.InReplication = gcpgenserver.NewOptNilBool(false)
		}
	case "snapshotPolicy":
		if v.SnapshotPolicy.IsSet() {
			var snapshotPolicy gcpgenserver.BatchVolumeV1betaSnapshotPolicy
			if err := utils.RemapJSON(v.SnapshotPolicy.Value, &snapshotPolicy); err != nil {
				logBatchVolumeRemapError(logger, bv.VolumeId, "snapshotPolicy", err)
				bv.SnapshotPolicy.SetToNull()
			} else {
				bv.SnapshotPolicy = gcpgenserver.NewOptNilBatchVolumeV1betaSnapshotPolicy(snapshotPolicy)
			}
		} else {
			bv.SnapshotPolicy.SetToNull()
		}
	case "storageClass":
		if v.StorageClass.IsSet() {
			bv.StorageClass = gcpgenserver.NewOptNilBatchVolumeV1betaStorageClass(gcpgenserver.BatchVolumeV1betaStorageClass(v.StorageClass.Value))
		} else {
			bv.StorageClass = gcpgenserver.NewOptNilBatchVolumeV1betaStorageClass(gcpgenserver.BatchVolumeV1betaStorageClassSTORAGECLASSUNSPECIFIED)
		}
	case "exportPolicy":
		if v.ExportPolicy.IsSet() {
			var exportPolicy gcpgenserver.BatchVolumeV1betaExportPolicy
			if err := utils.RemapJSON(v.ExportPolicy.Value, &exportPolicy); err != nil {
				logBatchVolumeRemapError(logger, bv.VolumeId, "exportPolicy", err)
				bv.ExportPolicy.SetToNull()
			} else {
				bv.ExportPolicy = gcpgenserver.NewOptNilBatchVolumeV1betaExportPolicy(exportPolicy)
			}
		} else {
			bv.ExportPolicy.SetToNull()
		}
	case "backupConfig":
		if v.BackupConfig.IsSet() {
			var backupConfig gcpgenserver.BatchVolumeV1betaBackupConfig
			if err := utils.RemapJSON(v.BackupConfig.Value, &backupConfig); err != nil {
				logBatchVolumeRemapError(logger, bv.VolumeId, "backupConfig", err)
				bv.BackupConfig.SetToNull()
			} else {
				bv.BackupConfig = gcpgenserver.NewOptNilBatchVolumeV1betaBackupConfig(backupConfig)
			}
		} else {
			bv.BackupConfig.SetToNull()
		}
	case "tieringPolicy":
		if v.TieringPolicy.IsSet() {
			var tieringPolicy gcpgenserver.BatchVolumeV1betaTieringPolicy
			if err := utils.RemapJSON(v.TieringPolicy.Value, &tieringPolicy); err != nil {
				logBatchVolumeRemapError(logger, bv.VolumeId, "tieringPolicy", err)
				bv.TieringPolicy.SetToNull()
			} else {
				bv.TieringPolicy = gcpgenserver.NewOptNilBatchVolumeV1betaTieringPolicy(tieringPolicy)
			}
		} else {
			bv.TieringPolicy.SetToNull()
		}
	case "blockProperties":
		if v.BlockProperties.IsSet() {
			var blockProperties gcpgenserver.BatchVolumeV1betaBlockProperties
			if err := utils.RemapJSON(v.BlockProperties.Value, &blockProperties); err != nil {
				logBatchVolumeRemapError(logger, bv.VolumeId, "blockProperties", err)
				bv.BlockProperties.SetToNull()
			} else {
				bv.BlockProperties = gcpgenserver.NewOptNilBatchVolumeV1betaBlockProperties(blockProperties)
			}
		} else {
			bv.BlockProperties.SetToNull()
		}
	case "blockDevices":
		if len(v.BlockDevices) > 0 {
			bv.BlockDevices = gcpgenserver.NewOptNilBlockDeviceV1betaArray(v.BlockDevices)
		} else {
			bv.BlockDevices.SetToNull()
		}
	case "protocols":
		if len(v.Protocols) > 0 {
			bv.Protocols = gcpgenserver.NewOptNilProtocolsV1betaArray(v.Protocols)
		} else {
			bv.Protocols.SetToNull()
		}
	case "restrictedActions":
		if len(v.RestrictedActions) > 0 {
			restrictedActions := make([]gcpgenserver.BatchVolumeV1betaRestrictedActionsItem, len(v.RestrictedActions))
			for i, action := range v.RestrictedActions {
				restrictedActions[i] = gcpgenserver.BatchVolumeV1betaRestrictedActionsItem(action)
			}
			bv.RestrictedActions = gcpgenserver.NewOptNilBatchVolumeV1betaRestrictedActionsItemArray(restrictedActions)
		} else {
			bv.RestrictedActions = gcpgenserver.NewOptNilBatchVolumeV1betaRestrictedActionsItemArray([]gcpgenserver.BatchVolumeV1betaRestrictedActionsItem{
				gcpgenserver.BatchVolumeV1betaRestrictedActionsItemRESTRICTEDACTIONUNSPECIFIED,
			})
		}
	case "smbSettings":
		if len(v.SmbSettings) > 0 {
			smbSettings := make([]gcpgenserver.BatchVolumeV1betaSmbSettingsItem, len(v.SmbSettings))
			for i, setting := range v.SmbSettings {
				smbSettings[i] = gcpgenserver.BatchVolumeV1betaSmbSettingsItem(setting)
			}
			bv.SmbSettings = gcpgenserver.NewOptNilBatchVolumeV1betaSmbSettingsItemArray(smbSettings)
		} else {
			bv.SmbSettings.SetToNull()
		}
	case "mountPoints":
		if len(v.MountPoints) > 0 {
			bv.MountPoints = gcpgenserver.NewOptNilMountPointV1betaArray(v.MountPoints)
		} else {
			bv.MountPoints.SetToNull()
		}
	case "labels":
		if v.Labels.IsSet() {
			bv.Labels = gcpgenserver.NewOptNilBatchVolumeV1betaLabels(gcpgenserver.BatchVolumeV1betaLabels(v.Labels.Value))
		} else {
			bv.Labels.SetToNull()
		}
	case "kerberosEnabled":
		if kerberosEnabled, ok := v.KerberosEnabled.Get(); ok {
			bv.KerberosEnabled = gcpgenserver.NewOptNilBool(kerberosEnabled)
		} else {
			bv.KerberosEnabled.SetToNull()
		}
	case "ldapEnabled":
		if ldapEnabled, ok := v.LdapEnabled.Get(); ok {
			bv.LdapEnabled = gcpgenserver.NewOptNilBool(ldapEnabled)
		} else {
			bv.LdapEnabled.SetToNull()
		}
	case "unixPermissions":
		if unixPermissions, ok := v.UnixPermissions.Get(); ok {
			bv.UnixPermissions = gcpgenserver.NewOptNilString(unixPermissions)
		} else {
			bv.UnixPermissions.SetToNull()
		}
	case "encryptionType":
		if v.EncryptionType.IsSet() {
			bv.EncryptionType = gcpgenserver.NewOptNilBatchVolumeV1betaEncryptionType(gcpgenserver.BatchVolumeV1betaEncryptionType(v.EncryptionType.Value))
		} else {
			bv.EncryptionType = gcpgenserver.NewOptNilBatchVolumeV1betaEncryptionType(gcpgenserver.BatchVolumeV1betaEncryptionTypeENCRYPTIONTYPEUNSPECIFIED)
		}
	case "description":
		if description, ok := v.Description.Get(); ok {
			bv.Description = gcpgenserver.NewOptNilString(description)
		} else {
			bv.Description.SetToNull()
		}
	case "zone":
		if v.Zone.IsSet() {
			bv.Zone = gcpgenserver.NewOptNilString(v.Zone.Value)
		} else {
			bv.Zone.SetToNull()
		}
	case "multipleEndpoints":
		if multipleEndpoints, ok := v.MultipleEndpoints.Get(); ok {
			bv.MultipleEndpoints = gcpgenserver.NewOptNilBool(multipleEndpoints)
		} else {
			bv.MultipleEndpoints.SetToNull()
		}
	case "largeCapacity":
		if largeCapacity, ok := v.LargeCapacity.Get(); ok {
			bv.LargeCapacity = gcpgenserver.NewOptNilBool(largeCapacity)
		} else {
			bv.LargeCapacity.SetToNull()
		}
	case "secondaryZone":
		if secondaryZone, ok := v.SecondaryZone.Get(); ok {
			bv.SecondaryZone = gcpgenserver.NewOptNilString(secondaryZone)
		} else {
			bv.SecondaryZone.SetToNull()
		}
	case "dedicatedCapacity":
		if dedicatedCapacity, ok := v.DedicatedCapacity.Get(); ok {
			bv.DedicatedCapacity = gcpgenserver.NewOptNilBool(dedicatedCapacity)
		} else {
			bv.DedicatedCapacity.SetToNull()
		}
	case "largeVolumeConstituentCount":
		if largeVolumeConstituentCount, ok := v.LargeVolumeConstituentCount.Get(); ok {
			bv.LargeVolumeConstituentCount = gcpgenserver.NewOptNilInt32(largeVolumeConstituentCount)
		} else {
			bv.LargeVolumeConstituentCount.SetToNull()
		}
	case "cacheParameters":
		if v.CacheParameters.IsSet() {
			var cacheParameters gcpgenserver.BatchVolumeV1betaCacheParameters
			if err := utils.RemapJSON(v.CacheParameters.Value, &cacheParameters); err != nil {
				logBatchVolumeRemapError(logger, bv.VolumeId, "cacheParameters", err)
				bv.CacheParameters.SetToNull()
			} else {
				bv.CacheParameters = gcpgenserver.NewOptNilBatchVolumeV1betaCacheParameters(cacheParameters)
			}
		} else {
			bv.CacheParameters.SetToNull()
		}
	case "hotTierSizeGib":
		if hotTierSizeGib, ok := v.HotTierSizeGib.Get(); ok {
			bv.HotTierSizeGib = gcpgenserver.NewOptNilFloat64(hotTierSizeGib)
		} else {
			bv.HotTierSizeGib.SetToNull()
		}
	case "cloneDetails":
		if v.CloneDetails.IsSet() {
			var cloneDetails gcpgenserver.BatchVolumeV1betaCloneDetails
			if err := utils.RemapJSON(v.CloneDetails.Value, &cloneDetails); err != nil {
				logBatchVolumeRemapError(logger, bv.VolumeId, "cloneDetails", err)
				bv.CloneDetails.SetToNull()
			} else {
				bv.CloneDetails = gcpgenserver.NewOptNilBatchVolumeV1betaCloneDetails(cloneDetails)
			}
		} else {
			bv.CloneDetails.SetToNull()
		}
	case "region":
		if v.Zone.IsSet() {
			if region, _, err := utils.ParseRegionAndZone(v.Zone.Value); err == nil {
				bv.Region = gcpgenserver.NewOptNilString(region)
			} else {
				bv.Region.SetToNull()
			}
		} else {
			bv.Region.SetToNull()
		}
	}
}

func convertCVPBatchVolumeToGCPBatchVolume(logger log.Logger, in *cvpmodels.BatchVolumeV1beta, fieldSet map[string]bool) (gcpgenserver.BatchVolumeV1beta, error) {
	bv := gcpgenserver.BatchVolumeV1beta{VolumeId: in.VolumeID}
	if fieldSet == nil {
		return bv, nil
	}

	if err := utils.RemapJSON(in, &bv); err != nil {
		logBatchVolumeRemapError(logger, bv.VolumeId, "cvpBatchVolume", err)
	}
	applyBatchVolumeFieldSelection(&bv, fieldSet)
	return bv, nil
}

func logBatchVolumeRemapError(logger log.Logger, volumeID, field string, err error) {
	logger.Error("Failed to remap batch volume field", "volumeID", volumeID, "field", field, "error", err.Error())
}

func applyBatchVolumeFieldSelection(bv *gcpgenserver.BatchVolumeV1beta, fieldSet map[string]bool) {
	for _, field := range batchVolumeFieldNames {
		applyBatchVolumeField(bv, field, fieldSet != nil && fieldSet[field])
	}
}

func applyBatchVolumeField(bv *gcpgenserver.BatchVolumeV1beta, field string, requested bool) {
	switch field {
	case "resourceId":
		if !requested {
			bv.ResourceId.Reset()
		} else if !bv.ResourceId.Set {
			bv.ResourceId.SetToNull()
		}
	case "created":
		if !requested {
			bv.Created.Reset()
		} else if !bv.Created.Set {
			bv.Created.SetToNull()
		}
	case "creationToken":
		if !requested {
			bv.CreationToken.Reset()
		} else if !bv.CreationToken.Set {
			bv.CreationToken.SetToNull()
		}
	case "poolId":
		if !requested {
			bv.PoolId.Reset()
		} else if !bv.PoolId.Set {
			bv.PoolId.SetToNull()
		}
	case "kmsConfigId":
		if !requested {
			bv.KmsConfigId.Reset()
		} else if !bv.KmsConfigId.Set {
			bv.KmsConfigId.SetToNull()
		}
	case "kmsConfigResourceId":
		if !requested {
			bv.KmsConfigResourceId.Reset()
		} else if !bv.KmsConfigResourceId.Set {
			bv.KmsConfigResourceId.SetToNull()
		}
	case "network":
		if !requested {
			bv.Network.Reset()
		} else if !bv.Network.Set {
			bv.Network.SetToNull()
		}
	case "activeDirectoryConfigId":
		if !requested {
			bv.ActiveDirectoryConfigId.Reset()
		} else if !bv.ActiveDirectoryConfigId.Set {
			bv.ActiveDirectoryConfigId.SetToNull()
		}
	case "activeDirectoryResourceId":
		if !requested {
			bv.ActiveDirectoryResourceId.Reset()
		} else if !bv.ActiveDirectoryResourceId.Set {
			bv.ActiveDirectoryResourceId.SetToNull()
		}
	case "serviceLevel":
		if !requested {
			bv.ServiceLevel.Reset()
		} else if !bv.ServiceLevel.Set || bv.ServiceLevel.Value == gcpgenserver.BatchVolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED {
			bv.ServiceLevel = gcpgenserver.NewOptNilBatchVolumeV1betaServiceLevel(gcpgenserver.BatchVolumeV1betaServiceLevelSERVICELEVELUNSPECIFIED)
		}
	case "securityStyle":
		if !requested {
			bv.SecurityStyle.Reset()
		} else if !bv.SecurityStyle.Set || bv.SecurityStyle.Value == gcpgenserver.BatchVolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED {
			bv.SecurityStyle = gcpgenserver.NewOptNilBatchVolumeV1betaSecurityStyle(gcpgenserver.BatchVolumeV1betaSecurityStyleSECURITYSTYLEUNSPECIFIED)
		}
	case "usedBytes":
		if !requested {
			bv.UsedBytes.Reset()
		} else if !bv.UsedBytes.Set {
			bv.UsedBytes.SetToNull()
		}
	case "quotaInBytes":
		if !requested {
			bv.QuotaInBytes.Reset()
		} else if !bv.QuotaInBytes.Set {
			bv.QuotaInBytes.SetToNull()
		}
	case "throughputMibps":
		if !requested {
			bv.ThroughputMibps.Reset()
		} else if !bv.ThroughputMibps.Set {
			bv.ThroughputMibps.SetToNull()
		}
	case "coldTierSizeGib":
		if !requested {
			bv.ColdTierSizeGib.Reset()
		} else if !bv.ColdTierSizeGib.Set {
			bv.ColdTierSizeGib.SetToNull()
		}
	case "snapReserve":
		if !requested {
			bv.SnapReserve.Reset()
		} else if !bv.SnapReserve.Set {
			bv.SnapReserve.SetToNull()
		}
	case "snapshotDirectory":
		if !requested {
			bv.SnapshotDirectory.Reset()
		} else if !bv.SnapshotDirectory.Set {
			bv.SnapshotDirectory.SetToNull()
		}
	case "volumeState":
		if !requested {
			bv.VolumeState.Reset()
		} else if !bv.VolumeState.Set || bv.VolumeState.Value == gcpgenserver.BatchVolumeV1betaVolumeStateSTATEUNSPECIFIED {
			bv.VolumeState = gcpgenserver.NewOptNilBatchVolumeV1betaVolumeState(gcpgenserver.BatchVolumeV1betaVolumeStateSTATEUNSPECIFIED)
		}
	case "volumeStateDetails":
		if !requested {
			bv.VolumeStateDetails.Reset()
		} else if !bv.VolumeStateDetails.Set {
			bv.VolumeStateDetails.SetToNull()
		}
	case "isDataProtection":
		if !requested {
			bv.IsDataProtection.Reset()
		} else if !bv.IsDataProtection.Set {
			bv.IsDataProtection.SetToNull()
		}
	case "inReplication":
		if !requested {
			bv.InReplication.Reset()
		} else if !bv.InReplication.Set {
			bv.InReplication.SetToNull()
		}
	case "snapshotPolicy":
		if !requested {
			bv.SnapshotPolicy.Reset()
		} else if !bv.SnapshotPolicy.Set {
			bv.SnapshotPolicy.SetToNull()
		}
	case "storageClass":
		if !requested {
			bv.StorageClass.Reset()
		} else if !bv.StorageClass.Set {
			bv.StorageClass = gcpgenserver.NewOptNilBatchVolumeV1betaStorageClass(gcpgenserver.BatchVolumeV1betaStorageClassSTORAGECLASSUNSPECIFIED)
		}
	case "exportPolicy":
		if !requested {
			bv.ExportPolicy.Reset()
		} else if !bv.ExportPolicy.Set {
			bv.ExportPolicy.SetToNull()
		}
	case "backupConfig":
		if !requested {
			bv.BackupConfig.Reset()
		} else if !bv.BackupConfig.Set {
			bv.BackupConfig.SetToNull()
		}
	case "tieringPolicy":
		if !requested {
			bv.TieringPolicy.Reset()
		} else if !bv.TieringPolicy.Set {
			bv.TieringPolicy.SetToNull()
		}
	case "blockProperties":
		if !requested {
			bv.BlockProperties.Reset()
		} else if !bv.BlockProperties.Set {
			bv.BlockProperties.SetToNull()
		}
	case "blockDevices":
		if !requested {
			bv.BlockDevices.Reset()
		} else if !bv.BlockDevices.Set {
			bv.BlockDevices.SetToNull()
		}
	case "protocols":
		if !requested {
			bv.Protocols.Reset()
		} else if !bv.Protocols.Set {
			bv.Protocols.SetToNull()
		}
	case "restrictedActions":
		if !requested {
			bv.RestrictedActions.Reset()
		} else if !bv.RestrictedActions.Set {
			bv.RestrictedActions = gcpgenserver.NewOptNilBatchVolumeV1betaRestrictedActionsItemArray([]gcpgenserver.BatchVolumeV1betaRestrictedActionsItem{
				gcpgenserver.BatchVolumeV1betaRestrictedActionsItemRESTRICTEDACTIONUNSPECIFIED,
			})
		}
	case "smbSettings":
		if !requested {
			bv.SmbSettings.Reset()
		} else if !bv.SmbSettings.Set {
			bv.SmbSettings.SetToNull()
		}
	case "mountPoints":
		if !requested {
			bv.MountPoints.Reset()
		} else if !bv.MountPoints.Set {
			bv.MountPoints.SetToNull()
		}
	case "labels":
		if !requested {
			bv.Labels.Reset()
		} else if !bv.Labels.Set {
			bv.Labels.SetToNull()
		}
	case "kerberosEnabled":
		if !requested {
			bv.KerberosEnabled.Reset()
		} else if !bv.KerberosEnabled.Set {
			bv.KerberosEnabled.SetToNull()
		}
	case "ldapEnabled":
		if !requested {
			bv.LdapEnabled.Reset()
		} else if !bv.LdapEnabled.Set {
			bv.LdapEnabled.SetToNull()
		}
	case "unixPermissions":
		if !requested {
			bv.UnixPermissions.Reset()
		} else if !bv.UnixPermissions.Set {
			bv.UnixPermissions.SetToNull()
		}
	case "encryptionType":
		if !requested {
			bv.EncryptionType.Reset()
		} else if !bv.EncryptionType.Set || bv.EncryptionType.Value == gcpgenserver.BatchVolumeV1betaEncryptionTypeENCRYPTIONTYPEUNSPECIFIED {
			bv.EncryptionType = gcpgenserver.NewOptNilBatchVolumeV1betaEncryptionType(gcpgenserver.BatchVolumeV1betaEncryptionTypeENCRYPTIONTYPEUNSPECIFIED)
		}
	case "description":
		if !requested {
			bv.Description.Reset()
		} else if !bv.Description.Set {
			bv.Description.SetToNull()
		}
	case "zone":
		if !requested {
			bv.Zone.Reset()
		} else if !bv.Zone.Set {
			bv.Zone.SetToNull()
		}
	case "multipleEndpoints":
		if !requested {
			bv.MultipleEndpoints.Reset()
		} else if !bv.MultipleEndpoints.Set {
			bv.MultipleEndpoints.SetToNull()
		}
	case "largeCapacity":
		if !requested {
			bv.LargeCapacity.Reset()
		} else if !bv.LargeCapacity.Set {
			bv.LargeCapacity.SetToNull()
		}
	case "secondaryZone":
		if !requested {
			bv.SecondaryZone.Reset()
		} else if !bv.SecondaryZone.Set {
			bv.SecondaryZone.SetToNull()
		}
	case "dedicatedCapacity":
		if !requested {
			bv.DedicatedCapacity.Reset()
		} else if !bv.DedicatedCapacity.Set {
			bv.DedicatedCapacity.SetToNull()
		}
	case "largeVolumeConstituentCount":
		if !requested {
			bv.LargeVolumeConstituentCount.Reset()
		} else if !bv.LargeVolumeConstituentCount.Set {
			bv.LargeVolumeConstituentCount.SetToNull()
		}
	case "cacheParameters":
		if !requested {
			bv.CacheParameters.Reset()
		} else if !bv.CacheParameters.Set {
			bv.CacheParameters.SetToNull()
		}
	case "hotTierSizeGib":
		if !requested {
			bv.HotTierSizeGib.Reset()
		} else if !bv.HotTierSizeGib.Set {
			bv.HotTierSizeGib.SetToNull()
		}
	case "cloneDetails":
		if !requested {
			bv.CloneDetails.Reset()
		} else if !bv.CloneDetails.Set {
			bv.CloneDetails.SetToNull()
		}
	case "region":
		if !requested {
			bv.Region.Reset()
		} else if !bv.Region.Set {
			bv.Region.SetToNull()
		}
	}
}
