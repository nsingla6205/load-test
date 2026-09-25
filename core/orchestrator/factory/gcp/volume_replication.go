package gcp

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/replicationWorkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	logger "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

var (
	// Internal functions for orchestrator package
	createVolumeReplicationInternal = _createVolumeReplicationInternal
	updateVolumeReplicationInternal = _updateVolumeReplicationInternal
	stopReplicationInternal         = _stopReplicationInternal
	resumeReplicationInternal       = _resumeReplicationInternal
	deleteReplicationInternal       = _deleteReplicationInternal
	reverseReplicationInternal      = _reverseReplicationInternal
	establishReplicationPeering     = _establishReplicationPeering

	createVolumeReplication     = _createVolumeReplication
	stopReplication             = _stopReplication
	resumeReplication           = _resumeReplication
	deleteReplication           = _deleteReplication
	releaseVolumeReplication    = _releaseVolumeReplication
	syncReplication             = _syncReplication
	reverseAndResumeReplication = _reverseAndResumeReplication
	updateReplication           = _updateReplication
	getActiveReplicationJobs    = _getActiveReplicationJobs

	validateCreateReplicationParams       = replication.ValidateCreateReplicationParams
	validateReplicationParams             = replication.ValidateReplicationParams
	verifyDstReplicationResume            = replication.VerifyDstReplicationResume
	verifySourceQuotaRules                = replication.VerifySourceQuotaRules
	verifyDestinationQuotaRules           = replication.VerifyDestinationQuotaRules
	verifyNewSourceQuotaRulesReverse      = replication.VerifyNewSourceQuotaRulesReverse
	verifyNewDestinationQuotaRulesReverse = replication.VerifyNewDestinationQuotaRulesReverse
	verifyDstReplicationStop              = replication.VerifyDstReplicationStop
	VerifyReplicationDelete               = replication.VerifyReplication
	verifyDstReplicationSync              = replication.VerifyDstReplicationSync
	validateReplicationUpdate             = replication.ValidateReplicationUpdate
	verifyDstReplicationReverse           = replication.VerifyDstReplicationReverse
	verifyEstablishPeering                = replication.VerifyEstablishPeering
	hybridReplicationJobsInProcess        = replication.HybridReplicationJobsInProcess

	convertCreateReplicationParamsToEventParam = _convertCreateReplicationParamsToEventParam
	getReplicationObjects                      = _getReplicationObjects
	googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
	GetProjectNumberForRegion                  = _getProjectNumberForRegion

	utilParseRegionAndZone            = utils.ParseRegionAndZone
	utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
	utilsGetPairedRegionUri           = utils.GetPairedRegionURI
	utilsParseProjectNumberFromURI    = utils.ParseProjectNumberFromURI
	authGetSignedJwtToken             = auth.GetSignedJwtToken
	utilGetVolumeUriFromCcfeUri       = utils.GetVolumeUriFromCcfeUri
	convertLabelsMapToJSONB           = utils.ConvertLabelsMapToJSONB

	WorkflowGlobalTimeoutForReplicationMinutes = env.GetInt("WORKFLOW_GLOBAL_TIMEOUT_FOR_REPLICATION_MINUTES", 20)
)

const (
	volumeReplicationCVPV1betaLifeCycleStateAvailableForUse = "Available for use"
	volumeReplicationCVP1betaLifeCycleStateCreation         = "Create in progress"
	volumeReplicationCVP1betaLifeCycleStateStopping         = "Stop in progress"
	volumeReplicationCVP1betaLifeCycleStateResuming         = "Resume in progress"
	volumeReplicationCVP1betaLifeCycleStateSync             = "Sync in progress"
	volumeReplicationCVP1betaLifeCycleStateReversing        = "Reverse in progress"
	volumeReplicationCVP1betaLifeCycleStateUpdating         = "Update in progress"
	volumeReplicationCVP1betaLifeCycleStateDeleting         = "Delete in progress"
	remoteRegionCustomer                                    = "customer"
)

func (o *GCPOrchestrator) CreateVolumeReplicationInternal(ctx context.Context, params *commonparams.CreateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error) {
	return createVolumeReplicationInternal(ctx, o.storage, o.temporal, params)
}

func _createVolumeReplicationInternal(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.CreateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, params.VolumeReplication.Account.Name)
	if err != nil {
		logger.Error("Failed to get or create account", "error", err)
		return nil, nil, err
	}

	volume, err := se.GetVolume(ctx, params.VolumeReplication.ReplicationAttributes.DestinationVolumeUUID)
	if err != nil {
		logger.Error("Failed to get volume", "error", err)
		return nil, nil, err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateVolumeReplicationInternal),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.VolumeReplication.RemoteUri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	replicationDb, err := se.CreateVolumeReplication(ctx, prepareReplicationDataModel(params, account, volume))
	if err != nil {
		logger.Error("Failed to create volume replication in database", "error", err)
		return nil, nil, err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
			_, deleteError := se.DeleteVolumeReplication(ctx, replicationDb)
			if deleteError != nil {
				logger.Error("Failed to delete volume replication after creation job failed", "volume_repl_id", replicationDb.UUID, "error", deleteError)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    time.Duration(WorkflowGlobalTimeoutForReplicationMinutes) * time.Minute,
		},
		replicationWorkflows.CreateInternalVolumeReplicationWorkflow,
		params,
		replicationDb,
	)

	if err != nil {
		logger.Error("Failed to execute workflow for volume replication creation", "error", err)
		return nil, nil, err
	}

	return convertDataStoreReplicationToModel(replicationDb), createdJob, nil
}

func (o *GCPOrchestrator) UpdateVolumeReplicationInternal(ctx context.Context, params *commonparams.UpdateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error) {
	return updateVolumeReplicationInternal(ctx, o.storage, o.temporal, params)
}

func _updateVolumeReplicationInternal(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.UpdateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to get account", "error", err)
		return nil, nil, err
	}

	replicationDb, err := se.GetVolumeReplication(ctx, params.VolumeReplicationUuid)
	if err != nil {
		logger.Error("Failed to get volume replication from database", "error", err)
		return nil, nil, err
	}

	previousState := replicationDb.State
	previousStateDetails := replicationDb.StateDetails
	replicationDb.State = datamodel.LifeCycleStateUpdating
	replicationDb.StateDetails = datamodel.LifeCycleStateUpdatingDetails
	err = se.UpdateVolumeReplicationStates(ctx, replicationDb)
	if err != nil {
		return nil, nil, err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateVolumeReplicationInternal),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  replicationDb.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         replicationDb.UUID,
			PoolUUID:             replicationDb.Volume.Pool.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.UpdateInternalVolumeReplicationWorkflow,
		params,
		replicationDb,
	)

	if err != nil {
		logger.Error("Failed to execute workflow for volume replication update", "error", err)
		return nil, nil, err
	}

	return convertDataStoreReplicationToModel(replicationDb), createdJob, nil
}

func (o *GCPOrchestrator) StopReplicationInternal(ctx context.Context, replicationUUID string, accountName string, forceStop bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return stopReplicationInternal(ctx, o.storage, o.temporal, replicationUUID, accountName, forceStop)
}

func _stopReplicationInternal(ctx context.Context, se database.Storage, temporal client.Client, volumeReplicationId, accountName string, forceStop bool) (*models.VolumeReplication, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		logger.Error("Failed to get account", "error", err)
		return nil, nil, err
	}

	replicationDb, err := se.GetVolumeReplication(ctx, volumeReplicationId)
	if err != nil {
		return nil, nil, err
	}

	replicationDb.State = datamodel.LifeCycleStateUpdating
	replicationDb.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	err = se.UpdateVolumeReplicationStates(ctx, replicationDb)
	if err != nil {
		logger.Error("Failed to update volume replication states in database", "error", err)
		return nil, nil, err
	}

	replicationDb.Account = &datamodel.Account{
		Name: accountName,
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeStopVolumeReplicationInternal),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  replicationDb.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: replicationDb.UUID,
			PoolUUID:     replicationDb.Volume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
			// Set replication state to ERROR if workflow fails to start
			replicationDb.State = datamodel.LifeCycleStateError
			replicationDb.StateDetails = err.Error()
			if stateErr := se.UpdateVolumeReplicationStates(ctx, replicationDb); stateErr != nil {
				logger.Error("Failed to set replication state to ERROR", "replicationUUID", replicationDb.UUID, "error", stateErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.StopInternalVolumeReplicationWorkflow,
		replicationDb,
		forceStop,
	)
	if err != nil {
		logger.Error("Failed to execute workflow for resuming volume replication", "error", err)
		return nil, nil, err
	}

	return convertDataStoreReplicationToModel(replicationDb), createdJob, nil
}

func (o *GCPOrchestrator) StopReplication(ctx context.Context, params *commonparams.StopReplicationParams) (*models.VolumeReplication, string, error) {
	return stopReplication(ctx, o.storage, o.temporal, params)
}

func _stopReplication(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.StopReplicationParams) (*models.VolumeReplication, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	event := replication.StopReplicationEvent{
		CommonReplicationEventParams: replication.CommonReplicationEventParams{
			VolumeResourceID:      params.VolumeResourceId,
			ReplicationResourceID: params.ReplicationResourceId,
			AccountName:           params.AccountName,
			XCorrelationID:        &params.CorrelationId,
			Location:              params.Region,
			Zone:                  params.Zone,
		},
		ForceStop: params.ForceStop,
	}

	if params.Zone != "" {
		event.CommonReplicationEventParams.Location = params.Zone
	}

	existingReplication, existingJobUuid, err := validateReplicationParams(ctx, &event.CommonReplicationEventParams, account.ID, se, false, string(datamodel.JobTypeStopVolumeReplication))
	if err != nil {
		return nil, "", err
	}
	if existingJobUuid != nil {
		existingReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

		return existingReplication, *existingJobUuid, nil
	}

	dstReplication, err := verifyDstReplicationStop(ctx, &event)
	if err != nil {
		return nil, "", err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeStopVolumeReplication),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  event.ReplicationModel.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: event.ReplicationModel.UUID,
			PoolUUID:     event.ReplicationModel.Volume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.StopReplicationWorkflow,
		params,
		&event,
	)
	if err != nil {
		logger.Error("Failed to execute workflow", "error", err)
		return nil, "", err
	}
	dstReplication.State = datamodel.LifeCycleStateUpdating
	dstReplication.StateDetails = datamodel.LifeCycleStateUpdatingDetails
	dstReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType
	return dstReplication, createdJob.UUID, nil
}

func (o *GCPOrchestrator) EstablishReplicationPeering(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams) (*models.VolumeReplication, string, error) {
	return establishReplicationPeering(ctx, o.storage, o.temporal, params)
}

