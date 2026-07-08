package active_directory_activities

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/active_directories"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type ActiveDirectoryDeleteActivity struct {
	SE        database.Storage
	Scheduler *scheduler.TemporalScheduler
}

// CheckDeletionAllowedResult holds the result of CheckDeletionAllowed activity
type CheckDeletionAllowedResult struct {
	ADExists        bool
	DeletionAllowed bool
}

// ListActiveDirectoriesActivity lists all Active Directories for the given project/account.
func (a ActiveDirectoryDeleteActivity) ListActiveDirectoriesActivity(ctx context.Context, projectNumber string) ([]*datamodel.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)
	logger.Debug("Starting ListActiveDirectoriesActivity", "projectNumber", projectNumber)

	se := a.SE
	account, err := se.GetAccount(ctx, projectNumber)
	if err != nil {
		logger.Errorf("Failed to get account for project %s: %v", projectNumber, err)
		return nil, err
	}

	ads, err := se.ListActiveDirectories(ctx, account.ID)
	if err != nil {
		logger.Errorf("Failed to list active directories for account %d: %v", account.ID, err)
		return nil, err
	}

	return ads, nil
}

// CheckDeletionAllowed checks if Active Directory can be deleted
func (a ActiveDirectoryDeleteActivity) CheckDeletionAllowed(ctx context.Context, params *common.DeleteActiveDirectoryParams) (*CheckDeletionAllowedResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debug("Starting CheckDeletionAllowed activity", "active_directory_uuid", params.ActiveDirectoryUUID)

	// Check if the Active Directory exists in the database for this account
	ad, err := a.SE.GetActiveDirectoryByUuidAndAccountId(ctx, params.ActiveDirectoryUUID, params.AccountId)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Info("Active Directory not found in VCP database", "active_directory_uuid", params.ActiveDirectoryUUID)
			return &CheckDeletionAllowedResult{
				ADExists:        false,
				DeletionAllowed: true,
			}, nil
		}
		logger.Error("Failed to get Active Directory from database", "error", err, "active_directory_uuid", params.ActiveDirectoryUUID)
		return nil, err
	}

	// Check if any SVMs are using this Active Directory
	svms, err := a.SE.GetSVMsUsingActiveDirectory(ctx, ad.ID)
	if err != nil {
		logger.Error("Failed to check SVMs using Active Directory", "error", err, "active_directory_id", ad.ID)
		return nil, err
	}

	if len(svms) > 0 {
		svmNames := make([]string, len(svms))
		for i, svm := range svms {
			svmNames[i] = svm.Name
		}
		logger.Errorf("Active Directory is in use by %d SVM(s): %v", len(svms), svmNames)
		return &CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: false,
		}, nil
	}

	// Check if any pools are using this Active Directory
	filter := dbutils.CreateFilterWithConditions(dbutils.NewFilterCondition("active_directory_id", "=", ad.ID))
	pools, err2 := a.SE.ListPools(ctx, filter)
	if err2 != nil {
		logger.Errorf("Failed to check pools using Active Directory error: %v", err2)
		return nil, err2
	}
	if len(pools) > 0 {
		poolNames := make([]string, len(pools))
		for i, pool := range pools {
			poolNames[i] = pool.Name
		}
		logger.Errorf("Active Directory credentials are in use by %d pool(s): %v", len(pools), poolNames)
		return &CheckDeletionAllowedResult{
			ADExists:        true,
			DeletionAllowed: false,
		}, nil
	}

	logger.Info("Active Directory can be deleted", "active_directory_uuid", params.ActiveDirectoryUUID)
	return &CheckDeletionAllowedResult{
		ADExists:        true,
		DeletionAllowed: true,
	}, nil
}

