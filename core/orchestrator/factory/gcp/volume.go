package gcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_policy"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factorycommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/mqos"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/flexcache_workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/replicationWorkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbUtils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

var (
	minCVSizeInBytes                 = env.GetUint64("MIN_CONSTITUENT_VOLUME_SIZE_BYTES", 100*bytesPerGB)
	maxCVSizeInBytes                 = env.GetUint64("MAX_CONSTITUENT_VOLUME_SIZE_BYTES", 300*utils.TiBInBytes)
	numOfLvHAPairs                   = env.GetInt64("NUMBER_OF_HA_PAIRS_LARGE_CAPACITY", 6)
	defaultConstituentsPerAggregate  = env.GetInt64("DEFAULT_CONSTITUENTS_PER_AGGREGATE", 8)
	volumeRefreshIntervalMinutes     = env.GetInt("VOLUME_REFRESH_INTERVAL_MINUTES", 5)
	volumeRefreshBatchSize           = env.GetInt("VOLUME_REFRESH_BATCH_SIZE", 50)
	maxThinClonesPerPool             = env.GetInt64("MAX_THIN_CLONES_PER_POOL", 100)
	thinCloneGASupport               = env.GetBool("THIN_CLONE_GA_SUPPORT", false)
	validateCloneATPolicyMatchParent = env.GetBool("VALIDATE_CLONE_AT_POLICY_MATCH_PARENT", false)
	minQuotaInBytesVolume            = utils.MinQuotaInBytesVolumeForVolume
	maxQuotaInBytesVolume            = utils.MaxQuotaInBytesVolumeForVolume
	createVolume                     = _createVolume
	revertVolume                     = _revertVolume
	splitStartVolume                 = _splitStartVolume
	splitStopVolume                  = _splitStopVolume
	updateCloneState                 = _updateCloneState
	validateCreateVolumeParams       = _validateCreateVolumeParams
	validateVolumeQosParams          = mqos.ValidateVolumeQosParams
	validateSplitStartVolumeParams   = _validateSplitStartVolumeParams
	validateSplitStopVolumeParams    = _validateSplitStopVolumeParams
	getIPAddressForVolume            = _getIPAddressForVolume
	batchGetIPAddressesForVolumes    = _batchGetIPAddressesForVolumes
	getVolumeByName                  = _getVolumeByName
	updateVolume                     = _updateVolume
	deleteVolume                     = _deleteVolume
	validateDeleteVolumeParams       = _validateDeleteVolumeParams
	updateVolumeStatus               = _updateVolumeStatus
	convertDatastoreVolumeToModel    = _convertDatastoreVolumeToModel
	minPrimeNumberConfigAllowed      = 7

	envIsLocalEnv                              = env.IsLocalEnv
	cvpCreateClient                            = cvp.CreateClient
	GetBackupVaultFromCVP                      = getBackupVaultFromCVP
	enableAutoPoolScaling                      = env.GetBool("ENABLE_AUTO_POOL_SCALING", true)
	autoPoolScalingLimits                      = env.GetString("AUTO_POOL_SCALING_LIMITS", "{\"c3-standard-4-lssd\":{\"min_volume_count\":0,\"max_volume_count\":245},\"c3-standard-8-lssd\":{\"min_volume_count\":0,\"max_volume_count\":495},\"c3-standard-22-lssd\":{\"min_volume_count\":0,\"max_volume_count\":995}}")
	maxConstituentVolumesPerVolumePerAggregate = env.GetInt64("MAX_CONSTITUENT_VOLUMES_PER_VOLUME_PER_AGGREGATE", 200)
	checkIsValidImmutableBackupPolicyWithRetry = _checkIsValidImmutableBackupPolicyWithRetry
	enableVolDeleteProtection                  = env.GetBool("VOL_DEL_PROTECTION_ENABLED", true)
	enableMqos                                 = env.GetBool("ENABLE_MQOS", true)
	enableInferredIops                         = env.GetBool("ENABLE_INFERRED_IOPS", false)
	enableVolumePerformanceGroupAssignment     = env.GetBool("ENABLE_VOLUME_PERFORMANCE_GROUP_ASSIGNMENT", false)
	splitWaitForTemporalEnabled                = env.GetBool("SPLIT_WAIT_FOR_TEMPORAL_ENABLED", false)
	// sequenceResilienceEnabled gates the duplicate active-job guard that backs the
	// delete retry path. Kept on the same flag as the sequencer resilience changes.
	sequenceResilienceEnabled = env.GetBool("SEQUENCE_WORKFLOW_RESILIENCE_ENABLED", true)
	// enableTrueZonalLVDefaultConstituentCount doubles the default CV count for true-zonal (active-active)
	// pools (6 HA pairs × 8 CVs × 2 = 96).
	enableTrueZonalLVDefaultConstituentCount = env.GetBool("ENABLE_TRUE_ZONAL_LV_DEFAULT_CONSTITUENT_COUNT", false)

	// WAIT_FOR_TEMPORAL background retry knobs. Vars (not consts) so unit tests can
	// shorten the exponential backoff without changing production defaults.
	waitForTemporalUpdateMaxRetries = 5
	waitForTemporalUpdateInitDelay  = 1 * time.Second
	waitForTemporalUpdateMaxDelay   = 16 * time.Second
	waitForTemporalBgTimeout        = 45 * time.Second
)

const (
	minCoolingThresholdDays   = 2
	maxCoolingThresholdDays   = 183
	MaxBackupPathComponents   = 8          // The expected number of components in the backup path
	BackupNameIndex           = 7          // The index of the backup name in the components
	BackupVaultNameIndex      = 5          // The index of the backup vault name in the components
	LocationIdIndex           = 3          // The index of the locationId in the backup path
	bytesPerGB                = 1073741824 // 1024^3 bytes = 1 GB
	ErrMsgSnapReserveIncrease = "Cannot increase SnapReserve to %.0f%% as we cannot decrease the available space (%.2f GB). " +
		"Please increase the volume size to at least %.0f GB with this SnapReserve or reduce the SnapReserve percentage to continue."
	NtfsSecurityStyle = "ntfs" // lower-case for persistence and ONTAP REST (enum: ntfs/unix)
)

// securityStyleForAPIResponse maps stored lower-case security style (ONTAP convention) to GCNV Swagger enum casing for API responses.
func securityStyleForAPIResponse(stored string) string {
	switch strings.ToLower(stored) {
	case "ntfs":
		return "NTFS"
	case "unix":
		return "UNIX"
	default:
		return stored
	}
}

// calculateIopsFromThroughput calculates IOPS based on the formula: iops = floor(total_iops * throughput / total_throughput)
// This is used when only throughput is provided and IOPS needs to be calculated.
func calculateIopsFromThroughput(throughputMibps int64, totalThroughputMibps int64, totalIops int64) int64 {
	if totalThroughputMibps == 0 {
		return 0
	}
	ratio := float64(throughputMibps) / float64(totalThroughputMibps)
	return int64(math.Floor(float64(totalIops) * ratio))
}

// validatePoolCapacityForVPGVolumeCreate validates pool QoS capacity when assigning a volume
// to an existing VPG. Shared VPGs with existing volumes need no check (already counted).
func validatePoolCapacityForVPGVolumeCreate(ctx context.Context, se database.Storage, poolUUID string, vpgUUID string) error {
	vpg, err := se.GetVolumePerformanceGroupByUUID(ctx, vpgUUID)
	if err != nil {
		return err
	}

	if vpg.IsShared() {
		volumeCount, err := se.GetVolumeCountByVolumePerformanceGroupID(ctx, vpg.ID)
		if err != nil {
			return err
		}
		if volumeCount > 0 {
			return nil
		}
	}

	return mqos.ValidatePoolCapacityForVolume(ctx, se, poolUUID, &vpg.ThroughputMibps, &vpg.Iops, nil)
}

// CreateVolume creates the specified volume and adds it to the list of volume belonging to the specified owner
func (o *GCPOrchestrator) CreateVolume(ctx context.Context, params *common.CreateVolumeParams) (*models.Volume, string, error) {
	return createVolume(ctx, o.storage, o.temporal, params)
}

func _createVolume(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateVolumeParams) (*models.Volume, string, error) {
	logger := util.GetLogger(ctx)
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	pool, err := se.GetPool(ctx, params.PoolID, account.ID)
	if err != nil {
		return nil, "", err
	}
	params.PoolDBID = pool.ID

	if pool.APIAccessMode == common.ONTAPMode {
		return nil, "", customerrors.NewUserInputValidationErr("Cannot create Volumes in ONTAP mode pool using GCNV API")
	}

	if pool.PoolAttributes == nil {
		return nil, "", customerrors.NewUserInputValidationErr("Pool attributes are required")
	}

	if pool.PoolAttributes == nil {
		return nil, "", customerrors.NewUserInputValidationErr("Pool attributes are required")
	}

	poolPrimaryZone := pool.PoolAttributes.PrimaryZone
	isRegionalPool := pool.PoolAttributes.IsRegionalHA
	// Validate that volume zone matches pool's primary zone for zonal volume
	if !isRegionalPool && params.Zone != poolPrimaryZone {
		return nil, "", customerrors.NewConflictErr(fmt.Sprintf("Volume zone '%s' does not match pool's primary zone '%s'.", params.Zone, poolPrimaryZone))
	}

	// Check for existing volume with same name in the determined zone
	vol, volErr := se.GetVolumeByNameAccountIDAndZone(ctx, params.Name, pool.Account.ID, params.Zone, isRegionalPool)
	if volErr != nil {
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(volErr, &customErr) && !customerrors.IsNotFoundErr(customErr.Unwrap()) {
			// propagate the Non-NotFound errors
			return nil, "", volErr
		}
		logger.Debug("No existing volume found with the given name in the same zone, proceeding to create a new volume",
			"volume_name", params.Name, "zone", params.Zone)
	} else {
		if vol.State != datamodel.LifeCycleStateCreating {
			// Provide appropriate error message based on pool type
			var errorMsg string
			if isRegionalPool {
				errorMsg = fmt.Sprintf("Volume with resource_id '%s' already exists in region '%s'", params.Name, params.Region)
			} else {
				errorMsg = fmt.Sprintf("Volume with resource_id '%s' already exists in zone '%s'", params.Name, params.Zone)
			}
			return nil, "", customerrors.NewConflictErr(errorMsg)
		} else {
			// If volume is in CREATING state, check if it belongs to the same pool
			if vol.Pool.UUID != pool.UUID {
				return nil, "", customerrors.NewConflictErr(fmt.Sprintf("Volume with resource_id '%s' already exists in the '%s' pool, which is different from the requested pool '%s'", params.Name, vol.Pool.Name, pool.Name))
			}

			// Determine the correct job type based on whether it's a large capacity volume
			jobType := string(datamodel.JobTypeCreateVolume)
			if params.LargeCapacity {
				jobType = string(datamodel.JobTypeCreateLargeVolume)
			}

			job, jobErr := se.GetJobByResourceUUID(ctx, vol.UUID, jobType)
			if jobErr != nil {
				logger.Error("Failed to fetch existing create volume job for volume in CREATING state", "error", jobErr)
				return convertDatastoreVolumeToModel(vol, nil), "", nil
			}
			return convertDatastoreVolumeToModel(vol, nil), job.UUID, nil
		}
	}

	if hp := params.HybridReplicationParameters; hp != nil {
		// check for duplicate jobs
		existingJob, err := se.CheckAndFetchDuplicateJobs(ctx, string(datamodel.JobTypeCreateHybridReplication), utils.GetCoRelationIDFromContext(ctx))
		if err != nil {
			return nil, "", err
		}
		if existingJob != nil {
			vol, err := se.GetVolume(ctx, existingJob.JobAttributes.ResourceUUID)
			if err != nil {
				logger.Error("Failed to get volume from database", "error", err)
				return nil, "", err
			}
			return convertDatastoreVolumeToModel(vol, nil), existingJob.UUID, nil
		}
	}

	err = validateCreateVolumeParams(ctx, se, params, pool)
	if err != nil {
		return nil, "", err
	}

	svm, err := se.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		return nil, "", err
	}

	clonesSharedBytes := uint64(0)
	parentVolumeUUID := ""
	if params.SnapshotID != "" {
		dbSnapshot, err := se.GetSnapshotByPoolID(ctx, params.SnapshotID, account.ID, pool.ID, true)
		if err != nil {
			logger.Error("Failed to fetch parent snapshot for volume creation. Please use the correct snapshot and retry again.", "error", err)
			return nil, "", err
		}
		// block volume creation from snapshot if snapshot is created for CRR or backup, or clone volume
		if dbSnapshot != nil {
			// Block if snapshot type is backup
			if dbSnapshot.Type == activities.SnapshotTypeBackup {
				logger.Error("Snapshot created for backup is not eligible for volume creation", "snapshot_id", dbSnapshot.UUID)
				return nil, "", customerrors.NewUserInputValidationErr("Snapshot is not eligible for volume creation. Snapshots created for backup, data protection, replication, or clone volumes are not supported.")
			}
			// Block if underlying volume itself is a clone (shares bytes with parent)
			if !thinCloneGASupport && dbSnapshot.Volume != nil && dbSnapshot.Volume.ClonesSharedBytes > 0 {
				logger.Error("Snapshot from a clone volume is not eligible for volume creation", "snapshot_id", dbSnapshot.UUID, "volume_id", dbSnapshot.Volume.UUID, "clones_shared_bytes", dbSnapshot.Volume.ClonesSharedBytes)
				return nil, "", customerrors.NewUserInputValidationErr("Snapshot is not eligible for volume creation. Snapshots created for backup, data protection, replication, or clone volumes are not supported.")
			}
			// Block if snapshot name has snapmirror prefix (CRR replication snapshot)
			if strings.HasPrefix(dbSnapshot.Name, "snapmirror.") {
				logger.Error("Replication (snapmirror) snapshot is not eligible for volume creation", "snapshot_id", dbSnapshot.UUID, "snapshot_name", dbSnapshot.Name)
				return nil, "", customerrors.NewUserInputValidationErr("Snapshot is not eligible for volume creation. Snapshots created for backup, data protection, replication, or clone volumes are not supported.")
			}
			// Block if parent volume is in DELETING state
			if dbSnapshot.Volume != nil && dbSnapshot.Volume.State == datamodel.LifeCycleStateDeleting {
				logger.Error("Parent volume is in deleting state and cannot be used for volume creation", "snapshot_id", dbSnapshot.UUID, "volume_id", dbSnapshot.Volume.UUID, "volume_state", dbSnapshot.Volume.State)
				return nil, "", customerrors.NewUserInputValidationErr("Parent volume is in deleting state and cannot be used for volume creation")
			}
		}

		if params.Protocols != nil && dbSnapshot != nil && dbSnapshot.Volume != nil && dbSnapshot.Volume.VolumeAttributes != nil && dbSnapshot.Volume.VolumeAttributes.Protocols != nil {
			if (utils.IsSanProtocols(params.Protocols) && utils.IsNasProtocols(dbSnapshot.Volume.VolumeAttributes.Protocols)) || (utils.IsNasProtocols(params.Protocols) && utils.IsSanProtocols(dbSnapshot.Volume.VolumeAttributes.Protocols)) {
				logger.Error("Snapshot volume protocol type does not match requested volume protocol type", "snapshot_protocols", dbSnapshot.Volume.VolumeAttributes.Protocols, "requested_protocols", params.Protocols)
				return nil, "", customerrors.NewUserInputValidationErr("Snapshot volume protocol type does not match requested volume protocol type. Please use the correct snapshot and retry again.")
			}
		}
		if dbSnapshot != nil && dbSnapshot.State != datamodel.LifeCycleStateREADY {
			logger.Error("Parent snapshot is not in a valid state for volume creation", "snapshot_state", dbSnapshot.State)
			return nil, "", customerrors.NewUserInputValidationErr("Parent snapshot is not in a valid state for volume creation. Please wait for the snapshot to be ready and retry again.")
		}

		if dbSnapshot != nil && dbSnapshot.Volume != nil {
			parentVolumeUUID = dbSnapshot.Volume.UUID
		}

		if dbSnapshot != nil && dbSnapshot.Volume != nil && dbSnapshot.Volume.LargeVolumeAttributes != nil && dbSnapshot.Volume.LargeVolumeAttributes.LargeVolumeConstituentCount != nil {
			params.LargeVolumeConstituentCount = *dbSnapshot.Volume.LargeVolumeAttributes.LargeVolumeConstituentCount
		}
		params.Snapshot = dbSnapshot
		if dbSnapshot != nil && dbSnapshot.SnapshotAttributes != nil {
			clonesSharedBytes = uint64(dbSnapshot.SnapshotAttributes.LogicalSizeUsedInBytes)
		}

		// Validate clone AT policy matches parent volume AT policy if flag is enabled
		if validateCloneATPolicyMatchParent && dbSnapshot != nil && dbSnapshot.Volume != nil {
			parentVolume := dbSnapshot.Volume
			if err := validateCloneATPolicyMatchParentVolume(parentVolume, params.AutoTieringPolicy); err != nil {
				logger.Error("Clone volume auto tiering policy does not match parent volume", "parent_volume_uuid", parentVolume.UUID, "error", err)
				return nil, "", err
			}
		}
	}
	dbPool := database.ConvertPoolViewToPool(pool)
	ldapEnabled := false
	if dbPool != nil && dbPool.PoolAttributes != nil {
		ldapEnabled = dbPool.PoolAttributes.LdapEnabled
	}
	volumeObj := &datamodel.Volume{
		Name:        params.Name,
		Account:     account,
		AccountID:   account.ID,
		SizeInBytes: int64(params.QuotaInBytes),
		Description: params.Description,
		PoolID:      pool.ID,
		SvmID:       svm.ID,
		Pool:        dbPool,
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken:     params.CreationToken,
			Protocols:         params.Protocols,
			VendorSubnetID:    params.Network,
			IsDataProtection:  params.IsDataProtection,
			SnapReserve:       params.SnapReserve,
			SnapshotDirectory: params.SnapshotDirectory,
			KerberosEnabled:   params.KerberosEnabled,
			LdapEnabled:       ldapEnabled,
			Labels:            params.Labels,
			AccountName:       getAccountName(account),
			DeploymentName:    getPoolDeploymentName(dbPool),
			IsRegionalHA:      getPoolIsRegionalHA(dbPool),
			RestrictedActions: params.RestrictedActions,
		},
		ClonesSharedBytes: clonesSharedBytes,
	}

	if utils.IsSanProtocols(params.Protocols) {
		volumeObj.VolumeAttributes.SnapshotDirectory = false
	}

	// Check BlockDevices first, then fallback to BlockProperties
	if params.BlockDevices != nil && len(*params.BlockDevices) > 0 {
		// Process BlockDevices as primary source
		blockDevices := make([]datamodel.BlockDevice, 0, len(*params.BlockDevices))
		for _, blockDeviceReq := range *params.BlockDevices {
			blockDevice := datamodel.BlockDevice{
				Name:   blockDeviceReq.Name,
				OSType: blockDeviceReq.OSType,
			}
			if len(blockDeviceReq.HostGroups) > 0 {
				hgs, err := getMultipleHostGroup(ctx, se, blockDeviceReq.HostGroups, account.Name)
				if err != nil {
					return nil, "", err
				}
				for _, hg := range hgs {
					blockDevice.HostGroupDetails = append(blockDevice.HostGroupDetails, datamodel.HostGroupDetail{
						HostGroupUUID: hg.UUID,
						HostQNs:       hg.Hosts,
					})
				}
			}
			blockDevices = append(blockDevices, blockDevice)
		}
		volumeObj.VolumeAttributes.BlockDevices = &blockDevices
	} else if params.BlockProperties != nil {
		// Fallback: Process BlockProperties if BlockDevices are not provided
		volumeObj.VolumeAttributes.BlockProperties = &datamodel.BlockProperties{
			OSType: params.BlockProperties.OSType,
		}
		hgs, err := getMultipleHostGroup(ctx, se, params.BlockProperties.HostGroupUUIDs, account.Name)
		if err != nil {
			return nil, "", err
		}
		for _, hg := range hgs {
			volumeObj.VolumeAttributes.BlockProperties.HostGroupDetails = append(
				volumeObj.VolumeAttributes.BlockProperties.HostGroupDetails, datamodel.HostGroupDetail{
					HostGroupUUID: hg.UUID,
					HostQNs:       hg.Hosts,
				})
		}
	}

	if params.FileProperties != nil {
		volumeObj.VolumeAttributes.FileProperties = factorycommon.BuildFilePropertiesFromParams(params.FileProperties, params.CreationToken)
	}

	if params.SnapshotID != "" {
		volumeObj.VolumeAttributes.CloneParentInfo = &datamodel.CloneParentInfo{
			ParentSnapshotUUID: params.SnapshotID,
			ParentVolumeUUID:   parentVolumeUUID,
			State:              datamodel.CloneStateCloned,
		}
		// Capture the at-creation shared-bytes baseline. splitStart later zeroes
		// volumes.clones_shared_bytes to reserve pool capacity, so without this
		// snapshot the synchronous splitStop API cannot report a meaningful
		// remaining-shared-bytes value. See VolumeAttributes.OriginalSharedBytes.
		originalSharedBytes := clonesSharedBytes
		volumeObj.VolumeAttributes.OriginalSharedBytes = &originalSharedBytes
	}

	if params.DataProtection != nil {
		volumeObj.DataProtection = &datamodel.DataProtection{
			BackupVaultID:          params.DataProtection.BackupVaultID,
			BackupPolicyID:         params.DataProtection.BackupPolicyId,
			BackupChainBytes:       params.DataProtection.BackupChainBytes,
			ScheduledBackupEnabled: params.DataProtection.ScheduledBackupEnabled,
			KmsGrant:               params.DataProtection.KmsGrant,
		}
	}

	if params.SnapshotPolicy != nil {
		volumeObj.SnapshotPolicy = &datamodel.SnapshotPolicy{
			Name:      volumeObj.Name,
			IsEnabled: params.SnapshotPolicy.IsEnabled,
			Schedules: factorycommon.ConvertToDBSnapshotPolicySchedule(params.SnapshotPolicy.Schedules),
		}
	}

	if params.AutoTieringPolicy != nil {
		volumeObj.AutoTieringEnabled = params.AutoTieringPolicy.AutoTieringEnabled
		volumeObj.AutoTieringPolicy = &datamodel.AutoTieringPolicy{
			TieringPolicy:            params.AutoTieringPolicy.TieringPolicy,
			CoolingThresholdDays:     params.AutoTieringPolicy.CoolingThresholdDays,
			RetrievalPolicy:          params.AutoTieringPolicy.RetrievalPolicy,
			HotTierBypassModeEnabled: params.AutoTieringPolicy.HotTierBypassModeEnabled,
			CloudWriteModeEnabled:    params.AutoTieringPolicy.CloudWriteModeEnabled,
		}
	}

	// Handle backup restore path - validation only, actual backup fetching is done in workflow

	if params.BackupPath != "" {
		if volumeObj.VolumeAttributes == nil {
			volumeObj.VolumeAttributes = &datamodel.VolumeAttributes{
				AccountName:    getAccountName(account),
				DeploymentName: getPoolDeploymentName(dbPool),
				IsRegionalHA:   getPoolIsRegionalHA(dbPool),
			}
		}
		logger.Debugf("params.BackupPath: %s", params.BackupPath)
		volumeObj.VolumeAttributes.RestoredBackupPath = params.BackupPath
		components := strings.Split(params.BackupPath, "/")

		// Ensure there are enough components to avoid out of range errors
		if len(components) < MaxBackupPathComponents {
			return nil, "", customerrors.NewUserInputValidationErr("Backup path is not in correct format")
		}
		// Note: Backup vault/backup fetching, size validation, and large volume compatibility
		// are all handled by FetchBackupMetadataForRestore activity in the workflow
		logger.Infof("Backup path validated, backup metadata will be fetched in workflow")
	}

	if params.LargeCapacity {
		volumeObj.LargeVolumeAttributes = &datamodel.LargeVolumeAttributes{
			LargeCapacity: true,
		}
		if params.LargeVolumeConstituentCount > 0 {
			volumeObj.LargeVolumeAttributes.LargeVolumeConstituentCount = nillable.GetInt32Ptr(params.LargeVolumeConstituentCount)
		} else {
			// Set default constituent count: (HA pairs for this account × CVs per aggregate), doubled for true-zonal pools when ENABLE_TRUE_ZONAL_LV_DEFAULT_CONSTITUENT_COUNT is enabled.
			if params.BackupPath == "" {
				defaultConstituentCount := int32(defaultLargeVolumeConstituentCount(account.Name, pool.IsActiveActive()))
				volumeObj.LargeVolumeAttributes.LargeVolumeConstituentCount = &defaultConstituentCount
			}
		}
	}

	// Handle QoS parameters: create/find VPG or validate existing VPG (only if MQOS is enabled)
	var vpgID *int64
	if enableMqos && params.ThroughputMibps != nil {
		throughputValue := *params.ThroughputMibps
		var iopsValue int64

		if enableInferredIops {
			// Use proportional calculation based on pool totals (not IopsPerMiBps)
			if pool == nil || pool.PoolAttributes == nil {
				return nil, "", customerrors.NewUserInputValidationErr("pool throughput totals are required for inferred IOPS calculation")
			}
			iopsValue = calculateIopsFromThroughput(throughputValue, pool.PoolAttributes.ThroughputMibps, pool.PoolAttributes.Iops)
		} else {
			// Inferred IOPS is disabled - require IOPS to be provided explicitly
			if params.Iops == nil {
				return nil, "", customerrors.NewUserInputValidationErr("IOPS inference is disabled. IOPS must be provided explicitly when throughputMibps is specified.")
			}
			iopsValue = *params.Iops
		}

		// Pass VPG details to workflow - it will create VPG in DB and QoS policy in ONTAP
		params.Iops = &iopsValue
		logger.Info("VPG will be auto-generated in workflow", "throughput_mibps", throughputValue, "iops", iopsValue)
	} else if params.VolumePerformanceGroupID != nil {
		// Validate that the VPG exists and belongs to the pool
		vpgUUID := *params.VolumePerformanceGroupID
		vpg, err := se.GetVolumePerformanceGroupByUUID(ctx, vpgUUID)
		if err != nil {
			if customerrors.IsNotFoundErr(err) {
				return nil, "", customerrors.NewUserInputValidationErr(fmt.Sprintf("Volume performance group '%s' not found", vpgUUID))
			}
			return nil, "", err
		}

		// Verify VPG belongs to the pool
		if vpg.PoolID != pool.ID {
			return nil, "", customerrors.NewUserInputValidationErr(fmt.Sprintf("Volume performance group '%s' does not belong to the specified pool", vpgUUID))
		}
		vpgID = &vpg.ID
		logger.Info("Using existing VPG for volume", "vpg_id", vpgUUID)
	}

	// Assign VPG to volume if one was created/found (for existing VPGs only)
	// Note: For auto-generated VPGs, the VPG ID will be set in the workflow after VPG creation
	if vpgID != nil {
		volumeObj.VolumePerformanceGroupID = sql.NullInt64{Int64: *vpgID, Valid: true}
	}

	// Generate UUID for the volume (needed for job creation)
	volumeUUID := uuid.New().String()
	volumeObj.UUID = volumeUUID

	dbVolume, err := se.CreateVolume(ctx, volumeObj)
	if err != nil {
		return nil, "", err
	}

	defer func() {
		if err != nil {
			// Mark volume in deleted state
			_, volumeDeleteErr := se.DeleteVolume(ctx, dbVolume.UUID)
			if volumeDeleteErr != nil {
				logger.Error("Failed to delete volume", "volume_id", dbVolume.UUID, "error", volumeDeleteErr)
			}
		}
	}()

	location, err := utils.GetLocationFromVendorID(dbVolume.Pool.VendorID)
	if err != nil {
		logger.Error("Failed to get location from vendor ID: ", "error", err)
		return nil, "", err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateVolume),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbVolume.UUID,
			PoolUUID:     pool.UUID,
		},
	}

	if params.LargeCapacity {
		job.Type = string(datamodel.JobTypeCreateLargeVolume)
	}

	var wf interface{} = workflows.CreateVolumeWorkflow
	wfArgs := []interface{}{params, dbVolume.UUID}

	if params.HybridReplicationParameters != nil {
		job.Type = string(datamodel.JobTypeCreateHybridReplication)
		job.ResourceName = fmt.Sprintf("projects/%s/locations/%s/volumes/%s/replications/%s",
			params.AccountName,
			location,
			params.Name,
			params.HybridReplicationParameters.ResourceID)
		wf = replicationWorkflows.CreateHybridReplicationWorkflow
		wfArgs = []interface{}{params, dbVolume}
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer to mark job as error if workflow execution fails
	defer func() {
		if err != nil {
			updateErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error())
			if updateErr != nil {
				logger.Error("Failed to update job state to ERROR", "job_id", createdJob.UUID, "error", updateErr)
			}
		}
	}()

	// controlWorkflowID defines the workflow ID for the control workflow
	controlWorkflowID := workflows.GenerateControlWorkflowID(dbVolume.Account.ID, location, dbVolume.Pool.Name)
	workflowOptions := workflows.DefaultSequentialWorkflowOptions(controlWorkflowID, createdJob.WorkflowID)

	// Execute workflow using centralized executor
	err = workflowExecutor.ExecuteSequentialWorkflow(
		ctx,
		workflowOptions,
		wf,
		wfArgs...,
	)
	if err != nil {
		logger.Error("Failed to start create volume workflow: ", "error", err)
		return nil, "", err
	}

	// Check if pool needs scaling based on volume count (async, non-blocking)
	// This happens after volume creation workflow is triggered successfully
	// Configuration variables
	if enableAutoPoolScaling {
		checkAndTriggerPoolScalingIfNeeded(ctx, se, temporal, dbPool, false)
	}

	// Set volume state for API response (volume will be created in workflow with this state)
	// For restore operations, set RESTORING state; otherwise set CREATING state
	if params.BackupPath != "" {
		dbVolume.State = datamodel.LifeCycleStateRestoring
		dbVolume.StateDetails = datamodel.LifeCycleStateRestoringDetails
	} else {
		dbVolume.State = datamodel.LifeCycleStateCreating
		dbVolume.StateDetails = datamodel.LifeCycleStateCreatingDetails
	}

	return convertDatastoreVolumeToModel(dbVolume, nil), createdJob.UUID, nil
}