func _establishReplicationPeering(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.EstablishReplicationPeeringParams) (*models.VolumeReplication, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to get account", "error", err)
		return nil, "", err
	}
	location := params.Region
	if params.Zone != "" {
		location = params.Zone
	}
	ccfeURI := replication.GetCCFEURI(params.AccountName, location, params.VolumeResourceId, params.ReplicationResourceId)

	// check for duplicate jobs
	existingJob, err := se.CheckAndFetchDuplicateJobs(ctx, string(datamodel.JobTypeHybridReplicationEstablishPeering), utils.GetCoRelationIDFromContext(ctx))
	if err != nil {
		return nil, "", err
	}
	if existingJob != nil {
		replication, err := se.GetVolumeReplication(ctx, existingJob.JobAttributes.ResourceUUID)
		if err != nil {
			logger.Error("Failed to get replication from database", "error", err)
			return nil, "", err
		}
		return convertDataStoreReplicationToModel(replication), existingJob.UUID, nil
	}

	dstReplication, err := verifyEstablishPeering(ctx, params, se, account.ID, ccfeURI)
	if err != nil {
		return nil, "", err
	}

	jobUUID, err := hybridReplicationJobsInProcess(ctx, se, account.ID, dstReplication.ReplicationAttributes.DestinationPoolUUID, ccfeURI)
	if err != nil {
		return nil, "", err
	}
	// Return jobUUID if a job is already in progress for tracking instead of returning an error
	if jobUUID != "" {
		return convertDataStoreReplicationToModel(dstReplication), jobUUID, nil
	}

	replicationResult := replication.CreateHybridReplicationResult{
		DestinationVolume:        dstReplication.Volume,
		DestinationRegion:        params.Region,
		DestinationZone:          params.Zone,
		DestinationProjectNumber: params.AccountName,
		HybridReplicationParameters: &models.HybridReplicationParameters{
			Labels:          make(map[string]string),
			ResourceID:      params.ReplicationResourceId,
			PeerVolumeName:  params.PeerVolumeName,
			PeerClusterName: params.PeerClusterName,
			PeerSvmName:     params.PeerSvmName,
			PeerIPAddresses: params.PeerIPAddresses,
			ReplicationType: datamodel.HybridReplicationParametersReplicationType(dstReplication.ReplicationAttributes.ReplicationType),
		},
		CorrelationID:    &params.CorrelationId,
		DbVolReplication: dstReplication,
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeHybridReplicationEstablishPeering),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  ccfeURI,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: replicationResult.DbVolReplication.UUID,
			PoolUUID: replicationResult.DbVolReplication.ReplicationAttributes.DestinationPoolUUID},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.EstablishPeeringWorkflow,
		replicationResult,
		dstReplication.Volume,
	)
	if err != nil {
		logger.Error("Failed to execute workflow", "error", err)
		return nil, "", err
	}

	dstReplication.State = datamodel.LifeCycleStateUpdating
	dstReplication.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	return convertDataStoreReplicationToModel(dstReplication), createdJob.UUID, nil
}

func convertDataStoreReplicationToModel(replication *datamodel.VolumeReplication) *models.VolumeReplication {
	return &models.VolumeReplication{
		BaseModel: models.BaseModel{
			UUID:      replication.UUID,
			CreatedAt: replication.CreatedAt,
			UpdatedAt: replication.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(replication.DeletedAt),
		},
		Name:         replication.Name,
		Description:  replication.Description,
		State:        replication.State,
		StateDetails: replication.StateDetails,
		Uri:          replication.Uri,
		RemoteUri:    replication.RemoteUri,
		ReplicationAttributes: &models.ReplicationDetails{
			EndpointType:               replication.ReplicationAttributes.EndpointType,
			ReplicationType:            replication.ReplicationAttributes.ReplicationType,
			ReplicationSchedule:        replication.ReplicationAttributes.ReplicationSchedule,
			SourcePoolUUID:             replication.ReplicationAttributes.SourcePoolUUID,
			SourceVolumeUUID:           replication.ReplicationAttributes.SourceVolumeUUID,
			SourceRegion:               replication.ReplicationAttributes.SourceLocation,
			SourceHostName:             replication.ReplicationAttributes.SourceHostName,
			SourceReplicationUUID:      replication.ReplicationAttributes.SourceReplicationUUID,
			SourceSvmName:              replication.ReplicationAttributes.SourceSvmName,
			SourceVolumeName:           replication.ReplicationAttributes.SourceVolumeName,
			DestinationPoolUUID:        replication.ReplicationAttributes.DestinationPoolUUID,
			DestinationVolumeUUID:      replication.ReplicationAttributes.DestinationVolumeUUID,
			DestinationRegion:          replication.ReplicationAttributes.DestinationLocation,
			DestinationHostName:        replication.ReplicationAttributes.DestinationHostName,
			DestinationReplicationUUID: replication.ReplicationAttributes.DestinationReplicationUUID,
			DestinationSvmName:         replication.ReplicationAttributes.DestinationSvmName,
			DestinationVolumeName:      replication.ReplicationAttributes.DestinationVolumeName,
			Labels:                     convertJSONBLabelsToMap(replication.ReplicationAttributes.Labels),
		},
		MirrorState:           replication.MirrorState,
		RelationshipStatus:    replication.RelationshipStatus,
		TotalProgress:         replication.TotalProgress,
		TotalTransferBytes:    replication.TotalTransferBytes,
		TotalTransferTimeSecs: replication.TotalTransferTimeSecs,
		LastTransferSize:      replication.LastTransferSize,
		LastTransferError:     replication.LastTransferError,
		LastTransferDuration:  replication.LastTransferDuration,
		LastTransferEndTime:   replication.LastTransferEndTime,
		ProgressLastUpdated:   replication.ProgressLastUpdated,
		LagTime:               replication.LagTime,
		AccountID:             replication.AccountID,
		VolumeID:              replication.VolumeID,
	}
}

func convertJSONBLabelsToMap(jsonb *datamodel.JSONB) map[string]string {
	if jsonb == nil {
		return nil
	}

	result := make(map[string]string)
	for key, value := range *jsonb {
		if strValue, ok := value.(string); ok {
			result[key] = strValue
		}
	}

	return result
}

func prepareReplicationDataModel(params *commonparams.CreateVolumeReplicationInternalParams, account *datamodel.Account, volume *datamodel.Volume) *datamodel.VolumeReplication {
	return &datamodel.VolumeReplication{
		Name:        params.VolumeReplication.Name,
		Description: params.VolumeReplication.Description,
		Uri:         params.VolumeReplication.RemoteUri,
		RemoteUri:   params.VolumeReplication.Uri,
		ReplicationAttributes: &datamodel.ReplicationDetails{
			EndpointType:               params.VolumeReplication.ReplicationAttributes.EndpointType,
			ReplicationType:            params.VolumeReplication.ReplicationAttributes.ReplicationType,
			ReplicationSchedule:        params.VolumeReplication.ReplicationAttributes.ReplicationSchedule,
			SourcePoolUUID:             params.VolumeReplication.ReplicationAttributes.SourcePoolUUID,
			SourceVolumeUUID:           params.VolumeReplication.ReplicationAttributes.SourceVolumeUUID,
			SourceLocation:             params.VolumeReplication.ReplicationAttributes.SourceRegion,
			SourceHostName:             params.VolumeReplication.ReplicationAttributes.SourceHostName,
			SourceReplicationUUID:      params.VolumeReplication.ReplicationAttributes.SourceReplicationUUID,
			SourceSvmName:              params.VolumeReplication.ReplicationAttributes.SourceSvmName,
			SourceVolumeName:           params.VolumeReplication.ReplicationAttributes.SourceVolumeName,
			DestinationPoolUUID:        params.VolumeReplication.ReplicationAttributes.DestinationPoolUUID,
			DestinationVolumeUUID:      params.VolumeReplication.ReplicationAttributes.DestinationVolumeUUID,
			DestinationLocation:        params.VolumeReplication.ReplicationAttributes.DestinationRegion,
			DestinationHostName:        params.VolumeReplication.ReplicationAttributes.DestinationHostName,
			DestinationReplicationUUID: params.VolumeReplication.ReplicationAttributes.DestinationReplicationUUID,
			DestinationSvmName:         params.VolumeReplication.ReplicationAttributes.DestinationSvmName,
			DestinationVolumeName:      params.VolumeReplication.ReplicationAttributes.DestinationVolumeName,
			Labels:                     convertLabelsMapToJSONB(params.VolumeReplication.ReplicationAttributes.Labels),
		},
		MirrorState:           params.VolumeReplication.MirrorState,
		RelationshipStatus:    params.VolumeReplication.RelationshipStatus,
		TotalProgress:         params.VolumeReplication.TotalProgress,
		TotalTransferBytes:    params.VolumeReplication.TotalTransferBytes,
		TotalTransferTimeSecs: params.VolumeReplication.TotalTransferTimeSecs,
		LastTransferSize:      params.VolumeReplication.LastTransferSize,
		LastTransferError:     params.VolumeReplication.LastTransferError,
		LastTransferDuration:  params.VolumeReplication.LastTransferDuration,
		LastTransferEndTime:   params.VolumeReplication.LastTransferEndTime,
		ProgressLastUpdated:   params.VolumeReplication.ProgressLastUpdated,
		LagTime:               params.VolumeReplication.LagTime,
		AccountID:             account.ID,
		Account:               account,
		VolumeID:              params.VolumeReplication.VolumeID,
		Volume:                volume,
	}
}

func (o *GCPOrchestrator) GetReplicationCount(ctx context.Context, projectNumber string) (int64, error) {
	// Get the count of volume replications for the specified account
	count, err := o.storage.GetVolumeReplicationCount(ctx, projectNumber)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// validateQuotaRulesForVolume validates that no quota rules for the volume are in error state.
// If any quota rules are in error state, it logs them and returns an error.
func validateQuotaRulesForVolume(ctx context.Context, se database.Storage, volumeID int64) error {
	logger := util.GetLogger(ctx)

	// Fetch all quota rules for the volume
	quotaRules, err := se.GetQuotaRulesByVolumeID(ctx, volumeID)
	if err != nil {
		logger.Error("Failed to fetch quota rules for volume", "volumeID", volumeID, "error", err)
		return err
	}

	// Check for quota rules in error state
	var erroredQuotaRules []*datamodel.QuotaRule
	for _, quotaRule := range quotaRules {
		if quotaRule.State == datamodel.LifeCycleStateError {
			erroredQuotaRules = append(erroredQuotaRules, quotaRule)
		}
	}

	// If there are errored quota rules, log them and return an error
	if len(erroredQuotaRules) > 0 {
		var erroredRuleDetails []string
		for _, rule := range erroredQuotaRules {
			erroredRuleDetails = append(erroredRuleDetails,
				"UUID: "+rule.UUID+", Name: "+rule.Name+", State: "+rule.State+", StateDetails: "+rule.StateDetails)
		}
		logger.Error("Volume has quota rules in error state",
			"volumeID", volumeID,
			"errorCount", len(erroredQuotaRules),
			"erroredRules", erroredRuleDetails)
		return errors.NewUserInputValidationErr("Cannot create volume replication: volume has quota rules in error state. Errored quota rules: " + strings.Join(erroredRuleDetails, "; "))
	}

	return nil
}

// CreateVolume creates the specified volume and adds it to the list of volume belonging to the specified owner
func (o *GCPOrchestrator) CreateVolumeReplication(ctx context.Context, params *commonparams.CreateVolumeReplicationParams) (*models.VolumeReplication, string, error) {
	return createVolumeReplication(ctx, o.storage, o.temporal, params)
}

func _createVolumeReplication(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.CreateVolumeReplicationParams) (*models.VolumeReplication, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	srcVolume, err := se.GetVolumeByNameAndAccountID(ctx, params.SourceVolumeName, account.ID)
	if err != nil {
		return nil, "", err
	}

	// check for duplicate jobs
	existingJob, err := se.CheckAndFetchDuplicateJobs(ctx, string(datamodel.JobTypeCreateVolumeReplication), utils.GetCoRelationIDFromContext(ctx))
	if err != nil {
		return nil, "", err
	}
	if existingJob != nil {
		replication, err := se.GetVolumeReplication(ctx, existingJob.JobAttributes.ResourceUUID)
		if err != nil {
			logger.Error("Failed to get replication from database", "error", err)
			return nil, "", err
		}
		return convertDataStoreReplicationToModel(replication), existingJob.UUID, nil
	}

	baseEvent := replication.ReplicationEventBase{}
	baseEvent.AddEvent(commonparams.NewEvent(commonparams.EventCreated, time.Now(), commonparams.String("created_by", "CreateReplication")))

	event := replication.CreateReplicationEvent{
		ReplicationEventBase: baseEvent,
		SourceVolume:         *srcVolume,
		SourcePool:           *srcVolume.Pool,
		XCorrelationID:       &params.CorrelationId,
	}

	err = convertCreateReplicationParamsToEventParam(params, &event)
	if err != nil {
		return nil, "", err
	}

	dbRepl, err := validateCreateReplicationParams(ctx, &event, se)
	if err != nil {
		return nil, "", err
	}

	// Validate quota rules for the source volume
	err = validateQuotaRulesForVolume(ctx, se, srcVolume.ID)
	if err != nil {
		return nil, "", err
	}

	dbRepl.AccountID = account.ID
	dbRepl.VolumeID = srcVolume.ID
	volumeRep, err := se.CreateVolumeReplication(ctx, dbRepl)
	if err != nil {
		return nil, "", err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateVolumeReplication),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  dbRepl.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: volumeRep.UUID,
			PoolUUID:     srcVolume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
			_, deleteError := se.DeleteVolumeReplication(ctx, dbRepl)
			if deleteError != nil {
				logger.Error("Failed to delete volume replication after creation job failed", "volume_repl_id", dbRepl.UUID, "error", deleteError)
			}
		}
	}()

	// Set the workflow ID for the job
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.CreateVolumeReplicationWorkflow,
		params,
		volumeRep,
		&event,
	)

	if err != nil {
		logger.Error("Failed to start create volume replication workflow: ", "error", err)
		return nil, "", err
	}

	return convertDataStoreReplicationToModel(volumeRep), createdJob.UUID, nil
}

