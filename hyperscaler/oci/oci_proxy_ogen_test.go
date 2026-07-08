package oci

import (
	"context"
	"net/http"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ocicloudproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/oci-cloud-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestValidateOCIProxyBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		base      string
		allowHTTP bool
		wantErr   string
	}{
		{name: "empty", base: "", wantErr: "OCI_PROXY_BASE_URL is not set"},
		{name: "invalid", base: "not-a-url", wantErr: "invalid OCI_PROXY_BASE_URL"},
		{name: "https ok", base: "https://proxy.internal"},
		{name: "http rejected without opt-in", base: "http://proxy.internal", wantErr: "must use https"},
		{name: "http allowed with opt-in", base: "http://proxy.internal", allowHTTP: true},
		{name: "bad scheme", base: "ftp://proxy.internal", wantErr: "must use http or https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := env.OCIProxyAllowHTTP
			env.OCIProxyAllowHTTP = tt.allowHTTP
			t.Cleanup(func() { env.OCIProxyAllowHTTP = orig })

			err := validateOCIProxyBaseURL(tt.base)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestOCIProxyAPIError(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var e *ociProxyAPIError
		assert.Equal(t, "", e.Error())
	})

	t.Run("with code (OCI-forwarded)", func(t *testing.T) {
		e := &ociProxyAPIError{statusCode: http.StatusNotFound, code: "NotFound", message: "missing"}
		got := e.Error()
		assert.Contains(t, got, "OCI error")
		assert.Contains(t, got, "NotFound")
		assert.Contains(t, got, "missing")
		assert.Equal(t, "NotFound", e.GetCode())
		assert.Equal(t, "missing", e.GetMessage())
	})

	t.Run("proxy-generated error", func(t *testing.T) {
		e := &ociProxyAPIError{statusCode: http.StatusBadRequest, message: "bad request", proxyGenerated: true}
		got := e.Error()
		assert.Contains(t, got, "proxy error")
		assert.Contains(t, got, "bad request")
		assert.Equal(t, http.StatusBadRequest, e.GetHTTPStatusCode())
	})

	t.Run("status only", func(t *testing.T) {
		e := &ociProxyAPIError{statusCode: http.StatusInternalServerError}
		assert.Contains(t, e.Error(), "HTTP 500")
	})
}

func TestIsOCIProxyNotFound(t *testing.T) {
	assert.True(t, isOCIProxyNotFound(&ociProxyAPIError{statusCode: http.StatusNotFound}))
	assert.False(t, isOCIProxyNotFound(&ociProxyAPIError{statusCode: http.StatusBadRequest}))
	assert.False(t, isOCIProxyNotFound(assert.AnError))
}

func TestRequiredProxyPathID(t *testing.T) {
	v := "ocid1.secret"
	got, err := requiredProxyPathID("SecretId", &v)
	require.NoError(t, err)
	assert.Equal(t, "ocid1.secret", got)

	_, err = requiredProxyPathID("SecretId", common.String("  "))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SecretId is required")
}

func TestOgenOpaqueBodyAndDecode(t *testing.T) {
	body, err := ogenOpaqueBody(vault.CreateSecretDetails{SecretName: common.String("n")})
	require.NoError(t, err)
	require.NotNil(t, body)

	var secret vault.Secret
	require.NoError(t, ogenDecode(&body, &secret))
}

func TestOgenStatus(t *testing.T) {
	tests := []struct {
		res  any
		want int
	}{
		{&ocicloudproxyclient.Error400Headers{}, http.StatusBadRequest},
		{&ocicloudproxyclient.Error401Headers{}, http.StatusUnauthorized},
		{&ocicloudproxyclient.Error403Headers{}, http.StatusForbidden},
		{&ocicloudproxyclient.Error404Headers{}, http.StatusNotFound},
		{&ocicloudproxyclient.Error409Headers{}, http.StatusConflict},
		{&ocicloudproxyclient.Error412Headers{}, http.StatusPreconditionFailed},
		{&ocicloudproxyclient.Error429Headers{}, http.StatusTooManyRequests},
		{&ocicloudproxyclient.Error500Headers{}, http.StatusInternalServerError},
		{&ocicloudproxyclient.Error502Headers{}, http.StatusBadGateway},
		{&ocicloudproxyclient.Error503Headers{}, http.StatusServiceUnavailable},
		{&ocicloudproxyclient.Error504Headers{}, http.StatusGatewayTimeout},
		{"unknown", 0},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, ogenStatus(tt.res))
	}
}

func TestOgenResultError(t *testing.T) {
	err := ogenResultError("CreateSecret", &ocicloudproxyclient.Error404Headers{})
	var apiErr *ociProxyAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.GetHTTPStatusCode())
	assert.Contains(t, apiErr.Error(), "CreateSecret")
}

func TestOgenCommonHeaders(t *testing.T) {
	origValidate := env.OCIProxyValidateOnly
	t.Cleanup(func() { env.OCIProxyValidateOnly = origValidate })

	env.OCIProxyValidateOnly = true

	t.Run("uses opc-work-request-id from context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), ctxKeyOpcWorkRequestID, "work-req-1")
		reqID, validateOnly := ogenCommonHeaders(ctx)
		assert.True(t, reqID.IsSet())
		assert.Equal(t, "work-req-1", reqID.Value)
		assert.True(t, validateOnly.IsSet())
		assert.True(t, validateOnly.Value)
	})

	t.Run("falls back to a unique generated id", func(t *testing.T) {
		reqID1, _ := ogenCommonHeaders(context.Background())
		reqID2, _ := ogenCommonHeaders(context.Background())
		assert.True(t, reqID1.IsSet())
		assert.True(t, reqID2.IsSet())
		assert.NotEmpty(t, reqID1.Value)
		assert.NotEqual(t, reqID1.Value, reqID2.Value)
	})
}

func TestNewOgenProxyClient(t *testing.T) {
	origBase := env.OCIProxyBaseURL
	t.Cleanup(func() {
		env.OCIProxyBaseURL = origBase
	})

	withOCIProxyBaseURL(t, "https://oci-cloud-proxy.internal/")
	client, err := newOgenProxyClient()
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestOgenCreateSecret_proxyErrorResponse(t *testing.T) {
	svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proxy-Error", "true")
		writeProxyJSON(w, http.StatusBadRequest, map[string]string{
			"code":    "InvalidParameter",
			"message": "bad body",
		})
	})
	_, err := ogenCreateSecret(svc.Ctx, svc.wrapper.(*ociProxyWrapper).proxyClient, vault.CreateSecretRequest{
		CreateSecretDetails: vault.CreateSecretDetails{
			SecretName: common.String("n"),
		},
		OpcRetryToken: common.String("retry-token"),
	})
	require.Error(t, err)
	var apiErr *ociProxyAPIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.GetHTTPStatusCode())
	// The OCI error body must survive the opaque-JSON layer (not collapse to a
	// bare HTTP status), and the X-Proxy-Error signal must be captured.
	assert.Equal(t, "InvalidParameter", apiErr.GetCode())
	assert.Equal(t, "bad body", apiErr.GetMessage())
	assert.True(t, apiErr.proxyGenerated)
	assert.Contains(t, apiErr.Error(), "proxy error")
	assert.Contains(t, apiErr.Error(), "InvalidParameter")
}
