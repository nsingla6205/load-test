package datamodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestAccountHost_TableName(t *testing.T) {
	assert.Equal(t, "account_hosts", (AccountHost{}).TableName())
}

func TestAccountHost_HasProvisionedCredentials(t *testing.T) {
	assert.False(t, (*AccountHost)(nil).HasProvisionedCredentials())
	assert.False(t, (&AccountHost{}).HasProvisionedCredentials())
	assert.False(t, (&AccountHost{OntapModeCredentials: &ExpertModeCredential{Username: "user"}}).HasProvisionedCredentials())
	assert.False(t, (&AccountHost{OntapModeCredentials: &ExpertModeCredential{
		AuthType: env.USERNAME_PWD_SEC_MGR,
		Username: "user",
	}}).HasProvisionedCredentials())
	assert.False(t, (&AccountHost{OntapModeCredentials: &ExpertModeCredential{
		AuthType: env.USERNAME_PWD,
		Username: "user",
	}}).HasProvisionedCredentials())

	assert.True(t, (&AccountHost{OntapModeCredentials: &ExpertModeCredential{
		AuthType: env.USERNAME_PWD_SEC_MGR,
		Username: "user",
		SecretID: "projects/p/secrets/s/versions/1",
	}}).HasProvisionedCredentials())
	assert.True(t, (&AccountHost{OntapModeCredentials: &ExpertModeCredential{
		AuthType: env.USERNAME_PWD,
		Username: "user",
		Password: "encrypted-password",
	}}).HasProvisionedCredentials())
	assert.True(t, (&AccountHost{OntapModeCredentials: &ExpertModeCredential{
		AuthType:      env.USER_CERTIFICATE,
		Username:      "user",
		CertificateID: "cert-1",
	}}).HasProvisionedCredentials())
}

func TestAccountHost_GetServiceUsername(t *testing.T) {
	assert.Equal(t, "", (*AccountHost)(nil).GetServiceUsername())
	assert.Equal(t, "svc_user", (&AccountHost{
		OntapModeCredentials: &ExpertModeCredential{Username: "svc_user"},
	}).GetServiceUsername())
}

func TestAccountHost_IsPinnedWithCredentials(t *testing.T) {
	assert.False(t, (*AccountHost)(nil).IsPinnedWithCredentials())
	assert.False(t, (&AccountHost{Pinned: true}).IsPinnedWithCredentials())
	assert.True(t, (&AccountHost{
		Pinned: true,
		OntapModeCredentials: &ExpertModeCredential{
			AuthType: env.USERNAME_PWD_SEC_MGR,
			Username: "svc_user",
			SecretID: "projects/p/secrets/s/versions/1",
		},
	}).IsPinnedWithCredentials())
}

func TestAccountHost_HasDedicatedProxyCredentials(t *testing.T) {
	assert.False(t, (*AccountHost)(nil).HasDedicatedProxyCredentials())

	inlinePassword := &AccountHost{OntapModeCredentials: &ExpertModeCredential{
		AuthType: env.USERNAME_PWD,
		Username: "user",
		Password: "encrypted-password",
	}}
	assert.True(t, inlinePassword.HasProvisionedCredentials())
	assert.True(t, inlinePassword.HasDedicatedProxyCredentials())

	smPassword := &AccountHost{OntapModeCredentials: &ExpertModeCredential{
		AuthType: env.USERNAME_PWD_SEC_MGR,
		Username: "user",
		SecretID: "secret-id",
	}}
	assert.True(t, smPassword.HasDedicatedProxyCredentials())

	cert := &AccountHost{OntapModeCredentials: &ExpertModeCredential{
		AuthType:      env.USER_CERTIFICATE,
		Username:      "user",
		CertificateID: "cert-id",
		SecretID:      "secret-id",
	}}
	assert.True(t, cert.HasDedicatedProxyCredentials())
}