func _convertCreateReplicationParamsToEventParam(in *commonparams.CreateVolumeReplicationParams, out *replication.CreateReplicationEvent) error {
	bytes, err := replication.JsonMarshal(in)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrorFailedToMarshalModel, err)
	}

	err = replication.JsonUnMarshal(bytes, out)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrorFailedToUnmarshal, err)
	}

	uri := strings.Split(*out.CreateReplicationParams.DestinationVolumeParameters.StoragePool, "/")

	if len(uri) >= 4 {
		// Grab the pool name from the uri of the destination pool
		out.DestinationPoolName = uri[len(uri)-1]
		// Grab the location from the uri of the destination pool
		out.DestinationLocationID = uri[3]
		// Grab the project number from the uri of the destination pool to check for cross project replication
		out.DestinationProjectNumber = uri[1]
	}
	out.SourceProjectNumber = in.AccountName
	out.LocationID = in.LocationId
	out.SourceRegion = in.Region
	out.VolumeResourceID = in.SourceVolumeName
	out.XCorrelationID = &in.CorrelationId

	return nil
}

func (o *GCPOrchestrator) GetMultipleReplications(ctx context.Context, params commonparams.GetMultipleReplicationsParams) ([]commonparams.ReplicationV1beta, error) {
	gcpReplications, err := _getMultipleReplications(ctx, o.storage, params)
	if err != nil {
		return nil, err
	}
	return convertGcpReplicationV1betaToCommon(gcpReplications), nil
}

func (o *GCPOrchestrator) GetMultipleReplicationsByExternalUUID(ctx context.Context, params commonparams.GetMultipleReplicationsByExternalUUIDParams) ([]commonparams.ReplicationV1beta, error) {
	gcpReplications, err := _getMultipleReplicationsByExternalUUID(ctx, o.storage, params)
	if err != nil {
		return nil, err
	}
	return convertGcpReplicationV1betaToCommon(gcpReplications), nil
}

// GetBatchReplications lists volume replications by replication URI only, without resolving the account
// or filtering by account_id (used by batch list replications).
func (o *GCPOrchestrator) GetBatchReplications(ctx context.Context, params commonparams.GetMultipleReplicationsParams) ([]commonparams.ReplicationV1beta, error) {
	if len(params.ReplicationURIs) == 0 {
		return convertGcpReplicationV1betaToCommon(nil), nil
	}
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("uri", "in", params.ReplicationURIs),
	)
	gcpReplications, err := _listAndFetchMultipleReplications(ctx, o.storage, params, filter)
	if err != nil {
		return nil, err
	}
	return convertGcpReplicationV1betaToCommon(gcpReplications), nil
}

func _getMultipleReplications(ctx context.Context, se database.Storage, params commonparams.GetMultipleReplicationsParams) ([]gcpgenserver.ReplicationV1beta, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to get account", "error", err)
		return nil, err
	}

	filterConditions := []*utils2.FilterCondition{
		utils2.NewFilterCondition("account_id", "=", account.ID),
	}
	if len(params.ReplicationURIs) > 0 {
		filterConditions = append(filterConditions, utils2.NewFilterCondition("uri", "in", params.ReplicationURIs))
	}
	filter := utils2.CreateFilterWithConditions(filterConditions...)
	return _listAndFetchMultipleReplications(ctx, se, params, filter)
}

func _listAndFetchMultipleReplications(ctx context.Context, se database.Storage, params commonparams.GetMultipleReplicationsParams, filter *utils2.Filter) ([]gcpgenserver.ReplicationV1beta, error) {
	logger := util.GetLogger(ctx)
	resp := []gcpgenserver.ReplicationV1beta{}
	replications, err := se.ListVolumeReplications(ctx, *filter, database.QueryDepthOne)
	if err != nil {
		logger.Errorf("Failed to list volume replications: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if len(replications) == 0 {
		logger.Warnf("No replications found for URIs %v", params.ReplicationURIs)
		return resp, nil
	}

	// Create a region - replication UUID map
	currentRegion, currentZone, parsingErr := utilParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		logger.Error("Failed to parse current region", "error", parsingErr)
		custErr := vsaerrors.CustomError{
			TrackingID: 0,
			Message:    parsingErr.Message,
			Retriable:  false,
			HttpCode:   nillable.GetIntPtr(500),
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrRegionZoneParsingErrorCurrentRegion, &custErr)
	}

	currentLocation := currentRegion
	if currentZone != "" {
		currentLocation = currentZone
	}

	regionReplicationMap := make(map[string][]*datamodel.VolumeReplication)
	regionProjectMap := make(map[string]string) // region -> unique project numbers
	emptyUUID := uuid.UUID{}

	// Add destination regions with their replications and source regions for job fetching
	for _, replication := range replications {
		// Add destination region with replication
		if replication.ReplicationAttributes.DestinationReplicationUUID != emptyUUID.String() && !nillable.IsNilOrEmpty(&replication.ReplicationAttributes.DestinationLocation) {
			destRegion, _, err := utilParseRegionAndZone(replication.ReplicationAttributes.DestinationLocation)
			if err != nil {
				logger.Error("Failed to parse destination region", "error", err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrRegionZoneParsingErrorDestinationRegion, err)
			}
			regionReplicationMap[destRegion] = append(regionReplicationMap[destRegion], replication)

			var projectNumber string
			if replication.ReplicationAttributes.EndpointType == database.VolumeReplicationEndpointTypeDestination {
				projectNumber, err = utilsParseProjectNumberFromURI(replication.Uri)
			} else {
				projectNumber, err = utilsParseProjectNumberFromURI(replication.RemoteUri)
			}
			if err != nil {
				logger.Error("Failed to parse project number from replication URI", "error", err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrProjectParsingError, err)
			}
			regionProjectMap[destRegion] = projectNumber
		}

		// Add source region to map (without replications) so we can get active jobs from both regions
		if replication.ReplicationAttributes.SourceReplicationUUID != emptyUUID.String() &&
			!nillable.IsNilOrEmpty(&replication.ReplicationAttributes.SourceLocation) {
			srcRegion, _, err := utilParseRegionAndZone(replication.ReplicationAttributes.SourceLocation)
			if err != nil {
				logger.Error("Failed to parse source region", "error", err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrRegionZoneParsingErrorSourceRegion, err)
			}

			// Add source region to map if not already present
			if _, exists := regionReplicationMap[srcRegion]; !exists {
				regionReplicationMap[srcRegion] = []*datamodel.VolumeReplication{}
			}

			// If destination location is customer, add the replication to the source region
			if replication.ReplicationAttributes.DestinationLocation == remoteRegionCustomer {
				regionReplicationMap[srcRegion] = append(regionReplicationMap[srcRegion], replication)
			}

			var projectNumber string
			if replication.ReplicationAttributes.EndpointType == database.VolumeReplicationEndpointTypeDestination {
				projectNumber, err = utilsParseProjectNumberFromURI(replication.RemoteUri)
			} else {
				projectNumber, err = utilsParseProjectNumberFromURI(replication.Uri)
			}
			if err != nil {
				logger.Error("Failed to parse project number from replication URI", "error", err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrProjectParsingError, err)
			}
			regionProjectMap[srcRegion] = projectNumber
		}
	}

	// Add current region to map if it is missing
	if _, ok := regionReplicationMap[currentRegion]; !ok {
		regionReplicationMap[currentRegion] = []*datamodel.VolumeReplication{}
	}

	// Fetch the replications from the respective regions via internal API calls
	list, jobsList, replicationRoleMap, err := getReplicationObjects(ctx, regionReplicationMap, logger, params, regionProjectMap)
	if err != nil {
		logger.Error("Failed to get replication objects", "error", err)
		return nil, err
	}

	// Convert the internal replications to the response format
	for _, repl := range list {
		resp = append(resp, convertInternalReplicationToCCFEModel(*repl, currentLocation, &jobsList, regionReplicationMap, replicationRoleMap))
	}

	return resp, nil
}

