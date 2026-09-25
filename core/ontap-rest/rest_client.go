package ontap_rest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-openapi/runtime"
	rtclient "github.com/go-openapi/runtime/client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	clientPriv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ottransport "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest/transport"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcptls "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/tlsconfig"
)

// RESTClient describes an ONTAP REST client
type RESTClient interface { // generate:mock
	Host() string
	Cluster() ClusterClient
	Cloud() CloudClient
	NVMe() NVMeClient
	SAN() SANClient
	NAS() NASClient
	Networking() NetworkingClient
	Security() SecurityClient
	Storage() StorageClient
	SVM() SVMClient
	Poll(jobUUID string) error
	Snapmirror() SnapmirrorClient
	NameServices() NameServicesClient
	Support() SupportClient
}

type OntapRestClient struct {
	params                    RESTClientParams
	httpRoundTripperTransport *http.Transport
	authenticatedTransport    http.RoundTripper
	cluster                   *clusterClient
	san                       *sanClient
	nas                       *nasClient
	nvme                      *nvmeClient
	security                  *securityClient
	networking                *networkingClient
	storage                   *storageClient
	svm                       *svmClient
	poller                    Poller
	snapmirror                *snapmirrorClient
	cloud                     *cloudClient
	nameServices              *nameServicesClient
	support                   *supportClient
}

// RESTClientParams describes the parameters for creating a new RESTClient
type RESTClientParams struct {
	Hosts                       map[string]string
	Host                        string
	Username                    string
	Password                    log.Secret
	Certificate                 *models.Certificate
	InsecureSkipVerify          bool
	CertificateBasedAuthEnabled bool
	FastConnection              bool // When true, bypasses retries and uses shorter timeout for test connections
	// FastHostSelect, when true (and the ONTAP_REST_FAST_HOST_SELECT deployment
	// flag is enabled), opts into two-phase host selection via
	// NewClientWithFastHostSelect: a cheap single-attempt probe (no retry budget)
	// selects a reachable host, then the returned client is built for that host
	// with the full retry policy. Ignored by NewClient.
	FastHostSelect bool
	// ServerName, when non-empty, switches the connect loop into "SNI override"
	// mode: dial Hosts by IP (keys) instead of by FQDN (values) and set
	// tls.Config.ServerName to this value so hostname verification matches
	// the cert's wildcard SAN without needing DNS resolution. Used by the
	// OCI SNI-override path. Empty for GCP and password-auth (legacy
	// behaviour preserved).
	ServerName string
	// Trace & Ctx fields are not serializable to JSON because of being an interface.
	// Hence, explicitly including JSON tags to nullify the fields. This is to avoid
	// JSON serialization error during temporal activity execution.
	Trace log.Logger      `json:"-"`
	Ctx   context.Context `json:"-"`
}

var (
	ontapRestLogVerbose = env.GetBool("ONTAP_REST_LOG_VERBOSE", false)
	TestConnection      = testConnection     // Allow overriding for testing purposes
	FastTestConnection  = fastTestConnection // Fast test connection with no retries
)

var (
	NewOntapRestClient    = NewClient
	GetAPICallCertificate = _getAPICallCertificate
)

const (
	MaxIdleConns        = 100
	IdleConnTimeout     = time.Second * 90
	TLSHandshakeTimeout = time.Second * 15
	CERTIFICATE         = "CERTIFICATE"
)

func NewClient(params RESTClientParams) (RESTClient, error) {
	hosts := candidateHosts(params)

	// Build each host's client WITH retries and verify connectivity through the
	// full retry budget, failing over to the next host on error.
	var lastErr error
	var rClient *OntapRestClient
	for _, host := range hosts {
		rc, err := buildClientForHost(params, host, true)
		if err != nil {
			return nil, err
		}
		if params.FastConnection {
			if terr := FastTestConnection(rc); terr != nil {
				lastErr = terr
				rClient = rc
				continue
			}
			return rc, nil
		}
		if terr := TestConnection(rc); terr != nil {
			lastErr = terr
			rClient = rc
			continue
		}
		return rc, nil
	}
	if lastErr != nil {
		params.Trace.Errorf("Failed to connect to any ONTAP REST API host: %v", lastErr)
	}
	params.Trace.Warnf("returning client with last tried host")
	return rClient, lastErr
}

