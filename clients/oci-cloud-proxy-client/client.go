package ocicloudproxyclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

//go:generate go run github.com/ogen-go/ogen/cmd/ogen@latest --clean --package ocicloudproxyclient --config .ogenclient.yml --target . oci-cloud-proxy-api.yaml

// PathPrefixes holds OCI API version path roots for each proxy operation family.
// Codegen defaults come from oci-cloud-proxy-api.yaml/
// hyperscaler/oci overrides them from OCI_PROXY_*_API_VERSION_PREFIX env vars.
type PathPrefixes struct {
	ObjectStorage string
	SecretBundles string
	Vault         string
	Certificates  string
	DNS           string
}

// DefaultPathPrefixes are the path roots in oci-cloud-proxy-api.yaml / oas_*_gen.go.
// Update when regenerating from a new spec.
func DefaultPathPrefixes() PathPrefixes {
	return PathPrefixes{
		ObjectStorage: "",
		SecretBundles: "/20190301/secretbundles",
		Vault:         "/20180608",
		Certificates:  "/20210224",
		DNS:           "/20180115",
	}
}

// proxyRoundTripper sets the JSON Accept header and forces identity content-encoding.
type proxyRoundTripper struct {
	next http.RoundTripper
}

func (b proxyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Accept-Encoding", "identity")
	return b.next.RoundTrip(req)
}

// ProxyTLSConfig configures TLS for the OCI cloud proxy client. All fields are
// optional; when all are empty the system trust store is used with no client
// certificate (equivalent to the default transport).
type ProxyTLSConfig struct {
	// CABundlePath is a filesystem path to a PEM file of CA certificate(s) to
	// trust for the proxy's server certificate. Set this when the proxy is fronted
	// by a private/internal CA. The certificates are added to the system trust
	// store rather than replacing it, so publicly-trusted certs still validate.
	CABundlePath string
	// ClientCertPath and ClientKeyPath are filesystem paths to a PEM client
	// certificate and private key used to authenticate to the proxy via mutual
	// TLS. Both must be set together, or neither.
	ClientCertPath string
	ClientKeyPath  string
}

func (c ProxyTLSConfig) isZero() bool {
	return strings.TrimSpace(c.CABundlePath) == "" &&
		strings.TrimSpace(c.ClientCertPath) == "" &&
		strings.TrimSpace(c.ClientKeyPath) == ""
}

// build constructs a *tls.Config from the configured CA bundle and/or client
// certificate. Returns nil when no TLS customization is requested.
func (c ProxyTLSConfig) build() (*tls.Config, error) {
	if c.isZero() {
		return nil, nil
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if p := strings.TrimSpace(c.CABundlePath); p != "" {
		pemBytes, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read OCI proxy CA bundle %q: %w", p, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("OCI proxy CA bundle %q contains no valid PEM certificates", p)
		}
		cfg.RootCAs = pool
	}

	certPath := strings.TrimSpace(c.ClientCertPath)
	keyPath := strings.TrimSpace(c.ClientKeyPath)
	switch {
	case certPath != "" && keyPath != "":
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load OCI proxy client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	case certPath != "" || keyPath != "":
		return nil, fmt.Errorf("OCI proxy mTLS requires both client cert and key (cert set=%t, key set=%t)", certPath != "", keyPath != "")
	}

	return cfg, nil
}

// NewProxyClient builds the generated client against serverURL (OCI_PROXY_BASE_URL).
// prefixes overrides the codegen path roots when any field differs from DefaultPathPrefixes.
// tlsCfg optionally supplies a private CA bundle and/or a client certificate for
// mutual TLS; a zero value uses the default transport (system trust, no client cert).
func NewProxyClient(serverURL string, timeout time.Duration, prefixes PathPrefixes, tlsCfg ProxyTLSConfig) (*Client, error) {
	base, err := baseTransport(tlsCfg)
	if err != nil {
		return nil, err
	}
	transport := http.RoundTripper(base)
	if rewrites := pathRewrites(DefaultPathPrefixes(), prefixes); len(rewrites) > 0 {
		transport = pathRewriteTransport{next: transport, rewrites: rewrites}
	}
	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: proxyRoundTripper{next: transport},
	}
	return NewClient(serverURL, WithClient(httpClient))
}

// baseTransport returns the shared default transport when no TLS customization is
// requested, or a cloned transport with the custom TLS config otherwise. The
// shared http.DefaultTransport is never mutated.
func baseTransport(tlsCfg ProxyTLSConfig) (http.RoundTripper, error) {
	tc, err := tlsCfg.build()
	if err != nil {
		return nil, err
	}
	if tc == nil {
		return http.DefaultTransport, nil
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = tc
	return t, nil
}

type pathRewrite struct {
	from, to string
}

type pathRewriteTransport struct {
	next     http.RoundTripper
	rewrites []pathRewrite
}

func (t pathRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	for _, rw := range t.rewrites {
		if strings.HasPrefix(path, rw.from) {
			req.URL.Path = rw.to + strings.TrimPrefix(path, rw.from)
			break
		}
	}
	return t.next.RoundTrip(req)
}

func pathRewrites(defaults, configured PathPrefixes) []pathRewrite {
	cfg := effectivePrefixes(configured)
	pairs := []struct{ from, to string }{
		{defaults.SecretBundles, cfg.SecretBundles},
		{defaults.ObjectStorage, cfg.ObjectStorage},
		{defaults.Vault, cfg.Vault},
		{defaults.Certificates, cfg.Certificates},
		{defaults.DNS, cfg.DNS},
	}
	var out []pathRewrite
	for _, p := range pairs {
		from := normalizePrefix(p.from)
		to := normalizePrefix(p.to)
		if from != "" && to != "" && from != to {
			out = append(out, pathRewrite{from: from, to: to})
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].from) > len(out[j].from) })
	return out
}

func effectivePrefixes(p PathPrefixes) PathPrefixes {
	d := DefaultPathPrefixes()
	return PathPrefixes{
		ObjectStorage: coalescePrefix(p.ObjectStorage, d.ObjectStorage),
		SecretBundles: coalescePrefix(p.SecretBundles, d.SecretBundles),
		Vault:         coalescePrefix(p.Vault, d.Vault),
		Certificates:  coalescePrefix(p.Certificates, d.Certificates),
		DNS:           coalescePrefix(p.DNS, d.DNS),
	}
}

func coalescePrefix(value, fallback string) string {
	if v := normalizePrefix(value); v != "" {
		return v
	}
	return normalizePrefix(fallback)
}

func normalizePrefix(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	v = "/" + strings.Trim(v, "/")
	if v == "/" {
		return ""
	}
	return v
}
