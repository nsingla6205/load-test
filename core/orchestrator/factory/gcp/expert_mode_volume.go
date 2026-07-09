package gcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	expertModeWorkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/expertMode"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

// ManageBackupConfigForExpertModeVolume attaches (or updates) a backup vault and optional backup policy
// on an expert mode volume. It validates that the pool is ONTAP mode, the volume exists and is READY,
// then creates a job and launches the ManageBackupConfigWorkflow.
func (o *GCPOrchestrator) ManageBackupConfigForExpertModeVolume(ctx context.Context, params *commonparams.ManageBackupConfigForExpertModeVolumeParams) (*datamodel.DataProtection, string, error) {
	return manageBackupConfigForExpertModeVolume(ctx, o.storage, o.temporal, params)
}

func manageBackupConfigForExpertModeVolume(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ManageBackupConfigForExpertModeVolumeParams) (*datamodel.DataProtection, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	dbPoolView, err := se.GetPool(ctx, params.PoolUUID, account.ID)
	if err != nil {
		logger.Error("Failed to get pool", "poolUUID", params.PoolUUID, "error", err)
		return nil, "", err
	}

	if dbPoolView.APIAccessMode != APIAccessModeONTAP {
		return nil, "", customerrors.NewUserInputValidationErr("manageBackupConfig is only supported for ONTAP mode (expert mode) pools")
	}

	expertModeVolume, err := se.GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			return nil, "", customerrors.NewUserInputValidationErr(fmt.Sprintf("expert mode volume with UUID '%s' not found", params.VolumeUUID))
		}
		logger.Error("Failed to get expert mode volume", "volumeUUID", params.VolumeUUID, "error", err)
		return nil, "", err
	}

	if expertModeVolume.Pool.UUID != dbPoolView.UUID {
		return nil, "", customerrors.NewUserInputValidationErr("volume does not belong to the specified pool")
	}

	if expertModeVolume.State == datamodel.LifeCycleStateDeleting || expertModeVolume.State == datamodel.LifeCycleStateDeleted {
		return nil, "", customerrors.NewUserInputValidationErr(fmt.Sprintf("volume is not in a ready state (current state: %s)", expertModeVolume.State))
	}

	kmsGrantProvided := params.KmsGrant != nil && *params.KmsGrant != ""
	managingPolicyOrKms := (params.BackupPolicyID != nil && *params.BackupPolicyID != "") || kmsGrantProvided

	// BackupVaultID patch semantics:
	//   nil    → not provided: carry forward existing vault when policy/KMS requires one; otherwise no vault operation.
	//   &""    → explicit detach: remove vault from volume (validated below).
	//   &"uuid"→ attach/set: validate and (if different) switch vaults.
	if params.BackupVaultID == nil {
		// Vault not in payload: if managing policy/KMS, use the vault already on the volume.
		if managingPolicyOrKms {
			existingVaultID := ""
			if expertModeVolume.BackupConfig != nil {
				existingVaultID = expertModeVolume.BackupConfig.BackupVaultID
			}
			if existingVaultID == "" {
				return nil, "", customerrors.NewUserInputValidationErr("backup vault id is required to assign a backup policy to a volume")
			}
			params.BackupVaultID = &existingVaultID
		}
		// else: nil + no policy/KMS = no vault operation needed; fall through.
	} else if *params.BackupVaultID == "" {
		// Explicit detach: block if a policy is attached, a KMS grant is set, or backups exist.
		if expertModeVolume.BackupConfig != nil && expertModeVolume.BackupConfig.BackupVaultID != "" {
			if expertModeVolume.BackupConfig.BackupPolicyID != "" {
				return nil, "", customerrors.NewUserInputValidationErr("cannot remove backup vault while a backup policy is attached; detach the backup policy first")
			}
			if expertModeVolume.BackupConfig.KmsGrant != nil && *expertModeVolume.BackupConfig.KmsGrant != "" {
				return nil, "", customerrors.NewUserInputValidationErr("cannot remove backup vault while a KMS grant is attached; remove the KMS grant first")
			}
			backupCounts, err := se.GetBackupCountByVolumeUUIDs(ctx, []string{expertModeVolume.ExternalUUID}, nil)
			if err != nil {
				logger.Error("Failed to check backup count for volume", "volumeUUID", expertModeVolume.ExternalUUID, "error", err)
				return nil, "", err
			}
			if backupCounts[expertModeVolume.ExternalUUID] > 0 {
				return nil, "", customerrors.NewUserInputValidationErr("cannot remove backup vault as there are backups associated with it")
			}
		}
	}

	if params.ScheduledBackupEnabled != nil && *params.ScheduledBackupEnabled {
		effectiveBackupPolicyID := ""
		if params.BackupPolicyID != nil {
			effectiveBackupPolicyID = *params.BackupPolicyID
		} else if expertModeVolume.BackupConfig != nil {
			effectiveBackupPolicyID = expertModeVolume.BackupConfig.BackupPolicyID
		}
		if strings.TrimSpace(effectiveBackupPolicyID) == "" {
			return nil, "", customerrors.NewUserInputValidationErr("cannot enable scheduled backups without a backup policy")
		}
	}

	if params.BackupVaultID != nil && *params.BackupVaultID != "" {
		// Resolve the vault. When EnableBackupVaultSwitching is on the vault may belong to a
		// different project (cross-project GCBDR), so look it up by UUID only; otherwise scope
		// the lookup to the current account to preserve existing ownership checks.
		var bv *datamodel.BackupVault
		if utils.EnableBackupVaultSwitching {
			bv, err = se.GetBackupVault(ctx, *params.BackupVaultID)
		} else {
			bv, err = se.GetBackupVaultByUUIDndOwnerID(ctx, *params.BackupVaultID, account.ID)
		}
		if err != nil && !customerrors.IsNotFoundErr(err) {
			return nil, "", err
		}
		// In a VCP-only region (CVP_HOST not set), VCP is the sole source of truth: a vault absent from
		// the local DB is a hard not-found (no SDE/CVP fallback in the workflow).
		if bv == nil && !utils.IsCVPHostConfigured() {
			return nil, "", customerrors.NewNotFoundErr("backup vault", params.BackupVaultID)
		}
		if bv != nil {
			if bv.LifeCycleState == datamodel.LifeCycleStateError {
				return nil, "", customerrors.NewUserInputValidationErr("backup vault is in error state, please check the backup vault and try again")
			}
			if err := validateCRBBackupVault(bv, params.Region); err != nil {
				return nil, "", err
			}
			if bv.CmekAttributes != nil && !nillable.IsNilOrEmpty(bv.CmekAttributes.KmsConfigResourcePath) && !kmsGrantProvided {
				return nil, "", customerrors.NewUserInputValidationErr("KMS Grant is required for CMEK Backup vault")
			}

			// Re-attaching a vault when none is currently set: if the volume already has available
			// backups tied to a previously detached vault, only a GCBDR vault may be attached
			// (non-GCBDR cannot consume that backup chain).
			if utils.EnableBackupVaultSwitching {
				noVaultAttached := expertModeVolume.BackupConfig == nil || expertModeVolume.BackupConfig.BackupVaultID == ""
				if noVaultAttached {
					vaultIDs, errDistinct := se.GetDistinctBackupVaultIDsByVolumeUUID(ctx, expertModeVolume.UUID)
					if errDistinct != nil {
						return nil, "", errDistinct
					}
					if len(vaultIDs) > 0 && bv.ServiceType != activities.GCBDRServiceType {
						return nil, "", customerrors.NewUserInputValidationErr("cannot attach a non-GCBDR backup vault while the volume has existing backups from a detached backup vault; delete those backups first, or attach a GCBDR backup vault")
					}
				}
			}
		}

		if params.BackupPolicyID != nil && *params.BackupPolicyID != "" {
			// Validate the backup policy exists and is ready.
			backupPolicy, err := se.GetBackupPolicyByUUIDAndOwnerID(ctx, *params.BackupPolicyID, account.ID)
			if err != nil && !customerrors.IsNotFoundErr(err) {
				return nil, "", err
			}
			// In a VCP-only region (CVP_HOST not set), a policy absent from VCP is a hard not-found:
			// the workflow must not fall back to SDE to import it.
			if backupPolicy == nil && !utils.IsCVPHostConfigured() {
				return nil, "", customerrors.NewNotFoundErr("backup policy", params.BackupPolicyID)
			}
			if backupPolicy != nil && backupPolicy.LifeCycleState != datamodel.LifeCycleStateREADY {
				return nil, "", customerrors.NewUserInputValidationErr("backup policy is not in ready state, please check the backup policy and try again")
			}
			if params.ScheduledBackupEnabled == nil {
				return nil, "", customerrors.NewUserInputValidationErr("scheduled backups needs to be enabled/disabled when a backup policy is assigned to a volume")
			}

			// Validate that the backup policy's retention settings comply with the vault's
			// immutable backup configuration (with retry for concurrent update races).
			if utils.IsImmutableBackupEnabled() {
				logger.Debug("Validating immutable backup policy compliance for expert mode volume",
					"backupPolicyID", *params.BackupPolicyID,
					"backupVaultID", *params.BackupVaultID)
				if immErr := checkIsValidImmutableBackupPolicyWithRetry(ctx, se, *params.BackupPolicyID, *params.BackupVaultID, account.ID, params.Region, params.AccountName); immErr != nil {
					logger.Errorf("Immutable backup policy validation failed: %v", immErr)
					if customerrors.IsUnavailableErr(immErr) || customerrors.IsNetworkError(immErr) {
						return nil, "", customerrors.NewUnavailableErr(fmt.Sprintf("service is temporarily unavailable, please try again later: %v", immErr))
					}
					var customErr *vsaerrors.CustomError
					if vsaerrors.As(immErr, &customErr) {
						if customErr.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupPolicy ||
							customErr.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupVault {
							return nil, "", customerrors.NewUnavailableErr(fmt.Sprintf("backup policy or vault is currently being updated, please try again later: %v", immErr))
						}
					}
					return nil, "", customerrors.NewUserInputValidationErr(fmt.Sprintf("backup policy is not compliant with immutable backup vault settings: %v", immErr))
				}
			}
		}

		// Guard against switching to a different backup vault while backups exist.
		if expertModeVolume.BackupConfig != nil &&
			expertModeVolume.BackupConfig.BackupVaultID != "" &&
			expertModeVolume.BackupConfig.BackupVaultID != *params.BackupVaultID {
			currentVault, errVault := se.GetBackupVault(ctx, expertModeVolume.BackupConfig.BackupVaultID)
			if errVault != nil {
				logger.Error("Failed to look up current backup vault for vault-switch check", "backupVaultID", expertModeVolume.BackupConfig.BackupVaultID, "error", errVault)
				return nil, "", errVault
			}

			if utils.EnableBackupVaultSwitching {
				backupInProgress, errInProgress := se.IsBackupInCreatingorDeletingStateByVolume(ctx, expertModeVolume.ExternalUUID)
				if errInProgress != nil {
					return nil, "", errInProgress
				}
				if backupInProgress {
					return nil, "", customerrors.NewUserInputValidationErr("A backup operation on the volume is currently in progress. Please wait for it to complete before changing or removing the backup vault")
				}

				distinctVaultIDs, errDistinct := se.GetDistinctBackupVaultIDsByVolumeUUID(ctx, expertModeVolume.ExternalUUID)
				if errDistinct != nil {
					return nil, "", errDistinct
				}
				volumeHasBackups := len(distinctVaultIDs) > 0

				if err := validateBackupVaultFamilySwitch(currentVault, bv, volumeHasBackups); err != nil {
					return nil, "", err
				}
				if err := validateDetachedBackupsAllowAttachVault(ctx, se, expertModeVolume.ExternalUUID, bv); err != nil {
					return nil, "", err
				}
				if bv != nil && bv.ServiceType == datamodel.ServiceTypeGCNV && bv.AccountID != expertModeVolume.AccountID {
					return nil, "", customerrors.NewUserInputValidationErr("The target backup vault belongs to a different account and cannot be associated with the volume")
				}
			} else {
				backupCount, errCount := se.GetBackupCountByVolumeAndVault(ctx, expertModeVolume.ExternalUUID, currentVault.ID)
				if errCount != nil {
					logger.Error("Failed to check backup count for vault-switch check", "volumeUUID", expertModeVolume.ExternalUUID, "error", errCount)
					return nil, "", errCount
				}
				if backupCount > 0 {
					return nil, "", customerrors.NewUserInputValidationErr("switching backup vault is not supported while backups exist; delete the existing backups first")
				}
			}
		}
	}

	previousState := expertModeVolume.State

	job := &datamodel.Job{
		Type:         string(datamodel.JobTypeManageBackupConfigExpertModeVolume),
		State:        string(datamodel.JobsStateNEW),
		ResourceName: expertModeVolume.Name,
		AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:  expertModeVolume.UUID,
			PoolUUID:      dbPoolView.UUID,
			PreviousState: previousState,
		},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job for manage backup config", "error", err)
		return nil, "", err
	}

	// Defer 1: mark job as ERROR if anything after this point fails.
	defer func() {
		if err != nil && createdJob != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to ERROR", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	// Mark volume as UPDATING so concurrent operations are blocked while the workflow runs.
	// Work on a copy to avoid mutating the struct returned from the DB.
	volCopy := *expertModeVolume
	volCopy.State = datamodel.LifeCycleStateUpdating
	_, err = se.UpdateExpertModeVolume(ctx, &volCopy)
	if err != nil {
		logger.Error("Failed to update expert mode volume state to UPDATING", "volumeUUID", expertModeVolume.UUID, "error", err)
		return nil, "", err
	}

	// Defer 2: if workflow fails to launch after UPDATING was set, revert volume to its previous
	// state so the user can retry. Setting ERROR here would be invisible to CCFE (the caller
	// already receives the launch error) and would permanently block all future update attempts.
	defer func() {
		if err != nil && createdJob != nil {
			volCopy.State = previousState
			if _, revertErr := se.UpdateExpertModeVolume(ctx, &volCopy); revertErr != nil {
				logger.Error("Failed to revert expert mode volume state after workflow launch failure", "volumeUUID", expertModeVolume.UUID, "error", revertErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    *workflowengine.GetCreateBackupWorkflowTimeout(),
		},
		expertModeWorkflows.ManageBackupConfigWorkflow,
		expertModeVolume,
		params,
	)
	if err != nil {
		logger.Error("Failed to start manage backup config workflow", "workflowID", createdJob.WorkflowID, "error", err)
		return nil, "", err
	}

	backupConfig := &datamodel.DataProtection{
		ScheduledBackupEnabled: params.ScheduledBackupEnabled,
	}
	if params.BackupVaultID != nil {
		backupConfig.BackupVaultID = *params.BackupVaultID
	}
	if params.BackupPolicyID != nil {
		backupConfig.BackupPolicyID = *params.BackupPolicyID
	}
	return backupConfig, createdJob.UUID, nil
}

const (
	ExpertModeVolumeStyleFlexgroup = "flexgroup"
	APIAccessModeONTAP             = "ONTAP"
)

// expertModeVolumeToVolumeForSFR builds a partial *datamodel.Volume from expert mode volume for use with RestoreFilesFromBackupWorkflow.
// Same field mapping as convertExpertModeVolumeToVolume in workflows/expertMode (VolumeAttributes has no FileProperties/BlockProperties).
func expertModeVolumeToVolumeForSFR(emv *datamodel.ExpertModeVolumes) *datamodel.Volume {
	volumeAttributes := &datamodel.VolumeAttributes{ExternalUUID: emv.ExternalUUID}
	return &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: emv.UUID},
		Name:             emv.Name,
		Description:      emv.Description,
		State:            emv.State,
		SizeInBytes:      emv.SizeInBytes,
		AccountID:        emv.AccountID,
		PoolID:           emv.PoolID,
		SvmID:            emv.SvmID,
		Account:          emv.Account,
		Pool:             emv.Pool,
		Svm:              emv.Svm,
		VolumeAttributes: volumeAttributes,
		DataProtection:   emv.BackupConfig,
	}
}

