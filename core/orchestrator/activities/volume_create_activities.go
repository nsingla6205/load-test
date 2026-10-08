package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_policy"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/volumes"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/hydrationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

const (
	VolumeTypeRW                 = "rw"
	VolumeTypeDP                 = "dp"
	CrossRegionBackupType        = datamodel.BackupVaultTypeCrossRegion
	GCBDRServiceType             = datamodel.ServiceTypeCrossProject
	CrossRegionBackupVaultErrMsg = "Cross region backup vaults are not supported for ISCSI volumes"
	RestoreBackupWorkflow        = "RestoreBackupWorkflow"
	BytesPerGB                   = 1073741824 // 1024^3 bytes = 1 GB
	NtfsSecurityStyle            = "ntfs"
)

var GCBDRVaultEnabled = env.GetBool("GCBDR_VAULT_ENABLED", true)

var LargeFileSizeEnabled = env.GetBool("LARGE_FILE_SIZE_ENABLED", true)

type VolumeCreateActivity struct {
	SE        database.Storage
	Scheduler *scheduler.TemporalScheduler
}

var (
	GetResourceNamesForBackup        = _getResourceNamesForBackup
	FindTenancy                      = _findTenancy
	CreateBucket                     = _createBucket
	GenerateResourceNames            = _generateResourceNames
	GetOrCreateAndGCSResources       = _getOrCreateAndGCSResources
	CheckBackupVaultExistsInVCP      = _checkBackupVaultExistsInVCP
	CheckForBucketResourceName       = _checkForBucketResourceName
	CheckIfBackupPolicyExistsInVCP   = _checkIfBackupPolicyExistsInVCP
	CreateBackupPolicyFetchedFromSDE = _createBackupPolicyFetchedFromSDE
	CreateBackupPolicySchedule       = _createBackupPolicySchedule
	GetPoolServiceAccountName        = _getPoolServiceAccount
	GrantStorageObjectAdminRole      = _grantStorageObjectAdminRole
	GetBucket                        = _getBucket
	GetAuthTokenFromContext          = utils.GetAuthTokenFromContext
	GetCoRelationIDFromContext       = utils.GetCoRelationIDFromContext
	FetchVolumeProtocolsFromSDE      = _fetchVolumeProtocolsFromSDE
	FetchBucketDetailsFromGCS        = _fetchBucketDetailsFromGCS
	FetchBackupVaultFromRemoteVCP    = _fetchBackupVaultFromRemoteVCP
	FetchBackupFromRemoteVCP         = _fetchBackupFromRemoteVCP
	FetchBackupOrFallbackToRemoteVCP = _fetchBackupOrFallbackToRemoteVCP
)

var fetchTemporalClient = _fetchTemporalClient

// GetSignedJwtTokenFunc is a variable to allow mocking of auth.GetSignedJwtToken in tests
var GetSignedJwtTokenFunc = auth.GetSignedJwtToken

func _fetchTemporalClient(ctx context.Context) client.Client {
	return activity.GetClient(ctx)
}

func (a VolumeCreateActivity) CreateVolume(ctx context.Context, volume *datamodel.Volume) (*datamodel.Volume, error) {
	se := a.SE

	recordHeartbeat(ctx, fmt.Sprintf("Creating volume %s in database", volume.UUID))
	return se.CreateVolume(ctx, volume)
}

func (a VolumeCreateActivity) GetAggregatesFromOntap(ctx context.Context, volume *datamodel.Volume, node *models.Node, totalNodes int) (*models.AggregateDistributionResult, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Resolving ONTAP provider for volume %s aggregate selection", volume.UUID))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// By default we get all aggregates in a cluster
	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP aggregates for volume %s", volume.UUID))
	res, err := provider.GetAggregates()
	if err != nil || res == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var largeVolumeConstituentCount int64
	// We can't have a large volume constituent count as zero or negative, unless it is updated intentionally. This is being checked at API level
	if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeVolumeConstituentCount != nil && *volume.LargeVolumeAttributes.LargeVolumeConstituentCount > 0 {
		largeVolumeConstituentCount = int64(*volume.LargeVolumeAttributes.LargeVolumeConstituentCount)
	}

	// Get the VSA instance type detail from Pool table
	vlmConfig := &vlm.VLMConfig{}
	err = json.Unmarshal([]byte(volume.Pool.VLMConfig), vlmConfig)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("error unmarshalling VLM config from pool: %v", err))
	}

	var result *models.AggregateDistributionResult
	isActiveActive := volume.Pool.IsActiveActive()
	// Get the aggregate distribution using the optimized greedy approach
	if volume.Pool.AllowAutoTiering {
		result, err = CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, res, largeVolumeConstituentCount, totalNodes, vlmConfig.Deployment.VSAInstanceType, isActiveActive)
	} else {
		result, err = CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, res, largeVolumeConstituentCount, volume.SizeInBytes, totalNodes, vlmConfig.Deployment.VSAInstanceType, isActiveActive)
	}
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Log the distribution details for debugging
	logger.Debugf("CV distribution - Aggregates: %v, HCF: %d", result.Aggregates, result.AggrMultiplier)

	return result, nil
}

func usePrivateCLIVolumeCreate(volume *datamodel.Volume) bool {
	if volume == nil || volume.Pool == nil || volume.Pool.PoolAttributes == nil {
		return false
	}
	if !volume.Pool.IsActiveActive() || volume.Pool.PoolAttributes.LvHAPairs != common.LvHAPairsNodeCount12 {
		return false
	}
	if volume.LargeVolumeAttributes == nil || volume.LargeVolumeAttributes.LargeVolumeConstituentCount == nil {
		return false
	}
	return int64(*volume.LargeVolumeAttributes.LargeVolumeConstituentCount) == common.SixHAPairActiveActiveDefaultConstituentCount()
}

func (a VolumeCreateActivity) CreateVolumeInONTAP(ctx context.Context, volume *datamodel.Volume, node *models.Node, snapshot *datamodel.Snapshot, backup *datamodel.Backup, aggrs *models.AggregateDistributionResult) (*vsa.VolumeResponse, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	orchestrator.RecordHeartbeat(ctx, "Starting CreateVolumeInONTAP activity & Getting ONTAP provider")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	volumeType := VolumeTypeRW
	if volume.VolumeAttributes.IsDataProtection || backup != nil {
		logger.Debug("create a DP volume !")
		volumeType = VolumeTypeDP
	}

	snapshotPolicyName := models.DefaultONTAPSnapshotPolicyName
	if volume.SnapshotPolicy != nil && volume.SnapshotPolicy.Name != "" {
		snapshotPolicyName = volume.SnapshotPolicy.Name
	}
	var restoreFromSnapshotParam vsa.RestoreFromSnapshotParams
	if snapshot != nil {
		restoreFromSnapshotParam.ParentVolumeExternalUUID = snapshot.Volume.VolumeAttributes.ExternalUUID
		restoreFromSnapshotParam.SnapshotUUID = snapshot.GetExternalUUID()
		restoreFromSnapshotParam.ParentVolumeName = snapshot.Volume.Name
		restoreFromSnapshotParam.ParentVolumeSvmName = snapshot.Volume.Svm.Name
		restoreFromSnapshotParam.SnapshotName = snapshot.Name
	}

	params := vsa.CreateVolumeParams{
		VolumeName:          volume.Name,
		SvmName:             volume.Svm.Name,
		Size:                volume.SizeInBytes,
		VolumeType:          volumeType,
		SnapshotPolicyName:  snapshotPolicyName,
		RestoreFromSnapshot: &restoreFromSnapshotParam,
		SnapReserve:         volume.VolumeAttributes.SnapReserve,
		SnapshotDirectory:   volume.VolumeAttributes.SnapshotDirectory,
		TieringPolicy: &vsa.TieringPolicy{
			CoolAccessTieringPolicy: ontapModels.VolumeInlineTieringPolicyNone,
		},
		SecurityStyle: func() *string {
			if volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil && volume.VolumeAttributes.FileProperties.SecurityStyle != "" {
				style := volume.VolumeAttributes.FileProperties.SecurityStyle
				return &style
			}
			// Pass ntfs to ONTAP when protocol is SMB and security style was not provided in request (not persisted on volume).
			if volume.VolumeAttributes != nil && len(volume.VolumeAttributes.Protocols) == 1 && utils.IsSMBProtocol(volume.VolumeAttributes.Protocols[0]) {
				ntfsStyle := NtfsSecurityStyle // ONTAP enum value for SMB default
				return &ntfsStyle
			}
			return nil
		}(),
		UnixPermissions: func() *string {
			if volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil {
				if volumeType != VolumeTypeDP {
					return &volume.VolumeAttributes.FileProperties.UnixPermissions
				}
			}
			return nil
		}(),
	}

	if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeCapacity {
		params.Style = nillable.GetStringPtr(volStyleFlexGroup)
		if volume.LargeVolumeAttributes.LargeVolumeConstituentCount != nil {
			if aggrs != nil {
				params.Aggregates = aggrs.Aggregates
				params.ConstituentsPerAggregate = nillable.GetInt64Ptr(aggrs.AggrMultiplier)
				// A snapshot restore clones the parent layout through REST. Do not
				// send a newly calculated aggregate list through private CLI.
				params.UsePrivateCLI = snapshot == nil && usePrivateCLIVolumeCreate(volume)
			}
		} else {
			// this is being set for auto-provisioning of constituents
			params.TieringSupported = nillable.GetBoolPtr(true)
		}
	} else {
		params.Aggregates = []string{AggregateName}
	}
	if LargeFileSizeEnabled {
		params.LargeSizeEnabled = nillable.ToPointer(true)
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil && volume.VolumeAttributes.FileProperties.ExportPolicy != nil {
		if !utils.IsSMBOnlyProtocols(volume.VolumeAttributes.Protocols) {
			params.ExportPolicy = &volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName
		}
		if params.VolumeType != VolumeTypeDP {
			params.JunctionPath = &volume.VolumeAttributes.FileProperties.JunctionPath
		}
		// When AllSquash is enabled, propagate the first AnonUid found so CreateVolume can
		// issue a post-create VolumeModify to set nas.uid on the volume root directory.
		if utils.IsAllSquashEnabled && params.VolumeType != VolumeTypeDP {
			for _, rule := range volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules {
				if rule.AllSquash != nil && *rule.AllSquash && rule.AnonUid != nil {
					params.AllSquashUid = rule.AnonUid
					break
				}
			}
		}
	}

	// TODO: Once VCM Volume create flow supports AT, then we have to refactor this as this is using db calls which will not be applicable for VCM flow.
	if volume.AutoTieringEnabled && volume.AutoTieringPolicy != nil {
		params.TieringPolicy, err = CreateAutoTieringParams(ctx, se, &params, volume)
		if err != nil {
			return nil, err
		}
	}

	// Handle QoS policy assignment if volume has a VPG (only if MQOS is enabled)
	enableMqos := env.GetBool("ENABLE_MQOS", true)
	logger.Debug("QoS policy assignment check",
		"enable_mqos", enableMqos,
		"volume_id", volume.UUID,
		"vpg_id_valid", volume.VolumePerformanceGroupID.Valid,
		"vpg_id", volume.VolumePerformanceGroupID.Int64)

	if enableMqos && volume.VolumePerformanceGroupID.Valid {
		recordHeartbeat(ctx, "Getting QoS policy for volume performance group")

		vpg := volume.VolumePerformanceGroup
		if vpg == nil {
			logger.Error("Volume has VPG ID but VPG relationship is nil - this should not be possible with foreign key constraints", "volume_id", volume.UUID, "vpg_id", volume.VolumePerformanceGroupID.Int64)
			return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("volume performance group relationship is nil for volume %s with VPG ID %d", volume.UUID, volume.VolumePerformanceGroupID.Int64))
		}

		// QoS policy should already be created at VPG creation time
		// If it's missing, return an error (should not happen in normal flow)
		if vpg.OntapQosPolicyID == "" {
			logger.Error("VPG missing OntapQosPolicyID - QoS policy should have been created at VPG creation time", "vpg_id", vpg.UUID)
			return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("QoS policy not found for VPG %s - should have been created at VPG creation time", vpg.UUID))
		}

		// ONTAP volume create API requires the QoS policy name, not UUID; policy is created with VPG name.
		logger.Info("Assigning QoS policy to volume", "volume_id", volume.UUID, "vpg_id", vpg.UUID, "qos_policy_name", vpg.Name, "qos_policy_uuid", vpg.OntapQosPolicyID)
		params.QosPolicy = &vpg.Name
	} else {
		if !enableMqos {
			logger.Debug("Skipping QoS policy assignment: ENABLE_MQOS feature flag is disabled", "volume_id", volume.UUID)
		} else if !volume.VolumePerformanceGroupID.Valid {
			logger.Debug("Skipping QoS policy assignment: volume does not have a valid VPG ID", "volume_id", volume.UUID)
		}
	}

	orchestrator.RecordHeartbeat(ctx, "Starting volume creation in ONTAP")
	logger.Info("Creating volume in ONTAP",
		"volume_id", volume.UUID,
		"use_private_cli", params.UsePrivateCLI,
	)
	res, err := provider.CreateVolume(params)

	if err != nil {
		logger.Error("Error in provider.CreateVolume", "err", err)
		if errors.IsConflictErr(err) {
			return HandleVolumeCreateConflict(volume, provider)
		}
		return nil, err
	}
	logger.Debug("volume created successfully")

	orchestrator.RecordHeartbeat(ctx, "Finished CreateVolumeInONTAP activity")
	return res, nil
}

func CreateAutoTieringParams(ctx context.Context, se database.Storage, params *vsa.CreateVolumeParams, volume *datamodel.Volume) (*vsa.TieringPolicy, error) {
	// If auto-tiering is paused for pool, we don't set the all auto-tiering policy during
	// volume creation in ontap. Since this supersedes the tiering fullness threshold and
	// doesn't stop tiering. We let the volume be created with default tiering policy 'none'
	// This will get later corrected when the pool will resume auto-tiering.
	shouldSetTieringPolicy := true

	if volume.AutoTieringPolicy.TieringPolicy == ontapModels.VolumeInlineTieringPolicyAll {
		// Fetch pool from db to check if auto-tiering is currently paused
		dbPool, err := se.GetPoolByUUIDForAccount(ctx, volume.Pool.UUID, volume.AccountID)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}

		shouldSetTieringPolicy = dbPool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPaused && dbPool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPartiallyPaused
	}

	if shouldSetTieringPolicy {
		params.TieringPolicy.CoolAccessTieringPolicy = nillable.GetString(&volume.AutoTieringPolicy.TieringPolicy, utils.FetchTieringPolicyAsPerVolumeType(!utils.IsSanProtocols(volume.VolumeAttributes.Protocols)))
		params.TieringPolicy.CoolAccessRetrievalPolicy = nillable.GetString(&volume.AutoTieringPolicy.RetrievalPolicy, ontapModels.VolumeCloudRetrievalPolicyDefault)
		params.TieringPolicy.CoolnessPeriod = int64(volume.AutoTieringPolicy.CoolingThresholdDays)
		params.TieringPolicy.CloudWriteModeEnabled = volume.AutoTieringPolicy.CloudWriteModeEnabled
	} else {
		params.TieringPolicy.CoolAccessTieringPolicy = ontapModels.VolumeInlineTieringPolicyNone
		params.TieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(false)
	}

	return params.TieringPolicy, nil
}

// UpdateVolumeAutoTieringPolicyInONTAP updates the auto tiering policy for a volume in ONTAP
// This is used after clone creation since ONTAP doesn't accept auto tiering policy during clone creation
// It also handles the case where AT is disabled for the clone - explicitly sets it to "none" to prevent
// ONTAP from inheriting the parent volume's AT policy
func (a VolumeCreateActivity) UpdateVolumeAutoTieringPolicyInONTAP(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	logger := util.GetLogger(ctx)
	se := a.SE
	recordHeartbeat(ctx, "Starting UpdateVolumeAutoTieringPolicyInONTAP activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	updateVolumeParams := &vsa.UpdateVolumeParams{
		UUID: volume.VolumeAttributes.ExternalUUID,
	}

	updateVolumeParams.TieringPolicy = &vsa.TieringPolicy{}

	// If auto tiering is not enabled or policy is nil, explicitly disable it in ONTAP
	// This prevents ONTAP from inheriting the parent volume's AT policy
	if !volume.AutoTieringEnabled || volume.AutoTieringPolicy == nil {
		logger.Debugf("Auto tiering is disabled or policy is nil for clone volume %s, explicitly disabling AT in ONTAP", volume.Name)
		updateVolumeParams.TieringPolicy.CoolAccessTieringPolicy = ontapModels.VolumeInlineTieringPolicyNone
		updateVolumeParams.TieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(false)
	} else {
		// Auto tiering is enabled, set the policy
		// Check if pool auto-tiering is paused (similar logic to CreateAutoTieringParams)
		pool := &datamodel.Pool{
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				TieringStatus: datamodel.TieringStatusResumed,
			},
		}
		if volume.AutoTieringPolicy.TieringPolicy == ontapModels.VolumeInlineTieringPolicyAll {
			var poolErr error
			pool, poolErr = se.GetPoolByUUIDForAccount(ctx, volume.Pool.UUID, volume.AccountID)
			if poolErr != nil {
				return vsaerrors.WrapAsTemporalApplicationError(poolErr)
			}
		}

		shouldSetTieringPolicy := pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPaused && pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPartiallyPaused

		if shouldSetTieringPolicy {
			updateVolumeParams.TieringPolicy.CoolAccessTieringPolicy = nillable.GetString(&volume.AutoTieringPolicy.TieringPolicy, utils.FetchTieringPolicyAsPerVolumeType(!utils.IsSanProtocols(volume.VolumeAttributes.Protocols)))
			updateVolumeParams.TieringPolicy.CoolAccessRetrievalPolicy = nillable.GetString(&volume.AutoTieringPolicy.RetrievalPolicy, ontapModels.VolumeCloudRetrievalPolicyDefault)
			updateVolumeParams.TieringPolicy.CloudWriteModeEnabled = volume.AutoTieringPolicy.CloudWriteModeEnabled
			if updateVolumeParams.TieringPolicy.CloudWriteModeEnabled == nil && !utils.IsSanProtocols(volume.VolumeAttributes.Protocols) {
				updateVolumeParams.TieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(false)
			}
			updateVolumeParams.TieringPolicy.CoolnessPeriod = int64(volume.AutoTieringPolicy.CoolingThresholdDays)
		} else {
			updateVolumeParams.TieringPolicy.CoolAccessTieringPolicy = ontapModels.VolumeInlineTieringPolicyNone
			updateVolumeParams.TieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(false)
		}
	}

	err = provider.UpdateVolume(*updateVolumeParams)
	if err != nil {
		logger.Errorf("Failed to update auto tiering policy for volume %s in ONTAP: %v", volume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Debugf("Auto tiering policy updated successfully for volume %s in ONTAP", volume.Name)

	recordHeartbeat(ctx, "Finished UpdateVolumeAutoTieringPolicyInONTAP activity")
	return nil
}

func (a VolumeCreateActivity) UpdateLunName(ctx context.Context, volume *datamodel.Volume, node *models.Node, restoreVolCreateResponse *vsa.VolumeResponse) (*vsa.LunResponse, error) {
	recordHeartbeat(ctx, "Initializing LUN name update")
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "Retrieving existing LUN")
	lunName := utils.GetLunName(volume.Name)

	lun, err := LunGet(ctx, "", volume.Name, volume.Svm.Name, provider)
	if err != nil {
		logger.Debug("lun not found !")
		return nil, err
	}
	response := lun.ProviderResponse
	uuid := response.ExternalUUID
	logger.Debugf("\n\nLun Name : %s\n\n", lun.Name)
	lunSpace := restoreVolCreateResponse.AFSSize - restoreVolCreateResponse.MetadataSize
	lunUpdateParams := vsa.LunUpdateParams{
		UUID:       uuid,
		LunName:    lunName,
		VolumeName: volume.Name,
		SvmName:    volume.Svm.Name,
	}
	if lunSpace < lun.Size {
		lunUpdateParams.Size = lun.Size
	} else {
		lunUpdateParams.Size = lunSpace
	}
	recordHeartbeat(ctx, "Updating LUN in ONTAP")
	err = provider.LunUpdate(lunUpdateParams)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrRestoreVolumeValidation, err))
	}
	logger.Debug("lun updated successfully")
	recordHeartbeat(ctx, "Retrieving updated LUN")
	lun, err = LunGet(ctx, lunName, volume.Name, volume.Svm.Name, provider)
	if err != nil {
		logger.Debug("lun not found !")
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "LUN name updated successfully")
	return lun, nil
}

