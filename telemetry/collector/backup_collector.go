package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	pageSize = env.GetInt64("PAGE_SIZE", 1000)
)

const EmptyDeploymentName = ""

// BackupMetricsResult holds the results from GetBackupMetrics operation
type BackupMetricsResult struct {
	// HydratedMetrics contains the traditional hydrated metrics
	HydratedMetrics []entity.HydratedMetric
	// HydratedMetricsDataModel contains the data model hydrated metrics
	HydratedMetricsDataModel []datamodel2.HydratedMetrics
}

// GetBackupMetrics retrieves backup logical size metrics from the database and returns them
// in a structured result.
//
// Behaviour depends on config.EnableGcbdrBackupBilling:
//   - false (default): one BackupLogicalSize row per volume (main behaviour); uses
//     vcpDB.GetBackupMetrics (GROUP BY volume_uuid) and bills to the volume owner.
//   - true: one row per (volume, vault) covering GCBDR vault-switched volumes; uses
//     vcpDB.GetBackupChainMetrics (per-chain) and bills cross-project vaults to the
//     vault's owning project.
func GetBackupMetrics(ctx context.Context, vcpDB database.Storage, config *common.TelemetryConfig, timestamp time.Time) (*BackupMetricsResult, error) {
	if config != nil && config.EnableGcbdrBackupBilling {
		return getBackupMetricsPerVault(ctx, vcpDB, config, timestamp)
	}
	return getBackupMetricsPerVolume(ctx, vcpDB, config, timestamp)
}

