package oci

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	ocicloudproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/oci-cloud-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
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

func TestOCIProxyContextString(t *testing.T) {
	assert.Equal(t, "", ociProxyContextString(nil, "key"))

	ctx := context.WithValue(context.Background(), "slice-key", []string{"", " first "})
	assert.Equal(t, "first", ociProxyContextString(ctx, "slice-key"))

	ctx = context.WithValue(context.Background(), "string-key", " value ")
	assert.Equal(t, "value", ociProxyContextString(ctx, "string-key"))
}

func TestOCIProxyWorkflowRequestID(t *testing.T) {
	t.Run("nil context", func(t *testing.T) {
		var nilCtx context.Context
		assert.Equal(t, "", ociProxyWorkflowRequestID(nilCtx))
	})

	t.Run("opc-work-request-id from context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ctxKeyOpcWorkRequestID, " work-456 ")
		assert.Equal(t, "work-456", ociProxyWorkflowRequestID(ctx))
	})

	t.Run("empty when opc-work-request-id absent", func(t *testing.T) {
		assert.Equal(t, "", ociProxyWorkflowRequestID(context.Background()))
	})
}
