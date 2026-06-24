package gcp

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/active_directories"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonfactory "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	adHelper "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	customValidators "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/validator"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
)

var (
	createActiveDirectory         = _createActiveDirectory
	updateActiveDirectory         = _updateActiveDirectory
	getActiveDirectory            = _getActiveDirectory
	getActiveDirectoryVcp         = _getActiveDirectoryVCP
	getActiveDirectorySde         = _getActiveDirectorySDE
	listActiveDirectories         = _listActiveDirectories
	getMultipleActiveDirectories  = _getMultipleActiveDirectories
	batchListActiveDirectories    = _batchListActiveDirectories
	batchListActiveDirectoriesSde = _batchListActiveDirectoriesSDE
	deleteActiveDirectory         = _deleteActiveDirectory
	checkIfDomainUpdateAllowed    = _checkIfDomainUpdateAllowed
	validateMultiADConstraints    = _validateMultiADConstraints
)

const (
	DefaultOrganizationalUnit        = "CN=Computers"
	MultiADNotAllowedError           = "Multiple Active Directories are not allowed."
	MaxADLimitReachedForAccountError = "Maximum Active Directory limit reached for this account."
)

// _createActiveDirectory orchestrates the creation of an Active Directory resource.
// It validates input, creates a job, and starts the corresponding Temporal workflow.
func _createActiveDirectory(
	ctx context.Context,
	se database.Storage,
	temporal client.Client,
	params *common.CreateActiveDirectoryParams,
) (*models.ActiveDirectory, string, error) {
	logger := util.GetLogger(ctx)

	adValidator := customValidators.NewActiveDirectoryValidator(ctx, se)
	err := adValidator.RegisterValidators()
	if err != nil {
		return nil, "", err
	}
	err = adValidator.ValidateParams(params)
	if err != nil {
		errMsg := strings.Join(func() []string {
			var messages []string
			for _, validationErr := range err.(validator.ValidationErrors) {
				messages = append(messages, validationErr.Translate(adValidator.Translator))
			}
			return messages
		}(), "; ")
		return nil, "", customerrors.NewUserInputValidationErr(errMsg)
	}

	account, err := getOrCreateAccount(ctx, se, params.AccountId)
	if err != nil {
		return nil, "", err
	}

	if err = persistAccountTrialMetadataIfSet(ctx, se, account, params.TrialMode); err != nil {
		logger.Error("Failed to update account trial metadata", "accountUUID", account.UUID, "error", err)
		return nil, "", err
	}

	if params.OrganizationalUnit == "" {
		params.OrganizationalUnit = DefaultOrganizationalUnit
	}

	err = validateMultiADConstraints(ctx, se, account.ID)
	if err != nil {
		return nil, "", err
	}

	var adRecord *datamodel.ActiveDirectory

	if cvp.CVP_HOST == "" {
		adRecord, err = createAdRecordForNonSDE(ctx, se, params, account.ID)
		if err != nil {
			return nil, "", err
		}
	}

	var resourceUUID string
	if adRecord != nil {
		resourceUUID = adRecord.UUID
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateActiveDirectory),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.ResourceId,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: resourceUUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	controlWorkflowID := fmt.Sprintf("Account_%d_ActiveDirectory_%s", account.ID, params.ResourceId)
	err = workflowsExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		workflows.CreateActiveDirectoryWorkflow,
		workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowID:            createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		params,
		adRecord,
	)
	if err != nil {
		logger.Error("Failed to start create active directory workflow: ", "error", err)
		if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
			logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
		}
		return nil, "", err
	}

	if cvp.CVP_HOST == "" {
		return commonfactory.ConvertDatastoreActiveDirectoryToModel(adRecord), createdJob.UUID, nil
	}

	return convertActiveDirectoryParamsToModel(params), createdJob.UUID, nil
}