// RevertVolume creates the specified volume and adds it to the list of volume belonging to the specified owner
func (o *GCPOrchestrator) RevertVolume(ctx context.Context, params *common.RevertVolumeParams) (*models.Volume, string, error) {
	return revertVolume(ctx, o.storage, o.temporal, params)
}

func _revertVolume(ctx context.Context, se database.Storage, temporal client.Client, params *common.RevertVolumeParams) (*models.Volume, string, error) {
	logger := util.GetLogger(ctx)
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to fetch account for the given projectNumber", "error", err)
		return nil, "", err
	}

	volume, err := se.GetVolumeWithAccountID(ctx, params.VolumeID, account.ID)
	if err != nil {
		logger.Error("Failed to fetch volume for the given account ID", "error", err)
		return nil, "", err
	}

	// Check if volume is in REVERTING state - if so, check for existing revert jobs for idempotency
	if volume.State == datamodel.LifeCycleStateReverting {
		resourceUUIDCol := "job_attributes ->> 'resource_uuid'"
		if utils.EnableJobResourceUUIDIndex {
			resourceUUIDCol = "resource_uuid"
		}
		filter := dbUtils.CreateFilterWithConditions(
			dbUtils.NewFilterCondition("resource_name", "=", volume.Name),
			dbUtils.NewFilterCondition("account_id", "=", volume.AccountID),
			dbUtils.NewFilterCondition("type", "=", string(datamodel.JobTypeRevertVolume)),
			dbUtils.NewFilterCondition("state", "!=", string(datamodel.JobsStateDONE)),
			dbUtils.NewFilterCondition("state", "!=", string(datamodel.JobsStateERROR)),
			dbUtils.NewFilterCondition(resourceUUIDCol, "=", volume.UUID))

		jobs, err := se.GetJobsWithCondition(ctx, *filter)
		if err != nil {
			logger.Errorf("Failed to get jobs with conditions: %v. Error: %v", filter, err)
			return nil, "", err
		}
		if len(jobs) > 0 {
			logger.Infof("Found ongoing volume revert job for account %s with volume %s. Job UUID: %s", params.AccountName, volume.Name, jobs[0].UUID)
			return convertDatastoreVolumeToModel(volume, nil), jobs[0].UUID, nil
		}
	}

	if utils.IsTransitionalState(volume.State) {
		logger.Errorf("Volume %s cannot be reverted, while in transitioning state: %s", volume.Name, volume.State)
		return nil, "", customerrors.NewConflictErr("volume is in transition state and cannot be reverted, state: " + volume.State)
	}

	if volume.State != datamodel.LifeCycleStateREADY {
		return nil, "", customerrors.NewConflictErr("Volume is not in READY state, state: " + volume.State)
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.IsDataProtection {
		return nil, "", customerrors.NewUserInputValidationErr("Cannot revert a Data Protection Volume")
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil {
		if volume.VolumeAttributes.CloneParentInfo.State == datamodel.CloneStateSplitting {
			return nil, "", customerrors.NewConflictErr("Reverting to a snapshot is not allowed when the volume is splitting")
		}
	}

	// Validate snapshot exists and is accessible
	snapshot, err := se.GetSnapshotByUUID(ctx, params.SnapshotID, volume.Account.ID, volume.ID)
	if err != nil {
		logger.Error("Failed to fetch snapshot for volume revert", "error", err)
		return nil, "", customerrors.NewUserInputValidationErr("Snapshot not found")
	}

	// Validate snapshot state
	if snapshot.State != datamodel.LifeCycleStateREADY {
		logger.Error("Snapshot is not in a valid state for volume revert", "snapshot_state", snapshot.State)
		return nil, "", customerrors.NewConflictErr("Snapshot is not in a valid state for volume revert. Please wait for the snapshot to be ready and retry again.")
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeRevertVolume),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  volume.Name,
		AccountID:     sql.NullInt64{Int64: volume.AccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: volume.UUID},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create volume revert job in database", "error", err)
		return nil, "", err
	}

	// Defer to mark job as deleted if any error happens
	defer func() {
		if err != nil {
			// Delete job if error occurred
			if createdJob != nil && createdJob.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", createdJob.UUID)
				if delErr := se.DeleteJob(ctx, createdJob.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
			}
		}
	}()

	previousState := volume.State
	previousStateDetails := volume.StateDetails
	volume, err = updateVolumeStatus(ctx, se, volume, datamodel.LifeCycleStateReverting, datamodel.LifeCycleStateRevertingDetails)
	if err != nil {
		logger.Error("Failed to update volume state in database", "error", err)
		return nil, "", err
	}
	// Defer to revert the resource state
	defer func() {
		if err != nil {
			// Revert volume state back to previous state if it was set to REVERTING
			if volume.State == datamodel.LifeCycleStateReverting {
				logger.Warnf("Error occurred during volume revert, reverting volume state to READY. Volume UUID: %s", volume.UUID)
				volumeUpdateErr := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
					"state":         previousState,
					"state_details": previousStateDetails,
				})
				if volumeUpdateErr != nil {
					logger.Errorf("Failed to revert volume state to previous volume state: %v", volumeUpdateErr)
				}
			}
		}
	}()

	location, err := utils.GetLocationFromVendorID(volume.Pool.VendorID)
	if err != nil {
		logger.Error("Failed to get location from vendor ID: ", "error", err)
		return nil, "", err
	}

	// controlWorkflowID defines the workflow ID for the control workflow
	controlWorkflowID := workflows.GenerateControlWorkflowID(volume.Account.ID, location, volume.Pool.Name)
	workflowOptions := workflows.DefaultSequentialWorkflowOptions(controlWorkflowID, createdJob.WorkflowID)
	revertVolumeTimeout := workflowengine.GetRevertVolumeWorkflowTimeout()
	workflowOptions.WorkflowRunTimeout = revertVolumeTimeout
	err = workflowExecutor.ExecuteSequentialWorkflow(
		ctx,
		workflowOptions,
		workflows.RevertVolumeWorkflow,
		params,
		volume,
		snapshot,
	)
	if err != nil {
		logger.Error("Failed to start revert volume workflow after retries: ", "error", err)
		return nil, "", err
	}

	return convertDatastoreVolumeToModel(volume, nil), createdJob.UUID, nil
}

// GetVolume gets the specified volume
func (o *GCPOrchestrator) GetVolume(ctx context.Context, volumeId string, refreshVolumeFields bool) (*models.Volume, error) {
	se := o.storage

	volume, err := se.DescribeVolume(ctx, volumeId)
	if err != nil {
		return nil, err
	}

	ipAddresses, err := getIPAddressForVolume(ctx, se, volume)
	if err != nil {
		return nil, err
	}

	return convertDatastoreVolumeToModel(volume, &ipAddresses), nil
}

// GetVolumeByName gets the specified volume by name and account.
func (o *GCPOrchestrator) GetVolumeByName(ctx context.Context, volumeName string, accountName string) (*models.Volume, error) {
	return getVolumeByName(ctx, o.storage, volumeName, accountName)
}

func _getVolumeByName(ctx context.Context, se database.Storage, volumeName string, accountName string) (*models.Volume, error) {
	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		return nil, err
	}

	volume, err := se.GetVolumeByNameAndAccountIDWithPool(ctx, volumeName, account.ID)
	if err != nil {
		return nil, err
	}

	volume.Account = account

	return convertDatastoreVolumeToModel(volume, nil), nil
}

func (o *GCPOrchestrator) GetVolumeCount(ctx context.Context, projectNumber string) (int64, error) {
	// Get the count of volume replications for the specified account
	count, err := o.storage.GetVolumeCount(ctx, projectNumber)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ListVolumes returns list of volumes belonging to the specified owner
func (o *GCPOrchestrator) ListVolumes(ctx context.Context, accountName string) ([]*models.Volume, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		return nil, err
	}

	conditions := [][]interface{}{{"account_id = ?", account.ID}}
	volumes, err := se.ListVolumes(ctx, conditions)
	if err != nil {
		return nil, err
	}

	return convertDatastoreVolumesToModel(ctx, se, volumes), nil
}

func convertDatastoreVolumesToModel(ctx context.Context, se database.Storage, volumes []*datamodel.Volume) []*models.Volume {
	ipAddressByVolumeID, err := batchGetIPAddressesForVolumes(ctx, se, volumes)
	if err != nil {
		// IP addresses are best-effort: keep listing the volumes without them.
		util.GetLogger(ctx).Warnf("Failed to get IP addresses for %d volumes: %v", len(volumes), err)
	}

	volumesList := make([]*models.Volume, 0, len(volumes))
	for _, volume := range volumes {
		var ipAddresses *[]string
		if ips, ok := ipAddressByVolumeID[volume.ID]; ok {
			ipAddresses = &ips
		}
		volumesList = append(volumesList, convertDatastoreVolumeToModel(volume, ipAddresses))
	}
	return volumesList
}

// volumePoolProtocolKey groups volumes that share the same pool-level IP lookup.
type volumePoolProtocolKey struct {
	poolID    int64
	accountID int64
	protocol  string // non-empty for file volumes; empty for block volumes
}

type accountProtocolKey struct {
	accountID int64
	protocol  string
}

func volumePoolProtocolKeyFromVolume(volume *datamodel.Volume) volumePoolProtocolKey {
	key := volumePoolProtocolKey{
		poolID:    volume.PoolID,
		accountID: volume.AccountID,
	}
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.Protocols != nil && len(volume.VolumeAttributes.Protocols) > 0 {
		key.protocol = string(utils.GetProtocolType(volume.VolumeAttributes.Protocols[0]))
	}
	return key
}

func (k volumePoolProtocolKey) accountProtocolKey() accountProtocolKey {
	return accountProtocolKey{accountID: k.accountID, protocol: k.protocol}
}

func getIPAddressesForPoolProtocolBatch(ctx context.Context, se database.Storage, accountID int64, protocol string, poolIDs []int64) (map[int64][]string, error) {
	logger := util.GetLogger(ctx)
	result := make(map[int64][]string, len(poolIDs))
	for _, poolID := range poolIDs {
		result[poolID] = []string{}
	}
	if len(poolIDs) == 0 {
		return result, nil
	}

	nodes, err := se.GetNodesByPoolIDs(ctx, poolIDs)
	if err != nil {
		return nil, err
	}

	nodesByPool := make(map[int64][]*datamodel.Node)
	allNodeIDs := make([]int64, 0, len(nodes))
	for _, node := range nodes {
		nodesByPool[node.PoolID] = append(nodesByPool[node.PoolID], node)
		allNodeIDs = append(allNodeIDs, node.ID)
	}
	if len(allNodeIDs) == 0 {
		return result, nil
	}

	if protocol != "" {
		lifs, err := se.GetLifsForNodesWithProtocol(ctx, allNodeIDs, accountID, protocol)
		if err != nil {
			return nil, err
		}
		for poolID, poolNodes := range nodesByPool {
			nodeIDSet := make(map[int64]struct{}, len(poolNodes))
			for _, node := range poolNodes {
				nodeIDSet[node.ID] = struct{}{}
			}
			ips := make([]string, 0, len(lifs))
			for _, lif := range lifs {
				if _, ok := nodeIDSet[lif.NodeID]; ok {
					ips = append(ips, lif.IPAddress)
				}
			}
			result[poolID] = ips
		}
		return result, nil
	}

	lifs, err := se.GetLifsForNodesWithProtocol(ctx, allNodeIDs, accountID, "")
	if err != nil {
		return nil, err
	}
	lifByNode := make(map[int64]string, len(lifs))
	for _, lif := range lifs {
		if _, ok := lifByNode[lif.NodeID]; !ok {
			lifByNode[lif.NodeID] = lif.IPAddress
		}
	}

	for poolID, poolNodes := range nodesByPool {
		ips := make([]string, 0, len(poolNodes))
		for _, node := range poolNodes {
			ip, ok := lifByNode[node.ID]
			if !ok {
				_, err := se.GetLifForNode(ctx, node.ID, accountID)
				if err != nil {
					logger.Errorf("Failed to get lif for node %d account %d: %s", node.ID, accountID, err.Error())
				}
			}
			ips = append(ips, ip)
		}
		result[poolID] = ips
	}
	return result, nil
}

func _getIPAddressForVolume(ctx context.Context, se database.Storage, volume *datamodel.Volume) ([]string, error) {
	ipAddresses := make([]string, 0)
	nodes, err := se.GetNodesByPoolID(ctx, volume.PoolID)
	if err != nil {
		return ipAddresses, err
	}

	if volume.VolumeAttributes.FileProperties != nil {
		protocol := volume.VolumeAttributes.Protocols[0]
		pType := utils.GetProtocolType(protocol)
		var nodesId []int64
		for _, node := range nodes {
			nodesId = append(nodesId, node.ID)
		}
		lifs, err := se.GetLifsForNodesWithProtocol(ctx, nodesId, volume.AccountID, string(pType))
		if err != nil {
			return ipAddresses, err
		}
		for _, lif := range lifs {
			ipAddresses = append(ipAddresses, lif.IPAddress)
		}
	} else {
		for _, node := range nodes {
			lif, err := se.GetLifForNode(ctx, node.ID, volume.AccountID)
			if err != nil {
				return ipAddresses, err
			}
			ipAddresses = append(ipAddresses, lif.IPAddress)
		}
	}

	return ipAddresses, nil
}

func _batchGetIPAddressesForVolumes(ctx context.Context, se database.Storage, volumes []*datamodel.Volume) (map[int64][]string, error) {
	if len(volumes) == 0 {
		return map[int64][]string{}, nil
	}

	poolIDsByAccountProtocol := make(map[accountProtocolKey]map[int64]struct{})
	volumeKeys := make(map[int64]volumePoolProtocolKey, len(volumes))
	for _, volume := range volumes {
		key := volumePoolProtocolKeyFromVolume(volume)
		volumeKeys[volume.ID] = key
		apKey := key.accountProtocolKey()
		if poolIDsByAccountProtocol[apKey] == nil {
			poolIDsByAccountProtocol[apKey] = make(map[int64]struct{})
		}
		poolIDsByAccountProtocol[apKey][key.poolID] = struct{}{}
	}

	cache := make(map[volumePoolProtocolKey][]string)
	for apKey, poolIDSet := range poolIDsByAccountProtocol {
		poolIDs := make([]int64, 0, len(poolIDSet))
		for poolID := range poolIDSet {
			poolIDs = append(poolIDs, poolID)
		}
		ipsByPool, err := getIPAddressesForPoolProtocolBatch(ctx, se, apKey.accountID, apKey.protocol, poolIDs)
		if err != nil {
			return nil, err
		}
		for poolID, ips := range ipsByPool {
			cache[volumePoolProtocolKey{poolID: poolID, accountID: apKey.accountID, protocol: apKey.protocol}] = ips
		}
	}

	result := make(map[int64][]string, len(volumes))
	for _, volume := range volumes {
		result[volume.ID] = cache[volumeKeys[volume.ID]]
	}
	return result, nil
}

// VolumeTypeProcessor defines protocol-specific validation for volume creation
type VolumeTypeProcessor interface {
	Validate(ctx context.Context, se database.Storage, params *common.CreateVolumeParams, accountID int64) error
}

type (
	BlockVolumeProcessor struct{}
	FileVolumeProcessor  struct{}
)

func (v *BlockVolumeProcessor) Validate(ctx context.Context, se database.Storage, params *common.CreateVolumeParams, accountID int64) error {
	// Block-specific validation: host group checks, block properties, etc.
	params.FileProperties = nil // Ensure FileProperties is nil for block volumes

	// NOTE: we only bypass block device validation for hybrid replications. If additional checks are introduced in the future, we will need to evaluate each one individually
	if params.HybridReplicationParameters != nil {
		return nil
	}
	// Validate BlockDevices if provided
	if params.BlockDevices != nil && len(*params.BlockDevices) > 0 {
		blockDevice := (*params.BlockDevices)[0]
		hostGroupUUIDs := blockDevice.HostGroups
		err := validateBlockProperties(ctx, se, hostGroupUUIDs, accountID)
		if err != nil {
			return err
		}
	} else if params.BlockProperties != nil {
		hostGroupUUIDs := params.BlockProperties.HostGroupUUIDs
		err := validateBlockProperties(ctx, se, hostGroupUUIDs, accountID)
		if err != nil {
			return err
		}
	} else {
		return customerrors.NewUserInputValidationErr("Block Device/Block Properties is required")
	}
	return nil
}

func (v *FileVolumeProcessor) Validate(ctx context.Context, se database.Storage, params *common.CreateVolumeParams, accountID int64) error {
	params.BlockProperties = nil // Ensure BlockProperties is nil for file volumes
	if params.FileProperties == nil {
		return customerrors.NewUserInputValidationErr("FileProperties cannot be nil for NAS volumes")
	}

	if params.FileProperties.ExportPolicy != nil && params.FileProperties.ExportPolicy.ExportRules != nil {
		if err := factorycommon.ValidateExportRulesAgainstProtocols(params.FileProperties.ExportPolicy.ExportRules, params.Protocols); err != nil {
			return err
		}
	}

	if params.CreationToken == "" {
		return customerrors.NewUserInputValidationErr("Creation Token cannot be empty")
	}

	existingVolume, err := se.GetVolumeByJunctionPath(ctx, params.CreationToken, accountID, params.PoolDBID)
	if err != nil && !customerrors.IsNotFoundErr(err) {
		return err
	}
	if existingVolume != nil {
		return customerrors.NewConflictErr("A volume with the same creation token already exists")
	}
	return nil
}

func GetVolumeTypeValidator(protocols []string, ontapVersion string) (VolumeTypeProcessor, error) {
	if utils.IsSanProtocols(protocols) {
		return &BlockVolumeProcessor{}, nil
	}
	if utils.IsNasProtocols(protocols) {
		if !utils.IsFileProtocolSupportedV2(ontapVersion) {
			return nil, customerrors.NewUserInputValidationErr("file protocols are not enabled")
		}
		return &FileVolumeProcessor{}, nil
	}
	return nil, customerrors.NewUserInputValidationErr("unsupported or unspecified protocol")
}

// checkIsValidImmutableBackupPolicyWithRetry validates immutable backup policy compliance with retry logic
// to handle concurrent backup policy or backup vault update operations.
// It performs the following validations:
// 1. Fetches the backup policy and backup vault
// 2. Validates daily backup retention against immutable period
// 3. Validates weekly backup retention against immutable period
// 4. Validates monthly backup retention against immutable period
// Returns error if any validation fails, nil otherwise.
func _checkIsValidImmutableBackupPolicyWithRetry(ctx context.Context, se database.Storage, backupPolicyUUID string, backupVaultUUID string, accountID int64, region string, accountName string) error {
	logger := util.GetLogger(ctx)

	for attempt := 1; attempt <= common.MaxRetries; attempt++ {
		err := _checkIsValidImmutableBackupPolicyWithStateCheck(ctx, se, backupPolicyUUID, backupVaultUUID, accountID, region, accountName)
		if err == nil {
			return nil // Success
		}

		// Check if this is a retryable error (backup policy or backup vault in updating state)
		if isImmutableBackupPolicyRetryableError(err) {
			if attempt < common.MaxRetries {
				logger.Warn("Immutable backup policy validation failed due to concurrent update, retrying",
					"attempt", attempt,
					"maxRetries", common.MaxRetries,
					"retryAfter", common.RetryDelay,
					"error", err)
				common.SleepFn(common.RetryDelay)
				continue
			} else {
				logger.Error("Immutable backup policy validation failed after all retry attempts",
					"attempt", attempt,
					"maxRetries", common.MaxRetries,
					"error", err)
				return err
			}
		}

		// Non-retryable error, return immediately
		return err
	}

	return fmt.Errorf("immutable backup policy validation failed after %d attempts", common.MaxRetries)
}

// isImmutableBackupPolicyRetryableError checks if the error is related to backup policy or backup vault
// being in updating state, which is a retryable condition.
func isImmutableBackupPolicyRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var customError *vsaerrors.CustomError
	if vsaerrors.As(err, &customError) {
		if customError.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupPolicy || customError.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupVault {
			return true
		}
	}
	return false
}

