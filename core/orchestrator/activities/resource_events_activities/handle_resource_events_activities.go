package resource_events_activities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/async"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/resource_events"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
)

const (
	ErrSignedTokenFailed = "SignedTokenFailedError"
)

var (
	PollCvpOperationForWorkflow = pollCvpOperationForWorkflow
)

type ResourceEventsActivity struct {
	SE        database.Storage
	Scheduler scheduler.Scheduler
}

func (a *ResourceEventsActivity) HandleResourceEventCheckForVCPActivity(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	switch params.ResourceType {
	case common.ResourceStateV1ResourceTypeKmsConfig:
		return a.checkKmsConfigExistence(ctx, params)
	case common.ResourceStateV1ResourceTypeStoragePool:
		return a.checkStoragePoolExistence(ctx, params)
	case common.ResourceStateV1ResourceTypeSnapshot:
		return a.checkSnapshotExistence(ctx, params)
	case common.ResourceStateV1ResourceTypeVolume:
		return a.checkVolumeExistence(ctx, params)
	case common.ResourceStateV1ResourceTypeBackupPolicy:
		return a.checkBackupPolicyExistence(ctx, params)
	case common.ResourceStateV1ResourceTypeAD:
		return a.checkActiveDirectoryExistence(ctx, params)
	default:
		return false, errors.New("unsupported resource type")
	}
}

