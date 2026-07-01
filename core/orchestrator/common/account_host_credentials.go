package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

const (
	AccountHostCredentialsIDPrefix         = "account-host"
	accountHostServiceUserAuthTypeEnvUnset = -1
	accountHostServiceUserAuthTypeEnvVar   = "ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE"
)

// AccountHostServiceUserAuthType returns how external cluster service user passwords are stored.
// Local dev defaults to encrypted password in Postgres; non-local deployments use Secret Manager.
// Set ACCOUNT_HOST_SERVICE_USER_AUTH_TYPE to override (0=USERNAME_PWD, 1=USERNAME_PWD_SEC_MGR).
func AccountHostServiceUserAuthType() int {
	if configured := env.GetInt(accountHostServiceUserAuthTypeEnvVar, accountHostServiceUserAuthTypeEnvUnset); configured != accountHostServiceUserAuthTypeEnvUnset {
		return configured
	}
	if env.IsLocalEnv() {
		return env.USERNAME_PWD
	}
	return env.USERNAME_PWD_SEC_MGR
}

// CreateAccountHostServiceUserCredentials builds credential metadata with a stable SecretID for retries.
func CreateAccountHostServiceUserCredentials(accountHostUUID, serviceUsername string) *datamodel.ExpertModeCredential {
	secretKey := strings.TrimSpace(accountHostUUID)
	if secretKey == "" {
		secretKey = serviceUsername
	}
	return createAccountHostServiceUserCredentials(secretKey, serviceUsername)
}

func createAccountHostServiceUserCredentials(secretKey, serviceUsername string) *datamodel.ExpertModeCredential {
	authType := AccountHostServiceUserAuthType()

	return &datamodel.ExpertModeCredential{
		AuthType: authType,
		Username: serviceUsername,
		SecretID: fmt.Sprintf("%s-secret-%s", AccountHostCredentialsIDPrefix, secretKey),
	}
}

// DeriveStablePasswordFromKey returns a deterministic password for idempotent workflow retries.
func DeriveStablePasswordFromKey(stableKey string) string {
	sum := sha256.Sum256([]byte("vcp-account-host-pwd:" + strings.TrimSpace(stableKey)))
	// Prefix guarantees complexity; suffix is derived from the stable key hash.
	return "Aa1!" + hex.EncodeToString(sum[:8])
}

// EncryptServiceCredentialPassword encrypts the password field when present (local auth).
func EncryptServiceCredentialPassword(cred *datamodel.ExpertModeCredential) error {
	if cred == nil || cred.Password == "" {
		return nil
	}
	encrypted, err := utils.EncryptPassword(log.Secret(cred.Password))
	if err != nil {
		return err
	}
	cred.Password = *encrypted
	return nil
}
