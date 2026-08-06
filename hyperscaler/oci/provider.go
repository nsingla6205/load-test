package oci

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/common/auth"
	"github.com/oracle/oci-go-sdk/v65/dns"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"github.com/oracle/oci-go-sdk/v65/secrets"
	"github.com/oracle/oci-go-sdk/v65/vault"
	ocicloudproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/oci-cloud-proxy-client"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	logger "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// OciServices is the top-level struct for OCI interactions, mirroring GcpServices.
// It holds the request context, logger, and the admin-level OCI clients.
type OciServices struct {
	Ctx    context.Context
	Logger logger.Logger
	Retry  RetryStrategy

	AdminOCIService *AdminOCIService

	// wrapper is the active Strategy implementation (SDK or proxy) selected by
	// InitializeClients based on whether OCI_PROXY_BASE_URL is set. When nil
	wrapper ociWrapper
}

// AdminOCIService groups the OCI SDK clients needed for secret, certificate,
// DNS management and object storage pre-authenticated request (PAR) generation.
//
// OCI splits each domain into a write/management API and a read/retrieval API:
//   - vault.VaultsClient                                   — secret CRUD (write)
//   - secrets.SecretsClient                                — secret bundle retrieval (read)
//   - certificatesmanagement.CertificatesManagementClient  — certificate CRUD (write)
//   - certificates.CertificatesClient                      — certificate bundle retrieval (read)
//   - dns.DnsClient                                        — DNS zones / RRSets (read + write)
//
// Object Storage exposes a single client used to mint PARs for the VSA image
// hand-off to VLM, mirroring what GcpServices does with V4 signed URLs on GCS.
//
// Docs:
//   - Vault (write):                   https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/
//   - Secrets (read):                  https://docs.oracle.com/en-us/iaas/api/#/en/secretretrieval/20190301/
//   - Certificates Management (write): https://docs.oracle.com/en-us/iaas/api/#/en/certificatesmanagement/20210224/
//   - Certificates (read):             https://docs.oracle.com/en-us/iaas/api/#/en/certificates/20210224/
//   - DNS:                             https://docs.oracle.com/en-us/iaas/api/#/en/dns/20180115/
//   - Object Storage:      https://docs.oracle.com/en-us/iaas/api/#/en/objectstorage/20160918/
type AdminOCIService struct {
	vaultClient          vault.VaultsClient
	secretsClient        secrets.SecretsClient
	certManagementClient certificatesmanagement.CertificatesManagementClient
	certReadClient       certificates.CertificatesClient
	dnsClient            dns.DnsClient
	objectStorageClient  objectstorage.ObjectStorageClient
}

var (
	initializeVaultClient          = _initializeVaultClient
	initializeSecretsClient        = _initializeSecretsClient
	initializeCertManagementClient = _initializeCertManagementClient
	initializeCertReadClient       = _initializeCertReadClient
	initializeDnsClient            = _initializeDnsClient
	initializeObjectStorageClient  = _initializeObjectStorageClient
)

// Process-wide OCI client transport, built once at startup by
// InitSharedOCIClients and only read afterwards.
var (
	sharedAdminOCISvc *AdminOCIService
	sharedProxyClient *ocicloudproxyclient.Client
)

// InitSharedOCIClients eagerly builds the process-wide OCI clients at startup.
// It is idempotent after a successful init
func InitSharedOCIClients(ctx context.Context) error {
	log := util.GetLogger(ctx)
	if sharedAdminOCISvc != nil {
		log.Debug("Shared OCI clients already initialized; skipping")
		return nil
	}
	admin, err := newOCIClients(ctx)
	if err != nil {
		return err
	}
	if ociProxyEnabled() {
		proxyClient, err := newOgenProxyClient()
		if err != nil {
			return err
		}
		sharedProxyClient = proxyClient
	}
	sharedAdminOCISvc = admin
	log.Info("Shared OCI clients initialized",
		"proxy_enabled", ociProxyEnabled(),
		"proxy_base_url", strings.TrimSpace(env.OCIProxyBaseURL),
		"validate_only", env.OCIProxyValidateOnly)
	return nil
}

// getSharedAdminOCIService returns the process-wide OCI SDK client bundle.
func getSharedAdminOCIService(ctx context.Context) (*AdminOCIService, error) {
	if sharedAdminOCISvc == nil {
		if err := InitSharedOCIClients(ctx); err != nil {
			return nil, err
		}
	}
	return sharedAdminOCISvc, nil
}

