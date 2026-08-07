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

func withAuthType(t *testing.T, authType int) {
	t.Helper()
	original := env.AuthType
	env.AuthType = authType
	t.Cleanup(func() { env.AuthType = original })
}

func withNodePassword(t *testing.T, password string) {
	t.Helper()
	original := env.NodePassword
	env.NodePassword = password
	t.Cleanup(func() { env.NodePassword = original })
}

func defaultOntapCredentials(secretID, certificateID string) []vcmserver.OntapCredentialsV1 {
	entry := vcmserver.OntapCredentialsV1{
		Type:     vcmserver.OntapCredentialsV1TypeDefault,
		SecretId: vcmserver.NewOptString(secretID),
	}
	if certificateID != "" {
		entry.CertificateId = vcmserver.NewOptString(certificateID)
	}
	return []vcmserver.OntapCredentialsV1{entry}
}

func configuredStore(t *testing.T, mutate func(*vcmserver.SetConfigRequestV1)) *Store {
	t.Helper()
	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	req.DeploymentId = vcmserver.NewOptString("gcnv-test")
	req.OntapCredentials = defaultOntapCredentials("secret-1", "")
	req.IpConfig.SetTo(vcmserver.IpConfigV1{
		Lifs: []vcmserver.VmLifsV1{
			{
				Name:     vcmserver.NewOptString("vm1"),
				Nodemgmt: vcmserver.NewOptString("198.18.0.5"),
			},
		},
	})
	if mutate != nil {
		mutate(req)
	}
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })
	return store
}

func TestNodeProviderInput_SecretManagerAuth(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	input, err := configuredStore(t, nil).NodeProviderInput()
	require.NoError(t, err)
	assert.Equal(t, "gcnv-test", input.DeploymentName)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, input.OntapCredentials.AuthType)
	assert.Equal(t, "secret-1", input.OntapCredentials.SecretID)
	assert.Equal(t, "", input.OntapCredentials.CertificateID)
	require.Len(t, input.Nodes, 1)
	assert.Equal(t, "vm1", input.Nodes[0].Name)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
}

func TestNodeProviderInput_CertificateAuth(t *testing.T) {
	withAuthType(t, env.USER_CERTIFICATE)

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		req.OntapCredentials = defaultOntapCredentials("secret-1", "cert-1")
	})

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USER_CERTIFICATE, input.OntapCredentials.AuthType)
	assert.Equal(t, "cert-1", input.OntapCredentials.CertificateID)
	assert.Equal(t, "secret-1", input.OntapCredentials.SecretID)
	require.Len(t, input.Nodes, 1)
}

func TestNodeProviderInput_PlainPasswordAuth(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD)
	withNodePassword(t, "node-password")

	input, err := configuredStore(t, nil).NodeProviderInput()
	require.NoError(t, err)
	require.NotNil(t, input.OntapCredentials)
	assert.Equal(t, env.USERNAME_PWD, input.OntapCredentials.AuthType)
	assert.Equal(t, "node-password", input.OntapCredentials.Password)
	assert.Equal(t, "", input.OntapCredentials.SecretID)
}

func TestNodeProviderInput_SkipsMediatorAndEmptyLifs(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		req.IpConfig.SetTo(vcmserver.IpConfigV1{
			Lifs: []vcmserver.VmLifsV1{
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

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.Len(t, input.Nodes, 2)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
	assert.Equal(t, "198.18.0.6", input.Nodes[1].EndpointAddress)
}

func TestNodeProviderInput_TrimsLifNames(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		req.IpConfig.SetTo(vcmserver.IpConfigV1{
			Lifs: []vcmserver.VmLifsV1{
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

	input, err := store.NodeProviderInput()
	require.NoError(t, err)
	require.Len(t, input.Nodes, 1)
	assert.Equal(t, "vm1", input.Nodes[0].Name)
	assert.Equal(t, "198.18.0.5", input.Nodes[0].EndpointAddress)
}

func TestNodeProviderInput_SecretManagerRequiresSecretId(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		req.OntapCredentials = []vcmserver.OntapCredentialsV1{{
			Type: vcmserver.OntapCredentialsV1TypeDefault,
		}}
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "secretId")
}

func TestNodeProviderInput_MissingOntapCredentials(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		req.OntapCredentials = nil
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "ontapCredentials")
}

func TestNodeProviderInput_MissingDeploymentId(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		req.DeploymentId.Reset()
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "deploymentId")
}

func TestNodeProviderInput_EmptyDeploymentId(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		req.DeploymentId = vcmserver.NewOptString("   ")
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "deploymentId")
}

func TestNodeProviderInput_MissingIpConfig(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD_SEC_MGR)

	store := NewStoreWithOpener(log.NewLogger(), &stubDBOpener{delay: 10 * time.Millisecond})
	req := testSetConfigRequest(testDBConfig(), "us-central1-a")
	req.DeploymentId = vcmserver.NewOptString("gcnv-test")
	req.OntapCredentials = defaultOntapCredentials("secret-1", "")
	store.Apply(context.Background(), req)
	waitFor(t, func() bool { return store.Configured() })

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "ipConfig")
}

func TestNodeProviderInput_PlainPasswordRequiresNodePassword(t *testing.T) {
	withAuthType(t, env.USERNAME_PWD)
	withNodePassword(t, "")

	_, err := configuredStore(t, nil).NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "VSA_NODE_PASSWORD")
}

func TestNodeProviderInput_CertificateRequiresCertificateId(t *testing.T) {
	withAuthType(t, env.USER_CERTIFICATE)

	store := configuredStore(t, func(req *vcmserver.SetConfigRequestV1) {
		req.OntapCredentials = defaultOntapCredentials("secret-1", "")
	})

	_, err := store.NodeProviderInput()
	require.Error(t, err)
	assert.ErrorIs(t, err, datamodel.ErrInvalidConfig)
	assert.Contains(t, err.Error(), "certificateId")
}
