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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

const (
	InRegionBackupType    = "IN_REGION"
	CrossRegionBackupType = "CROSS_REGION"
)

var (
	convertDatastoreBackupVaultToModel  = _convertDatastoreBackupVaultToModel
	getBackupVaultByNameAndOwnerID      = _getBackupVaultByNameAndOwnerID
	updateBackupVault                   = _updateBackupVault
	deleteBackupVault                   = _deleteBackupVault
	hydrateCreatedBackupVaults          = _hydrateCreatedBackupVaults
	hydrateDeletedBackupVaults          = _hydrateDeletedBackupVaults
	getBackupVaultFromSDEByExternalUUID = _getBackupVaultFromSDEByExternalUUID
	deleteBackupVaultInSDE              = _deleteBackupVaultInSDE
)

// CreateBackupVaultParams describes parameters supplied to CreateBackupVault
type CreateBackupVaultParams struct {
	BackupVaultID              string
	Name                       string
	Description                *string
	Region                     string
	AccountVendorID            string
	BackupRegion               *string
	BackupVaultType            string
	SourceRegion               *string
	BackupRetentionPolicy      *BackupRetentionPolicyV2params
	ExternalUUID               string
	CrossRegionBackupVaultName *string
	ProjectNumber              string
	TenantProject              string
	ServiceType                string
}

// BackupRetentionPolicyV2params describes request parameters for BackupRetentionPolicyV2
type BackupRetentionPolicyV2params struct {
	BackupMinimumEnforcedRetentionDuration *int64
	IsDailyBackupImmutable                 bool
	IsMonthlyBackupImmutable               bool
	IsWeeklyBackupImmutable                bool
	IsAdhocBackupImmutable                 bool
}

func (o *GCPOrchestrator) ListBackupVaults(ctx context.Context, accountName string) ([]*models.BackupVaultV1beta, error) {
	se := o.storage
	account, err := getOrCreateAccount(ctx, se, accountName)
	if err != nil {
		return nil, err
	}

	return ListBackupVaultsByOwnerID(ctx, se, account.ID)
}

func (o *GCPOrchestrator) DeleteBackupVault(ctx context.Context, params *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error) {
	return deleteBackupVault(ctx, o.storage, o.temporal, params)
}

func (o *GCPOrchestrator) DeleteBackupVaultInternal(ctx context.Context, params *commonparams.BackupVaultParams) (string, error) {
	se := o.storage
	logger := util.GetLogger(ctx)

	account, err := se.GetAccount(ctx, params.OwnerID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Infof("Account %s not found, nothing to delete", params.OwnerID)
			return "", nil
		}
		return "", err
	}

	var remoteBv *datamodel.BackupVault
	remoteBv, err = se.GetBackupVaultByExternalUUIDAndOwnerID(ctx, params.BackupVaultID, account.ID)
	if err != nil {
		if !customerrors.IsNotFoundErr(err) {
			return "", err
		}
		// In case where CVP_HOST is not configured, the backup vault has to be deleted from VCP, if it exists. If it doesn't exist, there is nothing to dehydrate from CCFE.
		// In case where ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION is not enabled, the backup vault deletion in SDE and dehydration to CCFE is handled by SDE.
		if !utils.IsCVPHostConfigured() || !env.EnableBackupVaultVCPOrchestration {
			logger.Infof("Backup vault with external uuid %s not found, nothing to delete", params.BackupVaultID)
			return "", nil
		}
	}

	// If ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION is enabled, it could be possible that the backup vault was created
	// before enabling the flag and it is present in SDE. In such cases, VCP needs to delete the backup vault from SDE
	// and dehydrate it from CCFE.
	if env.EnableBackupVaultVCPOrchestration {
		if remoteBv == nil {
			remoteBv, err = getBackupVaultFromSDEByExternalUUID(ctx, params.BackupVaultID, params.Region, params.OwnerID)
			if err != nil {
				logger.Errorf("Failed to resolve backup vault %s from SDE: %v", params.BackupVaultID, err)
				return "", err
			}
			if remoteBv == nil {
				logger.Infof("Backup vault with external uuid %s not found in VCP or SDE, nothing to delete", params.BackupVaultID)
				return "", nil
			}
		}
		if err = deleteBackupVaultInSDE(ctx, params.Region, params.OwnerID, remoteBv.UUID); err != nil {
			logger.Errorf("Failed to delete backup vault %s in SDE: %v", remoteBv.UUID, err)
			return "", err
		}
	}

	// Delete the VCP DB record only when one existed.
	params.BackupVaultID = remoteBv.UUID
	if _, err = se.DeleteBackupVaultInVCP(ctx, remoteBv.UUID); err != nil {
		if !customerrors.IsNotFoundErr(err) {
			logger.Errorf("Failed to delete backup vault %s in VCP: %v", remoteBv.UUID, err)
			return "", err
		}
	}

	if hydrationEnabled && (!utils.IsCVPHostConfigured() || env.EnableBackupVaultVCPOrchestration || remoteBv.ServiceType == datamodel.ServiceTypeCrossProject) {
		err = hydrateDeletedBackupVaults(ctx, remoteBv, params)
		if err != nil {
			logger.Errorf("Failed to hydrate deleted backup vault to CCFE: %v", err)
			return "", err
		}
	}
	return "", nil
}

