package oci

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/dns"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"github.com/oracle/oci-go-sdk/v65/secrets"
	"github.com/oracle/oci-go-sdk/v65/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mockServiceError implements common.ServiceError (and the error interface)
// so that common.IsServiceError returns true for it.
type mockServiceError struct {
	statusCode int
	code       string
	message    string
	opcReqID   string
}

func (e *mockServiceError) GetHTTPStatusCode() int  { return e.statusCode }
func (e *mockServiceError) GetMessage() string      { return e.message }
func (e *mockServiceError) GetCode() string         { return e.code }
func (e *mockServiceError) GetOpcRequestID() string { return e.opcReqID }
func (e *mockServiceError) Error() string {
	return fmt.Sprintf("Service error: %s (status %d)", e.message, e.statusCode)
}

// ---------------------------------------------------------------------------
// TestInitializeClients
// ---------------------------------------------------------------------------

func TestInitializeClients(t *testing.T) {
	ctx := context.Background()

	t.Run("already initialised — returns nil immediately", func(t *testing.T) {
		svc := &OciServices{
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
			AdminOCIService: &AdminOCIService{},
		}
		err := svc.InitializeClients()
		assert.NoError(t, err)
	})

	t.Run("proxy backend when OCI_PROXY_BASE_URL is set", func(t *testing.T) {
		origBase := env.OCIProxyBaseURL
		t.Cleanup(func() { env.OCIProxyBaseURL = origBase })
		env.OCIProxyBaseURL = "https://oci-cloud-proxy.internal"
		withStubbedSDKClientInit(t)

		svc := &OciServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		err := svc.InitializeClients()
		require.NoError(t, err)
		// SDK clients are initialised in proxy mode too (reads use the SDK).
		assert.NotNil(t, svc.AdminOCIService)
		require.NotNil(t, svc.wrapper)
		_, ok := svc.wrapper.(*ociProxyWrapper)
		assert.True(t, ok, "expected ociProxyWrapper")
	})

	t.Run("proxy backend invalid URL returns error", func(t *testing.T) {
		origBase := env.OCIProxyBaseURL
		t.Cleanup(func() { env.OCIProxyBaseURL = origBase })
		env.OCIProxyBaseURL = "not-a-url"
		withStubbedSDKClientInit(t)

		svc := &OciServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
		err := svc.InitializeClients()
		require.Error(t, err)
	})

	t.Run("successful initialisation", func(t *testing.T) {
		origBase := env.OCIProxyBaseURL
		t.Cleanup(func() { env.OCIProxyBaseURL = origBase })
		env.OCIProxyBaseURL = ""

		origVault := initializeVaultClient
		origSecrets := initializeSecretsClient
		origCertMgmt := initializeCertManagementClient
		origCertRead := initializeCertReadClient
		origDns := initializeDnsClient
		origObjStorage := initializeObjectStorageClient
		defer func() {
			initializeVaultClient = origVault
			initializeSecretsClient = origSecrets
			initializeCertManagementClient = origCertMgmt
			initializeCertReadClient = origCertRead
			initializeDnsClient = origDns
			initializeObjectStorageClient = origObjStorage
		}()

		initializeVaultClient = func() (*vault.VaultsClient, error) {
			cl := vault.VaultsClient{}
			return &cl, nil
		}
		initializeSecretsClient = func() (*secrets.SecretsClient, error) {
			cl := secrets.SecretsClient{}
			return &cl, nil
		}
		initializeCertManagementClient = func() (*certificatesmanagement.CertificatesManagementClient, error) {
			cl := certificatesmanagement.CertificatesManagementClient{}
			return &cl, nil
		}
		initializeCertReadClient = func() (*certificates.CertificatesClient, error) {
			cl := certificates.CertificatesClient{}
			return &cl, nil
		}
		initializeDnsClient = func() (*dns.DnsClient, error) {
			cl := dns.DnsClient{}
			return &cl, nil
		}
		initializeObjectStorageClient = func() (*objectstorage.ObjectStorageClient, error) {
			cl := objectstorage.ObjectStorageClient{}
			return &cl, nil
		}

		svc := &OciServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		err := svc.InitializeClients()
		assert.NoError(t, err)
		assert.True(t, svc.IsAdminClientInitialized())
	})

	t.Run("vault client init fails — returns error", func(t *testing.T) {
		origVault := initializeVaultClient
		origSecrets := initializeSecretsClient
		defer func() {
			initializeVaultClient = origVault
			initializeSecretsClient = origSecrets
		}()

		initializeVaultClient = func() (*vault.VaultsClient, error) {
			return nil, errors.New("vault auth failure")
		}

		svc := &OciServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		err := svc.InitializeClients()
		assert.Error(t, err)
		assert.False(t, svc.IsAdminClientInitialized())
	})

	t.Run("secrets client init fails — returns error", func(t *testing.T) {
		origVault := initializeVaultClient
		origSecrets := initializeSecretsClient
		defer func() {
			initializeVaultClient = origVault
			initializeSecretsClient = origSecrets
		}()

		initializeVaultClient = func() (*vault.VaultsClient, error) {
			cl := vault.VaultsClient{}
			return &cl, nil
		}
		initializeSecretsClient = func() (*secrets.SecretsClient, error) {
			return nil, errors.New("secrets auth failure")
		}

		svc := &OciServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		err := svc.InitializeClients()
		assert.Error(t, err)
		assert.False(t, svc.IsAdminClientInitialized())
	})
}

