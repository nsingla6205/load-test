package oci

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	ocicloudproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/oci-cloud-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	logger "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestNormalizeOCIProxyPathPrefix(t *testing.T) {
	assert.Equal(t, "/20160918", normalizeOCIProxyPathPrefix("", "/20160918"))
	assert.Equal(t, "/20160918", normalizeOCIProxyPathPrefix("/", "/20160918"))
	assert.Equal(t, "/20160918", normalizeOCIProxyPathPrefix("20160918", "/20160918"))
	assert.Equal(t, "/20190301/secretbundles", normalizeOCIProxyPathPrefix(" /20190301/secretbundles/ ", "/20160918"))
}

func TestOCIProxyPathPrefixesFromEnv(t *testing.T) {
	// Snapshot and restore every prefix env var so subtests don't leak state.
	origObj := env.OCIProxyAPIVersionPrefix
	origSecret := env.OCIProxySecretBundlesAPIVersionPrefix
	origVault := env.OCIProxyVaultAPIVersionPrefix
	origCert := env.OCIProxyCertificatesAPIVersionPrefix
	origDNS := env.OCIProxyDNSAPIVersionPrefix
	t.Cleanup(func() {
		env.OCIProxyAPIVersionPrefix = origObj
		env.OCIProxySecretBundlesAPIVersionPrefix = origSecret
		env.OCIProxyVaultAPIVersionPrefix = origVault
		env.OCIProxyCertificatesAPIVersionPrefix = origCert
		env.OCIProxyDNSAPIVersionPrefix = origDNS
	})

	d := ocicloudproxyclient.DefaultPathPrefixes()

	t.Run("empty env falls back to the built-in defaults for every family", func(t *testing.T) {
		// This is the Option-A contract: the env vars ship empty and the
		// version literals live only in DefaultPathPrefixes().
		env.OCIProxyAPIVersionPrefix = ""
		env.OCIProxySecretBundlesAPIVersionPrefix = ""
		env.OCIProxyVaultAPIVersionPrefix = ""
		env.OCIProxyCertificatesAPIVersionPrefix = ""
		env.OCIProxyDNSAPIVersionPrefix = ""

		p := ociProxyPathPrefixesFromEnv()
		assert.Equal(t, d.ObjectStorage, p.ObjectStorage)
		assert.Equal(t, d.SecretBundles, p.SecretBundles)
		assert.Equal(t, d.Vault, p.Vault)
		assert.Equal(t, d.Certificates, p.Certificates)
		assert.Equal(t, d.DNS, p.DNS)
	})

	t.Run("a non-empty env override wins over the built-in default", func(t *testing.T) {
		env.OCIProxyVaultAPIVersionPrefix = "/custom/vault"
		env.OCIProxyDNSAPIVersionPrefix = "" // still falls back

		p := ociProxyPathPrefixesFromEnv()
		assert.Equal(t, "/custom/vault", p.Vault)
		assert.Equal(t, d.DNS, p.DNS)
	})
}

func TestProxyEnabled(t *testing.T) {
	orig := env.OCIProxyBaseURL
	t.Cleanup(func() { env.OCIProxyBaseURL = orig })

	t.Run("true when base URL is set", func(t *testing.T) {
		env.OCIProxyBaseURL = "http://proxy.local"
		assert.True(t, ProxyEnabled())
	})

	t.Run("false when base URL is blank", func(t *testing.T) {
		env.OCIProxyBaseURL = "   "
		assert.False(t, ProxyEnabled())
	})
}

