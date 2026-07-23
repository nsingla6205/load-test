package gcp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows/background_kms_workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/kms_workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

var (
	createKmsConfig               = _createKmsConfig
	getKmsConfig                  = _getKmsConfig
	parseKeyFullPathResource      = utils.ParseKeyFullPathResource
	updateKmsConfig               = _updateKmsConfig
	validateUpdateKmsConfigParams = _validateUpdateKmsConfigParams
	getExistingKmsConfig          = _getExistingKmsConfig
	validateDeleteKmsConfigParams = _validateDeleteKmsConfigParams
	ConvertKmsConfigStateV1beta   = convertKmsConfigStateV1beta
	updateSDEKmsConfiguration     = sde.UpdateSDEKmsConfiguration
	createAndSyncKmsConfig        = kms_activities.CreateAndSyncKmsConfig
	getSDEKmsConfiguration        = kms_activities.GetSDEKmsConfiguration
	waitForTemporalEnabled        = env.GetBool("WAIT_FOR_TEMPORAL_ENABLED", false)
)

const (
	RetryTimeoutVerifyKmsConfigHealthCheck  = time.Second * 15
	RetryIntervalVerifyKmsConfigHealthCheck = time.Second * 5
)

type KmsConfigInterface interface {
	CreateKmsConfig(ctx context.Context, params *common.CreateKmsConfigParams) (*models.KmsConfig, string, error)
	GetKmsConfig(ctx context.Context, params *common.GetKmsConfigParams) (*models.KmsConfig, error)
	GetExistingKmsConfig(ctx context.Context, params *common.GetKmsConfigParams) (*models.KmsConfig, error)
	GetMultipleKMSConfigs(ctx context.Context, kmsConfigIDList []string) ([]*models.KmsConfig, error)
	GetKmsConfigsByUUIDs(ctx context.Context, kmsConfigUUIDs []string) ([]*models.KmsConfig, error)
	ListKmsConfigs(ctx context.Context, accountName string) ([]*models.KmsConfig, error)
	UpdateKmsConfig(ctx context.Context, params *common.UpdateKmsConfigParams) (*models.KmsConfig, error)
	CheckAndUpdateKmsConfigHealth(ctx context.Context, params *models.KmsConfigCheck) (*models.KmsConfig, error)
	AccessCryptoKeyAndEncryptDataWithImpersonation(ctx context.Context, kmsConfig *models.KmsConfig) error
	DeleteKmsConfig(ctx context.Context, params *common.DeleteKmsConfigParams) (*models.KmsConfig, string, error)
	MigrateKmsConfig(ctx context.Context, params *common.MigrateKmsConfigParams) (string, error)
	RotateKmsConfig(ctx context.Context, params *common.RotateKmsConfigParams) (*models.KmsConfig, *models.Job, error)
	CreateAndSyncKmsConfig(ctx context.Context, params *common.CreateKmsConfigParams) (*models.KmsConfig, error)
	GetSDEKmsConfiguration(ctx context.Context, params *common.GetKmsConfigParams) (*cvpModels.KmsConfigV1beta, error)
}

// CreateKmsConfig creates a new KMS configuration.
func (o *GCPOrchestrator) CreateKmsConfig(ctx context.Context, params *common.CreateKmsConfigParams) (*models.KmsConfig, string, error) {
	return createKmsConfig(ctx, o.storage, o.temporal, params)
}

func _createKmsConfig(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateKmsConfigParams) (*models.KmsConfig, string, error) {
	logger := util.GetLogger(ctx)
	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	if err = persistAccountTrialMetadataIfSet(ctx, se, account, params.TrialMode); err != nil {
		logger.Error("Failed to update account trial metadata", "accountUUID", account.UUID, "error", err)
		return nil, "", err
	}

	var kmsConfig *datamodel.KmsConfig = nil
	var createdJob *datamodel.Job = nil

	defer func() {
		if !waitForTemporalEnabled {
			if err != nil {
				if createdJob != nil {
					// Mark the job as error
					_ = se.UpdateJob(ctx, createdJob.UUID, datamodel.LifeCycleStateError, createdJob.TrackingID, err.Error())
				}
				if kmsConfig != nil {
					// bring back the kms config to original state
					_, delError := se.DeleteKmsConfig(ctx, kmsConfig.UUID, datamodel.LifeCycleStateError, err.Error())
					if delError != nil {
						logger.Error("Failed to delete kms config after create error", "error", delError)
					}
				}
			}
		}
	}()

	parsedKeyFullPath, err := parseKeyFullPathResource(params.KeyFullPath)
	if err != nil {
		return nil, "", err
	}
	kmsConfig = &datamodel.KmsConfig{}
	kmsConfig.CreatedAt = time.Now()
	kmsConfig.UUID = params.UUID // Use the uuid of the sde; this is to make sure CCFE gets the same UUID as SDE for PO volume creation
	kmsConfig.State = datamodel.LifeCycleStateCreating
	kmsConfig.StateDetails = datamodel.LifeCycleStateCreatingDetails
	kmsConfig.AccountID = account.ID
	kmsConfig.UpdatedAt = time.Now()
	kmsConfig.KeyName = parsedKeyFullPath.CryptoKey
	kmsConfig.CustomerProjectID = params.ProjectNumber
	kmsConfig.KeyRingLocation = parsedKeyFullPath.Location
	kmsConfig.KeyRing = parsedKeyFullPath.KeyRing
	kmsConfig.ResourceID = params.ResourceID
	kmsConfig.KeyProjectID = parsedKeyFullPath.ProjectID
	kmsConfig.KmsGrant = parsedKeyFullPath.Grant

	// Set KmsAttributes based on whether common resources are created in VCP or SDE
	if !utils.IsCVPHostSet() {
		kmsConfig.KmsAttributes = &datamodel.KmsAttributes{
			CreationMode: datamodel.KmsCreationModeVCP,
		}
	} else {
		kmsConfig.KmsAttributes = &datamodel.KmsAttributes{
			SdeKmsConfigUUID:          params.UUID,
			SdeKmsConfigOperationURI:  params.OperationUri,
			SdeKmsConfigOperationDone: params.OperationDone,
			CreationMode:              datamodel.KmsCreationModeSDE,
		}
	}
	kmsConfig.Description = params.Description

	kmsConfig, err = se.CreateKmsConfig(ctx, kmsConfig)
	if err != nil {
		return nil, "", err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateKmsConfig),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.ResourceID,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: kmsConfig.UUID},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err.Error())
		return nil, "", err
	}
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetCMEKWorkFlowGlobalTimeout(),
		},
		kms_workflows.CreateKmsConfigWorkflow,
		params,
		kmsConfig,
	)
	if err != nil {
		logger.Warn("Failed to start create kms workflow: ", "error", err)
		if waitForTemporalEnabled {
			// Update job to pending state if workflow fails to start, so that pending job processor can pick it up later, do not return error
			logger.Info("Updating job to pending state, so that pending job processor can pick it up later")
			updateErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateWaitForTemporal), createdJob.TrackingID, err.Error())
			if updateErr != nil {
				logger.Error("Failed to update job to temporal pending state", "error", updateErr)
			}
		} else {
			return nil, "", err
		}
	}
	return convertDatastoreKmsConfigToModel(kmsConfig), createdJob.UUID, nil
}

