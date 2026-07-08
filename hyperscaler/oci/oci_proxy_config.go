package oci

import (
	"context"
	"strings"

	ocicloudproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/oci-cloud-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const ctxKeyOpcWorkRequestID = "opc-work-request-id"

// ociProxyEnabled reports whether hyperscaler/oci should route Category-A
// operations (secrets, certificates, DNS, object-storage PARs) through the OCI
// cloud proxy.
// when empty, all methods use the OCI Go SDK (existing behavior).
func ociProxyEnabled() bool {
	return strings.TrimSpace(env.OCIProxyBaseURL) != ""
}

// ociProxyPathPrefixesFromEnv returns path roots for the ogen client, read directly
// from OCI_PROXY_*_API_VERSION_PREFIX env vars (defaults match oci-cloud-proxy-api.yaml).
func ociProxyPathPrefixesFromEnv() ocicloudproxyclient.PathPrefixes {
	d := ocicloudproxyclient.DefaultPathPrefixes()
	return ocicloudproxyclient.PathPrefixes{
		ObjectStorage: normalizeOCIProxyPathPrefix(env.OCIProxyAPIVersionPrefix, d.ObjectStorage),
		SecretBundles: normalizeOCIProxyPathPrefix(env.OCIProxySecretBundlesAPIVersionPrefix, d.SecretBundles),
		Vault:         normalizeOCIProxyPathPrefix(env.OCIProxyVaultAPIVersionPrefix, d.Vault),
		Certificates:  normalizeOCIProxyPathPrefix(env.OCIProxyCertificatesAPIVersionPrefix, d.Certificates),
		DNS:           normalizeOCIProxyPathPrefix(env.OCIProxyDNSAPIVersionPrefix, d.DNS),
	}
}

func normalizeOCIProxyPathPrefix(value, defaultValue string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return defaultValue
	}
	v = "/" + strings.Trim(v, "/")
	if v == "/" {
		return defaultValue
	}
	return v
}

// ociProxyWorkflowRequestID resolves the value sent as X-Proxy-Request-Id: the
// opc-work-request-id minted per request by the customer-facing OCI proxy
// frontend  and propagated into the activity context by workflow_engine/util/ctx_propagator.go.
//
// Per design this id must be unique per request. This function returns "" when
// the opc-work-request-id is absent; callers must fall back to a freshly
// generated unique id so the header is always present and unique — never a
// static or non-per-request value.
func ociProxyWorkflowRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	return ociProxyContextString(ctx, ctxKeyOpcWorkRequestID)
}

func ociProxyContextString(ctx context.Context, key string) string {
	if ctx == nil {
		return ""
	}
	switch v := ctx.Value(key).(type) {
	case string:
		return strings.TrimSpace(v)
	case []string:
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	return ""
}
