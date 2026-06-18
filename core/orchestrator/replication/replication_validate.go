package replication

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/pools"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/replications"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	coreModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/mqos"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	ValidateReplicationParams       = _validateReplicationParams
	ValidateCreateReplicationParams = _validateCreateReplicationParams
	ValidateReplicationResourceId   = _validateReplicationResourceId
	CheckActiveReplicationJobs      = _checkActiveReplicationJobs
	ValidateLabels                  = _validateLabels
	internalUtilGetCCFEURI          = GetCCFEURI
	utilsParseProjectNumberFromURI  = utils.ParseProjectNumberFromURI
	convertLabelsMapToJSONB         = utils.ConvertLabelsMapToJSONB

	validateStoragePoolUri                = _validateStoragePoolUri
	getDestinationPool                    = _getDestinationPool
	validateVolumeQosParamsForReplication = mqos.ValidateVolumeQosParams
	getVolume                             = _getVolume
	describeVolume                        = _describeVolume
	verifyHybridParameters                = _verifyHybridParameters
	isClusterPeeringStateValid            = _isClusterPeeringStateValid
	createReplicationObjects              = _createReplicationObjects
	replicationJobInProcess               = _replicationJobInProcess
	internalGetReplicationCount           = _internalGetReplicationCount
	internalCountInRegionReplications     = _internalCountInRegionReplications
	getInRegionQuotaState                 = _getInRegionQuotaState
	internalGetVolumeCount                = _internalGetVolumeCount
	getReplicationJobs                    = _getReplicationJobs
	getReplication                        = _getReplication
	VerifyDstReplicationResume            = _verifyDstReplicationResume
	VerifySourceQuotaRules                = _verifySourceQuotaRules
	VerifyDestinationQuotaRules           = _verifyDestinationQuotaRules
	VerifyNewSourceQuotaRulesReverse      = _verifyNewSourceQuotaRulesReverse
	VerifyNewDestinationQuotaRulesReverse = _verifyNewDestinationQuotaRulesReverse
	ValidateReplicationUpdate             = _validateReplicationUpdate
	VerifyDstReplicationStop              = _verifyDstReplicationStop
	VerifyDstVolume                       = _verifyDstVolume
	VerifyReplication                     = _verifyReplication
	VerifyDstReplicationSync              = _verifyDstReplicationSync
	VerifyDstReplicationReverse           = _verifyDstReplicationReverse
	VerifyEstablishPeering                = _verifyEstablishPeering
	HybridReplicationJobsInProcess        = _hybridReplicationJobsInProcess
	listVolumeReplicationsByCCFEURI       = _listVolumeReplicationsByCCFEURI

	InternalUtilGetCallbackToken   = auth.GetSignedAccessToken
	InternalUtilGetSignedToken     = auth.GetSignedJwtToken
	InternalUtilGetPairedRegionURI = utils.GetPairedRegionURI
	InternalParseRegionAndZone     = utils.ParseRegionAndZone

	regexpCompile           = regexp.Compile
	JsonMarshal             = json.Marshal
	JsonUnMarshal           = json.Unmarshal
	hydrationEnabled        = env.GetBool("GCP_HYDRATE_ENABLED", true)
	autoTieringEnabled      = env.GetBool("AUTO_TIERING_ENABLED", false)
	cpcrEnabled             = env.GetBool("CPCRR_ENABLED", false)
	czcrEnabled             = env.GetBool("CZCRR_ENABLED", false)
	getQuotaLimit           = common.GetQuotaLimit
	minCoolingThresholdDays = 2
	maxCoolingThresholdDays = 183

	// activeJobStates defines the job states that indicate a job is still in progress
	activeJobStates = []string{
		string(datamodel.JobsStateNEW),
		string(datamodel.JobsStatePROCESSING),
	}
	replicationJobTypes = []string{
		string(datamodel.JobTypeCreateVolumeReplication),
		string(datamodel.JobTypeDeleteVolumeReplication),
		string(datamodel.JobTypeUpdateVolumeReplication),
		string(datamodel.JobTypeResumeVolumeReplication),
		string(datamodel.JobTypeReverseResumeVolumeReplication),
		string(datamodel.JobTypeStopVolumeReplication),
		string(datamodel.JobTypeCreateHybridReplication),
		string(datamodel.JobTypeHybridReplicationEstablishPeering),
		string(datamodel.JobTypeHybridReplicationInternalEstablish),
		string(datamodel.JobTypeReverseHybridReplicationInternal),
	}
)

type QuotaType string
type ResourceType string

const (
	storageUriRegex      = "^projects\\/([^\\/]+)\\/locations\\/([^\\/]+)\\/storagePools|pools\\/([^\\/]+)$"
	maxRuneCount         = 63
	maxByteCount         = 128
	dstVolumeNameRegex   = "^[a-z]([a-z0-9_]{0,61}[a-z0-9])?$"
	remoteRegionCustomer = "customer"
)

var compiledRegex = regexp.MustCompile(dstVolumeNameRegex)

// hasActiveClusterUpgrade returns true if the given cluster (pool UUID) has an active upgrade job (PENDING or IN_PROGRESS).
func hasActiveClusterUpgrade(ctx context.Context, se database.Storage, clusterID string) (bool, error) {
	jobs, err := se.GetClusterUpgradeJobsByClusterID(ctx, clusterID)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.Status == string(coreModels.UpgradeStatusPending) || job.Status == string(coreModels.UpgradeStatusInProgress) {
			return true, nil
		}
	}
	return false, nil
}