func TestOCIProxyWorkflowRequestID(t *testing.T) {
	t.Run("resolves opc-work-request-id from logger fields", func(t *testing.T) {
		fields := logger.Fields{string(middleware.RequestOpcWorkRequestID): "work-123"}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
		assert.Equal(t, "work-123", ociProxyWorkflowRequestID(ctx))
	})

	t.Run("empty when opc-work-request-id absent", func(t *testing.T) {
		assert.Equal(t, "", ociProxyWorkflowRequestID(context.Background()))
	})

	t.Run("resolves explicit id set via ContextWithProxyRequestID", func(t *testing.T) {
		ctx := ContextWithProxyRequestID(context.Background(), "ontap-uuid-a")
		assert.Equal(t, "ontap-uuid-a", ociProxyWorkflowRequestID(ctx))
	})

	t.Run("resolves id from the raw propagated key (ingress/VLM/propagator)", func(t *testing.T) {
		// The Temporal ContextPropagator, OCI ingress, and VLM store the id under
		// the shared OPCWorkRequestIDHeaderName key directly, not via ContextWithProxyRequestID.
		ctx := context.WithValue(context.Background(), middleware.OPCWorkRequestIDHeaderName, "work-123")
		assert.Equal(t, "work-123", ociProxyWorkflowRequestID(ctx))
	})

	t.Run("explicit id takes precedence over logger fields", func(t *testing.T) {
		fields := logger.Fields{string(middleware.RequestOpcWorkRequestID): "work-123"}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
		ctx = ContextWithProxyRequestID(ctx, "ontap-uuid-a")
		assert.Equal(t, "ontap-uuid-a", ociProxyWorkflowRequestID(ctx))
	})

	t.Run("falls back to logger fields when explicit id is blank", func(t *testing.T) {
		fields := logger.Fields{string(middleware.RequestOpcWorkRequestID): "work-123"}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
		ctx = ContextWithProxyRequestID(ctx, "   ") // blank ignored
		assert.Equal(t, "work-123", ociProxyWorkflowRequestID(ctx))
	})
}

func TestContextWithProxyRequestID(t *testing.T) {
	t.Run("round-trips a trimmed id", func(t *testing.T) {
		ctx := ContextWithProxyRequestID(context.Background(), "  ontap-uuid-a  ")
		assert.Equal(t, "ontap-uuid-a", ProxyRequestIDFromContext(ctx))
	})

	t.Run("blank id leaves ctx unchanged", func(t *testing.T) {
		base := context.Background()
		assert.Equal(t, base, ContextWithProxyRequestID(base, "   "))
		assert.Equal(t, "", ProxyRequestIDFromContext(base))
	})

	t.Run("nil ctx is returned unchanged", func(t *testing.T) {
		var nilCtx context.Context
		assert.Nil(t, ContextWithProxyRequestID(nilCtx, "ontap-uuid-a"))
		assert.Equal(t, "", ProxyRequestIDFromContext(nilCtx))
	})
}

func TestOCIProxyWorkRequestIDFromLoggerFields(t *testing.T) {
	t.Run("nil ctx", func(t *testing.T) {
		var nilCtx context.Context
		assert.Equal(t, "", ociProxyWorkRequestIDFromLoggerFields(nilCtx))
	})

	t.Run("value from propagated logger fields (trimmed)", func(t *testing.T) {
		fields := logger.Fields{string(middleware.RequestOpcWorkRequestID): " work-123 "}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
		assert.Equal(t, "work-123", ociProxyWorkRequestIDFromLoggerFields(ctx))
	})

	t.Run("logger-fields value is wrong type", func(t *testing.T) {
		fields := logger.Fields{string(middleware.RequestOpcWorkRequestID): 123}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
		assert.Equal(t, "", ociProxyWorkRequestIDFromLoggerFields(ctx))
	})

	t.Run("opc-work-request-id key absent from logger fields", func(t *testing.T) {
		fields := logger.Fields{"some-other-field": "value"}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
		assert.Equal(t, "", ociProxyWorkRequestIDFromLoggerFields(ctx))
	})

	t.Run("TemporalSLoggerKey holds wrong type", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, "not-fields")
		assert.Equal(t, "", ociProxyWorkRequestIDFromLoggerFields(ctx))
	})
}
