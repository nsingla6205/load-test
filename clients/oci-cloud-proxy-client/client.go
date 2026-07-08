package ocicloudproxyclient

import (
	"net/http"
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
		ObjectStorage: "/20160918",
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

// NewProxyClient builds the generated client against serverURL (OCI_PROXY_BASE_URL).
// prefixes overrides the codegen path roots when any field differs from DefaultPathPrefixes.
func NewProxyClient(serverURL string, timeout time.Duration, prefixes PathPrefixes) (*Client, error) {
	transport := http.RoundTripper(http.DefaultTransport)
	if rewrites := pathRewrites(DefaultPathPrefixes(), prefixes); len(rewrites) > 0 {
		transport = pathRewriteTransport{next: transport, rewrites: rewrites}
	}
	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: proxyRoundTripper{next: transport},
	}
	return NewClient(serverURL, WithClient(httpClient))
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