// GetKmsConfig retrieves a KMS configuration by its UUID.
func (o *GCPOrchestrator) GetKmsConfig(ctx context.Context, params *common.GetKmsConfigParams) (*models.KmsConfig, error) {
	return getKmsConfig(ctx, o.storage, params)
}

func _getKmsConfig(ctx context.Context, se database.Storage, params *common.GetKmsConfigParams) (*models.KmsConfig, error) {
	_, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, err
	}
	dbKmsConfig, err := se.GetKmsConfigByUUID(ctx, params.UUID)
	if err != nil {
		return nil, err
	}
	return convertDatastoreKmsConfigToModel(dbKmsConfig), nil
}

// GetMultipleKMSConfigs gets KMS config records for the UUIDs provided
func (o *GCPOrchestrator) GetMultipleKMSConfigs(ctx context.Context, kmsConfigUUIDList []string) ([]*models.KmsConfig, error) {
	se := o.storage

	conditions := [][]interface{}{{"uuid in ?", kmsConfigUUIDList}}
	kmsConfigDataStoreList, err := se.GetMultipleKmsConfigs(ctx, conditions)
	if err != nil {
		return nil, err
	}
	var kmsConfigModelList []*models.KmsConfig
	for _, kmsConfigDataStore := range kmsConfigDataStoreList {
		kmsConfigModel := convertDataStoreKmsConfigToModel(kmsConfigDataStore)
		kmsConfigModelList = append(kmsConfigModelList, kmsConfigModel)
	}

	return kmsConfigModelList, nil
}

// GetKmsConfigsByUUIDs returns KMS configs matching the given UUIDs across all accounts.
// Every supported batch KMS field is served from the base row and the KmsAttributes JSON column,
// so no fetch options or relation preloads are required.
func (o *GCPOrchestrator) GetKmsConfigsByUUIDs(
	ctx context.Context,
	kmsConfigUUIDs []string,
) ([]*models.KmsConfig, error) {
	kmsConfigs, err := o.storage.GetKmsConfigsByUUIDs(ctx, kmsConfigUUIDs)
	if err != nil {
		return nil, err
	}

	result := make([]*models.KmsConfig, 0, len(kmsConfigs))
	for _, kmsConfig := range kmsConfigs {
		result = append(result, convertDataStoreKmsConfigToModel(kmsConfig))
	}
	return result, nil
}

// ListKmsConfigs lists all KMS configurations for the given account.
func (o *GCPOrchestrator) ListKmsConfigs(ctx context.Context, accountName string) ([]*models.KmsConfig, error) {
	se := o.storage
	account, err := getOrCreateAccount(ctx, se, accountName)
	if err != nil {
		return nil, err
	}
	kmsConfigs, err := se.ListKmsConfigByAccountID(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	var result []*models.KmsConfig
	for _, kmsConfig := range kmsConfigs {
		result = append(result, convertDataStoreKmsConfigToModel(kmsConfig))
	}
	return result, nil
}

// UpdateKmsConfig updates the specified kms configuration.
func (o *GCPOrchestrator) UpdateKmsConfig(ctx context.Context, params *common.UpdateKmsConfigParams) (*models.KmsConfig, error) {
	return updateKmsConfig(ctx, o.storage, params)
}

func _updateKmsConfig(ctx context.Context, se database.Storage, params *common.UpdateKmsConfigParams) (*models.KmsConfig, error) {
	logger := util.GetLogger(ctx)

	_, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, err
	}

	kmsConfig, err := se.GetKmsConfig(ctx, params.KmsConfigID)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return nil, err
		}

		// VCP path: KMS config must exist in VCP DB, no SDE fallback when CVP is not configured.
		if !utils.IsCVPHostConfigured() {
			return nil, errors.NewNotFoundErr("KMS Configuration", nil)
		}

		logger.Info("Kms config not found in vcp database", "error", err)

		// SDE path: For KmsConfig not found, directly call SDE & return
		kmsConfig = &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: params.KmsConfigID,
			},
		}

		sdeRes, err := updateSDEKmsConfiguration(ctx, kmsConfig, params)
		if err != nil {
			logger.Error("Failed to update KMS configuration in SDE", "error", err)
			return nil, err
		}

		sdeKmsConfig, ok := sdeRes.(*gcpserver.KmsConfigV1beta)
		if ok && sdeKmsConfig != nil {
			return convertSDEResponseToKmsConfig(sdeKmsConfig), nil
		}

		return nil, errors.New("Failed to update KMS configuration in SDE")
	}

	kms_activities.EnrichUpdateKmsConfigParamsFromKeyURI(params)

	err = validateUpdateKmsConfigParams(ctx, se, kmsConfig, params)
	if err != nil {
		return nil, err
	}

	// Existing config routing: use SDE flow only for SDE-created configs.
	if !utils.ShouldUseVCPForExistingKMS(kmsConfig) {
		_, err = updateSDEKmsConfiguration(ctx, kmsConfig, params)
		if err != nil {
			logger.Error("Failed to update KMS configuration in SDE", "error", err)
			return nil, err
		}
	}

	return updateKmsConfigInVCPDB(ctx, se, kmsConfig, params, logger)
}

