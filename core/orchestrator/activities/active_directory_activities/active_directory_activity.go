package active_directory_activities

import (
	"context"
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	adHelper "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	logmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

type ActiveDirectoryActivity struct {
	SE database.Storage
}

var (
	getPasswordSecret = _getPasswordSecret
)

// GetActiveDirectoryForPool retrieves the Active Directory configuration associated with the pool ID.
func (a ActiveDirectoryActivity) GetActiveDirectoryForPool(ctx context.Context, poolID int64) (*vsa.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting GetActiveDirectoryForPool activity")

	activeDirectory, err := a.SE.GetActiveDirectoryForPoolByPoolID(ctx, poolID)
	if err != nil {
		logger.Error("Failed to fetch Active Directory for the pool", "poolID", poolID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if activeDirectory == nil {
		logger.Error("Active Directory not found for the pool", "poolID", poolID)
		return nil, errors.New("active directory not found for the pool")
	}

	activity.RecordHeartbeat(ctx, "Finished GetActiveDirectoryForPool activity")
	return validateAndGetVsaActiveDirectory(ctx, activeDirectory)
}

// GetActiveDirectoryStateFromSVMUsage returns the appropriate state and stateDetails for an AD
// based on whether any SVMs use it (IN_USE when in use, READY when not). Same logic as Create Volume flow.
// On error fetching SVMs, defaults to READY and returns nil error.
func (a ActiveDirectoryActivity) GetActiveDirectoryStateFromSVMUsage(ctx context.Context, activeDirectoryId int64) (common.ActiveDirectoryStateResult, error) {
	logger := util.GetLogger(ctx)
	svms, err := a.SE.GetSVMsUsingActiveDirectory(ctx, activeDirectoryId)
	if err != nil {
		logger.Warnf("Failed to fetch SVMs for Active Directory id %d, defaulting state to READY: %v", activeDirectoryId, err)
		return common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}, nil
	}
	if len(svms) > 0 {
		return common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateInUse, StateDetails: datamodel.LifeCycleStateInUseDetails}, nil
	}
	return common.ActiveDirectoryStateResult{State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}, nil
}

func (a ActiveDirectoryActivity) GetSvmsForAd(ctx context.Context, activeDirectoryId int64) ([]*datamodel.Svm, error) {
	logger := util.GetLogger(ctx)
	svms, err := a.SE.GetSVMsUsingActiveDirectory(ctx, activeDirectoryId)
	if err != nil {
		logger.Errorf("Failed to fetch svms for Active Directory id %d: %v", activeDirectoryId, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return svms, nil
}

func (a ActiveDirectoryActivity) GenerateUpdateAdCredentialsParams(ctx context.Context, oldAd models.ActiveDirectory, params common.UpdateActiveDirectoryParams) (*vsa.UpdateActiveDirectoryCredentialsParams, error) {
	oldDbAd, err := a.SE.GetActiveDirectoryByUUID(ctx, oldAd.UUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	oldVsaAd, err := validateAndGetVsaActiveDirectory(ctx, oldDbAd)

	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	newCredentials := a.buildNewCredentials(ctx, params, oldAd, oldVsaAd)
	if newCredentials == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to build new credentials"))
	}

	return &vsa.UpdateActiveDirectoryCredentialsParams{
		OldCredentials: oldVsaAd,
		NewCredentials: newCredentials,
	}, nil
}

func validateAndGetVsaActiveDirectory(ctx context.Context, activeDirectory *datamodel.ActiveDirectory) (*vsa.ActiveDirectory, error) {
	logger := util.GetLogger(ctx)

	if activeDirectory.ActiveDirectoryAttributes == nil {
		logger.Error("Active Directory attributes missing", "adUUID", activeDirectory.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("active directory attributes not populated"))
	}

	if activeDirectory.CredentialPath == "" {
		logger.Error("Active Directory credential path missing", "adUUID", activeDirectory.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("active directory credential path is empty"))
	}

	passwordSecret, err := adHelper.GetPasswordSecret(ctx, activeDirectory.CredentialPath)
	if err != nil {
		logger.Error("Failed to fetch Active Directory password", "adUUID", activeDirectory.UUID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if passwordSecret == nil || passwordSecret.SecretVersion == nil {
		logger.Error("Password secret or secret version is nil", "adUUID", activeDirectory.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("password secret fetch unsuccessful"))
	}

	password := passwordSecret.SecretVersion.Value
	encryptedPassword, err := utils.EncryptPassword(logmiddleware.Secret(password))
	if err != nil {
		logger.Error("failed to encrypt AD password", "error", err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to encrypt AD password: %w", err))
	}

	attributes := activeDirectory.ActiveDirectoryAttributes
	ad := &vsa.ActiveDirectory{
		UUID:                    activeDirectory.UUID,
		Domain:                  activeDirectory.Domain,
		DNS:                     activeDirectory.DNS,
		NetBIOS:                 activeDirectory.NetBIOS,
		Username:                activeDirectory.Username,
		Password:                logmiddleware.Secret(*encryptedPassword),
		ManagedAD:               &attributes.ManagedAD,
		PrimaryAD:               &attributes.PrimaryAD,
		OrganizationalUnit:      attributes.OrganizationalUnit,
		Site:                    &attributes.Site,
		Users:                   attributes.AdUsers,
		AesEncryption:           &attributes.AesEncryption,
		LdapOverTLS:             &attributes.LdapOverTLS,
		EncryptDCConnections:    &attributes.EncryptDCConnections,
		ServerRootCaCertificate: &attributes.ServerRootCaCertificate,
		LdapSigning:             &attributes.LdapSigning,
		KdcIP:                   attributes.KdcIP,
		AdName:                  activeDirectory.AdName,
		Status:                  activeDirectory.State,
	}

	return ad, nil
}

// _getPasswordSecret retrieves the password from the cloud provider's secret manager.
func _getPasswordSecret(ctx context.Context, secretID string) (*hyperscalermodels.CustomSecret, error) {
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}
	secret, err := cloudService.GetSecretWithLatestVersion(env.SecretManagerProjectID, secretID)
	if err != nil || secret == nil || secret.SecretVersion == nil {
		return nil, fmt.Errorf("failed to get secret for project: %s, userName: %s, err: %s", env.SecretManagerProjectID, secretID, err)
	}
	return secret, nil
}

func (a ActiveDirectoryActivity) buildNewCredentials(ctx context.Context, params common.UpdateActiveDirectoryParams, oldAd models.ActiveDirectory, oldVsaAd *vsa.ActiveDirectory) *vsa.ActiveDirectory {
	logger := util.GetLogger(ctx)
	newCredentials := &vsa.ActiveDirectory{}

	newCredentials.Domain = nillable.GetString(params.Domain, oldAd.Domain)
	newCredentials.DNS = nillable.GetString(params.DNS, oldAd.DNS)
	newCredentials.NetBIOS = nillable.GetString(params.NetBIOS, oldAd.NetBIOS)
	newCredentials.Username = nillable.GetString(params.Username, oldAd.Username)

	if params.Password != nil {
		newCredentials.Password = logmiddleware.Secret(*params.Password)
	} else {
		// use old password from oldVsaAd as oldAd object would have password masked
		if oldVsaAd == nil {
			logger.Error("old Active Directory password could not be retrieved")
			return nil
		}
		newCredentials.Password = oldVsaAd.Password
	}

	var users map[string][]string
	if params.BackupOperators != nil || params.Administrators != nil || params.SecurityOperators != nil {
		users = make(map[string][]string)

		if params.BackupOperators != nil {
			users[utils.ActiveDirectoryGroupBuiltInBackupOperators] = params.BackupOperators
		}
		if params.Administrators != nil {
			users[utils.ActiveDirectoryGroupBuiltInAdministrators] = params.Administrators
		}

		if params.SecurityOperators != nil {
			users[utils.ActiveDirectorySeSecurityPrivilege] = params.SecurityOperators
		}
	}
	newCredentials.Users = users

	// default to old OU if new OU is empty
	if params.OrganizationalUnit == nil || *params.OrganizationalUnit == "" {
		newCredentials.OrganizationalUnit = oldAd.ActiveDirectoryAttributes.OrganizationalUnit
	} else {
		newCredentials.OrganizationalUnit = *params.OrganizationalUnit
	}

	encryptDC := nillable.GetBool(params.EncryptDCConnections, oldAd.ActiveDirectoryAttributes.EncryptDCConnections)
	newCredentials.EncryptDCConnections = &encryptDC

	site := nillable.GetString(params.Site, oldAd.ActiveDirectoryAttributes.Site)
	newCredentials.Site = &site

	if params.AesEncryption != nil {
		newCredentials.AesEncryption = params.AesEncryption
	}

	if params.LdapSigning != nil {
		newCredentials.LdapSigning = params.LdapSigning
	}

	if params.AllowLocalNFSUsersWithLdap != nil {
		newCredentials.AllowLocalNFSUsersWithLdap = params.AllowLocalNFSUsersWithLdap
	}

	return newCredentials
}

func (a ActiveDirectoryActivity) UpdateActiveDirectoryState(ctx context.Context, activeDirectoryUuid string, adState string, adStateDetails string) error {
	logger := util.GetLogger(ctx)
	logger.Debug("Updating VCP ActiveDirectory DB Record", "activeDirectoryUuid", activeDirectoryUuid, "state", adState, "stateDetails", adStateDetails)

	adRecord, err := a.SE.GetActiveDirectoryByUUID(ctx, activeDirectoryUuid)
	if err != nil {
		return err
	}
	if adRecord == nil {
		return fmt.Errorf("active directory with uuid %s not found in VCP", activeDirectoryUuid)
	}

	if adRecord.State != adState {
		adRecord.State = adState
		adRecord.StateDetails = adStateDetails

		_, err = a.SE.UpdateActiveDirectory(ctx, adRecord)
		if err != nil {
			return err
		}
		logger.Info("Successfully updated VCP ActiveDirectory DB Record", "activeDirectoryUuid", activeDirectoryUuid, "state", adState, "stateDetails", adStateDetails)
	}
	return nil
}