// _getActiveDirectoryVCP retrieves an Active Directory resource by UUID from the VCP database.
func _getActiveDirectoryVCP(
	ctx context.Context,
	se database.Storage,
	activeDirectoryUUID string,
) (*models.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)

	// Get ActiveDirectory by UUID from database
	ad, err := se.GetActiveDirectoryByUuidAndAccountId(ctx, activeDirectoryUUID, 0)
	if err != nil {
		logger.Error("Failed to retrieve ActiveDirectory from database", "uuid", activeDirectoryUUID, "error", err)
		return nil, err
	}

	if ad == nil {
		logger.Warn("ActiveDirectory not found", "uuid", activeDirectoryUUID)
		return nil, customerrors.NewNotFoundErr("ActiveDirectory", &activeDirectoryUUID)
	}

	// Convert datamodel to model
	return commonfactory.ConvertDatastoreActiveDirectoryToModel(ad), nil
}

// _getActiveDirectorySDE retrieves an Active Directory resource by UUID from SDE
func _getActiveDirectorySDE(
	ctx context.Context,
	params *common.GetADParams,
) (*models.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)

	if params.CorrelationID == "" {
		logger.Warn("XCorrelationID not set in DescribeActiveDirectoryParams")
		return nil, customerrors.New("unknown error during the describe active directory")
	}

	pathParams := &active_directories.V1betaDescribeActiveDirectoryParams{
		LocationID:        params.LocationID,
		ProjectNumber:     params.ProjectNumber,
		XCorrelationID:    &params.CorrelationID,
		ActiveDirectoryID: params.UUID,
	}
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := cvp.CreateClient(logger, jwtToken)
	resp, err := cvpClient.ActiveDirectories.V1betaDescribeActiveDirectory(pathParams)

	if err != nil {
		switch e := err.(type) {
		case *active_directories.V1betaDescribeActiveDirectoryNotFound:
			return nil, customerrors.NewNotFoundErr("ActiveDirectory", &params.UUID)
		case *active_directories.V1betaDescribeActiveDirectoryBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			return nil, customerrors.NewBadRequestErr(msg)
		case *active_directories.V1betaDescribeActiveDirectoryDefault:
			return nil, err
		default:
			return nil, err
		}
	}
	if resp == nil || resp.Payload == nil {
		return nil, customerrors.New("unknown error during the describe active directory")
	}

	adModel := adHelper.ConvertCVPActiveDirectoryV1BetaToModel(resp.Payload)
	return adModel, nil
}

func _getActiveDirectory(
	ctx context.Context,
	params *common.GetADParams,
	se database.Storage,
) (*models.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)
	// If SDE is disabled, get AD from VCP only
	if cvp.CVP_HOST == "" {
		ad, err := getActiveDirectoryVcp(ctx, se, params.UUID)
		if err != nil {
			return nil, err
		}
		return ad, nil
	}
	// Get AD from SDE (for state information)
	sdeAdModel, err := getActiveDirectorySde(ctx, params)
	if err != nil {
		return nil, err
	}
	// Get AD from VCP (for configuration data)
	vcpAd, vcpErr := getActiveDirectoryVcp(ctx, se, params.UUID)
	if vcpErr != nil {
		// If VCP AD not found, just return SDE AD
		if customerrors.IsNotFoundErr(vcpErr) {
			logger.Infof("AD %s not found in VCP, returning AD from SDE", params.UUID)
			return sdeAdModel, nil
		}
		return nil, vcpErr
	}
	// Compare states and select the higher priority state
	adHelper.CompareADStateHierarchy(sdeAdModel, vcpAd)

	// Set the ID to the ID from VCP AD as it will be used in AD update flow for fetching associated SVMs
	if vcpAd != nil {
		sdeAdModel.ID = vcpAd.ID
	}

	// Return the merged AD model with reconciled state and a nil error.
	return sdeAdModel, nil
}

// CreateActiveDirectory is the public orchestrator method for creating an Active Directory resource.
func (o *GCPOrchestrator) CreateActiveDirectory(
	ctx context.Context,
	params *common.CreateActiveDirectoryParams,
) (*models.ActiveDirectory, string, error) {
	ad, jobUUID, err := createActiveDirectory(ctx, o.storage, o.temporal, params)
	if err != nil {
		return nil, "", err
	}
	return ad, jobUUID, nil
}

