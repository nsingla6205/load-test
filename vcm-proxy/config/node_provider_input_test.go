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

// certUser builds a certificate-auth user credential.
func certUser(purpose vcmserver.UserCredentialsV1UserPurpose, username, certID, keyID string) vcmserver.UserCredentialsV1 {
	u := vcmserver.UserCredentialsV1{}
	if purpose != "" {
		u.UserPurpose.SetTo(purpose)
	}
	if username != "" {
		u.Username.SetTo(username)
	}
	u.ActiveAuthMode.SetTo(vcmserver.UserCredentialsV1ActiveAuthModeAUTHMODECERT)
	if certID != "" {
		u.CertId.SetTo(certID)
	}
	if keyID != "" {
		u.CertPrivateKeySecret.SetTo(keyID)
	}
	return u
}

// passwordUser builds a password-auth user credential.
func passwordUser(purpose vcmserver.UserCredentialsV1UserPurpose, username, passwordSecret string) vcmserver.UserCredentialsV1 {
	u := vcmserver.UserCredentialsV1{}
	if purpose != "" {
		u.UserPurpose.SetTo(purpose)
	}
	if username != "" {
		u.Username.SetTo(username)
	}
	u.ActiveAuthMode.SetTo(vcmserver.UserCredentialsV1ActiveAuthModeAUTHMODEPASSWORD)
	if passwordSecret != "" {
		u.PasswordSecret.SetTo(passwordSecret)
	}
	return u
}

// ontapCredentials builds the structured OntapCredentials payload with the given
// admin password secret and default-mode (VCM_ADMIN) user.
func ontapCredentials(adminPassSecretID string, def vcmserver.UserCredentialsV1) vcmserver.OptOntapCredentialsV1 {
	oc := vcmserver.OntapCredentialsV1{}
	if adminPassSecretID != "" {
		oc.AdminPassSecretId.SetTo(adminPassSecretID)
	}
	oc.DefaultModeUserCredentials.SetTo(def)
	return vcmserver.NewOptOntapCredentialsV1(oc)
}

// defaultOntapCredentials returns the baseline used by configuredStore: an admin
// password secret plus a password-auth default-mode (VCM_ADMIN) user whose
// password secret is passwordSecretID.
func defaultOntapCredentials(passwordSecretID string) vcmserver.OptOntapCredentialsV1 {
	return ontapCredentials("admin-pass", passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", passwordSecretID))
}

// layoutNode builds a layout node carrying the identity and management
// addressing NodeProviderInput resolves ONTAP endpoints from.
func layoutNode(name, nodeManagementIP string) vcmserver.VsaNodeV1 {
	node := vcmserver.VsaNodeV1{GceVmName: vcmserver.NewOptString(name)}
	if nodeManagementIP != "" {
		node.NodeManagementIp = vcmserver.NewOptString(nodeManagementIP)
	}
	return node
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
		layout.Nodes = []vcmserver.VsaNodeV1{layoutNode("vm1", "198.18.0.5")}
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
	// Password mode authenticates as the built-in admin, not the configured
	// default-mode username, which exists only as a certificate user.
	assert.Equal(t, env.Admin, input.OntapCredentials.Username)
	assert.Equal(t, "", input.OntapCredentials.CertificateID)
	require.Len(t, input.Nodes, 1)
	assert.Equal(t, "vm1", input.Nodes[0].Name)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
}

func TestNodeProviderInput_CertificateAuth(t *testing.T) {
	// The default-mode user with AUTHMODE_CERT resolves the provided cert + key;
	// the bootstrap admin password (adminPassSecretId) becomes SecretID.
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = ontapCredentials("admin-pass",
				certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "cert-1", "key-1"))
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USER_CERTIFICATE, input.OntapCredentials.AuthType)
	assert.Equal(t, "cert-1", input.OntapCredentials.CertificateID)
	assert.Equal(t, "key-1", input.OntapCredentials.CertPrivateKeySecretID)
	assert.Equal(t, "admin-pass", input.OntapCredentials.SecretID, "cert bootstrap uses adminPassSecretId")
	require.Len(t, input.Nodes, 1)
}

func TestNodeProviderInput_DefaultsToCertificateWhenAuthModeUnset(t *testing.T) {
	// A user with cert material and no explicit activeAuthMode defaults to CERT.
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			def := vcmserver.UserCredentialsV1{}
			def.UserPurpose.SetTo(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN)
			def.CertId.SetTo("cert-1")
			def.CertPrivateKeySecret.SetTo("key-1")
			seed.OntapCredentials = ontapCredentials("admin-pass", def)
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USER_CERTIFICATE, input.OntapCredentials.AuthType)
	assert.Equal(t, "cert-1", input.OntapCredentials.CertificateID)
	assert.Equal(t, "key-1", input.OntapCredentials.CertPrivateKeySecretID)
}

