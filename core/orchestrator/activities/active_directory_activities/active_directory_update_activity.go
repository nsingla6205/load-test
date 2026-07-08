package active_directory_activities

import (
	"context"
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/active_directories"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/async"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
)

type ActiveDirectoryUpdateActivity struct {
	SE        database.Storage
	Scheduler *scheduler.TemporalScheduler
}

const (
	ErrTypeResourceNotFound = "NotFoundErr"
	ErrInvalidRequest       = "InvalidRequestErr"
)

var (
	updatePasswordSecret = _updatePasswordSecret
	decryptPassword      = utils.DecryptPassword
)

func (a ActiveDirectoryUpdateActivity) MarkVcpAdToUpdatingActivity(ctx context.Context, params *common.UpdateActiveDirectoryParams, oldAd *models.ActiveDirectory) error {
	Logger := util.GetLogger(ctx)
	Logger.Debug("Updating VCP ActiveDirectory DB Record to UPDATING state")

	account, err := a.SE.GetAccount(ctx, params.AccountId)
	if err != nil {
		return errors.New("Could not fetch related Account for Active Directory update")
	}

	olAdVcpDbRecord, _ := a.SE.GetActiveDirectoryByNameAndAccountID(ctx, oldAd.AdName, account.ID)
	if olAdVcpDbRecord == nil {
		Logger.Info("Active Directory from SDE not found in VCP, skipping VCP update.")
		return nil
	}

	olAdVcpDbRecord.State = datamodel.LifeCycleStateUpdating
	olAdVcpDbRecord.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	_, err = a.SE.UpdateActiveDirectory(ctx, olAdVcpDbRecord)
	if err != nil {
		return err
	}
	Logger.Info("Marked VCP ActiveDirectory as updating")

	return nil
}

func (a ActiveDirectoryUpdateActivity) MarkVcpAdToErrorActivity(ctx context.Context, params *common.UpdateActiveDirectoryParams, oldAd *models.ActiveDirectory) error {
	Logger := util.GetLogger(ctx)
	Logger.Debug("Updating VCP ActiveDirectory DB Record to ERROR state")

	account, err := a.SE.GetAccount(ctx, params.AccountId)
	if err != nil {
		return errors.New("Could not fetch related Account for Active Directory update")
	}

	olAdVcpDbRecord, _ := a.SE.GetActiveDirectoryByNameAndAccountID(ctx, oldAd.AdName, account.ID)
	if olAdVcpDbRecord == nil {
		Logger.Info("Active Directory from SDE not found in VCP, skipping VCP update.")
		return nil
	}

	olAdVcpDbRecord.State = datamodel.LifeCycleStateError
	olAdVcpDbRecord.StateDetails = datamodel.LifeCycleStateUpdateErrorDetails

	_, err = a.SE.UpdateActiveDirectory(ctx, olAdVcpDbRecord)
	if err != nil {
		return err
	}
	Logger.Info("Marked VCP ActiveDirectory as Error")

	return nil
}

func (a ActiveDirectoryUpdateActivity) UpdateVcpActiveDirectory(ctx context.Context, params *common.UpdateActiveDirectoryParams, oldAd *models.ActiveDirectory, changeId string, state string, stateDetails string) error {
	Logger := util.GetLogger(ctx)
	Logger.Debug("Updating VCP ActiveDirectory")

	account, err := a.SE.GetAccount(ctx, params.AccountId)
	if err != nil {
		return errors.New("Could not fetch related Account for Active Directory update")
	}

	// Fetch again to ensure working with current record
	oldDbAd, err := a.SE.GetActiveDirectoryByNameAndAccountID(ctx, oldAd.AdName, account.ID)
	if err != nil {
		Logger.Errorf("Failed to fetch Active Directory by name and account ID: %v", err)
		return errors.New("Could not fetch Active Directory from VCP database")
	}
	if oldDbAd == nil {
		Logger.Info("Active Directory from SDE not found in VCP, skipping VCP update.")
		return nil
	}

	updatedAd := convertUpdateParamsToModel(params, oldDbAd)
	if params.Password != nil {
		decryptedPassword, decryptErr := utils.DecryptPassword(log.Secret(*params.Password))
		if decryptErr != nil {
			return decryptErr
		}

		passwordErr := updatePasswordSecret(ctx, *decryptedPassword, oldDbAd.CredentialPath)
		if passwordErr != nil {
			return passwordErr
		}
		updatedAd.CredentialPath = oldDbAd.CredentialPath
	}

	updatedAd.State = state
	updatedAd.StateDetails = stateDetails
	updatedAd.ChangeId = changeId
	updatedAd.ID = oldDbAd.ID
	_, err = a.SE.UpdateActiveDirectory(ctx, updatedAd)
	if err != nil {
		return err
	}
	Logger.Debug("Updated VCP ActiveDirectory")

	return nil
}

