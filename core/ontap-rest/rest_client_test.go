package ontap_rest

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/go-openapi/runtime"
	rtclient "github.com/go-openapi/runtime/client"
	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	operations "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest/transport"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcptls "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/tlsconfig"
)

type mockTransport struct {
	response interface{}
	err      error
}

func (mock *mockTransport) Submit(*runtime.ClientOperation) (interface{}, error) {
	return mock.response, mock.err
}

func TestNewClient(t *testing.T) {
	hostMap := map[string]string{}
	hostMap["localhost"] = "10.0.0.1"
	params := RESTClientParams{
		Hosts:                       hostMap,
		Host:                        "10.0.0.1",
		Password:                    log.Secret("secret"),
		InsecureSkipVerify:          true,
		Trace:                       log.NewLogger().(*log.Slogger),
		CertificateBasedAuthEnabled: false,
	}
	defer func() {
		TestConnection = testConnection // Reset to original after test
	}()
	TestConnection = func(params *OntapRestClient) error {
		return nil
	}

	f := func(rcl RESTClient) {
		if c, ok := rcl.(*OntapRestClient); !ok {
			t.Error("Client type does not match expected one")
		} else {
			assert.NotNil(t, c.httpRoundTripperTransport.Proxy)
			assert.Equal(t, 100, c.httpRoundTripperTransport.MaxIdleConns)
			assert.Equal(t, IdleConnTimeout, c.httpRoundTripperTransport.IdleConnTimeout)
			assert.Equal(t, TLSHandshakeTimeout, c.httpRoundTripperTransport.TLSHandshakeTimeout)
			assert.Equal(t, time.Second*1, c.httpRoundTripperTransport.ExpectContinueTimeout)
			assert.Equal(t, uint16(tls.VersionTLS12), c.httpRoundTripperTransport.TLSClientConfig.MinVersion)
			assert.Equal(t, vcptls.FIPSTLS12CipherSuites(), c.httpRoundTripperTransport.TLSClientConfig.CipherSuites)
			assert.True(t, c.httpRoundTripperTransport.TLSClientConfig.InsecureSkipVerify)
			assert.Equal(t, params, c.params)

			assert.NotNil(t, c.cluster.api)
			assert.NotNil(t, c.networking.api)
			assert.NotNil(t, (*c.networking.apiPriv).(*operations.Client))
			assert.NotNil(t, c.nas.api)
			assert.NotNil(t, c.nas.apiPriv)

			fv := reflect.ValueOf(c.cluster.api).Elem().FieldByName("transport")
			it := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*transport.IdempotentTransport)

			fv = reflect.ValueOf(it).Elem().FieldByName("transport")
			rt := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*transport.RetryTransport)

			fv = reflect.ValueOf(rt).Elem().FieldByName("transport")
			rtc := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*rtclient.Runtime)
			assert.NotNil(t, rtc.Producers["application/hal+json"])
			assert.NotNil(t, rtc.Consumers["application/hal+json"])
			assert.NotNil(t, rtc.Consumers["text/html"])

			_, ok := rtc.Transport.(*transport.AuthenticationRoundTripper)
			if !ok {
				t.Errorf("Expected *transport.AuthenticationRoundTripper but got: %v", rtc.Transport)
			}
		}
	}

	cl, err := NewClient(params)
	assert.NoError(t, err)
	f(cl)
}

