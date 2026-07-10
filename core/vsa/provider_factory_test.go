package vsa

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	ocidns "github.com/oracle/oci-go-sdk/v65/dns"
	"github.com/oracle/oci-go-sdk/v65/secrets"
	"github.com/oracle/oci-go-sdk/v65/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	common2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	hyperscaler3 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	oci2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	coreerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func Test_GetProviderByNode(t *testing.T) {
	ctx := context.Background()

	t.Run("USER_CERTIFICATE success", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			CertificateID:                  "cert-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "1.2.3.4"},
			AuthType:                       env.USER_CERTIFICATE,
		}

		origGetCert := GetCertificateFromCacheOrSecretManager
		defer func() { GetCertificateFromCacheOrSecretManager = origGetCert }()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return &models.Certificate{
				SignedCertificate:        "signed",
				InterMediateCertificates: []string{"intermediate"},
				CommonName:               "common",
				PrivateKey:               "key",
			}, nil
		}

		provider, err := GetProviderByNode(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("USER_CERTIFICATE error", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			CertificateID:                  "cert-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "1.2.3.4"},
			AuthType:                       env.USER_CERTIFICATE,
		}

		origGetCert := GetCertificateFromCacheOrSecretManager
		defer func() { GetCertificateFromCacheOrSecretManager = origGetCert }()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return nil, errors.New("error")
		}

		provider, err := GetProviderByNode(ctx, node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error")
		assert.Nil(t, provider)
	})

	t.Run("USERNAME_PWD_SEC_MGR success", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node2",
			SecretID:                       "secret-id",
			EndpointAddress:                "1.2.3.4",
			EndpointAddressesToHostNameMap: map[string]string{},
			AuthType:                       env.USERNAME_PWD_SEC_MGR,
		}

		origGetPwd := GetPasswordFromCacheOrSecretManager
		defer func() { GetPasswordFromCacheOrSecretManager = origGetPwd }()
		GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
			return "pwd", nil
		}

		provider, err := GetProviderByNode(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("USERNAME_PWD_SEC_MGR error", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node2",
			SecretID:                       "secret-id",
			EndpointAddress:                "1.2.3.4",
			EndpointAddressesToHostNameMap: map[string]string{},
			AuthType:                       env.USERNAME_PWD_SEC_MGR,
		}

		origGetPwd := GetPasswordFromCacheOrSecretManager
		defer func() { GetPasswordFromCacheOrSecretManager = origGetPwd }()
		GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
			return "", errors.New("error")
		}

		provider, err := GetProviderByNode(ctx, node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error")
		assert.Nil(t, provider)
	})

	t.Run("Password from node, missing endpoint address", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node3",
			Password:                       "pwd",
			EndpointAddressesToHostNameMap: map[string]string{},
			EndpointAddress:                "",
			AuthType:                       env.USERNAME_PWD,
		}

		provider, err := GetProviderByNode(ctx, node)
		assert.Error(t, err)
		assert.Nil(t, provider)
	})

	t.Run("Password from node, endpoint address present", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node3",
			Password:                       "pwd",
			EndpointAddressesToHostNameMap: map[string]string{},
			EndpointAddress:                "1.2.3.4",
			AuthType:                       env.USERNAME_PWD,
		}

		provider, err := GetProviderByNode(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("GetProviderByNode_NilNode", func(t *testing.T) {
		// Test with nil node - covers lines 34-35
		provider, err := GetProviderByNode(ctx, nil)
		assert.Error(t, err)
		assert.Nil(t, provider)
		// The error is wrapped in VCPError, so check for the error type
		assert.Contains(t, err.Error(), "Resource not found")
	})

	t.Run("GetProviderByNode_EmptyEndpointAddressesToHostNameMap", func(t *testing.T) {
		// Test with empty EndpointAddressesToHostNameMap for certificate auth - covers lines 41-42
		node := &models.Node{
			Name:                           "node4",
			CertificateID:                  "cert-id",
			EndpointAddressesToHostNameMap: map[string]string{}, // Empty map
			AuthType:                       env.USER_CERTIFICATE,
		}

		provider, err := GetProviderByNode(ctx, node)
		assert.Error(t, err)
		assert.Nil(t, provider)
		// The error is wrapped in VCPError, so check for the error type
		assert.Contains(t, err.Error(), "VSA cluster node IP address not found")
	})

	t.Run("GetProviderByNode_WithPassword", func(t *testing.T) {
		// Test with node.Password not empty - covers line 68
		node := &models.Node{
			Name:                           "node5",
			CertificateID:                  "cert-id",
			Password:                       "test-password",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "1.2.3.4"},
			AuthType:                       env.USER_CERTIFICATE,
		}

		origGetCert := GetCertificateFromCacheOrSecretManager
		defer func() { GetCertificateFromCacheOrSecretManager = origGetCert }()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return &models.Certificate{
				SignedCertificate:        "signed",
				InterMediateCertificates: []string{"intermediate"},
				CommonName:               "common",
				PrivateKey:               "key",
			}, nil
		}

		provider, err := GetProviderByNode(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	// Certificate auth with SecretID: GetPasswordFromCacheOrSecretManager returns error (lines 68-69).
	t.Run("GetProviderByNode_USER_CERTIFICATE_SecretID_GetPasswordError", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node-cert-secret",
			CertificateID:                  "cert-id",
			SecretID:                       "secret-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "host"},
			AuthType:                       env.USER_CERTIFICATE,
		}
		origGetCert := GetCertificateFromCacheOrSecretManager
		origGetPwd := GetPasswordFromCacheOrSecretManager
		defer func() {
			GetCertificateFromCacheOrSecretManager = origGetCert
			GetPasswordFromCacheOrSecretManager = origGetPwd
		}()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return &models.Certificate{
				SignedCertificate:        "signed",
				InterMediateCertificates: []string{"intermediate"},
				CommonName:               "common",
				PrivateKey:               "key",
			}, nil
		}
		GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
			return "", errors.New("secret not found")
		}
		provider, err := GetProviderByNode(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	// Certificate auth with SecretID: GetPassword returns empty string (lines 73-74).
	t.Run("GetProviderByNode_USER_CERTIFICATE_SecretID_EmptyPassword", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node-cert-secret-empty",
			CertificateID:                  "cert-id",
			SecretID:                       "secret-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "host"},
			AuthType:                       env.USER_CERTIFICATE,
		}
		origGetCert := GetCertificateFromCacheOrSecretManager
		origGetPwd := GetPasswordFromCacheOrSecretManager
		defer func() {
			GetCertificateFromCacheOrSecretManager = origGetCert
			GetPasswordFromCacheOrSecretManager = origGetPwd
		}()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return &models.Certificate{
				SignedCertificate:        "signed",
				InterMediateCertificates: []string{"intermediate"},
				CommonName:               "common",
				PrivateKey:               "key",
			}, nil
		}
		GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
			return "", nil
		}
		provider, err := GetProviderByNode(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	// Certificate auth with SecretID: GetPassword returns non-empty (lines 70-72).
	t.Run("GetProviderByNode_USER_CERTIFICATE_SecretID_PasswordFromSecret", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node-cert-secret-ok",
			CertificateID:                  "cert-id",
			SecretID:                       "secret-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "host"},
			AuthType:                       env.USER_CERTIFICATE,
		}
		origGetCert := GetCertificateFromCacheOrSecretManager
		origGetPwd := GetPasswordFromCacheOrSecretManager
		defer func() {
			GetCertificateFromCacheOrSecretManager = origGetCert
			GetPasswordFromCacheOrSecretManager = origGetPwd
		}()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return &models.Certificate{
				SignedCertificate:        "signed",
				InterMediateCertificates: []string{"intermediate"},
				CommonName:               "common",
				PrivateKey:               "key",
			}, nil
		}
		GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
			return "secret-pwd", nil
		}
		provider, err := GetProviderByNode(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	// Certificate auth, no SecretID, no password (lines 76-78).
	t.Run("GetProviderByNode_USER_CERTIFICATE_NoPasswordNoSecretID", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node-cert-nopwd",
			CertificateID:                  "cert-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "host"},
			AuthType:                       env.USER_CERTIFICATE,
		}
		origGetCert := GetCertificateFromCacheOrSecretManager
		defer func() { GetCertificateFromCacheOrSecretManager = origGetCert }()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return &models.Certificate{
				SignedCertificate:        "signed",
				InterMediateCertificates: []string{"intermediate"},
				CommonName:               "common",
				PrivateKey:               "key",
			}, nil
		}
		provider, err := GetProviderByNode(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})
}

// Test_GetProviderByNode_OCI_USERNAME_PWD_SEC_MGR_Success exercises the OCI
// branch added to _getProviderByNode: when env.GetHyperscaler() == "oci"
// and Node.AuthType == USERNAME_PWD_SEC_MGR, the provider builder must look
// the password up via GetPasswordFromCacheOrOCIVault using Node.ExternalSecret
// (NOT Node.SecretID, which is the GCP Secret Manager key). On the GCP path
// (env.GetHyperscaler() == "gcp", the default) the same branch falls through
// to GetPasswordFromCacheOrSecretManager — see the existing
// Test_GetProviderByNode subtests "USERNAME_PWD_SEC_MGR success" / "error"
// for that side.
//
// This test is the unified-path replacement for the deleted
// Test_GetOCIProviderByNode_SecMgrSuccess that previously guarded the OCI
// vault lookup against silent regressions to the GCP code path.
func Test_GetProviderByNode_OCI_USERNAME_PWD_SEC_MGR_Success(t *testing.T) {
	origHS := env.Hyperscaler
	defer func() { env.Hyperscaler = origHS }()
	env.Hyperscaler = common.ProviderOCI

	origGetVault := GetPasswordFromCacheOrOCIVault
	defer func() { GetPasswordFromCacheOrOCIVault = origGetVault }()

	expectedRef := &datamodel.ExternalCredRef{
		Name:               "ocnv-cafef00d-secret",
		ExternalIdentifier: "ocid1.vaultsecret.oc1..xyz",
		Version:            7,
	}
	var capturedRef *datamodel.ExternalCredRef
	GetPasswordFromCacheOrOCIVault = func(ctx context.Context, ref *datamodel.ExternalCredRef) (string, error) {
		capturedRef = ref
		return "vault-pw", nil
	}

	node := &models.Node{
		Name:                           "node-1",
		EndpointAddressesToHostNameMap: map[string]string{"10.0.0.1": "host-1"},
		AuthType:                       env.USERNAME_PWD_SEC_MGR,
		// SecretID intentionally populated. If anyone ever changes the OCI
		// branch to fall back to SecretID (the GCP key) instead of the
		// ExternalCredRef, this test catches it via the captured-ref assertion.
		SecretID:       "projects/foo/secrets/bar",
		ExternalSecret: expectedRef,
	}

	provider, err := _getProviderByNode(context.Background(), node)
	assert.NoError(t, err)
	assert.NotNil(t, provider)

	if assert.NotNil(t, capturedRef, "OCI Vault lookup must be invoked with a non-nil ref") {
		assert.Same(t, expectedRef, capturedRef,
			"the exact ExternalCredRef from Node must be passed through; never synthesised from SecretID")
	}
}

// Test_GetProviderByNode_OCI_USERNAME_PWD_SEC_MGR_VaultFetchFails verifies
// that vault-side failures on the OCI path are surfaced under
// coreerrors.ErrOCIResourceFetchError (not the generic ErrGCPResourceFetchError
// used by the default branch). This distinction lets steady-state callers
// retry / log OCI failures separately from GCP Secret Manager failures.
func Test_GetProviderByNode_OCI_USERNAME_PWD_SEC_MGR_VaultFetchFails(t *testing.T) {
	origHS := env.Hyperscaler
	defer func() { env.Hyperscaler = origHS }()
	env.Hyperscaler = common.ProviderOCI

	origGetVault := GetPasswordFromCacheOrOCIVault
	defer func() { GetPasswordFromCacheOrOCIVault = origGetVault }()
	GetPasswordFromCacheOrOCIVault = func(ctx context.Context, ref *datamodel.ExternalCredRef) (string, error) {
		return "", fmt.Errorf("vault: 403 forbidden")
	}

	node := &models.Node{
		Name:                           "node-1",
		EndpointAddressesToHostNameMap: map[string]string{"10.0.0.1": "host-1"},
		AuthType:                       env.USERNAME_PWD_SEC_MGR,
		ExternalSecret:                 &datamodel.ExternalCredRef{Name: "n", ExternalIdentifier: "o"},
	}

	provider, err := _getProviderByNode(context.Background(), node)
	assert.Nil(t, provider)
	require.Error(t, err)

	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
		assert.Equal(t, coreerrors.ErrOCIResourceFetchError, cerr.TrackingID,
			"vault failures must surface under ErrOCIResourceFetchError so callers can distinguish them from GCP Secret Manager failures")
	}
}

func Test_GetPasswordForVSACluster_Success(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		gcpService := hyperscaler.NewMockServices(t)
		secretID := "test-secret-id"
		expectedSecret := &hyperscaler3.CustomSecret{
			SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "super-secret"},
		}
		gcpService.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(expectedSecret, nil)

		secret, err := GetPasswordForVSACluster(gcpService, secretID)
		assert.NoError(t, err)
		assert.Equal(t, expectedSecret, secret)
		gcpService.AssertExpectations(t)
	})

	t.Run("failure", func(t *testing.T) {
		gcpService := hyperscaler.NewMockServices(t)
		secretID := "test-secret-id"
		gcpService.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(nil, errors.New("not found"))

		secret, err := GetPasswordForVSACluster(gcpService, secretID)
		assert.Error(t, err)
		assert.Nil(t, secret)
		gcpService.AssertExpectations(t)
	})
}

func Test_GetCertificateFromCacheOrSecretManager(t *testing.T) {
	ctx := context.Background()
	certificateID := "test-cert-id"

	t.Run("Certificate found in cache", func(t *testing.T) {
		mockCert := &models.Certificate{
			SignedCertificate:        "signed-cert",
			PrivateKey:               "private-key",
			CommonName:               "common-name",
			InterMediateCertificates: []string{"intermediate"},
		}
		defer func() {
			common.RemoveFromCertAuthCache(certificateID)
		}()
		common.AddToCertAuthCache(certificateID, mockCert)
		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		cert, err := GetCertificateFromCacheOrSecretManager(ctx, poolCredentials)
		assert.NoError(t, err)
		assert.Equal(t, mockCert, cert)
	})
	t.Run("Certificate not in cache, found via GCP", func(t *testing.T) {
		origGetGCPService := hyperscaler.GetGCPService
		origGetCertificateAndPrivateKeyByID := GetCertificateAndPrivateKeyByID
		defer func() {
			common.RemoveFromCertAuthCache(certificateID)
			hyperscaler.GetGCPService = origGetGCPService
			GetCertificateAndPrivateKeyByID = origGetCertificateAndPrivateKeyByID
		}()
		hyperscaler.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
			return &google.GcpServices{}, nil
		}
		mockCertResp := &hyperscaler3.CustomCertificateResponse{
			Certificate: &hyperscaler3.CustomCertificate{
				PemCertificate:      "signed-cert",
				SubjectCommonName:   "common-name",
				PemCertificateChain: []string{"intermediate"},
			},
			Secret: &hyperscaler3.CustomSecret{
				SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"},
			},
		}
		GetCertificateAndPrivateKeyByID = func(gcpService hyperscaler.Services, caDeployedProjectID, secretManagerProjectID, region, caPoolName, certificateID string) (*hyperscaler3.CustomCertificateResponse, error) {
			return mockCertResp, nil
		}
		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		cert, err := GetCertificateFromCacheOrSecretManager(ctx, poolCredentials)
		assert.NoError(t, err)
		assert.Equal(t, "signed-cert", cert.SignedCertificate)
		assert.Equal(t, "private-key", cert.PrivateKey)
		assert.Equal(t, "common-name", cert.CommonName)
		assert.Equal(t, []string{"intermediate"}, cert.InterMediateCertificates)
	})
	t.Run("GCP service returns error", func(t *testing.T) {
		hyperscaler.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
			return nil, errors.New("gcp error")
		}
		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		cert, err := GetCertificateFromCacheOrSecretManager(ctx, poolCredentials)
		assert.Error(t, err)
		assert.Nil(t, cert)
	})
}

func Test_GeneratePasswordForVSACluster(t *testing.T) {
	userName := "test-user"

	t.Run("PasswordGenerationFails", func(tt *testing.T) {
		mockGCPService := new(hyperscaler.MockServices)
		originalGeneratePassword := utils.GenerateStrongPassword
		utils.GenerateStrongPassword = func(length int) (string, error) {
			return "", errors.New("password generation failed")
		}
		defer func() { utils.GenerateStrongPassword = originalGeneratePassword }()
		mockGCPService.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(nil, nil)
		mockGCPService.On("GetLogger").Return(log.NewLogger())
		secret, err := GeneratePasswordForVSACluster(mockGCPService, userName)

		assert.Error(tt, err)
		assert.Nil(tt, secret)
		assert.Contains(tt, err.Error(), "password generation failed")
	})

	t.Run("SecretCreationFails", func(tt *testing.T) {
		mockGCPService := new(hyperscaler.MockServices)
		originalGeneratePassword := utils.GenerateStrongPassword
		utils.GenerateStrongPassword = func(length int) (string, error) {
			return "xyzpassword", nil
		}
		defer func() { utils.GenerateStrongPassword = originalGeneratePassword }()

		mockGCPService.On("GetLogger").Return(log.NewLogger())
		mockGCPService.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(nil, nil)
		mockGCPService.On("CreateSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("secret creation failed"))

		secret, err := GeneratePasswordForVSACluster(mockGCPService, userName)

		assert.Error(tt, err)
		assert.Nil(tt, secret)
		assert.Contains(tt, err.Error(), "secret creation failed")
		mockGCPService.AssertExpectations(tt)
	})

	t.Run("GetSecretWithLatestVersion success", func(tt *testing.T) {
		mockGCPService := new(hyperscaler.MockServices)
		mockGCPService.On("GetLogger").Return(log.NewLogger())
		mockGCPService.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(&hyperscaler3.CustomSecret{}, nil)

		secret, err := GeneratePasswordForVSACluster(mockGCPService, userName)
		assert.NoError(tt, err)
		assert.NotNil(tt, secret)
		mockGCPService.AssertExpectations(tt)
	})

	t.Run("Success", func(tt *testing.T) {
		mockGCPService := new(hyperscaler.MockServices)
		mockGCPService.On("GetLogger").Return(log.NewLogger())
		mockGCPService.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(nil, nil)
		mockGCPService.On("CreateSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler3.CustomSecret{SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "secretID"}}, nil)
		defer func() {
			common.RemoveFromUserAuthCache("secretID")
		}()

		secret, err := GeneratePasswordForVSACluster(mockGCPService, userName)

		assert.NoError(tt, err)
		assert.NotNil(tt, secret)
		mockGCPService.AssertExpectations(tt)
	})
}

func Test_getPasswordFromCacheOrSecretManager(t *testing.T) {
	ctx := context.Background()
	secretID := "test-secret"

	t.Run("PasswordExistsInCache", func(tt *testing.T) {
		common.AddToUserAuthCache(secretID, "cached-password")
		getPasswordForVSACluster := GetPasswordForVSACluster
		defer func() {
			GetPasswordForVSACluster = getPasswordForVSACluster
			common.RemoveFromUserAuthCache(secretID)
		}()
		GetPasswordForVSACluster = func(gcpService hyperscaler.Services, secretID string) (*hyperscaler3.CustomSecret, error) {
			return &hyperscaler3.CustomSecret{SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "cached-password"}}, nil
		}
		password, err := GetPasswordFromCacheOrSecretManager(ctx, secretID)
		assert.Equal(tt, "cached-password", password)
		assert.NoError(tt, err)
	})

	t.Run("PasswordNotInCacheAndSecretManagerSucceeds", func(tt *testing.T) {
		getPasswordForVSACluster := GetPasswordForVSACluster
		originalGcpService := hyperscaler.GetGCPService
		defer func() {
			common.RemoveFromUserAuthCache(secretID)
			GetPasswordForVSACluster = getPasswordForVSACluster
			common.RemoveFromUserAuthCache(secretID)
			hyperscaler.GetGCPService = originalGcpService
		}()

		hyperscaler.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
			return &google.GcpServices{Logger: log.NewLogger()}, nil
		}
		GetPasswordForVSACluster = func(gcpService hyperscaler.Services, secretID string) (*hyperscaler3.CustomSecret, error) {
			return &hyperscaler3.CustomSecret{SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "secret-manager-password"}}, nil
		}
		password, err := GetPasswordFromCacheOrSecretManager(ctx, secretID)
		assert.Equal(tt, "secret-manager-password", password)
		assert.NoError(tt, err)
	})

	t.Run("PasswordNotInCacheAndSecretManagerFails", func(tt *testing.T) {
		originalGcpService := hyperscaler.GetGCPService
		getPasswordForVSACluster := GetPasswordForVSACluster
		defer func() {
			GetPasswordForVSACluster = getPasswordForVSACluster
			common.RemoveFromUserAuthCache(secretID)
			hyperscaler.GetGCPService = originalGcpService
			common.RemoveFromUserAuthCache(secretID)
		}()
		hyperscaler.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
			return &google.GcpServices{Logger: log.NewLogger()}, nil
		}
		GetPasswordForVSACluster = func(gcpService hyperscaler.Services, secretID string) (*hyperscaler3.CustomSecret, error) {
			return nil, assert.AnError
		}
		password, err := GetPasswordFromCacheOrSecretManager(ctx, secretID)
		assert.Equal(tt, "", password)
		assert.Error(tt, err)
	})
}