// getBackupVaultFromCVP fetches a specific backup vault from CVP by its ID
func getBackupVaultFromCVP(ctx context.Context, backupVaultID string, region string, accountName string) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)

	// Get authentication token and create CVP client
	getSignedJwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := cvpCreateClient(logger, getSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)

	// List all backup vaults from CVP
	vaults, err := cvpClient.BackupVault.V1betaListBackupVaults(&backup_vault.V1betaListBackupVaultsParams{
		LocationID:     region,
		ProjectNumber:  accountName,
		XCorrelationID: &xCorrelationID,
	})
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, customerrors.NewNotFoundErr("Backup vault", nil)
		}
		logger.Errorf("Error fetching backup vaults from CVP: %v", err)
		return nil, err
	}

	// Search for the specific backup vault
	for _, bv := range vaults.Payload.BackupVaults {
		if bv.BackupVaultID == backupVaultID {
			// Convert to data model
			bvModel, err := activities.ConvertToBackupVaultDataModel(bv, region)
			if err != nil {
				return nil, fmt.Errorf("failed to convert backup vault to data model: %w", err)
			}

			return bvModel, nil
		}
	}

	// Backup vault not found
	return nil, customerrors.NewNotFoundErr("Backup vault", &backupVaultID)
}

// GetBackupPolicyFromCVP fetches backup policy from CVP and converts it to the internal data model
func GetBackupPolicyFromCVP(ctx context.Context, backupPolicyUUID, region, accountName string) (*datamodel.BackupPolicy, error) {
	logger := util.GetLogger(ctx)
	GetSignedJwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := cvpCreateClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)

	// Fetch backup policy from CVP
	cvpBackupPolicy, err := cvpClient.BackupPolicy.V1betaDescribeBackupPolicy(&backup_policy.V1betaDescribeBackupPolicyParams{
		BackupPolicyID: backupPolicyUUID,
		LocationID:     region,
		ProjectNumber:  accountName,
		XCorrelationID: &xCorrelationID,
	})
	if err != nil {
		logger.Errorf("Error fetching backup policy from CVP: %v", err)
		return nil, err
	}

	if cvpBackupPolicy == nil || cvpBackupPolicy.Payload == nil {
		logger.Error("No backup policy found in CVP")
		return nil, customerrors.NewNotFoundErr("Backup policy", &backupPolicyUUID)
	}

	// Convert CVP response to internal data model
	backupPolicy := activities.ConvertToBackupPolicyDataModel(cvpBackupPolicy.Payload)

	return backupPolicy, nil
}

// _checkIsValidImmutableBackupPolicyWithStateCheck validates immutable backup policy compliance
// and checks for backup policy/vault updating states before performing validation.
func _checkIsValidImmutableBackupPolicyWithStateCheck(ctx context.Context, se database.Storage, backupPolicyUUID string, backupVaultUUID string, accountID int64, region string, accountName string) error {
	// Add input validation
	if backupPolicyUUID == "" {
		return fmt.Errorf("backup policy UUID cannot be empty")
	}
	if backupVaultUUID == "" {
		return fmt.Errorf("backup vault UUID cannot be empty")
	}
	if accountID <= 0 {
		return fmt.Errorf("account ID must be positive")
	}

	// Get backup policy details
	backupPolicy, err := se.GetBackupPolicyByUUIDAndOwnerID(ctx, backupPolicyUUID, accountID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger := util.GetLogger(ctx)
			logger.Warnf("Backup policy '%v' not found in local DB, attempting to fetch from CVP", backupPolicyUUID)
			// If not found in local DB, try fetching from CVP
			backupPolicy, err = GetBackupPolicyFromCVP(ctx, backupPolicyUUID, region, accountName)
			if err != nil {
				return fmt.Errorf("failed to get backup policy from CVP: %v", err)
			}
		} else {
			return fmt.Errorf("failed to get backup policy: %v", err)
		}
	}

	// Check if backup policy is in updating state
	if backupPolicy.LifeCycleState == datamodel.LifeCycleStateUpdating {
		return vsaerrors.NewVCPError(vsaerrors.ErrImmutableValidationWithUpdatingBackupPolicy, fmt.Errorf("Cannot validate immutable backup policy: backup policy '%v' is currently being updated. Please wait for the policy update to complete.", backupPolicyUUID))
	}

	// Get backup vault details
	backupVault, err := se.GetBackupVaultByUUIDndOwnerID(ctx, backupVaultUUID, accountID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			if !utils.IsCVPHostConfigured() {
				return fmt.Errorf("backup vault '%s' not found", backupVaultUUID)
			}
			logger := util.GetLogger(ctx)
			logger.Warnf("Backup vault '%v' not found in local DB, attempting to fetch from CVP", backupVaultUUID)
			// If not found in local DB, try fetching from CVP
			backupVault, err = GetBackupVaultFromCVP(ctx, backupVaultUUID, region, accountName)
			if err != nil {
				return fmt.Errorf("failed to get backup vault from CVP: %v", err)
			}
		} else {
			return fmt.Errorf("failed to get backup vault: %v", err)
		}
	}

	// Check if backup vault is in updating state
	if backupVault.LifeCycleState == datamodel.LifeCycleStateUpdating {
		return vsaerrors.NewVCPError(vsaerrors.ErrImmutableValidationWithUpdatingBackupVault, fmt.Errorf("Cannot validate immutable backup policy: backup vault '%s' is currently being updated. Please wait for the vault update to complete.", backupVaultUUID))
	}

	// Skip validation if backup vault doesn't have immutable attributes configured
	if backupVault.ImmutableAttributes == nil {
		return nil
	}
	if *backupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration == 0 {
		return nil
	}
	immutableAttrs := backupVault.ImmutableAttributes
	backupPolicyParams := &common.BackupPolicyParams{
		DailyBackupsToKeep:   backupPolicy.DailyBackupsToKeep,
		WeeklyBackupsToKeep:  backupPolicy.WeeklyBackupsToKeep,
		MonthlyBackupsToKeep: backupPolicy.MonthlyBackupsToKeep,
	}
	retentionPolicyParams := &common.BackupRetentionPolicyParams{
		BackupMinimumEnforcedRetentionDuration: immutableAttrs.BackupMinimumEnforcedRetentionDuration,
		IsDailyBackupImmutable:                 &immutableAttrs.IsDailyBackupImmutable,
		IsWeeklyBackupImmutable:                &immutableAttrs.IsWeeklyBackupImmutable,
		IsMonthlyBackupImmutable:               &immutableAttrs.IsMonthlyBackupImmutable,
		IsAdhocBackupImmutable:                 &immutableAttrs.IsAdhocBackupImmutable,
	}
	err = common.ValidateBackupPolicyRetentionLimits(backupPolicyParams, retentionPolicyParams)
	if err != nil {
		return fmt.Errorf("immutable backup policy validation failed: %w", err)
	}
	return nil
}

// isPrime return true if constituentVolumeCount is prime for constituentVolumeCount greater than and equal to seven
func isPrime(constituentVolumeCount int) bool {
	if constituentVolumeCount%2 == 0 || constituentVolumeCount%3 == 0 {
		return false
	}
	sq_root := int(math.Sqrt(float64(constituentVolumeCount)))

	for i := 5; i <= sq_root; i = i + 6 {
		if constituentVolumeCount%i == 0 || constituentVolumeCount%(i+2) == 0 {
			return false
		}
	}
	return true
}

func lvHaPairsForLargeVolumeAccount(accountID string) int64 {
	return utils.LvHaPairsForLargeVolume(accountID, numOfLvHAPairs)
}

// defaultLargeVolumeConstituentCount returns the default constituent volume count for a large
// capacity volume: HA pairs × CVs per aggregate. When the pool is true-zonal (active-active)
// and ENABLE_TRUE_ZONAL_LV_DEFAULT_CONSTITUENT_COUNT is enabled, the count is doubled because both
// nodes in each HA pair host active aggregates.
func defaultLargeVolumeConstituentCount(accountName string, isActiveActive bool) int64 {
	count := lvHaPairsForLargeVolumeAccount(accountName) * defaultConstituentsPerAggregate
	if isActiveActive && enableTrueZonalLVDefaultConstituentCount {
		count *= 2
	}
	return count
}

// largeVolumeAggregateCount returns the number of aggregates available for CV placement.
// Active-passive pools have one aggregate per HA pair, active-active (true-zonal) pools
// have aggregates on both nodes of each HA pair
func largeVolumeAggregateCount(lvHaPairs int64, isActiveActive bool) int64 {
	if isActiveActive {
		return lvHaPairs * 2
	}
	return lvHaPairs
}

func getMaxConstituentVolumesPerAggregate(logger log.Logger, config string) (int64, error) {
	// Get the VSA instance type detail from Pool table
	vlmConfig := &vlm.VLMConfig{}
	err := json.Unmarshal([]byte(config), vlmConfig)
	if err != nil {
		return 0, err
	}

	return activities.GetMaxConstituentsPerAggregate(logger, vlmConfig.Deployment.VSAInstanceType), nil
}

func _validateCreateVolumeParams(ctx context.Context, se database.Storage, params *common.CreateVolumeParams, pool *datamodel.PoolView) error {
	logger := utilGetLogger(ctx)
	if pool.LargeCapacity != params.LargeCapacity {
		if params.CacheParameters != nil {
			// Cache Volumes cannot have large capacity set but should still be allowed to be created on a large capacity pool.
			logger.Debug("Allowing cache volume to have different large capacity setting than pool")
		} else {
			return customerrors.NewUserInputValidationErr("pool large capacity setting does not match volume large capacity setting")
		}
	}

	if hp := params.HybridReplicationParameters; hp != nil {
		replicationType := hp.ReplicationType
		replicationSchedule := hp.ReplicationSchedule
		if replicationType == datamodel.HybridReplicationParametersReplicationTypeREVERSE || replicationType == datamodel.HybridReplicationParametersReplicationTypeCONTINUOUS {
			msg := "Hybrid replication is not allowed for replicationType: " + string(replicationType)
			return customerrors.NewUserInputValidationErr(msg)
		}

		if replicationType == datamodel.HybridReplicationParametersReplicationTypeONPREM {
			if nillable.IsNilOrEmpty(&replicationSchedule) {
				msg := "Can't have empty replicationSchedule for " + string(replicationType)
				return customerrors.NewUserInputValidationErr(msg)
			}
		}

		if hp.PeerClusterName == "" || hp.PeerVolumeName == "" || hp.PeerSvmName == "" || len(hp.PeerIPAddresses) == 0 || hp.ResourceID == "" {
			msg := "PeerClusterName, PeerSvmName, PeerVolumeName, PeerIPAddresses and ResourceID are required for Hybrid Replication"
			return customerrors.NewUserInputValidationErr(msg)
		}

		replicationCount, err := se.GetVolumeReplicationCountByPeerDetails(ctx, params.AccountName, params.HybridReplicationParameters.PeerSvmName, params.HybridReplicationParameters.PeerVolumeName)
		if err != nil {
			return err
		}

		if replicationCount > 0 {
			return customerrors.NewUserInputValidationErr("Hybrid replication already exists for the given peer SVM and volume")
		}

		if params.SnapshotID != "" || params.BackupID != "" || params.SnapshotPolicy != nil || params.BackupPath != "" {
			msg := "Restoring volume from snapshot, backup, or enabling snapshot policy is not supported for Hybrid Replication volumes"
			return customerrors.NewUserInputValidationErr(msg)
		}

		if params.DataProtection != nil && ((params.DataProtection.ScheduledBackupEnabled != nil && *params.DataProtection.ScheduledBackupEnabled) || (params.DataProtection.BackupPolicyId) != "") {
			msg := "Scheduled backups are not supported for Hybrid Replication, only manual backups are supported"
			return customerrors.NewUserInputValidationErr(msg)
		}
		for _, ipAddress := range params.HybridReplicationParameters.PeerIPAddresses {
			if !utils.ValidateIPv4Address(ipAddress) {
				msg := "Invalid IP Address provided in Hybrid Replication Parameters"
				return customerrors.NewUserInputValidationErr(msg)
			}
		}

		if params.HybridReplicationParameters.Labels != nil {
			err := replication.ValidateLabels(params.HybridReplicationParameters.Labels)
			if err != nil {
				return err
			}
		}

		err = replication.ValidateReplicationResourceId(ctx, params.AccountName, params.HybridReplicationParameters.ResourceID, params.Name, se)
		if err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrValidateCreateResourceIdInUse, err)
		}

		// Check for active replication jobs to prevent conflicts
		ccfeReplicationUri := fmt.Sprintf("projects/%s/locations/%s/volumes/%s/replications/%s",
			params.AccountName,
			params.Region,
			params.Name,
			hp.ResourceID)

		err = replication.CheckActiveReplicationJobs(ctx, se, pool.AccountID, pool.UUID, ccfeReplicationUri)
		if err != nil {
			return err
		}
	}

	if params.LargeCapacity {
		lvHaPairs := lvHaPairsForLargeVolumeAccount(params.AccountName)
		if utils.IsSanProtocols(params.Protocols) {
			return customerrors.NewUserInputValidationErr("SAN protocols are not supported for large capacity volumes")
		}

		if params.BlockDevices != nil && len(*params.BlockDevices) > 0 {
			return customerrors.NewUserInputValidationErr("BlockDevices are not supported for large capacity volumes")
		}

		maxLVVolSize := utils.MaxLvHotTierCapacity
		// For AT enabled volume, max volume size is 20 PiB, otherwise 2.48 PiB
		if params.AutoTieringPolicy != nil && params.AutoTieringPolicy.AutoTieringEnabled {
			maxLVVolSize = utils.MaxQuotaInBytesLargeVolume
		}
		minLVVolSize := utils.MinQuotaInBytesLargeVolume

		var cvSizeInBytes uint64
		var cvCount int64
		if params.LargeVolumeConstituentCount > 0 {
			minLVVolSize = utils.MinQuotaInBytesLargeVolumeWithCV

			// validate large volume constituent count is not prime
			if params.LargeVolumeConstituentCount >= int32(minPrimeNumberConfigAllowed) && isPrime(int(params.LargeVolumeConstituentCount)) {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("Constituent volume count with %d is not supported", params.LargeVolumeConstituentCount))
			}

			// validate large volume constituent count is within allowed limit
			maxConstituentVolumesPerAggregate, err := getMaxConstituentVolumesPerAggregate(logger, pool.VLMConfig)
			if err != nil {
				return customerrors.NewTransientErr(fmt.Sprintf("error unmarshalling VLM config from pool: %v", err))
			}

			// Active-active pools have aggregates on both nodes per HA pair, use that
			// aggregate count for per-volume and per-aggregate CV caps.
			aggregateCount := largeVolumeAggregateCount(lvHaPairs, pool.IsActiveActive())
			maxCVsPerVolumeLimit := aggregateCount * maxConstituentVolumesPerVolumePerAggregate
			if int64(params.LargeVolumeConstituentCount) > maxCVsPerVolumeLimit {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("Large Volume constituent count cannot be greater than %d for the current per-aggregate limit", maxCVsPerVolumeLimit))
			}

			// Subtracting 1 because there will always be root vol in the aggregate at start
			finalMaxCVs := (aggregateCount * maxConstituentVolumesPerAggregate) - 1
			if int64(params.LargeVolumeConstituentCount) > finalMaxCVs {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("Large Volume constituent count cannot be greater than %d", int32(finalMaxCVs)))
			}

			// validate that each constituent volume size is at least 100 GB
			cvSizeInBytes = params.QuotaInBytes / uint64(params.LargeVolumeConstituentCount)
			cvCount = int64(params.LargeVolumeConstituentCount)
		} else {
			// For default CVs params.LargeVolumeConstituentCount is 0, we need to validate if params.QuotaInBytes/ uint64(defaultConstituentCount) >= minCVSizeInBytes
			// Set default constituent count: 8 CVs per aggregate × HA pairs for this account (doubled for true-zonal when ENABLE_TRUE_ZONAL_LV_DEFAULT_CONSTITUENT_COUNT is on)
			cvCount = defaultLargeVolumeConstituentCount(params.AccountName, pool.IsActiveActive())
			// validate that each constituent volume size is at least 100 GB
			cvSizeInBytes = params.QuotaInBytes / uint64(cvCount)
		}

		if cvSizeInBytes < minCVSizeInBytes {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("Constituent volume size cannot be less than %s. Current CV size is %s with %d constituent volumes",
				utils.FmtUint64Bytes(minCVSizeInBytes), utils.FmtUint64Bytes(cvSizeInBytes), cvCount))
		}
		if cvSizeInBytes > maxCVSizeInBytes {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("Constituent volume size cannot be more than %s. Current CV size is %s with %d constituent volumes",
				utils.FmtUint64Bytes(maxCVSizeInBytes), utils.FmtUint64Bytes(cvSizeInBytes), cvCount))
		}

		if params.QuotaInBytes < minLVVolSize || params.QuotaInBytes > maxLVVolSize {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("Invalid volume capacity %s. Must be between %s and %s.",
				utils.FmtUint64Bytes(params.QuotaInBytes), utils.FmtUint64Bytes(minLVVolSize),
				utils.FmtUint64Bytes(maxLVVolSize)))
		}
	}

	if !params.LargeCapacity {
		if params.LargeVolumeConstituentCount > 0 {
			return customerrors.NewUserInputValidationErr("Large Volume constituent count is only supported for large capacity volumes")
		}
		maxQuotaInBytes := factorycommon.GetMaxQuotaForVolume(params.Protocols)
		if params.QuotaInBytes < minQuotaInBytesVolume || params.QuotaInBytes > maxQuotaInBytes {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("Invalid volume capacity %s. Must be between %s and %s.",
				utils.FmtUint64Bytes(params.QuotaInBytes), utils.FmtUint64Bytes(minQuotaInBytesVolume),
				utils.FmtUint64Bytes(maxQuotaInBytes)))
		}
	}

	cloneSharedBytes := uint64(0)
	if params.SnapshotID != "" {
		account, err := getOrCreateAccount(ctx, se, params.AccountName)
		if err != nil {
			return err
		}

		dbSnapshot, err := se.GetSnapshotByPoolID(ctx, params.SnapshotID, account.ID, pool.ID, true)
		if err != nil {
			if customerrors.IsNotFoundErr(err) {
				return customerrors.NewUserInputValidationErr("snapshot not found")
			}
			return err
		}

		if dbSnapshot.Volume != nil && dbSnapshot.Volume.VolumeAttributes != nil && dbSnapshot.Volume.VolumeAttributes.CloneParentInfo != nil {
			if dbSnapshot.Volume.VolumeAttributes.CloneParentInfo.State == datamodel.CloneStateSplitting {
				logger.Error("Cannot restore volume from snapshot as parent volume is undergoing split operation", "volume_id", dbSnapshot.Volume.UUID)
				return customerrors.NewConflictErr("Cannot restore volume from snapshot as the parent volume is undergoing split operation")
			}
		}

		if !thinCloneGASupport {
			if pool.ThinCloneVolumeCount+1 > maxThinClonesPerPool {
				return customerrors.NewUserInputValidationErr("pool has reached maximum clone volume limit")
			}
		}
		if dbSnapshot != nil && dbSnapshot.SnapshotAttributes != nil {
			cloneSharedBytes = uint64(dbSnapshot.SnapshotAttributes.LogicalSizeUsedInBytes)
		}
	}

	if pool.QuotaInBytes+params.QuotaInBytes-cloneSharedBytes > uint64(pool.SizeInBytes) {
		return customerrors.NewUserInputValidationErr("volume size cannot be greater than pool size")
	}

	// Validate QoS parameters (MQoS rules and throughput range); pool capacity is validated below
	poolQos := mqos.PoolQosInput{QosType: pool.QosType}
	if pool.PoolAttributes != nil {
		poolQos.PoolThroughputMibps = pool.PoolAttributes.ThroughputMibps
		poolQos.PoolIops = pool.PoolAttributes.Iops
	}
	calculatedIops, err := validateVolumeQosParams(poolQos, params.ThroughputMibps, params.Iops, params.VolumePerformanceGroupID)
	if err != nil {
		return err
	}

	// Validate pool capacity (only if MQOS is enabled)
	if enableMqos && params.ThroughputMibps != nil {
		err := mqos.ValidatePoolCapacityForVolume(ctx, se, pool.UUID, params.ThroughputMibps, calculatedIops, nil)
		if err != nil {
			return err
		}
		if params.VolumePerformanceGroupID == nil {
			if err := mqos.ValidateVPGCountForPool(ctx, se, pool.ID); err != nil {
				return err
			}
		}
	} else if enableMqos && params.VolumePerformanceGroupID != nil {
		if err := validatePoolCapacityForVPGVolumeCreate(ctx, se, pool.UUID, *params.VolumePerformanceGroupID); err != nil {
			return err
		}
	}

	switch pool.State {
	case datamodel.LifeCycleStateCreating, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeleted:
		return customerrors.NewUserInputValidationErr(fmt.Sprintf("Specified pool is in %s state, hence volume cannot be created", pool.State))
	case datamodel.LifeCycleStateError:
		return customerrors.NewUserInputValidationErr("Pool is currently unavailable for creating volume")
	case datamodel.LifeCycleStateDegraded:
		if pool.KmsConfigID.Valid {
			return customerrors.NewConflictErr("Pool is in degraded state, hence CMEK enabled volumes cannot be created")
		}
	}

	// Block CMEK volume create when cluster upgrade is in progress (same as degraded mode). Only check when CMEK is enabled to avoid extra DB read for non-CMEK pools.
	if pool.KmsConfigID.Valid {
		hasUpgrade, err := HasActiveClusterUpgrade(ctx, se, pool.UUID)
		if err != nil {
			return err
		}
		if hasUpgrade {
			return customerrors.NewConflictErr("Storage pool is temporarily unavailable, please try again later")
		}
	}

	if params.Network == "" {
		params.Network = pool.Network
	} else if params.Network != pool.Network {
		return customerrors.NewUserInputValidationErr("pool network and volume network should be same")
	}

	svm, err := se.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		return err
	}

	if svm.State != datamodel.LifeCycleStateREADY {
		return customerrors.NewUserInputValidationErr("svm is not ready")
	}

	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return err
	}

	minNodeCount := 2
	if envIsLocalEnv() {
		// VSIMs may have 1 node, VSA clusters should have at least 2 nodes
		minNodeCount = 1
	}

	if len(nodes) < minNodeCount {
		return customerrors.NewUserInputValidationErr("required count of nodes not found")
	}

	for _, node := range nodes {
		if node.State != datamodel.LifeCycleStateREADY {
			return customerrors.NewUserInputValidationErr("node is not ready")
		}
		lif, err := se.GetLifForNode(ctx, node.ID, node.AccountID)
		if err != nil {
			return err
		}
		if lif.Name == "" {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("lif for node %s is not available", node.Name))
		}
	}

	if params.DataProtection != nil && params.DataProtection.BackupVaultID != "" {
		bv, err := se.GetBackupVaultByUUIDndOwnerID(ctx, params.DataProtection.BackupVaultID, pool.Account.ID)
		if err != nil && !customerrors.IsNotFoundErr(err) {
			return err
		}
		if bv != nil {
			if bv.LifeCycleState == datamodel.LifeCycleStateError {
				return customerrors.NewUserInputValidationErr("backup vault is in error state, please check the backup vault and try again")
			}
			if err := validateCRBBackupVault(bv, params.Region); err != nil {
				return err
			}
			if bv.CmekAttributes != nil && !nillable.IsNilOrEmpty(bv.CmekAttributes.KmsConfigResourcePath) && nillable.IsNilOrEmpty(params.DataProtection.KmsGrant) {
				return customerrors.NewUserInputValidationErr("KMS Grant is required for CMEK Backup vault")
			}
		}
	}

	if params.DataProtection != nil && params.DataProtection.BackupPolicyId != "" {
		// Validate assigning backup policy to the volume
		if params.DataProtection.BackupVaultID == "" {
			return customerrors.NewUserInputValidationErr("backup vault id is required to assign a backup policy to a volume")
		}
		if utils.IsImmutableBackupEnabled() {
			logger := util.GetLogger(ctx)
			logger.Debug("Validating immutable backup policy compliance",
				"backupPolicyId", params.DataProtection.BackupPolicyId,
				"backupVaultId", params.DataProtection.BackupVaultID)

			// Validate immutable backup policy compliance
			err = checkIsValidImmutableBackupPolicyWithRetry(ctx, se, params.DataProtection.BackupPolicyId, params.DataProtection.BackupVaultID, pool.Account.ID, params.Region, params.AccountName)
			if err != nil {
				logger.Errorf("Immutable backup policy validation failed %v", err)

				// Check if it's a service-related error (CVP down, network issues, etc.)
				if customerrors.IsUnavailableErr(err) || customerrors.IsNetworkError(err) {
					return customerrors.NewUnavailableErr(fmt.Sprintf("Service is temporarily unavailable, please try again later: %v", err.Error()))
				}

				// Check if it's a retryable error (backup policy/vault in updating state)
				var customErr *vsaerrors.CustomError
				if vsaerrors.As(err, &customErr) {
					if customErr.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupPolicy ||
						customErr.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupVault {
						return customerrors.NewUnavailableErr(fmt.Sprintf("Backup policy or vault is currently being updated, please try again later: %v", err.Error()))
					}
				}

				// For all other errors (actual validation failures), return 400
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("Backup policy is not compliant with immutable backup vault settings: %v", err.Error()))
			}
		}

		if params.DataProtection.ScheduledBackupEnabled == nil {
			return customerrors.NewUserInputValidationErr("scheduled backups needs to be enabled/disabled when a backup policy is assigned to a volume")
		}
		if params.IsDataProtection {
			return customerrors.NewUserInputValidationErr("scheduled backups are not supported for cross region replication, only manual backups with existing snapshots are supported")
		}

		// In a VCP-only region (CVP_HOST not set), backup policy must exist in VCP
		backupPolicy, err := se.GetBackupPolicyByUUIDAndOwnerID(ctx, params.DataProtection.BackupPolicyId, pool.Account.ID)
		if err != nil && !customerrors.IsNotFoundErr(err) {
			return err
		}
		if backupPolicy == nil && !utils.IsCVPHostConfigured() {
			util.GetLogger(ctx).Warn("Backup policy does not exist in VCP in a VCP-only region (SDE unavailable); volume creation requires an existing VCP backup policy",
				"backupPolicyId", params.DataProtection.BackupPolicyId)
			return customerrors.NewNotFoundErr(
				fmt.Sprintf("Backup policy %s not found", params.DataProtection.BackupPolicyId),
				nil,
			)
		}
		if backupPolicy != nil && backupPolicy.LifeCycleState != datamodel.LifeCycleStateREADY {
			return customerrors.NewUserInputValidationErr("backup policy is not in ready state, please check the backup policy and try again")
		}
	}

	if !pool.AllowAutoTiering && params.AutoTieringPolicy != nil && (params.AutoTieringPolicy.AutoTieringEnabled || params.AutoTieringPolicy.HotTierBypassModeEnabled) {
		return customerrors.NewUserInputValidationErr("Auto Tiering is not allowed for this volume. Please enable Auto Tiering on the Pool and try again")
	} else if params.AutoTieringPolicy != nil && params.AutoTieringPolicy.AutoTieringEnabled {
		if params.AutoTieringPolicy.CoolingThresholdDays < minCoolingThresholdDays || params.AutoTieringPolicy.CoolingThresholdDays > maxCoolingThresholdDays {
			return customerrors.NewUserInputValidationErr("Auto Tiering Cooling Threshold days must be between 2 and 183 days")
		}
	}

	// Validate HotTierBypassModeEnabled
	if params.AutoTieringPolicy != nil && params.AutoTieringPolicy.HotTierBypassModeEnabled {
		if !params.AutoTieringPolicy.AutoTieringEnabled {
			return customerrors.NewUserInputValidationErr("Hot Tier Bypass Mode can only be enabled when Auto Tiering is enabled on the Volume")
		}
	}

	// Protocol validation based on FileProtocolSupported flag
	if len(params.Protocols) == 0 {
		return customerrors.NewUserInputValidationErr("at least one protocol must be specified")
	}

	// Extract ONTAP version from pool
	ontapVersion := activities.GetOntapVersionFromPool(&pool.Pool)

	// Protocol-specific validation
	validator, err := GetVolumeTypeValidator(params.Protocols, ontapVersion)
	if err != nil {
		return err
	}
	return validator.Validate(ctx, se, params, pool.AccountID)
}