func (a VolumeCreateActivity) CreateExportPolicyInOntap(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting CreateExportPolicyInOntap activity")
	if volume.VolumeAttributes == nil || volume.VolumeAttributes.FileProperties == nil {
		logger.Info("Skipping export policy creation for non-file volume")
		return nil
	}
	if volume.Svm == nil {
		svm, err := a.SE.GetSvmForPoolID(ctx, volume.PoolID)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		volume.Svm = svm
	}
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	vsaExportRules := make([]*vsa.ExportRule, 0)
	for _, rule := range volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules {
		vsaExportRule := &vsa.ExportRule{
			AccessType:          rule.AccessType,
			AllowedClients:      rule.AllowedClients,
			CIFS:                rule.CIFS,
			NFSv3:               rule.NFSv3,
			NFSv4:               rule.NFSv4,
			Index:               rule.Index,
			AnonymousUser:       rule.AnonymousUser,
			Kerberos5ReadOnly:   rule.Kerberos5ReadOnly,
			Kerberos5ReadWrite:  rule.Kerberos5ReadWrite,
			Kerberos5pReadOnly:  rule.Kerberos5pReadOnly,
			Kerberos5pReadWrite: rule.Kerberos5pReadWrite,
			Kerberos5iReadOnly:  rule.Kerberos5iReadOnly,
			Kerberos5iReadWrite: rule.Kerberos5iReadWrite,
			Superuser:           rule.Superuser,
			AllSquash:           rule.AllSquash,
			AnonUid:             rule.AnonUid,
			UnixReadOnly:        rule.UnixReadOnly,
			UnixReadWrite:       rule.UnixReadWrite,
		}
		vsaExportRules = append(vsaExportRules, vsaExportRule)
	}
	vsaExportPolicy := &vsa.ExportPolicy{
		ExportPolicyName: volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName,
		SvmName:          volume.Svm.Name,
		ExportRules:      vsaExportRules,
	}
	err = provider.CreateExportPolicy(vsaExportPolicy)
	if err != nil {
		if errors.IsConflictErr(err) || strings.Contains(err.Error(), "duplicate entry") {
			// If export policy already exists, we can skip creation
			logger.Debug("Export policy already exists, skipping creation", "name", vsaExportPolicy.ExportPolicyName)
			return nil
		}
		return err
	}
	orchestrator.RecordHeartbeat(ctx, "Finished CreateExportPolicyInOntap activity")
	return nil
}

func HandleVolumeCreateConflict(volume *datamodel.Volume, provider vsa.Provider) (*vsa.VolumeResponse, error) {
	isRestore := false
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.RestoredBackupPath != "" {
		isRestore = true
	}
	volumeRes, err := provider.GetVolume(vsa.GetVolumeParams{
		VolumeName: volume.Name,
		SvmName:    volume.Svm.Name,
		IsRestore:  isRestore,
	})
	if err != nil {
		return nil, err
	}
	if volumeRes.State != ontapModels.VolumeStateOnline {
		err = provider.DeleteVolume(volume.VolumeAttributes.ExternalUUID, volume.Name)
		if err != nil {
			return nil, err
		}
		return nil, errors.New("volume " + volume.Name + " is not in online state, deleting & retrying creation")
	}
	return volumeRes, nil
}

func (a VolumeCreateActivity) CreateIgroup(ctx context.Context, volume *datamodel.Volume, hostParams []*common.HostParams, node *models.Node) error {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting CreateIgroup activity")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	for _, host := range hostParams {
		orchestrator.RecordHeartbeat(ctx, "Checking if igroup exists in ONTAP")
		igroupExists, _, err := provider.IgroupExists(host.HostName, &volume.Svm.Name)
		if err != nil {
			return err
		}

		if !igroupExists {
			orchestrator.RecordHeartbeat(ctx, "Creating igroup in ONTAP")
			_, err := provider.IgroupCreate(vsa.IgroupCreateParams{
				IgroupName: host.HostName,
				SvmName:    volume.Svm.Name,
				OsType:     host.OsType,
				Initiator:  host.HostIQNs,
			})
			if err != nil {
				return err
			}
			logger.Debug("Igroup created successfully", "name", host.HostName)
		}
	}

	orchestrator.RecordHeartbeat(ctx, "Finished CreateIgroup activity")
	return nil
}

func (a VolumeCreateActivity) CreateLun(ctx context.Context, volume *datamodel.Volume, node *models.Node, availableSpace int64) (*vsa.LunResponse, error) {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting CreateLun activity")
	if volume.VolumeAttributes.IsDataProtection {
		logger.Info("Skipping lun creation for data protection volume")
		return &vsa.LunResponse{}, nil
	}
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	lunName := utils.GetLunName(volume.Name)
	osType := ""
	if volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
		osType = (*volume.VolumeAttributes.BlockDevices)[0].OSType
		lunName = (*volume.VolumeAttributes.BlockDevices)[0].Name
	} else {
		osType = volume.VolumeAttributes.BlockProperties.OSType
	}

	orchestrator.RecordHeartbeat(ctx, "Creating LUN in ONTAP")
	lun, err := provider.LunCreate(vsa.LunCreateParams{
		LunName:    lunName,
		VolumeName: volume.Name,
		SvmName:    volume.Svm.Name,
		OsType:     osType,
		Size:       availableSpace,
	})
	if err != nil {
		if errors.IsConflictErr(err) {
			orchestrator.RecordHeartbeat(ctx, "LUN already exists, retrieving existing LUN")
			return LunGet(ctx, lunName, volume.Name, volume.Svm.Name, provider)
		}
		return nil, err
	}
	orchestrator.RecordHeartbeat(ctx, "Finished CreateLun activity")
	logger.Debug("lun created successfully")
	return lun, nil
}

func (a VolumeCreateActivity) UpdateVolumeStateInDB(ctx context.Context, volumeUUID, state, stateDetails string) error {
	se := a.SE
	recordHeartbeat(ctx, "Updating volume state in database")

	err := se.UpdateVolumeFields(ctx, volumeUUID, map[string]interface{}{
		"state":         state,
		"state_details": stateDetails,
	})
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "Volume state updated successfully")

	return nil
}

func LunGet(ctx context.Context, lunName, volumeName, svmName string, provider vsa.Provider) (*vsa.LunResponse, error) {
	logger := util.GetLogger(ctx)

	lun, err := provider.LunGet(vsa.LunGetParams{
		SvmName:    svmName,
		VolumeName: volumeName,
		LunName:    lunName,
	})
	if err != nil {
		return nil, err
	}

	logger.Debug("lun retrieved successfully", "lunName", lunName, "volumeName", volumeName, "svmName", svmName)
	return lun, nil
}

func (a VolumeCreateActivity) GetOntapClusterHealth(ctx context.Context, node *models.Node) (*bool, error) {
	logger := util.GetLogger(ctx)
	isOntapClusterHealthy := new(bool)
	*isOntapClusterHealthy = false

	orchestrator.RecordHeartbeat(ctx, "Getting ONTAP provider")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return isOntapClusterHealthy, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	ontapProvider, ok := provider.(*vsa.OntapRestProvider)
	if !ok {
		return isOntapClusterHealthy, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("provider is not OntapRestProvider"))
	}

	orchestrator.RecordHeartbeat(ctx, "Getting ONTAP version to check cluster connectivity")
	_, err = ontapProvider.GetONTAPVersion()
	if err != nil {
		logger.Errorf("Failed to get ONTAP version: %v", err)
		retriesExhaustedMsg := "Retries exhausted when attempting to reach the storage server"
		if errors.IsTimeoutErr(err) || strings.Contains(err.Error(), retriesExhaustedMsg) {
			return isOntapClusterHealthy, nil
		}
		return isOntapClusterHealthy, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	*isOntapClusterHealthy = true
	return isOntapClusterHealthy, nil
}

func (a VolumeCreateActivity) CreateLunMap(ctx context.Context, volume *datamodel.Volume, params *common.CreateLunMapParams, node *models.Node) error {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting CreateLunMap activity")
	if volume.VolumeAttributes.IsDataProtection {
		logger.Info("Skipping CreateLunMap for data protection volume")
		return nil
	}
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	orchestrator.RecordHeartbeat(ctx, "Creating LUN map in ONTAP")
	err = provider.LunMapCreate(vsa.LunMapCreateParams{
		LunName:    params.LunName,
		SvmName:    params.SvmName,
		IGroupName: params.HostNames,
	})
	if err != nil {
		orchestrator.RecordHeartbeat(ctx, "Error creating LUN map in ONTAP")
		if errors.IsConflictErr(err) {
			return nil
		}
		return err
	}
	orchestrator.RecordHeartbeat(ctx, "Finished CreateLunMap activity")
	logger.Debug("lun map created successfully")
	return nil
}

// mergeRestrictedActionsFromExisting copies restricted_actions from existing when attrs would clear them.
func mergeRestrictedActionsFromExisting(attrs, existing *datamodel.VolumeAttributes) {
	if attrs == nil || existing == nil {
		return
	}
	if len(attrs.RestrictedActions) == 0 && len(existing.RestrictedActions) > 0 {
		attrs.RestrictedActions = existing.RestrictedActions
	}
}

func (a VolumeCreateActivity) UpdateVolumeDetails(ctx context.Context, volume *datamodel.Volume, volCreateResponse *vsa.ProviderResponse) error {
	se := a.SE
	recordHeartbeat(ctx, "Starting UpdateVolumeDetails activity")

	if volume != nil && volume.UUID != "" && volume.VolumeAttributes != nil {
		existing, err := se.GetVolume(ctx, volume.UUID)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if existing != nil {
			mergeRestrictedActionsFromExisting(volume.VolumeAttributes, existing.VolumeAttributes)
		}
	}

	volume.VolumeAttributes.ExternalUUID = volCreateResponse.ExternalUUID
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.RestoredBackupPath != "" {
		// This is volume restore case
		volume.State = datamodel.LifeCycleStateRestoring
		volume.StateDetails = datamodel.LifeCycleStateRestoringDetails
	} else {
		volume.State = datamodel.LifeCycleStateREADY
		volume.StateDetails = datamodel.LifeCycleStateAvailableDetails
	}
	if err := se.UpdateVolume(ctx, volume); err != nil {
		return err
	}

	recordHeartbeat(ctx, "Finished UpdateVolumeDetails activity")
	return nil
}

func (a VolumeCreateActivity) FinaliseRestoredVolume(ctx context.Context, volume *datamodel.Volume) error {
	se := a.SE
	volume.State = datamodel.LifeCycleStateREADY
	volume.StateDetails = datamodel.LifeCycleStateAvailableDetails
	recordHeartbeat(ctx, fmt.Sprintf("Finalising restored volume %s in database", volume.UUID))
	if err := se.UpdateVolume(ctx, volume); err != nil {
		return err
	}

	return nil
}

func (a VolumeCreateActivity) GetHosts(ctx context.Context, volume *datamodel.Volume) ([]*datamodel.HostGroup, error) {
	se := a.SE
	recordHeartbeat(ctx, "Starting GetHosts activity")

	if volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
		blockDevice := (*volume.VolumeAttributes.BlockDevices)[0]
		uuids := utils.GetHgUUIDs(blockDevice.HostGroupDetails)

		recordHeartbeat(ctx, "Fetching host groups from database")
		dbHostGroups, err := se.GetMultipleHostGroups(ctx, uuids, volume.AccountID)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}

		if len(dbHostGroups) != len(uuids) {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrAllHostGroupsNotFoundError, errors.New("all host groups could not be found")))
		}
		return dbHostGroups, nil
	}

	if volume.VolumeAttributes.BlockProperties == nil {
		return nil, errors.New("block properties not found")
	}

	uuids := utils.GetHgUUIDs(volume.VolumeAttributes.BlockProperties.HostGroupDetails)

	recordHeartbeat(ctx, "Fetching host groups from database")
	dbHostGroups, err := se.GetMultipleHostGroups(ctx, uuids, volume.AccountID)
	if err != nil {
		return nil, err
	}

	if len(dbHostGroups) != len(uuids) {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrAllHostGroupsNotFoundError, errors.New("all host groups could not be found")))
	}

	recordHeartbeat(ctx, "Finished GetHosts activity")
	return dbHostGroups, nil
}

func (a VolumeCreateActivity) GetVolumesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Volume, error) {
	recordHeartbeat(ctx, fmt.Sprintf("GetVolumesByPoolID started poolID=%d", poolID))
	volumes, err := a.SE.GetVolumesByPoolID(ctx, poolID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, fmt.Sprintf("GetVolumesByPoolID completed poolID=%d volumes=%d", poolID, len(volumes)))
	return volumes, nil
}

// GetPoolVolumesForQosTransition loads pool volumes for manual↔auto qosType transition using a single
// lightweight DB query (no association preloads). Used instead of GetVolumesByPoolID on large pools.
func (a VolumeCreateActivity) GetPoolVolumesForQosTransition(ctx context.Context, poolID int64) ([]*datamodel.Volume, error) {
	recordHeartbeat(ctx, fmt.Sprintf("GetPoolVolumesForQosTransition started poolID=%d", poolID))
	volumes, err := a.SE.GetPoolVolumesForQosTransition(ctx, poolID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, fmt.Sprintf("GetPoolVolumesForQosTransition completed poolID=%d volumes=%d", poolID, len(volumes)))
	return volumes, nil
}

func (a VolumeCreateActivity) GetVolumeByVolumeID(ctx context.Context, volumeID string) (*datamodel.Volume, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Fetching volume %s from database", volumeID))
	dbVolume, err := se.DescribeVolume(ctx, volumeID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return dbVolume, nil
}

// deleteVolumeOnPoolValidationFailure performs best-effort volume deletion when pool validation fails,
// so the volume does not remain in ERROR state. Returns the given err wrapped as non-retryable.
func (a VolumeCreateActivity) deleteVolumeOnPoolValidationFailure(ctx context.Context, volumeUUID string, err error) error {
	if volumeUUID != "" {
		recordHeartbeat(ctx, fmt.Sprintf("Deleting volume %s after pool validation failure", volumeUUID))
		if _, delErr := a.SE.DeleteVolume(ctx, volumeUUID); delErr != nil {
			util.GetLogger(ctx).Errorf("Failed to delete volume %s on pool validation failure: %v", volumeUUID, delErr)
		}
	}
	return vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
}

// ValidatePoolStateForVolumeCreate ensures the pool is in a state that allows volume creation.
// Returns a non-retryable validation error if the pool is Creating, Deleting, or Deleted.
// When validation fails, the volume record is deleted before returning so it does not remain in ERROR state.
func (a VolumeCreateActivity) ValidatePoolStateForVolumeCreate(ctx context.Context, pool *datamodel.Pool, volumeUUID string) error {
	if pool == nil {
		return nil
	}
	recordHeartbeat(ctx, fmt.Sprintf("Validating pool %s state for volume %s", pool.UUID, volumeUUID))
	dbPool, err := a.SE.GetPoolByUUIDForAccount(ctx, pool.UUID, pool.AccountID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return a.deleteVolumeOnPoolValidationFailure(ctx, volumeUUID,
				vsaerrors.NewVCPError(vsaerrors.ErrVolumeCreationFailedDueToPoolIsDeleted,
					fmt.Errorf("specified pool is in Deleted state, hence volume cannot be created")))
		}
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) && customErr != nil && customErr.IsError(vsaerrors.ErrPoolNotFound) {
			return a.deleteVolumeOnPoolValidationFailure(ctx, volumeUUID,
				vsaerrors.NewVCPError(vsaerrors.ErrVolumeCreationFailedDueToPoolIsDeleted,
					fmt.Errorf("specified pool is in Deleted state, hence volume cannot be created")))
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if dbPool == nil {
		return nil
	}
	if dbPool.State == datamodel.LifeCycleStateDeleting {
		return a.deleteVolumeOnPoolValidationFailure(ctx, volumeUUID,
			vsaerrors.NewVCPError(vsaerrors.ErrVolumeCreationFailedDueToPoolInDeletion,
				fmt.Errorf("specified pool is in Deleting state, hence volume cannot be created")))
	}
	return nil
}

func _findTenancy(ctx context.Context, cloudService hyperscaler.Services, consumerVPC string, customerProjectNumber string, tenantProjectRegion *string) (*common.TenancyInfo, error) {
	// need to pass tenantProjectRegion only in case of CBR where region != the regional region as set from env variable
	if tenantProjectRegion == nil {
		tenantProjectRegion = &Region
	}

	recordHeartbeat(ctx, fmt.Sprintf("Finding tenancy for customer project %s", customerProjectNumber))
	tenantProjectNumber, err := cloudService.GetTenantProject(consumerVPC, customerProjectNumber, *tenantProjectRegion)
	if err != nil {
		cloudService.GetLogger().Errorf("Error finding tenancy unit: %v", err)
		return nil, err
	}

	return &common.TenancyInfo{
		RegionalTenantProject: tenantProjectNumber,
	}, nil
}

func (a VolumeCreateActivity) FindTenancy(ctx context.Context, consumerVPC string, customerProjectNumber string, tenantProjectRegion *string) (*common.TenancyInfo, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for customer project %s tenancy", customerProjectNumber))
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	cloudService.GetLogger().Debug("cloudService initialized")
	return FindTenancy(ctx, cloudService, consumerVPC, customerProjectNumber, tenantProjectRegion)
}

func _checkBackupVaultExistsInVCP(ctx context.Context, se database.Storage, volume *datamodel.Volume, region string) (*datamodel.BackupVault, error) {
	bvId := volume.DataProtection.BackupVaultID
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup vault %s from database", bvId))
	backupVault, err := se.GetBackupVaultByUUIDndOwnerID(ctx, bvId, volume.AccountID)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return nil, err
		}
	}
	if backupVault == nil && GCBDRVaultEnabled {
		recordHeartbeat(ctx, fmt.Sprintf("Fetching shared backup vault %s from database", bvId))
		backupVault, err = se.GetBackupVault(ctx, bvId)
		if err != nil {
			if !errors.IsNotFoundErr(err) {
				return nil, err
			}
		}
		if backupVault != nil && backupVault.ServiceType != GCBDRServiceType {
			backupVault = nil
		}
	}

	if backupVault != nil {
		if backupVault.ImmutableAttributes != nil && !utils.IsImmutableBackupEnabled() {
			err := validateImmutableBackupVault(*backupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration)
			if err != nil {
				return nil, err
			}
		}
		if backupVault.BackupVaultType == CrossRegionBackupType {
			if backupVault.BackupRegionName != nil && *backupVault.BackupRegionName == region {
				return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCrossRegionBackupVaultAssignmentToDestinationRegion, fmt.Errorf("cannot assign a cross-region backup vault to a volume in the destination region")))
			}
		}
		return backupVault, nil
	}
	if !utils.IsCVPHostConfigured() {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("backup vault with id %s not found", bvId)))
	}
	bvParams := &datamodel.BackupVault{}

	logger := util.GetLogger(ctx)
	GetSignedJwtToken := utils.GetAuthTokenFromContext(ctx)
	cvpClient := CvpCreateClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Listing backup vaults from CVP for volume %s", volume.UUID))
	vaults, err := cvpClient.BackupVault.V1betaListBackupVaults(&backup_vault.V1betaListBackupVaultsParams{
		LocationID:     region,
		ProjectNumber:  volume.Account.Name,
		XCorrelationID: &xCorrelationID,
	})
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return nil, errors.NewNotFoundErr("Backup vault", nil)
		}
		logger.Error("Error checking backupVault : ", err)
		return nil, err
	}

	bvs := vaults.Payload.BackupVaults

	for _, bv := range bvs {
		if bv.BackupVaultID == bvId {
			if bv.BackupRetentionPolicy != nil && !utils.IsImmutableBackupEnabled() {
				err := validateImmutableBackupVault(*bv.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays)
				if err != nil {
					return nil, err
				}
			}
			bvModel, err := ConvertToBackupVaultDataModel(bv, region)
			if err != nil {
				return nil, err
			}
			// Validate CrossRegionBackupVaultName for cross-region backup vaults
			if bvModel.BackupVaultType == CrossRegionBackupType {
				if bvModel.CrossRegionBackupVaultName == nil || *bvModel.CrossRegionBackupVaultName == "" {
					return nil, errors.NewBadRequestErr("Cross-region backup vault name must be specified for cross-region backup vault")
				}
				if bvModel.BackupRegionName != nil && *bvModel.BackupRegionName == region {
					return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCrossRegionBackupVaultAssignmentToDestinationRegion, fmt.Errorf("cannot assign a cross-region backup vault to a volume in the destination region")))
				}
			}
			bvParams = bvModel
			break
		}
	}

	if bvParams.UUID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("backup vault with id %s not found", bvId)))
	}

	bvParams.AccountID = volume.AccountID
	recordHeartbeat(ctx, fmt.Sprintf("Creating backup vault %s in database", bvId))
	createdBackupVault, err := se.CreateBackupVaultEntryInVCP(ctx, bvParams)
	if err != nil {
		return nil, err
	}

	return createdBackupVault, nil
}

