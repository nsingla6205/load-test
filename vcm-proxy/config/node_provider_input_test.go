package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// certOntapCredential builds a certificate-auth credential for the given role.
func certOntapCredential(role vcmserver.OntapCredentialV1Role, certSecretID, certPrivateKeySecretID string) vcmserver.OntapCredentialV1 {
	entry := vcmserver.OntapCredentialV1{Role: role}
	entry.ActiveAuthType.SetTo(vcmserver.OntapCredentialV1ActiveAuthTypeCERT)
	cert := vcmserver.CertificateAuthCredsV1{}
	if certSecretID != "" {
		cert.CertSecretId.SetTo(certSecretID)
	}
	if certPrivateKeySecretID != "" {
		cert.CertPrivateKeySecretId.SetTo(certPrivateKeySecretID)
	}
	entry.CertificateAuthCreds.SetTo(cert)
	return entry
}

// passwordOntapCredential builds a password-auth credential for the given role.
func passwordOntapCredential(role vcmserver.OntapCredentialV1Role, username, passwordSecretID string) vcmserver.OntapCredentialV1 {
	entry := vcmserver.OntapCredentialV1{Role: role}
	entry.ActiveAuthType.SetTo(vcmserver.OntapCredentialV1ActiveAuthTypePWD)
	pwd := vcmserver.PasswordAuthCredsV1{}
	if username != "" {
		pwd.Username.SetTo(username)
	}
	if passwordSecretID != "" {
		pwd.PasswordSecretId.SetTo(passwordSecretID)
	}
	entry.PasswordAuthCreds.SetTo(pwd)
	return entry
}

// defaultOntapCredentials returns a single VCM_ADMIN password credential used as
// the baseline for configuredStore.
func defaultOntapCredentials(passwordSecretID string) []vcmserver.OntapCredentialV1 {
	return []vcmserver.OntapCredentialV1{
		passwordOntapCredential(vcmserver.OntapCredentialV1RoleADMIN, "root", passwordSecretID),
	}
}

func configuredStore(t *testing.T, mutate func(*vcmserver.SetConfigRequestV1)) *Store {
	t.Helper()
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
		seed.DeploymentId = vcmserver.NewOptString("gcnv-test")
		seed.OntapCredentials = defaultOntapCredentials("secret-1")
	})
	mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
		layout.IpConfig.SetTo(vcmserver.IpConfigV1{
			VmLifs: []vcmserver.VmLifV1{
				{
					Name:     vcmserver.NewOptString("vm1"),
					Nodemgmt: vcmserver.NewOptString("198.18.0.5"),
				},
			},
		})
	})
	if mutate != nil {
		mutate(req)
	}
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })
	return store
}

func TestNodeProviderInput_PasswordAuth(t *testing.T) {
	input, err := configuredStore(t, nil).NodeProviderInput()
	require.NoError(t, err)
	assert.Equal(t, "gcnv-test", input.DeploymentName)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, input.OntapCredentials.AuthType)
	assert.Equal(t, "secret-1", input.OntapCredentials.SecretID)
	assert.Equal(t, "root", input.OntapCredentials.Username)
	assert.Equal(t, "", input.OntapCredentials.CertificateID)
	require.Len(t, input.Nodes, 1)
	assert.Equal(t, "vm1", input.Nodes[0].Name)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
}

func TestNodeProviderInput_CertificateAuth(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = []vcmserver.OntapCredentialV1{
				certOntapCredential(vcmserver.OntapCredentialV1RoleADMIN, "cert-1", "key-1"),
			}
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USER_CERTIFICATE, input.OntapCredentials.AuthType)
	assert.Equal(t, "cert-1", input.OntapCredentials.CertificateID)
	assert.Equal(t, "key-1", input.OntapCredentials.SecretID)
	require.Len(t, input.Nodes, 1)
}