// updateKmsConfigInVCPDB updates the KMS config in the VCP database and returns the updated model.
func updateKmsConfigInVCPDB(ctx context.Context, se database.Storage, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams, logger log.Logger) (*models.KmsConfig, error) {
	if kmsConfig.UUID == "" {
		return convertDataStoreKmsConfigToModel(kmsConfig), nil
	}

	kms_activities.EnrichUpdateKmsConfigParamsFromKeyURI(params)

	// Use the activity function to update the KMS config in database
	err := kms_activities.UpdateKmsConfig(se, ctx, kmsConfig, params)
	if err != nil {
		logger.Error("Failed to update KMS config in database", "error", err)
		return nil, err
	}

	// Get the updated config from database
	updatedKmsConfig, err := se.GetKmsConfig(ctx, params.KmsConfigID)
	if err != nil {
		logger.Error("Failed to get updated KMS config from database", "error", err)
		return nil, err
	}
	return convertDataStoreKmsConfigToModel(updatedKmsConfig), nil
}

// DeleteKmsConfig updates the specified kms configuration.
func (o *GCPOrchestrator) DeleteKmsConfig(ctx context.Context, params *common.DeleteKmsConfigParams) (*models.KmsConfig, string, error) {
	return _deleteKmsConfig(ctx, o.storage, o.temporal, params)
}

func _deleteKmsConfig(ctx context.Context, se database.Storage, temporal client.Client, params *common.DeleteKmsConfigParams) (*models.KmsConfig, string, error) {
	logger := util.GetLogger(ctx)
	var createdJob *datamodel.Job = nil
	var kmsConfig *datamodel.KmsConfig = nil
	kmsConfigOriginalState := datamodel.LifeCycleStateUnknown

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	defer func() {
		if !waitForTemporalEnabled {
			if err != nil {
				if createdJob != nil {
					// Mark the job as error
					_ = se.UpdateJob(ctx, createdJob.UUID, datamodel.LifeCycleStateError, createdJob.TrackingID, err.Error())
				}
				if kmsConfig != nil {
					// bring back the kms config to original state
					_, _ = se.UpdateKmsConfigState(ctx, kmsConfig.UUID, kmsConfigOriginalState, "Reverted to previous state due to error: "+err.Error())
				}
			}
		}
	}()

	var kmsConfigOriginalStateDetails string
	var existingDeleteJobUUID string
	kmsConfig, err = se.GetKmsConfig(ctx, params.KmsConfigID)
	isCancellationRequest := false
	if err == nil {
		kmsConfigOriginalState = kmsConfig.State
		kmsConfigOriginalStateDetails = kmsConfig.StateDetails
		if kmsConfig.State == datamodel.LifeCycleStateCreating {
			existingDeleteJobUUID, _, err = database.ValidateCorrelationIDForCreatingResource(
				ctx, se, params.KmsConfigID, "KMS config", datamodel.JobTypeCreateKmsConfig, datamodel.JobTypeDeleteKmsConfig, logger)
			if err != nil {
				logger.Warnf("KMS config %s cannot be deleted: existing create job not present and state is in CREATING", params.KmsConfigID)
				return nil, "", err
			}
			if existingDeleteJobUUID != "" {
				return convertDataStoreKmsConfigToModel(kmsConfig), existingDeleteJobUUID, nil
			}
			logger.Info("Delete request with same correlation ID as create, proceeding with cancellation", "kms_config_uuid", params.KmsConfigID)
			isCancellationRequest = true
		} else if utils.IsTransitionalState(kmsConfig.State) && kmsConfig.State != datamodel.LifeCycleStateDeleting {
			logger.Errorf("KMS config %s cannot be deleted, while in transitioning state: %s", params.KmsConfigID, kmsConfig.State)
			return nil, "", errors.NewConflictErr(fmt.Sprintf("KMS config is in transition state and cannot be deleted, state: %s", kmsConfig.State))
		}

		existingJobUUID := database.GetExistingDeleteJobForDeletingState(ctx, se, params.KmsConfigID, datamodel.JobTypeDeleteKmsConfig, logger)
		if existingJobUUID != "" {
			return convertDataStoreKmsConfigToModel(kmsConfig), existingJobUUID, nil
		}

		// Skipping validation for cancellation requests (already validated above)
		if !isCancellationRequest {
			err = validateDeleteKmsConfigParams(ctx, se, kmsConfig, params)
			if err != nil {
				return nil, "", err
			}
		}

		// Skipping state update for cancellation requests - cancellation will be handled in workflow
		if !isCancellationRequest {
			kmsConfig, err = se.UpdateKmsConfigState(ctx, kmsConfig.UUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails)
			if err != nil {
				return nil, "", err
			}
		}
	} else {
		if !errors.IsNotFoundErr(err) {
			return nil, "", err
		}

		// VCP path: if KMS config not found in DB and CVP is not configured, return not found
		if !utils.IsCVPHostConfigured() {
			return nil, "", errors.NewNotFoundErr("KMS Configuration", nil)
		}

		// SDE path: config might exist only in SDE, create a stub for the workflow
		logger.Error("Failed to get kms config from database", "error", err)
		kmsConfig = &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{
				SdeKmsConfigUUID: params.KmsConfigID,
			},
		}
		kmsConfigOriginalState = datamodel.LifeCycleStateUnknown
		kmsConfigOriginalStateDetails = ""
	}

	job := &datamodel.Job{
		Type:         string(datamodel.JobTypeDeleteKmsConfig),
		State:        string(datamodel.JobsStateNEW),
		ResourceName: kmsConfig.ResourceID,
		AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         params.KmsConfigID,
			PreviousState:        kmsConfigOriginalState,
			PreviousStateDetails: kmsConfigOriginalStateDetails,
		},
		CorrelationID: params.XCorrelationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}
	createdJob, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		kms_workflows.DeleteKmsConfigWorkflow,
		kmsConfig,
		params,
	)

	if err != nil {
		logger.Error("Failed to start update kms config workflow: ", "error", err)
		if waitForTemporalEnabled {
			logger.Info("Updating job to pending state, so that pending job processor can pick it up later")
			// Update job to pending state if workflow fails to start, so that pending job processor can pick it up later
			updateErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateWaitForTemporal), createdJob.TrackingID, err.Error())
			if updateErr != nil {
				logger.Error("Failed to update job to temporal pending state", "error", updateErr)
			}
		} else {
			return nil, "", err
		}
	}

	return convertDataStoreKmsConfigToModel(kmsConfig), createdJob.UUID, nil
}