func _validateDeleteVolumeParams(ctx context.Context, se database.Storage, volume *datamodel.Volume) error {
	// Check if backup is in transition state for this volume
	backupTransitionState, err := se.IsBackupInCreatingorDeletingStateByVolume(ctx, volume.UUID)
	if err != nil {
		return err
	}

	if backupTransitionState {
		return customerrors.NewUserInputValidationErr("A backup operation on volume is currently in progress. Please wait for it to complete before deleting the volume")
	}

	// Check if volume is in replication
	replicationCount, err := se.GetVolumeReplicationCountByVolumeID(ctx, volume.ID)
	if err != nil {
		return err
	}

	if replicationCount > 0 {
		return customerrors.NewUserInputValidationErr("Cannot delete volume that has active replication. Please delete the replication first.")
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil {
		cloneState := volume.VolumeAttributes.CloneParentInfo.State
		if cloneState == datamodel.CloneStateSplitting {
			return customerrors.NewConflictErr("Volume deletion is not allowed when the volume is splitting")
		}
	}

	return nil
}

func _convertDatastoreVolumeToModel(volume *datamodel.Volume, ipAddress *[]string) *models.Volume {
	res := &models.Volume{
		BaseModel: models.BaseModel{
			UUID:      volume.UUID,
			CreatedAt: volume.CreatedAt,
			UpdatedAt: volume.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(volume.DeletedAt),
		},
		PoolID:                volume.Pool.UUID,
		PoolName:              volume.Pool.Name,
		AccountName:           volume.Account.Name,
		ServiceLevel:          volume.Pool.ServiceLevel,
		DisplayName:           volume.Name,
		Description:           volume.Description,
		QuotaInBytes:          uint64(volume.SizeInBytes),
		LifeCycleState:        volume.State,
		LifeCycleStateDetails: volume.StateDetails,
		IsDataProtection:      volume.VolumeAttributes.IsDataProtection,
		Mounted:               volume.VolumeAttributes.Mounted,
		Zone:                  volume.Pool.PoolAttributes.PrimaryZone,
		SecondaryZone:         volume.Pool.PoolAttributes.SecondaryZone,
		UsedBytes:             volume.UsedBytes,
		SnapReserve:           volume.VolumeAttributes.SnapReserve,
		SnapshotDirectory:     volume.VolumeAttributes.SnapshotDirectory,
		CloneSharedBytes:      volume.ClonesSharedBytes,
	}
	attributes := volume.VolumeAttributes
	if attributes != nil {
		res.KerberosEnabled = attributes.KerberosEnabled
		res.LdapEnabled = attributes.LdapEnabled
		res.IsRegionalHA = attributes.IsRegionalHA
		res.RestrictedActions = attributes.RestrictedActions
	}
	if volume.Pool != nil {
		if volume.Pool.PoolAttributes != nil {
			res.LdapEnabled = volume.Pool.PoolAttributes.LdapEnabled
			if region, _, err := utils.ParseRegionAndZone(volume.Pool.PoolAttributes.PrimaryZone); err == nil {
				res.Region = region
			}
		}
		if volume.Pool.ActiveDirectory != nil {
			res.ActiveDirectoryConfigId = volume.Pool.ActiveDirectory.UUID
			if volume.Pool.PoolAttributes != nil {
				if region, _, err := utils.ParseRegionAndZone(volume.Pool.PoolAttributes.PrimaryZone); err == nil {
					res.ActiveDirectoryResourceId = fmt.Sprintf("projects/%s/locations/%s/activeDirectories/%s", volume.Account.Name, region, volume.Pool.ActiveDirectory.AdName)
				} else {
					res.ActiveDirectoryResourceId = volume.Pool.ActiveDirectory.AdName
				}
			} else {
				res.ActiveDirectoryResourceId = volume.Pool.ActiveDirectory.AdName
			}
		}
	}
	res.VendorSubnetID = attributes.VendorSubnetID
	res.CreationToken = attributes.CreationToken
	res.ProtocolTypes = attributes.Protocols

	// Volumes in manual pools always have a VPG (invariant). The only distinction is autogen vs non-autogen:
	// - Autogen VPG: expose throughput/iops on the volume (no VPG UUID).
	// - Non-autogen VPG: expose the VPG UUID on the volume (no throughput/iops).
	// Auto pools return none of the three. If we ever see a manual-pool volume without a VPG, something has gone wrong.
	if volume.Pool != nil && volume.Pool.QosType == utils.QosTypeManual {
		if volume.VolumePerformanceGroup != nil {
			if volume.VolumePerformanceGroup.IsAutoGen {
				res.ThroughputMibps = &volume.VolumePerformanceGroup.ThroughputMibps
				res.Iops = &volume.VolumePerformanceGroup.Iops
			} else {
				res.VolumePerformanceGroupId = volume.VolumePerformanceGroup.UUID
			}
		} else {
			log.NewLogger().Error("Invariant violation: volume in manual pool has no VPG", "volumeUUID", volume.UUID, "poolUUID", volume.Pool.UUID)
		}
	}

	if volume.Svm != nil {
		res.SvmName = volume.Svm.Name
	}
	var kmsConfigUUID string
	if volume.Pool.KmsConfig != nil {
		res.KmsConfig = &models.KmsConfig{
			BaseModel: models.BaseModel{
				UUID:      volume.Pool.KmsConfig.UUID,
				CreatedAt: volume.Pool.KmsConfig.CreatedAt,
				UpdatedAt: volume.Pool.KmsConfig.UpdatedAt,
				DeletedAt: utils.DeletedAtOrNil(volume.Pool.KmsConfig.DeletedAt),
			},
			Name:              volume.Pool.KmsConfig.Name,
			Description:       volume.Pool.KmsConfig.Description,
			State:             volume.Pool.KmsConfig.State,
			StateDetails:      volume.Pool.KmsConfig.StateDetails,
			KeyRing:           volume.Pool.KmsConfig.KeyRing,
			KeyRingLocation:   volume.Pool.KmsConfig.KeyRingLocation,
			KeyName:           volume.Pool.KmsConfig.KeyName,
			AccountID:         volume.Pool.KmsConfig.AccountID,
			CustomerProjectID: volume.Pool.KmsConfig.CustomerProjectID,
			KeyProjectID:      volume.Pool.KmsConfig.KeyProjectID,
			ResourceID:        volume.Pool.KmsConfig.ResourceID,
		}
		kmsConfigUUID = volume.Pool.KmsConfig.UUID
	}
	res.EncryptionType = utils.GetEncryptionType(&kmsConfigUUID)
	if attributes.BlockProperties != nil {
		res.BlockProperties = &models.BlockProperties{
			OSType:          attributes.BlockProperties.OSType,
			LunName:         attributes.BlockProperties.LunName,
			LunSerialNumber: attributes.BlockProperties.LunSerialNumber,
			HostGroupDetail: convertHostGroupDetails(attributes.BlockProperties.HostGroupDetails),
		}
	}
	if attributes.BlockDevices != nil {
		blockDevices := make([]models.BlockDevice, 0, len(*attributes.BlockDevices))
		for _, blockDevice := range *attributes.BlockDevices {
			blockDeviceModel := &models.BlockDevice{
				Name:       blockDevice.Name,
				OSType:     blockDevice.OSType,
				Size:       uint64(blockDevice.Size),
				Identifier: blockDevice.Identifier,
			}
			if len(blockDevice.HostGroupDetails) > 0 {
				hostGroups := make([]models.HostGroupDetails, 0, len(blockDevice.HostGroupDetails))
				for _, hg := range blockDevice.HostGroupDetails {
					hostGroups = append(hostGroups, models.HostGroupDetails{
						Hosts:       hg.HostQNs,
						HostGroupID: hg.HostGroupUUID,
					})
				}
				blockDeviceModel.HostGroupDetail = hostGroups
			}
			blockDevices = append(blockDevices, *blockDeviceModel)
		}
		res.BlockDevices = &blockDevices
	}
	labels := make(map[string]string)
	if attributes.Labels != nil {
		labels = utils.ConvertJSONBToMap(attributes.Labels)
	}
	res.Labels = labels
	if volume.DataProtection != nil {
		res.DataProtection = &models.DataProtection{
			BackupVaultID:          volume.DataProtection.BackupVaultID,
			BackupPolicyId:         volume.DataProtection.BackupPolicyID,
			BackupChainBytes:       volume.DataProtection.BackupChainBytes,
			ScheduledBackupEnabled: volume.DataProtection.ScheduledBackupEnabled,
		}
	}

	if ipAddress != nil {
		res.IPAddresses = *ipAddress
	}

	if volume.SnapshotPolicy != nil {
		res.SnapshotPolicy = &models.SnapshotPolicy{
			Name:      volume.SnapshotPolicy.Name,
			IsEnabled: volume.SnapshotPolicy.IsEnabled,
			Comment:   volume.SnapshotPolicy.Comment,
			Schedules: convertToModelSnapshotPolicySchedule(volume.SnapshotPolicy.Schedules),
		}
	}

	if attributes.FileProperties != nil {
		res.FileProperties = &models.FileProperties{
			JunctionPath: attributes.FileProperties.JunctionPath,
		}
		ensureFileProps := func() {
			if res.FileProperties == nil {
				res.FileProperties = &models.FileProperties{}
			}
		}
		if attributes.FileProperties.ExportPolicy != nil {
			exportRules := make([]*models.ExportRule, 0, len(attributes.FileProperties.ExportPolicy.ExportRules))
			for _, rule := range attributes.FileProperties.ExportPolicy.ExportRules {
				exportRules = append(exportRules, &models.ExportRule{
					AllowedClients:      rule.AllowedClients,
					AccessType:          rule.AccessType,
					CIFS:                rule.CIFS,
					NFSv3:               rule.NFSv3,
					NFSv4:               rule.NFSv4,
					UnixReadOnly:        rule.UnixReadOnly,
					UnixReadWrite:       rule.UnixReadWrite,
					Index:               rule.Index,
					ChownMode:           rule.ChownMode,
					AnonymousUser:       rule.AnonymousUser,
					Kerberos5iReadOnly:  rule.Kerberos5iReadOnly,
					Kerberos5iReadWrite: rule.Kerberos5iReadWrite,
					Kerberos5pReadOnly:  rule.Kerberos5pReadOnly,
					Kerberos5pReadWrite: rule.Kerberos5pReadWrite,
					Kerberos5ReadOnly:   rule.Kerberos5ReadOnly,
					Kerberos5ReadWrite:  rule.Kerberos5ReadWrite,
					S3:                  rule.S3,
					Superuser:           rule.Superuser,
					AllSquash:           rule.AllSquash,
					AnonUid:             rule.AnonUid,
				})
			}
			ensureFileProps()
			res.FileProperties.ExportPolicy = &models.ExportPolicy{
				ExportPolicyName: attributes.FileProperties.ExportPolicy.ExportPolicyName,
				ExportRules:      exportRules,
			}
			// Preserve existing junction path if already set; otherwise set from attributes.
			if res.FileProperties.JunctionPath == "" {
				res.FileProperties.JunctionPath = attributes.FileProperties.JunctionPath
			}
		}
		if attributes.FileProperties.Fqdn != "" {
			ensureFileProps()
			res.FileProperties.Fqdn = attributes.FileProperties.Fqdn
		}
		if attributes.FileProperties.SMBShareSettings != nil {
			ensureFileProps()
			res.FileProperties.SMBShareSettings = attributes.FileProperties.SMBShareSettings
		}
		if attributes.FileProperties.SecurityStyle != "" {
			ensureFileProps()
			res.FileProperties.SecurityStyle = securityStyleForAPIResponse(attributes.FileProperties.SecurityStyle)
		}
		if attributes.FileProperties.UnixPermissions != "" {
			ensureFileProps()
			res.FileProperties.UnixPermissions = attributes.FileProperties.UnixPermissions
		}
	}

	// Return AutoTieringPolicy if pool has auto tiering enabled.
	// This ensures volumes created with PAUSED tierAction still return their tieringPolicy
	// when the pool supports auto tiering, regardless of the volume's AutoTieringEnabled state.
	if volume.AutoTieringPolicy != nil && (volume.Pool != nil && volume.Pool.AllowAutoTiering) {
		res.AutoTieringPolicy = &models.AutoTieringPolicy{
			AutoTieringEnabled:       volume.AutoTieringEnabled,
			CoolingThresholdDays:     volume.AutoTieringPolicy.CoolingThresholdDays,
			TieringPolicy:            volume.AutoTieringPolicy.TieringPolicy,
			HotTierBypassModeEnabled: volume.AutoTieringPolicy.HotTierBypassModeEnabled,
		}
		res.HotTierSizeGib = volume.HotTierSizeGib
		res.ColdTierSizeGib = volume.ColdTierSizeGib
	}

	if volume.LargeVolumeAttributes != nil {
		res.LargeCapacity = volume.LargeVolumeAttributes.LargeCapacity
		res.LargeVolumeConstituentCount = volume.LargeVolumeAttributes.LargeVolumeConstituentCount
	}

	if volume.CacheParameters != nil {
		var cacheConfig *models.CacheConfig
		if volume.CacheParameters.CacheConfig != nil {
			cacheConfig = &models.CacheConfig{
				AtimeScrubEnabled:       volume.CacheParameters.CacheConfig.AtimeScrubEnabled,
				AtimeScrubDays:          volume.CacheParameters.CacheConfig.AtimeScrubDays,
				CifsChangeNotifyEnabled: volume.CacheParameters.CacheConfig.CifsChangeNotifyEnabled,
				WritebackEnabled:        volume.CacheParameters.CacheConfig.WritebackEnabled,
				CachePrePopulateState:   volume.CacheParameters.CacheConfig.CachePrePopulateState,
			}

			if volume.CacheParameters.CacheConfig.CachePrePopulate != nil {
				cacheConfig.CachePrePopulate = &models.CachePrePopulate{
					ExcludePathList: volume.CacheParameters.CacheConfig.CachePrePopulate.ExcludePathList,
					PathList:        volume.CacheParameters.CacheConfig.CachePrePopulate.PathList,
					Recursion:       volume.CacheParameters.CacheConfig.CachePrePopulate.Recursion,
				}
			}
		}

		res.CacheParameters = &models.CacheParameters{
			PeerClusterName:       volume.CacheParameters.PeerClusterName,
			PeerSvmName:           volume.CacheParameters.PeerSvmName,
			PeerVolumeName:        volume.CacheParameters.PeerVolumeName,
			PeerIPAddresses:       volume.CacheParameters.PeerIpAddresses,
			EnableGlobalFileLock:  volume.CacheParameters.EnableGlobalFileLock,
			CacheConfig:           cacheConfig,
			CacheState:            volume.CacheParameters.CacheState,
			PreviousCacheState:    volume.CacheParameters.PreviousCacheState,
			CacheStateDetails:     volume.CacheParameters.CacheStateDetails,
			CacheStateDetailsCode: volume.CacheParameters.CacheStateDetailsCode,
			PeerExpiryTime:        volume.CacheParameters.CommandExpiryTime,
			PeeringCommand:        nillable.GetString(volume.CacheParameters.Command, ""),
			Passphrase:            volume.CacheParameters.Passphrase,
		}
	}

	if attributes != nil && attributes.CloneParentInfo != nil {
		var parentVolumeId *string
		var parentSnapshotId *string
		var cloneState *string

		if attributes.CloneParentInfo.ParentVolumeUUID != "" {
			parentVolumeId = &attributes.CloneParentInfo.ParentVolumeUUID
		}
		if attributes.CloneParentInfo.ParentSnapshotUUID != "" {
			parentSnapshotId = &attributes.CloneParentInfo.ParentSnapshotUUID
		}
		if attributes.CloneParentInfo.State != "" {
			cloneState = &attributes.CloneParentInfo.State
		}
		var cloneStateDetails *string
		if attributes.CloneParentInfo.StateDetails != "" {
			cloneStateDetails = &attributes.CloneParentInfo.StateDetails
		}

		res.CloneParentInfo = &models.CloneParentInfo{
			ParentVolumeId:       parentVolumeId,
			ParentSnapshotId:     parentSnapshotId,
			State:                cloneState,
			StateDetails:         cloneStateDetails,
			SplitCompletePercent: attributes.CloneParentInfo.SplitCompletePercent,
		}
	}

	return res
}

// populateVolumeInReplication sets models.Volume.InReplication for getMultipleVolumes only (UI batch path).
func populateVolumeInReplication(ctx context.Context, se database.Storage, dbVolume *datamodel.Volume, modelVol *models.Volume) {
	if se == nil {
		return
	}
	count, err := se.GetVolumeReplicationCountByVolumeID(ctx, dbVolume.ID)
	if err != nil {
		util.GetLogger(ctx).Warn("Error populating inReplication for volume", "volumeID", dbVolume.ID, "error", err)
		return
	}
	modelVol.InReplication = nillable.ToPointer(count > 0)
}

func convertHostGroupDetails(hgs []datamodel.HostGroupDetail) []models.HostGroupDetails {
	resp := make([]models.HostGroupDetails, 0)
	for _, hg := range hgs {
		resp = append(resp, models.HostGroupDetails{
			Hosts:       hg.HostQNs,
			HostGroupID: hg.HostGroupUUID,
		})
	}
	return resp
}

func (o *GCPOrchestrator) DeleteVolume(ctx context.Context, params *common.DeleteVolumeParams) (*models.Volume, string, error) {
	return deleteVolume(ctx, o.storage, o.temporal, params.VolumeUUID)
}

func _deleteVolume(ctx context.Context, se database.Storage, temporal client.Client, volumeId string) (*models.Volume, string, error) {
	logger := util.GetLogger(ctx)
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)

	volume, err := se.GetVolume(ctx, volumeId)
	if err != nil {
		return nil, "", err
	}

	if volume.Pool == nil {
		logger.Warn("Volume's pool is nil (pool likely already deleted), deleting volume directly from database", "volume_id", volume.UUID)
		_, err = se.DeleteVolumeAndChildResources(ctx, volumeId)
		if err != nil {
			if customerrors.IsNotFoundErr(err) {
				logger.Info("Orphan volume already removed from database (concurrent delete), treating delete as success", "volume_id", volume.UUID)
			} else {
				logger.Error("Failed to delete orphaned volume from database", "volume_id", volume.UUID, "error", err)
				return nil, "", err
			}
		}
		return &models.Volume{
			BaseModel: models.BaseModel{
				UUID:      volume.UUID,
				CreatedAt: volume.CreatedAt,
				UpdatedAt: volume.UpdatedAt,
			},
			DisplayName:           volume.Name,
			AccountName:           volume.Account.Name,
			LifeCycleState:        datamodel.LifeCycleStateDeleted,
			LifeCycleStateDetails: datamodel.LifeCycleStateDeletedDetails,
			QuotaInBytes:          uint64(volume.SizeInBytes),
		}, "", nil
	}

	correlationID := utils.GetCoRelationIDFromContext(ctx)
	var existingDeleteJobUUID string
	// Flag to determine if we should use non-sequential execution (for same correlation ID case)
	useNonSequentialExecution := false
	// Determine the correct delete job type based on volume type
	deleteJobType := datamodel.JobTypeDeleteVolume
	createJobType := datamodel.JobTypeCreateVolume
	if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeCapacity {
		deleteJobType = datamodel.JobTypeDeleteLargeVolume
		createJobType = datamodel.JobTypeCreateLargeVolume
	}
	if volume.CacheParameters != nil {
		deleteJobType = datamodel.JobTypeFlexCacheDeleteVolume
		createJobType = datamodel.JobTypeFlexCacheCreateVolume
	}

	if volume.State == datamodel.LifeCycleStateCreating {
		existingDeleteJobUUID, _, err = database.ValidateCorrelationIDForCreatingResource(
			ctx, se, volume.UUID, "volume", createJobType, deleteJobType, logger)
		if err != nil {
			logger.Warnf("Volume %s cannot be deleted: existing create job not present and state is in CREATING", volume.UUID)
			return nil, "", err
		}
		if existingDeleteJobUUID != "" {
			return convertDatastoreVolumeToModel(volume, nil), existingDeleteJobUUID, nil
		}
		useNonSequentialExecution = true
		logger.Infof("Create job found for volume %s with matching correlation ID %s, using non-sequential execution for immediate delete workflow start", volume.UUID, correlationID)
	} else if utils.IsTransitionalState(volume.State) && volume.State != datamodel.LifeCycleStateDeleting {
		logger.Errorf("Volume %s cannot be deleted, while in transitioning state: %s", volume.Name, volume.State)
		return nil, "", customerrors.NewConflictErr(fmt.Sprintf("volume is in transition state and cannot be deleted, state: %s", volume.State))
	}

	existingJobUUID := database.GetExistingDeleteJobForDeletingState(ctx, se, volume.UUID, deleteJobType, logger)
	if existingJobUUID != "" {
		return convertDatastoreVolumeToModel(volume, nil), existingJobUUID, nil
	}

	if sequenceResilienceEnabled {
		activeJobUUID, activeJobErr := database.GetActiveJobForResource(ctx, se, volume.UUID, deleteJobType, logger)
		if activeJobErr != nil {
			return nil, "", activeJobErr
		}
		if activeJobUUID != "" {
			return convertDatastoreVolumeToModel(volume, nil), activeJobUUID, nil
		}
	}

	// Validate delete volume parameters and preconditions
	err = validateDeleteVolumeParams(ctx, se, volume)
	if err != nil {
		return nil, "", err
	}

	// NFS delete-protection is enforced in worker (DeleteVolume workflow activity)
	// to avoid proxy-side ONTAP provider/certificate lookups.
	// Keep API-side checks for non-NFS protocols (e.g., SAN/SMB).
	if enableVolDeleteProtection && (volume.VolumeAttributes == nil || !utils.IsNFSProtocols(volume.VolumeAttributes.Protocols)) {
		if protectionErr := activities.CheckDeleteProtection(ctx, volume, nil, se); protectionErr != nil {
			var deny *vsaerrors.CustomError
			if errors.As(protectionErr, &deny) && deny.TrackingID == vsaerrors.ErrDeleteVolumeRestrictedAction {
				return nil, "", customerrors.NewConflictErrWithTrackingID(deny.GetMessage(), deny.TrackingID)
			}
			return nil, "", protectionErr
		}
	}

	previousState := volume.State
	previousStateDetails := volume.StateDetails
	job := &datamodel.Job{
		Type:          string(deleteJobType),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  volume.Name,
		AccountID:     sql.NullInt64{Int64: volume.Account.ID, Valid: true},
		CorrelationID: correlationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         volume.UUID,
			PoolUUID:             volume.Pool.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	var workflowFunc interface{} = workflows.DeleteVolumeWorkflow
	if volume.CacheParameters != nil {
		workflowFunc = flexcache_workflows.DeleteFlexCacheVolumeWorkflow
		if volume.State == datamodel.LifeCycleStatePreparing {
			useNonSequentialExecution = true
		}
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create volume delete job in database", "error", err)
		return nil, "", err
	}

	// Defer to mark job as error if workflow execution fails
	defer func() {
		if err != nil {
			updateErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error())
			if updateErr != nil {
				logger.Error("Failed to update job state to ERROR", "job_id", createdJob.UUID, "error", updateErr)
			}
		}
	}()

	if volume.State != datamodel.LifeCycleStateCreating {
		err = se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateDeleting,
			"state_details": datamodel.LifeCycleStateDeletingDetails,
		})
		if err != nil {
			logger.Error("Failed to update volume state in database", "error", err)
			return nil, "", err
		}
	}
	// Defer to mark volume as error if workflow execution fails
	defer func() {
		if err != nil {
			volumeUpdateErr := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
				"state":         datamodel.LifeCycleStateError,
				"state_details": datamodel.LifeCycleStateDeletionErrorDetails,
			})
			if volumeUpdateErr != nil {
				logger.Error("Failed to update volume state to ERROR", "volume_id", volume.UUID, "error", volumeUpdateErr)
			}
		}
	}()

	location, err := utils.GetLocationFromVendorID(volume.Pool.VendorID)
	if err != nil {
		logger.Error("Failed to get location from vendor ID: ", "error", err)
		return nil, "", err
	}

	if useNonSequentialExecution {
		// Non-sequential execution: allows delete workflow to start immediately. This enables parallel execution with create workflow for cancellation handling
		taskQueue := workflowengine.CustomerTaskQueue
		err = workflowExecutor.ExecuteWorkflow(
			ctx,
			createdJob.WorkflowID,
			taskQueue,
			workflowFunc,
			nil,
			volume,
		)
	} else {
		controlWorkflowID := workflows.GenerateControlWorkflowID(volume.Account.ID, location, volume.Pool.Name)
		workflowOptions := workflows.DefaultSequentialWorkflowOptions(controlWorkflowID, createdJob.WorkflowID)
		if volume.CacheParameters != nil {
			err = workflowExecutor.ExecuteSequentialWorkflow(ctx, workflowOptions, workflowFunc, volume)
		} else {
			// Pass the UUID to keep the sequencer payload within Temporal's blob size limit.
			err = workflowExecutor.ExecuteSequentialWorkflow(ctx, workflowOptions, workflowFunc, volume.UUID)
		}
	}
	if err != nil {
		logger.Error("Failed to start delete volume workflow: ", "error", err)
		return nil, "", err
	}

	// Check if pool needs scaling based on volume count (async, non-blocking)
	// This happens after volume deletion workflow is triggered successfully
	// Configuration variables
	if enableAutoPoolScaling {
		pool, err := se.GetPool(ctx, volume.Pool.UUID, volume.Account.ID)
		if err != nil {
			return nil, "", err
		}
		dbPool := database.ConvertPoolViewToPool(pool)
		checkAndTriggerPoolScalingIfNeeded(ctx, se, temporal, dbPool, true)
	}

	// If volume is in CREATING state (correlation IDs matched), keep it as CREATING in the response
	// Otherwise, set it to DELETING
	if volume.State != datamodel.LifeCycleStateCreating {
		volume.State = datamodel.LifeCycleStateDeleting
		volume.StateDetails = datamodel.LifeCycleStateDeletingDetails
	}
	return convertDatastoreVolumeToModel(volume, nil), createdJob.UUID, nil
}