// getSharedProxyClient returns the process-wide ogen OCI cloud-proxy client.
// // enabled (OCI_PROXY_BASE_URL set)
func getSharedProxyClient(ctx context.Context) (*ocicloudproxyclient.Client, error) {
	if sharedProxyClient == nil {
		if err := InitSharedOCIClients(ctx); err != nil {
			return nil, err
		}
	}
	if sharedProxyClient == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError,
			fmt.Errorf("OCI cloud-proxy client not initialized; OCI_PROXY_BASE_URL must be set"))
	}
	return sharedProxyClient, nil
}

// InitializeClients wires the OCI clients and the active wrapper.
//
// The OCI Go SDK clients (AdminOCIService) are initialised in EVERY mode because
// read (GET/LIST) operations always go directly to OCI via the SDK, even when
// the cloud proxy is enabled. The cloud proxy only fronts mutating operations:
//   - OCI_PROXY_BASE_URL empty (default) — SDK clients + ociSDKWrapper (all ops via SDK)
//   - OCI_PROXY_BASE_URL set             — SDK clients + ociProxyWrapper (reads via SDK, mutations via proxy)
func (ociService *OciServices) InitializeClients() error {
	if ociService.IsAdminClientInitialized() {
		return nil
	}

	adminService, err := getSharedAdminOCIService(ociService.Ctx)
	if err != nil {
		return err
	}
	ociService.AdminOCIService = adminService

	// The active backend (proxy vs SDK) and validateOnly are announced once at
	// startup by InitSharedOCIClients.
	if ociProxyEnabled() {
		wrapper, err := newOCIProxyWrapper(ociService)
		if err != nil {
			return err
		}
		ociService.wrapper = wrapper
		return nil
	}

	ociService.wrapper = &ociSDKWrapper{svc: ociService}
	return nil
}

// IsAdminClientInitialized returns true when In
// proxy mode the proxy wrapper must also be set.
func (ociService *OciServices) IsAdminClientInitialized() bool {
	if ociService.AdminOCIService == nil {
		return false
	}
	if ociProxyEnabled() {
		_, ok := ociService.wrapper.(*ociProxyWrapper)
		return ok
	}
	return true
}

// GetLogger returns the logger, creating one from context if necessary.
func (ociService *OciServices) GetLogger() logger.Logger {
	if ociService.Logger == nil {
		ociService.Logger = util.GetLogger(ociService.Ctx)
	}
	return ociService.Logger
}

// GetContext returns the context, falling back to context.Background().
func (ociService *OciServices) GetContext() context.Context {
	if ociService.Ctx == nil {
		ociService.Ctx = context.Background()
	}
	return ociService.Ctx
}

