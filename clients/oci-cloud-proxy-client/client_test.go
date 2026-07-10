package ocicloudproxyclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestProxyRoundTripper(t *testing.T) {
	t.Run("sets json accept and identity encoding, no authorization", func(t *testing.T) {
		var gotAuth, gotAccept, gotEncoding string
		rt := proxyRoundTripper{
			next: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				gotAuth = req.Header.Get("Authorization")
				gotAccept = req.Header.Get("Accept")
				gotEncoding = req.Header.Get("Accept-Encoding")
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}
		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)

		_, err = rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Empty(t, gotAuth)
		assert.Equal(t, "application/json", gotAccept)
		assert.Equal(t, "identity", gotEncoding)
	})

	t.Run("does not override a preset accept header", func(t *testing.T) {
		var gotAccept string
		rt := proxyRoundTripper{
			next: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				gotAccept = req.Header.Get("Accept")
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("")),
				}, nil
			}),
		}
		req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/xml")

		_, err = rt.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, "application/xml", gotAccept)
	})
}

func TestNewProxyClient(t *testing.T) {
	client, err := NewProxyClient("https://oci-cloud-proxy.internal/v1", 30*time.Second, PathPrefixes{}, ProxyTLSConfig{})
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestBaseTransport(t *testing.T) {
	t.Run("zero config reuses default transport", func(t *testing.T) {
		rt, err := baseTransport(ProxyTLSConfig{})
		require.NoError(t, err)
		assert.Same(t, http.DefaultTransport, rt)
	})

	t.Run("CA bundle produces a cloned transport with custom roots", func(t *testing.T) {
		caPath := writeTempPEM(t, generateTestCACertPEM(t))
		rt, err := baseTransport(ProxyTLSConfig{CABundlePath: caPath})
		require.NoError(t, err)
		tr, ok := rt.(*http.Transport)
		require.True(t, ok)
		assert.NotSame(t, http.DefaultTransport, tr)
		require.NotNil(t, tr.TLSClientConfig)
		require.NotNil(t, tr.TLSClientConfig.RootCAs)
	})

	t.Run("missing CA bundle file errors", func(t *testing.T) {
		_, err := baseTransport(ProxyTLSConfig{CABundlePath: "/no/such/ca.pem"})
		require.Error(t, err)
	})

	t.Run("invalid CA bundle contents errors", func(t *testing.T) {
		badPath := writeTempPEM(t, "not a pem")
		_, err := baseTransport(ProxyTLSConfig{CABundlePath: badPath})
		require.Error(t, err)
	})

	t.Run("client cert without key errors", func(t *testing.T) {
		_, err := baseTransport(ProxyTLSConfig{ClientCertPath: "/some/cert.pem"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "both client cert and key")
	})

	t.Run("valid client cert and key produce an mTLS transport", func(t *testing.T) {
		certPEM, keyPEM := generateTestCertAndKeyPEM(t)
		certPath := writeTempPEM(t, certPEM)
		keyPath := writeTempPEM(t, keyPEM)
		rt, err := baseTransport(ProxyTLSConfig{ClientCertPath: certPath, ClientKeyPath: keyPath})
		require.NoError(t, err)
		tr, ok := rt.(*http.Transport)
		require.True(t, ok)
		assert.NotSame(t, http.DefaultTransport, tr)
		require.NotNil(t, tr.TLSClientConfig)
		require.Len(t, tr.TLSClientConfig.Certificates, 1)
	})

	t.Run("invalid client cert/key contents error", func(t *testing.T) {
		certPath := writeTempPEM(t, "not a certificate")
		keyPath := writeTempPEM(t, "not a key")
		_, err := baseTransport(ProxyTLSConfig{ClientCertPath: certPath, ClientKeyPath: keyPath})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "client cert/key")
	})
}

func TestNewProxyClient_TLSBuildError(t *testing.T) {
	// A CA bundle path that does not exist makes baseTransport (and thus the
	// TLS build) fail, so NewProxyClient must surface the error rather than
	// returning a client.
	client, err := NewProxyClient("https://oci-cloud-proxy.internal", 30*time.Second, PathPrefixes{}, ProxyTLSConfig{
		CABundlePath: "/no/such/ca-bundle.pem",
	})
	require.Error(t, err)
	assert.Nil(t, client)
}

func writeTempPEM(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/bundle.pem"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func generateTestCACertPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-oci-proxy-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// generateTestCertAndKeyPEM returns a matching self-signed client certificate
// and its EC private key, both PEM-encoded, suitable for tls.LoadX509KeyPair.
func generateTestCertAndKeyPEM(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-oci-proxy-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

func TestPathRewriteTransport(t *testing.T) {
	var gotPath string
	rt := pathRewriteTransport{
		next: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			gotPath = req.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}),
		rewrites: pathRewrites(DefaultPathPrefixes(), PathPrefixes{
			Vault: "/v2/vault",
		}),
	}
	req, err := http.NewRequest(http.MethodPost, "https://proxy/20180608/secrets", nil)
	require.NoError(t, err)

	_, err = rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, "/v2/vault/secrets", gotPath)
}
