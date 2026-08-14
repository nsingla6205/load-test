package activities

import (
	"context"
	"fmt"
	"strings"

	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

const accountHostClientCertEnableServerAuth = false

var (
	accountHostGetGCPService                               = _accountHostGetGCPService
	accountHostGetProviderForHost                          = vsa.GetProviderForHost
	accountHostVerifyHostServiceCredentials                = vsa.VerifyHostServiceCredentials
	verifySAWithCertAuth = vsa.VerifyHostServiceCredentialsWithCertificate
	accountHostEnsureExpertModeRole                        = vsa.EnsureExpertModeRole
	accountHostEnsureExpertModeAccount                     = vsa.EnsureExpertModeAccount
	createSAWithCertAuth                                     = vsa.EnsureExpertModeSAWithCertAuth
	accountHostDeleteExpertModeAccount                     = vsa.DeleteExpertModeAccount
	accountHostDeleteExpertModeRole                        = vsa.DeleteExpertModeRole
	accountHostGetRoleOwnerUUID                            = getAccountHostRoleOwnerUUID
	accountHostGetBucketFileContent                        = _accountHostGetBucketFileContent
	loadAccountHostRBACPrivileges                          = _loadAccountHostRBACPrivilegesFromBucket
	generateAccountHostServicePassword                     = _generateAccountHostServicePassword
	generateClientCertificate                   = vsa.GenerateAndCreateCertificateForVSACluster
	revokeAccountHostClientCertificate                     = vsa.RevokeCertificateAndDeleteFromCacheAndSecretManager
	accountHostBuildCertificatePoolCredentials             = orchcommon.BuildAccountHostCertificatePoolCredentials
)

func _accountHostGetGCPService(ctx context.Context) (hyperscaler.Services, error) {
	return hyperscaler.GetGCPService(ctx)
}

func _accountHostGetBucketFileContent(
	service hyperscaler.Services,
	ctx context.Context,
	bucketName, fileURL string,
) (*hyperscalermodels.BucketFileDetails, error) {
	return service.GetFileContentFromBucket(ctx, bucketName, fileURL)
}

// AccountHostActivity implements Temporal activities for account-host ONTAP provisioning.
type AccountHostActivity struct {
	SE database.Storage
}

// PreparedAccountHostMapping holds in-memory workflow state for ONTAP role and user
// provisioning. RoleOwnerUUID is transient (looked up from ONTAP during the workflow) and is
// never persisted on account_hosts.
type PreparedAccountHostMapping struct {
	MappingUUID   string
	HostUUID      string
	AdminUsername string
	RoleName      string
	RoleOwnerUUID string
	SvmPattern    string
	OntapVersion  string
}

// PrepareAccountHostMapping loads account-host context and RBAC metadata for provisioning.
func (a *AccountHostActivity) PrepareAccountHostMapping(
	ctx context.Context,
	accountHostUUID string,
) (*PreparedAccountHostMapping, error) {
	activity.RecordHeartbeat(ctx, "prepare-account-host-mapping")
	logger := util.GetLogger(ctx)

	accountHost, err := a.SE.GetAccountHost(ctx, accountHostUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if accountHost.HasProvisionedCredentials() {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("account host %s already has provisioned credentials", accountHostUUID)))
	}

	account, err := a.SE.GetAccountByInternalID(ctx, accountHost.AccountID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	accountUUID := strings.TrimSpace(account.UUID)
	if accountUUID == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("account %d has no UUID for account host %s", accountHost.AccountID, accountHostUUID)))
	}

	host, err := a.SE.GetHost(ctx, accountHost.HostID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	mgmtIP := strings.TrimSpace(host.IPAddress)
	if mgmtIP == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s has no management IP", host.UUID)))
	}

	baseHash := orchcommon.DeriveAccountHostBaseHash(accountHostUUID)
	roleName := orchcommon.BuildAccountHostRoleName(baseHash)
	serviceUsername := orchcommon.BuildAccountHostUsername(baseHash)
	svmPattern := orchcommon.BuildAccountHostSvmPattern(accountUUID)
	ontapVersion := ""
	if host.Attributes != nil {
		ontapVersion = utils.ExtractOntapRBACVersion(strings.TrimSpace(host.Attributes.Version))
	}

	logger.Info("Prepared account host for ONTAP role creation",
		"accountHostUUID", accountHostUUID, "hostUUID", host.UUID,
		"svmPattern", svmPattern, "roleName", roleName, "serviceUsername", serviceUsername)

	return &PreparedAccountHostMapping{
		MappingUUID:   accountHostUUID,
		HostUUID:      host.UUID,
		AdminUsername: serviceUsername,
		RoleName:      roleName,
		SvmPattern:    svmPattern,
		OntapVersion:  ontapVersion,
	}, nil
}

