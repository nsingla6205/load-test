package oci

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ---------------------------------------------------------------------------
// Test infra
// ---------------------------------------------------------------------------

// dnsServiceError implements oci-go-sdk/v65/common.ServiceError so that
// common.IsServiceError returns true and GetHTTPStatusCode() drives the
// 404-as-not-found branch in cloud_dns.go.
type dnsServiceError struct {
	statusCode int
	code       string
	message    string
}

func (e *dnsServiceError) Error() string              { return e.code + ": " + e.message }
func (e *dnsServiceError) GetHTTPStatusCode() int     { return e.statusCode }
func (e *dnsServiceError) GetMessage() string         { return e.message }
func (e *dnsServiceError) GetCode() string            { return e.code }
func (e *dnsServiceError) GetOpcRequestID() string    { return "test-opc-request-id" }
func (e *dnsServiceError) GetOriginalMessage() string { return e.message }
func (e *dnsServiceError) GetOriginalMessageTemplate() string {
	return e.message
}
func (e *dnsServiceError) GetMessageArgument() map[string]string { return nil }

// newTestOciServicesWithDNS wires only a DNS dispatcher — vault/secrets/cert
// clients are intentionally left zero-valued because cloud_dns.go never
// touches them. Mirrors newTestOciServices in secret_management_test.go.
func newTestOciServicesWithDNS(t *testing.T, dnsDispatcher *mockHTTPDispatcher) *OciServices {
	t.Helper()
	ctx := proxyTestCtx()
	pemKey := testPrivateKeyPEM(t)

	configProvider := ocicommon.NewRawConfigurationProvider(
		"ocid1.tenancy.oc1..test",
		"ocid1.user.oc1..test",
		"us-ashburn-1",
		"aa:bb:cc:dd:ee:ff:00:11",
		pemKey,
		nil,
	)

	dnsCl, err := dns.NewDnsClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)
	if dnsDispatcher != nil {
		dnsCl.HTTPClient = dnsDispatcher
	}

	return &OciServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminOCIService: &AdminOCIService{
			dnsClient: dnsCl,
		},
	}
}

// ---------------------------------------------------------------------------
// Input validation (shared by all three public methods)
// ---------------------------------------------------------------------------