// _listActiveDirectories retrieves a list of Active Directory resources for an account.
func _listActiveDirectories(
	ctx context.Context,
	se database.Storage,
	accountName string,
) ([]*models.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)

	// Get account by ID first
	account, err := getOrCreateAccount(ctx, se, accountName)
	if err != nil {
		return nil, err
	}

	// List ActiveDirectories for the account
	ads, err := se.ListActiveDirectories(ctx, account.ID)
	if err != nil {
		logger.Error("Failed to list ActiveDirectories from database", "accountID", account.ID, "error", err)
		return nil, err
	}

	// Convert datamodel to model
	var result []*models.ActiveDirectory
	for _, ad := range ads {
		result = append(result, commonfactory.ConvertDatastoreActiveDirectoryToModel(ad))
	}

	return result, nil
}

// GetActiveDirectory is the public orchestrator method for retrieving an Active Directory resource by UUID.
func (o *GCPOrchestrator) GetActiveDirectory(
	ctx context.Context,
	params *common.GetADParams,
) (*models.ActiveDirectory, error) {
	return getActiveDirectory(ctx, params, o.storage)
}

// _getMultipleActiveDirectories retrieves multiple Active Directory resources by UUIDs.
func _getMultipleActiveDirectories(
	ctx context.Context,
	se database.Storage,
	uuids []string,
) ([]*models.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)

	// Get multiple ActiveDirectories by UUIDs from database
	ads, err := se.GetMultipleActiveDirectoriesByUUIDs(ctx, uuids)
	if err != nil {
		logger.Error("Failed to retrieve multiple ActiveDirectories from database", "uuids", uuids, "error", err)
		return nil, err
	}

	// Convert datamodel to model
	var result []*models.ActiveDirectory
	for _, ad := range ads {
		result = append(result, commonfactory.ConvertDatastoreActiveDirectoryToModel(ad))
	}

	logger.Debug("batch list active directories (VCP DB only, no SDE)",
		"requested_uuid_count", len(uuids),
		"found_in_vcp_count", len(result))

	return result, nil
}

// ListActiveDirectories is the public orchestrator method for listing Active Directory resources for an account.
func (o *GCPOrchestrator) ListActiveDirectories(
	ctx context.Context,
	accountName string,
) ([]*models.ActiveDirectory, error) {
	ads, err := listActiveDirectories(ctx, o.storage, accountName)
	if err != nil {
		return nil, err
	}
	return ads, nil
}

// GetMultipleActiveDirectories is the public orchestrator method for retrieving multiple Active Directory resources by UUIDs.
func (o *GCPOrchestrator) GetMultipleActiveDirectories(
	ctx context.Context,
	uuids []string,
) ([]*models.ActiveDirectory, error) {
	ads, err := getMultipleActiveDirectories(ctx, o.storage, uuids)
	if err != nil {
		return nil, err
	}
	return ads, nil
}

// BatchListActiveDirectories retrieves multiple AD resources, merging CVP and VCP
// state when CVP_HOST is set. In non-CVP mode it delegates to GetMultipleActiveDirectories.
func (o *GCPOrchestrator) BatchListActiveDirectories(
	ctx context.Context,
	params *common.BatchListADsParams,
) ([]*models.ActiveDirectory, error) {
	return batchListActiveDirectories(ctx, o.storage, params)
}