func _getBackupVaultFromSDEByExternalUUID(ctx context.Context, externalUUID, region, accountName string) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	bv, err := sde.GetSDEBackupVaultByExternalUUID(ctx, region, accountName, externalUUID, logger)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, nil
		}
		return nil, err
	}
	if bv == nil {
		return nil, nil
	}
	bvModel, convErr := activities.ConvertToBackupVaultDataModel(bv, region)
	if convErr != nil {
		return nil, fmt.Errorf("failed to convert backup vault to data model: %w", convErr)
	}
	return bvModel, nil
}

func _deleteBackupVaultInSDE(ctx context.Context, region, accountName, sdeBackupVaultID string) error {
	logger := util.GetLogger(ctx)
	correlationID := utils.GetCoRelationIDFromContext(ctx)
	res, err := sde.DeleteSDEBackupVault(ctx, region, accountName, correlationID, sdeBackupVaultID, logger)
	if err != nil {
		return err
	}
	switch res.(type) {
	case *gcpgenserver.OperationV1beta, *gcpgenserver.V1betaDeleteBackupVaultNotFound:
		return nil
	default:
		return fmt.Errorf("failed to delete backup vault %s in SDE: %v", sdeBackupVaultID, res)
	}
}

func _hydrateDeletedBackupVaults(ctx context.Context, backupVault *datamodel.BackupVault, params *commonparams.BackupVaultParams) error {
	logger := util.GetLogger(ctx)
	if shouldSkipDestinationHydration(backupVault) {
		logger.Infof("Skipping destination backup vault delete hydration to Google for cross-project cross-region backup vault %s", backupVault.Name)
		return nil
	}
	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		return err
	}
	requests := commonparams.ConvertToGCPHydrateBackupVaultDeleteRequests([]*datamodel.BackupVault{backupVault})
	err = commonparams.HydrateDeletedBackupVaults(ctx, logger, requests, backupVault.Name, *backupVault.BackupRegionName, params.OwnerID, token)
	if err != nil {
		return err
	}
	return nil
}

func _deleteBackupVault(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error) {
	logger := util.GetLogger(ctx)
	account, err := getOrCreateAccount(ctx, se, params.OwnerID)
	if err != nil {
		return nil, "", err
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteBackupVault),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: params.BackupVaultID,
		},
	}

	dbBv, err := se.GetBackupVaultByUUIDndOwnerID(ctx, params.BackupVaultID, account.ID)
	if err != nil {
		if !customerrors.IsNotFoundErr(err) {
			return nil, "", err
		}
	}

	// Store original state for rollback
	originalState := datamodel.LifeCycleStateREADY
	originalStateDetails := datamodel.LifeCycleStateAvailableDetails
	stateUpdated := false

	// In cases where ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION is enabled, VCP is responsible for the backup vault lifecycle operations.
	// VCP is responsible for backup vault hydration to the destination regions and to CCFE in case of cross-region backup vaults.
	// When the flag is enabled, SDE cannot make use of Pub/Sub to communicate with the destination region.
	// For existing SDE backup vaults, it could be possible that we don't have the metadata in VCP.
	// In such cases, we construct a minimal backup vault record with the necessary details before launching the workflow.
	if dbBv != nil {
		if dbBv.LifeCycleState == datamodel.LifeCycleStateUpdating || dbBv.LifeCycleState == datamodel.LifeCycleStateDeleting {
			return nil, "", customerrors.NewUserInputValidationErr("backup vault is in transition state")
		}

		if dbBv.BackupVaultType == activities.CrossRegionBackupType && params.Region == *dbBv.BackupRegionName {
			return nil, "", customerrors.NewUserInputValidationErr("backup vault cannot be deleted from the destination region")
		}

		backups, err := se.GetBackupCountByBackupVaultID(ctx, dbBv.ID)
		if backups > 0 {
			return nil, "", customerrors.NewUserInputValidationErr("backup vault has backups, please delete backups before deleting backup vault")
		}
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, "", customerrors.NewNotFoundErr("backup vault", &params.BackupVaultID)
			}
			return nil, "", err
		}

		volumes, err := se.GetVolumeCountByBackupVaultID(ctx, dbBv.UUID)
		if volumes > 0 {
			return nil, "", customerrors.NewUserInputValidationErr("backup vault has volumes attached, please delete volumes before deleting backup vault")
		}
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, "", customerrors.NewNotFoundErr("backup vault", &params.BackupVaultID)
			}
			return nil, "", err
		}

		// Update the job attributes with the previous state and details
		job.JobAttributes.PreviousState = dbBv.LifeCycleState
		job.JobAttributes.PreviousStateDetails = dbBv.LifeCycleStateDetails

		// Update the original state and details for rollback
		originalState = dbBv.LifeCycleState
		originalStateDetails = dbBv.LifeCycleStateDetails
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer function to handle rollback on workflow startup failure only
	defer func() {
		if err != nil {
			// This condition is met: err != nil (workflow start failed)
			// && stateUpdated (true, since state was successfully updated)

			// Rollback the backup vault state to original state
			if stateUpdated {
				if _, rollbackErr := se.UpdateBackupVaultState(ctx, dbBv, originalState, originalStateDetails); rollbackErr != nil {
					logger.Error("Failed to rollback backup vault state", "error", rollbackErr, "originalState", originalState)
				}
			}

			// Mark the job as ERROR
			if createdJob != nil {
				if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
					logger.Error("Failed to update job state to ERROR", "error", jobErr, "jobUUID", createdJob.UUID)
				}
			}
		}
	}()

	if dbBv != nil {
		// Update the backup vault state to deleting if the backup vault is present in VCP
		dbBv, err = se.UpdateBackupVaultState(ctx, dbBv, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails)
		if err != nil {
			return nil, "", err
		}
		stateUpdated = true
	}

	if dbBv == nil {
		// Construct a minimal backup vault record with the necessary details if the backup vault is not present in VCP.
		// This case can occur when deleting SDE backup vaults with ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION flag enabled.
		dbBv = &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: params.BackupVaultID},
			AccountID: account.ID,
			Account:   account,
			Name:      params.Name,
		}
	}

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.DeleteBackupVaultWorkflow,
		nil,
		params,
		dbBv,
	)
	if err != nil {
		logger.Error("Failed to start backup vault delete workflow after retries: ", "error", err)
		return nil, "", err
	}
	return convertDatastoreBackupVaultToModel(dbBv), createdJob.UUID, nil
}

