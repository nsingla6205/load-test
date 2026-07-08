//go:build integration

package oci

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// Live proxy smoke tests — require port-forward (or any reachable OCI_PROXY_BASE_URL).
//
// Terminal 1:
//
//	kubectl config use-context context-cefhfq7k5wa
//	kubectl port-forward -n fsn svc/resource-manager-service 8080:80
//
// Terminal 2 (from repo root):
//
//	OCI_PROXY_BASE_URL=http://localhost:8080 \
//	OCI_TEST_SECRET_OCID=ocid1.vaultsecret.oc1.... \
//	go test -tags=integration ./hyperscaler/oci/ -run TestIntegration_ProxyGetSecret -v
func TestIntegration_ProxyGetSecretWithLatestVersion(t *testing.T) {
	secretOCID := strings.TrimSpace(os.Getenv("OCI_TEST_SECRET_OCID"))
	if secretOCID == "" {
		t.Skip("set OCI_TEST_SECRET_OCID to an existing vault secret OCID")
	}

	baseURL := strings.TrimSpace(os.Getenv("OCI_PROXY_BASE_URL"))
	if baseURL == "" {
		t.Skip("set OCI_PROXY_BASE_URL (e.g. http://localhost:8080 while port-forwarding)")
	}

	env.OCIProxyBaseURL = baseURL
	// Local/in-cluster proxy is typically reached over http (e.g. port-forward),
	// so allow plaintext http for this smoke test.
	env.OCIProxyAllowHTTP = true

	ctx := context.Background()
	svc := &OciServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
	require.NoError(t, svc.InitializeClients())

	secret, err := svc.GetSecretWithLatestVersion(secretOCID)
	require.NoError(t, err)
	require.NotNil(t, secret, "expected secret value (nil,nil means not found or empty bundle)")
	assert.Equal(t, secretOCID, secret.Ocid)
	assert.NotEmpty(t, secret.Value)
	assert.Positive(t, secret.Version)
	t.Logf("secret OCID=%s version=%d valueLen=%d", secret.Ocid, secret.Version, len(secret.Value))
}
