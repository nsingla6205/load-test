package gcp

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factorycommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/flexcache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/flexcache_workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

var (
	createFlexCacheVolume = _createFlexCacheVolume

	utilGetLogger                    = util.GetLogger
	utilsGetLocationFromVendorID     = utils.GetLocationFromVendorID
	utilsGetRequestIDFromContext     = utils.GetRequestIDFromContext
	utilsGetCorrelationIDFromContext = utils.GetCoRelationIDFromContext

	workflowsExecuteWorkflowSequentially = workflows.ExecuteWorkflowSequentially
	establishFlexCacheVolumePeering      = _establishFlexCacheVolumePeering
	isEstablishVolumePeeringNeeded       = _isEstablishVolumePeeringNeeded
	verifyVolumeState                    = _verifyVolumeState
	verifyFlexCacheParameters            = _verifyFlexCacheParameters
	verifyClusterPeering                 = _verifyClusterPeering
	checkForFlexCacheJobInProgress       = _checkForFlexCacheJobInProgress
	flexCacheParamsMatch                 = _flexCacheParamsMatch
	verifyCommandExpiryTime              = _verifyCommandExpiryTime

	// flexCacheCreateVolumeSupervisorGracePeriod controls how long the supervisor waits
	// before cleaning up a flexcache create volume job in NEW state. This prevents cleanup
	// of jobs stuck in NEW state during the customer peering command window.
	flexCacheCreateVolumeSupervisorGracePeriod = env.GetDuration("FLEXCACHE_CREATE_VOLUME_SUPERVISOR_GRACE_PERIOD", 60*time.Minute)
)

func (o *GCPOrchestrator) CreateFlexCacheVolume(ctx context.Context, params *common.CreateVolumeParams) (*coremodels.Volume, string, error) {
	return createFlexCacheVolume(ctx, o.storage, o.temporal, params)
}