// RestoreOntapModeBackup restores an expert mode volume from backup by starting RestoreForOntapModeVolumeWorkflow.
func (o *GCPOrchestrator) RestoreOntapModeBackup(ctx context.Context, params *commonparams.RestoreOntapModeBackupParams) (string, error) {
	return restoreOntapModeBackup(ctx, o.storage, o.temporal, params)
}

// SFROntapModeBackup performs selective file restore for an expert mode volume from backup (when SourceFileList is set).
func (o *GCPOrchestrator) SFROntapModeBackup(ctx context.Context, params *commonparams.RestoreOntapModeBackupParams) (string, error) {
	return sfrOntapModeBackup(ctx, o.storage, o.temporal, params)
}

// sfrOntapModeBackup runs the selective file restore path for ontap mode: expert-mode validation + backup resolution + RestoreFilesFromBackupWorkflow.
func sfrOntapModeBackup(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.RestoreOntapModeBackupParams) (string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return "", err
	}

	if params.PoolID == "" {
		return "", customerrors.NewUserInputValidationErr("PoolID is required for expert mode restore")
	}

	poolView, err := se.DescribePool(ctx, params.PoolID, account.ID)
	if err != nil {
		return "", err
	}
	if poolView.APIAccessMode != commonparams.ONTAPMode {
		return "", customerrors.NewUserInputValidationErr("Pool is not an expert mode (ONTAP) pool")
	}

	expertModeVolume, err := se.GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID)
	if err != nil {
		return "", err
	}

	// Check runs at restore start only. If a previous restore failed and left state in ERROR/RESTORING,
	// this check fails and blocks starting a new restore until state is corrected (e.g. by defer in SFR workflow).
	if expertModeVolume.State != datamodel.LifeCycleStateAvailable && expertModeVolume.State != datamodel.LifeCycleStateREADY {
		return "", customerrors.NewUserInputValidationErr("Volume is not available")
	}

	if params.BackupPath == "" {
		return "", customerrors.NewUserInputValidationErr("BackupPath must be provided")
	}

	components := strings.Split(params.BackupPath, "/")
	if len(components) < MaxBackupPathComponents {
		return "", customerrors.NewUserInputValidationErr("Backup path is not in correct format")
	}

	backupRegion := components[LocationIdIndex]
	location, err := utils.GetLocationFromVendorID(expertModeVolume.Pool.VendorID)
	if err != nil {
		logger.Error("Failed to get location from vendor ID: ", "error", err)
		return "", err
	}
	volumeRegion, _, err := utils.ParseRegionAndZone(location)
	if err != nil {
		return "", err
	}

	var backupVault *datamodel.BackupVault
	backupVaultName := components[BackupVaultNameIndex]
	if backupRegion != volumeRegion {
		backupVaultPath := strings.Join(components[:BackupVaultNameIndex+1], "/")
		backupVault, err = se.GetBackupVaultByCrossRegionBackupVaultName(ctx, backupVaultPath, account.ID)
		if err != nil {
			return "", err
		}
	} else {
		backupVault, err = se.GetBackupVaultByNameAndOwnerID(ctx, backupVaultName, strconv.FormatInt(account.ID, 10))
		if err != nil {
			return "", err
		}
	}

	backupName := components[BackupNameIndex]
	backup, err := se.GetBackupByNameAndBackupVaultID(ctx, backupName, backupVault.ID)
	if err != nil {
		return "", err
	}

	if backup.State != datamodel.LifeCycleStateAvailable {
		return "", customerrors.NewUserInputValidationErr("Cannot restore files from backup which is not available")
	}

	originalState := expertModeVolume.State
	stateUpdated := false

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeRestoreFilesBackup),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  expertModeVolume.UUID,
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
			if stateUpdated {
				if rollbackErr := se.UpdateExpertModeVolumeFields(ctx, expertModeVolume.ExternalUUID, map[string]interface{}{
					"state": originalState,
				}); rollbackErr != nil {
					logger.Error("Failed to rollback expert mode volume state", "error", rollbackErr, "originalState", originalState)
				}
			}
			if createdJob != nil {
				if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
					logger.Error("Failed to update job state to ERROR", "error", jobErr, "jobUUID", createdJob.UUID)
				}
			}
		}
	}()

	err = se.UpdateExpertModeVolumeFields(ctx, expertModeVolume.ExternalUUID, map[string]interface{}{
		"state": datamodel.LifeCycleStateRestoring,
	})
	if err != nil {
		logger.Error("Failed to update expert mode volume state to restoring", "error", err)
		return "", err
	}
	stateUpdated = true

	sfrParams := &commonparams.RestoreFilesFromBackupParams{
		AccountName:         params.AccountName,
		BackupPath:          params.BackupPath,
		SourceFileList:      params.SourceFileList,
		RestoreFilePath:     params.RestoreFilePath,
		VolumeUUID:          params.VolumeUUID,
		Region:              params.Region,
		PoolID:              params.PoolID,
		IsExpertModeRestore: true,
	}
	volumeForWorkflow := expertModeVolumeToVolumeForSFR(expertModeVolume)

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.RestoreFilesFromBackupWorkflow,
		workflowengine.GetSFRWorkflowTimeout(),
		sfrParams,
		volumeForWorkflow,
	)
	if err != nil {
		logger.Error("Failed to start restore files from backup workflow after retries: ", "error", err)
		return "", err
	}
	return createdJob.UUID, nil
}