func (o *GCPOrchestrator) MigrateKmsConfig(ctx context.Context, params *common.MigrateKmsConfigParams) (string, error) {
	return migrateKmsConfig(ctx, o.storage, o.temporal, params)
}

func migrateKmsConfig(ctx context.Context, se database.Storage, temporal client.Client, params *common.MigrateKmsConfigParams) (string, error) {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return "", err
	}

	localEntryPresent := false
	params.SdeUUID = params.UUID
	dbKmsConfig, err := se.GetKmsConfigByUUID(ctx, params.UUID)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return "", err
		}
		// VCP path: KMS config must exist in VCP DB when CVP is not configured
		if !utils.IsCVPHostConfigured() {
			return "", errors.NewNotFoundErr("KMS Configuration", nil)
		}
	} else {
		if !utils.ShouldUseVCPForExistingKMS(dbKmsConfig) {
			// SDE-created config: resolve SDE UUID for CVP client calls.
			if dbKmsConfig.KmsAttributes != nil && dbKmsConfig.KmsAttributes.SdeKmsConfigUUID != "" {
				params.SdeUUID = dbKmsConfig.KmsAttributes.SdeKmsConfigUUID
				localEntryPresent = true
			} else {
				return "", errors.New("KmsAttributes property not present within KmsConfig DB entry in VCP")
			}
		} else {
			// VCP-created config: proceed without SDE UUID remapping.
			localEntryPresent = true
		}
	}

	// Check for validation errors before starting the workflow because SDE does not return...
	// ...validation errors as part of operation response. Also use this to return ongoing migration job (if present)
	ongoingJobUuid, errValidate := validateKmsConfigState(ctx, se, params.State, account.ID, localEntryPresent)
	if errValidate != nil {
		return "", errValidate
	} else if ongoingJobUuid != "" {
		return ongoingJobUuid, nil
	}

	// Get previous state from KMS config before updating to MIGRATING
	var previousState, previousStateDetails string
	if localEntryPresent {
		previousState = dbKmsConfig.State
		previousStateDetails = dbKmsConfig.StateDetails
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeMigrateKmsConfig),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         params.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}
	createdJob, errJob := se.CreateJob(ctx, job)
	if errJob != nil {
		logger.Error("Failed to create job in database", "error", errJob)
		return "", errJob
	}

	if localEntryPresent {
		_, errUpdateState := se.UpdateKmsConfigState(ctx, params.UUID, datamodel.LifeCycleStateMigrating, datamodel.LifeCycleStateMigratingDetails)
		if errUpdateState != nil {
			return "", errUpdateState
		}
	}

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		kms_workflows.MigrateKmsConfigWorkflow,
		params,
	)
	if err != nil {
		logger.Error("Failure encountered during CMEK migrate workflow: ", "error", err)
		return "", err
	}

	return createdJob.UUID, nil
}

// RotateKmsConfig rotates the service account key for a KMS configuration.
func (o *GCPOrchestrator) RotateKmsConfig(ctx context.Context, params *common.RotateKmsConfigParams) (*models.KmsConfig, *models.Job, error) {
	return rotateKmsConfig(ctx, o.storage, o.temporal, params)
}