func Test_deleteVSAClusterPassword(t *testing.T) {
	secretID := "test-secret"

	t.Run("DeleteSecret called when GetSecretWithLatestVersion passes", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(&hyperscaler3.CustomSecret{}, nil)
		mockGCP.On("DeleteSecret", mock.Anything, mock.Anything).Return(nil)

		err := DeletePasswordFromCacheAndSecretManager(mockGCP, secretID)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})

	t.Run("DeleteSecret returns error", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(&hyperscaler3.CustomSecret{}, nil)
		mockGCP.On("DeleteSecret", mock.Anything, mock.Anything).Return(fmt.Errorf("delete error"))

		err := DeletePasswordFromCacheAndSecretManager(mockGCP, secretID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete error")
		mockGCP.AssertExpectations(t)
	})

	t.Run("Delete Secret fails if GetSecretWithLatestVersion fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("get secret error"))

		err := DeletePasswordFromCacheAndSecretManager(mockGCP, secretID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "get secret error")
		mockGCP.AssertExpectations(t)
	})

	t.Run("RemoveFromUserAuthCache returns false", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(&hyperscaler3.CustomSecret{}, nil)
		mockGCP.On("DeleteSecret", mock.Anything, mock.Anything).Return(nil)

		// Mock the cache removal to return false
		origRemove := common.RemoveFromUserAuthCache
		defer func() { common.RemoveFromUserAuthCache = origRemove }()
		common.RemoveFromUserAuthCache = func(secretID string) bool { return false }

		err := DeletePasswordFromCacheAndSecretManager(mockGCP, secretID)
		assert.NoError(t, err) // Should still return nil even if cache removal fails
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate is revoked - GetSecretWithLatestVersion returns nil secret (no error), cache removed", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		// GetSecretWithLatestVersion returns nil, nil (no error, but secret is nil)
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(nil, nil)
		// DeleteSecret should NOT be called since secret is nil
		// We don't set up expectation for DeleteSecret, so if it's called, the test will fail

		// Mock cache removal to return true (cache was removed)
		origRemove := common.RemoveFromUserAuthCache
		defer func() { common.RemoveFromUserAuthCache = origRemove }()
		common.RemoveFromUserAuthCache = func(secretID string) bool { return true }

		err := DeletePasswordFromCacheAndSecretManager(mockGCP, secretID)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
		// Verify DeleteSecret was NOT called
		mockGCP.AssertNotCalled(t, "DeleteSecret", mock.Anything, mock.Anything)
	})

	t.Run("certificate is revoked - GetSecretWithLatestVersion returns nil secret (no error), cache not in cache", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		// GetSecretWithLatestVersion returns nil, nil (no error, but secret is nil)
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(nil, nil)
		// DeleteSecret should NOT be called since secret is nil
		// We don't set up expectation for DeleteSecret, so if it's called, the test will fail

		// Mock cache removal to return false (certificate was not in cache)
		origRemove := common.RemoveFromUserAuthCache
		defer func() { common.RemoveFromUserAuthCache = origRemove }()
		common.RemoveFromUserAuthCache = func(secretID string) bool { return false }

		err := DeletePasswordFromCacheAndSecretManager(mockGCP, secretID)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
		// Verify DeleteSecret was NOT called
		mockGCP.AssertNotCalled(t, "DeleteSecret", mock.Anything, mock.Anything)
	})
}

func Test_GetCertificateAndPrivateKeyByID(t *testing.T) {
	caProjectID := "ca-project"
	smProjectID := "sm-project"
	region := "us-central1"
	caPoolName := "test-pool"
	certificateID := "test-cert"

	t.Run("success", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedCert := &hyperscaler3.CustomCertificate{CertificateID: certificateID}
		expectedSecret := &hyperscaler3.CustomSecret{
			SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"},
		}

		mockGCP.On("GetCertificate", caProjectID, region, caPoolName, certificateID).Return(expectedCert, nil)
		mockGCP.On("GetSecretWithLatestVersion", smProjectID, certificateID).Return(expectedSecret, nil)

		resp, err := GetCertificateAndPrivateKeyByID(mockGCP, caProjectID, smProjectID, region, caPoolName, certificateID)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedCert, resp.Certificate)
		assert.Equal(t, expectedSecret, resp.Secret)
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate error", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetCertificate", caProjectID, region, caPoolName, certificateID).Return(nil, errors.New("cert error"))

		resp, err := GetCertificateAndPrivateKeyByID(mockGCP, caProjectID, smProjectID, region, caPoolName, certificateID)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to get certficate")
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate nil", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetCertificate", caProjectID, region, caPoolName, certificateID).Return(nil, nil)

		resp, err := GetCertificateAndPrivateKeyByID(mockGCP, caProjectID, smProjectID, region, caPoolName, certificateID)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to get certficate")
		mockGCP.AssertExpectations(t)
	})

	t.Run("secret error", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedCert := &hyperscaler3.CustomCertificate{CertificateID: certificateID}
		mockGCP.On("GetCertificate", caProjectID, region, caPoolName, certificateID).Return(expectedCert, nil)
		mockGCP.On("GetSecretWithLatestVersion", smProjectID, certificateID).Return(nil, errors.New("secret error"))

		resp, err := GetCertificateAndPrivateKeyByID(mockGCP, caProjectID, smProjectID, region, caPoolName, certificateID)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to get secret")
		mockGCP.AssertExpectations(t)
	})

	t.Run("secret nil", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedCert := &hyperscaler3.CustomCertificate{CertificateID: certificateID}
		mockGCP.On("GetCertificate", caProjectID, region, caPoolName, certificateID).Return(expectedCert, nil)
		mockGCP.On("GetSecretWithLatestVersion", smProjectID, certificateID).Return(nil, nil)

		resp, err := GetCertificateAndPrivateKeyByID(mockGCP, caProjectID, smProjectID, region, caPoolName, certificateID)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to get secret")
		mockGCP.AssertExpectations(t)
	})

	t.Run("secret version nil", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedCert := &hyperscaler3.CustomCertificate{CertificateID: certificateID}
		secretWithoutVersion := &hyperscaler3.CustomSecret{SecretVersion: nil}
		mockGCP.On("GetCertificate", caProjectID, region, caPoolName, certificateID).Return(expectedCert, nil)
		mockGCP.On("GetSecretWithLatestVersion", smProjectID, certificateID).Return(secretWithoutVersion, nil)

		resp, err := GetCertificateAndPrivateKeyByID(mockGCP, caProjectID, smProjectID, region, caPoolName, certificateID)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to get secret")
		mockGCP.AssertExpectations(t)
	})
}

func Test_GetOrCreateCloudDNSRecord(t *testing.T) {
	recordName := "test-record"
	ipAddress := "1.2.3.4"

	t.Run("record exists", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedRecord := &hyperscaler3.CustomCloudDNSRecord{RecordName: recordName}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetResourceRecordSet", mock.Anything, mock.Anything, recordName).Return(expectedRecord, nil)

		record, err := GetOrCreateCloudDNSRecord(mockGCP, recordName, ipAddress)
		assert.NoError(t, err)
		assert.Equal(t, expectedRecord, record)
		mockGCP.AssertExpectations(t)
	})

	t.Run("record does not exist, create succeeds", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedRecord := &hyperscaler3.CustomCloudDNSRecord{RecordName: recordName}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetResourceRecordSet", mock.Anything, mock.Anything, recordName).Return(nil, nil)
		mockGCP.On("CreateResourceRecordSet", mock.Anything, mock.Anything, ipAddress, recordName).Return(expectedRecord, nil)

		record, err := GetOrCreateCloudDNSRecord(mockGCP, recordName, ipAddress)
		assert.NoError(t, err)
		assert.Equal(t, expectedRecord, record)
		mockGCP.AssertExpectations(t)
	})

	t.Run("record does not exist, create fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetResourceRecordSet", mock.Anything, mock.Anything, recordName).Return(nil, nil)
		mockGCP.On("CreateResourceRecordSet", mock.Anything, mock.Anything, ipAddress, recordName).Return(nil, errors.New("create failed"))

		record, err := GetOrCreateCloudDNSRecord(mockGCP, recordName, ipAddress)
		assert.Error(t, err)
		assert.Nil(t, record)
		mockGCP.AssertExpectations(t)
	})
}

func Test_DeleteCloudDNSRecord(t *testing.T) {
	recordName := "test-record"

	t.Run("record exists, delete succeeds", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetResourceRecordSet", mock.Anything, mock.Anything, recordName).Return(&hyperscaler3.CustomCloudDNSRecord{}, nil)
		mockGCP.On("DeleteResourceRecordSet", mock.Anything, mock.Anything, recordName).Return(nil)

		err := DeleteCloudDNSRecord(mockGCP, recordName)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})

	t.Run("record exists, delete fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetResourceRecordSet", mock.Anything, mock.Anything, recordName).Return(&hyperscaler3.CustomCloudDNSRecord{}, nil)
		mockGCP.On("DeleteResourceRecordSet", mock.Anything, mock.Anything, recordName).Return(errors.New("delete failed"))

		err := DeleteCloudDNSRecord(mockGCP, recordName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
		mockGCP.AssertExpectations(t)
	})

	t.Run("record does not exist", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetResourceRecordSet", mock.Anything, mock.Anything, recordName).Return(nil, nil)

		err := DeleteCloudDNSRecord(mockGCP, recordName)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})
}

func Test_CreateNodeForProvider(t *testing.T) {
	t.Run("USER_CERTIFICATE auth type", func(t *testing.T) {
		input := NodeProviderInput{
			Nodes: []*datamodel.Node{
				{EndpointAddress: "1.2.3.4", HostDNSName: "host1.example.com"},
				{EndpointAddress: "5.6.7.8", HostDNSName: "host2.example.com"},
				{EndpointAddress: "", HostDNSName: "host3.example.com"}, // empty endpoint should be ignored
			},
			DeploymentName: "test-deployment",
			OntapCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-123",
				SecretID:      "secret-123",
				AuthType:      env.USER_CERTIFICATE,
			},
		}

		node := CreateNodeForProvider(input)
		assert.NotNil(t, node)
		assert.Equal(t, "test-deployment", node.DeploymentName)
		assert.Equal(t, "cert-123", node.CertificateID)
		assert.Equal(t, "secret-123", node.SecretID)
		assert.Equal(t, env.USER_CERTIFICATE, node.AuthType)
		assert.Equal(t, "", node.Password) // Password should be empty when not provided in input

		expectedMap := map[string]string{
			"1.2.3.4": "host1.example.com",
			"5.6.7.8": "host2.example.com",
		}
		assert.Equal(t, expectedMap, node.EndpointAddressesToHostNameMap)
	})

	t.Run("non-certificate auth type", func(t *testing.T) {
		input := NodeProviderInput{
			Nodes: []*datamodel.Node{
				{EndpointAddress: "1.2.3.4", HostDNSName: "host1.example.com"},
				{EndpointAddress: "5.6.7.8", HostDNSName: "host2.example.com"},
				{EndpointAddress: "", HostDNSName: "host3.example.com"}, // empty endpoint should be ignored
			},
			DeploymentName: "test-deployment",
			OntapCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
				SecretID: "secret-123",
				AuthType: env.USERNAME_PWD,
			},
		}

		node := CreateNodeForProvider(input)
		assert.NotNil(t, node)
		assert.Equal(t, "test-deployment", node.DeploymentName)
		assert.Equal(t, "", node.CertificateID)
		assert.Equal(t, "secret-123", node.SecretID)
		assert.Equal(t, env.USERNAME_PWD, node.AuthType)
		assert.Equal(t, "test-password", node.Password)

		expectedMap := map[string]string{
			"1.2.3.4": "1.2.3.4",
			"5.6.7.8": "5.6.7.8",
		}
		assert.Equal(t, expectedMap, node.EndpointAddressesToHostNameMap)
	})
}

func Test_PrepareOperationID(t *testing.T) {
	t.Run("valid inputs", func(t *testing.T) {
		projectNumber := "123456789"
		locationId := "us-central1"
		jobId := "job-123"

		expected := "/v1beta/projects/123456789/locations/us-central1/operations/job-123"
		result := PrepareOperationID(projectNumber, locationId, jobId)
		assert.Equal(t, expected, result)
	})

	t.Run("empty project number", func(t *testing.T) {
		result := PrepareOperationID("", "us-central1", "job-123")
		assert.Equal(t, "", result)
	})

	t.Run("empty location id", func(t *testing.T) {
		result := PrepareOperationID("123456789", "", "job-123")
		assert.Equal(t, "", result)
	})

	t.Run("empty job id", func(t *testing.T) {
		result := PrepareOperationID("123456789", "us-central1", "")
		assert.Equal(t, "", result)
	})

	t.Run("all empty", func(t *testing.T) {
		result := PrepareOperationID("", "", "")
		assert.Equal(t, "", result)
	})
}

func Test_GenerateCSR(t *testing.T) {
	commonName := "test.example.com"
	domains := []string{"*.test.example.com", "test.example.com"}

	t.Run("success", func(t *testing.T) {
		csrDER, key, err := GenerateCSR(commonName, domains, true)
		assert.NoError(t, err)
		assert.NotNil(t, csrDER)
		assert.NotNil(t, key)
		assert.Greater(t, len(csrDER), 0)
		assert.Equal(t, 4096, key.Size()*8) // Key size should be 4096 bits
	})

	t.Run("empty common name", func(t *testing.T) {
		csrDER, key, err := GenerateCSR("", domains, true)
		assert.NoError(t, err) // Should still work with empty common name
		assert.NotNil(t, csrDER)
		assert.NotNil(t, key)
	})

	t.Run("empty domains", func(t *testing.T) {
		csrDER, key, err := GenerateCSR(commonName, []string{}, true)
		assert.NoError(t, err) // Should still work with empty domains
		assert.NotNil(t, csrDER)
		assert.NotNil(t, key)
	})

	t.Run("nil domains", func(t *testing.T) {
		csrDER, key, err := GenerateCSR(commonName, nil, true)
		assert.NoError(t, err) // Should still work with nil domains
		assert.NotNil(t, csrDER)
		assert.NotNil(t, key)
	})
}

func Test_GetCertificateFromCacheOrSecretManager_GetGCPServiceError(t *testing.T) {
	ctx := context.Background()
	certificateID := "test-cert-id"

	t.Run("hyperscaler.GetGCPService fails", func(t *testing.T) {
		origGetGCPService := hyperscaler.GetGCPService
		defer func() { hyperscaler.GetGCPService = origGetGCPService }()

		hyperscaler.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
			return nil, errors.New("gcp service error")
		}

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		cert, err := GetCertificateFromCacheOrSecretManager(ctx, poolCredentials)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Contains(t, err.Error(), "gcp service error")
	})

	t.Run("GetCertificateAndPrivateKeyByID fails", func(t *testing.T) {
		origGetGCPService := hyperscaler.GetGCPService
		origGetCertificateAndPrivateKeyByID := GetCertificateAndPrivateKeyByID
		defer func() {
			hyperscaler.GetGCPService = origGetGCPService
			GetCertificateAndPrivateKeyByID = origGetCertificateAndPrivateKeyByID
		}()

		hyperscaler.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
			return &google.GcpServices{}, nil
		}
		GetCertificateAndPrivateKeyByID = func(gcpService hyperscaler.Services, caDeployedProjectID, secretManagerProjectID, region, caPoolName, certificateID string) (*hyperscaler3.CustomCertificateResponse, error) {
			return nil, errors.New("get certificate by id error")
		}

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		cert, err := GetCertificateFromCacheOrSecretManager(ctx, poolCredentials)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Contains(t, err.Error(), "get certificate by id error")
	})
}

func Test_GetPasswordFromCacheOrSecretManager_GetGCPServiceError(t *testing.T) {
	ctx := context.Background()
	secretID := "test-secret-id"

	t.Run("hyperscaler.GetGCPService fails", func(t *testing.T) {
		origGetGCPService := hyperscaler.GetGCPService
		defer func() {
			hyperscaler.GetGCPService = origGetGCPService
			common.RemoveFromUserAuthCache(secretID)
		}()

		hyperscaler.GetGCPService = func(ctx context.Context) (*google.GcpServices, error) {
			return nil, errors.New("gcp service error")
		}

		password, err := GetPasswordFromCacheOrSecretManager(ctx, secretID)
		assert.Error(t, err)
		assert.Equal(t, "", password)
		assert.Contains(t, err.Error(), "gcp service error")
	})
}