// getBackupMetricsPerVolume is the legacy (flag-off) emission path: one BackupLogicalSize row
// per volume, billed to the volume owner. Matches pre-GCBDR-multi-vault behaviour.
func getBackupMetricsPerVolume(ctx context.Context, vcpDB database.Storage, config *common.TelemetryConfig, timestamp time.Time) (*BackupMetricsResult, error) {
	logger := util.GetLogger(ctx)

	var allBackups []*datamodel.Backup
	offset := int64(0)
	limit := pageSize
	conditions := [][]interface{}{}

	for {
		pagination := &dbutils.Pagination{
			Offset: int(offset),
			Limit:  int(limit),
		}
		backups, err := vcpDB.GetBackupMetrics(ctx, conditions, pagination)
		if err != nil {
			logger.Error("Failed to get backup logical size metrics", "error", err.Error())
			return &BackupMetricsResult{}, err
		}
		if len(backups) == 0 {
			break
		}
		allBackups = append(allBackups, backups...)
		offset += limit
	}

	logger.Info(fmt.Sprintf("Found %d backup metrics", len(allBackups)))

	if len(allBackups) == 0 {
		return &BackupMetricsResult{}, nil
	}

	// Fetch account states so backups owned by a disabled account (HYPERSCALERDISABLED) can be skipped.
	accountStateMap := BuildAccountStateMap(ctx, vcpDB)

	var metrics []entity.HydratedMetric
	var hydratedMetrics []datamodel2.HydratedMetrics

	for _, backup := range allBackups {
		if backup.Attributes == nil {
			logger.Error(fmt.Sprintf("Backup attributes missing for volume %s", backup.VolumeUUID))
			continue
		}

		// In the per-volume path the BackupLogicalSize metric is attributed to the volume
		// owner (AccountIdentifier). Skip emission entirely when that account is disabled
		if IsAccountDisabled(accountStateMap, backup.Attributes.AccountIdentifier) {
			logger.Debug("Skipping BackupLogicalSize metric: owning account is disabled",
				"backupUUID", backup.UUID,
				"volumeUUID", backup.VolumeUUID,
				"account", backup.Attributes.AccountIdentifier,
				"accountState", datamodel.AccountStateHyperscalerDisabled)
			continue
		}

		backupMetadata := assembleBackupMetadata(backup, config)

		metric := setupHydratedMetric(timestamp, backupMetadata, metadata.BackupLogicalSize, float64(backup.LatestLogicalBackupSize))
		if shouldEmitBackupUsed(backup, config) {
			metrics = append(metrics, metric)
		} else {
			logger.Debug("Skipping backup_used metric for cross-project backup", "backupUUID", backup.UUID, "backupVaultID", backup.BackupVault.UUID)
		}

		skipBilling := false

		if !config.EnableCrossRegionBackupBillingMetrics {
			if backup.BackupVault != nil && backup.BackupVault.BackupVaultType == activities.CrossRegionBackupType {
				logger.Debug("Skipping BackupLogicalSize billing metric for cross-region backup", "backupUUID", backup.UUID)
				skipBilling = true
			}
		}

		if !skipBilling && config.EnableCrossRegionBackupBillingMetrics &&
			backup.BackupVault != nil &&
			backup.BackupVault.BackupVaultType == activities.CrossRegionBackupType {
			if backup.BackupVault.BackupRegionName == nil {
				logger.Warnf("Skipping BackupLogicalSize billing for cross-region backup %s (volume %s): BackupRegionName is nil", backup.UUID, backup.VolumeUUID)
				skipBilling = true
			} else if *backup.BackupVault.BackupRegionName == config.RegionName {
				logger.Warnf("Skipping BackupLogicalSize billing for cross-region backup %s (volume %s): BackupRegionName %s matches current region", backup.UUID, backup.VolumeUUID, *backup.BackupVault.BackupRegionName)
				skipBilling = true
			}
		}

		if !skipBilling && !config.EnableCmekBackupBilling {
			if backup.BackupVault != nil &&
				backup.BackupVault.CmekAttributes != nil &&
				backup.BackupVault.CmekAttributes.KmsConfigResourcePath != nil &&
				*backup.BackupVault.CmekAttributes.KmsConfigResourcePath != "" {
				logger.Debug("Skipping BackupLogicalSize billing metric for CMEK backup", "backupUUID", backup.UUID, "backupVaultID", backup.BackupVault.UUID)
				skipBilling = true
			}
		}

		if !skipBilling && !config.EnableGcbdrBackupBilling {
			if backup.BackupVault != nil && backup.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject {
				logger.Debug("Skipping BackupLogicalSize billing metric for cross-project backup", "backupUUID", backup.UUID, "backupVaultID", backup.BackupVault.UUID)
				skipBilling = true
			}
		}

		if !skipBilling && !config.EnableExpertModeBackupBilling && backup.Attributes != nil && backup.Attributes.IsExpertModeBackup {
			logger.Debug("Skipping BackupLogicalSize billing metric for expert mode backup", "backupUUID", backup.UUID)
			skipBilling = true
		}

		accountName := ""
		if backup.Attributes != nil {
			accountName = backup.Attributes.AccountIdentifier
		}

		// Determine backup mode: ONTAP for expert mode backups, DEFAULT for standard backups.
		backupMode := BackupModeDefault
		if backup.Attributes != nil && backup.Attributes.IsExpertModeBackup {
			backupMode = BackupModeOntap
		}

		isSANProtocol := utils.IsSanProtocols(backup.Attributes.Protocols)
		if !skipBilling && (config.EnableFilesBackupBilling || isSANProtocol) {
			if hydratedMetric := setupHydratedMetricsDataModel(metric.MeasuredType, metric.Metadata.ResourceType, accountName, backupMetadata, timestamp, float64(backup.LatestLogicalBackupSize)); hydratedMetric != nil {
				setBackupModeMetadata(hydratedMetric, backupMode)
				hydratedMetrics = append(hydratedMetrics, *hydratedMetric)
			}
			if config.EnableCrossRegionBackupBillingMetrics &&
				backup.BackupVault != nil &&
				backup.BackupVault.BackupVaultType == activities.CrossRegionBackupType &&
				backup.Attributes.GetTotalTransferBytes() > 0 {
				totalTransferBytes := float64(backup.Attributes.GetTotalTransferBytes())
				if hm := setupHydratedMetricsDataModel(
					metadata.CbsCrossRegionVolumeBackupTransferBytes,
					metadata.Backup,
					accountName,
					backupMetadata,
					timestamp,
					totalTransferBytes,
				); hm != nil {
					setCrossRegionRegionMetadata(logger, hm, backupMetadata)
					setBackupModeMetadata(hm, backupMode)
					hydratedMetrics = append(hydratedMetrics, *hm)
				}
			}
		}
	}

	return &BackupMetricsResult{
		HydratedMetrics:          metrics,
		HydratedMetricsDataModel: hydratedMetrics,
	}, nil
}