func TestNewClient_with_lastErr(t *testing.T) {
	hostMap := map[string]string{}
	hostMap["localhost"] = "10.0.0.1"
	params := RESTClientParams{
		Hosts:              hostMap,
		Host:               "10.0.0.1",
		Password:           log.Secret("secret"),
		InsecureSkipVerify: true,
		Trace:              log.NewLogger().(*log.Slogger),
	}
	defer func() {
		TestConnection = testConnection // Reset to original after test
	}()
	TestConnection = func(params *OntapRestClient) error {
		return errors.New("test error")
	}

	f := func(rcl RESTClient) {
		if c, ok := rcl.(*OntapRestClient); !ok {
			t.Error("Client type does not match expected one")
		} else {
			assert.NotNil(t, c.httpRoundTripperTransport.Proxy)
			assert.Equal(t, 100, c.httpRoundTripperTransport.MaxIdleConns)
			assert.Equal(t, IdleConnTimeout, c.httpRoundTripperTransport.IdleConnTimeout)
			assert.Equal(t, TLSHandshakeTimeout, c.httpRoundTripperTransport.TLSHandshakeTimeout)
			assert.Equal(t, time.Second*1, c.httpRoundTripperTransport.ExpectContinueTimeout)
			assert.Equal(t, params, c.params)

			assert.NotNil(t, c.cluster.api)
			assert.NotNil(t, c.networking.api)
			assert.NotNil(t, (*c.networking.apiPriv).(*operations.Client))
			assert.NotNil(t, c.nas.api)
			assert.NotNil(t, c.nas.apiPriv)

			fv := reflect.ValueOf(c.cluster.api).Elem().FieldByName("transport")
			it := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*transport.IdempotentTransport)

			fv = reflect.ValueOf(it).Elem().FieldByName("transport")
			rt := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*transport.RetryTransport)

			fv = reflect.ValueOf(rt).Elem().FieldByName("transport")
			rtc := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*rtclient.Runtime)
			assert.NotNil(t, rtc.Producers["application/hal+json"])
			assert.NotNil(t, rtc.Consumers["application/hal+json"])
			assert.NotNil(t, rtc.Consumers["text/html"])

			_, ok := rtc.Transport.(*transport.AuthenticationRoundTripper)
			if !ok {
				t.Errorf("Expected *transport.AuthenticationRoundTripper but got: %v", rtc.Transport)
			}
		}
	}

	cl, err := NewClient(params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "test error")
	f(cl)
}

func TestNewClientWithCert(t *testing.T) {
	hostMap := map[string]string{}
	hostMap["10.0.0.1"] = "localhost"
	params := RESTClientParams{
		Hosts: hostMap,
		Host:  "localhost",
		Certificate: &models.Certificate{
			SignedCertificate:        "signedCert",
			PrivateKey:               "privateKey",
			InterMediateCertificates: []string{"intermediateCert", "rootCaCert"},
			CommonName:               "commonName",
		},
		InsecureSkipVerify:          false,
		Trace:                       log.NewLogger().(*log.Slogger),
		CertificateBasedAuthEnabled: true,
	}

	getAPICallCertificate := GetAPICallCertificate

	defer func() {
		TestConnection = testConnection               // Reset to original after test
		GetAPICallCertificate = getAPICallCertificate // Reset to original after test
	}()
	TestConnection = func(params *OntapRestClient) error {
		return nil
	}

	GetAPICallCertificate = func(params RESTClientParams) (*x509.CertPool, tls.Certificate, error) {
		return x509.NewCertPool(), tls.Certificate{}, nil
	}

	f := func(rcl RESTClient) {
		if c, ok := rcl.(*OntapRestClient); !ok {
			t.Error("Client type does not match expected one")
		} else {
			assert.NotNil(t, c.httpRoundTripperTransport.Proxy)
			assert.Equal(t, 100, c.httpRoundTripperTransport.MaxIdleConns)
			assert.Equal(t, IdleConnTimeout, c.httpRoundTripperTransport.IdleConnTimeout)
			assert.Equal(t, TLSHandshakeTimeout, c.httpRoundTripperTransport.TLSHandshakeTimeout)
			assert.Equal(t, time.Second*1, c.httpRoundTripperTransport.ExpectContinueTimeout)
			assert.Equal(t, params, c.params)

			assert.NotNil(t, c.cluster.api)
			assert.NotNil(t, c.networking.api)
			assert.NotNil(t, (*c.networking.apiPriv).(*operations.Client))
			assert.NotNil(t, c.nas.api)
			assert.NotNil(t, c.nas.apiPriv)

			fv := reflect.ValueOf(c.cluster.api).Elem().FieldByName("transport")
			it := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*transport.IdempotentTransport)

			fv = reflect.ValueOf(it).Elem().FieldByName("transport")
			rt := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*transport.RetryTransport)

			fv = reflect.ValueOf(rt).Elem().FieldByName("transport")
			rtc := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*rtclient.Runtime)
			assert.NotNil(t, rtc.Producers["application/hal+json"])
			assert.NotNil(t, rtc.Consumers["application/hal+json"])
			assert.NotNil(t, rtc.Consumers["text/html"])

			_, ok := rtc.Transport.(*transport.AuthenticationRoundTripper)
			if !ok {
				t.Errorf("Expected *transport.AuthenticationRoundTripper but got: %v", rtc.Transport)
			}
		}
	}

	cl, err := NewClient(params)
	assert.NoError(t, err)
	f(cl)
}