func rotateKmsConfig(ctx context.Context, se database.Storage, temporal client.Client, params *common.RotateKmsConfigParams) (*models.KmsConfig, *models.Job, error) {
	logger := util.GetLogger(ctx)

	// Validate the KMS config exists
	kmsConfig, err := se.GetKmsConfig(ctx, params.KmsConfigID)
	if err != nil {
		logger.Error("Failed to get KMS config", "KmsConfigID", params.KmsConfigID, "Error", err)
		return nil, nil, err
	}

	// Get account info using UUID instead of name
	// Using AccountName as UUID for now - this may need to be adjusted based on your actual requirement
	account, err := getAccountFromUUID(ctx, se, params.AccountName)
	if err != nil {
		return nil, nil, err
	}

	// Check if KMS config is in Migrating state
	if kmsConfig.State == datamodel.LifeCycleStateMigrating {
		return nil, nil, errors.NewConflictErr("KMS config is currently in Migrating state and cannot be rotated")
	}

	if kmsConfig.State != cvpModels.KmsConfigV1betaKmsStateINUSE && kmsConfig.State != cvpModels.KmsConfigV1betaKmsStateREADY {
		return nil, nil, errors.NewBadRequestErr("Concerned GCP KMS config is not in a state(ready/in use) to rotate the service account key")
	}

	// Check if any pools for this account are in CREATING state
	filter := dbutils.CreateFilterWithConditions(dbutils.NewFilterCondition("account_id", "=", account.ID))
	pools, err := se.ListPools(ctx, filter)
	if err != nil {
		logger.Error("Failed to list pools for account", "AccountID", account.ID, "Error", err)
		return nil, nil, err
	}

	for _, pool := range pools {
		if pool.State == string(gcpserver.PoolV1betaStoragePoolStateCREATING) && pool.KmsConfigID.Valid {
			return nil, nil, errors.NewConflictErr("Cannot rotate KMS config while CMEK-enabled storage pool is in creating state: " + pool.Name)
		}
	}

	// Create a new job for the rotation
	job := &datamodel.Job{
		Type:         string(datamodel.JobTypeRotateKmsConfig),
		State:        string(datamodel.JobsStateNEW),
		ResourceName: kmsConfig.Name,
		AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: params.KmsConfigID,
		},
		CorrelationID: params.XCorrelationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create rotation job", "Error", err)
		return nil, nil, err
	}

	// Start the rotation workflow
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		background_kms_workflows.RotateKmsConfigWorkflow,
		params,
	)
	if err != nil {
		logger.Error("Failed to start rotation workflow", "Error", err)
		return nil, nil, err
	}

	logger.Info("Started KMS config rotation workflow",
		"WorkflowID", createdJob.WorkflowID,
		"KmsConfigID", params.KmsConfigID,
		"JobID", createdJob.UUID)

	// Convert the KMS config to model and return it along with job model
	kmsConfigModel := convertDataStoreKmsConfigToModel(kmsConfig)
	jobModel := convertDatastoreJobToModel(createdJob)
	return kmsConfigModel, jobModel, nil
}

func convertDataStoreKmsConfigToModel(kmsConfig *datamodel.KmsConfig) *models.KmsConfig {
	if kmsConfig == nil || kmsConfig.UUID == "" {
		return nil
	}
	state, stateDetails := convertKmsConfigStateV1beta(kmsConfig.State, kmsConfig.StateDetails)
	kmsModel := &models.KmsConfig{
		BaseModel: models.BaseModel{
			UUID:      kmsConfig.UUID,
			CreatedAt: kmsConfig.CreatedAt,
			UpdatedAt: kmsConfig.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(kmsConfig.DeletedAt),
		},
		Name:              kmsConfig.Name,
		Description:       kmsConfig.Description,
		State:             state,
		StateDetails:      stateDetails,
		KeyRing:           kmsConfig.KeyRing,
		KeyRingLocation:   kmsConfig.KeyRingLocation,
		KeyName:           kmsConfig.KeyName,
		KmsGrant:          kmsConfig.KmsGrant,
		AccountID:         kmsConfig.AccountID,
		CustomerProjectID: kmsConfig.CustomerProjectID,
		KeyProjectID:      kmsConfig.KeyProjectID,
		ServiceAccountID:  kmsConfig.ServiceAccountID,
		ResourceID:        kmsConfig.ResourceID,
	}
	if kmsConfig.KmsAttributes != nil {
		kmsModel.KmsAttributes = &models.KmsAttributes{
			SdeKmsConfigUUID:       kmsConfig.KmsAttributes.SdeKmsConfigUUID,
			SdeServiceAccountEmail: kmsConfig.KmsAttributes.SdeServiceAccountEmail,
			Instructions:           kmsConfig.KmsAttributes.Instructions,
			CreationMode:           kmsConfig.KmsAttributes.CreationMode,
			VcpServiceAccountEmail: kmsConfig.KmsAttributes.VcpServiceAccountEmail,
		}
	}
	return kmsModel
}