// _batchListActiveDirectories fetches ADs from CVP and VCP in parallel when
// CVP_HOST is set, merges state using CompareADStateHierarchy, and returns a
// unified slice. In non-CVP mode it delegates to getMultipleActiveDirectories.
func _batchListActiveDirectories(
	ctx context.Context,
	se database.Storage,
	params *common.BatchListADsParams,
) ([]*models.ActiveDirectory, error) {
	if cvp.CVP_HOST == "" {
		return getMultipleActiveDirectories(ctx, se, params.UUIDs)
	}

	logger := util.GetLogger(ctx)
	logger.Debug("batch list active directories: starting parallel SDE (CVP) and VCP DB fetch",
		"requested_uuid_count", len(params.UUIDs),
		"location_id", params.LocationID)

	var (
		sdeADs []*models.ActiveDirectory
		sdeErr error
		vcpADs []*models.ActiveDirectory
		vcpErr error
		wg     sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		sdeADs, sdeErr = batchListActiveDirectoriesSde(ctx, params)
	}()

	go func() {
		defer wg.Done()
		dbADs, err := se.GetMultipleActiveDirectoriesByUUIDs(ctx, params.UUIDs)
		if err != nil {
			vcpErr = err
			return
		}
		vcpADs = make([]*models.ActiveDirectory, 0, len(dbADs))
		for _, ad := range dbADs {
			vcpADs = append(vcpADs, commonfactory.ConvertDatastoreActiveDirectoryToModel(ad))
		}
	}()

	wg.Wait()

	if sdeErr != nil {
		logger.Error("CVP batch list active directories failed", "error", sdeErr)
		return nil, sdeErr
	}
	if vcpErr != nil {
		logger.Error("VCP batch list active directories failed", "error", vcpErr)
		return nil, vcpErr
	}

	vcpADMap := make(map[string]*models.ActiveDirectory, len(vcpADs))
	for _, ad := range vcpADs {
		if ad != nil {
			vcpADMap[ad.UUID] = ad
		}
	}

	for _, sdeAD := range sdeADs {
		if sdeAD == nil {
			continue
		}
		if vcpAD, ok := vcpADMap[sdeAD.UUID]; ok {
			adHelper.CompareADStateHierarchy(sdeAD, vcpAD)
		}
	}

	logger.Debug("batch list active directories: completed SDE/VCP fetch and state merge",
		"requested_uuid_count", len(params.UUIDs),
		"found_in_sde_count", len(sdeADs),
		"found_in_vcp_db_count", len(vcpADs),
		"response_active_directory_count", len(sdeADs))

	return sdeADs, nil
}

// _batchListActiveDirectoriesSDE fetches multiple ADs from CVP (SDE) via the
// batch API and converts the results to domain models.
func _batchListActiveDirectoriesSDE(
	ctx context.Context,
	params *common.BatchListADsParams,
) ([]*models.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := cvp.CreateClient(logger, jwtToken)

	cvpParams := cvpBatch.NewV1betaBatchListActiveDirectoriesParamsWithContext(ctx)
	cvpParams.SetLocationID(params.LocationID)
	cvpParams.SetBody(&cvpmodels.ActiveDirectoryIDListV1beta{
		ActiveDirectoryUUIDs: params.UUIDs,
	})
	if len(params.Fields) > 0 {
		cvpParams.SetFields(params.Fields)
	}
	if params.CorrelationID != "" {
		cvpParams.SetXCorrelationID(&params.CorrelationID)
	}

	resp, err := cvpClient.Batch.V1betaBatchListActiveDirectories(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list active directories failed: %w", err)
	}

	var result []*models.ActiveDirectory
	if resp != nil && resp.Payload != nil {
		for _, cvpAD := range resp.Payload.ActiveDirectories {
			if cvpAD != nil {
				result = append(result, adHelper.ConvertCVPBatchADToModel(cvpAD))
			}
		}
	}

	return result, nil
}

// GetADConfig retrieves an Active Directory resource by account ID and resource name.
func (o *GCPOrchestrator) GetADConfig(ctx context.Context, params *common.GetADParams) (*models.ActiveDirectory, error) {
	account, err := getAccountWithName(ctx, o.storage, params.AccountName)
	if err != nil {
		return nil, err
	}
	adConfig, err2 := o.storage.GetActiveDirectoryByUuidAndAccountId(ctx, params.UUID, account.ID)
	if err2 != nil {
		return nil, err2
	}

	return commonfactory.ConvertDatastoreActiveDirectoryToModel(adConfig), nil
}