func (o *GCPOrchestrator) UpdateBackupVault(ctx context.Context, params *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error) {
	return updateBackupVault(ctx, o.storage, o.temporal, params)
}

func _updateBackupVault(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error) {
	logger := util.GetLogger(ctx)
	account, err := getOrCreateAccount(ctx, se, params.OwnerID)
	if err != nil {
		return nil, "", err
	}

	dbBv, err := se.GetBackupVaultByUUIDndOwnerID(ctx, params.BackupVaultID, account.ID)
	if err != nil {
		return nil, "", err
	}

	if dbBv.LifeCycleState == datamodel.LifeCycleStateUpdating || dbBv.LifeCycleState == datamodel.LifeCycleStateDeleting {
		return nil, "", customerrors.NewUserInputValidationErr("backup vault is in transition state")
	}

	if dbBv.BackupVaultType == activities.CrossRegionBackupType && params.Region == *dbBv.BackupRegionName {
		return nil, "", customerrors.NewUserInputValidationErr("cross-region backup vault cannot be updated from the destination region")
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateBackupVault),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         params.BackupVaultID,
			PreviousState:        dbBv.LifeCycleState,
			PreviousStateDetails: dbBv.LifeCycleStateDetails,
		},
	}

	// Store original state for rollback
	originalState := dbBv.LifeCycleState
	originalStateDetails := dbBv.LifeCycleStateDetails
	stateUpdated := false

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer function to handle rollback on workflow startup failure only
	defer func() {
		if err != nil {
			// Only rollback if the state was successfully updated but workflow failed to start
			// The workflow will handle its own error states
			if stateUpdated {
				if _, rollbackErr := se.UpdateBackupVaultState(ctx, dbBv, originalState, originalStateDetails); rollbackErr != nil {
					logger.Error("Failed to rollback backup vault state", "error", rollbackErr, "originalState", originalState)
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

	dbBV, err := se.UpdateBackupVaultState(ctx, dbBv, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails)
	if err != nil {
		return nil, "", err
	}
	stateUpdated = true

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.UpdateBackupVaultWorkflow,
		nil,
		params,
		dbBV,
	)
	if err != nil {
		logger.Error("Failed to start backup vault update workflow after retries: ", "error", err)
		return nil, "", err
	}
	return convertDatastoreBackupVaultToModel(dbBV), createdJob.UUID, nil
}

func (o *GCPOrchestrator) GetBackupVaultByUUID(ctx context.Context, bvUUID string, ownerID string) (*models.BackupVaultV1beta, error) {
	se := o.storage
	account, err := getOrCreateAccount(ctx, se, ownerID)
	if err != nil {
		return nil, err
	}
	return GetBackupVaultByUUIDAndOwnerID(ctx, se, bvUUID, account.ID)
}

// GetBackupVaultByUUIDWithoutAccount gets backup vault by UUID without account filtering (for GCBDR vaults)
func (o *GCPOrchestrator) GetBackupVaultByUUIDWithoutAccount(ctx context.Context, bvUUID string) (*models.BackupVaultV1beta, error) {
	se := o.storage
	bvDetails, err := se.GetBackupVault(ctx, bvUUID)
	if err != nil {
		return nil, err
	}
	return convertDatastoreBackupVaultToModel(bvDetails), nil
}

func GetBackupVaultByUUIDAndOwnerID(ctx context.Context, se database.Storage, bvUUID string, accountID int64) (*models.BackupVaultV1beta, error) {
	bvDetails, err := se.GetBackupVaultByUUIDndOwnerID(ctx, bvUUID, accountID)
	if err != nil {
		return nil, err
	}

	return convertDatastoreBackupVaultToModel(bvDetails), nil
}

func ListBackupVaultsByOwnerID(ctx context.Context, se database.Storage, ownerID int64) ([]*models.BackupVaultV1beta, error) {
	bvDetails, err := se.ListBackupVaults(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	var backupVaults []*models.BackupVaultV1beta
	for _, bv := range bvDetails {
		backupVaults = append(backupVaults, convertDatastoreBackupVaultToModel(bv))
	}
	return backupVaults, nil
}

func (o *GCPOrchestrator) GetBackupVaultByNameAndOwnerID(ctx context.Context, bvName, ownerID string) (*models.BackupVaultV1beta, error) {
	se := o.storage
	bvDetails, err := getBackupVaultByNameAndOwnerID(ctx, se, bvName, ownerID)
	if err != nil {
		return nil, err
	}
	return convertDatastoreBackupVaultToModel(bvDetails), nil
}

func _getBackupVaultByNameAndOwnerID(ctx context.Context, se database.Storage, bvName, ownerID string) (*datamodel.BackupVault, error) {
	account, err := getOrCreateAccount(ctx, se, ownerID)
	if err != nil {
		return nil, err
	}

	bv, err := se.GetBackupVaultByNameAndOwnerID(ctx, bvName, strconv.FormatInt(account.ID, 10))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("backup vault", &bvName)
		}
		return nil, err
	}

	return bv, nil
}

func _convertDatastoreBackupVaultToModel(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
	res := &models.BackupVaultV1beta{
		ID:                         bv.ID,
		OwnerID:                    bv.Account.UUID,
		BackupVaultID:              bv.UUID,
		Name:                       bv.Name,
		Description:                bv.Description,
		LifeCycleState:             bv.LifeCycleState,
		LifeCycleStateDetails:      bv.LifeCycleStateDetails,
		CreatedAt:                  bv.CreatedAt,
		UpdatedAt:                  bv.UpdatedAt,
		BackupRegion:               bv.BackupRegionName,
		SourceRegion:               bv.SourceRegionName,
		Region:                     bv.RegionName,
		AccountVendorID:            bv.AccountVendorID,
		SourceBackupVault:          &bv.Name,
		DestinationBackupVault:     bv.CrossRegionBackupVaultName,
		BackupVaultType:            &bv.BackupVaultType,
		CrossRegionBackupVaultName: bv.CrossRegionBackupVaultName,
		ExternalUUID:               bv.ExternalUUID,
		AccountName:                bv.Account.Name,
		ServiceType:                bv.ServiceType,
	}
	if bv.ImmutableAttributes != nil {
		res.BackupRetentionPolicy = models.BackupRetentionPolicyparams{
			BackupMinimumEnforcedRetentionDuration: bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration,
			IsDailyBackupImmutable:                 bv.ImmutableAttributes.IsDailyBackupImmutable,
			IsMonthlyBackupImmutable:               bv.ImmutableAttributes.IsMonthlyBackupImmutable,
			IsWeeklyBackupImmutable:                bv.ImmutableAttributes.IsWeeklyBackupImmutable,
			IsAdhocBackupImmutable:                 bv.ImmutableAttributes.IsAdhocBackupImmutable,
		}
	}
	if bv.CmekAttributes != nil {
		res.KmsConfigResourcePath = bv.CmekAttributes.KmsConfigResourcePath
		res.EncryptionState = bv.CmekAttributes.EncryptionState
		res.BackupsPrimaryKeyVersion = bv.CmekAttributes.BackupsPrimaryKeyVersion
	}
	if bv.ServiceType == datamodel.ServiceTypeCrossProject && len(bv.BucketDetails) > 0 &&
		bv.BucketDetails[0] != nil && bv.BucketDetails[0].TenantProjectNumber != "" {
		res.TenantProject = nillable.GetStringPtr(bv.BucketDetails[0].TenantProjectNumber)
	}
	if bv.BackupVaultType == CrossRegionBackupType && bv.CrossRegionBackupVaultName != nil {
		// Extract cross-region backup vault region from its name to determine whether it is the source or destination vault
		// Source region stores cross-region backup vault name of the destination vault, and destination region stores cross-region backup vault name of the source vault
		// Cross-region backup vault name format: projects/{project}/locations/{location}/backupVaults/{backupVault}
		components := strings.Split(*bv.CrossRegionBackupVaultName, "/")
		if len(components) == 6 {
			region := components[3]
			if res.SourceRegion != nil && res.BackupRegion != nil && region == *res.BackupRegion {
				// If cross-region backup vault region is the same as backup region, then the current backup vault is the source vault and the cross-region backup vault is the destination vault
				res.SourceBackupVault = nillable.GetStringPtr(
					fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", bv.Account.Name, *bv.SourceRegionName, bv.Name))
				res.DestinationBackupVault = bv.CrossRegionBackupVaultName
			} else if res.SourceRegion != nil && res.BackupRegion != nil && region == *res.SourceRegion {
				// If cross-region backup vault region is different from backup region, then the current backup vault is the destination vault and the cross-region backup vault is the source vault
				res.SourceBackupVault = bv.CrossRegionBackupVaultName
				res.DestinationBackupVault = nillable.GetStringPtr(
					fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", bv.Account.Name, *bv.BackupRegionName, bv.Name))
			}
		}
	}
	return res
}

// GetMultipleBackupVaults gets BackupVault records for the UUIDs provided
func (o *GCPOrchestrator) GetMultipleBackupVaults(ctx context.Context, backupVaultUUIDList []string) ([]*models.BackupVaultV1beta, error) {
	se := o.storage

	conditions := [][]interface{}{{"uuid in ?", backupVaultUUIDList}}
	backupVaultList, err := se.GetMultipleBackupVaults(ctx, conditions)
	if err != nil {
		return nil, err
	}
	backupVaultModelList := make([]*models.BackupVaultV1beta, 0, len(backupVaultList))
	for _, backupVault := range backupVaultList {
		backupVaultModelList = append(backupVaultModelList, convertDatastoreBackupVaultToModel(backupVault))
	}

	return backupVaultModelList, nil
}

// IsBackupVaultAttachedToVolume checks if a backup vault is attached to any volume in the VCP database
func (o *GCPOrchestrator) IsBackupVaultAttachedToVolume(ctx context.Context, backupVaultUUID string) (bool, error) {
	se := o.storage

	volumeCount, err := se.GetVolumeCountByBackupVaultID(ctx, backupVaultUUID)
	if err != nil {
		return false, err
	}

	return volumeCount > 0, nil
}

// GetBackupVaultUUIDsFromBackupPolicyUUID retrieves all backup vault UUIDs associated with a backup policy
func (o *GCPOrchestrator) GetBackupVaultUUIDsFromBackupPolicyUUID(ctx context.Context, backupPolicyUUID string, accountName string) ([]string, error) {
	se := o.storage
	account, err := se.GetAccount(ctx, accountName)
	if err != nil {
		return nil, err
	}
	backupVaultUUIDs, err := se.GetBackupVaultUUIDsFromBackupPolicyUUID(ctx, backupPolicyUUID, account.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("backup vaults for backup policy", &backupPolicyUUID)
		}
		return nil, err
	}
	return backupVaultUUIDs, nil
}

// CreateBackupVaultEntryInVCP creates a BackupVault entry directly in the VCP database for cross-region operations
func (o *GCPOrchestrator) CreateBackupVaultEntryInVCP(ctx context.Context, bv *datamodel.BackupVault, params *commonparams.BackupVaultParams) (*datamodel.BackupVault, error) {
	se := o.storage
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, err
	}
	bv.AccountID = account.ID

	var createdBv *datamodel.BackupVault
	defer func() {
		if err != nil {
			// When VCP orchestration is enabled, the SDE vault will be created by VCP.
			// Roll it back to prevent a dangling vault in SDE if the VCP create or hydration step fails.
			if env.EnableBackupVaultVCPOrchestration && bv.UUID != "" {
				if sdeErr := deleteBackupVaultInSDE(ctx, params.Region, params.OwnerID, bv.UUID); sdeErr != nil {
					logger.Errorf("Failed to rollback backup vault creation in SDE for backup vault UUID %s. Error: %v", bv.UUID, sdeErr)
				} else {
					logger.Infof("Successfully rolled back backup vault creation in SDE for backup vault UUID %s", bv.UUID)
				}
			}
			if createdBv != nil {
				_, deleteErr := se.DeleteBackupVaultInVCP(ctx, createdBv.UUID)
				if deleteErr != nil {
					logger.Errorf("Failed to rollback backup vault creation in VCP for backup vault UUID %s. Error: %v", createdBv.UUID, deleteErr)
				} else {
					logger.Infof("Successfully rolled back backup vault creation in VCP for backup vault UUID %s", createdBv.UUID)
				}
			}
		}
	}()

	createdBv, err = se.CreateBackupVaultEntryInVCP(ctx, bv)
	if err != nil {
		logger.Errorf("Failed to create cross-region backup vault entry in VCP: %v", err)
		return nil, err
	}

	if hydrationEnabled && (!utils.IsCVPHostConfigured() || createdBv.ServiceType == datamodel.ServiceTypeCrossProject || env.EnableBackupVaultVCPOrchestration) {
		err = hydrateCreatedBackupVaults(ctx, createdBv, params)
		if err != nil {
			logger.Errorf("Failed to hydrate created backup vault to CCFE: %v", err)
			return nil, err
		}
	}
	return createdBv, nil
}

// shouldSkipDestinationHydration reports whether hydration to Google should be
// skipped for the destination backup vault. The destination backup vault of a
// cross-project cross-region backup vault is never hydrated to Google, so both
// the create and delete hydration paths skip it when the vault is a cross-project
// cross-region vault.
func shouldSkipDestinationHydration(backupVault *datamodel.BackupVault) bool {
	if backupVault == nil {
		return false
	}
	return backupVault.ServiceType == datamodel.ServiceTypeCrossProject &&
		backupVault.BackupVaultType == CrossRegionBackupType
}

func _hydrateCreatedBackupVaults(ctx context.Context, backupVault *datamodel.BackupVault, params *commonparams.BackupVaultParams) error {
	logger := util.GetLogger(ctx)
	if shouldSkipDestinationHydration(backupVault) {
		logger.Infof("Skipping destination backup vault hydration to Google for cross-project cross-region backup vault %s", backupVault.Name)
		return nil
	}
	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		return err
	}
	requests := commonparams.ConvertToGCPHydrateBackupVaultCreateRequests([]*datamodel.BackupVault{backupVault})
	err = commonparams.HydrateCreatedBackupVaults(ctx, logger, requests, backupVault.Name, *backupVault.BackupRegionName, params.OwnerID, token)
	if err != nil {
		return err
	}
	return nil
}

