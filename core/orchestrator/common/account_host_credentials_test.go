package common

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestCreateAccountHostServiceUserCredentials_StableSecretID(t *testing.T) {
	origIsLocal := env.IsLocalEnv
	env.IsLocalEnv = func() bool { return false }
	t.Cleanup(func() { env.IsLocalEnv = origIsLocal })

	accountHostUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	serviceUsername := "a1b2c3d4_gadmin"
	cred := CreateAccountHostServiceUserCredentials(accountHostUUID, serviceUsername)

	require.NotNil(t, cred)
	assert.Equal(t, "account-host-secret-"+accountHostUUID, cred.SecretID)
}

func TestCreateAccountHostServiceUserCredentials_SecretManagerAuth(t *testing.T) {
	origIsLocal := env.IsLocalEnv
	env.IsLocalEnv = func() bool { return false }
	t.Cleanup(func() { env.IsLocalEnv = origIsLocal })

	serviceUsername := "gcnv_u_abcdefghij"
	cred := CreateAccountHostServiceUserCredentials(serviceUsername, serviceUsername)

	require.NotNil(t, cred)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, AccountHostServiceUserAuthType())
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, cred.AuthType)
	assert.Equal(t, serviceUsername, cred.Username)
	assert.Equal(t, "account-host-secret-"+serviceUsername, cred.SecretID)
	assert.Empty(t, cred.Password)
}

func TestCreateAccountHostServiceUserCredentials_LocalAuthWithoutAccountHostUUID(t *testing.T) {
	origIsLocal := env.IsLocalEnv
	env.IsLocalEnv = func() bool { return true }
	t.Cleanup(func() {
		env.IsLocalEnv = origIsLocal
		_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)
	})
	_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)

	serviceUsername := "gcnv_u_abcdefghij"
	cred := CreateAccountHostServiceUserCredentials(serviceUsername, serviceUsername)

	require.NotNil(t, cred)
	assert.Equal(t, env.USERNAME_PWD, AccountHostServiceUserAuthType())
	assert.Equal(t, env.USERNAME_PWD, cred.AuthType)
	assert.Equal(t, serviceUsername, cred.Username)
	assert.Equal(t, "account-host-secret-"+serviceUsername, cred.SecretID)
	assert.Empty(t, cred.Password)
}

func TestAccountHostServiceUserAuthType_EnvOverride(t *testing.T) {
	origIsLocal := env.IsLocalEnv
	env.IsLocalEnv = func() bool { return true }
	t.Cleanup(func() {
		env.IsLocalEnv = origIsLocal
		_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)
	})

	require.NoError(t, os.Setenv(accountHostServiceUserAuthTypeEnvVar, "1"))
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, AccountHostServiceUserAuthType())

	cred := CreateAccountHostServiceUserCredentials("account-host-uuid", "a1b2c3d4_gadmin")
	require.NotNil(t, cred)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, cred.AuthType)
}

func TestCreateAccountHostServiceUserCredentials_LocalStableSecretID(t *testing.T) {
	origIsLocal := env.IsLocalEnv
	env.IsLocalEnv = func() bool { return true }
	t.Cleanup(func() { env.IsLocalEnv = origIsLocal })

	accountHostUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	serviceUsername := "gcnv_u_abcdefghij"
	cred := CreateAccountHostServiceUserCredentials(accountHostUUID, serviceUsername)

	require.NotNil(t, cred)
	assert.Equal(t, env.USERNAME_PWD, cred.AuthType)
	assert.Equal(t, "account-host-secret-"+accountHostUUID, cred.SecretID)
}

func TestDeriveStablePasswordFromKey_Idempotent(t *testing.T) {
	stableKey := "account-host-secret-bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	first := DeriveStablePasswordFromKey(stableKey)
	second := DeriveStablePasswordFromKey(stableKey)
	assert.Equal(t, first, second)
	assert.True(t, len(first) >= 12)
}

func TestEncryptServiceCredentialPassword_EncryptsLocalPassword(t *testing.T) {
	cred := &datamodel.ExpertModeCredential{
		AuthType: env.USERNAME_PWD,
		Password: "plaintext-password",
	}
	require.NoError(t, EncryptServiceCredentialPassword(cred))
	assert.NotEmpty(t, cred.Password)
	assert.NotEqual(t, "plaintext-password", cred.Password)
}

func TestEncryptServiceCredentialPassword_NoOpForSecretManager(t *testing.T) {
	cred := &datamodel.ExpertModeCredential{
		AuthType: env.USERNAME_PWD_SEC_MGR,
		SecretID: "secret-id",
	}
	require.NoError(t, EncryptServiceCredentialPassword(cred))
	assert.Empty(t, cred.Password)
}
