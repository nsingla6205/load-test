package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
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
	// A non-admin role honors an explicit CERT auth type.
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = []vcmserver.OntapCredentialV1{
				certOntapCredential(vcmserver.OntapCredentialV1RoleVCMADMIN, "cert-1", "key-1"),
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

// TestNodeProviderInput_AdminForcesPassword asserts an ADMIN credential resolves
// via password even when it declares CERT and carries certificate material.
func TestNodeProviderInput_AdminForcesPassword(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			entry := vcmserver.OntapCredentialV1{Role: vcmserver.OntapCredentialV1RoleADMIN}
			entry.ActiveAuthType.SetTo(vcmserver.OntapCredentialV1ActiveAuthTypeCERT)
			entry.CertificateAuthCreds.SetTo(vcmserver.CertificateAuthCredsV1{
				CertSecretId:           vcmserver.NewOptString("cert-1"),
				CertPrivateKeySecretId: vcmserver.NewOptString("key-1"),
			})
			entry.PasswordAuthCreds.SetTo(vcmserver.PasswordAuthCredsV1{
				Username:         vcmserver.NewOptString("root"),
				PasswordSecretId: vcmserver.NewOptString("admin-secret"),
			})
			seed.OntapCredentials = []vcmserver.OntapCredentialV1{entry}
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, input.OntapCredentials.AuthType, "ADMIN must be password-only")
	assert.Equal(t, "admin-secret", input.OntapCredentials.SecretID)
	assert.Equal(t, "root", input.OntapCredentials.Username)
	assert.Empty(t, input.OntapCredentials.CertificateID, "ADMIN must not resolve certificate material")
}

func TestNodeProviderInput_DefaultsToCertificateWhenAuthTypeUnset(t *testing.T) {
	// A non-admin role with no explicit auth type defaults to CERT.
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			entry := vcmserver.OntapCredentialV1{Role: vcmserver.OntapCredentialV1RoleVCMADMIN}
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
				certOntapCredential(vcmserver.OntapCredentialV1RoleVCMADMIN, "", "key-1"),
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
				certOntapCredential(vcmserver.OntapCredentialV1RoleVCMADMIN, "cert-1", ""),
			}
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "certPrivateKeySecretId")
}

func TestNodeProviderInput_CertificateAuth_AppliesDialWorkaround(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = []vcmserver.OntapCredentialV1{
				certOntapCredential(vcmserver.OntapCredentialV1RoleVCMADMIN, "cert-1", "key-1"),
			}
		})
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
				},
			})
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	assert.True(t, input.InsecureSkipVerify)
	require.Len(t, input.Nodes, 2)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].HostDNSName)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
	assert.Equal(t, "198.18.0.6", input.Nodes[1].HostDNSName)
}

func TestNodeProviderInput_SecretManagerAuth_DoesNotApplyDialWorkaround(t *testing.T) {
	input, err := configuredStore(t, nil).NodeProviderInput()
	require.NoError(t, err)
	assert.False(t, input.InsecureSkipVerify)
	require.Len(t, input.Nodes, 1)
	assert.Empty(t, input.Nodes[0].HostDNSName)
}

func TestApplyVCMCertAuthNodeDialWorkaround(t *testing.T) {
	t.Run("certificate auth sets skip verify and host DNS to endpoint IP", func(t *testing.T) {
		input := &vsa.NodeProviderInput{
			OntapCredentials: &datamodel.PoolCredentials{AuthType: env.USER_CERTIFICATE},
			Nodes: []*datamodel.Node{
				{Name: "vm1", EndpointAddress: "198.18.0.5"},
				nil,
				{Name: "vm3", EndpointAddress: ""},
				{Name: "vm4", EndpointAddress: "198.18.0.6"},
			},
		}

		applyVCMCertAuthNodeDialWorkaround(input)

		assert.True(t, input.InsecureSkipVerify)
		assert.Equal(t, "198.18.0.5", input.Nodes[0].HostDNSName)
		assert.Empty(t, input.Nodes[2].HostDNSName)
		assert.Equal(t, "198.18.0.6", input.Nodes[3].HostDNSName)
	})

	t.Run("non certificate auth is unchanged", func(t *testing.T) {
		input := &vsa.NodeProviderInput{
			OntapCredentials: &datamodel.PoolCredentials{AuthType: env.USERNAME_PWD_SEC_MGR},
			Nodes:            []*datamodel.Node{{Name: "vm1", EndpointAddress: "198.18.0.5"}},
		}

		applyVCMCertAuthNodeDialWorkaround(input)

		assert.False(t, input.InsecureSkipVerify)
		assert.Empty(t, input.Nodes[0].HostDNSName)
	})

	t.Run("nil input is safe", func(t *testing.T) {
		assert.NotPanics(t, func() { applyVCMCertAuthNodeDialWorkaround(nil) })
	})
}