func (o *GCPOrchestrator) GetMultipleVolumes(ctx context.Context, volumeIds []string, accountName string) ([]*models.Volume, error) {
	log := util.GetLogger(ctx)
	se := o.storage

	account, err := getOrCreateAccount(ctx, se, accountName)
	if err != nil {
		return nil, err
	}

	conditions := [][]interface{}{{"account_id = ?", account.ID}}
	conditions = append(conditions, []interface{}{"uuid in ?", volumeIds})
	volumes, err2 := se.GetMultipleVolumes(ctx, conditions)
	if err2 != nil {
		return nil, err2
	}
	if len(volumes) == 0 {
		return nil, nil
	}

	ipAddressByVolumeID, ipErr := batchGetIPAddressesForVolumes(ctx, se, volumes)
	if ipErr != nil {
		return nil, ipErr
	}

	volumeUUIDs := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		volumeUUIDs = append(volumeUUIDs, volume.UUID)
	}
	replicatedUUIDs, replErr := se.GetReplicatedVolumeUUIDs(ctx, volumeUUIDs)
	if replErr != nil {
		log.Warn("Error batch fetching replicated volume UUIDs", "error", replErr)
	}
	replicatedUUIDSet := make(map[string]struct{}, len(replicatedUUIDs))
	for _, uuid := range replicatedUUIDs {
		replicatedUUIDSet[uuid] = struct{}{}
	}

	result := make([]*models.Volume, 0, len(volumes))
	for _, volume := range volumes {
		ipAddresses := ipAddressByVolumeID[volume.ID]
		p := convertDatastoreVolumeToModel(volume, &ipAddresses)
		if replErr == nil {
			_, inReplication := replicatedUUIDSet[volume.UUID]
			p.InReplication = nillable.ToPointer(inReplication)
		}
		result = append(result, p)
	}

	foundVolumeIDs := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		foundVolumeIDs = append(foundVolumeIDs, volume.UUID)
	}
	// Detach from the HTTP request context so Temporal trigger retries are not cancelled
	// when the client disconnects or the handler returns.
	refreshCtx := context.WithoutCancel(ctx)
	wfErr := o.TriggerRefreshWorkflow(refreshCtx, account, foundVolumeIDs)
	if wfErr != nil {
		log.Error("Error occurred in TriggerRefreshWorkflow", "error", wfErr.Error())
	}
	return result, nil
}

// GetVolumesByUUIDs returns volumes matching the given UUIDs across all accounts.
func (o *GCPOrchestrator) GetVolumesByUUIDs(ctx context.Context, volumeIds []string, opts common.VolumeFetchOptions) ([]*models.Volume, error) {
	se := o.storage

	var (
		volumes         []*datamodel.Volume
		replicatedUUIDs []string
		fetchVolumesErr error
		replicationErr  error
		wg              sync.WaitGroup
	)

	conditions := [][]interface{}{{"uuid in ?", volumeIds}}
	wg.Add(1)
	go func() {
		defer wg.Done()
		volumes, fetchVolumesErr = se.GetMultipleVolumesSelective(ctx, conditions, database.VolumePreloadOptions{
			ActiveDirectory:        opts.NeedActiveDirectory,
			KmsConfig:              opts.NeedKmsConfig,
			VolumePerformanceGroup: opts.NeedVolumePerformanceGroup,
		})
	}()

	if opts.NeedInReplication {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replicatedUUIDs, replicationErr = se.GetReplicatedVolumeUUIDs(ctx, volumeIds)
		}()
	}

	wg.Wait()

	if fetchVolumesErr != nil {
		return nil, fetchVolumesErr
	}
	if replicationErr != nil {
		return nil, replicationErr
	}

	replicatedUUIDSet := make(map[string]struct{}, len(replicatedUUIDs))
	for _, uuid := range replicatedUUIDs {
		replicatedUUIDSet[uuid] = struct{}{}
	}

	result := make([]*models.Volume, 0, len(volumes))
	for _, volume := range volumes {
		var ipAddresses *[]string

		if opts.NeedIPAddresses {
			fetchedIPAddresses, ipErr := getIPAddressForVolume(ctx, se, volume)
			if ipErr != nil {
				util.GetLogger(ctx).Warnf("Failed to get IP addresses for volume %s: %v", volume.UUID, ipErr)
			} else {
				ipAddresses = &fetchedIPAddresses
			}
		}

		modelVolume := convertDatastoreVolumeToModel(volume, ipAddresses)
		if opts.NeedInReplication {
			_, ok := replicatedUUIDSet[volume.UUID]
			modelVolume.InReplication = nillable.ToPointer(ok)
		}
		result = append(result, modelVolume)
	}

	return result, nil
}

func (o *GCPOrchestrator) UpdateVolumeV2(ctx context.Context, param *common.UpdateVolumeParams) (*models.Volume, string, error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	dbVolume, err := se.GetVolume(ctx, param.VolumeId)
	if err != nil {
		return nil, "", err
	}

	isReplication := false
	volumeReplication, err := se.GetVolumeReplicationByVolumeID(ctx, dbVolume.ID)
	if err != nil {
		// If replication doesn't exist, it's not an error - just means no replication
		if !customerrors.IsNotFoundErr(err) {
			logger.Error("Failed to get volume replication", "error", err)
			return nil, "", err
		}
		logger.Debug("No volume replication found during volume update", "volumeID", dbVolume.ID)
	} else if volumeReplication != nil {
		// If it's a hybrid replication, set isReplication to false
		if volumeReplication.HybridReplicationAttributes != nil {
			isReplication = false
		} else {
			isReplication = true
		}
	}

	return updateVolume(ctx, se, o.temporal, param, isReplication)
}

// UpdateVolume updates the specified volume with the new parameters
func (o *GCPOrchestrator) UpdateVolume(ctx context.Context, param *common.UpdateVolumeParams) (*models.Volume, string, error) {
	return updateVolume(ctx, o.storage, o.temporal, param, false)
}

func chunkVolumeIDs(volumeIDs []string, chunkSize int) [][]string {
	if chunkSize <= 0 {
		chunkSize = 50
	}
	if len(volumeIDs) == 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(volumeIDs)+chunkSize-1)/chunkSize)
	for i := 0; i < len(volumeIDs); i += chunkSize {
		end := i + chunkSize
		if end > len(volumeIDs) {
			end = len(volumeIDs)
		}
		chunks = append(chunks, volumeIDs[i:end])
	}
	return chunks
}

func (o *GCPOrchestrator) TriggerRefreshWorkflow(ctx context.Context, account *datamodel.Account, volumeIDs []string) error {
	log := util.GetLogger(ctx)
	if len(volumeIDs) == 0 {
		log.Info("No volumes provided for refresh workflow")
		return nil
	}
	if account == nil {
		return fmt.Errorf("account is required to trigger volume refresh workflow")
	}

	if account.AccountMetadata != nil && !account.AccountMetadata.VolumeRefreshWorkflowLastCompletionAt.IsZero() {
		lastCompletionTime := account.AccountMetadata.VolumeRefreshWorkflowLastCompletionAt
		timeSinceCompletion := time.Now().Sub(lastCompletionTime)

		if timeSinceCompletion <= time.Duration(volumeRefreshIntervalMinutes)*time.Minute {
			log.Debugf("Skipping VolumeRefreshWorkflow execution for account %s due to recent completion at %v (%.1f minutes ago, interval: %d minutes)",
				account.Name, lastCompletionTime, timeSinceCompletion.Minutes(), volumeRefreshIntervalMinutes)
			return nil
		}

		log.Debugf("Last VolumeRefreshWorkflow completion for account %s was at %v (%.1f minutes ago), triggering new execution",
			account.Name, lastCompletionTime, timeSinceCompletion.Minutes())
	} else {
		log.Debugf("No previous VolumeRefreshWorkflow completion timestamp found for account %s, triggering new execution",
			account.Name)
	}

	workflowExecutor := workflows.NewWorkflowExecutor(o.temporal, log)
	reusePolicy := enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	batches := chunkVolumeIDs(volumeIDs, volumeRefreshBatchSize)
	log.Infof("Triggering %d VolumeRefreshWorkflow batch(es) for account %s (%d volume IDs, batch size %d)",
		len(batches), account.Name, len(volumeIDs), volumeRefreshBatchSize)

	var batchErrors []error
	for batchIndex, batchVolumeIDs := range batches {
		workflowID := fmt.Sprintf("VolumeRefreshWorkflow_AccountId_%s_Batch_%d", account.UUID, batchIndex)
		err := workflowExecutor.ExecuteWorkflowWithRetry(
			ctx,
			workflowID,
			workflowengine.BackgroundTaskQueue,
			workflows.VolumeRefreshWorkflow,
			workflowengine.GetVolumeRefreshWorkflowTimeout(),
			&reusePolicy,
			workflows.VolumeRefreshWorkflowInput{
				AccountUUID: account.UUID,
				AccountName: account.Name,
				AccountID:   account.ID,
				VolumeIDs:   batchVolumeIDs,
			},
		)
		if err != nil {
			log.Errorf("Volume refresh workflow batch %d failed: %v", batchIndex, err)
			batchErrors = append(batchErrors, fmt.Errorf("volume refresh workflow batch %d failed: %w", batchIndex, err))
			continue
		}
	}

	return errors.Join(batchErrors...)
}

// validateReattachVaultMatchesDetachedServiceTypes ensures that when re-attaching a vault after
// detach while available backups still reference prior vault rows, the attach vault matches the
// backup vault family implied by existing backups.
func validateReattachVaultMatchesDetachedServiceTypes(distinctServiceTypes []string, attachVault *datamodel.BackupVault) error {
	if attachVault == nil {
		return nil
	}
	if len(distinctServiceTypes) == 0 {
		return customerrors.NewUserInputValidationErr("Could not resolve backup vault type for existing volume backups; delete stale backups before attaching a backup vault.")
	}
	if len(distinctServiceTypes) > 1 {
		return customerrors.NewUserInputValidationErr("Existing backups reference both CrossProject and GCNV backup vaults; resolve backup data before attaching a backup vault.")
	}
	st := distinctServiceTypes[0]
	if st != attachVault.ServiceType {
		if st == activities.GCBDRServiceType {
			return customerrors.NewUserInputValidationErr("Existing backups are from a CrossProject backup vault; attach a CrossProject backup vault only.")
		}
		return customerrors.NewUserInputValidationErr("Existing backups are from a GCNV backup vault; attach a GCNV backup vault only.")
	}
	return nil
}

// validateDetachedBackupsAllowAttachVault enforces that when attaching a backup vault to a volume,
// any existing volume backups (e.g. after detach) allow a matching attach vault type for the prior backup family.
func validateDetachedBackupsAllowAttachVault(ctx context.Context, se database.Storage, volumeUUID string, attachVault *datamodel.BackupVault) error {
	if attachVault == nil {
		return nil
	}
	vaultIDs, err := se.GetDistinctBackupVaultIDsByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		return err
	}
	if len(vaultIDs) > 0 {
		serviceTypes, errSvc := se.GetDistinctBackupVaultServiceTypesByVaultIDs(ctx, vaultIDs)
		if errSvc != nil {
			return errSvc
		}
		return validateReattachVaultMatchesDetachedServiceTypes(serviceTypes, attachVault)
	}
	return nil
}

// validateDetachedBackupsAllowAttachVaultLegacy enforces pre-GCNV-vault-switching re-attach rules:
// only GCBDR vaults may be attached when detached backups still exist on the volume.
func validateDetachedBackupsAllowAttachVaultLegacy(ctx context.Context, se database.Storage, volumeUUID string, attachVault *datamodel.BackupVault) error {
	if attachVault == nil {
		return nil
	}
	vaultIDs, err := se.GetDistinctBackupVaultIDsByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		return err
	}
	if len(vaultIDs) > 0 && attachVault.ServiceType != activities.GCBDRServiceType {
		return customerrors.NewUserInputValidationErr("Cannot attach a non-cross project backup vault while the volume has existing backups from a detached backup vault. Delete those backups first, or attach a cross project backup vault.")
	}
	return nil
}

func validateReattachVaultAfterDetach(ctx context.Context, se database.Storage, volumeUUID string, attachVault *datamodel.BackupVault) error {
	if utils.EnableGCNVVaultSwitching {
		return validateDetachedBackupsAllowAttachVault(ctx, se, volumeUUID, attachVault)
	}
	return validateDetachedBackupsAllowAttachVaultLegacy(ctx, se, volumeUUID, attachVault)
}

// validateBackupVaultChangeLegacy enforces backup-vault detach/switch rules before GCNV vault switching.
func validateBackupVaultChangeLegacy(
	currentVault, newVault *datamodel.BackupVault,
	dbVolume *datamodel.Volume,
	removingVault bool,
	distinctVaultIDsWithBackups []int64,
) error {
	if removingVault {
		if currentVault != nil && currentVault.ServiceType != activities.GCBDRServiceType && len(distinctVaultIDsWithBackups) > 0 {
			return customerrors.NewUserInputValidationErr("cannot remove backup vault as there are backups associated with it")
		}
		return nil
	}

	volumeHasBackups := len(distinctVaultIDsWithBackups) > 0
	if volumeHasBackups {
		if currentVault != nil && currentVault.ServiceType != activities.GCBDRServiceType {
			return customerrors.NewUserInputValidationErr("Backup vault switching is only allowed for cross project backup vaults. The current backup vault is not a cross project vault.")
		}
		if newVault != nil && newVault.ServiceType != activities.GCBDRServiceType {
			if newVault.AccountID != dbVolume.AccountID {
				return customerrors.NewUserInputValidationErr("The target backup vault belongs to a different account and cannot be associated with the volume")
			}
			return customerrors.NewUserInputValidationErr("Backup vault switching is only allowed between cross project backup vaults. The target backup vault is not a cross project vault.")
		}
	} else if newVault != nil && newVault.ServiceType != activities.GCBDRServiceType && newVault.AccountID != dbVolume.AccountID {
		return customerrors.NewUserInputValidationErr("The target backup vault belongs to a different account and cannot be associated with the volume")
	}
	return nil
}

// validateBackupVaultFamilySwitch enforces backup-vault switches that do not cross
// GCBDR (CrossProject) and GCNV families when the volume still has backups in another vault.
// With no such backups, switching across families is allowed.
func validateBackupVaultFamilySwitch(currentVault, newVault *datamodel.BackupVault, volumeHasBackups bool) error {
	if currentVault == nil || newVault == nil {
		return nil
	}
	cur := currentVault.ServiceType
	newSt := newVault.ServiceType
	curGCBDR := cur == activities.GCBDRServiceType
	curGCNV := cur == datamodel.ServiceTypeGCNV
	newGCBDR := newSt == activities.GCBDRServiceType
	newGCNV := newSt == datamodel.ServiceTypeGCNV

	sameFamily := (curGCBDR && newGCBDR) || (curGCNV && newGCNV)
	if volumeHasBackups && !sameFamily {
		return customerrors.NewUserInputValidationErr(fmt.Sprintf("Cannot switch from a %s backup vault to a %s backup vault.", cur, newSt))
	}
	return nil
}

func _updateVolume(ctx context.Context, se database.Storage, temporal client.Client, params *common.UpdateVolumeParams, isReplication bool) (*models.Volume, string, error) {
	logger := util.GetLogger(ctx)
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)

	dbVolume, err := se.GetVolume(ctx, params.VolumeId)
	if err != nil {
		return nil, "", err
	}
	params.PoolID = dbVolume.Pool.UUID // In update volume we don't get Pool UUID from request, Set pool ID for backwards compatibility

	if params.DataProtection != nil {
		if dbVolume.DataProtection == nil {
			dbVolume.DataProtection = &datamodel.DataProtection{}
		}

		// If backup vault is already attached to the volume and the backup vault is changed or removed
		if dbVolume.DataProtection != nil && dbVolume.DataProtection.BackupVaultID != "" && params.DataProtection.BackupVaultID != nil && (*params.DataProtection.BackupVaultID == "" || *params.DataProtection.BackupVaultID != dbVolume.DataProtection.BackupVaultID) {
			filters := [][]interface{}{{"volume_uuid = ?", dbVolume.UUID}}
			var backups []*datamodel.Backup
			if utils.EnableBackupVaultSwitching {
				backupInProgress, err := se.IsBackupInCreatingorDeletingStateByVolume(ctx, dbVolume.UUID)
				if err != nil {
					return nil, "", err
				}
				if backupInProgress {
					return nil, "", customerrors.NewUserInputValidationErr("A backup operation on the volume is currently in progress. Please wait for it to complete before changing or removing the backup vault")
				}
				// Vault change/remove: one volume-wide query for any vault with available backups (detach + switch).
				currentVault, err := se.GetBackupVault(ctx, dbVolume.DataProtection.BackupVaultID)
				if err != nil && !customerrors.IsNotFoundErr(err) {
					return nil, "", err
				}
				distinctVaultIDsWithBackups, errDistinct := se.GetDistinctBackupVaultIDsByVolumeUUID(ctx, dbVolume.UUID)
				if errDistinct != nil {
					return nil, "", errDistinct
				}
				removingVault := *params.DataProtection.BackupVaultID == ""
				serviceType := ""
				if currentVault != nil {
					serviceType = currentVault.ServiceType
				}
				if removingVault {
					logger.Infof("Removing backup vault from volume %s. Current vault service type: %s, distinct backup vaults with available backups: %d", dbVolume.UUID, serviceType, len(distinctVaultIDsWithBackups))
					if dbVolume.DataProtection.BackupPolicyID != "" {
						return nil, "", customerrors.NewUserInputValidationErr("Cannot remove backup vault while a backup policy is attached. Detach the backup policy first, then remove the backup vault.")
					}
				} else {
					logger.Infof("Switching backup vault for volume %s. Distinct vaults with available backups: %d", dbVolume.UUID, len(distinctVaultIDsWithBackups))
				}

				var newVault *datamodel.BackupVault
				if !removingVault {
					newVault, err = se.GetBackupVault(ctx, *params.DataProtection.BackupVaultID)
					if err != nil && !customerrors.IsNotFoundErr(err) {
						return nil, "", err
					}
				}

				if utils.EnableGCNVVaultSwitching {
					// GCNV vault switching: same-family switch/detach with backups; cross-family only without backups.
					if !removingVault {
						volumeHasBackups := len(distinctVaultIDsWithBackups) > 0
						if err := validateBackupVaultFamilySwitch(currentVault, newVault, volumeHasBackups); err != nil {
							return nil, "", err
						}
						if newVault != nil && newVault.ServiceType == datamodel.ServiceTypeGCNV && newVault.AccountID != dbVolume.AccountID {
							return nil, "", customerrors.NewUserInputValidationErr("The target backup vault belongs to a different account and cannot be associated with the volume")
						}
					}
				} else if err := validateBackupVaultChangeLegacy(currentVault, newVault, dbVolume, removingVault, distinctVaultIDsWithBackups); err != nil {
					return nil, "", err
				}
			} else {
				// Flag off: block when backup policy is attached (change or remove)
				if dbVolume.DataProtection.BackupPolicyID != "" {
					return nil, "", customerrors.NewUserInputValidationErr("cannot remove backup vault as backup policy is associated to the volume")
				}
				backups, err = se.GetBackupsByBackupVaultOwnerIDAndFilter(ctx, dbVolume.DataProtection.BackupVaultID, dbVolume.Account.ID, filters)
				if err != nil {
					return nil, "", err
				}
			}
			// When flag is enabled, allow both switch and detach when backups exist (policy blocks detach above when needed).
			// When flag is disabled, keep current behaviour (block change or remove when backups exist).
			if len(backups) > 0 && !utils.EnableBackupVaultSwitching {
				return nil, "", customerrors.NewUserInputValidationErr("cannot remove backup vault as there are backups associated with it")
			}
			// When flag is on, do not set BackupVaultID here; the volume update workflow will delete
			// snapmirror for the current vault, then set and persist the new BackupVaultID.
			if !utils.EnableBackupVaultSwitching {
				dbVolume.DataProtection.BackupVaultID = *params.DataProtection.BackupVaultID
			}
			// Apply other DataProtection params (policy, schedule, KMS) so they are not dropped on vault change/remove.
			dbVolume.DataProtection.BackupPolicyID = nillable.GetString(params.DataProtection.BackupPolicyId, dbVolume.DataProtection.BackupPolicyID)
			dbVolume.DataProtection.ScheduledBackupEnabled = params.DataProtection.ScheduledBackupEnabled
			dbVolume.DataProtection.KmsGrant = params.DataProtection.KmsGrant
			if dbVolume.DataProtection.BackupPolicyID != "" && params.DataProtection.ScheduledBackupEnabled == nil {
				return nil, "", customerrors.NewUserInputValidationErr("scheduled backups needs to be enabled/disabled when a backup policy is assigned to a volume")
			}
		} else {
			noVaultAttachedPrior := dbVolume.DataProtection == nil || nillable.IsNilOrEmpty(&dbVolume.DataProtection.BackupVaultID)
			if dbVolume.DataProtection == nil {
				dbVolume.DataProtection = &datamodel.DataProtection{}
			}

			if params.DataProtection.BackupVaultID != nil && *params.DataProtection.BackupVaultID != "" {
				incomingVault, errVault := se.GetBackupVault(ctx, *params.DataProtection.BackupVaultID)
				if errVault != nil && !customerrors.IsNotFoundErr(errVault) {
					return nil, "", errVault
				}
				if incomingVault != nil && incomingVault.ServiceType == datamodel.ServiceTypeGCNV && incomingVault.AccountID != dbVolume.AccountID {
					return nil, "", customerrors.NewUserInputValidationErr("The target backup vault belongs to a different account and cannot be associated with the volume")
				}
				if utils.EnableBackupVaultSwitching && noVaultAttachedPrior {
					if err := validateReattachVaultAfterDetach(ctx, se, dbVolume.UUID, incomingVault); err != nil {
						return nil, "", err
					}
				}
			}

			dbVolume.DataProtection.BackupVaultID = nillable.GetString(params.DataProtection.BackupVaultID, dbVolume.DataProtection.BackupVaultID)
			dbVolume.DataProtection.BackupPolicyID = nillable.GetString(params.DataProtection.BackupPolicyId, dbVolume.DataProtection.BackupPolicyID)
			dbVolume.DataProtection.ScheduledBackupEnabled = params.DataProtection.ScheduledBackupEnabled
			dbVolume.DataProtection.KmsGrant = params.DataProtection.KmsGrant

			if dbVolume.DataProtection.BackupVaultID == "" && !nillable.IsNilOrEmpty(params.DataProtection.BackupPolicyId) {
				return nil, "", customerrors.NewUserInputValidationErr("backup vault is required to assign a backup policy to a volume")
			}
			if dbVolume.DataProtection.BackupPolicyID != "" && params.DataProtection.ScheduledBackupEnabled == nil {
				return nil, "", customerrors.NewUserInputValidationErr("scheduled backups needs to be enabled/disabled when a backup policy is assigned to a volume")
			}
		}
	}

	if params.Labels != nil && dbVolume.VolumeAttributes != nil {
		dbVolume.VolumeAttributes.Labels = params.Labels
	}

	if params.SnapshotDirectoryAccess != nil {
		dbVolume.VolumeAttributes.SnapshotDirectory = *params.SnapshotDirectoryAccess
	}

	// @TODO: Implement CIFSAccessBasedEnumeration check when implementing security style

	pool, err := se.GetPool(ctx, params.PoolID, dbVolume.AccountID)
	if err != nil {
		return nil, "", err
	}
	logger.Debugf("Pool details: UUID: %s, AccountID: %d, SizeBytes: %d, QuotaBytes: %d, VolumeCount: %d, Throughput: %f, Iops: %d", pool.UUID, pool.AccountID, pool.SizeInBytes, pool.QuotaInBytes, pool.VolumeCount, pool.Throughput, pool.Iops)

	// Handle Inferred IOPs now that pool information is available as well
	// Only assign param.ThroughputMibps/param.Iops for throughput-based updates, not when volumePerformanceGroupId is set
	if enableInferredIops && params.ThroughputMibps != nil && params.Iops == nil && params.VolumePerformanceGroupId == nil {
		totalThroughputMibps := int64(pool.Throughput)
		totalIops := int64(pool.Iops)
		if pool.PoolAttributes != nil {
			if pool.PoolAttributes.ThroughputMibps > 0 {
				totalThroughputMibps = pool.PoolAttributes.ThroughputMibps
			}
			if pool.PoolAttributes.Iops > 0 {
				totalIops = pool.PoolAttributes.Iops
			}
		}
		params.Iops = nillable.ToPointer(calculateIopsFromThroughput(*params.ThroughputMibps, totalThroughputMibps, totalIops))
	}
	err = validateUpdateVolumeRequest(ctx, se, dbVolume, params, pool)
	if err != nil {
		return nil, "", err
	}

	previousState := dbVolume.State
	previousStateDetails := dbVolume.StateDetails
	job := &datamodel.Job{
		Type:         string(datamodel.JobTypeUpdateVolume),
		State:        string(datamodel.JobsStateNEW),
		ResourceName: dbVolume.Name,
		AccountID:    sql.NullInt64{Int64: dbVolume.AccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         dbVolume.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	wf := workflows.UpdateVolumeWorkflow
	if isReplication {
		job.Type = string(datamodel.JobTypeUpdateVolumeInReplication)
		wf = workflows.UpdateVolumeInReplicationWorkflow
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create volume update job in database", "error", err)
		return nil, "", err
	}

	// Store the original dbVolume for use in defer function to avoid nil pointer issues
	originalDBVolume := dbVolume

	// Defer to mark job as error if workflow execution fails
	defer func() {
		if err != nil && createdJob != nil {
			updateErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error())
			if updateErr != nil {
				logger.Error("Failed to update job state to ERROR", "job_id", createdJob.UUID, "error", updateErr)
			}
		}
	}()

	if params.SnapshotPolicy != nil {
		params.SnapshotPolicy.Name = dbVolume.Name
	}

	if params.SMBShareSettings != nil {
		if dbVolume.VolumeAttributes.FileProperties == nil {
			dbVolume.VolumeAttributes.FileProperties = &datamodel.FileProperties{}
		}
		dbVolume.VolumeAttributes.FileProperties.SMBShareSettings = params.SMBShareSettings
	}

	if params.FileProperties != nil {
		if dbVolume.VolumeAttributes.FileProperties == nil {
			dbVolume.VolumeAttributes.FileProperties = &datamodel.FileProperties{}
		}
		dbVolume.VolumeAttributes.FileProperties.UnixPermissions = params.FileProperties.UnixPermissions
	}

	dbVolume, err = updateVolumeStatus(ctx, se, dbVolume, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails)
	if err != nil {
		logger.Error("Failed to update volume state in database", "error", err)
		return nil, "", err
	}

	defer func() {
		if err != nil && createdJob != nil {
			volumeUpdateErr := se.UpdateVolumeFields(ctx, originalDBVolume.UUID, map[string]interface{}{
				"state":         datamodel.LifeCycleStateError,
				"state_details": datamodel.LifeCycleStateUpdateErrorDetails,
			})
			if volumeUpdateErr != nil {
				logger.Error("Failed to update volume state to ERROR", "volume_id", originalDBVolume.UUID, "error", volumeUpdateErr)
			}
		}
	}()

	// UpdateVolumeWorkflow uses a safety-net WorkflowRunTimeout (2× operation budget) so
	// the in-workflow operation budget drives rollback before Temporal's hard kill.
	// Replication workflows keep the nil fallback (global timeout).
	runTimeout := workflowengine.GetUpdateVolumeWorkflowRunTimeout()
	if isReplication {
		runTimeout = nil
	}
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		wf,
		runTimeout,
		params,
		dbVolume,
	)
	if err != nil {
		logger.Error("Failed to start update volume workflow: ", "error", err)
		return nil, "", err
	}
	dbVolume = updateLatestTieringInformationToVolumeResponse(dbVolume, params)

	return convertDatastoreVolumeToModel(dbVolume, nil), createdJob.UUID, nil
}

