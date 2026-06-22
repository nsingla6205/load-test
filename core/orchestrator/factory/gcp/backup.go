package gcp

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

const (
	GCBDRServiceType = datamodel.ServiceTypeCrossProject
)

var (
	hydrationEnabled = env.GetBool("GCP_HYDRATE_ENABLED", true)

	createBackupInternal        = _createBackupInternal
	updateBackupInternal        = _updateBackupInternal
	createBackup                = _createBackup
	validateCreateBackupParams  = _validateCreateBackupParams
	getBackups                  = _getBackups
	deleteBackup                = _deleteBackup
	updateBackup                = _updateBackup
	validateBackupDeleteParams  = _validateBackupDeleteParams
	validateSnapshotForBackup   = _validateSnapshotForBackup
	fetchRemoteBackupFromVCP    = _fetchRemoteBackupFromVCP
	hydrateDeletedBackupsToCCFE = _hydrateDeletedBackupsToCCFE
	hydrateCreatedBackupsToCCFE = _hydrateCreatedBackupsToCCFE

	// Dependency injection hooks for unit tests.
	getProviderByNode     = vsa.GetProviderByNode
	createNodeForProvider = vsa.CreateNodeForProvider
)

// CreateBackup creates the specified backup and adds it to the list of backup belonging to the specified BackupVault
func (o *GCPOrchestrator) CreateBackup(ctx context.Context, params *common.CreateBackupParams) (*models.Backup, string, error) {
	return createBackup(ctx, o.storage, o.temporal, params)
}

func (o *GCPOrchestrator) UpdateBackup(ctx context.Context, params *common.UpdateBackupParams) (*models.Backup, string, error) {
	return updateBackup(ctx, o.storage, o.temporal, params)
}

func (o *GCPOrchestrator) CreateBackupInternal(ctx context.Context, params *common.CreateBackupParams) (*models.Backup, string, error) {
	return createBackupInternal(ctx, o.storage, o.temporal, params)
}

func (o *GCPOrchestrator) UpdateBackupInternal(ctx context.Context, params *common.UpdateBackupParams) (*models.Backup, string, error) {
	return updateBackupInternal(ctx, o.storage, o.temporal, params)
}

func (o *GCPOrchestrator) ListBackups(ctx context.Context, backupVaultID, ownerID string, filters [][]interface{}) ([]*datamodel.Backup, error) {
	se := o.storage
	account, err := getOrCreateAccount(ctx, se, ownerID)
	if err != nil {
		return nil, err
	}
	params := &common.GetBackupsParams{
		BackupVaultID: backupVaultID,
		AccountID:     account.ID,
	}
	return getBackups(ctx, o.storage, params, filters)
}

// ListBackupsWithoutAccountFilter lists backups by vault UUID without account filtering
// This is used for GCBDR vaults where backups can come from multiple accounts/projects
func (o *GCPOrchestrator) ListBackupsWithoutAccountFilter(ctx context.Context, backupVaultID string, filters [][]interface{}) ([]*datamodel.Backup, error) {
	return o.storage.GetBackupsByBackupVaultUUIDAndFilter(ctx, backupVaultID, filters)
}

// GetBackupsByUUIDs retrieves multiple backups across all accounts/vaults by their UUIDs.
// The backup vault is preloaded with a narrow column set (id, uuid, source_region_name,
// backup_region_name, service_type, bucket_details, immutable_attributes) so callers can
// build batch responses without additional queries. Note that BackupVault.Account is NOT
// preloaded; callers that need account data must fetch it separately. Missing UUIDs are
// silently omitted from the result.
func (o *GCPOrchestrator) GetBackupsByUUIDs(ctx context.Context, backupUUIDs []string) ([]*datamodel.Backup, error) {
	return o.storage.BatchGetBackupsByUUIDs(ctx, backupUUIDs)
}

// GetBackupsUnderBackupVault retrieves all backups associated with the specified BackupVault
func (o *GCPOrchestrator) GetBackupsUnderBackupVault(ctx context.Context, backupVaultID, ownerID string, backupUUIDs []string) ([]*datamodel.Backup, error) {
	se := o.storage
	account, err := getOrCreateAccount(ctx, se, ownerID)
	if err != nil {
		return nil, err
	}
	params := &common.GetBackupsParams{
		BackupVaultID: backupVaultID,
		AccountID:     account.ID,
	}
	conditions := [][]interface{}{{"uuid in ?", backupUUIDs}}
	return o.GetBackups(ctx, params, conditions)
}

func (o *GCPOrchestrator) UpdateBackupLatestLogicalBackupSizeByVolume(ctx context.Context, volumeUUID, backupUUID string) error {
	se := o.storage
	err := se.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, volumeUUID, backupUUID)
	if err != nil {
		return err
	}
	return nil
}

