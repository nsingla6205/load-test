package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/jx"
	"github.com/google/uuid"
	"github.com/robfig/cron"
	coreapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/volumes"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	ontapmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	createCVPClient                   = cvp.CreateClient
	convertVolumeV1betaToCVPModel     = _convertVolumeV1betaCVPToModel
	getMultipleVolumesFromCVP         = _getMultipleVolumesFromCVP
	prepareUpdateVolumeParams         = _prepareUpdateVolumeParams
	prepareCreateVolumeParams         = _prepareCreateVolumeParams
	prepareRevertVolumeParams         = _prepareRevertVolumeParams
	hasNfs4KerberosV1beta             = _hasNfs4KerberosV1beta
	validateKerberosPolicyV1beta      = _validateKerberosPolicyV1beta
	getKerberosEnabledFlagFromRequest = _getKerberosEnabledFlagFromRequest
	autoTieringEnabled                = env.GetBool("AUTO_TIERING_ENABLED", false)
	qaEnabled                         = env.GetBool("QA_ENABLED", false)
	flexCacheEnabled                  = env.GetBool("FLEXCACHE_ENABLED", false)
	sfrEnabled                        = env.GetBool("SFR_ENABLED", false)
	MaxSourceFileList                 = env.GetInt("MAX_SOURCE_FILE_LIST", 8)
	thinCloneGASupport                = env.GetBool("THIN_CLONE_GA_SUPPORT", false)
	hybridReplicationEnabled          = env.GetBool("HYBRID_REPLICATION_ENABLED", false)
	cmekBackupEnabled                 = env.GetBool("CMEK_BACKUP_ENABLED", false)
	bidiReplicationEnabled            = env.GetBool("BIDI_REPLICATION_ENABLED", false)
	enableSmb                         = env.GetBool("ENABLE_SMB", false)
	enableKerberos                    = env.GetBool("ENABLE_KERBEROS", false)
	unixPermissionsEnabled            = env.GetBool("ENABLE_UNIX_PERMISSIONS", true)
	smbCaShareEnabled                 = env.GetBool("SMB_CA_SHARE_ENABLED", false)
	exportRulesLimit                  = env.GetInt("EXPORT_POLICY_RULES_LIMIT", 20)
	enableInferredIops                = env.GetBool("ENABLE_INFERRED_IOPS", false)
)

const (
	volumeTypeSecondary          = "SECONDARY"
	SnapshotScheduleLabelHourly  = "hourly"
	SnapshotScheduleLabelDaily   = "daily"
	SnapshotScheduleLabelWeekly  = "weekly"
	SnapshotScheduleLabelMonthly = "monthly"
	MaxBackupPathComponents      = 8
	MinBackupScheduleInterval    = 5 * time.Minute // Minimum 5 minutes interval

	daysOfMonthError            = `daysOfMonth must include unique values in the range 1-31 (inclusive).`
	daysOfWeekError             = `day in weeklySchedule must include 1-7 (inclusive) unique weekdays, that are comma separated.`
	unixPermissionsRegexPattern = "^[0-7]{4}$"
)

func (h Handler) V1betaDescribeVolume(ctx context.Context, params gcpgenserver.V1betaDescribeVolumeParams) (gcpgenserver.V1betaDescribeVolumeRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	volume, err := h.Orchestrator.GetVolume(ctx, params.VolumeId, true)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaDescribeVolumeNotFound{
				Code:    404,
				Message: "Volume not found",
			}, nil
		}
		logger.Error("Failed to describe volume", "error", err.Error())
		return &gcpgenserver.V1betaDescribeVolumeInternalServerError{Code: 500, Message: "Internal server error"}, nil
	}

	return convertModelToVCPVolume(volume), nil
}

func (h Handler) V1betaGetVolumeCount(ctx context.Context, params gcpgenserver.V1betaGetVolumeCountParams) (gcpgenserver.V1betaGetVolumeCountRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	count, err := h.Orchestrator.GetVolumeCount(ctx, params.ProjectNumber)
	if err != nil {
		logger.Error("Error while getting volume count", "error", err.Error())
		return &gcpgenserver.V1betaGetVolumeCountInternalServerError{Code: 500, Message: "Internal server error"}, nil
	}
	return &gcpgenserver.V1betaGetVolumeCountOK{VolumeCount: int(count)}, nil
}

func (h Handler) V1betaListVolumes(ctx context.Context, params gcpgenserver.V1betaListVolumesParams) (gcpgenserver.V1betaListVolumesRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	volumes, err := h.Orchestrator.ListVolumes(ctx, params.ProjectNumber)
	if err != nil {
		logger.Error("Failed to list volumes", "error", err.Error())
		return &gcpgenserver.V1betaListVolumesInternalServerError{Code: 500, Message: "Internal server error"}, nil
	}
	resp := &gcpgenserver.V1betaListVolumesOK{
		Volumes: convertModelsToVCPVolumes(volumes),
	}
	return resp, nil
}

func convertModelsToVCPVolumes(volumes []*models.Volume) []gcpgenserver.VolumeV1beta {
	var volumesList []gcpgenserver.VolumeV1beta
	for _, volume := range volumes {
		p := convertModelToVCPVolume(volume)
		volumesList = append(volumesList, *p)
	}
	return volumesList
}