func TestNewClientWithCert_GetAPICallCertificateFails(t *testing.T) {
	hostMap := map[string]string{}
	hostMap["10.0.0.1"] = "localhost"
	params := RESTClientParams{
		Hosts: hostMap,
		Host:  "localhost",
		Certificate: &models.Certificate{
			SignedCertificate:        "signedCert",
			PrivateKey:               "privateKey",
			InterMediateCertificates: []string{"intermediateCert", "rootCaCert"},
			CommonName:               "commonName",
		},
		InsecureSkipVerify:          false,
		Trace:                       log.NewLogger().(*log.Slogger),
		CertificateBasedAuthEnabled: true,
	}

	getAPICallCertificate := GetAPICallCertificate

	defer func() {
		TestConnection = testConnection               // Reset to original after test
		GetAPICallCertificate = getAPICallCertificate // Reset to original after test
	}()
	TestConnection = func(params *OntapRestClient) error {
		return nil
	}

	GetAPICallCertificate = func(params RESTClientParams) (*x509.CertPool, tls.Certificate, error) {
		return x509.NewCertPool(), tls.Certificate{}, errors.New("test error")
	}

	f := func(rcl RESTClient) {
		if c, ok := rcl.(*OntapRestClient); !ok {
			assert.Error(t, errors.New("test error"))
		} else {
			assert.NotNil(t, c.httpRoundTripperTransport.Proxy)
			assert.Equal(t, 100, c.httpRoundTripperTransport.MaxIdleConns)
			assert.Equal(t, IdleConnTimeout, c.httpRoundTripperTransport.IdleConnTimeout)
			assert.Equal(t, TLSHandshakeTimeout, c.httpRoundTripperTransport.TLSHandshakeTimeout)
			assert.Equal(t, time.Second*1, c.httpRoundTripperTransport.ExpectContinueTimeout)
			assert.Equal(t, params, c.params)

			assert.NotNil(t, c.cluster.api)
			assert.NotNil(t, c.networking.api)
			assert.NotNil(t, (*c.networking.apiPriv).(*operations.Client))
			assert.NotNil(t, c.nas.api)
			assert.NotNil(t, c.nas.apiPriv)

			fv := reflect.ValueOf(c.cluster.api).Elem().FieldByName("transport")
			it := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*transport.IdempotentTransport)

			fv = reflect.ValueOf(it).Elem().FieldByName("transport")
			rt := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*transport.RetryTransport)

			fv = reflect.ValueOf(rt).Elem().FieldByName("transport")
			rtc := reflect.NewAt(fv.Type(), unsafe.Pointer(fv.UnsafeAddr())).Elem().Interface().(*rtclient.Runtime)
			assert.NotNil(t, rtc.Producers["application/hal+json"])
			assert.NotNil(t, rtc.Consumers["application/hal+json"])
			assert.NotNil(t, rtc.Consumers["text/html"])

			_, ok := rtc.Transport.(*transport.AuthenticationRoundTripper)
			if !ok {
				t.Errorf("Expected *transport.AuthenticationRoundTripper but got: %v", rtc.Transport)
			}
		}
	}

	cl, err := NewClient(params)
	assert.Error(t, err)
	assert.Equal(t, err.Error(), "test error")
	f(cl)
}

func TestRESTClient_Host(t *testing.T) {
	client := &OntapRestClient{params: RESTClientParams{Host: "host"}}
	assert.Equal(t, "host", client.Host())
}

func TestRESTClient_Cluster(t *testing.T) {
	cc := &clusterClient{}
	client := &OntapRestClient{cluster: cc}
	assert.Equal(t, cc, client.Cluster())
}

func TestRESTClient_Networking(t *testing.T) {
	nc := &networkingClient{}
	client := &OntapRestClient{networking: nc}
	assert.Equal(t, nc, client.Networking())
}

func TestRESTClient_Security(t *testing.T) {
	sc := &securityClient{}
	client := &OntapRestClient{security: sc}
	assert.Equal(t, sc, client.Security())
}