func updateLatestTieringInformationToVolumeResponse(dbVolume *datamodel.Volume, updateParams *common.UpdateVolumeParams) *datamodel.Volume {
	updatedDBVolume := dbVolume
	if updateParams != nil && updateParams.AutoTieringPolicy != nil {
		if updatedDBVolume.AutoTieringPolicy == nil {
			updatedDBVolume.AutoTieringPolicy = &datamodel.AutoTieringPolicy{}
		}
		updatedDBVolume.AutoTieringEnabled = updateParams.AutoTieringPolicy.AutoTieringEnabled
		updatedDBVolume.AutoTieringPolicy.TieringPolicy = updateParams.AutoTieringPolicy.TieringPolicy
		updatedDBVolume.AutoTieringPolicy.CoolingThresholdDays = updateParams.AutoTieringPolicy.CoolingThresholdDays
		updatedDBVolume.AutoTieringPolicy.HotTierBypassModeEnabled = updateParams.AutoTieringPolicy.HotTierBypassModeEnabled
	}

	return updatedDBVolume
}

func _updateVolumeStatus(ctx context.Context, se database.Storage, dbVolume *datamodel.Volume, state string, stateDetails string) (*datamodel.Volume, error) {
	err := se.UpdateVolumeFields(ctx, dbVolume.UUID, map[string]interface{}{
		"state":         state,
		"state_details": stateDetails,
	})
	if err != nil {
		return nil, err
	}
	dbVolume.State = state
	dbVolume.StateDetails = stateDetails
	return dbVolume, err
}

// validateUpdateVolumeRequest validates update parameters for an existing volume.
func validateUpdateVolumeRequest(ctx context.Context, se database.Storage, volume *datamodel.Volume, params *common.UpdateVolumeParams, pool *datamodel.PoolView) error {
	log := util.GetLogger(ctx)

	if params.LargeCapacity != nil && (pool.LargeCapacity != *params.LargeCapacity) {
		return customerrors.NewUserInputValidationErr("Given large capacity value is not supported. Large capacity cannot be changed for existing volume")
	}

	if params.LargeVolumeConstituentCount != nil {
		// Check if the volume has existing LargeVolumeConstituentCount
		if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeVolumeConstituentCount != nil {
			// Only return error if the provided value is different from the existing value
			if *params.LargeVolumeConstituentCount != *volume.LargeVolumeAttributes.LargeVolumeConstituentCount {
				return customerrors.NewUserInputValidationErr("Updating large volume constituent count is not supported")
			}
		} else {
			// If volume doesn't have a constituent count, we don't allow setting it during update
			return customerrors.NewUserInputValidationErr("Updating large volume constituent count is not supported")
		}
	}

	if volume.State == datamodel.LifeCycleStateUpdating {
		return customerrors.NewConflictErr("An update operation is already in progress for this volume")
	} else if utils.IsTransitionalState(volume.State) {
		return customerrors.NewUserInputValidationErr(fmt.Sprintf("Volume %s cannot be updated, while in transitioning state: %s", volume.Name, volume.State))
	}

	// Greater than 0 means the value was provided in the request
	if params.QuotaInBytes > 0 {
		if params.QuotaInBytes < volume.SizeInBytes {
			if volume.VolumeAttributes != nil && len(volume.VolumeAttributes.Protocols) > 0 && utils.IsSANProtocol(volume.VolumeAttributes.Protocols[0]) {
				return customerrors.NewUserInputValidationErr("volume size cannot be reduced")
			}
		}
		// Calculate the size increase
		sizeIncrease := params.QuotaInBytes - volume.SizeInBytes

		log.Debugf("Current Volume Size: %d, New Volume Size: %d, Size Increase: %d, Pool Size: %d, Pool Quota: %d, Clones Shared Bytes: %d", volume.SizeInBytes, params.QuotaInBytes, sizeIncrease, pool.SizeInBytes, pool.QuotaInBytes, volume.ClonesSharedBytes)

		// Check if adding the increase to current pool usage exceeds pool size
		if sizeIncrease > 0 && pool.QuotaInBytes+uint64(sizeIncrease)-volume.ClonesSharedBytes > uint64(pool.SizeInBytes) {
			return customerrors.NewUserInputValidationErr("Total size of volumes in a pool cannot exceed the pool capacity.")
		}

		// Large capacity quota validation
		if pool.LargeCapacity {
			maxLVVolSize := utils.MaxLvHotTierCapacity
			// For auto tiering enabled pool and volume, max size is 20 PiB
			if (params.AutoTieringPolicy != nil && params.AutoTieringPolicy.AutoTieringEnabled) || (volume.AutoTieringEnabled) {
				maxLVVolSize = utils.MaxQuotaInBytesLargeVolume
			}
			if uint64(params.QuotaInBytes) < utils.MinQuotaInBytesLargeVolumeWithCV || uint64(params.QuotaInBytes) > maxLVVolSize {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("Invalid volume capacity %s. Must be between %s and %s.",
					utils.FmtUint64Bytes(uint64(params.QuotaInBytes)), utils.FmtUint64Bytes(utils.MinQuotaInBytesLargeVolumeWithCV),
					utils.FmtUint64Bytes(utils.MaxQuotaInBytesLargeVolume)))
			}

			// Validate CV size stays within limits for the update flow. The requested
			// quota is spread across the volume's existing constituent volumes, so a
			// resize can push the per-CV size below the minimum or above the maximum.
			var cvSizeInBytes uint64
			var cvCount int64
			if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeVolumeConstituentCount != nil {
				// Use existing CV count from volume
				cvCount = int64(*volume.LargeVolumeAttributes.LargeVolumeConstituentCount)
				cvSizeInBytes = uint64(params.QuotaInBytes) / uint64(cvCount)

				// Enforce the minimum CV size, mirroring create-time validation.
				// Without this check an update can pass VCP validation but fail later
				// in ONTAP with a "size is too small" internal error.
				if cvSizeInBytes < minCVSizeInBytes {
					return customerrors.NewUserInputValidationErr(fmt.Sprintf("Constituent volume size cannot be less than %s. Current CV size is %s with %d constituent volumes",
						utils.FmtUint64Bytes(minCVSizeInBytes), utils.FmtUint64Bytes(cvSizeInBytes), cvCount))
				}
			}

			if cvSizeInBytes > maxCVSizeInBytes {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("Constituent volume size cannot be more than %s. Current CV size is %s with %d constituent volumes",
					utils.FmtUint64Bytes(maxCVSizeInBytes), utils.FmtUint64Bytes(cvSizeInBytes), cvCount))
			}
		} else {
			maxQuotaInBytes := maxQuotaInBytesVolume
			if volume.VolumeAttributes != nil {
				maxQuotaInBytes = factorycommon.GetMaxQuotaForVolume(volume.VolumeAttributes.Protocols)
			}
			if uint64(params.QuotaInBytes) < minQuotaInBytesVolume || uint64(params.QuotaInBytes) > maxQuotaInBytes {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("Invalid volume capacity %s. Must be between %s and %s.",
					utils.FmtUint64Bytes(uint64(params.QuotaInBytes)), utils.FmtUint64Bytes(minQuotaInBytesVolume),
					utils.FmtUint64Bytes(maxQuotaInBytes)))
			}
		}
	}

	// Large capacity validations
	if pool.LargeCapacity {
		// BlockDevices are not supported for large capacity volumes
		if params.BlockProperties != nil {
			return customerrors.NewUserInputValidationErr("BlockProperties are not supported for large capacity volumes")
		}
		if len(params.BlockDevices) > 0 {
			return customerrors.NewUserInputValidationErr("BlockDevices are not supported for large capacity volumes")
		}
	}

	if !pool.AllowAutoTiering && params.AutoTieringPolicy != nil && (params.AutoTieringPolicy.AutoTieringEnabled || params.AutoTieringPolicy.HotTierBypassModeEnabled) {
		return customerrors.NewUserInputValidationErr("Auto Tiering is not allowed for this volume. Please enable Auto Tiering on the Pool and try again")
	} else if params.AutoTieringPolicy != nil && params.AutoTieringPolicy.AutoTieringEnabled {
		if params.AutoTieringPolicy.CoolingThresholdDays < minCoolingThresholdDays || params.AutoTieringPolicy.CoolingThresholdDays > maxCoolingThresholdDays {
			return customerrors.NewUserInputValidationErr("Auto Tiering Cooling Threshold days must be between 2 and 183 days")
		}
	}

	// Validate HotTierBypassModeEnabled for update
	if params.AutoTieringPolicy != nil && params.AutoTieringPolicy.HotTierBypassModeEnabled {
		if !params.AutoTieringPolicy.AutoTieringEnabled {
			return customerrors.NewUserInputValidationErr("Hot Tier Bypass Mode can only be enabled when Auto Tiering is enabled on the Volume")
		}
	}

	if len(params.BlockDevices) > 0 {
		// Find the corresponding BlockDevice in the volume by LUN name
		var matchingBlockDevice *common.BlockDevice

		// Check if volume has BlockDevices
		if volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
			// Try to find matching BlockDevice by name
			for _, paramBlockDevice := range params.BlockDevices {
				if paramBlockDevice.Name != "" {
					for _, volBlockDevice := range *volume.VolumeAttributes.BlockDevices {
						if volBlockDevice.Name == paramBlockDevice.Name {
							matchingBlockDevice = paramBlockDevice
							// Validate that OSType cannot be changed
							if paramBlockDevice.OSType != "" && paramBlockDevice.OSType != volBlockDevice.OSType {
								return customerrors.NewUserInputValidationErr("Cannot update OSType for block device. OSType is immutable after creation")
							}

							// assign the read-only properties from the volume's BlockDevice
							matchingBlockDevice.SizeInBytes = volBlockDevice.Size
							matchingBlockDevice.OSType = volBlockDevice.OSType
							matchingBlockDevice.LunSerialNumber = volBlockDevice.Identifier
							matchingBlockDevice.LunUUID = volBlockDevice.LunUUID
							break
						}
					}
					if matchingBlockDevice != nil {
						break
					}
				}
			}
		}
		if matchingBlockDevice == nil {
			return customerrors.NewUserInputValidationErr("could not find matching BlockDevice.")
		}
		hostGroupUUIDs := matchingBlockDevice.HostGroups
		err := validateBlockProperties(ctx, se, hostGroupUUIDs, volume.Account.ID)
		if err != nil {
			return err
		}
	} else if params.BlockProperties != nil {
		hostGroupUUIDs := params.BlockProperties.HostGroupUUIDs
		err := validateBlockProperties(ctx, se, hostGroupUUIDs, volume.Account.ID)
		if err != nil {
			return err
		}
	}
	ontapVersion := activities.GetOntapVersionFromPool(&pool.Pool)
	if len(params.Protocols) > 0 {
		// Protocol-specific validation
		_, err := GetVolumeTypeValidator(params.Protocols, ontapVersion)
		if err != nil {
			return err
		}
	}

	if params.FileProperties != nil {
		err := validateUpdateFileProperties(params, volume, ontapVersion)
		if err != nil {
			return err
		}
	}

	if params.DataProtection != nil && params.DataProtection.BackupVaultID != nil && *params.DataProtection.BackupVaultID != "" {
		var bv *datamodel.BackupVault
		var err error
		if utils.EnableBackupVaultSwitching {
			// Vault can be from a different project (owner); resolve by UUID only so we can validate and allow attach.
			bv, err = se.GetBackupVault(ctx, *params.DataProtection.BackupVaultID)
		} else {
			bv, err = se.GetBackupVaultByUUIDndOwnerID(ctx, *params.DataProtection.BackupVaultID, pool.Account.ID)
		}
		if err != nil && !customerrors.IsNotFoundErr(err) {
			return err
		}
		if bv != nil {
			// Re-attaching a vault after detach: available backups may still reference a detached vault.
			// Only applies when vault switching is enabled: detach can leave available backups tied to the old vault.
			noVaultAttached := volume.DataProtection == nil || volume.DataProtection.BackupVaultID == ""
			if utils.EnableBackupVaultSwitching && noVaultAttached {
				if err := validateReattachVaultAfterDetach(ctx, se, volume.UUID, bv); err != nil {
					return err
				}
			}
			if bv.LifeCycleState == datamodel.LifeCycleStateError {
				return customerrors.NewUserInputValidationErr("backup vault is in error state, please check the backup vault and try again")
			}
			if err := validateCRBBackupVault(bv, params.Region); err != nil {
				return err
			}
			if bv.CmekAttributes != nil && !nillable.IsNilOrEmpty(bv.CmekAttributes.KmsConfigResourcePath) && nillable.IsNilOrEmpty(params.DataProtection.KmsGrant) {
				return customerrors.NewUserInputValidationErr("KMS Grant is required for CMEK Backup vault")
			}
		}
	}

	if params.DataProtection != nil && !nillable.IsNilOrEmpty(params.DataProtection.BackupPolicyId) {
		backupPolicy, err := se.GetBackupPolicyByUUIDAndOwnerID(ctx, *params.DataProtection.BackupPolicyId, pool.Account.ID)
		if err != nil && !customerrors.IsNotFoundErr(err) {
			return err
		}
		// In a VCP-only region (CVP_HOST not set), backup policy must exist in VCP
		if backupPolicy == nil && !utils.IsCVPHostConfigured() {
			return customerrors.NewNotFoundErr(
				fmt.Sprintf("Backup policy %s does not exist in VCP. In a VCP-only region (SDE unavailable), backup policies must exist in VCP before volume update", *params.DataProtection.BackupPolicyId),
				nil,
			)
		}
		if backupPolicy != nil && backupPolicy.LifeCycleState != datamodel.LifeCycleStateREADY {
			return customerrors.NewUserInputValidationErr("backup policy is not in ready state, please check the backup policy and try again")
		}
	}

	if params.DataProtection != nil && !nillable.IsNilOrEmpty(params.DataProtection.BackupPolicyId) {
		backupPolicy, err := se.GetBackupPolicyByUUIDAndOwnerID(ctx, *params.DataProtection.BackupPolicyId, pool.Account.ID)
		if err != nil && !customerrors.IsNotFoundErr(err) {
			return err
		}
		if backupPolicy != nil && backupPolicy.LifeCycleState != datamodel.LifeCycleStateREADY {
			return customerrors.NewUserInputValidationErr("backup policy is not in ready state, please check the backup policy and try again")
		}
	}

	// When just enabling or disabling the snapshot policy, we need to check if there is an existing snapshot policy
	if params.SnapshotPolicy != nil && len(params.SnapshotPolicy.Schedules) == 0 && (volume.SnapshotPolicy == nil || volume.SnapshotPolicy.Name == "") {
		return customerrors.NewUserInputValidationErr("no existing snapshot policy found for the volume and no schedules provided in the update request. Cannot create a new snapshot policy without schedules")
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.IsDataProtection {
		if params.SnapReserve != nil && *params.SnapReserve != volume.VolumeAttributes.SnapReserve {
			return customerrors.NewUserInputValidationErr("Cannot update snapshotReserve on a Data Protection Volume")
		}
		if params.DataProtection != nil && !nillable.IsNilOrEmpty(params.DataProtection.BackupPolicyId) {
			return customerrors.NewUserInputValidationErr("Cannot update backup policy on a Data Protection Volume. Only manual backups are supported")
		}
		if params.SnapshotPolicy != nil && len(params.SnapshotPolicy.Schedules) > 0 {
			return customerrors.NewUserInputValidationErr("Cannot update snapshot policy on a Data Protection Volume.")
		}
	}
	if utils.IsImmutableBackupEnabled() {
		logger := util.GetLogger(ctx)
		if params.DataProtection != nil {
			// Validate immutable backup policy compliance when both BackupPolicyId and BackupVaultID are set
			if volume.DataProtection != nil && volume.DataProtection.BackupVaultID != "" && volume.DataProtection.BackupPolicyID != "" {
				err := checkIsValidImmutableBackupPolicyWithRetry(ctx, se, volume.DataProtection.BackupPolicyID, volume.DataProtection.BackupVaultID, volume.Account.ID, params.Region, params.AccountName)
				if err != nil {
					logger.Errorf("Immutable backup policy validation failed %v", err)

					// Check if it's a service-related error (CVP down, network issues, etc.)
					if customerrors.IsUnavailableErr(err) || customerrors.IsNetworkError(err) {
						return customerrors.NewUnavailableErr(fmt.Sprintf("Service is temporarily unavailable, please try again later: %v", err.Error()))
					}

					// Check if it's a retryable error (backup policy/vault in updating state)
					var customErr *vsaerrors.CustomError
					if vsaerrors.As(err, &customErr) {
						if customErr.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupPolicy ||
							customErr.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupVault {
							return customerrors.NewUnavailableErr(fmt.Sprintf("Backup policy or vault is currently being updated, please try again later: %v", err.Error()))
						}
					}

					// For all other errors (actual validation failures), return 400
					return customerrors.NewUserInputValidationErr(fmt.Sprintf("Backup policy is not compliant with immutable backup vault settings: %v", err.Error()))
				}
			}
		}
	}

	// Validate snapReserve changes to ensure sufficient LUN space
	if params.SnapReserve != nil && volume.VolumeAttributes != nil && utils.IsSanProtocols(volume.VolumeAttributes.Protocols) && *params.SnapReserve != volume.VolumeAttributes.SnapReserve {
		if *params.SnapReserve > volume.VolumeAttributes.SnapReserve {
			var requiredQuotaInBytes int64
			// Calculate current available LUN space
			currentLunSpace := volume.SizeInBytes - int64(float64(volume.SizeInBytes)*float64(volume.VolumeAttributes.SnapReserve)/utils.PercentageBase)
			if params.QuotaInBytes == 0 {
				// Calculate required size with the given snapReserve to ensure sufficient LUN space
				requiredQuotaInBytes = int64(float64(currentLunSpace) / (1 - float64(*params.SnapReserve)/utils.PercentageBase))
				return customerrors.NewUserInputValidationErr(fmt.Sprintf(ErrMsgSnapReserveIncrease, float64(*params.SnapReserve), float64(currentLunSpace)/float64(bytesPerGB), math.Ceil(float64(requiredQuotaInBytes)/float64(bytesPerGB))))
			} else {
				// Calculate updated LUN space with the new given size
				updatedLunSpace := params.QuotaInBytes - int64(float64(params.QuotaInBytes)*float64(*params.SnapReserve)/utils.PercentageBase)
				if updatedLunSpace < currentLunSpace {
					// Calculate required size to ensure sufficient LUN space
					requiredQuotaInBytes = int64(float64(currentLunSpace) / (1 - float64(*params.SnapReserve)/utils.PercentageBase))
					return customerrors.NewUserInputValidationErr(fmt.Sprintf(ErrMsgSnapReserveIncrease, float64(*params.SnapReserve), float64(currentLunSpace)/float64(bytesPerGB), math.Ceil(float64(requiredQuotaInBytes)/float64(bytesPerGB))))
				}
			}
		}
		// Allow snapReserve decrease as it increases available LUN space
	}

	// Validate that any requested updates to qos parameters are valid within the pool's limits
	if params.ThroughputMibps != nil || params.Iops != nil {
		if pool.QosType != utils.QosTypeManual {
			return customerrors.NewUserInputValidationErr("Parent pool's QosType must be set to Manual to update QoS parameters.")
		}

		poolThroughputAfterUpdate := 0
		poolIopsAfterUpdate := 0
		volumeThroughputBeforeUpdate := 0
		volumeIopsBeforeUpdate := 0
		if params.ThroughputMibps != nil {
			if volume.VolumePerformanceGroupID.Valid && volume.VolumePerformanceGroup != nil {
				volumeThroughputBeforeUpdate = int(volume.VolumePerformanceGroup.ThroughputMibps)
			}
			// Requested final iops = Current pool iops - current volume iops + requested iops
			poolThroughputAfterUpdate = int(pool.Throughput) - volumeThroughputBeforeUpdate + int(*params.ThroughputMibps)
		}
		if params.Iops != nil {
			if volume.VolumePerformanceGroupID.Valid && volume.VolumePerformanceGroup != nil {
				volumeIopsBeforeUpdate = int(volume.VolumePerformanceGroup.Iops)
			}
			// Requested final iops = Current pool iops - current volume iops + requested iops
			poolIopsAfterUpdate = int(pool.Iops) - volumeIopsBeforeUpdate + int(*params.Iops)
		}
		if err := assertQosLimits(pool, poolThroughputAfterUpdate, poolIopsAfterUpdate); err != nil {
			return err
		}

		if volume.VolumePerformanceGroupID.Valid && volume.VolumePerformanceGroup != nil && !volume.VolumePerformanceGroup.IsAutoGen {
			if err := mqos.ValidateVPGCountForPool(ctx, se, pool.ID); err != nil {
				return err
			}
		}
	}
	if params.VolumePerformanceGroupId != nil {
		if !enableVolumePerformanceGroupAssignment {
			return customerrors.NewUserInputValidationErr(utils.ErrMsgVpgAssignmentNotEnabled)
		}
		vpg, err := se.GetVolumePerformanceGroupByUUID(ctx, *params.VolumePerformanceGroupId)
		if err != nil {
			log.Error("Failed to get VolumePerformanceGroup", "vpgUUID", *params.VolumePerformanceGroupId, "error", err)
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("VolumePerformanceGroup %s does not exist", *params.VolumePerformanceGroupId))
		}
		if vpg.IsAutoGen {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("Cannot assign volume to autogenerated VolumePerformanceGroup %s", vpg.UUID))
		}
		if vpg.PoolID != pool.ID {
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("VolumePerformanceGroup %s does not belong to the same pool as the volume", vpg.UUID))
		}
		if volume.VolumePerformanceGroupID.Valid && volume.VolumePerformanceGroup != nil &&
			vpg.UUID == volume.VolumePerformanceGroup.UUID {
			return nil
		}

		poolThroughputAfterUpdate := int(pool.Throughput)
		poolIopsAfterUpdate := int(pool.Iops)
		// Calculate what the pool utilization would be after assigning this volume to the VPG
		// First, subtract the current volume's VPG contribution (if any)
		if volume.VolumePerformanceGroupID.Valid && volume.VolumePerformanceGroup != nil {
			shouldSubtract, err := mqos.ShouldSubtractCurrentVpgContribution(ctx, se, volume)
			if err != nil {
				log.Error("Failed to evaluate current VPG contribution for validation", "vpgID", vpg.ID, "error", err)
				return err
			}

			// If it is currently assigned to a non shared VPG or it is the only volume assigned to that VPG,
			// subtract the volume's current qos values as updating the assigned VPG will free up some of the throughput/iops.
			if shouldSubtract {
				poolThroughputAfterUpdate = poolThroughputAfterUpdate - int(volume.VolumePerformanceGroup.ThroughputMibps)
				poolIopsAfterUpdate = poolIopsAfterUpdate - int(volume.VolumePerformanceGroup.Iops)
			}
			// TODO: When updating enableVPGAssignment flag, use create volume utilities to calculate the pool throughput and iops after assignment.
		}
		shouldAdd, err := mqos.ShouldAddNewVpgContribution(ctx, se, vpg)
		if err != nil {
			log.Error("Failed to evaluate new VPG contribution for validation", "vpgID", vpg.ID, "error", err)
			return err
		}
		if shouldAdd {
			poolThroughputAfterUpdate = poolThroughputAfterUpdate + int(vpg.ThroughputMibps)
			poolIopsAfterUpdate = poolIopsAfterUpdate + int(vpg.Iops)
		}

		if err := assertQosLimits(pool, poolThroughputAfterUpdate, poolIopsAfterUpdate); err != nil {
			return err
		}
	}

	return nil
}