func _getMultipleReplicationsByExternalUUID(ctx context.Context, se database.Storage, params commonparams.GetMultipleReplicationsByExternalUUIDParams) ([]gcpgenserver.ReplicationV1beta, error) {
	logger := util.GetLogger(ctx)
	resp := []gcpgenserver.ReplicationV1beta{}

	// Check if replication exists in the database using external_uuid and endpoint_type filters
	// For JSONB field filtering, we need to use PostgreSQL JSONB operators
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("replication_attributes->>'external_uuid'", "in", params.ExternalUUIDs),
		utils2.NewFilterCondition("replication_attributes->>'endpoint_type'", "=", params.EndpointType))

	replications, err := se.ListVolumeReplications(ctx, *filter, database.QueryDepthZero)
	if err != nil {
		logger.Errorf("Failed to list replications with external UUIDs %v and endpoint_type %s: %v", params.ExternalUUIDs, params.EndpointType, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(replications) == 0 {
		logger.Warnf("No replications found with external UUIDs %v and endpoint_type %s", params.ExternalUUIDs, params.EndpointType)
		return resp, nil
	}

	// Convert the replications to the response format
	for _, replication := range replications {
		// Convert datamodel.VolumeReplication to gcpgenserver.ReplicationV1beta
		converted := convertDataStoreReplicationToGcpGenServerModel(replication)
		resp = append(resp, converted)
	}

	return resp, nil
}

// convertDataStoreReplicationToGcpGenServerModel converts a datamodel.VolumeReplication to gcpgenserver.ReplicationV1beta
func convertDataStoreReplicationToGcpGenServerModel(replication *datamodel.VolumeReplication) gcpgenserver.ReplicationV1beta {
	return gcpgenserver.ReplicationV1beta{
		ReplicationId: gcpgenserver.NewOptString(replication.ReplicationAttributes.ExternalUUID),
		ResourceId:    gcpgenserver.NewOptString(replication.Name),
	}
}

func _getReplicationObjects(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger logger.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
	type ReplicationsForProject struct {
		replicationUUIDs []string
		token            string
	}
	replicationList := make([]*googleproxyclient.VolumeReplicationInternalV1beta, 0)
	jobsList := make([]googleproxyclient.InternalJobV1beta, 0)
	replicationRoleMap := make(map[string]string)

	for region, replicationsInRegion := range regionReplicationMap {
		basePath, err := utilsGetPairedRegionUri(region)
		if err != nil {
			logger.Error("Failed to get paired region URI", "region", region, "error", err)
			return nil, nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrRegionZoneParsingErrorPairedRegionURI, err)
		}

		emptyUUID := uuid.UUID{}

		if len(replicationsInRegion) == 0 {
			// No replications found in this region, get all the jobs for the region
			projectNumber := regionProjectMap[region]
			token, err := authGetSignedJwtToken(projectNumber)
			if err != nil {
				logger.Error("Failed to get signed JWT token", "error", err)
				return nil, nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrFailedToGenerateAccessToken, err)
			}

			jobs, err := getActiveReplicationJobs(ctx, basePath, token, region, projectNumber, &params.XCorrelationID)
			if err != nil {
				logger.Error("Failed to get active replication jobs", "error", err, "region", region)
				return nil, nil, nil, err
			}
			jobsList = append(jobsList, jobs...)
			continue
		}

		// Build a map with a list of replication UUIDs for each project
		// Because the replications could use different projects we need to get the token for each project
		replicationsForProjects := make(map[string]ReplicationsForProject)
		for _, replication := range replicationsInRegion {
			projectNumber, err := GetProjectNumberForRegion(replication, region)
			if err != nil {
				return nil, nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrProjectParsingError, err)
			}
			var replicationUUID string
			if replication.ReplicationAttributes != nil {
				if replication.ReplicationAttributes.DestinationReplicationUUID != emptyUUID.String() {
					replicationUUID = replication.ReplicationAttributes.DestinationReplicationUUID
				} else if replication.ReplicationAttributes.SourceReplicationUUID != emptyUUID.String() {
					replicationUUID = replication.ReplicationAttributes.SourceReplicationUUID
				}
			if replicationUUID != "" {
				replicationRoleMap[replicationUUID] = replication.ReplicationAttributes.EndpointType
		} else {
				// UUID not yet assigned; return a partial stub and register the project/token so active jobs are still included in the response.
					replicationRoleMap[replication.UUID] = replication.ReplicationAttributes.EndpointType
					replicationList = append(replicationList, buildInitializingReplication(replication))
					if _, ok := replicationsForProjects[projectNumber]; !ok {
						token, tokenErr := authGetSignedJwtToken(projectNumber)
						if tokenErr != nil {
							return nil, nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrFailedToGenerateAccessToken, tokenErr)
						}
						replicationsForProjects[projectNumber] = ReplicationsForProject{token: token, replicationUUIDs: []string{}}
					}
					continue
				}
		}

		found, ok := replicationsForProjects[projectNumber]
		if !ok {
			token, err := authGetSignedJwtToken(projectNumber)
			if err != nil {
				return nil, nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrFailedToGenerateAccessToken, err)
			}
			replicationsForProjects[projectNumber] = ReplicationsForProject{token: token, replicationUUIDs: []string{replicationUUID}}
		} else {
			// Project already exists in the map so we don't need to get a new token and just append to the UUID list.
			found.replicationUUIDs = append(found.replicationUUIDs, replicationUUID)
			replicationsForProjects[projectNumber] = found
		}
	}

	// Now we have a map of project numbers to replication UUIDs, we can make the API calls
	for projectNumber, replicationsForProject := range replicationsForProjects {
		// Skip internal get-multiple call when all replications in this project are
		// initialising stubs (UUID list is empty); jobs are still fetched below.
		if len(replicationsForProject.replicationUUIDs) == 0 {
			continue
		}
		internalGetReplicationBody := googleproxyclient.ReplicationIDListV1beta{
			ReplicationUUIDs: replicationsForProject.replicationUUIDs,
		}
		list, err := googleProxyInternalGetMultipleReplications(ctx, basePath, projectNumber, region, replicationsForProject.token, internalGetReplicationBody, logger, params)
			if err != nil {
				if err.(*vsaerrors.CustomError).TrackingID != vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsNotFound {
					logger.Error("Failed to get multiple replications from Google Proxy", "error", err, "projectNumber", projectNumber, "region", region)
					return nil, nil, nil, err
				}
			}
			if len(list) == 0 {
				logger.Warn("No replications found for project", "projectNumber", projectNumber, "region", region)
				continue
			}

			// Append the replications to the final list
			for _, replication := range list {
				replicationList = append(replicationList, &replication)
			}
		}

		// Get active replication jobs
		for projectNumber, replicationsForProject := range replicationsForProjects {
			// Get all replication jobs for the project
			jobs, err := getActiveReplicationJobs(ctx, basePath, replicationsForProject.token, region, projectNumber, &params.XCorrelationID)
			if err != nil {
				logger.Error("Failed to get active replication jobs", "error", err, "projectNumber", projectNumber, "region", region)
				return nil, nil, nil, err
			}
			jobsList = append(jobsList, jobs...)
		}
	}
	return replicationList, jobsList, replicationRoleMap, nil
}

// buildInitializingReplication synthesises a VolumeReplicationInternalV1beta from the DB
// record for replications whose VCP UUIDs (DestinationReplicationUUID / SourceReplicationUUID)
// are not yet assigned. This happens during the race window between CreateReplication being
// persisted to VCP's DB and the Temporal workflow writing the VCP UUIDs back.
func buildInitializingReplication(r *datamodel.VolumeReplication) *googleproxyclient.VolumeReplicationInternalV1beta {
	state := r.State
	if state == "" {
		state = datamodel.LifeCycleStateCreating
	}
	stub := &googleproxyclient.VolumeReplicationInternalV1beta{
		// VolumeReplicationUuid must equal r.UUID so that convertInternalReplicationToCCFEModel
		// can key into replicationRoleMap to resolve SOURCE / DESTINATION role.
		VolumeReplicationUuid: googleproxyclient.NewOptString(r.UUID),
		LifeCycleState: googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(
			googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState(strings.ToLower(state)),
		),
		LifeCycleStateDetails: googleproxyclient.NewOptString(r.StateDetails),
		CcfeUri:               googleproxyclient.NewOptString(r.Uri),
		CcfeRemoteUri:         googleproxyclient.NewOptString(r.RemoteUri),
		Name:                  googleproxyclient.NewOptString(r.Name),
	}
	if r.ReplicationAttributes != nil {
		stub.EndpointType = googleproxyclient.VolumeReplicationInternalV1betaEndpointType(
			r.ReplicationAttributes.EndpointType,
		)
		stub.ReplicationSchedule = googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(
			googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule(
				r.ReplicationAttributes.ReplicationSchedule,
			),
		)
		stub.RemoteRegion = r.ReplicationAttributes.DestinationLocation
	}
	return stub
}

func _getActiveReplicationJobs(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
	logger := util.GetLogger(ctx)

	logger.Debug(
		"cvp geActiveReplicationJobs",
		"destBasePath", basePath,
		"projectNumber", projectNumber,
		"locationID", locationID,
	)

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, token, logger)
	params := googleproxyclient.V1betaInternalGetReplicationJobsParams{}
	params.ProjectNumber = projectNumber
	params.LocationId = locationID
	if xCorrelationID != nil {
		params.XCorrelationID = googleproxyclient.OptString{Value: *xCorrelationID, Set: true}
	} else {
		params.XCorrelationID = googleproxyclient.OptString{Value: "", Set: false}
	}

	getReplicationJobsResponse, err := googleProxyClient.Invoker.V1betaInternalGetReplicationJobs(ctx, params)
	if err != nil {
		return nil, err
	}

	switch r := getReplicationJobsResponse.(type) {
	case *googleproxyclient.V1betaInternalGetReplicationJobsOK:
		return r.Jobs, nil
	case *googleproxyclient.V1betaInternalGetReplicationJobsBadRequest:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsGetActiveReplicationJobsBadRequest, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalGetReplicationJobsInternalServerError:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsGetActiveReplicationJobsInternalServerError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalGetReplicationJobsUnauthorized:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsGetActiveReplicationJobsUnauthorized, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalGetReplicationJobsForbidden:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsGetActiveReplicationJobsForbidden, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalGetReplicationJobsNotFound:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsGetActiveReplicationJobsNotFound, errors.New(r.Message))
	default:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsGetActiveReplicationJobsUnknown, errors.New("unknown response type"))
	}
}

func _googleProxyInternalGetMultipleReplications(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger logger.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
	cli := googleproxyclient.GetGProxyClient(basePath, token, logger)
	params := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
		ProjectNumber:  projectNumber,
		LocationId:     location,
		XCorrelationID: googleproxyclient.NewOptString(paramz.XCorrelationID),
	}

	res, err := cli.Invoker.V1betaGetMultipleReplicationsInternal(ctx, &body, params)
	if err != nil {
		logger.Error("Failed to get multiple replications from Google Proxy", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalOK:
		return r.GetReplications(), nil
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalBadRequest:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsBadRequest, errors.New(r.Message))
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalInternalServerError:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsInternalServerError, errors.New(r.Message))
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalUnauthorized:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsUnauthorized, errors.New(r.Message))
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalForbidden:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsForbidden, errors.New(r.Message))
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalNotFound:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsNotFound, errors.New(r.Message))
	default:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGoogleProxyInternalGetMultipleReplicationsUnknown, errors.New("unknown response type"))
	}
}

func _getProjectNumberForRegion(replication *datamodel.VolumeReplication, region string) (string, error) {
	if replication.HybridReplicationAttributes != nil {
		return utilsParseProjectNumberFromURI(replication.Uri)
	}
	if replication.ReplicationAttributes.EndpointType == database.VolumeReplicationEndpointTypeSource {
		return utilsParseProjectNumberFromURI(replication.RemoteUri)
	} else {
		return utilsParseProjectNumberFromURI(replication.Uri)
	}
}

func (o *GCPOrchestrator) ReleaseVolumeReplication(ctx context.Context, replicationUUID string, isCleanup bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return releaseVolumeReplication(ctx, o.storage, o.temporal, replicationUUID, isCleanup)
}

func _releaseVolumeReplication(ctx context.Context, se database.Storage, temporal client.Client, replicationUUID string, isCleanup bool) (*models.VolumeReplication, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)

	dbVolumeReplication, err := se.GetVolumeReplication(ctx, replicationUUID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("Replication not found", "uuid", replicationUUID)
			return nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("VolumeReplication", nil))
		} else {
			logger.Error("Failed to check existing replication", "error", err.Error())
			return nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
	}
	if !isCleanup && dbVolumeReplication.ReplicationAttributes.EndpointType == cvpmodels.VolumeReplicationCVPV1betaEndpointTypeSrc {
		if dbVolumeReplication.State == datamodel.LifeCycleStateCreating ||
			dbVolumeReplication.State == datamodel.LifeCycleStateUpdating ||
			dbVolumeReplication.State == datamodel.LifeCycleStateDeleting {
			return nil, nil, errors.New("Error releasing volume Replication - Volume replication is already transitioning between states")
		}
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeReleaseVolumeReplicationInternal),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  dbVolumeReplication.Uri,
		AccountID:     sql.NullInt64{Int64: dbVolumeReplication.AccountID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbVolumeReplication.UUID,
			PoolUUID:     dbVolumeReplication.ReplicationAttributes.DestinationPoolUUID,
		},
	}
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	if dbVolumeReplication.ReplicationAttributes.EndpointType == cvpmodels.VolumeReplicationCVPV1betaEndpointTypeSrc {
		dbVolumeReplication.State = datamodel.LifeCycleStateDeleting
		dbVolumeReplication.StateDetails = datamodel.LifeCycleStateDeletingDetails

		if err = se.UpdateVolumeReplicationStates(ctx, dbVolumeReplication); err != nil {
			return nil, nil, err
		}
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.ReleaseVolumeReplicationInternalWorkflow,
		dbVolumeReplication,
	)

	if err != nil {
		logger.Error("Failed to execute workflow for release volume replication ", "error", err)
		return nil, nil, err
	}
	return convertDataStoreReplicationToModel(dbVolumeReplication), createdJob, nil
}

