package oci

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// newProxyWrapperServices wires an ociProxyWrapper backed by an httptest server.
func newProxyWrapperServices(t *testing.T, handler http.HandlerFunc) *OciServices {
	t.Helper()
	ctx := context.Background()
	lg := util.GetLogger(ctx)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	// All proxy operations go through the ogen client, which builds itself from
	// env.OCIProxyBaseURL — point it at this (http) server and allow http.
	origBase := env.OCIProxyBaseURL
	origAllowHTTP := env.OCIProxyAllowHTTP
	env.OCIProxyBaseURL = server.URL
	env.OCIProxyAllowHTTP = true
	t.Cleanup(func() {
		env.OCIProxyBaseURL = origBase
		env.OCIProxyAllowHTTP = origAllowHTTP
	})

	baseSvc := &OciServices{Ctx: ctx, Logger: lg}
	client, err := newOgenProxyClient()
	require.NoError(t, err)
	return &OciServices{
		Ctx:     ctx,
		Logger:  lg,
		wrapper: &ociProxyWrapper{svc: baseSvc, proxyClient: client},
	}
}

func writeProxyJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeProxyNotFound(w http.ResponseWriter) {
	writeProxyJSON(w, http.StatusNotFound, map[string]string{
		"code":    "NotFound",
		"message": "HTTP 404 Not Found",
	})
}

// ---------------------------------------------------------------------------
// Secrets
// ---------------------------------------------------------------------------

func TestOCIProxyWrapper_CreateSecret_emptyValue(t *testing.T) {
	svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("proxy must not be called when secret value is empty")
	})
	secret, err := svc.CreateSecret("comp", "vault", "key", "name", "")
	assert.Nil(t, secret)
	require.Error(t, err)
}

// newProxyDeleteSvc wires an ociProxyWrapper whose SDK pre-flight read hits the
// supplied dispatcher (vault or cert-management) and whose schedule-deletion
// mutation hits an httptest proxy server. Reads never use the proxy, so the
// pre-flight is served by the SDK client while the write goes through the proxy.
func newProxyDeleteSvc(t *testing.T, svc *OciServices, proxyHandler http.HandlerFunc) *OciServices {
	t.Helper()
	server := httptest.NewServer(proxyHandler)
	t.Cleanup(server.Close)
	origBase := env.OCIProxyBaseURL
	origAllowHTTP := env.OCIProxyAllowHTTP
	env.OCIProxyBaseURL = server.URL
	env.OCIProxyAllowHTTP = true
	t.Cleanup(func() {
		env.OCIProxyBaseURL = origBase
		env.OCIProxyAllowHTTP = origAllowHTTP
	})
	client, err := newOgenProxyClient()
	require.NoError(t, err)
	svc.wrapper = &ociProxyWrapper{svc: svc, proxyClient: client}
	return svc
}

func TestOCIProxyWrapper_DeleteSecret(t *testing.T) {
	const secretID = "ocid1.vaultsecret.oc1..todelete"

	t.Run("active secret schedules deletion", func(t *testing.T) {
		var scheduleCalled bool
		vaultDisp := &mockHTTPDispatcher{doFunc: func(*http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{"id":"`+secretID+`","lifecycleState":"ACTIVE"}`), nil
		}}
		svc := newProxyDeleteSvc(t, newTestOciServices(t, vaultDisp, nil), func(w http.ResponseWriter, r *http.Request) {
			require.Contains(t, r.URL.Path, "scheduleDeletion")
			scheduleCalled = true
			writeProxyJSON(w, http.StatusOK, map[string]any{})
		})
		require.NoError(t, svc.DeleteSecret(secretID))
		assert.True(t, scheduleCalled)
	})

	t.Run("404 treated as already deleted", func(t *testing.T) {
		vaultDisp := &mockHTTPDispatcher{doFunc: func(*http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusNotFound, `{"code":"NotFound","message":"missing"}`), nil
		}}
		svc := newProxyDeleteSvc(t, newTestOciServices(t, vaultDisp, nil), func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("proxy must not be called when pre-flight returns 404")
		})
		assert.NoError(t, svc.DeleteSecret(secretID))
	})

	t.Run("already pending deletion skips schedule", func(t *testing.T) {
		vaultDisp := &mockHTTPDispatcher{doFunc: func(*http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{"id":"`+secretID+`","lifecycleState":"PENDING_DELETION"}`), nil
		}}
		svc := newProxyDeleteSvc(t, newTestOciServices(t, vaultDisp, nil), func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("proxy must not be called when already pending deletion")
		})
		assert.NoError(t, svc.DeleteSecret(secretID))
	})
}