func _createFlexCacheVolume(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateVolumeParams) (*coremodels.Volume, string, error) {
	logger := utilGetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	pool, err := se.GetPool(ctx, params.PoolID, account.ID)
	if err != nil {
		return nil, "", err
	}

	if pool.APIAccessMode == common.ONTAPMode {
		return nil, "", errors.NewUserInputValidationErr("Cannot create Volumes in ONTAP mode pool using GCNV API")
	}

	err = validateCreateVolumeParams(ctx, se, params, pool)
	if err != nil {
		return nil, "", err
	}

	svm, err := se.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		return nil, "", err
	}

	dbPool := database.ConvertPoolViewToPool(pool)
	volumeObj := &datamodel.Volume{
		Name:        params.Name,
		Account:     account,
		AccountID:   account.ID,
		SizeInBytes: int64(params.QuotaInBytes),
		Description: params.Description,
		PoolID:      pool.ID,
		SvmID:       svm.ID,
		Pool:        dbPool,
		State:       datamodel.LifeCycleStatePreparing,
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken:  params.CreationToken,
			Protocols:      params.Protocols,
			VendorSubnetID: params.Network,
			Labels:         params.Labels,
			AccountName:    getAccountName(account),
			DeploymentName: getPoolDeploymentName(dbPool),
			IsRegionalHA:   getPoolIsRegionalHA(dbPool),
		},
	}

	if params.FileProperties != nil {
		volumeObj.VolumeAttributes.FileProperties = factorycommon.BuildFilePropertiesFromParams(params.FileProperties, params.CreationToken)
	}

	err = verifyCommandExpiryTime(params.CacheParameters.PeerExpiryTime)
	if err != nil {
		return nil, "", err
	}

	if params.CacheParameters != nil {
		volumeObj.CacheParameters = &datamodel.CacheParameters{
			PeerSvmName:           params.CacheParameters.PeerSvmName,
			PeerVolumeName:        params.CacheParameters.PeerVolumeName,
			PeerClusterName:       params.CacheParameters.PeerClusterName,
			PeerIpAddresses:       params.CacheParameters.PeerIPAddresses,
			CacheState:            params.CacheParameters.CacheState,
			CacheStateDetailsCode: params.CacheParameters.CacheStateDetailsCode,
			CacheStateDetails:     params.CacheParameters.CacheStateDetails,
			CommandExpiryTime:     params.CacheParameters.PeerExpiryTime,
			EnableGlobalFileLock:  params.CacheParameters.EnableGlobalFileLock,
		}

		// Pass CacheConfig if provided
		if params.CacheParameters.CacheConfig != nil {
			volumeObj.CacheParameters.CacheConfig = &datamodel.CacheConfig{
				WritebackEnabled:        params.CacheParameters.CacheConfig.WritebackEnabled,
				AtimeScrubEnabled:       params.CacheParameters.CacheConfig.AtimeScrubEnabled,
				AtimeScrubDays:          params.CacheParameters.CacheConfig.AtimeScrubDays,
				CifsChangeNotifyEnabled: params.CacheParameters.CacheConfig.CifsChangeNotifyEnabled,
			}
		}
	}

	dbVolume, err := se.CreateVolume(ctx, volumeObj)
	if err != nil {
		var ce *vsaerrors.CustomError
		if vsaerrors.As(err, &ce) {
			logger.Errorf("CreateVolume failed trackingID=%d message=%s original=%v", ce.TrackingID, ce.Message, ce.OriginalErr)
			return nil, "", ce.OriginalErr
		}
		logger.Errorf("CreateVolume failed: %v", err)
		return nil, "", err
	}

	location, err := utilsGetLocationFromVendorID(dbVolume.Pool.VendorID)
	if err != nil {
		logger.Errorf("Failed to get location from vendor ID for pool %s, error: %v", dbVolume.Pool.Name, err)
		return nil, "", err
	}

	requestURI := utilsGetRequestIDFromContext(ctx)
	correlationID := utilsGetCorrelationIDFromContext(ctx)

	event := &flexcache.CreateFlexCacheEvent{
		LocationID:    location,
		ProjectNumber: params.AccountName,
		RequestUri:    requestURI,
		CorrelationID: &correlationID,
	}

	// Create job is stored as DONE before Temporal handoff. The API returns this job's UUID
	// so the create operation finishes immediately for clients: the control/sequence workflow
	// may be queued, and a single in-flight op per resource must not block on establish work.
	createJob := &datamodel.Job{
		Type:          string(datamodel.JobTypeFlexCacheCreateVolume),
		State:         string(datamodel.JobsStateDONE),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: correlationID,
		RequestID:     requestURI,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbVolume.UUID,
			SupervisorAttributes: &datamodel.SupervisorAttributes{
				OverrideGracePeriod: flexCacheCreateVolumeSupervisorGracePeriod,
			},
		},
	}
	createdJob, err := se.CreateJob(ctx, createJob)
	if err != nil {
		logger.Errorf("Failed to create job in database, error: %v", err)
		return nil, "", err
	}

	establishJob := &datamodel.Job{
		Type:          string(datamodel.JobTypeFlexCacheEstablishPeering),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: correlationID,
		RequestID:     requestURI,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbVolume.UUID,
			PoolUUID:     dbVolume.Pool.UUID,
		},
	}
	createdEstablishJob, err := se.CreateJobWithWorkflowID(ctx, establishJob, createdJob.WorkflowID)
	if err != nil {
		logger.Errorf("Failed to create establish peering job in database, error: %v", err)
		return nil, "", err
	}
	event.EstablishPeeringJobUUID = createdEstablishJob.UUID

	// controlWorkflowID defines the workflow ID for the control workflow
	controlWorkflowID := fmt.Sprintf(workflows.VolumeCreateDeleteSnapshotDeleteSeq, dbVolume.Account.ID, location, dbVolume.Pool.Name)
	err = workflowsExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		flexcache_workflows.CreateFlexCacheWorkflow,
		workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowID:            createdEstablishJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		params,
		dbVolume,
		event,
	)
	if err != nil {
		logger.Errorf("Failed to start create FlexCache volume workflow, error: %v", err)
		return nil, "", err
	}
	return convertDatastoreVolumeToModel(dbVolume, nil), createdJob.UUID, nil
}