func Test_AdditionalMissingLineCoverage(t *testing.T) {
	t.Run("Test constant values", func(t *testing.T) {
		// Test constants for coverage
		assert.Equal(t, "CERTIFICATE REQUEST", CsrType)
		assert.Equal(t, "RSA PRIVATE KEY", RsaKeyType)
		assert.Equal(t, 0x80, DigitalSignature)
		assert.Equal(t, 0x20, KeyEncipherment)
	})

	t.Run("Test PrepareOperationID edge cases", func(t *testing.T) {
		// Test with various combinations of empty strings
		assert.Equal(t, "", PrepareOperationID("", "loc", "job"))
		assert.Equal(t, "", PrepareOperationID("proj", "", "job"))
		assert.Equal(t, "", PrepareOperationID("proj", "loc", ""))
		assert.Equal(t, "", PrepareOperationID("", "", "job"))
		assert.Equal(t, "", PrepareOperationID("proj", "", ""))
		assert.Equal(t, "", PrepareOperationID("", "loc", ""))
	})
}

func Test_GeneratePasswordForVSACluster_AllScenarios(t *testing.T) {
	userName := "test-user"

	t.Run("GetSecretWithLatestVersion succeeds - use existing", func(t *testing.T) {
		mockGCPService := new(hyperscaler.MockServices)
		expectedSecret := &hyperscaler3.CustomSecret{
			SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "existing-password"},
		}
		mockGCPService.On("GetLogger").Return(log.NewLogger())
		mockGCPService.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(expectedSecret, nil)

		secret, err := GeneratePasswordForVSACluster(mockGCPService, userName)
		assert.NoError(t, err)
		assert.Equal(t, expectedSecret, secret)
		mockGCPService.AssertExpectations(t)
	})

	t.Run("Secret generation and cache addition success path", func(t *testing.T) {
		mockGCPService := new(hyperscaler.MockServices)
		originalGeneratePassword := utils.GenerateStrongPassword
		utils.GenerateStrongPassword = func(length int) (string, error) {
			return "generated-password", nil
		}
		defer func() {
			utils.GenerateStrongPassword = originalGeneratePassword
			common.RemoveFromUserAuthCache(userName)
		}()

		expectedSecret := &hyperscaler3.CustomSecret{
			SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "generated-password"},
		}
		mockGCPService.On("GetLogger").Return(log.NewLogger())
		mockGCPService.On("GetSecretWithLatestVersion", mock.Anything, mock.Anything).Return(nil, nil)
		mockGCPService.On("CreateSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(expectedSecret, nil)

		secret, err := GeneratePasswordForVSACluster(mockGCPService, userName)
		assert.NoError(t, err)
		assert.Equal(t, expectedSecret, secret)
		mockGCPService.AssertExpectations(t)
	})
}

func Test_DeleteCertificateAndSecret(t *testing.T) {
	certificateID := "test-cert-id"
	certificate := &hyperscaler3.CustomCertificate{}
	secret := &hyperscaler3.CustomSecret{}

	t.Run("both certificate and secret are not nil, all succeed", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("RevokeCertificate", mock.Anything).Return("", nil)
		mockGCP.On("DeleteSecret", mock.Anything, mock.Anything).Return(nil)

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := DeleteCertificateAndSecret(mockGCP, certificate, secret, poolCredentials)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate revoke fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("RevokeCertificate", mock.Anything).Return("", fmt.Errorf("revoke error"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := DeleteCertificateAndSecret(mockGCP, certificate, nil, poolCredentials)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "revoke error")
		mockGCP.AssertExpectations(t)
	})

	t.Run("secret delete fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("DeleteSecret", mock.Anything, mock.Anything).Return(fmt.Errorf("delete error"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := DeleteCertificateAndSecret(mockGCP, nil, secret, poolCredentials)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete error")
		mockGCP.AssertExpectations(t)
	})

	t.Run("both certificate and secret are nil", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := DeleteCertificateAndSecret(mockGCP, nil, nil, poolCredentials)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})
}

func Test_GetCertificateAndSecret(t *testing.T) {
	certificateID := "test-cert-id"
	expectedCert := &hyperscaler3.CustomCertificate{CertificateID: certificateID}
	expectedSecret := &hyperscaler3.CustomSecret{SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"}}

	t.Run("success", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetCertificate", env.CaPoolDeployedProjectID, env.Region, env.CaPoolName, certificateID).Return(expectedCert, nil)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(expectedSecret, nil)

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		cert, secret, err := GetCertificateAndSecret(mockGCP, poolCredentials)
		assert.NoError(t, err)
		assert.Equal(t, expectedCert, cert)
		assert.Equal(t, expectedSecret, secret)
		mockGCP.AssertExpectations(t)
	})

	t.Run("GetCertificate fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetCertificate", env.CaPoolDeployedProjectID, env.Region, env.CaPoolName, certificateID).Return(nil, fmt.Errorf("cert error"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		cert, secret, err := GetCertificateAndSecret(mockGCP, poolCredentials)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Nil(t, secret)
	})

	t.Run("GetSecretWithLatestVersion fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("GetCertificate", env.CaPoolDeployedProjectID, env.Region, env.CaPoolName, certificateID).Return(expectedCert, nil)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(nil, fmt.Errorf("secret error"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		cert, secret, err := GetCertificateAndSecret(mockGCP, poolCredentials)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Nil(t, secret)
	})
}

func Test_CreateCertificateInCAS(t *testing.T) {
	certificate := &hyperscaler3.CustomCertificate{
		SubjectCommonName: "test-cn",
		CertificateID:     "cert-123",
	}
	t.Run("success", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("CreateCertificate", certificate).Return(certificate, nil)

		cert, err := CreateCertificateInCAS(mockGCP, certificate)
		assert.NoError(t, err)
		assert.Equal(t, certificate, cert)
		mockGCP.AssertExpectations(t)
	})

	t.Run("CreateCertificate fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("CreateCertificate", certificate).Return(nil, fmt.Errorf("create error"))

		cert, err := CreateCertificateInCAS(mockGCP, certificate)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Contains(t, err.Error(), "create error")
		mockGCP.AssertExpectations(t)
	})
}

func Test_CreatePrivateKeyInSecretManager(t *testing.T) {
	certificate := &hyperscaler3.CustomCertificate{
		SubjectCommonName: "test-cn",
		CertificateID:     "cert-123",
		Region:            "us-central1",
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)

	t.Run("success", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedSecret := &hyperscaler3.CustomSecret{SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"}}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("CreateSecret", env.SecretManagerProjectID, certificate.Region, certificate.CertificateID, mock.Anything).Return(expectedSecret, nil)

		secret, err := CreatePrivateKeyInSecretManager(mockGCP, certificate, key)
		assert.NoError(t, err)
		assert.Equal(t, expectedSecret, secret)
		mockGCP.AssertExpectations(t)
	})

	t.Run("failure", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		mockGCP.On("CreateSecret", env.SecretManagerProjectID, certificate.Region, certificate.CertificateID, mock.Anything).Return(nil, fmt.Errorf("create failed"))
		secret, err := CreatePrivateKeyInSecretManager(mockGCP, certificate, key)
		assert.Nil(t, secret)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "create failed")
		mockGCP.AssertExpectations(t)
	})
}

func Test_CreateCertificateInCASAndPrivateKeyInSM(t *testing.T) {
	certificateID := "test-cert-id"
	clusterName := "test-cluster"
	username := "test-user"
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	expectedDomains := []string{fmt.Sprintf("*.%s.%s", clusterName, env.VsaDeployedDnsName)}

	// Patch GenerateCSR and ValidateAndConvertCertParam

	t.Run("success", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origGenerateCSR := GenerateCSR
		origValidate := common2.ValidateAndConvertCertParams
		origCreateCertificateInCAS := CreateCertificateInCAS
		origCreatePrivateKeyInSecretManager := CreatePrivateKeyInSecretManager

		defer func() {
			GenerateCSR = origGenerateCSR
			common2.ValidateAndConvertCertParams = origValidate
			CreateCertificateInCAS = origCreateCertificateInCAS
			CreatePrivateKeyInSecretManager = origCreatePrivateKeyInSecretManager
		}()
		GenerateCSR = func(commonName string, domains []string, isServerAuthEnabled bool) ([]byte, *rsa.PrivateKey, error) {
			assert.Equal(t, username, commonName)
			assert.Equal(t, expectedDomains, domains)
			return []byte("csr"), key, nil
		}
		common2.ValidateAndConvertCertParams = func(param *hyperscaler3.CustomCertificateParam, pemBlock pem.Block) (*hyperscaler3.CustomCertificate, error) {
			assert.Equal(t, certificateID, param.CertificateID)
			return &hyperscaler3.CustomCertificate{CertificateID: certificateID}, nil
		}
		CreateCertificateInCAS = func(gcpService hyperscaler.Services, cert *hyperscaler3.CustomCertificate) (*hyperscaler3.CustomCertificate, error) {
			return cert, nil
		}
		CreatePrivateKeyInSecretManager = func(gcpService hyperscaler.Services, cert *hyperscaler3.CustomCertificate, k *rsa.PrivateKey) (*hyperscaler3.CustomSecret, error) {
			return &hyperscaler3.CustomSecret{SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"}}, nil
		}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		cert, secret, err := CreateCertificateInCASAndPrivateKeyInSM(mockGCP, certificateID, clusterName, username, nil, true)
		assert.NoError(t, err)
		assert.NotNil(t, cert)
		assert.NotNil(t, secret)
	})

	t.Run("GenerateCSR fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origGenerateCSR := GenerateCSR

		defer func() {
			GenerateCSR = origGenerateCSR
		}()
		GenerateCSR = func(commonName string, domains []string, isServerAuthEnabled bool) ([]byte, *rsa.PrivateKey, error) {
			return nil, nil, fmt.Errorf("csr error")
		}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		cert, secret, err := CreateCertificateInCASAndPrivateKeyInSM(mockGCP, certificateID, clusterName, username, nil, true)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Nil(t, secret)
	})

	t.Run("ValidateAndConvertCertParams fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origGenerateCSR := GenerateCSR
		origValidate := common2.ValidateAndConvertCertParams

		defer func() {
			GenerateCSR = origGenerateCSR
			common2.ValidateAndConvertCertParams = origValidate
		}()
		GenerateCSR = func(commonName string, domains []string, isServerAuthEnabled bool) ([]byte, *rsa.PrivateKey, error) {
			return []byte("csr"), key, nil
		}
		common2.ValidateAndConvertCertParams = func(param *hyperscaler3.CustomCertificateParam, pemBlock pem.Block) (*hyperscaler3.CustomCertificate, error) {
			return nil, fmt.Errorf("validate error")
		}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		cert, secret, err := CreateCertificateInCASAndPrivateKeyInSM(mockGCP, certificateID, clusterName, username, nil, true)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Nil(t, secret)
	})

	t.Run("CreateCertificateInCAS fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origGenerateCSR := GenerateCSR
		origValidate := common2.ValidateAndConvertCertParams
		origCreateCertificateInCAS := CreateCertificateInCAS

		defer func() {
			GenerateCSR = origGenerateCSR
			common2.ValidateAndConvertCertParams = origValidate
			CreateCertificateInCAS = origCreateCertificateInCAS
		}()
		GenerateCSR = func(commonName string, domains []string, isServerAuthEnabled bool) ([]byte, *rsa.PrivateKey, error) {
			return []byte("csr"), key, nil
		}
		common2.ValidateAndConvertCertParams = func(param *hyperscaler3.CustomCertificateParam, pemBlock pem.Block) (*hyperscaler3.CustomCertificate, error) {
			return &hyperscaler3.CustomCertificate{CertificateID: certificateID}, nil
		}
		CreatePrivateKeyInSecretManager = func(gcpService hyperscaler.Services, cert *hyperscaler3.CustomCertificate, k *rsa.PrivateKey) (*hyperscaler3.CustomSecret, error) {
			return &hyperscaler3.CustomSecret{SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"}}, nil
		}
		CreateCertificateInCAS = func(gcpService hyperscaler.Services, cert *hyperscaler3.CustomCertificate) (*hyperscaler3.CustomCertificate, error) {
			return nil, fmt.Errorf("cas error")
		}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		cert, secret, err := CreateCertificateInCASAndPrivateKeyInSM(mockGCP, certificateID, clusterName, username, nil, true)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Nil(t, secret)
	})

	t.Run("CreatePrivateKeyInSecretManager fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origGenerateCSR := GenerateCSR
		origValidate := common2.ValidateAndConvertCertParams
		origCreateCertificateInCAS := CreateCertificateInCAS
		origCreatePrivateKeyInSecretManager := CreatePrivateKeyInSecretManager

		defer func() {
			GenerateCSR = origGenerateCSR
			common2.ValidateAndConvertCertParams = origValidate
			CreateCertificateInCAS = origCreateCertificateInCAS
			CreatePrivateKeyInSecretManager = origCreatePrivateKeyInSecretManager
		}()
		GenerateCSR = func(commonName string, domains []string, isServerAuthEnabled bool) ([]byte, *rsa.PrivateKey, error) {
			return []byte("csr"), key, nil
		}
		common2.ValidateAndConvertCertParams = func(param *hyperscaler3.CustomCertificateParam, pemBlock pem.Block) (*hyperscaler3.CustomCertificate, error) {
			return &hyperscaler3.CustomCertificate{CertificateID: certificateID}, nil
		}
		CreatePrivateKeyInSecretManager = func(gcpService hyperscaler.Services, cert *hyperscaler3.CustomCertificate, k *rsa.PrivateKey) (*hyperscaler3.CustomSecret, error) {
			return nil, fmt.Errorf("sm error")
		}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		cert, secret, err := CreateCertificateInCASAndPrivateKeyInSM(mockGCP, certificateID, clusterName, username, nil, true)
		assert.Error(t, err)
		assert.Nil(t, cert)
		assert.Nil(t, secret)
	})
}

func Test_GenerateAndCreateCertificateForVSACluster(t *testing.T) {
	certificateID := "test-cert-id"
	clusterName := "test-cluster"
	username := "test-user"

	t.Run("returns cached certificate and secret if found", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedCert := &hyperscaler3.CustomCertificate{
			SubjectCommonName:   "test-cn",
			PemCertificate:      "pem-cert",
			PemCertificateChain: []string{"chain1", "chain2"},
		}
		expectedSecret := &hyperscaler3.CustomSecret{
			SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"},
		}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		// Mock GetCertificate with the parameters that will be used (defaultCredentials has env vars)
		mockGCP.On("GetCertificate", env.CaPoolDeployedProjectID, env.Region, env.CaPoolName, certificateID).Return(expectedCert, nil)
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, certificateID).Return(&hyperscaler3.CustomSecret{SecretVersion: expectedSecret.SecretVersion}, nil)
		poolCreds := &datamodel.PoolCredentials{CertificateID: certificateID}
		resp, err := GenerateAndCreateCertificateForVSACluster(mockGCP, clusterName, username, poolCreds, true)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedCert, resp.Certificate)
		assert.Equal(t, expectedSecret, resp.Secret)
	})

	t.Run("GetCertificateAndSecret returns error", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		// Mock GetCertificate to return error
		mockGCP.On("GetCertificate", env.CaPoolDeployedProjectID, env.Region, env.CaPoolName, certificateID).Return(nil, fmt.Errorf("get cert error"))
		poolCreds := &datamodel.PoolCredentials{CertificateID: certificateID}
		resp, err := GenerateAndCreateCertificateForVSACluster(mockGCP, clusterName, username, poolCreds, true)
		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("DeleteCertificateAndSecret returns error", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		// Mock GetCertificate to return existing cert (so certificate is non-nil)
		existingCert := &hyperscaler3.CustomCertificate{CertificateID: certificateID}
		mockGCP.On("GetCertificate", env.CaPoolDeployedProjectID, env.Region, env.CaPoolName, certificateID).Return(existingCert, nil)
		// Mock GetSecretWithLatestVersion to return nil, nil (404 handled - googleResourceNotFoundCheck returns nil for 404)
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, certificateID).Return(nil, nil)
		// Now when certificate is non-nil and secret is nil, delete path will be triggered
		// Mock RevokeCertificate to return error (this will cause _deleteCertificateAndSecret to fail)
		// RevokeCertificate is called with a CustomCertificate object and returns (string, error)
		mockGCP.On("RevokeCertificate", mock.MatchedBy(func(cert *hyperscaler3.CustomCertificate) bool {
			return cert != nil && cert.CertificateID == certificateID
		})).Return("", fmt.Errorf("delete error"))
		poolCreds := &datamodel.PoolCredentials{CertificateID: certificateID}
		resp, err := GenerateAndCreateCertificateForVSACluster(mockGCP, clusterName, username, poolCreds, true)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "delete error")
	})

	t.Run("CreateCertificateInCASAndPrivateKeyInSM returns error", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		mockGCP.On("GetLogger").Return(log.NewLogger())
		// Mock GetCertificate to return nil (404 handled - cert doesn't exist)
		mockGCP.On("GetCertificate", env.CaPoolDeployedProjectID, env.Region, env.CaPoolName, certificateID).Return(nil, nil)
		// Mock GetSecretWithLatestVersion to return nil (404 handled - secret doesn't exist)
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, certificateID).Return(nil, nil)
		// Mock DeleteSecret (called during cleanup)
		mockGCP.On("DeleteSecret", mock.Anything, certificateID).Return(nil)
		// Mock certificate creation functions to fail
		originalGenerateCSR := GenerateCSR
		originalValidateAndConvertCertParams := common2.ValidateAndConvertCertParams
		originalCreateCertificateInCAS := CreateCertificateInCAS
		defer func() {
			GenerateCSR = originalGenerateCSR
			common2.ValidateAndConvertCertParams = originalValidateAndConvertCertParams
			CreateCertificateInCAS = originalCreateCertificateInCAS
		}()
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		GenerateCSR = func(commonName string, domains []string, isServerAuthEnabled bool) ([]byte, *rsa.PrivateKey, error) {
			return []byte("csr"), key, nil
		}
		common2.ValidateAndConvertCertParams = func(param *hyperscaler3.CustomCertificateParam, pemBlock pem.Block) (*hyperscaler3.CustomCertificate, error) {
			return &hyperscaler3.CustomCertificate{CertificateID: certificateID}, nil
		}
		CreateCertificateInCAS = func(gcpService hyperscaler.Services, cert *hyperscaler3.CustomCertificate) (*hyperscaler3.CustomCertificate, error) {
			return nil, fmt.Errorf("create error")
		}
		poolCreds := &datamodel.PoolCredentials{CertificateID: certificateID}
		resp, err := GenerateAndCreateCertificateForVSACluster(mockGCP, clusterName, username, poolCreds, true)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "create error")
	})

	t.Run("successfully creates new certificate and secret", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		expectedCert := &hyperscaler3.CustomCertificate{
			SubjectCommonName:   "test-cn",
			PemCertificate:      "pem-cert",
			PemCertificateChain: []string{"chain1", "chain2"},
		}
		expectedSecret := &hyperscaler3.CustomSecret{
			SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"},
		}
		mockGCP.On("GetLogger").Return(log.NewLogger())
		// Mock GetCertificate to return nil (404 handled - cert doesn't exist)
		mockGCP.On("GetCertificate", env.CaPoolDeployedProjectID, env.Region, env.CaPoolName, certificateID).Return(nil, nil)
		// Mock GetSecretWithLatestVersion to return nil (404 handled - secret doesn't exist)
		mockGCP.On("GetSecretWithLatestVersion", mock.Anything, certificateID).Return(nil, nil)
		// Mock DeleteSecret (called during cleanup)
		mockGCP.On("DeleteSecret", mock.Anything, certificateID).Return(nil)
		// Mock certificate creation functions
		originalGenerateCSR := GenerateCSR
		originalValidateAndConvertCertParams := common2.ValidateAndConvertCertParams
		originalCreateCertificateInCAS := CreateCertificateInCAS
		originalCreatePrivateKeyInSecretManager := CreatePrivateKeyInSecretManager
		defer func() {
			GenerateCSR = originalGenerateCSR
			common2.ValidateAndConvertCertParams = originalValidateAndConvertCertParams
			CreateCertificateInCAS = originalCreateCertificateInCAS
			CreatePrivateKeyInSecretManager = originalCreatePrivateKeyInSecretManager
		}()
		key, _ := rsa.GenerateKey(rand.Reader, 2048)
		GenerateCSR = func(commonName string, domains []string, isServerAuthEnabled bool) ([]byte, *rsa.PrivateKey, error) {
			assert.Equal(t, username, commonName)
			return []byte("csr"), key, nil
		}
		common2.ValidateAndConvertCertParams = func(param *hyperscaler3.CustomCertificateParam, pemBlock pem.Block) (*hyperscaler3.CustomCertificate, error) {
			assert.Equal(t, certificateID, param.CertificateID)
			return expectedCert, nil
		}
		CreateCertificateInCAS = func(gcpService hyperscaler.Services, cert *hyperscaler3.CustomCertificate) (*hyperscaler3.CustomCertificate, error) {
			return expectedCert, nil
		}
		CreatePrivateKeyInSecretManager = func(gcpService hyperscaler.Services, cert *hyperscaler3.CustomCertificate, k *rsa.PrivateKey) (*hyperscaler3.CustomSecret, error) {
			return expectedSecret, nil
		}
		poolCreds := &datamodel.PoolCredentials{CertificateID: certificateID}
		resp, err := GenerateAndCreateCertificateForVSACluster(mockGCP, clusterName, username, poolCreds, true)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, expectedCert, resp.Certificate)
		assert.Equal(t, expectedSecret, resp.Secret)
	})
}