func validateImmutableBackupVault(minRetentionDuration int64) error {
	if minRetentionDuration > 0 {
		return errors.NewBadRequestErr(utils.ImmutableBackupVaultErrMsg)
	}
	return nil
}

func (a VolumeCreateActivity) CheckBackupVaultExistsInVCP(ctx context.Context, volume *datamodel.Volume, region string) (*datamodel.BackupVault, error) {
	return CheckBackupVaultExistsInVCP(ctx, a.SE, volume, region)
}

func (a VolumeCreateActivity) CheckOrCreateRemoteBackupVaultInVCP(ctx context.Context, volume *datamodel.Volume, backupVault *datamodel.BackupVault, bucketDetails *common.BucketDetails) (*datamodel.BackupVault, error) {
	return CheckOrCreateRemoteBackupVaultInVCP(ctx, a.SE, volume, backupVault, bucketDetails)
}

func (a VolumeCreateActivity) UpdateRemoteBackupVaultWithBucketDetails(ctx context.Context, volume *datamodel.Volume, sourceBV *datamodel.BackupVault, remoteBV *datamodel.BackupVault, bucketDetails *common.BucketDetails) error {
	return UpdateRemoteBackupVaultWithBucketDetails(ctx, a.SE, volume, sourceBV, remoteBV, bucketDetails)
}

// SetupCrossRegionBackupPermissionsActivity sets up IAM permissions for cross-region backup vaults
// This activity grants the necessary permissions for the backup vault in region2 to access resources in region1
func (a *VolumeCreateActivity) SetupCrossRegionBackupPermissionsActivity(ctx context.Context, backupVault *datamodel.BackupVault, pool *datamodel.Pool, bucketDetails *common.BucketDetails) error {
	logger := util.GetLogger(ctx)
	volumeRegion := pool.ClusterDetails.RegionalTenantProject
	backupRegion := *backupVault.BackupRegionName
	if volumeRegion == backupRegion {
		logger.Infof("Volume and backup are in same region, skipping cross-region permission setup")
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Setting up cross-region backup permissions for vault %s", backupVault.UUID))

	poolServiceAccount, err := getBackupVaultPoolServiceAccount(pool, pool.ClusterDetails.RegionalTenantProject)
	if err != nil {
		logger.Errorf("Failed to get pool service account name: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Grant storage.objectAdmin role to the pool service account in the backup tenant project
	backupTenantProject := bucketDetails.TenantProjectNumber
	if backupTenantProject == "" {
		logger.Errorf("Backup vault %s missing TenantProjectNumber in bucket details", backupVault.UUID)
		return temporal.NewNonRetryableApplicationError(
			"TenantProjectNumber is required for cross-region permission setup",
			"MissingTenantProjectNumber",
			nil,
		)
	}

	logger.Infof("Backup tenant project: %s", backupTenantProject)
	err = grantBackupVaultStorageObjectAdminRole(ctx, poolServiceAccount, backupTenantProject)
	if err != nil {
		logger.Errorf("Failed to grant storage.objectAdmin role for cross-region backup: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully granted storage.objectAdmin role to service account %s in backup project %s for cross-region access", poolServiceAccount, backupTenantProject)

	// Track the tenant project for IAM cleanup during pool deletion
	recordHeartbeat(ctx, fmt.Sprintf("Tracking cross-region backup permissions for pool %s", pool.UUID))
	err = addServiceAccountPermissionProject(ctx, a.SE, pool.UUID, backupTenantProject)
	if err != nil {
		logger.Errorf("Failed to track tenant project %s for pool %s: %v", backupTenantProject, pool.UUID, err)
	}

	return nil
}

// getBackupVaultPoolServiceAccount extracts the service account from the pool for backup vault operations
func getBackupVaultPoolServiceAccount(pool *datamodel.Pool, projectID string) (string, error) {
	saEmail := utils.ConstructServiceAccountEmail(pool.ServiceAccountId, projectID)
	return saEmail, nil
}

// grantBackupVaultStorageObjectAdminRole grants the storage.objectAdmin role to a service account for backup vault operations
func grantBackupVaultStorageObjectAdminRole(ctx context.Context, serviceAccountEmail, projectID string) error {
	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for project %s role grant", projectID))
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Grant the specific role needed for backup access
	roles := []string{"roles/storage.objectAdmin"}
	recordHeartbeat(ctx, fmt.Sprintf("Granting backup access role in project %s", projectID))
	err = cloudService.AttachOrUpdateRolesForServiceAccounts(roles, serviceAccountEmail, projectID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, fmt.Sprintf("Finished granting backup access role in project %s", projectID))

	return nil
}

func (a VolumeCreateActivity) CheckForBucketResourceName(ctx context.Context, volume *datamodel.Volume) (*common.BucketDetails, error) {
	return CheckForBucketResourceName(ctx, a.SE, volume)
}

func _checkForBucketResourceName(ctx context.Context, se database.Storage, volume *datamodel.Volume) (*common.BucketDetails, error) {
	logger := util.GetLogger(ctx)

	bvDetails, err := getBackupVaultDetails(se, ctx, volume.DataProtection.BackupVaultID, volume.AccountID)
	if err != nil {
		logger.Errorf("Error getting backup vault details: %v", err)
		return nil, err
	}
	if bvDetails != nil && bvDetails.BucketDetails != nil {
		for i, bucket := range bvDetails.BucketDetails {
			if bvDetails.ServiceType == GCBDRServiceType {
				if i == 0 && bucket.BucketName != "" {
					return &common.BucketDetails{
						BucketName:          bucket.BucketName,
						ServiceAccountName:  bucket.ServiceAccountName,
						VendorSubnetID:      bucket.VendorSubnetID,
						TenantProjectNumber: bucket.TenantProjectNumber,
						HmacAccessKeyID:     bucket.HmacAccessKeyID,
						HmacSecretID:        bucket.HmacSecretID,
					}, nil
				}
				break
			}
			if strings.Contains(bucket.BucketName, volume.DataProtection.BackupVaultID) && volume.VolumeAttributes.VendorSubnetID == bucket.VendorSubnetID {
				return &common.BucketDetails{
					BucketName:          bucket.BucketName,
					ServiceAccountName:  bucket.ServiceAccountName,
					VendorSubnetID:      bucket.VendorSubnetID,
					TenantProjectNumber: bucket.TenantProjectNumber,
					HmacAccessKeyID:     bucket.HmacAccessKeyID,
					HmacSecretID:        bucket.HmacSecretID,
				}, nil
			}
		}
	}
	return nil, nil
}

func getBackupVaultDetails(se database.Storage, ctx context.Context, bvID string, accountId int64) (*datamodel.BackupVault, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup vault %s details from database", bvID))
	backupVault, err := se.GetBackupVaultByUUIDndOwnerID(ctx, bvID, accountId)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return nil, err
		}
	}
	if backupVault == nil && GCBDRVaultEnabled {
		recordHeartbeat(ctx, fmt.Sprintf("Fetching shared backup vault %s details from database", bvID))
		backupVault, err = se.GetBackupVault(ctx, bvID)
		if err != nil {
			if !errors.IsNotFoundErr(err) {
				return nil, err
			}
		}
		if backupVault != nil && backupVault.ServiceType != GCBDRServiceType {
			backupVault = nil
		}
	}
	return backupVault, nil
}

func (a VolumeCreateActivity) GenerateResourceNames(ctx context.Context, volume *datamodel.Volume, tenancyDetails *common.TenancyInfo, gcpRegion string) (*common.ResourceNames, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Generating backup resource names for volume %s", volume.UUID))
	return GenerateResourceNames(ctx, volume, tenancyDetails, gcpRegion)
}

func _generateResourceNames(ctx context.Context, volume *datamodel.Volume, tenancyDetails *common.TenancyInfo, gcpRegion string) (*common.ResourceNames, error) {
	logger := util.GetLogger(ctx)

	email, bucketName, serviceAccountId, err := GetResourceNamesForBackup(gcpRegion, gcpRegion, tenancyDetails.RegionalTenantProject, volume.DataProtection.BackupVaultID)
	if err != nil {
		logger.Errorf("Error generating resource names: %v", err)
		return nil, err
	}
	return &common.ResourceNames{
		Email:            email,
		BucketName:       bucketName,
		ServiceAccountId: serviceAccountId,
	}, nil
}

func (a VolumeCreateActivity) CreateBucket(ctx context.Context, resourceName *common.ResourceNames, tenancyDetails *common.TenancyInfo, region string, kmsGrant *string) (*common.BucketDetails, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Creating backup bucket %s", resourceName.BucketName))
	return CreateBucket(ctx, resourceName, tenancyDetails, region, kmsGrant)
}

func _createBucket(ctx context.Context, resourceName *common.ResourceNames, tenancyDetails *common.TenancyInfo, region string, kmsGrant *string) (*common.BucketDetails, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for backup bucket %s", resourceName.BucketName))
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	_, bucketDetails, err := GetOrCreateAndGCSResources(ctx, cloudService, resourceName.ServiceAccountId, tenancyDetails.RegionalTenantProject, resourceName.Email, resourceName.BucketName, region, "region", kmsGrant)
	if err != nil {
		cloudService.GetLogger().Errorf("Error creating bucket: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return bucketDetails[0], nil
}

func mergeBucketDetails(existing, updated *datamodel.BucketDetails) *datamodel.BucketDetails {
	mergeString := func(updatedVal, existingVal string) string {
		if updatedVal != "" {
			return updatedVal
		}
		return existingVal
	}
	return &datamodel.BucketDetails{
		BucketName:          mergeString(updated.BucketName, existing.BucketName),
		ServiceAccountName:  mergeString(updated.ServiceAccountName, existing.ServiceAccountName),
		TenantProjectNumber: mergeString(updated.TenantProjectNumber, existing.TenantProjectNumber),
		VendorSubnetID:      mergeString(updated.VendorSubnetID, existing.VendorSubnetID),
		HmacAccessKeyID:     mergeString(updated.HmacAccessKeyID, existing.HmacAccessKeyID),
		HmacSecretID:        mergeString(updated.HmacSecretID, existing.HmacSecretID),
		SatisfiesPzi:        updated.SatisfiesPzi,
		SatisfiesPzs:        updated.SatisfiesPzs,
	}
}

func bucketDetailsEqual(a, b *datamodel.BucketDetails) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.BucketName == b.BucketName &&
		a.ServiceAccountName == b.ServiceAccountName &&
		a.TenantProjectNumber == b.TenantProjectNumber &&
		a.VendorSubnetID == b.VendorSubnetID &&
		a.HmacAccessKeyID == b.HmacAccessKeyID &&
		a.HmacSecretID == b.HmacSecretID &&
		a.SatisfiesPzi == b.SatisfiesPzi &&
		a.SatisfiesPzs == b.SatisfiesPzs
}

func UpdateBackupVaultWithBucketDetails(se database.Storage, ctx context.Context, volume *datamodel.Volume, bucketDetails *common.BucketDetails) error {
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup vault for volume %s bucket update", volume.UUID))
	existingBackupVault, err := se.GetBackupVaultByUUIDndOwnerID(ctx, volume.DataProtection.BackupVaultID, volume.AccountID)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return err
		}
	}
	if existingBackupVault == nil && GCBDRVaultEnabled {
		recordHeartbeat(ctx, fmt.Sprintf("Fetching shared backup vault for volume %s bucket update", volume.UUID))
		existingBackupVault, err = se.GetBackupVault(ctx, volume.DataProtection.BackupVaultID)
		if err != nil {
			return err
		}
		if existingBackupVault != nil && existingBackupVault.ServiceType != GCBDRServiceType {
			existingBackupVault = nil
		}
	}

	if existingBackupVault == nil {
		return fmt.Errorf("backup vault %s not found for volume %s", volume.DataProtection.BackupVaultID, volume.UUID)
	}

	newBucketDetail := &datamodel.BucketDetails{
		BucketName:          bucketDetails.BucketName,
		ServiceAccountName:  bucketDetails.ServiceAccountName,
		TenantProjectNumber: bucketDetails.TenantProjectNumber,
		VendorSubnetID:      bucketDetails.VendorSubnetID, // For GCBDR this is empty, for others it's from volume
		SatisfiesPzi:        bucketDetails.SatisfiesPzi,
		SatisfiesPzs:        bucketDetails.SatisfiesPzs,
		HmacAccessKeyID:     bucketDetails.HmacAccessKeyID,
		HmacSecretID:        bucketDetails.HmacSecretID,
	}

	// For GCBDR vaults: only one bucket entry, replace it
	if existingBackupVault.ServiceType == GCBDRServiceType {
		existingBackupVault.BucketDetails = []*datamodel.BucketDetails{newBucketDetail}
	} else {
		// For regular/cross-region vaults: enrich an existing bucket+subnet entry or append a new one.
		if existingBackupVault.BucketDetails != nil {
			for i, bucket := range existingBackupVault.BucketDetails {
				if bucket.BucketName == bucketDetails.BucketName && bucket.VendorSubnetID == volume.VolumeAttributes.VendorSubnetID {
					merged := mergeBucketDetails(bucket, newBucketDetail)
					if bucketDetailsEqual(bucket, merged) {
						return nil
					}
					existingBackupVault.BucketDetails[i] = merged
					return se.UpdateBackupVaultBucketDetails(ctx, existingBackupVault)
				}
			}
		}
		existingBackupVault.BucketDetails = append(existingBackupVault.BucketDetails, newBucketDetail)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Updating backup vault %s bucket details in database", existingBackupVault.UUID))
	err = se.UpdateBackupVaultBucketDetails(ctx, existingBackupVault)
	if err != nil {
		return err
	}

	return nil
}

func UpdateRemoteBackupVaultWithBucketDetails(ctx context.Context, se database.Storage, volume *datamodel.Volume, sourceBV *datamodel.BackupVault, remoteBV *datamodel.BackupVault, bucketDetails *common.BucketDetails) error {
	logger := util.GetLogger(ctx)
	if sourceBV.BackupVaultType != CrossRegionBackupType ||
		sourceBV.SourceRegionName == nil || sourceBV.BackupRegionName == nil ||
		*sourceBV.SourceRegionName == *sourceBV.BackupRegionName {
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Updating remote backup vault %s for volume %s", remoteBV.UUID, volume.UUID))

	newBucketDetail := &datamodel.BucketDetails{
		BucketName:          bucketDetails.BucketName,
		ServiceAccountName:  bucketDetails.ServiceAccountName,
		TenantProjectNumber: bucketDetails.TenantProjectNumber,
		SatisfiesPzi:        bucketDetails.SatisfiesPzi,
		SatisfiesPzs:        bucketDetails.SatisfiesPzs,
		HmacAccessKeyID:     bucketDetails.HmacAccessKeyID,
		HmacSecretID:        bucketDetails.HmacSecretID,
	}

	var updatedBucketDetails []*datamodel.BucketDetails
	if sourceBV.ServiceType == GCBDRServiceType {
		updatedBucketDetails = []*datamodel.BucketDetails{newBucketDetail}
	} else {
		newBucketDetail.VendorSubnetID = volume.VolumeAttributes.VendorSubnetID
		updatedBucketDetails = append(sourceBV.BucketDetails, newBucketDetail)
	}

	if bucketDetailsExist(remoteBV.BucketDetails, newBucketDetail) {
		logger.Info("Bucket details already exist in remote BackupVault",
			"backupVaultID", remoteBV.UUID,
			"bucketName", bucketDetails.BucketName)
		return nil
	}

	projectNumber, err := backupVaultProjectNumber(ctx, se, sourceBV)
	if err != nil {
		return err
	}
	backupRegion := *remoteBV.BackupRegionName
	recordHeartbeat(ctx, fmt.Sprintf("Loading remote region configuration for region %s", backupRegion))
	basePath, jwtToken, err := common.GetRemoteRegionConfig(backupRegion, projectNumber)
	if err != nil {
		logger.Error("Failed to get remote region configuration", "region", backupRegion, "error", err.Error())
		return err
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	internalBucketDetails := make([]googleproxyclient.BackupVaultInternalUpdateV1betaBucketDetailsItem, 0, len(updatedBucketDetails))
	for _, bd := range updatedBucketDetails {
		internalBucketDetails = append(internalBucketDetails, googleproxyclient.BackupVaultInternalUpdateV1betaBucketDetailsItem{
			BucketName:          googleproxyclient.NewOptString(bd.BucketName),
			ServiceAccountName:  googleproxyclient.NewOptString(bd.ServiceAccountName),
			VendorSubnetId:      googleproxyclient.NewOptString(bd.VendorSubnetID),
			TenantProjectNumber: googleproxyclient.NewOptString(bd.TenantProjectNumber),
			SatisfiesPzi:        googleproxyclient.NewOptBool(bd.SatisfiesPzi),
			SatisfiesPzs:        googleproxyclient.NewOptBool(bd.SatisfiesPzs),
		})
	}

	updateRequest := &googleproxyclient.BackupVaultInternalUpdateV1beta{
		BucketDetails: internalBucketDetails,
	}

	params := googleproxyclient.V1betaInternalUpdateBackupVaultParams{
		BackupVaultId:  sourceBV.UUID,
		ProjectNumber:  projectNumber,
		LocationId:     backupRegion,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	recordHeartbeat(ctx, fmt.Sprintf("Calling remote VCP to update backup vault %s", remoteBV.UUID))
	res, err := googleProxyClient.Invoker.V1betaInternalUpdateBackupVault(ctx, updateRequest, params)
	if err != nil {
		logger.Error("Failed to call V1betaInternalUpdateBackupVault",
			"error", err.Error(),
			"backupVaultID", remoteBV.UUID,
			"region", backupRegion)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to update remote backup vault: %v", err),
			"InternalUpdateBackupVaultFailed",
			err,
		)
	}

	switch r := res.(type) {
	case *googleproxyclient.OperationV1beta:
		isDone := r.Done.Value
		if !isDone {
			logger.Warn("Update operation for remote backup vault not marked as done, but treating as synchronous",
				"backupVaultID", remoteBV.UUID)
		}
		logger.Info("Successfully updated remote backup vault with new bucket details",
			"backupVaultID", remoteBV.UUID,
			"bucketName", bucketDetails.BucketName)
		return nil

	case *googleproxyclient.V1betaInternalUpdateBackupVaultBadRequest:
		logger.Error("Bad request updating remote backup vault", "message", r.Message, "backupVaultID", remoteBV.UUID)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Bad request updating remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultBadRequest",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultUnauthorized:
		logger.Error("Unauthorized to update remote backup vault", "message", r.Message, "backupVaultID", remoteBV.UUID)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unauthorized to update remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultUnauthorized",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultForbidden:
		logger.Error("Forbidden to update remote backup vault", "message", r.Message, "backupVaultID", remoteBV.UUID)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Forbidden to update remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultForbidden",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultNotFound:
		logger.Error("Remote backup vault not found", "message", r.Message, "backupVaultID", remoteBV.UUID)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Remote backup vault not found: %s", r.Message),
			"V1betaInternalUpdateBackupVaultNotFound",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultConflict:
		logger.Warn("Conflict updating remote backup vault", "message", r.Message, "backupVaultID", remoteBV.UUID)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Conflict updating remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultConflict",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultUnprocessableEntity:
		logger.Error("Unprocessable entity updating remote backup vault", "message", r.Message, "backupVaultID", remoteBV.UUID)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unprocessable entity updating remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultUnprocessableEntity",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultInternalServerError:
		logger.Error("Internal server error updating remote backup vault", "message", r.Message, "backupVaultID", remoteBV.UUID)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Internal server error updating remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultInternalServerError",
			errors.New(r.Message),
		)

	default:
		logger.Error("Unexpected response type from internal update backup vault endpoint",
			"type", fmt.Sprintf("%T", r),
			"backupVaultID", remoteBV.UUID)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unexpected response type from internal update backup vault endpoint: %T", r),
			"UnexpectedUpdateResponseType",
			fmt.Errorf("unexpected response type: %T", r),
		)
	}
}

// backupVaultProjectNumber returns the project number of the account that owns the backup vault.
// The owner is loaded from AccountID because Account is tagged json:"-" and is dropped across the
// Temporal activity boundary. GCNV CRB and GCBDR CRB both use the vault owner; for GCNV that
// account matches the volume account.
func backupVaultProjectNumber(ctx context.Context, se database.Storage, backupVault *datamodel.BackupVault) (string, error) {
	if se == nil {
		return "", fmt.Errorf("storage is required to resolve owner account for backup vault %s", backupVault.UUID)
	}
	account, err := se.GetAccountByInternalID(ctx, backupVault.AccountID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve owner account for backup vault %s: %w", backupVault.UUID, err)
	}
	if account == nil || account.Name == "" {
		return "", fmt.Errorf("owner account is missing for backup vault %s", backupVault.UUID)
	}
	return account.Name, nil
}

// CheckOrCreateRemoteBackupVaultInVCP checks if the remote BackupVault exists in VCP for cross-region backups
func CheckOrCreateRemoteBackupVaultInVCP(ctx context.Context, se database.Storage, volume *datamodel.Volume, backupVault *datamodel.BackupVault, bucketDetails *common.BucketDetails) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	if backupVault.BackupVaultType != CrossRegionBackupType ||
		backupVault.SourceRegionName == nil || backupVault.BackupRegionName == nil ||
		*backupVault.SourceRegionName == *backupVault.BackupRegionName {
		return nil, nil
	}
	projectNumber, err := backupVaultProjectNumber(ctx, se, backupVault)
	if err != nil {
		return nil, err
	}

	remoteBV, err := FetchRemoteBackupVaultFromVCP(ctx, backupVault.UUID, projectNumber, *backupVault.BackupRegionName)
	if err != nil {
		if backupVault.ServiceType == GCBDRServiceType {
			logger.Error("Failed to fetch cross-project remote BackupVault from VCP",
				"backupVaultID", backupVault.UUID,
				"projectNumber", projectNumber,
				"region", *backupVault.BackupRegionName,
				"error", err.Error())
			return nil, err
		}
		if !errors.IsNotFoundErr(err) {
			logger.Error("Failed to fetch remote BackupVault from VCP", "error", err.Error())
			return nil, err
		}
	}

	if remoteBV != nil {
		logger.Info("Remote BackupVault already exists in VCP", "backupVaultID", remoteBV.Name, "region", *backupVault.BackupRegionName)
		return remoteBV, nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating remote backup vault %s in region %s", backupVault.UUID, *backupVault.BackupRegionName))
	bv, err := CreateRemoteBackupVault(ctx, projectNumber, backupVault, bucketDetails)
	if err != nil {
		logger.Error("Failed to create remote BackupVault with bucket details", "error", err.Error())
		return nil, err
	}

	return bv, nil
}

// PopulateBucketDetailsForTemporaryRestoreActivity resolves backup bucket metadata for cross-pool HW restore.
// An empty service account is valid for FLEX attach; temporary restore provisioning creates one when needed.
func (a VolumeCreateActivity) PopulateBucketDetailsForTemporaryRestoreActivity(
	ctx context.Context,
	bucketDetails *datamodel.BucketDetails,
	backup *datamodel.Backup,
	backupVault *datamodel.BackupVault,
	backupPath string,
) (*datamodel.BucketDetails, error) {
	activity.RecordHeartbeat(ctx, "PopulateBucketDetailsForTemporaryRestore started")
	if bucketDetails == nil {
		return nil, fmt.Errorf("bucket details required for temporary restore population")
	}

	PopulateBucketDetailsForTemporaryRestore(bucketDetails, backup, backupVault)

	if bucketDetails.ServiceAccountName == "" && strings.TrimSpace(backupPath) != "" {
		pathInfo, err := parseBackupPath(backupPath)
		if err != nil {
			return nil, err
		}
		if err = populateBucketServiceAccountFromSourceVault(ctx, a.SE, bucketDetails, backupVault, pathInfo); err != nil {
			return nil, err
		}
	}

	if bucketDetails.ServiceAccountName != "" {
		cloudService, err := GetCloudService(ctx)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if _, err = cloudService.GetServiceAccountByEmail(bucketDetails.ServiceAccountName); err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				fmt.Errorf("backup vault service account %s not found: %w", bucketDetails.ServiceAccountName, err))
		}
	}

	activity.RecordHeartbeat(ctx, "PopulateBucketDetailsForTemporaryRestore completed")
	return bucketDetails, nil
}