// ---------------------------------------------------------------------------
// TestIsAdminClientInitialized
// ---------------------------------------------------------------------------

func TestIsAdminClientInitialized(t *testing.T) {
	t.Run("nil AdminOCIService — returns false", func(t *testing.T) {
		svc := &OciServices{}
		assert.False(t, svc.IsAdminClientInitialized())
	})

	t.Run("non-nil AdminOCIService — returns true", func(t *testing.T) {
		svc := &OciServices{AdminOCIService: &AdminOCIService{}}
		assert.True(t, svc.IsAdminClientInitialized())
	})
}

// ---------------------------------------------------------------------------
// TestGetLogger
// ---------------------------------------------------------------------------

func TestGetLogger(t *testing.T) {
	t.Run("logger already set — returns it", func(t *testing.T) {
		ctx := context.Background()
		lg := util.GetLogger(ctx)
		svc := &OciServices{Ctx: ctx, Logger: lg}
		assert.Equal(t, lg, svc.GetLogger())
	})

	t.Run("logger nil — creates from context", func(t *testing.T) {
		ctx := context.Background()
		svc := &OciServices{Ctx: ctx}
		lg := svc.GetLogger()
		assert.NotNil(t, lg)
		assert.Equal(t, lg, svc.Logger, "logger should be cached on the struct")
	})
}

// ---------------------------------------------------------------------------
// TestGetContext
// ---------------------------------------------------------------------------

func TestGetContext(t *testing.T) {
	t.Run("context already set — returns it", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), struct{}{}, "test")
		svc := &OciServices{Ctx: ctx}
		assert.Equal(t, ctx, svc.GetContext())
	})

	t.Run("context nil — returns background", func(t *testing.T) {
		svc := &OciServices{}
		ctx := svc.GetContext()
		assert.NotNil(t, ctx)
		assert.Equal(t, ctx, svc.Ctx, "context should be cached on the struct")
	})
}

// ---------------------------------------------------------------------------
// TestOciConfigProvider
// ---------------------------------------------------------------------------

func TestOciConfigProvider(t *testing.T) {
	t.Run("config_file — returns default provider", func(t *testing.T) {
		origAuthType := env.OCIAuthType
		defer func() { env.OCIAuthType = origAuthType }()

		env.OCIAuthType = "config_file"
		provider, err := ociConfigProvider()
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("unsupported auth type — returns error", func(t *testing.T) {
		origAuthType := env.OCIAuthType
		defer func() { env.OCIAuthType = origAuthType }()

		env.OCIAuthType = "some_unknown_type"
		provider, err := ociConfigProvider()
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "unsupported OCI_AUTH_TYPE")
		assert.Contains(t, err.Error(), "some_unknown_type")
	})

	t.Run("env auth type — success when all vars set", func(t *testing.T) {
		origAuthType := env.OCIAuthType
		defer func() { env.OCIAuthType = origAuthType }()

		env.OCIAuthType = "env"

		envVars := map[string]string{
			"OCI_TENANCY":     "ocid1.tenancy.oc1..test",
			"OCI_USER":        "ocid1.user.oc1..test",
			"OCI_REGION":      "us-ashburn-1",
			"OCI_FINGERPRINT": "aa:bb:cc:dd:ee:ff",
			"OCI_PRIVATE_KEY": "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----",
		}
		for k, v := range envVars {
			t.Setenv(k, v)
		}

		provider, err := ociConfigProvider()
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})
}

// ---------------------------------------------------------------------------
// TestOciConfigProviderFromEnv
// ---------------------------------------------------------------------------

func TestOciConfigProviderFromEnv(t *testing.T) {
	setRequiredEnvVars := func(t *testing.T) {
		t.Helper()
		t.Setenv("OCI_TENANCY", "ocid1.tenancy.oc1..test")
		t.Setenv("OCI_USER", "ocid1.user.oc1..test")
		t.Setenv("OCI_REGION", "us-ashburn-1")
		t.Setenv("OCI_FINGERPRINT", "aa:bb:cc:dd:ee:ff")
		t.Setenv("OCI_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----")
	}

	t.Run("all required vars present — success", func(t *testing.T) {
		setRequiredEnvVars(t)
		provider, err := ociConfigProviderFromEnv()
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("with passphrase — success", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("OCI_PASSPHRASE", "my-secret-passphrase")
		provider, err := ociConfigProviderFromEnv()
		assert.NoError(t, err)
		assert.NotNil(t, provider)
	})

	t.Run("missing single var — error lists it", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("OCI_TENANCY", "")

		provider, err := ociConfigProviderFromEnv()
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "OCI_TENANCY")
	})

	t.Run("missing multiple vars — error lists all", func(t *testing.T) {
		t.Setenv("OCI_TENANCY", "")
		t.Setenv("OCI_USER", "")
		t.Setenv("OCI_REGION", "us-ashburn-1")
		t.Setenv("OCI_FINGERPRINT", "")
		t.Setenv("OCI_PRIVATE_KEY", "")

		provider, err := ociConfigProviderFromEnv()
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "OCI_TENANCY")
		assert.Contains(t, err.Error(), "OCI_USER")
		assert.Contains(t, err.Error(), "OCI_FINGERPRINT")
		assert.Contains(t, err.Error(), "OCI_PRIVATE_KEY")
	})

	t.Run("missing all required vars — error lists all", func(t *testing.T) {
		t.Setenv("OCI_TENANCY", "")
		t.Setenv("OCI_USER", "")
		t.Setenv("OCI_REGION", "")
		t.Setenv("OCI_FINGERPRINT", "")
		t.Setenv("OCI_PRIVATE_KEY", "")

		provider, err := ociConfigProviderFromEnv()
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Contains(t, err.Error(), "missing required env vars")
	})
}