// CreateAccountHostRole parses configured RBAC privileges and creates the ONTAP role.
func (a *AccountHostActivity) CreateAccountHostRole(
	ctx context.Context,
	prepared *PreparedAccountHostMapping,
) (*PreparedAccountHostMapping, error) {
	activity.RecordHeartbeat(ctx, "create-account-host-role")
	logger := util.GetLogger(ctx)

	if prepared == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("prepared mapping is required")))
	}

	privileges, err := loadAccountHostRBACPrivileges(ctx, prepared.OntapVersion)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("failed to load account host RBAC privileges: %w", err)))
	}
	if prepared.RoleName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("provisioned role name is required")))
	}

	scopedRBAC := orchcommon.ApplyAccountHostRBACSubstitutions(
		privileges,
		orchcommon.AccountHostRBACSubstitutionInput{
			TargetRoleName: prepared.RoleName,
			SvmPattern:     prepared.SvmPattern,
		},
	)
	scopedPrivileges := toVSARolePrivileges(scopedRBAC)

	host, err := a.SE.GetHost(ctx, prepared.HostUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	provider, err := accountHostGetProviderForHost(ctx, host)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if err := accountHostEnsureExpertModeRole(ctx, provider, prepared.RoleName, scopedPrivileges); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	accountProvider, ok := provider.(vsa.ExpertModeAccountProvider)
	if !ok {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("ONTAP provider does not support security role owner lookup")))
	}
	roleOwnerUUID, err := accountHostGetRoleOwnerUUID(accountProvider, prepared.RoleName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	prepared.RoleOwnerUUID = roleOwnerUUID

	logger.Info("Created ONTAP expert-mode role for account host",
		"accountHostUUID", prepared.MappingUUID, "hostUUID", prepared.HostUUID,
		"roleName", prepared.RoleName, "roleOwnerUUID", prepared.RoleOwnerUUID)
	return prepared, nil
}

// CreateAccountHostUser creates the ONTAP service user (or verifies an existing one),
// persists credentials, and pins the account-host row.
func (a *AccountHostActivity) CreateAccountHostUser(
	ctx context.Context,
	prepared *PreparedAccountHostMapping,
) error {
	activity.RecordHeartbeat(ctx, "create-account-host-user")

	if prepared == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("prepared mapping is required")))
	}
	if strings.TrimSpace(prepared.RoleOwnerUUID) == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("role owner UUID is required before creating service user")))
	}

	serviceUsername := strings.TrimSpace(prepared.AdminUsername)
	if serviceUsername == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("service username is required before creating service user")))
	}
	cred := orchcommon.CreateAccountHostServiceUserCredentials(prepared.MappingUUID, serviceUsername)

	host, err := a.SE.GetHost(ctx, prepared.HostUUID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	switch cred.AuthType {
	case env.USER_CERTIFICATE:
		return a.ensureSAWithCertAuth(ctx, prepared, host, cred, serviceUsername)
	default:
		return a.ensureSAWithPasswordAuth(ctx, prepared, host, cred, serviceUsername)
	}
}

