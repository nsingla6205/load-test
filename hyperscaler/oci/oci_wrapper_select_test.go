package oci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// resetSharedOCIClientsForTest clears the cached OCI client transport so tests
// that stub client constructors or toggle proxy config get a fresh build.
func resetSharedOCIClientsForTest() {
	sharedAdminOCISvc = nil
	sharedProxyClient = nil
}

func withOCIProxyBaseURL(t *testing.T, baseURL string) {
	t.Helper()
	orig := env.OCIProxyBaseURL
	origAllowHTTP := env.OCIProxyAllowHTTP
	env.OCIProxyBaseURL = baseURL
	// Tests point the proxy at http httptest servers, so allow plaintext http.
	env.OCIProxyAllowHTTP = true
	// The proxy client is cached per process; clear it so this test builds a
	// fresh client against baseURL and does not leak one to later tests.
	resetSharedOCIClientsForTest()
	t.Cleanup(func() {
		env.OCIProxyBaseURL = orig
		env.OCIProxyAllowHTTP = origAllowHTTP
		resetSharedOCIClientsForTest()
	})
}

// withStubbedSDKClientInit replaces the SDK client constructors with zero-value
// stubs so InitializeClients can wire AdminOCIService without real OCI creds.
func withStubbedSDKClientInit(t *testing.T) {
	t.Helper()
	origVault := initializeVaultClient
	origSecrets := initializeSecretsClient
	origCertMgmt := initializeCertManagementClient
	origCertRead := initializeCertReadClient
	origDns := initializeDnsClient
	origObjStorage := initializeObjectStorageClient
	// Clear the cached transport so the stubs below are actually exercised on the
	// next InitSharedOCIClients and are not reused by a later test.
	resetSharedOCIClientsForTest()
	t.Cleanup(func() {
		initializeVaultClient = origVault
		initializeSecretsClient = origSecrets
		initializeCertManagementClient = origCertMgmt
		initializeCertReadClient = origCertRead
		initializeDnsClient = origDns
		initializeObjectStorageClient = origObjStorage
		resetSharedOCIClientsForTest()
	})

	initializeVaultClient = func() (*vault.VaultsClient, error) { cl := vault.VaultsClient{}; return &cl, nil }
	initializeSecretsClient = func() (*secrets.SecretsClient, error) { cl := secrets.SecretsClient{}; return &cl, nil }
	initializeCertManagementClient = func() (*certificatesmanagement.CertificatesManagementClient, error) {
		cl := certificatesmanagement.CertificatesManagementClient{}
		return &cl, nil
	}
	initializeCertReadClient = func() (*certificates.CertificatesClient, error) {
		cl := certificates.CertificatesClient{}
		return &cl, nil
	}
	initializeDnsClient = func() (*dns.DnsClient, error) { cl := dns.DnsClient{}; return &cl, nil }
	initializeObjectStorageClient = func() (*objectstorage.ObjectStorageClient, error) {
		cl := objectstorage.ObjectStorageClient{}
		return &cl, nil
	}
}

func TestInitializeClients_OCIProxyWrapper(t *testing.T) {
	ctx := context.Background()

	t.Run("proxy mode — base URL set initialises proxy wrapper AND SDK clients (reads use SDK)", func(t *testing.T) {
		withOCIProxyBaseURL(t, "https://oci-cloud-proxy.internal")
		withStubbedSDKClientInit(t)
		require.NoError(t, InitSharedOCIClients(ctx))

		svc := &OciServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		err := svc.InitializeClients()
		require.NoError(t, err)
		assert.True(t, svc.IsAdminClientInitialized())
		// SDK clients are initialised in proxy mode too — read (GET/LIST)
		// operations always go directly to OCI via the SDK.
		assert.NotNil(t, svc.AdminOCIService)
		assert.NotNil(t, svc.wrapper)
		proxyW, ok := svc.wrapper.(*ociProxyWrapper)
		assert.True(t, ok, "expected ociProxyWrapper")
		assert.NotNil(t, proxyW.svc)
	})

	t.Run("proxy mode — already initialised returns nil immediately", func(t *testing.T) {
		withOCIProxyBaseURL(t, "https://oci-cloud-proxy.internal")

		svc := &OciServices{
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
			AdminOCIService: &AdminOCIService{},
			wrapper:         &ociProxyWrapper{svc: &OciServices{}},
		}
		err := svc.InitializeClients()
		assert.NoError(t, err)
	})

	t.Run("SDK mode — sets ociSDKWrapper after SDK client init", func(t *testing.T) {
		withOCIProxyBaseURL(t, "")
		withStubbedSDKClientInit(t)
		require.NoError(t, InitSharedOCIClients(ctx))

		svc := &OciServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
		}
		err := svc.InitializeClients()
		require.NoError(t, err)
		assert.True(t, svc.IsAdminClientInitialized())
		assert.NotNil(t, svc.AdminOCIService)
		_, ok := svc.wrapper.(*ociSDKWrapper)
		assert.True(t, ok, "expected ociSDKWrapper")
	})
}