func convertInternalReplicationToCCFEModel(in googleproxyclient.VolumeReplicationInternalV1beta, currentLocation string, jobsList *[]googleproxyclient.InternalJobV1beta, regionReplicationMap map[string][]*datamodel.VolumeReplication, replicationRoleMap map[string]string) gcpgenserver.ReplicationV1beta {
	var srcVolUri, dstVolUri string
	var role gcpgenserver.OptReplicationV1betaRole
	emptyUUID := uuid.UUID{}

	// Find the corresponding replication from regionReplicationMap
	dbReplication := &datamodel.VolumeReplication{}
	// Extract region from CcfeUri to search only in the relevant region
	if in.SourceVolumeUuid.Value == emptyUUID.String() || in.DestinationVolumeUuid.Value == emptyUUID.String() {
		for _, replications := range regionReplicationMap {
			for _, replication := range replications {
				// Match by external UUID (VolumeReplicationUuid)
				if replication.UUID == in.VolumeReplicationUuid.Value {
					dbReplication = replication
					break
				}
			}
		}
	}

	var hybridPeeringDetails *gcpgenserver.HybridPeeringV1beta
	var command string
	var passphrase string
	var commandExpiryTime time.Time
	var stateDetailsCode gcpgenserver.OptInt32
	var hybridReplicationType *string
	var clusterLocation gcpgenserver.OptString
	if dbReplication.HybridReplicationAttributes != nil {
		stateDetailsCode = gcpgenserver.NewOptInt32(int32(dbReplication.HybridReplicationAttributes.StateDetailsCode))
		hybridReplicationType = dbReplication.HybridReplicationAttributes.HybridReplicationType
		if dbReplication.ClusterPeer != nil && dbReplication.ClusterPeer.ClusterPeeringAttributes != nil && dbReplication.ClusterPeer.ClusterPeeringAttributes.ClusterLocation != nil {
			clusterLocation = gcpgenserver.NewOptString(*dbReplication.ClusterPeer.ClusterPeeringAttributes.ClusterLocation)
		}
		if dbReplication.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingClusterPeer {
			if dbReplication.ClusterPeer != nil && dbReplication.ClusterPeer.ClusterPeeringAttributes != nil && dbReplication.ClusterPeer.ClusterPeeringAttributes.Command != nil {
				command = *dbReplication.ClusterPeer.ClusterPeeringAttributes.Command
				if dbReplication.ClusterPeer.ClusterPeeringAttributes.PassPhrase != nil {
					passphrase = *dbReplication.ClusterPeer.ClusterPeeringAttributes.PassPhrase
				}
				if dbReplication.ClusterPeer.ClusterPeeringAttributes.ExpiryTime != nil {
					commandExpiryTime = *dbReplication.ClusterPeer.ClusterPeeringAttributes.ExpiryTime
				}
			}
			hybridPeeringDetails = &gcpgenserver.HybridPeeringV1beta{
				Command:           gcpgenserver.NewOptString(command),
				CommandExpiryTime: gcpgenserver.NewOptDateTime(commandExpiryTime),
				Passphrase:        gcpgenserver.NewOptString(passphrase),
			}
		} else if dbReplication.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingSVMPeer {
			var svmPeerCommand string
			if dbReplication.HybridReplicationAttributes.SvmPeerCommand != nil {
				svmPeerCommand = *dbReplication.HybridReplicationAttributes.SvmPeerCommand
			}
			hybridPeeringDetails = &gcpgenserver.HybridPeeringV1beta{
				Command: gcpgenserver.NewOptString(svmPeerCommand),
			}
		} else if dbReplication.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusSVMPeered {
			hybridPeeringDetails = &gcpgenserver.HybridPeeringV1beta{
				Command:           gcpgenserver.NewOptString(command),
				CommandExpiryTime: gcpgenserver.NewOptDateTime(commandExpiryTime),
				Passphrase:        gcpgenserver.NewOptString(passphrase),
			}
		}
		if hybridReplicationType != nil && (nillable.GetString(hybridReplicationType, "") == string(gcpgenserver.ReplicationV1betaHybridReplicationTypeMIGRATION) ||
			nillable.GetString(hybridReplicationType, "") == string(gcpgenserver.ReplicationV1betaHybridReplicationTypeONPREMREPLICATION) ||
			nillable.GetString(hybridReplicationType, "") == string(gcpgenserver.ReplicationV1betaHybridReplicationTypeREVERSEONPREMREPLICATION)) {
			if hybridPeeringDetails == nil {
				hybridPeeringDetails = &gcpgenserver.HybridPeeringV1beta{}
			}
			hybridPeeringDetails.PeerVolumeName = gcpgenserver.NewOptString(dbReplication.HybridReplicationAttributes.PeerVolumeName)
			hybridPeeringDetails.PeerSvmName = gcpgenserver.NewOptString(dbReplication.HybridReplicationAttributes.PeerSvmName)
			if dbReplication.ReplicationAttributes != nil {
				hybridPeeringDetails.PeerClusterName = gcpgenserver.NewOptString(dbReplication.ReplicationAttributes.SourceHostName)
			}
		}
	}

	endpointType := ""
	if replicationRoleMap != nil {
		endpointType = replicationRoleMap[in.VolumeReplicationUuid.Value]
	}
	if endpointType == "dst" {
		role = gcpgenserver.NewOptReplicationV1betaRole(gcpgenserver.ReplicationV1betaRoleDESTINATION)
		srcVolUri = utilGetVolumeUriFromCcfeUri(in.CcfeRemoteUri.Value)
		dstVolUri = utilGetVolumeUriFromCcfeUri(in.CcfeUri.Value)
	} else {
		role = gcpgenserver.NewOptReplicationV1betaRole(gcpgenserver.ReplicationV1betaRoleSOURCE)
		srcVolUri = utilGetVolumeUriFromCcfeUri(in.CcfeRemoteUri.Value)
		dstVolUri = utilGetVolumeUriFromCcfeUri(in.CcfeUri.Value)
	}

	if replication.IsSrcForHybridReplication(dbReplication) {
		srcVolUri = utilGetVolumeUriFromCcfeUri(dbReplication.Uri)
	}

	var sourceReplication gcpgenserver.ReplicationVolumeInformationV1beta
	if in.SourceVolumeUuid.Set && in.SourceVolumeUuid.Value != emptyUUID.String() {
		sourceReplication = gcpgenserver.ReplicationVolumeInformationV1beta{
			VolumeName: gcpgenserver.NewOptString(srcVolUri),
			VolumeId:   gcpgenserver.NewOptString(in.SourceVolumeUuid.Value),
		}
	}

	var destinationReplication gcpgenserver.ReplicationVolumeInformationV1beta
	if in.DestinationVolumeUuid.Set && in.DestinationVolumeUuid.Value != emptyUUID.String() {
		destinationReplication = gcpgenserver.ReplicationVolumeInformationV1beta{
			VolumeName: gcpgenserver.NewOptString(dstVolUri),
			VolumeId:   gcpgenserver.NewOptString(in.DestinationVolumeUuid.Value),
		}
	}

	transferStats := gcpgenserver.TransferStatsV1beta{
		TotalTransferBytes:    gcpgenserver.NewOptFloat64(float64(in.TotalTransferBytes.Value)),
		TotalTransferTimeSecs: gcpgenserver.NewOptFloat64(float64(in.TotalTransferTimeSecs.Value)),
		LastTransferSize:      gcpgenserver.NewOptFloat64(float64(in.LastTransferSize.Value)),
		LastTransferError:     gcpgenserver.NewOptString(in.LastTransferError.Value),
		LastTransferDuration:  gcpgenserver.NewOptFloat64(float64(in.LastTransferDuration.Value)),
		LastTransferEndTime:   gcpgenserver.NewOptDateTime(in.LastTransferEndTime.Value),
		TotalProgress:         gcpgenserver.NewOptFloat64(float64(in.TotalProgress.Value)),
		ProgressLastUpdated:   gcpgenserver.NewOptDateTime(in.ProgressLastUpdated.Value),
		LagTime:               gcpgenserver.NewOptFloat64(float64(in.LagTime.Value)),
	}

	out := gcpgenserver.ReplicationV1beta{
		ReplicationId:                 gcpgenserver.NewOptString(in.VolumeReplicationUuid.Value),
		ResourceId:                    gcpgenserver.NewOptString(in.Name.Value),
		Description:                   gcpgenserver.NewOptString(in.Description.Value),
		Source:                        gcpgenserver.NewOptReplicationVolumeInformationV1beta(sourceReplication),
		Destination:                   gcpgenserver.NewOptReplicationVolumeInformationV1beta(destinationReplication),
		State:                         gcpgenserver.NewOptReplicationV1betaState(mapInternalReplicationStateToCCFEState(in.LifeCycleState.Value)),
		StateDetails:                  gcpgenserver.NewOptString(in.LifeCycleStateDetails.Value),
		StateDetailsCode:              stateDetailsCode,
		ReplicationSchedule:           gcpgenserver.NewOptReplicationV1betaReplicationSchedule(mapInternalReplicationScheduleToCCFEReschedule(in.ReplicationSchedule.Value)),
		MirrorState:                   gcpgenserver.NewOptReplicationV1betaMirrorState(mapInternalReplicationMirrorStateToCCFEMirrorState(in.MirrorState.Value)),
		Healthy:                       gcpgenserver.NewOptBool(in.Healthy.Value),
		TransferStats:                 gcpgenserver.NewOptTransferStatsV1beta(transferStats),
		Created:                       gcpgenserver.NewOptDateTime(in.CreatedAt.Value),
		Role:                          role,
		Labels:                        gcpgenserver.NewOptReplicationV1betaLabels(gcpgenserver.ReplicationV1betaLabels(in.Labels.Value)),
		ClusterLocation:               clusterLocation,
		HybridReplicationUserCommands: gcpgenserver.OptHybridReplicationUserCommandsV1beta{},
	}

	if hybridReplicationType != nil {
		out.HybridReplicationType = gcpgenserver.NewOptReplicationV1betaHybridReplicationType(gcpgenserver.ReplicationV1betaHybridReplicationType(*hybridReplicationType))
	}
	if hybridPeeringDetails != nil {
		out.HybridPeeringDetails = gcpgenserver.NewOptHybridPeeringV1beta(*hybridPeeringDetails)
	}
	// Check active jobs and override state and mirror state if needed
	replicationJobType, hasJob := replicationHasJob(in, jobsList)
	if hasJob {
		switch replicationJobType {
		case string(datamodel.JobTypeDeleteVolumeReplication):
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateDELETING)
			out.StateDetails = gcpgenserver.NewOptString(volumeReplicationCVP1betaLifeCycleStateDeleting)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)

		case string(datamodel.JobTypeCreateVolumeReplication):
			out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStatePREPARING)
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateCREATING)
			out.StateDetails = gcpgenserver.NewOptString(volumeReplicationCVP1betaLifeCycleStateCreation)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)

		case string(datamodel.JobTypeStopVolumeReplication):
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateUPDATING)
			out.StateDetails = gcpgenserver.NewOptString(volumeReplicationCVP1betaLifeCycleStateStopping)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)

		case string(datamodel.JobTypeHybridReplicationInternalEstablish):
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateREADY)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)
			out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStatePREPARING)

		case string(datamodel.JobTypeReverseResumeVolumeReplication):
			out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStatePREPARING)
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateUPDATING)
			out.StateDetails = gcpgenserver.NewOptString(volumeReplicationCVP1betaLifeCycleStateReversing)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)

		case string(datamodel.JobTypeResumeVolumeReplication):
			out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStatePREPARING)
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateUPDATING)
			out.StateDetails = gcpgenserver.NewOptString(volumeReplicationCVP1betaLifeCycleStateResuming)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)

		case string(datamodel.JobTypeUpdateVolumeReplication):
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateUPDATING)
			out.StateDetails = gcpgenserver.NewOptString(volumeReplicationCVP1betaLifeCycleStateUpdating)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)

		case string(datamodel.JobTypeReverseHybridReplicationInternal):
			out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStateSTOPPED)
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStatePENDINGREMOTERESYNC)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)

		default:
			out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStatePREPARING)
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateUPDATING)
			out.StateDetailsCode = gcpgenserver.NewOptInt32(0)
		}
	}

	if dbReplication.HybridReplicationAttributes != nil {
		if in.MirrorState.Value == googleproxyclient.VolumeReplicationInternalV1betaMirrorStateUNINITIALIZED || in.MirrorState.Value == googleproxyclient.VolumeReplicationInternalV1betaMirrorStatePREPARING {
			if dbReplication.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingClusterPeer {
				out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStatePENDINGCLUSTERPEERING)
				out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStatePENDINGPEERING)
				if dbReplication.ClusterPeer != nil && dbReplication.ClusterPeer.StateDetails != "" {
					out.StateDetails = gcpgenserver.NewOptString(dbReplication.ClusterPeer.StateDetails)
				} else {
					out.StateDetails = gcpgenserver.NewOptString(dbReplication.HybridReplicationAttributes.StatusDetails)
				}
			} else if dbReplication.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingSVMPeer || dbReplication.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusSVMPeered {
				out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStatePENDINGSVMPEERING)
				out.StateDetails = gcpgenserver.NewOptString(dbReplication.HybridReplicationAttributes.StatusDetails)
				out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStatePENDINGPEERING)
			}
		}

		vrAttrs := dbReplication.HybridReplicationAttributes
		jobType := replicationJobType

		// Check for PENDING_REMOTE_RESYNC status
		if userCommands := vrAttrs.HybridReplicationUserCommands; vrAttrs.Status == datamodel.HybridReplicationStatusPendingRemoteResync {
			// Snapmirror commands will not be displayed to the user once the JobTypeReverseHybridReplicationInternal has timed out
			if hasJob {
				if userCommands != nil && len(userCommands) > 0 {
					hybridReplicationUserCommands := gcpgenserver.HybridReplicationUserCommandsV1beta{
						Commands: userCommands,
					}
					out.HybridReplicationUserCommands = gcpgenserver.NewOptHybridReplicationUserCommandsV1beta(hybridReplicationUserCommands)
					out.StateDetails = gcpgenserver.NewOptString(vrAttrs.StatusDetails)
				}
			} else {
				// Once JobTypeReverseHybridReplicationInternal has timed out we will update the replication state
				out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateREADY)
				out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStateSTOPPED)
			}
		}

		// Handle EXTERNALLY_MANAGED_REPLICATION status with reverse and resume job
		if hasJob && jobType == string(datamodel.JobTypeReverseResumeVolumeReplication) && vrAttrs.Status == datamodel.HybridReplicationStatusExternalManaged {
			out.HybridReplicationUserCommands = gcpgenserver.OptHybridReplicationUserCommandsV1beta{}
			out.StateDetails = gcpgenserver.NewOptString(volumeReplicationCVP1betaLifeCycleStateReversing)
		}

		// Handle EXTERNALLY_MANAGED_REPLICATION status without job
		if !hasJob && vrAttrs.Status == datamodel.HybridReplicationStatusExternalManaged {
			out.MirrorState = gcpgenserver.NewOptReplicationV1betaMirrorState(gcpgenserver.ReplicationV1betaMirrorStateEXTERNALLYMANAGED)
			out.State = gcpgenserver.NewOptReplicationV1betaState(gcpgenserver.ReplicationV1betaStateEXTERNALLYMANAGEDREPLICATION)
			out.StateDetails = gcpgenserver.NewOptString(vrAttrs.StatusDetails)
			out.Destination = gcpgenserver.OptReplicationVolumeInformationV1beta{}
		}

		// Handle source for hybrid replication
		if replication.IsSrcForHybridReplication(dbReplication) {
			if vrAttrs.HybridReplicationUserCommands != nil && len(vrAttrs.HybridReplicationUserCommands) > 0 {
				hybridReplicationUserCommands := gcpgenserver.HybridReplicationUserCommandsV1beta{
					Commands: vrAttrs.HybridReplicationUserCommands,
				}
				out.HybridReplicationUserCommands = gcpgenserver.NewOptHybridReplicationUserCommandsV1beta(hybridReplicationUserCommands)
				out.StateDetails = gcpgenserver.NewOptString("Please execute the snapmirror commands on Onprem ONTAP")
			}
		}
	}

	return out
}