func _validateCreateReplicationParams(ctx context.Context, event *CreateReplicationEvent, se database.Storage) (*datamodel.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	logger.Debug("Starting validateCreateReplicationParams")
	destVolumeParams := event.CreateReplicationParams.DestinationVolumeParameters

	if destVolumeParams.TieringPolicy != nil {
		if !autoTieringEnabled {
			return nil, utilErrors.NewUserInputValidationErr("Auto-Tiering feature is currently not enabled.")
		}
	}
	if *event.CreateReplicationParams.ReplicationSchedule == models.ReplicationV1betaReplicationScheduleREPLICATIONSCHEDULEUNSPECIFIED {
		typeErr := errors.NewVCPError(errors.ErrWorkflowConfigurationError, errors.New("replicationSchedule is UNSPECIFIED"))
		logger.Error("replicationSchedule is UNSPECIFIED", "error", typeErr)
		return nil, typeErr
	}

	if destVolumeParams.VolumeID != "" && !compiledRegex.MatchString(destVolumeParams.VolumeID) {
		return nil, utilErrors.NewUserInputValidationErr("Volume ID can only contain lowercase letters, numbers, and underscores. It must start with a letter and cannot end with an underscore.")
	}

	if event.CreateReplicationParams.Labels != nil {
		err := ValidateLabels(event.CreateReplicationParams.Labels)
		if err != nil {
			logger.Error("validateLabels error", "error", err)
			return nil, err
		}
	}

	token, err := InternalUtilGetSignedToken(event.SourceProjectNumber)
	if err != nil {
		logger.Error("Get Signed Token Error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGetSignedToken, err)
	}

	dstToken := token
	if event.DestinationProjectNumber != event.SourceProjectNumber {
		// if remoteProject is not the same as the projectNumber, we need to get a new token for the remote project
		dstToken, err = InternalUtilGetSignedToken(event.DestinationProjectNumber)
		if err != nil {
			logger.Error("Get Signed Token Error For Remote Project", "error", err)
			return nil, errors.NewVCPError(errors.ErrGetSignedToken, err)
		}
	}

	sourceRegion, _, parseError := InternalParseRegionAndZone(event.LocationID)
	if parseError != nil {
		logger.Error("Parse Source Location Error")
		return nil, errors.NewVCPError(errors.ErrParseSourceLocation, errors.New(parseError.Error()))
	}
	srcBasePath, err := InternalUtilGetPairedRegionURI(sourceRegion)
	if err != nil {
		logger.Error("Get Paired Source Region Uri error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGetSrcBasePath, err)
	}

	destRegion, _, parseError := InternalParseRegionAndZone(event.DestinationLocationID)
	if parseError != nil {
		logger.Error("Parse Destination Location Error", "error", errors.New(parseError.Error()))
		return nil, errors.NewVCPError(errors.ErrParseDestinationLocation, errors.New(parseError.Error()))
	}
	destBasePath, err := InternalUtilGetPairedRegionURI(destRegion)
	if err != nil {
		logger.Error("Get Paired Destination Region Uri error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGetDstBasePath, err)
	}
	event.DestinationRegion = destRegion
	event.CCFEUri = internalUtilGetCCFEURI(event.SourceProjectNumber, event.LocationID, event.VolumeResourceID, *event.CreateReplicationParams.ResourceID)

	// Block Cross Project and Cross Zone Replication for now. Will be enabled once the feature is validated
	// Check environment variables to control this behavior
	if event.SourceProjectNumber != event.DestinationProjectNumber {
		if !cpcrEnabled {
			return nil, utilErrors.NewUserInputValidationErr("cross project replication is not supported")
		}
	}
	if sourceRegion == destRegion {
		if !czcrEnabled {
			return nil, utilErrors.NewUserInputValidationErr("cross zone replication is not supported")
		}
	}

	err = ValidateReplicationResourceId(ctx, event.SourceProjectNumber, *event.CreateReplicationParams.ResourceID, event.VolumeResourceID, se)
	if err != nil {
		logger.Error("Replication resourceId error", "error", err)
		return nil, errors.NewVCPError(errors.ErrValidateCreateResourceIdInUse, err)
	}

	if event.SourceVolume.VolumeAttributes.IsDataProtection {
		logger.Error("sourceVolume already in replication")
		return nil, errors.NewVCPError(errors.ErrValidateCreateSourceVolumeInReplicationGroup, errors.New("sourceVolume already in replication"))
	}

	if event.SourceVolume.State != string(googleproxyclient.VolumeV1betaVolumeStateREADY) {
		logger.Error("sourceVolume is not in a READY state")
		return nil, errors.NewVCPError(errors.ErrValidateCreateSourceVolumeNotReady, errors.New("sourceVolume is not in a READY state"))
	}

	// Block replication if source volume is a thin clone undergoing split operation
	if event.SourceVolume.VolumeAttributes != nil && event.SourceVolume.VolumeAttributes.CloneParentInfo != nil {
		if event.SourceVolume.VolumeAttributes.CloneParentInfo.State == datamodel.CloneStateSplitting {
			logger.Error("Source volume is a thin clone undergoing split operation and cannot be used for replication", "volume_id", event.SourceVolume.UUID)
			return nil, utilErrors.NewConflictErr("Cannot create replication as source volume is undergoing split operation")
		}
	}

	if !isPoolHealthy(event.SourcePool.State) {
		typeErr := errors.NewVCPError(
			errors.ErrValidateSourceStoragePoolState, errors.New("source pool is in unhealthy state, please try after some time"))
		if event.SourcePool.State == string(googleproxyclient.PoolV1betaStoragePoolStateDEGRADED) {
			typeErr = errors.NewVCPError(
				errors.ErrValidateSourceStoragePoolStateDegraded, errors.New("source pool is in degraded state, please try after some time"))
		}
		logger.Error("Source pool is in unhealthy state, Please try after some time", "error", typeErr)
		return nil, typeErr
	}

	// Block replication when source pool has cluster upgrade in progress (same as degraded mode)
	hasActive, err := hasActiveClusterUpgrade(ctx, se, event.SourcePool.UUID)
	if err != nil {
		logger.Error("Failed to check source pool cluster upgrade status", "error", err)
		return nil, errors.NewVCPError(errors.ErrWorkflowConfigurationError, err)
	}
	if hasActive {
		typeErr := errors.NewVCPError(
			errors.ErrStoragePoolTemporarilyUnavailable, errors.New("storage pool is temporarily unavailable, please try again later"))
		logger.Error("Source pool is temporarily unavailable (cluster upgrade in progress)", "error", typeErr)
		return nil, typeErr
	}

	err = validateStoragePoolUri(*destVolumeParams.StoragePool)
	if err != nil {
		logger.Error("validateStoragePoolUri error", "error", err)
		return nil, errors.NewVCPError(errors.ErrValidateStoragePoolUri, err)
	}

	destPool, err := getDestinationPool(ctx, destBasePath, dstToken, event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, event.DestinationPoolName)
	if err != nil {
		logger.Error("getDestinationPool error", "error", err)
		return nil, err
	}
	if isPoolInONTAPMode(destPool) {
		typeErr := errors.NewVCPError(errors.ErrValidateDestinationPoolMode, errors.New("Cannot create Replication with ONTAP-mode pool using GCNV API"))
		logger.Error("Destination pool is in ONTAP mode, cannot create replication using GCNV API", "error", typeErr)
		return nil, typeErr
	}

	if isPoolInTransitionState(destPool) {
		typeErr := errors.NewVCPError(errors.ErrValidateDestinationPoolTransitioning, errors.New("Destination pool is in transition state"))
		logger.Error("Destination pool is in transition state, Please try after some time", "error", typeErr)
		return nil, typeErr
	}
	if !isPoolHealthy(string(destPool.StoragePoolState.Value)) {
		typeErr := errors.NewVCPError(
			errors.ErrValidateDestinationStoragePoolState, errors.New("destination pool is in unhealthy state, please try after some time"))
		if destPool.StoragePoolState.Value == googleproxyclient.PoolInternalV1betaStoragePoolStateDEGRADED {
			typeErr = errors.NewVCPError(
				errors.ErrValidateDestinationStoragePoolStateDegraded, errors.New("destination pool is in degraded state, please try after some time"))
		}
		logger.Error("Destination pool is in unhealthy state, Please try after some time", "error", typeErr)
		return nil, typeErr
	}

	allocatedBytes := float64(0)
	if destPool.AllocatedBytes.Set {
		allocatedBytes = destPool.AllocatedBytes.Value
	}
	bytesNeeded := float64(event.SourceVolume.SizeInBytes) + allocatedBytes
	if bytesNeeded > destPool.SizeInBytes {
		typeErr := errors.NewVCPError(errors.ErrDestPoolSize, errors.New("Volume exceeds destination pool size"))
		logger.Error("Volume exceeds destination pool size", "error", typeErr)
		return nil, typeErr
	}

	// Validate AutoTiering
	tieringPolicy := destVolumeParams.TieringPolicy

	if (tieringPolicy != nil && !tieringPolicy.TierAction.IsNull()) && !destPool.AllowAutoTiering.IsNull() && !destPool.AllowAutoTiering.Value {
		typeErr := errors.NewVCPError(errors.ErrDestPoolTieringPolicyMismatch, errors.New("Auto tiering is not enabled on the destination pool"))
		logger.Error("Auto tiering is not enabled on the destination pool", "error", typeErr)
		return nil, typeErr
	}

	if tieringPolicy != nil && !tieringPolicy.CoolingThresholdDays.IsNull() {
		if tieringPolicy.CoolingThresholdDays.Value < int32(minCoolingThresholdDays) || tieringPolicy.CoolingThresholdDays.Value > int32(maxCoolingThresholdDays) {
			typeErr := errors.NewVCPError(errors.ErrDestVolumeTieringThresholdOutOfRange, errors.New("Coolness threshold days should be in between 2 and 183"))
			logger.Error("Coolness threshold days should be in between 2 and 183", "error", typeErr)
			return nil, typeErr
		}
	}

	if event.SourceVolume.Pool.ServiceLevel != string(destPool.ServiceLevel) {
		typeErr := errors.NewVCPError(errors.ErrServiceLevelMismatch, errors.New("Service level on source volume and destination pool do not match"))
		logger.Error("Service level on source volume and destination pool do not match", "error", typeErr)
		return nil, typeErr
	}

	// Validate LargeCapacity pool type match
	if destPool.LargeCapacity.Set && event.SourceVolume.Pool.LargeCapacity != destPool.LargeCapacity.Value {
		typeErr := errors.NewVCPError(errors.ErrVolumePoolTypeMismatch, errors.New("CRR cannot be created between normal and large capacity pools"))
		logger.Error("CRR cannot be created between normal and large capacity pools", "error", typeErr, "sourceLargeCapacity", event.SourceVolume.Pool.LargeCapacity, "destLargeCapacity", destPool.LargeCapacity.Value)
		return nil, typeErr
	}

	// Validate QoS parameters (MQoS rules and throughput range); pool capacity is validated below
	poolQos := mqos.PoolQosInput{QosType: destPool.QosType.Value, ForReplication: true}
	poolQos.PoolThroughputMibps = int64(destPool.TotalThroughputMibps.Value)
	poolQos.PoolIops = int64(destPool.TotalIops.Value)
	calculatedIops, err := validateVolumeQosParamsForReplication(poolQos, destVolumeParams.ThroughputMibps, destVolumeParams.Iops, destVolumeParams.VolumePerformanceGroupId)
	if err != nil {
		return nil, err
	}
	destVolumeParams.Iops = calculatedIops

	err = replicationJobInProcess(ctx, event.SourceProjectNumber, event.DestinationProjectNumber, srcBasePath, destBasePath, event.LocationID, event.DestinationLocationID, token, dstToken, event.CCFEUri, "", event.SourcePool.UUID, destPool.PoolId.Value, event.XCorrelationID)
	if err != nil {
		return nil, err
	}

	if hydrationEnabled {
		storageClass := models.StorageClassV1betaSOFTWARE
		serviceLevel := event.SourceVolume.Pool.ServiceLevel
		callbackToken, err := InternalUtilGetCallbackToken()
		if err != nil {
			logger.Error("Get callback token error", "error", err)
			return nil, errors.NewVCPError(errors.ErrGetSignedCallbackToken, err)
		}

		// Same region: IRR (same project) or CPR (cross-project).
		if sourceRegion == destRegion {
			if event.SourceProjectNumber == event.DestinationProjectNumber {
				totalInRegionQuota, existingInRegionReplications, err := getInRegionQuotaState(ctx, se, event.DestinationLocationID, event.DestinationProjectNumber, callbackToken)
				if err != nil {
					return nil, err
				}
				if totalInRegionQuota-existingInRegionReplications < 2 {
					return nil, errors.NewVCPError(errors.ErrInRegionReplicationQuotaLimitExceeded,
						fmt.Errorf("quota limit 'Flex Unified in-region replicated volumes per region' has been exceeded for project %s", event.DestinationProjectNumber))
				}
			} else {
				totalInRegionQuota, existingInRegionReplications, err := getInRegionQuotaState(ctx, se, event.LocationID, event.SourceProjectNumber, callbackToken)
				if err != nil {
					return nil, err
				}
				if totalInRegionQuota <= existingInRegionReplications {
					return nil, errors.NewVCPError(errors.ErrInRegionReplicationQuotaLimitExceeded,
						fmt.Errorf("quota limit 'Flex Unified in-region replicated volumes per region' has been exceeded for project %s", event.SourceProjectNumber))
				}

				totalInRegionQuota, existingInRegionReplications, err = getInRegionQuotaState(ctx, se, event.DestinationLocationID, event.DestinationProjectNumber, callbackToken)
				if err != nil {
					return nil, err
				}
				if totalInRegionQuota <= existingInRegionReplications {
					return nil, errors.NewVCPError(errors.ErrInRegionReplicationQuotaLimitExceeded,
						fmt.Errorf("quota limit 'Flex Unified in-region replicated volumes per region' has been exceeded for project %s", event.DestinationProjectNumber))
				}
			}
		} else {
			replicationQuotaLimit, err := getQuotaLimit(ctx, logger, event.DestinationLocationID, event.DestinationProjectNumber, callbackToken, common.ResourceTypeReplication)
			if err != nil {
				logger.Error("Get replication quota limit error", "error", err)
				return nil, errors.NewVCPError(errors.ErrGetReplicationQuotaLimitInternal, err)
			}
			destReplicationCount, err := internalGetReplicationCount(ctx, destBasePath, event.DestinationProjectNumber, event.DestinationLocationID, "", dstToken, string(storageClass), string(serviceLevel))
			if err != nil {
				return nil, errors.NewVCPError(errors.ErrValidateCreateReplicationCvpInternalGetReplicationCount, err)
			}
			if replicationQuotaLimit <= destReplicationCount {
				return nil, errors.NewVCPError(errors.ErrReplicationQuotaLimitExceeded, errors.New("Quota limit 'ReplicatedVolumesPerRegion' has been exceeded."))
			}
		}

		destVolumeQuotaLimit, err := getQuotaLimit(ctx, logger, event.DestinationLocationID, event.DestinationProjectNumber, callbackToken, common.ResourceTypeVolume)
		if err != nil {
			logger.Error("Get volume quota limit error", "error", err)
			return nil, errors.NewVCPError(errors.ErrGetVolumeQuotaLimitInternal, err)
		}
		destVolumeCount, err := internalGetVolumeCount(ctx, destBasePath, event.DestinationProjectNumber, event.DestinationLocationID, "", dstToken, string(storageClass), string(serviceLevel))
		if err != nil {
			return nil, errors.NewVCPError(errors.ErrValidateCreateReplicationCvpInternalGetVolumeCount, err)
		}
		if destVolumeQuotaLimit <= destVolumeCount {
			return nil, errors.NewVCPError(errors.ErrVolumeQuotaLimitExceeded, errors.New("Quota limit 'VolumesPerRegion' on destination region has been exceeded."))
		}
	}

	destShareName := event.CreateReplicationParams.DestinationVolumeParameters.ShareName
	if destShareName == "" && event.SourceVolume.VolumeAttributes.CreationToken != "" {
		destShareName = event.SourceVolume.VolumeAttributes.CreationToken
	}
	destVolumeID := event.CreateReplicationParams.DestinationVolumeParameters.VolumeID
	if destVolumeID == "" {
		destVolumeID = event.SourceVolume.Name
		event.CreateReplicationParams.DestinationVolumeParameters.VolumeID = destVolumeID
	}
	destVolume, err := getVolume(ctx, destBasePath, dstToken, event.DestinationLocationID, event.DestinationProjectNumber, event.XCorrelationID, destVolumeID)
	if err != nil {
		if !utilErrors.IsNotFoundErr(err) {
			logger.Error("getDestinationVolume error", "error", err)
			return nil, errors.NewVCPError(errors.ErrValidateGetVolumeReplicationCreation, err)
		}
	}
	if destVolume.ResourceId != "" {
		if destVolume.CreationToken.Value == destShareName {
			typeErr := errors.NewVCPError(errors.ErrGetVolumeCreateTokenInUseRemoteShareName, errors.New("RemoteShareName already Exists"))
			logger.Error("RemoteShareName already Exists", "error", typeErr)
			return nil, typeErr
		}

		typeErr := errors.NewVCPError(errors.ErrGetVolumeCreateTokenInUseRemoteResourceID, errors.New("RemoteResourceID already Exists"))
		logger.Error("RemoteResourceID already Exists", "error", typeErr)
		return nil, typeErr
	}

	expectedDbReplication, err := createReplicationObjects(event, event.DestinationLocationID, sourceRegion, destRegion)
	if err != nil {
		logger.Error("create dummy replication error", "error", err)
		return nil, errors.NewVCPError(errors.ErrValidateCreateDummyReplication, err)
	}

	logger.Debug("Finished validateCreateReplicationParams")

	return expectedDbReplication, nil
}

func isPoolHealthy(poolState string) bool {
	if poolState == string(googleproxyclient.PoolV1betaStoragePoolStateERROR) || poolState == string(googleproxyclient.PoolV1betaStoragePoolStateDISABLED) || poolState == string(googleproxyclient.PoolV1betaStoragePoolStateDEGRADED) {
		return false
	}
	return true
}

func isPoolInTransitionState(dstPool *googleproxyclient.PoolInternalV1beta) bool {
	if dstPool.StoragePoolState.Value == googleproxyclient.PoolInternalV1betaStoragePoolStateDELETING || dstPool.StoragePoolState.Value == googleproxyclient.PoolInternalV1betaStoragePoolStateCREATING {
		return true
	}
	return false
}

// isPoolInONTAPMode checks if the pool is configured in ONTAP mode
func isPoolInONTAPMode(pool *googleproxyclient.PoolInternalV1beta) bool {
	return pool.Mode.Set && pool.Mode.Value == common.ONTAPMode
}

func _getVolume(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeResourceId string) (googleproxyclient.VolumeV1beta, error) {
	logger := util.GetLogger(ctx)
	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, token, logger)
	params := googleproxyclient.V1betaListVolumesParams{}
	params.LocationId = locationID
	params.ProjectNumber = projectNumber
	params.XCorrelationID = googleproxyclient.OptString{Value: *xCorrelationID, Set: true}

	response, err := googleProxyClient.Invoker.V1betaListVolumes(ctx, params)
	if err != nil {
		return googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrListVolumes, err)
	}

	for _, vol := range response.(*googleproxyclient.V1betaListVolumesOK).Volumes {
		if volumeResourceId == vol.ResourceId {
			return vol, nil
		}
	}
	return googleproxyclient.VolumeV1beta{}, utilErrors.NewNotFoundErr("Volume", &volumeResourceId)
}

func _internalGetReplicationCount(ctx context.Context, basePath string, projectNumber string, locationID string, poolID string, jwt string, storageClass, serviceLevel string) (int, error) {
	logger := util.GetLogger(ctx)
	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwt, logger)
	params := googleproxyclient.V1betaGetReplicationCountParams{}
	params.LocationId = locationID
	params.ProjectNumber = projectNumber
	params.PoolID = googleproxyclient.NewOptString(poolID)
	params.StorageClass = googleproxyclient.OptStorageClassQueryParameter{Value: googleproxyclient.StorageClassQueryParameterSoftware, Set: true}
	params.ServiceLevel = []googleproxyclient.ServiceLevelQueryParameterItem{googleproxyclient.ServiceLevelQueryParameterItemFlex}
	response, err := googleProxyClient.Invoker.V1betaGetReplicationCount(ctx, params)
	if err != nil {
		return 0, nil
	}
	return response.(*googleproxyclient.V1betaGetReplicationCountOK).ReplicationCount, nil
}

