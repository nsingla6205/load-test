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
	var lastErr error
	var rClient *OntapRestClient

	// Decide what to iterate over. Default (legacy) behaviour iterates
	// the FQDN values from params.Hosts and lets Go derive SNI from the
	// URL host. SNI-override mode (params.ServerName != "") iterates the
	// IP keys instead and forces tls.Config.ServerName to the supplied
	// SNI string so we can keep full mTLS without DNS resolution.
	type hostAttempt struct{ host string }
	var attempts []hostAttempt
	if params.ServerName != "" {
		for ip := range params.Hosts {
			attempts = append(attempts, hostAttempt{host: ip})
		}
	} else {
		for _, h := range params.Hosts {
			attempts = append(attempts, hostAttempt{host: h})
		}
	}

	for _, a := range attempts {
		tryParams := params
		// Set Host for this attempt
		tryParams.Host = a.host

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
		retryTransport := ottransport.NewRetryTransport(tryParams.Trace, rt)
		idempotentTransport := ottransport.NewIdempotentTransport(retryTransport, func(operation *runtime.ClientOperation) (interface{}, error) {
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
		if params.FastConnection {
			if err := FastTestConnection(rc); err == nil {
				return rc, nil
			} else {
				lastErr = err
				rClient = rc
				continue
			}
		} else {
			if err := TestConnection(rc); err == nil {
				return rc, nil
			} else {
				lastErr = err
				rClient = rc
				continue
			}
		}
	}
	if lastErr != nil {
		params.Trace.Errorf("Failed to connect to any ONTAP REST API host: %v", lastErr)
	}
	params.Trace.Warnf("returning client with last tried host")
	return rClient, lastErr
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