func replicationHasJob(in googleproxyclient.VolumeReplicationInternalV1beta, jobsList *[]googleproxyclient.InternalJobV1beta) (string, bool) {
	if jobsList == nil {
		return "", false
	}
	for _, job := range *jobsList {
		if in.CcfeUri.Value == job.ResourceName.Value || in.CcfeRemoteUri.Value == job.ResourceName.Value {
			return job.JobType.Value, true
		}
	}
	return "", false
}

func mapInternalReplicationStateToCCFEState(state googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState) gcpgenserver.ReplicationV1betaState {
	// TODO: Add cluster peer states when hybrid replication support is added
	switch state {
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable:
		return gcpgenserver.ReplicationV1betaStateREADY
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating:
		return gcpgenserver.ReplicationV1betaStateCREATING
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleting:
		return gcpgenserver.ReplicationV1betaStateDELETING
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateUpdating:
		return gcpgenserver.ReplicationV1betaStateUPDATING
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateError:
		return gcpgenserver.ReplicationV1betaStateERROR
	case googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDisabled:
		return gcpgenserver.ReplicationV1betaStateDISABLED
	default:
		return gcpgenserver.ReplicationV1betaStateSTATEUNSPECIFIED
	}
}

func mapInternalReplicationScheduleToCCFEReschedule(schedule googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule) gcpgenserver.ReplicationV1betaReplicationSchedule {
	switch schedule {
	case googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly:
		return gcpgenserver.ReplicationV1betaReplicationScheduleHOURLY
	case googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleDaily:
		return gcpgenserver.ReplicationV1betaReplicationScheduleDAILY
	case googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule10minutely:
		return gcpgenserver.ReplicationV1betaReplicationScheduleEVERY10MINUTES
	default:
		return gcpgenserver.ReplicationV1betaReplicationScheduleREPLICATIONSCHEDULEUNSPECIFIED
	}
}

func mapInternalReplicationMirrorStateToCCFEMirrorState(mirrorState googleproxyclient.VolumeReplicationInternalV1betaMirrorState) gcpgenserver.ReplicationV1betaMirrorState {
	switch mirrorState {
	case googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED:
		return gcpgenserver.ReplicationV1betaMirrorStateMIRRORED
	case googleproxyclient.VolumeReplicationInternalV1betaMirrorStateUNINITIALIZED:
		return gcpgenserver.ReplicationV1betaMirrorStateUNINITIALIZED
	case googleproxyclient.VolumeReplicationInternalV1betaMirrorStateSTOPPED:
		return gcpgenserver.ReplicationV1betaMirrorStateSTOPPED
	case googleproxyclient.VolumeReplicationInternalV1betaMirrorStateBASELINETRANSFERRING:
		return gcpgenserver.ReplicationV1betaMirrorStateBASELINETRANSFERRING
	case googleproxyclient.VolumeReplicationInternalV1betaMirrorStateABORTED:
		return gcpgenserver.ReplicationV1betaMirrorStateABORTED
	case googleproxyclient.VolumeReplicationInternalV1betaMirrorStatePREPARING:
		return gcpgenserver.ReplicationV1betaMirrorStatePREPARING
	case googleproxyclient.VolumeReplicationInternalV1betaMirrorStateEXTERNALLYMANAGED:
		return gcpgenserver.ReplicationV1betaMirrorStateEXTERNALLYMANAGED
	case googleproxyclient.VolumeReplicationInternalV1betaMirrorStateTRANSFERRING:
		return gcpgenserver.ReplicationV1betaMirrorStateTRANSFERRING
	default:
		return gcpgenserver.ReplicationV1betaMirrorStateMIRRORSTATEUNSPECIFIED
	}
}

func (o *GCPOrchestrator) ResumeReplication(ctx context.Context, params *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error) {
	return resumeReplication(ctx, o.storage, o.temporal, params)
}

func _resumeReplication(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	event := replication.ResumeReplicationEvent{
		CommonReplicationEventParams: replication.CommonReplicationEventParams{
			VolumeResourceID:      params.VolumeResourceId,
			ReplicationResourceID: params.ReplicationResourceId,
			AccountName:           params.AccountName,
			XCorrelationID:        &params.CorrelationId,
			Location:              params.Region,
			Zone:                  params.Zone,
		},
	}

	if params.Zone != "" {
		event.CommonReplicationEventParams.Location = params.Zone
	}

	existingReplication, existingJobUuid, err := validateReplicationParams(ctx, &event.CommonReplicationEventParams, account.ID, se, false, string(datamodel.JobTypeResumeVolumeReplication))
	if err != nil {
		return nil, "", err
	}
	if existingJobUuid != nil {
		existingReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

		return existingReplication, *existingJobUuid, nil
	}

	dstReplication, err := verifyDstReplicationResume(ctx, &event)
	if err != nil {
		return nil, "", err
	}

	// Verify source and destination quota rules only for file volumes (not for hybrid replication)
	// Quota rules are only applicable to file volumes (volumes with FileProperties)
	// Skip quota verification for hybrid replication as HybridReplicationAttributes indicates different replication path
	if event.ReplicationModel != nil && event.ReplicationModel.Volume != nil && event.ReplicationModel.Volume.VolumeAttributes != nil && event.ReplicationModel.Volume.VolumeAttributes.FileProperties != nil && event.ReplicationModel.HybridReplicationAttributes == nil {
		// Verify source quota rules are ready before resuming replication
		err = verifySourceQuotaRules(ctx, &event)
		if err != nil {
			return nil, "", err
		}

		// Verify destination quota rules are not in transitioning state
		err = verifyDestinationQuotaRules(ctx, &event)
		if err != nil {
			return nil, "", err
		}
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeResumeVolumeReplication),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  event.ReplicationModel.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: event.ReplicationModel.UUID,
			PoolUUID:     event.ReplicationModel.Volume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.ResumeReplicationWorkflow,
		params,
		&event,
	)
	if err != nil {
		logger.Error("Failed to execute workflow", "error", err)
		return nil, "", err
	}

	dstReplication.State = datamodel.LifeCycleStateUpdating
	dstReplication.StateDetails = datamodel.LifeCycleStateUpdatingDetails
	dstReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

	return dstReplication, createdJob.UUID, nil
}

func (o *GCPOrchestrator) UpdateReplication(ctx context.Context, params *commonparams.UpdateReplicationParams) (*models.VolumeReplication, string, error) {
	return updateReplication(ctx, o.storage, o.temporal, params)
}

func _updateReplication(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.UpdateReplicationParams) (*models.VolumeReplication, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	event := replication.UpdateReplicationEvent{
		CommonReplicationEventParams: replication.CommonReplicationEventParams{
			VolumeResourceID:      params.VolumeResourceId,
			ReplicationResourceID: params.ReplicationResourceId,
			AccountName:           params.AccountName,
			XCorrelationID:        &params.CorrelationId,
			Location:              params.Region,
			Zone:                  params.Zone,
		},
		ReplicationSchedule: params.ReplicationSchedule,
		Description:         params.Description,
		Labels:              params.Labels,
		ClusterLocation:     params.ClusterLocation,
	}

	if params.Zone != "" {
		event.CommonReplicationEventParams.Location = params.Zone
	}

	existingReplication, existingJobUuid, err := validateReplicationParams(ctx, &event.CommonReplicationEventParams, account.ID, se, false, string(datamodel.JobTypeUpdateVolumeReplication))
	if err != nil {
		return nil, "", err
	}
	if existingJobUuid != nil {
		existingReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

		return existingReplication, *existingJobUuid, nil
	}

	dstReplication, err := validateReplicationUpdate(ctx, &event)
	if err != nil {
		return nil, "", err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateVolumeReplication),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  event.ReplicationModel.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: event.ReplicationModel.UUID,
			PoolUUID:     event.ReplicationModel.Volume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		replicationWorkflows.UpdateVolumeReplicationWorkflow,
		params,
		&event,
	)
	if err != nil {
		logger.Error("Failed to execute workflow", "error", err)
		return nil, "", err
	}

	dstReplication.State = datamodel.LifeCycleStateUpdating
	dstReplication.StateDetails = datamodel.LifeCycleStateUpdatingDetails
	dstReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

	return dstReplication, createdJob.UUID, nil
}

func (o *GCPOrchestrator) ResumeReplicationInternal(ctx context.Context, volumeReplicationId, accountName string, forceResume bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return resumeReplicationInternal(ctx, o.storage, o.temporal, volumeReplicationId, accountName, forceResume)
}