// _getInRegionQuotaState returns the CCFE in-region replication quota limit and the number of
// existing non-hybrid in-region pairs for the given project.
func _getInRegionQuotaState(ctx context.Context, se database.Storage, locationID, projectNumber, callbackToken string) (totalInRegionQuota, existingInRegionReplications int, err error) {
	logger := util.GetLogger(ctx)
	totalInRegionQuota, err = getQuotaLimit(ctx, logger, locationID, projectNumber, callbackToken, common.ResourceTypeInRegionReplication)
	if err != nil {
		logger.Error("Get in-region replication quota limit error", "error", err, "projectNumber", projectNumber)
		return 0, 0, errors.NewVCPError(errors.ErrGetReplicationQuotaLimitInternal, err)
	}
	existingInRegionReplications, err = internalCountInRegionReplications(ctx, se, projectNumber)
	if err != nil {
		return 0, 0, errors.NewVCPError(errors.ErrValidateCreateReplicationCvpInternalGetReplicationCount, err)
	}
	return totalInRegionQuota, existingInRegionReplications, nil
}

// _internalCountInRegionReplications returns non-hybrid in-region replication pairs for the given
// project. Regional DB scope limits rows to the local region; pairs count when source and dest share a region.
func _internalCountInRegionReplications(ctx context.Context, se database.Storage, projectNumber string) (int, error) {
	logger := util.GetLogger(ctx)

	account, err := se.GetAccount(ctx, projectNumber)
	if err != nil {
		logger.Error("Failed to get account for in-region replication count", "projectNumber", projectNumber, "error", err)
		return 0, err
	}

	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("account_id", "=", account.ID))
	replications, err := se.ListVolumeReplications(ctx, *filter, database.QueryDepthZero)
	if err != nil {
		logger.Error("Failed to list volume replications for in-region count", "error", err)
		return 0, err
	}

	count := 0
	for _, replication := range replications {
		if replication == nil || replication.HybridReplicationAttributes != nil || replication.ReplicationAttributes == nil {
			continue
		}
		sourceRegion, _, err := InternalParseRegionAndZone(replication.ReplicationAttributes.SourceLocation)
		if err != nil {
			logger.Warnf("Skipping replication with invalid source location for in-region count: %v", err)
			continue
		}
		destinationRegion, _, err := InternalParseRegionAndZone(replication.ReplicationAttributes.DestinationLocation)
		if err != nil {
			logger.Warnf("Skipping replication with invalid destination location for in-region count: %v", err)
			continue
		}
		if sourceRegion == destinationRegion {
			count++
		}
	}
	return count, nil
}

func _internalGetVolumeCount(ctx context.Context, basePath string, projectNumber string, locationID string, poolID string, jwt string, storageClass, serviceLevel string) (int, error) {
	logger := util.GetLogger(ctx)
	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwt, logger)
	params := googleproxyclient.V1betaGetVolumeCountParams{}
	params.LocationId = locationID
	params.ProjectNumber = projectNumber
	params.PoolID = googleproxyclient.NewOptString(poolID)
	params.StorageClass = googleproxyclient.NewOptStorageClassQueryParameter(googleproxyclient.StorageClassQueryParameterSoftware)
	params.ServiceLevel = []googleproxyclient.ServiceLevelQueryParameterItem{googleproxyclient.ServiceLevelQueryParameterItemFlex}
	response, err := googleProxyClient.Invoker.V1betaGetVolumeCount(ctx, params)
	if err != nil {
		return 0, nil
	}
	return response.(*googleproxyclient.V1betaGetVolumeCountOK).VolumeCount, nil
}

func _getDestinationPool(ctx context.Context, destBasePath string, token string, remoteLocationID string, projectNumber string, xCorrelationID *string, name string) (*googleproxyclient.PoolInternalV1beta, error) {
	logger := util.GetLogger(ctx)

	logger.Debug(
		"getDestinationPool",
		"destBasePath", destBasePath,
		"projectNumber", projectNumber,
		"remoteLocationID", remoteLocationID,
		"name", name,
	)

	googleProxyClient := googleproxyclient.GetGProxyClient(destBasePath, token, logger)
	params := googleproxyclient.V1betaInternalDescribePoolParams{
		ProjectNumber:  projectNumber,
		LocationId:     remoteLocationID,
		PoolName:       name,
		XCorrelationID: googleproxyclient.OptString{Value: *xCorrelationID, Set: true},
	}

	response, err := googleProxyClient.Invoker.V1betaInternalDescribePool(ctx, params)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrListPools, err)
	}

	if serverErr, ok := response.(*googleproxyclient.V1betaInternalDescribePoolInternalServerError); ok {
		logger.Error("Internal server error from destination pool describe", "message", serverErr.Message)
		return nil, errors.NewVCPError(errors.ErrInternalServerError, fmt.Errorf("internal server error while fetching destination pool %s: %s", name, serverErr.Message))
	}

	if pool, ok := response.(*googleproxyclient.PoolInternalV1beta); ok {
		if pool.GetHasActiveClusterUpgrade().IsSet() && pool.GetHasActiveClusterUpgrade().Value {
			typeErr := errors.NewVCPError(
				errors.ErrStoragePoolTemporarilyUnavailable, errors.New("destination storage pool is temporarily unavailable, please try again later"))
			logger.Error("Destination pool is temporarily unavailable (cluster upgrade in progress)", "error", typeErr)
			return nil, typeErr
		}
		return pool, nil
	}

	payloadError := &models.Error{Code: float64(404), Message: fmt.Sprintf("Error fetching pool - Pool %s not found", name)}
	return nil, errors.NewVCPError(errors.ErrGetPoolNotFound, &pools.V1betaListPoolsNotFound{Payload: payloadError})
}

func _getReplicationJobs(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, poolId string) ([]googleproxyclient.InternalJobV1beta, error) {
	logger := util.GetLogger(ctx)

	logger.Debug(
		"getReplicationJobs",
		"destBasePath", basePath,
		"projectNumber", projectNumber,
		"locationID", locationID,
		"poolId", poolId,
	)

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, token, logger)
	params := googleproxyclient.V1betaInternalGetReplicationJobsParams{}
	params.ProjectNumber = projectNumber
	params.LocationId = locationID
	params.PoolUUID = googleproxyclient.NewOptString(poolId)
	params.XCorrelationID = googleproxyclient.OptString{Value: *xCorrelationID, Set: true}

	getReplicationJobsResponse, err := googleProxyClient.Invoker.V1betaInternalGetReplicationJobs(ctx, params)
	if err != nil {
		return nil, err
	}

	jobs := getReplicationJobsResponse.(*googleproxyclient.V1betaInternalGetReplicationJobsOK).Jobs

	return jobs, nil
}

// Validates that account does not already have a replication with same resourceId
func _validateReplicationResourceId(ctx context.Context, projectNumber string, paramReplicationResourceId string, paramsVolumeResourceId string, se database.Storage) error {
	account, err := se.GetAccount(ctx, projectNumber)
	if err != nil {
		return err
	}
	replications, err := se.GetVolumeReplicationByProjectId(ctx, account.ID)
	if err != nil {
		return err
	}

	for _, replication := range replications {
		ccfeReplicationSplit := strings.Split(replication.Uri, "/")
		replicationResourceID := ccfeReplicationSplit[len(ccfeReplicationSplit)-1]
		volumeName := ccfeReplicationSplit[5]
		if replicationResourceID == paramReplicationResourceId && volumeName == paramsVolumeResourceId {
			return fmt.Errorf("replication resourceId already in use")
		}
	}

	return nil
}

func _verifyHybridParameters(ctx context.Context, params *common.EstablishReplicationPeeringParams,
	hybridReplicationParameters datamodel.HybridReplicationAttribute) error {
	if hybridReplicationParameters.PeerSvmName == params.PeerSvmName &&
		hybridReplicationParameters.PeerVolumeName == params.PeerVolumeName {
		return nil
	}
	return fmt.Errorf("provided hybrid Replication parameters do not match with existing hybrid Replication parameters")
}

func _isClusterPeeringStateValid(ctx context.Context, replication *datamodel.VolumeReplication) bool {
	logger := util.GetLogger(ctx)
	logger.Debugf("verifying cluster peering state for replication %s", replication.Name)
	if replication.ClusterPeer != nil && replication.ClusterPeer.State == datamodel.CvpClusterPeeringStatusPEERED {
		return true
	}
	if replication.HybridReplicationAttributes.Status != datamodel.HybridReplicationStatusPendingClusterPeer &&
		replication.HybridReplicationAttributes.Status != datamodel.HybridReplicationStatusPendingSVMPeer {
		logger.Error("Invalid hybrid replication status for establishing peering",
			common.String("replicationUUID", replication.UUID),
			common.String("status", string(replication.HybridReplicationAttributes.Status)))
		return true
	}
	return false
}

func _listVolumeReplicationsByCCFEURI(ctx context.Context, se database.Storage, accountID int64, ccfeURI string, queryDepth int) ([]*datamodel.VolumeReplication, error) {
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("account_id", "=", accountID),
		utils2.NewFilterCondition("uri", "=", ccfeURI))
	replicationDb, err := se.ListVolumeReplications(ctx, *filter, queryDepth)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrDatabaseDataReadError, err)
	}
	return replicationDb, nil
}

func _verifyEstablishPeering(ctx context.Context, params *common.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	replicationDb, err := listVolumeReplicationsByCCFEURI(ctx, se, accountID, ccfeURI, database.QueryDepthOne)
	if err != nil {
		return nil, err
	}
	if len(replicationDb) == 0 {
		logger.Error("Replication not found in database", common.String("ccfeURI", ccfeURI))
		return nil, utilErrors.NewUserInputValidationErr("No replication found for the given URI")
	}
	replication := replicationDb[0]
	if replication.HybridReplicationAttributes == nil {
		logger.Error("HybridReplicationAttributes is nil for replication", common.String("ccfeURI", ccfeURI))
		return nil, utilErrors.NewUserInputValidationErr("Replication does not have hybrid replication attributes")
	}
	err = verifyHybridParameters(ctx, params, *replication.HybridReplicationAttributes)
	if err != nil {
		return nil, utilErrors.NewUserInputValidationErr(err.Error())
	}
	if isClusterPeeringStateValid(ctx, replication) {
		return nil, utilErrors.NewUserInputValidationErr("cluster peering is already established")
	}

	return replication, nil
}