func _validateUpdateKmsConfigParams(ctx context.Context, se database.Storage, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) error {
	if kmsConfig.State == datamodel.LifeCycleStateCreating || kmsConfig.State == datamodel.LifeCycleStateError {
		return errors.NewConflictErr("can not update a gcpKmsConfig which is in creating or error state.")
	}

	isConfigInUse, err := se.IsKmsConfigInUse(ctx, kmsConfig.UUID)
	if err != nil {
		return err
	}

	if isConfigInUse && (params.KeyUri != "" || !nillable.IsNilOrEmpty(&params.KeyName)) && !kms_activities.IsGrantOnlyKmsKeyUpdate(kmsConfig, params) {
		return errors.NewConflictErr("can not update key details while kms config is in use")
	}

	keyProjectID := params.KeyProjectID
	keyRingLocation := params.KeyRingLocation
	keyRing := params.KeyRing
	keyName := params.KeyName
	keyPathUpdate := false
	if params.KeyUri != "" {
		parsedKeyFullPath, parseErr := parseKeyFullPathResource(params.KeyUri)
		if parseErr != nil {
			return errors.NewUserInputValidationErr(fmt.Sprintf("invalid KMS key URI: %v", parseErr))
		}
		keyProjectID = parsedKeyFullPath.ProjectID
		keyRingLocation = parsedKeyFullPath.Location
		keyRing = parsedKeyFullPath.KeyRing
		keyName = parsedKeyFullPath.CryptoKey
		keyPathUpdate = true
	} else if !nillable.IsNilOrEmpty(&params.KeyName) {
		keyPathUpdate = true
	}
	if !keyPathUpdate {
		return nil
	}

	candidate := &datamodel.KmsConfig{
		KeyProjectID:    keyProjectID,
		KeyRingLocation: keyRingLocation,
		KeyRing:         keyRing,
		KeyName:         keyName,
	}
	if kmsConfigKeyPathEqual(candidate, kmsConfig) {
		return nil
	}

	configs, err := se.ListKmsConfigByAccountID(ctx, kmsConfig.AccountID)
	if err != nil {
		return err
	}
	for _, other := range configs {
		if other.UUID == kmsConfig.UUID {
			continue
		}
		if kmsConfigKeyPathEqual(candidate, other) {
			return errors.NewConflictErr("another KMS configuration in this account already uses this crypto key path")
		}
	}
	return nil
}

// kmsConfigKeyPathEqual reports whether a and b refer to the same KMS key tuple
// (KeyProjectID, KeyRingLocation, KeyRing, KeyName).
func kmsConfigKeyPathEqual(a, b *datamodel.KmsConfig) bool {
	if a == nil || b == nil {
		return false
	}
	return a.KeyProjectID == b.KeyProjectID &&
		a.KeyRingLocation == b.KeyRingLocation &&
		a.KeyRing == b.KeyRing &&
		a.KeyName == b.KeyName
}

func _validateDeleteKmsConfigParams(ctx context.Context, se database.Storage, kmsConfig *datamodel.KmsConfig, params *common.DeleteKmsConfigParams) error {
	isConfigInUse, err := se.IsKmsConfigInUse(ctx, kmsConfig.UUID)
	if err != nil {
		return err
	}

	if isConfigInUse {
		return errors.NewConflictErr("can not delete this policy as it is still in use by one or more pools")
	}

	if kmsConfig.State == datamodel.LifeCycleStateCreating {
		return errors.NewConflictErr("can not delete a gcpKmsConfig which is in creating")
	}

	findOngoingJobs, err := se.ListOngoingPoolJobsWithKmsConfigId(ctx, kmsConfig.ID, kmsConfig.AccountID)
	if err != nil {
		return err
	}

	if len(findOngoingJobs) > 0 {
		return errors.NewConflictErr("can not delete this policy as there are ongoing pool creation using it")
	}
	return nil
}

// CheckAndUpdateKmsConfigHealth UpdateKmsConfigHealth checks the health of a KMS configuration and updates its status accordingly.
func (o *GCPOrchestrator) CheckAndUpdateKmsConfigHealth(ctx context.Context, configCheck *models.KmsConfigCheck) (*models.KmsConfig, error) {
	se := o.storage
	kmsConfig, err := kms_activities.UpdateKmsConfigHealth(ctx, se, configCheck)
	if err != nil {
		return nil, err
	}
	return convertDataStoreKmsConfigToModel(kmsConfig), nil
}

// AccessCryptoKeyAndEncryptDataWithImpersonation use impersonation to retrieve the details of a specific KMS crypto key.
func (o *GCPOrchestrator) AccessCryptoKeyAndEncryptDataWithImpersonation(ctx context.Context, kmsConfig *models.KmsConfig) error {
	se := o.storage
	dbKmsConfig, err := se.GetKmsConfig(ctx, kmsConfig.UUID)
	if err != nil {
		return err
	}
	return kms_activities.AccessCryptoKeyAndEncryptData(ctx, dbKmsConfig, dbKmsConfig.ServiceAccount.ServiceAccountPasswordLocation,
		RetryTimeoutVerifyKmsConfigHealthCheck, RetryIntervalVerifyKmsConfigHealthCheck)
}

func (o *GCPOrchestrator) GetExistingKmsConfig(ctx context.Context, params *common.GetKmsConfigParams) (*models.KmsConfig, error) {
	return getExistingKmsConfig(ctx, o.storage, params)
}

// _getExistingKmsConfig looks up an existing KMS config for the account that matches the requested key path.
// Uniqueness is by (account, key path) rather than (account) alone, so additional CMEK policies with different
// key paths are allowed when utils.EnableMultipleCmek is true.
func _getExistingKmsConfig(ctx context.Context, se database.Storage, params *common.GetKmsConfigParams) (*models.KmsConfig, error) {
	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, err
	}

	configs, err := se.ListKmsConfigByAccountID(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		return nil, errors.NewNotFoundErr("KMS Configuration", nil)
	}

	parsedKeyFullPath, err := parseKeyFullPathResource(params.KeyFullPath)
	if err != nil {
		return nil, err
	}

	for _, existingConfig := range configs {
		if existingConfig.KeyProjectID == parsedKeyFullPath.ProjectID &&
			existingConfig.KeyRingLocation == parsedKeyFullPath.Location &&
			existingConfig.KeyRing == parsedKeyFullPath.KeyRing &&
			existingConfig.KeyName == parsedKeyFullPath.CryptoKey {
			return convertDatastoreKmsConfigToModel(existingConfig), nil
		}
	}

	if !utils.EnableMultipleCmek {
		existingConfig := configs[0]
		return nil, errors.NewConflictErr(
			fmt.Sprintf("A KMS configuration already exists for this account with a different key path. Existing key: projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s, resourceId: %s",
				existingConfig.KeyProjectID, existingConfig.KeyRingLocation, existingConfig.KeyRing, existingConfig.KeyName, existingConfig.ResourceID))
	}

	return nil, errors.NewNotFoundErr("KMS Configuration", nil)
}