func _resumeReplicationInternal(ctx context.Context, se database.Storage, temporal client.Client, volumeReplicationId, accountName string, forceResume bool) (*models.VolumeReplication, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		logger.Error("Failed to get or create account", "error", err)
		return nil, nil, err
	}

	replicationDb, err := se.GetVolumeReplication(ctx, volumeReplicationId)
	if err != nil {
		return nil, nil, err
	}

	replicationDb.State = datamodel.LifeCycleStateUpdating
	replicationDb.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	err = se.UpdateVolumeReplicationStates(ctx, replicationDb)
	if err != nil {
		logger.Error("Failed to update volume replication states in database", "error", err)
		return nil, nil, err
	}

	replicationDb.Account = &datamodel.Account{
		Name: accountName,
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeResumeVolumeReplicationInternal),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  replicationDb.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: replicationDb.UUID,
			PoolUUID:     replicationDb.Volume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
			// Set replication state to ERROR if workflow fails to start
			replicationDb.State = datamodel.LifeCycleStateError
			replicationDb.StateDetails = err.Error()
			if stateErr := se.UpdateVolumeReplicationStates(ctx, replicationDb); stateErr != nil {
				logger.Error("Failed to set replication state to ERROR", "replicationUUID", replicationDb.UUID, "error", stateErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.ResumeInternalVolumeReplicationWorkflow,
		replicationDb,
		forceResume,
	)
	if err != nil {
		logger.Error("Failed to execute workflow for resuming volume replication", "error", err)
		return nil, nil, err
	}

	return convertDataStoreReplicationToModel(replicationDb), createdJob, nil
}

// GetReplication gets the specified replication
func (o *GCPOrchestrator) GetReplication(ctx context.Context, volumeReplicationId string) (*models.VolumeReplication, error) {
	se := o.storage

	replication, err := se.GetVolumeReplication(ctx, volumeReplicationId)
	if err != nil {
		return nil, err
	}

	return convertDataStoreReplicationToModel(replication), nil
}

func (o *GCPOrchestrator) DeleteReplicationInternal(ctx context.Context, volumeReplicationId string, cleanupAfterReverse bool, isCleanup bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return deleteReplicationInternal(ctx, o.storage, o.temporal, volumeReplicationId, cleanupAfterReverse, isCleanup)
}

func _deleteReplicationInternal(ctx context.Context, se database.Storage, temporal client.Client, volumeReplicationId string, cleanupAfterReverse bool, isCleanup bool) (*models.VolumeReplication, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)

	dbVolumeReplication, err := se.GetVolumeReplication(ctx, volumeReplicationId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Warn("Volume replication not found", "volumeReplicationId", volumeReplicationId)
			return nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("replication", nil))
		}
		return nil, nil, err
	}

	// State validation logic differs based on isCleanup flag
	if isCleanup {
		// For cleanup operations, only check for Updating or Deleting states
		// Allow cleanup even if replication is in Creating state
		if dbVolumeReplication.State == datamodel.LifeCycleStateUpdating ||
			dbVolumeReplication.State == datamodel.LifeCycleStateDeleting {
			return nil, nil, errors.New("Error deleting volume Replication - Volume replication is already transitioning between states")
		}
	} else {
		// For regular delete operations, check for Creating, Updating, or Deleting states
		if dbVolumeReplication.State == datamodel.LifeCycleStateCreating ||
			dbVolumeReplication.State == datamodel.LifeCycleStateUpdating ||
			dbVolumeReplication.State == datamodel.LifeCycleStateDeleting {
			return nil, nil, errors.New("Error deleting volume Replication - Volume replication is already transitioning between states")
		}
	}

	previousState := dbVolumeReplication.State
	previousStateDetails := dbVolumeReplication.StateDetails
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteVolumeReplicationInternal),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  dbVolumeReplication.Uri,
		AccountID:     sql.NullInt64{Int64: dbVolumeReplication.AccountID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         dbVolumeReplication.UUID,
			PoolUUID:             dbVolumeReplication.Volume.Pool.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	if !cleanupAfterReverse {
		dbVolumeReplication.State = datamodel.LifeCycleStateDeleting
		dbVolumeReplication.StateDetails = datamodel.LifeCycleStateDeletingDetails

		if err = se.UpdateVolumeReplicationStates(ctx, dbVolumeReplication); err != nil {
			return nil, nil, err
		}
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.DeleteInternalVolumeReplicationWorkflow,
		dbVolumeReplication,
		cleanupAfterReverse,
		isCleanup,
	)

	if err != nil {
		logger.Error("Failed to execute workflow for volume replication deletion", "error", err)
		return nil, nil, err
	}

	return convertDataStoreReplicationToModel(dbVolumeReplication), createdJob, nil
}

func (o *GCPOrchestrator) DeleteReplication(ctx context.Context, params *commonparams.DeleteReplicationParams, cleanupResourcesJobId string, isCleanUp bool) (*models.VolumeReplication, string, error) {
	return deleteReplication(ctx, o.storage, o.temporal, params, cleanupResourcesJobId, isCleanUp)
}

func _deleteReplication(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.DeleteReplicationParams, cleanupResourcesJobId string, isCleanUp bool) (*models.VolumeReplication, string, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to get or create account", "error", err)

		return nil, "", err
	}

	// Extract job UUID from cleanupResourcesJobId
	// Format: "/v1beta/projects/242512777037/locations/us-central1/operations/6294efb1-c7c1-4742-a014-425f774dc986"
	var jobUUID string
	if cleanupResourcesJobId != "" {
		jobUUID, err = utils.ValidateOperationUri(cleanupResourcesJobId)
		if err != nil {
			return nil, "", err
		}

		// Get job by UUID to extract resource name
		cleanupJob, err := se.GetJob(ctx, jobUUID)
		if err != nil {
			logger.Debug("Failed to get cleanup job by UUID", "jobUUID", jobUUID, "error", err)
			return nil, "", err
		}

		// Parse resource name to extract components using existing utility functions
		// Format: "projects/45110233509/locations/australia-southeast1-a/volumes/mrasrc1255/replications/replicationtest581"
		resourceName := cleanupJob.ResourceName
		if resourceName != "" {
			// Extract replication name using dedicated utility function
			replicationName, err := utils.GetReplicationNameFromURI(resourceName)
			if err != nil {
				return nil, "", err
			}
			if replicationName != params.ReplicationResourceId {
				return nil, "", vsaerrors.NewVCPError(vsaerrors.ErrDeleteVolumeReplication, errors.New("Mismatch between replication resource ID in delete request and cleanup job"))
			}
		}
	}

	event := replication.DeleteReplicationEvent{
		CommonReplicationEventParams: replication.CommonReplicationEventParams{
			VolumeResourceID:      params.VolumeResourceId,
			ReplicationResourceID: params.ReplicationResourceId,
			AccountName:           params.AccountName,
			XCorrelationID:        &params.CorrelationId,
			Location:              params.Region,
			Zone:                  params.Zone,
		},
	}

	if params.Zone != "" {
		event.CommonReplicationEventParams.Location = params.Zone
	}

	existingReplication, existingJobUuid, err := validateReplicationParams(ctx, &event.CommonReplicationEventParams, account.ID, se, isCleanUp, string(datamodel.JobTypeDeleteVolumeReplication))
	if err != nil {
		return nil, "", err
	}
	if existingJobUuid != nil {
		existingReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

		return existingReplication, *existingJobUuid, nil
	}

	dstReplication := &models.VolumeReplication{
		ReplicationAttributes: &models.ReplicationDetails{
			EndpointType: event.ReplicationModel.ReplicationAttributes.EndpointType,
		},
	}

	// Skip the mirror-state gate for a replication still in CREATING state: the delete workflow will
	// cancel the in-flight create and clean up, matching how the cleanup path handles CREATING resources.
	if !isCleanUp && event.ReplicationModel.State != datamodel.LifeCycleStateCreating {
		dstReplication, err = VerifyReplicationDelete(ctx, &event)
		if err != nil {
			return nil, "", err
		}
	}

	// Get previous state from database replication model
	var previousState, previousStateDetails string
	if event.ReplicationModel != nil {
		previousState = event.ReplicationModel.State
		previousStateDetails = event.ReplicationModel.StateDetails
	} else {
		// Fallback: try to get from database
		dbReplication, dbErr := se.GetVolumeReplication(ctx, event.CommonReplicationEventParams.ReplicationResourceID)
		if dbErr == nil && dbReplication != nil {
			previousState = dbReplication.State
			previousStateDetails = dbReplication.StateDetails
		}
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteVolumeReplication),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  event.ReplicationModel.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         event.ReplicationModel.UUID,
			PoolUUID:             event.ReplicationModel.Volume.Pool.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	var workflowFunc interface{}
	if isCleanUp {
		workflowFunc = replicationWorkflows.ReplicationCleanupWorkflow
	} else if event.ReplicationModel != nil && event.ReplicationModel.HybridReplicationAttributes != nil {
		workflowFunc = replicationWorkflows.HybridReplicationDeleteWorkflow
	} else {
		workflowFunc = replicationWorkflows.ReplicationDeleteWorkflow
	}

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflowFunc,
		params,
		&event,
	)

	if err != nil {
		logger.Error("Failed to execute workflow", "error", err)
		return nil, "", err
	}
	dstReplication.State = datamodel.LifeCycleStateDeleting
	dstReplication.StateDetails = datamodel.LifeCycleStateDeletingDetails

	return dstReplication, createdJob.UUID, nil
}

func (o *GCPOrchestrator) SyncReplication(ctx context.Context, params *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error) {
	return syncReplication(ctx, o.storage, o.temporal, params)
}

func _syncReplication(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	event := replication.ResumeReplicationEvent{
		CommonReplicationEventParams: replication.CommonReplicationEventParams{
			VolumeResourceID:      params.VolumeResourceId,
			ReplicationResourceID: params.ReplicationResourceId,
			AccountName:           params.AccountName,
			XCorrelationID:        &params.CorrelationId,
			Location:              params.Region,
			Zone:                  params.Zone,
		},
	}

	if params.Zone != "" {
		event.CommonReplicationEventParams.Location = params.Zone
	}

	existingReplication, existingJobUuid, err := validateReplicationParams(ctx, &event.CommonReplicationEventParams, account.ID, se, false, string(datamodel.JobTypeSyncVolumeReplication))
	if err != nil {
		return nil, "", err
	}
	if existingJobUuid != nil {
		existingReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

		return existingReplication, *existingJobUuid, nil
	}

	dstReplication, err := verifyDstReplicationSync(ctx, &event)
	if err != nil {
		return nil, "", err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeSyncVolumeReplication),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  event.ReplicationModel.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: event.ReplicationModel.UUID,
			PoolUUID:     event.ReplicationModel.Volume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.ResumeReplicationWorkflow,
		params,
		&event,
	)
	if err != nil {
		logger.Error("Failed to execute workflow", "error", err)
		return nil, "", err
	}

	dstReplication.State = datamodel.LifeCycleStateUpdating
	dstReplication.StateDetails = datamodel.LifeCycleStateSyncDetails
	dstReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

	return dstReplication, createdJob.UUID, nil
}

func (o *GCPOrchestrator) ReverseReplicationInternal(ctx context.Context, volumeReplicationId, accountName string) (*models.VolumeReplication, *datamodel.Job, error) {
	return reverseReplicationInternal(ctx, o.storage, o.temporal, volumeReplicationId, accountName)
}

func _reverseReplicationInternal(ctx context.Context, se database.Storage, temporal client.Client, volumeReplicationId, accountName string) (*models.VolumeReplication, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		logger.Error("Failed to get or create account", "error", err)
		return nil, nil, err
	}

	replicationDb, err := se.GetVolumeReplication(ctx, volumeReplicationId)
	if err != nil {
		return nil, nil, err
	}

	replicationDb.State = datamodel.LifeCycleStateUpdating
	replicationDb.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	err = se.UpdateVolumeReplicationStates(ctx, replicationDb)
	if err != nil {
		logger.Error("Failed to update volume replication states in database", "error", err)
		return nil, nil, err
	}

	replicationDb.Account = &datamodel.Account{
		Name: accountName,
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeReverseVolumeReplicationInternal),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  replicationDb.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: replicationDb.UUID,
			PoolUUID:     replicationDb.Volume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
			// Set replication state to ERROR if workflow fails to start
			replicationDb.State = datamodel.LifeCycleStateError
			replicationDb.StateDetails = err.Error()
			if stateErr := se.UpdateVolumeReplicationStates(ctx, replicationDb); stateErr != nil {
				logger.Error("Failed to set replication state to ERROR", "replicationUUID", replicationDb.UUID, "error", stateErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		replicationWorkflows.ReverseInternalVolumeReplicationWorkflow,
		replicationDb,
	)
	if err != nil {
		logger.Error("Failed to execute workflow for reversing volume replication", "error", err)
		return nil, nil, err
	}

	return convertDataStoreReplicationToModel(replicationDb), createdJob, nil
}