// CreateBackupVault creates a new BackupVault entry in the VCP database and, for cross-region vaults,
// hydrates the metadata to the destination region if backup vault VCP orchestration is enabled.
func (o *GCPOrchestrator) CreateBackupVault(ctx context.Context, params *commonparams.CreateBackupVaultParams) (*models.BackupVaultV1beta, error) {
	se := o.storage
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.ProjectNumber)
	if err != nil {
		return nil, err
	}

	var createdBv *datamodel.BackupVault
	var createdJob *datamodel.Job
	defer func() {
		if err != nil {
			logger.Errorf("Failed to create backup vault entry in VCP, rolling back. Error: %v", err)
			// If a UUID was pre-assigned, the SDE vault was already created with it before this function was called.
			// Attempt a best-effort delete so the UUID does not leak a dangling vault in SDE.
			if params.BackupVaultID != "" {
				if sdeErr := deleteBackupVaultInSDE(ctx, params.LocationId, params.ProjectNumber, params.BackupVaultID); sdeErr != nil {
					logger.Errorf("Failed to roll back SDE backup vault %s after VCP create failure: %v", params.BackupVaultID, sdeErr)
				} else {
					logger.Infof("Rolled back SDE backup vault %s after VCP create failure", params.BackupVaultID)
				}
			}
			if createdBv != nil {
				if _, deleteErr := se.DeleteBackupVaultInVCP(ctx, createdBv.UUID); deleteErr != nil {
					logger.Errorf("Failed to rollback backup vault creation in VCP for backup vault UUID %s. Error: %v", createdBv.UUID, deleteErr)
				} else {
					logger.Infof("Successfully rolled back backup vault creation in VCP for backup vault UUID %s", createdBv.UUID)
				}
			}
			if createdJob != nil {
				if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
					logger.Errorf("Failed to update job state to ERROR for job %s. Error: %v", createdJob.UUID, jobErr)
				}
			}
		}
	}()

	bv := buildBackupVaultFromCreateParams(params, account)
	createdBv, err = se.CreateBackupVaultEntryInVCP(ctx, bv)
	if err != nil {
		return nil, err
	}

	if createdBv.BackupVaultType != CrossRegionBackupType {
		return convertDatastoreBackupVaultToModel(createdBv), nil
	}

	// hydrate the destination region backup vault asynchronously via CreateBackupVaultWorkflow.
	bvParams := &commonparams.BackupVaultParams{
		BackupVaultID: createdBv.UUID,
		AccountName:   params.ProjectNumber,
		OwnerID:       params.ProjectNumber,
		Region:        params.LocationId,
		Name:          createdBv.Name,
		BackupRegion:  createdBv.BackupRegionName,
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateBackupVault),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  createdBv.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: createdBv.UUID,
		},
	}
	createdJob, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, err
	}

	workflowExecutor := workflows.NewWorkflowExecutor(o.temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.CreateBackupVaultWorkflow,
		nil,
		bvParams,
		createdBv,
	)
	if err != nil {
		logger.Error("Failed to start backup vault create workflow after retries: ", "error", err)
		return nil, err
	}

	return convertDatastoreBackupVaultToModel(createdBv), nil
}