func (h Handler) V1betaCreateVolume(ctx context.Context, req *gcpgenserver.VolumeCreateV1beta, params gcpgenserver.V1betaCreateVolumeParams) (gcpgenserver.V1betaCreateVolumeRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	if req.HybridReplicationParameters.IsSet() && !hybridReplicationEnabled {
		return &gcpgenserver.V1betaCreateVolumeBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Hybrid migration is not enabled",
		}, nil
	}

	if req.HybridReplicationParameters.IsSet() && req.HybridReplicationParameters.Value.HybridReplicationType == gcpgenserver.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION && !bidiReplicationEnabled {
		return &gcpgenserver.V1betaCreateVolumeBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Onprem replication is not enabled",
		}, nil
	}

	region, zone, parsingErr := utils.ParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaCreateVolumeBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req.HybridReplicationParameters.IsSet() && len(req.Volume.BlockDevices) > 0 {
		for _, paramBlockDevice := range req.Volume.BlockDevices {
			if paramBlockDevice.Name.IsSet() && paramBlockDevice.Name.Value != "" {
				return &gcpgenserver.V1betaCreateVolumeBadRequest{
					Code:    http.StatusBadRequest,
					Message: "Block device name is not supported for hybrid replication volume. This will be replicated from onprem volume.",
				}, nil
			}
		}
	}

	var pool *models.Pool
	var err error

	pool, err = h.Orchestrator.DescribePool(ctx, req.Volume.PoolId.Value, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaCreateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to fetch pool.", "error", err.Error())
		return &gcpgenserver.V1betaCreateVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	if pool != nil && pool.PoolAttributes != nil && (pool.PoolAttributes.ZoneSwitchState == datamodel.ZoneSwitching || pool.PoolAttributes.ZoneSwitchState == datamodel.ZoneSwitched) {
		return &gcpgenserver.V1betaCreateVolumeBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Volume creation is not supported when the pool is switching/switched to a different primary zone.",
		}, nil
	}

	param, err := prepareCreateVolumeParams(req, params, region, zone, pool)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaCreateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to create volume", "error", err.Error())
		return &gcpgenserver.V1betaCreateVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	var volume *models.Volume
	var jobUUID string
	if param.CacheParameters != nil {
		if !flexCacheEnabled {
			return &gcpgenserver.V1betaCreateVolumeBadRequest{
				Code:    403,
				Message: "FlexCache feature is currently not enabled.",
			}, nil
		}
		volume, jobUUID, err = h.Orchestrator.CreateFlexCacheVolume(ctx, param)
	} else {
		volume, jobUUID, err = h.Orchestrator.CreateVolume(ctx, param)
	}
	if err != nil {
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaCreateVolumeConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaCreateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to create volume", "error", err.Error())
		return &gcpgenserver.V1betaCreateVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	resp, err := encodeVolumeV1(convertModelToVCPVolume(volume))
	if err != nil {
		return nil, err
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + jobUUID
	if volume.LifeCycleState == datamodel.LifeCycleStateCreating || volume.LifeCycleState == datamodel.LifeCycleStatePreparing {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

func (h Handler) V1betaRevertVolume(ctx context.Context, req *gcpgenserver.VolumeRevertV1beta, params gcpgenserver.V1betaRevertVolumeParams) (gcpgenserver.V1betaRevertVolumeRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	region, _, parsingErr := utils.ParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaRevertVolumeBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	param, err := prepareRevertVolumeParams(req, params, region)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaRevertVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to revert volume", "error", err.Error())
		return &gcpgenserver.V1betaRevertVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	volume, jobUUID, err := h.Orchestrator.RevertVolume(ctx, param)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaRevertVolumeNotFound{
				Code:    404,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaRevertVolumeConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}
		if errors.IsUserInputValidationErr(err) || errors.IsConflictErr(err) || strings.Contains(err.Error(), "one or more newer Snapshot copies are currently used as a reference Snapshot copy for data protection operations") {
			return &gcpgenserver.V1betaRevertVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to revert volume", "error", err.Error())
		return &gcpgenserver.V1betaRevertVolumeInternalServerError{Code: 500, Message: err.Error()}, err
	}

	resp, err := encodeVolumeV1(convertModelToVCPVolume(volume))
	if err != nil {
		return nil, err
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + jobUUID
	if volume.LifeCycleState == datamodel.LifeCycleStateReverting {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

func _prepareCreateVolumeParams(req *gcpgenserver.VolumeCreateV1beta, params gcpgenserver.V1betaCreateVolumeParams, region, zone string, pool *models.Pool) (*common.CreateVolumeParams, error) {
	vendorId := fmt.Sprintf("/projects/%v/locations/%v/volumes/%s", params.ProjectNumber, params.LocationId, req.Volume.ResourceId)

	if strings.Contains(req.Volume.ResourceId, "-") {
		return nil, errors.NewUserInputValidationErr("The Resource ID can only contain lowercase letters, numbers, and underscores. It must start with a letter and cannot end with an underscore.")
	}

	isRestoreFromSnapshot := false
	if req.SnapshotId.IsSet() {
		isRestoreFromSnapshot = true
	}

	if !req.Volume.QuotaInBytes.IsSet() {
		if !isRestoreFromSnapshot {
			return nil, errors.NewUserInputValidationErr("QuotaInBytes is required")
		}
	}

	var backupPath string
	if req.BackupPath.IsSet() {
		if !backupEnabled {
			return nil, errors.NewUserInputValidationErr("Backup feature is currently not enabled.")
		}
		backupPath = req.BackupPath.Value
		components := strings.Split(backupPath, "/")
		// Ensure there are enough components to avoid out of range errors
		if len(components) < MaxBackupPathComponents {
			return nil, errors.NewUserInputValidationErr("Backup path is not in correct format")
		}
	}
	var backupID string
	if req.BackupId.IsSet() {
		backupID = req.BackupId.Value
	}

	var backupSchedule string
	if qaEnabled && params.XNetappBackupSchedule.IsSet() {
		backupSchedule = params.XNetappBackupSchedule.Value
		// Validate the backup schedule cron expression
		if err := validateBackupScheduleCron(backupSchedule); err != nil {
			return nil, err
		}
	}

	kerberosEnabled := getKerberosEnabledFlagFromRequest(&req.Volume.KerberosEnabled.Value)

	param := &common.CreateVolumeParams{
		AccountName:     params.ProjectNumber,
		Region:          region,
		Zone:            zone,
		Name:            req.Volume.ResourceId,
		VendorID:        vendorId,
		CreationToken:   req.Volume.CreationToken.Value,
		PoolID:          req.Volume.PoolId.Value,
		QuotaInBytes:    uint64(req.Volume.QuotaInBytes.Value),
		BackupID:        backupID,
		BackupPath:      backupPath,
		BackupSchedule:  backupSchedule,
		KerberosEnabled: kerberosEnabled,
	}

	if req.Volume.CacheParameters.IsSet() {
		if err := validateFlexCacheRequest(req); err != nil {
			return nil, errors.NewUserInputValidationErr(err.Error())
		}

		reqCacheProperties, _ := req.Volume.CacheParameters.Get()
		param.CacheParameters = &models.CacheParameters{
			CacheState:            cvpmodels.FlexCacheV1betaPreviousCacheStatePENDINGCLUSTERPEERING,
			CacheStateDetailsCode: models.InitiatingClusterPeeringCode,
			CacheStateDetails:     datamodel.InitiatingClusterPeering,
		}
		if reqCacheProperties.PeerVolumeName.IsSet() {
			param.CacheParameters.PeerVolumeName = reqCacheProperties.PeerVolumeName.Value
		}
		if reqCacheProperties.PeerClusterName.IsSet() {
			param.CacheParameters.PeerClusterName = reqCacheProperties.PeerClusterName.Value
		}
		if reqCacheProperties.PeerSvmName.IsSet() {
			param.CacheParameters.PeerSvmName = reqCacheProperties.PeerSvmName.Value
		}
		param.CacheParameters.PeerIPAddresses = reqCacheProperties.PeerIpAddresses

		if reqCacheProperties.PeeringCommandExpiryTime.IsSet() {
			param.CacheParameters.PeerExpiryTime = &reqCacheProperties.PeeringCommandExpiryTime.Value
		}

		if reqCacheProperties.EnableGlobalFileLock.IsSet() {
			param.CacheParameters.EnableGlobalFileLock = &reqCacheProperties.EnableGlobalFileLock.Value
		}

		if reqCacheProperties.CacheConfig.IsSet() {
			reqCacheConfig, _ := reqCacheProperties.CacheConfig.Get()
			param.CacheParameters.CacheConfig = &models.CacheConfig{}

			if reqCacheConfig.WritebackEnabled.IsSet() {
				param.CacheParameters.CacheConfig.WritebackEnabled = &reqCacheConfig.WritebackEnabled.Value
			}
			if reqCacheConfig.AtimeScrubEnabled.IsSet() {
				param.CacheParameters.CacheConfig.AtimeScrubEnabled = &reqCacheConfig.AtimeScrubEnabled.Value
			}
			if reqCacheConfig.AtimeScrubDays.IsSet() {
				param.CacheParameters.CacheConfig.AtimeScrubDays = &reqCacheConfig.AtimeScrubDays.Value
			}
			if reqCacheConfig.CifsChangeNotifyEnabled.IsSet() {
				param.CacheParameters.CacheConfig.CifsChangeNotifyEnabled = &reqCacheConfig.CifsChangeNotifyEnabled.Value
			}
		}
	}

	if req.Volume.SnapshotDirectory.IsSet() {
		param.SnapshotDirectory = req.Volume.SnapshotDirectory.Value
	}

	if (req.VolumeType.IsSet() && req.VolumeType.Value == volumeTypeSecondary) || req.HybridReplicationParameters.IsSet() {
		param.IsDataProtection = true
	}

	if req.Volume.Description.IsSet() {
		param.Description, _ = req.Volume.Description.Get()
	}

	if req.Volume.Network.IsSet() {
		param.Network, _ = req.Volume.Network.Get()
	}

	if req.Volume.Labels.IsSet() {
		jsonbLabels, err := validateLabels(req.Volume.Labels.Value)
		if err != nil {
			return nil, errors.NewUserInputValidationErr(err.Error())
		}
		param.Labels = jsonbLabels
	}

	hasThroughput := req.Volume.ThroughputMibps.IsSet()
	hasIops := req.Volume.Iops.IsSet()
	hasVpg := req.Volume.VolumePerformanceGroupId.IsSet()

	// Check MQOS feature flag for each parameter separately to provide specific error messages
	if !enableMqos {
		if hasThroughput {
			return nil, errors.NewUserInputValidationErr(utils.ErrMsgMqosNotEnabledThroughput)
		}
		if hasIops {
			return nil, errors.NewUserInputValidationErr(utils.ErrMsgMqosNotEnabledIops)
		}
		if hasVpg {
			return nil, errors.NewUserInputValidationErr(utils.ErrMsgMqosNotEnabledVpgId)
		}
	}

	// VPG feature flag check
	if hasVpg && !enableVolumePerformanceGroupAssignment {
		return nil, errors.NewUserInputValidationErr(utils.ErrMsgVpgAssignmentNotEnabled)
	}

	// Mutual exclusion: VPG cannot be combined with throughput or iops
	if hasVpg && (hasThroughput || hasIops) {
		return nil, errors.NewUserInputValidationErr(utils.ErrMsgVpgMutuallyExclusiveWithQos)
	}

	// Early pool QosType validation - fail fast before orchestrator processing
	// Auto pools reject throughputMibps, iops, and VPG (regardless of feature flag)
	if pool != nil && pool.QosType == utils.QosTypeAuto {
		if hasThroughput {
			return nil, errors.NewUserInputValidationErr(utils.ErrMsgPoolAutoQosTypeCannotSpecifyThroughput)
		}
		if hasIops {
			return nil, errors.NewUserInputValidationErr(utils.ErrMsgPoolAutoQosTypeCannotSpecifyIops)
		}
		if hasVpg {
			return nil, errors.NewUserInputValidationErr(utils.ErrMsgPoolAutoQosTypeCannotSpecifyVpgId)
		}
	}

	// Manual pools require either throughputMibps or volumePerformanceGroupId when MQOS is enabled
	if pool != nil && pool.QosType == utils.QosTypeManual && enableMqos && !hasThroughput && !hasVpg {
		return nil, errors.NewUserInputValidationErr(utils.ErrMsgPoolManualQosTypeRequiresThroughputOrVpg)
	}

	// When inferred IOPS is enabled, the server calculates IOPS from throughput -- reject user-provided IOPS
	if hasIops && enableInferredIops {
		return nil, errors.NewUserInputValidationErr("iops must not be set when IOPS inference is enabled. Only throughputMibps is needed.")
	}

	// When inferred IOPS is disabled, IOPS must be provided explicitly alongside throughput
	if enableMqos && !enableInferredIops && hasThroughput && !hasIops {
		return nil, errors.NewUserInputValidationErr("IOPS inference is disabled. IOPS must be provided explicitly when throughputMibps is specified.")
	}

	// Extract QoS parameters
	if hasThroughput {
		throughputValue := int64(math.Floor(req.Volume.ThroughputMibps.Value))
		param.ThroughputMibps = &throughputValue
	}

	if hasIops && !enableInferredIops {
		iopsValue := req.Volume.Iops.Value
		param.Iops = &iopsValue
	}

	if hasVpg {
		vpgId := req.Volume.VolumePerformanceGroupId.Value
		param.VolumePerformanceGroupID = &vpgId
	}

	smbProtocolRequested := hasSMBProtocol(req.Volume.GetProtocols())

	for _, protocol := range req.Volume.GetProtocols() {
		protocolStr, err := protocol.MarshalText()
		if err != nil {
			return nil, err
		}
		protocolName := string(protocolStr)
		if smbProtocolRequested && utils.IsSMBProtocol(protocolName) {
			if !enableSmb {
				return nil, errors.NewUserInputValidationErr("SMB protocol is currently not enabled.")
			}
			if pool == nil || pool.ActiveDirectoryConfigId == "" {
				return nil, errors.NewUserInputValidationErr("SMB protocol requires the pool to be joined to an Active Directory.")
			}
		}
		param.Protocols = append(param.Protocols, protocolName)
	}

	if req.Volume.RestrictedActions != nil {
		err := setRestrictedActionsFromRequest(req.Volume.RestrictedActions, &param.RestrictedActions, param.Protocols)
		if err != nil {
			return nil, err
		}
	}

	if len(param.Protocols) == 1 && utils.IsSMBProtocols(param.Protocols) {
		if req.Volume.ExportPolicy.IsSet() && len(req.Volume.ExportPolicy.Value.GetRules()) > 0 {
			return nil, errors.NewUserInputValidationErr("Cannot specify export policy rules for non-NFS volume")
		}
	}

	if pool != nil && pool.PoolAttributes != nil && !pool.PoolAttributes.LdapEnabled {
		if len(param.Protocols) == 2 && utils.IsSMBProtocols(param.Protocols) && utils.IsNFSProtocols(param.Protocols) {
			return nil, errors.NewUserInputValidationErr("Cannot create dual protocol volume in LDAP disabled pool")
		}
	}

	if req.Volume.TieringPolicy.IsSet() {
		if !autoTieringEnabled {
			return nil, errors.NewUserInputValidationErr("Auto-Tiering feature is currently not enabled.")
		}
		isBlockVolume := len(param.Protocols) > 0 && utils.IsSanProtocols(param.Protocols)
		if isBlockVolume && req.Volume.TieringPolicy.Value.HotTierBypassModeEnabled.IsSet() && req.Volume.TieringPolicy.Value.HotTierBypassModeEnabled.Value {
			return nil, errors.NewUserInputValidationErr("hotTierBypassMode is not supported for block volume")
		}

		if !req.Volume.TieringPolicy.Value.TierAction.IsSet() {
			return nil, errors.NewUserInputValidationErr("Tiering action is required when enabling auto-tiering on volume")
		}

		param.AutoTieringPolicy = &common.AutoTieringPolicy{}

		param.AutoTieringPolicy.CoolingThresholdDays = 0
		if req.Volume.TieringPolicy.Value.CoolingThresholdDays.IsSet() {
			param.AutoTieringPolicy.CoolingThresholdDays = req.Volume.TieringPolicy.Value.CoolingThresholdDays.Value
		}

		switch req.Volume.TieringPolicy.Value.TierAction.Value {
		case gcpgenserver.TieringPolicyV1betaTierActionENABLED:
			param.AutoTieringPolicy.AutoTieringEnabled = true
			param.AutoTieringPolicy.TieringPolicy = utils.FetchTieringPolicyAsPerVolumeType(!isBlockVolume)
			param.AutoTieringPolicy.RetrievalPolicy = ontapmodels.VolumeCloudRetrievalPolicyDefault
			param.AutoTieringPolicy.CoolingThresholdDays = req.Volume.TieringPolicy.Value.CoolingThresholdDays.Value
		case gcpgenserver.TieringPolicyV1betaTierActionPAUSED:
			param.AutoTieringPolicy.AutoTieringEnabled = false
			param.AutoTieringPolicy.TieringPolicy = ontapmodels.VolumeInlineTieringPolicyNone
			if !isBlockVolume {
				param.AutoTieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(false)
			}
		}

		// Process HotTierBypassModeEnabled if provided. Supported only for file volume.
		if req.Volume.TieringPolicy.Value.HotTierBypassModeEnabled.IsSet() && !isBlockVolume {
			param.AutoTieringPolicy.HotTierBypassModeEnabled = req.Volume.TieringPolicy.Value.HotTierBypassModeEnabled.Value
			if param.AutoTieringPolicy.HotTierBypassModeEnabled {
				param.AutoTieringPolicy.TieringPolicy = ontapmodels.VolumeInlineTieringPolicyAll
				param.AutoTieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(true)
			}
		}
	}

	if len(param.Protocols) > 0 && !utils.IsSANProtocol(param.Protocols[0]) {
		err := validateKerberosPolicyV1beta(req.Volume.Protocols, &req.Volume.KerberosEnabled.Value, req.Volume.ExportPolicy, pool)
		if err != nil {
			return nil, errors.NewUserInputValidationErr(err.Error())
		}
		param.FileProperties = &models.FileProperties{
			ExportPolicy: &models.ExportPolicy{
				ExportPolicyName: req.Volume.CreationToken.Value,
			},
		}
		if req.Volume.SecurityStyle.Set {
			param.FileProperties.SecurityStyle = string(req.Volume.SecurityStyle.Value)
		}
		if req.Volume.SmbSettings != nil {
			if slices.Contains(req.Volume.SmbSettings, gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE) {
				if !smbCaShareEnabled {
					return nil, errors.NewUserInputValidationErr("SMB continuously_available share feature is not enabled")
				}
				if param.FileProperties.SecurityStyle != string(gcpgenserver.VolumeV1betaSecurityStyleNTFS) {
					return nil, errors.NewUserInputValidationErr("Security style must be ntfs when specifying continuously_available share property for a SMB volume")
				}
			}
			if slices.Contains(req.Volume.SmbSettings, gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION) &&
				param.FileProperties.SecurityStyle == string(gcpgenserver.VolumeV1betaSecurityStyleUNIX) {
				return nil, errors.NewUserInputValidationErr("Security style must be ntfs when specifying access_based_enumeration share property for a SMB volume")
			}
			err := validateSmbShareSettingsV2(req.Volume.SmbSettings)
			if err != nil {
				return nil, err
			}
			param.FileProperties.SMBShareSettings = getSMBShareSettings(req.Volume.SmbSettings)
		}
		if req.Volume.SecurityStyle.Set {
			param.FileProperties.SecurityStyle = string(req.Volume.SecurityStyle.Value)
		}
		if req.Volume.UnixPermissions.IsSet() && unixPermissionsEnabled {
			param.FileProperties.UnixPermissions = req.Volume.UnixPermissions.Value
		}
	}
	if req.Volume.ExportPolicy.IsSet() {
		var exportRules []*models.ExportRule
		if utils.IsAllSquashEnabled {
			err := validateAllSquash(req.Volume.ExportPolicy.Value.GetRules())
			if err != nil {
				return nil, err
			}
			for index, rule := range req.Volume.ExportPolicy.Value.GetRules() {
				accessType, err := rule.AccessType.MarshalText()
				if err != nil {
					continue
				}
				exportRule := &models.ExportRule{
					AllowedClients:      rule.GetAllowedClients(),
					AccessType:          string(accessType),
					NFSv3:               rule.Nfsv3.Value,
					NFSv4:               rule.Nfsv4.Value,
					Index:               index + 1, // adding 1 as 0 index is not supported by ontap
					Kerberos5ReadOnly:   rule.Kerberos5ReadOnly.Value,
					Kerberos5ReadWrite:  rule.Kerberos5ReadWrite.Value,
					Kerberos5iReadOnly:  rule.Kerberos5iReadOnly.Value,
					Kerberos5iReadWrite: rule.Kerberos5iReadWrite.Value,
					Kerberos5pReadOnly:  rule.Kerberos5pReadOnly.Value,
					Kerberos5pReadWrite: rule.Kerberos5pReadWrite.Value,
				}
				if rule.HasRootAccess.IsSet() {
					if val, ok := rule.HasRootAccess.Get(); ok {
						exportRule.Superuser = val == gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue ||
							val == gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessOn
					}
				}
				if rule.AllSquash.IsSet() {
					exportRule.AllSquash = nillable.ToPointer(rule.AllSquash.Value)
				}
				if rule.AllSquash.IsSet() && rule.AllSquash.Value {
					exportRule.UnixReadOnly = false
					exportRule.UnixReadWrite = true
					exportRule.Superuser = false
					if rule.AnonUid.IsSet() {
						exportRule.AnonUid = nillable.ToPointer(rule.AnonUid.Value)
					}
				} else {
					switch rule.AccessType {
					case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY:
						exportRule.UnixReadOnly = true
					case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
						gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeACCESSTYPEUNSPECIFIED:
						exportRule.UnixReadWrite = true
					case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE:
						// No Unix access flags set; convertStorageExportPolicyRuleToONTAP emits never/never.
					}
				}
				exportRules = append(exportRules, exportRule)
			}
		} else {
			// Fallback to old model if no new fields are set
			for index, rule := range req.Volume.ExportPolicy.Value.GetRules() {
				accessType, err := rule.AccessType.MarshalText()
				if err != nil {
					continue
				}
				exportRule := &models.ExportRule{
					AllowedClients:      rule.GetAllowedClients(),
					AccessType:          string(accessType),
					NFSv3:               rule.Nfsv3.Value,
					NFSv4:               rule.Nfsv4.Value,
					Index:               index + 1, // adding 1 as 0 index is not supported by ontap
					Kerberos5ReadOnly:   rule.Kerberos5ReadOnly.Value,
					Kerberos5ReadWrite:  rule.Kerberos5ReadWrite.Value,
					Kerberos5iReadOnly:  rule.Kerberos5iReadOnly.Value,
					Kerberos5iReadWrite: rule.Kerberos5iReadWrite.Value,
					Kerberos5pReadOnly:  rule.Kerberos5pReadOnly.Value,
					Kerberos5pReadWrite: rule.Kerberos5pReadWrite.Value,
				}
				if rule.HasRootAccess.IsSet() {
					if val, ok := rule.HasRootAccess.Get(); ok {
						exportRule.Superuser = val == gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue ||
							val == gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessOn
					}
				}
				switch rule.AccessType {
				case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY:
					exportRule.UnixReadOnly = true
				case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeACCESSTYPEUNSPECIFIED:
					exportRule.UnixReadWrite = true
				case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE:
					// No Unix access flags set; convertStorageExportPolicyRuleToONTAP emits never/never.
				}
				exportRules = append(exportRules, exportRule)
			}
		}
		if len(exportRules) > exportRulesLimit {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("Number of export rules cannot exceed %d", exportRulesLimit))
		}
		param.FileProperties.ExportPolicy.ExportRules = exportRules
	}
	if len(req.Volume.BlockDevices) > 1 {
		return nil, errors.NewUserInputValidationErr("Only one BlockDevice can be specified for create")
	}
	if len(req.Volume.BlockDevices) > 0 {
		blockDevice := req.Volume.BlockDevices[0]

		if blockDevice.OsType.IsSet() {
			osType := blockDevice.GetOsType()
			blockDeviceName := "lun_" + param.Name
			if blockDevice.Name.IsSet() && blockDevice.Name.Value != "" {
				blockDeviceName = blockDevice.Name.Value
			}
			param.BlockDevices = &[]common.BlockDevice{
				{
					Name:       blockDeviceName,
					OSType:     string(osType.Value),
					HostGroups: DeduplicateSlice(blockDevice.GetHostGroups()),
				},
			}
		} else {
			return nil, errors.NewUserInputValidationErr("BlockDevice OS type is required")
		}
	} else if req.Volume.BlockProperties.IsSet() {
		reqBlockProperties, _ := req.Volume.BlockProperties.Get()
		if reqBlockProperties.OsType.IsSet() {
			osType := reqBlockProperties.GetOsType()
			param.BlockProperties = &common.BlockPropertiesRequest{
				OSType:         string(osType.Value),
				HostGroupUUIDs: DeduplicateSlice(reqBlockProperties.GetHostGroupIds()),
			}
		}
	}
	if req.Volume.BackupConfig.IsSet() {
		if !backupEnabled {
			return nil, errors.NewUserInputValidationErr("Backup feature is currently not enabled.")
		}
		param.DataProtection = &models.DataProtection{}
		reqBackupConfig, _ := req.Volume.BackupConfig.Get()
		if reqBackupConfig.BackupVaultId.IsSet() {
			param.DataProtection.BackupVaultID = reqBackupConfig.BackupVaultId.Value
		}
		if reqBackupConfig.BackupPolicyId.IsSet() {
			param.DataProtection.BackupPolicyId = reqBackupConfig.BackupPolicyId.Value
		}
		if reqBackupConfig.ScheduledBackupEnabled.IsSet() {
			param.DataProtection.ScheduledBackupEnabled = &reqBackupConfig.ScheduledBackupEnabled.Value
		}
		if reqBackupConfig.KmsGrant.IsSet() {
			if !cmekBackupEnabled {
				return nil, errors.NewUserInputValidationErr("CMEK backup is not enabled")
			}
			param.DataProtection.KmsGrant = &reqBackupConfig.KmsGrant.Value
		}
	}

	if req.Volume.SnapshotPolicy.IsSet() {
		snapShotPolicy, err := convertFromSnapshotPolicyV2(&req.Volume.SnapshotPolicy.Value)
		if err != nil {
			return nil, err
		}
		if snapShotPolicy != nil {
			if snapShotPolicy.Schedules == nil || (snapShotPolicy.Schedules != nil && len(snapShotPolicy.Schedules) == 0) {
				err = errors.NewUserInputValidationErr("SnapshotPolicy parameter must have at least one schedule populated")
				return nil, err
			}
		}
		param.SnapshotPolicy = snapShotPolicy
	}

	if req.SnapshotId.IsSet() {
		param.SnapshotID = req.SnapshotId.Value
		if req.Volume.LargeVolumeConstituentCount.IsSet() {
			return nil, errors.NewUserInputValidationErr("LargeVolumeConstituentCount cannot be set when SnapshotId is provided")
		}
	}

	if req.Volume.SnapReserve.IsSet() {
		snapReserve, ok := req.Volume.SnapReserve.Get()
		if !ok {
			return nil, errors.NewUserInputValidationErr("SnapReserve must be a valid number")
		}
		if snapReserve < 0 {
			return nil, errors.NewUserInputValidationErr("SnapReserve cannot be negative")
		}
		if snapReserve > 90 { // ONTAP allows a maximum of 90% for snapshot reserve during creation
			return nil, errors.NewUserInputValidationErr("Maximum allowed snapshot-reserve-percentage value during create is 90.Use volume update to set it to a higher value after the volume has been created.")
		}
		param.SnapReserve = int64(snapReserve)
	}

	var replicationSchedule, description, clusterLocation string
	var labels map[string]string
	if req.HybridReplicationParameters.IsSet() {
		hybridReplicationParameters, _ := req.HybridReplicationParameters.Get()

		if hybridReplicationParameters.ReplicationSchedule.IsSet() {
			replicationSchedule = string(replication.MapCCFERescheduleToInternalReplicationSchedule(gcpgenserver.ReplicationV1betaReplicationSchedule(hybridReplicationParameters.ReplicationSchedule.Value)))
		}
		replicationType := datamodel.HybridReplicationParametersReplicationType(hybridReplicationParameters.HybridReplicationType)
		// return error in case replication schedule not set from swagger
		if replicationType == datamodel.HybridReplicationParametersReplicationTypeONPREM {
			if nillable.IsNilOrEmpty(&replicationSchedule) {
				msg := "Can't have empty replicationSchedule for " + string(replicationType)
				return nil, errors.NewUserInputValidationErr(msg)
			}
		}
		if replicationType == datamodel.HybridReplicationParametersReplicationTypeMIGRATION {
			replicationSchedule = SnapshotScheduleLabelHourly
		}
		if hybridReplicationParameters.Labels.IsSet() {
			labels = hybridReplicationParameters.Labels.Value
		}
		if hybridReplicationParameters.Description.IsSet() {
			description = hybridReplicationParameters.Description.Value
		}

		if hybridReplicationParameters.ClusterLocation.IsSet() {
			clusterLocation = hybridReplicationParameters.ClusterLocation.Value
		}

		param.HybridReplicationParameters = &models.HybridReplicationParameters{
			ResourceID:          hybridReplicationParameters.ResourceId,
			ReplicationType:     replicationType,
			PeerVolumeName:      hybridReplicationParameters.PeerVolumeName,
			PeerClusterName:     hybridReplicationParameters.PeerClusterName,
			PeerSvmName:         hybridReplicationParameters.PeerSvmName,
			PeerIPAddresses:     hybridReplicationParameters.PeerIpAddresses,
			Labels:              labels,
			Description:         description,
			ClusterLocation:     clusterLocation,
			ReplicationSchedule: replicationSchedule,
		}
		if req.Volume.LargeCapacity.IsSet() && req.Volume.LargeCapacity.Value == true {
			if !hybridReplicationParameters.LargeVolumeConstituentCount.IsSet() {
				return nil, errors.NewUserInputValidationErr("LargeVolumeConstituentCount must be set for Large Volumes in hybrid replication parameters.")
			}
			param.LargeVolumeConstituentCount = hybridReplicationParameters.LargeVolumeConstituentCount.Value
		}
	}

	if req.Volume.LargeCapacity.IsSet() {
		param.LargeCapacity, _ = req.Volume.LargeCapacity.Get()
	}

	if req.Volume.LargeVolumeConstituentCount.IsSet() {
		param.LargeVolumeConstituentCount = req.Volume.LargeVolumeConstituentCount.Value
	}

	if unixPermissionsEnabled && req.Volume.UnixPermissions.IsSet() {
		unixPermissions := req.Volume.UnixPermissions.Value
		securityStyle := utils.ResolveEffectiveUnixSecurityStyle(
			param.Protocols,
			string(req.Volume.SecurityStyle.Value),
			req.Volume.SecurityStyle.Set,
			unixPermissions,
		)
		if securityStyle == utils.UnixSecurityStyle && !req.Volume.SecurityStyle.Set && param.FileProperties != nil {
			param.FileProperties.SecurityStyle = string(gcpgenserver.VolumeV1betaSecurityStyleUNIX)
		}
		err := validateUnixPermissions(param.Protocols, unixPermissions, securityStyle)
		if err != nil {
			return nil, err
		}
		param.FileProperties.UnixPermissions = unixPermissions
	}
	return param, nil
}

func validateUnixPermissions(protocolTypes []string, unixPermissions string, securityStyle string) error {
	if unixPermissions == "" {
		return errors.NewUserInputValidationErr("UnixPermissions cannot be empty.")
	}
	if utils.IsNFSProtocols(protocolTypes) {
		result, _ := regexp.MatchString(unixPermissionsRegexPattern, unixPermissions)
		if !result {
			return errors.NewUserInputValidationErr("Unix permissions should be 4 digit long in octal format(0 to 7),  Create/Update volume with valid unix permissions, example: 0755")
		}
		effectiveStyle := strings.ToLower(utils.ResolveEffectiveUnixSecurityStyle(protocolTypes, securityStyle, false, unixPermissions))
		if effectiveStyle != utils.UnixSecurityStyle {
			return errors.NewUserInputValidationErr("Unix permissions are only supported with unix security-style")
		}
	} else {
		return errors.NewUserInputValidationErr("Unix permissions is only supported with NFS protocol volumes, Create/Update SMB volume without unix permissions.")
	}
	return nil
}

func validateAllSquash(rules []gcpgenserver.SimpleExportPolicyRuleV1beta) error {
	hasAllSquashRule := false
	for _, rule := range rules {
		if rule.AllSquash.IsSet() && rule.AllSquash.Value {
			if hasAllSquashRule {
				return errors.NewUserInputValidationErr("only one AllSquash rule is allowed per export policy")
			}
			if _, ok := rule.AnonUid.Get(); !ok {
				return errors.NewUserInputValidationErr("AnonUid must be set when AllSquash is enabled")
			}
			if rule.AccessType != gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE {
				return errors.NewUserInputValidationErr("AccessType must be READ_WRITE when AllSquash is enabled")
			}
			hasAllSquashRule = true
			if rule.HasRootAccess.IsSet() {
				if val, ok := rule.HasRootAccess.Get(); ok {
					if val == gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue ||
						val == gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessOn {
						return errors.NewUserInputValidationErr(
							"RootSquash cannot be enabled when AllSquash is true for the same rule")
					}
				}
			}

			if rule.Kerberos5ReadWrite.Value || rule.Kerberos5ReadOnly.Value ||
				rule.Kerberos5iReadOnly.Value || rule.Kerberos5iReadWrite.Value ||
				rule.Kerberos5pReadOnly.Value || rule.Kerberos5pReadWrite.Value {
				return errors.NewUserInputValidationErr(
					"AllSquash cannot be enabled for Kerberos-enabled export rules")
			}
		}
	}

	return nil
}

func _prepareRevertVolumeParams(req *gcpgenserver.VolumeRevertV1beta, params gcpgenserver.V1betaRevertVolumeParams, region string) (*common.RevertVolumeParams, error) {
	if params.VolumeId == "" {
		return nil, errors.NewUserInputValidationErr("No Volume ID given")
	}
	if params.ProjectNumber == "" {
		return nil, errors.NewUserInputValidationErr("No Project Number given")
	}
	if req.SnapshotId == "" {
		return nil, errors.NewUserInputValidationErr("No Snapshot ID given")
	}
	param := &common.RevertVolumeParams{
		AccountName: params.ProjectNumber,
		Region:      region,
		VolumeID:    params.VolumeId,
		SnapshotID:  req.SnapshotId,
	}

	return param, nil
}

func (h Handler) V1betaUpdateVolume(ctx context.Context, req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams) (gcpgenserver.V1betaUpdateVolumeRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	region, _, parsingErr := utils.ParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaUpdateVolumeBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	volume, err := h.Orchestrator.GetVolume(ctx, params.VolumeId, false)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateVolumeNotFound{
				Code:    404,
				Message: "Volume not found",
			}, nil
		}
		logger.Error("Failed to get volume before update", "error", err.Error())
		return &gcpgenserver.V1betaUpdateVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	pool, err := h.Orchestrator.DescribePool(ctx, volume.PoolID, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to fetch corresponding pool to the volume.", "error", err.Error())
		return &gcpgenserver.V1betaUpdateVolumeInternalServerError{Code: 500, Message: "Internal server error"}, nil
	}
	if pool != nil && pool.PoolAttributes != nil &&
		(pool.PoolAttributes.ZoneSwitchState == datamodel.ZoneSwitching || pool.PoolAttributes.ZoneSwitchState == datamodel.ZoneSwitched) {
		return &gcpgenserver.V1betaUpdateVolumeBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Volume update is not supported when the pool is switching/switched to a different primary zone.",
		}, nil
	}

	param, err := prepareUpdateVolumeParams(req, params, region, volume)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to update volume", "error", err.Error())
		return &gcpgenserver.V1betaUpdateVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	if param.CacheParameters != nil && !flexCacheEnabled {
		return &gcpgenserver.V1betaUpdateVolumeBadRequest{
			Code:    403,
			Message: "FlexCache feature is currently not enabled.",
		}, nil
	}

	volume, jobUUID, err := h.Orchestrator.UpdateVolumeV2(ctx, param)
	if err != nil {
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaUpdateVolumeConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		} else if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to update volume", "error", err.Error())
		return &gcpgenserver.V1betaUpdateVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	resp, err := encodeVolumeV1(convertModelToVCPVolume(volume))
	if err != nil {
		return nil, err
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + jobUUID
	if volume.LifeCycleState == datamodel.LifeCycleStateUpdating {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

func _prepareUpdateVolumeParams(req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaUpdateVolumeParams, region string, dbVolume *models.Volume) (*common.UpdateVolumeParams, error) {
	var backupSchedule string
	if qaEnabled && params.XNetappBackupSchedule.IsSet() {
		backupSchedule = params.XNetappBackupSchedule.Value
		// Validate the backup schedule cron expression
		if err := validateBackupScheduleCron(backupSchedule); err != nil {
			return nil, err
		}
	}

	param := &common.UpdateVolumeParams{
		AccountName:    params.ProjectNumber,
		Region:         region,
		PoolID:         req.PoolId.Value,
		VolumeId:       params.VolumeId,
		BackupSchedule: backupSchedule,
		CorrelationID:  params.XCorrelationID.Value,
	}

	if req.LargeCapacity.IsSet() {
		param.LargeCapacity = nillable.GetBoolPtr(req.LargeCapacity.Or(false))
	}

	if req.LargeVolumeConstituentCount.IsSet() {
		param.LargeVolumeConstituentCount = nillable.GetInt32Ptr(req.LargeVolumeConstituentCount.Value)
	}

	if req.Description.IsSet() {
		param.Description, _ = req.Description.Get()
	}

	if req.QuotaInBytes.IsSet() {
		quota, _ := req.QuotaInBytes.Get()
		param.QuotaInBytes = int64(quota)
	}

	for _, protocol := range req.GetProtocols() {
		protocolStr, err := protocol.MarshalText()
		if err != nil {
			return nil, err
		}
		param.Protocols = append(param.Protocols, string(protocolStr))
	}

	if len(req.BlockDevices) > 1 {
		return nil, errors.NewUserInputValidationErr("Only one BlockDevice can be specified for update")
	}
	// Check BlockDevices first, then fallback to BlockProperties
	if len(req.BlockDevices) > 0 {
		blockDevices := make([]*common.BlockDevice, 0, len(req.BlockDevices))
		for _, blockDevice := range req.BlockDevices {
			blockDeviceReq := &common.BlockDevice{}

			if blockDevice.Name.IsSet() {
				blockDeviceReq.Name, _ = blockDevice.Name.Get()
			} else {
				return nil, errors.NewUserInputValidationErr("BlockDevice name is required")
			}

			if blockDevice.OsType.IsSet() {
				osType, _ := blockDevice.OsType.Get()
				blockDeviceReq.OSType = (string)(osType)
			}

			if len(blockDevice.HostGroups) > 0 {
				blockDeviceReq.HostGroups = DeduplicateSlice(blockDevice.HostGroups)
			}

			blockDevices = append(blockDevices, blockDeviceReq)
		}

		param.BlockDevices = blockDevices
	} else if req.BlockProperties.IsSet() {
		// Fallback: Use BlockProperties if BlockDevices are not provided
		reqBlockProperties, _ := req.BlockProperties.Get()
		param.BlockProperties = &common.BlockPropertiesRequest{
			HostGroupUUIDs: DeduplicateSlice(reqBlockProperties.HostGroupIds),
		}
		if reqBlockProperties.OsType.IsSet() {
			osType := reqBlockProperties.GetOsType().Value
			param.BlockProperties.OSType = string(osType)
		}
	}

	if req.TieringPolicy.IsSet() {
		if !autoTieringEnabled {
			return nil, errors.NewUserInputValidationErr("Auto-Tiering feature is currently not enabled.")
		}

		isBlockVolume := dbVolume != nil && len(dbVolume.ProtocolTypes) > 0 && utils.IsSanProtocols(dbVolume.ProtocolTypes)
		if isBlockVolume && req.TieringPolicy.Value.HotTierBypassModeEnabled.IsSet() && req.TieringPolicy.Value.HotTierBypassModeEnabled.Value {
			return nil, errors.NewUserInputValidationErr("hotTierBypassMode is not supported for block volume")
		}
		param.AutoTieringPolicy = &common.AutoTieringPolicy{}

		// Set the default cooling threshold from DB if available
		if dbVolume != nil && dbVolume.AutoTieringPolicy != nil {
			param.AutoTieringPolicy.CoolingThresholdDays = dbVolume.AutoTieringPolicy.CoolingThresholdDays
		}
		if req.TieringPolicy.Value.CoolingThresholdDays.IsSet() {
			param.AutoTieringPolicy.CoolingThresholdDays = req.TieringPolicy.Value.CoolingThresholdDays.Value
		}

		if req.TieringPolicy.Value.CoolingThresholdDays.IsSet() &&
			!req.TieringPolicy.Value.HotTierBypassModeEnabled.IsSet() &&
			!req.TieringPolicy.Value.TierAction.IsSet() &&
			dbVolume != nil && dbVolume.AutoTieringPolicy != nil && dbVolume.AutoTieringPolicy.HotTierBypassModeEnabled {
			return nil, errors.NewUserInputValidationErr("Cooling threshold days cannot be updated when Hot Tier Bypass Mode is enabled.")
		}

		if req.TieringPolicy.Value.TierAction.IsSet() {
			switch req.TieringPolicy.Value.TierAction.Value {
			case gcpgenserver.TieringPolicyV1betaTierActionENABLED:
				param.AutoTieringPolicy.AutoTieringEnabled = true
				param.AutoTieringPolicy.TieringPolicy = utils.FetchTieringPolicyAsPerVolumeType(!isBlockVolume)
				param.AutoTieringPolicy.RetrievalPolicy = ontapmodels.VolumeCloudRetrievalPolicyDefault
			case gcpgenserver.TieringPolicyV1betaTierActionPAUSED:
				if req.TieringPolicy.Value.HotTierBypassModeEnabled.IsSet() && req.TieringPolicy.Value.HotTierBypassModeEnabled.Value {
					return nil, errors.NewUserInputValidationErr("hotTierBypassMode can not be enabled along with pausing tiering on volume")
				} else if !req.TieringPolicy.Value.HotTierBypassModeEnabled.IsSet() && dbVolume != nil && dbVolume.AutoTieringPolicy != nil && dbVolume.AutoTieringPolicy.HotTierBypassModeEnabled {
					return nil, errors.NewUserInputValidationErr("existing volume has hotTierBypassMode enabled, cannot pause tiering. To pause, hotTierBypassMode needs to be disabled.")
				}
				param.AutoTieringPolicy.AutoTieringEnabled = false
				param.AutoTieringPolicy.TieringPolicy = ontapmodels.VolumeInlineTieringPolicyNone
			}
			if !isBlockVolume {
				param.AutoTieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(false)
			}
		} else {
			// If tiering action is not present in request, check existing in db & fill.
			if dbVolume == nil || dbVolume.AutoTieringPolicy == nil || dbVolume.AutoTieringPolicy.TieringPolicy == "" {
				return nil, errors.NewUserInputValidationErr("Tiering action is required when enabling auto-tiering on volume for the first time")
			}
			param.AutoTieringPolicy.TieringPolicy = dbVolume.AutoTieringPolicy.TieringPolicy
			param.AutoTieringPolicy.AutoTieringEnabled = dbVolume.AutoTieringPolicy.AutoTieringEnabled
			if param.AutoTieringPolicy.TieringPolicy == utils.FetchTieringPolicyAsPerVolumeType(!isBlockVolume) {
				param.AutoTieringPolicy.RetrievalPolicy = ontapmodels.VolumeCloudRetrievalPolicyDefault
			}
			param.AutoTieringPolicy.CloudWriteModeEnabled = dbVolume.AutoTieringPolicy.CloudWriteModeEnabled
		}

		// Process HotTierBypassModeEnabled if provided. Supported only for file volume.
		if req.TieringPolicy.Value.HotTierBypassModeEnabled.IsSet() && !isBlockVolume {
			param.AutoTieringPolicy.HotTierBypassModeEnabled = req.TieringPolicy.Value.HotTierBypassModeEnabled.Value
			if param.AutoTieringPolicy.HotTierBypassModeEnabled {
				param.AutoTieringPolicy.TieringPolicy = ontapmodels.VolumeInlineTieringPolicyAll
				param.AutoTieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(true)
				// Only disable HotTierBypassModeEnabled, if it was previously enabled and no tiering policy has come as part of the request body.
			} else if dbVolume != nil && dbVolume.AutoTieringPolicy != nil && dbVolume.AutoTieringPolicy.HotTierBypassModeEnabled && !req.TieringPolicy.Value.TierAction.IsSet() {
				param.AutoTieringPolicy.TieringPolicy = ontapmodels.VolumeInlineTieringPolicyAuto
				param.AutoTieringPolicy.AutoTieringEnabled = dbVolume.AutoTieringPolicy.AutoTieringEnabled
				param.AutoTieringPolicy.RetrievalPolicy = ontapmodels.VolumeCloudRetrievalPolicyDefault
				param.AutoTieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(false)
			}
		} else if !isBlockVolume && dbVolume != nil && dbVolume.AutoTieringPolicy != nil {
			// Preserve existing HotTierBypassModeEnabled when it's not explicitly set in the request
			param.AutoTieringPolicy.HotTierBypassModeEnabled = dbVolume.AutoTieringPolicy.HotTierBypassModeEnabled
			// If HT bypass is enabled, ensure consistency with TieringPolicy and CloudWriteModeEnabled
			if param.AutoTieringPolicy.HotTierBypassModeEnabled {
				param.AutoTieringPolicy.TieringPolicy = ontapmodels.VolumeInlineTieringPolicyAll
				param.AutoTieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(true)
			}
		}
	}

	if req.BackupConfig.IsSet() {
		if !backupEnabled {
			return nil, errors.NewUserInputValidationErr("Backup feature is currently not enabled.")
		}
		param.DataProtection = &models.UpdateDataProtection{}
		reqBackupConfig, _ := req.BackupConfig.Get()
		if reqBackupConfig.BackupVaultId.IsSet() {
			param.DataProtection.BackupVaultID = &reqBackupConfig.BackupVaultId.Value
		}
		if reqBackupConfig.BackupPolicyId.IsSet() {
			param.DataProtection.BackupPolicyId = &reqBackupConfig.BackupPolicyId.Value
		}
		if reqBackupConfig.ScheduledBackupEnabled.IsSet() {
			param.DataProtection.ScheduledBackupEnabled = &reqBackupConfig.ScheduledBackupEnabled.Value
		}
		if reqBackupConfig.KmsGrant.IsSet() {
			if !cmekBackupEnabled {
				return nil, errors.NewUserInputValidationErr("CMEK backup is not enabled")
			}
			param.DataProtection.KmsGrant = &reqBackupConfig.KmsGrant.Value
		}
	}

	if req.Labels.IsSet() {
		jsonbLabels, err := validateLabels(req.Labels.Value)
		if err != nil {
			return nil, errors.NewUserInputValidationErr(err.Error())
		}
		param.Labels = jsonbLabels
	}

	if req.SnapshotPolicy.IsSet() {
		snapShotPolicy, err := convertFromSnapshotPolicyV2(&req.SnapshotPolicy.Value)
		if err != nil {
			return nil, err
		}
		param.SnapshotPolicy = snapShotPolicy
	}

	if req.SnapReserve.IsSet() {
		snapReserve := int64(req.SnapReserve.Value)
		if snapReserve >= 100 {
			return nil, errors.NewUserInputValidationErr("SnapReserve should be less than 100")
		}
		param.SnapReserve = nillable.ToPointer(snapReserve)
	} else {
		param.SnapReserve = nil
	}

	if req.CreationToken.IsSet() {
		if param.FileProperties == nil {
			param.FileProperties = &models.FileProperties{}
		}
		param.FileProperties.JunctionPath = "/" + req.CreationToken.Value
	}

	if req.ExportPolicy.IsSet() {
		exportPolicy := req.ExportPolicy.Value

		if dbVolume != nil && len(dbVolume.ProtocolTypes) == 1 && utils.IsSMBProtocols(dbVolume.ProtocolTypes) {
			if len(exportPolicy.GetRules()) > 0 {
				return nil, errors.NewUserInputValidationErr("Cannot specify export policy rules for non-NFS volume")
			}
		}

		if param.FileProperties == nil {
			param.FileProperties = &models.FileProperties{}
		}
		if param.FileProperties.ExportPolicy == nil {
			param.FileProperties.ExportPolicy = &models.ExportPolicy{}
		}

		// Validate allSquash rules if the feature is enabled
		if utils.IsAllSquashEnabled {
			err := validateAllSquash(exportPolicy.GetRules())
			if err != nil {
				return nil, err
			}
		}

		// Deep copy export rules
		param.FileProperties.ExportPolicy.ExportRules = make([]*models.ExportRule, 0, len(exportPolicy.GetRules()))
		for _, rule := range exportPolicy.GetRules() {
			exportRule := &models.ExportRule{}
			exportRule.AllowedClients = rule.AllowedClients
			exportRule.AccessType = string(rule.AccessType)

			if rule.Nfsv3.IsSet() {
				exportRule.NFSv3 = rule.Nfsv3.Value
			}
			if rule.Nfsv4.IsSet() {
				exportRule.NFSv4 = rule.Nfsv4.Value
			}
			if rule.Kerberos5ReadOnly.IsSet() {
				exportRule.Kerberos5ReadOnly = rule.Kerberos5ReadOnly.Value
			}
			if rule.Kerberos5ReadWrite.IsSet() {
				exportRule.Kerberos5ReadWrite = rule.Kerberos5ReadWrite.Value
			}
			if rule.Kerberos5iReadOnly.IsSet() {
				exportRule.Kerberos5iReadOnly = rule.Kerberos5iReadOnly.Value
			}
			if rule.Kerberos5iReadWrite.IsSet() {
				exportRule.Kerberos5iReadWrite = rule.Kerberos5iReadWrite.Value
			}
			if rule.Kerberos5pReadOnly.IsSet() {
				exportRule.Kerberos5pReadOnly = rule.Kerberos5pReadOnly.Value
			}
			if rule.Kerberos5pReadWrite.IsSet() {
				exportRule.Kerberos5pReadWrite = rule.Kerberos5pReadWrite.Value
			}
			if rule.HasRootAccess.IsSet() {
				if val, ok := rule.HasRootAccess.Get(); ok {
					exportRule.Superuser = val == gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue ||
						val == gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessOn
				}
			}
			if utils.IsAllSquashEnabled {
				if rule.AllSquash.IsSet() {
					exportRule.AllSquash = nillable.ToPointer(rule.AllSquash.Value)
				}
				if rule.AnonUid.IsSet() {
					exportRule.AnonUid = nillable.ToPointer(rule.AnonUid.Value)
				}
			}
			if utils.IsAllSquashEnabled && rule.AllSquash.IsSet() && rule.AllSquash.Value {
				exportRule.UnixReadWrite = true
				exportRule.Superuser = false
			} else {
				switch rule.AccessType {
				case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADONLY:
					exportRule.UnixReadOnly = true
				case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADWRITE,
					gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeACCESSTYPEUNSPECIFIED:
					exportRule.UnixReadWrite = true
				case gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE:
					// No Unix access flags set; convertStorageExportPolicyRuleToONTAP emits never/never.
				}
			}
			param.FileProperties.ExportPolicy.ExportRules = append(param.FileProperties.ExportPolicy.ExportRules, exportRule)
		}
		if len(param.FileProperties.ExportPolicy.ExportRules) > exportRulesLimit {
			return nil, errors.NewUserInputValidationErr(fmt.Sprintf("Number of export rules cannot exceed %d", exportRulesLimit))
		}
	}
	if req.SnapshotDirectory.IsSet() {
		param.SnapshotDirectoryAccess = &req.SnapshotDirectory.Value
	}

	if req.CacheParameters.IsSet() {
		reqCacheProperties, _ := req.CacheParameters.Get()

		if err := validateFlexCacheUpdateParams(&reqCacheProperties, dbVolume); err != nil {
			return nil, err
		}

		// Handle CacheConfig if present
		if reqCacheProperties.CacheConfig.IsSet() {
			cacheConfig, _ := reqCacheProperties.CacheConfig.Get()
			if param.CacheParameters == nil {
				param.CacheParameters = &models.CacheParameters{}
			}
			param.CacheParameters.CacheConfig = &models.CacheConfig{}
			if reqCacheProperties.PeerVolumeName.IsSet() {
				param.CacheParameters.PeerVolumeName = reqCacheProperties.PeerVolumeName.Value
			}
			if reqCacheProperties.PeerClusterName.IsSet() {
				param.CacheParameters.PeerClusterName = reqCacheProperties.PeerClusterName.Value
			}
			if reqCacheProperties.PeerSvmName.IsSet() {
				param.CacheParameters.PeerSvmName = reqCacheProperties.PeerSvmName.Value
			}
			if reqCacheProperties.PeerIpAddresses != nil {
				param.CacheParameters.PeerIPAddresses = reqCacheProperties.PeerIpAddresses
			}

			if cacheConfig.WritebackEnabled.IsSet() {
				param.CacheParameters.CacheConfig.WritebackEnabled = &cacheConfig.WritebackEnabled.Value
			}
			if cacheConfig.AtimeScrubDays.IsSet() {
				param.CacheParameters.CacheConfig.AtimeScrubDays = &cacheConfig.AtimeScrubDays.Value
			}
			if cacheConfig.AtimeScrubEnabled.IsSet() {
				param.CacheParameters.CacheConfig.AtimeScrubEnabled = &cacheConfig.AtimeScrubEnabled.Value
			}
			if cacheConfig.CifsChangeNotifyEnabled.IsSet() {
				param.CacheParameters.CacheConfig.CifsChangeNotifyEnabled = &cacheConfig.CifsChangeNotifyEnabled.Value
			}

			// Handle PrePopulate if present
			if cacheConfig.CachePrePopulate.IsSet() {
				prePopulate, _ := cacheConfig.CachePrePopulate.Get()
				param.CacheParameters.CacheConfig.CachePrePopulate = &models.CachePrePopulate{}

				if prePopulate.PathList.IsSet() {
					param.CacheParameters.CacheConfig.CachePrePopulate.PathList = prePopulate.PathList.Value
				}
				if prePopulate.ExcludePathList.IsSet() {
					param.CacheParameters.CacheConfig.CachePrePopulate.ExcludePathList = prePopulate.ExcludePathList.Value
				}

				if prePopulate.Recursion.IsSet() {
					param.CacheParameters.CacheConfig.CachePrePopulate.Recursion = &prePopulate.Recursion.Value
				}
			}
		}
	}

	throughputMibps, hasThroughput := req.ThroughputMibps.Get()
	iops, hasIops := req.Iops.Get()
	hasVpg := req.VolumePerformanceGroupId.IsSet()

	if hasThroughput || hasIops || hasVpg {
		if !enableMqos {
			return nil, errors.NewUserInputValidationErr("MQOS is not enabled")
		}

		if hasVpg {
			if !enableVolumePerformanceGroupAssignment {
				return nil, errors.NewUserInputValidationErr(utils.ErrMsgVpgAssignmentNotEnabled)
			}
			if hasThroughput || hasIops {
				return nil, errors.NewUserInputValidationErr("Only (throughputMibps + iops), or volumePerformanceGroupId can be set at a time")
			}
			param.VolumePerformanceGroupId = nillable.ToPointer(req.VolumePerformanceGroupId.Value)
		} else {
			// Manual QoS without VPG in the request.
			// If the volume is currently VPG-assigned, this is a revert-to-individual operation and both
			// throughputMibps and iops are required (they have no existing individual values to merge into).
			// If the volume already carries individual QoS, partial deltas are fine — the orchestrator
			// merges with the existing values. Null is treated as omitted (not a delta instruction).
			currentlyVpg := dbVolume != nil && dbVolume.VolumePerformanceGroupId != ""
			if currentlyVpg && (!hasThroughput || !hasIops) {
				return nil, errors.NewUserInputValidationErr("throughputMibps and iops must both be set when reverting a VPG volume to individual QoS")
			}
			if hasThroughput {
				param.ThroughputMibps = nillable.ToPointer(int64(math.Floor(throughputMibps)))
			}
			if hasIops {
				if enableInferredIops {
					return nil, errors.NewUserInputValidationErr("iops must not be set when IOPS inference is enabled. Only throughputMibps is needed.")
				}
				param.Iops = nillable.ToPointer(iops)
			}
		}
	}

	if len(req.SmbSettings) > 0 {
		if dbVolume != nil && !utils.IsSMBProtocols(dbVolume.ProtocolTypes) {
			return nil, errors.NewUserInputValidationErr("Cannot change SMB share settings for NFS volume or Block Volume")
		}
		if slices.Contains(req.SmbSettings, gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION) && dbVolume != nil && dbVolume.FileProperties != nil &&
			(dbVolume.FileProperties.SecurityStyle == string(gcpgenserver.VolumeV1betaSecurityStyleUNIX)) {
			return nil, errors.NewUserInputValidationErr("Security style must be ntfs when specifying access based enumeration share property")
		}
		err := validateSmbShareSettingsV2(req.SmbSettings)
		if err != nil {
			return nil, err
		}
		err = validateSmbVolumeParams(req)
		if err != nil {
			return nil, err
		}
		param.SMBShareSettings = getSMBShareSettings(req.SmbSettings)
	}
	if req.UnixPermissions.IsSet() && unixPermissionsEnabled {
		var securityStyle string
		var protocols []string
		unixPermissions := req.UnixPermissions.Value
		if dbVolume != nil && dbVolume.FileProperties != nil {
			securityStyle = dbVolume.FileProperties.SecurityStyle
			protocols = dbVolume.ProtocolTypes
		}
		effectiveStyle := utils.ResolveEffectiveUnixSecurityStyle(protocols, securityStyle, securityStyle != "", unixPermissions)
		err := validateUnixPermissions(protocols, unixPermissions, effectiveStyle)
		if err != nil {
			return nil, err
		}
		if dbVolume.FileProperties != nil && dbVolume.FileProperties.UnixPermissions == unixPermissions {
			return nil, errors.NewUserInputValidationErr("Unix permissions are already set to the desired value")
		}
		param.FileProperties = &models.FileProperties{
			UnixPermissions: unixPermissions,
		}
		if effectiveStyle == utils.UnixSecurityStyle {
			param.FileProperties.SecurityStyle = utils.UnixSecurityStyle
		}
	}

	if req.RestrictedActions != nil {
		restrictedActions, err := parseRestrictedActionsFromRequest(req.RestrictedActions, dbVolume.ProtocolTypes)
		if err != nil {
			return nil, err
		}
		param.RestrictedActions = restrictedActions
	}

	return param, nil
}

func getSMBShareSettings(params gcpgenserver.SMBSettingsV1beta) []string {
	if params != nil {
		shareSettings := make([]string, 0)
		seenShareSettings := make(map[gcpgenserver.SMBSettingsV1betaItem]bool)
		for _, setting := range params {
			if !seenShareSettings[setting] {
				seenShareSettings[setting] = true
				shareSettings = append(shareSettings, convertToOntapShareSettingString(setting))
			}
		}
		return shareSettings
	}
	return nil
}

func convertToOntapShareSettingString(setting gcpgenserver.SMBSettingsV1betaItem) string {
	switch setting {
	case gcpgenserver.SMBSettingsV1betaItemNONBROWSABLE:
		return utils.CIFSSharePropertyNonBrowsable
	case gcpgenserver.SMBSettingsV1betaItemSMBSETTINGSUNSPECIFIED:
		return utils.CIFSShareSmbSettingsUnspecified
	case gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA:
		return utils.CIFSSharePropertyEncryptData
	case gcpgenserver.SMBSettingsV1betaItemCHANGENOTIFY:
		return utils.CIFSSharePropertyChangenotify
	case gcpgenserver.SMBSettingsV1betaItemBROWSABLE:
		return utils.CIFSSharePropertyBrowsable
	case gcpgenserver.SMBSettingsV1betaItemOPLOCKS:
		return utils.CIFSSharePropertyOplocks
	case gcpgenserver.SMBSettingsV1betaItemSHOWSNAPSHOT:
		return utils.CIFSSharePropertyShowsnapshot
	case gcpgenserver.SMBSettingsV1betaItemSHOWPREVIOUSVERSIONS:
		return utils.CIFSSharePropertyShowPreviousVersions
	case gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION:
		return utils.CIFSAccessBasedEnumeration
	case gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE:
		return utils.CIFSSharePropertyCA
	default:
		return utils.CIFSShareSmbSettingsUnspecified
	}
}

func convertFromOntapShareSettingString(setting string) gcpgenserver.SMBSettingsV1betaItem {
	switch setting {
	case utils.CIFSSharePropertyNonBrowsable:
		return gcpgenserver.SMBSettingsV1betaItemNONBROWSABLE
	case utils.CIFSShareSmbSettingsUnspecified:
		return gcpgenserver.SMBSettingsV1betaItemSMBSETTINGSUNSPECIFIED
	case utils.CIFSSharePropertyEncryptData:
		return gcpgenserver.SMBSettingsV1betaItemENCRYPTDATA
	case utils.CIFSSharePropertyChangenotify:
		return gcpgenserver.SMBSettingsV1betaItemCHANGENOTIFY
	case utils.CIFSSharePropertyBrowsable:
		return gcpgenserver.SMBSettingsV1betaItemBROWSABLE
	case utils.CIFSSharePropertyOplocks:
		return gcpgenserver.SMBSettingsV1betaItemOPLOCKS
	case utils.CIFSSharePropertyShowsnapshot:
		return gcpgenserver.SMBSettingsV1betaItemSHOWSNAPSHOT
	case utils.CIFSSharePropertyShowPreviousVersions:
		return gcpgenserver.SMBSettingsV1betaItemSHOWPREVIOUSVERSIONS
	case utils.CIFSAccessBasedEnumeration:
		return gcpgenserver.SMBSettingsV1betaItemACCESSBASEDENUMERATION
	case utils.CIFSSharePropertyCA:
		return gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE
	default:
		return gcpgenserver.SMBSettingsV1betaItemSMBSETTINGSUNSPECIFIED
	}
}

func convertSMBShareSettingToVCP(settings []string) []gcpgenserver.SMBSettingsV1betaItem {
	res := make([]gcpgenserver.SMBSettingsV1betaItem, 0)
	for _, setting := range settings {
		setting := convertFromOntapShareSettingString(setting)
		res = append(res, setting)
	}
	return res
}

func (h Handler) V1betaDeleteVolume(ctx context.Context, req gcpgenserver.OptV1betaDeleteVolumeReq, params gcpgenserver.V1betaDeleteVolumeParams) (gcpgenserver.V1betaDeleteVolumeRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	volume, err := h.Orchestrator.GetVolume(ctx, params.VolumeId, false)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaDeleteVolumeNotFound{
				Code:    404,
				Message: "Volume not found",
			}, nil
		}
		logger.Error("Failed to get volume before deletion", "error", err.Error())
		return &gcpgenserver.V1betaDeleteVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	dummyOperationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + uuid.UUID{}.String()
	if volume != nil && volume.LifeCycleState == datamodel.LifeCycleStateDeleting {
		log := util.GetLogger(ctx)

		jobType := string(datamodel.JobTypeDeleteVolume)
		if volume.LargeCapacity {
			jobType = string(datamodel.JobTypeDeleteLargeVolume)
		}

		job, jobErr := h.Orchestrator.GetJobByResourceUUID(ctx, volume.UUID, jobType)
		if jobErr != nil {
			log.Error("Failed to find job for deleting volume", "volumeUUID", volume.UUID, "error", jobErr.Error())
			// Return the volume response even if job lookup fails
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
	}

	if volume != nil && volume.LifeCycleState == datamodel.LifeCycleStateDeleted {
		// in case the pool is deleted, we should return 404 as the volumes is no longer accessible
		pool, getPoolErr := h.Orchestrator.GetPoolByName(ctx, volume.PoolName, params.ProjectNumber, 0)
		if getPoolErr != nil && !errors.IsNotFoundErr(getPoolErr) {
			logger.Info("Failed to get pool while deleting volume", "poolName", volume.PoolName, "error", getPoolErr.Error())
		}
		if pool.State == datamodel.LifeCycleStateDeleting || pool.State == datamodel.LifeCycleStateDeleted {
			return &gcpgenserver.V1betaDeleteVolumeNotFound{
				Code:    404,
				Message: "Volume not found",
			}, nil
		}

		return &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(dummyOperationID),
			Done: gcpgenserver.NewOptBool(true),
		}, nil
	}

	if volume != nil && volume.PoolID != "" {
		pool, err := h.Orchestrator.DescribePool(ctx, volume.PoolID, params.ProjectNumber)
		if err != nil {
			if errors.IsNotFoundErr(err) {
				return &gcpgenserver.V1betaDeleteVolumeBadRequest{
					Code:    400,
					Message: err.Error(),
				}, nil
			}
			logger.Error("Failed to fetch corresponding pool to the volume.", "error", err.Error())
			return &gcpgenserver.V1betaDeleteVolumeInternalServerError{Code: 500, Message: "Internal server error"}, nil
		}

		if pool != nil && pool.PoolAttributes != nil &&
			(pool.PoolAttributes.ZoneSwitchState == datamodel.ZoneSwitching || pool.PoolAttributes.ZoneSwitchState == datamodel.ZoneSwitched) {
			return &gcpgenserver.V1betaDeleteVolumeBadRequest{
				Code:    http.StatusBadRequest,
				Message: "Volume delete is not supported when the pool is switching/switched to a different primary zone.",
			}, nil
		}
	}

	volume, jobUUID, err := h.Orchestrator.DeleteVolume(ctx, params.VolumeId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.OperationV1beta{
				Name: gcpgenserver.NewOptString(dummyOperationID),
				Done: gcpgenserver.NewOptBool(true),
			}, nil
		}
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaDeleteVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaDeleteVolumeConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			if hasCode, httpCode := customErr.GetHttpCode(); hasCode && httpCode == http.StatusConflict {
				return &gcpgenserver.V1betaDeleteVolumeConflict{
					Code:    float64(httpCode),
					Message: customErr.GetMessage(),
				}, nil
			}
		}
		logger.Error("Failed to delete volume", "error", err.Error())
		return &gcpgenserver.V1betaDeleteVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	resp, err := encodeVolumeV1(convertModelToVCPVolume(volume))
	if err != nil {
		return nil, err
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + jobUUID
	if volume.LifeCycleState == datamodel.LifeCycleStateDeleting || volume.LifeCycleState == datamodel.LifeCycleStateCreating {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

// encodeVolumeV1 encodes a PoolV1 struct to JSON.
func encodeVolumeV1(volumeV1beta *gcpgenserver.VolumeV1beta) (jx.Raw, error) {
	data, err := json.Marshal(volumeV1beta)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func convertModelToVCPVolume(volume *models.Volume) *gcpgenserver.VolumeV1beta {
	res := &gcpgenserver.VolumeV1beta{
		VolumeId:           gcpgenserver.NewOptString(volume.UUID),
		ResourceId:         volume.DisplayName,
		Created:            gcpgenserver.NewOptDateTime(volume.CreatedAt),
		VolumeStateDetails: gcpgenserver.NewOptString(volume.LifeCycleStateDetails),
		VolumeState:        gcpgenserver.NewOptVolumeV1betaVolumeState(gcpgenserver.VolumeV1betaVolumeState(strings.ToUpper(volume.LifeCycleState))),
		Network:            gcpgenserver.NewOptString(volume.VendorSubnetID),
		Description:        gcpgenserver.NewOptNilString(volume.Description),
		PoolId:             gcpgenserver.NewNilString(volume.PoolID),
		CreationToken:      gcpgenserver.NewOptString(volume.CreationToken),
		QuotaInBytes:       gcpgenserver.NewOptFloat64(float64(volume.QuotaInBytes)),
		PoolResourceId:     gcpgenserver.NewOptNilString(volume.PoolName),
		StorageClass:       gcpgenserver.NewOptStorageClassV1beta(gcpgenserver.StorageClassV1betaSOFTWARE),
		ServiceLevel:       gcpgenserver.NewOptVolumeV1betaServiceLevel(gcpgenserver.VolumeV1betaServiceLevelFLEX),
		IsDataProtection:   gcpgenserver.NewOptBool(volume.IsDataProtection),
		EncryptionType:     gcpgenserver.NewOptVolumeV1betaEncryptionType(gcpgenserver.VolumeV1betaEncryptionType(volume.EncryptionType)),
		SnapshotDirectory:  gcpgenserver.NewOptBool(volume.SnapshotDirectory),
		SnapReserve:        gcpgenserver.NewOptFloat64(float64(volume.SnapReserve)),
		Zone:               gcpgenserver.NewOptString(volume.Zone),
		UsedBytes:          gcpgenserver.NewOptNilFloat64(float64(volume.UsedBytes)), // default value for now
		LargeCapacity:      gcpgenserver.NewOptNilBool(volume.LargeCapacity),
	}
	if volume.IsRegionalHA && volume.SecondaryZone != "" {
		res.SecondaryZone = gcpgenserver.NewOptNilString(volume.SecondaryZone)
	}

	// inReplication: only set when true (source or destination); omit when not in replication
	if volume.InReplication != nil && *volume.InReplication {
		res.InReplication = gcpgenserver.NewOptBool(true)
	}
	// Include throughput and iops if they were set from VPG (nullable int64; only for autogen VPG / individual values)
	if volume.ThroughputMibps != nil {
		throughputMibps := float64(*volume.ThroughputMibps)
		res.ThroughputMibps = gcpgenserver.NewOptNilFloat64(throughputMibps)
	}
	if volume.Iops != nil {
		res.Iops = utils.SafeInt64(volume.Iops)
	}
	if volume.VolumePerformanceGroupId != "" {
		res.VolumePerformanceGroupId = gcpgenserver.NewOptNilString(volume.VolumePerformanceGroupId)
	}
	res.KerberosEnabled = gcpgenserver.NewOptNilBool(volume.KerberosEnabled)
	res.LdapEnabled = gcpgenserver.NewOptNilBool(volume.LdapEnabled)
	if volume.ActiveDirectoryConfigId != "" {
		res.ActiveDirectoryConfigId = gcpgenserver.NewOptNilString(volume.ActiveDirectoryConfigId)
	}
	if volume.ActiveDirectoryResourceId != "" {
		res.ActiveDirectoryResourceId = gcpgenserver.NewOptNilString(volume.ActiveDirectoryResourceId)
	}
	if volume.LargeVolumeConstituentCount != nil {
		res.LargeVolumeConstituentCount = gcpgenserver.NewOptNilInt32(*volume.LargeVolumeConstituentCount)
	}
	if volume.KmsConfig != nil {
		res.KmsConfigId = gcpgenserver.NewOptNilString(volume.KmsConfig.UUID)
		res.KmsConfigResourceId = gcpgenserver.NewOptNilString(utils.ParsedKeyFullPathResource{ProjectID: volume.KmsConfig.KeyProjectID,
			KeyRing: volume.KmsConfig.KeyRing, Location: volume.KmsConfig.KeyRingLocation, CryptoKey: volume.KmsConfig.KeyName}.String())
	}
	if volume.DeletedAt != nil {
		res.Deleted = gcpgenserver.NewOptNilDateTime(*volume.DeletedAt)
	}

	res.Protocols = make([]gcpgenserver.ProtocolsV1beta, 0)
	for _, value := range volume.ProtocolTypes {
		var protocolsV1beta gcpgenserver.ProtocolsV1beta
		err := protocolsV1beta.UnmarshalText([]byte(value))
		if err != nil {
			return nil
		}
		res.Protocols = append(res.Protocols, protocolsV1beta)
	}

	if len(volume.RestrictedActions) > 0 {
		res.RestrictedActions = make(gcpgenserver.RestrictedActionsV1beta, 0, len(volume.RestrictedActions))
		for _, val := range volume.RestrictedActions {
			res.RestrictedActions = append(res.RestrictedActions, gcpgenserver.RestrictedActionsV1betaItem(val))
		}
	}

	if volume.Labels != nil {
		labels := gcpgenserver.VolumeV1betaLabels{}
		for key, value := range volume.Labels {
			labels[key] = value
		}
		res.Labels = gcpgenserver.NewOptVolumeV1betaLabels(labels)
	}

	if volume.FileProperties != nil && volume.FileProperties.ExportPolicy != nil {
		rules := make([]gcpgenserver.SimpleExportPolicyRuleV1beta, 0)
		for _, rule := range volume.FileProperties.ExportPolicy.ExportRules {
			ruleV1beta := gcpgenserver.SimpleExportPolicyRuleV1beta{
				AllowedClients:      rule.AllowedClients,
				AccessType:          gcpgenserver.SimpleExportPolicyRuleV1betaAccessType(rule.AccessType),
				Nfsv3:               gcpgenserver.NewOptNilBool(rule.NFSv3),
				Nfsv4:               gcpgenserver.NewOptNilBool(rule.NFSv4),
				Kerberos5ReadOnly:   gcpgenserver.NewOptNilBool(rule.Kerberos5ReadOnly),
				Kerberos5ReadWrite:  gcpgenserver.NewOptNilBool(rule.Kerberos5ReadWrite),
				Kerberos5iReadOnly:  gcpgenserver.NewOptNilBool(rule.Kerberos5iReadOnly),
				Kerberos5iReadWrite: gcpgenserver.NewOptNilBool(rule.Kerberos5iReadWrite),
				Kerberos5pReadOnly:  gcpgenserver.NewOptNilBool(rule.Kerberos5pReadOnly),
				Kerberos5pReadWrite: gcpgenserver.NewOptNilBool(rule.Kerberos5pReadWrite),
			}
			if rule.Superuser {
				ruleV1beta.HasRootAccess = gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessTrue)
			} else {
				ruleV1beta.HasRootAccess = gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccessFalse)
			}
			// Only set AllSquash and AnonUid if they are explicitly set (not nil)
			if rule.AllSquash != nil {
				ruleV1beta.AllSquash = gcpgenserver.NewOptNilBool(*rule.AllSquash)
			}
			if rule.AnonUid != nil {
				ruleV1beta.AnonUid = gcpgenserver.NewOptNilInt64(*rule.AnonUid)
			}
			rules = append(rules, ruleV1beta)
		}
		res.ExportPolicy = gcpgenserver.NewOptExportPolicyV1beta(
			gcpgenserver.ExportPolicyV1beta{
				Rules: rules,
			})
		if volume.LifeCycleState == string(gcpgenserver.VolumeV1betaVolumeStateREADY) {
			res.MountPoints = make([]gcpgenserver.MountPointV1beta, 0)
			for _, ipAddress := range volume.IPAddresses {
				for _, protocol := range volume.ProtocolTypes {
					if utils.IsFilesProtocol(protocol) {
						res.MountPoints = append(res.MountPoints, gcpgenserver.MountPointV1beta{
							Export:       getExportPath(volume.FileProperties.JunctionPath, protocol),
							ExportFull:   getExportFullPath(ipAddress, volume.FileProperties.JunctionPath, protocol, volume.FileProperties.Fqdn),
							IpAddress:    gcpgenserver.NewOptString(ipAddress),
							Protocol:     gcpgenserver.NewOptProtocolsV1beta(gcpgenserver.ProtocolsV1beta(protocol)),
							Instructions: getFilesMountInstructions(ipAddress, volume.FileProperties.JunctionPath, "/"+volume.DisplayName, protocol, volume.FileProperties.Fqdn),
						})
					}
				}
			}
		}
		if len(volume.FileProperties.SMBShareSettings) > 0 {
			res.SmbSettings = convertSMBShareSettingToVCP(volume.FileProperties.SMBShareSettings)
		}
		if volume.FileProperties.SecurityStyle != "" {
			res.SecurityStyle = gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyle(volume.FileProperties.SecurityStyle))
		}
		if volume.FileProperties.UnixPermissions != "" {
			res.UnixPermissions = gcpgenserver.NewOptNilString(volume.FileProperties.UnixPermissions)
		}
	}

	// Check BlockDevices first, then fallback to BlockProperties
	if volume.BlockDevices != nil && len(*volume.BlockDevices) > 0 {
		// Use BlockDevices as primary source
		res.BlockDevices = make([]gcpgenserver.BlockDeviceV1beta, 0, len(*volume.BlockDevices))
		for _, blockDevice := range *volume.BlockDevices {
			blockDeviceV1beta := gcpgenserver.BlockDeviceV1beta{}
			// Convert host groups from BlockDevice format to API format
			if len(blockDevice.HostGroupDetail) > 0 {
				hostGroups := make([]string, 0, len(blockDevice.HostGroupDetail))
				for _, hg := range blockDevice.HostGroupDetail {
					hostGroups = append(hostGroups, hg.HostGroupID)
					blockDeviceV1beta.HostGroupDetails = append(blockDeviceV1beta.HostGroupDetails, gcpgenserver.HostGroupDetail{
						Hosts:       hg.Hosts,
						HostGroupId: gcpgenserver.NewOptString(hg.HostGroupID),
					})
				}
				blockDeviceV1beta.HostGroups = hostGroups
			}
			// Set name if present
			if blockDevice.Name != "" {
				blockDeviceV1beta.Name = gcpgenserver.NewOptString(blockDevice.Name)
			}

			// Set identifier if present
			if blockDevice.Identifier != "" {
				blockDeviceV1beta.Identifier = gcpgenserver.NewOptString(blockDevice.Identifier)
			}

			// Set size if present
			if blockDevice.Size > 0 {
				blockDeviceV1beta.SizeInBytes = gcpgenserver.NewOptFloat64(float64(blockDevice.Size))
			}

			// Set OS type if present
			if blockDevice.OSType != "" {
				blockDeviceV1beta.OsType = gcpgenserver.NewOptBlockDeviceV1betaOsType(gcpgenserver.BlockDeviceV1betaOsType(blockDevice.OSType))
			}

			res.BlockDevices = append(res.BlockDevices, blockDeviceV1beta)
		}

		// Set mount points using BlockDevices data
		if volume.LifeCycleState == string(gcpgenserver.VolumeV1betaVolumeStateREADY) && len(*volume.BlockDevices) > 0 {
			primaryDevice := (*volume.BlockDevices)[0]
			if primaryDevice.OSType != "" && primaryDevice.Identifier != "" {
				res.MountPoints = make([]gcpgenserver.MountPointV1beta, 0)
				res.MountPoints = append(res.MountPoints, gcpgenserver.MountPointV1beta{
					IpAddress:    gcpgenserver.NewOptString(strings.Join(volume.IPAddresses, ",")),
					Protocol:     gcpgenserver.NewOptProtocolsV1beta(gcpgenserver.ProtocolsV1betaISCSI),
					Instructions: getMountInstructions(primaryDevice.OSType, volume.IPAddresses, primaryDevice.Identifier),
				})
			}
		}
	} else if volume.BlockProperties != nil {
		blockPropertiesV1beta := gcpgenserver.BlockPropertiesV1beta{
			OsType:          gcpgenserver.NewOptBlockPropertiesV1betaOsType(gcpgenserver.BlockPropertiesV1betaOsType(volume.BlockProperties.OSType)),
			LunSerialNumber: gcpgenserver.NewOptString(volume.BlockProperties.LunSerialNumber),
		}
		for _, hostGroup := range volume.BlockProperties.HostGroupDetail {
			blockPropertiesV1beta.HostGroupDetails = append(blockPropertiesV1beta.HostGroupDetails, gcpgenserver.HostGroupDetail{
				Hosts:       hostGroup.Hosts,
				HostGroupId: gcpgenserver.NewOptString(hostGroup.HostGroupID),
			})
			blockPropertiesV1beta.HostGroupIds = append(blockPropertiesV1beta.HostGroupIds, hostGroup.HostGroupID)
		}
		res.BlockProperties = gcpgenserver.NewOptBlockPropertiesV1beta(blockPropertiesV1beta)
		// Only show mount points if volume is ready and has valid LUN name
		if volume.LifeCycleState == string(gcpgenserver.VolumeV1betaVolumeStateREADY) && volume.BlockProperties.LunName != "" {
			res.MountPoints = make([]gcpgenserver.MountPointV1beta, 0)
			res.MountPoints = append(res.MountPoints, gcpgenserver.MountPointV1beta{
				IpAddress:    gcpgenserver.NewOptString(strings.Join(volume.IPAddresses, ",")),
				Protocol:     gcpgenserver.NewOptProtocolsV1beta(gcpgenserver.ProtocolsV1betaISCSI),
				Instructions: getMountInstructions(volume.BlockProperties.OSType, volume.IPAddresses, volume.BlockProperties.LunName),
			})
		}
	}
	backupConfig := gcpgenserver.BackupConfigV1beta{}
	if volume.DataProtection != nil {
		if volume.DataProtection.BackupVaultID != "" {
			backupConfig.BackupVaultId = gcpgenserver.NewOptNilString(volume.DataProtection.BackupVaultID)
		}
		if volume.DataProtection.BackupPolicyId != "" {
			backupConfig.BackupPolicyId = gcpgenserver.NewOptNilString(volume.DataProtection.BackupPolicyId)
			if volume.DataProtection.ScheduledBackupEnabled != nil {
				backupConfig.ScheduledBackupEnabled = gcpgenserver.NewOptNilBool(*volume.DataProtection.ScheduledBackupEnabled)
			}
		}
		if volume.DataProtection.BackupChainBytes != nil {
			backupConfig.BackupChainBytes = gcpgenserver.NewOptNilInt64(*volume.DataProtection.BackupChainBytes)
		}
		if volume.DataProtection.KmsGrant != nil && *volume.DataProtection.KmsGrant != "" {
			backupConfig.KmsGrant = gcpgenserver.NewOptNilString(*volume.DataProtection.KmsGrant)
		}
		if backupConfig.BackupVaultId.Set || backupConfig.BackupPolicyId.Set ||
			backupConfig.BackupChainBytes.Set || backupConfig.ScheduledBackupEnabled.Set ||
			backupConfig.KmsGrant.Set {
			res.BackupConfig = gcpgenserver.NewOptBackupConfigV1beta(backupConfig)
		}
	}

	if volume.SnapshotPolicy != nil {
		res.SnapshotPolicy = gcpgenserver.NewOptSnapshotPolicyV1beta(*convertToSnapshotPolicyV2(volume.SnapshotPolicy))
	}

	if volume.AutoTieringPolicy != nil {
		// Determine TierAction based on AutoTieringEnabled
		var tierAction gcpgenserver.TieringPolicyV1betaTierAction
		if volume.AutoTieringPolicy.AutoTieringEnabled {
			tierAction = gcpgenserver.TieringPolicyV1betaTierActionENABLED
		} else {
			tierAction = gcpgenserver.TieringPolicyV1betaTierActionPAUSED
		}

		res.TieringPolicy = gcpgenserver.NewOptTieringPolicyV1beta(
			gcpgenserver.TieringPolicyV1beta{
				TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction(tierAction),
				CoolingThresholdDays:     gcpgenserver.NewOptNilInt32(volume.AutoTieringPolicy.CoolingThresholdDays),
				HotTierBypassModeEnabled: gcpgenserver.NewOptNilBool(volume.AutoTieringPolicy.HotTierBypassModeEnabled),
			})
		res.HotTierSizeGib = gcpgenserver.NewOptNilFloat64(float64(volume.HotTierSizeGib))
		res.ColdTierSizeGib = gcpgenserver.NewOptNilFloat64(float64(volume.ColdTierSizeGib))
	}

	if volume.CacheParameters != nil {
		res.CacheParameters = gcpgenserver.NewOptFlexCacheV1beta(convertToFlexCacheV1(volume.CacheParameters))
	}

	// Update Volume state to PREPARING or READONLY based on the mount status
	if volume.IsDataProtection && res.VolumeState.Value == gcpgenserver.VolumeV1betaVolumeStateREADY {
		if volume.Mounted {
			res.VolumeState = gcpgenserver.NewOptVolumeV1betaVolumeState(gcpgenserver.VolumeV1betaVolumeStateREADONLY)
		} else {
			res.VolumeState = gcpgenserver.NewOptVolumeV1betaVolumeState(gcpgenserver.VolumeV1betaVolumeStatePREPARING)
		}
	}

	// Show cloneDetails whenever CloneParentInfo exists in the DB.
	// CloneParentInfo is only present while is_flexclone=true; the refresh activity removes it
	// once is_flexclone flips to false (split fully complete), so no extra suppression is needed
	// based on volume lifecycle state.
	// The only case where we suppress cloneDetails while CloneParentInfo still exists is when
	// split_complete_percent has reached 100 — the split is finishing and is_flexclone is about
	// to flip; showing a "100% done" clone detail would be misleading.
	if volume.CloneParentInfo != nil {
		splitComplete := volume.CloneParentInfo.SplitCompletePercent != nil &&
			*volume.CloneParentInfo.SplitCompletePercent == 100

		if !splitComplete {
			res.CloneDetails = gcpgenserver.NewOptCloneDetailsV1beta(convertToCloneParentInfoV1(volume.CloneParentInfo, volume.CloneSharedBytes))
		}
	}

	return res
}

func getExportFullPath(address string, path string, protocol string, fqdn string) gcpgenserver.OptString {
	if protocol == string(gcpgenserver.ProtocolsV1betaSMB) {
		return gcpgenserver.NewOptString(fmt.Sprintf(`\\%s\%s`, fqdn, strings.TrimPrefix(path, "/")))
	}
	return gcpgenserver.NewOptString(fmt.Sprintf(`%s:%s`, address, path))
}

func getExportPath(junctionPath, protocol string) gcpgenserver.OptString {
	if protocol == string(gcpgenserver.ProtocolsV1betaSMB) {
		return gcpgenserver.NewOptString(strings.TrimPrefix(junctionPath, "/"))
	}
	return gcpgenserver.NewOptString(junctionPath)
}

func getFilesMountInstructions(ipAddress, junctionPath, fileDir, protocol, fqdn string) gcpgenserver.OptString {
	var instructions string
	exportFull := ipAddress + ":" + junctionPath
	switch protocol {
	case string(gcpgenserver.ProtocolsV1betaNFSV3):
		instructions = fmt.Sprintf(`Setting up your instance
Open an SSH client and connect to your instance.
Install the nfs client on your instance.
On Red Hat Enterprise Linux or SuSE Linux instance:
sudo yum install -y nfs-utils
On an Ubuntu or Debian instance:
sudo apt-get install nfs-common

Mounting your volume
Create a new directory on your instance, such as "%s":
sudo mkdir %s
Mount your volume using the example command below:
sudo mount -t nfs -o rw,hard,rsize=65536,wsize=65536,vers=3,tcp %s %s
Note. Please use mount options appropriate for your specific workloads when known.`, junctionPath, junctionPath, exportFull, junctionPath)
	case string(gcpgenserver.ProtocolsV1betaNFSV4):
		instructions = fmt.Sprintf(`Setting up your instance
Open an SSH client and connect to your instance.
Install the nfs client on your instance.
On Red Hat Enterprise Linux or SuSE Linux instance:
sudo yum install -y nfs-utils
On an Ubuntu or Debian instance:
sudo apt-get install nfs-common

Mounting your volume
Create a new directory on your instance, such as "%s":
sudo mkdir %s
Mount your volume using the example command below:
sudo mount -t nfs -o rw,hard,rsize=65536,wsize=65536,vers=4.1,tcp %s %s
Note. Please use mount options appropriate for your specific workloads when known.`, junctionPath, junctionPath, exportFull, junctionPath)
	case string(gcpgenserver.ProtocolsV1betaSMB):
		exportFull := fmt.Sprintf(`\\%s\%s`, fqdn, strings.TrimPrefix(junctionPath, "/"))
		instructions = fmt.Sprintf(`Mapping your network drive
Click the Start button and then click on Computer.
Click Map Network Drive.
In the Drive list, click any available drive letter.
In the Folder box, type %s. To connect every time you log on to your computer, select the Reconnect at logon check box.
Click Finish.`, exportFull)
	}
	return gcpgenserver.NewOptString(instructions)
}

func getMountInstructions(osType string, ipAddresses []string, lunName string) gcpgenserver.OptString {
	instructions := ""
	switch osType {
	case "LINUX":

		providedIpAddresses := ""
		if len(ipAddresses) >= 2 {
			providedIpAddresses = fmt.Sprintf("The provided IP addresses are %s and %s.", ipAddresses[0], ipAddresses[1])
		} else if len(ipAddresses) == 1 {
			providedIpAddresses = fmt.Sprintf("The provided IP address is %s.", ipAddresses[0])
		}

		ipAddress := ""
		if len(ipAddresses) > 0 {
			ipAddress = ipAddresses[0]
		}

		instructions = fmt.Sprintf(`Prerequisites
Ensure the iSCSI initiator, multipath and sg3 utils packages are installed.

1. Install the iSCSI initiator

Install and enable the iSCSI initiator and sg3 utils package on your system. This software enables your host to connect to the iSCSI target.

On Red Hat Enterprise Linux or SUSE Linux:
$ sudo yum install -y iscsi-initiator-utils device-mapper-multipath sg3_utils
$ sudo systemctl enable iscsid --now

On Ubuntu or Debian instances:
$ sudo apt-get install -y open-iscsi multipath-tools sg3-utils
$ sudo systemctl enable iscsid --now

2. Discover the iSCSI target

Discover the available iSCSI targets by specifying the target's IP address and port. The default iSCSI port is 3260. %s

$ sudo iscsiadm -m discovery -t sendtargets -p %s:3260

This command lists the discovered targets, including their IQN (iSCSI Qualified Name).

3. Log in to the iSCSI target

Log in to the specific target using its IQN that you discovered in the previous step. Replace <<target-iqn>> with the actual IQN.

$ sudo iscsiadm -m node -T <<target-iqn>> -l

4. Identify the LUN on your host

After logging in, your host needs to rescan its SCSI bus to detect the newly connected Logical Unit Number (LUN).

Rescan for new devices:
$ rescan-scsi-bus.sh

Check for the new device (e.g., /dev/sdb). The output displays a new block device.
$ lsblk

5. Format and mount the LUN

If a filesystem is needed, format it with the required filesystem. Then, you can create a mount point and mount the device.

Create a filesystem (e.g., ext4) on the new device. For example, /dev/sdb:
$ sudo mkfs.ext4 /dev/mapper/mpathX

Create a mount point and mount the device:
$ sudo mkdir /mnt/%s
$ sudo mount /dev/sdb /mnt/%s

To ensure the drive mounts automatically after a reboot, add the following line to the /etc/fstab file:
$ /dev/sdb /mnt/%s ext4 defaults 0 0`, providedIpAddresses, ipAddress, lunName, lunName, lunName)

		return gcpgenserver.NewOptString(instructions)
	case "WINDOWS":
		instructions = `Mount instruction for iSCSI target on Windows

Prerequisites
• Windows Server with iSCSI Initiator and Multipath I/O (MPIO) features installed.

1. iSCSI Target Discovery and Login
• Open the iSCSI Initiator (iscsicpl.exe)
  If prompted to start the service, click Yes to enable the Microsoft iSCSI Initiator Service.
• In the Discovery tab, click Discover Portal.
• Enter the IP address of the target and click OK. Repeat this for both IP addresses reported by target,
• In the Targets tab, select the target and click Connect.
• For each of the Target portal IPs, do the following:
  In the Connect to Target dialog, check Enable multi-path (if using multipathing).
  Click Advanced to open a new dialog box.
  In the General tab of the Advanced dialog, select the target portal IP from the dropdown for Target portal IP and click OK.
  In the Connect to Target dialog, click OK.

2. Multipath Configuration
• Enable the MPIO feature:
  Open Server Manager > Manage > Add Roles and Features.
  Under Features, select Multipath I/O and install.
• Configure MPIO for iSCSI devices:
  Open MPIO from the Control Panel or Server Manager.
  Click the Discover Multi-Paths tab.
  Check Add support for iSCSI devices and click Add.
  Reboot if prompted.
• Verify Multipath:
  Open the Device Manager and expand Disk drives.
  Your iSCSI disk should be displayed as a multi-path device.
  In MPIO Properties, under Devices, you should see your iSCSI device listed.

3. Configure the volume as usual
• Use Disk Management to initialize, partition, and format the disk.`
		return gcpgenserver.NewOptString(instructions)

	case "ESXI":
		instructions = `Mount instructions for iSCSI target on on VMware ESXi
1. Enable the ISCSI initiator on your ESXi host.
• Log in to the Sphere Client and select your ESXi host.
• Navigate to Configure > Storage Adapters.
• Select the ISCSI Software Adapter (e.g., vmhbaXX) and click Properties.
• Under General, click Enable to activate the iSCSI initiator.
2. Add the target IP address for discovery
• In the ISCSI Software Adapter properties, go to the Dynamic Discovery tab.
• Click Add and enter the target IP address from the Target details section.
• Leave the port as 3260 (default) and click OK.
3. Rescan the iSCSI adapter to discover the target
• In the Storage Adapters view, select the iSCSI Software Adapter and click Rescan.
• The target IQN from the Target details section should appear under Targets.
3. Verify the LUN is visible and create a datestore.
• Go to Configure > Storage Devices to confirm the LUN (ID 0) is listed.
• Navigate to Datastores and click New Datastore.
• Select VMFS, name the datastore (e.g., iscsi-oras-u02), and choose the ISCSI LUN (LUN ID 0).
• Follow the wizard to format the LUN with VMFS (e.g., VMFS 6) and
complete the setup.`
	}
	return gcpgenserver.NewOptString(instructions)
}

func (h Handler) V1betaGetMultipleVolumes(ctx context.Context, req *gcpgenserver.VolumeIdListV1beta, params gcpgenserver.V1betaGetMultipleVolumesParams) (gcpgenserver.V1betaGetMultipleVolumesRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	// Validate the location first
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaGetMultipleVolumesBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req.VolumeUuids == nil {
		return &gcpgenserver.V1betaGetMultipleVolumesBadRequest{
			Code:    400,
			Message: "VolumeUuids are required",
		}, nil
	}

	if len(req.VolumeUuids) > 1000 {
		return &gcpgenserver.V1betaGetMultipleVolumesBadRequest{
			Code:    400,
			Message: "VolumeUuids in body should have at most 1000 items",
		}, nil
	}

	volumesModelVCP, err := h.Orchestrator.GetMultipleVolumes(ctx, req.VolumeUuids, params.ProjectNumber)
	if err != nil {
		logger.Error("Failed to fetch volume", "error", err.Error())
		return &gcpgenserver.V1betaGetMultipleVolumesInternalServerError{Code: 500, Message: "Internal server error"}, nil
	}

	volumesVCP := make([]gcpgenserver.VolumeV1beta, 0, len(req.VolumeUuids))
	foundVolumeUUIDs := make(map[string]struct{}, len(volumesModelVCP))
	for _, vol := range volumesModelVCP {
		response := convertModelToVCPVolume(vol)
		volumesVCP = append(volumesVCP, *response)
		foundVolumeUUIDs[vol.UUID] = struct{}{}
	}

	// If all volumes are found in VCP, just return them.
	if len(req.VolumeUuids) == len(volumesVCP) {
		return &gcpgenserver.V1betaGetMultipleVolumesOK{
			Volumes: volumesVCP,
		}, nil
	}

	if cvp.CVP_HOST == "" {
		logger.Info("CVP_HOST environment variable is not set, skipping CVP call", "foundVolumes", len(volumesVCP), "requestedVolumes", len(req.VolumeUuids))
		return &gcpgenserver.V1betaGetMultipleVolumesOK{
			Volumes: volumesVCP,
		}, nil
	}

	// Figure out which volumes are missing and need to be fetched from CVP
	missingVolumeUUIDs := helper.FindMissingUUIDs(req.VolumeUuids, foundVolumeUUIDs)

	// If no volumes are missing (e.g. due to duplicates in request), we don't need to call CVP
	if len(missingVolumeUUIDs) == 0 {
		return &gcpgenserver.V1betaGetMultipleVolumesOK{
			Volumes: volumesVCP,
		}, nil
	}

	// The original request object `req` contains all UUIDs. We need a new one with only the missing UUIDs.
	cvpReq := &gcpgenserver.VolumeIdListV1beta{
		VolumeUuids: missingVolumeUUIDs,
	}

	return getMultipleVolumesFromCVP(ctx, cvpReq, params, volumesVCP)
}

func _getMultipleVolumesFromCVP(ctx context.Context, req *gcpgenserver.VolumeIdListV1beta, params gcpgenserver.V1betaGetMultipleVolumesParams, vcpVolumes []gcpgenserver.VolumeV1beta) (gcpgenserver.V1betaGetMultipleVolumesRes, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createCVPClient(logger, jwtToken)

	getMultipleVolumesParams := &volumes.V1betaGetMultipleVolumesParams{
		LocationID:    params.LocationId,
		ProjectNumber: params.ProjectNumber,
		Body: &cvpmodels.VolumeIDListV1beta{
			VolumeUUIDs: req.GetVolumeUuids(),
		},
	}
	if params.XCorrelationID.IsSet() {
		getMultipleVolumesParams.XCorrelationID = &params.XCorrelationID.Value
	}

	res, err := cvpClient.Volumes.V1betaGetMultipleVolumes(getMultipleVolumesParams)
	if err != nil {
		switch e := err.(type) {
		case *volumes.V1betaGetMultipleVolumesBadRequest:
			return &gcpgenserver.V1betaGetMultipleVolumesBadRequest{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *volumes.V1betaGetMultipleVolumesUnprocessableEntity:
			return &gcpgenserver.V1betaGetMultipleVolumesUnprocessableEntity{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *volumes.V1betaGetMultipleVolumesUnauthorized:
			return &gcpgenserver.V1betaGetMultipleVolumesUnauthorized{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *volumes.V1betaGetMultipleVolumesForbidden:
			return &gcpgenserver.V1betaGetMultipleVolumesForbidden{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *volumes.V1betaGetMultipleVolumesNotFound:
			return &gcpgenserver.V1betaGetMultipleVolumesNotFound{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *volumes.V1betaGetMultipleVolumesTooManyRequests:
			return &gcpgenserver.V1betaGetMultipleVolumesTooManyRequests{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		case *volumes.V1betaGetMultipleVolumesDefault:
			return &gcpgenserver.V1betaGetMultipleVolumesInternalServerError{
				Code:    e.Payload.Code,
				Message: e.Payload.Message,
			}, nil
		}
	}
	if res == nil || res.Payload == nil {
		return &gcpgenserver.V1betaGetMultipleVolumesInternalServerError{
			Code:    500,
			Message: "unknown error during get multiple volumes operation",
		}, nil
	}

	volumesList := make([]gcpgenserver.VolumeV1beta, 0)
	for _, vol := range res.GetPayload().Volumes {
		response := convertVolumeV1betaToCVPModel(vol)
		volumesList = append(volumesList, response)
	}

	if vcpVolumes != nil {
		volumesList = append(volumesList, vcpVolumes...)
	}

	return &gcpgenserver.V1betaGetMultipleVolumesOK{
		Volumes: volumesList,
	}, nil
}

func _convertVolumeV1betaCVPToModel(in *cvpmodels.VolumeV1beta) gcpgenserver.VolumeV1beta {
	var resourceId string
	if in.ResourceID != nil {
		resourceId = *in.ResourceID
	}
	volume := gcpgenserver.VolumeV1beta{
		ResourceId:              resourceId,
		VolumeId:                gcpgenserver.NewOptString(in.VolumeID),
		Created:                 gcpgenserver.NewOptDateTime(time.Time(in.Created)),
		Deleted:                 utils.SafeTime(in.Deleted),
		VolumeState:             gcpgenserver.NewOptVolumeV1betaVolumeState(gcpgenserver.VolumeV1betaVolumeState(in.VolumeState)),
		VolumeStateDetails:      gcpgenserver.NewOptString(in.VolumeStateDetails),
		Zone:                    gcpgenserver.NewOptString(in.Zone),
		UsedBytes:               utils.SafeFloat64(in.UsedBytes),
		IsOnPremMigration:       utils.SafeBool(in.IsOnPremMigration),
		Description:             utils.SafeString(in.Description),
		KerberosEnabled:         utils.SafeBool(in.KerberosEnabled),
		LdapEnabled:             utils.SafeBool(in.LdapEnabled),
		UnixPermissions:         utils.SafeString(in.UnixPermissions),
		SecondaryZone:           utils.SafeString(in.SecondaryZone),
		MultipleEndpoints:       utils.SafeBool(in.MultipleEndpoints),
		LargeCapacity:           utils.SafeBool(in.LargeCapacity),
		QuotaInBytes:            utils.SafeOptFloat64(in.QuotaInBytes),
		SnapReserve:             utils.SafeOptFloat64(in.SnapReserve),
		PoolResourceId:          utils.SafeString(in.PoolResourceID),
		ActiveDirectoryConfigId: utils.SafeString(in.ActiveDirectoryConfigID),
		SnapshotDirectory:       utils.GetOptBool(in.SnapshotDirectory),
		KmsConfigId:             utils.SafeString(in.KmsConfigID),
		KmsConfigResourceId:     utils.SafeString(in.KmsConfigResourceID),
		ColdTierSizeGib:         utils.SafeFloat64(in.ColdTierSizeGib),
		InReplication:           utils.GetOptBool(in.InReplication),
		IsDataProtection:        utils.GetOptBool(in.IsDataProtection),
		CreationToken:           utils.GetOptString(in.CreationToken),
		HotTierSizeGib:          utils.SafeOptNilFloat64(in.HotTierSizeGib),
	}

	if in.Network != "" {
		volume.Network = gcpgenserver.NewOptString(in.Network)
	}

	if in.ThroughputMibps != nil {
		throughputMibps := float64(*in.ThroughputMibps)
		volume.ThroughputMibps = gcpgenserver.NewOptNilFloat64(throughputMibps)
	}

	if in.SecurityStyle != "" {
		volume.SecurityStyle = gcpgenserver.NewOptVolumeV1betaSecurityStyle(gcpgenserver.VolumeV1betaSecurityStyle(in.SecurityStyle))
	}

	if in.ServiceLevel != "" {
		volume.ServiceLevel = gcpgenserver.NewOptVolumeV1betaServiceLevel(gcpgenserver.VolumeV1betaServiceLevel(in.ServiceLevel))
	}

	if in.EncryptionType != "" {
		volume.EncryptionType = gcpgenserver.NewOptVolumeV1betaEncryptionType(gcpgenserver.VolumeV1betaEncryptionType(in.EncryptionType))
	}

	if in.StorageClass != "" {
		volume.StorageClass = gcpgenserver.NewOptStorageClassV1beta(gcpgenserver.StorageClassV1beta(in.StorageClass))
	}

	if in.ExportPolicy != nil {
		exportPolicyV1beta := gcpgenserver.ExportPolicyV1beta{}
		if in.ExportPolicy.Rules != nil {
			exportPolicyV1beta.Rules = make([]gcpgenserver.SimpleExportPolicyRuleV1beta, 0)
			for _, rule := range in.ExportPolicy.Rules {
				exportRule := gcpgenserver.SimpleExportPolicyRuleV1beta{
					Kerberos5ReadOnly:   utils.SafeBool(rule.Kerberos5ReadOnly),
					Kerberos5ReadWrite:  utils.SafeBool(rule.Kerberos5ReadWrite),
					Kerberos5iReadOnly:  utils.SafeBool(rule.Kerberos5iReadOnly),
					Kerberos5iReadWrite: utils.SafeBool(rule.Kerberos5iReadWrite),
					Kerberos5pReadOnly:  utils.SafeBool(rule.Kerberos5pReadOnly),
					Kerberos5pReadWrite: utils.SafeBool(rule.Kerberos5pReadWrite),
					Nfsv3:               utils.SafeBool(rule.Nfsv3),
					Nfsv4:               utils.SafeBool(rule.Nfsv4),
				}
				// Only set AllSquash and AnonUid if they are explicitly set (not nil)
				if rule.AllSquash != nil {
					exportRule.AllSquash = gcpgenserver.NewOptNilBool(*rule.AllSquash)
				}
				if rule.AnonUID != nil {
					exportRule.AnonUid = gcpgenserver.NewOptNilInt64(*rule.AnonUID)
				}
				if rule.AccessType != nil {
					exportRule.AccessType = gcpgenserver.SimpleExportPolicyRuleV1betaAccessType(*rule.AccessType)
				}

				if rule.AllowedClients != nil {
					exportRule.AllowedClients = *rule.AllowedClients
				}

				if rule.HasRootAccess != nil {
					exportRule.HasRootAccess = gcpgenserver.NewOptNilSimpleExportPolicyRuleV1betaHasRootAccess(gcpgenserver.SimpleExportPolicyRuleV1betaHasRootAccess(*rule.HasRootAccess))
				}

				exportPolicyV1beta.Rules = append(exportPolicyV1beta.Rules, exportRule)
			}
		}
		volume.ExportPolicy = gcpgenserver.NewOptExportPolicyV1beta(exportPolicyV1beta)
	}

	volume.RestrictedActions = make(gcpgenserver.RestrictedActionsV1beta, 0, len(in.RestrictedActions))
	for _, val := range in.RestrictedActions {
		volume.RestrictedActions = append(volume.RestrictedActions, gcpgenserver.RestrictedActionsV1betaItem(val))
	}

	if in.BackupConfig != nil {
		backupConfigV1beta := gcpgenserver.BackupConfigV1beta{
			BackupVaultId: utils.SafeString(in.BackupConfig.BackupVaultID),
		}
		if in.BackupConfig.BackupChainBytes != nil {
			backupConfigV1beta.BackupChainBytes = utils.SafeInt64(in.BackupConfig.BackupChainBytes)
		}
		if in.BackupConfig.BackupPolicyID != nil {
			backupConfigV1beta.BackupPolicyId = utils.SafeString(in.BackupConfig.BackupPolicyID)
			backupConfigV1beta.ScheduledBackupEnabled = utils.SafeBool(in.BackupConfig.ScheduledBackupEnabled)
		}
		if in.BackupConfig.KmsGrant != nil {
			backupConfigV1beta.KmsGrant = utils.SafeString(in.BackupConfig.KmsGrant)
		}

		volume.BackupConfig = gcpgenserver.NewOptBackupConfigV1beta(backupConfigV1beta)
	}

	if in.Labels != nil {
		labels := gcpgenserver.VolumeV1betaLabels{}
		for key, value := range in.Labels {
			labels[key] = value
		}
		volume.Labels = gcpgenserver.NewOptVolumeV1betaLabels(labels)
	}

	if in.Protocols != nil {
		for _, protocol := range in.Protocols {
			var protocolV1beta gcpgenserver.ProtocolsV1beta
			err := protocolV1beta.UnmarshalText([]byte(protocol))
			if err != nil {
				return volume
			}
			volume.Protocols = append(volume.Protocols, protocolV1beta)
		}
	}

	if in.TieringPolicy != nil && in.TieringPolicy.TierAction != nil {
		tieringPolicyV1beta := gcpgenserver.TieringPolicyV1beta{
			TierAction:               gcpgenserver.NewOptNilTieringPolicyV1betaTierAction(gcpgenserver.TieringPolicyV1betaTierAction(*in.TieringPolicy.TierAction)),
			CoolingThresholdDays:     utils.SafeInt32(in.TieringPolicy.CoolingThresholdDays),
			HotTierBypassModeEnabled: utils.SafeBool(in.TieringPolicy.HotTierBypassModeEnabled),
		}
		volume.TieringPolicy = gcpgenserver.NewOptTieringPolicyV1beta(tieringPolicyV1beta)
	}

	var snapshotPolicy *gcpgenserver.SnapshotPolicyV1beta
	if in.SnapshotPolicy != nil {
		if in.SnapshotPolicy.Enabled != nil && *in.SnapshotPolicy.Enabled {
			var hourlySchedule *gcpgenserver.HourlyScheduleV1beta
			if in.SnapshotPolicy.HourlySchedule != nil {
				hourlySchedule = &gcpgenserver.HourlyScheduleV1beta{
					Minute:          utils.SafeOptFloat64(in.SnapshotPolicy.HourlySchedule.Minute),
					SnapshotsToKeep: utils.SafeOptFloat64(in.SnapshotPolicy.HourlySchedule.SnapshotsToKeep),
				}
			}

			var dailySchedule *gcpgenserver.DailyScheduleV1beta
			if in.SnapshotPolicy.DailySchedule != nil {
				dailySchedule = &gcpgenserver.DailyScheduleV1beta{
					Hour:            utils.SafeOptFloat64(in.SnapshotPolicy.DailySchedule.Hour),
					Minute:          utils.SafeOptFloat64(in.SnapshotPolicy.DailySchedule.Minute),
					SnapshotsToKeep: utils.SafeOptFloat64(in.SnapshotPolicy.DailySchedule.SnapshotsToKeep),
				}
			}

			var weeklySchedule *gcpgenserver.WeeklyScheduleV1beta
			if in.SnapshotPolicy.WeeklySchedule != nil {
				weeklySchedule = &gcpgenserver.WeeklyScheduleV1beta{
					Day:             gcpgenserver.NewOptString(in.SnapshotPolicy.WeeklySchedule.Day),
					Hour:            utils.SafeOptFloat64(in.SnapshotPolicy.WeeklySchedule.Hour),
					Minute:          utils.SafeOptFloat64(in.SnapshotPolicy.WeeklySchedule.Minute),
					SnapshotsToKeep: utils.SafeOptFloat64(in.SnapshotPolicy.WeeklySchedule.SnapshotsToKeep),
				}
			}

			var monthlySchedule *gcpgenserver.MonthlyScheduleV1beta
			if in.SnapshotPolicy.MonthlySchedule != nil {
				monthlySchedule = &gcpgenserver.MonthlyScheduleV1beta{
					DaysOfMonth:     gcpgenserver.NewOptString(in.SnapshotPolicy.MonthlySchedule.DaysOfMonth),
					Hour:            utils.SafeOptFloat64(in.SnapshotPolicy.MonthlySchedule.Hour),
					Minute:          utils.SafeOptFloat64(in.SnapshotPolicy.MonthlySchedule.Minute),
					SnapshotsToKeep: utils.SafeOptFloat64(in.SnapshotPolicy.MonthlySchedule.SnapshotsToKeep),
				}
			}

			snapshotPolicy = &gcpgenserver.SnapshotPolicyV1beta{
				Enabled: utils.SafeBool(in.SnapshotPolicy.Enabled),
			}
			if dailySchedule != nil {
				snapshotPolicy.DailySchedule = gcpgenserver.NewOptDailyScheduleV1beta(*dailySchedule)
			}
			if weeklySchedule != nil {
				snapshotPolicy.WeeklySchedule = gcpgenserver.NewOptWeeklyScheduleV1beta(*weeklySchedule)
			}
			if monthlySchedule != nil {
				snapshotPolicy.MonthlySchedule = gcpgenserver.NewOptMonthlyScheduleV1beta(*monthlySchedule)
			}
			if hourlySchedule != nil {
				snapshotPolicy.HourlySchedule = gcpgenserver.NewOptHourlyScheduleV1beta(*hourlySchedule)
			}
			volume.SnapshotPolicy = gcpgenserver.NewOptSnapshotPolicyV1beta(*snapshotPolicy)
		}
	}

	volume.SmbSettings = make(gcpgenserver.SMBSettingsV1beta, 0)
	for _, val := range in.SmbSettings {
		volume.SmbSettings = append(volume.SmbSettings, gcpgenserver.SMBSettingsV1betaItem(val))
	}

	volume.MountPoints = make([]gcpgenserver.MountPointV1beta, len(in.MountPoints))
	for i, mp := range in.MountPoints {
		volume.MountPoints[i] = gcpgenserver.MountPointV1beta{
			Export:       gcpgenserver.NewOptString(mp.Export),
			ExportFull:   gcpgenserver.NewOptString(mp.ExportFull),
			IpAddress:    gcpgenserver.NewOptString(mp.IPAddress),
			Instructions: gcpgenserver.NewOptString(mp.Instructions),
			Protocol:     gcpgenserver.NewOptProtocolsV1beta(gcpgenserver.ProtocolsV1beta(mp.Protocol)),
		}
	}

	if in.PoolID != nil {
		volume.PoolId = gcpgenserver.NewNilString(*in.PoolID)
	}

	if in.CacheParameters != nil {
		cacheParams := gcpgenserver.FlexCacheV1beta{
			PeerIpAddresses: in.CacheParameters.PeerIPAddresses,
			CacheState:      gcpgenserver.NewOptFlexCacheV1betaCacheState(gcpgenserver.FlexCacheV1betaCacheState(in.CacheParameters.CacheState)),
			Command:         gcpgenserver.NewOptString(in.CacheParameters.Command),
		}
		if in.CacheParameters.PeerVolumeName != "" {
			cacheParams.PeerVolumeName = gcpgenserver.NewOptString(in.CacheParameters.PeerVolumeName)
		}
		if in.CacheParameters.PeerClusterName != "" {
			cacheParams.PeerClusterName = gcpgenserver.NewOptString(in.CacheParameters.PeerClusterName)
		}
		if in.CacheParameters.PeerSvmName != "" {
			cacheParams.PeerSvmName = gcpgenserver.NewOptString(in.CacheParameters.PeerSvmName)
		}

		// Add nil checks for these pointer fields
		if in.CacheParameters.EnableGlobalFileLock != nil {
			cacheParams.EnableGlobalFileLock = gcpgenserver.NewOptNilBool(*in.CacheParameters.EnableGlobalFileLock)
		}
		if in.CacheParameters.PeeringCommandExpiryTime != nil {
			cacheParams.PeeringCommandExpiryTime = gcpgenserver.NewOptNilDateTime(time.Time(*in.CacheParameters.PeeringCommandExpiryTime))
		}
		if in.CacheParameters.Passphrase != nil {
			cacheParams.Passphrase = gcpgenserver.NewOptNilString(*in.CacheParameters.Passphrase)
		}

		if in.CacheParameters.CacheConfig != nil {
			cacheConfigV1beta := gcpgenserver.FlexCacheConfigV1beta{}

			// Add nil checks for each field before dereferencing
			if in.CacheParameters.CacheConfig.WritebackEnabled != nil {
				cacheConfigV1beta.WritebackEnabled = gcpgenserver.NewOptNilBool(*in.CacheParameters.CacheConfig.WritebackEnabled)
			}
			if in.CacheParameters.CacheConfig.AtimeScrubEnabled != nil {
				cacheConfigV1beta.AtimeScrubEnabled = gcpgenserver.NewOptNilBool(*in.CacheParameters.CacheConfig.AtimeScrubEnabled)
			}
			if in.CacheParameters.CacheConfig.AtimeScrubDays != nil {
				cacheConfigV1beta.AtimeScrubDays = gcpgenserver.NewOptNilInt16(*in.CacheParameters.CacheConfig.AtimeScrubDays)
			}
			if in.CacheParameters.CacheConfig.CifsChangeNotifyEnabled != nil {
				cacheConfigV1beta.CifsChangeNotifyEnabled = gcpgenserver.NewOptNilBool(*in.CacheParameters.CacheConfig.CifsChangeNotifyEnabled)
			}

			if in.CacheParameters.CacheConfig.CachePrePopulate != nil {
				flexCachePrePopulateV1beta := gcpgenserver.FlexCachePrePopulateV1beta{
					PathList:        gcpgenserver.NewOptNilStringArray(in.CacheParameters.CacheConfig.CachePrePopulate.PathList),
					ExcludePathList: gcpgenserver.NewOptNilStringArray(in.CacheParameters.CacheConfig.CachePrePopulate.ExcludePathList),
				}
				if in.CacheParameters.CacheConfig.CachePrePopulate.Recursion != nil {
					flexCachePrePopulateV1beta.Recursion = gcpgenserver.NewOptNilBool(*in.CacheParameters.CacheConfig.CachePrePopulate.Recursion)
				}
				cacheConfigV1beta.CachePrePopulate = gcpgenserver.NewOptFlexCachePrePopulateV1beta(flexCachePrePopulateV1beta)
			}

			cacheParams.CacheConfig = gcpgenserver.NewOptFlexCacheConfigV1beta(cacheConfigV1beta)
		}

		volume.CacheParameters = gcpgenserver.NewOptFlexCacheV1beta(cacheParams)
	}

	return volume
}

func convertFromSnapshotPolicyV2(snapshotPolicy *gcpgenserver.SnapshotPolicyV1beta) (*models.SnapshotPolicy, error) {
	if snapshotPolicy == nil {
		return nil, nil
	}
	snapshotPolicySchedule := make([]*models.SnapshotPolicySchedule, 0)

	monthlySchedule := snapshotPolicy.MonthlySchedule
	if monthlySchedule.IsSet() {
		count := int64(0)
		monthly := monthlySchedule.Value
		if monthly.SnapshotsToKeep.IsSet() {
			count = int64(monthly.SnapshotsToKeep.Value)
		}
		daysOfMonth := []int{}
		if monthly.DaysOfMonth.IsSet() {
			days := strings.Split(monthly.DaysOfMonth.Value, ",")
			for _, day := range days {
				dayOfMonth, err := strconv.Atoi(strings.TrimSpace(day))
				if err == nil {
					daysOfMonth = append(daysOfMonth, dayOfMonth)
				}
			}
		} else {
			daysOfMonth = append(daysOfMonth, 1)
		}
		hours := []int{0}
		if monthly.Hour.IsSet() {
			hours[0] = int(monthly.Hour.Value)
		}
		minutes := []int{0}
		if monthly.Minute.IsSet() {
			minutes[0] = int(monthly.Minute.Value)
		}

		snapshotPolicySchedule = append(snapshotPolicySchedule, &models.SnapshotPolicySchedule{
			Count:           count,
			SnapmirrorLabel: SnapshotScheduleLabelMonthly,
			Schedule: &models.Schedule{
				DaysOfMonth: daysOfMonth,
				Hours:       hours,
				Minutes:     minutes,
			},
		})
	}

	weeklySchedule := snapshotPolicy.WeeklySchedule
	if weeklySchedule.IsSet() {
		count := int64(0)
		var err error
		var daysOfWeek []int
		weekly := weeklySchedule.Value
		if weekly.SnapshotsToKeep.IsSet() {
			count = int64(weekly.SnapshotsToKeep.Value)
		}
		if weekly.Day.IsSet() && len(weekly.Day.Value) > 0 {
			daysOfWeek, err = convertDaysOfWeekToIntArray(weekly.Day.Value)
			if err != nil {
				return nil, err
			}
		}
		hours := []int{0}
		if weekly.Hour.IsSet() {
			hours[0] = int(weekly.Hour.Value)
		}

		minutes := []int{0}
		if weekly.Minute.IsSet() {
			minutes[0] = int(weekly.Minute.Value)
		}

		snapshotPolicySchedule = append(snapshotPolicySchedule, &models.SnapshotPolicySchedule{
			Count:           count,
			SnapmirrorLabel: SnapshotScheduleLabelWeekly,
			Schedule: &models.Schedule{
				DaysOfWeek: daysOfWeek,
				Hours:      hours,
				Minutes:    minutes,
			},
		})
	}

	dailySchedule := snapshotPolicy.DailySchedule
	if dailySchedule.IsSet() {
		count := int64(0)
		daily := dailySchedule.Value
		if daily.SnapshotsToKeep.IsSet() {
			count = int64(daily.SnapshotsToKeep.Value)
		}
		hours := []int{0}
		if daily.Hour.IsSet() {
			hours[0] = int(daily.Hour.Value)
		}
		minutes := []int{0}
		if daily.Minute.IsSet() {
			minutes[0] = int(daily.Minute.Value)
		}

		snapshotPolicySchedule = append(snapshotPolicySchedule, &models.SnapshotPolicySchedule{
			Count:           count,
			SnapmirrorLabel: SnapshotScheduleLabelDaily,
			Schedule: &models.Schedule{
				Hours:   hours,
				Minutes: minutes,
			},
		})
	}

	hourlySchedule := snapshotPolicy.HourlySchedule
	if hourlySchedule.IsSet() {
		count := int64(0)
		hourly := hourlySchedule.Value
		if hourly.SnapshotsToKeep.IsSet() {
			count = int64(hourly.SnapshotsToKeep.Value)
		}
		minutes := []int{0}
		if hourly.Minute.IsSet() {
			minutes[0] = int(hourly.Minute.Value)
		}

		snapshotPolicySchedule = append(snapshotPolicySchedule, &models.SnapshotPolicySchedule{
			Count:           count,
			SnapmirrorLabel: SnapshotScheduleLabelHourly,
			Schedule: &models.Schedule{
				Minutes: minutes,
			},
		})
	}

	return &models.SnapshotPolicy{
		IsEnabled: snapshotPolicy.Enabled.IsSet() && snapshotPolicy.Enabled.Value,
		Schedules: snapshotPolicySchedule,
	}, nil
}

func convertDaysOfWeekToIntArray(days string) ([]int, error) {
	// Return Sunday by default
	if days == "" {
		return []int{int(time.Sunday)}, nil
	}

	splitDays := strings.Split(days, ",")
	weekdays := []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday}
	var result []int

	for _, day := range splitDays {
		day := strings.TrimSpace(day)
		hasFoundDay := false
		for _, weekday := range weekdays {
			// Allow for Monday,Tuesday and also MON,TUE
			if strings.HasPrefix(strings.ToLower(weekday.String()), strings.ToLower(day)) {
				weekDayValue := int(weekday)
				if slices.Contains(result, weekDayValue) {
					return nil, errors.NewUserInputValidationErr(daysOfWeekError)
				}
				result = append(result, weekDayValue)
				hasFoundDay = true
				break
			}
		}
		if !hasFoundDay {
			return nil, errors.NewUserInputValidationErr(daysOfWeekError)
		}
	}

	return result, nil
}

func convertToSnapshotPolicyV2(pol *models.SnapshotPolicy) *gcpgenserver.SnapshotPolicyV1beta {
	if pol == nil {
		return nil
	}

	var monthly gcpgenserver.MonthlyScheduleV1beta
	var weekly gcpgenserver.WeeklyScheduleV1beta
	var daily gcpgenserver.DailyScheduleV1beta
	var hourly gcpgenserver.HourlyScheduleV1beta

	for _, sc := range pol.Schedules {
		schedule := *sc
		count := float64(schedule.Count)

		var minute float64
		if len(schedule.Schedule.Minutes) > 0 {
			minute = float64(schedule.Schedule.Minutes[0])
		}

		if len(schedule.Schedule.DaysOfMonth) > 0 {
			var days []string
			for _, day := range schedule.Schedule.DaysOfMonth {
				days = append(days, strconv.Itoa(day))
			}
			daysOfMonth := strings.Join(days, ",")
			hour := float64(0)
			if len(schedule.Schedule.Hours) > 0 {
				hour = float64(schedule.Schedule.Hours[0])
			}
			monthly = gcpgenserver.MonthlyScheduleV1beta{
				SnapshotsToKeep: gcpgenserver.NewOptFloat64(count),
				DaysOfMonth:     gcpgenserver.NewOptString(daysOfMonth),
				Hour:            gcpgenserver.NewOptFloat64(hour),
				Minute:          gcpgenserver.NewOptFloat64(minute),
			}
		} else if len(schedule.Schedule.DaysOfWeek) > 0 {
			days := convertDaysOfWeekFromIntArray(schedule.Schedule.DaysOfWeek)
			hour := float64(0)
			if len(schedule.Schedule.Hours) > 0 {
				hour = float64(schedule.Schedule.Hours[0])
			}
			weekly = gcpgenserver.WeeklyScheduleV1beta{
				SnapshotsToKeep: gcpgenserver.NewOptFloat64(count),
				Day:             gcpgenserver.NewOptString(days),
				Hour:            gcpgenserver.NewOptFloat64(hour),
				Minute:          gcpgenserver.NewOptFloat64(minute),
			}
		} else if len(schedule.Schedule.Hours) > 0 {
			hour := float64(schedule.Schedule.Hours[0])
			daily = gcpgenserver.DailyScheduleV1beta{
				SnapshotsToKeep: gcpgenserver.NewOptFloat64(count),
				Hour:            gcpgenserver.NewOptFloat64(hour),
				Minute:          gcpgenserver.NewOptFloat64(minute),
			}
		} else {
			hourly = gcpgenserver.HourlyScheduleV1beta{
				SnapshotsToKeep: gcpgenserver.NewOptFloat64(count),
				Minute:          gcpgenserver.NewOptFloat64(minute),
			}
		}
	}

	return &gcpgenserver.SnapshotPolicyV1beta{
		Enabled:         gcpgenserver.NewOptNilBool(pol.IsEnabled),
		MonthlySchedule: gcpgenserver.NewOptMonthlyScheduleV1beta(monthly),
		WeeklySchedule:  gcpgenserver.NewOptWeeklyScheduleV1beta(weekly),
		DailySchedule:   gcpgenserver.NewOptDailyScheduleV1beta(daily),
		HourlySchedule:  gcpgenserver.NewOptHourlyScheduleV1beta(hourly),
	}
}

func convertToFlexCacheV1(cp *models.CacheParameters) gcpgenserver.FlexCacheV1beta {
	cacheState := gcpgenserver.FlexCacheV1betaCacheState(cp.CacheState)
	prevCacheState := gcpgenserver.FlexCacheV1betaPreviousCacheState(cp.CacheState)
	cacheParameters := gcpgenserver.FlexCacheV1beta{
		CacheState:         gcpgenserver.NewOptFlexCacheV1betaCacheState(cacheState),
		PreviousCacheState: gcpgenserver.NewOptFlexCacheV1betaPreviousCacheState(prevCacheState),
	}
	if cp.PeerVolumeName != "" {
		cacheParameters.PeerVolumeName = gcpgenserver.NewOptString(cp.PeerVolumeName)
	}
	if cp.PeerClusterName != "" {
		cacheParameters.PeerClusterName = gcpgenserver.NewOptString(cp.PeerClusterName)
	}
	if cp.PeerSvmName != "" {
		cacheParameters.PeerSvmName = gcpgenserver.NewOptString(cp.PeerSvmName)
	}

	if cp.PeeringCommand != "" {
		cacheParameters.Command = gcpgenserver.NewOptString(cp.PeeringCommand)
	}

	if cp.CacheStateDetails != "" {
		cacheParameters.StateDetails = gcpgenserver.NewOptString(cp.CacheStateDetails)
	}

	if cp.CacheStateDetailsCode != 0 {
		cacheParameters.StateDetailsCode = gcpgenserver.NewOptInt32((int32)(cp.CacheStateDetailsCode))
	}

	if cp.Passphrase != nil {
		cacheParameters.Passphrase = gcpgenserver.NewOptNilString(*cp.Passphrase)
	}

	if cp.PeerExpiryTime != nil {
		cacheParameters.PeeringCommandExpiryTime = gcpgenserver.NewOptNilDateTime(*cp.PeerExpiryTime)
	}

	if cp.EnableGlobalFileLock != nil {
		cacheParameters.EnableGlobalFileLock = gcpgenserver.NewOptNilBool(*cp.EnableGlobalFileLock)
	}

	if cp.CacheConfig != nil {
		incomingConfig := cp.CacheConfig
		cacheConfig := gcpgenserver.FlexCacheConfigV1beta{
			WritebackEnabled:        gcpgenserver.NewOptNilBool(nillable.GetBool(incomingConfig.WritebackEnabled, false)),
			AtimeScrubDays:          gcpgenserver.NewOptNilInt16(nillable.GetInt16(incomingConfig.AtimeScrubDays, 0)),
			AtimeScrubEnabled:       gcpgenserver.NewOptNilBool(nillable.GetBool(incomingConfig.AtimeScrubEnabled, false)),
			CifsChangeNotifyEnabled: gcpgenserver.NewOptNilBool(nillable.GetBool(incomingConfig.CifsChangeNotifyEnabled, false)),
		}

		if incomingConfig.CachePrePopulate != nil {
			prepopulate := gcpgenserver.FlexCachePrePopulateV1beta{
				PathList:        gcpgenserver.NewOptNilStringArray(incomingConfig.CachePrePopulate.PathList),
				ExcludePathList: gcpgenserver.NewOptNilStringArray(incomingConfig.CachePrePopulate.ExcludePathList),
				Recursion:       gcpgenserver.NewOptNilBool(nillable.GetBool(incomingConfig.CachePrePopulate.Recursion, false)),
			}
			cacheConfig.CachePrePopulate = gcpgenserver.NewOptFlexCachePrePopulateV1beta(prepopulate)
		}

		if incomingConfig.CachePrePopulateState != "" {
			cacheConfig.CachePrePopulateState = gcpgenserver.NewOptFlexCacheConfigV1betaCachePrePopulateState(
				gcpgenserver.FlexCacheConfigV1betaCachePrePopulateState(incomingConfig.CachePrePopulateState),
			)
		}

		cacheParameters.CacheConfig = gcpgenserver.NewOptFlexCacheConfigV1beta(cacheConfig)
	}

	return cacheParameters
}

func convertToCloneParentInfoV1(cp *models.CloneParentInfo, cloneSharedBytes uint64) gcpgenserver.CloneDetailsV1beta {
	cloneParentInfo := gcpgenserver.CloneDetailsV1beta{
		ParentVolumeId:   gcpgenserver.NewOptString(nillable.GetString(cp.ParentVolumeId, "")),
		ParentSnapshotId: gcpgenserver.NewOptString(nillable.GetString(cp.ParentSnapshotId, "")),
		SharedBytes:      gcpgenserver.NewOptNilFloat64(float64(cloneSharedBytes)),
	}
	if cp.State != nil && *cp.State != "" {
		cloneParentInfo.State = gcpgenserver.NewOptNilCloneDetailsV1betaState(gcpgenserver.CloneDetailsV1betaState(*cp.State))
	}
	if cp.SplitCompletePercent != nil {
		cloneParentInfo.SplitCompletePercent = gcpgenserver.NewOptNilInt64(*cp.SplitCompletePercent)
	}
	// Only set stateDetails if non-empty
	if cp.StateDetails != nil && *cp.StateDetails != "" {
		cloneParentInfo.StateDetails = gcpgenserver.NewOptNilString(*cp.StateDetails)
	}
	return cloneParentInfo
}

func convertDaysOfWeekFromIntArray(days []int) string {
	var resultDays []string
	for _, day := range days {
		if day >= 0 && day <= 6 {
			resultDays = append(resultDays, time.Weekday(day).String())
		}
	}

	// Return Sunday by default
	if len(resultDays) < 1 {
		resultDays = append(resultDays, time.Sunday.String())
	}

	return strings.Join(resultDays, ",")
}

// validateBackupScheduleCron validates that the cron expression has a minimum interval of 5 minutes
func validateBackupScheduleCron(cronExpression string) error {
	if cronExpression == "" {
		return nil
	}

	// First validate that it's a valid cron expression
	_, err := cron.Parse(cronExpression)
	if err != nil {
		return errors.NewUserInputValidationErr(fmt.Sprintf("Invalid cron expression: %s", err.Error()))
	}

	// Split the cron expression to check the minute field
	parts := strings.Fields(cronExpression)
	if len(parts) != 5 {
		return errors.NewUserInputValidationErr("Invalid cron expression format. Expected 5 fields: minute hour day month weekday")
	}

	minuteField := parts[0]

	// Check if the minute field indicates a frequency less than 5 minutes
	if minuteField == "*" {
		// Every minute - too frequent
		return errors.NewUserInputValidationErr("Backup schedule interval must be at least 5 minutes. Current schedule: every minute")
	}

	if strings.HasPrefix(minuteField, "*/") {
		// Parse the interval
		intervalStr := strings.TrimPrefix(minuteField, "*/")
		interval, err := strconv.Atoi(intervalStr)
		if err != nil {
			return errors.NewUserInputValidationErr(fmt.Sprintf("Invalid minute interval in cron expression: %s", minuteField))
		}

		if interval < 5 {
			return errors.NewUserInputValidationErr(fmt.Sprintf("Backup schedule interval must be at least 5 minutes. Current interval: %d minutes", interval))
		}
	}

	return nil
}

func (h Handler) V1betaEstablishVolumePeering(ctx context.Context, req *gcpgenserver.EstablishPeeringRequestV1beta,
	params gcpgenserver.V1betaEstablishVolumePeeringParams) (gcpgenserver.V1betaEstablishVolumePeeringRes, error) {
	logger := util.GetLogger(ctx)
	if !flexCacheEnabled {
		return &gcpgenserver.V1betaEstablishVolumePeeringForbidden{
			Code:    403,
			Message: "FlexCache feature is currently not enabled.",
		}, nil
	}

	var peerAddrs []string
	if v, ok := req.PeerIpAddresses.Get(); ok {
		peerAddrs = v
	}

	var expiry *time.Time
	if t, ok := req.PeeringCommandExpiryTime.Get(); ok {
		expiry = &t
	}

	region, zone, parsingErr := utils.ParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaEstablishVolumePeeringBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	peeringParams := &common.EstablishVolumePeeringParams{
		AccountName:     params.ProjectNumber,
		Region:          region,
		Zone:            zone,
		Name:            params.VolumeResourceId,
		PeerClusterName: req.PeerClusterName,
		PeerAddresses:   peerAddrs,
		ExpiryTime:      expiry,
		PeerSvmName:     req.PeerSvmName,
		PeerVolumeName:  req.PeerVolumeName,
	}

	volume, jobUUID, err := h.Orchestrator.EstablishFlexCacheVolumePeering(ctx, peeringParams)
	if err != nil {
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaEstablishVolumePeeringConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaEstablishVolumePeeringBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to establish volume peering", "error", err.Error())
		return &gcpgenserver.V1betaEstablishVolumePeeringInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	resp, err := encodeVolumeV1(convertModelToVCPVolume(volume))
	if err != nil {
		return nil, err
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + jobUUID
	if volume.LifeCycleState == datamodel.LifeCycleStateCreating || volume.LifeCycleState == datamodel.LifeCycleStatePreparing {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}

	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

// validateFlexCacheRequest validates FlexCache volumes against the full API request
func validateFlexCacheRequest(req *gcpgenserver.VolumeCreateV1beta) error {
	vol := req.Volume

	if req.SnapshotId.IsSet() && req.SnapshotId.Value != "" {
		return fmt.Errorf("cache volume creation from snapshot is not supported")
	}

	if req.BackupId.IsSet() && req.BackupId.Value != "" {
		return fmt.Errorf("cache volume creation from backup is not supported")
	}

	cp := vol.CacheParameters.Value

	var peerClusterName string
	if cp.PeerClusterName.IsSet() {
		peerClusterName = cp.PeerClusterName.Value
	}
	if peerClusterName == "" {
		return fmt.Errorf("cache volume creation requires cacheParameters.peerClusterName")
	}

	var peerVolumeName string
	if cp.PeerVolumeName.IsSet() {
		peerVolumeName = cp.PeerVolumeName.Value
	}
	if peerVolumeName == "" {
		return fmt.Errorf("cache volume creation requires cacheParameters.peerVolumeName")
	} else {
		originVolumeNamePattern := `^[a-zA-Z_][a-zA-Z0-9_]{0,202}$`
		if matched, _ := regexp.MatchString(originVolumeNamePattern, peerVolumeName); !matched {
			return fmt.Errorf(
				"origin volume name '%s' is invalid. It must start with an alphabetic character or underscore, contain only letters, digits, and underscores, and be between 1 and 203 characters in length",
				peerVolumeName,
			)
		}
	}

	var peerSvmName string
	if cp.PeerSvmName.IsSet() {
		peerSvmName = cp.PeerSvmName.Value
	}
	if peerSvmName == "" {
		return fmt.Errorf("cache volume creation requires cacheParameters.peerSvmName")
	}

	if len(cp.PeerIpAddresses) == 0 {
		return fmt.Errorf("cache volume creation requires cacheParameters.peerIPAddresses")
	}

	if cp.CacheConfig.IsSet() {
		cc := cp.CacheConfig.Value
		if cc.AtimeScrubDays.IsSet() && (!cc.AtimeScrubEnabled.IsSet() || !cc.AtimeScrubEnabled.Value) {
			return fmt.Errorf("atimeScrubEnabled must be true to set atimeScrubDays")
		}

		if cp.CacheConfig.Value.CachePrePopulate.IsSet() {
			pp := cp.CacheConfig.Value.CachePrePopulate.Value
			if len(pp.PathList.Value) > 0 || len(pp.ExcludePathList.Value) > 0 || (pp.Recursion.IsSet() && pp.Recursion.Value) {
				return fmt.Errorf("pre-populate is not supported during FlexCache volume creation")
			}
		}
	}

	err := validateProtocolsV1beta(vol.Protocols)
	if err != nil {
		return err
	}

	if vol.SnapshotPolicy.IsSet() {
		return fmt.Errorf("snapshot policy is not allowed for FlexCache volumes")
	}

	if vol.SnapReserve.IsSet() && vol.SnapReserve.Value != 0 {
		return fmt.Errorf("snapshot reserve is not allowed for FlexCache volumes")
	}

	if vol.SnapshotDirectory.IsSet() && vol.SnapshotDirectory.Value {
		return fmt.Errorf("snapshot directory is not allowed for FlexCache volumes")
	}

	if vol.BackupConfig.IsSet() {
		return fmt.Errorf("backup config is not allowed for FlexCache volumes")
	}

	if vol.TieringPolicy.IsSet() {
		return fmt.Errorf("tiering policy is not allowed for FlexCache volumes")
	}

	if req.HybridReplicationParameters.IsSet() {
		return fmt.Errorf("hybrid replication is not allowed for FlexCache volumes")
	}

	if len(vol.SmbSettings) > 0 {
		notSupportedSmbSettings := map[string]bool{
			"CONTINUOUSLY_AVAILABLE": true,
			"SHOW_SNAPSHOT":          true,
			"SHOW_PREVIOUS_VERSIONS": true,
		}

		var notSupported []string
		for _, smbSetting := range vol.SmbSettings {
			if notSupportedSmbSettings[string(smbSetting)] {
				notSupported = append(notSupported, string(smbSetting))
			}
		}

		if len(notSupported) > 0 {
			return fmt.Errorf("SMB share properties %s are not supported for FlexCache volumes", strings.Join(notSupported, ", "))
		}
	}

	if vol.LargeVolumeConstituentCount.IsSet() {
		return fmt.Errorf("large volume constituent count is not allowed for FlexCache volumes")
	}

	if vol.LargeCapacity.IsSet() && vol.LargeCapacity.Value {
		return fmt.Errorf("large capacity is not allowed for FlexCache volumes")
	}

	if vol.UnixPermissions.IsSet() && vol.UnixPermissions.Value != "" {
		return fmt.Errorf("unix permissions are not allowed during FlexCache volume creation")
	}

	return nil
}

func (h Handler) V1betaRestoreBackupFiles(ctx context.Context, req *gcpgenserver.BackupRestoreFilesV1beta,
	params gcpgenserver.V1betaRestoreBackupFilesParams) (gcpgenserver.V1betaRestoreBackupFilesRes, error) {
	logger := util.GetLogger(ctx)

	if !sfrEnabled {
		return &gcpgenserver.V1betaRestoreBackupFilesBadRequest{
			Code:    400,
			Message: "SFR feature is currently not enabled.",
		}, nil
	}

	var backupPath string
	if req.BackupPath.IsSet() {
		backupPath = req.BackupPath.Value
		components := strings.Split(backupPath, "/")
		// Ensure there are enough components to avoid out of range errors
		if len(components) < MaxBackupPathComponents {
			return &gcpgenserver.V1betaRestoreBackupFilesBadRequest{
				Code:    400,
				Message: "Invalid backup path format",
			}, nil
		}
	}

	var backupID string
	if req.BackupId.IsSet() {
		backupID = req.BackupId.Value
	}

	if len(req.SourceFileList) == 0 {
		return &gcpgenserver.V1betaRestoreBackupFilesBadRequest{
			Code:    400,
			Message: "Source file list cannot be empty",
		}, nil
	} else if len(req.SourceFileList) > MaxSourceFileList {
		return &gcpgenserver.V1betaRestoreBackupFilesBadRequest{
			Code:    400,
			Message: fmt.Sprintf("Source file list cannot contain more than %d files", MaxSourceFileList),
		}, nil
	}

	if params.VolumeId == "" {
		return &gcpgenserver.V1betaRestoreBackupFilesBadRequest{
			Code:    400,
			Message: "Volume ID cannot be empty",
		}, nil
	}

	var restoreFilePath string
	if req.RestoreFilePath.IsSet() {
		restoreFilePath = req.RestoreFilePath.Value
	}

	restoreFilesParams := &common.RestoreFilesFromBackupParams{
		AccountName:     params.ProjectNumber,
		BackupPath:      backupPath,
		BackupID:        backupID,
		SourceFileList:  req.SourceFileList,
		RestoreFilePath: restoreFilePath,
		VolumeUUID:      params.VolumeId,
	}

	jobUUID, err := h.Orchestrator.RestoreFilesFromBackup(ctx, restoreFilesParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaRestoreBackupFilesBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to restore files from backup", "error", err.Error())
		return &gcpgenserver.V1betaRestoreBackupFilesInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + jobUUID

	return &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(operationID),
		Done: gcpgenserver.NewOptBool(false),
	}, nil
}

func (h Handler) V1betaSplitStartVolume(ctx context.Context, params gcpgenserver.V1betaSplitStartVolumeParams) (gcpgenserver.V1betaSplitStartVolumeRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	if !thinCloneGASupport {
		return &gcpgenserver.V1betaSplitStartVolumeForbidden{
			Code:    403,
			Message: "Thin clone split feature is currently not enabled.",
		}, nil
	}

	logger.Debugf("Initiating synchronous split start for volume: %s", params.VolumeId)
	return h.splitStartVolumeViaCoreAPI(ctx, params)
}

// splitStartVolumeViaCoreAPI forwards the split start request to the core-api service.
// This ensures ONTAP calls and DB access are performed in the core pod, not in google-proxy.
func (h Handler) splitStartVolumeViaCoreAPI(ctx context.Context, params gcpgenserver.V1betaSplitStartVolumeParams) (gcpgenserver.V1betaSplitStartVolumeRes, error) {
	logger := util.GetLogger(ctx)
	if coreAPIHost == "" {
		logger.Error("CORE_API_HOST is not set, cannot forward split start to core API")
		return &gcpgenserver.V1betaSplitStartVolumeInternalServerError{
			Code:    500,
			Message: "Core API host not configured",
		}, nil
	}

	jwtToken := utils.GetJWTTokenFromContext(ctx)
	client := createCoreAPIClient(coreAPIHost, jwtToken, logger)
	if client == nil {
		logger.Error("Failed to create core API client")
		return &gcpgenserver.V1betaSplitStartVolumeInternalServerError{
			Code:    500,
			Message: "Failed to create core API client",
		}, nil
	}

	coreParams := coreapi.V1SplitStartVolumeParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		VolumeId:      params.VolumeId,
		XCorrelationID: func() coreapi.OptString {
			if params.XCorrelationID.IsSet() {
				return coreapi.NewOptString(params.XCorrelationID.Value)
			}
			return coreapi.OptString{}
		}(),
	}

	response, err := client.Invoker.V1SplitStartVolume(ctx, coreParams)
	if err != nil {
		logger.Errorf("Core API call failed: %v", err)
		return &gcpgenserver.V1betaSplitStartVolumeInternalServerError{
			Code:    500,
			Message: fmt.Sprintf("Core API call failed: %v", err),
		}, nil
	}

	switch resp := response.(type) {
	case *coreapi.OperationV1:
		operationID := resp.Name.Or("")
		if operationID == "" {
			operationID = fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, uuid.New().String())
		}
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp.Response,
			Done:     gcpgenserver.NewOptBool(resp.Done.Or(false)),
		}, nil
	case *coreapi.V1SplitStartVolumeBadRequest:
		return &gcpgenserver.V1betaSplitStartVolumeBadRequest{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStartVolumeNotFound:
		return &gcpgenserver.V1betaSplitStartVolumeNotFound{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStartVolumeConflict:
		return &gcpgenserver.V1betaSplitStartVolumeConflict{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStartVolumeInternalServerError:
		return &gcpgenserver.V1betaSplitStartVolumeInternalServerError{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	default:
		logger.Errorf("Unexpected response type from core API: %T", response)
		return &gcpgenserver.V1betaSplitStartVolumeInternalServerError{
			Code:    500,
			Message: "An internal error occurred",
		}, nil
	}
}

func (h Handler) V1betaSplitStopVolume(ctx context.Context, params gcpgenserver.V1betaSplitStopVolumeParams) (gcpgenserver.V1betaSplitStopVolumeRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	if !thinCloneGASupport {
		return &gcpgenserver.V1betaSplitStopVolumeForbidden{
			Code:    403,
			Message: "Thin clone split feature is currently not enabled.",
		}, nil
	}

	logger.Debugf("Initiating synchronous split stop for volume: %s", params.VolumeId)
	return h.splitStopVolumeViaCoreAPI(ctx, params)
}

// splitStopVolumeViaCoreAPI forwards the split stop request to the core-api service.
// The stop is synchronous on the core side (no Temporal workflow), so a single
// round-trip suffices and we always return done=true on success.
func (h Handler) splitStopVolumeViaCoreAPI(ctx context.Context, params gcpgenserver.V1betaSplitStopVolumeParams) (gcpgenserver.V1betaSplitStopVolumeRes, error) {
	logger := util.GetLogger(ctx)
	if coreAPIHost == "" {
		logger.Error("CORE_API_HOST is not set, cannot forward split stop to core API")
		return &gcpgenserver.V1betaSplitStopVolumeInternalServerError{
			Code:    500,
			Message: "Core API host not configured",
		}, nil
	}

	jwtToken := utils.GetJWTTokenFromContext(ctx)
	client := createCoreAPIClient(coreAPIHost, jwtToken, logger)
	if client == nil {
		logger.Error("Failed to create core API client")
		return &gcpgenserver.V1betaSplitStopVolumeInternalServerError{
			Code:    500,
			Message: "Failed to create core API client",
		}, nil
	}

	coreParams := coreapi.V1SplitStopVolumeParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		VolumeId:      params.VolumeId,
		XCorrelationID: func() coreapi.OptString {
			if params.XCorrelationID.IsSet() {
				return coreapi.NewOptString(params.XCorrelationID.Value)
			}
			return coreapi.OptString{}
		}(),
	}

	response, err := client.Invoker.V1SplitStopVolume(ctx, coreParams)
	if err != nil {
		logger.Errorf("Core API call failed: %v", err)
		return &gcpgenserver.V1betaSplitStopVolumeInternalServerError{
			Code:    500,
			Message: fmt.Sprintf("Core API call failed: %v", err),
		}, nil
	}

	switch resp := response.(type) {
	case *coreapi.OperationV1:
		operationID := resp.Name.Or("")
		if operationID == "" {
			operationID = fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, uuid.New().String())
		}
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp.Response,
			Done:     gcpgenserver.NewOptBool(resp.Done.Or(true)),
		}, nil
	case *coreapi.V1SplitStopVolumeBadRequest:
		return &gcpgenserver.V1betaSplitStopVolumeBadRequest{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStopVolumeUnauthorized:
		return &gcpgenserver.V1betaSplitStopVolumeUnauthorized{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStopVolumeForbidden:
		return &gcpgenserver.V1betaSplitStopVolumeForbidden{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStopVolumeNotFound:
		return &gcpgenserver.V1betaSplitStopVolumeNotFound{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStopVolumeConflict:
		return &gcpgenserver.V1betaSplitStopVolumeConflict{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStopVolumeUnprocessableEntity:
		return &gcpgenserver.V1betaSplitStopVolumeUnprocessableEntity{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStopVolumeTooManyRequests:
		return &gcpgenserver.V1betaSplitStopVolumeTooManyRequests{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	case *coreapi.V1SplitStopVolumeInternalServerError:
		return &gcpgenserver.V1betaSplitStopVolumeInternalServerError{
			Code:    resp.Code,
			Message: resp.Message,
		}, nil
	default:
		logger.Errorf("Unexpected response type from core API: %T", response)
		return &gcpgenserver.V1betaSplitStopVolumeInternalServerError{
			Code:    500,
			Message: "An internal error occurred",
		}, nil
	}
}

// validateProtocolsV1beta enforces protocol constraints for FlexCache volume requests.
// FlexCache volumes are file-only; block protocols (currently iSCSI) must be excluded.
// Rules:
// 1. PROTOCOL_UNSPECIFIED is not allowed (explicit protocols required).
// 2. Block protocols (e.g. iSCSI) are rejected since FlexCache does not support block.
// 3. Maximum of two protocols (e.g. NFS + SMB) allowed.
// Returns the first validation error encountered or nil if valid.
// NOTE: If additional block protocols are introduced, extend the block protocol checks here.
func validateProtocolsV1beta(protocols []gcpgenserver.ProtocolsV1beta) error {
	if containsProtocolTypeV1beta(protocols, gcpgenserver.ProtocolsV1betaPROTOCOLUNSPECIFIED) {
		return fmt.Errorf("can't have PROTOCOL_UNSPECIFIED in protocol list")
	}
	if containsProtocolTypeV1beta(protocols, gcpgenserver.ProtocolsV1betaISCSI) {
		return fmt.Errorf("iSCSI protocol is not supported")
	}
	if len(protocols) > 2 {
		return fmt.Errorf("volume can only support up to two protocols, please remove any additional entries in the protocols list")
	}
	return nil
}

func containsProtocolTypeV1beta(protocols []gcpgenserver.ProtocolsV1beta, protocolType gcpgenserver.ProtocolsV1beta) bool {
	for _, prot := range protocols {
		if prot == protocolType {
			return true
		}
	}
	return false
}

func hasSMBProtocol(protocols []gcpgenserver.ProtocolsV1beta) bool {
	for _, protocol := range protocols {
		if protocol == gcpgenserver.ProtocolsV1betaSMB {
			return true
		}
	}
	return false
}

func validateSmbShareSettingsV2(settings []gcpgenserver.SMBSettingsV1betaItem) error {
	browsable := false
	nonBrowsable := false
	for _, setting := range settings {
		if setting == gcpgenserver.SMBSettingsV1betaItemBROWSABLE {
			browsable = true
		}
		if setting == gcpgenserver.SMBSettingsV1betaItemNONBROWSABLE {
			nonBrowsable = true
		}
	}
	if nonBrowsable && browsable {
		return errors.NewUserInputValidationErr("SMBShareSettings cannot have both browsable and non_browsable settings")
	}
	return nil
}

func _validateKerberosPolicyV1beta(protocols []gcpgenserver.ProtocolsV1beta, kerberosEnabled *bool, policy gcpgenserver.OptExportPolicyV1beta, pool *models.Pool) error {
	// When kerberosEnabled is not set, default value is false
	isKerberosEnabledInVolumeRequest := getKerberosEnabledFlagFromRequest(kerberosEnabled)
	hasNfs4KerberosPolicy := hasNfs4KerberosV1beta(policy)
	if !enableKerberos && isKerberosEnabledInVolumeRequest && hasNfs4KerberosPolicy {
		return errors.New("Kerberos is not supported in this region")
	}
	if enableKerberos && isKerberosEnabledInVolumeRequest && hasNfs4KerberosPolicy &&
		(pool == nil || pool.ActiveDirectoryConfigId == "") {
		return errors.NewUserInputValidationErr("Kerberos requires the pool to be joined to an Active Directory.")
	}
	if isKerberosEnabledInVolumeRequest {
		if pool == nil || pool.ActiveDirectory == nil || pool.ActiveDirectory.ActiveDirectoryAttributes == nil {
			return errors.NewUserInputValidationErr("Kerberos requires the pool to be joined to an Active Directory.")
		}
		kdcIP := pool.ActiveDirectory.ActiveDirectoryAttributes.KdcIP
		kdcHostname := pool.ActiveDirectory.ActiveDirectoryAttributes.KdcHostname
		if kdcIP == "" || kdcHostname == "" {
			return errors.NewUserInputValidationErr("Active directory configuration must have KDC Name and KDC IP set for creating kerberos volume")
		}
		if !policy.IsSet() {
			return errors.New("Export policy must be defined for kerberos enabled volumes")
		}
		for _, rule := range policy.Value.Rules {
			if rule.AccessType != gcpgenserver.SimpleExportPolicyRuleV1betaAccessTypeREADNONE {
				return errors.New("When kerberos is enabled, 'accessType' should be set to READ_NONE in export policy rules")
			}
		}
		if !hasNfs4KerberosPolicy {
			return errors.New("Export policy rules doesn't contain any kerberos export policy rule for kerberos enabled volume")
		} else {
			if !containsProtocolTypeV1beta(protocols, gcpgenserver.ProtocolsV1betaNFSV4) {
				return errors.New("Kerberos feature is enabled for only NFSv4 volumes")
			}
		}
	} else {
		if hasNfs4KerberosPolicy {
			return errors.New("Export policy rules don't match kerberos enabled flag")
		}
	}
	return nil
}

func _hasNfs4KerberosV1beta(policy gcpgenserver.OptExportPolicyV1beta) bool {
	if policy.IsSet() {
		for _, rule := range policy.Value.Rules {
			if (rule.Kerberos5ReadWrite.Set && rule.Kerberos5ReadWrite.Value) ||
				(rule.Kerberos5ReadOnly.Set && rule.Kerberos5ReadOnly.Value) ||
				(rule.Kerberos5pReadWrite.Set && rule.Kerberos5pReadWrite.Value) ||
				(rule.Kerberos5pReadOnly.Set && rule.Kerberos5pReadOnly.Value) ||
				(rule.Kerberos5iReadOnly.Set && rule.Kerberos5iReadOnly.Value) ||
				(rule.Kerberos5iReadWrite.Set && rule.Kerberos5iReadWrite.Value) {
				return true
			}
		}
	}
	return false
}

func _getKerberosEnabledFlagFromRequest(kerberosEnableFlagFromRequest *bool) bool {
	return nillable.GetBool(kerberosEnableFlagFromRequest, false)
}

func setRestrictedActionsFromRequest(restrictedActions []gcpgenserver.RestrictedActionsV1betaItem, newRestrictedActions *[]string, protocols []string) error {
	parsed, err := parseRestrictedActionsFromRequest(restrictedActions, protocols)
	if err != nil {
		return err
	}
	if parsed != nil && len(*parsed) > 0 {
		*newRestrictedActions = *parsed
	}
	return nil
}

// parseRestrictedActionsFromRequest normalizes restrictedActions from the request.
// Duplicate values are deduplicated (first occurrence wins). Call only when the field
// was present on the request. Returns a non-nil *[]string: empty slice clears
// restrictions on update; non-empty replaces them. Create callers should assign *parsed
// only when len(*parsed) > 0.
func parseRestrictedActionsFromRequest(restrictedActions []gcpgenserver.RestrictedActionsV1betaItem, protocols []string) (*[]string, error) {
	if len(restrictedActions) == 0 {
		empty := []string{}
		return &empty, nil
	}

	for _, action := range restrictedActions {
		if action == gcpgenserver.RestrictedActionsV1betaItemDELETE && utils.IsSMBOnlyProtocols(protocols) {
			return nil, errors.NewUserInputValidationErr("Delete protection is not supported for SMB volumes")
		}
	}

	apiActions := make([]string, 0, len(restrictedActions))
	seen := make(map[string]struct{}, len(restrictedActions))
	for _, action := range restrictedActions {
		if action == gcpgenserver.RestrictedActionsV1betaItemRESTRICTEDACTIONUNSPECIFIED {
			continue
		}
		actionStr := string(action)
		if _, ok := seen[actionStr]; ok {
			continue
		}
		seen[actionStr] = struct{}{}
		apiActions = append(apiActions, actionStr)
	}
	if len(apiActions) == 0 {
		empty := []string{}
		return &empty, nil
	}
	value := append([]string(nil), apiActions...)
	return &value, nil
}

func validateSmbVolumeParams(req *gcpgenserver.VolumeUpdateV1beta) error {
	if req == nil {
		return nil
	}
	if req.UnixPermissions.IsSet() && req.UnixPermissions.Value != "" {
		return errors.NewUserInputValidationErr("Setting Unix permission is not allowed for SMB volumes")
	}
	if req.ExportPolicy.IsSet() && len(req.ExportPolicy.Value.GetRules()) > 0 {
		return errors.NewUserInputValidationErr("Cannot specify export policy rules for non-NFS volume")
	}
	if slices.Contains(req.SmbSettings, gcpgenserver.SMBSettingsV1betaItemCONTINUOUSLYAVAILABLE) {
		return errors.NewUserInputValidationErr("Cannot modify continuously_available smb share property")
	}
	return nil
}

func validateFlexCacheUpdateParams(cacheParams *gcpgenserver.FlexCacheV1beta, dbVolume *models.Volume) error {
	hasFlexCacheFields := cacheParams.CacheConfig.IsSet() ||
		cacheParams.EnableGlobalFileLock.IsSet() ||
		cacheParams.PeerClusterName.IsSet() ||
		cacheParams.PeerSvmName.IsSet() ||
		cacheParams.PeerVolumeName.IsSet() ||
		cacheParams.PeerIpAddresses != nil

	if !hasFlexCacheFields {
		return nil
	}

	if dbVolume != nil && dbVolume.CacheParameters == nil {
		return errors.NewUserInputValidationErr(
			"Cannot update cacheConfig on a non-FlexCache volume",
		)
	}

	if dbVolume != nil && dbVolume.CacheParameters != nil {
		if cacheParams.EnableGlobalFileLock.IsSet() {
			requestedValue, _ := cacheParams.EnableGlobalFileLock.Get()
			currentValue := false
			if dbVolume.CacheParameters.EnableGlobalFileLock != nil {
				currentValue = *dbVolume.CacheParameters.EnableGlobalFileLock
			}
			if requestedValue != currentValue {
				return errors.NewUserInputValidationErr(
					"EnableGlobalFileLock is immutable and cannot be changed",
				)
			}
		}

		if cacheParams.PeerClusterName.IsSet() {
			if cacheParams.PeerClusterName.Value != dbVolume.CacheParameters.PeerClusterName {
				return errors.NewUserInputValidationErr(
					"PeerClusterName is immutable and cannot be changed",
				)
			}
		}

		if cacheParams.PeerIpAddresses != nil {
			if !utils.IsSliceEqual(cacheParams.PeerIpAddresses, dbVolume.CacheParameters.PeerIPAddresses) {
				return errors.NewUserInputValidationErr(
					"PeerIpAddresses is immutable and cannot be changed",
				)
			}
		}

		if cacheParams.PeerSvmName.IsSet() {
			if cacheParams.PeerSvmName.Value != dbVolume.CacheParameters.PeerSvmName {
				return errors.NewUserInputValidationErr(
					"PeerSvmName is immutable and cannot be changed",
				)
			}
		}

		if cacheParams.PeerVolumeName.IsSet() {
			if cacheParams.PeerVolumeName.Value != dbVolume.CacheParameters.PeerVolumeName {
				return errors.NewUserInputValidationErr(
					"PeerVolumeName is immutable and cannot be changed",
				)
			}
		}
	}

	// Validate CacheConfig-specific rules only if CacheConfig is being updated
	if !cacheParams.CacheConfig.IsSet() {
		return nil
	}

	cacheConfig, _ := cacheParams.CacheConfig.Get()

	if cacheConfig.AtimeScrubDays.IsSet() {
		atimeScrubEnabled := false

		if cacheConfig.AtimeScrubEnabled.IsSet() {
			atimeScrubEnabled, _ = cacheConfig.AtimeScrubEnabled.Get()
		} else if dbVolume != nil &&
			dbVolume.CacheParameters != nil &&
			dbVolume.CacheParameters.CacheConfig != nil &&
			dbVolume.CacheParameters.CacheConfig.AtimeScrubEnabled != nil {
			atimeScrubEnabled = *dbVolume.CacheParameters.CacheConfig.AtimeScrubEnabled
		}

		if !atimeScrubEnabled {
			return errors.NewUserInputValidationErr(
				"atimeScrubDays can only be set when atimeScrubEnabled is true",
			)
		}
	}

	return nil
}