func (a ActiveDirectoryUpdateActivity) PollSdeUpdateActivity(ctx context.Context, params *common.UpdateActiveDirectoryParams, result *cvpModels.OperationV1beta) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Starting PollSdeUpdateActivity for account %s, location %s", params.AccountId, params.LocationId)

	if result == nil {
		logger.Warn("PollSdeUpdateActivity called with nil result, skipping poll")
		return nil
	}

	// Check if operation is already done (synchronous completion)
	if result.Done != nil && *result.Done {
		logger.Info("Operation already completed synchronously, skipping poll")
		return nil
	}

	// For async operations, we need the operation name to poll
	if result.Name == "" {
		logger.Error("Operation name is empty, cannot poll")
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInvalidOperationName, errors.New("operation name is nil")))
	}

	logger.Debugf("Polling async operation: %s", result.Name)
	jwtToken := utils.GetCVPJWTFromContext(ctx)
	cvpClient := CvpClient(logger, jwtToken)

	// Extract the operation UUID
	operationUUID := utils.GetOperationUUID(result.Name)
	logger.Infof("Extracted operation UUID: %s", operationUUID)

	operationParams := async.NewV1betaDescribeOperationParams()
	operationParams.OperationID = operationUUID
	operationParams.ProjectNumber = params.AccountId
	operationParams.LocationID = params.LocationId

	logger.Debugf("Polling CVP operation with params: ProjectNumber=%s, LocationID=%s, OperationID=%s",
		params.AccountId, params.LocationId, operationUUID)

	res, err := pollCvpOperationForWorkflow(ctx, cvpClient, operationParams)
	if err != nil {
		logger.Errorf("Failed to poll CVP operation %s: %v", operationUUID, err)
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrCVPClientHandleResourceEventError, err),
		)
	}

	logger.Debugf("Poll response for operation %s: Done=%v, Error=%v", operationUUID, res.Done, res.Error)

	if res.Done != nil && *res.Done {
		if res.Error != nil {
			logger.Errorf("Operation %s completed with error: Code=%d, Message=%s",
				operationUUID, int(res.Error.Code), res.Error.Message)
			// Operation is terminal (Done=true) — retrying cannot change the outcome.
			return WrapCvpErrorNonRetryable(int(res.Error.Code), res.Error.Message)
		}
		logger.Infof("Operation %s completed successfully", operationUUID)
		return nil
	}

	logger.Debugf("Operation %s not yet finished, will retry", operationUUID)
	return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrSDEJobNotFinished, errors.New("job not finished")))
}

func (a ActiveDirectoryUpdateActivity) UpdateSdeActiveDirectory(ctx context.Context, params *common.UpdateActiveDirectoryParams) (*cvpModels.OperationV1beta, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.AccountId, params.LocationId, nil)

	body := &cvpModels.ActiveDirectoryUpdateV1beta{}

	if params.Username != nil {
		body.Username = *params.Username
	}
	if params.Description != nil {
		body.Description = params.Description
	}
	if params.Password != nil {
		decryptedPassword, err := decryptPassword(log.Secret(*params.Password))
		if err != nil {
			return nil, errors.New("Password could not be decrypted.")
		}
		body.Password = *decryptedPassword
	}
	if params.Domain != nil {
		body.Domain = *params.Domain
	}
	if params.DNS != nil {
		body.DNS = *params.DNS
	}
	if params.NetBIOS != nil {
		body.NetBIOS = *params.NetBIOS
	}
	if params.OrganizationalUnit != nil {
		body.OrganizationalUnit = params.OrganizationalUnit
	}
	if params.Site != nil {
		body.Site = params.Site
	}
	if params.KdcIP != nil {
		body.KdcIP = *params.KdcIP
	}
	if params.KdcHostname != nil {
		body.KdcHostname = *params.KdcHostname
	}
	if params.LdapSigning != nil {
		body.LdapSigning = params.LdapSigning
	}
	if params.AllowLocalNFSUsersWithLdap != nil {
		body.AllowLocalNFSUsersWithLdap = params.AllowLocalNFSUsersWithLdap
	}
	if params.EncryptDCConnections != nil {
		body.EncryptDCConnections = params.EncryptDCConnections
	}
	if params.AesEncryption != nil {
		body.AesEncryption = params.AesEncryption
	}
	if params.SecurityOperators != nil {
		body.SecurityOperators = params.SecurityOperators
	}
	if params.BackupOperators != nil {
		body.BackupOperators = params.BackupOperators
	}
	if params.Administrators != nil {
		body.Administrators = params.Administrators
	}

	updateParams := &active_directories.V1betaUpdateActiveDirectoryParams{
		ActiveDirectoryID: params.ActiveDirectoryId,
		LocationID:        params.LocationId,
		ProjectNumber:     params.AccountId,
		XCorrelationID:    &params.XCorrelationId,
		Body:              body,
	}

	jwtToken := utils.GetAuthTokenFromContext(ctx)
	cvpClient := CvpClient(logger, jwtToken)
	sdeResponse, err := cvpClient.ActiveDirectories.V1betaUpdateActiveDirectory(updateParams)
	if err != nil {
		logger.Errorf("Failed to update Active Directory in SDE: %v", err)
		return nil, WrapCvpError(err)
	}
	return sdeResponse.Payload, nil
}