func convertDatastoreKmsConfigToModel(kmsConfig *datamodel.KmsConfig) *models.KmsConfig {
	return &models.KmsConfig{
		BaseModel: models.BaseModel{
			ID:        kmsConfig.ID,
			UUID:      kmsConfig.UUID,
			CreatedAt: kmsConfig.CreatedAt,
			UpdatedAt: kmsConfig.UpdatedAt,
		},
		CustomerProjectID: kmsConfig.CustomerProjectID,
		KeyProjectID:      kmsConfig.KeyProjectID,
		State:             kmsConfig.State,
		StateDetails:      kmsConfig.StateDetails,
		Description:       kmsConfig.Description,
		ResourceID:        kmsConfig.ResourceID,
		AccountID:         kmsConfig.AccountID,
		ServiceAccountID:  kmsConfig.ServiceAccountID,
		KmsAttributes: &models.KmsAttributes{
			SdeKmsConfigUUID:       kmsConfig.KmsAttributes.SdeKmsConfigUUID,
			SdeServiceAccountEmail: kmsConfig.KmsAttributes.SdeServiceAccountEmail,
			Instructions:           kmsConfig.KmsAttributes.Instructions,
			CreationMode:           kmsConfig.KmsAttributes.CreationMode,
			VcpServiceAccountEmail: kmsConfig.KmsAttributes.VcpServiceAccountEmail,
		},
		ServiceAccount:  convertDatastoreServiceAccountToModel(kmsConfig.ServiceAccount),
		Name:            kmsConfig.Name,
		KeyRing:         kmsConfig.KeyRing,
		KeyRingLocation: kmsConfig.KeyRingLocation,
		KeyName:         kmsConfig.KeyName,
		KmsGrant:        kmsConfig.KmsGrant,
	}
}

func convertDatastoreServiceAccountToModel(sa *datamodel.ServiceAccount) *models.ServiceAccount {
	if sa == nil {
		return nil
	}
	return &models.ServiceAccount{
		BaseModel: models.BaseModel{
			UUID:      sa.UUID,
			CreatedAt: sa.CreatedAt,
			UpdatedAt: sa.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(sa.DeletedAt),
		},
		Name:                           sa.Name,
		Description:                    sa.Description,
		State:                          sa.State,
		StateDetails:                   sa.StateDetails,
		AccountID:                      sa.AccountID,
		ServiceName:                    sa.ServiceName,
		ServiceAccountEmail:            sa.ServiceAccountEmail,
		ServiceAccountPasswordLocation: sa.ServiceAccountPasswordLocation,
	}
}

// convertDatastoreJobToModel converts a datastore Job to a model Job
func convertDatastoreJobToModel(job *datamodel.Job) *models.Job {
	if job == nil {
		return nil
	}

	modelJob := &models.Job{
		BaseModel: models.BaseModel{
			UUID:      job.UUID,
			CreatedAt: job.CreatedAt,
			UpdatedAt: job.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(job.DeletedAt),
		},
		CorrelationID: job.CorrelationID,
		RequestID:     job.RequestID,
		Type:          datamodel.JobType(job.Type),
		State:         datamodel.JobState(job.State),
		StateDetails:  "", // datamodel.Job doesn't have StateDetails field
		TrackingID:    job.TrackingID,
		ErrorDetails:  []byte(job.ErrorDetails),
		AccountID:     job.AccountID,
		IsAdminJob:    job.IsAdminJob,
		WorkflowID:    job.WorkflowID,
		ScheduledAt:   job.ScheduledAt,
		ResourceName:  job.ResourceName,
	}

	if job.JobAttributes != nil {
		modelJob.JobAttributes = &models.JobAttributes{
			ResourceUUID: job.JobAttributes.ResourceUUID,
			PoolUUID:     job.JobAttributes.PoolUUID,
		}
	}

	return modelJob
}

func validateKmsConfigState(ctx context.Context, se database.Storage, kmsConfigState string, accountId int64, localEntry bool) (string, error) {
	if kmsConfigState == datamodel.LifeCycleStateUpdating || kmsConfigState == datamodel.LifeCycleStateMigrating {
		jobMigration, dbErr := se.GetOngoingMigrateKmsConfigJob(ctx, accountId)
		if dbErr != nil {
			if errors.IsNotFoundErr(dbErr) {
				// In this case there is an Update job that is ongoing, which is not a migration job
				return "", errors.NewBadRequestErr("CMEK Configuration is undergoing an Update operation")
			}
			return "", dbErr
		}
		return jobMigration.UUID, nil
	}
	if kmsConfigState != datamodel.LifeCycleStateREADY && kmsConfigState != datamodel.LifeCycleStateInUse {
		return "", errors.NewBadRequestErr("CMEK Configuration needs to be in either Ready or In_Use state for migration")
	}
	return "", nil
}