func backupVaultIDsForInternalDescribe(backupVault *datamodel.BackupVault, vaultNameFromPath string) []string {
	ids := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if backupVault != nil && backupVault.ExternalUUID != nil {
		add(*backupVault.ExternalUUID)
	}
	if backupVault != nil {
		add(backupVault.UUID)
		add(backupVault.Name)
	}
	add(vaultNameFromPath)
	return ids
}

func isCrossRegionBackupPath(backupRegion string) bool {
	workerRegion := strings.TrimSpace(env.Region)
	backupRegion = strings.TrimSpace(backupRegion)
	if workerRegion == "" || backupRegion == "" {
		return false
	}
	return !strings.EqualFold(workerRegion, backupRegion)
}

func lookupBackupVaultInLocalDB(
	ctx context.Context,
	se database.Storage,
	backupVault *datamodel.BackupVault,
	pathInfo *BackupPathInfo,
) *datamodel.BackupVault {
	account, err := se.GetAccount(ctx, pathInfo.ProjectName)
	if err != nil || account == nil {
		return nil
	}
	ownerID := fmt.Sprintf("%d", account.ID)
	for _, vaultID := range backupVaultIDsForInternalDescribe(backupVault, pathInfo.VaultName) {
		if vault, dbErr := se.GetBackupVaultByExternalUUIDAndOwnerID(ctx, vaultID, account.ID); dbErr == nil && vault != nil {
			return vault
		}
		if vault, dbErr := se.GetBackupVaultByNameAndOwnerID(ctx, vaultID, ownerID); dbErr == nil && vault != nil {
			return vault
		}
		if vault, dbErr := se.GetBackupVaultByUUIDndOwnerID(ctx, vaultID, account.ID); dbErr == nil && vault != nil {
			return vault
		}
	}
	return nil
}

func populateBucketServiceAccountFromSourceVault(
	ctx context.Context,
	se database.Storage,
	bucketDetails *datamodel.BucketDetails,
	backupVault *datamodel.BackupVault,
	pathInfo *BackupPathInfo,
) error {
	if vault := lookupBackupVaultInLocalDB(ctx, se, backupVault, pathInfo); vault != nil {
		mergeBucketServiceAccountFromVault(bucketDetails, vault)
		if bucketDetails.ServiceAccountName != "" {
			return nil
		}
	}

	if !isCrossRegionBackupPath(pathInfo.Region) {
		return nil
	}

	var fetchErr error
	for _, vaultID := range backupVaultIDsForInternalDescribe(backupVault, pathInfo.VaultName) {
		fullVault, err := FetchRemoteBackupVaultFromVCP(ctx, vaultID, pathInfo.ProjectName, pathInfo.Region)
		if err != nil {
			fetchErr = err
			continue
		}
		mergeBucketServiceAccountFromVault(bucketDetails, fullVault)
		if bucketDetails.ServiceAccountName != "" {
			return nil
		}
	}
	if fetchErr != nil {
		return fmt.Errorf("failed to fetch source backup vault bucket details: %w", fetchErr)
	}
	return nil
}