func (a *ResourceEventsActivity) checkKmsConfigExistence(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	_, err := a.SE.GetKmsConfig(ctx, params.ResourceId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	return true, nil
}

func (a *ResourceEventsActivity) checkStoragePoolExistence(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	account, err := a.SE.GetAccount(ctx, params.ProjectNumber)
	if err != nil {
		return false, err
	}
	exists, err := a.SE.PoolExistsForAccount(ctx, params.ResourceId, account.ID)
	if err != nil {
		return false, err
	}
	if !exists {
		notFoundErr := errors.NewNotFoundErr("Pool", &params.ResourceId)
		return false, temporal.NewNonRetryableApplicationError(notFoundErr.Error(), ErrTypeResourceNotFound, notFoundErr)
	}
	return true, nil
}

func (a *ResourceEventsActivity) checkSnapshotExistence(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	account, err := a.SE.GetAccount(ctx, params.ProjectNumber)
	if err != nil {
		return false, err
	}
	volume, err := a.SE.GetVolumeWithAccountID(ctx, params.ParentResourceID, account.ID)
	if err != nil {
		return false, err
	}

	_, err = a.SE.GetSnapshotByUUID(ctx, params.ResourceId, account.ID, volume.ID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	return true, nil
}

func (a *ResourceEventsActivity) checkVolumeExistence(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	_, err := a.SE.GetVolume(ctx, params.ResourceId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	return true, nil
}

func (a *ResourceEventsActivity) checkBackupPolicyExistence(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	account, err := a.SE.GetAccount(ctx, params.ProjectNumber)
	if err != nil {
		return false, err
	}

	_, err = a.SE.GetBackupPolicyByUUIDAndOwnerID(ctx, params.ResourceId, account.ID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	return true, nil
}

func (a *ResourceEventsActivity) checkActiveDirectoryExistence(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	ad, err := a.SE.GetActiveDirectoryByUUID(ctx, params.ResourceId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	if ad == nil {
		notFoundErr := errors.New("active directory not found")
		return false, temporal.NewNonRetryableApplicationError(notFoundErr.Error(), ErrTypeResourceNotFound, notFoundErr)
	}
	return true, nil
}

func (a *ResourceEventsActivity) handleActiveDirectory(ctx context.Context, params *common.HandleResourceEventParams, state string, stateDetails string) (bool, error) {
	logger := util.GetLogger(ctx)

	ad, err := a.SE.GetActiveDirectoryByUUID(ctx, params.ResourceId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	if ad == nil {
		notFoundErr := errors.New("active directory not found")
		return false, temporal.NewNonRetryableApplicationError(notFoundErr.Error(), ErrTypeResourceNotFound, notFoundErr)
	}

	// For ON events, determine the AD state based on SVM usage.
	// If any SVM references this AD, mark it as IN_USE; otherwise READY.
	// For OFF events, apply the passed state (e.g. DISABLED) directly.
	if state != datamodel.LifeCycleStateDisabled {
		svms, err := a.SE.GetSVMsUsingActiveDirectory(ctx, ad.ID)
		if err != nil {
			logger.Error("Failed to check SVMs using active directory", "error", err, "active_directory_id", ad.ID)
			return false, err
		}
		if len(svms) > 0 {
			state = datamodel.LifeCycleStateInUse
			stateDetails = datamodel.LifeCycleStateInUseDetails
		} else {
			state = datamodel.LifeCycleStateREADY
			stateDetails = datamodel.LifeCycleStateReadyDetails
		}
	}

	ad.State = state
	ad.StateDetails = stateDetails
	_, err = a.SE.UpdateActiveDirectory(ctx, ad)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (a *ResourceEventsActivity) HandleResourceEventsOFFForVCPActivity(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	switch params.ResourceType {
	case common.ResourceStateV1ResourceTypeKmsConfig:
		return a.handleKmsConfig(ctx, params, datamodel.LifeCycleStateDisabledDetails)
	case common.ResourceStateV1ResourceTypeStoragePool:
		return a.checkStoragePoolExistence(ctx, params)
	case common.ResourceStateV1ResourceTypeSnapshot:
		return a.handleSnapshot(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
	case common.ResourceStateV1ResourceTypeVolume:
		return a.handleVolume(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
	case common.ResourceStateV1ResourceTypeAD:
		return a.handleActiveDirectory(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
	case common.ResourceStateV1ResourceTypeBackupPolicy:
		return a.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
	case common.ResourceStateV1ResourceTypeHostGroup:
		return a.handleHostGroup(ctx, params, datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails)
	default:
		return false, errors.New("unsupported resource type")
	}
}

func (a *ResourceEventsActivity) HandleResourceEventsONForVCPActivity(ctx context.Context, params *common.HandleResourceEventParams) (bool, error) {
	switch params.ResourceType {
	case common.ResourceStateV1ResourceTypeKmsConfig:
		return a.handleKmsConfig(ctx, params, common.ResourceLifeCycleStateEnabledDetails)
	case common.ResourceStateV1ResourceTypeStoragePool:
		return a.checkStoragePoolExistence(ctx, params)
	case common.ResourceStateV1ResourceTypeSnapshot:
		return a.handleSnapshot(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
	case common.ResourceStateV1ResourceTypeVolume:
		return a.handleVolume(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
	case common.ResourceStateV1ResourceTypeAD:
		return a.handleActiveDirectory(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
	case common.ResourceStateV1ResourceTypeBackupPolicy:
		return a.handleBackupPolicy(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
	case common.ResourceStateV1ResourceTypeHostGroup:
		return a.handleHostGroup(ctx, params, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
	default:
		unsupportedErr := errors.New("unsupported resource type")
		return false, temporal.NewNonRetryableApplicationError(unsupportedErr.Error(), ErrInvalidRequest, unsupportedErr)
	}
}

func (a *ResourceEventsActivity) HandleResourceEventsForSDEActivity(ctx context.Context, params *common.HandleResourceEventParams) (*common.HandleResourceEventResult, error) {
	var parentResourceId, parentResourceType *string
	if params.ResourceType == common.ResourceStateV1ResourceTypeSnapshot {
		parentResourceType = &params.ParentResourceType
		parentResourceId = &params.ParentResourceID
	}
	body := &models.ResourceStateUpdateV1beta{
		StateUpdateV1beta: models.StateUpdateV1beta{
			State: params.State,
		},
		ResourceType:       params.ResourceType,
		ResourceID:         params.ResourceId,
		ParentResourceID:   parentResourceId,
		ParentResourceType: parentResourceType,
	}

	reqParams := &resource_events.V1betaResourceStateUpdateParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID,
		Body:           body,
	}

	jwtToken, err := getSignedToken(params.ProjectNumber)
	if err != nil {
		logger := util.GetLogger(ctx)
		logger.Errorf("Failed to get signed token for project %s: %v", params.ProjectNumber, err)
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), ErrSignedTokenFailed, err)
	}

	logger := util.GetLogger(ctx)
	cvpClient := createClient(logger, jwtToken)
	created, accepted, _, err := cvpClient.ResourceEvents.V1betaResourceStateUpdate(reqParams)
	if err != nil {
		logger.Errorf("Error updating %s Resource state for resource %s: %v", params.State, params.ResourceId, err)
		switch e := err.(type) {
		case *resource_events.V1betaResourceStateUpdateBadRequest:
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorBadRequest,
					fmt.Errorf("Bad request for resource state update %s: %s", params.ResourceId, e.Error())),
			)
		case *resource_events.V1betaResourceStateUpdateUnauthorized:
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorUnauthorized,
					fmt.Errorf("Unauthorized for resource state update %s: %s", params.ResourceId, e.Error())),
			)
		case *resource_events.V1betaResourceStateUpdateForbidden:
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorForbidden,
					fmt.Errorf("Forbidden for resource state update %s: %s", params.ResourceId, e.Error())),
			)
		case *resource_events.V1betaResourceStateUpdateNotFound:
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorNotFound,
					fmt.Errorf("Resource %s not found for state update: %s", params.ResourceId, e.Error())),
			)
		case *resource_events.V1betaResourceStateUpdateConflict:
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorConflict,
					fmt.Errorf("Conflict for resource state update %s: %s", params.ResourceId, e.Error())),
			)
		case *resource_events.V1betaResourceStateUpdateInternalServerError:
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorInternalServerError,
					fmt.Errorf("Internal server error for resource state update %s: %s", params.ResourceId, e.Error())),
			)
		case *resource_events.V1betaResourceStateUpdateTooManyRequests:
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorTooManyRequests,
					fmt.Errorf("Too many requests for resource state update: %s", e.Error())),
			)
		case *resource_events.V1betaResourceStateUpdateNotImplemented:
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorNotImplemented,
					fmt.Errorf("Not implemented error for resource state update %s: %s", params.ResourceId, e.Error())),
			)
		default:
			logger.Warnf("Unknown error type for resource state update %s: %T - %s", params.ResourceId, err, err.Error())
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrCVPClientHandleResourceEventError, err),
			)
		}
	}

	if created != nil {
		pl := created.GetPayload()
		return &common.HandleResourceEventResult{
			Done: pl.Done,
			Name: &pl.Name,
		}, nil
	}

	if accepted != nil {
		pl := accepted.GetPayload()
		return &common.HandleResourceEventResult{
			Done: pl.Done,
			Name: &pl.Name,
		}, nil
	}

	return nil, nil
}