// assertQosLimits validates that the requested throughput and IOPS updates don't exceed pool limits
func assertQosLimits(pool *datamodel.PoolView, poolThroughputAfterUpdate int, poolIopsAfterUpdate int) error {
	// If PoolAttributes is nil, we cannot validate against limits, so reject positive values
	if pool.PoolAttributes == nil {
		if poolThroughputAfterUpdate > 0 || poolIopsAfterUpdate > 0 {
			return customerrors.NewUserInputValidationErr("Requested throughput update would exceed the pool's throughput limit of 0 MiBps")
		}
		return nil
	}

	poolThroughputLimit := int(pool.PoolAttributes.ThroughputMibps)
	poolIopsLimit := int(pool.PoolAttributes.Iops)

	if poolThroughputAfterUpdate > poolThroughputLimit {
		return customerrors.NewUserInputValidationErr(fmt.Sprintf("Requested throughput update would exceed the pool's throughput limit of %d MiBps", poolThroughputLimit))
	}

	if poolIopsAfterUpdate > poolIopsLimit {
		return customerrors.NewUserInputValidationErr(fmt.Sprintf("Requested IOPS update would exceed the pool's IOPS limit of %d IOPS", poolIopsLimit))
	}

	return nil
}

// validateCRBBackupVault validates cross-region backup vault configuration
// region is the region where the volume is being created/updated
func validateCRBBackupVault(backupVault *datamodel.BackupVault, region string) error {
	if backupVault.BackupVaultType == activities.CrossRegionBackupType {
		if !utils.IsCrossRegionBackupEnabled() {
			return customerrors.NewBadRequestErr(activities.CrossRegionBackupVaultErrMsg)
		}
		if backupVault.SourceRegionName == nil || *backupVault.SourceRegionName == "" {
			return customerrors.NewBadRequestErr("Source region must be specified for cross-region backup vault")
		}
		if backupVault.BackupRegionName == nil || *backupVault.BackupRegionName == "" {
			return customerrors.NewBadRequestErr("Backup region must be specified for cross-region backup vault")
		}
		if *backupVault.SourceRegionName == *backupVault.BackupRegionName {
			return customerrors.NewBadRequestErr("Backup region must be different from source region for cross-region backup vault")
		}
		if backupVault.LifeCycleState != datamodel.LifeCycleStateREADY {
			return customerrors.NewBadRequestErr("Cross-region backup vault must be in READY state")
		}
		if *backupVault.BackupRegionName == region {
			return customerrors.NewUserInputValidationErr("cannot assign a cross-region backup vault to a volume in the destination region")
		}
	}
	return nil
}

func validateBlockProperties(ctx context.Context, se database.Storage, hostGroupUUIDs []string, accountID int64) error {
	if len(hostGroupUUIDs) > 0 {
		hostGroups, err := se.GetMultipleHostGroups(ctx, hostGroupUUIDs, accountID)
		if err != nil {
			return err
		}
		if len(hostGroupUUIDs) != len(hostGroups) {
			return customerrors.NewUserInputValidationErr("could not find some of the host groups, please check the hostgroup details and try with valid host group names.")
		}
		uniqueHostSet := make(map[string]bool)
		for _, hostGroup := range hostGroups {
			if hostGroup.State != datamodel.LifeCycleStateREADY {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("host group %s is not available", hostGroup.Name))
			}
			for _, host := range hostGroup.Hosts.Hosts {
				if _, exists := uniqueHostSet[host]; exists {
					return customerrors.NewUserInputValidationErr(fmt.Sprintf("host : %s is present in multiple host groups", host))
				}
				uniqueHostSet[host] = true
			}
		}
	}

	return nil
}

func validateUpdateFileProperties(params *common.UpdateVolumeParams, volume *datamodel.Volume, ontapVersion string) error {
	if volume.VolumeAttributes == nil || volume.VolumeAttributes.FileProperties == nil {
		return customerrors.NewUserInputValidationErr("File properties is mandatory to update file properties on the volume")
	}

	// Validate that the volume supports NFS protocols before allowing file property updates
	if !utils.IsFileProtocolSupportedV2(ontapVersion) {
		return customerrors.NewUserInputValidationErr("file properties can only be supported for volumes with NAS protocols")
	}

	if params.FileProperties == nil {
		return customerrors.NewUserInputValidationErr("File properties cannot be nil")
	}

	if params.FileProperties.ExportPolicy != nil && params.FileProperties.ExportPolicy.ExportRules != nil {
		// Use update params protocols if provided, otherwise use existing volume protocols
		protocols := params.Protocols
		if len(protocols) == 0 && volume.VolumeAttributes != nil {
			protocols = volume.VolumeAttributes.Protocols
		}

		if err := factorycommon.ValidateExportRulesAgainstProtocols(params.FileProperties.ExportPolicy.ExportRules, protocols); err != nil {
			return err
		}
	}
	return nil
}

func convertToModelSnapshotPolicySchedule(schedules []*datamodel.SnapshotPolicySchedule) []*models.SnapshotPolicySchedule {
	var dbSnapshotPolicySchedules []*models.SnapshotPolicySchedule
	for _, schedule := range schedules {
		dbSnapshotPolicySchedules = append(dbSnapshotPolicySchedules, &models.SnapshotPolicySchedule{
			Count:           schedule.Count,
			SnapmirrorLabel: schedule.SnapmirrorLabel,
			Prefix:          schedule.SnapmirrorLabel,
			Schedule: &models.Schedule{
				DaysOfMonth: schedule.DaysOfMonth,
				DaysOfWeek:  schedule.DaysOfWeek,
				Hours:       schedule.Hours,
				Minutes:     schedule.Minutes,
			},
		})
	}
	return dbSnapshotPolicySchedules
}

// checkIfPoolUpdateRequired determines if a pool update is needed based on volume count, current instance type, and operation type
// For volume create: Returns true if volumeCount exceeds the maxVolumeCount of current instance type
// For volume delete: Returns true if volumeCount falls below the maxVolumeCount of the previous (smaller) instance type
func checkIfPoolUpdateRequired(volumeCount int64, currentInstanceType string, volLimitPerInstanceMap map[string]common.VolumeCountRange, isDelete bool) bool {
	currentLimits, exists := volLimitPerInstanceMap[currentInstanceType]
	if !exists {
		// If instance type not found in map, assume no update needed
		return false
	}

	if !isDelete {
		// Volume Create scenario: Update required if volume count exceeds current instance type's max
		return volumeCount > currentLimits.MaxVolumeCount
	}

	// Volume Delete scenario: Check if we can scale down to a smaller instance type
	// Find the previous (smaller) instance type's max capacity
	var previousMaxVolumeCount int64 = -1

	// Iterate through the map to find the instance type with max capacity just below the current one
	for _, limits := range volLimitPerInstanceMap {
		if limits.MaxVolumeCount < currentLimits.MaxVolumeCount {
			if previousMaxVolumeCount == -1 || limits.MaxVolumeCount > previousMaxVolumeCount {
				previousMaxVolumeCount = limits.MaxVolumeCount
			}
		}
	}

	// If no previous instance type exists (we're on the smallest), no update needed
	if previousMaxVolumeCount == -1 {
		return false
	}

	// Update required if volume count is less than the previous instance type's max
	// This means we can scale down to a smaller instance type
	return volumeCount < previousMaxVolumeCount
}

// checkAndTriggerPoolScalingIfNeeded checks if the pool needs scaling and triggers it asynchronously
func checkAndTriggerPoolScalingIfNeeded(ctx context.Context, se database.Storage, temporal client.Client, pool *datamodel.Pool, isDelete bool) {
	logger := util.GetLogger(ctx)

	// Validate pool state - only scale pools in a stable state
	if pool.State != datamodel.LifeCycleStateREADY {
		logger.Warnf("Pool not in ready state poolID: %s, state: %s", pool.UUID, pool.State)
		return
	}

	// Get current volume count for the pool
	currentVolumeCount, err := se.GetVolumeCountByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Error("Failed to get volume count for pool", "poolID", pool.ID, "error", err)
		return
	}
	volLimitPerInstanceMap := make(map[string]common.VolumeCountRange)
	err = json.Unmarshal([]byte(autoPoolScalingLimits), &volLimitPerInstanceMap)
	if err != nil || len(volLimitPerInstanceMap) == 0 {
		logger.Error("Failed to parse auto pool scaling limits", "error", err)
		return
	}

	// Get current instance type from pool's nodes
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil || len(nodes) == 0 {
		logger.Error("Failed to get nodes for pool", "poolID", pool.ID, "error", err)
		return
	}
	currentInstanceType := ""
	if nodes[0].NodeAttributes != nil {
		currentInstanceType = nodes[0].NodeAttributes.InstanceType
	}
	if currentInstanceType == "" {
		logger.Warn("Current instance type not found for pool", "poolID", pool.UUID)
		return
	}

	// Check if current instance type is appropriate for the volume count
	requiresUpdate := checkIfPoolUpdateRequired(currentVolumeCount, currentInstanceType, volLimitPerInstanceMap, isDelete)
	if !requiresUpdate {
		logger.Infof("Pool update not required. Current instance type %s is suitable for %d volumes", currentInstanceType, currentVolumeCount)
		return
	}

	logger.Infof("Pool update required. Current instance type: %s, Volume count: %d", currentInstanceType, currentVolumeCount)

	autoScalingParams := &common.AutoPoolScalingParams{
		VolLimitPerInstanceMap: volLimitPerInstanceMap,
		CurrentVolumeCount:     currentVolumeCount,
	}

	totalThroughputMibps := int64(0)
	var totalIops *int64
	var labels *datamodel.JSONB
	if pool.PoolAttributes != nil {
		totalThroughputMibps = pool.PoolAttributes.ThroughputMibps
		totalIops = &pool.PoolAttributes.Iops
		labels = pool.PoolAttributes.Labels
	}

	region := env.GetString("LOCAL_REGION", "")
	updateParams := &common.UpdatePoolParams{
		PoolId:               pool.UUID,
		AccountName:          pool.Account.Name,
		Region:               region,
		SizeInBytes:          uint64(pool.SizeInBytes),
		TotalThroughputMibps: totalThroughputMibps,
		TotalIops:            totalIops,
		Description:          pool.Description,
		Labels:               labels,
		AllowAutoTiering:     pool.AllowAutoTiering,
		LargeCapacity:        &pool.LargeCapacity,
	}

	if pool.AllowAutoTiering && pool.AutoTieringConfig != nil {
		updateParams.HotTierSizeInBytes = uint64(pool.AutoTieringConfig.HotTierSizeInBytes)
		updateParams.EnableHotTierAutoResize = pool.AutoTieringConfig.EnableHotTierAutoResize
	}

	poolCategory := models.GetPoolCategory(pool.LargeCapacity)
	job := &datamodel.Job{
		Type:          string(models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationUpdate, poolCategory)),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  pool.UUID,
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		AccountID:     sql.NullInt64{Int64: pool.AccountID, Valid: true},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return
	}

	poolCurrentState := pool.State
	previousStateDetails := pool.StateDetails

	// Put the pool in updating state before the operation
	if _, poolErr := se.UpdatePoolState(ctx, pool, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails); poolErr != nil {
		logger.Error("Failed to update pool state to ERROR", "poolID", pool.UUID, "error", poolErr)
	}

	defer func() {
		if err != nil {
			// Revert the state in error
			if _, poolErr := se.UpdatePoolState(ctx, pool, poolCurrentState, previousStateDetails); poolErr != nil {
				logger.Error("Failed to update pool state to ERROR", "poolID", pool.UUID, "error", poolErr)
			}
		}
	}()

	updatePoolRunTimeout := workflowengine.GetUpdatePoolWorkflowRunTimeout(pool.LargeCapacity, true)
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.BackgroundTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    *updatePoolRunTimeout,
		},
		workflows.UpdatePoolWorkflow,
		updateParams,
		pool,
		autoScalingParams,
	)
	if err != nil {
		logger.Errorf("failed to start automatic pool scaling workflow: %v", err)
	}

	logger.Infof("Triggered instance upgrade for pool: %s", pool.Name)
	return
}

func (o *GCPOrchestrator) RestoreFilesFromBackup(ctx context.Context, params *common.RestoreFilesFromBackupParams) (string, error) {
	return _restoreFilesFromBackup(ctx, o.storage, o.temporal, params)
}

func _restoreFilesFromBackup(ctx context.Context, se database.Storage, temporal client.Client, params *common.RestoreFilesFromBackupParams) (string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return "", err
	}

	volume, err := se.GetVolume(ctx, params.VolumeUUID)
	if err != nil {
		return "", err
	}

	if volume.State != datamodel.LifeCycleStateREADY {
		return "", customerrors.NewUserInputValidationErr("Volume is not ready")
	}

	// Validate BackupPath is provided
	if params.BackupPath == "" {
		return "", customerrors.NewUserInputValidationErr("BackupPath must be provided")
	}

	if len(strings.Split(params.BackupPath, "/")) != 8 {
		return "", customerrors.NewUserInputValidationErr("Backup path is not in correct format - expected format: projects/{project}/locations/{location}/backupVaults/{vaultName}/backups/{backupName}")
	}

	if volume.VolumeAttributes == nil || volume.VolumeAttributes.Protocols == nil {
		return "", customerrors.NewUserInputValidationErr("Volume attributes or protocols cannot be nil")
	}

	// Validate volume protocol (SAN not supported)
	if utils.IsSanProtocols(volume.VolumeAttributes.Protocols) {
		return "", customerrors.NewUserInputValidationErr("Single file restore is not supported for ISCSI Volumes")
	}

	originalState := volume.State
	originalStateDetails := volume.StateDetails
	stateUpdated := false

	// Update volume state to RESTORING
	volume.State = datamodel.LifeCycleStateRestoring
	volume.StateDetails = datamodel.LifeCycleStateRestoringDetails

	// Create a job for the restore files operation
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeRestoreFilesBackup),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  volume.UUID,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create restore files from backup job in database", "error", err)
		return "", err
	}

	defer func() {
		if err != nil {
			// Rollback occurs for any error after a successful state update.
			// WorkflowExecutor now handles retries and workflow error states internally.
			if stateUpdated {
				volume.State = originalState
				volume.StateDetails = originalStateDetails
				if _, rollbackErr := updateVolumeStatus(ctx, se, volume, originalState, originalStateDetails); rollbackErr != nil {
					logger.Error("Failed to rollback volume state", "error", rollbackErr, "originalState", originalState)
				}
			}

			// Mark job as error if it was created
			if createdJob != nil {
				if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
					logger.Error("Failed to update job state to ERROR", "error", jobErr, "jobUUID", createdJob.UUID)
				}
			}
		}
	}()

	// Update volume state in database
	volume, err = updateVolumeStatus(ctx, se, volume, datamodel.LifeCycleStateRestoring, datamodel.LifeCycleStateRestoringDetails)
	if err != nil {
		logger.Error("Failed to update volume state to restoring", "error", err)
		return "", err
	}
	stateUpdated = true

	// Execute the workflow
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.RestoreFilesFromBackupWorkflow,
		workflowengine.GetSFRWorkflowTimeout(),
		params,
		volume,
	)
	if err != nil {
		logger.Error("Failed to start restore files from backup workflow after retries: ", "error", err)
		return "", err
	}
	return createdJob.UUID, nil
}

func (o *GCPOrchestrator) SplitStartVolume(ctx context.Context, params *common.SplitStartVolumeParams) (*models.Volume, string, error) {
	return splitStartVolume(ctx, o.storage, o.temporal, params)
}

func (o *GCPOrchestrator) SplitStopVolume(ctx context.Context, params *common.SplitStopVolumeParams) (*models.Volume, error) {
	return splitStopVolume(ctx, o.storage, params)
}

// isOntapError checks if the error is from the ONTAP layer (error codes 5000-5999)
func isOntapError(err error) bool {
	if err == nil {
		return false
	}
	customErr := vsaerrors.ExtractCustomError(err)
	if customErr != nil {
		// ONTAP errors have TrackingID in the range 5000-5999
		return customErr.TrackingID >= 5000 && customErr.TrackingID < 6000
	}
	return false
}

// extractONTAPErrorDetails attempts to pull the ONTAP error code and message out of a
// CustomError whose OriginalErr is a go-swagger VolumeModifyDefault response.
// The response Error() string has the form:
//
//	[PATCH /storage/volumes/{uuid}][500] volume_modify default {"error":{"code":"460765","message":"..."}}
//
// If extraction fails for any reason we fall back to OriginalErr.Error() as the message
// and an empty code (which routes to the generic ErrSplitCloneJobFailed bucket).
func extractONTAPErrorDetails(customErr *vsaerrors.CustomError) (ontapMessage, ontapCode string) {
	if customErr == nil || customErr.OriginalErr == nil {
		return "", ""
	}
	raw := customErr.OriginalErr.Error()
	// Find the JSON payload — it starts at the first '{'.
	jsonStart := strings.Index(raw, "{")
	if jsonStart < 0 {
		return raw, ""
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw[jsonStart:]), &payload); err != nil || payload.Error.Message == "" {
		// Not parseable — return the whole raw string as the message.
		return raw, ""
	}
	return payload.Error.Message, payload.Error.Code
}

// _updateCloneState updates the clone state in volume attributes
func _updateCloneState(ctx context.Context, se database.Storage, volumeUUID string, cloneState string) error {
	logger := util.GetLogger(ctx)
	volume, err := se.GetVolume(ctx, volumeUUID)
	if err != nil {
		return err
	}

	var updatedAttributes *datamodel.VolumeAttributes
	if volume.VolumeAttributes != nil {
		// Shallow copy preserves all fields (including kerberos_enabled, ldap_enabled,
		// account_name, deployment_name, is_regional_ha, etc.) so nothing is silently erased.
		attrs := *volume.VolumeAttributes
		updatedAttributes = &attrs

		// Update clone parent info state if it exists
		if volume.VolumeAttributes.CloneParentInfo != nil {
			cloneInfo := *volume.VolumeAttributes.CloneParentInfo
			cloneInfo.State = cloneState
			// Clear any previous StateDetails (e.g. from a prior failed split) so the
			// new attempt starts with a clean state.
			cloneInfo.StateDetails = ""
			updatedAttributes.CloneParentInfo = &cloneInfo
		}
	} else {
		logger.Warnf("Volume %s has no VolumeAttributes, cannot update clone state", volumeUUID)
		return nil
	}

	// Update volume with new attributes
	updateFields := map[string]interface{}{
		"volume_attributes": updatedAttributes,
	}

	err = se.UpdateVolumeFields(ctx, volumeUUID, updateFields)
	if err != nil {
		logger.Errorf("Failed to update clone state to %s for volume %s: %v", cloneState, volumeUUID, err)
		return err
	}

	logger.Debugf("Successfully updated clone state to %s for volume %s", cloneState, volumeUUID)
	return nil
}