// convertUpdateParamsToModel converts UpdateActiveDirectoryParams to a model, merging with oldAd
func convertUpdateParamsToModel(params *common.UpdateActiveDirectoryParams, oldAd *datamodel.ActiveDirectory) *datamodel.ActiveDirectory {
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{
			UUID:      oldAd.UUID,
			CreatedAt: oldAd.CreatedAt,
		},
		AdName:   oldAd.AdName,
		Username: oldAd.Username,
		Domain:   oldAd.Domain,
		DNS:      oldAd.DNS,
		NetBIOS:  oldAd.NetBIOS,
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: oldAd.ActiveDirectoryAttributes.OrganizationalUnit,
			Site:               oldAd.ActiveDirectoryAttributes.Site,
			AdUsers: map[string][]string{
				utils.ActiveDirectorySeSecurityPrivilege:         oldAd.ActiveDirectoryAttributes.AdUsers[utils.ActiveDirectorySeSecurityPrivilege],
				utils.ActiveDirectoryGroupBuiltInBackupOperators: oldAd.ActiveDirectoryAttributes.AdUsers[utils.ActiveDirectoryGroupBuiltInBackupOperators],
				utils.ActiveDirectoryGroupBuiltInAdministrators:  oldAd.ActiveDirectoryAttributes.AdUsers[utils.ActiveDirectoryGroupBuiltInAdministrators],
			},
			KdcIP:                      oldAd.ActiveDirectoryAttributes.KdcIP,
			KdcHostname:                oldAd.ActiveDirectoryAttributes.KdcHostname,
			AesEncryption:              oldAd.ActiveDirectoryAttributes.AesEncryption,
			EncryptDCConnections:       oldAd.ActiveDirectoryAttributes.EncryptDCConnections,
			LdapSigning:                oldAd.ActiveDirectoryAttributes.LdapSigning,
			AllowLocalNFSUsersWithLdap: oldAd.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap,
			Description:                oldAd.ActiveDirectoryAttributes.Description,
		},
	}

	// Update with non-empty params from request
	if params.Username != nil && *params.Username != "" {
		ad.Username = *params.Username
	}
	if params.Domain != nil && *params.Domain != "" {
		ad.Domain = *params.Domain
	}
	if params.DNS != nil && *params.DNS != "" {
		ad.DNS = *params.DNS
	}
	if params.NetBIOS != nil && *params.NetBIOS != "" {
		ad.NetBIOS = *params.NetBIOS
	}
	if params.OrganizationalUnit != nil && *params.OrganizationalUnit != "" {
		ad.ActiveDirectoryAttributes.OrganizationalUnit = *params.OrganizationalUnit
	}
	if params.Site != nil && *params.Site != "" {
		ad.ActiveDirectoryAttributes.Site = *params.Site
	}
	updateAdUsers := func(key string, values []string) {
		if values != nil {
			if len(values) == 0 {
				ad.ActiveDirectoryAttributes.AdUsers[key] = nil
			} else {
				ad.ActiveDirectoryAttributes.AdUsers[key] = values
			}
		}
	}
	updateAdUsers(utils.ActiveDirectorySeSecurityPrivilege, params.SecurityOperators)
	updateAdUsers(utils.ActiveDirectoryGroupBuiltInBackupOperators, params.BackupOperators)
	updateAdUsers(utils.ActiveDirectoryGroupBuiltInAdministrators, params.Administrators)
	if params.KdcIP != nil && *params.KdcIP != "" {
		ad.ActiveDirectoryAttributes.KdcIP = *params.KdcIP
	}
	if params.KdcHostname != nil && *params.KdcHostname != "" {
		ad.ActiveDirectoryAttributes.KdcHostname = *params.KdcHostname
	}
	if params.AesEncryption != nil {
		ad.ActiveDirectoryAttributes.AesEncryption = *params.AesEncryption
	}
	if params.EncryptDCConnections != nil {
		ad.ActiveDirectoryAttributes.EncryptDCConnections = *params.EncryptDCConnections
	}
	if params.LdapSigning != nil {
		ad.ActiveDirectoryAttributes.LdapSigning = *params.LdapSigning
	}
	if params.AllowLocalNFSUsersWithLdap != nil {
		ad.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap = *params.AllowLocalNFSUsersWithLdap
	}
	if params.Description != nil && *params.Description != "" {
		ad.ActiveDirectoryAttributes.Description = *params.Description
	}

	return ad
}