// createVolumePayloadFromExpertModeVolume creates a volume object from an expert mode volume for workflow compatibility.
// The workflow needs PoolID, Pool (with DeploymentName, PoolCredentials, PoolAttributes), and VolumeAttributes.
func createVolumePayloadFromExpertModeVolume(ctx context.Context, se database.Storage, expertModeVol *datamodel.ExpertModeVolumes) (*datamodel.Volume, error) {
	// Build VolumeAttributes with VendorSubnetID from pool if available
	volumeAttributes := &datamodel.VolumeAttributes{
		ExternalUUID: expertModeVol.ExternalUUID,
	}
	// Set VendorSubnetID from pool if available, otherwise leave empty
	if expertModeVol.Pool.VendorID != "" {
		volumeAttributes.VendorSubnetID = expertModeVol.Pool.Network
	}

	volumeForWorkflow := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID:      expertModeVol.UUID,
			ID:        expertModeVol.ID,
			CreatedAt: expertModeVol.CreatedAt,
			UpdatedAt: expertModeVol.UpdatedAt,
		},
		Name:             expertModeVol.Name,
		AccountID:        expertModeVol.AccountID,
		PoolID:           expertModeVol.PoolID,
		State:            expertModeVol.State,
		Account:          expertModeVol.Account,
		Pool:             expertModeVol.Pool,
		VolumeAttributes: volumeAttributes,
		SvmID:            expertModeVol.Svm.ID,
		Svm:              expertModeVol.Svm,
	}

	return volumeForWorkflow, nil
}

