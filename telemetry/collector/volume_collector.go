package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// bmfDedupeKey deduplicates BMF billing rows within the per-volume loop for GCBDR Vault Switch Case, emit at most one row per (volume, billing project).
type bmfDedupeKey struct {
	VolumeUUID     string
	BillingAccount string
}

// VolumeMetricsResult holds the results from GetVolumeMetrics operation
type VolumeMetricsResult struct {
	// HydratedMetrics contains the traditional hydrated metrics
	HydratedMetrics []entity.HydratedMetric
	// HydratedMetricsDataModel contains the data model hydrated metrics
	HydratedMetricsDataModel []datamodel2.HydratedMetrics
	// VolumeAllocatedThroughputHydratedMetrics contains volume allocated throughput metrics
	VolumeAllocatedThroughputHydratedMetrics []entity.HydratedMetric
	// SFRHydratedMetrics contains sfr metrics
	SFRHydratedMetrics []entity.HydratedMetric
}

// GetVolumeMetrics retrieves volume allocated size metrics for volumes with backup data from the database and returns them in a structured result.
func GetVolumeMetrics(ctx context.Context, vcpDB database.Storage, config *common.TelemetryConfig, poolMetadataMap map[int64]metadata.ResourceMetadata, timestamp time.Time) (*VolumeMetricsResult, error) {
	logger := util.GetLogger(ctx)
	// Use optimized query that fetches only required fields without JOINs
	volumes, err := vcpDB.ListVolumesForTelemetryMetrics(ctx)
	if err != nil {
		logger.Error("Failed to get volume metrics", "error", err.Error())
		return &VolumeMetricsResult{}, err
	}
	logger.Info(fmt.Sprintf("Found %d volume metrics", len(volumes)))

	expertModeBackupEnabled := config.EnableBackupBillingMetrics && config.EnableExpertModeBackupBilling

	// Preserve the regular ST empty-volume path. Expert-mode volume throughput
	// loads account state lazily only if the expert table contains an eligible
	// volume.
	var accountStateMap map[string]string
	if len(volumes) > 0 || expertModeBackupEnabled {
		accountStateMap = buildAccountStateMap(ctx, vcpDB)
	}

	// Initialize a slice to hold the hydrated metrics
	var metrics []entity.HydratedMetric
	var volumeAllocatedThroughputMetrics []entity.HydratedMetric
	var sfrMetrics []entity.HydratedMetric
	var hydratedMetrics []datamodel2.HydratedMetrics

	expertModeThroughputFound := false
	if !expertModeBackupEnabled && hasExpertModeMetricsPoolMetadata(poolMetadataMap) {
		expertModeThroughputMetrics, err := getExpertModeVolumeAllocatedThroughputMetrics(
			ctx,
			vcpDB,
			config,
			poolMetadataMap,
			accountStateMap,
			timestamp,
		)
		if err != nil {
			logger.Error("Failed to get expert mode volume allocated throughput metrics", "error", err.Error())
		} else {
			volumeAllocatedThroughputMetrics = append(volumeAllocatedThroughputMetrics, expertModeThroughputMetrics...)
			expertModeThroughputFound = len(expertModeThroughputMetrics) > 0
		}
	}
	if len(volumes) == 0 && !expertModeBackupEnabled && !expertModeThroughputFound {
		return &VolumeMetricsResult{}, nil
	}

	// Tracks BMF rows already emitted to prevent double-billing a (volume, billing project)
	// pair — e.g. when the active vault and a detached vault belong to the same project.
	emittedBMF := make(map[bmfDedupeKey]struct{})

	// Pre-fetch backup vaults whenever backup billing is enabled. The individual billing
	// flags (CRB, CMEK, GCBDR, GCBDR BMF) are feature gates that decide eligibility and
	// attribution using this map; they do not control whether it is loaded. Loading it
	// unconditionally keeps active cross-project attribution and the detached-vault BMF
	// pass correct even when every skip flag is on.
	backupVaultMap := make(map[string]*datamodel.BackupVault)
	if config.EnableBackupBillingMetrics {
		backupVaults, err := vcpDB.GetMultipleBackupVaults(ctx, nil)
		if err != nil {
			logger.Error("Failed to fetch backup vaults for billing filters", "error", err.Error())
		} else {
			for _, bv := range backupVaults {
				backupVaultMap[bv.UUID] = bv
			}
			logger.Info(fmt.Sprintf("Fetched %d backup vaults for billing filters", len(backupVaults)))
		}
	}

	var sfrMetricsMap map[string]datamodel.SfrMetricsAggregate
	if config.SFRMetricsEnabled {
		startTime := timestamp.Add(-5 * time.Minute)
		endTime := timestamp
		logger.Infof("fetching sfr metrics from start time %v and end time %v", startTime, endTime)
		var err error
		sfrMetricsMap, err = vcpDB.GetSfrMetricsByTimeRange(ctx, startTime, endTime)
		if err != nil {
			logger.Error("Failed to get SFR metrics", "error", err.Error())
			// Continue processing even if SFR metrics fetch fails
			sfrMetricsMap = make(map[string]datamodel.SfrMetricsAggregate)
		}
		logger.Info(fmt.Sprintf("Found %d SFR metrics", len(sfrMetricsMap)))
	}

	// Iterate over all volumes and generate metrics
	for _, volume := range volumes {
		var volumeAllocatedThroughputMetric entity.HydratedMetric
		// Assemble metadata for the volume using optimized struct
		volumeMetadata := assembleVolumeMetadata(volume, config)

		// Get account name from VolumeAttributes
		accountName := volume.GetAccountName()

		// Validate volume attributes before processing
		if volume.UUID == "" {
			logger.Error(fmt.Sprintf("Volume UUID is missing for volume %s", volume.Name))
			continue
		}
		if volume.Name == "" {
			logger.Error(fmt.Sprintf("Volume name is missing for volume %s", volume.UUID))
			continue
		}
		if accountName == "" {
			logger.Error(fmt.Sprintf("Volume account name is missing for volume %s", volume.UUID))
			continue
		}
		// Skip metrics collection if account state is HYPERSCALERDISABLED
		if accountState, exists := accountStateMap[accountName]; exists && accountState == datamodel.AccountStateHyperscalerDisabled {
			logger.Debugf("Skipping volume %s (UUID: %s) metrics collection as account %s is in HYPERSCALERDISABLED state", volume.Name, volume.UUID, accountName)
			continue
		}

		// Handle case for zonal and regional volumes for VolumeAllocatedThroughput metric
		var resourceType metadata.ResourceType
		if poolMeta, ok := poolMetadataMap[volume.PoolID]; ok {
			if poolMeta.ResourceType == metadata.VolumePoolRegionalHA {
				resourceType = metadata.VolumeRegionalHA
			} else {
				resourceType = metadata.Volume
			}
		}

		if volume.Throughput != 0 {
			volumeAllocatedThroughputMetric = setupHydratedMetric(timestamp, volumeMetadata, metadata.VolumeAllocatedThroughput, float64(volume.Throughput))
			volumeAllocatedThroughputMetric.Metadata.ResourceType = resourceType
			volumeAllocatedThroughputMetrics = append(volumeAllocatedThroughputMetrics, volumeAllocatedThroughputMetric)
		} else {
			var poolThroughput *float64
			// Lookup pool metadata using PoolID
			if poolMeta, ok := poolMetadataMap[volume.PoolID]; ok {
				poolThroughput = poolMeta.Throughput
				if poolThroughput == nil {
					poolThroughput = nillable.ToPointer(0.0)
				}

				volumeAllocatedThroughputMetric = setupHydratedMetric(timestamp, volumeMetadata, metadata.VolumeAllocatedThroughput, *poolThroughput)
				volumeAllocatedThroughputMetric.Metadata.ResourceType = resourceType
				volumeAllocatedThroughputMetrics = append(volumeAllocatedThroughputMetrics, volumeAllocatedThroughputMetric)
			} else {
				logger.Warnf("Pool metadata missing for PoolID %d (volume %s)", volume.PoolID, volume.UUID)
			}
		}

		// Create a metric for the volume allocated size
		// Use the allocated size (size_in_bytes) as the quantity for volume allocated size
		allocatedSize := volume.SizeInBytes
		if config.EnableBackupBillingMetrics {
			// Include if files backup billing is enabled or if volume is SAN protocol
			isSANProtocol := volume.VolumeAttributes != nil && utils.IsSanProtocols(volume.VolumeAttributes.Protocols)
			if config.EnableFilesBackupBilling || isSANProtocol {
				if volume.DataProtection != nil && volume.DataProtection.BackupChainBytes != nil && *volume.DataProtection.BackupChainBytes > 0 {
					metric := setupHydratedMetric(timestamp, volumeMetadata, metadata.BackupEnabledVolumeAllocatedSize, float64(allocatedSize))
					metric.Metadata.ResourceType = resourceType

					// Skip BMF billing metrics for volumes whose vault feature (CRB/CMEK/GCBDR)
					// is disabled. TODO: CRB billing is temporarily disabled for preview; will
					// enable cross-region backup billing metrics as per VSCP-3455.
					skipBilling := shouldSkipBMFForVault(ctx, config, volume.DataProtection.BackupVaultID, lookupBackupVault(backupVaultMap, volume.DataProtection.BackupVaultID), volume.UUID)

					if !skipBilling {
						// For cross-project backup vaults, bill to the vault's owning project instead of the volume owner.
						billingAccountName := accountName
						if config.EnableGcbdrBackupBilling && config.EnableGcbdrBmfBilling && volume.DataProtection.BackupVaultID != "" {
							if bv, exists := backupVaultMap[volume.DataProtection.BackupVaultID]; exists &&
								bv.ServiceType == datamodel.ServiceTypeCrossProject &&
								bv.Account != nil && bv.Account.Name != "" {
								billingAccountName = bv.Account.Name
							}
						}
						if hydratedMetric := setupHydratedMetricsDataModel(metric.MeasuredType, metric.Metadata.ResourceType, billingAccountName, volumeMetadata, timestamp, float64(allocatedSize)); hydratedMetric != nil {
							if volume.DataProtection.BackupVaultID != "" {
								if bv, exists := backupVaultMap[volume.DataProtection.BackupVaultID]; exists &&
									bv.BackupVaultType == activities.CrossRegionBackupType &&
									bv.BackupRegionName != nil && *bv.BackupRegionName != "" {
									volumeMetadata.SetBackupRegionName(*bv.BackupRegionName)
									setCrossRegionRegionMetadata(logger, hydratedMetric, volumeMetadata)
								}
							}
							// Standard volumes always use DEFAULT mode.
							setBackupModeMetadata(hydratedMetric, BackupModeDefault)
							hydratedMetrics = append(hydratedMetrics, *hydratedMetric)
							emittedBMF[bmfDedupeKey{VolumeUUID: volume.UUID, BillingAccount: billingAccountName}] = struct{}{}
						}
					}
				}
			}
		}

		// Process SFR metrics if enabled and volume exists in map
		if config.SFRMetricsEnabled && len(sfrMetricsMap) > 0 {
			if sfrData, exists := sfrMetricsMap[volume.UUID]; exists {
				// Create SFR Total Size Restored Bytes metric
				sizeMetric := setupHydratedMetric(timestamp, volumeMetadata, metadata.SFRTotalSizeRestoredBytes, float64(sfrData.TotalSize))
				sizeMetric.Metadata.ResourceType = resourceType
				sfrMetrics = append(sfrMetrics, sizeMetric)

				// Create SFR Total Files Restored Count metric
				countMetric := setupHydratedMetric(timestamp, volumeMetadata, metadata.SFRTotalFilesRestoredCount, float64(sfrData.TotalCount))
				countMetric.Metadata.ResourceType = resourceType
				sfrMetrics = append(sfrMetrics, countMetric)
			} else {
				logger.Infof("No SFR metrics found for volume UUID: %s", volume.UUID)
			}
		}
	}

	// Expert-mode BMF; map stays empty when expert mode billing is disabled.
	expertVolumeMap := make(map[string]*database.ExpertModeVolumeMetricsData)
	if expertModeBackupEnabled {
		expertModeMetrics, expertModeThroughputMetrics, fetchedExpertVolumes, err := getExpertModeVolumeBackupMetrics(ctx, vcpDB, config, poolMetadataMap, backupVaultMap, accountStateMap, emittedBMF, timestamp)
		if err != nil {
			logger.Error("Failed to get expert mode volume backup metrics", "error", err.Error())
		} else {
			hydratedMetrics = append(hydratedMetrics, expertModeMetrics...)
			volumeAllocatedThroughputMetrics = append(volumeAllocatedThroughputMetrics, expertModeThroughputMetrics...)
			expertVolumeMap = fetchedExpertVolumes
		}
	}

	// Emit BMF rows for (volume, vault) pairs where available backups exist in vaults
	// other than the volume's currently-attached vault. The currently-attached vault row
	// (gated by BackupChainBytes > 0) is already emitted by the per-volume loop above.
	//
	// This covers GCBDR vault switching: a volume may have available backups in vaults it
	// has since detached from. Backups across multiple endpoints of the same vault are
	// collapsed to a single row (billed on volume allocated size).
	if config.EnableBackupBillingMetrics && config.EnableGcbdrBackupBilling && config.EnableGcbdrBmfBilling {
		hydratedMetrics = append(hydratedMetrics, collectDetachedVaultBMF(ctx, vcpDB, config, volumes, expertVolumeMap, backupVaultMap, accountStateMap, poolMetadataMap, emittedBMF, timestamp)...)
	}

	// Return the structured result
	return &VolumeMetricsResult{
		HydratedMetrics:                          metrics,
		HydratedMetricsDataModel:                 hydratedMetrics,
		VolumeAllocatedThroughputHydratedMetrics: volumeAllocatedThroughputMetrics,
		SFRHydratedMetrics:                       sfrMetrics,
	}, nil
}