func restoreOntapModeBackup(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.RestoreOntapModeBackupParams) (string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return "", err
	}

	if params.PoolID == "" {
		return "", customerrors.NewUserInputValidationErr("PoolID is required for expert mode restore")
	}

	poolView, err := se.DescribePool(ctx, params.PoolID, account.ID)
	if err != nil {
		return "", err
	}
	if poolView.APIAccessMode != commonparams.ONTAPMode {
		return "", customerrors.NewUserInputValidationErr("Pool is not an expert mode (ONTAP) pool")
	}

	expertModeVolume, err := se.GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID)
	if err != nil {
		return "", err
	}

	if expertModeVolume.State != datamodel.LifeCycleStateAvailable {
		return "", customerrors.NewUserInputValidationErr("Volume is not available")
	}

	volumeRegion := params.Region
	originalState := expertModeVolume.State
	stateUpdated := false

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeRestoreOntapModeBackup),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  expertModeVolume.UUID,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create restore from backup job in database", "error", err)
		return "", err
	}

	defer func() {
		if err != nil {
			if stateUpdated {
				if rollbackErr := se.UpdateExpertModeVolumeFields(ctx, expertModeVolume.ExternalUUID, map[string]interface{}{
					"state": originalState,
				}); rollbackErr != nil {
					logger.Error("Failed to rollback expert mode volume state", "error", rollbackErr, "originalState", originalState)
				}
			}
			if createdJob != nil {
				if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
					logger.Error("Failed to update job state to ERROR", "error", jobErr, "jobUUID", createdJob.UUID)
				}
			}
		}
	}()

	err = se.UpdateExpertModeVolumeFields(ctx, expertModeVolume.ExternalUUID, map[string]interface{}{
		"state": datamodel.LifeCycleStateRestoring,
	})
	if err != nil {
		logger.Error("Failed to update expert mode volume state to restoring", "error", err)
		return "", err
	}
	stateUpdated = true

	restoreParams := &commonparams.RestoreForOntapModeParams{
		AccountName:      params.AccountName,
		BackupPath:       params.BackupPath,
		Region:           volumeRegion,
		ExpertModeVolume: expertModeVolume,
	}

	// Start Temporal workflow for restore
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    *workflowengine.GetCreateBackupWorkflowTimeout(),
		},
		expertModeWorkflows.RestoreForOntapModeVolumeWorkflow,
		restoreParams,
	)
	if err != nil {
		logger.Error("Failed to start restore workflow", "error", err)
		return "", err
	}
	return createdJob.UUID, nil
}

