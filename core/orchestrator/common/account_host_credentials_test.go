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
	t.Cleanup(func() {
		env.IsLocalEnv = origIsLocal
		_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)
	})
	_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)

	serviceUsername := "gcnv_u_abcdefghij"
	cred := CreateAccountHostServiceUserCredentials(serviceUsername, serviceUsername)

	require.NotNil(t, cred)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, AccountHostServiceUserAuthType())
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, cred.AuthType)
	assert.Equal(t, serviceUsername, cred.Username)
	assert.Equal(t, "account-host-secret-"+serviceUsername, cred.SecretID)
	assert.Empty(t, cred.CertificateID)
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
	assert.Empty(t, cred.CertificateID)
}

func TestAccountHostServiceUserAuthType_EnvOverride(t *testing.T) {
	origIsLocal := env.IsLocalEnv
	env.IsLocalEnv = func() bool { return true }
	t.Cleanup(func() {
		env.IsLocalEnv = origIsLocal
		_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)
	})

	// An explicit override must be honored as-is; all three auth types remain
	// selectable (e.g. for local testing or rollback), not just USER_CERTIFICATE.
	require.NoError(t, os.Setenv(accountHostServiceUserAuthTypeEnvVar, "1"))
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, AccountHostServiceUserAuthType())

	cred := CreateAccountHostServiceUserCredentials("account-host-uuid", "a1b2c3d4_gadmin")
	require.NotNil(t, cred)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, cred.AuthType)
}

func TestCreateAccountHostServiceUserCredentials_CertificateAuth(t *testing.T) {
	origIsLocal := env.IsLocalEnv
	env.IsLocalEnv = func() bool { return false }
	t.Cleanup(func() {
		env.IsLocalEnv = origIsLocal
		_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)
	})

	require.NoError(t, os.Setenv(accountHostServiceUserAuthTypeEnvVar, "2"))

	accountHostUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	serviceUsername := "gcnv_u_abcdefghij"
	cred := CreateAccountHostServiceUserCredentials(accountHostUUID, serviceUsername)

	require.NotNil(t, cred)
	assert.Equal(t, env.USER_CERTIFICATE, AccountHostServiceUserAuthType())
	assert.Equal(t, env.USER_CERTIFICATE, cred.AuthType)
	assert.Equal(t, serviceUsername, cred.Username)
	assert.Equal(t, "account-host-secret-"+accountHostUUID, cred.SecretID)
	assert.Equal(t, "account-host-cert-"+accountHostUUID, cred.CertificateID)
	assert.Empty(t, cred.Password)
}

func TestBuildAccountHostCertificatePoolCredentials(t *testing.T) {
	t.Run("uses host CaURI when host credentials present", func(t *testing.T) {
		host := &datamodel.Host{
			IPAddress: "10.0.0.5",
			HostCredentials: &datamodel.PoolCredentials{
				CaURI: "project/pool/ca",
			},
		}
		cred := &datamodel.ExpertModeCredential{
			AuthType:      env.USER_CERTIFICATE,
			Username:      "gcnv_u_test",
			CertificateID: "account-host-cert-uuid",
			SecretID:      "account-host-secret-uuid",
		}

		poolCreds, err := BuildAccountHostCertificatePoolCredentials(host, cred)
		require.NoError(t, err)
		require.NotNil(t, poolCreds)
		assert.Equal(t, env.USER_CERTIFICATE, poolCreds.AuthType)
		assert.Equal(t, "gcnv_u_test", poolCreds.Username)
		assert.Equal(t, "account-host-cert-uuid", poolCreds.CertificateID)
		assert.Equal(t, "account-host-secret-uuid", poolCreds.SecretID)
		assert.Equal(t, "project/pool/ca", poolCreds.CaURI)
	})

	t.Run("falls back to env BuildCaURI like host onboard when host credentials nil", func(t *testing.T) {
		origProject, origPool, origCA := env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName
		env.CaPoolDeployedProjectID = "env-project"
		env.CaPoolName = "env-pool"
		env.CaName = "env-ca"
		t.Cleanup(func() {
			env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName = origProject, origPool, origCA
		})

		host := &datamodel.Host{IPAddress: "10.0.0.5"}
		cred := &datamodel.ExpertModeCredential{
			AuthType:      env.USER_CERTIFICATE,
			Username:      "gcnv_u_test",
			CertificateID: "account-host-cert-uuid",
			SecretID:      "account-host-secret-uuid",
		}

		poolCreds, err := BuildAccountHostCertificatePoolCredentials(host, cred)
		require.NoError(t, err)
		require.NotNil(t, poolCreds)
		assert.Equal(t, "env-project/env-pool/env-ca", poolCreds.CaURI)
	})
}

func TestCreateAccountHostServiceUserCredentials_LocalStableSecretID(t *testing.T) {
	origIsLocal := env.IsLocalEnv
	env.IsLocalEnv = func() bool { return true }
	t.Cleanup(func() {
		env.IsLocalEnv = origIsLocal
		_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)
	})
	_ = os.Unsetenv(accountHostServiceUserAuthTypeEnvVar)

	accountHostUUID := "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	serviceUsername := "gcnv_u_abcdefghij"
	cred := CreateAccountHostServiceUserCredentials(accountHostUUID, serviceUsername)

	require.NotNil(t, cred)
	assert.Equal(t, env.USERNAME_PWD, cred.AuthType)
	assert.Equal(t, "account-host-secret-"+accountHostUUID, cred.SecretID)
	assert.Empty(t, cred.CertificateID)
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