// getBackupMetricsPerVault is the flag-on emission path: one BackupLogicalSize row per
// (volume, vault) so GCBDR vault-switched volumes emit multiple rows — one per vault with
// available backups. Cross-project backups are billed to the vault's owning project.
func getBackupMetricsPerVault(ctx context.Context, vcpDB database.Storage, config *common.TelemetryConfig, timestamp time.Time) (*BackupMetricsResult, error) {
	logger := util.GetLogger(ctx)

	var allBackups []*datamodel.Backup
	offset := int64(0)
	limit := pageSize
	conditions := [][]interface{}{}

	for {
		pagination := &dbutils.Pagination{
			Offset: int(offset),
			Limit:  int(limit),
		}
		backups, err := vcpDB.GetBackupChainMetrics(ctx, conditions, pagination)
		if err != nil {
			logger.Error("Failed to get backup chain metrics", "error", err.Error())
			return &BackupMetricsResult{}, err
		}
		if len(backups) == 0 {
			break
		}
		allBackups = append(allBackups, backups...)
		offset += limit
	}

	logger.Info(fmt.Sprintf("Found %d backup chain tips", len(allBackups)))

	if len(allBackups) == 0 {
		return &BackupMetricsResult{}, nil
	}

	// Fetch account states so groups whose billing account is disabled (HYPERSCALERDISABLED) can be skipped.
	accountStateMap := BuildAccountStateMap(ctx, vcpDB)

	// Group chain tips by (VolumeUUID, BackupVault UUID) so that each vault attached to a
	// volume produces its own billing row. A GCBDR vault attached volumes may have backups across multiple
	// vaults (vault switching) or across multiple endpoints within the same vault
	// (detach/re-attach); the DB query returns one chain tip per
	// (volume_uuid, backup_vault_id, endpoint_uuid), and here we merge chain tips sharing
	// the same vault so each billing row represents a single vault's total backup size.
	type billingGroupKey struct {
		VolumeUUID string
		VaultUUID  string
	}
	type vaultChainGroup struct {
		chainTips []*datamodel.Backup
	}
	var orderedGroups []*vaultChainGroup
	groupIndex := make(map[billingGroupKey]int)
	for _, backup := range allBackups {
		if backup.Attributes == nil {
			logger.Error(fmt.Sprintf("Backup attributes is missing for volume %s", backup.VolumeUUID))
			continue
		}
		vaultUUID := ""
		if backup.BackupVault != nil {
			vaultUUID = backup.BackupVault.UUID
		}
		key := billingGroupKey{VolumeUUID: backup.VolumeUUID, VaultUUID: vaultUUID}
		idx, exists := groupIndex[key]
		if !exists {
			orderedGroups = append(orderedGroups, &vaultChainGroup{})
			idx = len(orderedGroups) - 1
			groupIndex[key] = idx
		}
		orderedGroups[idx].chainTips = append(orderedGroups[idx].chainTips, backup)
	}

	var metrics []entity.HydratedMetric
	var hydratedMetrics []datamodel2.HydratedMetrics

	for _, group := range orderedGroups {
		sort.Slice(group.chainTips, func(i, j int) bool {
			return group.chainTips[i].ID > group.chainTips[j].ID
		})
		canonicalBackup := group.chainTips[0]

		// Determine the project this BackupLogicalSize metric is attributed to: cross-project
		// vaults bill to the vault owner, all others bill to the volume owner. Skip emission
		// when that project is disabled (HYPERSCALERDISABLED)
		effectiveAccount := resolveBillingAccount(canonicalBackup)
		if IsAccountDisabled(accountStateMap, effectiveAccount) {
			logger.Debug("Skipping BackupLogicalSize metric: owning account is disabled",
				"backupUUID", canonicalBackup.UUID,
				"volumeUUID", canonicalBackup.VolumeUUID,
				"account", effectiveAccount,
				"accountState", datamodel.AccountStateHyperscalerDisabled)
			continue
		}

		var totalSize int64
		for _, b := range group.chainTips {
			totalSize += b.LatestLogicalBackupSize
		}

		backupMetadata := assembleBackupMetadata(canonicalBackup, config)
		backupMetadata.SetAccountName(effectiveAccount)

		metric := setupHydratedMetric(timestamp, backupMetadata, metadata.BackupLogicalSize, float64(totalSize))
		if shouldEmitBackupUsed(canonicalBackup, config) {
			metrics = append(metrics, metric)
		} else {
			logger.Debug("Skipping backup_used metric for cross-project backup", "backupUUID", canonicalBackup.UUID, "backupVaultID", canonicalBackup.BackupVault.UUID)
		}

		skipBilling := false

		if !config.EnableCrossRegionBackupBillingMetrics {
			if canonicalBackup.BackupVault != nil && canonicalBackup.BackupVault.BackupVaultType == activities.CrossRegionBackupType {
				logger.Debug("Skipping BackupLogicalSize billing metric for cross-region backup", "backupUUID", canonicalBackup.UUID)
				skipBilling = true
			}
		}

		if !skipBilling && config.EnableCrossRegionBackupBillingMetrics &&
			canonicalBackup.BackupVault != nil &&
			canonicalBackup.BackupVault.BackupVaultType == activities.CrossRegionBackupType {
			if canonicalBackup.BackupVault.BackupRegionName == nil {
				logger.Warnf("Skipping BackupLogicalSize billing for cross-region backup %s (volume %s): BackupRegionName is nil", canonicalBackup.UUID, canonicalBackup.VolumeUUID)
				skipBilling = true
			} else if *canonicalBackup.BackupVault.BackupRegionName == config.RegionName {
				logger.Warnf("Skipping BackupLogicalSize billing for cross-region backup %s (volume %s): BackupRegionName %s matches current region", canonicalBackup.UUID, canonicalBackup.VolumeUUID, *canonicalBackup.BackupVault.BackupRegionName)
				skipBilling = true
			}
		}

		if !skipBilling && !config.EnableCmekBackupBilling {
			if canonicalBackup.BackupVault != nil &&
				canonicalBackup.BackupVault.CmekAttributes != nil &&
				canonicalBackup.BackupVault.CmekAttributes.KmsConfigResourcePath != nil &&
				*canonicalBackup.BackupVault.CmekAttributes.KmsConfigResourcePath != "" {
				logger.Debug("Skipping BackupLogicalSize billing metric for CMEK backup", "backupUUID", canonicalBackup.UUID, "backupVaultID", canonicalBackup.BackupVault.UUID)
				skipBilling = true
			}
		}

		if !skipBilling && !config.EnableGcbdrBackupBilling {
			if canonicalBackup.BackupVault != nil && canonicalBackup.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject {
				logger.Debug("Skipping BackupLogicalSize billing metric for cross-project backup", "backupUUID", canonicalBackup.UUID, "backupVaultID", canonicalBackup.BackupVault.UUID)
				skipBilling = true
			}
		}

		// Skip billing for expert mode backups when the feature flag is disabled.
		if !skipBilling && !config.EnableExpertModeBackupBilling && canonicalBackup.Attributes != nil && canonicalBackup.Attributes.IsExpertModeBackup {
			logger.Debug("Skipping BackupLogicalSize billing metric for expert mode backup", "backupUUID", canonicalBackup.UUID)
			skipBilling = true
		}

		// Bill to the same account the metric is attributed to (see resolveBillingAccount).
		accountName := effectiveAccount

		// Determine backup mode: ONTAP for expert mode backups, DEFAULT for standard backups.
		backupMode := BackupModeDefault
		if canonicalBackup.Attributes != nil && canonicalBackup.Attributes.IsExpertModeBackup {
			backupMode = BackupModeOntap
		}

		isSANProtocol := utils.IsSanProtocols(canonicalBackup.Attributes.Protocols)
		if !skipBilling && (config.EnableFilesBackupBilling || isSANProtocol) {
			if hydratedMetric := setupHydratedMetricsDataModel(metric.MeasuredType, metric.Metadata.ResourceType, accountName, backupMetadata, timestamp, float64(totalSize)); hydratedMetric != nil {
				setBackupModeMetadata(hydratedMetric, backupMode)
				hydratedMetrics = append(hydratedMetrics, *hydratedMetric)
			}
			for _, b := range group.chainTips {
				if config.EnableCrossRegionBackupBillingMetrics &&
					b.BackupVault != nil &&
					b.BackupVault.BackupVaultType == activities.CrossRegionBackupType &&
					b.Attributes.GetTotalTransferBytes() > 0 {
					chainMetadata := assembleBackupMetadata(b, config)
					if hm := setupHydratedMetricsDataModel(
						metadata.CbsCrossRegionVolumeBackupTransferBytes,
						metadata.Backup,
						b.Attributes.AccountIdentifier,
						chainMetadata,
						timestamp,
						float64(b.Attributes.GetTotalTransferBytes()),
					); hm != nil {
						setCrossRegionRegionMetadata(logger, hm, chainMetadata)
						setBackupModeMetadata(hm, backupMode)
						hydratedMetrics = append(hydratedMetrics, *hm)
					}
				}
			}
		}
	}

	return &BackupMetricsResult{
		HydratedMetrics:          metrics,
		HydratedMetricsDataModel: hydratedMetrics,
	}, nil
}