func hasExpertModeMetricsPoolMetadata(poolMetadataMap map[int64]metadata.ResourceMetadata) bool {
	for _, poolMetadata := range poolMetadataMap {
		if poolMetadata.APIAccessMode != nil &&
			poolMetadata.ServiceLevel != nil &&
			isExpertModeMetricsPoolValues(*poolMetadata.APIAccessMode, *poolMetadata.ServiceLevel) {
			return true
		}
	}
	return false
}

// getExpertModeVolumeAllocatedThroughputMetrics emits the same configured
// throughput-limit metric as regular ST volumes, using volumes from the
// expert_mode_volumes table. Expert-mode volumes do not have a per-volume VPG,
// so their configured throughput falls back to the containing pool.
func getExpertModeVolumeAllocatedThroughputMetrics(
	ctx context.Context,
	vcpDB database.Storage,
	config *common.TelemetryConfig,
	poolMetadataMap map[int64]metadata.ResourceMetadata,
	accountStateMap map[string]string,
	timestamp time.Time,
) ([]entity.HydratedMetric, error) {
	logger := util.GetLogger(ctx)
	var metrics []entity.HydratedMetric
	offset := 0
	const limit = 1000
	accountStateLoaded := accountStateMap != nil

	for {
		pagination := &dbutils.Pagination{Offset: offset, Limit: limit}
		expertVolumes, err := vcpDB.ListExpertModeVolumesForTelemetryMetrics(ctx, pagination)
		if err != nil {
			return nil, fmt.Errorf("failed to list expert mode volumes for allocated throughput metrics (offset %d): %w", offset, err)
		}
		if len(expertVolumes) == 0 {
			break
		}

		for _, volume := range expertVolumes {
			if !accountStateLoaded &&
				isExpertModeMetricsPoolValues(volume.PoolAPIAccessMode, volume.PoolServiceLevel) {
				accountStateMap = buildAccountStateMap(ctx, vcpDB)
				accountStateLoaded = true
			}
			if metric := createExpertModeVolumeAllocatedThroughputMetric(
				logger,
				volume,
				config,
				poolMetadataMap,
				accountStateMap,
				timestamp,
			); metric != nil {
				metrics = append(metrics, *metric)
			}
		}

		offset += len(expertVolumes)
		if len(expertVolumes) < limit {
			break
		}
	}

	return metrics, nil
}