// CreateExpertModeVolume creates a new expert mode volume
func (o *GCPOrchestrator) CreateExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error {
	return _createExpertModeVolume(ctx, o.storage, o.temporal, params)
}

// createExpertModeVolume creates a new expert mode volume and triggers reconciliation workflow
func _createExpertModeVolume(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ExpertModeVolumeParams) error {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return err
	}

	// Get the pool by ID
	dbPoolView, err := se.GetPool(ctx, params.PoolUUID, account.ID)
	if err != nil {
		logger.Error("Failed to get pool by UUID", "poolUUID", params.PoolUUID, "error", err)
		return err
	}

	volumeName := params.VolumeName
	existingVolume, err := se.GetExpertModeVolumeByNameAndPoolID(ctx, volumeName, dbPoolView.ID)
	if err != nil {
		// If the error is NOT "record not found", it's a real database error
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Error("Failed to check for existing volume", "volumeName", volumeName, "poolID", dbPoolView.ID, "error", err)
			return err
		}
	} else if existingVolume != nil {
		logger.Error("Volume with same name already exists in pool",
			"volumeName", volumeName,
			"poolID", dbPoolView.ID)
		return customerrors.NewBadRequestErr(fmt.Sprintf("a volume named '%s' already exists in this pool; if the previous volume was deleted or creation failed, wait at least 180 seconds before reusing the name", volumeName))
	}

	var svm *datamodel.Svm

	isCloneCreate, parentVolumeUUID, parentVolumeName, parentSnapshotUUID, parentSnapshotName := resolveCloneIdentifiers(params.Clone)
	effectiveSizeInBytes := params.SizeInBytes
	if isCloneCreate {
		if parentVolumeUUID == "" && parentVolumeName == "" {
			return customerrors.NewBadRequestErr("clone.parentVolume.uuid or clone.parentVolume.name is required for clone create")
		}
		parentSizeInBytes, resolvedParentVolumeUUID, resolvedParentVolumeName, err := fetchParentVolumeSizeForCloneCreate(ctx, se, dbPoolView.Pool, parentVolumeUUID, parentVolumeName)
		if err != nil {
			return err
		}
		effectiveSizeInBytes = parentSizeInBytes
		parentVolumeUUID = resolvedParentVolumeUUID
		parentVolumeName = resolvedParentVolumeName
	}

	err = canFitInPool(ctx, se, dbPoolView.ID, dbPoolView.SizeInBytes, effectiveSizeInBytes)
	if err != nil {
		return err
	}

	// Look up SVM based on provided parameters
	if params.SvmUuid != "" {
		// If svmUUID is provided, fetch SVM by external UUID and validate it belongs to the pool
		svm, err = se.GetSvmByExternalUUID(ctx, params.SvmUuid, dbPoolView.ID)
		if err != nil {
			logger.Error("Failed to find SVM by external UUID", "svmUuid", params.SvmUuid, "error", err)
			if customerrors.IsNotFoundErr(err) {
				return customerrors.NewBadRequestErr(fmt.Sprintf("SVM with UUID '%s' not found in pool", params.SvmUuid))
			}
			return err
		}
	} else if params.SvmName != "" {
		// If svmName is provided, fetch SVM by name and poolID
		svm, err = se.GetSvmByNameAndPoolID(ctx, params.SvmName, dbPoolView.ID)
		if err != nil {
			logger.Error("Failed to find SVM by name and poolID", "svmName", params.SvmName, "poolID", dbPoolView.ID, "error", err)
			if customerrors.IsNotFoundErr(err) {
				return customerrors.NewBadRequestErr(fmt.Sprintf("SVM with name '%s' not found in pool", params.SvmName))
			}
			return err
		}
	} else {
		// Neither svmUUID nor svmName is provided
		logger.Error("Neither svmName nor svmUUID has been passed")
		return customerrors.NewBadRequestErr("neither svmName nor svmUUID has been passed")
	}

	// Create expert mode volume record
	expertModeVolume := &datamodel.ExpertModeVolumes{
		Name:        params.VolumeName,
		SizeInBytes: effectiveSizeInBytes,
		PoolID:      dbPoolView.ID,
		AccountID:   dbPoolView.AccountID,
		Style:       params.Style,
		State:       datamodel.LifeCycleStateCreating,
		SvmID:       svm.ID,
	}
	if isCloneCreate {
		expertModeVolume.VolumeAttributes = &datamodel.ExpertModeVolumeAttributes{
			IsFlexclone: true,
			Clone: &datamodel.ExpertModeCloneInfo{
				ParentVolume: &datamodel.ExpertModeCloneParent{
					UUID: parentVolumeUUID,
					Name: parentVolumeName,
				},
				ParentSnapshot: &datamodel.ExpertModeCloneParent{
					UUID: parentSnapshotUUID,
					Name: parentSnapshotName,
				},
			},
		}
	}

	createdVolume, err := se.CreateExpertModeVolume(ctx, expertModeVolume)
	if err != nil {
		logger.Error("Failed to create expert mode volume", "error", err)
		return err
	}

	volume, err := se.GetExpertModeVolumeByUUID(ctx, createdVolume.UUID)
	if err != nil {
		logger.Error("Failed to get expert mode volume with preloads", "volumeUUID", createdVolume.UUID, "error", err)
		return err
	}

	// Create a job for the volume creation workflow
	correlationID := utils.GetCoRelationIDFromContext(ctx)
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateExpertModeVolume),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  createdVolume.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: createdVolume.UUID, PoolUUID: dbPoolView.UUID},
		CorrelationID: correlationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job for expert mode volume", "error", err)
		return err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.BackgroundTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetExpertModeSyncWorkflowTimeout(),
		},
		expertModeWorkflows.VolumeCreateReconciliationWorkflow,
		volume,
	)

	if err != nil {
		logger.Error("Failed to start volume reconciliation workflow", "workflowID", createdJob.WorkflowID, "error", err)
		return err
	}

	return nil
}