func Test_RevokeCertificateAndDeleteFromCacheAndSecretManager(t *testing.T) {
	certificateID := "test-cert-id"
	mockLogger := log.NewLogger()
	cert := &hyperscaler3.CustomCertificate{}
	secret := &hyperscaler3.CustomSecret{SecretVersion: &hyperscaler3.CustomSecretVersion{Value: "private-key"}}

	t.Run("success", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		origDeleteCertificateAndSecret := DeleteCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
			DeleteCertificateAndSecret = origDeleteCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return cert, secret, nil
		}
		DeleteCertificateAndSecret = func(gcpService hyperscaler.Services, certificate *hyperscaler3.CustomCertificate, secret *hyperscaler3.CustomSecret, poolCredentials *datamodel.PoolCredentials) error {
			return nil
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return true }
		mockGCP.On("GetLogger").Return(mockLogger)

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err)
	})

	t.Run("GetCertificateAndSecret fails - best-effort cleanup so pool deletion can continue", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, fmt.Errorf("get cert error")
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return true }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(nil, fmt.Errorf("secret not found"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})

	t.Run("DeleteCertificateAndSecret fails", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		origDeleteCertificateAndSecret := DeleteCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
			DeleteCertificateAndSecret = origDeleteCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return cert, secret, nil
		}
		DeleteCertificateAndSecret = func(gcpService hyperscaler.Services, certificate *hyperscaler3.CustomCertificate, secret *hyperscaler3.CustomSecret, poolCredentials *datamodel.PoolCredentials) error {
			return fmt.Errorf("delete error")
		}
		mockGCP.On("GetLogger").Return(mockLogger)

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete error")
	})

	t.Run("RemoveFromCertAuthCache returns false", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		origDeleteCertificateAndSecret := DeleteCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
			DeleteCertificateAndSecret = origDeleteCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return cert, secret, nil
		}
		DeleteCertificateAndSecret = func(gcpService hyperscaler.Services, certificate *hyperscaler3.CustomCertificate, secret *hyperscaler3.CustomSecret, poolCredentials *datamodel.PoolCredentials) error {
			return nil
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return false }
		mockGCP.On("GetLogger").Return(mockLogger)

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err)
	})

	t.Run("certificate is revoked - secret exists and deleted successfully, cache removed", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, fmt.Errorf("certificate is revoked and cannot be used")
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return true }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(&hyperscaler3.CustomSecret{}, nil)
		mockGCP.On("DeleteSecret", env.SecretManagerProjectID, certificateID).Return(nil)

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate is revoked - secret exists and deleted successfully, cache not in cache", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, fmt.Errorf("certificate is revoked and cannot be used")
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return false }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(&hyperscaler3.CustomSecret{}, nil)
		mockGCP.On("DeleteSecret", env.SecretManagerProjectID, certificateID).Return(nil)

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate is revoked - secret not found, cache removed", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, fmt.Errorf("certificate is revoked and cannot be used")
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return true }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(nil, fmt.Errorf("secret not found"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err) // Should not return error even if secret doesn't exist
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate is revoked - secret not found, cache not in cache", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, fmt.Errorf("certificate is revoked and cannot be used")
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return false }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(nil, fmt.Errorf("secret not found"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err) // Should not return error even if secret doesn't exist
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate is revoked - secret exists but DeleteSecret fails, cache removed", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, fmt.Errorf("certificate is revoked and cannot be used")
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return true }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(&hyperscaler3.CustomSecret{}, nil)
		mockGCP.On("DeleteSecret", env.SecretManagerProjectID, certificateID).Return(fmt.Errorf("delete secret failed"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err) // Should not return error even if DeleteSecret fails
		mockGCP.AssertExpectations(t)
	})

	t.Run("certificate is revoked - secret exists but DeleteSecret fails, cache not in cache", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, fmt.Errorf("certificate is revoked and cannot be used")
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return false }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(&hyperscaler3.CustomSecret{}, nil)
		mockGCP.On("DeleteSecret", env.SecretManagerProjectID, certificateID).Return(fmt.Errorf("delete secret failed"))

		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err) // Should not return error even if DeleteSecret fails
		mockGCP.AssertExpectations(t)
	})

	t.Run("permission denied (403) - log and continue so pool deletion can succeed", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		permissionDeniedErr := fmt.Errorf("googleapi: Error 403: Permission 'privateca.certificates.get' denied on 'projects/266893635349/locations/us-east4/caPools/vsa-pool-ca/certificates/gcnv-9471c3a41714607-cert', forbidden")
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, permissionDeniedErr
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return true }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(nil, fmt.Errorf("secret not found"))
		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})

	t.Run("permission denied (403) - secret exists and deleted successfully, cache removed", func(t *testing.T) {
		mockGCP := new(hyperscaler.MockServices)
		origRemoveFromCertAuthCache := common.RemoveFromCertAuthCache
		origGetCertificateAndSecret := GetCertificateAndSecret
		defer func() {
			common.RemoveFromCertAuthCache = origRemoveFromCertAuthCache
			GetCertificateAndSecret = origGetCertificateAndSecret
		}()
		GetCertificateAndSecret = func(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscaler3.CustomCertificate, *hyperscaler3.CustomSecret, error) {
			return nil, nil, fmt.Errorf("googleapi: Error 403: Permission denied, forbidden")
		}
		common.RemoveFromCertAuthCache = func(certID string) bool { return true }
		mockGCP.On("GetLogger").Return(mockLogger)
		mockGCP.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, certificateID).Return(&hyperscaler3.CustomSecret{}, nil)
		mockGCP.On("DeleteSecret", env.SecretManagerProjectID, certificateID).Return(nil)
		poolCredentials := &datamodel.PoolCredentials{CertificateID: certificateID}
		err := RevokeCertificateAndDeleteFromCacheAndSecretManager(mockGCP, poolCredentials)
		assert.NoError(t, err)
		mockGCP.AssertExpectations(t)
	})
}

func Test_GetProviderByNodeWithFastConnection(t *testing.T) {
	ctx := context.Background()

	t.Run("USER_CERTIFICATE success", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			CertificateID:                  "cert-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "1.2.3.4"},
			AuthType:                       env.USER_CERTIFICATE,
		}

		origGetCert := GetCertificateFromCacheOrSecretManager
		defer func() { GetCertificateFromCacheOrSecretManager = origGetCert }()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return &models.Certificate{
				SignedCertificate:        "signed",
				InterMediateCertificates: []string{"intermediate"},
				CommonName:               "common",
				PrivateKey:               "key",
			}, nil
		}

		provider, err := GetProviderByNodeWithFastConnection(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("USER_CERTIFICATE get certificate error", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			CertificateID:                  "cert-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "1.2.3.4"},
			AuthType:                       env.USER_CERTIFICATE,
		}

		origGetCert := GetCertificateFromCacheOrSecretManager
		defer func() { GetCertificateFromCacheOrSecretManager = origGetCert }()
		GetCertificateFromCacheOrSecretManager = func(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
			return nil, fmt.Errorf("certificate not found")
		}

		provider, err := GetProviderByNodeWithFastConnection(ctx, node)
		assert.Error(t, err)
		assert.Nil(t, provider)
	})

	t.Run("USERNAME_PWD_SEC_MGR success", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			SecretID:                       "secret-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "1.2.3.4"},
			AuthType:                       env.USERNAME_PWD_SEC_MGR,
		}

		origGetPassword := GetPasswordFromCacheOrSecretManager
		defer func() { GetPasswordFromCacheOrSecretManager = origGetPassword }()
		GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
			return "password123", nil
		}

		provider, err := GetProviderByNodeWithFastConnection(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("USERNAME_PWD_SEC_MGR get password error", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			SecretID:                       "secret-id",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "1.2.3.4"},
			AuthType:                       env.USERNAME_PWD_SEC_MGR,
		}

		origGetPassword := GetPasswordFromCacheOrSecretManager
		defer func() { GetPasswordFromCacheOrSecretManager = origGetPassword }()
		GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
			return "", fmt.Errorf("secret not found")
		}

		provider, err := GetProviderByNodeWithFastConnection(ctx, node)
		assert.Error(t, err)
		assert.Nil(t, provider)
	})

	t.Run("USERNAME_PWD auth type with password", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			Password:                       "password123",
			EndpointAddressesToHostNameMap: map[string]string{"1.2.3.4": "1.2.3.4"},
			AuthType:                       env.USERNAME_PWD, // Different auth type that uses password directly
		}

		provider, err := GetProviderByNodeWithFastConnection(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("empty endpoint addresses map with endpoint address", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			Password:                       "password123",
			EndpointAddress:                "192.168.1.1",
			EndpointAddressesToHostNameMap: map[string]string{}, // Empty map
			AuthType:                       env.USERNAME_PWD,
		}

		provider, err := GetProviderByNodeWithFastConnection(ctx, node)
		assert.NoError(t, err)
		assert.NotNil(t, provider)
		// Check that the endpoint address was added to the map
		assert.Equal(t, "192.168.1.1", node.EndpointAddressesToHostNameMap["192.168.1.1"])
	})

	t.Run("empty endpoint addresses map and empty endpoint address", func(t *testing.T) {
		node := &models.Node{
			Name:                           "node1",
			Password:                       "password123",
			EndpointAddress:                "",                  // Empty endpoint address
			EndpointAddressesToHostNameMap: map[string]string{}, // Empty map
			AuthType:                       env.USERNAME_PWD,
		}

		provider, err := GetProviderByNodeWithFastConnection(ctx, node)
		assert.Error(t, err)
		assert.Nil(t, provider)
	})
}

// Test_validateOCIVaultConfig exercises the pre-flight validator that
// _generatePasswordForVSAClusterOCI and _deletePasswordForVSAClusterOCI rely on
// to surface missing OCI vault env vars as ErrOCIClientInitializationError
// instead of an opaque OCI HTTP 400 from the SDK.
func Test_validateOCIVaultConfig(t *testing.T) {
	// Snapshot and restore the env-package globals so the subtests don't leak
	// state into each other or into other tests in this package.
	origVault := env.OCIVaultOCID
	origCompartment := env.OCICompartmentOCID
	origMasterKey := env.OCIMasterKeyOCID
	defer func() {
		env.OCIVaultOCID = origVault
		env.OCICompartmentOCID = origCompartment
		env.OCIMasterKeyOCID = origMasterKey
	}()

	t.Run("write mode — all three vars set returns nil", func(t *testing.T) {
		env.OCIVaultOCID = "ocid1.vault.oc1..test"
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIMasterKeyOCID = "ocid1.key.oc1..test"

		assert.NoError(t, _validateOCIVaultConfig(true))
	})

	t.Run("write mode — only vault set reports compartment + master key missing", func(t *testing.T) {
		env.OCIVaultOCID = "ocid1.vault.oc1..test"
		env.OCICompartmentOCID = ""
		env.OCIMasterKeyOCID = ""

		err := _validateOCIVaultConfig(true)
		if !assert.Error(t, err) {
			return
		}

		cerr, ok := err.(*coreerrors.CustomError)
		if !assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
			return
		}
		assert.Equal(t, coreerrors.ErrOCIClientInitializationError, cerr.TrackingID)
		// CustomError.Error() returns the canned message from the error map; the
		// dynamic list of missing env var names lives on OriginalErr.
		assert.NotNil(t, cerr.OriginalErr)
		origMsg := cerr.OriginalErr.Error()
		assert.Contains(t, origMsg, "OCI_COMPARTMENT_OCID")
		assert.Contains(t, origMsg, "OCI_MASTER_KEY_OCID")
		assert.NotContains(t, origMsg, "OCI_VAULT_OCID")
	})

	t.Run("write mode — all empty reports all three", func(t *testing.T) {
		env.OCIVaultOCID = ""
		env.OCICompartmentOCID = ""
		env.OCIMasterKeyOCID = ""

		err := _validateOCIVaultConfig(true)
		if !assert.Error(t, err) {
			return
		}
		cerr, ok := err.(*coreerrors.CustomError)
		if !assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
			return
		}
		assert.NotNil(t, cerr.OriginalErr)
		origMsg := cerr.OriginalErr.Error()
		assert.Contains(t, origMsg, "OCI_VAULT_OCID")
		assert.Contains(t, origMsg, "OCI_COMPARTMENT_OCID")
		assert.Contains(t, origMsg, "OCI_MASTER_KEY_OCID")
	})

	t.Run("read mode — only vault required, all empty reports vault only", func(t *testing.T) {
		env.OCIVaultOCID = ""
		env.OCICompartmentOCID = ""
		env.OCIMasterKeyOCID = ""

		err := _validateOCIVaultConfig(false)
		if !assert.Error(t, err) {
			return
		}

		cerr, ok := err.(*coreerrors.CustomError)
		if !assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
			return
		}
		assert.Equal(t, coreerrors.ErrOCIClientInitializationError, cerr.TrackingID)
		assert.NotNil(t, cerr.OriginalErr)
		origMsg := cerr.OriginalErr.Error()
		assert.Contains(t, origMsg, "OCI_VAULT_OCID")
		assert.NotContains(t, origMsg, "OCI_COMPARTMENT_OCID")
		assert.NotContains(t, origMsg, "OCI_MASTER_KEY_OCID")
	})

	t.Run("read mode — vault set returns nil regardless of write-only vars", func(t *testing.T) {
		env.OCIVaultOCID = "ocid1.vault.oc1..test"
		env.OCICompartmentOCID = ""
		env.OCIMasterKeyOCID = ""

		assert.NoError(t, _validateOCIVaultConfig(false))
	})
}

// ---------------------------------------------------------------------------
// _getCertificateFromCacheOrCAS
// ---------------------------------------------------------------------------
//
// The OCI cert cache helper has three observable shapes: bad input, cache hit
// (no OCI round-trip), and OCI-service init failure. The full CAS round-trip
// path is exercised end-to-end via the provider tests above (which monkey-patch
// GetCertificateFromCacheOrCAS), so we don't reach into ociService here.

func Test_GetCertificateFromCacheOrCAS_NilRef(t *testing.T) {
	cert, err := _getCertificateFromCacheOrCAS(context.Background(), nil)
	assert.Nil(t, cert)
	assert.Error(t, err)

	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_GetCertificateFromCacheOrCAS_EmptyName(t *testing.T) {
	cert, err := _getCertificateFromCacheOrCAS(context.Background(), &datamodel.ExternalCredRef{
		Name:               "",
		ExternalIdentifier: "ocid1.certificate.oc1..abc",
	})
	assert.Nil(t, cert)
	assert.Error(t, err)

	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_GetCertificateFromCacheOrCAS_EmptyExternalIdentifier(t *testing.T) {
	cert, err := _getCertificateFromCacheOrCAS(context.Background(), &datamodel.ExternalCredRef{
		Name:               "dep-cert",
		ExternalIdentifier: "",
	})
	assert.Nil(t, cert)
	assert.Error(t, err)

	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_GetCertificateFromCacheOrCAS_CacheHit(t *testing.T) {
	// Use a unique cache key to avoid collisions with other tests that share
	// the process-scoped cert-auth cache.
	const cacheKey = "test-getcertfromcache-hit-cert"

	origGet := common.GetCertAuthCache
	defer func() { common.GetCertAuthCache = origGet }()
	common.GetCertAuthCache = func(key string) (*models.CertCache, bool) {
		assert.Equal(t, cacheKey, key)
		return &models.CertCache{Certificate: &models.Certificate{
			CommonName:        "cached-cn",
			SignedCertificate: "cached-cert",
			PrivateKey:        "cached-key",
		}}, true
	}

	// If the cache hit is honoured, GetOCIService must NOT be called.
	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		t.Fatalf("OCI service must not be initialised when cache contains a value")
		return nil, nil
	}

	cert, err := _getCertificateFromCacheOrCAS(context.Background(), &datamodel.ExternalCredRef{
		Name:               cacheKey,
		ExternalIdentifier: "ocid1.certificate.oc1..abc",
	})
	assert.NoError(t, err)
	if assert.NotNil(t, cert) {
		assert.Equal(t, "cached-cn", cert.CommonName)
		assert.Equal(t, "cached-cert", cert.SignedCertificate)
		assert.Equal(t, "cached-key", cert.PrivateKey)
	}
}

func Test_GetCertificateFromCacheOrCAS_GetOCIServiceFails(t *testing.T) {
	const cacheKey = "test-getcertfromcache-svcfail-cert"

	origGet := common.GetCertAuthCache
	defer func() { common.GetCertAuthCache = origGet }()
	// Cache miss so the function falls through to GetOCIService.
	common.GetCertAuthCache = func(key string) (*models.CertCache, bool) {
		return nil, false
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		return nil, fmt.Errorf("oci client init failed")
	}

	cert, err := _getCertificateFromCacheOrCAS(context.Background(), &datamodel.ExternalCredRef{
		Name:               cacheKey,
		ExternalIdentifier: "ocid1.certificate.oc1..abc",
	})
	assert.Nil(t, cert)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "oci client init failed")
}

// ---------------------------------------------------------------------------
// _getOrCreateOCIDNSRecord and _deleteOCIDNSRecord
// ---------------------------------------------------------------------------
//
// _getOrCreateOCIDNSRecord keeps the get-then-create semantics of the GCP
// variant, while _deleteOCIDNSRecord now deletes directly (no pre-fetch) and
// relies on DeleteDnsRecord's idempotent not-found handling. Because the
// underlying SDK methods are concrete (not vars), we exercise them by wiring an
// HTTP-level dispatcher into a real DnsClient via NewAdminOCIServiceWithAllClients.

// newTestOciServicesWithDNSForHyperscaler is the DNS-aware counterpart of
// newTestOciServicesForHyperscaler. It returns an OciServices with only a
// DNS client wired (vault/secrets/cert clients are deliberately zero-valued)
// because the helpers under test never touch any of those.
func newTestOciServicesWithDNSForHyperscaler(t *testing.T, dnsDispatcher *mockOCIHTTPDispatcher) *oci2.OciServices {
	t.Helper()
	ctx := context.Background()
	pemKey := testOCIRSAPrivateKeyPEM(t)

	configProvider := ocicommon.NewRawConfigurationProvider(
		"ocid1.tenancy.oc1..test",
		"ocid1.user.oc1..test",
		"us-ashburn-1",
		"aa:bb:cc:dd:ee:ff:00:11",
		pemKey,
		nil,
	)

	dnsCl, err := ocidns.NewDnsClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)
	if dnsDispatcher != nil {
		dnsCl.HTTPClient = dnsDispatcher
	}

	return &oci2.OciServices{
		Ctx:    ctx,
		Logger: log.NewLogger(),
		AdminOCIService: oci2.NewAdminOCIServiceWithAllClients(
			vault.VaultsClient{},
			secrets.SecretsClient{},
			certificatesmanagement.CertificatesManagementClient{},
			certificates.CertificatesClient{},
			dnsCl,
		),
	}
}