func (o *GCPOrchestrator) EstablishFlexCacheVolumePeering(ctx context.Context, params *common.EstablishVolumePeeringParams) (*coremodels.Volume, string, error) {
	return establishFlexCacheVolumePeering(ctx, o.storage, o.temporal, params)
}

func _establishFlexCacheVolumePeering(ctx context.Context, se database.Storage, temporal client.Client, params *common.EstablishVolumePeeringParams) (*coremodels.Volume, string, error) {
	logger := utilGetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	dbVolume, err := se.GetVolumeByNameAndAccountID(ctx, params.Name, account.ID)
	if err != nil {
		return nil, "", err
	}

	jobUUID, err := isEstablishVolumePeeringNeeded(ctx, se, params, dbVolume)
	if err != nil {
		logger.Errorf("Establish volume peering pre-checks failed: %v", err)
		return nil, "", err
	}

	// Return jobUUID if a job is already in progress for tracking instead of returning an error
	if jobUUID != "" {
		return convertDatastoreVolumeToModel(dbVolume, nil), jobUUID, nil
	}

	location, err := utilsGetLocationFromVendorID(dbVolume.Pool.VendorID)
	if err != nil {
		logger.Errorf("Failed to get location from vendor ID for pool %s, error: %v", dbVolume.Pool.Name, err)
		return nil, "", err
	}

	err = verifyCommandExpiryTime(params.ExpiryTime)
	if err != nil {
		return nil, "", err
	}

	requestURI := utilsGetRequestIDFromContext(ctx)
	correlationID := utilsGetCorrelationIDFromContext(ctx)

	event := &flexcache.CreateFlexCacheEvent{
		LocationID:    location,
		ProjectNumber: params.AccountName,
		RequestUri:    requestURI,
		CorrelationID: &correlationID,
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeFlexCacheEstablishPeering),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: correlationID,
		RequestID:     requestURI,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbVolume.UUID,
			PoolUUID:     dbVolume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create job in database, error: %v", err)
		return nil, "", err
	}

	controlWorkflowID := fmt.Sprintf(workflows.VolumeCreateDeleteSnapshotDeleteSeq, dbVolume.Account.ID, location, dbVolume.Pool.Name)

	err = workflowsExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		flexcache_workflows.CreateFlexCacheWorkflow,
		workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowID:            createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		convertEstablishVolumePeeringParamsToCreateVolumeParams(params),
		dbVolume,
		event,
	)
	if err != nil {
		logger.Errorf("Failed to start establish volume peering workflow, error: %v", err)
		return nil, "", err
	}

	return convertDatastoreVolumeToModel(dbVolume, nil), createdJob.UUID, nil
}

func convertEstablishVolumePeeringParamsToCreateVolumeParams(params *common.EstablishVolumePeeringParams) *common.CreateVolumeParams {
	cp := &coremodels.CacheParameters{
		PeerSvmName:     params.PeerSvmName,
		PeerVolumeName:  params.PeerVolumeName,
		PeerClusterName: params.PeerClusterName,
		PeerIPAddresses: params.PeerAddresses,
	}
	if params.ExpiryTime != nil {
		cp.PeerExpiryTime = params.ExpiryTime
	}

	return &common.CreateVolumeParams{
		Name:            params.Name,
		AccountName:     params.AccountName,
		Region:          params.Region,
		Zone:            params.Zone,
		CacheParameters: cp,
	}
}