func fetchParentVolumeSizeForCloneCreate(ctx context.Context, se database.Storage, pool datamodel.Pool, parentVolumeUUID string, parentVolumeName string) (int64, string, string, error) {
	logger := util.GetLogger(ctx)

	var parentVolume *datamodel.ExpertModeVolumes
	var err error

	if parentVolumeUUID != "" {
		parentVolume, err = se.GetExpertModeVolumeByExternalUUID(ctx, parentVolumeUUID)
		if err != nil {
			logger.Error("Failed to fetch parent volume by external UUID for clone create", "parentVolumeUUID", parentVolumeUUID, "error", err)
			if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
				return 0, "", "", customerrors.NewBadRequestErr(fmt.Sprintf("parent volume '%s' not found", parentVolumeUUID))
			}
			return 0, "", "", err
		}
		if parentVolume.PoolID != pool.ID {
			return 0, "", "", customerrors.NewBadRequestErr("parent volume is not in the requested pool")
		}
		if parentVolumeName != "" && parentVolume.Name != parentVolumeName {
			return 0, "", "", customerrors.NewBadRequestErr("parent volume name does not match parent volume UUID")
		}
	} else {
		parentVolume, err = se.GetExpertModeVolumeByNameAndPoolID(ctx, parentVolumeName, pool.ID)
		if err != nil {
			logger.Error("Failed to fetch parent volume by name for clone create", "parentVolumeName", parentVolumeName, "poolID", pool.ID, "error", err)
			if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
				return 0, "", "", customerrors.NewBadRequestErr(fmt.Sprintf("parent volume '%s' not found", parentVolumeName))
			}
			return 0, "", "", err
		}
	}
	if parentVolume == nil || parentVolume.SizeInBytes <= 0 {
		return 0, "", "", customerrors.NewBadRequestErr("invalid parent volume size for clone create")
	}
	return parentVolume.SizeInBytes, parentVolume.ExternalUUID, parentVolume.Name, nil
}

func resolveCloneIdentifiers(clone *commonparams.ExpertModeVolumeCloneParams) (bool, string, string, string, string) {
	isCloneCreate := false
	parentVolumeUUID := ""
	parentVolumeName := ""
	parentSnapshotUUID := ""
	parentSnapshotName := ""

	if clone != nil {
		isCloneCreate = clone.IsFlexclone
		if clone.ParentVolume != nil {
			parentVolumeUUID = clone.ParentVolume.UUID
			parentVolumeName = clone.ParentVolume.Name
		}
		if clone.ParentSnapshot != nil {
			parentSnapshotUUID = clone.ParentSnapshot.UUID
			parentSnapshotName = clone.ParentSnapshot.Name
		}
	}
	return isCloneCreate, parentVolumeUUID, parentVolumeName, parentSnapshotUUID, parentSnapshotName
}

// returns error if the new volume cannot fit in the pool
func canFitInPool(ctx context.Context, se database.Storage, poolID, poolSizeInBytes, newVolumeSizeToAdd int64) error {
	logger := util.GetLogger(ctx)
	if newVolumeSizeToAdd <= 0 {
		logger.Error("Volume size must be greater than 0")
		return customerrors.NewBadRequestErr("volume size must be greater than 0")
	}

	// Calculate the total existing size to validate pool capacity
	capacity, err := se.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, poolID)
	if err != nil {
		logger.Error("Failed to calculate total existing size", "poolID", poolID, "error", err)
		return err
	}

	consumedSizeOfPool := capacity.TotalSize
	// Check if the new volume can fit in the pool
	if consumedSizeOfPool+newVolumeSizeToAdd > int64(poolSizeInBytes) {
		logger.Error("Insufficient pool capacity", "poolID", poolID, "requestedSize", newVolumeSizeToAdd, "availableSize", int64(poolSizeInBytes)-consumedSizeOfPool)
		return customerrors.NewBadRequestErr("insufficient pool capacity for the requested volume size")
	}
	return nil
}

// validatePoolCapacityForExpertModeFlexCloneSplit ensures pool remaining free space is at least vol.SharedBytes
// (the shared clone space that must be materialized on split).
func validatePoolCapacityForExpertModeFlexCloneSplit(ctx context.Context, se database.Storage, vol *datamodel.ExpertModeVolumes, poolSizeInBytes int64, poolID int64) error {
	logger := util.GetLogger(ctx)
	need := vol.SharedBytes
	if need < 0 {
		need = 0
	}
	capacity, err := se.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, poolID)
	if err != nil {
		logger.Error("Failed to get expert mode pool used capacity for flexclone split", "poolID", poolID, "error", err)
		return err
	}
	remaining := poolSizeInBytes - capacity.TotalSize
	if remaining < need {
		return customerrors.NewBadRequestErr(fmt.Sprintf("insufficient pool capacity for flexclone split: need at least %d bytes free, have %d bytes", need, remaining))
	}
	return nil
}

// DeleteExpertModeVolume deletes an expert mode volume
func (o *GCPOrchestrator) DeleteExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error {
	return _deleteExpertModeVolume(ctx, o.storage, o.temporal, params)
}

// _deleteExpertModeVolume deletes an expert mode volume and triggers reconciliation workflow
func _deleteExpertModeVolume(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ExpertModeVolumeParams) error {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return err
	}

	dbPoolView, err := se.GetPool(ctx, params.PoolUUID, account.ID)
	if err != nil {
		logger.Error("Failed to get pool", "poolUUID", params.PoolUUID, "error", err)
		return err
	}

	volume, err := getExpertModeVolume(ctx, se, params, dbPoolView)
	if err != nil {
		return err
	}

	if params.PoolUUID != volume.Pool.UUID {
		logger.Error("Volume is not associated to the pool for delete operation", "volumeUUID", volume.ExternalUUID, "poolUUID", params.PoolUUID)
		return customerrors.NewBadRequestErr("volume is not associated to the specified pool for delete operation")
	}

	// Block deletion if any backup for this volume is currently in a transition state (CREATING or DELETING).
	backupInTransition, err := se.IsBackupInCreatingorDeletingStateByVolume(ctx, volume.ExternalUUID)
	if err != nil {
		logger.Error("Failed to check backup transition state for expert mode volume", "volumeUUID", volume.ExternalUUID, "error", err)
		return err
	}
	if backupInTransition {
		return customerrors.NewUserInputValidationErr("A backup operation on volume is currently in progress. Please wait for it to complete before deleting the volume")
	}

	// Check if volume is already deleted
	if volume.State == datamodel.LifeCycleStateDeleted {
		return nil
	}

	previousState := volume.State
	volume.State = datamodel.LifeCycleStateDeleting
	_, err = se.UpdateExpertModeVolume(ctx, volume)
	if err != nil {
		logger.Error("Failed to update volume state to DELETING", "volumeUUID", volume.UUID, "error", err)
		return err
	}

	var volumeMarkedAsDeleting bool = true

	// Create a job for the volume deletion workflow
	correlationID := utils.GetCoRelationIDFromContext(ctx)
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteExpertModeVolume),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  volume.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: volume.UUID, PoolUUID: volume.Pool.UUID},
		CorrelationID: correlationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job for expert mode volume deletion", "error", err)
		return err
	}

	// Defer statement to mark job as errored and revert volume state if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
			// Revert volume state only if it was successfully marked as deleting
			if volumeMarkedAsDeleting {
				volume.State = previousState
				if _, revertErr := se.UpdateExpertModeVolume(ctx, volume); revertErr != nil {
					logger.Error("Failed to revert volume state", "volumeUUID", volume.UUID, "previousState", previousState, "error", revertErr)
				}
			}
		}
	}()

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.BackgroundTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetExpertModeSyncWorkflowTimeout(),
		},
		expertModeWorkflows.VolumeDeleteReconciliationWorkflow,
		volume,
	)

	if err != nil {
		logger.Error("Failed to start volume deletion reconciliation workflow", "workflowID", createdJob.WorkflowID, "error", err)
		return err
	}

	return nil
}