func buildBackupVaultFromCreateParams(params *commonparams.CreateBackupVaultParams, account *datamodel.Account) *datamodel.BackupVault {
	var backupVaultType string
	if params.BackupRegion != nil && *params.BackupRegion != params.LocationId {
		backupVaultType = CrossRegionBackupType
	} else {
		backupVaultType = InRegionBackupType
	}
	backupVaultUUID := params.BackupVaultID
	if backupVaultUUID == "" {
		backupVaultUUID = utils.RandomUUID()
	}
	bv := &datamodel.BackupVault{
		BaseModel:             datamodel.BaseModel{UUID: backupVaultUUID},
		Name:                  params.ResourceId,
		AccountID:             account.ID,
		Account:               account,
		RegionName:            params.LocationId,
		BackupRegionName:      params.BackupRegion,
		LifeCycleState:        datamodel.LifeCycleStateREADY,
		LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		BackupVaultType:       backupVaultType,
		SourceRegionName:      nillable.ToPointer(params.LocationId),
	}
	if params.Description != "" {
		bv.Description = &params.Description
	}
	if params.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil ||
		params.BackupRetentionPolicy.IsDailyBackupImmutable != nil ||
		params.BackupRetentionPolicy.IsWeeklyBackupImmutable != nil ||
		params.BackupRetentionPolicy.IsMonthlyBackupImmutable != nil ||
		params.BackupRetentionPolicy.IsAdhocBackupImmutable != nil {
		bv.ImmutableAttributes = &datamodel.ImmutableAttributes{}
		if params.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil {
			bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration = params.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration
		}
		if params.BackupRetentionPolicy.IsDailyBackupImmutable != nil {
			bv.ImmutableAttributes.IsDailyBackupImmutable = *params.BackupRetentionPolicy.IsDailyBackupImmutable
		}
		if params.BackupRetentionPolicy.IsWeeklyBackupImmutable != nil {
			bv.ImmutableAttributes.IsWeeklyBackupImmutable = *params.BackupRetentionPolicy.IsWeeklyBackupImmutable
		}
		if params.BackupRetentionPolicy.IsMonthlyBackupImmutable != nil {
			bv.ImmutableAttributes.IsMonthlyBackupImmutable = *params.BackupRetentionPolicy.IsMonthlyBackupImmutable
		}
		if params.BackupRetentionPolicy.IsAdhocBackupImmutable != nil {
			bv.ImmutableAttributes.IsAdhocBackupImmutable = *params.BackupRetentionPolicy.IsAdhocBackupImmutable
		}
	}
	if params.KmsConfigResourcePath != nil || params.BackupsPrimaryKeyVersion != nil {
		bv.CmekAttributes = &datamodel.CmekAttributes{}
		if params.KmsConfigResourcePath != nil {
			bv.CmekAttributes.KmsConfigResourcePath = params.KmsConfigResourcePath
		}
		if params.BackupsPrimaryKeyVersion != nil {
			bv.CmekAttributes.BackupsPrimaryKeyVersion = params.BackupsPrimaryKeyVersion
		}
		bv.CmekAttributes.EncryptionState = nillable.GetStringPtr(datamodel.EncryptionStateCompleted)
	}
	if backupVaultType == CrossRegionBackupType {
		bv.CrossRegionBackupVaultName = nillable.GetStringPtr(
			fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s",
				params.ProjectNumber, *params.BackupRegion, utils.ConvertSourceBackupVaultNameToRemoteBackupVaultName(bv.Name, bv.UUID)))
	}
	if params.TenantProject != nil {
		bv.ServiceType = datamodel.ServiceTypeCrossProject
		bv.BucketDetails = datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{
				TenantProjectNumber: *params.TenantProject,
			},
		}
	} else {
		bv.ServiceType = datamodel.ServiceTypeGCNV
	}
	return bv
}