func convertKmsConfigStateV1beta(status, stateDetails string) (state, details string) {
	switch status {
	case datamodel.LifeCycleStateCreated, datamodel.LifeCycleStateKeyCheckPending:
		return cvpModels.KmsConfigV1betaKmsStateKEYCHECKPENDING, "Credentials created and key check pending"
	case datamodel.LifeCycleStateInUse:
		return cvpModels.KmsConfigV1betaKmsStateINUSE, "Kms config in use"
	case datamodel.LifeCycleStateDeleted:
		return cvpModels.KmsConfigV1betaKmsStateDELETED, "Kms config deleted"
	case datamodel.LifeCycleStateUpdating:
		return cvpModels.KmsConfigV1betaKmsStateUPDATING, "Updating Kms config"
	case datamodel.LifeCycleStateDeleting:
		return cvpModels.KmsConfigV1betaKmsStateDELETING, "Deleting Kms config"
	case datamodel.LifeCycleStateCreating:
		return cvpModels.KmsConfigV1betaKmsStateCREATING, "Creating Kms config"
	case datamodel.LifeCycleStateREADY:
		return cvpModels.KmsConfigV1betaKmsStateREADY, "Kms config is ready for use"
	case datamodel.LifeCycleStateMigrating:
		return cvpModels.KmsConfigV1betaKmsStateMIGRATING, "Kms config is in migrating state"
	default:
		if strings.Contains(strings.ToLower(status), "error") {
			return cvpModels.KmsConfigV1betaKmsStateERROR, strings.TrimPrefix(stateDetails, "error - ")
		}
		return status, ""
	}
}

// convertSDEResponseToKmsConfig converts SDE response to models.KmsConfig
func convertSDEResponseToKmsConfig(kmsConfig *gcpserver.KmsConfigV1beta) *models.KmsConfig {
	modelKmsConfig := &models.KmsConfig{
		BaseModel: models.BaseModel{
			UUID: kmsConfig.UUID.Value,
		},
	}

	if kmsConfig.Description.IsSet() {
		modelKmsConfig.Description = kmsConfig.Description.Value
	}

	if kmsConfig.ResourceId.IsSet() {
		modelKmsConfig.ResourceID = kmsConfig.ResourceId.Value
	}

	if kmsConfig.KeyFullPath != "" {
		// Parse key full path to extract individual components
		if parsedKey, err := utils.ParseKeyFullPathResource(kmsConfig.KeyFullPath); err == nil {
			modelKmsConfig.KeyName = parsedKey.CryptoKey
			modelKmsConfig.KeyRing = parsedKey.KeyRing
			modelKmsConfig.KeyRingLocation = parsedKey.Location
			modelKmsConfig.KeyProjectID = parsedKey.ProjectID
			modelKmsConfig.KmsGrant = parsedKey.Grant
		}
	}

	if kmsConfig.KmsState.IsSet() {
		// Convert SDE state to internal state
		switch kmsConfig.KmsState.Value {
		case gcpserver.KmsConfigV1betaKmsStateREADY:
			modelKmsConfig.State = datamodel.LifeCycleStateREADY
		case gcpserver.KmsConfigV1betaKmsStateKEYCHECKPENDING:
			modelKmsConfig.State = datamodel.LifeCycleStateKeyCheckPending
		case gcpserver.KmsConfigV1betaKmsStateUPDATING:
			modelKmsConfig.State = datamodel.LifeCycleStateUpdating
		case gcpserver.KmsConfigV1betaKmsStateINUSE:
			modelKmsConfig.State = datamodel.LifeCycleStateInUse
		case gcpserver.KmsConfigV1betaKmsStateERROR:
			modelKmsConfig.State = datamodel.LifeCycleStateError
		default:
			modelKmsConfig.State = models.KmsConfigV1betaKmsStateKEYSTATEUNSPECIFIED
		}
	} else {
		modelKmsConfig.State = models.KmsConfigV1betaKmsStateKEYSTATEUNSPECIFIED
	}

	if kmsConfig.KmsStateDetails.IsSet() {
		modelKmsConfig.StateDetails = kmsConfig.KmsStateDetails.Value
	} else {
		modelKmsConfig.StateDetails = "Updated successfully"
	}

	if kmsConfig.ServiceAccountEmail.IsSet() {
		modelKmsConfig.KmsAttributes = &models.KmsAttributes{
			SdeKmsConfigUUID:       kmsConfig.UUID.Value,
			SdeServiceAccountEmail: kmsConfig.ServiceAccountEmail.Value,
			CreationMode:           datamodel.KmsCreationModeSDE,
		}
	} else {
		modelKmsConfig.KmsAttributes = &models.KmsAttributes{
			SdeKmsConfigUUID: kmsConfig.UUID.Value,
			CreationMode:     datamodel.KmsCreationModeSDE,
		}
	}

	if kmsConfig.CreatedTime.IsSet() {
		modelKmsConfig.CreatedAt = kmsConfig.CreatedTime.Value
	}

	if kmsConfig.UpdatedTime.IsSet() {
		modelKmsConfig.UpdatedAt = kmsConfig.UpdatedTime.Value
	}

	return modelKmsConfig
}

func (o *GCPOrchestrator) CreateAndSyncKmsConfig(ctx context.Context, params *common.CreateKmsConfigParams) (*models.KmsConfig, error) {
	kmsConfig, err := createAndSyncKmsConfig(ctx, o.storage, params)
	if err != nil {
		return nil, err
	}
	return convertDatastoreKmsConfigToModel(kmsConfig), nil
}

func (o *GCPOrchestrator) GetSDEKmsConfiguration(ctx context.Context, params *common.GetKmsConfigParams) (*cvpModels.KmsConfigV1beta, error) {
	// TODO:- check if we are able to get the jwt token from context
	return getSDEKmsConfiguration(ctx, params)
}