// GetExpertModeVolumeByExternalUUID retrieves an expert mode volume by its UUID
func (o *GCPOrchestrator) GetExpertModeVolumeByExternalUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error) {
	return o.storage.GetExpertModeVolumeByExternalUUID(ctx, volumeUUID)
}

// UpdateExpertModeVolume updates an existing expert mode volume
func (o *GCPOrchestrator) UpdateExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error {
	return _updateExpertModeVolume(ctx, o.storage, o.temporal, params)
}

func validateUpdateParams(ctx context.Context, se database.Storage, params *commonparams.ExpertModeVolumeParams, volume *datamodel.ExpertModeVolumes) error {
	logger := util.GetLogger(ctx)

	if params.PoolUUID != volume.Pool.UUID {
		logger.Error("Volume is not associated to the pool for update operation", "volumeUUID", volume.ExternalUUID, "params.PoolUUID", params.PoolUUID, "volume.Pool.UUID", volume.Pool.UUID)
		return customerrors.NewBadRequestErr("volume is not associated to the pool for update operation")
	}
	if params.SizeInBytes < 0 {
		logger.Error("Volume size must be greater than or equal to 0", "volumeSize", params.SizeInBytes)
		return customerrors.NewBadRequestErr("Volume size must be greater than or equal to 0")
	}

	if volume.State == datamodel.LifeCycleStateDeleted || volume.State == datamodel.LifeCycleStateError {
		logger.Error("Volume is deleted, cannot update", "volumeUUID", volume.ExternalUUID)
		return customerrors.NewBadRequestErr(fmt.Sprintf("volume with UUID '%s' is deleted", volume.ExternalUUID))
	}

	if volume.State == datamodel.LifeCycleStateCreating || volume.State == datamodel.LifeCycleStateDeleting || volume.State == datamodel.LifeCycleStateUpdating {
		logger.Error("Volume is in a transitional state and cannot be updated", "volumeUUID", volume.ExternalUUID, "state", volume.State)
		return customerrors.NewBadRequestErr(fmt.Sprintf("volume with UUID '%s' is in a transitional state and cannot be updated", volume.ExternalUUID))
	}

	if params.VolumeName != "" {
		poolID := volume.PoolID
		// Check if another volume with the same name exists in the same pool
		existingVolume, err := se.GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, poolID)
		if err != nil {
			// If the error is NOT "record not found", it's a real database error
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Error("Failed to check for existing volume", "volumeName", params.VolumeName, "poolID", poolID, "error", err)
				return err
			}
		} else if existingVolume != nil {
			// If a volume with the same name exists and it's not the same volume being updated, return an error
			if existingVolume.ExternalUUID != volume.ExternalUUID {
				logger.Error("Volume with same name already exists in pool",
					"volumeName", params.VolumeName,
					"poolID", poolID,
					"existingVolumeExternalUUID", existingVolume.ExternalUUID)
				return customerrors.NewBadRequestErr(fmt.Sprintf("volume with name '%s' already exists in pool", params.VolumeName))
			}
		}
	}
	return nil
}

// _updateExpertModeVolume updates an expert mode volume and updates it in the DB
func _updateExpertModeVolume(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ExpertModeVolumeParams) error {
	logger := util.GetLogger(ctx)

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return err
	}

	dbPoolView, err := se.GetPool(ctx, params.PoolUUID, account.ID)
	if err != nil {
		logger.Error("Failed to get pool", "poolUUID", params.PoolUUID, "error", err)
		return err
	}

	volume, err := getExpertModeVolume(ctx, se, params, dbPoolView)
	if err != nil {
		return err
	}

	err = validateUpdateParams(ctx, se, params, volume)
	if err != nil {
		return err
	}

	// Validate account matches - use AccountID directly as it's always set
	if volume.AccountID != account.ID {
		logger.Error("Volume does not belong to the specified account", "volumeUUID", volume.ExternalUUID, "volumeAccountID", volume.AccountID, "accountID", account.ID)
		return customerrors.NewBadRequestErr("volume does not belong to the specified account")
	}

	// Create a deep copy of the volume before modifying it
	// This preserves the original values for the workflow's error handling
	oldVolumeCopy := *volume
	oldVolume := &oldVolumeCopy
	var previousSize int64 = volume.SizeInBytes
	// Validate size if provided
	if params.SizeInBytes > 0 {
		// Calculate size increase
		sizeIncrease := params.SizeInBytes - volume.SizeInBytes
		if sizeIncrease > 0 {
			// Check pool capacity if size is being increased
			err = canFitInPool(ctx, se, volume.Pool.ID, volume.Pool.SizeInBytes, sizeIncrease)
			if err != nil {
				return err
			}
		}
		// Update size only if provided and > 0
		volume.SizeInBytes = params.SizeInBytes
	}

	if params.VolumeName != "" {
		volume.Name = params.VolumeName
	}

	previousState := volume.State
	volume.State = datamodel.LifeCycleStateUpdating

	volumeMarkedAsUpdating := true

	// Update volume in DB
	_, err = se.UpdateExpertModeVolume(ctx, volume)
	if err != nil {
		logger.Error("Failed to update volume state to UPDATING and size", "volumeUUID", volume.UUID, "error", err)
		return err
	}

	// Defer statement to mark job as errored and revert volume state if any operation fails
	// Registered early to ensure it executes even if CreateJob or ExecuteWorkflow fails
	var createdJob *datamodel.Job
	defer func() {
		if err != nil {
			if createdJob != nil {
				if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, err.Error()); jobErr != nil {
					logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
				}
			}
			// Revert volume state only if it was successfully marked as updating
			if volumeMarkedAsUpdating {
				volume.State = previousState
				volume.SizeInBytes = previousSize
				if _, revertErr := se.UpdateExpertModeVolume(ctx, volume); revertErr != nil {
					logger.Error("Failed to revert volume state", "volumeUUID", volume.UUID, "previousState", previousState, "error", revertErr)
				}
			}
		}
	}()

	// Create a job for the volume update workflow
	correlationID := utils.GetCoRelationIDFromContext(ctx)
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateExpertModeVolume),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  volume.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: volume.UUID, PoolUUID: volume.Pool.UUID},
		CorrelationID: correlationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job for expert mode volume update", "error", err)
		// Defer will handle the volume state revert
		return err
	}

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.BackgroundTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetExpertModeSyncWorkflowTimeout(),
		},
		expertModeWorkflows.VolumeUpdateReconciliationWorkflow,
		volume, oldVolume,
	)

	if err != nil {
		logger.Error("Failed to start volume update reconciliation workflow", "workflowID", createdJob.WorkflowID, "error", err)
		return err
	}

	return nil
}

// GetExpertModeVolumeByUUID retrieves an expert mode volume by its UUID
func (o *GCPOrchestrator) GetExpertModeVolumeByUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error) {
	return o.storage.GetExpertModeVolumeByUUID(ctx, volumeUUID)
}