func TestRESTClient_Storage(t *testing.T) {
	sc := &storageClient{}
	client := &OntapRestClient{storage: sc}
	assert.Equal(t, sc, client.Storage())
}

func TestRESTClient_SVM(t *testing.T) {
	svmc := &svmClient{}
	client := &OntapRestClient{svm: svmc}
	assert.Equal(t, svmc, client.SVM())
}

func TestRESTClient_SAN(t *testing.T) {
	sanc := &sanClient{}
	client := &OntapRestClient{san: sanc}
	assert.Equal(t, sanc, client.SAN())
}

func TestRESTClient_NVMe(t *testing.T) {
	nvmec := &nvmeClient{}
	client := &OntapRestClient{nvme: nvmec}
	assert.Equal(t, nvmec, client.NVMe())
}

func TestRESTClient_Support(t *testing.T) {
	sc := &supportClient{}
	client := &OntapRestClient{support: sc}
	assert.Equal(t, sc, client.Support())
}

func TestRESTClient_NAS(t *testing.T) {
	nasc := &nasClient{}
	client := &OntapRestClient{nas: nasc}
	assert.Equal(t, nasc, client.NAS())
}

func TestRESTClient_Snapmirror(t *testing.T) {
	snap := &snapmirrorClient{}
	client := &OntapRestClient{snapmirror: snap}
	assert.Equal(t, snap, client.Snapmirror())
}

func TestRESTClient_Poll(t *testing.T) {
	mockPoller := new(MockPoller)
	client := &OntapRestClient{poller: mockPoller}
	mockPoller.On("Poll", "job-uuid").Return(nil)

	err := client.Poll("job-uuid")
	assert.NoError(t, err)
	mockPoller.AssertCalled(t, "Poll", "job-uuid")
}

func TestTestConnection(t *testing.T) {
	t.Run("cluster client not initialized", func(tt *testing.T) {
		rc := &OntapRestClient{cluster: nil}
		err := testConnection(rc)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "cluster client not initialized")
	})

	t.Run("cluster.api not initialized", func(tt *testing.T) {
		rc := &OntapRestClient{cluster: &clusterClient{api: nil}}
		err := testConnection(rc)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "cluster client not initialized")
	})

	t.Run("ClusterGet returns error", func(tt *testing.T) {
		mockAPI := cluster.NewMockClientService(tt)
		params := cluster.NewClusterGetParams().WithFields([]string{"version"})
		rc := &OntapRestClient{cluster: &clusterClient{api: mockAPI}}
		done := make(chan struct{})
		go func() {
			err := testConnection(rc)
			assert.Error(tt, err)
			assert.EqualError(tt, err, "api error")
			close(done)
		}()
		mockAPI.AssertClusterGet(params, nil, nil, nil, errors.New("api error"))
		<-done
	})

	t.Run("ClusterGet returns success", func(tt *testing.T) {
		mockAPI := cluster.NewMockClientService(tt)
		params := cluster.NewClusterGetParams().WithFields([]string{"version"})
		rc := &OntapRestClient{cluster: &clusterClient{api: mockAPI}}
		done := make(chan struct{})
		go func() {
			err := testConnection(rc)
			assert.NoError(tt, err)
			close(done)
		}()
		mockAPI.AssertClusterGet(params, nil, nil, &cluster.ClusterGetOK{}, nil)
		<-done
	})
}