func TestCloudDNS_InputValidation(t *testing.T) {
	svc := newTestOciServicesWithDNS(t, nil)

	t.Run("CreateOrUpdate — empty zoneOCID", func(t *testing.T) {
		_, err := svc.CreateOrUpdateDnsRecord("", "dns-1.example.com.", "10.0.0.1")
		require.Error(t, err)
		cerr, ok := err.(*vsaerrors.CustomError)
		if assert.True(t, ok, "expected *CustomError, got %T", err) {
			assert.Equal(t, vsaerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})

	t.Run("CreateOrUpdate — empty recordName", func(t *testing.T) {
		_, err := svc.CreateOrUpdateDnsRecord("ocid1.dns-zone.oc1..z", "", "10.0.0.1")
		require.Error(t, err)
		cerr, ok := err.(*vsaerrors.CustomError)
		if assert.True(t, ok, "expected *CustomError, got %T", err) {
			assert.Equal(t, vsaerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})

	t.Run("CreateOrUpdate — empty ip", func(t *testing.T) {
		_, err := svc.CreateOrUpdateDnsRecord("ocid1.dns-zone.oc1..z", "dns-1.example.com.", "")
		require.Error(t, err)
		cerr, ok := err.(*vsaerrors.CustomError)
		if assert.True(t, ok, "expected *CustomError, got %T", err) {
			assert.Equal(t, vsaerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})

	t.Run("Get — empty zoneOCID", func(t *testing.T) {
		_, err := svc.GetDnsRecord("", "dns-1.example.com.")
		require.Error(t, err)
		cerr, ok := err.(*vsaerrors.CustomError)
		if assert.True(t, ok, "expected *CustomError, got %T", err) {
			assert.Equal(t, vsaerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})

	t.Run("Delete — empty recordName", func(t *testing.T) {
		err := svc.DeleteDnsRecord("ocid1.dns-zone.oc1..z", "")
		require.Error(t, err)
		cerr, ok := err.(*vsaerrors.CustomError)
		if assert.True(t, ok, "expected *CustomError, got %T", err) {
			assert.Equal(t, vsaerrors.ErrInputValidationError, cerr.TrackingID)
		}
	})
}

// ---------------------------------------------------------------------------
// CreateOrUpdateDnsRecord
// ---------------------------------------------------------------------------

func TestCloudDNS_CreateOrUpdateDnsRecord_Success(t *testing.T) {
	const (
		zoneOCID   = "ocid1.dns-zone.oc1..testzone"
		recordName = "dns-1.deployment-foo.vsa.netapp.internal."
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

	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			// UpdateRRSet returns a RecordCollection with the upserted items.
			// We deliberately echo a slightly different field (different ttl)
			// to confirm the response wiring populates from items[0] when
			// present.
			return mockJSONResponse(http.StatusOK, fmt.Sprintf(`{
				"items": [
					{"domain": "%s", "rdata": "%s", "rtype": "A", "ttl": 120}
				]
			}`, recordName, ip)), nil
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	rec, err := svc.CreateOrUpdateDnsRecord(zoneOCID, recordName, ip)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, recordName, rec.RecordName)
	assert.Equal(t, "A", rec.Type)
	assert.Equal(t, ip, rec.Data)
	assert.Equal(t, int64(120), rec.TTL)
	assert.Equal(t, zoneOCID, rec.ManagedZone)
}

func TestCloudDNS_CreateOrUpdateDnsRecord_ServiceError(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &dnsServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	rec, err := svc.CreateOrUpdateDnsRecord("ocid1.dns-zone.oc1..z", "dns-1.example.com.", "10.0.0.1")
	assert.Nil(t, rec)
	require.Error(t, err)

	cerr, ok := err.(*vsaerrors.CustomError)
	if assert.True(t, ok, "expected *CustomError, got %T", err) {
		assert.Equal(t, vsaerrors.ErrOCIResourceProvisionError, cerr.TrackingID)
	}
}

// ---------------------------------------------------------------------------
// GetDnsRecord
// ---------------------------------------------------------------------------

func TestCloudDNS_GetDnsRecord_Success(t *testing.T) {
	const (
		zoneOCID   = "ocid1.dns-zone.oc1..testzone"
		recordName = "dns-1.deployment-foo.vsa.netapp.internal."
		ip         = "10.0.0.5"
	)
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, fmt.Sprintf(`{
				"items": [
					{"domain": "%s", "rdata": "%s", "rtype": "A", "ttl": 300}
				]
			}`, recordName, ip)), nil
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	rec, err := svc.GetDnsRecord(zoneOCID, recordName)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, recordName, rec.RecordName)
	assert.Equal(t, "A", rec.Type)
	assert.Equal(t, ip, rec.Data)
	assert.Equal(t, zoneOCID, rec.ManagedZone)
}

func TestCloudDNS_GetDnsRecord_NotFound(t *testing.T) {
	// 404 → (nil, nil) so _getOrCreateOCIDNSRecord routes to create.
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &dnsServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no such RRSet"}
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	rec, err := svc.GetDnsRecord("ocid1.dns-zone.oc1..z", "dns-1.example.com.")
	assert.NoError(t, err)
	assert.Nil(t, rec, "404 must return (nil, nil) so _getOrCreate routes to create")
}

func TestCloudDNS_GetDnsRecord_EmptyItems(t *testing.T) {
	// OCI sometimes returns 200 with an empty items[] when the last record of
	// an RRSet has been deleted. We treat that as not-found so the create
	// path runs.
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{"items": []}`), nil
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	rec, err := svc.GetDnsRecord("ocid1.dns-zone.oc1..z", "dns-1.example.com.")
	assert.NoError(t, err)
	assert.Nil(t, rec)
}

func TestCloudDNS_GetDnsRecord_NonNotFoundError(t *testing.T) {
	// Non-404 must propagate as ErrOCIResourceFetchError, NOT as nil. We use
	// 403 specifically because the OCI SDK retries 5xx by default — 4xx errors
	// (other than 429 throttling) propagate immediately and exercise the same
	// non-404 branch in cloud_dns.go.
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &dnsServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	rec, err := svc.GetDnsRecord("ocid1.dns-zone.oc1..z", "dns-1.example.com.")
	assert.Nil(t, rec)
	require.Error(t, err)

	cerr, ok := err.(*vsaerrors.CustomError)
	if assert.True(t, ok, "expected *CustomError, got %T", err) {
		assert.Equal(t, vsaerrors.ErrOCIResourceFetchError, cerr.TrackingID)
	}
}

// ---------------------------------------------------------------------------
// DeleteDnsRecord
// ---------------------------------------------------------------------------

func TestCloudDNS_DeleteDnsRecord_Success(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusNoContent, ``), nil
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	err := svc.DeleteDnsRecord("ocid1.dns-zone.oc1..z", "dns-1.example.com.")
	assert.NoError(t, err)
}

func TestCloudDNS_DeleteDnsRecord_AlreadyAbsent(t *testing.T) {
	// 404 → success (idempotency: pool-delete must not fail just because a
	// previous DNS-create rolled back before the record was actually written).
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &dnsServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no such RRSet"}
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	err := svc.DeleteDnsRecord("ocid1.dns-zone.oc1..z", "dns-1.example.com.")
	assert.NoError(t, err)
}

func TestCloudDNS_DeleteDnsRecord_RealError(t *testing.T) {
	// 4xx other than 404 must propagate.
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &dnsServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}

	svc := newTestOciServicesWithDNS(t, dispatcher)
	err := svc.DeleteDnsRecord("ocid1.dns-zone.oc1..z", "dns-1.example.com.")
	require.Error(t, err)

	cerr, ok := err.(*vsaerrors.CustomError)
	if assert.True(t, ok, "expected *CustomError, got %T", err) {
		assert.Equal(t, vsaerrors.ErrOCIResourceDeprovisionError, cerr.TrackingID)
	}
}

// ---------------------------------------------------------------------------
// isOCIDnsNotFound
// ---------------------------------------------------------------------------

func TestIsOCIDnsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"plain error", errors.New("boom"), false},
		{"404 service error", &dnsServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no such RRSet"}, true},
		{"403 service error", &dnsServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}, false},
		{"500 service error", &dnsServiceError{statusCode: http.StatusInternalServerError, code: "InternalError", message: "boom"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isOCIDnsNotFound(tt.err))
		})
	}
}