// GetBackupConfigsForPool retrieves backup configurations for all expert mode volumes in a pool
func (o *GCPOrchestrator) GetBackupConfigsForPool(ctx context.Context, poolID string, accountName string, locationId string) ([]*models.ExpertModeVolumeBackupConfig, error) {
	logger := util.GetLogger(ctx)

	// Get account
	account, err := getAccountWithName(ctx, o.storage, accountName)
	if err != nil {
		return nil, err
	}

	// Get pool to validate it exists and get the internal ID
	dbPoolView, err := o.storage.GetPool(ctx, poolID, account.ID)
	if err != nil {
		logger.Error("Failed to get pool", "poolID", poolID, "error", err)
		return nil, err
	}

	if dbPoolView.APIAccessMode != APIAccessModeONTAP {
		logger.Error("Backup configurations are only available for ONTAP-mode pools", "poolID", poolID, "apiAccessMode", dbPoolView.APIAccessMode)
		return nil, customerrors.NewBadRequestErr("backup configurations are only available for ONTAP pools")
	}

	// Get all expert mode volumes for this pool
	expertModeVolumes, err := o.storage.ListExpertModeVolumesByPoolID(ctx, dbPoolView.ID)
	if err != nil {
		logger.Error("Failed to list expert mode volumes", "poolID", dbPoolView.ID, "error", err)
		return nil, err
	}

	backupVaults, err := o.storage.ListBackupVaults(ctx, account.ID)
	if err != nil {
		logger.Error("Failed to list backup vaults", "accountID", account.ID, "error", err)
		return nil, err
	}
	vaultsByUUID := make(map[string]string, len(backupVaults))
	for _, bv := range backupVaults {
		vaultsByUUID[bv.UUID] = bv.Name
	}

	conditions := [][]interface{}{{"account_id = ?", account.ID}}
	backupPolicies, err := o.storage.ListBackupPolicies(ctx, conditions)
	if err != nil {
		logger.Error("Failed to list backup policies", "accountID", account.ID, "error", err)
		return nil, err
	}
	policiesByUUID := make(map[string]string, len(backupPolicies))
	for _, bp := range backupPolicies {
		policiesByUUID[bp.UUID] = bp.Name
	}

	backupConfigs := make([]*models.ExpertModeVolumeBackupConfig, 0, len(expertModeVolumes))
	for _, vol := range expertModeVolumes {
		config := &models.ExpertModeVolumeBackupConfig{
			VolumeResourceID: vol.ExternalUUID,
		}

		if vol.BackupConfig != nil && vol.BackupConfig.BackupVaultID != "" {
			if path, ok := expertModeBackupVaultPath(
				ctx, o.storage, vol.BackupConfig.BackupVaultID, accountName, locationId, vaultsByUUID,
			); ok {
				config.BackupVaultPath = &path
			} else {
				logger.Error("Backup vault not found for volume", "volumeName", vol.Name, "backupVaultID", vol.BackupConfig.BackupVaultID)
			}
		}

		if vol.BackupConfig != nil && vol.BackupConfig.BackupPolicyID != "" {
			if policyName, ok := policiesByUUID[vol.BackupConfig.BackupPolicyID]; ok {
				path := fmt.Sprintf("projects/%s/locations/%s/backupPolicies/%s", accountName, locationId, policyName)
				config.BackupPolicyPath = &path
			} else {
				logger.Error("Backup policy not found for volume", "volumeName", vol.Name, "backupPolicyID", vol.BackupConfig.BackupPolicyID)
			}
		}

		if vol.BackupConfig != nil {
			config.ScheduledBackupEnabled = vol.BackupConfig.ScheduledBackupEnabled
			config.BackupChainBytes = vol.BackupConfig.BackupChainBytes
		}

		backupConfigs = append(backupConfigs, config)
	}

	return backupConfigs, nil
}

// expertModeBackupVaultPath resolves the API resource path for a volume's backup vault.
// Pool-account vaults are resolved from vaultsByUUID; CrossProject (GCBDR) vaults are looked up by UUID.
func expertModeBackupVaultPath(
	ctx context.Context,
	se database.Storage,
	vaultUUID, poolAccountName, locationID string,
	vaultsByUUID map[string]string,
) (string, bool) {
	if vaultName, ok := vaultsByUUID[vaultUUID]; ok {
		return fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", poolAccountName, locationID, vaultName), true
	}

	bv, err := se.GetBackupVault(ctx, vaultUUID)
	if err != nil || bv == nil || bv.ServiceType != datamodel.ServiceTypeCrossProject || bv.Name == "" {
		return "", false
	}

	if bv.Account == nil || bv.Account.Name == "" {
		return "", false
	}
	return fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", bv.Account.Name, locationID, bv.Name), true
}

// RenameExpertModeVolume renames an expert mode volume after validating pool, SVM name, and new name uniqueness; then triggers the update workflow.
func (o *GCPOrchestrator) RenameExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeRenameParams) error {
	return _renameExpertModeVolume(ctx, o.storage, o.temporal, params)
}

// StartExpertModeFlexCloneSplit validates capacity and FlexClone preconditions, marks the volume UPDATING, and starts the split workflow.
func (o *GCPOrchestrator) StartExpertModeFlexCloneSplit(ctx context.Context, params *commonparams.ExpertModeFlexCloneSplitParams) error {
	return _startExpertModeFlexCloneSplit(ctx, o.storage, o.temporal, params)
}

func _renameExpertModeVolume(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ExpertModeVolumeRenameParams) error {
	logger := util.GetLogger(ctx)

	if params.VolumeName == "" || params.NewName == "" {
		return customerrors.NewBadRequestErr("volumeName and new name are required for rename")
	}

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return err
	}

	dbPoolView, err := se.GetPool(ctx, params.PoolUUID, account.ID)
	if err != nil {
		logger.Error("Failed to get pool by UUID", "poolUUID", params.PoolUUID, "error", err)
		return err
	}

	volume, err := se.GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, dbPoolView.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			return customerrors.NewBadRequestErr(fmt.Sprintf("volume with name '%s' not found in pool", params.VolumeName))
		}
		logger.Error("Failed to find volume by name and pool", "volumeName", params.VolumeName, "poolID", dbPoolView.ID, "error", err)
		return err
	}

	if volume.Svm == nil {
		return customerrors.NewBadRequestErr("volume has no SVM and cannot be renamed")
	}
	if volume.Svm.Name != params.SvmName {
		return customerrors.NewBadRequestErr(fmt.Sprintf("SVM name does not match: expected %s", params.SvmName))
	}

	if volume.AccountID != account.ID {
		logger.Error("Volume does not belong to the specified account", "volumeName", params.VolumeName, "volumeAccountID", volume.AccountID, "accountID", account.ID)
		return customerrors.NewBadRequestErr("volume does not belong to the specified account")
	}

	if volume.State == datamodel.LifeCycleStateCreating || volume.State == datamodel.LifeCycleStateDeleting || volume.State == datamodel.LifeCycleStateUpdating {
		return customerrors.NewBadRequestErr(fmt.Sprintf("volume '%s' is in a transitional state and cannot be renamed", params.VolumeName))
	}

	existingWithNewName, err := se.GetExpertModeVolumeByNameAndPoolID(ctx, params.NewName, dbPoolView.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.Error("Failed to check for existing volume with new name", "newName", params.NewName, "poolID", dbPoolView.ID, "error", err)
		return err
	}
	if existingWithNewName != nil && existingWithNewName.UUID != volume.UUID {
		return customerrors.NewBadRequestErr(fmt.Sprintf("volume with name '%s' already exists in pool", params.NewName))
	}

	oldName := volume.Name
	previousState := volume.State
	volume.Name = params.NewName
	volume.State = datamodel.LifeCycleStateUpdating
	volumeMarkedAsUpdating := true

	oldVolume := &datamodel.ExpertModeVolumes{
		BaseModel:    datamodel.BaseModel{UUID: volume.UUID},
		Name:         oldName,
		SizeInBytes:  volume.SizeInBytes,
		Style:        volume.Style,
		State:        previousState,
		ExternalUUID: volume.ExternalUUID,
	}

	_, err = se.UpdateExpertModeVolume(ctx, volume)
	if err != nil {
		logger.Error("Failed to update volume name and state for rename", "volumeUUID", volume.UUID, "error", err)
		return err
	}

	var createdJob *datamodel.Job
	defer func() {
		if err != nil {
			if createdJob != nil {
				if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, err.Error()); jobErr != nil {
					logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
				}
			}
			if volumeMarkedAsUpdating {
				volume.Name = oldName
				volume.State = previousState
				if _, revertErr := se.UpdateExpertModeVolume(ctx, volume); revertErr != nil {
					logger.Error("Failed to revert volume state after rename failure", "volumeUUID", volume.UUID, "error", revertErr)
				}
			}
		}
	}()

	correlationID := utils.GetCoRelationIDFromContext(ctx)
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateExpertModeVolume),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  volume.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: volume.UUID, PoolUUID: volume.Pool.UUID},
		CorrelationID: correlationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job for expert mode volume rename", "error", err)
		return err
	}

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.BackgroundTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetExpertModeSyncWorkflowTimeout(),
		},
		expertModeWorkflows.VolumeUpdateReconciliationWorkflow,
		volume, oldVolume,
	)
	if err != nil {
		logger.Error("Failed to start volume rename reconciliation workflow", "workflowID", createdJob.WorkflowID, "error", err)
		return err
	}

	return nil
}