// GetBackupVaultByExternalUUIDAndOwnerID gets a BackupVault by external UUID directly from storage for cross-region operations
// The external UUID is used as the identifier for cross-region BackupVault references
func (o *GCPOrchestrator) GetBackupVaultByExternalUUIDAndOwnerID(ctx context.Context, externalUUID string, ownerID string) (*datamodel.BackupVault, error) {
	se := o.storage
	account, err := getOrCreateAccount(ctx, se, ownerID)
	if err != nil {
		return nil, err
	}
	return se.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, account.ID)
}

// UpdateBackupVaultInternal handles internal updates to a BackupVault in the VCP database
// This is used for cross-region operations where the remote region's VCP database needs to be updated
func (o *GCPOrchestrator) UpdateBackupVaultInternal(ctx context.Context, params *commonparams.BackupVaultParams, useExternalUUID bool) (*models.BackupVaultV1beta, string, error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	account, err := getOrCreateAccount(ctx, se, params.OwnerID)
	if err != nil {
		return nil, "", err
	}
	var existingBV *datamodel.BackupVault
	if useExternalUUID {
		existingBV, err = se.GetBackupVaultByExternalUUIDAndOwnerID(ctx, params.BackupVaultID, account.ID)
		if err != nil {
			return nil, "", err
		}
	} else {
		existingBV, err = se.GetBackupVaultByUUIDndOwnerID(ctx, params.BackupVaultID, account.ID)
		if err != nil {
			return nil, "", err
		}
	}
	updatedBV := &datamodel.BackupVault{
		BaseModel: existingBV.BaseModel,
		AccountID: existingBV.AccountID,
	}

	if params.Description != nil {
		updatedBV.Description = params.Description
	} else {
		updatedBV.Description = existingBV.Description
	}

	brp := params.BackupRetentionPolicy
	if brp.BackupMinimumEnforcedRetentionDuration != nil ||
		brp.IsDailyBackupImmutable != nil ||
		brp.IsWeeklyBackupImmutable != nil ||
		brp.IsMonthlyBackupImmutable != nil ||
		brp.IsAdhocBackupImmutable != nil {
		if existingBV.ImmutableAttributes != nil {
			updatedBV.ImmutableAttributes = &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: existingBV.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration,
				IsDailyBackupImmutable:                 existingBV.ImmutableAttributes.IsDailyBackupImmutable,
				IsWeeklyBackupImmutable:                existingBV.ImmutableAttributes.IsWeeklyBackupImmutable,
				IsMonthlyBackupImmutable:               existingBV.ImmutableAttributes.IsMonthlyBackupImmutable,
				IsAdhocBackupImmutable:                 existingBV.ImmutableAttributes.IsAdhocBackupImmutable,
			}
		} else {
			updatedBV.ImmutableAttributes = &datamodel.ImmutableAttributes{}
		}

		if brp.BackupMinimumEnforcedRetentionDuration != nil {
			updatedBV.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration = brp.BackupMinimumEnforcedRetentionDuration
		}
		if brp.IsDailyBackupImmutable != nil {
			updatedBV.ImmutableAttributes.IsDailyBackupImmutable = *brp.IsDailyBackupImmutable
		}
		if brp.IsWeeklyBackupImmutable != nil {
			updatedBV.ImmutableAttributes.IsWeeklyBackupImmutable = *brp.IsWeeklyBackupImmutable
		}
		if brp.IsMonthlyBackupImmutable != nil {
			updatedBV.ImmutableAttributes.IsMonthlyBackupImmutable = *brp.IsMonthlyBackupImmutable
		}
		if brp.IsAdhocBackupImmutable != nil {
			updatedBV.ImmutableAttributes.IsAdhocBackupImmutable = *brp.IsAdhocBackupImmutable
		}
	} else {
		updatedBV.ImmutableAttributes = existingBV.ImmutableAttributes
	}

	// CMEK attributes: start from existing attributes and overlay any CMEK fields
	// provided in the internal request (used by cross-region CMEK hydration).
	if existingBV.CmekAttributes != nil {
		updatedBV.CmekAttributes = &datamodel.CmekAttributes{
			KmsConfigResourcePath:    existingBV.CmekAttributes.KmsConfigResourcePath,
			EncryptionState:          existingBV.CmekAttributes.EncryptionState,
			BackupsPrimaryKeyVersion: existingBV.CmekAttributes.BackupsPrimaryKeyVersion,
		}
	}
	if params.CmekEncryptionState != nil || params.CmekBackupsPrimaryKeyVersion != nil {
		if updatedBV.CmekAttributes == nil {
			updatedBV.CmekAttributes = &datamodel.CmekAttributes{}
		}
		if params.CmekEncryptionState != nil {
			updatedBV.CmekAttributes.EncryptionState = params.CmekEncryptionState
		}
		if params.CmekBackupsPrimaryKeyVersion != nil {
			updatedBV.CmekAttributes.BackupsPrimaryKeyVersion = params.CmekBackupsPrimaryKeyVersion
		}
	}

	if params.BucketDetails != nil && len(params.BucketDetails) > 0 {
		updatedBV.BucketDetails = params.BucketDetails
	} else {
		updatedBV.BucketDetails = existingBV.BucketDetails
	}

	updatedBV.LifeCycleState = existingBV.LifeCycleState
	updatedBV.LifeCycleStateDetails = existingBV.LifeCycleStateDetails
	// Only manipulate ExternalUUID when we are in the "external UUID lookup"
	// mode (typical CRB flows). For CMEK hydration (useExternalUUID == false),
	// we must not overwrite the source vault's ExternalUUID.
	if useExternalUUID {
		updatedBV.ExternalUUID = &params.BackupVaultID
	} else {
		updatedBV.ExternalUUID = existingBV.ExternalUUID
	}

	resultBV, err := se.UpdateBackupVaultInVCP(ctx, updatedBV, existingBV)
	if err != nil {
		logger.Error("Failed to update backup vault in VCP database", "error", err, "backupVaultId", params.BackupVaultID)
		return nil, "", err
	}
	err = se.UpdateBackupVaultBucketDetails(ctx, updatedBV)
	if err != nil {
		logger.Warnf("Failed to update backup vault bucket details for %s in VCP database. Error: %v",
			params.BackupVaultID, err.Error())
	}

	logger.Info("Successfully updated backup vault in VCP database",
		"backupVaultId", params.BackupVaultID,
		"ownerID", params.OwnerID)

	return convertDatastoreBackupVaultToModel(resultBV), "", nil
}