func createExpertModeVolumeAllocatedThroughputMetric(
	logger log.Logger,
	volume *database.ExpertModeVolumeMetricsData,
	config *common.TelemetryConfig,
	poolMetadataMap map[int64]metadata.ResourceMetadata,
	accountStateMap map[string]string,
	timestamp time.Time,
) *entity.HydratedMetric {
	if !isExpertModeMetricsPoolValues(volume.PoolAPIAccessMode, volume.PoolServiceLevel) {
		return nil
	}
	if volume.ExternalUUID == "" {
		logger.Warnf("Skipping expert mode volume %s allocated throughput metric because external UUID is missing", volume.Name)
		return nil
	}
	if volume.Name == "" || volume.AccountName == "" {
		logger.Warnf("Skipping expert mode volume %s allocated throughput metric because name or account is missing", volume.ExternalUUID)
		return nil
	}
	if accountState, exists := accountStateMap[volume.AccountName]; exists &&
		accountState == datamodel.AccountStateHyperscalerDisabled {
		return nil
	}

	poolMetadata, exists := poolMetadataMap[volume.PoolID]
	if !exists {
		logger.Warnf("Pool metadata missing for PoolID %d (expert mode volume %s)", volume.PoolID, volume.ExternalUUID)
		return nil
	}

	throughput := 0.0
	if poolMetadata.Throughput != nil {
		throughput = *poolMetadata.Throughput
	}

	volumeMetadata := assembleExpertModeVolumeMetadata(volume, config)
	volumeMetadata.SetResourceUUID(volume.ExternalUUID)
	resourceType := metadata.Volume
	if poolMetadata.ResourceType == metadata.VolumePoolRegionalHA {
		resourceType = metadata.VolumeRegionalHA
	}
	volumeMetadata.SetResourceType(resourceType)

	metric := setupHydratedMetric(
		timestamp,
		volumeMetadata,
		metadata.VolumeAllocatedThroughput,
		throughput,
	)
	return &metric
}

