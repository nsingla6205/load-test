package vsa

import (
	"context"
	"fmt"
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type ExpertModeAccountProvider interface {
	GetClusterUUID() (string, error)
	GetRoleOwnerUUID(roleName string) (string, error)
	CreateSecurityAccount(params CreateSecurityAccountParams) error
	ModifySecurityAccount(params ModifySecurityAccountParams) error
	GetSecurityAccount(params GetSecurityAccountParams) (*SecurityAccount, error)
	DeleteSecurityAccount(params DeleteSecurityAccountParams) error
}

// CreateExpertModeAccountParams describes inputs for idempotent expert-mode account creation.
type CreateExpertModeAccountParams struct {
	Username    string
	Password    string
	RoleName    string
	ClusterUUID string
}

// EnsureExpertModeAccount creates the expert-mode ONTAP security account if it does not already exist.
func EnsureExpertModeAccount(ctx context.Context, provider ExpertModeAccountProvider, params CreateExpertModeAccountParams) error {
	logger := util.GetLogger(ctx)
	if provider == nil {
		return fmt.Errorf("expert mode account provider is required")
	}
	if strings.TrimSpace(params.Username) == "" {
		return fmt.Errorf("expert mode account username is required")
	}
	if strings.TrimSpace(params.Password) == "" {
		return fmt.Errorf("expert mode account password is required")
	}
	if strings.TrimSpace(params.RoleName) == "" {
		return fmt.Errorf("expert mode account role name is required")
	}

	clusterUUID := strings.TrimSpace(params.ClusterUUID)
	if clusterUUID == "" && params.RoleName != "" {
		var err error
		clusterUUID, err = provider.GetRoleOwnerUUID(params.RoleName)
		if err != nil {
			logger.Errorf("Failed to get ONTAP role owner UUID for account %s: %v", params.Username, err)
			return err
		}
	}
	if clusterUUID == "" {
		var err error
		clusterUUID, err = provider.GetClusterUUID()
		if err != nil {
			logger.Errorf("Failed to get ONTAP cluster UUID for account %s: %v", params.Username, err)
			return err
		}
	}

	if _, err := provider.GetSecurityAccount(GetSecurityAccountParams{Name: params.Username}); err == nil {
		logger.Infof("ONTAP security account %s already exists, skipping creation", params.Username)
		return nil
	} else if !utilserrors.IsNotFoundErrForObjectTypeInChain(err, "SecurityAccount") {
		logger.Errorf("Failed to lookup ONTAP security account %s: %v", params.Username, err)
		return err
	}

	err := provider.CreateSecurityAccount(CreateSecurityAccountParams{
		Name:        params.Username,
		Password:    params.Password,
		RoleName:    params.RoleName,
		ClusterUUID: clusterUUID,
	})
	if err == nil {
		logger.Infof("Successfully created ONTAP security account %s", params.Username)
		return nil
	}
	if strings.Contains(err.Error(), "already exists") {
		logger.Infof("ONTAP security account %s already exists, skipping creation", params.Username)
		return nil
	}
	logger.Errorf("Failed to create ONTAP security account %s: %v", params.Username, err)
	return err
}

// EnsureExpertModeSAWithCertAuth creates the expert-mode account for certificate-based
// REST authentication (no bootstrap password) or enables certificate auth on an existing account.
func EnsureExpertModeSAWithCertAuth(
	ctx context.Context,
	provider ExpertModeAccountProvider,
	params CreateExpertModeAccountParams,
) error {
	logger := util.GetLogger(ctx)
	if provider == nil {
		return fmt.Errorf("expert mode account provider is required")
	}
	if params.Username == "" {
		return fmt.Errorf("expert mode account username is required")
	}
	if params.RoleName == "" {
		return fmt.Errorf("expert mode account role name is required")
	}
	clusterUUID := params.ClusterUUID
	if clusterUUID == "" {
		return fmt.Errorf("expert mode account cluster uuid is required")
	}

	accountExists := false
	if _, err := provider.GetSecurityAccount(GetSecurityAccountParams{Name: params.Username}); err == nil {
		accountExists = true
		logger.Infof("ONTAP security account %s already exists, ensuring certificate auth", params.Username)
	} else if !utilserrors.IsNotFoundErrForObjectTypeInChain(err, "SecurityAccount") {
		logger.Errorf("Failed to lookup ONTAP security account %s: %v", params.Username, err)
		return err
	}

	if !accountExists {
		err := provider.CreateSecurityAccount(CreateSecurityAccountParams{
			Name:                  params.Username,
			RoleName:              params.RoleName,
			ClusterUUID:           clusterUUID,
			AuthenticationMethods: []string{"certificate"},
		})
		if err == nil {
			logger.Infof("Successfully created ONTAP security account %s with certificate auth", params.Username)
			return nil
		}
		if strings.Contains(err.Error(), "already exists") {
			accountExists = true
			logger.Infof("ONTAP security account %s already exists, ensuring certificate auth", params.Username)
		} else {
			logger.Errorf("Failed to create ONTAP security account %s: %v", params.Username, err)
			return err
		}
	}

	err := provider.ModifySecurityAccount(ModifySecurityAccountParams{
		Name:      params.Username,
		OwnerUUID: clusterUUID,
		Applications: []ontapRest.SecurityAccountApplication{
			{
				Application:           "http",
				AuthenticationMethods: []string{"password", "certificate"},
			},
			{
				Application:           "ontapi",
				AuthenticationMethods: []string{"password", "certificate"},
			},
		},
	})
	if err != nil {
		logger.Errorf("Failed to enable certificate auth for ONTAP security account %s: %v", params.Username, err)
		return err
	}
	logger.Infof("Enabled certificate auth for ONTAP security account %s", params.Username)
	return nil
}

// CreateReadOnlyServiceAccountParams describes inputs for the idempotent
// certificate-auth, read-only ONTAP account used by an external (non-VCP) service.
// Trust for the presented client certificate comes from the cluster-scoped
// client_ca installed separately via InstallClientCA; ONTAP maps the cert CN
// to this account name. Do not pass ssl_ca_certificate on account create —
// this ONTAP REST surface rejects that field.
type CreateReadOnlyServiceAccountParams struct {
	// Username is the account name and must equal the CN of the service client
	// certificate so ONTAP maps the presented cert to this account.
	Username string
	// RoleName is the ONTAP role assigned to the account (built-in "readonly").
	RoleName string
	// ClusterUUID, when empty, is resolved from RoleName then the cluster.
	ClusterUUID string
}

// EnsureReadOnlyServiceAccount creates the certificate-auth, read-only ONTAP
// security account for an external service if it does not already exist. It is
// idempotent (existing account or "already exists" errors are treated as
// success) and mirrors EnsureExpertModeAccount, but binds the account to
// certificate authentication rather than a password.
func EnsureReadOnlyServiceAccount(ctx context.Context, provider ExpertModeAccountProvider, params CreateReadOnlyServiceAccountParams) error {
	logger := util.GetLogger(ctx)
	if provider == nil {
		return fmt.Errorf("read-only service account provider is required")
	}
	if strings.TrimSpace(params.Username) == "" {
		return fmt.Errorf("read-only service account username is required")
	}
	if strings.TrimSpace(params.RoleName) == "" {
		return fmt.Errorf("read-only service account role name is required")
	}

	clusterUUID := strings.TrimSpace(params.ClusterUUID)
	if clusterUUID == "" {
		var err error
		clusterUUID, err = provider.GetRoleOwnerUUID(params.RoleName)
		if err != nil {
			logger.Errorf("Failed to get ONTAP role owner UUID for service account %s: %v", params.Username, err)
			return err
		}
	}
	if clusterUUID == "" {
		var err error
		clusterUUID, err = provider.GetClusterUUID()
		if err != nil {
			logger.Errorf("Failed to get ONTAP cluster UUID for service account %s: %v", params.Username, err)
			return err
		}
	}

	if _, err := provider.GetSecurityAccount(GetSecurityAccountParams{Name: params.Username}); err == nil {
		logger.Infof("ONTAP read-only service account %s already exists, skipping creation", params.Username)
		return nil
	} else if !utilserrors.IsNotFoundErrForObjectTypeInChain(err, "SecurityAccount") {
		logger.Errorf("Failed to lookup ONTAP read-only service account %s: %v", params.Username, err)
		return err
	}

	err := provider.CreateSecurityAccount(CreateSecurityAccountParams{
		Name:                  params.Username,
		RoleName:              params.RoleName,
		ClusterUUID:           clusterUUID,
		AuthenticationMethods: []string{"certificate"},
	})
	if err == nil {
		logger.Infof("Successfully created ONTAP read-only service account %s", params.Username)
		return nil
	}
	if strings.Contains(err.Error(), "already exists") {
		logger.Infof("ONTAP read-only service account %s already exists, skipping creation", params.Username)
		return nil
	}
	logger.Errorf("Failed to create ONTAP read-only service account %s: %v", params.Username, err)
	return err
}

// DeleteExpertModeAccount deletes the expert-mode ONTAP security account if it exists.
func DeleteExpertModeAccount(ctx context.Context, provider ExpertModeAccountProvider, username, ownerUUID string) error {
	logger := util.GetLogger(ctx)
	if provider == nil {
		return nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil
	}

	ownerUUID = strings.TrimSpace(ownerUUID)
	if ownerUUID == "" {
		account, err := provider.GetSecurityAccount(GetSecurityAccountParams{Name: username})
		if err != nil {
			if utilserrors.IsNotFoundErrForObjectTypeInChain(err, "SecurityAccount") {
				logger.Infof("ONTAP security account %s not found, skipping delete", username)
				return nil
			}
			logger.Errorf("Failed to lookup ONTAP security account %s: %v", username, err)
			return err
		}
		if account == nil || strings.TrimSpace(account.OwnerUUID) == "" {
			logger.Infof("ONTAP security account %s has no owner uuid, skipping delete", username)
			return nil
		}
		ownerUUID = account.OwnerUUID
	}

	err := provider.DeleteSecurityAccount(DeleteSecurityAccountParams{
		Name:      username,
		OwnerUUID: ownerUUID,
	})
	if err == nil {
		logger.Infof("Deleted ONTAP security account %s", username)
		return nil
	}
	if utilserrors.IsNotFoundErrForObjectTypeInChain(err, "SecurityAccount") {
		logger.Infof("ONTAP security account %s not found, skipping delete", username)
		return nil
	}
	logger.Errorf("Failed to delete ONTAP security account %s: %v", username, err)
	return err
}