// candidateHosts returns the list of hosts NewClient should try. SNI override mode
// (ServerName set) dials the IP keys of params.Hosts; legacy mode dials the FQDN
// values. Go randomizes map iteration order, and that randomization is relied
// upon here so calls spread across healthy management LIFs instead of always
// hammering one; callers must not assume a stable ordering.
func candidateHosts(params RESTClientParams) []string {
	var hosts []string
	if params.ServerName != "" {
		for ip := range params.Hosts {
			hosts = append(hosts, ip)
		}
	} else {
		for _, h := range params.Hosts {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// buildClientForHost constructs an *OntapRestClient bound to a single host. When
// withRetry is false the RetryTransport layer is omitted, yielding a true
// single-attempt client suitable for host-selection probes (Phase 1). When true,
// the full retry policy is applied for real operations (Phase 2 and the legacy
// path).
func buildClientForHost(params RESTClientParams, host string, withRetry bool) (*OntapRestClient, error) {
	tryParams := params
	tryParams.Host = host

	rt := newRuntimeClient(tryParams)
	var httpRoundTripperTransport *http.Transport
	if params.CertificateBasedAuthEnabled {
		rootCA, clientCert, err := GetAPICallCertificate(params)
		if err != nil {
			return nil, err
		}
		tlsCfg := vcptls.NewFIPSClientConfig()
		tlsCfg.InsecureSkipVerify = params.InsecureSkipVerify
		tlsCfg.RootCAs = rootCA
		tlsCfg.Certificates = []tls.Certificate{clientCert}
		if params.ServerName != "" {
			tlsCfg.ServerName = params.ServerName
		}
		httpRoundTripperTransport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          MaxIdleConns,
			IdleConnTimeout:       IdleConnTimeout,
			TLSHandshakeTimeout:   TLSHandshakeTimeout,
			ExpectContinueTimeout: time.Second,
			TLSClientConfig:       tlsCfg,
		}
	} else {
		tlsCfg := vcptls.NewFIPSClientConfig()
		tlsCfg.InsecureSkipVerify = params.InsecureSkipVerify
		if params.ServerName != "" {
			tlsCfg.ServerName = params.ServerName
		}
		httpRoundTripperTransport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          MaxIdleConns,
			IdleConnTimeout:       IdleConnTimeout,
			TLSHandshakeTimeout:   TLSHandshakeTimeout,
			ExpectContinueTimeout: time.Second,
			TLSClientConfig:       tlsCfg,
		}
	}

	var rc *OntapRestClient
	rt.Transport = httpRoundTripperTransport
	rt.Transport = ottransport.NewLoggingRoundTripper(tryParams.Trace, ontapRestLogVerbose, params.CertificateBasedAuthEnabled, rt.Transport)
	rt.Transport = ottransport.NewPaginationRoundTripper(rt.Transport)
	authenticatedTransport := ottransport.NewAuthenticationRoundTripper(
		rt.Transport, resolveRESTAuthUsername(tryParams.Username), tryParams.Password, params.CertificateBasedAuthEnabled)
	rt.Transport = authenticatedTransport

	clientTransport := withRetryTransport(tryParams.Trace, rt, withRetry)
	idempotentTransport := ottransport.NewIdempotentTransport(clientTransport, func(operation *runtime.ClientOperation) (interface{}, error) {
		return resolveRESTClientRouterConflict(tryParams.Trace, rc, operation)
	})
	api := client.New(idempotentTransport, nil)
	apiPriv := clientPriv.New(idempotentTransport, nil)
	p := &poller{api: api.Cluster, logger: tryParams.Trace, clientParams: tryParams}
	rc = &OntapRestClient{
		httpRoundTripperTransport: httpRoundTripperTransport,
		authenticatedTransport:    authenticatedTransport,
		params:                    tryParams,
		cluster:                   &clusterClient{api: api.Cluster, apiPriv: &apiPriv.Operations},
		cloud:                     &cloudClient{api: api.Cloud},
		svm:                       &svmClient{api: api.Svm, apiPriv: &apiPriv.Operations, poller: p},
		networking:                &networkingClient{api: api.Networking, apiPriv: &apiPriv.Operations},
		storage:                   &storageClient{api: api.Storage},
		san:                       &sanClient{api: api.San, poller: p},
		nvme:                      &nvmeClient{api: api.NvMe},
		nas:                       &nasClient{api: api.Nas, apiPriv: &apiPriv.Operations, poller: p, Trace: tryParams.Trace},
		snapmirror:                &snapmirrorClient{api: api.Snapmirror, apiPriv: apiPriv.Snapmirror},
		poller:                    p,
		security: &securityClient{
			api:     &api.Security,
			apiPriv: &apiPriv.Operations,
		},
		nameServices: &nameServicesClient{api: &api.NameServices},
		support:      &supportClient{api: &api.Support},
	}
	return rc, nil
}

// withRetryTransport returns rt wrapped in the retry layer when withRetry is
// true, or rt unchanged otherwise. Phase 1 probes pass withRetry=false so an
// unreachable host fails within the probe deadline instead of consuming the full
// retry budget; Phase 2 and the legacy path pass true for real operations. It is
// split out from buildClientForHost so the retry-layer decision is unit-testable
// without inspecting a fully built client.
func withRetryTransport(trace log.Logger, rt runtime.ClientTransport, withRetry bool) runtime.ClientTransport {
	if withRetry {
		return ottransport.NewRetryTransport(trace, rt)
	}
	return rt
}

// testConnection tries a simple API call to verify connectivity
func testConnection(rc *OntapRestClient) error {
	// Try to get cluster info (adjust as needed for your API)
	if rc.cluster == nil || rc.cluster.api == nil {
		return fmt.Errorf("cluster client not initialized")
	}
	_, err := rc.cluster.api.ClusterGet(cluster.NewClusterGetParams().WithFields([]string{"version"}), nil)
	return err
}

// fastTestConnection tries a simple API call with a short timeout and no retries
func fastTestConnection(rc *OntapRestClient) error {
	if rc.cluster == nil || rc.cluster.api == nil {
		return fmt.Errorf("cluster client not initialized")
	}

	// Create a context with 3-second timeout
	ctx, cancel := context.WithTimeout(rc.params.Ctx, 3*time.Second)
	defer cancel()

	// Try to get cluster info with timeout
	params := cluster.NewClusterGetParams().WithFields([]string{"version"}).WithContext(ctx)
	_, err := rc.cluster.api.ClusterGet(params, nil)
	return err
}

func resolveRESTAuthUsername(username string) string {
	if strings.TrimSpace(username) != "" {
		return strings.TrimSpace(username)
	}
	return env.Admin
}

func newRuntimeClient(params RESTClientParams) *rtclient.Runtime {
	rt := rtclient.New(params.Host, client.DefaultBasePath, client.DefaultSchemes)
	rt.Producers["application/hal+json"] = runtime.JSONProducer()
	rt.Consumers["application/hal+json"] = runtime.JSONConsumer()
	rt.Consumers["text/html"] = runtime.JSONConsumer()
	return rt
}

// Host returns the hostname of the REST API
func (rc *OntapRestClient) Host() string {
	return rc.params.Host
}

// Cluster returns a cluster client
func (rc *OntapRestClient) Cluster() ClusterClient {
	return rc.cluster
}

// Networking returns a networking client
func (rc *OntapRestClient) Networking() NetworkingClient {
	return rc.networking
}

// Storage returns a storage client
func (rc *OntapRestClient) Storage() StorageClient {
	return rc.storage
}

// SVM returns an SVM client
func (rc *OntapRestClient) SVM() SVMClient {
	return rc.svm
}

// Poll polls the job with the given UUID
func (rc *OntapRestClient) Poll(jobUUID string) error {
	return rc.poller.Poll(jobUUID)
}

// Security returns a security client
func (rc *OntapRestClient) Security() SecurityClient {
	return rc.security
}

// SAN returns a SAN client
func (rc *OntapRestClient) SAN() SANClient {
	return rc.san
}

// NVMe returns an NVMe client
func (rc *OntapRestClient) NVMe() NVMeClient {
	return rc.nvme
}

// NAS returns a NAS client
func (rc *OntapRestClient) NAS() NASClient {
	return rc.nas
}

// Snapmirror returns a Snapmirror client
func (rc *OntapRestClient) Snapmirror() SnapmirrorClient {
	return rc.snapmirror
}

// Cloud returns a Cloud client
func (rc *OntapRestClient) Cloud() CloudClient {
	return rc.cloud
}

// NameServicesClient returns a Name Services client
func (rc *OntapRestClient) NameServices() NameServicesClient {
	return rc.nameServices
}

// Support returns a Support client
func (rc *OntapRestClient) Support() SupportClient {
	return rc.support
}

// getAPICallCertificate retrieves the certificate and root CA for API calls
func _getAPICallCertificate(params RESTClientParams) (*x509.CertPool, tls.Certificate, error) {
	if params.Certificate != nil && params.Certificate.InterMediateCertificates != nil && len(params.Certificate.InterMediateCertificates) > 0 && params.Certificate.SignedCertificate != "" && params.Certificate.PrivateKey != "" {
		rootCA, err := utils.ParsePEMCertificate(params.Certificate.InterMediateCertificates, CERTIFICATE)
		if err != nil {
			params.Trace.Errorf("error parsing root CA certificate: %v", err)
			return nil, tls.Certificate{}, err
		}
		signedCertPem := []byte(params.Certificate.SignedCertificate)
		privateKeyPem := []byte(params.Certificate.PrivateKey)

		// Load client certificate and key
		clientCert, err := tls.X509KeyPair(signedCertPem, privateKeyPem)
		if err != nil {
			params.Trace.Errorf("error loading client certificate and key: %v", err)
			return nil, tls.Certificate{}, err
		}
		return rootCA, clientCert, nil
	}
	return nil, tls.Certificate{}, fmt.Errorf("invalid certificate parameters: ensure SignedCertificate, PrivateKey, and InterMediateCertificates are set correctly")
}