// getExpertModeVolumeBackupMetrics generates BackupEnabledVolumeAllocatedSize hydrated metrics
// for expert mode volumes that have active backup chains, emitting the
// /BackupManagementFeeGbBillable SKU when the volume qualifies for billing. It also returns a
// map of every expert mode volume fetched (regardless of whether it was billed here)
func getExpertModeVolumeBackupMetrics(
	ctx context.Context,
	vcpDB database.Storage,
	config *common.TelemetryConfig,
	poolMetadataMap map[int64]metadata.ResourceMetadata,
	backupVaultMap map[string]*datamodel.BackupVault,
	accountStateMap map[string]string,
	emittedBMF map[bmfDedupeKey]struct{},
	timestamp time.Time,
) ([]datamodel2.HydratedMetrics, []entity.HydratedMetric, map[string]*database.ExpertModeVolumeMetricsData, error) {
	logger := util.GetLogger(ctx)

	var hydratedMetrics []datamodel2.HydratedMetrics
	var throughputMetrics []entity.HydratedMetric
	expertVolumeDataMap := make(map[string]*database.ExpertModeVolumeMetricsData)

	offset := 0
	limit := config.PoolVolumeLabelPageSize
	totalProcessed := 0

	for {
		pagination := &dbutils.Pagination{
			Offset: offset,
			Limit:  limit,
		}

		expertVolumes, err := vcpDB.ListExpertModeVolumesForTelemetryMetrics(ctx, pagination)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to list expert mode volumes for backup billing metrics (offset %d): %w", offset, err)
		}

		if len(expertVolumes) == 0 {
			break
		}

		logger.Info(fmt.Sprintf("Processing %d expert mode volumes for backup billing metrics (offset %d)", len(expertVolumes), offset))

		for _, volume := range expertVolumes {
			if metric := createExpertModeVolumeAllocatedThroughputMetric(
				logger,
				volume,
				config,
				poolMetadataMap,
				accountStateMap,
				timestamp,
			); metric != nil {
				throughputMetrics = append(throughputMetrics, *metric)
			}
			// Key by backups.volume_uuid identity (external UUID for expert mode) for detached-vault joins.
			if volume.BackupVolumeUUID() != "" {
				expertVolumeDataMap[volume.BackupVolumeUUID()] = volume
			}
			if volume.AccountName == "" {
				logger.Error(fmt.Sprintf("Expert mode volume account name is missing for volume %s", volume.UUID))
				continue
			}
			if accountState, exists := accountStateMap[volume.AccountName]; exists && accountState == datamodel.AccountStateHyperscalerDisabled {
				logger.Debugf("Skipping expert mode volume %s (UUID: %s) metrics collection as account %s is in HYPERSCALERDISABLED state", volume.Name, volume.UUID, volume.AccountName)
				continue
			}

			if volume.BackupConfig == nil || volume.BackupConfig.BackupChainBytes == nil || *volume.BackupConfig.BackupChainBytes <= 0 {
				continue
			}

			isSANProtocol := utils.IsSanProtocols(volume.GetProtocols())
			if !config.EnableFilesBackupBilling && !isSANProtocol {
				continue
			}

			// Determine resource type based on pool metadata (regional HA vs standard).
			resourceType := metadata.Volume
			if poolMeta, ok := poolMetadataMap[volume.PoolID]; ok {
				if poolMeta.ResourceType == metadata.VolumePoolRegionalHA {
					resourceType = metadata.VolumeRegionalHA
				}
			}

			if shouldSkipBMFForVault(ctx, config, volume.BackupConfig.BackupVaultID, lookupBackupVault(backupVaultMap, volume.BackupConfig.BackupVaultID), volume.UUID) {
				continue
			}

			volumeMetadata := assembleExpertModeVolumeMetadata(volume, config)
			billingAccountName := volume.AccountName
			if config.EnableGcbdrBackupBilling && config.EnableGcbdrBmfBilling && volume.BackupConfig.BackupVaultID != "" {
				if bv, exists := backupVaultMap[volume.BackupConfig.BackupVaultID]; exists &&
					bv.ServiceType == datamodel.ServiceTypeCrossProject &&
					bv.Account != nil && bv.Account.Name != "" {
					billingAccountName = bv.Account.Name
				}
			}
			if hm := setupHydratedMetricsDataModel(
				metadata.BackupEnabledVolumeAllocatedSize,
				resourceType,
				billingAccountName,
				volumeMetadata,
				timestamp,
				float64(volume.SizeInBytes),
			); hm != nil {
				if volume.BackupConfig.BackupVaultID != "" {
					if bv, exists := backupVaultMap[volume.BackupConfig.BackupVaultID]; exists &&
						bv.BackupVaultType == activities.CrossRegionBackupType &&
						bv.BackupRegionName != nil && *bv.BackupRegionName != "" {
						volumeMetadata.SetBackupRegionName(*bv.BackupRegionName)
						setCrossRegionRegionMetadata(logger, hm, volumeMetadata)
					}
				}
				// Expert mode volumes always use ONTAP mode.
				setBackupModeMetadata(hm, BackupModeOntap)
				hydratedMetrics = append(hydratedMetrics, *hm)
				// Same key as the detached-vault pass so the active vault is not billed twice.
				emittedBMF[bmfDedupeKey{VolumeUUID: volume.BackupVolumeUUID(), BillingAccount: billingAccountName}] = struct{}{}
			}
		}

		totalProcessed += len(expertVolumes)
		offset += len(expertVolumes)
	}

	logger.Info(fmt.Sprintf("Processed %d expert mode volumes for backup billing metrics", totalProcessed))

	return hydratedMetrics, throughputMetrics, expertVolumeDataMap, nil
}