func (o *GCPOrchestrator) GetSDEActiveDirectory(ctx context.Context, getADParams *common.GetADParams) (*cvpmodels.ActiveDirectoryV1beta, error) {
	// Phase 2 implementation
	return nil, nil
}

// createAdRecordForNonSDE creates a database record for Active Directory when SDE is disabled.
// This ensures the AD record exists in the DB for job tracking purposes.
func createAdRecordForNonSDE(
	ctx context.Context,
	se database.Storage,
	params *common.CreateActiveDirectoryParams,
	accountID int64,
) (*datamodel.ActiveDirectory, error) {
	adRecord := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{
			UUID: utils.RandomUUID(),
		},
		AdName:       params.ResourceId,
		Username:     params.Username,
		Domain:       params.Domain,
		DNS:          params.DNS,
		NetBIOS:      params.NetBIOS,
		State:        datamodel.LifeCycleStateCreating,
		StateDetails: datamodel.LifeCycleStateCreatingDetails,
		AccountId:    accountID,
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: params.OrganizationalUnit,
			Site:               params.Site,
			AdUsers: map[string][]string{
				utils.ActiveDirectoryGroupBuiltInBackupOperators: params.BackupOperators,
				utils.ActiveDirectoryGroupBuiltInAdministrators:  params.Administrators,
				utils.ActiveDirectorySeSecurityPrivilege:         params.SecurityOperators,
			},
			KdcIP:                      params.KdcIP,
			KdcHostname:                params.KdcHostname,
			AesEncryption:              params.AesEncryption,
			EncryptDCConnections:       params.EncryptDCConnections,
			LdapSigning:                params.LdapSigning,
			AllowLocalNFSUsersWithLdap: params.AllowLocalNFSUsersWithLdap,
			Description:                params.Description,
			PrimaryAD:                  true,
		},
	}

	return se.CreateActiveDirectory(ctx, adRecord)
}

func convertActiveDirectoryParamsToModel(params *common.CreateActiveDirectoryParams) *models.ActiveDirectory {
	ad := &models.ActiveDirectory{
		AdName:       params.ResourceId,
		Username:     params.Username,
		Domain:       params.Domain,
		DNS:          params.DNS,
		NetBIOS:      params.NetBIOS,
		State:        datamodel.LifeCycleStateCreating,
		StateDetails: datamodel.LifeCycleStateCreatingDetails,
		ActiveDirectoryAttributes: &models.ActiveDirectoryAttributes{
			OrganizationalUnit:         params.OrganizationalUnit,
			Site:                       params.Site,
			SecurityOperators:          params.SecurityOperators,
			BackupOperators:            params.BackupOperators,
			Administrators:             params.Administrators,
			KdcIP:                      params.KdcIP,
			KdcHostname:                params.KdcHostname,
			AesEncryption:              params.AesEncryption,
			EncryptDCConnections:       params.EncryptDCConnections,
			LdapSigning:                params.LdapSigning,
			AllowLocalNFSUsersWithLdap: params.AllowLocalNFSUsersWithLdap,
			Description:                params.Description,
		},
	}
	return ad
}

// UpdateActiveDirectory is the public orchestrator method for updating an Active Directory resource.
func (o *GCPOrchestrator) UpdateActiveDirectory(
	ctx context.Context,
	params *common.UpdateActiveDirectoryParams,
) (*models.ActiveDirectory, string, error) {
	ad, jobUUID, err := updateActiveDirectory(ctx, o.storage, o.temporal, params)
	if err != nil {
		return nil, "", err
	}
	return ad, jobUUID, nil
}