func _createBackup(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateBackupParams) (*models.Backup, string, error) {
	logger := util.GetLogger(ctx)
	// Get the account
	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}
	err = validateCreateBackupParams(ctx, se, params)
	if err != nil {
		return nil, "", err
	}

	// Fetch from the appropriate table based on IsExpertModeVolume flag
	var volume *datamodel.Volume
	var expertModeVol *datamodel.ExpertModeVolumes
	var isExpertModeVolume bool
	isExpertModeVolume = params.IsExpertModeVolume

	if isExpertModeVolume {
		// Fetch from ExpertModeVolumes table
		expertModeVol, err = se.GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID)
		if err != nil {
			return nil, "", err
		}
		// Guard: if the volume's attached vault differs from the requested vault, block
		// the backup creation when existing backups are tied to the current vault.
		if expertModeVol.BackupConfig != nil &&
			expertModeVol.BackupConfig.BackupVaultID != "" &&
			expertModeVol.BackupConfig.BackupVaultID != params.BackupVaultID {
			currentVault, errVault := se.GetBackupVault(ctx, expertModeVol.BackupConfig.BackupVaultID)
			if errVault != nil {
				logger.Error("Failed to look up current backup vault for vault-switch check", "backupVaultID", expertModeVol.BackupConfig.BackupVaultID, "error", errVault)
				return nil, "", errVault
			}
			backupCount, errCount := se.GetBackupCountByVolumeAndVault(ctx, expertModeVol.ExternalUUID, currentVault.ID)
			if errCount != nil {
				logger.Error("Failed to check backup count for vault-switch check", "volumeUUID", expertModeVol.ExternalUUID, "error", errCount)
				return nil, "", errCount
			}
			if backupCount > 0 {
				return nil, "", customerrors.NewUserInputValidationErr("switching backup vault is not supported while backups exist; delete the existing backups first")
			}
		}
	} else {
		// Fetch from regular Volumes table
		// For GCBDR vaults, skip account validation - fetch by UUID only
		if params.BackupVaultServiceType == GCBDRServiceType {
			volume, err = se.GetVolume(ctx, params.VolumeUUID)
		} else {
			volume, err = se.GetVolumeWithAccountID(ctx, params.VolumeUUID, account.ID)
		}
		if err != nil {
			return nil, "", err
		}
	}

	backupVault, err := se.GetBackupVault(ctx, params.BackupVaultID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, "", customerrors.NewUserInputValidationErr("Backup vault not found")
		}
		return nil, "", err
	}
	stateUpdated := false
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateBackup),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.BackupName,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create backup create job in database", "error", err)
		return nil, "", err
	}

	var backupAttributes datamodel.BackupAttributes
	var volumeID int64
	if isExpertModeVolume && expertModeVol != nil {
		// Handle expert mode volume
		backupAttributes = datamodel.BackupAttributes{
			VolumeName:          expertModeVol.Name,
			AccountIdentifier:   account.Name,
			Protocols:           []string{}, // Expert mode volume protocols will fetch from ONTAP during workflow execution.
			UseExistingSnapshot: params.UseExistingSnapshot,
		}
		volumeID = expertModeVol.ID
	} else {
		// Handle regular volume
		if volume != nil {
			backupAttributes = datamodel.BackupAttributes{
				VolumeName:          volume.Name,
				AccountIdentifier:   account.Name,
				Protocols:           volume.VolumeAttributes.Protocols,
				UseExistingSnapshot: params.UseExistingSnapshot,
			}
			volumeID = volume.ID
		}
	}

	if params.UseExistingSnapshot && !isExpertModeVolume {
		dbSnapshot, err := se.GetSnapshotByUUID(ctx, params.SnapshotID, account.ID, volumeID)
		if err != nil {
			if customerrors.IsNotFoundErr(err) {
				return nil, "", customerrors.NewUserInputValidationErr("Snapshot not found")
			}
			return nil, "", err
		}
		backupAttributes.SnapshotName = dbSnapshot.Name
		backupAttributes.SnapshotID = dbSnapshot.SnapshotAttributes.ExternalUUID
	} else {
		if params.SnapshotID != "" {
			backupAttributes.SnapshotID = params.SnapshotID
		}
	}
	// Set backup attributes from params (for cross-region operations)
	if params.BucketName != "" {
		backupAttributes.BucketName = params.BucketName
	}
	if params.EndpointUUID != "" {
		backupAttributes.EndpointUUID = params.EndpointUUID
	}
	backupAttributes.IsRegionalHA = params.IsRegionalHA
	if params.CompletionTime != "" {
		backupAttributes.CompletionTime = params.CompletionTime
	}
	if params.BackupPolicyName != "" {
		backupAttributes.BackupPolicyName = params.BackupPolicyName
	}
	if params.OntapVolumeStyle != "" {
		backupAttributes.OntapVolumeStyle = params.OntapVolumeStyle
	}
	if params.SourceVolumeZone != "" {
		backupAttributes.SourceVolumeZone = params.SourceVolumeZone
	}
	if params.ServiceAccountName != "" {
		backupAttributes.ServiceAccountName = params.ServiceAccountName
	}
	if params.SnapshotCreationTime != "" {
		backupAttributes.SnapshotCreationTime = params.SnapshotCreationTime
	}
	if params.ConstituentCountOfBackup > 0 {
		backupAttributes.ConstituentCountOfBackup = params.ConstituentCountOfBackup
	}
	if params.BackupVaultServiceType == GCBDRServiceType && volume != nil && volume.Account != nil {
		backupAttributes.VolumeAccountName = volume.Account.Name
	}
	backupAttributes.IsExpertModeBackup = isExpertModeVolume
	dbBackup := &datamodel.Backup{
		Name:          params.BackupName,
		VolumeUUID:    params.VolumeUUID,
		BackupVaultID: backupVault.ID,
		Attributes:    &backupAttributes,
		Description:   params.Description,
		Type:          params.BackupType,
	}
	dbBackup.State = datamodel.LifeCycleStateCreating
	dbBackup.StateDetails = datamodel.LifeCycleStateCreatingDetails

	defer func() {
		if err != nil {
			// Delete backup if workflow execution failed
			// The workflow will handle its own error states
			if stateUpdated && dbBackup != nil && dbBackup.UUID != "" {
				if _, deleteErr := se.DeleteBackup(ctx, dbBackup.UUID); deleteErr != nil {
					logger.Error("Failed to delete backup after workflow start failure", "error", deleteErr, "backupUUID", dbBackup.UUID)
				} else {
					logger.Infof("Deleted backup %s after workflow failed to start", dbBackup.UUID)
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

	dbBackup, err = se.CreateBackup(ctx, dbBackup)
	if err != nil {
		return nil, "", err
	}

	stateUpdated = true

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	// For expert mode volumes, create a minimal volume object with essential fields for the workflow
	// The workflow needs volume.PoolID, Pool relationship, and VolumeAttributes.ExternalUUID
	var volumeForWorkflow *datamodel.Volume
	if isExpertModeVolume {
		volumeForWorkflow, err = createVolumePayloadFromExpertModeVolume(ctx, se, expertModeVol)
		if err != nil {
			return nil, "", err
		}
	} else {
		// Validate regular volume is not nil
		if volume == nil {
			return nil, "", fmt.Errorf("volume is nil")
		}
		volumeForWorkflow = volume
	}

	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.CreateBackupWorkflow,
		workflowengine.GetCreateBackupWorkflowTimeout(),
		params,
		dbBackup,
		backupVault,
		volumeForWorkflow,
	)
	if err != nil {
		logger.Error("Failed to start create backup workflow after retries: ", "error", err)
		return nil, "", err
	}

	return convertDatastoreBackupToModel(dbBackup), createdJob.UUID, nil
}

func _updateBackup(ctx context.Context, se database.Storage, temporal client.Client, params *common.UpdateBackupParams) (*models.Backup, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}
	// Fetch the backup
	backup, err := se.GetBackup(ctx, params.BackupVaultUUID, params.BackupUUID, account.Name)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, "", customerrors.NewUserInputValidationErr("Backup not found")
		}
		return nil, "", err
	}
	// Check if the backup is in a state that allows updates
	if backup.State != datamodel.LifeCycleStateAvailable {
		logger.Errorf("Backup %s cannot be updated, current state: %s. Only backups in AVAILABLE state can be updated", params.BackupUUID, backup.State)
		return nil, "", customerrors.NewUserInputValidationErr("Backup can only be updated when in AVAILABLE state, current state: " + backup.State)
	}

	if backup.BackupVault.BackupVaultType == activities.CrossRegionBackupType && params.Region == *backup.BackupVault.BackupRegionName {
		return nil, "", customerrors.NewUserInputValidationErr("Cannot update backup from the destination region")
	}

	stateUpdated := false
	originalState := backup.State
	originalStateDetails := backup.StateDetails

	// Update backup state
	backup.State = datamodel.LifeCycleStateUpdating
	backup.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	// Create a job for the update operation
	backupVaultUUID := backup.BackupVault.UUID

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateBackup),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.BackupUUID,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         backup.UUID,
			PreviousState:        originalState,
			PreviousStateDetails: originalStateDetails,
			PayloadAttributes:    map[string]interface{}{"backup_vault_uuid": backupVaultUUID, "account_name": account.Name},
		},
	}
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	defer func() {
		if err != nil {
			// Only rollback if the state was successfully updated but the workflow failed to start after all retry attempts.
			// The workflow will handle its own error states during execution and retries.
			if stateUpdated {
				backup.State = originalState
				backup.StateDetails = originalStateDetails
				if _, rollbackErr := se.UpdateBackupState(ctx, backup); rollbackErr != nil {
					logger.Error("Failed to rollback backup  state", "error", rollbackErr, "originalState", originalState)
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

	backup, err = se.UpdateBackupState(ctx, backup)
	if err != nil {
		logger.Error("Failed to update backup state in database", "error", err)
		return nil, "", err
	}
	stateUpdated = true
	backup.Description = params.Description

	// Execute the workflow for updating the backup
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.UpdateBackupWorkflow,
		nil,
		backup,
	)
	if err != nil {
		logger.Error("Failed to start update backup workflow after retries: ", "error", err)
		return nil, "", err
	}
	return convertDatastoreBackupToModel(backup), createdJob.UUID, nil
}

func _validateCreateBackupParams(ctx context.Context, se database.Storage, params *common.CreateBackupParams) error {
	backupInTransition, err := se.IsBackupInCreatingorDeletingStateByVolume(ctx, params.VolumeUUID)
	if err != nil {
		return err
	}
	if backupInTransition {
		return customerrors.NewUserInputValidationErr("A backup operation from the same volume is currently in progress. Please wait for it to complete before starting a new backup")
	}
	if params.IsExpertModeVolume {
		account, err := se.GetAccount(ctx, params.AccountName)
		if err != nil {
			return err
		}
		filters := [][]interface{}{{"name = ?", params.BackupName}}
		backups, err := se.GetBackupsByBackupVaultOwnerIDAndFilter(ctx, params.BackupVaultID, account.ID, filters)
		if err != nil {
			return err
		}
		if len(backups) > 0 {
			return customerrors.NewConflictErr("Backup with the same name already exists in the specified backup vault")
		}
		return nil
	} else {
		vol, err := se.GetVolume(ctx, params.VolumeUUID)
		if err != nil {
			return err
		}
		if vol.State != datamodel.LifeCycleStateREADY {
			return customerrors.NewUserInputValidationErr("Volume is not in available state")
		}
		if vol.VolumeAttributes != nil && vol.VolumeAttributes.CloneParentInfo != nil {
			cloneState := vol.VolumeAttributes.CloneParentInfo.State
			if cloneState == datamodel.CloneStateSplitting {
				return customerrors.NewConflictErr("Backup is not allowed when volume is splitting")
			}
		}
		if vol.DataProtection == nil {
			return customerrors.NewUserInputValidationErr("Volume does not have any backup vault associated with it")
		}
		if vol.DataProtection != nil && vol.DataProtection.BackupVaultID != params.BackupVaultID {
			return customerrors.NewUserInputValidationErr("Volume does not have the specified backup vault associated with it")
		}
		if vol.VolumeAttributes != nil && vol.VolumeAttributes.IsDataProtection && params.SnapshotID == "" {
			return customerrors.NewUserInputValidationErr("Backup creation is not supported for destination volumes without specifying an existing snapshot. Please use an existing snapshot to create backups or create a snapshot on the source volume and back that up on this volume once it has been replicated to this volume")
		}

		if common.SnapmirrorSnapshotPrefix.MatchString(params.BackupName) {
			return customerrors.NewUserInputValidationErr("Backups cannot be created from snapshots resulting from volume replication. Please use a non-replication snapshot and update the backup name to a non-replication snapshot name")
		}
		err = validateSnapshotForBackup(ctx, se, params, vol)
		if err != nil {
			return customerrors.NewUserInputValidationErr("Failed to validate snapshot for backup: " + err.Error())
		}
	}

	return nil
}

// GetBackups retrieves all backups associated with the specified BackupVault
func (o *GCPOrchestrator) GetBackups(ctx context.Context, params *common.GetBackupsParams, filters [][]interface{}) ([]*datamodel.Backup, error) {
	return _getBackups(ctx, o.storage, params, filters)
}

func _getBackups(ctx context.Context, se database.Storage, params *common.GetBackupsParams, filters [][]interface{}) ([]*datamodel.Backup, error) {
	return se.GetBackupsByBackupVaultOwnerIDAndFilter(ctx, params.BackupVaultID, params.AccountID, filters)
}

// GetBackup retrieves the backup associated with the specified BackupVault uuid and backup uuid and account name
func (o *GCPOrchestrator) GetBackup(ctx context.Context, params *common.GetBackupParams) (*datamodel.Backup, error) {
	return _getBackup(ctx, o.storage, params)
}

func _getBackup(ctx context.Context, se database.Storage, params *common.GetBackupParams) (*datamodel.Backup, error) {
	return se.GetBackup(ctx, params.BackupVaultID, params.BackupUUID, params.AccountName)
}

func (o *GCPOrchestrator) GetBackupByExternalUUID(ctx context.Context, backupVaultUUID string, externalUUID string, accountName string) (*datamodel.Backup, error) {
	return o.storage.GetBackupByExternalUUID(ctx, backupVaultUUID, externalUUID, accountName)
}

func convertDatastoreBackupToModel(backup *datamodel.Backup) *models.Backup {
	minimumEnforcedRetentionDuration := int64(0)
	isBackupImmutable := false
	var region string
	var backupVaultID string

	if backup.BackupVault != nil && backup.BackupVault.ImmutableAttributes != nil {
		minimumEnforcedRetentionDuration = *backup.BackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration
		isBackupImmutable = common.CheckIfBackupIsImmutable(backup)
	}

	if backup.BackupVault != nil {
		if backup.BackupVault.SourceRegionName != nil {
			region = *backup.BackupVault.SourceRegionName
		}
		backupVaultID = backup.BackupVault.UUID
	}

	protocols := []string{}
	if backup.Attributes != nil && backup.Attributes.Protocols != nil {
		protocols = backup.Attributes.Protocols
	}

	return &models.Backup{
		BackupID:                         backup.UUID,
		Name:                             backup.Name,
		VolumeID:                         backup.VolumeUUID,
		Region:                           region,
		VolumeName:                       backup.Attributes.VolumeName,
		BackupVaultID:                    backupVaultID,
		LifeCycleState:                   backup.State,
		LifeCycleStateDetails:            backup.StateDetails,
		Description:                      &backup.Description,
		Type:                             backup.Type,
		SnapshotName:                     backup.Attributes.SnapshotName,
		MinimumEnforcedRetentionDuration: &minimumEnforcedRetentionDuration,
		IsBackupImmutable:                isBackupImmutable,
		Protocols:                        protocols,
	}
}

func (o *GCPOrchestrator) DeleteBackup(ctx context.Context, params *common.DeleteBackupParams) (*models.BaseModel, string, error) {
	return deleteBackup(ctx, o.storage, o.temporal, params)
}

func (o *GCPOrchestrator) DeleteBackupInternal(ctx context.Context, params *common.DeleteBackupParams) (string, error) {
	se := o.storage
	logger := util.GetLogger(ctx)

	backup, err := se.GetBackupByExternalUUID(ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Infof("Backup with external uuid %s not found, nothing to delete", params.BackupUUID)
			return "", nil
		}
		return "", err
	}

	if backup.Attributes != nil && backup.Attributes.RestoreVolumeCount > 0 {
		logger.Errorf("Could not delete the backup as it is being used to restore volume(s): %d", backup.Attributes.RestoreVolumeCount)
		return "", customerrors.NewUserInputValidationErr("Cannot delete the backup as it is being used to restore a volume")
	}

	_, err = se.DeleteBackup(ctx, backup.UUID)
	if err != nil {
		logger.Error("Failed to delete backup in database", "error", err)
		return "", err
	}

	if hydrationEnabled {
		if backup.BackupVault == nil {
			logger.Errorf("Could not find the backup vault associated with the backup. Could not hydrate deleted cross-region backup")
			return "", vsaerrors.New("Could not find the backup vault associated with the backup. Could not hydrate deleted cross-region backup")
		}

		err = hydrateDeletedBackupsToCCFE(ctx, params, backup)
		if err != nil {
			logger.Errorf("Failed to hydrate deleted backup to CCFE: %v", err)
			return "", err
		}
	}
	return "", nil
}

func _hydrateCreatedBackupsToCCFE(ctx context.Context, params *common.CreateBackupParams, backup *datamodel.Backup) error {
	logger := util.GetLogger(ctx)
	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		return err
	}
	mode := models.BackupHydrationModeDefault
	if params.IsExpertModeVolume {
		mode = models.BackupHydrationModeONTAP
	}
	requests := common.ConvertToGCPHydrateBackupCreateRequests([]*datamodel.Backup{backup}, mode, params.SourceStoragePool)
	err = common.HydrateCreatedBackups(ctx, logger, requests, backup.BackupVault.Name, params.Region, params.AccountName, token)
	if err != nil {
		return err
	}
	return nil
}