func (j *ResourceEventsActivity) PollHandleResourceEventSDEOperationActivity(ctx context.Context, params *common.HandleResourceEventParams, result *common.HandleResourceEventResult) error {
	if result.Done != nil && *result.Done {
		return nil
	}

	if result.Name == nil {
		return temporal.NewNonRetryableApplicationError("operation name is nil", ErrInvalidRequest, errors.New("operation name is nil"))
	}

	logger := util.GetLogger(ctx)
	jwtToken, err := getSignedToken(params.ProjectNumber)
	if err != nil {
		logger.Errorf("Failed to get signed token for polling operation: %v", err)
		return temporal.NewNonRetryableApplicationError(err.Error(), ErrSignedTokenFailed, err)
	}
	cvpClient := createClient(logger, jwtToken)

	// Extract the operation UUID
	operationUUID := utils.GetOperationUUID(*result.Name)
	operationParams := async.NewV1betaDescribeOperationParams()
	operationParams.OperationID = operationUUID
	operationParams.ProjectNumber = params.ProjectNumber
	operationParams.LocationID = params.LocationId
	res, err := PollCvpOperationForWorkflow(ctx, cvpClient, operationParams)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrCVPClientHandleResourceEventError, err),
		)
	}

	if res.Done != nil && *res.Done {
		if res.Error != nil {
			switch int(res.Error.Code) {
			case common.HTTPStatusBadRequest:
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorBadRequest,
						fmt.Errorf("Bad request while polling operation %s for resource %s: %s", operationUUID, params.ResourceId, res.Error.Message)),
				)

			case common.HTTPStatusUnauthorized:
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorUnauthorized,
						fmt.Errorf("Unauthorized while polling operation %s for resource %s: %s", operationUUID, params.ResourceId, res.Error.Message)),
				)

			case common.HTTPStatusForbidden:
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorForbidden,
						fmt.Errorf("Forbidden while polling operation %s for resource %s: %s", operationUUID, params.ResourceId, res.Error.Message)),
				)

			case common.HTTPStatusNotFound:
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorNotFound,
						fmt.Errorf("Operation %s not found while polling for resource %s: %s", operationUUID, params.ResourceId, res.Error.Message)),
				)

			case common.HTTPStatusInternalServerError:
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorInternalServerError,
						fmt.Errorf("Internal server error while polling operation %s for resource %s: %s", operationUUID, params.ResourceId, res.Error.Message)),
				)

			case common.HTTPStatusTooManyRequests:
				return vsaerrors.WrapAsTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorTooManyRequests,
						fmt.Errorf("Too many requests while polling operation %s for resource %s: %s", operationUUID, params.ResourceId, res.Error.Message)),
				)

			case common.HttpStatusNotImplemented:
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrHandleResourceEventErrorNotImplemented,
						fmt.Errorf("Not implemented error while polling operation %s for resource %s: %s", operationUUID, params.ResourceId, res.Error.Message)),
				)

			default:
				logger.Warnf("Unknown error code while polling operation %s for resource %s: %d - %s", operationUUID, params.ResourceId, int(res.Error.Code), res.Error.Message)
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrCVPClientHandleResourceEventError,
						fmt.Errorf("SDE polling failed for operation %s (resource: %s): %s", operationUUID, params.ResourceId, res.Error.Message)),
				)
			}
		}
		return nil
	}
	return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrSDEJobNotFinished, errors.New("job not finished")))
}