func TestNodeProviderInput_SkipsMediatorAndEmptyLifs(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
			mediator := layoutNode("mediator", "")
			mediator.IpConfig.SetTo(vcmserver.IpConfigV1{
				RsmLifIp: vcmserver.NewOptString("198.18.16.41"),
			})
			layout.Nodes = []vcmserver.VsaNodeV1{
				layoutNode("vm1", "198.18.0.5"),
				layoutNode("vm2", "198.18.0.6"),
				mediator,
				layoutNode("", ""),
				layoutNode("vm3", ""),
			}
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
			layout.Nodes = []vcmserver.VsaNodeV1{
				layoutNode("  vm1  ", "198.18.0.5"),
				layoutNode("  mediator  ", "198.18.16.41"),
				layoutNode("   ", "198.18.0.7"),
			}
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.Len(t, input.Nodes, 1)
	assert.Equal(t, "vm1", input.Nodes[0].Name)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
}

func TestNodeProviderInput_PasswordRequiresPasswordSecret(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = ontapCredentials("admin-pass",
				passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", ""))
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "passwordSecret")
}

func TestNodeProviderInput_RequiresAdminPassSecretId(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = ontapCredentials("",
				passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "root", "secret-1"))
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "adminPassSecretId")
}

func TestNodeProviderInput_MissingOntapCredentials(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials.Reset()
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "ontapCredentials")
}

func TestNodeProviderInput_MissingDefaultModeUserCredentials(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			// adminPassSecretId alone is not enough: VCM still needs the
			// PURPOSE_VCM_ADMIN user it authenticates as.
			oc := vcmserver.OntapCredentialsV1{}
			oc.AdminPassSecretId.SetTo("admin-pass")
			seed.OntapCredentials = vcmserver.NewOptOntapCredentialsV1(oc)
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "defaultModeUserCredentials")
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

func TestNodeProviderInput_MissingLayoutNodes(t *testing.T) {
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
	assert.Contains(t, err.Error(), "no nodes")
}

func TestNodeProviderInput_CertificateRequiresCertId(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = ontapCredentials("admin-pass",
				certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "", "key-1"))
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "certId")
}

func TestNodeProviderInput_CertificateRequiresCertPrivateKeySecret(t *testing.T) {
	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = ontapCredentials("admin-pass",
				certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "cert-1", ""))
		})
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "certPrivateKeySecret")
}

func TestNodeProviderInput_CertificateAuth_AppliesSNI(t *testing.T) {
	restore := setVsaDeployedDnsName(t, "vsa.example.com")
	defer restore()

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		mutateSeed(req, func(seed *vcmserver.SeedConfigV1) {
			seed.OntapCredentials = ontapCredentials("admin-pass",
				certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "cert-1", "key-1"))
		})
		mutateLayout(req, func(layout *vcmserver.ClusterLayoutV1) {
			layout.Nodes = []vcmserver.VsaNodeV1{
				layoutNode("vm1", "198.18.0.5"),
				layoutNode("vm2", "198.18.0.6"),
			}
		})
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	// SNI override matches the cert wildcard SAN; nodes keep their mgmt IPs and
	// no per-node DNS name is fabricated (server-cert verification stays on).
	assert.Equal(t, "mgmt.gcnv-test.vsa.example.com", input.ServerName)
	require.Len(t, input.Nodes, 2)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
	assert.Empty(t, input.Nodes[0].HostDNSName)
	assert.Equal(t, "198.18.0.6", input.Nodes[1].EndpointAddress)
	assert.Empty(t, input.Nodes[1].HostDNSName)
}

func TestNodeProviderInput_SecretManagerAuth_DoesNotApplySNI(t *testing.T) {
	restore := setVsaDeployedDnsName(t, "vsa.example.com")
	defer restore()

	input, err := configuredStore(t, nil).NodeProviderInput()
	require.NoError(t, err)
	assert.Empty(t, input.ServerName)
	require.Len(t, input.Nodes, 1)
	assert.Empty(t, input.Nodes[0].HostDNSName)
}