func newOCIClients(ctx context.Context) (*AdminOCIService, error) {
	log := util.GetLogger(ctx)

	vaultCl, err := initializeVaultClient()
	if err != nil {
		log.Errorf("Error initializing OCI Vault client: %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err)
	}

	secretsCl, err := initializeSecretsClient()
	if err != nil {
		log.Errorf("Error initializing OCI Secrets client: %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err)
	}

	certMgmtCl, err := initializeCertManagementClient()
	if err != nil {
		log.Errorf("Error initializing OCI Certificates Management client: %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err)
	}

	certReadCl, err := initializeCertReadClient()
	if err != nil {
		log.Errorf("Error initializing OCI Certificates read client: %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err)
	}

	dnsCl, err := initializeDnsClient()
	if err != nil {
		log.Errorf("Error initializing OCI DNS client: %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err)
	}

	// NOTE: In proxy mode (OCI_PROXY_BASE_URL set) objectStorageClient is unused.
	// so this SDK client is never called. It is still built unconditionally today (a
	// one-time cost now that the transport is cached); it could be built only when the
	// proxy is disabled if we want to trim it. Not doing that for now.
	objectStorageCl, err := initializeObjectStorageClient()
	if err != nil {
		log.Errorf("Error initializing OCI Object Storage client: %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err)
	}

	return &AdminOCIService{
		vaultClient:          *vaultCl,
		secretsClient:        *secretsCl,
		certManagementClient: *certMgmtCl,
		certReadClient:       *certReadCl,
		dnsClient:            *dnsCl,
		objectStorageClient:  *objectStorageCl,
	}, nil
}

// ociConfigProvider returns the appropriate OCI ConfigurationProvider based on
// the OCI_AUTH_TYPE environment variable:
//   - "instance_principal":    for workloads on OCI compute instances
//   - "oke_workload_identity": for pods on OKE with workload identity federation
//   - "config_file":           reads from ~/.oci/config (local dev outside containers)
//   - "env":                   reads credentials from OCI_TENANCY, OCI_USER,
//     OCI_REGION, OCI_FINGERPRINT, OCI_PRIVATE_KEY env vars
//     (best for Kubernetes where secrets are injected as env vars)
func ociConfigProvider() (common.ConfigurationProvider, error) {
	switch env.OCIAuthType {
	case "config_file":
		return common.DefaultConfigProvider(), nil
	case "instance_principal":
		return auth.InstancePrincipalConfigurationProvider()
	case "oke_workload_identity":
		return auth.OkeWorkloadIdentityConfigurationProvider()
	case "env":
		return ociConfigProviderFromEnv()
	default:
		return nil, fmt.Errorf("unsupported OCI_AUTH_TYPE %q; expected one of: instance_principal, oke_workload_identity, config_file, env", env.OCIAuthType)
	}
}

// ociConfigProviderFromEnv builds a ConfigurationProvider from individual
// environment variables. This is the preferred method for Kubernetes
// deployments where credentials are injected via ConfigMap or Secret.
//
// Required env vars: OCI_TENANCY, OCI_USER, OCI_REGION, OCI_FINGERPRINT, OCI_PRIVATE_KEY
// Optional env var:  OCI_PASSPHRASE (private key passphrase)
func ociConfigProviderFromEnv() (common.ConfigurationProvider, error) {
	const (
		envTenancy     = "OCI_TENANCY"
		envUser        = "OCI_USER"
		envRegion      = "OCI_REGION"
		envFingerprint = "OCI_FINGERPRINT"
		envPrivateKey  = "OCI_PRIVATE_KEY"
		envPassphrase  = "OCI_PASSPHRASE"
	)

	tenancyID := os.Getenv(envTenancy)
	userID := os.Getenv(envUser)
	region := os.Getenv(envRegion)
	fingerprint := os.Getenv(envFingerprint)
	privateKey := os.Getenv(envPrivateKey)
	passphrase := os.Getenv(envPassphrase)

	var missing []string
	for _, kv := range []struct{ name, val string }{
		{envTenancy, tenancyID},
		{envUser, userID},
		{envRegion, region},
		{envFingerprint, fingerprint},
		{envPrivateKey, privateKey},
	} {
		if kv.val == "" {
			missing = append(missing, kv.name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("OCI_AUTH_TYPE=env but missing required env vars: %s", strings.Join(missing, ", "))
	}

	var pp *string
	if passphrase != "" {
		pp = &passphrase
	}
	return common.NewRawConfigurationProvider(tenancyID, userID, region, fingerprint, privateKey, pp), nil
}

// _initializeVaultClient creates the Vault (write) client.
// Docs: https://docs.oracle.com/en-us/iaas/tools/go/65.108.3/vault/index.html
func _initializeVaultClient() (*vault.VaultsClient, error) {
	configProvider, err := ociConfigProvider()
	if err != nil {
		return nil, fmt.Errorf("ociConfigProvider: %w", err)
	}
	client, err := vault.NewVaultsClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, fmt.Errorf("vault.NewVaultsClientWithConfigurationProvider: %w", err)
	}
	return &client, nil
}

// _initializeSecretsClient creates the Secrets (read) client.
// Docs: https://docs.oracle.com/en-us/iaas/tools/go/65.108.3/secrets/index.html
func _initializeSecretsClient() (*secrets.SecretsClient, error) {
	configProvider, err := ociConfigProvider()
	if err != nil {
		return nil, fmt.Errorf("ociConfigProvider: %w", err)
	}
	client, err := secrets.NewSecretsClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, fmt.Errorf("secrets.NewSecretsClientWithConfigurationProvider: %w", err)
	}
	return &client, nil
}

// _initializeCertManagementClient creates the Certificates Management (write) client.
// Docs: https://docs.oracle.com/en-us/iaas/tools/go/latest/certificatesmanagement/index.html
func _initializeCertManagementClient() (*certificatesmanagement.CertificatesManagementClient, error) {
	configProvider, err := ociConfigProvider()
	if err != nil {
		return nil, fmt.Errorf("ociConfigProvider: %w", err)
	}
	client, err := certificatesmanagement.NewCertificatesManagementClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, fmt.Errorf("certificatesmanagement.NewCertificatesManagementClientWithConfigurationProvider: %w", err)
	}
	return &client, nil
}

// _initializeObjectStorageClient creates the Object Storage client used for
// pre-authenticated request (PAR) generation and namespace lookup.
// Docs: https://docs.oracle.com/en-us/iaas/tools/go/65.108.3/objectstorage/index.html
func _initializeObjectStorageClient() (*objectstorage.ObjectStorageClient, error) {
	configProvider, err := ociConfigProvider()
	if err != nil {
		return nil, fmt.Errorf("ociConfigProvider: %w", err)
	}
	client, err := objectstorage.NewObjectStorageClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, fmt.Errorf("objectstorage.NewObjectStorageClientWithConfigurationProvider: %w", err)
	}
	return &client, nil
}

// _initializeCertReadClient creates the Certificates (read) client used to retrieve
// certificate bundles (signed cert + chain + private key for internally-managed certs).
// Docs: https://docs.oracle.com/en-us/iaas/tools/go/latest/certificates/index.html
func _initializeCertReadClient() (*certificates.CertificatesClient, error) {
	configProvider, err := ociConfigProvider()
	if err != nil {
		return nil, fmt.Errorf("ociConfigProvider: %w", err)
	}
	client, err := certificates.NewCertificatesClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, fmt.Errorf("certificates.NewCertificatesClientWithConfigurationProvider: %w", err)
	}
	return &client, nil
}

// _initializeDnsClient creates the DNS client used to manage per-node A records
// for VSA cluster management LIFs. Records are written into the zone identified
// by env.OCIVsaDnsZoneOCID so the certificate's wildcard SAN (issued for
// `*.<deploymentName>.<env.VsaDeployedDnsName>`) covers them at TLS handshake.
// Docs: https://docs.oracle.com/en-us/iaas/tools/go/latest/dns/index.html
func _initializeDnsClient() (*dns.DnsClient, error) {
	configProvider, err := ociConfigProvider()
	if err != nil {
		return nil, fmt.Errorf("ociConfigProvider: %w", err)
	}
	client, err := dns.NewDnsClientWithConfigurationProvider(configProvider)
	if err != nil {
		return nil, fmt.Errorf("dns.NewDnsClientWithConfigurationProvider: %w", err)
	}
	return &client, nil
}

// NewAdminOCIService creates an AdminOCIService from the secret-management SDK
// clients only. The certificate clients are left zero-valued so this constructor
// stays backward-compatible with existing callers (e.g. ontap_provider_test.go).
// Callers that need certificate APIs should use NewAdminOCIServiceWithCertClients.
func NewAdminOCIService(vaultClient vault.VaultsClient, secretsClient secrets.SecretsClient) *AdminOCIService {
	return &AdminOCIService{vaultClient: vaultClient, secretsClient: secretsClient}
}

// NewAdminOCIServiceWithCertClients creates an AdminOCIService from pre-built
// SDK clients for both secret and certificate management. Use this constructor
// in tests that exercise the certificate_management.go code paths.
func NewAdminOCIServiceWithCertClients(
	vaultClient vault.VaultsClient,
	secretsClient secrets.SecretsClient,
	certManagementClient certificatesmanagement.CertificatesManagementClient,
	certReadClient certificates.CertificatesClient,
) *AdminOCIService {
	return &AdminOCIService{
		vaultClient:          vaultClient,
		secretsClient:        secretsClient,
		certManagementClient: certManagementClient,
		certReadClient:       certReadClient,
	}
}

// NewAdminOCIServiceWithAllClients creates an AdminOCIService from pre-built
// SDK clients for secret, certificate, and DNS management. Use this constructor
// in tests that exercise the cloud_dns.go code paths (or any test that needs
// all five SDK clients populated).
func NewAdminOCIServiceWithAllClients(
	vaultClient vault.VaultsClient,
	secretsClient secrets.SecretsClient,
	certManagementClient certificatesmanagement.CertificatesManagementClient,
	certReadClient certificates.CertificatesClient,
	dnsClient dns.DnsClient,
) *AdminOCIService {
	return &AdminOCIService{
		vaultClient:          vaultClient,
		secretsClient:        secretsClient,
		certManagementClient: certManagementClient,
		certReadClient:       certReadClient,
		dnsClient:            dnsClient,
	}
}

// ociResourceNotFoundCheck: it returns nil
// when the error is an HTTP 404 (resource not found), allowing callers to
// distinguish "not found" from real failures.
func ociResourceNotFoundCheck(err error) error {
	if serviceErr, ok := common.IsServiceError(err); ok && serviceErr.GetHTTPStatusCode() == http.StatusNotFound {
		return nil
	}
	return err
}