// _updateActiveDirectory orchestrates the creation of an Active Directory resource.
// It validates input, creates a job, and starts the corresponding Temporal workflow.
func _updateActiveDirectory(
	ctx context.Context,
	se database.Storage,
	temporal client.Client,
	params *common.UpdateActiveDirectoryParams,
) (*models.ActiveDirectory, string, error) {
	logger := util.GetLogger(ctx)

	adValidator := customValidators.NewActiveDirectoryValidator(ctx, se)
	err := adValidator.RegisterValidators()
	if err != nil {
		return nil, "", err
	}
	err = adValidator.ValidateParams(params)
	if err != nil {
		errMsg := strings.Join(func() []string {
			var messages []string
			for _, validationErr := range err.(validator.ValidationErrors) {
				messages = append(messages, validationErr.Translate(adValidator.Translator))
			}
			return messages
		}(), "; ")
		return nil, "", customerrors.NewUserInputValidationErr(errMsg)
	}

	account, err := getOrCreateAccount(ctx, se, params.AccountId)
	if err != nil {
		return nil, "", err
	}

	describeParams, err := adHelper.ConvertUpdateParamsToDescribeParams(params, account.Name)
	if err != nil {
		return nil, "", err
	}

	ad, err := getActiveDirectory(ctx, describeParams, se)
	if err != nil {
		return nil, "", err
	}

	// If AD not found, return not found error immediately without creating a job
	if ad == nil {
		return nil, "", customerrors.NewNotFoundErr("ActiveDirectory", &params.ActiveDirectoryId)
	}

	if params.Domain != nil && *params.Domain != ad.Domain {
		// Check if domain update is allowed
		err = checkIfDomainUpdateAllowed(ctx, se, ad)
		if err != nil {
			return nil, "", err
		}
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateActiveDirectory),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  ad.AdName,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: params.ActiveDirectoryId,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	controlWorkflowID := fmt.Sprintf("Account_%d_ActiveDirectory_%s", account.ID, ad.AdName)
	err = workflowsExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		workflows.UpdateActiveDirectoryWorkflow,
		workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowID:            createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		params,
		ad,
	)
	if err != nil {
		logger.Error("Failed to start update active directory workflow: ", "error", err)
		if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
			logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
		}
		return nil, "", err
	}

	if cvp.CVP_HOST == "" {
		adRecord, _ := se.GetActiveDirectoryByNameAndAccountID(ctx, ad.AdName, account.ID)
		if adRecord == nil {
			return nil, "", customerrors.NewNotFoundErr("ActiveDirectory", &params.ActiveDirectoryId)
		}
		adRecord.State = datamodel.LifeCycleStateUpdating
		adRecord.StateDetails = datamodel.LifeCycleStateUpdatingDetails
		adRecord, _ = se.UpdateActiveDirectory(ctx, adRecord)
	}

	ad.State = datamodel.LifeCycleStateUpdating
	ad.StateDetails = datamodel.LifeCycleStateUpdatingDetails
	return ad, createdJob.UUID, nil
}

func _checkIfDomainUpdateAllowed(ctx context.Context, se database.Storage, oldAd *models.ActiveDirectory) error {
	logger := util.GetLogger(ctx)

	svms, err := se.GetSVMsUsingActiveDirectory(ctx, oldAd.ID)
	if err != nil {
		logger.Error("Failed to check SVMs using Active Directory", "error", err, "active_directory_id", oldAd.ID)
		return err
	}

	if len(svms) > 0 {
		logger.Errorf("Active Directory is in use by %d SVM(s)", len(svms))
		return customerrors.NewBadRequestErr("Active Directory domain cannot be updated while it is in use")
	}

	return nil
}