func TestSelectOntapCredentials(t *testing.T) {
	t.Run("prefers VCM_ADMIN credential entry", func(t *testing.T) {
		creds := []vcmserver.OntapCredentialV1{
			passwordOntapCredential(vcmserver.OntapCredentialV1RoleADMIN, "admin", "admin-secret"),
			passwordOntapCredential(vcmserver.OntapCredentialV1RoleVCMADMIN, "vcm", "vcm-secret"),
		}

		entry, err := selectOntapCredentials(creds)
		require.NoError(t, err)
		assert.Equal(t, vcmserver.OntapCredentialV1RoleVCMADMIN, entry.Role)
		assert.Equal(t, "vcm-secret", entry.PasswordAuthCreds.Value.PasswordSecretId.Or(""))
	})

	t.Run("treats empty role as default", func(t *testing.T) {
		creds := []vcmserver.OntapCredentialV1{
			passwordOntapCredential(vcmserver.OntapCredentialV1RoleADMIN, "admin", "admin-secret"),
			passwordOntapCredential("", "vcm", "empty-role-secret"),
		}

		entry, err := selectOntapCredentials(creds)
		require.NoError(t, err)
		assert.Equal(t, vcmserver.OntapCredentialV1Role(""), entry.Role)
		assert.Equal(t, "empty-role-secret", entry.PasswordAuthCreds.Value.PasswordSecretId.Or(""))
	})

	t.Run("falls back to first when no default", func(t *testing.T) {
		creds := []vcmserver.OntapCredentialV1{
			passwordOntapCredential(vcmserver.OntapCredentialV1RoleADMIN, "first", "first-secret"),
			passwordOntapCredential(vcmserver.OntapCredentialV1RoleONTAPMODE, "second", "second-secret"),
		}

		entry, err := selectOntapCredentials(creds)
		require.NoError(t, err)
		assert.Equal(t, vcmserver.OntapCredentialV1RoleADMIN, entry.Role)
		assert.Equal(t, "first-secret", entry.PasswordAuthCreds.Value.PasswordSecretId.Or(""))
	})

	t.Run("empty list returns error", func(t *testing.T) {
		_, err := selectOntapCredentials(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ontapCredentials")
	})
}

func TestPoolCredentialsFromSetConfig(t *testing.T) {
	t.Run("certificate auth sets CaURI from env", func(t *testing.T) {
		withCAEnv(t, "proj-123", "pool-npd", "ca-npd")

		creds, err := poolCredentialsFromSetConfig([]vcmserver.OntapCredentialV1{
			certOntapCredential(vcmserver.OntapCredentialV1RoleVCMADMIN, "cert-1", "key-1"),
		})
		require.NoError(t, err)
		assert.Equal(t, env.USER_CERTIFICATE, creds.AuthType)
		assert.Equal(t, "proj-123/pool-npd/ca-npd", creds.CaURI)
	})

	t.Run("password auth maps secret manager fields", func(t *testing.T) {
		creds, err := poolCredentialsFromSetConfig(defaultOntapCredentials("secret-1"))
		require.NoError(t, err)
		assert.Equal(t, env.USERNAME_PWD_SEC_MGR, creds.AuthType)
		assert.Equal(t, "secret-1", creds.SecretID)
		assert.Equal(t, "root", creds.Username)
	})
}

func TestClusterNodesFromIpConfig(t *testing.T) {
	t.Run("unset ipConfig returns error", func(t *testing.T) {
		_, err := clusterNodesFromIpConfig(vcmserver.OptIpConfigV1{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ipConfig")
	})

	t.Run("no nodemgmt LIFs returns error", func(t *testing.T) {
		opt := vcmserver.OptIpConfigV1{}
		opt.SetTo(vcmserver.IpConfigV1{
			VmLifs: []vcmserver.VmLifV1{
				{Name: vcmserver.NewOptString("mediator"), Rsm: vcmserver.NewOptString("198.18.16.41")},
				{Name: vcmserver.NewOptString("vm1")},
			},
		})

		_, err := clusterNodesFromIpConfig(opt)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nodemgmt LIF")
	})

	t.Run("builds nodes from nodemgmt addresses", func(t *testing.T) {
		opt := vcmserver.OptIpConfigV1{}
		opt.SetTo(vcmserver.IpConfigV1{
			VmLifs: []vcmserver.VmLifV1{
				{
					Name:     vcmserver.NewOptString("vm1"),
					Nodemgmt: vcmserver.NewOptString("198.18.0.5"),
				},
			},
		})

		nodes, err := clusterNodesFromIpConfig(opt)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "vm1", nodes[0].Name)
		assert.Equal(t, "198.18.0.5", nodes[0].EndpointAddress)
	})
}

func withCAEnv(t *testing.T, projectID, poolName, caName string) {
	t.Helper()
	origProject := env.CaPoolDeployedProjectID
	origPool := env.CaPoolName
	origCA := env.CaName
	env.CaPoolDeployedProjectID = projectID
	env.CaPoolName = poolName
	env.CaName = caName
	t.Cleanup(func() {
		env.CaPoolDeployedProjectID = origProject
		env.CaPoolName = origPool
		env.CaName = origCA
	})
}