func (o *GCPOrchestrator) ReverseAndResumeReplication(ctx context.Context, params *commonparams.ReverseAndResumeReplicationParams) (*models.VolumeReplication, *string, error) {
	return reverseAndResumeReplication(ctx, o.storage, o.temporal, params)
}

func _reverseAndResumeReplication(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ReverseAndResumeReplicationParams) (*models.VolumeReplication, *string, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to get or create account", "error", err)
		return nil, nil, err
	}

	event := replication.ReverseReplicationEvent{
		CommonReplicationEventParams: replication.CommonReplicationEventParams{
			VolumeResourceID:      params.VolumeResourceId,
			ReplicationResourceID: params.ReplicationResourceId,
			AccountName:           params.AccountName,
			XCorrelationID:        &params.CorrelationId,
			Location:              params.Region,
			Zone:                  params.Zone,
		},
	}

	if params.Zone != "" {
		event.CommonReplicationEventParams.Location = params.Zone
	}

	existingReplication, existingJobUuid, err := validateReplicationParams(ctx, &event.CommonReplicationEventParams, account.ID, se, false, string(datamodel.JobTypeReverseResumeVolumeReplication))
	if err != nil {
		return nil, nil, err
	}

	var isHybridReplication bool
	if event.ReplicationModel != nil && event.ReplicationModel.HybridReplicationAttributes != nil {
		hybridReplicationAttributes := event.ReplicationModel.HybridReplicationAttributes
		isHybridReplication = nillable.GetString(hybridReplicationAttributes.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) || nillable.GetString(hybridReplicationAttributes.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		if !isHybridReplication {
			return nil, nil, errors.NewUserInputValidationErr("Reverse is not allowed for replications created by migration flow.")
		}
	}
	if existingJobUuid != nil {
		existingReplication.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

		return existingReplication, existingJobUuid, nil
	}

	replicationDb, err := verifyDstReplicationReverse(ctx, &event)
	if err != nil {
		return nil, nil, err
	}

	// Verify source and destination quota rules only for file volumes (not for hybrid replication)
	// Quota rules are only applicable to file volumes (volumes with FileProperties)
	// Skip quota verification for hybrid replication as HybridReplicationAttributes indicates different replication path
	if event.ReplicationModel != nil && event.ReplicationModel.Volume != nil && event.ReplicationModel.Volume.VolumeAttributes != nil && event.ReplicationModel.Volume.VolumeAttributes.FileProperties != nil && event.ReplicationModel.HybridReplicationAttributes == nil {
		// Verify new source (current destination) quota rules are ready before reversing replication
		err = verifyNewSourceQuotaRulesReverse(ctx, &event)
		if err != nil {
			return nil, nil, err
		}

		// Verify new destination (current source) quota rules are not in transitioning state
		err = verifyNewDestinationQuotaRulesReverse(ctx, &event)
		if err != nil {
			return nil, nil, err
		}
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeReverseResumeVolumeReplication),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  event.ReplicationModel.Uri,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: event.ReplicationModel.UUID,
			PoolUUID:     event.ReplicationModel.Volume.Pool.UUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	// Check if this is a hybrid replication and route to appropriate workflow
	var workflowFunc interface{}
	// We need to check the datamodel.VolumeReplication which has HybridReplicationAttributes of type *datamodel.HybridReplicationAttribute
	if isHybridReplication {
		logger.Infof("Detected hybrid reverse replication, routing to ReverseHybridReplicationWorkflow")
		workflowFunc = replicationWorkflows.ReverseHybridReplicationWorkflow
	} else {
		logger.Infof("Standard reverse replication, routing to ReverseAndResumeVolumeReplicationWorkflow")
		workflowFunc = replicationWorkflows.ReverseAndResumeVolumeReplicationWorkflow
	}

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflowFunc,
		params,
		&event,
	)

	if err != nil {
		logger.Error("Failed to execute workflow", "error", err)
		return nil, nil, err
	}

	replicationDb.State = datamodel.LifeCycleStateUpdating
	replicationDb.StateDetails = datamodel.LifeCycleStateUpdatingDetails
	replicationDb.ReplicationAttributes.EndpointType = event.ReplicationModel.ReplicationAttributes.EndpointType

	return replicationDb, &createdJob.UUID, nil
}

// convertGcpReplicationV1betaToCommon converts []gcpgenserver.ReplicationV1beta to []commonparams.ReplicationV1beta
func convertGcpReplicationV1betaToCommon(gcpReplications []gcpgenserver.ReplicationV1beta) []commonparams.ReplicationV1beta {
	commonReplications := make([]commonparams.ReplicationV1beta, 0, len(gcpReplications))
	for _, gcpRepl := range gcpReplications {
		commonRepl := commonparams.ReplicationV1beta{}

		if gcpRepl.ReplicationId.IsSet() {
			val := gcpRepl.ReplicationId.Value
			commonRepl.ReplicationId = &val
		}
		if gcpRepl.ResourceId.IsSet() {
			val := gcpRepl.ResourceId.Value
			commonRepl.ResourceId = &val
		}
		if gcpRepl.Description.IsSet() {
			val := gcpRepl.Description.Value
			commonRepl.Description = &val
		}
		if gcpRepl.Source.IsSet() {
			src := &commonparams.ReplicationVolumeInformationV1beta{}
			if gcpRepl.Source.Value.VolumeName.IsSet() {
				val := gcpRepl.Source.Value.VolumeName.Value
				src.VolumeName = &val
			}
			if gcpRepl.Source.Value.VolumeId.IsSet() {
				val := gcpRepl.Source.Value.VolumeId.Value
				src.VolumeId = &val
			}
			commonRepl.Source = src
		}
		if gcpRepl.Destination.IsSet() {
			dst := &commonparams.ReplicationVolumeInformationV1beta{}
			if gcpRepl.Destination.Value.VolumeName.IsSet() {
				val := gcpRepl.Destination.Value.VolumeName.Value
				dst.VolumeName = &val
			}
			if gcpRepl.Destination.Value.VolumeId.IsSet() {
				val := gcpRepl.Destination.Value.VolumeId.Value
				dst.VolumeId = &val
			}
			commonRepl.Destination = dst
		}
		if gcpRepl.State.IsSet() {
			val := string(gcpRepl.State.Value)
			commonRepl.State = &val
		}
		if gcpRepl.StateDetails.IsSet() {
			val := gcpRepl.StateDetails.Value
			commonRepl.StateDetails = &val
		}
		if gcpRepl.StateDetailsCode.IsSet() {
			val := gcpRepl.StateDetailsCode.Value
			commonRepl.StateDetailsCode = &val
		}
		if gcpRepl.Role.IsSet() {
			val := string(gcpRepl.Role.Value)
			commonRepl.Role = &val
		}
		if gcpRepl.ReplicationSchedule.IsSet() {
			val := string(gcpRepl.ReplicationSchedule.Value)
			commonRepl.ReplicationSchedule = &val
		}
		if gcpRepl.MirrorState.IsSet() {
			val := string(gcpRepl.MirrorState.Value)
			commonRepl.MirrorState = &val
		}
		if gcpRepl.Healthy.IsSet() {
			val := gcpRepl.Healthy.Value
			commonRepl.Healthy = &val
		}
		if gcpRepl.TransferStats.IsSet() {
			ts := &commonparams.TransferStatsV1beta{}
			if gcpRepl.TransferStats.Value.TotalTransferBytes.IsSet() {
				val := gcpRepl.TransferStats.Value.TotalTransferBytes.Value
				ts.TotalTransferBytes = &val
			}
			if gcpRepl.TransferStats.Value.TotalTransferTimeSecs.IsSet() {
				val := gcpRepl.TransferStats.Value.TotalTransferTimeSecs.Value
				ts.TotalTransferTimeSecs = &val
			}
			if gcpRepl.TransferStats.Value.LastTransferSize.IsSet() {
				val := gcpRepl.TransferStats.Value.LastTransferSize.Value
				ts.LastTransferSize = &val
			}
			if gcpRepl.TransferStats.Value.LastTransferError.IsSet() {
				val := gcpRepl.TransferStats.Value.LastTransferError.Value
				ts.LastTransferError = &val
			}
			if gcpRepl.TransferStats.Value.LastTransferDuration.IsSet() {
				val := gcpRepl.TransferStats.Value.LastTransferDuration.Value
				ts.LastTransferDuration = &val
			}
			if gcpRepl.TransferStats.Value.LastTransferEndTime.IsSet() {
				val := gcpRepl.TransferStats.Value.LastTransferEndTime.Value
				ts.LastTransferEndTime = &val
			}
			if gcpRepl.TransferStats.Value.TotalProgress.IsSet() {
				val := gcpRepl.TransferStats.Value.TotalProgress.Value
				ts.TotalProgress = &val
			}
			if gcpRepl.TransferStats.Value.ProgressLastUpdated.IsSet() {
				val := gcpRepl.TransferStats.Value.ProgressLastUpdated.Value
				ts.ProgressLastUpdated = &val
			}
			if gcpRepl.TransferStats.Value.LagTime.IsSet() {
				val := gcpRepl.TransferStats.Value.LagTime.Value
				ts.LagTime = &val
			}
			commonRepl.TransferStats = ts
		}
		if gcpRepl.Created.IsSet() {
			val := gcpRepl.Created.Value
			commonRepl.Created = &val
		}
		if gcpRepl.Labels.IsSet() {
			commonRepl.Labels = gcpRepl.Labels.Value
		}
		if gcpRepl.ClusterLocation.IsSet() {
			val := gcpRepl.ClusterLocation.Value
			commonRepl.ClusterLocation = &val
		}
		if gcpRepl.HybridReplicationType.IsSet() {
			val := string(gcpRepl.HybridReplicationType.Value)
			commonRepl.HybridReplicationType = &val
		}
		if gcpRepl.HybridPeeringDetails.IsSet() {
			gcpPeering := gcpRepl.HybridPeeringDetails.Value
			commonPeering := &commonparams.HybridPeeringV1beta{}
			if gcpPeering.SubnetIp.IsSet() {
				val := gcpPeering.SubnetIp.Value
				commonPeering.SubnetIp = &val
			}
			if gcpPeering.Command.IsSet() {
				val := gcpPeering.Command.Value
				commonPeering.Command = &val
			}
			if gcpPeering.Passphrase.IsSet() {
				val := gcpPeering.Passphrase.Value
				commonPeering.Passphrase = &val
			}
			if gcpPeering.CommandExpiryTime.IsSet() {
				val := time.Time(gcpPeering.CommandExpiryTime.Value)
				commonPeering.CommandExpiryTime = &val
			}
			if gcpPeering.PeerVolumeName.IsSet() {
				val := gcpPeering.PeerVolumeName.Value
				commonPeering.PeerVolumeName = &val
			}
			if gcpPeering.PeerClusterName.IsSet() {
				val := gcpPeering.PeerClusterName.Value
				commonPeering.PeerClusterName = &val
			}
			if gcpPeering.PeerSvmName.IsSet() {
				val := gcpPeering.PeerSvmName.Value
				commonPeering.PeerSvmName = &val
			}
			commonRepl.HybridPeeringDetails = commonPeering
		}
		if gcpRepl.HybridReplicationUserCommands.IsSet() {
			gcpCommands := gcpRepl.HybridReplicationUserCommands.Value
			commonRepl.HybridReplicationUserCommands = &commonparams.HybridReplicationUserCommandsV1beta{
				Commands: gcpCommands.Commands,
			}
		}

		commonReplications = append(commonReplications, commonRepl)
	}
	return commonReplications
}