// ---------------------------------------------------------------------------
// TestNewOCIClients
// ---------------------------------------------------------------------------

func TestNewOCIClients(t *testing.T) {
	ctx := context.Background()

	t.Run("both clients succeed — returns AdminOCIService", func(t *testing.T) {
		origVault := initializeVaultClient
		origSecrets := initializeSecretsClient
		origCertMgmt := initializeCertManagementClient
		origCertRead := initializeCertReadClient
		origDns := initializeDnsClient
		origObjStorage := initializeObjectStorageClient
		defer func() {
			initializeVaultClient = origVault
			initializeSecretsClient = origSecrets
			initializeCertManagementClient = origCertMgmt
			initializeCertReadClient = origCertRead
			initializeDnsClient = origDns
			initializeObjectStorageClient = origObjStorage
		}()

		initializeVaultClient = func() (*vault.VaultsClient, error) {
			return &vault.VaultsClient{}, nil
		}
		initializeSecretsClient = func() (*secrets.SecretsClient, error) {
			return &secrets.SecretsClient{}, nil
		}
		initializeCertManagementClient = func() (*certificatesmanagement.CertificatesManagementClient, error) {
			return &certificatesmanagement.CertificatesManagementClient{}, nil
		}
		initializeCertReadClient = func() (*certificates.CertificatesClient, error) {
			return &certificates.CertificatesClient{}, nil
		}
		initializeDnsClient = func() (*dns.DnsClient, error) {
			return &dns.DnsClient{}, nil
		}
		initializeObjectStorageClient = func() (*objectstorage.ObjectStorageClient, error) {
			return &objectstorage.ObjectStorageClient{}, nil
		}

		admin, err := newOCIClients(ctx)
		require.NoError(t, err)
		assert.NotNil(t, admin)
	})

	t.Run("vault client fails — returns error", func(t *testing.T) {
		origVault := initializeVaultClient
		origSecrets := initializeSecretsClient
		defer func() {
			initializeVaultClient = origVault
			initializeSecretsClient = origSecrets
		}()

		initializeVaultClient = func() (*vault.VaultsClient, error) {
			return nil, errors.New("vault failure")
		}
		initializeSecretsClient = func() (*secrets.SecretsClient, error) {
			return &secrets.SecretsClient{}, nil
		}

		admin, err := newOCIClients(ctx)
		assert.Error(t, err)
		assert.Nil(t, admin)
	})

	t.Run("secrets client fails — returns error", func(t *testing.T) {
		origVault := initializeVaultClient
		origSecrets := initializeSecretsClient
		defer func() {
			initializeVaultClient = origVault
			initializeSecretsClient = origSecrets
		}()

		initializeVaultClient = func() (*vault.VaultsClient, error) {
			return &vault.VaultsClient{}, nil
		}
		initializeSecretsClient = func() (*secrets.SecretsClient, error) {
			return nil, errors.New("secrets failure")
		}

		admin, err := newOCIClients(ctx)
		assert.Error(t, err)
		assert.Nil(t, admin)
	})

	t.Run("dns client fails — returns error", func(t *testing.T) {
		origVault := initializeVaultClient
		origSecrets := initializeSecretsClient
		origCertMgmt := initializeCertManagementClient
		origCertRead := initializeCertReadClient
		origDns := initializeDnsClient
		origObjStorage := initializeObjectStorageClient
		defer func() {
			initializeVaultClient = origVault
			initializeSecretsClient = origSecrets
			initializeCertManagementClient = origCertMgmt
			initializeCertReadClient = origCertRead
			initializeDnsClient = origDns
			initializeObjectStorageClient = origObjStorage
		}()

		initializeVaultClient = func() (*vault.VaultsClient, error) {
			return &vault.VaultsClient{}, nil
		}
		initializeSecretsClient = func() (*secrets.SecretsClient, error) {
			return &secrets.SecretsClient{}, nil
		}
		initializeCertManagementClient = func() (*certificatesmanagement.CertificatesManagementClient, error) {
			return &certificatesmanagement.CertificatesManagementClient{}, nil
		}
		initializeCertReadClient = func() (*certificates.CertificatesClient, error) {
			return &certificates.CertificatesClient{}, nil
		}
		initializeDnsClient = func() (*dns.DnsClient, error) {
			return nil, errors.New("dns failure")
		}
		initializeObjectStorageClient = func() (*objectstorage.ObjectStorageClient, error) {
			return nil, errors.New("object storage failure")
		}

		admin, err := newOCIClients(ctx)
		assert.Error(t, err)
		assert.Nil(t, admin)
	})
}