// _deleteActiveDirectory orchestrates the deletion of an Active Directory resource.
// It creates a job and starts the corresponding Temporal workflow to delete the AD.
// Returns empty string if the AD is already deleted (indicating operation is already done).
func _deleteActiveDirectory(ctx context.Context, se database.Storage, temporal client.Client, params *common.DeleteActiveDirectoryParams) (string, error) {
	logger := util.GetLogger(ctx)

	// Get account
	account, err := getOrCreateAccount(ctx, se, params.ProjectNumber)
	if err != nil {
		logger.Error("Failed to get account", "error", err, "project ", params.ProjectNumber)
		return "", err
	}

	params.AccountId = account.ID

	// Get Active Directory to check if it exists and its state
	ad, err := se.GetActiveDirectoryByUuidAndAccountId(ctx, params.ActiveDirectoryUUID, params.AccountId)
	if err != nil && !customerrors.IsNotFoundErr(err) {
		logger.Error("Failed to get Active Directory", "error", err, "active_directory_id", params.ActiveDirectoryUUID)
		return "", err
	}

	if ad != nil {
		// Check if the Active Directory is already in deleted state
		if ad.State == datamodel.LifeCycleStateDeleted {
			logger.Info("Active Directory is already deleted", "active_directory_uuid", params.ActiveDirectoryUUID, "state", ad.State)
			return "", nil
		}

		// Check if the Active Directory is already being deleted
		if ad.State == datamodel.LifeCycleStateDeleting {
			logger.Info("Active Directory is already being deleted", "active_directory_uuid", params.ActiveDirectoryUUID, "state", ad.State)
			// Check if there's an existing job
			existingJob, err := se.GetJobByResourceUUID(ctx, params.ActiveDirectoryUUID, string(datamodel.JobTypeDeleteActiveDirectory))
			if err == nil && existingJob != nil {
				logger.Info("Returning existing job UUID", "job_uuid", existingJob.UUID)
				return existingJob.UUID, nil
			}
			logger.Warn("Active Directory is in Deleting state but no job found, proceeding with new job creation")
		}
	}

	// Create a job for tracking the deletion
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteActiveDirectory),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.ActiveDirectoryUUID,
		AccountID:     sql.NullInt64{Int64: params.AccountId, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: params.ActiveDirectoryUUID,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return "", err
	}

	// Start the delete workflow
	controlWorkflowID := fmt.Sprintf("Account_%d_ActiveDirectory_%s_Delete", params.AccountId, params.ActiveDirectoryUUID)
	err = workflowsExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		workflows.DeleteActiveDirectoryWorkflow,
		workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowID:            createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		params,
	)
	if err != nil {
		logger.Error("Failed to start delete active directory workflow: ", "error", err)
		if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
			logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
		}
		return "", err
	}

	return createdJob.UUID, nil
}

// DeleteActiveDirectory is the public orchestrator method for deleting an Active Directory resource.
func (o *GCPOrchestrator) DeleteActiveDirectory(ctx context.Context, params *common.DeleteActiveDirectoryParams) (string, error) {
	jobUUID, err := deleteActiveDirectory(ctx, o.storage, o.temporal, params)
	if err != nil {
		return "", err
	}
	return jobUUID, nil
}

func _validateMultiADConstraints(ctx context.Context, se database.Storage, accountID int64) error {
	logger := util.GetLogger(ctx)
	ads, err := se.ListActiveDirectories(ctx, accountID)
	if err != nil {
		return err
	}
	maxADsPerAccount := utils.MaxNumberOfADPerAccount
	multiADEnabled := utils.EnableMultiAD

	// If multi-AD is disabled, only allow creation if no ADs exist (len(ads) == 0)
	// This ensures that after creation, there will be exactly 1 AD total (including the one being created)
	if !multiADEnabled {
		if len(ads) > 0 {
			logger.Error(MultiADNotAllowedError)
			return customerrors.NewUserInputValidationErr(MultiADNotAllowedError)
		}
		return nil
	}

	// If multi-AD is enabled, check if adding one more AD would exceed the limit
	// We check len(ads) < maxADsPerAccount because after creation, len(ads) + 1 <= maxADsPerAccount
	// This ensures the total number of ADs (existing + the one being created) does not exceed maxADsPerAccount
	if len(ads) >= maxADsPerAccount {
		logger.Error(MaxADLimitReachedForAccountError)
		return customerrors.NewConflictErr(MaxADLimitReachedForAccountError)
	}
	return nil
}
