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
	origVault := env.OCIProxyVaultAPIVersionPrefix
	t.Cleanup(func() { env.OCIProxyVaultAPIVersionPrefix = origVault })

	env.OCIProxyVaultAPIVersionPrefix = "/custom/vault"
	p := ociProxyPathPrefixesFromEnv()
	assert.Equal(t, "/custom/vault", p.Vault)
	assert.Equal(t, ocicloudproxyclient.DefaultPathPrefixes().DNS, p.DNS)
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