// IsAccountDisabled reports whether accountName resolves to an account in the
// HYPERSCALERDISABLED state within accountStateMap. Empty or unknown account names are
// treated as enabled so that emission behaviour is unchanged whenever an account state
// cannot be resolved (e.g. the account listing failed and the map is empty).
func IsAccountDisabled(accountStateMap map[string]string, accountName string) bool {
	if accountName == "" {
		return false
	}
	state, ok := accountStateMap[accountName]
	return ok && state == datamodel.AccountStateHyperscalerDisabled
}

// shouldEmitBackupUsed reports whether the backup_used observation metric should be emitted
// for this backup. Cross-project (GCBDR) vault backups are suppressed unless
// EnableCrossProjectBackupUsedMetric is set.
func shouldEmitBackupUsed(backup *datamodel.Backup, config *common.TelemetryConfig) bool {
	isCrossProjectVault := backup.BackupVault != nil && backup.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject
	if !isCrossProjectVault {
		return true
	}
	return config != nil && config.EnableCrossProjectBackupUsedMetric
}

// resolveBillingAccount returns the account we bill a backup to. For cross-project vaults
// that's the vault owner; for everything else it's the volume owner (AccountIdentifier).
// This lives in one place so the skip check, metadata, and billing all agree.
func resolveBillingAccount(b *datamodel.Backup) string {
	if b.BackupVault != nil && b.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject &&
		b.BackupVault.Account != nil && b.BackupVault.Account.Name != "" {
		return b.BackupVault.Account.Name
	}
	return b.Attributes.AccountIdentifier
}