func _isEstablishVolumePeeringNeeded(ctx context.Context, se database.Storage,
	params *common.EstablishVolumePeeringParams, dbVolume *datamodel.Volume) (string, error) {
	logger := utilGetLogger(ctx)
	err := verifyVolumeState(ctx, dbVolume)
	if err != nil {
		return "", err
	}

	err = verifyFlexCacheParameters(ctx, params, dbVolume)
	if err != nil {
		return "", err
	}

	if verifyClusterPeering(ctx, dbVolume) {
		return "", errors.NewUserInputValidationErr("cluster peering is already established")
	}

	isJobInProgress, jobUUID, err := checkForFlexCacheJobInProgress(ctx, se, dbVolume, params)
	if err != nil {
		return "", err
	}
	if isJobInProgress {
		logger.Infof("found an existing FlexCache job in progress for volume %s", dbVolume.Name)
		return jobUUID, nil
	}
	return "", nil
}

func _verifyVolumeState(ctx context.Context, dbVolume *datamodel.Volume) error {
	logger := utilGetLogger(ctx)
	logger.Debugf("verifying volume state: name=%s, state=%s", dbVolume.Name, dbVolume.State)
	// Establish Volume Peering can be tried if the volume is in PREPARING state
	if dbVolume.State != datamodel.LifeCycleStatePreparing {
		return errors.NewUserInputValidationErr(fmt.Sprintf("volume %s must be in %s state (current: %s)",
			dbVolume.Name, datamodel.LifeCycleStatePreparing, dbVolume.State))
	}
	return nil
}

func _verifyFlexCacheParameters(ctx context.Context, params *common.EstablishVolumePeeringParams,
	dbVolume *datamodel.Volume) error {
	logger := utilGetLogger(ctx)
	logger.Debugf("verifying FlexCache parameters for volume %s (ignoring IPs)", dbVolume.Name)
	if !flexCacheParamsMatch(dbVolume, params) {
		return errors.NewUserInputValidationErr("provided FlexCache parameters do not match with existing FlexCache volume parameters")
	}
	return nil
}

func _verifyClusterPeering(ctx context.Context, dbVolume *datamodel.Volume) bool {
	// If the cache state is PEERED then cluster peering is already established
	logger := utilGetLogger(ctx)
	logger.Debugf("verifying cluster peering state for volume %s", dbVolume.Name)
	if dbVolume.CacheParameters.CacheState == models.FlexCacheV1betaCacheStatePEERED {
		return true
	}
	return false
}

func _checkForFlexCacheJobInProgress(ctx context.Context, se database.Storage,
	dbVolume *datamodel.Volume, params *common.EstablishVolumePeeringParams) (bool, string, error) {
	logger := utilGetLogger(ctx)
	logger.Debugf("checking for any other FlexCache job in progress for volume %s", dbVolume.Name)
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("resource_name", "=", dbVolume.Name),
	)
	jobs, err := se.GetJobsWithCondition(ctx, *filter)
	if err != nil {
		return false, "", err
	}
	for _, job := range jobs {
		// Check if there is any FlexCache establish peering job in progress for the same volume with required params and in healthy state
		if job.Type == string(datamodel.JobTypeFlexCacheEstablishPeering) || job.Type == string(datamodel.JobTypeFlexCacheInternalPeering) {
			if flexCacheParamsMatch(dbVolume, params) && (job.State != string(datamodel.JobsStateERROR) && job.State != string(datamodel.JobsStateDONE)) {
				return true, job.UUID, nil
			}
		}
	}
	return false, "", nil
}

func _flexCacheParamsMatch(dbVolume *datamodel.Volume, params *common.EstablishVolumePeeringParams) bool {
	return dbVolume.CacheParameters.PeerClusterName == params.PeerClusterName &&
		dbVolume.CacheParameters.PeerSvmName == params.PeerSvmName &&
		dbVolume.CacheParameters.PeerVolumeName == params.PeerVolumeName
}

func _verifyCommandExpiryTime(peerExpiryTime *time.Time) error {
	if peerExpiryTime != nil {
		if peerExpiryTime.Before(time.Now().UTC()) {
			return errors.NewUserInputValidationErr("invalid CommandExpiryTime: cannot be in the past")
		}
	}
	return nil
}