func Test_GetOrCreateOCIDNSRecord_NilService(t *testing.T) {
	rec, err := _getOrCreateOCIDNSRecord(nil, "dns-1.example.com.", "10.0.0.1")
	assert.Nil(t, rec)
	require.Error(t, err)

	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_GetOrCreateOCIDNSRecord_RecordExists_ShortCircuitsCreate(t *testing.T) {
	const (
		zoneOCID   = "ocid1.dns-zone.oc1..testzone"
		recordName = "dns-1.deployment-foo.vsa.netapp.internal."
		ip         = "10.0.0.5"
	)

	origZone := env.OCIVsaDnsZoneOCID
	defer func() { env.OCIVsaDnsZoneOCID = origZone }()
	env.OCIVsaDnsZoneOCID = zoneOCID

	// All Get calls succeed with a populated RRSet → Create must not be
	// invoked. We assert that by failing the test if the dispatcher ever sees
	// a non-GET method.
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet {
				t.Fatalf("Create must not run when Get returns a record; got method %s", req.Method)
			}
			return mockOCIJSONResponse(http.StatusOK, fmt.Sprintf(`{
				"items": [
					{"domain": "%s", "rdata": "%s", "rtype": "A", "ttl": 300}
				]
			}`, recordName, ip)), nil
		},
	}

	svc := newTestOciServicesWithDNSForHyperscaler(t, dispatcher)
	rec, err := _getOrCreateOCIDNSRecord(svc, recordName, ip)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, recordName, rec.RecordName)
	assert.Equal(t, ip, rec.Data)
}

func Test_GetOrCreateOCIDNSRecord_RecordMissing_FallsThroughToCreate(t *testing.T) {
	const (
		zoneOCID   = "ocid1.dns-zone.oc1..testzone"
		recordName = "dns-1.deployment-foo.vsa.netapp.internal."
		ip         = "10.0.0.5"
	)

	origZone := env.OCIVsaDnsZoneOCID
	defer func() { env.OCIVsaDnsZoneOCID = origZone }()
	env.OCIVsaDnsZoneOCID = zoneOCID

	var sawGet, sawCreate bool
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				sawGet = true
				// 404 → _getOrCreateOCIDNSRecord routes to create.
				return nil, &mockOCIServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no such RRSet"}
			}
			// PUT/UpdateRRSet path.
			sawCreate = true
			return mockOCIJSONResponse(http.StatusOK, fmt.Sprintf(`{
				"items": [
					{"domain": "%s", "rdata": "%s", "rtype": "A", "ttl": 300}
				]
			}`, recordName, ip)), nil
		},
	}

	svc := newTestOciServicesWithDNSForHyperscaler(t, dispatcher)
	rec, err := _getOrCreateOCIDNSRecord(svc, recordName, ip)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, recordName, rec.RecordName)
	assert.Equal(t, ip, rec.Data)
	assert.True(t, sawGet, "Get must run first")
	assert.True(t, sawCreate, "Create must run when Get returns 404")
}

func Test_GetOrCreateOCIDNSRecord_GetErrorPropagates(t *testing.T) {
	origZone := env.OCIVsaDnsZoneOCID
	defer func() { env.OCIVsaDnsZoneOCID = origZone }()
	env.OCIVsaDnsZoneOCID = "ocid1.dns-zone.oc1..testzone"

	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockOCIServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}

	svc := newTestOciServicesWithDNSForHyperscaler(t, dispatcher)
	rec, err := _getOrCreateOCIDNSRecord(svc, "dns-1.example.com.", "10.0.0.5")
	assert.Nil(t, rec)
	require.Error(t, err)

	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
		assert.Equal(t, coreerrors.ErrOCIResourceFetchError, cerr.TrackingID)
	}
}

func Test_DeleteOCIDNSRecord_NilService(t *testing.T) {
	err := _deleteOCIDNSRecord(nil, "dns-1.example.com.")
	require.Error(t, err)

	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

// Test_DeleteOCIDNSRecord_AlreadyAbsent_204_NoOp covers the common
// "record was never present" case. In practice OCI's DeleteRRSet is idempotent
// at the RRSet level within an existing zone: deleting a domain that has no
// records returns 204 No Content (success), NOT 404. This is the path hit by
// SNI-override pools (which never registered any A record) and was confirmed
// against a live zone during skaffold testing. DeleteDnsRecord sees err == nil
// and simply returns nil — no retry, completes immediately.
func Test_DeleteOCIDNSRecord_AlreadyAbsent_204_NoOp(t *testing.T) {
	origZone := env.OCIVsaDnsZoneOCID
	defer func() { env.OCIVsaDnsZoneOCID = origZone }()
	env.OCIVsaDnsZoneOCID = "ocid1.dns-zone.oc1..testzone"

	// No pre-fetch happens: the helper calls DeleteRRSet directly, and OCI
	// answers 204 for an already-absent RRSet.
	var sawDelete bool
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				t.Fatalf("DeleteOCIDNSRecord must not GET before delete; got method %s", req.Method)
			}
			if req.Method != http.MethodDelete {
				t.Fatalf("unexpected method %s", req.Method)
			}
			sawDelete = true
			return mockOCIJSONResponse(http.StatusNoContent, ``), nil
		},
	}

	svc := newTestOciServicesWithDNSForHyperscaler(t, dispatcher)
	err := _deleteOCIDNSRecord(svc, "dns-1.example.com.")
	assert.NoError(t, err)
	assert.True(t, sawDelete, "DeleteRRSet must run even when the record is absent")
}

// Test_DeleteOCIDNSRecord_NotFound404_NoOp covers a DISTINCT not-found case
// from the 204 path above. A 404 here is not the everyday "record already
// absent" outcome (that is a 204); it represents a genuine service-level
// not-found — e.g. the DNS zone itself does not exist or the service otherwise
// returns 404. DeleteDnsRecord's isOCIDnsNotFound branch still treats it as an
// idempotent no-op (returns nil) so pool-delete stays robust.
//
// Note: we must return a real 404 *response* (not a (nil, error) tuple). A
// (nil, error) return mimics a dead connection, which the SDK classifies as a
// retryable transport failure and retries with backoff — hanging the test for
// minutes. A proper 404 response yields a non-retryable ServiceError.
func Test_DeleteOCIDNSRecord_NotFound404_NoOp(t *testing.T) {
	origZone := env.OCIVsaDnsZoneOCID
	defer func() { env.OCIVsaDnsZoneOCID = origZone }()
	env.OCIVsaDnsZoneOCID = "ocid1.dns-zone.oc1..testzone"

	var sawDelete bool
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				t.Fatalf("DeleteOCIDNSRecord must not GET before delete; got method %s", req.Method)
			}
			if req.Method != http.MethodDelete {
				t.Fatalf("unexpected method %s", req.Method)
			}
			sawDelete = true
			return mockOCIJSONResponse(http.StatusNotFound,
				`{"code":"NotFound","message":"no such RRSet"}`), nil
		},
	}

	svc := newTestOciServicesWithDNSForHyperscaler(t, dispatcher)
	err := _deleteOCIDNSRecord(svc, "dns-1.example.com.")
	assert.NoError(t, err)
	assert.True(t, sawDelete, "DeleteRRSet must run even when the service returns 404")
}

func Test_DeleteOCIDNSRecord_RecordExists_DeletesRRSet(t *testing.T) {
	const recordName = "dns-1.deployment-foo.vsa.netapp.internal."

	origZone := env.OCIVsaDnsZoneOCID
	defer func() { env.OCIVsaDnsZoneOCID = origZone }()
	env.OCIVsaDnsZoneOCID = "ocid1.dns-zone.oc1..testzone"

	// The pre-fetch was removed: the helper issues DeleteRRSet directly with
	// no preceding GET. OCI answers 204 when it removes (or no-ops) the RRSet.
	var sawDelete bool
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				t.Fatalf("DeleteOCIDNSRecord must not GET before delete; got method %s", req.Method)
			}
			if req.Method != http.MethodDelete {
				t.Fatalf("unexpected method %s", req.Method)
			}
			sawDelete = true
			return mockOCIJSONResponse(http.StatusNoContent, ``), nil
		},
	}

	svc := newTestOciServicesWithDNSForHyperscaler(t, dispatcher)
	err := _deleteOCIDNSRecord(svc, recordName)
	assert.NoError(t, err)
	assert.True(t, sawDelete, "Delete must run")
}

// ---------------------------------------------------------------------------
// _getPasswordFromCacheOrOCIVault
// ---------------------------------------------------------------------------
//
// The cache-or-vault helper short-circuits in three observable ways: (1) bad
// input, (2) cache hit, and (3) vault fetch (which we cover only for the
// "OCI client could not be initialised" failure path because exercising the
// full vault round-trip would require a real OCI HTTP fixture).

func Test_GetPasswordFromCacheOrOCIVault_NilRef(t *testing.T) {
	pw, err := _getPasswordFromCacheOrOCIVault(context.Background(), nil)
	assert.Empty(t, pw)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OCI vault reference is empty")
}

func Test_GetPasswordFromCacheOrOCIVault_EmptyName(t *testing.T) {
	pw, err := _getPasswordFromCacheOrOCIVault(context.Background(), &datamodel.ExternalCredRef{Name: ""})
	assert.Empty(t, pw)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OCI vault reference is empty")
}

func Test_GetPasswordFromCacheOrOCIVault_CacheHit(t *testing.T) {
	// Use a unique name to avoid colliding with cache entries left behind by
	// other tests in this package (the cache is process-scoped).
	const cacheKey = "test-getpasswordfromcache-hit-secret"

	origGet := common.GetFromUserAuthCache
	defer func() { common.GetFromUserAuthCache = origGet }()
	common.GetFromUserAuthCache = func(key string) (*models.UserCache, bool) {
		assert.Equal(t, cacheKey, key)
		return &models.UserCache{Password: "cached-pw"}, true
	}

	// Force a panic if the function falls through to hyperscaler.GetOCIService — that
	// would mean the cache hit was ignored.
	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		t.Fatalf("OCI service must not be initialised when cache contains a value")
		return nil, nil
	}

	pw, err := _getPasswordFromCacheOrOCIVault(context.Background(), &datamodel.ExternalCredRef{Name: cacheKey})
	assert.NoError(t, err)
	assert.Equal(t, "cached-pw", pw)
}

func Test_GetPasswordFromCacheOrOCIVault_GetOCIServiceFails(t *testing.T) {
	const cacheKey = "test-getpasswordfromcache-svcfail-secret"

	origGet := common.GetFromUserAuthCache
	defer func() { common.GetFromUserAuthCache = origGet }()
	// Cache miss so the function falls through to hyperscaler.GetOCIService.
	common.GetFromUserAuthCache = func(key string) (*models.UserCache, bool) {
		return nil, false
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		return nil, fmt.Errorf("oci client init failed")
	}

	pw, err := _getPasswordFromCacheOrOCIVault(context.Background(), &datamodel.ExternalCredRef{Name: cacheKey})
	assert.Empty(t, pw)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "oci client init failed")
}

// ---------------------------------------------------------------------------
// OCI test infrastructure
//
// mockOCIHTTPDispatcher and mockOCIServiceError mirror the private helpers in
// hyperscaler/oci/provider_test.go so that tests in this package can build
// fully-initialised OciServices backed by HTTP-level mocks without needing a
// real OCI environment or credentials.
// ---------------------------------------------------------------------------

type mockOCIHTTPDispatcher struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockOCIHTTPDispatcher) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

// mockOCIServiceError implements oci-go-sdk/v65/common.ServiceError so that
// common.IsServiceError returns true and GetHTTPStatusCode() is honoured.
type mockOCIServiceError struct {
	statusCode int
	code       string
	message    string
}

func (e *mockOCIServiceError) GetHTTPStatusCode() int  { return e.statusCode }
func (e *mockOCIServiceError) GetMessage() string      { return e.message }
func (e *mockOCIServiceError) GetCode() string         { return e.code }
func (e *mockOCIServiceError) GetOpcRequestID() string { return "" }
func (e *mockOCIServiceError) Error() string {
	return fmt.Sprintf("Service error: %s (status %d)", e.message, e.statusCode)
}

// mockOCIJSONResponse builds a minimal *http.Response carrying a JSON body.
func mockOCIJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Opc-Request-Id": []string{"test-req-id"},
		},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: &http.Request{URL: &url.URL{Path: "/mock"}},
	}
}

// testOCIRSAPrivateKeyPEM generates a throwaway RSA private key in PEM format
// for use with ocicommon.NewRawConfigurationProvider in tests.
func testOCIRSAPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

// newTestOciServicesForHyperscaler creates an OciServices value backed by mock
// HTTP dispatchers. Pass nil for a dispatcher you don't need in a given test.
//
//   - vaultDispatcher   → intercepted by vault.VaultsClient (CreateSecret, GetSecret,
//     ScheduleSecretDeletion)
//   - secretsDispatcher → intercepted by secrets.SecretsClient (GetSecretBundleByName,
//     GetSecretBundle)
func newTestOciServicesForHyperscaler(t *testing.T, vaultDispatcher, secretsDispatcher *mockOCIHTTPDispatcher) *oci2.OciServices {
	t.Helper()
	ctx := context.Background()
	pemKey := testOCIRSAPrivateKeyPEM(t)

	configProvider := ocicommon.NewRawConfigurationProvider(
		"ocid1.tenancy.oc1..test",
		"ocid1.user.oc1..test",
		"us-ashburn-1",
		"aa:bb:cc:dd:ee:ff:00:11",
		pemKey,
		nil,
	)

	vaultCl, err := vault.NewVaultsClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)
	if vaultDispatcher != nil {
		vaultCl.HTTPClient = vaultDispatcher
	}

	secretsCl, err := secrets.NewSecretsClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)
	if secretsDispatcher != nil {
		secretsCl.HTTPClient = secretsDispatcher
	}

	return &oci2.OciServices{
		Ctx:             ctx,
		Logger:          log.NewLogger(),
		AdminOCIService: oci2.NewAdminOCIService(vaultCl, secretsCl),
	}
}

// ---------------------------------------------------------------------------
// _generatePasswordForVSAClusterOCI
// ---------------------------------------------------------------------------