// ---------------------------------------------------------------------------
// Certificates
// ---------------------------------------------------------------------------

func TestOCIProxyWrapper_CreateCertificate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/20210224/certificates", r.URL.Path)
			writeProxyJSON(w, http.StatusOK, map[string]any{
				"id":                           "ocid1.certificate.oc1..created",
				"name":                         "my-cert",
				"compartmentId":                "ocid1.compartment.oc1..test",
				"timeCreated":                  "2026-01-01T00:00:00.000Z",
				"lifecycleState":               "CREATING",
				"configType":                   "ISSUED_BY_INTERNAL_CA",
				"issuerCertificateAuthorityId": "ocid1.certauthority.oc1..ca",
				"subject":                      map[string]string{"commonName": "admin", "organization": "Netapp"},
			})
		})

		cert, err := svc.CreateCertificate(
			"ocid1.compartment.oc1..test", "ocid1.certauthority.oc1..ca",
			"my-cert", "admin", "Netapp", []string{"*.foo.example."}, true, 30,
		)
		require.NoError(t, err)
		require.NotNil(t, cert)
		assert.Equal(t, "ocid1.certificate.oc1..created", cert.Ocid)
		assert.Equal(t, "my-cert", cert.Name)
	})

	t.Run("validation error", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("proxy must not be called for invalid input")
		})
		cert, err := svc.CreateCertificate("", "ca", "n", "cn", "Netapp", nil, false, 30)
		assert.Nil(t, cert)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
	})
}