// assembleExpertModeVolumeMetadata creates ResourceMetadata for an expert mode volume.
func assembleExpertModeVolumeMetadata(volume *database.ExpertModeVolumeMetricsData, config *common.TelemetryConfig) metadata.ResourceMetadata {
	met := metadata.ResourceMetadata{}
	met.SetResourceUUID(volume.UUID)
	met.SetResourceName(volume.Name)
	met.SetResourceDisplayName(volume.Name)
	met.SetResourceType(metadata.Volume)
	met.SetSizeInBytes(volume.SizeInBytes)
	met.SetRegionName(config.RegionName)
	met.SetAccountName(volume.AccountName)
	met.SetDeploymentName(volume.DeploymentName)
	return met
}

// lookupBackupVault returns the vault for vaultID from the prefetched map, or nil when the
// id is empty or absent. A nil return lets shouldSkipBMFForVault apply its conservative
// (skip when the feature is disabled and the vault cannot be inspected) behaviour.
func lookupBackupVault(backupVaultMap map[string]*datamodel.BackupVault, vaultID string) *datamodel.BackupVault {
	if vaultID == "" {
		return nil
	}
	return backupVaultMap[vaultID]
}

// shouldSkipBMFForVault reports whether a BMF row must be suppressed for the vault identified
// by vaultID, based on feature flags. vault is the looked-up vault (nil on miss). It is shared
// by the active regular loop, the expert-mode loop, and the detached-vault pass so the
// CRB/CMEK/cross-project eligibility rules cannot drift between them. An empty vaultID means
// the volume has no attached vault, so no vault-based skip applies.
func shouldSkipBMFForVault(ctx context.Context, config *common.TelemetryConfig, vaultID string, vault *datamodel.BackupVault, volumeUUID string) bool {
	if vaultID == "" {
		return false
	}
	logger := util.GetLogger(ctx)

	// Cross-region backup vaults when CRB billing is disabled.
	if !config.EnableCrossRegionBackupBillingMetrics {
		if vault != nil {
			if vault.SourceRegionName != nil && vault.BackupRegionName != nil && *vault.SourceRegionName != *vault.BackupRegionName {
				logger.Debug("Skipping BackupEnabledVolumeAllocatedSize billing metric for cross-region backup vault", "volumeUUID", volumeUUID, "backupVaultID", vaultID)
				return true
			}
		} else {
			logger.Error("Backup vault not found in map", "backupVaultID", vaultID, "volumeUUID", volumeUUID)
			return true
		}
	}

	// CMEK backup vaults when CMEK billing is disabled. Conservative: if the vault cannot be
	// looked up while CMEK billing is off, treat it as non-billable.
	if !config.EnableCmekBackupBilling {
		if vault != nil {
			if vault.CmekAttributes != nil && vault.CmekAttributes.KmsConfigResourcePath != nil && *vault.CmekAttributes.KmsConfigResourcePath != "" {
				logger.Debug("Skipping BackupEnabledVolumeAllocatedSize billing metric for CMEK backup vault", "volumeUUID", volumeUUID, "backupVaultID", vaultID)
				return true
			}
		} else {
			logger.Error("Backup vault not found in map for CMEK billing check", "backupVaultID", vaultID, "volumeUUID", volumeUUID)
			return true
		}
	}

	// Cross-project backup vaults when GCBDR BMF billing is disabled.
	if vault != nil {
		if vault.ServiceType == datamodel.ServiceTypeCrossProject &&
			(!config.EnableGcbdrBackupBilling || !config.EnableGcbdrBmfBilling) {
			logger.Debug("Skipping BackupEnabledVolumeAllocatedSize billing metric for cross-project backup vault", "volumeUUID", volumeUUID, "backupVaultID", vaultID)
			return true
		}
	} else if !config.EnableGcbdrBackupBilling {
		logger.Error("Backup vault not found in map for cross-project billing check", "backupVaultID", vaultID, "volumeUUID", volumeUUID)
		return true
	}

	return false
}