func (a *AccountHostActivity) ensureSAWithPasswordAuth(
	ctx context.Context,
	prepared *PreparedAccountHostMapping,
	host *datamodel.Host,
	cred *datamodel.ExpertModeCredential,
	serviceUsername string,
) error {
	logger := util.GetLogger(ctx)
	genPassword, err := generateAccountHostServicePassword(ctx, cred)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	provider, err := accountHostGetProviderForHost(ctx, host)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	accountProvider, ok := provider.(vsa.ExpertModeAccountProvider)
	if !ok {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("ONTAP provider does not support security account provisioning")))
	}

	if err := accountHostEnsureExpertModeAccount(ctx, accountProvider, vsa.CreateExpertModeAccountParams{
		Username:    serviceUsername,
		Password:    genPassword,
		RoleName:    prepared.RoleName,
		ClusterUUID: prepared.RoleOwnerUUID,
	}); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if err := accountHostVerifyHostServiceCredentials(ctx, host, prepared.SvmPattern, serviceUsername, genPassword); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if err := orchcommon.EncryptServiceCredentialPassword(cred); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("failed to encrypt service credential password: %w", err)))
	}

	if err := a.CompleteAccountHostMapping(ctx, prepared.MappingUUID, serviceUsername, cred); err != nil {
		return err
	}

	logger.Info("Created ONTAP expert-mode service user for account host",
		"accountHostUUID", prepared.MappingUUID, "hostUUID", prepared.HostUUID, "serviceUsername", serviceUsername)
	return nil
}

func (a *AccountHostActivity) ensureSAWithCertAuth(
	ctx context.Context,
	prepared *PreparedAccountHostMapping,
	host *datamodel.Host,
	cred *datamodel.ExpertModeCredential,
	serviceUsername string,
) error {
	logger := util.GetLogger(ctx)
	poolCredentials, err := accountHostBuildCertificatePoolCredentials(host, cred)
	if err != nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}

	cloudService, err := accountHostGetGCPService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
	}

	externalClusterName := host.ExternalName
	if externalClusterName == "" {
		externalClusterName = host.Name
	}
	if externalClusterName == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s external cluster name is required for certificate generation", host.UUID)))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("generating client certificate for account host service user %s", serviceUsername))
	if _, err := generateClientCertificate(
		cloudService,
		externalClusterName,
		serviceUsername,
		poolCredentials,
		accountHostClientCertEnableServerAuth,
	); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	provider, err := accountHostGetProviderForHost(ctx, host)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	accountProvider, ok := provider.(vsa.ExpertModeAccountProvider)
	if !ok {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("ONTAP provider does not support security account provisioning")))
	}

	if err := createSAWithCertAuth(ctx, accountProvider, vsa.CreateExpertModeAccountParams{
		Username:    serviceUsername,
		RoleName:    prepared.RoleName,
		ClusterUUID: prepared.RoleOwnerUUID,
	}); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if err := verifySAWithCertAuth(
		ctx, host, prepared.SvmPattern, serviceUsername, poolCredentials,
	); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if err := a.CompleteAccountHostMapping(ctx, prepared.MappingUUID, serviceUsername, cred); err != nil {
		return err
	}

	logger.Info("Created ONTAP expert-mode service user with certificate auth for account host",
		"accountHostUUID", prepared.MappingUUID, "hostUUID", prepared.HostUUID,
		"serviceUsername", serviceUsername, "certificateID", cred.CertificateID)
	return nil
}