func Test_GeneratePasswordForVSAClusterOCI(t *testing.T) {
	origVault := env.OCIVaultOCID
	origCompartment := env.OCICompartmentOCID
	origMasterKey := env.OCIMasterKeyOCID
	defer func() {
		env.OCIVaultOCID = origVault
		env.OCICompartmentOCID = origCompartment
		env.OCIMasterKeyOCID = origMasterKey
	}()

	const secretName = "test-generate-ontap-admin-secret"

	t.Run("config invalid — returns ErrOCIClientInitializationError before any OCI call", func(t *testing.T) {
		env.OCIVaultOCID = ""
		env.OCICompartmentOCID = ""
		env.OCIMasterKeyOCID = ""

		svc := newTestOciServicesForHyperscaler(t, nil, nil)
		result, err := _generatePasswordForVSAClusterOCI(svc, secretName)
		assert.Nil(t, result)
		require.Error(t, err)

		cerr, ok := err.(*coreerrors.CustomError)
		if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
			assert.Equal(t, coreerrors.ErrOCIClientInitializationError, cerr.TrackingID)
		}
	})

	t.Run("GetSecretByName fails — error propagated", func(t *testing.T) {
		env.OCIVaultOCID = "ocid1.vault.oc1..test"
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIMasterKeyOCID = "ocid1.key.oc1..test"

		secretsDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockOCIServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "access denied"}
			},
		}
		svc := newTestOciServicesForHyperscaler(t, nil, secretsDispatcher)
		result, err := _generatePasswordForVSAClusterOCI(svc, secretName)
		assert.Nil(t, result)
		assert.Error(t, err)
	})

	t.Run("secret already exists — existing secret returned without CreateSecret", func(t *testing.T) {
		env.OCIVaultOCID = "ocid1.vault.oc1..test"
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIMasterKeyOCID = "ocid1.key.oc1..test"

		encodedPW := base64.StdEncoding.EncodeToString([]byte("existing-password"))
		secretsDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockOCIJSONResponse(http.StatusOK, `{
					"secretId":      "ocid1.vaultsecret.oc1..existing",
					"versionNumber": 1,
					"secretBundleContent": {
						"contentType": "BASE64",
						"content":     "`+encodedPW+`"
					}
				}`), nil
			},
		}
		svc := newTestOciServicesForHyperscaler(t, nil, secretsDispatcher)
		result, err := _generatePasswordForVSAClusterOCI(svc, secretName)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "existing-password", result.Value)
		assert.Equal(t, "ocid1.vaultsecret.oc1..existing", result.Ocid)
	})

	t.Run("secret not found — GenerateStrongPassword fails — error propagated", func(t *testing.T) {
		env.OCIVaultOCID = "ocid1.vault.oc1..test"
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIMasterKeyOCID = "ocid1.key.oc1..test"

		secretsDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockOCIServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "not found"}
			},
		}

		origGeneratePW := utils.GenerateStrongPassword
		defer func() { utils.GenerateStrongPassword = origGeneratePW }()
		utils.GenerateStrongPassword = func(length int) (string, error) {
			return "", fmt.Errorf("entropy source failed")
		}

		svc := newTestOciServicesForHyperscaler(t, nil, secretsDispatcher)
		result, err := _generatePasswordForVSAClusterOCI(svc, secretName)
		assert.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "entropy source failed")
	})

	t.Run("secret not found — CreateSecret fails — error propagated", func(t *testing.T) {
		env.OCIVaultOCID = "ocid1.vault.oc1..test"
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIMasterKeyOCID = "ocid1.key.oc1..test"

		origGeneratePW := utils.GenerateStrongPassword
		defer func() { utils.GenerateStrongPassword = origGeneratePW }()
		utils.GenerateStrongPassword = func(length int) (string, error) {
			return "StrongPassword1!", nil
		}

		// secrets client: 404 → GetSecretByName returns nil, nil
		secretsDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockOCIServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "not found"}
			},
		}
		// vault client: error on CreateSecret
		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockOCIServiceError{statusCode: http.StatusBadRequest, code: "InvalidParameter", message: "vault invalid"}
			},
		}

		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, secretsDispatcher)
		result, err := _generatePasswordForVSAClusterOCI(svc, secretName)
		assert.Nil(t, result)
		assert.Error(t, err)
	})

	t.Run("success — new secret created and password added to cache", func(t *testing.T) {
		env.OCIVaultOCID = "ocid1.vault.oc1..test"
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIMasterKeyOCID = "ocid1.key.oc1..test"

		const createdOCID = "ocid1.vaultsecret.oc1..created"
		const createdName = "test-generate-success-secret"

		origGeneratePW := utils.GenerateStrongPassword
		defer func() { utils.GenerateStrongPassword = origGeneratePW }()
		utils.GenerateStrongPassword = func(length int) (string, error) {
			return "StrongPassword1!", nil
		}
		defer common.RemoveFromUserAuthCache(createdName)

		secretsDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockOCIServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "not found"}
			},
		}
		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":             "`+createdOCID+`",
					"secretName":     "`+createdName+`",
					"lifecycleState": "ACTIVE",
					"currentVersionNumber": 1
				}`), nil
			},
		}

		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, secretsDispatcher)
		result, err := _generatePasswordForVSAClusterOCI(svc, createdName)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, createdOCID, result.Ocid)
		assert.Equal(t, "StrongPassword1!", result.Value)
		assert.Equal(t, createdName, result.Name)

		// Cache must be populated after successful creation.
		cached, exists := common.GetFromUserAuthCache(createdName)
		assert.True(t, exists)
		if assert.NotNil(t, cached) {
			assert.Equal(t, "StrongPassword1!", cached.Password)
		}
	})
}

// ---------------------------------------------------------------------------
// _deletePasswordForVSAClusterOCI
// ---------------------------------------------------------------------------

func Test_DeletePasswordForVSAClusterOCI(t *testing.T) {
	const secretOCID = "ocid1.vaultsecret.oc1..todelete"
	const scheduleDeletionPath = "scheduleDeletion"

	newPoolCreds := func(name, ocid string) *datamodel.PoolCredentials {
		return &datamodel.PoolCredentials{
			ExternalSecret: &datamodel.ExternalCredRef{
				Name:               name,
				ExternalIdentifier: ocid,
			},
		}
	}

	t.Run("nil PoolCredentials returns ErrInputValidationError before any OCI call", func(t *testing.T) {
		svc := newTestOciServicesForHyperscaler(t, nil, nil)
		err := _deletePasswordForVSAClusterOCI(svc, nil)
		require.Error(t, err)

		cerr, ok := err.(*coreerrors.CustomError)
		if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
			assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})

	t.Run("nil ExternalSecret skips delete and returns nil", func(t *testing.T) {
		// Vault dispatcher fails the test if invoked — proves no OCI call is made.
		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				t.Fatalf("OCI vault must not be called when ExternalSecret is nil; got %s %s", req.Method, req.URL.Path)
				return nil, nil
			},
		}
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)

		err := _deletePasswordForVSAClusterOCI(svc, &datamodel.PoolCredentials{ExternalSecret: nil})
		assert.NoError(t, err)
	})

	t.Run("empty ExternalIdentifier skips delete and returns nil", func(t *testing.T) {
		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				t.Fatalf("OCI vault must not be called when ExternalIdentifier is empty; got %s %s", req.Method, req.URL.Path)
				return nil, nil
			},
		}
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)

		err := _deletePasswordForVSAClusterOCI(svc, newPoolCreds("some-name", ""))
		assert.NoError(t, err)
	})

	t.Run("DeleteSecret fails on ScheduleSecretDeletion — error propagated", func(t *testing.T) {
		const name = "test-delete-delfail-secret"

		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPath) {
					return nil, &mockOCIServiceError{statusCode: http.StatusConflict, code: "Conflict", message: "deletion conflict"}
				}
				// GetSecret pre-flight inside DeleteSecret — return ACTIVE so we proceed to schedule.
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":             "`+secretOCID+`",
					"secretName":     "`+name+`",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)
		err := _deletePasswordForVSAClusterOCI(svc, newPoolCreds(name, secretOCID))
		assert.Error(t, err)
	})

	t.Run("secret already 404 — DeleteSecret absorbs, cache evicted", func(t *testing.T) {
		const name = "test-delete-notfound-secret"
		common.AddToUserAuthCache(name, "some-pw")

		// GetSecret returns 404 — DeleteSecret treats this as "already gone" and returns nil.
		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &mockOCIServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "not found"}
			},
		}
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)
		err := _deletePasswordForVSAClusterOCI(svc, newPoolCreds(name, secretOCID))
		assert.NoError(t, err)

		_, exists := common.GetFromUserAuthCache(name)
		assert.False(t, exists, "cache entry should be removed even when secret is not in OCI Vault")
	})

	t.Run("success — secret scheduled for deletion, cache cleared", func(t *testing.T) {
		const name = "test-delete-success-secret"
		common.AddToUserAuthCache(name, "cached-pw")

		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPath) {
					return mockOCIJSONResponse(http.StatusOK, `{}`), nil
				}
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":             "`+secretOCID+`",
					"secretName":     "`+name+`",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)
		err := _deletePasswordForVSAClusterOCI(svc, newPoolCreds(name, secretOCID))
		assert.NoError(t, err)

		_, exists := common.GetFromUserAuthCache(name)
		assert.False(t, exists, "cache entry should be removed on successful deletion")
	})

	t.Run("success — cache miss is not an error", func(t *testing.T) {
		const name = "test-delete-cachemiss-secret"
		// No cache entry pre-populated — function should still return nil.

		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPath) {
					return mockOCIJSONResponse(http.StatusOK, `{}`), nil
				}
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":             "`+secretOCID+`",
					"secretName":     "`+name+`",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)
		err := _deletePasswordForVSAClusterOCI(svc, newPoolCreds(name, secretOCID))
		assert.NoError(t, err)
	})

	t.Run("success — empty Name only skips cache eviction, OCI delete still runs", func(t *testing.T) {
		// ExternalIdentifier is sufficient to drive the OCI delete; Name is
		// only used for cache eviction. An empty Name must not block the
		// delete path.
		var scheduledDeletion bool
		vaultDispatcher := &mockOCIHTTPDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, scheduleDeletionPath) {
					scheduledDeletion = true
					return mockOCIJSONResponse(http.StatusOK, `{}`), nil
				}
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":             "`+secretOCID+`",
					"secretName":     "any",
					"lifecycleState": "ACTIVE"
				}`), nil
			},
		}
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)
		err := _deletePasswordForVSAClusterOCI(svc, newPoolCreds("", secretOCID))
		assert.NoError(t, err)
		assert.True(t, scheduledDeletion, "ScheduleSecretDeletion must still be invoked when Name is empty")
	})
}

// ---------------------------------------------------------------------------
// _getPasswordFromCacheOrOCIVault — additional paths
//
// The tests below cover paths introduced in this branch that are not
// exercised by the earlier Test_GetPasswordFromCacheOrOCIVault_* tests:
//   - _validateOCIVaultConfig fails after hyperscaler.GetOCIService succeeds
//   - Vault fetch succeeds (full happy path through GetSecretByName)
//   - Vault fetch returns nil (secret not found)
// ---------------------------------------------------------------------------

func Test_GetPasswordFromCacheOrOCIVault_ValidateConfigFails(t *testing.T) {
	const cacheKey = "test-cache-validate-fail-secret"

	origVault := env.OCIVaultOCID
	defer func() { env.OCIVaultOCID = origVault }()
	env.OCIVaultOCID = "" // triggers _validateOCIVaultConfig(false) failure

	origGet := common.GetFromUserAuthCache
	defer func() { common.GetFromUserAuthCache = origGet }()
	common.GetFromUserAuthCache = func(key string) (*models.UserCache, bool) {
		return nil, false // cache miss so we proceed past cache check
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		return &oci2.OciServices{Ctx: ctx, Logger: log.NewLogger()}, nil
	}

	pw, err := _getPasswordFromCacheOrOCIVault(context.Background(), &datamodel.ExternalCredRef{Name: cacheKey})
	assert.Empty(t, pw)
	require.Error(t, err)

	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok, "expected *coreerrors.CustomError, got %T", err) {
		assert.Equal(t, coreerrors.ErrOCIClientInitializationError, cerr.TrackingID)
	}
}

func Test_GetPasswordFromCacheOrOCIVault_VaultFetchSuccess(t *testing.T) {
	const cacheKey = "test-cache-vaultfetch-success-secret"
	const secretOCID = "ocid1.vaultsecret.oc1..found"

	origVault := env.OCIVaultOCID
	defer func() {
		env.OCIVaultOCID = origVault
		common.RemoveFromUserAuthCache(cacheKey)
	}()
	env.OCIVaultOCID = "ocid1.vault.oc1..test"

	origGet := common.GetFromUserAuthCache
	defer func() { common.GetFromUserAuthCache = origGet }()
	common.GetFromUserAuthCache = func(key string) (*models.UserCache, bool) {
		return nil, false // cache miss — fall through to OCI Vault
	}

	encodedPW := base64.StdEncoding.EncodeToString([]byte("vault-fetched-pw"))
	secretsDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, `{
				"secretId":      "ocid1.vaultsecret.oc1..found",
				"versionNumber": 1,
				"secretBundleContent": {
					"contentType": "BASE64",
					"content":     "`+encodedPW+`"
				}
			}`), nil
		},
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		svc := newTestOciServicesForHyperscaler(t, nil, secretsDispatcher)
		return svc, nil
	}

	origGetPW := GetPasswordForVSAClusterOCI
	defer func() { GetPasswordForVSAClusterOCI = origGetPW }()
	GetPasswordForVSAClusterOCI = func(ctx context.Context, secretID string) (*oci2.OCICustomSecret, error) {
		return &oci2.OCICustomSecret{
			Ocid:    secretID,
			Name:    cacheKey,
			Value:   "vault-fetched-pw",
			Version: 1,
		}, nil
	}

	pw, err := _getPasswordFromCacheOrOCIVault(context.Background(), &datamodel.ExternalCredRef{Name: cacheKey, ExternalIdentifier: secretOCID})
	assert.NoError(t, err)
	assert.Equal(t, "vault-fetched-pw", pw)
}

func Test_GetPasswordFromCacheOrOCIVault_SecretNotFound(t *testing.T) {
	const cacheKey = "test-cache-secretnotfound-secret"
	const secretOCID = "ocid1.vaultsecret.oc1..missing"

	origVault := env.OCIVaultOCID
	defer func() { env.OCIVaultOCID = origVault }()
	env.OCIVaultOCID = "ocid1.vault.oc1..test"

	origGet := common.GetFromUserAuthCache
	defer func() { common.GetFromUserAuthCache = origGet }()
	common.GetFromUserAuthCache = func(key string) (*models.UserCache, bool) {
		return nil, false
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		return &oci2.OciServices{Ctx: ctx, Logger: log.NewLogger()}, nil
	}

	// Secret not found → underlying fetch returns (nil, nil) so the caller
	// produces the "empty or not found" error.
	origGetPW := GetPasswordForVSAClusterOCI
	defer func() { GetPasswordForVSAClusterOCI = origGetPW }()
	GetPasswordForVSAClusterOCI = func(ctx context.Context, secretID string) (*oci2.OCICustomSecret, error) {
		return nil, nil
	}

	pw, err := _getPasswordFromCacheOrOCIVault(context.Background(), &datamodel.ExternalCredRef{Name: cacheKey, ExternalIdentifier: secretOCID})
	assert.Empty(t, pw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty or not found")
}

// ---------------------------------------------------------------------------
// _getPasswordForVSAClusterOCI
//
// _getPasswordForVSAClusterOCI is the OCI counterpart of GetPasswordForVSACluster:
// it resolves the OCI service, fetches the latest version of the secret bundle
// for the supplied OCID, and treats nil/empty-value results as failures so the
// caller never sees a partially-populated *OCICustomSecret.
// ---------------------------------------------------------------------------

func Test_GetPasswordForVSAClusterOCI_GetOCIServiceFails(t *testing.T) {
	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		return nil, fmt.Errorf("oci client init failed")
	}

	secret, err := _getPasswordForVSAClusterOCI(context.Background(), "ocid1.vaultsecret.oc1..anything")
	assert.Nil(t, secret)
	require.Error(t, err)
	// The OCI service init error is returned unwrapped.
	assert.Contains(t, err.Error(), "oci client init failed")
	assert.NotContains(t, err.Error(), "failed to get secret for External Identifier")
}

func Test_GetPasswordForVSAClusterOCI_VaultGetSecretFails(t *testing.T) {
	const secretOCID = "ocid1.vaultsecret.oc1..unreachable"

	// vault.GetSecret (metadata fetch) returns a transport-level error. This
	// is wrapped by GetSecretWithLatestVersion as ErrOCIResourceFetchError and
	// then re-wrapped here as "failed to get secret for External Identifier".
	vaultDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("vault connection timeout")
		},
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)
		return svc, nil
	}

	secret, err := _getPasswordForVSAClusterOCI(context.Background(), secretOCID)
	assert.Nil(t, secret)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get secret for External Identifier")
	assert.Contains(t, err.Error(), secretOCID)
}

func Test_GetPasswordForVSAClusterOCI_SecretInDeletionState(t *testing.T) {
	const secretOCID = "ocid1.vaultsecret.oc1..pending"

	// GetSecretWithLatestVersion treats PENDING_DELETION as (nil, nil); the
	// caller must convert that into an error so we never hand a non-existent
	// password back to ONTAP.
	vaultDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, `{
				"id":             "`+secretOCID+`",
				"secretName":     "pending-secret",
				"lifecycleState": "PENDING_DELETION"
			}`), nil
		},
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, nil)
		return svc, nil
	}

	secret, err := _getPasswordForVSAClusterOCI(context.Background(), secretOCID)
	assert.Nil(t, secret)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get secret for External Identifier")
	assert.Contains(t, err.Error(), secretOCID)
}

func Test_GetPasswordForVSAClusterOCI_EmptySecretValue(t *testing.T) {
	const secretOCID = "ocid1.vaultsecret.oc1..emptyvalue"

	// vault metadata fetch reports ACTIVE so we proceed to the bundle fetch.
	vaultDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, `{
				"id":             "`+secretOCID+`",
				"secretName":     "empty-secret",
				"lifecycleState": "ACTIVE"
			}`), nil
		},
	}
	// Bundle returns ACTIVE+base64("") so secret.Value ends up empty after
	// decoding. The empty-value guard must turn this into the same "failed
	// to get secret" error as a missing secret.
	encodedEmpty := base64.StdEncoding.EncodeToString([]byte(""))
	secretsDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, `{
				"secretId":      "`+secretOCID+`",
				"versionNumber": 1,
				"secretBundleContent": {
					"contentType": "BASE64",
					"content":     "`+encodedEmpty+`"
				}
			}`), nil
		},
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, secretsDispatcher)
		return svc, nil
	}

	secret, err := _getPasswordForVSAClusterOCI(context.Background(), secretOCID)
	assert.Nil(t, secret)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get secret for External Identifier")
}

func Test_GetPasswordForVSAClusterOCI_Success(t *testing.T) {
	const secretOCID = "ocid1.vaultsecret.oc1..happy"
	const expectedPW = "happy-path-pw"

	vaultDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, `{
				"id":             "`+secretOCID+`",
				"secretName":     "happy-secret",
				"lifecycleState": "ACTIVE"
			}`), nil
		},
	}
	encodedPW := base64.StdEncoding.EncodeToString([]byte(expectedPW))
	secretsDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, `{
				"secretId":      "`+secretOCID+`",
				"versionNumber": 7,
				"secretBundleContent": {
					"contentType": "BASE64",
					"content":     "`+encodedPW+`"
				}
			}`), nil
		},
	}

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		svc := newTestOciServicesForHyperscaler(t, vaultDispatcher, secretsDispatcher)
		return svc, nil
	}

	secret, err := _getPasswordForVSAClusterOCI(context.Background(), secretOCID)
	require.NoError(t, err)
	require.NotNil(t, secret)
	assert.Equal(t, expectedPW, secret.Value)
	assert.Equal(t, int64(7), secret.Version)
	assert.Equal(t, secretOCID, secret.Ocid)
}

// ---------------------------------------------------------------------------
// _validateOCICertConfig
// ---------------------------------------------------------------------------

func Test_ValidateOCICertConfig(t *testing.T) {
	origCompartment := env.OCICompartmentOCID
	origIssuer := env.OCIIssuerCAOCID
	origDns := env.VsaDeployedDnsName
	defer func() {
		env.OCICompartmentOCID = origCompartment
		env.OCIIssuerCAOCID = origIssuer
		env.VsaDeployedDnsName = origDns
	}()

	t.Run("all set — no error", func(t *testing.T) {
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIIssuerCAOCID = "ocid1.certauthority.oc1..test"
		env.VsaDeployedDnsName = "vsa.example.internal"
		assert.NoError(t, _validateOCICertConfig())
	})

	assertMissingVar := func(t *testing.T, err error, wantVars ...string) {
		t.Helper()
		require.Error(t, err)
		cerr, ok := err.(*coreerrors.CustomError)
		require.True(t, ok, "expected *CustomError, got %T", err)
		assert.Equal(t, coreerrors.ErrOCIClientInitializationError, cerr.TrackingID)
		require.NotNil(t, cerr.OriginalErr)
		for _, v := range wantVars {
			assert.Contains(t, cerr.OriginalErr.Error(), v)
		}
	}

	t.Run("missing compartment", func(t *testing.T) {
		env.OCICompartmentOCID = ""
		env.OCIIssuerCAOCID = "ocid1.certauthority.oc1..test"
		env.VsaDeployedDnsName = "vsa.example.internal"
		assertMissingVar(t, _validateOCICertConfig(), "OCI_COMPARTMENT_OCID")
	})

	t.Run("missing issuer", func(t *testing.T) {
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIIssuerCAOCID = ""
		env.VsaDeployedDnsName = "vsa.example.internal"
		assertMissingVar(t, _validateOCICertConfig(), "OCI_ISSUER_CA_OCID")
	})

	t.Run("missing dns", func(t *testing.T) {
		env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
		env.OCIIssuerCAOCID = "ocid1.certauthority.oc1..test"
		env.VsaDeployedDnsName = ""
		assertMissingVar(t, _validateOCICertConfig(), "VSA_DEPLOYED_DNS_NAME")
	})

	t.Run("all missing — error lists every missing var", func(t *testing.T) {
		env.OCICompartmentOCID = ""
		env.OCIIssuerCAOCID = ""
		env.VsaDeployedDnsName = ""
		assertMissingVar(t, _validateOCICertConfig(), "OCI_COMPARTMENT_OCID", "OCI_ISSUER_CA_OCID", "VSA_DEPLOYED_DNS_NAME")
	})
}