func _hydrateDeletedBackupsToCCFE(ctx context.Context, params *common.DeleteBackupParams, backup *datamodel.Backup) error {
	logger := util.GetLogger(ctx)
	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		return err
	}
	requests := common.ConvertToGCPHydrateBackupDeleteRequests([]*datamodel.Backup{backup})
	err = common.HydrateDeletedBackups(ctx, logger, requests, backup.BackupVault.Name, params.Region, params.AccountName, token)
	if err != nil {
		return err
	}
	return nil
}

func _deleteBackup(ctx context.Context, se database.Storage, temporal client.Client, params *common.DeleteBackupParams) (*models.BaseModel, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	backup, err := se.GetBackup(ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	if backup.State == datamodel.LifeCycleStateError && !backup.Attributes.DeleteInitiated {
		_, err = se.DeleteBackup(ctx, backup.UUID)
		if err != nil {
			logger.Error("Failed to delete backup in database", "error", err)
			return nil, "", err
		}
		return nil, "", nil
	}

	if backup.State == datamodel.LifeCycleStateDeleting {
		filter := utils2.CreateFilterWithConditions(
			utils2.NewFilterCondition("resource_name", "=", backup.UUID),
			utils2.NewFilterCondition("state", "in", []string{string(datamodel.JobsStateNEW), string(datamodel.JobsStatePROCESSING)}),
		)
		jobs, err := se.GetJobsWithCondition(ctx, *filter)
		if err != nil {
			return nil, "", err
		}
		if len(jobs) != 0 {
			return nil, jobs[0].UUID, nil
		}
	}

	err = validateBackupDeleteParams(ctx, se, params)
	if err != nil {
		return nil, "", err
	}

	// Check whether any volume restore is in progress for this backup
	conditions := [][]interface{}{{"volume_attributes->>'restored_backup_id' = ?", backup.UUID}, {"state = ?", datamodel.LifeCycleStateRestoring}}
	volumes, err := se.ListVolumes(ctx, conditions)
	if err != nil {
		return nil, "", err
	}
	if len(volumes) > 0 {
		return nil, "", customerrors.NewUserInputValidationErr("Cannot delete backup as restore is in progress for this backup")
	}

	if backup.BackupVault != nil && backup.BackupVault.BackupVaultType == activities.CrossRegionBackupType {
		remoteBackup, err := fetchRemoteBackupFromVCP(ctx, backup.UUID, backup.BackupVault.UUID, params.AccountName, *backup.BackupVault.BackupRegionName)
		if err != nil && !customerrors.IsNotFoundErr(err) {
			logger.Errorf("Failed to fetch remote backup from VCP: %v", err)
			return nil, "", err
		}

		if remoteBackup.IsRestoring.Set && remoteBackup.IsRestoring.Value {
			logger.Errorf("Cannot delete backup %s as restore is in progress for this backup in remote region", backup.UUID)
			return nil, "", customerrors.NewUserInputValidationErr("Cannot delete backup as restore is in progress for this backup in remote region")
		}
	}

	originalState := backup.State
	originalStateDetails := backup.StateDetails
	stateUpdated := false

	backup.State = datamodel.LifeCycleStateDeleting
	backup.StateDetails = datamodel.LifeCycleStateDeletingDetails

	backupVaultUUID := ""
	if backup.BackupVault != nil {
		backupVaultUUID = backup.BackupVault.UUID
	}
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteBackup),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.BackupUUID,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         backup.UUID,
			PreviousState:        originalState,
			PreviousStateDetails: originalStateDetails,
			PayloadAttributes:    map[string]interface{}{"backup_vault_uuid": backupVaultUUID, "account_name": account.Name},
		},
	}
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	defer func() {
		if err != nil {
			// Only rollback if the state was successfully updated but workflow failed to start after all retry attempts.
			// With WorkflowExecutor retry logic, this defer block may execute after multiple failed workflow start attempts.
			// The workflow will handle its own error states.
			if stateUpdated {
				backup.State = originalState
				backup.StateDetails = originalStateDetails
				if _, rollbackErr := se.UpdateBackupState(ctx, backup); rollbackErr != nil {
					logger.Error("Failed to rollback backup  state", "error", rollbackErr, "originalState", originalState)
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

	_, err = se.UpdateBackupState(ctx, backup)
	if err != nil {
		logger.Error("Failed to change backup state in database", "error", err)
		return nil, "", err
	}
	stateUpdated = true
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.DeleteBackupWorkflow,
		workflowengine.GetDeleteBackupWorkflowTimeout(),
		params,
	)
	if err != nil {
		logger.Error("Failed to start delete backup workflow after retries: ", "error", err)
		return nil, "", err
	}
	return nil, createdJob.UUID, nil
}

func _validateBackupDeleteParams(ctx context.Context, se database.Storage, params *common.DeleteBackupParams) error {
	backup, err := se.GetBackup(ctx, params.BackupVaultUUID, params.BackupUUID, params.AccountName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return customerrors.NewUserInputValidationErr("Backup not found")
		}
		return err
	}

	// Check if any backup for the same volume is in transition state (CREATING or DELETING)
	backupInTransition, err := se.IsBackupInCreatingorDeletingStateByVolume(ctx, backup.VolumeUUID)
	if err != nil {
		return err
	}
	if backupInTransition {
		return customerrors.NewUserInputValidationErr("A backup operation from the same volume is currently in progress. Please wait for it to complete before starting a new backup")
	}

	if backup.BackupVault.BackupVaultType == activities.CrossRegionBackupType && params.Region == *backup.BackupVault.BackupRegionName {
		return customerrors.NewUserInputValidationErr("Cannot delete backup from the destination region")
	}

	// When EnableBackupVaultSwitching is true, the latest-backup + snapmirror check
	// runs inside DeleteBackupWorkflow where ONTAP calls are available (google-proxy
	// pods cannot reach ONTAP).
	if !utils.EnableBackupVaultSwitching {
		isLatest, err := se.IsLatestBackup(ctx, backup.UUID, backup.VolumeUUID)
		if err != nil {
			return err
		}

		count, err := se.BackupCountByVolumeID(ctx, backup.VolumeUUID)
		if err != nil {
			return err
		}

		if isLatest && count > 1 {
			return customerrors.NewUserInputValidationErr("Cannot delete latest backup")
		}
	}

	// Immutability check
	if utils.IsImmutableBackupEnabled() {
		backupVault, err := se.GetBackupVault(ctx, params.BackupVaultUUID)
		if err != nil {
			if customerrors.IsNotFoundErr(err) {
				return customerrors.NewUserInputValidationErr("Backup vault not found")
			}
			return err
		}

		if backupVault.ImmutableAttributes != nil {
			minRet := int64(0)
			if backupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration != nil {
				minRet = *backupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration
			}
			if minRet > 0 {
				isDailyRetEnabled := backupVault.ImmutableAttributes.IsDailyBackupImmutable
				isWeeklyRetEnabled := backupVault.ImmutableAttributes.IsWeeklyBackupImmutable
				isMonthlyRetEnabled := backupVault.ImmutableAttributes.IsMonthlyBackupImmutable
				isAdhocRetEnabled := backupVault.ImmutableAttributes.IsAdhocBackupImmutable
				backupCreationDate := backup.CreatedAt
				retExpiryDate := backupCreationDate.AddDate(0, 0, int(minRet))

				if backup.Type == common.BackupTypeSCHEDULED {
					if backup.ScheduleTag != nil && ((isDailyRetEnabled && *backup.ScheduleTag == common.ScheduleTagDaily) ||
						(isWeeklyRetEnabled && *backup.ScheduleTag == common.ScheduleTagWeekly) ||
						(isMonthlyRetEnabled && *backup.ScheduleTag == common.ScheduleTagMonthly)) {
						if time.Now().Before(retExpiryDate) {
							return customerrors.NewUserInputValidationErr("Cannot delete backup before minimum retention period")
						}
					}
				} else if backup.Type == common.BackupTypeMANUAL {
					if isAdhocRetEnabled {
						if time.Now().Before(retExpiryDate) {
							return customerrors.NewUserInputValidationErr("Cannot delete backup before minimum retention period")
						}
					}
				}
			}
		}
	}

	return nil
}

func _validateSnapshotForBackup(ctx context.Context, se database.Storage, params *common.CreateBackupParams, vol *datamodel.Volume) error {
	if params.UseExistingSnapshot {
		if params.SnapshotID == "" {
			return customerrors.NewUserInputValidationErr("Missing value for 'SnapshotID'")
		}
		snapshot, err := se.GetSnapshotByUUID(ctx, params.SnapshotID, vol.AccountID, vol.ID)
		if err != nil {
			if customerrors.IsNotFoundErr(err) {
				return customerrors.NewUserInputValidationErr("Snapshot not found")
			}
			return err
		}
		if snapshot.State != datamodel.LifeCycleStateREADY {
			return customerrors.NewUserInputValidationErr("Snapshot is not in available state")
		}
		if common.SnapmirrorSnapshotPrefix.MatchString(snapshot.Name) {
			return customerrors.NewUserInputValidationErr("Backups cannot be created from snapshots resulting from volume replication. Please use a non-replication snapshot.")
		}
		// Check if snapshot has already been used for creating a backup in available state
		filters := [][]interface{}{
			{"volume_uuid = ?", vol.UUID},
			{"state != ?", datamodel.LifeCycleStateDeleted},
			{"attributes->>'snapshot_id' = ?", snapshot.SnapshotAttributes.ExternalUUID},
		}
		backups, err := se.GetBackupsByBackupVaultOwnerIDAndFilter(ctx, vol.DataProtection.BackupVaultID, vol.Account.ID, filters)
		if err != nil {
			return err
		}
		if len(backups) > 0 {
			return customerrors.NewUserInputValidationErr("This snapshot has already been used to create a backup")
		}
	} else {
		if params.SnapshotID != "" {
			return customerrors.NewUserInputValidationErr("Cannot set Snapshot ID when useExistingSnapshot is false")
		}

		if snapshotInDB, err := se.GetSnapshotByNameAndVolumeId(ctx, params.BackupName, vol.AccountID, vol.ID); err != nil && !customerrors.IsNotFoundErr(err) {
			return err
		} else if snapshotInDB != nil {
			return customerrors.NewUserInputValidationErr("Backup creation failed because the name conflicts with an existing snapshot. Please use the existing snapshot or choose a new backup name. A new name will create a new snapshot for the backup")
		}
	}
	return nil
}

// fetchRemoteBackupFromVCP fetches the Backup from the remote region using Google Proxy Client
func _fetchRemoteBackupFromVCP(ctx context.Context, backupUUID, backupVaultUUID, projectNumber, region string) (googleproxyclient.InternalBackupV1beta, error) {
	logger := util.GetLogger(ctx)
	basePath, jwtToken, err := common.GetRemoteRegionConfig(region, projectNumber)
	if err != nil {
		logger.Error("Failed to get remote region configuration", "region", region, "error", err)
		return googleproxyclient.InternalBackupV1beta{}, err
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	params := googleproxyclient.V1betaInternalDescribeBackupParams{
		ProjectNumber:  projectNumber,
		LocationId:     region,
		BackupVaultId:  backupVaultUUID,
		BackupId:       backupUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	res, err := googleProxyClient.Invoker.V1betaInternalDescribeBackup(ctx, params)
	if err != nil {
		logger.Errorf("Failed to fetch remote Backup: %v, region=%s, backupVaultID=%s, backupID=%s", err, region, backupVaultUUID, backupUUID)
		return googleproxyclient.InternalBackupV1beta{}, err
	}

	switch r := res.(type) {
	case *googleproxyclient.V1betaInternalDescribeBackupOK:
		if len(r.Backups) == 0 {
			logger.Errorf("No backups found in remote response, backupID=%s", backupUUID)
			return googleproxyclient.InternalBackupV1beta{}, customerrors.NewNotFoundErr("remote backup", &backupUUID)
		}
		backup := r.Backups[0]
		logger.Infof("Successfully fetched remote Backup, backupID=%s, region=%s", backup.ResourceId.Value, region)
		return backup, nil
	case *googleproxyclient.V1betaInternalDescribeBackupNotFound:
		logger.Warnf("Remote backup not found in region %s, backupVaultID=%s, backupID=%s: %s", region, backupVaultUUID, backupUUID, r.Message)
		return googleproxyclient.InternalBackupV1beta{}, customerrors.NewNotFoundErr("remote backup", &backupUUID)
	default:
		apiErr := fmt.Errorf("remote describe backup failed: %v (%T)", res, res)
		logger.Errorf("Remote describe backup returned error response: %v, region=%s, backupVaultID=%s, backupID=%s", apiErr, region, backupVaultUUID, backupUUID)
		return googleproxyclient.InternalBackupV1beta{}, apiErr
	}
}

// _createBackupInternal creates a backup without starting a workflow (for internal cross-region operations)
func _createBackupInternal(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateBackupParams) (*models.Backup, string, error) {
	// Get the account
	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	// Internal calls are only for cross-region backups - expect everything in params
	// Volume and snapshot are not available in remote region DB, so we use params directly
	if params.VolumeName == "" || len(params.Protocols) == 0 {
		return nil, "", customerrors.NewUserInputValidationErr("Volume information (volumeName and protocols) is required for cross-region backup creation")
	}

	backupVault, err := se.GetBackupVaultByExternalUUIDAndOwnerID(ctx, params.BackupVaultID, account.ID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, "", customerrors.NewUserInputValidationErr("Backup vault not found")
		}
		return nil, "", err
	}

	// Check if backup already exists by ExternalUUID (backupUUID from request)
	existingBackup, err := se.GetBackupByExternalUUID(ctx, params.BackupVaultID, params.BackupUUID, params.AccountName)
	if err == nil && existingBackup != nil {
		// Backup already exists, return it
		return convertDatastoreBackupToModel(existingBackup), "", nil
	}
	// If error is not NotFound, return the error
	if err != nil && !customerrors.IsNotFoundErr(err) {
		return nil, "", err
	}

	// Use UseExistingSnapshot directly from params
	backupAttributes := datamodel.BackupAttributes{
		VolumeName:          params.VolumeName,
		AccountIdentifier:   account.Name,
		Protocols:           params.Protocols,
		UseExistingSnapshot: params.UseExistingSnapshot,
		SnapshotID:          params.SnapshotID,
		SnapshotName:        params.SnapshotName,
	}
	// Set backup attributes from params (for cross-region operations)
	if params.BucketName != "" {
		backupAttributes.BucketName = params.BucketName
	}
	if params.EndpointUUID != "" {
		backupAttributes.EndpointUUID = params.EndpointUUID
	}
	backupAttributes.IsRegionalHA = params.IsRegionalHA
	if params.CompletionTime != "" {
		backupAttributes.CompletionTime = params.CompletionTime
	}
	if params.BackupPolicyName != "" {
		backupAttributes.BackupPolicyName = params.BackupPolicyName
	}
	if params.OntapVolumeStyle != "" {
		backupAttributes.OntapVolumeStyle = params.OntapVolumeStyle
	}
	if params.SourceVolumeZone != "" {
		backupAttributes.SourceVolumeZone = params.SourceVolumeZone
	}
	if params.ServiceAccountName != "" {
		backupAttributes.ServiceAccountName = params.ServiceAccountName
	}
	if params.SnapshotCreationTime != "" {
		backupAttributes.SnapshotCreationTime = params.SnapshotCreationTime
	}
	if params.ConstituentCountOfBackup > 0 {
		backupAttributes.ConstituentCountOfBackup = params.ConstituentCountOfBackup
	}

	backupAttributes.IsExpertModeBackup = params.IsExpertModeVolume
	dbBackup := &datamodel.Backup{
		Name:          params.BackupName,
		ExternalUUID:  params.BackupUUID, // For cross-region backups, backupUUID from request becomes ExternalUUID
		VolumeUUID:    params.VolumeUUID,
		BackupVaultID: backupVault.ID,
		Attributes:    &backupAttributes,
		Description:   params.Description,
		Type:          params.BackupType,
		AssetMetadata: &datamodel.AssetMetadata{
			ChildAssets: []datamodel.ChildAsset{
				{
					AssetType:  common.BackupAssetType,
					AssetNames: []string{fmt.Sprintf("//storage.googleapis.com/%s", params.BucketName)},
				},
			},
		},
		LatestLogicalBackupSize: params.BackupChainBytes,
		SizeInBytes:             params.VolumeUsageBytes,
	}
	dbBackup.State = datamodel.LifeCycleStateAvailable
	dbBackup.StateDetails = datamodel.LifeCycleStateAvailableDetails

	dbBackup, err = se.CreateBackup(ctx, dbBackup)
	if err != nil {
		return nil, "", err
	}
	dbBackup, err = se.FinishBackup(ctx, dbBackup)
	if err != nil {
		return nil, "", err
	}

	if hydrationEnabled {
		if dbBackup.BackupVault == nil {
			return nil, "", vsaerrors.New("Could not find the backup vault associated with the backup. Could not hydrate created cross-region backup")
		}

		err = hydrateCreatedBackupsToCCFE(ctx, params, dbBackup)
		if err != nil {
			return nil, "", err
		}
	}

	return convertDatastoreBackupToModel(dbBackup), "", nil
}

// _updateBackupInternal updates a backup without starting a workflow (for internal cross-region operations)
func _updateBackupInternal(ctx context.Context, se database.Storage, temporal client.Client, params *common.UpdateBackupParams) (*models.Backup, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}
	// Fetch the backup using ExternalUUID (for internal cross-region operations, BackupUUID is ExternalUUID)
	backup, err := se.GetBackupByExternalUUID(ctx, params.BackupVaultUUID, params.BackupUUID, account.Name)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, "", customerrors.NewUserInputValidationErr("Backup not found")
		}
		return nil, "", err
	}
	// Check if the backup is in a state that allows updates
	if backup.State != datamodel.LifeCycleStateAvailable {
		logger.Errorf("Backup %s cannot be updated, current state: %s. Only backups in AVAILABLE state can be updated", params.BackupUUID, backup.State)
		return nil, "", customerrors.NewUserInputValidationErr("Backup can only be updated when in AVAILABLE state, current state: " + backup.State)
	}

	backup.Description = params.Description

	// Update backup in local database
	backup, err = se.UpdateBackup(ctx, backup)
	if err != nil {
		logger.Error("Failed to update backup in database", "error", err)
		return nil, "", err
	}

	return convertDatastoreBackupToModel(backup), "", nil
}