func (a *ResourceEventsActivity) handleKmsConfig(ctx context.Context, params *common.HandleResourceEventParams, stateDetails string) (bool, error) {
	_, err := a.SE.UpdateKmsConfigStateForHandleResource(ctx, params.ResourceId, stateDetails, params.State)
	if err != nil {
		if errors.IsNotFoundErr(err) || errors.IsUserInputValidationErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	return true, nil
}

func (a *ResourceEventsActivity) handleSnapshot(ctx context.Context, params *common.HandleResourceEventParams, state string, stateDetails string) (bool, error) {
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			UUID: params.ResourceId,
		},
		State:        state,
		StateDetails: stateDetails,
	}
	_, err := a.SE.UpdateSnapshotForHandleResource(ctx, snapshot)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	return true, nil
}

func (a *ResourceEventsActivity) handleVolume(ctx context.Context, params *common.HandleResourceEventParams, state string, stateDetails string) (bool, error) {
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: params.ResourceId,
		},
		State:        state,
		StateDetails: stateDetails,
	}
	err := a.SE.UpdateVolume(ctx, volume)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}
	return true, nil
}

func (a *ResourceEventsActivity) handleBackupPolicy(ctx context.Context, params *common.HandleResourceEventParams, state string, stateDetails string) (bool, error) {
	logger := util.GetLogger(ctx)

	account, err := a.SE.GetAccount(ctx, params.ProjectNumber)
	if err != nil {
		logger.Errorf("Failed to get account for project number %s: %v", params.ProjectNumber, err)
		return false, err
	}

	backupPolicy, err := a.SE.GetBackupPolicyByUUIDAndOwnerID(ctx, params.ResourceId, account.ID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}

	backupPolicyActivity := &activities.BackupPolicyActivity{
		SE:        a.SE,
		Scheduler: a.Scheduler,
	}

	switch state {
	case datamodel.LifeCycleStateDisabled:
		// For OFF events: check if the policy is enabled in DB, then pause
		if backupPolicy.PolicyEnabled {
			logger.Infof("Processing pause request for backup policy schedule: %s", params.ResourceId)
			err = backupPolicyActivity.PauseBackupPolicySchedule(ctx, backupPolicy)
			if err != nil {
				logger.Errorf("Failed to pause backup policy schedule: %v", err)
				return false, err
			}
			logger.Infof("Successfully processed pause request for backup policy schedule: %s", params.ResourceId)
		} else {
			logger.Infof("Backup policy is already disabled in database, skipping pause: %s", params.ResourceId)
		}

	case datamodel.LifeCycleStateREADY:
		if backupPolicy.PolicyEnabled {
			logger.Infof("Processing unpause request for backup policy schedule: %s", params.ResourceId)
			err = backupPolicyActivity.UnpauseBackupPolicySchedule(ctx, backupPolicy)
			if err != nil {
				logger.Errorf("Failed to unpause backup policy schedule: %v", err)
				return false, err
			}
			logger.Infof("Successfully processed unpause request for backup policy schedule: %s", params.ResourceId)
		} else {
			logger.Infof("Backup policy is disabled in database, skipping unpause: %s", params.ResourceId)
		}

	default:
		logger.Warnf("Unknown state for backup policy resource event: %s", state)
		return false, errors.New("unknown state for backup policy resource event")
	}

	return true, nil
}