// collectDetachedVaultBMF emits BackupEnabledVolumeAllocatedSize (BMF) billing rows for
// (volume, vault) pairs where available backups exist in vaults other than the volume's
// currently-attached vault. The active-vault row is produced by the main per-volume loop
// in GetVolumeMetrics (regular volumes) and by getExpertModeVolumeBackupMetrics (ONTAP/expert
// mode volumes); this function covers GCBDR vault switching where a volume still has backups
// in detached vaults, for both regular and expert mode volumes. Chain tips across multiple
// endpoints of the same vault collapse into a single billing row (volume allocated size billed
// once per vault).
func collectDetachedVaultBMF(
	ctx context.Context,
	vcpDB database.Storage,
	config *common.TelemetryConfig,
	volumes []*database.VolumeMetricsData,
	expertVolumeDataMap map[string]*database.ExpertModeVolumeMetricsData,
	backupVaultMap map[string]*datamodel.BackupVault,
	accountStateMap map[string]string,
	poolMetadataMap map[int64]metadata.ResourceMetadata,
	emittedBMF map[bmfDedupeKey]struct{},
	timestamp time.Time,
) []datamodel2.HydratedMetrics {
	logger := util.GetLogger(ctx)

	volumeDataMap := make(map[string]*database.VolumeMetricsData, len(volumes))
	for _, v := range volumes {
		volumeDataMap[v.UUID] = v
	}

	var allBackups []*datamodel.Backup
	offset := int64(0)
	limit := pageSize
	for {
		pagination := &dbutils.Pagination{Offset: int(offset), Limit: int(limit)}
		backups, err := vcpDB.GetBackupChainMetrics(ctx, nil, pagination)
		if err != nil {
			logger.Error("Failed to fetch backup chain metrics for detached-vault BMF rows", "error", err.Error())
			return nil
		}
		if len(backups) == 0 {
			break
		}
		allBackups = append(allBackups, backups...)
		offset += limit
	}
	if len(allBackups) == 0 {
		return nil
	}

	// Group chain tips by (VolumeUUID, VaultUUID) so endpoint splits collapse into one row.
	type bmfKey struct {
		VolumeUUID string
		VaultUUID  string
	}
	type bmfGroup struct {
		vault *datamodel.BackupVault
	}
	var orderedKeys []bmfKey
	groups := make(map[bmfKey]*bmfGroup)
	for _, b := range allBackups {
		if b.BackupVault == nil {
			continue
		}
		key := bmfKey{VolumeUUID: b.VolumeUUID, VaultUUID: b.BackupVault.UUID}
		if _, exists := groups[key]; !exists {
			groups[key] = &bmfGroup{vault: b.BackupVault}
			orderedKeys = append(orderedKeys, key)
		}
	}

	var out []datamodel2.HydratedMetrics
	for _, key := range orderedKeys {
		group := groups[key]

		resolved, ok := resolveDetachedBMFVolume(key.VolumeUUID, volumeDataMap, expertVolumeDataMap, poolMetadataMap, config)
		if !ok {
			continue
		}

		if accountState, ok := accountStateMap[resolved.accountName]; ok && accountState == datamodel.AccountStateHyperscalerDisabled {
			continue
		}

		// Protocol gate (matches existing BMF behaviour).
		if !(config.EnableFilesBackupBilling || resolved.isSANProtocol) {
			continue
		}

		// Apply the same CRB/CMEK/cross-project eligibility rules as the active paths so a
		// detached CMEK or cross-region chain is not billed while its feature is disabled.
		// Use the prefetched vault (nil on miss) so the conservative skip behaviour matches.
		if shouldSkipBMFForVault(ctx, config, key.VaultUUID, lookupBackupVault(backupVaultMap, key.VaultUUID), key.VolumeUUID) {
			continue
		}

		// Prefer the fully-populated vault from backupVaultMap (has SourceRegionName etc.).
		// Fall back to the vault returned by GetBackupChainMetrics if the map lookup misses.
		vault := group.vault
		if bv, ok := backupVaultMap[key.VaultUUID]; ok {
			vault = bv
		}

		// Cross-project vaults bill to the vault's owning project, not the volume owner.
		billingAccountName := resolved.accountName
		if vault.ServiceType == datamodel.ServiceTypeCrossProject && vault.Account != nil && vault.Account.Name != "" {
			billingAccountName = vault.Account.Name
		}

		// Dedupe: BMF is a volume-allocated-size charge. If this (volume, billing
		// project) already produced a BMF row (either from the main loop's active
		// vault or from an earlier detached vault in the same project), skip to
		// avoid billing the same project twice for the same volume.
		dedupeKey := bmfDedupeKey{VolumeUUID: key.VolumeUUID, BillingAccount: billingAccountName}
		if _, already := emittedBMF[dedupeKey]; already {
			continue
		}

		hm := setupHydratedMetricsDataModel(
			metadata.BackupEnabledVolumeAllocatedSize,
			resolved.resourceType,
			billingAccountName,
			resolved.volumeMetadata,
			timestamp,
			float64(resolved.sizeInBytes),
		)
		if hm == nil {
			continue
		}
		if vault.BackupVaultType == activities.CrossRegionBackupType &&
			vault.BackupRegionName != nil && *vault.BackupRegionName != "" {
			resolved.volumeMetadata.SetBackupRegionName(*vault.BackupRegionName)
			setCrossRegionRegionMetadata(logger, hm, resolved.volumeMetadata)
		}
		setBackupModeMetadata(hm, resolved.backupMode)
		out = append(out, *hm)
		emittedBMF[dedupeKey] = struct{}{}
	}
	return out
}