func Test_getAPICallCertificate(t *testing.T) {
	var parsePEMCertificateOrig = utils.ParsePEMCertificate
	defer func() { utils.ParsePEMCertificate = parsePEMCertificateOrig }()

	params := RESTClientParams{
		Certificate: &models.Certificate{
			SignedCertificate:        "signedCert",
			PrivateKey:               "privateKey",
			InterMediateCertificates: []string{"intermediateCert"},
			CommonName:               "commonName",
		},
		Trace: log.NewLogger().(*log.Slogger),
	}

	validParam := RESTClientParams{
		Certificate: &models.Certificate{
			SignedCertificate:        "-----BEGIN CERTIFICATE-----\nMIIElTCCAv2gAwIBAgIUf/gBBHqKQY3nzZ6vAAK836RR1Y0wDQYJKoZIhvcNAQEL\nBQAwWjELMAkGA1UEBhMCQVUxEzARBgNVBAgMClNvbWUtU3RhdGUxITAfBgNVBAoM\nGEludGVybmV0IFdpZGdpdHMgUHR5IEx0ZDETMBEGA1UEAwwKY29tbW9uTmFtZTAe\nFw0yNTA3MDcxMTE4NTVaFw0yNjA3MDcxMTE4NTVaMFoxCzAJBgNVBAYTAkFVMRMw\nEQYDVQQIDApTb21lLVN0YXRlMSEwHwYDVQQKDBhJbnRlcm5ldCBXaWRnaXRzIFB0\neSBMdGQxEzARBgNVBAMMCmNvbW1vbk5hbWUwggGiMA0GCSqGSIb3DQEBAQUAA4IB\njwAwggGKAoIBgQCuARBS7IwSa/rOivjHPi7JS48Qq+ytlfDd52s77+42dWOmcvK9\nXOdTEndL2CPqGYSNvdloSv37xqEDeTTi1mOaHsAydXBuQyEJ+DIWltJ3EWUQkLVb\nHQoLPhdVHLfgkP+T217SvuSz9VQgOYLSOU/pTPxOSaiD0DhcvytqpXJaTbN0jX7z\nxFn6NB0RpWQ10caPukLPvLBc4TUUdnP2FyqetnNqhcYTyhqW4YnE7xITIn7N/Ytm\nbsXpLVf6HcvAz3oJlaqvrKMerWFlu3RJlqBqMUHxBCT8EM0mubhKWlwdnAXJ1CkF\nuruoStyRBjbor7EbSHyv0UCnopOcCwAruxaZJ+D/ubJCp6RJITuYBZjjdres6jkI\n8tTcPdE7gXn2tYTCv0ohVB6E5r2FuGN0AT5emHtG35Tqf1dp1GisylTW3MwD+wnI\nkSrvbTm8TblCMYfBRfiMlJ7a/WdmtYsmm+lbn09EjB+95HJZBl/m/1qVx7MGyN0x\ne/eakTqNROVHyP8CAwEAAaNTMFEwHQYDVR0OBBYEFEv+6pMBj+bsQ9a8USMXONRa\nnuqCMB8GA1UdIwQYMBaAFEv+6pMBj+bsQ9a8USMXONRanuqCMA8GA1UdEwEB/wQF\nMAMBAf8wDQYJKoZIhvcNAQELBQADggGBAGNwydFCDRZQohYGE4b1yeqsnTxkF7M/\nFAeU7eiSAU5m3HGhauR9mf/b9NABQARyYCEtbOEwwIjvZGhGUWAfNeihNUKwn9oo\nzDOCQ9/7F8FFLjZWw9L4tdbpldWzcCh9UvlT3f2821l7qBs3RtX6tUni/Yt4fC0W\nW8r7sqnVdFbm97DLYMDcUIQCgLqswlv1jt+6u74fypYY77HKkMXIlqq2qo7sAr7p\niVQ87W7fpobGN7YA2xoI9XV0Jld5aSu2/MdIhVMtSe1HzsxtNqWnw0h6eanooBHw\nkpWXXOZ9vi1GklPUADUMyWAdeUVz52vUkkIPYXB9kygsfoGMHDj6rXu8DKuKGb/u\n/7k2vg+Xh4i9P1qemzn+sQXsmw0JxmGK0TbVsA9lV+/UNRlBBr3ExKJrfXKI/Mmq\nlynCzRoFM1VenLgIvvcQ6lJKFNNQp4GBuAPdbKPW2hk0HrqfNvVIwbsiY47m9Aqn\nBoq1EHq7lck9tZl0DasAEe5DBMa6EYTK0A==\n-----END CERTIFICATE-----",
			PrivateKey:               "-----BEGIN PRIVATE KEY-----\n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\noajITPMsTs0keLUerqNhaGUv\n-----END PRIVATE KEY-----",
			InterMediateCertificates: []string{"-----BEGIN CERTIFICATE-----\nMIIElTCCAv2gAwIBAgIUf/gBBHqKQY3nzZ6vAAK836RR1Y0wDQYJKoZIhvcNAQEL\nBQAwWjELMAkGA1UEBhMCQVUxEzARBgNVBAgMClNvbWUtU3RhdGUxITAfBgNVBAoM\nGEludGVybmV0IFdpZGdpdHMgUHR5IEx0ZDETMBEGA1UEAwwKY29tbW9uTmFtZTAe\nFw0yNTA3MDcxMTE4NTVaFw0yNjA3MDcxMTE4NTVaMFoxCzAJBgNVBAYTAkFVMRMw\nEQYDVQQIDApTb21lLVN0YXRlMSEwHwYDVQQKDBhJbnRlcm5ldCBXaWRnaXRzIFB0\neSBMdGQxEzARBgNVBAMMCmNvbW1vbk5hbWUwggGiMA0GCSqGSIb3DQEBAQUAA4IB\njwAwggGKAoIBgQCuARBS7IwSa/rOivjHPi7JS48Qq+ytlfDd52s77+42dWOmcvK9\nXOdTEndL2CPqGYSNvdloSv37xqEDeTTi1mOaHsAydXBuQyEJ+DIWltJ3EWUQkLVb\nHQoLPhdVHLfgkP+T217SvuSz9VQgOYLSOU/pTPxOSaiD0DhcvytqpXJaTbN0jX7z\nxFn6NB0RpWQ10caPukLPvLBc4TUUdnP2FyqetnNqhcYTyhqW4YnE7xITIn7N/Ytm\nbsXpLVf6HcvAz3oJlaqvrKMerWFlu3RJlqBqMUHxBCT8EM0mubhKWlwdnAXJ1CkF\nuruoStyRBjbor7EbSHyv0UCnopOcCwAruxaZJ+D/ubJCp6RJITuYBZjjdres6jkI\n8tTcPdE7gXn2tYTCv0ohVB6E5r2FuGN0AT5emHtG35Tqf1dp1GisylTW3MwD+wnI\nkSrvbTm8TblCMYfBRfiMlJ7a/WdmtYsmm+lbn09EjB+95HJZBl/m/1qVx7MGyN0x\ne/eakTqNROVHyP8CAwEAAaNTMFEwHQYDVR0OBBYEFEv+6pMBj+bsQ9a8USMXONRa\nnuqCMB8GA1UdIwQYMBaAFEv+6pMBj+bsQ9a8USMXONRanuqCMA8GA1UdEwEB/wQF\nMAMBAf8wDQYJKoZIhvcNAQELBQADggGBAGNwydFCDRZQohYGE4b1yeqsnTxkF7M/\nFAeU7eiSAU5m3HGhauR9mf/b9NABQARyYCEtbOEwwIjvZGhGUWAfNeihNUKwn9oo\nzDOCQ9/7F8FFLjZWw9L4tdbpldWzcCh9UvlT3f2821l7qBs3RtX6tUni/Yt4fC0W\nW8r7sqnVdFbm97DLYMDcUIQCgLqswlv1jt+6u74fypYY77HKkMXIlqq2qo7sAr7p\niVQ87W7fpobGN7YA2xoI9XV0Jld5aSu2/MdIhVMtSe1HzsxtNqWnw0h6eanooBHw\nkpWXXOZ9vi1GklPUADUMyWAdeUVz52vUkkIPYXB9kygsfoGMHDj6rXu8DKuKGb/u\n/7k2vg+Xh4i9P1qemzn+sQXsmw0JxmGK0TbVsA9lV+/UNRlBBr3ExKJrfXKI/Mmq\nlynCzRoFM1VenLgIvvcQ6lJKFNNQp4GBuAPdbKPW2hk0HrqfNvVIwbsiY47m9Aqn\nBoq1EHq7lck9tZl0DasAEe5DBMa6EYTK0A==\n-----END CERTIFICATE-----"},
			CommonName:               "commonName",
		},
		Trace: log.NewLogger().(*log.Slogger),
	}
	t.Run("success", func(t *testing.T) {
		utils.ParsePEMCertificate = func(cert []string, _ string) (*x509.CertPool, error) {
			return x509.NewCertPool(), nil
		}
		_, _, err := _getAPICallCertificate(validParam)
		assert.NoError(t, err)
	})

	t.Run("parse PEM error ", func(t *testing.T) {
		utils.ParsePEMCertificate = func(cert []string, _ string) (*x509.CertPool, error) {
			return nil, errors.New("parse error")
		}
		_, _, err := _getAPICallCertificate(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse error")
	})

	t.Run("Load PEM error", func(t *testing.T) {
		utils.ParsePEMCertificate = func(cert []string, _ string) (*x509.CertPool, error) {
			return x509.NewCertPool(), nil
		}
		_, _, err := _getAPICallCertificate(params)
		assert.Error(t, err)
	})

	t.Run("invalid params", func(t *testing.T) {
		_, _, err := _getAPICallCertificate(params)
		assert.Error(t, err)
	})

	t.Run("empty params", func(t *testing.T) {
		_, _, err := _getAPICallCertificate(RESTClientParams{})
		assert.Error(t, err)
	})
}

// Test cases for FastConnection functionality
func TestNewClient_WithFastConnection(t *testing.T) {
	hostMap := map[string]string{}
	hostMap["localhost"] = "10.0.0.1"
	hostMap["localhost2"] = "10.0.0.2"
	params := RESTClientParams{
		Hosts:                       hostMap,
		Host:                        "10.0.0.1",
		Password:                    log.Secret("secret"),
		InsecureSkipVerify:          true,
		Trace:                       log.NewLogger().(*log.Slogger),
		CertificateBasedAuthEnabled: false,
		FastConnection:              true, // Enable fast connection
	}

	defer func() {
		FastTestConnection = fastTestConnection // Reset to original after test
	}()

	t.Run("FastConnection success on first host", func(t *testing.T) {
		FastTestConnection = func(rc *OntapRestClient) error {
			return nil
		}

		cl, err := NewClient(params)
		assert.NoError(t, err)
		assert.NotNil(t, cl)
		// Since Go maps have no guaranteed iteration order, either host could be selected first
		selectedHost := cl.Host()
		assert.Contains(t, []string{"10.0.0.1", "10.0.0.2"}, selectedHost)
	})

	t.Run("FastConnection fails on first host, succeeds on second", func(t *testing.T) {
		callCount := 0
		FastTestConnection = func(rc *OntapRestClient) error {
			callCount++
			if callCount == 1 {
				return errors.New("connection failed")
			}
			return nil
		}

		cl, err := NewClient(params)
		assert.NoError(t, err)
		assert.NotNil(t, cl)
		assert.Equal(t, 2, callCount)
		// Should succeed with second host
		assert.Contains(t, []string{"10.0.0.1", "10.0.0.2"}, cl.Host())
	})

	t.Run("FastConnection fails on all hosts", func(t *testing.T) {
		FastTestConnection = func(rc *OntapRestClient) error {
			return errors.New("fast connection failed")
		}

		cl, err := NewClient(params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "fast connection failed")
		assert.NotNil(t, cl) // Should return last tried client
	})
}

func TestNewClient_FastConnection_vs_RegularConnection(t *testing.T) {
	hostMap := map[string]string{}
	hostMap["localhost"] = "10.0.0.1"

	baseParams := RESTClientParams{
		Hosts:                       hostMap,
		Host:                        "10.0.0.1",
		Password:                    log.Secret("secret"),
		InsecureSkipVerify:          true,
		Trace:                       log.NewLogger().(*log.Slogger),
		CertificateBasedAuthEnabled: false,
	}

	defer func() {
		TestConnection = testConnection // Reset to original after test
		FastTestConnection = fastTestConnection
	}()

	t.Run("Regular connection used when FastConnection is false", func(t *testing.T) {
		regularConnectionCalled := false
		fastConnectionCalled := false

		TestConnection = func(rc *OntapRestClient) error {
			regularConnectionCalled = true
			return nil
		}
		FastTestConnection = func(rc *OntapRestClient) error {
			fastConnectionCalled = true
			return nil
		}

		params := baseParams
		params.FastConnection = false

		cl, err := NewClient(params)
		assert.NoError(t, err)
		assert.NotNil(t, cl)
		assert.True(t, regularConnectionCalled)
		assert.False(t, fastConnectionCalled)
	})

	t.Run("Fast connection used when FastConnection is true", func(t *testing.T) {
		regularConnectionCalled := false
		fastConnectionCalled := false

		TestConnection = func(rc *OntapRestClient) error {
			regularConnectionCalled = true
			return nil
		}
		FastTestConnection = func(rc *OntapRestClient) error {
			fastConnectionCalled = true
			return nil
		}

		params := baseParams
		params.FastConnection = true

		cl, err := NewClient(params)
		assert.NoError(t, err)
		assert.NotNil(t, cl)
		assert.False(t, regularConnectionCalled)
		assert.True(t, fastConnectionCalled)
	})
}

func TestFastTestConnection(t *testing.T) {
	t.Run("fastTestConnection with nil cluster client", func(t *testing.T) {
		rc := &OntapRestClient{
			cluster: nil,
		}

		err := fastTestConnection(rc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cluster client not initialized")
	})

	t.Run("fastTestConnection with nil cluster api", func(t *testing.T) {
		rc := &OntapRestClient{
			cluster: &clusterClient{
				api: nil,
			},
		}

		err := fastTestConnection(rc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cluster client not initialized")
	})

	t.Run("fastTestConnection success", func(t *testing.T) {
		// Mock the cluster API call by overriding the function
		originalFastTestConnection := FastTestConnection
		defer func() {
			FastTestConnection = originalFastTestConnection
		}()

		FastTestConnection = func(rc *OntapRestClient) error {
			return nil
		}

		// Create a minimal valid client
		hostMap := map[string]string{"localhost": "10.0.0.1"}
		params := RESTClientParams{
			Hosts:              hostMap,
			Host:               "10.0.0.1",
			Password:           log.Secret("secret"),
			InsecureSkipVerify: true,
			Trace:              log.NewLogger().(*log.Slogger),
			FastConnection:     true,
		}

		rc := &OntapRestClient{params: params}

		err := FastTestConnection(rc)
		assert.NoError(t, err)
	})

	t.Run("fastTestConnection error", func(t *testing.T) {
		// Mock the cluster API call by overriding the function
		originalFastTestConnection := FastTestConnection
		defer func() {
			FastTestConnection = originalFastTestConnection
		}()

		FastTestConnection = func(rc *OntapRestClient) error {
			return errors.New("connection timeout")
		}

		// Create a minimal valid client
		hostMap := map[string]string{"localhost": "10.0.0.1"}
		params := RESTClientParams{
			Hosts:              hostMap,
			Host:               "10.0.0.1",
			Password:           log.Secret("secret"),
			InsecureSkipVerify: true,
			Trace:              log.NewLogger().(*log.Slogger),
			FastConnection:     true,
		}

		rc := &OntapRestClient{params: params}

		err := FastTestConnection(rc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "connection timeout")
	})
}

func TestRegularTestConnection(t *testing.T) {
	t.Run("testConnection with nil cluster client", func(t *testing.T) {
		rc := &OntapRestClient{
			cluster: nil,
		}

		err := testConnection(rc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cluster client not initialized")
	})

	t.Run("testConnection with nil cluster api", func(t *testing.T) {
		rc := &OntapRestClient{
			cluster: &clusterClient{
				api: nil,
			},
		}

		err := testConnection(rc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cluster client not initialized")
	})

	t.Run("testConnection success", func(t *testing.T) {
		// Mock the cluster API call by overriding the function
		originalTestConnection := TestConnection
		defer func() {
			TestConnection = originalTestConnection
		}()

		TestConnection = func(rc *OntapRestClient) error {
			return nil
		}

		// Create a minimal valid client
		hostMap := map[string]string{"localhost": "10.0.0.1"}
		params := RESTClientParams{
			Hosts:              hostMap,
			Host:               "10.0.0.1",
			Password:           log.Secret("secret"),
			InsecureSkipVerify: true,
			Trace:              log.NewLogger().(*log.Slogger),
			FastConnection:     false,
		}

		rc := &OntapRestClient{params: params}

		err := TestConnection(rc)
		assert.NoError(t, err)
	})

	t.Run("testConnection error", func(t *testing.T) {
		// Mock the cluster API call by overriding the function
		originalTestConnection := TestConnection
		defer func() {
			TestConnection = originalTestConnection
		}()

		TestConnection = func(rc *OntapRestClient) error {
			return errors.New("cluster unavailable")
		}

		// Create a minimal valid client
		hostMap := map[string]string{"localhost": "10.0.0.1"}
		params := RESTClientParams{
			Hosts:              hostMap,
			Host:               "10.0.0.1",
			Password:           log.Secret("secret"),
			InsecureSkipVerify: true,
			Trace:              log.NewLogger().(*log.Slogger),
			FastConnection:     false,
		}

		rc := &OntapRestClient{params: params}

		err := TestConnection(rc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cluster unavailable")
	})
}