func _startExpertModeFlexCloneSplit(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ExpertModeFlexCloneSplitParams) error {
	logger := util.GetLogger(ctx)
	if params.PoolUUID == "" || (params.VolumeUUID == "" && params.VolumeName == "") {
		return customerrors.NewBadRequestErr("poolUUID and either volumeUUID or volumeName are required")
	}

	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return err
	}

	dbPoolView, err := se.GetPool(ctx, params.PoolUUID, account.ID)
	if err != nil {
		logger.Error("Failed to get pool for flexclone split", "poolUUID", params.PoolUUID, "error", err)
		return err
	}
	if dbPoolView.APIAccessMode != APIAccessModeONTAP {
		return customerrors.NewBadRequestErr("flexclone split is only supported for ONTAP expert-mode pools")
	}

	var volume *datamodel.ExpertModeVolumes
	if params.VolumeUUID != "" {
		volume, err = se.GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
				return customerrors.NewBadRequestErr(fmt.Sprintf("volume with UUID '%s' not found", params.VolumeUUID))
			}
			logger.Error("Failed to load expert mode volume for flexclone split", "volumeUUID", params.VolumeUUID, "error", err)
			return err
		}
		if params.VolumeName != "" && volume.Name != params.VolumeName {
			return customerrors.NewBadRequestErr("volumeName does not match volumeUUID")
		}
	} else {
		volume, err = se.GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, dbPoolView.ID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
				return customerrors.NewBadRequestErr(fmt.Sprintf("volume with name '%s' not found in pool", params.VolumeName))
			}
			logger.Error("Failed to load expert mode volume by name for flexclone split", "volumeName", params.VolumeName, "poolID", dbPoolView.ID, "error", err)
			return err
		}
	}

	if volume.Pool == nil {
		return customerrors.NewBadRequestErr("volume has no pool association")
	}
	if volume.Pool.UUID != params.PoolUUID {
		return customerrors.NewBadRequestErr("volume is not in the specified pool")
	}
	if volume.AccountID != account.ID {
		return customerrors.NewBadRequestErr("volume does not belong to the specified account")
	}
	if volume.State != datamodel.LifeCycleStateAvailable {
		return customerrors.NewBadRequestErr(fmt.Sprintf("volume must be AVAILABLE to start flexclone split, current state: %s", volume.State))
	}
	if volume.VolumeAttributes == nil || !volume.VolumeAttributes.IsFlexclone {
		return customerrors.NewBadRequestErr("volume is not a FlexClone")
	}

	err = validatePoolCapacityForExpertModeFlexCloneSplit(ctx, se, volume, dbPoolView.SizeInBytes, volume.PoolID)
	if err != nil {
		return err
	}

	previousState := volume.State
	volume.State = datamodel.LifeCycleStateUpdating
	volumeMarkedAsUpdating := true

	updatedVolume, err := se.UpdateExpertModeVolume(ctx, volume)
	if err != nil {
		logger.Error("Failed to mark volume UPDATING for flexclone split", "volumeUUID", volume.UUID, "error", err)
		return err
	}

	var createdJob *datamodel.Job
	defer func() {
		if err != nil {
			if createdJob != nil {
				if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, err.Error()); jobErr != nil {
					logger.Error("Failed to update flexclone split job to ERROR", "jobID", createdJob.UUID, "error", jobErr)
				}
			}
			if volumeMarkedAsUpdating {
				updatedVolume.State = previousState
				if _, revertErr := se.UpdateExpertModeVolume(ctx, updatedVolume); revertErr != nil {
					logger.Error("Failed to revert volume state after flexclone split start failure", "volumeUUID", updatedVolume.UUID, "error", revertErr)
				}
			}
		}
	}()

	correlationID := utils.GetCoRelationIDFromContext(ctx)
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeExpertModeFlexCloneSplit),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  updatedVolume.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: updatedVolume.UUID, PoolUUID: updatedVolume.Pool.UUID},
		CorrelationID: correlationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job for flexclone split", "error", err)
		return err
	}

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.BackgroundTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetExpertModeFlexCloneSplitWorkflowTimeout(),
		},
		expertModeWorkflows.ExpertModeFlexCloneSplitWorkflow,
		updatedVolume,
	)
	if err != nil {
		logger.Error("Failed to start flexclone split workflow", "workflowID", createdJob.WorkflowID, "error", err)
		return err
	}

	volumeMarkedAsUpdating = false
	return nil
}

// getExpertModeVolume resolves a volume by UUID first; if UUID not provided, tries by volumeName.
func getExpertModeVolume(ctx context.Context, se database.Storage, params *commonparams.ExpertModeVolumeParams, dbPoolView *datamodel.PoolView) (*datamodel.ExpertModeVolumes, error) {
	logger := util.GetLogger(ctx)

	if params.VolumeUUID == "" && params.VolumeName == "" {
		return nil, customerrors.NewBadRequestErr("either volumeUUID or (volumeName and poolUUID) is required")
	}

	// 1. Look up by UUID when provided
	if params.VolumeUUID != "" {
		volume, err := se.GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
				return nil, customerrors.NewBadRequestErr(fmt.Sprintf("volume with UUID '%s' not found", params.VolumeUUID))
			}
			logger.Error("Failed to find volume by UUID", "volumeUUID", params.VolumeUUID, "error", err)
			return nil, err
		}
		return volume, nil
	}

	// 2. Try by name
	if dbPoolView != nil {
		volume, err := se.GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, dbPoolView.ID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
				return nil, customerrors.NewBadRequestErr(fmt.Sprintf("volume with name '%s' not found in pool", params.VolumeName))
			}
			logger.Error("Failed to find volume by name and pool", "volumeName", params.VolumeName, "poolID", dbPoolView.ID, "error", err)
			return nil, err
		}
		return volume, nil
	}

	return nil, customerrors.NewBadRequestErr("volume not found")
}