// hybridReplicationJobsInProcess checks for active replication jobs for the given account and pool
// to prevent conflicts during hybrid replication volume creation
func _hybridReplicationJobsInProcess(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeUri string) (string, error) {
	// Define replication job types to check for
	// Create filter conditions for replication jobs
	logger := util.GetLogger(ctx)
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("resource_name", "=", ccfeUri),
		utils2.NewFilterCondition("type", "in", replicationJobTypes),
		utils2.NewFilterCondition("state", "in", activeJobStates),
	)

	// Get jobs matching the filter conditions
	dbJobs, err := se.GetJobsWithCondition(ctx, *filter)
	if err != nil {
		logger.Errorf("Failed to get replication jobs with conditions: %v. Error: %v", filter, err)
		return "", err
	}

	// Check for active replication jobs for this specific pool
	for _, job := range dbJobs {
		if job.JobAttributes != nil && job.JobAttributes.PoolUUID == poolUUID {
			logger.Warnf("Active replication job found for pool %s: job_type=%s, job_state=%s, job_uuid=%s",
				poolUUID, job.Type, job.State, job.UUID)
			return job.UUID, nil
		}
	}
	logger.Infof("No active replication jobs found for pool %s, proceeding with hybrid replication volume creation", poolUUID)
	return "", nil
}

// checkActiveReplicationJobs checks for active replication jobs for the given account and pool
// to prevent conflicts during hybrid replication volume creation
func _checkActiveReplicationJobs(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeUri string) error {
	// Define replication job types to check for
	// Create filter conditions for replication jobs
	logger := util.GetLogger(ctx)
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("account_id", "=", accountID),
		utils2.NewFilterCondition("type", "in", replicationJobTypes),
		utils2.NewFilterCondition("state", "in", activeJobStates),
	)

	// Get jobs matching the filter conditions
	dbJobs, err := se.GetJobsWithCondition(ctx, *filter)
	if err != nil {
		logger.Errorf("Failed to get replication jobs with conditions: %v. Error: %v", filter, err)
		return err
	}
	var jobs []*coreModels.Job

	// Check for active replication jobs for this specific pool
	for _, job := range dbJobs {
		if job.JobAttributes != nil && job.JobAttributes.PoolUUID == poolUUID {
			logger.Warnf("Active replication job found for pool %s: job_type=%s, job_state=%s, job_uuid=%s",
				poolUUID, job.Type, job.State, job.UUID)
			jobs = append(jobs, common.ConvertDatastoreOperationToModel(job))
		}
	}
	for _, j := range jobs {
		if j.Type == datamodel.JobTypeCreateHybridReplication || j.Type == datamodel.JobTypeHybridReplicationEstablishPeering {
			// this make sure only one hybrid replication creation is in progress for a pool
			return utilErrors.NewUserInputValidationErr("There is an active replication operation in progress for this pool. Please wait until the operation has finished and try again later.")
		} else if j.ResourceName == ccfeUri {
			// this make sure no other operation is in progress for this replication
			return utilErrors.NewUserInputValidationErr("Another operation against this replication is in progress. Please wait until the operation has finished and try again later.")
		}
	}
	logger.Infof("No active replication jobs found for pool %s, proceeding with hybrid replication volume creation", poolUUID)
	return nil
}