// FetchRemoteBackupVaultFromVCP calls the internal GET endpoint to fetch BackupVault from a backup region
func FetchRemoteBackupVaultFromVCP(ctx context.Context, backupVaultUUID, projectNumber, region string) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Loading remote region configuration for region %s", region))
	basePath, jwtToken, err := common.GetRemoteRegionConfig(region, projectNumber)
	if err != nil {
		logger.Error("Failed to get remote region configuration", "region", region, "error", err)
		return nil, err
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	params := googleproxyclient.V1betaInternalDescribeBackupVaultParams{
		ProjectNumber:  projectNumber,
		LocationId:     region,
		BackupVaultId:  backupVaultUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	recordHeartbeat(ctx, fmt.Sprintf("Calling remote VCP to describe backup vault %s", backupVaultUUID))
	res, err := googleProxyClient.Invoker.V1betaInternalDescribeBackupVault(ctx, params)
	if err != nil {
		logger.Error("Failed to fetch remote BackupVault", "error", err.Error(), "region", region, "backupVaultID", backupVaultUUID)
		return nil, temporal.NewApplicationError(
			fmt.Sprintf("Failed to fetch remote backup vault: %v", err),
			"InternalDescribeBackupVaultTransportError",
			err,
		)
	}

	switch r := res.(type) {
	case *googleproxyclient.BackupVaultInternalV1beta:
		result := convertInternalAPIToDatamodel(r)
		logger.Info("Successfully fetched remote BackupVault", "backupVaultID", result.Name, "region", region)
		return result, nil

	case *googleproxyclient.V1betaInternalDescribeBackupVaultNotFound:
		return nil, errors.NewNotFoundErr("remote backup vault", &backupVaultUUID)

	case *googleproxyclient.V1betaInternalDescribeBackupVaultBadRequest:
		logger.Error("Bad request fetching remote backup vault", "message", r.Message, "region", region, "backupVaultID", backupVaultUUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Bad request fetching remote backup vault: %s", r.Message),
			"V1betaInternalDescribeBackupVaultBadRequest",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDescribeBackupVaultUnauthorized:
		logger.Error("Unauthorized fetching remote backup vault", "message", r.Message, "region", region, "backupVaultID", backupVaultUUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unauthorized fetching remote backup vault: %s", r.Message),
			"V1betaInternalDescribeBackupVaultUnauthorized",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDescribeBackupVaultForbidden:
		logger.Error("Forbidden fetching remote backup vault", "message", r.Message, "region", region, "backupVaultID", backupVaultUUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Forbidden fetching remote backup vault: %s", r.Message),
			"V1betaInternalDescribeBackupVaultForbidden",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDescribeBackupVaultUnprocessableEntity:
		logger.Error("Unprocessable entity fetching remote backup vault", "message", r.Message, "region", region, "backupVaultID", backupVaultUUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unprocessable entity fetching remote backup vault: %s", r.Message),
			"V1betaInternalDescribeBackupVaultUnprocessableEntity",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDescribeBackupVaultInternalServerError:
		logger.Error("Internal server error fetching remote backup vault", "message", r.Message, "region", region, "backupVaultID", backupVaultUUID)
		return nil, temporal.NewApplicationError(
			fmt.Sprintf("Internal server error fetching remote backup vault: %s", r.Message),
			"V1betaInternalDescribeBackupVaultInternalServerError",
			errors.New(r.Message),
		)

	default:
		logger.Error("Unexpected response type from remote BackupVault fetch", "type", fmt.Sprintf("%T", res))
		return nil, temporal.NewApplicationError(
			fmt.Sprintf("Unexpected response type from remote BackupVault fetch: %T", res),
			"UnexpectedDescribeBackupVaultResponseType",
			fmt.Errorf("unexpected response type: %T", res),
		)
	}
}

// CreateRemoteBackupVault calls the internal POST endpoint to create BackupVault in a backup region
func CreateRemoteBackupVault(ctx context.Context, projectNumber string, backupVault *datamodel.BackupVault, bucketDetails *common.BucketDetails) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	BackupRegion := *backupVault.BackupRegionName

	recordHeartbeat(ctx, fmt.Sprintf("Loading remote region configuration for region %s", BackupRegion))
	basePath, jwtToken, err := common.GetRemoteRegionConfig(BackupRegion, projectNumber)
	if err != nil {
		logger.Error("Failed to get remote region configuration", "region", BackupRegion, "error", err.Error())
		return nil, err
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	params := googleproxyclient.V1betaInternalCreateBackupVaultParams{
		ProjectNumber:  projectNumber,
		LocationId:     BackupRegion,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	if backupVault.BucketDetails == nil && bucketDetails != nil {
		backupVault.BucketDetails = append(backupVault.BucketDetails, &datamodel.BucketDetails{
			BucketName:          bucketDetails.BucketName,
			ServiceAccountName:  bucketDetails.ServiceAccountName,
			TenantProjectNumber: bucketDetails.TenantProjectNumber,
			VendorSubnetID:      bucketDetails.VendorSubnetID,
			SatisfiesPzi:        bucketDetails.SatisfiesPzi,
			SatisfiesPzs:        bucketDetails.SatisfiesPzs,
		})
	}

	recordHeartbeat(ctx, fmt.Sprintf("Calling remote VCP to create backup vault %s", backupVault.UUID))
	res, err := googleProxyClient.Invoker.V1betaInternalCreateBackupVault(ctx, convertDatamodelToInternalAPI(backupVault), params)
	if err != nil {
		logger.Error("Failed to call V1betaInternalCreateBackupVault", "error", err.Error(), "region", BackupRegion, "backupVaultID", backupVault.UUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to create remote backup vault: %v", err),
			"InternalCreateBackupVaultFailed",
			err,
		)
	}

	switch r := res.(type) {
	case *googleproxyclient.BackupVaultInternalV1beta:
		result := convertInternalAPIToDatamodel(r)
		return result, nil

	case *googleproxyclient.V1betaInternalCreateBackupVaultBadRequest:
		logger.Error("Bad request creating remote backup vault", "message", r.Message, "backupVaultID", backupVault.UUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Bad request creating remote backup vault: %s", r.Message),
			"V1betaInternalCreateBackupVaultBadRequest",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupVaultUnauthorized:
		logger.Error("Unauthorized to create remote backup vault", "message", r.Message, "backupVaultID", backupVault.UUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unauthorized to create remote backup vault: %s", r.Message),
			"V1betaInternalCreateBackupVaultUnauthorized",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupVaultForbidden:
		logger.Error("Forbidden to create remote backup vault", "message", r.Message, "backupVaultID", backupVault.UUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Forbidden to create remote backup vault: %s", r.Message),
			"V1betaInternalCreateBackupVaultForbidden",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupVaultConflict:
		logger.Warn("Conflict creating remote backup vault - may already exist", "message", r.Message, "backupVaultID", backupVault.UUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Conflict creating remote backup vault: %s", r.Message),
			"V1betaInternalCreateBackupVaultConflict",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupVaultUnprocessableEntity:
		logger.Error("Unprocessable entity creating remote backup vault", "message", r.Message, "backupVaultID", backupVault.UUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unprocessable entity creating remote backup vault: %s", r.Message),
			"V1betaInternalCreateBackupVaultUnprocessableEntity",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupVaultInternalServerError:
		logger.Error("Internal server error creating remote backup vault", "message", r.Message, "backupVaultID", backupVault.UUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Internal server error creating remote backup vault: %s", r.Message),
			"V1betaInternalCreateBackupVaultInternalServerError",
			errors.New(r.Message),
		)

	default:
		logger.Error("Unexpected response type from internal create backup vault endpoint", "type", fmt.Sprintf("%T", r), "backupVaultID", backupVault.UUID)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unexpected response type from internal create backup vault endpoint: %T", r),
			"UnexpectedCreateResponseType",
			fmt.Errorf("unexpected response type: %T", r),
		)
	}
}

func _getOrCreateAndGCSResources(ctx context.Context, cloudServices hyperscaler.Services, serviceAccountId, projectNumber, email, bucketName, tenantProjectRegion, locationType string, kmsGrant *string) (*hyperscalermodels.ServiceAccount, []*common.BucketDetails, error) {
	var bucketDetailsArr []*common.BucketDetails
	var err error

	// Only create the bucket - no service account creation
	recordHeartbeat(ctx, fmt.Sprintf("Creating GCS bucket %s in project %s", bucketName, projectNumber))
	err = cloudServices.CreateBucketIfNotExists(ctx, projectNumber, bucketName, tenantProjectRegion, kmsGrant)
	if err != nil {
		return nil, nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, fmt.Sprintf("Finished creating GCS bucket %s in project %s", bucketName, projectNumber))

	// Use empty service account name since we're not creating service accounts
	bucketDetails := &common.BucketDetails{
		BucketName:          bucketName,
		ServiceAccountName:  "", // No service account created
		TenantProjectNumber: projectNumber,
		Location:            locationType,
	}
	bucketDetailsArr = append(bucketDetailsArr, bucketDetails)

	return nil, bucketDetailsArr, nil
}

func (a VolumeCreateActivity) UpdateBackupVaultWithBucketDetails(ctx context.Context, volume *datamodel.Volume, bucketDetails *common.BucketDetails) error {
	return UpdateBackupVaultWithBucketDetails(a.SE, ctx, volume, bucketDetails)
}

func _getResourceNamesForBackup(gcpRegion, region, tenantProjectNumber, bvID string) (string, string, string, error) {
	return utils.GetResourcesNameForBackup(gcpRegion, region, tenantProjectNumber, bvID)
}

func (a VolumeCreateActivity) CreateSnapshotPolicyInONTAP(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	if node != nil && volume != nil && volume.SnapshotPolicy != nil && volume.SnapshotPolicy.Name != "" {
		orchestrator.RecordHeartbeat(ctx, "Initializing snapshot policy creation")
		logger := util.GetLogger(ctx)
		provider, err := vsa.GetProviderByNode(ctx, node)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		orchestrator.RecordHeartbeat(ctx, "Creating snapshot policy in ONTAP")
		err = provider.CreateSnapshotPolicy(&vsa.SnapshotPolicy{
			Name:      volume.SnapshotPolicy.Name,
			IsEnabled: volume.SnapshotPolicy.IsEnabled,
			Schedules: ConvertToVSASnapshotPolicySchedules(volume.SnapshotPolicy.Schedules),
		})
		if err != nil {
			logger.Errorf("failed to create snapshot policy: %v", err)
			return err
		}
		orchestrator.RecordHeartbeat(ctx, "Snapshot policy created successfully")
	}
	return nil
}

// LunSizeUpdateValidation Validates if the LUN size can be updated based on the available space and SnapReserve constraints.
func (a VolumeCreateActivity) LunSizeUpdateValidation(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	recordHeartbeat(ctx, "Initializing LUN size validation")
	logger := util.GetLogger(ctx)
	requiredLunSpace := volume.SizeInBytes * (100 - int64(volume.VolumeAttributes.SnapReserve)) / 100
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "Retrieving LUN for validation")
	lun, err := LunGet(ctx, "", volume.Name, volume.Svm.Name, provider)
	if err != nil {
		logger.Debug("lun not found !")
		return err
	}
	recordHeartbeat(ctx, "Validating LUN size constraints")
	// Check if the available space is less than the current LUN size
	if requiredLunSpace < lun.Size {
		logger.Errorf("Lun size %d cannot be reduced to %d", lun.Size, requiredLunSpace)
		err = vsaerrors.NewVCPError(vsaerrors.ErrRestoreVolumeValidation, fmt.Errorf("Error restoring volume - Cannot restore a volume with this given size and snapReserve. Please consider increasing the volume size to at least of size %.2f GB along with this snapReserve", float64(lun.Size)/float64(BytesPerGB)*(utils.PercentageBase/float64(utils.PercentageBase-volume.VolumeAttributes.SnapReserve))))
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "LUN size validation completed successfully")
	return nil
}

// UpdateClonedVolumeBeforeSplit updates the size, snapReserve of the cloned volume before split in ONTAP.
func (a VolumeCreateActivity) UpdateClonedVolumeBeforeSplit(ctx context.Context, volume *datamodel.Volume, node *models.Node) (*vsa.VolumeResponse, error) {
	recordHeartbeat(ctx, "Initializing cloned volume update before split")
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// By initializing snapReserve to 0, we avoid inheriting the parent's snapReserve and can safely update it to the customer-specified value after cloning.
	// Reason: ONTAP restricts increasing snapReserve beyond the parent's availableSpace if the parent volume's available space is fully consumed.
	recordHeartbeat(ctx, "Resetting snapReserve to 0 for cloned volume")
	err = updateVolume(ctx, provider, vsa.UpdateVolumeParams{
		UUID:        volume.VolumeAttributes.ExternalUUID,
		SnapReserve: nillable.GetInt64Ptr(0),
	})
	if err != nil {
		logger.Errorf("Failed to update snapReserve of cloned volume %s in ontap before split: %v", volume.Name, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "Preparing volume update parameters")
	preSplitUpdateParams := vsa.UpdateVolumeParams{
		UUID:               volume.VolumeAttributes.ExternalUUID,
		Size:               volume.SizeInBytes,
		SnapshotPolicyName: volume.SnapshotPolicy.Name,
		SnapReserve:        &volume.VolumeAttributes.SnapReserve,
	}
	if volume.VolumeAttributes != nil && utils.IsNasProtocols(volume.VolumeAttributes.Protocols) && volume.VolumeAttributes.FileProperties != nil && volume.VolumeAttributes.FileProperties.ExportPolicy != nil {
		preSplitUpdateParams.ExportPolicy = &volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName
		preSplitUpdateParams.JunctionPath = &volume.VolumeAttributes.FileProperties.JunctionPath
		preSplitUpdateParams.SnapshotDirectoryAccess = &volume.VolumeAttributes.SnapshotDirectory
	}
	if usePrivateCLIVolumeCreate(volume) {
		preSplitUpdateParams.UsePrivateCLI = true
		preSplitUpdateParams.VolumeName = volume.Name
		if volume.Svm != nil {
			preSplitUpdateParams.SvmName = volume.Svm.Name
		}
	}
	recordHeartbeat(ctx, "Updating cloned volume in ONTAP")
	err = updateVolume(ctx, provider, preSplitUpdateParams)
	if err != nil {
		logger.Errorf("Failed to update cloned volume %s in ontap before split: %v", volume.Name, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Debugf("Cloned volume %s updated successfully in ontap", volume.Name)
	recordHeartbeat(ctx, "Retrieving updated volume from ONTAP")
	volumeRes, err := provider.GetVolume(vsa.GetVolumeParams{
		UUID:       volume.VolumeAttributes.ExternalUUID,
		VolumeName: volume.Name,
		SvmName:    volume.Svm.Name,
	})
	if err != nil {
		logger.Errorf("Failed to get volume %s from ontap after pre-split update: %v", volume.Name, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "Cloned volume updated successfully before split")
	return volumeRes, nil
}

// InitiateSplitForVolume initiates a split for the given volume in ONTAP.
func (a VolumeCreateActivity) InitiateSplitForVolume(ctx context.Context, volume *datamodel.Volume, node *models.Node, snapshot *datamodel.Snapshot) error {
	recordHeartbeat(ctx, "Initializing volume split operation")
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	updateVolumeParams := &vsa.UpdateVolumeParams{
		UUID:          volume.VolumeAttributes.ExternalUUID,
		InitiateSplit: true,
	}
	recordHeartbeat(ctx, "Initiating split in ONTAP")
	err = updateVolume(ctx, provider, *updateVolumeParams)
	if err != nil {
		logger.Errorf("Failed to initiate split %s in ontap: %v", volume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var cloneSnapshot *datamodel.Snapshot
	// Get the clone volume snapshot that has the same name as the parent snapshot
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil && volume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID != "" && volume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID != "" {
		recordHeartbeat(ctx, "Retrieving parent volume information")
		// Get the parent volume to access its account ID and volume ID
		parentVolume, err := a.SE.GetVolume(ctx, volume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID)
		if err != nil {
			logger.Warnf("Failed to get parent volume %s: %v", volume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID, err)
			return nil
		}

		recordHeartbeat(ctx, "Retrieving parent snapshot information")
		// Get the parent snapshot by UUID to retrieve its name
		parentSnapshot, err := a.SE.GetSnapshotByUUID(ctx, volume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID, parentVolume.AccountID, parentVolume.ID)
		if err != nil {
			logger.Warnf("Failed to get parent snapshot %s: %v", volume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID, err)
			return nil
		}

		recordHeartbeat(ctx, "Retrieving clone volume snapshot")
		// Get the clone volume snapshot that has the same name as the parent snapshot
		cloneSnapshot, err = a.SE.GetSnapshotByNameAndVolumeId(ctx, parentSnapshot.Name, volume.AccountID, volume.ID)
		if err != nil {
			logger.Warnf("Failed to get clone volume snapshot with name %s for volume %s: %v", parentSnapshot.Name, volume.Name, err)
			return nil
		}
		logger.Debugf("Found clone volume snapshot %s (UUID: %s) with same name as parent snapshot %s", cloneSnapshot.Name, cloneSnapshot.UUID, parentSnapshot.Name)

		if cloneSnapshot != nil {
			recordHeartbeat(ctx, "Deleting clone snapshot")
			_, err := a.SE.DeleteSnapshot(ctx, cloneSnapshot.UUID)
			if err != nil {
				if errors.IsNotFoundErr(err) {
					logger.Warnf("Snapshot %s not found, assuming it is already deleted", cloneSnapshot.Name)
					return nil
				}
				logger.Errorf("Failed to delete snapshot after split operation %s: %v", cloneSnapshot.Name, err)
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}

			logger.Debugf("Snapshot %s (UUID: %s) marked as deleted successfully in the db", cloneSnapshot.Name, cloneSnapshot.UUID)

			recordHeartbeat(ctx, "Hydrating snapshot to CCFE")
			// Hydrate the clone snapshot to CCFE after split
			hydrateErr := hydrationActivities.HydrateBatchSnapshotstoCCFE(ctx, nil, []*datamodel.Snapshot{cloneSnapshot})
			if hydrateErr != nil {
				logger.Warnf("Failed to hydrate snapshots to CCFE after volume revert: %v, snapshots: %+v", hydrateErr, cloneSnapshot)
			}
		}
	}

	recordHeartbeat(ctx, "Volume split initiated successfully")
	logger.Debugf("Split %s initiated successfully in ontap", volume.Name)
	return nil
}

// UpdateCloneParentStateInDB updates the clone parent state and clones_shared_bytes in the database.
// It is called by VolumePollSplitWorkflow as a cleanup activity on both success and failure paths:
//   - On success (removeCloneInfo=true): removes CloneParentInfo entirely from volume_attributes,
//     signalling that the volume is no longer a clone.
//   - On ONTAP failure (removeCloneInfo=false): sets cloneState="SPLIT_FAILED" and records the
//     ONTAP error message in stateDetails so operators can see why the split failed.
func (a VolumeCreateActivity) UpdateCloneParentStateInDB(ctx context.Context, volumeUUID string, cloneState string, clonesSharedBytes uint64, stateDetails string, removeCloneInfo bool) error {
	recordHeartbeat(ctx, "Updating clone parent state in DB")
	logger := util.GetLogger(ctx)

	volume, err := a.SE.GetVolume(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get volume %s for clone state update: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	updateFields := map[string]interface{}{
		"clones_shared_bytes": clonesSharedBytes,
	}

	if volume.VolumeAttributes != nil {
		updatedAttributes := *volume.VolumeAttributes
		if removeCloneInfo {
			// Split completed successfully — the volume is no longer a clone, so remove the
			// cloneDetails block entirely.
			// Mark it for hydration pending
			updatedAttributes.CloneParentInfo = nil
			// OriginalSharedBytes is only meaningful while a volume is still a
			// thin clone (it scopes the splitStop response math). Clear it
			// alongside CloneParentInfo so a fully-split, now-independent
			// volume does not carry stale clone metadata.
			updatedAttributes.OriginalSharedBytes = nil
			updatedAttributes.SplitRegularVolumeHydrationPending = true
			logger.Debugf("Removing CloneParentInfo for volume %s after successful split", volumeUUID)
		} else if volume.VolumeAttributes.CloneParentInfo != nil {
			updatedAttributes.CloneParentInfo = &datamodel.CloneParentInfo{
				ParentVolumeUUID:     volume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID,
				ParentSnapshotUUID:   volume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID,
				SplitCompletePercent: volume.VolumeAttributes.CloneParentInfo.SplitCompletePercent,
				State:                cloneState,
				StateDetails:         stateDetails,
			}
		} else {
			logger.Warnf("Volume %s has no CloneParentInfo, skipping clone state update", volumeUUID)
		}
		updateFields["volume_attributes"] = &updatedAttributes
	} else {
		logger.Warnf("Volume %s has no VolumeAttributes, skipping clone state update", volumeUUID)
	}

	recordHeartbeat(ctx, "Writing clone parent state update to DB")
	if err := a.SE.UpdateVolumeFields(ctx, volumeUUID, updateFields); err != nil {
		logger.Errorf("Failed to update clone parent state to %s for volume %s: %v", cloneState, volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func ConvertToVSASnapshotPolicySchedules(schedules []*datamodel.SnapshotPolicySchedule) []*vsa.SnapshotPolicySchedule {
	if schedules == nil {
		return nil
	}
	var vsaPolicySchedules []*vsa.SnapshotPolicySchedule
	for _, schedule := range schedules {
		vsaSchedule := &vsa.Schedule{
			DaysOfMonth: schedule.DaysOfMonth,
			DaysOfWeek:  schedule.DaysOfWeek,
			Hours:       schedule.Hours,
			Minutes:     schedule.Minutes,
		}
		vsaPolicySchedules = append(vsaPolicySchedules, &vsa.SnapshotPolicySchedule{
			Schedule:        vsaSchedule,
			Prefix:          schedule.SnapmirrorLabel,
			Count:           schedule.Count,
			SnapmirrorLabel: schedule.SnapmirrorLabel,
		})
	}
	return vsaPolicySchedules
}

func (a VolumeCreateActivity) CheckIfBackupPolicyExistsInVCP(ctx context.Context, backupPolicyUUID string, accountId int64) (bool, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Checking backup policy %s in database", backupPolicyUUID))
	return _checkIfBackupPolicyExistsInVCP(ctx, a.SE, backupPolicyUUID, accountId)
}

func (a VolumeCreateActivity) CreateBackupPolicyFetchedFromSDE(ctx context.Context, volume *datamodel.Volume, region string) (*datamodel.BackupPolicy, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup policy for volume %s from CVP", volume.UUID))
	return _createBackupPolicyFetchedFromSDE(ctx, a.SE, volume, region)
}

func _checkIfBackupPolicyExistsInVCP(ctx context.Context, se database.Storage, backupPolicyUUID string, accountId int64) (bool, error) {
	backupPolicy, err := se.GetBackupPolicyByUUIDAndOwnerID(ctx, backupPolicyUUID, accountId)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return false, err
		}
	}
	if backupPolicy != nil {
		return true, nil
	}
	return false, nil
}

func _createBackupPolicyFetchedFromSDE(ctx context.Context, se database.Storage, volume *datamodel.Volume, region string) (*datamodel.BackupPolicy, error) {
	logger := util.GetLogger(ctx)
	GetSignedJwtToken := utils.GetAuthTokenFromContext(ctx)
	cvpClient := CvpCreateClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)
	backupPolicyUUID := volume.DataProtection.BackupPolicyID

	recordHeartbeat(ctx, fmt.Sprintf("Describing backup policy %s in CVP", backupPolicyUUID))
	cvpBackupPolicy, err := cvpClient.BackupPolicy.V1betaDescribeBackupPolicy(&backup_policy.V1betaDescribeBackupPolicyParams{
		BackupPolicyID: backupPolicyUUID,
		LocationID:     region,
		ProjectNumber:  volume.Account.Name,
		XCorrelationID: &xCorrelationID,
	})
	if err != nil {
		logger.Errorf("Error checking backup policy in SDE : %v", err)
		return nil, err
	}
	if cvpBackupPolicy == nil || cvpBackupPolicy.Payload == nil {
		logger.Error("No backup policy found in SDE")
		return nil, errors.NewNotFoundErr("Backup policy", &backupPolicyUUID)
	}

	backupPolicy := ConvertToBackupPolicyDataModel(cvpBackupPolicy.Payload)
	backupPolicy.AccountID = volume.AccountID

	recordHeartbeat(ctx, fmt.Sprintf("Creating backup policy %s in database", backupPolicyUUID))
	dbBackupPolicy, err := se.CreateBackupPolicyEntryInVCP(ctx, backupPolicy)
	if err != nil {
		return nil, err
	}
	return dbBackupPolicy, nil
}

func (a VolumeCreateActivity) CreateBackupPolicySchedule(ctx context.Context, vcpBackupPolicy *datamodel.BackupPolicy, customSchedule string) error {
	recordHeartbeat(ctx, fmt.Sprintf("Creating Temporal schedule for backup policy %s", vcpBackupPolicy.UUID))
	return _createBackupPolicySchedule(ctx, a.Scheduler, vcpBackupPolicy, customSchedule)
}

// BackupRestoreMetadata holds metadata for restoring a volume from an SDE/CVP backup
type BackupRestoreMetadata struct {
	BackupVault   *datamodel.BackupVault
	Backup        *datamodel.Backup
	BucketDetails *datamodel.BucketDetails
}

// _fetchVolumeProtocolsFromSDE fetches protocols from a volume from SDE
func _fetchVolumeProtocolsFromSDE(ctx context.Context, volumeID string, region string, account *datamodel.Account, cvpClient cvpapi.Cvp, xCorrelationID string) ([]string, error) {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Describing volume %s in CVP for restore protocols", volumeID))
	descRes, err := cvpClient.Volumes.V1betaDescribeVolume(&volumes.V1betaDescribeVolumeParams{
		Context:        ctx,
		LocationID:     region,
		ProjectNumber:  account.Name,
		VolumeID:       volumeID,
		IncludeDeleted: true,
		XCorrelationID: &xCorrelationID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe volume %q in CVP: %w", volumeID, err)
	}

	if descRes == nil || descRes.Payload == nil {
		return nil, fmt.Errorf("CVP describe volume returned empty response for volume %q", volumeID)
	}

	targetVolume := descRes.Payload
	if len(targetVolume.Protocols) == 0 {
		return nil, fmt.Errorf("volume '%s' has no protocols in CVP", volumeID)
	}

	protocols := make([]string, 0, len(targetVolume.Protocols))
	for _, p := range targetVolume.Protocols {
		protocols = append(protocols, string(p))
	}

	logger.Infof("Found protocols '%v' for volume '%s' in CVP", protocols, volumeID)
	return protocols, nil
}

// _fetchBucketDetailsFromGCS fetches bucket details (tenant project number) from GCS
func _fetchBucketDetailsFromGCS(ctx context.Context, bucketName string) (*datamodel.BucketDetails, error) {
	logger := util.GetLogger(ctx)

	// Get cloud service to access storage API
	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for bucket %s", bucketName))
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cloud service: %w", err)
	}

	// Fetch bucket details from GCS
	bucketInfo, err := GetBucket(ctx, bucketName, cloudService)
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket info from GCS: %w", err)
	}

	if bucketInfo.ProjectNumber == "" {
		return nil, fmt.Errorf("bucket %s does not have project number in metadata", bucketName)
	}

	// Create BucketDetails with the tenant project number from GCS
	bucketDetails := &datamodel.BucketDetails{
		BucketName:          bucketName,
		ServiceAccountName:  "",
		VendorSubnetID:      "",
		TenantProjectNumber: bucketInfo.ProjectNumber,
		SatisfiesPzi:        bucketInfo.SatisfiesPzi,
		SatisfiesPzs:        bucketInfo.SatisfiesPzs,
	}

	logger.Infof("Fetched bucket details from GCS - Bucket: %s, TenantProject: %s, PZI: %t, PZS: %t",
		bucketName, bucketDetails.TenantProjectNumber, bucketDetails.SatisfiesPzi, bucketDetails.SatisfiesPzs)

	return bucketDetails, nil
}

func _getBucket(ctx context.Context, bucketName string, cloudService hyperscaler.Services) (*hyperscalermodels.BucketDetails, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Getting bucket %s from cloud storage", bucketName))
	return cloudService.GetBucket(ctx, bucketName)
}

// FetchBackupVaultMetadataForRestore fetches backup vault metadata from VCP DB or CVP
func (a VolumeCreateActivity) FetchBackupVaultMetadataForRestore(ctx context.Context, backupPath string, volume *datamodel.Volume, region string) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching restore backup vault metadata for volume %s", volume.UUID))

	// Parse and validate backup path
	pathInfo, err := parseBackupPath(backupPath)
	if err != nil {
		return nil, err
	}

	logger.Debugf("Fetching backup vault metadata for restore: vault='%s', region='%s'", pathInfo.VaultName, pathInfo.Region)

	// Fetch backup vault (VCP DB or CVP)
	backupVault, err := fetchBackupVaultOrFallbackToRemoteVCP(ctx, a.SE, pathInfo, region, volume)
	if err != nil {
		return nil, err
	}

	logger.Infof("Successfully fetched backup vault metadata: vault='%s'", backupVault.Name)
	return backupVault, nil
}

// FetchBackupMetadataForRestore fetches backup metadata from VCP DB or CVP
func (a VolumeCreateActivity) FetchBackupMetadataForRestore(ctx context.Context, backupPath string, backupVault *datamodel.BackupVault, volume *datamodel.Volume, region string) (*datamodel.Backup, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching restore backup metadata for volume %s", volume.UUID))

	// Parse and validate backup path
	pathInfo, err := parseBackupPath(backupPath)
	if err != nil {
		return nil, err
	}

	logger.Infof("Fetching backup metadata for restore: backup='%s'", pathInfo.BackupName)

	// Fetch backup (VCP DB or remote VCP)
	backup, err := FetchBackupOrFallbackToRemoteVCP(ctx, a.SE, pathInfo, backupVault, volume, region)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err))
		}
		return nil, err
	}

	logger.Infof("Successfully fetched backup metadata: backup='%s'", backup.Name)
	return backup, nil
}

// FetchBucketMetadataForRestore fetches bucket details for the backup from GCS if needed
func (a VolumeCreateActivity) FetchBucketMetadataForRestore(ctx context.Context, backup *datamodel.Backup, backupVault *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	bucketName := backup.Attributes.BucketName
	recordHeartbeat(ctx, fmt.Sprintf("Fetching restore bucket metadata for backup %s", backup.UUID))
	logger.Infof("Fetching bucket metadata for restore: bucket='%s'", bucketName)

	// Ensure bucket details exist in backup vault (may trigger GCS API call)
	if err := EnsureBucketDetailsExist(ctx, backupVault, bucketName); err != nil {
		return nil, err
	}

	return backupVault, nil
}

// BackupPathInfo holds parsed components from a backup path
type BackupPathInfo struct {
	Region              string
	VaultName           string
	BackupName          string
	BackupVaultFullPath string
	ProjectName         string
}

// ============================================================================
// Helper Functions for SDE Backup Restore
// ============================================================================

// parseBackupPath parses and validates a backup path into its components
// Normalizes path keywords to ensure correct casing for database lookups and API calls
func parseBackupPath(backupPath string) (*BackupPathInfo, error) {
	const (
		ProjectsKeyIndex        = 0
		ProjectNameIndex        = 1
		LocationsKeyIndex       = 2
		LocationIdIndex         = 3
		BackupVaultKeyIndex     = 4
		BackupVaultNameIndex    = 5
		BackupKeyIndex          = 6
		BackupNameIndex         = 7
		MaxBackupPathComponents = 8
	)

	components := strings.Split(backupPath, "/")

	// Ensure there are enough components to avoid out of range errors
	if len(components) != MaxBackupPathComponents {
		return nil, errors.NewUserInputValidationErr("Backup path is not in correct format - expected format: projects/{project}/locations/{location}/backupVaults/{vaultName}/backups/{backupName}")
	}

	// Normalize all path keywords to ensure the correct Google Cloud format
	// Accept any casing variant but normalize to the standard format
	components[ProjectsKeyIndex] = "projects"
	components[LocationsKeyIndex] = "locations"
	components[BackupVaultKeyIndex] = "backupVaults"
	components[BackupKeyIndex] = "backups"

	locationID := components[LocationIdIndex]
	region, _, err := utils.ParseRegionAndZone(locationID)
	if err != nil {
		return nil, errors.NewUserInputValidationErr(fmt.Sprintf("Backup path location '%s' is not a valid region or zone", locationID))
	}

	return &BackupPathInfo{
		Region:              region,
		VaultName:           components[BackupVaultNameIndex],
		BackupName:          components[BackupNameIndex],
		ProjectName:         components[ProjectNameIndex],
		BackupVaultFullPath: strings.Join(components[:6], "/"),
	}, nil
}

// hasBucketDetails checks if a bucket already exists in the backup vault's bucket details
func hasBucketDetails(backupVault *datamodel.BackupVault, bucketName string) bool {
	if backupVault == nil || backupVault.BucketDetails == nil || bucketName == "" {
		return false
	}

	for _, bd := range backupVault.BucketDetails {
		if strings.EqualFold(bd.BucketName, bucketName) {
			return true
		}
	}
	return false
}

// appendBucketDetails appends bucket details to a backup vault (in-memory only)
func appendBucketDetails(backupVault *datamodel.BackupVault, bucketDetails *datamodel.BucketDetails) {
	if backupVault == nil || bucketDetails == nil {
		return
	}

	if backupVault.BucketDetails == nil {
		backupVault.BucketDetails = datamodel.BucketDetailsArray{bucketDetails}
	} else {
		backupVault.BucketDetails = append(backupVault.BucketDetails, bucketDetails)
	}
}

// EnsureBucketDetailsExist ensures bucket details exist in the backup vault, fetching from GCS if needed
func EnsureBucketDetailsExist(ctx context.Context, backupVault *datamodel.BackupVault, bucketName string) error {
	logger := util.GetLogger(ctx)

	if bucketName == "" {
		return fmt.Errorf("bucket name is empty")
	}

	// Check if bucket already exists
	if hasBucketDetails(backupVault, bucketName) {
		logger.Infof("Bucket '%s' already exists in BucketDetails", bucketName)
		return nil
	}

	// Fetch bucket details from GCS
	logger.Infof("Bucket '%s' not found in BucketDetails, fetching from GCS", bucketName)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching missing bucket %s details from GCS", bucketName))
	bucketDetails, err := FetchBucketDetailsFromGCS(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to fetch bucket details from GCS for bucket '%s': %w", bucketName, err)
	}

	if bucketDetails == nil || bucketDetails.TenantProjectNumber == "" {
		return fmt.Errorf("unable to get tenant project number for bucket '%s'", bucketName)
	}

	// Add bucket details (in-memory only)
	appendBucketDetails(backupVault, bucketDetails)
	logger.Infof("Added bucket '%s' with tenant project '%s' to backup vault (in-memory)",
		bucketName, bucketDetails.TenantProjectNumber)

	return nil
}

// fetchBackupVaultOrFallbackToRemoteVCP fetches a backup vault from VCP database or remote VCP
func fetchBackupVaultOrFallbackToRemoteVCP(ctx context.Context, se database.Storage, pathInfo *BackupPathInfo, volumeRegion string, volume *datamodel.Volume) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	var backupVault *datamodel.BackupVault
	var err error

	recordHeartbeat(ctx, fmt.Sprintf("Fetching account for restore project %s from database", pathInfo.ProjectName))
	backupPathAccount, err := se.GetAccount(ctx, pathInfo.ProjectName)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			logger.Errorf("Failed to find account for backup path project '%s': %v", pathInfo.ProjectName, err)
			return nil, fmt.Errorf("failed to look up account for project '%s' in backup path: %w", pathInfo.ProjectName, err)
		}
		// Cross-project backup vault: the BV's project has no account in this VCP region's DB.
		// Skip local DB lookup and fall through to remote VCP.
		logger.Infof("Account for backup path project '%s' not found locally (cross-project backup vault), will fetch from remote VCP", pathInfo.ProjectName)
	}

	// Try to get from VCP database first (only possible when the account exists locally)
	if backupPathAccount != nil && pathInfo.Region == volumeRegion {
		logger.Debugf("Checking for backup vault in VCP DB using vault name: %s, account: %d (project: %s)", pathInfo.VaultName, backupPathAccount.ID, pathInfo.ProjectName)
		recordHeartbeat(ctx, fmt.Sprintf("Fetching restore backup vault %s from database", pathInfo.VaultName))
		backupVault, err = se.GetBackupVaultByNameAndOwnerID(ctx, pathInfo.VaultName, fmt.Sprintf("%d", backupPathAccount.ID))
		if err != nil && !errors.IsNotFoundErr(err) {
			logger.Errorf("Error fetching backup vault from VCP DB: %v", err)
			return nil, err
		}
	}

	if backupVault != nil {
		logger.Infof("Found backup vault '%s' in VCP DB", pathInfo.VaultName)
		return backupVault, nil
	}

	pathInfo, err = rewriteGCBDRRemoteBackupPath(ctx, se, pathInfo, volumeRegion, backupPathAccount)
	if err != nil {
		return nil, err
	}

	// If not found in local VCP, fetch from remote VCP/SDE
	logger.Infof("Backup vault '%s' not found in VCP, fetching from remote VCP/SDE", pathInfo.VaultName)
	return FetchBackupVaultFromRemoteVCP(ctx, pathInfo, volume)
}

// rewriteGCBDRRemoteBackupPath replaces a GCBDR cross-region source vault name
// with its destination vault name before the remote-region lookup.
func rewriteGCBDRRemoteBackupPath(ctx context.Context, se database.Storage, pathInfo *BackupPathInfo, volumeRegion string, backupPathAccount *datamodel.Account) (*BackupPathInfo, error) {
	if pathInfo == nil || backupPathAccount == nil || pathInfo.Region == volumeRegion {
		return pathInfo, nil
	}

	logger := util.GetLogger(ctx)
	sourceVault, err := se.GetBackupVaultByNameAndOwnerID(ctx, pathInfo.VaultName, fmt.Sprintf("%d", backupPathAccount.ID))
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			logger.Warnf("Unable to check whether remote restore vault '%s' is a Cross Project cross-region vault; preserving the requested backup path: %v", pathInfo.VaultName, err)
		}
		return pathInfo, nil
	}
	if sourceVault == nil {
		return pathInfo, nil
	}

	if sourceVault.ServiceType != datamodel.ServiceTypeCrossProject ||
		sourceVault.BackupVaultType != datamodel.BackupVaultTypeCrossRegion {
		return pathInfo, nil
	}

	if sourceVault.CrossRegionBackupVaultName == nil ||
		strings.TrimSpace(*sourceVault.CrossRegionBackupVaultName) == "" {
		return nil, fmt.Errorf("Cross Project cross-region backup vault '%s' does not have a destination backup vault path", sourceVault.Name)
	}

	destinationBackupPath := fmt.Sprintf("%s/backups/%s",
		strings.TrimSuffix(strings.TrimSpace(*sourceVault.CrossRegionBackupVaultName), "/"),
		pathInfo.BackupName)
	destinationPathInfo, err := parseBackupPath(destinationBackupPath)
	if err != nil {
		return nil, fmt.Errorf("invalid destination backup vault path for Cross Project vault '%s': %w", sourceVault.Name, err)
	}
	if destinationPathInfo.ProjectName != pathInfo.ProjectName || destinationPathInfo.Region != pathInfo.Region {
		return nil, fmt.Errorf(
			"Cross Project destination backup vault path for '%s' does not match requested project and region",
			sourceVault.Name,
		)
	}

	logger.Infof("Using Cross Project destination backup vault '%s' for remote restore lookup instead of source vault '%s'",
		destinationPathInfo.VaultName, pathInfo.VaultName)
	return destinationPathInfo, nil
}

// ConvertGoogleProxyBackupVaultToDatamodel converts googleproxyclient.BackupVaultV1beta directly to datamodel.BackupVault
func ConvertGoogleProxyBackupVaultToDatamodel(bv *googleproxyclient.BackupVaultV1beta, locationId string) (*datamodel.BackupVault, error) {
	var resourceID, backupVaultType string
	var sourceRegion, backupRegion, description *string

	// Convert ResourceID
	resourceID = bv.ResourceId

	// Convert BackupRegion
	if bv.BackupRegion.IsSet() {
		backupRegion = &bv.BackupRegion.Value
	}

	// Convert BackupVaultType
	if bv.BackupVaultType.IsSet() {
		backupVaultType = string(bv.BackupVaultType.Value)
	}

	// Convert Description
	if bv.Description.IsSet() && bv.Description.Value != "" {
		description = &bv.Description.Value
	}

	// SourceRegion and BackupRegion are 'nil' in SDE for IN_REGION backup vaults. They're set only for CROSS_REGION backup vaults.
	if bv.SourceRegion.IsSet() {
		// CROSS_REGION Backup Vault
		sourceRegion = &bv.SourceRegion.Value
	} else {
		// IN_REGION Backup Vault
		sourceRegion = nillable.ToPointer(locationId)
	}

	// Convert BackupRetentionPolicy
	var minEnforcedRetentionDuration *int64
	var isDaily, isWeekly, isMonthly, isAdhoc bool
	if bv.BackupRetentionPolicy.IsSet() {
		policy := bv.BackupRetentionPolicy.Value
		if policy.BackupMinimumEnforcedRetentionDays.IsSet() {
			days := int64(policy.BackupMinimumEnforcedRetentionDays.Value)
			minEnforcedRetentionDuration = &days
		}
		if policy.DailyBackupImmutable.IsSet() {
			isDaily = policy.DailyBackupImmutable.Value
		}
		if policy.WeeklyBackupImmutable.IsSet() {
			isWeekly = policy.WeeklyBackupImmutable.Value
		}
		if policy.MonthlyBackupImmutable.IsSet() {
			isMonthly = policy.MonthlyBackupImmutable.Value
		}
		if policy.ManualBackupImmutable.IsSet() {
			isAdhoc = policy.ManualBackupImmutable.Value
		}
	}

	immutableFields := &datamodel.ImmutableAttributes{
		BackupMinimumEnforcedRetentionDuration: minEnforcedRetentionDuration,
		IsDailyBackupImmutable:                 isDaily,
		IsWeeklyBackupImmutable:                isWeekly,
		IsMonthlyBackupImmutable:               isMonthly,
		IsAdhocBackupImmutable:                 isAdhoc,
	}

	// Convert CMEK attributes
	var cmekFields *datamodel.CmekAttributes
	if bv.KmsConfigResourcePath.IsSet() {
		var encryptionState *string
		if bv.EncryptionState.IsSet() {
			state := string(bv.EncryptionState.Value)
			encryptionState = &state
		}
		cmekFields = &datamodel.CmekAttributes{
			KmsConfigResourcePath:    &bv.KmsConfigResourcePath.Value,
			EncryptionState:          encryptionState,
			BackupsPrimaryKeyVersion: nil,
		}
		if bv.BackupsPrimaryKeyVersion.IsSet() {
			cmekFields.BackupsPrimaryKeyVersion = &bv.BackupsPrimaryKeyVersion.Value
		}
	}

	// Convert State
	var state string
	if bv.State.IsSet() {
		state = string(bv.State.Value)
	}

	// Convert StateDetails
	var stateDetails string
	if bv.StateDetails.IsSet() {
		stateDetails = bv.StateDetails.Value
	}

	// Convert CreatedAt
	var createdAt time.Time
	if bv.CreatedAt.IsSet() {
		createdAt = bv.CreatedAt.Value
	}
	// If CreatedAt is not set, createdAt remains as time.Time{} (zero value)

	// Convert BackupVaultID (UUID)
	var uuid string
	if bv.BackupVaultId.IsSet() {
		uuid = bv.BackupVaultId.Value
	}

	extractVaultName := func(vaultPath string) string {
		if vaultPath == "" {
			return ""
		}
		idx := strings.LastIndexByte(vaultPath, '/')
		if idx < 0 || idx == len(vaultPath)-1 {
			return vaultPath
		}
		return vaultPath[idx+1:]
	}

	sourcePath := ""
	if bv.SourceBackupVault.IsSet() {
		sourcePath = strings.TrimSpace(bv.SourceBackupVault.Value)
	}
	destinationPath := ""
	if bv.DestinationBackupVault.IsSet() {
		destinationPath = strings.TrimSpace(bv.DestinationBackupVault.Value)
	}

	var crossRegionBackupVaultName *string
	if sourcePath != "" && destinationPath != "" {
		switch bv.ResourceId {
		case extractVaultName(sourcePath):
			crossRegionBackupVaultName = &destinationPath
		case extractVaultName(destinationPath):
			crossRegionBackupVaultName = &sourcePath
		}
	}

	return &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{
			UUID:      uuid,
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
			DeletedAt: nil,
		},
		Name:                       resourceID,
		BackupRegionName:           backupRegion,
		SourceRegionName:           sourceRegion,
		LifeCycleState:             state,
		LifeCycleStateDetails:      stateDetails,
		BackupVaultType:            backupVaultType,
		Description:                description,
		ImmutableAttributes:        immutableFields,
		CmekAttributes:             cmekFields,
		CrossRegionBackupVaultName: crossRegionBackupVaultName,
	}, nil
}

// fetchBackupVaultFromRemoteVCP fetches a backup vault from remote VCP API and prepares it for use
func _fetchBackupVaultFromRemoteVCP(ctx context.Context, pathInfo *BackupPathInfo, volume *datamodel.Volume) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)

	// Extract backup region from pathInfo
	backupRegion := pathInfo.Region
	if backupRegion == "" {
		return nil, fmt.Errorf("backup region is empty, cannot fetch from remote VCP")
	}

	logger.Infof("Fetching backup vault '%s' from remote VCP in region '%s'",
		pathInfo.VaultName, backupRegion)

	// Use the backup vault's project (from the backup path) for both the JWT and
	// the API query. In cross-project restores the BV project differs from the
	// volume's project, and the remote VCP validates that the JWT project_number
	// matches the project in the request URL.
	recordHeartbeat(ctx, fmt.Sprintf("Loading remote region configuration for region %s", backupRegion))
	basePath, jwtToken, err := common.GetRemoteRegionConfig(backupRegion, pathInfo.ProjectName)
	if err != nil {
		logger.Errorf("Failed to get remote region configuration for region '%s': %v", backupRegion, err)
		return nil, fmt.Errorf("failed to get remote region configuration: %w", err)
	}

	// Create Google Proxy Client for backup region
	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	params := googleproxyclient.V1betaListBackupVaultsParams{
		ProjectNumber:  pathInfo.ProjectName,
		LocationId:     backupRegion,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	recordHeartbeat(ctx, fmt.Sprintf("Listing backup vaults from remote VCP in region %s", backupRegion))
	res, err := googleProxyClient.Invoker.V1betaListBackupVaults(ctx, params)
	if err != nil {
		logger.Errorf("Error calling V1betaListBackupVaults on remote VCP: %v", err)
		return nil, fmt.Errorf("failed to list backup vaults from remote VCP: %w", err)
	}

	// Handle response type
	var backupVaults []googleproxyclient.BackupVaultV1beta
	switch r := res.(type) {
	case *googleproxyclient.V1betaListBackupVaultsOK:
		if r.BackupVaults != nil {
			backupVaults = r.BackupVaults
		}
	default:
		logger.Errorf("Unexpected response type from V1betaListBackupVaults: %T", res)
		return nil, fmt.Errorf("unexpected response type from remote VCP API")
	}

	if len(backupVaults) == 0 {
		logger.Warnf("No backup vaults returned from remote VCP in region '%s'", backupRegion)
		return nil, errors.NewNotFoundErr("Backup vault", &pathInfo.VaultName)
	}

	logger.Infof("Remote VCP returned %d backup vaults in region %s, searching for '%s'",
		len(backupVaults), backupRegion, pathInfo.VaultName)

	// Search for the specific backup vault by ResourceId (name)
	for i, bv := range backupVaults {
		recordHeartbeat(ctx, fmt.Sprintf("Inspecting remote backup vault %d of %d for restore", i+1, len(backupVaults)))
		if bv.ResourceId == pathInfo.VaultName {
			var backupVaultID string
			if bv.BackupVaultId.IsSet() {
				backupVaultID = bv.BackupVaultId.Value
			}
			logger.Infof("Found backup vault '%s' with ID '%s' in remote VCP region '%s'",
				pathInfo.VaultName, backupVaultID, backupRegion)

			// Convert directly from google-proxy-client format to datamodel format
			bvModel, err := ConvertGoogleProxyBackupVaultToDatamodel(&bv, backupRegion)
			if err != nil {
				return nil, fmt.Errorf("failed to convert backup vault to data model: %w", err)
			}

			bvModel.AccountID = volume.AccountID
			logger.Infof("Successfully fetched backup vault '%s' from remote VCP", pathInfo.VaultName)

			return bvModel, nil
		}
	}

	logger.Warnf("Backup vault '%s' not found in remote VCP region '%s'", pathInfo.VaultName, backupRegion)
	return nil, errors.NewNotFoundErr("Backup vault", &pathInfo.VaultName)
}

// _fetchBackupOrFallbackToRemoteVCP fetches a backup from VCP database or remote VCP
func _fetchBackupOrFallbackToRemoteVCP(ctx context.Context, se database.Storage, pathInfo *BackupPathInfo,
	backupVault *datamodel.BackupVault, volume *datamodel.Volume, region string) (*datamodel.Backup, error) {
	logger := util.GetLogger(ctx)

	// Try VCP database first
	recordHeartbeat(ctx, fmt.Sprintf("Fetching restore backup %s from database", pathInfo.BackupName))
	backup, err := se.GetBackupByNameAndBackupVaultID(ctx, pathInfo.BackupName, backupVault.ID)
	if err == nil {
		return backup, nil
	}

	// If not found, fetch from remote VCP
	if errors.IsNotFoundErr(err) {
		logger.Infof("Backup '%s' not found in VCP, fetching from remote VCP", pathInfo.BackupName)
		return FetchBackupFromRemoteVCP(ctx, pathInfo, backupVault, volume, region)
	}

	return nil, err
}

// _fetchBackupFromRemoteVCP fetches a backup from remote VCP API and prepares it for use
func _fetchBackupFromRemoteVCP(ctx context.Context, pathInfo *BackupPathInfo, backupVault *datamodel.BackupVault, volume *datamodel.Volume, region string) (*datamodel.Backup, error) {
	logger := util.GetLogger(ctx)

	// Extract backup region from pathInfo
	backupRegion := pathInfo.Region
	if backupRegion == "" {
		return nil, fmt.Errorf("backup region is empty, cannot fetch from remote VCP")
	}

	logger.Infof("Fetching backup '%s' from remote VCP in region '%s'", pathInfo.BackupName, backupRegion)

	// Use the backup vault's project (from the backup path) for both the JWT and
	// the API query. In cross-project restores the BV project differs from the
	// volume's project, and the remote VCP validates that the JWT project_number
	// matches the project in the request URL.
	recordHeartbeat(ctx, fmt.Sprintf("Loading remote region configuration for region %s", backupRegion))
	basePath, jwtToken, err := common.GetRemoteRegionConfig(backupRegion, pathInfo.ProjectName)
	if err != nil {
		logger.Errorf("Failed to get remote region configuration for region '%s': %v", backupRegion, err)
		return nil, fmt.Errorf("failed to get remote region configuration: %w", err)
	}

	// Create Google Proxy Client for backup region
	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Get backup vault UUID
	backupVaultUUID := backupVault.UUID
	if backupVaultUUID == "" {
		return nil, fmt.Errorf("backup vault UUID is empty, cannot fetch backup")
	}

	// Call V1betaListBackups API endpoint with BackupName filter
	params := googleproxyclient.V1betaListBackupsParams{
		ProjectNumber:  pathInfo.ProjectName,
		LocationId:     backupRegion,
		BackupVaultId:  backupVaultUUID,
		BackupName:     googleproxyclient.NewOptString(pathInfo.BackupName),
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	recordHeartbeat(ctx, fmt.Sprintf("Listing backups from remote VCP for vault %s", backupVaultUUID))
	res, err := googleProxyClient.Invoker.V1betaListBackups(ctx, params)
	if err != nil {
		logger.Errorf("Error calling V1betaListBackups on remote VCP: %v", err)
		return nil, fmt.Errorf("failed to list backups from remote VCP: %w", err)
	}

	// Handle response type
	var backups []googleproxyclient.BackupV1beta
	switch r := res.(type) {
	case *googleproxyclient.V1betaListBackupsOK:
		if r.Backups != nil {
			backups = r.Backups
		}
	default:
		logger.Errorf("Unexpected response type from V1betaListBackups: %T", res)
		return nil, fmt.Errorf(
			"unexpected response type %T from remote VCP API when listing backups (project=%s, region=%s, vaultID=%s, backupName=%s, correlationID=%s)",
			res, pathInfo.ProjectName, backupRegion, backupVaultUUID, pathInfo.BackupName, correlationID,
		)
	}

	if len(backups) == 0 {
		logger.Warnf("No backups returned from remote VCP in region '%s'", backupRegion)
		return nil, errors.NewNotFoundErr("Backup", &pathInfo.BackupName)
	}

	// Temporary workaround until SDE supports backup name filter on ListBackups API
	for i, b := range backups {
		recordHeartbeat(ctx, fmt.Sprintf("Inspecting remote backup %d of %d for restore", i+1, len(backups)))
		if b.ResourceId.IsSet() && b.ResourceId.Value == pathInfo.BackupName {
			logger.Infof("Found backup '%s' in remote VCP region '%s'", pathInfo.BackupName, backupRegion)
			backup, err := convertGoogleProxyBackupToDatamodel(ctx, &b, backupVault, volume.Account, region)
			if err != nil {
				return nil, fmt.Errorf("failed to convert backup to data model: %w", err)
			}
			logger.Infof("Successfully fetched backup '%s' from remote VCP", pathInfo.BackupName)
			return backup, nil
		}
	}
	return nil, errors.NewNotFoundErr("Backup", &pathInfo.BackupName)
}

// FetchProtocolsForBackup fetches protocols for a backup, either from the API response or from CVP for SDE backups
func FetchProtocolsForBackup(ctx context.Context, b *googleproxyclient.BackupV1beta, account *datamodel.Account, region string) []string {
	logger := util.GetLogger(ctx)

	// If protocols exist in the API response, use them
	if len(b.Protocols) > 0 {
		protocols := make([]string, 0, len(b.Protocols))
		for _, p := range b.Protocols {
			protocols = append(protocols, string(p))
		}
		logger.Debugf("Found protocols '%v' in backup response", protocols)
		return protocols
	}

	// Extract volume UUID from backup
	var volumeUUID string
	if b.VolumeId.IsSet() {
		volumeUUID = b.VolumeId.Value
	}

	// If protocols don't exist in the response, this must be an SDE backup
	// Fetch protocols from CVP using the source volume
	if volumeUUID == "" {
		logger.Debug("Volume UUID is empty, cannot fetch protocols from CVP")
		return nil
	}

	logger.Debugf("Protocols not found in backup response, fetching from CVP for volume '%s' in region '%s'", volumeUUID, region)
	token := GetAuthTokenFromContext(ctx)
	cvpClient := CvpCreateClient(logger, token)
	xCorrelationID := GetCoRelationIDFromContext(ctx)

	fetchedProtocols, err := FetchVolumeProtocolsFromSDE(ctx, volumeUUID, region, account, cvpClient, xCorrelationID)
	if err != nil {
		logger.Warnf("Failed to fetch protocols from CVP for volume '%s': %v", volumeUUID, err)
		// Don't fail the conversion if protocol fetch fails, just return empty slice
		return nil
	}

	logger.Infof("Successfully fetched protocols '%v' from CVP for volume '%s'", fetchedProtocols, volumeUUID)
	return fetchedProtocols
}

// convertGoogleProxyBackupToDatamodel converts googleproxyclient.BackupV1beta to datamodel.Backup
func convertGoogleProxyBackupToDatamodel(ctx context.Context, b *googleproxyclient.BackupV1beta, backupVault *datamodel.BackupVault, account *datamodel.Account, region string) (*datamodel.Backup, error) {
	// Extract backup UUID
	var backupUUID string
	if b.BackupId.IsSet() {
		backupUUID = b.BackupId.Value
	}

	// Extract backup name (ResourceId)
	var backupName string
	if b.ResourceId.IsSet() {
		backupName = b.ResourceId.Value
	}

	// Extract volume UUID
	var volumeUUID string
	if b.VolumeId.IsSet() {
		volumeUUID = b.VolumeId.Value
	}

	// Extract state
	var state string
	if b.State.IsSet() {
		state = string(b.State.Value)
	} else {
		state = string(datamodel.LifeCycleStateAvailable)
	}

	// Extract created time
	var createdAt time.Time
	if b.Created.IsSet() {
		createdAt = b.Created.Value
	}

	// Extract description
	var description string
	if b.Description.IsSet() {
		description = b.Description.Value
	}

	// Extract backup type
	var backupType string
	if b.BackupType.IsSet() {
		backupType = string(b.BackupType.Value)
	}

	// Calculate backup size: prefer VolumeUsageBytes, fallback to BackupChainBytes, default to 0
	backupSize := int64(0)
	if b.VolumeUsageBytes.IsSet() {
		backupSize = b.VolumeUsageBytes.Value
	} else if b.BackupChainBytes.IsSet() {
		backupSize = b.BackupChainBytes.Value
	}

	// Extract bucket name
	var bucketName string
	if b.BucketName.IsSet() {
		bucketName = b.BucketName.Value
	}

	// Extract snapshot name
	var snapshotName string
	if b.SnapshotName.IsSet() {
		snapshotName = b.SnapshotName.Value
	}

	// Extract endpoint UUID
	var endpointUUID string
	if b.EndPointUUID.IsSet() {
		endpointUUID = b.EndPointUUID.Value
	}

	var snapshotUUID string
	if b.SnapshotUuid.IsSet() {
		snapshotUUID = b.SnapshotUuid.Value
	}

	var ontapVolumeStyle string
	if b.OntapVolumeStyle.IsSet() {
		ontapVolumeStyle = b.OntapVolumeStyle.Value
	}

	var isExpertModeBackup bool
	if b.IsExpertModeBackup.IsSet() {
		isExpertModeBackup = b.IsExpertModeBackup.Value
	}

	var constituentCountOfBackup int32
	if b.ConstituentCountOfBackup.IsSet() {
		constituentCountOfBackup = b.ConstituentCountOfBackup.Value
	}

	// Fetch protocols from API response or CVP (for SDE backups)
	protocols := FetchProtocolsForBackup(ctx, b, account, region)

	// Convert to datamodel.Backup
	backup := &datamodel.Backup{
		BaseModel: datamodel.BaseModel{
			UUID:      backupUUID,
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
		},
		Name:          backupName,
		VolumeUUID:    volumeUUID,
		BackupVaultID: backupVault.ID,
		BackupVault:   backupVault,
		State:         state,
		StateDetails:  "Backup restored from remote VCP",
		Description:   description,
		Type:          backupType,
		SizeInBytes:   backupSize,
		Attributes: &datamodel.BackupAttributes{
			SnapshotID:               snapshotUUID,
			SnapshotName:             snapshotName,
			BucketName:               bucketName,
			EndpointUUID:             endpointUUID,
			AccountIdentifier:        account.Name,
			Protocols:                protocols,
			OntapVolumeStyle:         ontapVolumeStyle,
			IsExpertModeBackup:       isExpertModeBackup,
			ConstituentCountOfBackup: constituentCountOfBackup,
		},
	}

	return backup, nil
}

func _createBackupPolicySchedule(ctx context.Context, temporalScheduler *scheduler.TemporalScheduler, vcpBackupPolicy *datamodel.BackupPolicy, customSchedule string) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Creating backup policy schedule for policy: %s", vcpBackupPolicy.Name)

	var cronExpr string
	if customSchedule != "" {
		// Use the custom schedule if provided
		cronExpr = customSchedule
		logger.Infof("Using custom backup schedule: %s", cronExpr)
	} else {
		// Default cron expression based on the created time of the backup policy
		backupPolicyCreatedTime := vcpBackupPolicy.CreatedAt
		cronExpr = fmt.Sprintf("%d %d * * *", backupPolicyCreatedTime.Minute(), backupPolicyCreatedTime.Hour())
		logger.Infof("Using default backup schedule based on creation time: %s", cronExpr)
	}

	createParams := scheduler.CreateScheduleParams{
		ScheduleParams: scheduler.ScheduleParams{
			ScheduleID: vcpBackupPolicy.UUID,
			Args: []interface{}{
				vcpBackupPolicy,
			},
		},
		TemporalScheduleOptions: scheduler.TemporalCreateScheduleParams{
			WorkflowID: utils.RandomUUID(),
			Workflow:   "CreateScheduledBackupInitWorkflow",
			Spec: client.ScheduleSpec{
				CronExpressions: []string{cronExpr},
			},
			WorkflowExecutionTimeout: nillable.ToPointer(workflowengine.ScheduledBackupWorkflowExecutionTimeout),
		},
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating Temporal schedule for backup policy %s", vcpBackupPolicy.UUID))
	_, err := temporalScheduler.Create(ctx, createParams)
	if err != nil {
		logger.Errorf("Failed to create backup policy schedule: %v", err)
		return err
	}
	return nil
}

func (a VolumeCreateActivity) CreateRestoreWorkflow(ctx context.Context, createVolumeParams *common.CreateVolumeParams, volume *datamodel.Volume, hostParams []*common.HostParams, backupVault *datamodel.BackupVault, backup *datamodel.Backup, volCreateResponse *vsa.VolumeResponse) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Creating restore workflow for volume %s", volume.UUID))
	logger.Infof("Creating backup restore workflow for backup: %s", backup.Name)
	se := a.SE

	pathInfo, err := parseBackupPath(createVolumeParams.BackupPath)
	if err != nil {
		logger.Errorf("Failed to parse backup path for vault account resolution: %v", err)
		return err
	}
	backupVaultAccountName := pathInfo.ProjectName

	jobType := datamodel.JobTypeRestoreBackup
	job := &datamodel.Job{
		Type:          string(jobType),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  volume.Name,
		AccountID:     sql.NullInt64{Int64: volume.Account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:      volume.UUID,
			PayloadAttributes: BuildRestoreJobPayloadAttributes(volume, backupVault, backup),
		},
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating restore job for volume %s in database", volume.UUID))
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create snapshot delete job in database %v", err)
		return err
	}

	temporalClient := fetchTemporalClient(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Starting Temporal restore workflow for volume %s", volume.UUID))
	_, err = temporalClient.ExecuteWorkflow(
		ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.BackgroundTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		RestoreBackupWorkflow,
		createVolumeParams,
		volume,
		backupVault,
		backup,
		hostParams,
		volCreateResponse,
		backupVaultAccountName,
	)
	if err != nil {
		logger.Error("Failed to start restore backup workflow: ", "error", err)
		return err
	}

	return nil
}

func BuildRestoreJobPayloadAttributes(volume *datamodel.Volume, backupVault *datamodel.BackupVault, backup *datamodel.Backup) map[string]interface{} {
	attrs := map[string]interface{}{
		"volume_uuid":          volume.UUID,
		"backup_size_in_bytes": backup.SizeInBytes,
	}

	if volume.Account != nil && volume.Account.Name != "" {
		attrs["account_name"] = volume.Account.Name
	} else if volume.VolumeAttributes != nil && volume.VolumeAttributes.AccountName != "" {
		attrs["account_name"] = volume.VolumeAttributes.AccountName
	}

	if volume.VolumeAttributes != nil && len(volume.VolumeAttributes.Protocols) > 0 {
		attrs["protocols"] = strings.Join(volume.VolumeAttributes.Protocols, ",")
	}

	if volume.Pool != nil && volume.Pool.DeploymentName != "" {
		attrs["deployment_name"] = volume.Pool.DeploymentName
	} else if volume.VolumeAttributes != nil && volume.VolumeAttributes.DeploymentName != "" {
		attrs["deployment_name"] = volume.VolumeAttributes.DeploymentName
	}

	attrs["backup_vault_type"] = backupVault.BackupVaultType
	if backupVault.BackupRegionName != nil {
		attrs["backup_region_name"] = *backupVault.BackupRegionName
	}

	return attrs
}

func (a VolumeCreateActivity) UpdateVolumeAttributesInDB(ctx context.Context, volumeUUID string, volumeAttributes *datamodel.VolumeAttributes) error {
	recordHeartbeat(ctx, "Initializing volume attributes update")
	se := a.SE
	recordHeartbeat(ctx, "Starting UpdateVolumeAttributesInDB activity")

	if volumeAttributes != nil && volumeUUID != "" {
		existing, err := se.GetVolume(ctx, volumeUUID)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if existing != nil {
			mergeRestrictedActionsFromExisting(volumeAttributes, existing.VolumeAttributes)
		}
	}

	recordHeartbeat(ctx, "Updating volume attributes in database")
	err := se.UpdateVolumeFields(ctx, volumeUUID, map[string]interface{}{
		"volume_attributes": volumeAttributes,
	})
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "Volume attributes updated successfully")

	recordHeartbeat(ctx, "Finished UpdateVolumeAttributesInDB activity")
	return nil
}

func (a VolumeCreateActivity) UpdateVolumeLargeConstituentInDB(ctx context.Context, volumeUUID string, largeVolumeAttributes *datamodel.LargeVolumeAttributes) error {
	recordHeartbeat(ctx, "Initializing large volume constituent attributes update")
	se := a.SE
	recordHeartbeat(ctx, "Starting UpdateVolumeLargeConstituentInDB activity")

	recordHeartbeat(ctx, "Updating large volume attributes in database")
	err := se.UpdateVolumeFields(ctx, volumeUUID, map[string]interface{}{
		"large_volume_attributes": largeVolumeAttributes,
	})
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "Volume large constituent attributes updated successfully")

	recordHeartbeat(ctx, "Finished UpdateVolumeLargeConstituentInDB activity")
	return nil
}

// CrossPoolOrVPCRestorationActivity handles the VPC pool restoration logic when restoring a backup to a different VPC pool
func (a *VolumeCreateActivity) CrossPoolOrVPCRestorationActivity(ctx context.Context, targetPool *datamodel.Pool, backup *datamodel.Backup) error {
	log := util.GetLogger(ctx)

	targetPoolTenantProject, err := GetPoolTenantProject(targetPool)
	if err != nil {
		log.Errorf("Failed to get target pool tenant project: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	backupTenantProject, err := GetBackupTenantProject(backup)
	if err != nil {
		log.Errorf("Failed to get backup tenant project: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if strings.EqualFold(targetPoolTenantProject, backupTenantProject) {
		return nil
	}

	log.Infof("Target pool tenant project (%s) differs from backup tenant project (%s), setting up cross-project permissions", targetPoolTenantProject, backupTenantProject)

	recordHeartbeat(ctx, fmt.Sprintf("Setting up cross-project restore permissions for pool %s", targetPool.UUID))
	err = a.SetupCrossTenantProjectPermissions(ctx, targetPool, backupTenantProject)
	if err != nil {
		log.Errorf("Failed to setup cross-project permissions: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	log.Infof("Successfully set up cross-project permissions for VPC pool restoration")
	return nil
}

// setupCrossTenantProjectPermissions sets up the required IAM permissions for cross-project backup restoration
func (a *VolumeCreateActivity) SetupCrossTenantProjectPermissions(ctx context.Context, targetPool *datamodel.Pool, backupTenantProject string) error {
	log := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Preparing cross-tenant permissions for pool %s", targetPool.UUID))

	// Get the service account from the target pool
	poolServiceAccount, err := GetPoolServiceAccountName(targetPool, targetPool.ClusterDetails.RegionalTenantProject)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Grant the storage.objectAdmin role to the pool service account in the backup tenant project
	err = GrantStorageObjectAdminRole(ctx, poolServiceAccount, backupTenantProject)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	log.Infof("Successfully granted storage.objectAdmin role to service account %s in project %s", poolServiceAccount, backupTenantProject)

	return nil
}

// SetupCrossProjectBackupPermissions sets up IAM permissions for GCBDR cross-project backups.
// Delegates to SetupCrossTenantProjectPermissions for the core grant logic, then tracks the
// tenant project for cleanup during pool deletion.
func (a *VolumeCreateActivity) SetupCrossProjectBackupPermissions(ctx context.Context, pool *datamodel.Pool, bucketDetails *common.BucketDetails) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, "Checking cross-project backup permissions")

	if pool == nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("pool is nil"))
	}

	if bucketDetails == nil || bucketDetails.TenantProjectNumber == "" {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("bucket details or tenant project number is empty"))
	}

	bucketTenantProject := bucketDetails.TenantProjectNumber

	if strings.EqualFold(pool.ClusterDetails.RegionalTenantProject, bucketTenantProject) {
		logger.Info("Pool and bucket are in same tenant project, skipping cross-project permissions")
		return nil
	}

	if err := a.SetupCrossTenantProjectPermissions(ctx, pool, bucketTenantProject); err != nil {
		return err
	}

	// Track the tenant project for cleanup during pool deletion
	recordHeartbeat(ctx, fmt.Sprintf("Tracking cross-project backup permissions for pool %s", pool.UUID))
	if err := addServiceAccountPermissionProject(ctx, a.SE, pool.UUID, bucketTenantProject); err != nil {
		logger.Warnf("Failed to track tenant project %s for pool %s: %v", bucketTenantProject, pool.UUID, err)
	}

	return nil
}

// getPoolServiceAccount extracts the service account from the target pool
func _getPoolServiceAccount(pool *datamodel.Pool, projectID string) (string, error) {
	saEmail := utils.ConstructServiceAccountEmail(pool.ServiceAccountId, projectID)
	return saEmail, nil
}

// _grantStorageObjectAdminRole  grants the storage.objectAdmin role to a service account in a project
func _grantStorageObjectAdminRole(ctx context.Context, serviceAccountEmail, projectID string) error {
	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for project %s role grant", projectID))
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Grant the specific role needed for backup restoration
	roles := []string{"roles/storage.objectAdmin"}
	recordHeartbeat(ctx, fmt.Sprintf("Granting restore access role in project %s", projectID))
	err = cloudService.AttachOrUpdateRolesForServiceAccounts(roles, serviceAccountEmail, projectID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, fmt.Sprintf("Finished granting restore access role in project %s", projectID))

	return nil
}

// DeleteServiceAccountInBackupTenantProject deletes the service account in the backup tenant project after restoration is complete
func (a VolumeCreateActivity) DeleteRolesForServiceAccountInBackupTenantProject(ctx context.Context, targetPool *datamodel.Pool, backup *datamodel.Backup) error {
	log := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Removing restore permissions for pool %s", targetPool.UUID))

	// Get the service account from the target pool
	poolServiceAccount, err := GetPoolServiceAccountName(targetPool, targetPool.ClusterDetails.RegionalTenantProject)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	backupTenantProject, err := GetBackupTenantProject(backup)
	if err != nil {
		log.Errorf("Failed to get backup tenant project: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service to remove restore permissions for pool %s", targetPool.UUID))
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Grant the specific role needed for backup restoration
	roles := []string{"roles/storage.objectAdmin"}
	recordHeartbeat(ctx, fmt.Sprintf("Removing restore access role for pool %s", targetPool.UUID))
	err = cloudService.RemoveRolesFromServiceAccounts(roles, poolServiceAccount, backupTenantProject)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

// DeleteRestoreObjectStore deletes restore object store after the transfer is complete
func (a *VolumeCreateActivity) DeleteRestoreObjectStore(ctx context.Context, node *models.Node, name string) (*vsa.OntapAsyncResponse, error) {
	log := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Resolving ONTAP provider to delete restore object store %s", name))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Handle both return values from CloudTargetGet
	recordHeartbeat(ctx, fmt.Sprintf("Fetching restore object store %s from ONTAP", name))
	objectStore, err := provider.CloudTargetGet(&name)
	if err != nil {
		// If there is an error, it means the object store does not exist
		log.Infof("Object store %s does not exist, nothing to delete", name)
		return nil, nil
	}
	if objectStore == nil || objectStore.UUID == nil {
		log.Infof("Object store %s does not exist, nothing to delete", name)
		return nil, nil
	}
	recordHeartbeat(ctx, fmt.Sprintf("Deleting restore object store %s from ONTAP", name))
	asyncResp, err := provider.CloudTargetDelete(*objectStore.UUID)
	if err != nil {
		return nil, err
	}
	return asyncResp, nil
}

func (a VolumeCreateActivity) ConfigureLdap(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	se := a.SE

	logger := util.GetLogger(ctx)
	if volume.VolumeAttributes == nil || volume.VolumeAttributes.FileProperties == nil {
		logger.Info("Skipping ldap configuration for non-file volume")
		return nil
	}
	recordHeartbeat(ctx, fmt.Sprintf("Resolving ONTAP provider for LDAP configuration of volume %s", volume.UUID))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Fetching pool %s from database for LDAP configuration", volume.Pool.UUID))
	pool, err := se.GetPoolByUUIDForAccount(ctx, volume.Pool.UUID, volume.AccountID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	ldapEnabled := false
	if pool.PoolAttributes != nil {
		ldapEnabled = pool.PoolAttributes.LdapEnabled
	}
	logger.Infof("Configure LDAP for volume %s: ldapEnabled=%v", volume.UUID, ldapEnabled)
	if !ldapEnabled {
		logger.Info("Skipping ldap configuration for non-LDAP pool")
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Fetching active directory for pool %s from database", volume.Pool.UUID))
	ad, err := se.GetActiveDirectoryForPoolByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if ad == nil {
		logger.Error("Skipping ldap configuration for non-active directory pool")
		return vsaerrors.WrapAsTemporalApplicationError(errors.New("Active Directory configuration is required for LDAP-enabled pools but is missing"))
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating LDAP configuration on ONTAP for volume %s", volume.UUID))
	err = provider.CreateLdap(ad, volume)
	if err != nil {
		if errors.IsConflictErr(err) {
			// If LDAP config already exists, we can skip creation
			logger.Info("LDAP config already exists, skipping LDAP configuration")
			return nil
		}
		logger.Errorf("Failed to configure LDAP for pool (pool_id=%d, pool_uuid=%s, account_id=%d): %v",
			pool.ID, volume.Pool.UUID, volume.AccountID, err)
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainLDAP)
	}
	return nil
}

// convertInternalAPIToDatamodel converts internal API BackupVault to datamodel BackupVault
func convertInternalAPIToDatamodel(apiBackupVault *googleproxyclient.BackupVaultInternalV1beta) *datamodel.BackupVault {
	if apiBackupVault == nil {
		return nil
	}

	backupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{
			UUID: apiBackupVault.BackupVaultId,
		},
		Name:            apiBackupVault.BackupVaultId,
		AccountVendorID: apiBackupVault.AccountVendorId,
		LifeCycleState:  string(apiBackupVault.LifeCycleState),
		BackupVaultType: string(apiBackupVault.BackupVaultType),
		ServiceType:     datamodel.ServiceTypeGCNV,
	}

	if apiBackupVault.ServiceType.IsSet() && apiBackupVault.ServiceType.Value != "" {
		backupVault.ServiceType = apiBackupVault.ServiceType.Value
	}

	if apiBackupVault.Description.IsSet() && apiBackupVault.Description.Value != "" {
		desc := apiBackupVault.Description.Value
		backupVault.Description = &desc
	}

	if apiBackupVault.LifeCycleStateDetails.IsSet() && apiBackupVault.LifeCycleStateDetails.Value != "" {
		details := apiBackupVault.LifeCycleStateDetails.Value
		backupVault.LifeCycleStateDetails = details
	}

	if apiBackupVault.SourceRegion.IsSet() && apiBackupVault.SourceRegion.Value != "" {
		sourceRegion := apiBackupVault.SourceRegion.Value
		backupVault.SourceRegionName = &sourceRegion
	}

	if apiBackupVault.BackupRegion.IsSet() && apiBackupVault.BackupRegion.Value != "" {
		backupRegion := apiBackupVault.BackupRegion.Value
		backupVault.BackupRegionName = &backupRegion
	}

	if apiBackupVault.CrossRegionBackupVaultName.IsSet() && apiBackupVault.CrossRegionBackupVaultName.Value != "" {
		crossRegionName := apiBackupVault.CrossRegionBackupVaultName.Value
		backupVault.CrossRegionBackupVaultName = &crossRegionName
	}

	if apiBackupVault.ExternalUuid.IsSet() && apiBackupVault.ExternalUuid.Value != "" {
		externalUuid := apiBackupVault.ExternalUuid.Value
		backupVault.ExternalUUID = &externalUuid
	}

	if len(apiBackupVault.BucketDetails) > 0 {
		bucketDetails := make(datamodel.BucketDetailsArray, 0, len(apiBackupVault.BucketDetails))
		for _, bucket := range apiBackupVault.BucketDetails {
			bucketDetail := &datamodel.BucketDetails{}

			if bucket.BucketName.IsSet() && bucket.BucketName.Value != "" {
				bucketDetail.BucketName = bucket.BucketName.Value
			}
			if bucket.ServiceAccountName.IsSet() {
				bucketDetail.ServiceAccountName = bucket.ServiceAccountName.Value
			}
			if bucket.VendorSubnetId.IsSet() && bucket.VendorSubnetId.Value != "" {
				bucketDetail.VendorSubnetID = bucket.VendorSubnetId.Value
			}
			if bucket.TenantProjectNumber.IsSet() && bucket.TenantProjectNumber.Value != "" {
				bucketDetail.TenantProjectNumber = bucket.TenantProjectNumber.Value
			}

			if bucketDetail.BucketName != "" {
				bucketDetails = append(bucketDetails, bucketDetail)
			}
		}
		if len(bucketDetails) > 0 {
			backupVault.BucketDetails = bucketDetails
		}
	}

	// Extract CMEK attributes from internal API response
	var cmekFields *datamodel.CmekAttributes
	if apiBackupVault.KmsConfigResourcePath.IsSet() {
		cmekFields = &datamodel.CmekAttributes{}
		kmsConfigPath := apiBackupVault.KmsConfigResourcePath.Value
		cmekFields.KmsConfigResourcePath = &kmsConfigPath
		if apiBackupVault.EncryptionState.IsSet() {
			encryptionState := string(apiBackupVault.EncryptionState.Value)
			cmekFields.EncryptionState = &encryptionState
		}
		if apiBackupVault.BackupsPrimaryKeyVersion.IsSet() {
			backupsPrimaryKeyVersion := apiBackupVault.BackupsPrimaryKeyVersion.Value
			cmekFields.BackupsPrimaryKeyVersion = &backupsPrimaryKeyVersion
		}
	}
	backupVault.CmekAttributes = cmekFields

	return backupVault
}

// convertDatamodelToInternalAPI converts datamodel BackupVault to internal API BackupVault
func convertDatamodelToInternalAPI(datamodelBackupVault *datamodel.BackupVault) *googleproxyclient.BackupVaultInternalV1beta {
	if datamodelBackupVault == nil {
		return nil
	}

	apiBackupVault := &googleproxyclient.BackupVaultInternalV1beta{
		BackupVaultId:   datamodelBackupVault.UUID,
		ResourceId:      datamodelBackupVault.Name,
		AccountVendorId: datamodelBackupVault.AccountVendorID,
	}

	apiBackupVault.BackupVaultType = googleproxyclient.BackupVaultInternalV1betaBackupVaultType(datamodelBackupVault.BackupVaultType)
	apiBackupVault.LifeCycleState = googleproxyclient.BackupVaultInternalV1betaLifeCycleState(datamodelBackupVault.LifeCycleState)

	if datamodelBackupVault.ServiceType != "" {
		apiBackupVault.ServiceType = googleproxyclient.NewOptString(datamodelBackupVault.ServiceType)
	}

	if datamodelBackupVault.Description != nil {
		apiBackupVault.Description = googleproxyclient.NewOptString(*datamodelBackupVault.Description)
	}

	if datamodelBackupVault.LifeCycleStateDetails != "" {
		apiBackupVault.LifeCycleStateDetails = googleproxyclient.NewOptString(datamodelBackupVault.LifeCycleStateDetails)
	}

	if datamodelBackupVault.SourceRegionName != nil {
		apiBackupVault.SourceRegion = googleproxyclient.NewOptString(*datamodelBackupVault.SourceRegionName)
	}

	if datamodelBackupVault.BackupRegionName != nil {
		apiBackupVault.BackupRegion = googleproxyclient.NewOptString(*datamodelBackupVault.BackupRegionName)
	}

	if datamodelBackupVault.CrossRegionBackupVaultName != nil {
		apiBackupVault.CrossRegionBackupVaultName = googleproxyclient.NewOptString(*datamodelBackupVault.CrossRegionBackupVaultName)
	}

	if datamodelBackupVault.ExternalUUID != nil {
		apiBackupVault.ExternalUuid = googleproxyclient.NewOptString(*datamodelBackupVault.ExternalUUID)
	}

	if datamodelBackupVault.ImmutableAttributes != nil {
		immutableAttrs := googleproxyclient.BackupVaultInternalV1betaImmutableAttributes{
			IsDailyBackupImmutable:   googleproxyclient.NewOptBool(datamodelBackupVault.ImmutableAttributes.IsDailyBackupImmutable),
			IsWeeklyBackupImmutable:  googleproxyclient.NewOptBool(datamodelBackupVault.ImmutableAttributes.IsWeeklyBackupImmutable),
			IsMonthlyBackupImmutable: googleproxyclient.NewOptBool(datamodelBackupVault.ImmutableAttributes.IsMonthlyBackupImmutable),
			IsAdhocBackupImmutable:   googleproxyclient.NewOptBool(datamodelBackupVault.ImmutableAttributes.IsAdhocBackupImmutable),
		}
		if datamodelBackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration != nil {
			immutableAttrs.BackupMinimumEnforcedRetentionDuration = googleproxyclient.NewOptInt(int(*datamodelBackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration))
		}
		apiBackupVault.ImmutableAttributes = googleproxyclient.NewOptBackupVaultInternalV1betaImmutableAttributes(immutableAttrs)
	}

	if len(datamodelBackupVault.BucketDetails) > 0 {
		var bucketDetails []googleproxyclient.BackupVaultInternalV1betaBucketDetailsItem
		for _, bucket := range datamodelBackupVault.BucketDetails {
			bucketDetail := googleproxyclient.BackupVaultInternalV1betaBucketDetailsItem{
				BucketName:          googleproxyclient.NewOptString(bucket.BucketName),
				ServiceAccountName:  googleproxyclient.NewOptString(bucket.ServiceAccountName),
				VendorSubnetId:      googleproxyclient.NewOptString(bucket.VendorSubnetID),
				TenantProjectNumber: googleproxyclient.NewOptString(bucket.TenantProjectNumber),
				SatisfiesPzs:        googleproxyclient.NewOptBool(bucket.SatisfiesPzs),
				SatisfiesPzi:        googleproxyclient.NewOptBool(bucket.SatisfiesPzi),
			}
			bucketDetails = append(bucketDetails, bucketDetail)
		}
		apiBackupVault.BucketDetails = bucketDetails
	}

	// Include CMEK attributes in internal API response
	if datamodelBackupVault.CmekAttributes != nil {
		// KmsConfigResourcePath is always set if CmekAttributes exists (it's the primary indicator)
		apiBackupVault.KmsConfigResourcePath = googleproxyclient.NewOptString(*datamodelBackupVault.CmekAttributes.KmsConfigResourcePath)
		if datamodelBackupVault.CmekAttributes.EncryptionState != nil {
			apiBackupVault.EncryptionState = googleproxyclient.NewOptBackupVaultInternalV1betaEncryptionState(googleproxyclient.BackupVaultInternalV1betaEncryptionState(*datamodelBackupVault.CmekAttributes.EncryptionState))
		}
		if datamodelBackupVault.CmekAttributes.BackupsPrimaryKeyVersion != nil {
			apiBackupVault.BackupsPrimaryKeyVersion = googleproxyclient.NewOptString(*datamodelBackupVault.CmekAttributes.BackupsPrimaryKeyVersion)
		}
	}

	return apiBackupVault
}

// bucketDetailsExist checks if bucket details already exist in the array
func bucketDetailsExist(existingBuckets datamodel.BucketDetailsArray, newBucket *datamodel.BucketDetails) bool {
	if newBucket == nil {
		return false
	}

	for _, existing := range existingBuckets {
		if existing.BucketName == newBucket.BucketName &&
			existing.VendorSubnetID == newBucket.VendorSubnetID {
			return true
		}
	}
	return false
}