// UpdateActiveDirectoryStateActivity sets the lifecycle state of an Active Directory record in the VCP DB.
// It is used by the delete workflow rollback path (and the supervisor) to restore a previous state when a
// non-SDE delete fails before the record is removed, preventing the AD from getting stuck in DELETING.
// Only allow-listed rollback states are accepted; a missing record is treated as success since the resource
// may already have been deleted.
func (a ActiveDirectoryDeleteActivity) UpdateActiveDirectoryStateActivity(ctx context.Context, adUUID string, accountID int64, state string, stateDetails string) error {
	logger := util.GetLogger(ctx)

	if !datamodel.IsAllowedADRollbackState(state) {
		logger.Errorf("Rejecting Active Directory %s state update: %q is not a permitted rollback target", adUUID, state)
		return customerrors.Errorf("state %q is not a permitted Active Directory rollback target", state)
	}

	ad, err := a.SE.GetActiveDirectoryByUuidAndAccountId(ctx, adUUID, accountID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Infof("Active Directory %s not found while setting state to %s, skipping", adUUID, state)
			return nil
		}
		logger.Errorf("Failed to load Active Directory %s for state update: %v", adUUID, err)
		return err
	}

	ad.State = state
	ad.StateDetails = stateDetails
	if _, err := a.SE.UpdateActiveDirectory(ctx, ad); err != nil {
		logger.Errorf("Failed to update Active Directory %s state to %s: %v", adUUID, state, err)
		return err
	}

	logger.Infof("Updated Active Directory %s state to %s", adUUID, state)
	return nil
}

// DeleteVcpActiveDirectory deletes an Active Directory from VCP DB
func (a ActiveDirectoryDeleteActivity) DeleteVcpActiveDirectory(ctx context.Context, params *common.DeleteActiveDirectoryParams) error {
	logger := util.GetLogger(ctx)
	logger.Debug("Starting DeleteVcpActiveDirectory activity", "active_directory_uuid", params.ActiveDirectoryUUID)

	// Check if the Active Directory exists in the database for this account
	ad, err := a.SE.GetActiveDirectoryByUuidAndAccountId(ctx, params.ActiveDirectoryUUID, params.AccountId)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Info("Active Directory not found in database, considering it already deleted")
			return nil
		}
		logger.Errorf("Failed to get Active Directory from database. error: %v", err)
		return err
	}

	credentialPath := ad.CredentialPath

	// Delete the Active Directory from the database
	err = a.SE.DeleteActiveDirectory(ctx, params.ActiveDirectoryUUID)
	if err != nil {
		logger.Errorf("Failed to delete Active Directory from database: %v", err)
		return err
	}

	// Delete the password secret from Secret Manager
	cloudService, _ := hyperscaler.ResolveCloudService(ctx)
	if cloudService != nil && credentialPath != "" {
		projectID := env.SecretManagerProjectID
		err = cloudService.DeleteSecret(projectID, credentialPath)
		if err != nil {
			// Log the error but don't fail the operation
			logger.Warnf("Failed to delete password secret from Secret Manager: %v", err)
		} else {
			logger.Debug("Successfully deleted password secret from Secret Manager")
		}
	}

	logger.Infof("Successfully deleted Active Directory from VCP: %s", params.ActiveDirectoryUUID)
	return nil
}

// DeleteSdeActiveDirectory deletes an Active Directory from SDE/CVP.
// Propagates all CVP errors (including 404 Not Found) to match CVP/CVS behavior:
// CVP/CVS return 404 when the AD does not exist on delete; we do the same.
func (a ActiveDirectoryDeleteActivity) DeleteSdeActiveDirectory(ctx context.Context, params *common.DeleteActiveDirectoryParams) error {
	logger := util.GetLogger(ctx)
	logger.Debug("Starting DeleteSdeActiveDirectory activity")

	jwtToken := utils.GetCVPJWTFromContext(ctx)

	// Create CVP client
	cvpClient := cvp.CreateClient(logger, jwtToken)

	// Prepare the delete parameters
	deleteParams := &active_directories.V1betaDeleteActiveDirectoryParams{
		ProjectNumber:     params.ProjectNumber,
		LocationID:        env.Region,
		ActiveDirectoryID: params.ActiveDirectoryUUID,
	}

	// Call CVP to delete the Active Directory
	resp, err := cvpClient.ActiveDirectories.V1betaDeleteActiveDirectory(deleteParams)
	if err != nil {
		logger.Errorf("Failed to delete Active Directory from CVP: %v", err)
		return WrapCvpError(err)
	}

	if resp != nil && resp.Payload != nil {
		logger.Infof("Successfully initiated Active Directory deletion in CVP, operation: %v", resp.Payload.Name)
	}

	logger.Infof("Successfully deleted Active Directory from SDE/CVP: %s", params.ActiveDirectoryUUID)
	return nil
}