func TestNodeProviderInput_DefaultsToCertificateWhenAuthTypeUnset(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			entry := vcmserver.OntapCredentialV1{Role: vcmserver.OntapCredentialV1RoleADMIN}
			entry.CertificateAuthCreds.SetTo(vcmserver.CertificateAuthCredsV1{
				CertSecretId:           vcmserver.NewOptString("cert-1"),
				CertPrivateKeySecretId: vcmserver.NewOptString("key-1"),
			})
			seed.OntapCredentials = []vcmserver.OntapCredentialV1{entry}
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USER_CERTIFICATE, input.OntapCredentials.AuthType)
	assert.Equal(t, "cert-1", input.OntapCredentials.CertificateID)
	assert.Equal(t, "key-1", input.OntapCredentials.SecretID)
}

func TestNodeProviderInput_SkipsMediatorAndEmptyLifs(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
			layout.IpConfig.SetTo(vcmserver.IpConfigV1{
				VmLifs: []vcmserver.VmLifV1{
					{
						Name:     vcmserver.NewOptString("vm1"),
						Nodemgmt: vcmserver.NewOptString("198.18.0.5"),
					},
					{
						Name:     vcmserver.NewOptString("vm2"),
						Nodemgmt: vcmserver.NewOptString("198.18.0.6"),
					},
					{
						Name: vcmserver.NewOptString("mediator"),
						Rsm:  vcmserver.NewOptString("198.18.16.41"),
					},
					{Name: vcmserver.NewOptString("")},
					{Name: vcmserver.NewOptString("vm3")},
				},
			})
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.Len(t, input.Nodes, 2)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
	assert.Equal(t, "198.18.0.6", input.Nodes[1].EndpointAddress)
}

func TestNodeProviderInput_TrimsLifNames(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
			layout.IpConfig.SetTo(vcmserver.IpConfigV1{
				VmLifs: []vcmserver.VmLifV1{
					{
						Name:     vcmserver.NewOptString("  vm1  "),
						Nodemgmt: vcmserver.NewOptString("198.18.0.5"),
					},
					{
						Name:     vcmserver.NewOptString("  mediator  "),
						Nodemgmt: vcmserver.NewOptString("198.18.16.41"),
					},
					{
						Name:     vcmserver.NewOptString("   "),
						Nodemgmt: vcmserver.NewOptString("198.18.0.7"),
					},
				},
			})
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.Len(t, input.Nodes, 1)
	assert.Equal(t, "vm1", input.Nodes[0].Name)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
}

func TestNodeProviderInput_PasswordRequiresPasswordSecretId(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = []vcmserver.OntapCredentialV1{
				passwordOntapCredential(vcmserver.OntapCredentialV1RoleADMIN, "root", ""),
			}
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "passwordSecretId")
}

func TestNodeProviderInput_MissingOntapCredentials(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = nil
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "ontapCredentials")
}

func TestNodeProviderInput_MissingDeploymentId(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.DeploymentId.Reset()
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "deploymentId")
}

func TestNodeProviderInput_EmptyDeploymentId(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.DeploymentId = vcmserver.NewOptString("   ")
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "deploymentId")
}

func TestNodeProviderInput_MissingIpConfig(t *testing.T) {
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
		seed.DeploymentId = vcmserver.NewOptString("gcnv-test")
		seed.OntapCredentials = defaultOntapCredentials("secret-1")
	})
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "ipConfig")
}

func TestNodeProviderInput_CertificateRequiresCertSecretId(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = []vcmserver.OntapCredentialV1{
				certOntapCredential(vcmserver.OntapCredentialV1RoleADMIN, "", "key-1"),
			}
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "certSecretId")
}

func TestNodeProviderInput_CertificateRequiresCertPrivateKeySecretId(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = []vcmserver.OntapCredentialV1{
				certOntapCredential(vcmserver.OntapCredentialV1RoleADMIN, "cert-1", ""),
			}
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "certPrivateKeySecretId")
}