func TestOCIProxyWrapper_DeleteCertificate(t *testing.T) {
	const certID = "ocid1.certificate.oc1..ok"

	t.Run("success", func(t *testing.T) {
		var scheduleCalled bool
		mgmtDisp := &mockHTTPDispatcher{doFunc: func(*http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{"id":"`+certID+`","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA"}`), nil
		}}
		svc := newProxyDeleteSvc(t, newTestOciServicesWithCertMgmt(t, mgmtDisp), func(w http.ResponseWriter, r *http.Request) {
			require.Contains(t, r.URL.Path, "scheduleDeletion")
			scheduleCalled = true
			writeProxyJSON(w, http.StatusOK, map[string]any{})
		})
		require.NoError(t, svc.DeleteCertificate(certID))
		assert.True(t, scheduleCalled)
	})

	t.Run("404 treated as deleted", func(t *testing.T) {
		mgmtDisp := &mockHTTPDispatcher{doFunc: func(*http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusNotFound, `{"code":"NotFound","message":"missing"}`), nil
		}}
		svc := newProxyDeleteSvc(t, newTestOciServicesWithCertMgmt(t, mgmtDisp), func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("proxy must not be called when pre-flight returns 404")
		})
		assert.NoError(t, svc.DeleteCertificate(certID))
	})
}

// ---------------------------------------------------------------------------
// DNS
// ---------------------------------------------------------------------------

func TestOCIProxyWrapper_DNS(t *testing.T) {
	const (
		zoneOCID   = "ocid1.dns-zone.oc1..testzone"
		recordName = "dns-1.example.com."
		ip         = "10.0.0.5"
	)

	origTTL := env.CloudDNSCacheTTL
	origScope := env.OCIVsaDnsScope
	defer func() {
		env.CloudDNSCacheTTL = origTTL
		env.OCIVsaDnsScope = origScope
	}()
	env.CloudDNSCacheTTL = 120
	env.OCIVsaDnsScope = "PRIVATE"

	t.Run("CreateOrUpdate success", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPut, r.Method)
			assert.Contains(t, r.URL.Path, "/20180115/zones/")
			writeProxyJSON(w, http.StatusOK, map[string]any{
				"items": []map[string]any{
					{"domain": recordName, "rdata": ip, "rtype": "A", "ttl": 120},
				},
			})
		})
		rec, err := svc.CreateOrUpdateDnsRecord(zoneOCID, recordName, ip)
		require.NoError(t, err)
		require.NotNil(t, rec)
		assert.Equal(t, recordName, rec.RecordName)
		assert.Equal(t, ip, rec.Data)
		assert.Equal(t, zoneOCID, rec.ManagedZone)
	})

	t.Run("CreateOrUpdate validation error", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("proxy must not be called")
		})
		rec, err := svc.CreateOrUpdateDnsRecord("", recordName, ip)
		assert.Nil(t, rec)
		require.Error(t, err)
	})

	t.Run("Delete success", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodDelete, r.Method)
			w.WriteHeader(http.StatusNoContent)
		})
		assert.NoError(t, svc.DeleteDnsRecord(zoneOCID, recordName))
	})

	t.Run("Delete 404 is idempotent", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			writeProxyNotFound(w)
		})
		assert.NoError(t, svc.DeleteDnsRecord(zoneOCID, recordName))
	})
}

// ---------------------------------------------------------------------------
// Object Storage PAR
// ---------------------------------------------------------------------------

func TestOCIProxyWrapper_GenerateVSAPAR(t *testing.T) {
	ctx := context.Background()
	const imagePath = "/n/controlplane-nb/b/vsaimage/o/image.tgz"

	origHost := env.OCIObjectStorageHost
	defer func() { env.OCIObjectStorageHost = origHost }()
	env.OCIObjectStorageHost = "https://objectstorage.us-ashburn-1.oraclecloud.com"

	t.Run("success", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			assert.Contains(t, r.URL.Path, "/20160918/n/controlplane-nb/b/vsaimage/p")
			writeProxyJSON(w, http.StatusOK, map[string]any{
				"accessUri": "/p/abc123/n/controlplane-nb/b/vsaimage/o/image.tgz",
			})
		})

		url, err := svc.GenerateVSAPAR(ctx, imagePath)
		require.NoError(t, err)
		assert.Equal(t,
			"https://objectstorage.us-ashburn-1.oraclecloud.com/p/abc123/n/controlplane-nb/b/vsaimage/o/image.tgz",
			url,
		)
	})

	t.Run("invalid path", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("proxy must not be called")
		})
		url, err := svc.GenerateVSAPAR(ctx, "not-a-valid-path")
		assert.Empty(t, url)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrInputValidationError)
	})

	t.Run("empty accessUri", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			writeProxyJSON(w, http.StatusOK, map[string]any{"accessUri": ""})
		})
		url, err := svc.GenerateVSAPAR(ctx, imagePath)
		assert.Empty(t, url)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
	})

	t.Run("relative accessUri without host", func(t *testing.T) {
		env.OCIObjectStorageHost = ""
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			writeProxyJSON(w, http.StatusOK, map[string]any{
				"accessUri": "/p/abc123/n/ns/b/bkt/o/obj",
			})
		})
		url, err := svc.GenerateVSAPAR(ctx, imagePath)
		assert.Empty(t, url)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIClientInitializationError)
	})

	t.Run("proxy error response", func(t *testing.T) {
		svc := newProxyWrapperServices(t, func(w http.ResponseWriter, r *http.Request) {
			writeProxyJSON(w, http.StatusBadRequest, map[string]string{
				"code":    "InvalidParameter",
				"message": "bad PAR request",
			})
		})
		url, err := svc.GenerateVSAPAR(ctx, imagePath)
		assert.Empty(t, url)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
	})
}

func TestOCIProxyResourceNotFoundCheck_proxyAPIError(t *testing.T) {
	err404 := &ociProxyAPIError{statusCode: http.StatusNotFound, code: "NotFound", message: "missing"}
	assert.NoError(t, ociProxyResourceNotFoundCheck(0, err404))
}

func TestNewOCIProxyWrapper(t *testing.T) {
	withOCIProxyBaseURL(t, "https://oci-cloud-proxy.internal")
	ctx := context.Background()
	svc := &OciServices{Ctx: ctx, Logger: util.GetLogger(ctx)}
	wrapper, err := newOCIProxyWrapper(svc)
	require.NoError(t, err)
	require.NotNil(t, wrapper)
	assert.NotNil(t, wrapper.svc)
	assert.NotNil(t, wrapper.proxyClient)
}

func TestOCIProxyWrapper_ctx_fallback(t *testing.T) {
	w := &ociProxyWrapper{svc: nil}
	assert.Equal(t, context.Background(), w.ctx())
}