// detachedBMFVolume holds the billing fields resolved for a detached-vault BMF row.
type detachedBMFVolume struct {
	accountName    string
	resourceType   metadata.ResourceType
	isSANProtocol  bool
	volumeMetadata metadata.ResourceMetadata
	sizeInBytes    int64
	backupMode     string
}

// resolveDetachedBMFVolume looks up a chain-tip volume UUID as either a regular or expert-mode
// volume. Regular volumes take precedence if the UUID appears in both maps. ok=false means skip.
func resolveDetachedBMFVolume(
	volumeUUID string,
	volumeDataMap map[string]*database.VolumeMetricsData,
	expertVolumeDataMap map[string]*database.ExpertModeVolumeMetricsData,
	poolMetadataMap map[int64]metadata.ResourceMetadata,
	config *common.TelemetryConfig,
) (detachedBMFVolume, bool) {
	if volume, exists := volumeDataMap[volumeUUID]; exists {
		if volume.UUID == "" || volume.Name == "" {
			return detachedBMFVolume{}, false
		}
		accountName := volume.GetAccountName()
		if accountName == "" {
			return detachedBMFVolume{}, false
		}
		resourceType := metadata.Volume
		if poolMeta, ok := poolMetadataMap[volume.PoolID]; ok && poolMeta.ResourceType == metadata.VolumePoolRegionalHA {
			resourceType = metadata.VolumeRegionalHA
		}
		return detachedBMFVolume{
			accountName:    accountName,
			resourceType:   resourceType,
			isSANProtocol:  volume.VolumeAttributes != nil && utils.IsSanProtocols(volume.VolumeAttributes.Protocols),
			volumeMetadata: assembleVolumeMetadata(volume, config),
			sizeInBytes:    volume.SizeInBytes,
			backupMode:     BackupModeDefault,
		}, true
	}

	expertVolume, exists := expertVolumeDataMap[volumeUUID]
	if !exists {
		return detachedBMFVolume{}, false
	}
	if expertVolume.UUID == "" || expertVolume.Name == "" {
		return detachedBMFVolume{}, false
	}
	if expertVolume.AccountName == "" {
		return detachedBMFVolume{}, false
	}
	resourceType := metadata.Volume
	if poolMeta, ok := poolMetadataMap[expertVolume.PoolID]; ok && poolMeta.ResourceType == metadata.VolumePoolRegionalHA {
		resourceType = metadata.VolumeRegionalHA
	}
	return detachedBMFVolume{
		accountName:    expertVolume.AccountName,
		resourceType:   resourceType,
		isSANProtocol:  utils.IsSanProtocols(expertVolume.GetProtocols()),
		volumeMetadata: assembleExpertModeVolumeMetadata(expertVolume, config),
		sizeInBytes:    expertVolume.SizeInBytes,
		backupMode:     BackupModeOntap,
	}, true
}

// assembleVolumeMetadata creates metadata from optimized VolumeMetricsData struct
func assembleVolumeMetadata(volume *database.VolumeMetricsData, config *common.TelemetryConfig) metadata.ResourceMetadata {
	met := metadata.ResourceMetadata{}
	met.SetResourceUUID(volume.UUID)
	met.SetResourceName(volume.Name)
	met.SetResourceDisplayName(volume.Name)
	met.SetResourceType(metadata.Volume)
	// Use the allocated size (size_in_bytes) for billing
	met.SetSizeInBytes(volume.SizeInBytes)
	met.SetRegionName(config.RegionName)
	// Get account name and deployment name from VolumeAttributes JSONB
	met.SetAccountName(volume.GetAccountName())
	met.SetDeploymentName(volume.GetDeploymentName())
	return met
}