func (a *ResourceEventsActivity) DeleteVolumeForPool(ctx context.Context, volume *datamodel.Volume) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	_, err := se.DeleteVolumeAndChildResources(ctx, volume.UUID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Debugf("Volume:%s marked deleted successfully in the db", volume.Name)

	return nil
}

func (a *ResourceEventsActivity) DeleteExpertModeVolumesForPool(ctx context.Context, poolID int64) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	expertModeVolumes, err := se.ListExpertModeVolumesByPoolID(ctx, poolID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	for _, vol := range expertModeVolumes {
		if err := se.DeleteExpertModeVolume(ctx, vol.UUID); err != nil {
			logger.Errorf("Failed to soft-delete expert mode volume %s (UUID: %s) for pool %d: %v", vol.Name, vol.UUID, poolID, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		logger.Debugf("Expert mode volume %s (UUID: %s) marked deleted for pool %d", vol.Name, vol.UUID, poolID)
	}

	logger.Infof("Soft-deleted %d expert mode volumes for pool %d", len(expertModeVolumes), poolID)
	return nil
}

func (a *ResourceEventsActivity) DeleteReplicationsForVolume(ctx context.Context, volume *datamodel.Volume) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("account_id", "=", volume.AccountID),
		dbutils.NewFilterCondition("volume_id", "=", volume.ID))
	replications, err := se.ListVolumeReplications(ctx, *filter, database.QueryDepthZero)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	for _, replication := range replications {
		_, err = se.DeleteVolumeReplication(ctx, replication)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		logger.Debugf("Replication:%s marked deleted successfully in the db", replication.Name)

		// If this replication had a cluster peering, delete the peering row.
		if replication.ClusterPeerId.Valid {
			row := &datamodel.ClusterPeerings{BaseModel: datamodel.BaseModel{ID: replication.ClusterPeerId.Int64}}
			if delErr := se.DeleteClusterPeeringRow(ctx, row); delErr != nil {
				if errors.IsNotFoundErr(delErr) {
					logger.Debugf("Cluster peering row with ID %d already deleted", replication.ClusterPeerId.Int64)
				} else {
					return vsaerrors.WrapAsTemporalApplicationError(delErr)
				}
			} else {
				logger.Debugf("Cluster peering row with ID %d soft-deleted", replication.ClusterPeerId.Int64)
			}
		}
	}

	return nil
}

func (a *ResourceEventsActivity) DeleteVolumeAssociatedQuotaRules(ctx context.Context, volumeID int64) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	logger.Debugf("Retrieving quota rules for volumeID: %d", volumeID)
	quotaRules, err := se.GetQuotaRulesByVolumeID(ctx, volumeID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Debugf("no quota rules found for volumeID: %d", volumeID)
			return nil
		}
		logger.Errorf("failed to get quota rules by volumeID: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Debugf("Deleting volume associated quota rules for volumeID: %d", volumeID)
	for _, quotaRule := range quotaRules {
		_, err = se.DeleteQuotaRule(ctx, quotaRule.UUID)
		if err != nil {
			logger.Warnf("failed to mark quota rule %s as deleted because of error: %v", quotaRule.Name, err)
		}
	}
	logger.Debugf("Volume associated quota rules deleted successfully for volumeID: %d", volumeID)
	return nil
}

func (a *ResourceEventsActivity) DeleteClusterPeeringsForVolume(ctx context.Context, volume *datamodel.Volume) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	if volume == nil || !volume.ClusterPeerID.Valid {
		return nil
	}

	clusterPeerID := volume.ClusterPeerID.Int64
	row := &datamodel.ClusterPeerings{BaseModel: datamodel.BaseModel{ID: clusterPeerID}}
	if err := se.DeleteClusterPeeringRow(ctx, row); err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Debugf("Cluster peering row with ID %d already deleted", clusterPeerID)
			return nil
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Debugf("Cluster peering row with ID %d soft-deleted successfully", clusterPeerID)
	return nil
}

func (a *ResourceEventsActivity) handleHostGroup(ctx context.Context, params *common.HandleResourceEventParams, state string, stateDetails string) (bool, error) {
	logger := util.GetLogger(ctx)

	account, err := a.SE.GetAccount(ctx, params.ProjectNumber)
	if err != nil {
		logger.Errorf("Failed to get account for project number %s: %v", params.ProjectNumber, err)
		return false, err
	}

	err = a.SE.UpdateHostGroupsStateForHandleResource(ctx, params.ResourceId, account.ID, state, stateDetails)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return false, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		return false, err
	}

	return true, nil
}