func TestApplyVCMCertAuthNodeSNI(t *testing.T) {
	restore := setVsaDeployedDnsName(t, "vsa.example.com")
	defer restore()

	t.Run("certificate auth sets SNI from deployment name, leaves nodes untouched", func(t *testing.T) {
		input := &vsa.NodeProviderInput{
			DeploymentName:   "gcnv-test",
			OntapCredentials: &datamodel.PoolCredentials{AuthType: env.USER_CERTIFICATE},
			Nodes: []*datamodel.Node{
				{Name: "vm1", EndpointAddress: "198.18.0.5"},
				{Name: "vm2", EndpointAddress: "198.18.0.6"},
			},
		}

		applyVCMCertAuthNodeSNI(input)

		assert.Equal(t, "mgmt.gcnv-test.vsa.example.com", input.ServerName)
		assert.Empty(t, input.Nodes[0].HostDNSName)
		assert.Empty(t, input.Nodes[1].HostDNSName)
	})

	t.Run("certificate auth with no deployed DNS name leaves SNI empty", func(t *testing.T) {
		reset := setVsaDeployedDnsName(t, "")
		defer reset()

		input := &vsa.NodeProviderInput{
			DeploymentName:   "gcnv-test",
			OntapCredentials: &datamodel.PoolCredentials{AuthType: env.USER_CERTIFICATE},
			Nodes:            []*datamodel.Node{{Name: "vm1", EndpointAddress: "198.18.0.5"}},
		}

		applyVCMCertAuthNodeSNI(input)

		assert.Empty(t, input.ServerName)
	})

	t.Run("non certificate auth is unchanged", func(t *testing.T) {
		input := &vsa.NodeProviderInput{
			DeploymentName:   "gcnv-test",
			OntapCredentials: &datamodel.PoolCredentials{AuthType: env.USERNAME_PWD_SEC_MGR},
			Nodes:            []*datamodel.Node{{Name: "vm1", EndpointAddress: "198.18.0.5"}},
		}

		applyVCMCertAuthNodeSNI(input)

		assert.Empty(t, input.ServerName)
		assert.Empty(t, input.Nodes[0].HostDNSName)
	})

	t.Run("nil input is safe", func(t *testing.T) {
		assert.NotPanics(t, func() { applyVCMCertAuthNodeSNI(nil) })
	})
}

// setVsaDeployedDnsName overrides env.VsaDeployedDnsName for a test and returns
// a function that restores the previous value.
func setVsaDeployedDnsName(t *testing.T, value string) func() {
	t.Helper()
	previous := env.VsaDeployedDnsName
	env.VsaDeployedDnsName = value
	return func() { env.VsaDeployedDnsName = previous }
}

func TestPoolCredentialsFromSetConfig(t *testing.T) {
	t.Run("certificate auth carries cert + key and CaURI from env", func(t *testing.T) {
		withCAEnv(t, "proj-123", "pool-npd", "ca-npd")

		creds, err := poolCredentialsFromSetConfig(ontapCredentials("admin-pass",
			certUser(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN, "vcm-admin", "cert-1", "key-1")))
		require.NoError(t, err)
		assert.Equal(t, env.USER_CERTIFICATE, creds.AuthType)
		assert.Equal(t, "cert-1", creds.CertificateID)
		assert.Equal(t, "key-1", creds.CertPrivateKeySecretID)
		assert.Equal(t, "admin-pass", creds.SecretID)
		assert.Equal(t, "proj-123/pool-npd/ca-npd", creds.CaURI)
	})

	t.Run("password auth maps secret manager fields", func(t *testing.T) {
		creds, err := poolCredentialsFromSetConfig(defaultOntapCredentials("secret-1"))
		require.NoError(t, err)
		assert.Equal(t, env.USERNAME_PWD_SEC_MGR, creds.AuthType)
		assert.Equal(t, "secret-1", creds.SecretID)
		assert.Equal(t, env.Admin, creds.Username)
	})

	t.Run("rejects userPurpose mismatch on default user", func(t *testing.T) {
		_, err := poolCredentialsFromSetConfig(ontapCredentials("admin-pass",
			passwordUser(vcmserver.UserCredentialsV1UserPurposePURPOSEONTAPMODE, "root", "secret-1")))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "userPurpose")
	})
}

func TestClusterNodesFromLayout(t *testing.T) {
	t.Run("layout without nodes returns error", func(t *testing.T) {
		_, err := clusterNodesFromLayout(vcmserver.ClusterLayoutV1{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no nodes")
	})

	t.Run("no management addresses returns error", func(t *testing.T) {
		mediator := layoutNode("mediator", "")
		mediator.IpConfig.SetTo(vcmserver.IpConfigV1{
			RsmLifIp: vcmserver.NewOptString("198.18.16.41"),
		})

		_, err := clusterNodesFromLayout(vcmserver.ClusterLayoutV1{
			Nodes: []vcmserver.VsaNodeV1{mediator, layoutNode("vm1", "")},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "node management IP")
	})

	t.Run("builds nodes from node management addresses", func(t *testing.T) {
		nodes, err := clusterNodesFromLayout(vcmserver.ClusterLayoutV1{
			Nodes: []vcmserver.VsaNodeV1{layoutNode("vm1", "198.18.0.5")},
		})
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "vm1", nodes[0].Name)
		assert.Equal(t, "198.18.0.5", nodes[0].EndpointAddress)
	})

	t.Run("falls back to the nodemgmt LIF", func(t *testing.T) {
		node := layoutNode("vm1", "")
		node.IpConfig.SetTo(vcmserver.IpConfigV1{
			NodemgmtLifIp: vcmserver.NewOptString("198.18.0.9"),
		})

		nodes, err := clusterNodesFromLayout(vcmserver.ClusterLayoutV1{
			Nodes: []vcmserver.VsaNodeV1{node},
		})
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "198.18.0.9", nodes[0].EndpointAddress)
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