func _generateAccountHostServicePassword(
	ctx context.Context,
	cred *datamodel.ExpertModeCredential,
) (string, error) {
	if cred == nil {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("service credential is required"))
	}

	switch cred.AuthType {
	case env.USERNAME_PWD_SEC_MGR:
		if strings.TrimSpace(cred.SecretID) == "" {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("service credential secret_id is required"))
		}
		gcpService, err := accountHostGetGCPService(ctx)
		if err != nil {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err)
		}
		secret, err := vsa.GeneratePasswordForVSACluster(gcpService, cred.SecretID)
		if err != nil {
			return "", err
		}
		return secret.SecretVersion.Value, nil
	case env.USERNAME_PWD:
		stableKey := strings.TrimSpace(cred.SecretID)
		if stableKey == "" {
			password, err := utils.GenerateStrongPassword(12)
			if err != nil {
				return "", err
			}
			cred.Password = password
			return password, nil
		}
		password := orchcommon.DeriveStablePasswordFromKey(stableKey)
		cred.Password = password
		return password, nil
	case env.USER_CERTIFICATE:
		return "", vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("password generation is not used for certificate auth type"))
	default:
		return "", vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("unsupported account host service user auth type %d", cred.AuthType))
	}
}

// CompleteAccountHostMapping persists service credentials and pins the account-host row.
func (a *AccountHostActivity) CompleteAccountHostMapping(
	ctx context.Context,
	accountHostUUID, serviceUsername string,
	cred *datamodel.ExpertModeCredential,
) error {
	activity.RecordHeartbeat(ctx, "complete-account-host-mapping")
	if cred != nil && strings.TrimSpace(cred.Username) == "" {
		cred.Username = serviceUsername
	}
	if err := a.SE.UpdateAccountHostServiceCredentials(ctx, accountHostUUID, cred); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if err := a.SE.SetAccountHostPinned(ctx, accountHostUUID, true); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// RollbackAccountHostOntapProvisioning removes ONTAP resources created during a failed
// onboard attempt. It is best-effort: partial cleanup failures are logged and do not fail the activity.
func (a *AccountHostActivity) RollbackAccountHostOntapProvisioning(
	ctx context.Context,
	prepared *PreparedAccountHostMapping,
) error {
	activity.RecordHeartbeat(ctx, "rollback-account-host-ontap-provisioning")
	logger := util.GetLogger(ctx)

	if prepared == nil {
		return nil
	}

	roleName := strings.TrimSpace(prepared.RoleName)
	roleOwnerUUID := strings.TrimSpace(prepared.RoleOwnerUUID)
	if roleName == "" || roleOwnerUUID == "" {
		logger.Info("Skipping ONTAP onboard rollback: role metadata missing",
			"mappingUUID", prepared.MappingUUID, "roleName", roleName, "roleOwnerUUID", roleOwnerUUID)
		return nil
	}

	host, err := a.SE.GetHost(ctx, prepared.HostUUID)
	if err != nil {
		logger.Errorf("Failed to load host for ONTAP onboard rollback: %v", err)
		return nil
	}

	provider, err := accountHostGetProviderForHost(ctx, host)
	if err != nil {
		logger.Errorf("Failed to get ONTAP provider for onboard rollback: %v", err)
		return nil
	}

	serviceUsername := strings.TrimSpace(prepared.AdminUsername)
	if serviceUsername != "" {
		if accountProvider, ok := provider.(vsa.ExpertModeAccountProvider); ok {
			if err := accountHostDeleteExpertModeAccount(ctx, accountProvider, serviceUsername, roleOwnerUUID); err != nil {
				logger.Errorf("Failed to delete ONTAP service user %s during onboard rollback: %v", serviceUsername, err)
			}
		}
	}

	if orchcommon.AccountHostServiceUserAuthType() == env.USER_CERTIFICATE {
		cred := orchcommon.CreateAccountHostServiceUserCredentials(prepared.MappingUUID, serviceUsername)
		poolCredentials, buildErr := accountHostBuildCertificatePoolCredentials(host, cred)
		if buildErr != nil {
			logger.Errorf("Failed to build account host certificate credentials for rollback: %v", buildErr)
		} else {
			cloudService, gcpErr := accountHostGetGCPService(ctx)
			if gcpErr != nil {
				logger.Errorf("Failed to initialize GCP service for account host certificate rollback: %v", gcpErr)
			} else if revokeErr := revokeAccountHostClientCertificate(cloudService, poolCredentials); revokeErr != nil {
				logger.Errorf("Failed to revoke account host client certificate during rollback: %v", revokeErr)
			}
		}
	}

	if err := accountHostDeleteExpertModeRole(ctx, provider, roleName, roleOwnerUUID); err != nil {
		logger.Errorf("Failed to delete ONTAP role %s during onboard rollback: %v", roleName, err)
		return nil
	}

	logger.Info("Rolled back ONTAP provisioning for failed account host mapping",
		"mappingUUID", prepared.MappingUUID, "hostUUID", prepared.HostUUID,
		"roleName", roleName, "serviceUsername", serviceUsername)
	return nil
}

func getAccountHostRoleOwnerUUID(provider vsa.ExpertModeAccountProvider, roleName string) (string, error) {
	return provider.GetRoleOwnerUUID(roleName)
}

func toVSARolePrivileges(privileges []orchcommon.ExpertModeRBACPrivilege) []*vsa.RolePrivilege {
	out := make([]*vsa.RolePrivilege, 0, len(privileges))
	for _, p := range privileges {
		out = append(out, &vsa.RolePrivilege{
			Path:   p.Path,
			Access: p.Access,
			Query:  p.Query,
		})
	}
	return out
}

func (a *AccountHostActivity) loadRBACPrivilegesFromConfiguredSource(
	ctx context.Context,
	ontapVersion string,
) ([]orchcommon.ExpertModeRBACPrivilege, error) {
	return loadAccountHostRBACPrivileges(ctx, ontapVersion)
}

func _loadAccountHostRBACPrivilegesFromBucket(
	ctx context.Context,
	ontapVersion string,
) ([]orchcommon.ExpertModeRBACPrivilege, error) {
	trimmedVersion := strings.TrimSpace(ontapVersion)
	filePathTemplate := strings.TrimSpace(orchcommon.AccountHostRBACFileSourcePath)
	if filePathTemplate == "" {
		return nil, fmt.Errorf("ACCOUNT_HOST_RBAC_FILE_PATH must not be empty")
	}
	if strings.Contains(filePathTemplate, "%s") && trimmedVersion == "" {
		return nil, fmt.Errorf("ONTAP version is required to render ACCOUNT_HOST_RBAC_FILE_PATH %q", filePathTemplate)
	}
	bucketName := strings.TrimSpace(orchcommon.AccountHostRBACFileSourceBucketName)
	if bucketName == "" {
		return nil, fmt.Errorf("ACCOUNT_HOST_RBAC_BUCKET_NAME must not be empty")
	}

	rbacFilePath := utils.GenerateRbacFilePath(filePathTemplate, trimmedVersion)
	gcpService, err := accountHostGetGCPService(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize gcp service for account host RBAC file: %w", err)
	}
	bucketFileDetails, err := accountHostGetBucketFileContent(gcpService, ctx, bucketName, rbacFilePath)
	if err != nil {
		return nil, fmt.Errorf("fetch account host RBAC file from bucket gs://%s/%s: %w", bucketName, rbacFilePath, err)
	}
	if bucketFileDetails == nil || strings.TrimSpace(bucketFileDetails.FileContent) == "" {
		return nil, fmt.Errorf("account host RBAC file gs://%s/%s is empty", bucketName, rbacFilePath)
	}

	privileges, err := orchcommon.ParseAccountHostRBACPrivilegesFromConfig(
		bucketFileDetails.FileContent,
		fmt.Sprintf("account host RBAC file gs://%s/%s", bucketName, rbacFilePath),
	)
	if err != nil {
		return nil, err
	}
	util.GetLogger(ctx).Info("Loaded account host RBAC privileges from bucket file",
		"bucketName", bucketName,
		"filePath", rbacFilePath,
		"fileHash", bucketFileDetails.FileHashSHA256,
		"privilegeCount", len(privileges),
	)
	return privileges, nil
}