func pollCvpOperationForWorkflow(ctx context.Context, cvpClient cvpapi.Cvp, operationParams *async.V1betaDescribeOperationParams) (*cvpModels.OperationV1beta, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Polling for operation %s", operationParams.OperationID)
	operationResponse, err := cvpClient.Async.V1betaDescribeOperation(operationParams)
	if err != nil {
		var notFoundErr *async.V1betaDescribeOperationNotFound
		if errors.As(err, &notFoundErr) {
			logger.Infof("SDE Async.V1betaDescribeOperation returned 404 (resource not found), treating as non-retryable: %v", err)
			return nil, temporal.NewNonRetryableApplicationError(err.Error(), ErrTypeResourceNotFound, err)
		}
		var badReqErr *async.V1betaDescribeOperationBadRequest
		if errors.As(err, &badReqErr) {
			logger.Infof("SDE Async.V1betaDescribeOperation returned 400 (bad request), treating as non-retryable: %v", err)
			return nil, temporal.NewNonRetryableApplicationError(err.Error(), ErrInvalidRequest, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDescribingSDEJob, err)
	}

	return operationResponse.Payload, nil
}

func _updatePasswordSecret(ctx context.Context, password string, secretID string) error {
	logger := util.GetLogger(ctx)

	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		logger.Error("Failed to get cloud service", "error", err)
		return vsaerrors.New(err.Error())
	}

	_, err = cloudService.AddSecretVersion(env.SecretManagerProjectID, secretID, password)
	if err != nil {
		logger.Error("Failed to add secret version", "error", err, "secretID", secretID)
		return vsaerrors.New(err.Error())
	}

	logger.Info("Successfully updated secret with new version", "secretID", secretID)

	return nil
}

func (a ActiveDirectoryActivity) UpdateAdCredentialsForSvm(ctx context.Context, node *models.Node, params vsa.UpdateActiveDirectoryCredentialsParams, svmName, externalSVMUUID string, cifs ontapRest.CifsService) error {
	logger := util.GetLogger(ctx)
	ontapProvider, err := getOntapRestProvider(ctx, node)
	if err != nil {
		logger.Error("failed to get ONTAP client", "error", err.Error())
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	err = ontapProvider.UpdateActiveDirectoryCredentials(params, cifs, svmName, externalSVMUUID)
	if err != nil {
		logger.Error("failed to update CIFS service credentials", "error", err.Error())
		return vsaerrors.WrapOntapError(err, vsaerrors.DomainAD)
	}
	return nil
}

func (a ActiveDirectoryActivity) PropagateAdChangeIdToPool(ctx context.Context, pool *datamodel.Pool, adChangeId string) error {
	logger := util.GetLogger(ctx)
	if pool == nil {
		logger.Error("pool is nil, cannot propagate AD change ID")
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("pool is nil, cannot propagate AD change ID"))
	}
	if adChangeId == "" {
		logger.Error("adChangeId is empty")
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("adChangeId is empty"))
	}
	pool.ActiveDirectoryChangeId = adChangeId
	if err := a.SE.UpdatePoolFields(ctx, pool.UUID, map[string]interface{}{"active_directory_change_id": adChangeId}); err != nil {
		logger.Errorf("Failed to update pool %s with new AD change ID: %v", pool.UUID, err)
		return err
	}
	return nil
}
