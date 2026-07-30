package oci

import (
	"context"
	"strings"

	ocicloudproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/oci-cloud-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	logger "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// ociProxyEnabled reports whether hyperscaler/oci should route Category-A
// operations (secrets, certificates, DNS, object-storage PARs) through the OCI
// cloud proxy.
// when empty, all methods use the OCI Go SDK (existing behavior).
func ociProxyEnabled() bool {
	return strings.TrimSpace(env.OCIProxyBaseURL) != ""
}

func ProxyEnabled() bool {
	return ociProxyEnabled()
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

// ContextWithProxyRequestID returns a context carrying the opc-work-request-id
// sent as X-Proxy-Request-Id for OCI cloud-proxy calls.
func ContextWithProxyRequestID(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, middleware.OPCWorkRequestIDHeaderName, id)
}

// ProxyRequestIDFromContext returns the opc-work-request-id or "" if none.
func ProxyRequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, ok := ctx.Value(middleware.OPCWorkRequestIDHeaderName).(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(id)
}

// ociProxyWorkflowRequestID resolves the per-request opc-work-request-id sent as
// X-Proxy-Request-Id.
func ociProxyWorkflowRequestID(ctx context.Context) string {
	if id := ProxyRequestIDFromContext(ctx); id != "" {
		return id
	}
	return ociProxyWorkRequestIDFromLoggerFields(ctx)
}

// ociProxyWorkRequestIDFromLoggerFields extracts the opc-work-request-id from the
// propagated Temporal logger-fields map
func ociProxyWorkRequestIDFromLoggerFields(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(logger.Fields)
	if !ok {
		return ""
	}
	val, ok := fields[string(middleware.RequestOpcWorkRequestID)]
	if !ok {
		return ""
	}
	id, ok := val.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(id)
}