func _splitStartVolume(ctx context.Context, se database.Storage, temporal client.Client, params *common.SplitStartVolumeParams) (*models.Volume, string, error) {
	logger := util.GetLogger(ctx)
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to fetch account for the given projectNumber", "error", err)
		return nil, "", err
	}

	volume, err := se.GetVolumeWithAccountID(ctx, params.VolumeID, account.ID)
	if err != nil {
		logger.Error("Failed to fetch volume for the given account ID", "error", err)
		return nil, "", err
	}

	pool, err := se.GetPool(ctx, volume.Pool.UUID, account.ID)
	if err != nil {
		logger.Error("Failed to fetch pool for the given account ID", "error", err)
		return nil, "", err
	}

	if utils.IsTransitionalState(volume.State) {
		logger.Errorf("Volume %s cannot be split, while in transitioning state: %s", volume.Name, volume.State)
		return nil, "", customerrors.NewConflictErr("volume is in transition state and cannot be split, state: " + volume.State)
	}

	if volume.State != datamodel.LifeCycleStateREADY {
		return nil, "", customerrors.NewConflictErr("Volume is not in READY state, state: " + volume.State)
	}

	err = validateSplitStartVolumeParams(ctx, se, volume, pool)
	if err != nil {
		return nil, "", err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeSplitVolume),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  volume.Name,
		AccountID:     sql.NullInt64{Int64: volume.AccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: volume.UUID},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create volume split job in database", "error", err)
		return nil, "", err
	}

	// Defer to mark job as deleted if any error happens
	defer func() {
		if err != nil {
			// Delete job if error occurred
			if createdJob != nil && createdJob.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", createdJob.UUID)
				if delErr := se.DeleteJob(ctx, createdJob.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
			}
		}
	}()

	previousClonesSharedBytes := volume.ClonesSharedBytes
	previousCloneState := ""
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil {
		previousCloneState = volume.VolumeAttributes.CloneParentInfo.State
	}

	// Update clone state to SPLITTING when split starts
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil {
		err = updateCloneState(ctx, se, volume.UUID, datamodel.CloneStateSplitting)
		if err != nil {
			logger.Error("Failed to update clone state to SPLITTING", "error", err)
			return nil, "", err
		}
		volume.VolumeAttributes.CloneParentInfo.State = datamodel.CloneStateSplitting
		volume.VolumeAttributes.CloneParentInfo.StateDetails = ""
	}

	// Reserve the clonesharedbytes space by setting it to 0 before starting the workflow
	// This prevents other volume creation operations from using this space during the split
	logger.Infof("Reserving clonesharedbytes (%d) for volume split by setting clones_shared_bytes to 0. Volume UUID: %s", previousClonesSharedBytes, volume.UUID)
	err = se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
		"clones_shared_bytes": uint64(0),
	})
	if err != nil {
		logger.Error("Failed to reserve clonesharedbytes for volume split", "error", err)
		return nil, "", err
	}

	// Fetch the updated pool details after reserving the clones_shared_bytes to ensure we have the latest state for validation in the workflow
	pool, err = se.GetPool(ctx, pool.UUID, account.ID)
	if err != nil {
		logger.Error("Failed to fetch pool for the given account ID", "error", err)
		return nil, "", err
	}

	// splitInitiated is set to true only after InitiateSplitVolume succeeds, meaning ONTAP has
	// accepted the split request and data movement is underway. The defer below uses this flag
	// to distinguish three failure scenarios:
	//   1. split NOT initiated (error before/during ONTAP call)       → revert state to CLONED, restore cloneSharedBytes
	//   2. split initiated + ONTAP terminal error during polling       → set state to ERROR IN SPLITTING, keep cloneSharedBytes as 0
	//   3. split initiated + non-ONTAP error (e.g. Temporal not started) → keep state as SPLITTING, keep cloneSharedBytes as 0
	splitInitiated := false

	// Defer to revert the resource state and clones_shared_bytes if any error happens
	defer func() {
		if err != nil {
			isOntapErr := isOntapError(err)

			if !splitInitiated {
				// Split never reached ONTAP — safe to fully roll back.
				logger.Infof("Split not initiated, reverting clones_shared_bytes to %d for volume UUID: %s", previousClonesSharedBytes, volume.UUID)
				volumeUpdateErr := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
					"clones_shared_bytes": previousClonesSharedBytes,
				})
				if volumeUpdateErr != nil {
					logger.Errorf("Failed to revert clones_shared_bytes: %v", volumeUpdateErr)
				} else {
					logger.Infof("Successfully reverted clones_shared_bytes to %d for volume UUID: %s", previousClonesSharedBytes, volume.UUID)
				}

				// Revert clone state to CLONED regardless of error type.
				if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil && previousCloneState != "" {
					logger.Infof("Split not initiated, reverting clone state to CLONED for volume UUID: %s", volume.UUID)
					currentVolume, fetchErr := se.GetVolume(ctx, volume.UUID)
					if fetchErr != nil || currentVolume.VolumeAttributes == nil || currentVolume.VolumeAttributes.CloneParentInfo == nil {
						logger.Errorf("Failed to fetch current volume for clone state revert: %v", fetchErr)
					} else {
						updatedAttrs := *currentVolume.VolumeAttributes
						cloneInfo := *currentVolume.VolumeAttributes.CloneParentInfo
						cloneInfo.State = datamodel.CloneStateCloned
						cloneInfo.StateDetails = ""
						updatedAttrs.CloneParentInfo = &cloneInfo
						if cloneStateErr := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
							"volume_attributes": &updatedAttrs,
						}); cloneStateErr != nil {
							logger.Errorf("Failed to revert clone state to CLONED: %v", cloneStateErr)
						}
					}
				}
				return
			}

			// Split was initiated — ONTAP data movement is in progress. Keep cloneSharedBytes as 0.
			logger.Infof("Split already initiated in ONTAP, keeping clones_shared_bytes as 0 for volume UUID: %s", volume.UUID)

			if !isOntapErr {
				// Non-ONTAP error (e.g. Temporal workflow failed to start): ONTAP is still splitting,
				// so leave the clone state as SPLITTING and do not surface an error state.
				logger.Infof("Non-ONTAP error after split initiation, leaving clone state as SPLITTING for volume UUID: %s", volume.UUID)
				return
			}

			// ONTAP terminal error during polling: mark as ERROR IN SPLITTING.
			if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil && previousCloneState != "" {
				ontapMessage, ontapCode := extractONTAPErrorDetails(vsaerrors.ExtractCustomError(err))
				classifiedErr, ontapMsg := workflows.ClassifyONTAPSplitError(ontapMessage, ontapCode, previousClonesSharedBytes)
				cloneStateDetails := classifiedErr.GetMessage()
				logger.Errorf("ONTAP terminal error after split initiation for volume %s: %s", volume.UUID, ontapMsg)
				logger.Infof("Setting clone state to ERROR IN SPLITTING for volume UUID: %s", volume.UUID)

				currentVolume, fetchErr := se.GetVolume(ctx, volume.UUID)
				if fetchErr != nil || currentVolume.VolumeAttributes == nil || currentVolume.VolumeAttributes.CloneParentInfo == nil {
					logger.Errorf("Failed to fetch current volume for clone state update: %v", fetchErr)
				} else {
					updatedAttrs := *currentVolume.VolumeAttributes
					cloneInfo := *currentVolume.VolumeAttributes.CloneParentInfo
					cloneInfo.State = datamodel.CloneStateErrorInSplitting
					cloneInfo.StateDetails = cloneStateDetails
					updatedAttrs.CloneParentInfo = &cloneInfo
					if cloneStateErr := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
						"volume_attributes": &updatedAttrs,
					}); cloneStateErr != nil {
						logger.Errorf("Failed to update clone state to ERROR IN SPLITTING: %v", cloneStateErr)
					}
				}
			}
		}
	}()

	// Resolve ONTAP nodes for this pool synchronously (was previously done inside the Temporal workflow).
	dbNodes, err := se.GetNodesByPoolID(ctx, volume.Pool.ID)
	if err != nil {
		logger.Error("Failed to fetch nodes for pool", "error", err)
		return nil, "", err
	}
	if len(dbNodes) == 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrUnexpectedNodeCountForPool, fmt.Errorf("no nodes found for pool %s", volume.Pool.UUID))
		logger.Error("No nodes found for pool", "error", err)
		return nil, "", err
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   volume.Pool.DeploymentName,
		OntapCredentials: volume.Pool.PoolCredentials,
	})

	// Obtain an ONTAP provider and call InitiateSplitVolume synchronously.
	// This sends the split request to ONTAP and returns the ONTAP job UUID that
	// tracks the background data-movement; the Temporal workflow will poll it.
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get ONTAP provider for split", "error", err)
		return nil, "", err
	}

	if volume.VolumeAttributes == nil || volume.VolumeAttributes.ExternalUUID == "" {
		err = customerrors.NewUserInputValidationErr("volume has no external UUID, cannot initiate split in ONTAP")
		logger.Error("Volume missing ExternalUUID", "error", err)
		return nil, "", err
	}

	ontapJobUUID, err := provider.InitiateSplitVolume(volume.VolumeAttributes.ExternalUUID)
	if err != nil {
		logger.Errorf("Failed to initiate split for volume %s in ONTAP: %v", volume.Name, err)
		return nil, "", vsaerrors.NewVCPError(vsaerrors.ErrSplitInitiationFailed, err)
	}
	// Mark split as initiated so the defer knows ONTAP data movement has begun.
	splitInitiated = true
	logger.Infof("Split initiated in ONTAP for volume %s, ONTAP job UUID: %q", volume.Name, ontapJobUUID)

	// Fire the Temporal workflow that will poll the ONTAP job and clean up the clone snapshot.
	// The workflow receives the ONTAP job UUID so it can poll without re-calling ONTAP.
	// We set a per-run timeout that is slightly larger than the ContinueAsNew poll window so
	// Temporal does not kill an individual run before it has a chance to restart itself.
	splitWorkflowTimeout := workflowengine.GetSplitVolumeWorkflowTimeout()
	taskQueue := workflowengine.BackgroundTaskQueue
	workflowDispatchStart := time.Now()
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		taskQueue,
		workflows.VolumePollSplitWorkflow,
		splitWorkflowTimeout,
		volume,
		node,
		ontapJobUUID,
	)
	if err != nil {
		if splitWaitForTemporalEnabled {
			// Temporal is unavailable but ONTAP split is already in progress.
			// Fire UpdateJob(WAIT_FOR_TEMPORAL) in a background goroutine so the API
			// returns 200 immediately.  The goroutine retries with exponential backoff
			// for ~10s total.  If all retries are exhausted it calls DeleteJob so the
			// job does not linger in NEW state invisible to the orphan-job scheduler —
			// mirroring the deferred job-delete that runs on the main goroutine for
			// other error paths (see defer at line 3703).
			logger.Warn("Failed to start volume poll split workflow, placing job in WAIT_FOR_TEMPORAL via background goroutine: ", "error", err)
			logger.Infof("Split workflow dispatch failed in %s for volume %s (job %s)", time.Since(workflowDispatchStart), volume.UUID, createdJob.UUID)
			workflowStartErr := err.Error()
			jobUUID := createdJob.UUID
			trackingID := createdJob.TrackingID
			volumeUUID := volume.UUID
			volumeAttrs := *volume.VolumeAttributes
			go func() {
				bgCtx, bgCancel := context.WithTimeout(context.Background(), waitForTemporalBgTimeout)
				defer bgCancel()
				bgLogger := util.GetLogger(bgCtx)
				// Persist the ONTAP job UUID so the orphan-job processor can reconstruct
				// VolumePollSplitWorkflow args if this job is picked up via WAIT_FOR_TEMPORAL.
				// This is only needed on the WAIT_FOR_TEMPORAL fallback path.
				updatedAttrsForSplitUUID := volumeAttrs
				updatedAttrsForSplitUUID.SplitJobUUID = ontapJobUUID
				if persistErr := se.UpdateVolumeFields(bgCtx, volumeUUID, map[string]interface{}{
					"volume_attributes": &updatedAttrsForSplitUUID,
				}); persistErr != nil {
					bgLogger.Warnf("Failed to persist SplitJobUUID for volume %s (non-fatal): %v", volumeUUID, persistErr)
				}
				retryStart := time.Now()
				delay := waitForTemporalUpdateInitDelay
				var lastErr error
				for attempt := 1; attempt <= waitForTemporalUpdateMaxRetries; attempt++ {
					lastErr = se.UpdateJob(bgCtx, jobUUID, string(datamodel.JobsStateWaitForTemporal), trackingID, workflowStartErr)
					if lastErr == nil {
						bgLogger.Infof("Split job %s successfully updated to WAIT_FOR_TEMPORAL (attempt %d, elapsed %s)", jobUUID, attempt, time.Since(retryStart))
						return
					}
					bgLogger.Errorf("Failed to update split job %s to WAIT_FOR_TEMPORAL (attempt %d/%d, elapsed %s): %v", jobUUID, attempt, waitForTemporalUpdateMaxRetries, time.Since(retryStart), lastErr)
					if attempt < waitForTemporalUpdateMaxRetries {
						time.Sleep(delay)
						delay = min(delay*2, waitForTemporalUpdateMaxDelay)
					}
				}
				// All retries exhausted — delete the job so it does not linger in NEW
				// state where the orphan-job scheduler cannot see it.
				bgLogger.Errorf("Exhausted all %d attempts to update split job %s to WAIT_FOR_TEMPORAL (total elapsed %s) — marking job deleted", waitForTemporalUpdateMaxRetries, jobUUID, time.Since(retryStart))
				if delErr := se.DeleteJob(bgCtx, jobUUID, lastErr.Error()); delErr != nil {
					bgLogger.Errorf("Failed to delete split job %s after WAIT_FOR_TEMPORAL update exhaustion: %v", jobUUID, delErr)
				}
			}()
			// Clear err so the deferred job-delete and clone-state revert on the main
			// goroutine are suppressed, and the API returns 200.
			err = nil
		} else {
			logger.Infof("Split workflow dispatch failed in %s for volume %s (job %s)", time.Since(workflowDispatchStart), volume.UUID, createdJob.UUID)
			logger.Error("Failed to start volume poll split clone workflow after retries: ", "error", err)
			return nil, "", err
		}
	} else {
		logger.Infof("Split workflow dispatched successfully in %s for volume %s (job %s)", time.Since(workflowDispatchStart), volume.UUID, createdJob.UUID)
	}

	return convertDatastoreVolumeToModel(volume, nil), createdJob.UUID, nil
}

// _splitStopVolume synchronously stops an in-progress thin clone split.
//
// Unlike splitStart, this is NOT a long-running operation: ONTAP halts the
// background data-movement immediately, so there is no Temporal workflow and no
// job entry. The handler is purely a sequence of:
//  1. Read DB state and validate (volume is a clone, split is in progress).
//  2. Resolve the ONTAP node + provider.
//  3. Read pre-stop split progress from ONTAP (best-effort, for the response).
//  4. PATCH split_initiated=false in ONTAP.
//  5. Update VCP DB: clone state → CLONED, clear state details.
//
// `clones_shared_bytes` is intentionally not recomputed here. The split-start
// path sets it to 0 to reserve pool space; after a stop the value will be
// reconciled by the existing periodic volume-refresh workflow which is the
// single source of truth for that field.
//
// The synchronous response derives `cloneSharedBytes` from the immutable
// `VolumeAttributes.OriginalSharedBytes` baseline and the pre-stop split
// percent captured from ONTAP, so callers get a meaningful remaining value
// without waiting for the next refresh cycle.
func _splitStopVolume(ctx context.Context, se database.Storage, params *common.SplitStopVolumeParams) (*models.Volume, error) {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to fetch account for the given projectNumber", "error", err)
		return nil, err
	}

	volume, err := se.GetVolumeWithAccountID(ctx, params.VolumeID, account.ID)
	if err != nil {
		logger.Error("Failed to fetch volume for the given account ID", "error", err)
		return nil, err
	}

	if err = validateSplitStopVolumeParams(ctx, volume); err != nil {
		return nil, err
	}

	dbNodes, err := se.GetNodesByPoolID(ctx, volume.Pool.ID)
	if err != nil {
		logger.Error("Failed to fetch nodes for pool", "error", err)
		return nil, err
	}
	if len(dbNodes) == 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrUnexpectedNodeCountForPool, fmt.Errorf("no nodes found for pool %s", volume.Pool.UUID))
		logger.Error("No nodes found for pool", "error", err)
		return nil, err
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   volume.Pool.DeploymentName,
		OntapCredentials: volume.Pool.PoolCredentials,
	})

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get ONTAP provider for split stop", "error", err)
		return nil, err
	}

	if volume.VolumeAttributes == nil || volume.VolumeAttributes.ExternalUUID == "" {
		logger.Error("Volume missing ExternalUUID, cannot stop split in ONTAP")
		return nil, customerrors.NewUserInputValidationErr("volume has no external UUID, cannot stop split in ONTAP")
	}

	// Best-effort read of split progress before issuing the stop, so the
	// synchronous API response can include the captured percent without
	// requiring the caller to issue a follow-up describeVolume.
	var capturedSplitPercent *int64
	cloneInfo, getErr := provider.GetVolumeCloneInfo(volume.VolumeAttributes.ExternalUUID)
	if getErr != nil {
		// Non-fatal: proceed with the stop without the percent.
		logger.Warnf("Failed to read pre-stop clone info for volume %s (continuing with stop): %v", volume.Name, getErr)
	} else if cloneInfo != nil && cloneInfo.SplitCompletePercent != nil {
		pct := *cloneInfo.SplitCompletePercent
		capturedSplitPercent = &pct
	}

	if err = provider.StopSplitVolume(volume.VolumeAttributes.ExternalUUID); err != nil {
		logger.Errorf("Failed to stop split for volume %s in ONTAP: %v", volume.Name, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	logger.Infof("Split stopped in ONTAP for volume %s", volume.Name)

	// Persist post-stop DB state: revert clone state to CLONED. The
	// split_complete_percent captured above is returned to the caller via the
	// in-memory model below but is NOT persisted on CloneParentInfo because
	// CLONED implies "no active split"; the next periodic refresh will pick up
	// the authoritative ONTAP state.
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil {
		updatedAttrs := *volume.VolumeAttributes
		cloneParentInfo := *volume.VolumeAttributes.CloneParentInfo
		cloneParentInfo.State = datamodel.CloneStateCloned
		cloneParentInfo.StateDetails = ""
		updatedAttrs.CloneParentInfo = &cloneParentInfo
		if updateErr := se.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
			"volume_attributes": &updatedAttrs,
		}); updateErr != nil {
			logger.Errorf("Failed to update clone state to CLONED for volume %s after split stop: %v", volume.UUID, updateErr)
			return nil, updateErr
		}
		volume.VolumeAttributes = &updatedAttrs
	}

	resultModel := convertDatastoreVolumeToModel(volume, nil)
	if resultModel != nil && resultModel.CloneParentInfo != nil && capturedSplitPercent != nil {
		// Return the captured percent to the caller (response-only; not persisted).
		resultModel.CloneParentInfo.SplitCompletePercent = capturedSplitPercent
	}
	// Derive the response cloneSharedBytes from the immutable baseline and the
	// captured pre-stop split percent. `clones_shared_bytes` on the DB row is
	// still 0 (split-quota reservation); the periodic refresh will reconcile it
	// post-stop. For legacy rows without an OriginalSharedBytes baseline we
	// leave the field at its DB value (typically 0 during splitting) rather
	// than fabricate a number.
	if resultModel != nil && volume.VolumeAttributes != nil && volume.VolumeAttributes.OriginalSharedBytes != nil {
		resultModel.CloneSharedBytes = computeRemainingSharedBytes(
			*volume.VolumeAttributes.OriginalSharedBytes,
			resultModel.CloneParentInfo.SplitCompletePercent,
		)
	}
	return resultModel, nil
}

// computeRemainingSharedBytes derives the post-stop shared-bytes remaining
// between a thin clone and its parent from the at-creation baseline and the
// last-observed split-complete percent.
//
//	remaining = original * (100 - percent) / 100
//
// Rules:
//   - percent == nil  → no progress signal available; assume nothing has been
//     copied yet and return the original baseline. Callers who want a stricter
//     contract should ensure percent is captured before invoking.
//   - percent <= 0    → clamp to 0 → returns original.
//   - percent >= 100  → clamp to 100 → returns 0.
//   - otherwise       → linear interpolation.
//
// The arithmetic order (multiply before divide) avoids precision loss for
// realistic clone sizes (uint64 cannot overflow because `original * 100`
// would have to exceed ~1.8e19 bytes, i.e. ~16 EiB — well beyond any
// supported volume size).
func computeRemainingSharedBytes(original uint64, percent *int64) uint64 {
	if percent == nil {
		return original
	}
	p := *percent
	if p <= 0 {
		return original
	}
	if p >= 100 {
		return 0
	}
	return original * uint64(100-p) / 100
}

func _validateSplitStopVolumeParams(ctx context.Context, volume *datamodel.Volume) error {
	logger := util.GetLogger(ctx)

	if volume.VolumeAttributes == nil || volume.VolumeAttributes.CloneParentInfo == nil {
		logger.Errorf("Volume %s is not a thin clone volume (missing clone parent metadata), cannot stop split", volume.Name)
		return customerrors.NewUserInputValidationErr("volume is not a thin clone volume, cannot perform split stop operation")
	}
	if volume.VolumeAttributes.CloneParentInfo.State != datamodel.CloneStateSplitting {
		return customerrors.NewConflictErr("volume split is not in progress, current clone state: " + volume.VolumeAttributes.CloneParentInfo.State)
	}
	return nil
}

func _validateSplitStartVolumeParams(ctx context.Context, se database.Storage, volume *datamodel.Volume, pool *datamodel.PoolView) error {
	logger := util.GetLogger(ctx)

	if volume.VolumeAttributes == nil || volume.VolumeAttributes.CloneParentInfo == nil {
		logger.Errorf("Volume %s is not a thin clone volume (missing clone parent metadata), cannot perform split operation", volume.Name)
		return customerrors.NewUserInputValidationErr("volume is not a thin clone volume, cannot perform split operation")
	}
	if volume.VolumeAttributes.CloneParentInfo.State == datamodel.CloneStateSplitting {
		return customerrors.NewConflictErr("volume split is already in progress")
	}

	if pool.QuotaInBytes+volume.ClonesSharedBytes > uint64(pool.SizeInBytes) {
		logger.Errorf("Insufficient space in pool %s to split the clone volume %s", pool.Name, volume.Name)
		return customerrors.NewUserInputValidationErr("insufficient space in pool to split the clone volume, please free up space and try again")
	}

	hasChild, err := se.HasDependentChildThinClone(ctx, volume.PoolID, volume.UUID)
	if err != nil {
		return err
	}
	if hasChild {
		return vsaerrors.NewVCPError(vsaerrors.ErrSplitBlockedByDependentChildClones, nil)
	}

	replicationCount, err := se.GetVolumeReplicationCountByVolumeID(ctx, volume.ID)
	if err != nil {
		return fmt.Errorf("checking replication state for clone split: %w", err)
	}
	if replicationCount > 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrSplitBlockedByActiveReplication, nil)
	}

	backupCount, err := se.BackupCountByVolumeID(ctx, volume.UUID)
	if err != nil {
		return fmt.Errorf("checking backup state for clone split: %w", err)
	}
	if backupCount > 0 {
		logger.Errorf("Clone volume %s cannot be split while it has %d active backup(s)", volume.Name, backupCount)
		return vsaerrors.NewVCPError(vsaerrors.ErrSplitBlockedByActiveBackup, nil)
	}

	return nil
}

// validateCloneATPolicyMatchParentVolume validates that the clone volume's auto tiering policy matches the parent volume's policy
func validateCloneATPolicyMatchParentVolume(parentVolume *datamodel.Volume, cloneATPolicy *common.AutoTieringPolicy) error {
	parentATEnabled := parentVolume.AutoTieringEnabled
	parentHasATPolicy := parentVolume.AutoTieringPolicy != nil || parentATEnabled

	// If parent has no AT policy set (no policy and not enabled), clone can also have nil AT policy
	if !parentHasATPolicy && cloneATPolicy == nil {
		return nil
	}

	// If parent has no AT policy set, clone cannot set AT policy (must match parent's nil policy)
	if !parentHasATPolicy && cloneATPolicy != nil {
		return customerrors.NewUserInputValidationErr(
			"clone volume auto tiering policy cannot be different from parent volume: auto tiering policy must not be set for clone volume to match parent volume (parent volume has no auto tiering policy set)")
	}

	// If parent has AT policy set (even if paused/disabled), clone must explicitly set AT policy to match
	if parentHasATPolicy && cloneATPolicy == nil {
		// If parent has paused AT policy (policy exists but disabled), use specific message
		if parentVolume.AutoTieringPolicy != nil && !parentATEnabled {
			return customerrors.NewUserInputValidationErr(
				"clone volume auto tiering policy cannot be different from parent volume: auto tiering policy must be explicitly set to match parent volume (auto tiering policy is paused for parent volume)")
		}
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("clone volume auto tiering policy cannot be different from parent volume: auto tiering policy must be explicitly set to match parent volume (auto tiering is %s for parent volume)",
				map[bool]string{true: "enabled", false: "disabled"}[parentATEnabled]))
	}

	cloneATEnabled := cloneATPolicy.AutoTieringEnabled

	// If one is enabled and the other is disabled, they don't match
	if cloneATEnabled != parentATEnabled {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("clone volume auto tiering policy cannot be different from parent volume: auto tiering must be %s for clone volume to match parent volume",
				map[bool]string{true: "enabled", false: "disabled"}[parentATEnabled]))
	}

	// If both are disabled, validation passes (clone explicitly set AT to disabled to match parent)
	if !cloneATEnabled && !parentATEnabled {
		return nil
	}

	// Both are enabled, compare the policies
	// Note: cloneATPolicy is already validated to be non-nil above
	if parentVolume.AutoTieringPolicy == nil {
		return customerrors.NewUserInputValidationErr("clone volume auto tiering policy cannot be different from parent volume: parent volume has auto tiering enabled but no policy details")
	}

	// Compare tiering policy
	if cloneATPolicy.TieringPolicy != parentVolume.AutoTieringPolicy.TieringPolicy {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("clone volume auto tiering policy cannot be different from parent volume: tiering policy must be %s to match parent volume",
				parentVolume.AutoTieringPolicy.TieringPolicy))
	}

	// Compare retrieval policy
	if cloneATPolicy.RetrievalPolicy != parentVolume.AutoTieringPolicy.RetrievalPolicy {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("clone volume auto tiering policy cannot be different from parent volume: retrieval policy must be %s to match parent volume",
				parentVolume.AutoTieringPolicy.RetrievalPolicy))
	}

	// Compare cooling threshold days
	if cloneATPolicy.CoolingThresholdDays != parentVolume.AutoTieringPolicy.CoolingThresholdDays {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("clone volume auto tiering policy cannot be different from parent volume: cooling threshold days must be %d to match parent volume",
				parentVolume.AutoTieringPolicy.CoolingThresholdDays))
	}

	// Compare cloud write mode enabled (handle nil pointers)
	cloneCloudWriteMode := nillable.GetBool(cloneATPolicy.CloudWriteModeEnabled, false)
	parentCloudWriteMode := nillable.GetBool(parentVolume.AutoTieringPolicy.CloudWriteModeEnabled, false)
	if cloneCloudWriteMode != parentCloudWriteMode {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("clone volume auto tiering policy cannot be different from parent volume: cloud write mode must be %v to match parent volume",
				parentCloudWriteMode))
	}

	// Compare hot tier bypass mode enabled
	if cloneATPolicy.HotTierBypassModeEnabled != parentVolume.AutoTieringPolicy.HotTierBypassModeEnabled {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("clone volume auto tiering policy cannot be different from parent volume: hot tier bypass mode must be %v to match parent volume",
				parentVolume.AutoTieringPolicy.HotTierBypassModeEnabled))
	}

	return nil
}