// RotateCmekBackupsForBackupVault creates a job and starts a Temporal workflow to
// rotate CMEK for all backups within a backup vault tracked by VCP.
func (o *GCPOrchestrator) RotateCmekBackupsForBackupVault(
	ctx context.Context,
	params *commonparams.BackupVaultParams,
	primaryKeyVersion string,
) (string, error) {
	logger := util.GetLogger(ctx)
	se := o.storage

	account, err := getOrCreateAccount(ctx, se, params.OwnerID)
	if err != nil {
		return "", err
	}

	dbBv, err := se.GetBackupVaultByUUIDndOwnerID(ctx, params.BackupVaultID, account.ID)
	if err != nil {
		return "", err
	}

	// Reject rotation if backup vault is in a transitional state.
	if dbBv.LifeCycleState == datamodel.LifeCycleStateUpdating || dbBv.LifeCycleState == datamodel.LifeCycleStateDeleting {
		return "", customerrors.NewUserInputValidationErr("backup vault is in transition state")
	}

	// Reject rotation if the backup vault is not CMEK-configured. VCP's CMEK
	if dbBv.CmekAttributes == nil || dbBv.CmekAttributes.KmsConfigResourcePath == nil || *dbBv.CmekAttributes.KmsConfigResourcePath == "" {
		return "", customerrors.NewUserInputValidationErr("cmek backup rotation can not be called for backup vault without CMEK configuration")
	}

	// For cross-region backup vaults, only allow CMEK rotation from the destination
	if dbBv.BackupVaultType == activities.CrossRegionBackupType {
		if dbBv.SourceRegionName != nil && params.Region == *dbBv.SourceRegionName {
			return "", customerrors.NewUserInputValidationErr("cmek backup rotation can not be called for cross region source backup vault")
		}
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeRotateCmekBackups),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  dbBv.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbBv.UUID,
			Location:     params.Region,
			KmsAttributes: &datamodel.JobKmsAttributes{
				NewKmsKeyURL:      primaryKeyVersion,
				AccountIdentifier: account.Name,
			},
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create CMEK rotation job in database", "error", err)
		return "", err
	}

	// On workflow start failure, mark job as ERROR.
	defer func() {
		if err != nil && createdJob != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update CMEK rotation job state to ERROR", "error", jobErr, "jobUUID", createdJob.UUID)
			}
		}
	}()

	workflowExecutor := workflows.NewWorkflowExecutor(o.temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.RotateCmekBackupsWorkflow,
		workflowengine.GetCreateBackupWorkflowTimeout(),
		params,
		dbBv,
		primaryKeyVersion,
	)
	if err != nil {
		logger.Error("Failed to start CMEK rotation workflow after retries", "error", err)
		return "", err
	}

	return createdJob.UUID, nil
}