// ---------------------------------------------------------------------------
// ociPemChainToSlice
// ---------------------------------------------------------------------------

func Test_OCIPemChainToSlice(t *testing.T) {
	// Valid base64 bodies so pem.Decode actually parses each block; the
	// previous fixture used "ABC" which is invalid base64 and survived only
	// via the malformed-input fallback. These exercise the real split path.
	const (
		blockA = "-----BEGIN CERTIFICATE-----\nQUFB\n-----END CERTIFICATE-----\n"
		blockB = "-----BEGIN CERTIFICATE-----\nQkJC\n-----END CERTIFICATE-----\n"
		blockC = "-----BEGIN CERTIFICATE-----\nQ0ND\n-----END CERTIFICATE-----\n"
		keyBlk = "-----BEGIN PRIVATE KEY-----\nUEtZ\n-----END PRIVATE KEY-----\n"
	)
	// Re-encode through encoding/pem so test expectations match whatever
	// canonical formatting EncodeToMemory produces, instead of pinning a
	// specific byte sequence that would drift if the stdlib changed.
	canonical := func(t *testing.T, raw string) string {
		t.Helper()
		block, _ := pem.Decode([]byte(raw))
		require.NotNil(t, block, "fixture must decode as a PEM block")
		return string(pem.EncodeToMemory(block))
	}

	t.Run("empty returns nil", func(t *testing.T) {
		assert.Nil(t, ociPemChainToSlice(""))
	})

	t.Run("whitespace-only returns nil", func(t *testing.T) {
		assert.Nil(t, ociPemChainToSlice("  \n\t\n  "))
	})

	t.Run("single valid block — one canonical element", func(t *testing.T) {
		out := ociPemChainToSlice(blockA)
		require.Len(t, out, 1)
		assert.Equal(t, canonical(t, blockA), out[0])
	})

	t.Run("two concatenated blocks — split into two elements in order", func(t *testing.T) {
		out := ociPemChainToSlice(blockA + blockB)
		require.Len(t, out, 2)
		assert.Equal(t, canonical(t, blockA), out[0])
		assert.Equal(t, canonical(t, blockB), out[1])
	})

	t.Run("three concatenated blocks — split into three elements in order", func(t *testing.T) {
		out := ociPemChainToSlice(blockA + blockB + blockC)
		require.Len(t, out, 3)
		assert.Equal(t, canonical(t, blockA), out[0])
		assert.Equal(t, canonical(t, blockB), out[1])
		assert.Equal(t, canonical(t, blockC), out[2])
	})

	t.Run("non-CERTIFICATE block is filtered out", func(t *testing.T) {
		// A stray PRIVATE KEY between two CERTIFICATEs must not pollute the
		// output — InterMediateCertificates is strictly a cert chain.
		out := ociPemChainToSlice(blockA + keyBlk + blockB)
		require.Len(t, out, 2)
		assert.Equal(t, canonical(t, blockA), out[0])
		assert.Equal(t, canonical(t, blockB), out[1])
	})

	t.Run("trailing garbage after last block is ignored", func(t *testing.T) {
		out := ociPemChainToSlice(blockA + "\nnot pem content\n")
		require.Len(t, out, 1)
		assert.Equal(t, canonical(t, blockA), out[0])
	})

	t.Run("malformed input falls back to legacy single-element shape", func(t *testing.T) {
		// Pins the "no decodable CERTIFICATE block" fallback so the
		// downstream parser keeps producing its existing
		// "Failed to parse certificate" error rather than a less precise one.
		out := ociPemChainToSlice("not a pem chain")
		require.Len(t, out, 1)
		assert.Equal(t, "not a pem chain", out[0])
	})
}

// ---------------------------------------------------------------------------
// buildCustomCertificateResponseFromOCI
// ---------------------------------------------------------------------------

func Test_BuildCustomCertificateResponseFromOCI(t *testing.T) {
	cert := &oci2.OCICustomCertificate{
		Ocid:                "ocid1.certificate.oc1..abc",
		Name:                "my-cert",
		SubjectCommonName:   "admin",
		SubjectOrganization: "Netapp",
		PemCertificate:      "leaf-pem",
		PemCertificateChain: "chain-pem",
		PrivateKeyPem:       "private-key-pem",
		SerialNumber:        "01:02",
		IssuerCAOCID:        "ocid1.certauthority.oc1..ca",
		CompartmentID:       "ocid1.compartment.oc1..c",
		VersionNumber:       4,
	}
	resp := buildCustomCertificateResponseFromOCI(cert)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Certificate)
	require.NotNil(t, resp.Secret)
	require.NotNil(t, resp.Secret.SecretVersion)

	assert.Equal(t, "my-cert", resp.Certificate.Name)
	assert.Equal(t, "ocid1.certificate.oc1..abc", resp.Certificate.CertificateID)
	assert.Equal(t, "admin", resp.Certificate.SubjectCommonName)
	assert.Equal(t, "Netapp", resp.Certificate.SubjectOrganization)
	assert.Equal(t, "leaf-pem", resp.Certificate.PemCertificate)
	assert.Equal(t, []string{"chain-pem"}, resp.Certificate.PemCertificateChain)
	assert.Equal(t, "01:02", resp.Certificate.SerialNumber)
	assert.Equal(t, "ocid1.certauthority.oc1..ca", resp.Certificate.IssuerCertificateAuthority)
	assert.Equal(t, "ocid1.compartment.oc1..c", resp.Certificate.CertOwningEntity)
	assert.Equal(t, int64(4), resp.Certificate.VersionNumber)

	assert.Equal(t, "my-cert", resp.Secret.Name)
	assert.Equal(t, "ocid1.compartment.oc1..c", resp.Secret.SecretOwningEntity)
	assert.Equal(t, "private-key-pem", resp.Secret.SecretVersion.Value)
	assert.Equal(t, "ocid1.compartment.oc1..c", resp.Secret.SecretVersion.SecretOwningEntity)
}

func Test_BuildCustomCertificateResponseFromOCI_EmptyChainStaysNil(t *testing.T) {
	cert := &oci2.OCICustomCertificate{
		Ocid:                "ocid1.certificate.oc1..abc",
		Name:                "cert-no-chain",
		PemCertificate:      "leaf",
		PrivateKeyPem:       "key",
		PemCertificateChain: "",
	}
	resp := buildCustomCertificateResponseFromOCI(cert)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Certificate.PemCertificateChain)
}

// ---------------------------------------------------------------------------
// _revokeCertificateFromCAS
// ---------------------------------------------------------------------------

func Test_RevokeCertificateFromCAS_NilPoolCreds(t *testing.T) {
	svc := newTestOciServicesForHyperscaler(t, nil, nil)
	err := _revokeCertificateFromCAS(svc, nil)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_RevokeCertificateFromCAS_NilExternalCertificate_NoOp(t *testing.T) {
	svc := newTestOciServicesForHyperscaler(t, nil, nil)
	err := _revokeCertificateFromCAS(svc, &datamodel.PoolCredentials{})
	assert.NoError(t, err)
}

func Test_RevokeCertificateFromCAS_EmptyExternalIdentifier_NoOp(t *testing.T) {
	svc := newTestOciServicesForHyperscaler(t, nil, nil)
	pc := &datamodel.PoolCredentials{
		ExternalCertificate: &datamodel.ExternalCredRef{Name: "x", ExternalIdentifier: ""},
	}
	err := _revokeCertificateFromCAS(svc, pc)
	assert.NoError(t, err)
}

func Test_RevokeCertificateFromCAS_Success_EvictsCache(t *testing.T) {
	const certName = "test-revokecert-success"
	const certOCID = "ocid1.certificate.oc1..revoke-ok"

	common.AddToCertAuthCache(certName, &models.Certificate{CommonName: "x"})
	defer common.RemoveFromCertAuthCache(certName)

	// pre-flight Get returns ACTIVE, then schedule succeeds.
	mgmtDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "scheduleDeletion") {
				return mockOCIJSONResponse(http.StatusOK, `{}`), nil
			}
			return mockOCIJSONResponse(http.StatusOK, `{
				"id": "`+certOCID+`", "name": "`+certName+`", "compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, mgmtDispatcher)

	pc := &datamodel.PoolCredentials{
		ExternalCertificate: &datamodel.ExternalCredRef{Name: certName, ExternalIdentifier: certOCID},
	}
	err := _revokeCertificateFromCAS(svc, pc)
	assert.NoError(t, err)
	_, exists := common.GetCertAuthCache(certName)
	assert.False(t, exists, "certificate entry should be evicted from cache")
}

func Test_RevokeCertificateFromCAS_DeleteFailsPropagates(t *testing.T) {
	const certName = "test-revokecert-fail"
	const certOCID = "ocid1.certificate.oc1..revoke-bad"

	mgmtDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockOCIServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, mgmtDispatcher)
	pc := &datamodel.PoolCredentials{
		ExternalCertificate: &datamodel.ExternalCredRef{Name: certName, ExternalIdentifier: certOCID},
	}
	err := _revokeCertificateFromCAS(svc, pc)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrOCIResourceFetchError, cerr.TrackingID)
	}
}

// ---------------------------------------------------------------------------
// _createCertificateForVSAClusterOCI
// ---------------------------------------------------------------------------

// newTestOciServicesWithCertMgmtForHyperscaler returns an OciServices wired
// with a CertificatesManagementClient backed by the supplied dispatcher and
// a zero-valued read client. Tests stub oci2.GetCertificateBundleWithPrivateKey
// to control the bundle fetch path.
func newTestOciServicesWithCertMgmtForHyperscaler(t *testing.T, mgmtDispatcher *mockOCIHTTPDispatcher) *oci2.OciServices {
	t.Helper()
	ctx := context.Background()
	pemKey := testOCIRSAPrivateKeyPEM(t)

	configProvider := ocicommon.NewRawConfigurationProvider(
		"ocid1.tenancy.oc1..test",
		"ocid1.user.oc1..test",
		"us-ashburn-1",
		"aa:bb:cc:dd:ee:ff:00:11",
		pemKey,
		nil,
	)

	mgmtCl, err := certificatesmanagement.NewCertificatesManagementClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)
	if mgmtDispatcher != nil {
		mgmtCl.HTTPClient = mgmtDispatcher
	}
	readCl, err := certificates.NewCertificatesClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)

	return &oci2.OciServices{
		Ctx:    ctx,
		Logger: log.NewLogger(),
		AdminOCIService: oci2.NewAdminOCIServiceWithCertClients(
			vault.VaultsClient{},
			secrets.SecretsClient{},
			mgmtCl,
			readCl,
		),
	}
}

func setOCICertEnv(t *testing.T) {
	t.Helper()
	origCompartment := env.OCICompartmentOCID
	origIssuer := env.OCIIssuerCAOCID
	origDns := env.VsaDeployedDnsName
	t.Cleanup(func() {
		env.OCICompartmentOCID = origCompartment
		env.OCIIssuerCAOCID = origIssuer
		env.VsaDeployedDnsName = origDns
	})
	env.OCICompartmentOCID = "ocid1.compartment.oc1..test"
	env.OCIIssuerCAOCID = "ocid1.certauthority.oc1..ca"
	env.VsaDeployedDnsName = "vsa.example.internal"
}

func Test_CreateCertificateForVSAClusterOCI_ConfigInvalid(t *testing.T) {
	origCompartment := env.OCICompartmentOCID
	origIssuer := env.OCIIssuerCAOCID
	origDns := env.VsaDeployedDnsName
	defer func() {
		env.OCICompartmentOCID = origCompartment
		env.OCIIssuerCAOCID = origIssuer
		env.VsaDeployedDnsName = origDns
	}()
	env.OCICompartmentOCID = ""
	env.OCIIssuerCAOCID = ""
	env.VsaDeployedDnsName = ""

	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, nil)
	resp, err := _createCertificateForVSAClusterOCI(svc, "cluster", "cert", &datamodel.PoolCredentials{}, false)
	assert.Nil(t, resp)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrOCIClientInitializationError, cerr.TrackingID)
	}
}

func Test_CreateCertificateForVSAClusterOCI_InputValidation(t *testing.T) {
	setOCICertEnv(t)
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, nil)

	t.Run("nil poolCredentials", func(t *testing.T) {
		resp, err := _createCertificateForVSAClusterOCI(svc, "cluster", "cert", nil, false)
		assert.Nil(t, resp)
		require.Error(t, err)
		cerr, ok := err.(*coreerrors.CustomError)
		if assert.True(t, ok) {
			assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})

	t.Run("empty clusterName", func(t *testing.T) {
		resp, err := _createCertificateForVSAClusterOCI(svc, "", "cert", &datamodel.PoolCredentials{Username: "admin"}, false)
		assert.Nil(t, resp)
		require.Error(t, err)
		cerr, ok := err.(*coreerrors.CustomError)
		if assert.True(t, ok) {
			assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})

	t.Run("empty certName", func(t *testing.T) {
		resp, err := _createCertificateForVSAClusterOCI(svc, "cluster", "", &datamodel.PoolCredentials{Username: "admin"}, false)
		assert.Nil(t, resp)
		require.Error(t, err)
		cerr, ok := err.(*coreerrors.CustomError)
		if assert.True(t, ok) {
			assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})
}

func Test_CreateCertificateForVSAClusterOCI_ReuseExistingActive(t *testing.T) {
	setOCICertEnv(t)
	const certName = "test-reuse-active-cert"
	const certOCID = "ocid1.certificate.oc1..reuse-active"

	defer common.RemoveFromCertAuthCache(certName)

	// ListCertificates returns one ACTIVE entry, then GetCertificate returns
	// metadata for that entry. The bundle fetch is stubbed via the test seam.
	mgmtDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/certificates/") {
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":"`+certOCID+`","name":"`+certName+`","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA",
					"issuerCertificateAuthorityId":"ocid1.certauthority.oc1..ca",
					"subject":{"commonName":"admin","organization":"Netapp"},
					"currentVersion":{"versionNumber":1,"serialNumber":"01"}
				}`), nil
			}
			return mockOCIJSONResponse(http.StatusOK, `{"items":[{
				"id":"`+certOCID+`","name":"`+certName+`","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA",
				"issuerCertificateAuthorityId":"ocid1.certauthority.oc1..ca",
				"subject":{"commonName":"admin","organization":"Netapp"},
				"currentVersionSummary":{"versionNumber":1,"serialNumber":"01"}
			}]}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, mgmtDispatcher)

	origBundle := oci2.GetCertificateBundleWithPrivateKey
	defer func() { oci2.GetCertificateBundleWithPrivateKey = origBundle }()
	oci2.GetCertificateBundleWithPrivateKey = func(_ *oci2.OciServices, _ string) (*certificates.CertificateBundleWithPrivateKey, error) {
		return &certificates.CertificateBundleWithPrivateKey{
			CertificatePem: ocicommon.String("reused-leaf"),
			PrivateKeyPem:  ocicommon.String("reused-key"),
			CertChainPem:   ocicommon.String("reused-chain"),
			SerialNumber:   ocicommon.String("01"),
			VersionNumber:  ocicommon.Int64(1),
			Validity: &certificates.Validity{
				TimeOfValidityNotBefore: &ocicommon.SDKTime{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				TimeOfValidityNotAfter:  &ocicommon.SDKTime{Time: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		}, nil
	}

	pc := &datamodel.PoolCredentials{Username: "admin"}
	resp, err := _createCertificateForVSAClusterOCI(svc, "deployment-foo", certName, pc, false)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Certificate)
	require.NotNil(t, resp.Secret)
	assert.Equal(t, "reused-leaf", resp.Certificate.PemCertificate)
	assert.Equal(t, "reused-key", resp.Secret.SecretVersion.Value)

	cached, exists := common.GetCertAuthCache(certName)
	require.True(t, exists)
	assert.Equal(t, "reused-leaf", cached.Certificate.SignedCertificate)
}

func Test_CreateCertificateForVSAClusterOCI_GetByNameFails(t *testing.T) {
	setOCICertEnv(t)
	mgmtDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockOCIServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, mgmtDispatcher)
	resp, err := _createCertificateForVSAClusterOCI(svc, "deployment-foo", "test-cert-fail", &datamodel.PoolCredentials{Username: "admin"}, false)
	assert.Nil(t, resp)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrOCIResourceFetchError, cerr.TrackingID)
	}
}

func Test_CreateCertificateForVSAClusterOCI_CreateFreshSuccess(t *testing.T) {
	setOCICertEnv(t)
	const certName = "test-create-fresh-cert"
	const newOCID = "ocid1.certificate.oc1..new"

	defer common.RemoveFromCertAuthCache(certName)

	// 1) ListCertificates → empty (cert does not exist yet)
	// 2) POST /certificates → new cert in CREATING state
	// 3) GetCertificate (poll) → ACTIVE
	// 4) GetCertificate (final fetch) → ACTIVE metadata
	var listSeen, createSeen, getSeen bool
	mgmtDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			path := req.URL.Path
			if req.Method == http.MethodPost && strings.HasSuffix(path, "/certificates") {
				createSeen = true
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":"`+newOCID+`","name":"`+certName+`","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"CREATING","configType":"ISSUED_BY_INTERNAL_CA",
					"issuerCertificateAuthorityId":"ocid1.certauthority.oc1..ca",
					"subject":{"commonName":"admin","organization":"Netapp"}
				}`), nil
			}
			if req.Method == http.MethodGet && strings.Contains(path, "/certificates/") {
				getSeen = true
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":"`+newOCID+`","name":"`+certName+`","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA",
					"issuerCertificateAuthorityId":"ocid1.certauthority.oc1..ca",
					"subject":{"commonName":"admin","organization":"Netapp"},
					"currentVersion":{"versionNumber":1,"serialNumber":"01"}
				}`), nil
			}
			// ListCertificates
			listSeen = true
			return mockOCIJSONResponse(http.StatusOK, `{"items":[]}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, mgmtDispatcher)

	origBundle := oci2.GetCertificateBundleWithPrivateKey
	defer func() { oci2.GetCertificateBundleWithPrivateKey = origBundle }()
	oci2.GetCertificateBundleWithPrivateKey = func(_ *oci2.OciServices, _ string) (*certificates.CertificateBundleWithPrivateKey, error) {
		return &certificates.CertificateBundleWithPrivateKey{
			CertificatePem: ocicommon.String("new-leaf"),
			PrivateKeyPem:  ocicommon.String("new-key"),
			SerialNumber:   ocicommon.String("01"),
			VersionNumber:  ocicommon.Int64(1),
			Validity: &certificates.Validity{
				TimeOfValidityNotBefore: &ocicommon.SDKTime{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				TimeOfValidityNotAfter:  &ocicommon.SDKTime{Time: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
			},
		}, nil
	}

	resp, err := _createCertificateForVSAClusterOCI(svc, "deployment-foo", certName,
		&datamodel.PoolCredentials{Username: "admin"}, true)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "new-leaf", resp.Certificate.PemCertificate)
	assert.Equal(t, "new-key", resp.Secret.SecretVersion.Value)
	assert.Equal(t, newOCID, resp.Certificate.CertificateID)

	assert.True(t, listSeen, "ListCertificates should be the first call")
	assert.True(t, createSeen, "POST /certificates should run after list returns empty")
	assert.True(t, getSeen, "GET on the new cert should run after create")

	cached, exists := common.GetCertAuthCache(certName)
	require.True(t, exists)
	assert.Equal(t, "new-leaf", cached.Certificate.SignedCertificate)
}