// ---------------------------------------------------------------------------
// TestInitializeObjectStorageClient
// ---------------------------------------------------------------------------

func TestInitializeObjectStorageClient(t *testing.T) {
	t.Run("ociConfigProvider fails — returns error", func(t *testing.T) {
		origAuthType := env.OCIAuthType
		defer func() { env.OCIAuthType = origAuthType }()

		env.OCIAuthType = "unsupported_auth_type_for_test"

		client, err := _initializeObjectStorageClient()
		assert.Error(t, err)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "ociConfigProvider")
	})

	t.Run("invalid private key — SDK rejects config", func(t *testing.T) {
		origAuthType := env.OCIAuthType
		defer func() { env.OCIAuthType = origAuthType }()

		env.OCIAuthType = "env"
		t.Setenv("OCI_TENANCY", "ocid1.tenancy.oc1..test")
		t.Setenv("OCI_USER", "ocid1.user.oc1..test")
		t.Setenv("OCI_REGION", "us-ashburn-1")
		t.Setenv("OCI_FINGERPRINT", "aa:bb:cc:dd:ee:ff")
		t.Setenv("OCI_PRIVATE_KEY", "-----BEGIN RSA PRIVATE KEY-----\ninvalid\n-----END RSA PRIVATE KEY-----")

		client, err := _initializeObjectStorageClient()
		assert.Error(t, err)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "NewObjectStorageClientWithConfigurationProvider")
	})
}

// ---------------------------------------------------------------------------
// TestOciResourceNotFoundCheck
// ---------------------------------------------------------------------------

func TestOciResourceNotFoundCheck(t *testing.T) {
	t.Run("404 service error — returns nil", func(t *testing.T) {
		err := &mockServiceError{
			statusCode: http.StatusNotFound,
			code:       "NotAuthorizedOrNotFound",
			message:    "resource not found",
		}
		result := ociResourceNotFoundCheck(err)
		assert.NoError(t, result)
	})

	t.Run("400 service error — returns original error", func(t *testing.T) {
		err := &mockServiceError{
			statusCode: http.StatusBadRequest,
			code:       "InvalidParameter",
			message:    "bad request",
		}
		result := ociResourceNotFoundCheck(err)
		assert.Error(t, result)
		assert.Equal(t, err, result)
	})

	t.Run("500 service error — returns original error", func(t *testing.T) {
		err := &mockServiceError{
			statusCode: http.StatusInternalServerError,
			code:       "InternalError",
			message:    "internal server error",
		}
		result := ociResourceNotFoundCheck(err)
		assert.Error(t, result)
		assert.Equal(t, err, result)
	})

	t.Run("non-service error — returns original error", func(t *testing.T) {
		err := errors.New("generic network error")
		result := ociResourceNotFoundCheck(err)
		assert.Error(t, result)
		assert.Equal(t, err, result)
	})

	t.Run("nil error — returns nil", func(t *testing.T) {
		result := ociResourceNotFoundCheck(nil)
		assert.NoError(t, result)
	})
}