func assembleBackupMetadata(backup *datamodel.Backup, config *common.TelemetryConfig) metadata.ResourceMetadata {
	met := metadata.ResourceMetadata{}
	met.SetResourceUUID(backup.VolumeUUID)
	met.SetResourceType(metadata.Backup)
	met.SetSizeInBytes(backup.LatestLogicalBackupSize)
	met.SetRegionName(config.RegionName)
	met.SetResourceName(backup.VolumeUUID)
	met.SetResourceDisplayName(backup.Attributes.VolumeName)
	met.SetAccountName(backup.Attributes.AccountIdentifier)

	// Check if BackupVault is not nil before accessing its Name
	if backup.BackupVault != nil {
		met.SetDeploymentName(backup.BackupVault.Name)
		if backup.BackupVault.BackupRegionName != nil && *backup.BackupVault.BackupRegionName != "" &&
			backup.BackupVault.BackupVaultType == activities.CrossRegionBackupType {
			met.SetBackupRegionName(*backup.BackupVault.BackupRegionName)
		}
	} else {
		met.SetDeploymentName(EmptyDeploymentName)
	}
	return met
}

const (
	// BackupModeOntap is the mode value sent to Google for expert mode (ONTAP) backups.
	BackupModeOntap = "ONTAP"
	// BackupModeDefault is the mode value sent to Google for standard backups.
	BackupModeDefault = "DEFAULT"

	metadataKeyBackupMode       = "backup_mode"
	metadataKeyBackupRegionName = "backup_region_name"
)

// setCrossRegionRegionMetadata stores BackupRegionName into the
// HydratedMetrics.Metadata JSONB column so the aggregator can set the
// destination region on the AggregatedUsage record.
// Any fields already in hm.Metadata are preserved (merge, not overwrite).
func setCrossRegionRegionMetadata(logger log.Logger, hm *datamodel2.HydratedMetrics, rm metadata.ResourceMetadata) {
	if hm == nil || rm.BackupRegionName == nil {
		return
	}
	extra := mergeMetadata(hm)
	extra[metadataKeyBackupRegionName] = *rm.BackupRegionName
	if b, err := json.Marshal(extra); err == nil {
		hm.Metadata = b
	} else {
		logger.Warnf("Failed to marshal cross-region metadata: %v", err)
	}
}

// setBackupModeMetadata stores the backup mode (ONTAP or DEFAULT) into the
// HydratedMetrics.Metadata JSONB column so the aggregator can forward it as the
// /backups/mode label to Google. Any existing metadata fields are preserved.
func setBackupModeMetadata(hm *datamodel2.HydratedMetrics, mode string) {
	if hm == nil {
		return
	}
	extra := mergeMetadata(hm)
	extra[metadataKeyBackupMode] = mode
	if b, err := json.Marshal(extra); err == nil {
		hm.Metadata = b
	}
}

// mergeMetadata returns a map pre-populated with any existing JSONB entries in hm.Metadata.
func mergeMetadata(hm *datamodel2.HydratedMetrics) map[string]string {
	extra := make(map[string]string)
	if len(hm.Metadata) > 0 {
		_ = json.Unmarshal(hm.Metadata, &extra)
	}
	return extra
}