func TestIsAdminClientInitialized_ProxyMode(t *testing.T) {
	withOCIProxyBaseURL(t, "https://proxy")

	t.Run("proxy wrapper + SDK clients set — returns true", func(t *testing.T) {
		svc := &OciServices{
			AdminOCIService: &AdminOCIService{},
			wrapper:         &ociProxyWrapper{svc: &OciServices{}},
		}
		assert.True(t, svc.IsAdminClientInitialized())
	})

	t.Run("proxy wrapper set but SDK clients nil — returns false", func(t *testing.T) {
		svc := &OciServices{wrapper: &ociProxyWrapper{svc: &OciServices{}}}
		assert.False(t, svc.IsAdminClientInitialized())
	})

	t.Run("proxy wrapper nil — returns false", func(t *testing.T) {
		svc := &OciServices{AdminOCIService: &AdminOCIService{}}
		assert.False(t, svc.IsAdminClientInitialized())
	})
}

// TestIsAdminClientInitialized_modeMismatch guards the env-toggle contract: a
// wrapper left over from SDK mode must not be treated as "initialized" once the
// proxy toggle is on, so InitializeClients can switch to the proxy wrapper.
func TestIsAdminClientInitialized_modeMismatch(t *testing.T) {
	ctx := context.Background()
	withOCIProxyBaseURL(t, "https://oci-cloud-proxy.internal")
	withStubbedSDKClientInit(t)
	require.NoError(t, InitSharedOCIClients(ctx))

	svc := &OciServices{
		Ctx:             ctx,
		Logger:          util.GetLogger(ctx),
		AdminOCIService: &AdminOCIService{},
		wrapper:         &ociSDKWrapper{},
	}

	// Stale SDK wrapper does not satisfy proxy mode.
	assert.False(t, svc.IsAdminClientInitialized())

	// InitializeClients should proceed and swap in the proxy wrapper.
	require.NoError(t, svc.InitializeClients())
	_, ok := svc.wrapper.(*ociProxyWrapper)
	assert.True(t, ok, "expected proxy wrapper after re-init once proxy toggle is on")
	assert.True(t, svc.IsAdminClientInitialized())
}

func TestOCIProxyWrapper_CreateSecret_callsProxy(t *testing.T) {
	ctx := proxyTestCtx()
	lg := util.GetLogger(ctx)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/20180608/secrets", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "ocid1.vaultsecret.oc1..created",
		})
	}))
	t.Cleanup(server.Close)
	withOCIProxyBaseURL(t, server.URL)

	baseSvc := &OciServices{Ctx: ctx, Logger: lg}
	client, err := newOgenProxyClient()
	require.NoError(t, err)
	svc := &OciServices{
		Ctx:     ctx,
		Logger:  lg,
		wrapper: &ociProxyWrapper{svc: baseSvc, proxyClient: client},
	}

	secret, err := svc.CreateSecret("comp", "vault", "key", "name", "value")
	require.NoError(t, err)
	require.NotNil(t, secret)
	assert.Equal(t, "ocid1.vaultsecret.oc1..created", secret.Ocid)
	assert.Equal(t, "value", secret.Value)
}

func TestOCIProxyResourceNotFoundCheck(t *testing.T) {
	t.Run("404 with nil error — returns nil", func(t *testing.T) {
		assert.NoError(t, ociProxyResourceNotFoundCheck(http.StatusNotFound, nil))
	})

	t.Run("500 with error — returns original error", func(t *testing.T) {
		err := assert.AnError
		result := ociProxyResourceNotFoundCheck(http.StatusInternalServerError, err)
		assert.Equal(t, err, result)
	})
}

func TestActiveOCIWrapper_FallbackForTests(t *testing.T) {
	svc := &OciServices{
		AdminOCIService: &AdminOCIService{},
	}
	wrapper := svc.activeOCIWrapper()
	_, ok := wrapper.(*ociSDKWrapper)
	assert.True(t, ok, "nil wrapper should fall back to ociSDKWrapper for tests")
}