func Test_CreateCertificateForVSAClusterOCI_FailedStateTriggersRecreate(t *testing.T) {
	setOCICertEnv(t)
	const certName = "test-failed-recreate-cert"
	const oldOCID = "ocid1.certificate.oc1..failed"
	const newOCID = "ocid1.certificate.oc1..fresh"

	defer common.RemoveFromCertAuthCache(certName)

	var listSeen, scheduleSeen, createSeen bool
	mgmtDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			path := req.URL.Path
			switch {
			case strings.Contains(path, "scheduleDeletion"):
				scheduleSeen = true
				return mockOCIJSONResponse(http.StatusOK, `{}`), nil
			case req.Method == http.MethodGet && strings.Contains(path, "/certificates/"+oldOCID):
				// pre-flight Get for delete returns FAILED so schedule runs
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":"`+oldOCID+`","name":"`+certName+`","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"FAILED","configType":"ISSUED_BY_INTERNAL_CA"
				}`), nil
			case req.Method == http.MethodGet && strings.Contains(path, "/certificates/"+newOCID):
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":"`+newOCID+`","name":"`+certName+`","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA",
					"issuerCertificateAuthorityId":"ocid1.certauthority.oc1..ca",
					"subject":{"commonName":"admin","organization":"Netapp"}
				}`), nil
			case req.Method == http.MethodPost && strings.HasSuffix(path, "/certificates"):
				createSeen = true
				return mockOCIJSONResponse(http.StatusOK, `{
					"id":"`+newOCID+`","name":"`+certName+`","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"CREATING","configType":"ISSUED_BY_INTERNAL_CA",
					"issuerCertificateAuthorityId":"ocid1.certauthority.oc1..ca"
				}`), nil
			default:
				// ListCertificates returns the FAILED entry
				listSeen = true
				return mockOCIJSONResponse(http.StatusOK, `{"items":[{
					"id":"`+oldOCID+`","name":"`+certName+`","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"FAILED","configType":"ISSUED_BY_INTERNAL_CA"
				}]}`), nil
			}
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, mgmtDispatcher)

	origBundle := oci2.GetCertificateBundleWithPrivateKey
	defer func() { oci2.GetCertificateBundleWithPrivateKey = origBundle }()
	oci2.GetCertificateBundleWithPrivateKey = func(_ *oci2.OciServices, _ string) (*certificates.CertificateBundleWithPrivateKey, error) {
		return &certificates.CertificateBundleWithPrivateKey{
			CertificatePem: ocicommon.String("new-leaf-after-failed"),
			PrivateKeyPem:  ocicommon.String("new-key-after-failed"),
		}, nil
	}

	resp, err := _createCertificateForVSAClusterOCI(svc, "deployment-foo", certName,
		&datamodel.PoolCredentials{Username: "admin"}, false)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, listSeen, "list must run first")
	assert.True(t, scheduleSeen, "schedule deletion must run for FAILED cert")
	assert.True(t, createSeen, "create must run after deleting FAILED cert")
	assert.Equal(t, "new-leaf-after-failed", resp.Certificate.PemCertificate)
}

// ---------------------------------------------------------------------------
// _getCertificateFromCacheOrCAS — additional branches
// ---------------------------------------------------------------------------

func Test_GetCertificateFromCacheOrCAS_NotFoundOnOCI(t *testing.T) {
	const cacheKey = "test-getcertfromcache-oci-notfound"
	const certOCID = "ocid1.certificate.oc1..notfound"

	origGet := common.GetCertAuthCache
	defer func() { common.GetCertAuthCache = origGet }()
	common.GetCertAuthCache = func(key string) (*models.CertCache, bool) { return nil, false }

	// OCI service GetCertificate returns (nil, nil) for 404. We simulate that
	// by returning a 404 from the metadata Get inside the cert mgmt client.
	mgmtDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockOCIServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no cert"}
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, mgmtDispatcher)

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) {
		return svc, nil
	}

	cert, err := _getCertificateFromCacheOrCAS(context.Background(), &datamodel.ExternalCredRef{
		Name: cacheKey, ExternalIdentifier: certOCID,
	})
	assert.Nil(t, cert)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrOCIResourceFetchError, cerr.TrackingID)
		require.NotNil(t, cerr.OriginalErr)
		assert.Contains(t, cerr.OriginalErr.Error(), "not found in OCI Certificates Service")
	}
}

func Test_GetCertificateFromCacheOrCAS_MissingPemMaterial(t *testing.T) {
	const cacheKey = "test-getcertfromcache-oci-missingpem"
	const certOCID = "ocid1.certificate.oc1..missingpem"

	origGet := common.GetCertAuthCache
	defer func() { common.GetCertAuthCache = origGet }()
	common.GetCertAuthCache = func(key string) (*models.CertCache, bool) { return nil, false }

	mgmtDispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, `{
				"id":"`+certOCID+`","name":"`+cacheKey+`","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, mgmtDispatcher)

	origGetOCI := hyperscaler.GetOCIService
	defer func() { hyperscaler.GetOCIService = origGetOCI }()
	hyperscaler.GetOCIService = func(ctx context.Context) (*oci2.OciServices, error) { return svc, nil }

	origBundle := oci2.GetCertificateBundleWithPrivateKey
	defer func() { oci2.GetCertificateBundleWithPrivateKey = origBundle }()
	oci2.GetCertificateBundleWithPrivateKey = func(_ *oci2.OciServices, _ string) (*certificates.CertificateBundleWithPrivateKey, error) {
		// Missing PemCertificate and PrivateKeyPem.
		return &certificates.CertificateBundleWithPrivateKey{
			SerialNumber:  ocicommon.String("01"),
			VersionNumber: ocicommon.Int64(1),
		}, nil
	}

	cert, err := _getCertificateFromCacheOrCAS(context.Background(), &datamodel.ExternalCredRef{
		Name: cacheKey, ExternalIdentifier: certOCID,
	})
	assert.Nil(t, cert)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrOCIResourceFetchError, cerr.TrackingID)
		require.NotNil(t, cerr.OriginalErr)
		assert.Contains(t, cerr.OriginalErr.Error(), "missing required PEM material")
	}
}

// ---------------------------------------------------------------------------
// _getProviderByNode — OCI USER_CERTIFICATE branches
// ---------------------------------------------------------------------------

func Test_GetProviderByNode_OCI_UserCert_MissingExternalCertificate(t *testing.T) {
	origH := env.Hyperscaler
	defer func() { env.Hyperscaler = origH }()
	env.Hyperscaler = "oci"

	node := &models.Node{
		Name:                           "node-oci-missing",
		EndpointAddressesToHostNameMap: map[string]string{"10.0.0.1": "10.0.0.1"},
		AuthType:                       env.USER_CERTIFICATE,
		// ExternalCertificate intentionally nil
	}

	provider, err := _getProviderByNode(context.Background(), node)
	assert.Nil(t, provider)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_GetProviderByNode_OCI_UserCert_NoEndpointAddresses(t *testing.T) {
	origH := env.Hyperscaler
	defer func() { env.Hyperscaler = origH }()
	env.Hyperscaler = "oci"

	node := &models.Node{
		Name:                           "node-oci-no-ip",
		EndpointAddressesToHostNameMap: map[string]string{},
		AuthType:                       env.USER_CERTIFICATE,
	}

	provider, err := _getProviderByNode(context.Background(), node)
	assert.Nil(t, provider)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrVSAClusterNodeIPAddressNotFound, cerr.TrackingID)
	}
}

func Test_GetProviderByNode_OCI_UserCert_Success(t *testing.T) {
	origH := env.Hyperscaler
	defer func() { env.Hyperscaler = origH }()
	env.Hyperscaler = "oci"

	node := &models.Node{
		Name:                           "node-oci-cert-ok",
		EndpointAddressesToHostNameMap: map[string]string{"10.0.0.2": "10.0.0.2"},
		AuthType:                       env.USER_CERTIFICATE,
		ExternalCertificate: &datamodel.ExternalCredRef{
			Name:               "cluster-cert",
			ExternalIdentifier: "ocid1.certificate.oc1..abc",
		},
		ExternalSecret: &datamodel.ExternalCredRef{
			Name:               "cluster-admin-secret",
			ExternalIdentifier: "ocid1.vaultsecret.oc1..pw",
		},
	}

	origGetCert := GetCertificateFromCacheOrCAS
	defer func() { GetCertificateFromCacheOrCAS = origGetCert }()
	GetCertificateFromCacheOrCAS = func(ctx context.Context, ref *datamodel.ExternalCredRef) (*models.Certificate, error) {
		assert.Equal(t, "cluster-cert", ref.Name)
		return &models.Certificate{
			CommonName:               "admin",
			SignedCertificate:        "leaf",
			PrivateKey:               "key",
			InterMediateCertificates: []string{"chain"},
		}, nil
	}

	origGetPwd := GetPasswordFromCacheOrOCIVault
	defer func() { GetPasswordFromCacheOrOCIVault = origGetPwd }()
	GetPasswordFromCacheOrOCIVault = func(ctx context.Context, ref *datamodel.ExternalCredRef) (string, error) {
		return "vault-pw", nil
	}

	provider, err := _getProviderByNode(context.Background(), node)
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func Test_GetProviderByNode_OCI_UserCert_CertFetchFails(t *testing.T) {
	origH := env.Hyperscaler
	defer func() { env.Hyperscaler = origH }()
	env.Hyperscaler = "oci"

	node := &models.Node{
		Name:                           "node-oci-cert-fail",
		EndpointAddressesToHostNameMap: map[string]string{"10.0.0.3": "10.0.0.3"},
		AuthType:                       env.USER_CERTIFICATE,
		ExternalCertificate: &datamodel.ExternalCredRef{
			Name:               "cluster-cert",
			ExternalIdentifier: "ocid1.certificate.oc1..abc",
		},
	}

	origGetCert := GetCertificateFromCacheOrCAS
	defer func() { GetCertificateFromCacheOrCAS = origGetCert }()
	GetCertificateFromCacheOrCAS = func(ctx context.Context, ref *datamodel.ExternalCredRef) (*models.Certificate, error) {
		return nil, fmt.Errorf("cas fetch failed")
	}

	provider, err := _getProviderByNode(context.Background(), node)
	assert.Nil(t, provider)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrOCIResourceFetchError, cerr.TrackingID)
	}
}

func Test_GetProviderByNode_OCI_UserCert_PasswordFetchFailure_StillReturnsProvider(t *testing.T) {
	origH := env.Hyperscaler
	defer func() { env.Hyperscaler = origH }()
	env.Hyperscaler = "oci"

	node := &models.Node{
		Name:                           "node-oci-cert-pwfail",
		EndpointAddressesToHostNameMap: map[string]string{"10.0.0.4": "10.0.0.4"},
		AuthType:                       env.USER_CERTIFICATE,
		ExternalCertificate: &datamodel.ExternalCredRef{
			Name:               "cluster-cert",
			ExternalIdentifier: "ocid1.certificate.oc1..abc",
		},
		ExternalSecret: &datamodel.ExternalCredRef{
			Name:               "cluster-admin-secret",
			ExternalIdentifier: "ocid1.vaultsecret.oc1..pw",
		},
	}

	origGetCert := GetCertificateFromCacheOrCAS
	defer func() { GetCertificateFromCacheOrCAS = origGetCert }()
	GetCertificateFromCacheOrCAS = func(ctx context.Context, ref *datamodel.ExternalCredRef) (*models.Certificate, error) {
		return &models.Certificate{
			CommonName: "admin", SignedCertificate: "leaf", PrivateKey: "key",
		}, nil
	}

	origGetPwd := GetPasswordFromCacheOrOCIVault
	defer func() { GetPasswordFromCacheOrOCIVault = origGetPwd }()
	GetPasswordFromCacheOrOCIVault = func(ctx context.Context, ref *datamodel.ExternalCredRef) (string, error) {
		return "", fmt.Errorf("vault outage")
	}

	provider, err := _getProviderByNode(context.Background(), node)
	require.NoError(t, err, "password failure must be tolerated for cert-auth path")
	assert.NotNil(t, provider)
}

// ---------------------------------------------------------------------------
// _getProviderByNode — OCI separate client mTLS certificate branch
// ---------------------------------------------------------------------------

func ociUserCertNodeWithClientCert() *models.Node {
	return &models.Node{
		Name:                           "node-oci-sep-cli",
		DeploymentName:                 "dep",
		EndpointAddressesToHostNameMap: map[string]string{"10.0.0.5": "10.0.0.5"},
		AuthType:                       env.USER_CERTIFICATE,
		ExternalCertificate: &datamodel.ExternalCredRef{
			Name:               "cluster-cert",
			ExternalIdentifier: "ocid1.certificate.oc1..server",
		},
		ExternalClientCertificate: &datamodel.ExternalCredRef{
			Name:               "client-cert",
			ExternalIdentifier: "ocid1.certificate.oc1..client",
		},
	}
}

func Test_GetProviderByNode_OCI_SeparateClientCert_Success(t *testing.T) {
	origH := env.Hyperscaler
	origSep := env.OCISeparateMTLSClientCert
	defer func() { env.Hyperscaler = origH; env.OCISeparateMTLSClientCert = origSep }()
	env.Hyperscaler = "oci"
	env.OCISeparateMTLSClientCert = true

	origGetCert := GetCertificateFromCacheOrCAS
	defer func() { GetCertificateFromCacheOrCAS = origGetCert }()
	GetCertificateFromCacheOrCAS = func(ctx context.Context, ref *datamodel.ExternalCredRef) (*models.Certificate, error) {
		if ref.Name == "client-cert" {
			return &models.Certificate{
				CommonName:        "client-cn",
				SignedCertificate: "client-leaf",
				PrivateKey:        "client-key",
			}, nil
		}
		return &models.Certificate{
			CommonName:               "server-cn",
			SignedCertificate:        "server-leaf",
			PrivateKey:               "server-key",
			InterMediateCertificates: []string{"server-chain"},
		}, nil
	}

	provider, err := _getProviderByNode(context.Background(), ociUserCertNodeWithClientCert())
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func Test_GetProviderByNode_OCI_SeparateClientCert_FetchFails(t *testing.T) {
	origH := env.Hyperscaler
	origSep := env.OCISeparateMTLSClientCert
	defer func() { env.Hyperscaler = origH; env.OCISeparateMTLSClientCert = origSep }()
	env.Hyperscaler = "oci"
	env.OCISeparateMTLSClientCert = true

	origGetCert := GetCertificateFromCacheOrCAS
	defer func() { GetCertificateFromCacheOrCAS = origGetCert }()
	GetCertificateFromCacheOrCAS = func(ctx context.Context, ref *datamodel.ExternalCredRef) (*models.Certificate, error) {
		if ref.Name == "client-cert" {
			return nil, fmt.Errorf("client cas fetch failed")
		}
		return &models.Certificate{CommonName: "server-cn", SignedCertificate: "server-leaf", PrivateKey: "server-key"}, nil
	}

	provider, err := _getProviderByNode(context.Background(), ociUserCertNodeWithClientCert())
	assert.Nil(t, provider)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrOCIResourceFetchError, cerr.TrackingID)
	}
}

func Test_GetProviderByNode_OCI_SeparateClientCert_NilClientCert(t *testing.T) {
	origH := env.Hyperscaler
	origSep := env.OCISeparateMTLSClientCert
	defer func() { env.Hyperscaler = origH; env.OCISeparateMTLSClientCert = origSep }()
	env.Hyperscaler = "oci"
	env.OCISeparateMTLSClientCert = true

	origGetCert := GetCertificateFromCacheOrCAS
	defer func() { GetCertificateFromCacheOrCAS = origGetCert }()
	GetCertificateFromCacheOrCAS = func(ctx context.Context, ref *datamodel.ExternalCredRef) (*models.Certificate, error) {
		if ref.Name == "client-cert" {
			return nil, nil
		}
		return &models.Certificate{CommonName: "server-cn", SignedCertificate: "server-leaf", PrivateKey: "server-key"}, nil
	}

	provider, err := _getProviderByNode(context.Background(), ociUserCertNodeWithClientCert())
	assert.Nil(t, provider)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// _createClientCertificateForVSAClusterOCI validation
// ---------------------------------------------------------------------------

func Test_createClientCertificateForVSAClusterOCI_Validation(t *testing.T) {
	t.Run("nil oci service", func(tt *testing.T) {
		_, err := _createClientCertificateForVSAClusterOCI(nil, "cert", "cn")
		require.Error(tt, err)
	})

	t.Run("missing env config", func(tt *testing.T) {
		origComp := env.OCICompartmentOCID
		origCA := env.OCIClientIssuerCAOCID
		defer func() { env.OCICompartmentOCID = origComp; env.OCIClientIssuerCAOCID = origCA }()
		env.OCICompartmentOCID = ""
		env.OCIClientIssuerCAOCID = ""
		_, err := _createClientCertificateForVSAClusterOCI(&oci2.OciServices{}, "cert", "cn")
		require.Error(tt, err)
	})

	t.Run("empty certName or commonName", func(tt *testing.T) {
		origComp := env.OCICompartmentOCID
		origCA := env.OCIClientIssuerCAOCID
		defer func() { env.OCICompartmentOCID = origComp; env.OCIClientIssuerCAOCID = origCA }()
		env.OCICompartmentOCID = "ocid1.compartment.oc1..c"
		env.OCIClientIssuerCAOCID = "ocid1.certificateauthority.oc1..ca"
		_, err := _createClientCertificateForVSAClusterOCI(&oci2.OciServices{}, "", "")
		require.Error(tt, err)
	})
}