// _createReplicationObjects return a dummy replication objects for expectedResponseCreateReplication endpoint to return
func _createReplicationObjects(event *CreateReplicationEvent, remotelocation, region, remoteRegion string) (*datamodel.VolumeReplication, error) {
	// projects/netapp-prod-prs-14/locations/northAmerica-northeast1/volumes/vol-1/replications/replication-1
	ccfeReplicationUri := fmt.Sprintf("projects/%s/locations/%s/volumes/%s/replications/%s", event.SourceProjectNumber, event.LocationID, event.VolumeResourceID, *event.CreateReplicationParams.ResourceID)

	CcfeRemoteUri := fmt.Sprintf("projects/%s/locations/%s/volumes/%s/replications/%s", event.DestinationProjectNumber, remotelocation, event.CreateReplicationParams.DestinationVolumeParameters.VolumeID, *event.CreateReplicationParams.ResourceID)

	sourceVolumeUUID, err := uuid.Parse(event.SourceVolume.UUID)
	if err != nil {
		return nil, err
	}

	expectedDbReplication := &datamodel.VolumeReplication{
		Uri:       ccfeReplicationUri,
		RemoteUri: CcfeRemoteUri,
		BaseModel: datamodel.BaseModel{
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Name:        *event.CreateReplicationParams.ResourceID,
		Description: nillable.GetString(event.CreateReplicationParams.Description, ""),
	}
	replicationAttributes := datamodel.ReplicationDetails{
		SourceVolumeUUID:    sourceVolumeUUID.String(),
		SourceVolumeName:    event.SourceVolume.Name,
		SourceLocation:      event.LocationID,
		DestinationLocation: event.DestinationLocationID,
		EndpointType:        models.VolumeReplicationCVPV1betaEndpointTypeSrc,
		ReplicationSchedule: string(MapCCFERescheduleToInternalReplicationSchedule(gcpgenserver.ReplicationV1betaReplicationSchedule(*event.CreateReplicationParams.ReplicationSchedule))),
		SourcePoolUUID:      event.SourcePool.UUID,
		Labels:              convertLabelsMapToJSONB(event.CreateReplicationParams.Labels),
	}
	expectedDbReplication.ReplicationAttributes = &replicationAttributes

	return expectedDbReplication, nil
}

// _validateLabels will loop through the label map and validate labels according to Google requirements
func _validateLabels(labels map[string]string) error {
	_, err := json.Marshal(labels)
	if err != nil {
		return errors.NewVCPError(errors.ErrLabelsMarshalFailure, fmt.Errorf("unable to marshal labels"))
	}

	if len(labels) > 64 {
		return errors.NewVCPError(errors.ErrLabelsCountExceeded, fmt.Errorf("invalid label count"))
	}

	for k, v := range labels {
		if len(k) == 0 {
			return errors.NewVCPError(errors.ErrLabelsKeyRequired, fmt.Errorf("key is required in label"))
		}
		if len(strings.Split(k, "")) > maxRuneCount {
			return errors.NewVCPError(errors.ErrLabelsKeyTooLongCharacters, fmt.Errorf("label key '%s' is too long (length can't exceed %d characters)", k, maxRuneCount))
		}
		if len(k) > maxByteCount {
			return errors.NewVCPError(errors.ErrLabelsKeyTooLongBytes, fmt.Errorf("label key '%s' is too long (encoded length can't exceed %d bytes)", k, maxByteCount))
		}
		if len(strings.Split(v, "")) > maxRuneCount {
			return errors.NewVCPError(errors.ErrLabelsValueTooLongCharacters, fmt.Errorf("label value '%s' is too long (length can't exceed %d characters)", v, maxRuneCount))
		}
		if len(v) > maxByteCount {
			return errors.NewVCPError(errors.ErrLabelsValueTooLongBytes, fmt.Errorf("label value '%s' is too long (encoded length can't exceed %d bytes)", v, maxByteCount))
		}
	}
	return nil
}

func _replicationJobInProcess(ctx context.Context, srcProjectNumber string, destProjectNumber string, srcBasePath string, destBasePath string, srcLocationID string, destLocationId, srcToken string, destToken string, ccfeUri string, remoteCcfeUri string, srcPoolId, dstPoolId string, correlationId *string) error {
	logger := util.GetLogger(ctx)
	if srcBasePath != "" {
		srcJobs, err := getReplicationJobs(ctx, srcBasePath, srcToken, srcLocationID, srcProjectNumber, correlationId, srcPoolId)
		if err != nil {
			logger.Error("ListCvpReplicationJobsInProcessing source error", "error", err)
			return errors.NewVCPError(errors.ErrGetLocalReplicationJobs, err)
		}
		if len(srcJobs) > 0 {
			for _, j := range srcJobs {
				if j.ResourceName.Value == ccfeUri || j.ResourceName.Value == remoteCcfeUri {
					return errors.NewVCPError(errors.ErrorCvpReplicationJobAlreadyInProcess, errors.New("Another operation against this replication is in progress. Please wait until the operation has finished and try again later."))
				}
			}
		}
	}

	if destBasePath != "" {
		destJobs, err := getReplicationJobs(ctx, destBasePath, destToken, destLocationId, destProjectNumber, correlationId, dstPoolId)
		if err != nil {
			logger.Error("ListCvpReplicationJobsInProcessing destination error", "error", err)
			return errors.NewVCPError(errors.ErrGetRemoteReplicationJobs, err)
		}
		if len(destJobs) > 0 {
			for _, j := range destJobs {
				// edge case during reverse resume when replicationEventBase.CCFEUri == ccfeUri while job is still in progress.
				if j.ResourceName.Value == remoteCcfeUri || j.ResourceName.Value == ccfeUri {
					return errors.NewVCPError(errors.ErrorCvpReplicationJobAlreadyInProcess, errors.New("Another operation against this replication is in progress. Please wait until the operation has finished and try again later."))
				}
			}
		}
	}
	return nil
}

// URI example: projects/458122799691/locations/us-central1/pools/pool-name
func _validateStoragePoolUri(uri string) error {
	uriList := strings.Split(uri, "/")
	if len(uriList) < 5 {
		return fmt.Errorf("storagePool should match %s", storageUriRegex)
	}

	reg, err := regexpCompile(storageUriRegex)
	if err != nil {
		return fmt.Errorf("storagePool should match %s", storageUriRegex)
	}

	valid := reg.MatchString(uri)
	if !valid {
		return fmt.Errorf("storagePool should match %s", storageUriRegex)
	}

	return nil
}

// GetCCFEURI returns the CCFE URI for a replication
func GetCCFEURI(projectNumber, location, volumeName, replicationName string) string {
	out := fmt.Sprintf("projects/%s/locations/%s/volumes/%s/replications/%s", projectNumber, location, volumeName, replicationName)
	return out
}

func _validateReplicationParams(ctx context.Context, event *CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*coreModels.VolumeReplication, *string, error) {
	logger := util.GetLogger(ctx)
	ccfeURI := internalUtilGetCCFEURI(event.AccountName, event.Location, event.VolumeResourceID, event.ReplicationResourceID)
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("account_id", "=", accountID),
		utils2.NewFilterCondition("uri", "=", ccfeURI))
	replicationDb, err := se.ListVolumeReplications(ctx, *filter, database.QueryDepthOne)
	if err != nil {
		return nil, nil, errors.NewVCPError(errors.ErrDatabaseDataReadError, err)
	}
	if len(replicationDb) == 0 {
		logger.Error("Replication not found in database", "ccfeURI", ccfeURI)
		return nil, nil, utilErrors.NewUserInputValidationErr("No replication found for the given URI")
	}
	replication := replicationDb[0]

	remoteProject := event.AccountName
	if replication.RemoteUri != "" {
		remoteProject, err = utilsParseProjectNumberFromURI(replication.RemoteUri)
		if err != nil {
			logger.Error("Parse Remote URI Error", "error", err)
			return nil, nil, errors.NewVCPError(errors.ErrProjectParsingError, err)
		}
	}

	event.SourceProjectNumber, event.DestinationProjectNumber = event.AccountName, remoteProject
	if replication.ReplicationAttributes.EndpointType == datamodel.DstEndpoint {
		event.SourceProjectNumber, event.DestinationProjectNumber = remoteProject, event.AccountName
	}

	srcToken, err := InternalUtilGetSignedToken(event.SourceProjectNumber)
	if err != nil {
		logger.Error("Get Signed Token Error", "error", err)
		return nil, nil, errors.NewVCPError(errors.ErrGetSignedToken, err)
	}

	dstToken := srcToken
	if event.DestinationProjectNumber != event.SourceProjectNumber {
		// if remoteProject is not the same as the projectNumber, we need to get a new token for the remote project
		dstToken, err = InternalUtilGetSignedToken(event.DestinationProjectNumber)
		if err != nil {
			logger.Error("Get Signed Token Error For Remote Project", "error", err)
			return nil, nil, errors.NewVCPError(errors.ErrGetSignedToken, err)
		}
	}

	var srcBasePath string
	if replication.ReplicationAttributes.SourceLocation != remoteRegionCustomer {
		sourceRegion, _, parseError := InternalParseRegionAndZone(replication.ReplicationAttributes.SourceLocation)
		if parseError != nil {
			logger.Error("Parse Source Location Error")
			return nil, nil, errors.NewVCPError(errors.ErrParseSourceLocation, errors.New(parseError.Error()))
		}

		srcBasePath, err = InternalUtilGetPairedRegionURI(sourceRegion)
		if err != nil {
			logger.Error("Get Paired Source Region Uri error", "error", err)
			return nil, nil, errors.NewVCPError(errors.ErrGetSrcBasePath, err)
		}
	}

	var dstBasePath string
	if replication.ReplicationAttributes.DestinationLocation != remoteRegionCustomer {
		destRegion, _, parseError := InternalParseRegionAndZone(replication.ReplicationAttributes.DestinationLocation)
		if parseError != nil {
			logger.Error("Parse Destination Location Error", "error", errors.New(parseError.Error()))
			return nil, nil, errors.NewVCPError(errors.ErrParseDestinationLocation, errors.New(parseError.Error()))
		}

		dstBasePath, err = InternalUtilGetPairedRegionURI(destRegion)
		if err != nil {
			logger.Error("Get Paired Destination Region Uri error", "error", err)
			return nil, nil, errors.NewVCPError(errors.ErrGetDstBasePath, err)
		}
	}

	// Set the ReplicationModel before checking for duplicate jobs
	event.ReplicationModel = replication
	event.SrcBasePath = srcBasePath
	event.DstBasePath = dstBasePath
	event.SrcToken = srcToken
	event.DstToken = dstToken

	// check for duplicate jobs
	existingJob, err := se.CheckAndFetchDuplicateJobs(ctx, jobType, utils.GetCoRelationIDFromContext(ctx))
	if err != nil {
		return nil, nil, err
	}
	if existingJob != nil {
		if event.DstBasePath == "" {
			srcReplication, err := getReplication(ctx, event.SrcBasePath, event.SourceProjectNumber, event.ReplicationModel.ReplicationAttributes.SourceLocation, event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID, event.SrcToken)
			if err != nil || srcReplication == nil {
				logger.Error("getReplication error", "error", err)
				return nil, nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
			}
			return srcReplication, &existingJob.UUID, nil
		}
		dstReplication, err := getReplication(ctx, event.DstBasePath, event.DestinationProjectNumber, event.ReplicationModel.ReplicationAttributes.DestinationLocation, event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID, event.DstToken)
		if err != nil || dstReplication == nil {
			logger.Error("getReplication error", "error", err)
			return nil, nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
		}
		return dstReplication, &existingJob.UUID, nil
	}

	if !isCleanup {
		// Check if replication job is in process
		err = replicationJobInProcess(ctx, event.SourceProjectNumber, event.DestinationProjectNumber, srcBasePath, dstBasePath, replication.ReplicationAttributes.SourceLocation, replication.ReplicationAttributes.DestinationLocation, srcToken, dstToken, replication.Uri, replication.RemoteUri, replication.ReplicationAttributes.SourcePoolUUID, replication.ReplicationAttributes.DestinationPoolUUID, event.XCorrelationID)
		if err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func _verifyDstReplicationResume(ctx context.Context, event *ResumeReplicationEvent) (*coreModels.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	replication := event.ReplicationModel

	if IsSrcForHybridReplication(event.ReplicationModel) {
		if replication.ReplicationAttributes.DestinationReplicationUUID == uuid.Nil.String() && replication.HybridReplicationAttributes.Status != datamodel.HybridReplicationStatusExternalManaged {
			logger.Error("Hybrid Replication needs to be in externally managed state before resuming")
			return nil, utilErrors.NewUserInputValidationErr("Hybrid Replication needs to be in externally managed state before resuming")
		}

		srcReplication, err := getReplication(ctx, event.SrcBasePath, event.SourceProjectNumber, replication.ReplicationAttributes.SourceLocation, replication.ReplicationAttributes.SourceReplicationUUID, event.SrcToken)
		if err != nil || srcReplication == nil {
			logger.Error("getReplication error", "error", err)
			return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
		}

		extOntapPath := getPath(srcReplication.ReplicationAttributes.DestinationSvmName, srcReplication.ReplicationAttributes.DestinationVolumeName)
		gcnvPath := getPath(srcReplication.ReplicationAttributes.SourceSvmName, srcReplication.ReplicationAttributes.SourceVolumeName)
		command := []string{
			"# Please run the following command once on your ONTAP system.",
			fmt.Sprintf("snapmirror resync -source-path %s -destination-path %s", gcnvPath, extOntapPath),
			"# If ran successfully, MirrorState will switch to SnapMirrored after a few minutes. Please check by running:",
			fmt.Sprintf("snapmirror show -source-path %s -destination-path %s", gcnvPath, extOntapPath),
		}
		hybridReplicationUserCommands := models.HybridReplicationUserCommandsV1beta{
			Commands: command,
		}
		srcReplication.HybridReplicationAttributes.HybridReplicationUserCommands = hybridReplicationUserCommands.Commands
		srcReplication.StateDetails = "Please execute the commands on Onprem ONTAP to Resume replication"

		return srcReplication, nil
	}

	if replication.HybridReplicationAttributes != nil && replication.HybridReplicationAttributes.Status != datamodel.HybridReplicationStatusPeered {
		logger.Error("Hybrid Replication needs to be in peered state before resuming")
		return nil, utilErrors.NewUserInputValidationErr("Hybrid Replication needs to be in peered state before resuming")
	}

	dstReplication, err := getReplication(ctx, event.DstBasePath, event.DestinationProjectNumber, event.ReplicationModel.ReplicationAttributes.DestinationLocation, event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID, event.DstToken)
	if err != nil || dstReplication == nil {
		logger.Error("getReplication error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}

	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateMIRRORED {
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Replication mirror state should be %s", models.ReplicationV1betaMirrorStateSTOPPED))
	}

	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateUNINITIALIZED && *dstReplication.RelationshipStatus == datamodel.SnapmirrorRelationshipTransferring {
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Replication relationship status should be %s", models.VolumeReplicationCVPV1betaRelationshipStatusIdle))
	}

	return dstReplication, nil
}

// _verifySourceQuotaRules validates that all quota rules on the source volume are in READY state
// before resuming replication. This ensures quota rules are properly synced during replication resume.
func _verifySourceQuotaRules(ctx context.Context, event *ResumeReplicationEvent) error {
	logger := util.GetLogger(ctx)

	// Extract source volume details from the event
	sourceLocation := event.ReplicationModel.ReplicationAttributes.SourceLocation
	sourceVolumeUUID := event.ReplicationModel.ReplicationAttributes.SourceVolumeUUID
	sourceProjectNumber := event.SourceProjectNumber
	srcBasePath := event.SrcBasePath
	srcToken := event.SrcToken

	// Create Google Proxy client for the source VCP region
	googleProxyClient := googleproxyclient.GetGProxyClient(srcBasePath, srcToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare API parameters
	params := googleproxyclient.V1betaListAllQuotaRulesParams{
		ProjectNumber:  sourceProjectNumber,
		LocationId:     sourceLocation,
		VolumeId:       sourceVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	// Call the VCP API to list quota rules on source
	logger.Infof("Calling VCP API to list quota rules on source volume: volumeUUID=%s, location=%s",
		sourceVolumeUUID, sourceLocation)
	res, err := googleProxyClient.Invoker.V1betaListAllQuotaRules(ctx, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaListAllQuotaRules for source volume: %v", err)
		return fmt.Errorf("failed to list quota rules on source volume: %w", err)
	}

	// Handle response types
	switch r := res.(type) {
	case *googleproxyclient.V1betaListAllQuotaRulesOK:
		logger.Infof("Successfully fetched quota rules from source volume: count=%d, volumeUUID=%s",
			len(r.QuotaRules), sourceVolumeUUID)

		// If no quota rules exist, validation passes
		if len(r.QuotaRules) == 0 {
			logger.Info("No quota rules found on source volume, validation passed")
			return nil
		}

		// Check that all quota rules are in READY state
		var nonReadyQuotaRules []string
		for _, quotaRule := range r.QuotaRules {
			state, ok := quotaRule.State.Get()
			// Quota rules with no state set are invalid and should be treated as fatal error
			if !ok {
				stateDetails, _ := quotaRule.StateDetails.Get()
				logger.Errorf("Quota rule has no state set: resourceId=%s, stateDetails=%s",
					quotaRule.ResourceId, stateDetails)
				errorMsg := fmt.Sprintf("Cannot resume replication: source volume has quota rules not in READY state. "+
					"Please wait for quota rules to complete. Non-ready quota rules: ResourceId: %s, State: (not set), StateDetails: %s",
					quotaRule.ResourceId, stateDetails)
				logger.Error(errorMsg)
				return utilErrors.NewUserInputValidationErr(errorMsg)
			}

			if string(state) != datamodel.LifeCycleStateREADY {
				stateDetails, _ := quotaRule.StateDetails.Get()
				logger.Warnf("Quota rule not in READY state: resourceId=%s, state=%s, stateDetails=%s",
					quotaRule.ResourceId, state, stateDetails)
				nonReadyQuotaRules = append(nonReadyQuotaRules,
					fmt.Sprintf("ResourceId: %s, State: %s, StateDetails: %s",
						quotaRule.ResourceId, state, stateDetails))
			}
		}

		// If there are non-ready quota rules, return an error
		if len(nonReadyQuotaRules) > 0 {
			errorMsg := fmt.Sprintf("Cannot resume replication: source volume has quota rules not in READY state. "+
				"Please wait for quota rules to complete. Non-ready quota rules: %s",
				strings.Join(nonReadyQuotaRules, "; "))
			logger.Error(errorMsg)
			return utilErrors.NewUserInputValidationErr(errorMsg)
		}

		return nil

	case *googleproxyclient.V1betaListAllQuotaRulesBadRequest:
		logger.Errorf("Bad request when listing quota rules on source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleBadRequest, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesUnauthorized:
		logger.Errorf("Unauthorized when listing quota rules on source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleUnauthorized, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesForbidden:
		logger.Errorf("Forbidden when listing quota rules on source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleForbidden, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesNotFound:
		// If volume not found, this is likely a different error - let it propagate
		logger.Errorf("Not found when listing quota rules on source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleNotFound, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesConflict:
		logger.Errorf("Conflict when listing quota rules on source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleConflict, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesUnprocessableEntity:
		logger.Errorf("Unprocessable entity when listing quota rules on source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleBadRequest, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesTooManyRequests:
		logger.Errorf("Too many requests when listing quota rules on source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleTooManyRequests, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesInternalServerError:
		logger.Errorf("Internal server error when listing quota rules on source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleInternalServerError, fmt.Errorf("%s", r.Message))
	default:
		logger.Error("Unexpected response type from Google Proxy when listing quota rules")
		return errors.NewVCPError(errors.ErrInternalServerError, fmt.Errorf("unexpected response type from Google Proxy"))
	}
}

// _verifyDestinationQuotaRules validates that no quota rules on the destination volume are in transitioning states
// (CREATING, UPDATING, DELETING) before resuming replication. This prevents conflicts with ongoing quota rule operations.
func _verifyDestinationQuotaRules(ctx context.Context, event *ResumeReplicationEvent) error {
	logger := util.GetLogger(ctx)

	// Extract destination volume details from the event
	destinationLocation := event.ReplicationModel.ReplicationAttributes.DestinationLocation
	destinationVolumeUUID := event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID
	destinationProjectNumber := event.DestinationProjectNumber
	dstBasePath := event.DstBasePath
	dstToken := event.DstToken

	// Create Google Proxy client for the destination VCP region
	googleProxyClient := googleproxyclient.GetGProxyClient(dstBasePath, dstToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare API parameters
	params := googleproxyclient.V1betaListAllQuotaRulesParams{
		ProjectNumber:  destinationProjectNumber,
		LocationId:     destinationLocation,
		VolumeId:       destinationVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	// Call the VCP API to list quota rules on destination
	logger.Infof("Calling VCP API to list quota rules on destination volume: volumeUUID=%s, location=%s",
		destinationVolumeUUID, destinationLocation)
	res, err := googleProxyClient.Invoker.V1betaListAllQuotaRules(ctx, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaListAllQuotaRules for destination volume: %v", err)
		return fmt.Errorf("failed to list quota rules on destination volume: %w", err)
	}

	// Define transitioning states
	transitioningStates := map[string]bool{
		datamodel.LifeCycleStateCreating: true,
		datamodel.LifeCycleStateUpdating: true,
		datamodel.LifeCycleStateDeleting: true,
	}

	// Handle response types
	switch r := res.(type) {
	case *googleproxyclient.V1betaListAllQuotaRulesOK:
		logger.Infof("Successfully fetched quota rules from destination volume: count=%d, volumeUUID=%s",
			len(r.QuotaRules), destinationVolumeUUID)

		// If no quota rules exist, validation passes
		if len(r.QuotaRules) == 0 {
			logger.Info("No quota rules found on destination volume, validation passed")
			return nil
		}

		// Check that no quota rules are in transitioning states
		var transitioningQuotaRules []string
		for _, quotaRule := range r.QuotaRules {
			state, ok := quotaRule.State.Get()
			// Quota rules with no state set are invalid and should be treated as fatal error
			if !ok {
				stateDetails, _ := quotaRule.StateDetails.Get()
				logger.Errorf("Quota rule has no state set: resourceId=%s, stateDetails=%s",
					quotaRule.ResourceId, stateDetails)
				errorMsg := fmt.Sprintf("Cannot resume replication: destination volume has quota rules in transitioning state. "+
					"Please wait for quota rule operations to complete. Transitioning quota rules: ResourceId: %s, State: (not set), StateDetails: %s",
					quotaRule.ResourceId, stateDetails)
				logger.Error(errorMsg)
				return utilErrors.NewUserInputValidationErr(errorMsg)
			}

			if transitioningStates[string(state)] {
				stateDetails, _ := quotaRule.StateDetails.Get()
				logger.Warnf("Quota rule in transitioning state: resourceId=%s, state=%s, stateDetails=%s",
					quotaRule.ResourceId, state, stateDetails)
				transitioningQuotaRules = append(transitioningQuotaRules,
					fmt.Sprintf("ResourceId: %s, State: %s, StateDetails: %s",
						quotaRule.ResourceId, state, stateDetails))
			}
		}

		// If there are transitioning quota rules, return an error
		if len(transitioningQuotaRules) > 0 {
			errorMsg := fmt.Sprintf("Cannot resume replication: destination volume has quota rules in transitioning state. "+
				"Please wait for quota rule operations to complete. Transitioning quota rules: %s",
				strings.Join(transitioningQuotaRules, "; "))
			logger.Error(errorMsg)
			return utilErrors.NewUserInputValidationErr(errorMsg)
		}

		return nil

	case *googleproxyclient.V1betaListAllQuotaRulesBadRequest:
		logger.Errorf("Bad request when listing quota rules on destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleBadRequest, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesUnauthorized:
		logger.Errorf("Unauthorized when listing quota rules on destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleUnauthorized, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesForbidden:
		logger.Errorf("Forbidden when listing quota rules on destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleForbidden, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesNotFound:
		// If volume not found, this is likely a different error - let it propagate
		logger.Errorf("Not found when listing quota rules on destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleNotFound, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesConflict:
		logger.Errorf("Conflict when listing quota rules on destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleConflict, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesUnprocessableEntity:
		logger.Errorf("Unprocessable entity when listing quota rules on destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleBadRequest, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesTooManyRequests:
		logger.Errorf("Too many requests when listing quota rules on destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleTooManyRequests, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesInternalServerError:
		logger.Errorf("Internal server error when listing quota rules on destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleInternalServerError, fmt.Errorf("%s", r.Message))
	default:
		logger.Error("Unexpected response type from Google Proxy when listing quota rules")
		return errors.NewVCPError(errors.ErrInternalServerError, fmt.Errorf("unexpected response type from Google Proxy"))
	}
}

// _verifyNewSourceQuotaRulesReverse validates that all quota rules on the new source volume
// (current destination) are in READY state before reverse resume replication.
func _verifyNewSourceQuotaRulesReverse(ctx context.Context, event *ReverseReplicationEvent) error {
	logger := util.GetLogger(ctx)

	// Extract new source volume details from the event (current destination becomes new source)
	newSourceLocation := event.ReplicationModel.ReplicationAttributes.DestinationLocation
	newSourceVolumeUUID := event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID
	newSourceProjectNumber := event.DestinationProjectNumber
	dstBasePath := event.DstBasePath
	dstToken := event.DstToken

	// Create Google Proxy client for the new source VCP region (current destination)
	googleProxyClient := googleproxyclient.GetGProxyClient(dstBasePath, dstToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare API parameters
	params := googleproxyclient.V1betaListAllQuotaRulesParams{
		ProjectNumber:  newSourceProjectNumber,
		LocationId:     newSourceLocation,
		VolumeId:       newSourceVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	// Call the VCP API to list quota rules on new source (current destination)
	logger.Infof("Calling VCP API to list quota rules on new source volume (current destination): volumeUUID=%s, location=%s",
		newSourceVolumeUUID, newSourceLocation)
	res, err := googleProxyClient.Invoker.V1betaListAllQuotaRules(ctx, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaListAllQuotaRules for new source volume: %v", err)
		return fmt.Errorf("failed to list quota rules on new source volume: %w", err)
	}

	// Handle response types
	switch r := res.(type) {
	case *googleproxyclient.V1betaListAllQuotaRulesOK:
		logger.Infof("Successfully fetched quota rules from new source volume: count=%d, volumeUUID=%s",
			len(r.QuotaRules), newSourceVolumeUUID)

		// If no quota rules exist, validation passes
		if len(r.QuotaRules) == 0 {
			logger.Info("No quota rules found on new source volume, validation passed")
			return nil
		}

		// Check that all quota rules are in READY state
		var nonReadyQuotaRules []string
		for _, quotaRule := range r.QuotaRules {
			state, ok := quotaRule.State.Get()
			// Quota rules with no state set are invalid and should be treated as fatal error
			if !ok {
				stateDetails, _ := quotaRule.StateDetails.Get()
				logger.Errorf("Quota rule has no state set: resourceId=%s, stateDetails=%s",
					quotaRule.ResourceId, stateDetails)
				errorMsg := fmt.Sprintf("Cannot reverse resume replication: new source volume has quota rules not in READY state. "+
					"Please wait for quota rules to complete. Non-ready quota rules: ResourceId: %s, State: (not set), StateDetails: %s",
					quotaRule.ResourceId, stateDetails)
				logger.Error(errorMsg)
				return utilErrors.NewUserInputValidationErr(errorMsg)
			}

			if string(state) != datamodel.LifeCycleStateREADY {
				stateDetails, _ := quotaRule.StateDetails.Get()
				logger.Warnf("Quota rule not in READY state: resourceId=%s, state=%s, stateDetails=%s",
					quotaRule.ResourceId, state, stateDetails)
				nonReadyQuotaRules = append(nonReadyQuotaRules,
					fmt.Sprintf("ResourceId: %s, State: %s, StateDetails: %s",
						quotaRule.ResourceId, state, stateDetails))
			}
		}

		// If there are non-ready quota rules, return an error
		if len(nonReadyQuotaRules) > 0 {
			errorMsg := fmt.Sprintf("Cannot reverse resume replication: new source volume has quota rules not in READY state. "+
				"Please wait for quota rules to complete. Non-ready quota rules: %s",
				strings.Join(nonReadyQuotaRules, "; "))
			logger.Error(errorMsg)
			return utilErrors.NewUserInputValidationErr(errorMsg)
		}

		return nil

	case *googleproxyclient.V1betaListAllQuotaRulesBadRequest:
		logger.Errorf("Bad request when listing quota rules on new source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleBadRequest, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesUnauthorized:
		logger.Errorf("Unauthorized when listing quota rules on new source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleUnauthorized, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesForbidden:
		logger.Errorf("Forbidden when listing quota rules on new source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleForbidden, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesNotFound:
		// If volume not found, this is likely a different error - let it propagate
		logger.Errorf("Not found when listing quota rules on new source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleNotFound, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesConflict:
		logger.Errorf("Conflict when listing quota rules on new source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleConflict, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesUnprocessableEntity:
		logger.Errorf("Unprocessable entity when listing quota rules on new source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleBadRequest, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesTooManyRequests:
		logger.Errorf("Too many requests when listing quota rules on new source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleTooManyRequests, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesInternalServerError:
		logger.Errorf("Internal server error when listing quota rules on new source: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleInternalServerError, fmt.Errorf("%s", r.Message))
	default:
		logger.Error("Unexpected response type from Google Proxy when listing quota rules")
		return errors.NewVCPError(errors.ErrInternalServerError, fmt.Errorf("unexpected response type from Google Proxy"))
	}
}

// _verifyNewDestinationQuotaRulesReverse validates that no quota rules on the new destination volume
// (current source) are in transitioning states (CREATING, UPDATING, DELETING) before reverse resume replication.
func _verifyNewDestinationQuotaRulesReverse(ctx context.Context, event *ReverseReplicationEvent) error {
	logger := util.GetLogger(ctx)

	// Extract new destination volume details from the event (current source becomes new destination)
	newDestinationLocation := event.ReplicationModel.ReplicationAttributes.SourceLocation
	newDestinationVolumeUUID := event.ReplicationModel.ReplicationAttributes.SourceVolumeUUID
	newDestinationProjectNumber := event.SourceProjectNumber
	srcBasePath := event.SrcBasePath
	srcToken := event.SrcToken

	// Create Google Proxy client for the new destination VCP region (current source)
	googleProxyClient := googleproxyclient.GetGProxyClient(srcBasePath, srcToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare API parameters
	params := googleproxyclient.V1betaListAllQuotaRulesParams{
		ProjectNumber:  newDestinationProjectNumber,
		LocationId:     newDestinationLocation,
		VolumeId:       newDestinationVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	// Call the VCP API to list quota rules on new destination (current source)
	logger.Infof("Calling VCP API to list quota rules on new destination volume (current source): volumeUUID=%s, location=%s",
		newDestinationVolumeUUID, newDestinationLocation)
	res, err := googleProxyClient.Invoker.V1betaListAllQuotaRules(ctx, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaListAllQuotaRules for new destination volume: %v", err)
		return fmt.Errorf("failed to list quota rules on new destination volume: %w", err)
	}

	// Define transitioning states
	transitioningStates := map[string]bool{
		datamodel.LifeCycleStateCreating: true,
		datamodel.LifeCycleStateUpdating: true,
		datamodel.LifeCycleStateDeleting: true,
	}

	// Handle response types
	switch r := res.(type) {
	case *googleproxyclient.V1betaListAllQuotaRulesOK:
		logger.Infof("Successfully fetched quota rules from new destination volume: count=%d, volumeUUID=%s",
			len(r.QuotaRules), newDestinationVolumeUUID)

		// If no quota rules exist, validation passes
		if len(r.QuotaRules) == 0 {
			logger.Info("No quota rules found on new destination volume, validation passed")
			return nil
		}

		// Check that no quota rules are in transitioning states
		var transitioningQuotaRules []string
		for _, quotaRule := range r.QuotaRules {
			state, ok := quotaRule.State.Get()
			// Quota rules with no state set are invalid and should be treated as fatal error
			if !ok {
				stateDetails, _ := quotaRule.StateDetails.Get()
				logger.Errorf("Quota rule has no state set: resourceId=%s, stateDetails=%s",
					quotaRule.ResourceId, stateDetails)
				errorMsg := fmt.Sprintf("Cannot reverse resume replication: new destination volume has quota rules in transitioning state. "+
					"Please wait for quota rule operations to complete. Transitioning quota rules: ResourceId: %s, State: (not set), StateDetails: %s",
					quotaRule.ResourceId, stateDetails)
				logger.Error(errorMsg)
				return utilErrors.NewUserInputValidationErr(errorMsg)
			}

			if transitioningStates[string(state)] {
				stateDetails, _ := quotaRule.StateDetails.Get()
				logger.Warnf("Quota rule in transitioning state: resourceId=%s, state=%s, stateDetails=%s",
					quotaRule.ResourceId, state, stateDetails)
				transitioningQuotaRules = append(transitioningQuotaRules,
					fmt.Sprintf("ResourceId: %s, State: %s, StateDetails: %s",
						quotaRule.ResourceId, state, stateDetails))
			}
		}

		// If there are transitioning quota rules, return an error
		if len(transitioningQuotaRules) > 0 {
			errorMsg := fmt.Sprintf("Cannot reverse resume replication: new destination volume has quota rules in transitioning state. "+
				"Please wait for quota rule operations to complete. Transitioning quota rules: %s",
				strings.Join(transitioningQuotaRules, "; "))
			logger.Error(errorMsg)
			return utilErrors.NewUserInputValidationErr(errorMsg)
		}

		return nil

	case *googleproxyclient.V1betaListAllQuotaRulesBadRequest:
		logger.Errorf("Bad request when listing quota rules on new destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleBadRequest, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesUnauthorized:
		logger.Errorf("Unauthorized when listing quota rules on new destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleUnauthorized, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesForbidden:
		logger.Errorf("Forbidden when listing quota rules on new destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleForbidden, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesNotFound:
		// If volume not found, this is likely a different error - let it propagate
		logger.Errorf("Not found when listing quota rules on new destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleNotFound, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesConflict:
		logger.Errorf("Conflict when listing quota rules on new destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleConflict, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesUnprocessableEntity:
		logger.Errorf("Unprocessable entity when listing quota rules on new destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleBadRequest, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesTooManyRequests:
		logger.Errorf("Too many requests when listing quota rules on new destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleTooManyRequests, fmt.Errorf("%s", r.Message))
	case *googleproxyclient.V1betaListAllQuotaRulesInternalServerError:
		logger.Errorf("Internal server error when listing quota rules on new destination: %s", r.Message)
		return errors.NewVCPError(errors.ErrQuotaRuleInternalServerError, fmt.Errorf("%s", r.Message))
	default:
		logger.Error("Unexpected response type from Google Proxy when listing quota rules")
		return errors.NewVCPError(errors.ErrInternalServerError, fmt.Errorf("unexpected response type from Google Proxy"))
	}
}

func _verifyDstReplicationStop(ctx context.Context, event *StopReplicationEvent) (*coreModels.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	replication := event.ReplicationModel
	if IsSrcForHybridReplication(event.ReplicationModel) {
		if replication.ReplicationAttributes.DestinationReplicationUUID == uuid.Nil.String() && replication.HybridReplicationAttributes.Status != datamodel.HybridReplicationStatusExternalManaged {
			logger.Error("Hybrid Replication needs to be in externally managed state before stopping")
			return nil, utilErrors.NewUserInputValidationErr("Hybrid Replication needs to be in externally managed state before stopping")
		}

		srcReplication, err := getReplication(ctx, event.SrcBasePath, event.SourceProjectNumber, replication.ReplicationAttributes.SourceLocation, replication.ReplicationAttributes.SourceReplicationUUID, event.SrcToken)
		if err != nil || srcReplication == nil {
			logger.Error("getReplication error", "error", err)
			return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
		}

		extOntapPath := getPath(srcReplication.ReplicationAttributes.DestinationSvmName, srcReplication.ReplicationAttributes.DestinationVolumeName)
		gcnvPath := getPath(srcReplication.ReplicationAttributes.SourceSvmName, srcReplication.ReplicationAttributes.SourceVolumeName)
		command := []string{
			"# Please run the following command once on your ONTAP system.",
			fmt.Sprintf("snapmirror break -source-path %s -destination-path %s", gcnvPath, extOntapPath),
			"# If ran successfully, MirrorState will say Broken-Off. Please check by running:",
			fmt.Sprintf("snapmirror show -source-path %s -destination-path %s", gcnvPath, extOntapPath),
		}
		hybridReplicationUserCommands := models.HybridReplicationUserCommandsV1beta{
			Commands: command,
		}
		srcReplication.HybridReplicationAttributes.HybridReplicationUserCommands = hybridReplicationUserCommands.Commands
		srcReplication.StateDetails = "Please execute the commands on Onprem ONTAP to Stop replication"

		return srcReplication, nil
	}

	if replication.HybridReplicationAttributes != nil && replication.HybridReplicationAttributes.Status != datamodel.HybridReplicationStatusPeered {
		logger.Error("Hybrid Replication needs to be in peered state before stopping")
		return nil, utilErrors.NewUserInputValidationErr("Hybrid Replication needs to be in peered state before stopping")
	}

	dstReplication, err := getReplication(ctx, event.DstBasePath, event.DestinationProjectNumber, event.ReplicationModel.ReplicationAttributes.DestinationLocation, event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID, event.DstToken)
	if err != nil || dstReplication == nil {
		logger.Error("getReplication error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}

	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateSTOPPED || *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateABORTED {
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Replication is already in %s state", *dstReplication.MirrorState))
	}

	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateUNINITIALIZED {
		if *dstReplication.RelationshipStatus == strings.ToLower(models.ReplicationV1betaMirrorStateTRANSFERRING) && !event.ForceStop {
			return nil, utilErrors.NewUserInputValidationErr("Replication in preparing state. Please try again later")
		}
	}

	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateMIRRORED && *dstReplication.RelationshipStatus == strings.ToLower(models.ReplicationV1betaMirrorStateTRANSFERRING) && !event.ForceStop {
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Replication relationship status is in %s state", strings.ToLower(models.ReplicationV1betaMirrorStateTRANSFERRING)))
	}

	return dstReplication, nil
}

func _verifyDstReplicationReverse(ctx context.Context, event *ReverseReplicationEvent) (*coreModels.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	replication := event.ReplicationModel
	if IsSrcForHybridReplication(event.ReplicationModel) {
		if replication.ReplicationAttributes.DestinationReplicationUUID == uuid.Nil.String() && replication.HybridReplicationAttributes.Status != datamodel.HybridReplicationStatusExternalManaged {
			logger.Error("Hybrid Replication needs to be in externally managed state before reversing")
			return nil, utilErrors.NewUserInputValidationErr("Hybrid Replication needs to be in externally managed state before reversing")
		}

		srcReplication, err := getReplication(ctx, event.SrcBasePath, event.SourceProjectNumber, replication.ReplicationAttributes.SourceLocation, replication.ReplicationAttributes.SourceReplicationUUID, event.SrcToken)
		if err != nil || srcReplication == nil {
			logger.Error("getReplication error", "error", err)
			return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
		}
		return srcReplication, nil
	}

	if replication.HybridReplicationAttributes != nil && replication.HybridReplicationAttributes.Status != datamodel.HybridReplicationStatusPeered {
		logger.Error("Hybrid Replication needs to be in peered state before reversing")
		return nil, utilErrors.NewUserInputValidationErr("Hybrid Replication needs to be in peered state before reversing")
	}

	dstReplication, err := getReplication(ctx, event.DstBasePath, event.DestinationProjectNumber, event.ReplicationModel.ReplicationAttributes.DestinationLocation, event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID, event.DstToken)
	if err != nil || dstReplication == nil {
		logger.Error("getReplication error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}

	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateMIRRORED {
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Replication mirror state should be %s", models.ReplicationV1betaMirrorStateSTOPPED))
	}

	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateUNINITIALIZED {
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Replication relationship status should be %s", models.VolumeReplicationCVPV1betaRelationshipStatusIdle))
	}

	return dstReplication, nil
}

func _validateReplicationUpdate(ctx context.Context, event *UpdateReplicationEvent) (*coreModels.VolumeReplication, error) {
	logger := util.GetLogger(ctx)

	if event.ReplicationSchedule == nil && event.Description == nil && event.Labels == nil && event.ClusterLocation == nil {
		logger.Error("empty replication update payload")
		return nil, errors.NewVCPError(errors.ErrorEmptyUpdateReplicationPayload, errors.New("empty replication update payload"))
	}

	if event.ReplicationSchedule != nil && *event.ReplicationSchedule == models.ReplicationV1betaReplicationScheduleREPLICATIONSCHEDULEUNSPECIFIED {
		logger.Error("replicationSchedule is UNSPECIFIED for update replication")
		return nil, errors.NewVCPError(errors.ErrorReplicationScheduleUnspecified, errors.New("Invalid replication schedule provided."))
	}

	// Check if clusterLocation is provided for non-hybrid replication
	if event.ClusterLocation != nil && event.ReplicationModel.HybridReplicationAttributes == nil {
		logger.Error("Cluster location is not supported for non-hybrid replication")
		return nil, utilErrors.NewUserInputValidationErr("Cluster location is not supported for non-hybrid replication")
	}

	if event.ReplicationModel.HybridReplicationAttributes != nil {
		if nillable.GetString(event.ReplicationModel.HybridReplicationAttributes.HybridReplicationType, "") == string(models.HybridReplicationParametersV1betaHybridReplicationTypeREVERSEONPREMREPLICATION) {
			logger.Error("Update is not allowed when Hybrid Replication is externally managed")
			return nil, utilErrors.NewUserInputValidationErr("These fields cannot be updated when Hybrid Replication is Externally Managed")
		}
		if event.ReplicationModel.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingRemoteResync || event.ReplicationModel.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingSVMPeer || event.ReplicationModel.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingClusterPeer {
			logger.Error("Hybrid Replication can not be updated in transition state")
			return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Hybrid Replication can not be updated in the transition state - %s", event.ReplicationModel.HybridReplicationAttributes.Status))
		}
		// Check if replication schedule is hourly for hybrid rep type migration
		if nillable.GetString(event.ReplicationModel.HybridReplicationAttributes.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION) {
			if event.ReplicationSchedule != nil && *event.ReplicationSchedule != models.ReplicationV1betaReplicationScheduleHOURLY {
				logger.Error("Invalid replication schedule for hybrid rep type migration - must be hourly")
				return nil, utilErrors.NewUserInputValidationErr("Invalid replication schedule provided.")
			}
		}
	}

	if event.Labels != nil {
		err := _validateLabels(event.Labels)
		if err != nil {
			logger.Error("validateLabels error", common.Error(err))
			return nil, err
		}
	}

	dstReplication, err := getReplication(ctx, event.DstBasePath, event.DestinationProjectNumber, event.ReplicationModel.ReplicationAttributes.DestinationLocation, event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID, event.DstToken)
	if err != nil || dstReplication == nil {
		logger.Error("getReplication error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}

	return dstReplication, nil
}

func _verifyDstVolume(ctx context.Context, attributes *datamodel.ReplicationDetails, srcBasePath string, destBasePath string, srcToken string, dstToken string, srcProjectNumber, dstProjectNumber string, correlationId *string, isReverse bool) (googleproxyclient.VolumeV1beta, googleproxyclient.VolumeV1beta, error) {
	logger := util.GetLogger(ctx)
	var srcVolume, dstVolume googleproxyclient.VolumeV1beta
	var err error
	// if isReverse is true, swap source and destination volume for verification
	// this is because during reverse replication, source volume becomes destination and vice versa
	if isReverse {
		srcVolume, err = describeVolume(ctx, destBasePath, dstToken, attributes.DestinationLocation, dstProjectNumber, correlationId, attributes.DestinationVolumeUUID)
	} else {
		srcVolume, err = describeVolume(ctx, srcBasePath, srcToken, attributes.SourceLocation, srcProjectNumber, correlationId, attributes.SourceVolumeUUID)
	}
	if err != nil {
		if !utilErrors.IsNotFoundErr(err) {
			logger.Error("getSourceVolume error", "error", err)
			return googleproxyclient.VolumeV1beta{}, googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrDescribingVolume, err)
		}
		return googleproxyclient.VolumeV1beta{}, googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrVolumeNotFound, err)
	}

	if isReverse {
		dstVolume, err = describeVolume(ctx, srcBasePath, srcToken, attributes.SourceLocation, srcProjectNumber, correlationId, attributes.SourceVolumeUUID)
	} else {
		dstVolume, err = describeVolume(ctx, destBasePath, dstToken, attributes.DestinationLocation, dstProjectNumber, correlationId, attributes.DestinationVolumeUUID)
	}
	if err != nil {
		if !utilErrors.IsNotFoundErr(err) {
			logger.Error("getDestinationVolume error", "error", err)
			return googleproxyclient.VolumeV1beta{}, googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrDescribingVolume, err)
		}
		return googleproxyclient.VolumeV1beta{}, googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrVolumeNotFound, err)
	}

	if (srcVolume.VolumeState.Set && srcVolume.VolumeState.Value == vsa.VolumeStateOffline) && (dstVolume.VolumeState.Set && dstVolume.VolumeState.Value == vsa.VolumeStateOffline) {
		return googleproxyclient.VolumeV1beta{}, googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrVolumeNotOnlineForReplicationResume, errors.New("Volume is not online for replication"))
	}

	var srcQuotaInBytes float64
	var dstQuotaInBytes float64
	var dstUsedBytes float64

	if srcVolume.QuotaInBytes.Set {
		srcQuotaInBytes = srcVolume.QuotaInBytes.Value
	}
	if dstVolume.QuotaInBytes.Set {
		dstQuotaInBytes = dstVolume.QuotaInBytes.Value
	}
	if dstVolume.UsedBytes.Set {
		dstUsedBytes = dstVolume.UsedBytes.Value
	}
	if srcQuotaInBytes != dstQuotaInBytes {
		if dstUsedBytes > srcQuotaInBytes {
			return googleproxyclient.VolumeV1beta{}, googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrDestinationVolumeUsedSizeGreaterThanSourceVolumeAvailableQuota, errors.New("Destination volume used size is greater than source volume available quota"))
		}
	}
	return srcVolume, dstVolume, nil
}

func _describeVolume(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string, volumeId string) (googleproxyclient.VolumeV1beta, error) {
	logger := util.GetLogger(ctx)
	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, token, logger)
	params := googleproxyclient.V1betaDescribeVolumeParams{}
	params.LocationId = locationID
	params.ProjectNumber = projectNumber
	params.XCorrelationID = googleproxyclient.OptString{Value: *xCorrelationID, Set: true}
	params.VolumeId = volumeId

	response, err := googleProxyClient.Invoker.V1betaDescribeVolume(ctx, params)
	if err != nil {
		return googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrListVolumes, err)
	}
	volumeResponse := response.(*googleproxyclient.VolumeV1beta)
	if volumeResponse == nil {
		return googleproxyclient.VolumeV1beta{}, errors.NewVCPError(errors.ErrVolumeNotFound, nil)
	}

	return *volumeResponse, nil
}

func _getReplication(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*coreModels.VolumeReplication, error) {
	logger := util.GetLogger(ctx)

	logger.Debug(
		"get destination replication",
		"basePath", basePath,
		"projectNumber", projectNumber,
		"locationID", locationID,
		"volumeReplicationID", volumeReplicationID,
	)
	payloadError := &models.Error{Code: float64(404), Message: fmt.Sprintf("Error fetching replication - Replication %s not found", volumeReplicationID)}
	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwt, logger)
	params := &googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
		ProjectNumber:  projectNumber,
		LocationId:     locationID,
		XCorrelationID: googleproxyclient.NewOptString(utils.GetCoRelationIDFromContext(ctx)),
	}
	body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{volumeReplicationID}}
	response, err := googleProxyClient.Invoker.V1betaGetMultipleReplicationsInternal(ctx, &body, *params)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}
	replicationResponse := response.(*googleproxyclient.V1betaGetMultipleReplicationsInternalOK)

	if replicationResponse != nil && len(replicationResponse.Replications) < 1 {
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsNotFound, &replications.V1betaGetMultipleReplicationsNotFound{Payload: payloadError})
	}

	return convertReplicationResponseToModels(replicationResponse), nil
}

func convertReplicationResponseToModels(response *googleproxyclient.V1betaGetMultipleReplicationsInternalOK) *coreModels.VolumeReplication {
	var replication coreModels.VolumeReplication
	if response.Replications != nil && len(response.Replications) < 1 {
		return nil
	}
	var mirrorState, relationshipStatus string
	if response.Replications[0].MirrorState.Set {
		mirrorState = string(response.Replications[0].MirrorState.Value)
		replication.MirrorState = &mirrorState
	}
	if response.Replications[0].RelationshipStatus.Set {
		relationshipStatus = string(response.Replications[0].RelationshipStatus.Value)
		replication.RelationshipStatus = &relationshipStatus
	}
	replication.Name = response.Replications[0].Name.Value
	replication.UUID = response.Replications[0].VolumeReplicationUuid.Value
	replication.Description = response.Replications[0].Description.Value
	replication.ReplicationAttributes = &coreModels.ReplicationDetails{
		SourceVolumeUUID:      response.Replications[0].SourceVolumeUuid.Value,
		SourceVolumeName:      response.Replications[0].SourceVolumeName,
		DestinationVolumeUUID: response.Replications[0].DestinationVolumeUuid.Value,
		DestinationVolumeName: response.Replications[0].DestinationVolumeName,
		ReplicationSchedule:   string(response.Replications[0].ReplicationSchedule.Value),
		EndpointType:          string(response.Replications[0].EndpointType),
	}
	replication.TotalTransferBytes = response.Replications[0].TotalTransferBytes.Value
	replication.TotalTransferTimeSecs = response.Replications[0].TotalTransferTimeSecs.Value
	replication.LastTransferSize = response.Replications[0].LastTransferSize.Value
	replication.LastTransferError = response.Replications[0].LastTransferError.Value
	replication.LastTransferDuration = response.Replications[0].LastTransferDuration.Value
	replication.TotalProgress = response.Replications[0].TotalProgress.Value
	replication.LagTime = response.Replications[0].LagTime.Value
	if response.Replications[0].LastTransferEndTime.Set {
		replication.LastTransferEndTime = &response.Replications[0].LastTransferEndTime.Value
	}
	if response.Replications[0].ProgressLastUpdated.Set {
		replication.ProgressLastUpdated = &response.Replications[0].ProgressLastUpdated.Value
	}
	replication.CreatedAt = response.Replications[0].CreatedAt.Value
	replication.State = mapLifecycleStateToState(response.Replications[0].LifeCycleState.Value)
	replication.StateDetails = response.Replications[0].LifeCycleStateDetails.Value

	// Populate HybridReplicationAttributes if ReplicationType indicates hybrid replication
	if response.Replications[0].ReplicationType.Set {
		replicationType := string(response.Replications[0].ReplicationType.Value)
		// Check if this is a hybrid replication type (CONTINUOUS_REPLICATION, ONPREM_REPLICATION, or REVERSE_ONPREM_REPLICATION)
		var hybridReplicationType datamodel.HybridReplicationParametersReplicationType
		isHybrid := false

		switch replicationType {
		case "CONTINUOUS_REPLICATION":
			hybridReplicationType = datamodel.HybridReplicationParametersReplicationTypeCONTINUOUS
			isHybrid = true
		case "ONPREM_REPLICATION":
			hybridReplicationType = datamodel.HybridReplicationParametersReplicationTypeONPREM
			isHybrid = true
		case "REVERSE_ONPREM_REPLICATION":
			hybridReplicationType = datamodel.HybridReplicationParametersReplicationTypeREVERSE
			isHybrid = true
		case "MIGRATION":
			hybridReplicationType = datamodel.HybridReplicationParametersReplicationTypeMIGRATION
			isHybrid = true
		}

		if isHybrid {
			hybridParams := &coreModels.HybridReplicationParameters{
				ResourceID:                    response.Replications[0].Name.Value,
				ReplicationType:               hybridReplicationType,
				ReplicationSchedule:           string(response.Replications[0].ReplicationSchedule.Value),
				HybridReplicationUserCommands: []string{}, // Empty by default, populated from database if needed
				PeerVolumeName:                response.Replications[0].SourceVolumeName,
				Description:                   response.Replications[0].Description.Value,
			}
			replication.HybridReplicationAttributes = hybridParams
		}
	}

	return &replication
}

func _verifyReplication(ctx context.Context, event *DeleteReplicationEvent) (*coreModels.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	if event.DstBasePath == "" {
		srcReplication, err := getReplication(ctx, event.SrcBasePath, event.SourceProjectNumber, event.ReplicationModel.ReplicationAttributes.SourceLocation, event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID, event.SrcToken)
		if err != nil || srcReplication == nil {
			logger.Error("getReplication error", "error", err)
			return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
		}
		return srcReplication, nil
	}
	dstReplication, err := getReplication(ctx, event.DstBasePath, event.DestinationProjectNumber, event.ReplicationModel.ReplicationAttributes.DestinationLocation, event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID, event.DstToken)
	if err != nil || dstReplication == nil {
		logger.Error("getReplication error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}

	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStateMIRRORED {
		logger.Error("Replication in mirrored state", "error", err)
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Destination replication is in mirror_state: %v expected_mirror_state: %v", models.ReplicationV1betaMirrorStateMIRRORED, models.ReplicationV1betaMirrorStateSTOPPED))
	}

	// Check if replication is in valid state
	if *dstReplication.MirrorState != models.ReplicationV1betaMirrorStateSTOPPED && *dstReplication.MirrorState != models.ReplicationV1betaMirrorStatePREPARING {
		logger.Error("Replication should be in PREPARING or STOPPED state before deleting")
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Expected mirror state: %v or %v", models.ReplicationV1betaMirrorStatePREPARING, models.ReplicationV1betaMirrorStateSTOPPED))
	}

	// Edge Case where mirrorState is uninitialized but data is being transferred and state is PENDING_SVM_PEERING.
	if *dstReplication.MirrorState == models.ReplicationV1betaMirrorStatePREPARING && *dstReplication.RelationshipStatus == datamodel.SnapmirrorRelationshipTransferring {
		logger.Error("Replication needs to be in stopped state")
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Replication relationship status should be %s", models.VolumeReplicationCVPV1betaRelationshipStatusIdle))
	}

	if event.ReplicationModel.HybridReplicationAttributes != nil {
		if event.ReplicationModel.State == datamodel.LifeCycleStateCreating && event.ReplicationModel.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingSVMPeer {
			logger.Error("Hybrid Replication in pending SVM peering state")
			return nil, utilErrors.NewUserInputValidationErr("Hybrid Replication in pending SVM peering state")
		}
	}

	return dstReplication, nil
}

func _verifyDstReplicationSync(ctx context.Context, event *ResumeReplicationEvent) (*coreModels.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	if IsSrcForHybridReplication(event.ReplicationModel) {
		logger.Error("Sync not allowed when replication is in externally managed state")
		return nil, utilErrors.NewUserInputValidationErr("Sync not allowed when replication is in externally managed state")
	}
	dstReplication, err := getReplication(ctx, event.DstBasePath, event.DestinationProjectNumber, event.ReplicationModel.ReplicationAttributes.DestinationLocation, event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID, event.DstToken)
	if err != nil || dstReplication == nil {
		logger.Error("getReplication error", "error", err)
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}
	if *dstReplication.MirrorState != models.ReplicationV1betaMirrorStateMIRRORED {
		return nil, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Replication mirror state should be %s", models.ReplicationV1betaMirrorStateMIRRORED))
	}

	return dstReplication, nil
}

func MapCCFERescheduleToInternalReplicationSchedule(schedule gcpgenserver.ReplicationV1betaReplicationSchedule) googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule {
	switch schedule {
	case gcpgenserver.ReplicationV1betaReplicationScheduleHOURLY:
		return googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly
	case gcpgenserver.ReplicationV1betaReplicationScheduleDAILY:
		return googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleDaily
	case gcpgenserver.ReplicationV1betaReplicationScheduleEVERY10MINUTES:
		return googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule10minutely
	default:
		return googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly
	}
}

func mapLifecycleStateToState(state googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState) string {
	switch state {
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating:
		return datamodel.LifeCycleStateCreating
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable:
		return datamodel.LifeCycleStateAvailable
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleting:
		return datamodel.LifeCycleStateDeleting
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleted:
		return datamodel.LifeCycleStateDeleted
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateError:
		return datamodel.LifeCycleStateError
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDisabled:
		return datamodel.LifeCycleStateDisabled
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateUpdating:
		return datamodel.LifeCycleStateUpdating
	default:
		return datamodel.LifeCycleStateUnknown
	}
}

func IsSrcForHybridReplication(replication *datamodel.VolumeReplication) bool {
	if replication.HybridReplicationAttributes != nil && replication.HybridReplicationAttributes.HybridReplicationType != nil {
		if *replication.HybridReplicationAttributes.HybridReplicationType == string(datamodel.HybridReplicationParametersReplicationTypeREVERSE) &&
			replication.ReplicationAttributes.DestinationLocation == remoteRegionCustomer {
			return true
		}
	}
	return false
}

// getPath returns the path of an ONTAP snapmirror relationship in a <svm_name>:<volume_name> format
func getPath(svmName, volumeName string) string {
	return fmt.Sprintf("%s:%s", svmName, volumeName)
}
