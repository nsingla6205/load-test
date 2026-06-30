package vsa

import (
	"net/http"
	"strings"
	"testing"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	oci2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	coreerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

// activeCertMetadataJSON is the GET-certificate body used wherever the rotation
// flow polls for an ACTIVE certificate or reads stage metadata.
const activeCertMetadataJSON = `{
	"id":"ocid1.certificate.oc1..rot","name":"my-cert","compartmentId":"c",
	"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA",
	"issuerCertificateAuthorityId":"ocid1.certauthority.oc1..ca",
	"subject":{"commonName":"admin","organization":"Netapp"},
	"currentVersion":{"versionNumber":3,"serialNumber":"03"}
}`

func stubPendingBundle(t *testing.T, version int64) {
	t.Helper()
	orig := oci2.GetCertificateBundleForStageWithPrivateKey
	t.Cleanup(func() { oci2.GetCertificateBundleForStageWithPrivateKey = orig })
	oci2.GetCertificateBundleForStageWithPrivateKey = func(_ *oci2.OciServices, _ string, stage certificates.GetCertificateBundleStageEnum) (*certificates.CertificateBundleWithPrivateKey, error) {
		assert.Equal(t, certificates.GetCertificateBundleStagePending, stage)
		return &certificates.CertificateBundleWithPrivateKey{
			CertificatePem: ocicommon.String("pending-leaf"),
			PrivateKeyPem:  ocicommon.String("pending-key"),
			SerialNumber:   ocicommon.String("0A"),
			VersionNumber:  ocicommon.Int64(version),
		}, nil
	}
}

// ---------------------------------------------------------------------------
// _renewOciCertificateVersionForVSACluster
// ---------------------------------------------------------------------------

func Test_RenewOciCertificateVersionForVSACluster_NilService(t *testing.T) {
	resp, err := _renewOciCertificateVersionForVSACluster(nil, "ocid1.certificate.oc1..x", 30, true)
	assert.Nil(t, resp)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_RenewOciCertificateVersionForVSACluster_EmptyOCID(t *testing.T) {
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, nil)
	resp, err := _renewOciCertificateVersionForVSACluster(svc, "", 30, true)
	assert.Nil(t, resp)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_RenewOciCertificateVersionForVSACluster_ListVersionsErrorPropagates(t *testing.T) {
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockOCIServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)
	resp, err := _renewOciCertificateVersionForVSACluster(svc, "ocid1.certificate.oc1..rot", 30, true)
	assert.Nil(t, resp)
	require.Error(t, err)
}

func Test_RenewOciCertificateVersionForVSACluster_ReusesHealthyPending(t *testing.T) {
	var updateCalled bool
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/versions") {
				// A healthy (non-FAILED) PENDING version already exists.
				return mockOCIJSONResponse(http.StatusOK, `{"items":[
					{"certificateId":"ocid1.certificate.oc1..rot","versionNumber":4,"stages":["PENDING"],"timeCreated":"2026-02-01T00:00:00Z"}
				]}`), nil
			}
			if req.Method == http.MethodPut {
				updateCalled = true
			}
			return mockOCIJSONResponse(http.StatusOK, activeCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)
	stubPendingBundle(t, 4)

	resp, err := _renewOciCertificateVersionForVSACluster(svc, "ocid1.certificate.oc1..rot", 30, true)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, updateCalled, "must reuse the existing PENDING version and not mint a new one")
	assert.Equal(t, int64(4), resp.Certificate.VersionNumber)
}

func Test_RenewOciCertificateVersionForVSACluster_MintsWhenNoPending(t *testing.T) {
	var updateCalled bool
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/versions") {
				// Only a CURRENT version exists → must mint a new PENDING one.
				return mockOCIJSONResponse(http.StatusOK, `{"items":[
					{"certificateId":"ocid1.certificate.oc1..rot","versionNumber":3,"stages":["CURRENT","LATEST"],"timeCreated":"2026-01-01T00:00:00Z"}
				]}`), nil
			}
			if req.Method == http.MethodPut {
				updateCalled = true
			}
			return mockOCIJSONResponse(http.StatusOK, activeCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)
	stubPendingBundle(t, 5)

	resp, err := _renewOciCertificateVersionForVSACluster(svc, "ocid1.certificate.oc1..rot", 30, true)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, updateCalled, "must mint a new PENDING version when none is reusable")
	assert.Equal(t, int64(5), resp.Certificate.VersionNumber)
}

// ---------------------------------------------------------------------------
// _getPendingOciCertificateResponse
// ---------------------------------------------------------------------------

func Test_GetPendingOciCertificateResponse_NilService(t *testing.T) {
	resp, err := _getPendingOciCertificateResponse(nil, "ocid1.certificate.oc1..x")
	assert.Nil(t, resp)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_GetPendingOciCertificateResponse_EmptyOCID(t *testing.T) {
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, nil)
	resp, err := _getPendingOciCertificateResponse(svc, "")
	assert.Nil(t, resp)
	require.Error(t, err)
	cerr, ok := err.(*coreerrors.CustomError)
	if assert.True(t, ok) {
		assert.Equal(t, coreerrors.ErrInputValidationError, cerr.TrackingID)
	}
}

func Test_GetPendingOciCertificateResponse_NoPendingReturnsNilNil(t *testing.T) {
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, activeCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)

	orig := oci2.GetCertificateBundleForStageWithPrivateKey
	defer func() { oci2.GetCertificateBundleForStageWithPrivateKey = orig }()
	oci2.GetCertificateBundleForStageWithPrivateKey = func(_ *oci2.OciServices, _ string, _ certificates.GetCertificateBundleStageEnum) (*certificates.CertificateBundleWithPrivateKey, error) {
		return nil, &mockOCIServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no pending"}
	}

	resp, err := _getPendingOciCertificateResponse(svc, "ocid1.certificate.oc1..rot")
	assert.NoError(t, err)
	assert.Nil(t, resp)
}

func Test_GetPendingOciCertificateResponse_Success(t *testing.T) {
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, activeCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)
	stubPendingBundle(t, 7)

	resp, err := _getPendingOciCertificateResponse(svc, "ocid1.certificate.oc1..rot")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int64(7), resp.Certificate.VersionNumber)
	assert.Equal(t, "pending-key", resp.Secret.SecretVersion.Value)
}

func Test_GetPendingOciCertificateResponse_FetchErrorPropagates(t *testing.T) {
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockOCIJSONResponse(http.StatusOK, activeCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)

	orig := oci2.GetCertificateBundleForStageWithPrivateKey
	defer func() { oci2.GetCertificateBundleForStageWithPrivateKey = orig }()
	oci2.GetCertificateBundleForStageWithPrivateKey = func(_ *oci2.OciServices, _ string, _ certificates.GetCertificateBundleStageEnum) (*certificates.CertificateBundleWithPrivateKey, error) {
		return nil, &mockOCIServiceError{statusCode: http.StatusInternalServerError, code: "InternalError", message: "boom"}
	}

	resp, err := _getPendingOciCertificateResponse(svc, "ocid1.certificate.oc1..rot")
	assert.Nil(t, resp)
	require.Error(t, err)
}

// failedCertMetadataJSON is a GET-certificate body in the terminal FAILED state,
// used to make WaitForCertificateActive return immediately with an error.
const failedCertMetadataJSON = `{
	"id":"ocid1.certificate.oc1..rot","name":"my-cert","compartmentId":"c",
	"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"FAILED","configType":"ISSUED_BY_INTERNAL_CA"
}`

const currentOnlyVersionsJSON = `{"items":[
	{"certificateId":"ocid1.certificate.oc1..rot","versionNumber":3,"stages":["CURRENT","LATEST"],"timeCreated":"2026-01-01T00:00:00Z"}
]}`

const healthyPendingVersionsJSON = `{"items":[
	{"certificateId":"ocid1.certificate.oc1..rot","versionNumber":4,"stages":["PENDING"],"timeCreated":"2026-02-01T00:00:00Z"}
]}`

func Test_RenewOciCertificateVersionForVSACluster_PreStageWaitError(t *testing.T) {
	// No reusable PENDING version → mint path; the pre-stage settle wait hits a
	// FAILED certificate and aborts before staging.
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/versions") {
				return mockOCIJSONResponse(http.StatusOK, currentOnlyVersionsJSON), nil
			}
			return mockOCIJSONResponse(http.StatusOK, failedCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)

	resp, err := _renewOciCertificateVersionForVSACluster(svc, "ocid1.certificate.oc1..rot", 30, true)
	assert.Nil(t, resp)
	require.Error(t, err)
}

func Test_RenewOciCertificateVersionForVSACluster_StagePendingError(t *testing.T) {
	// Pre-stage wait succeeds (ACTIVE) but the UpdateCertificate (PUT) that mints
	// the PENDING version fails.
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/versions") {
				return mockOCIJSONResponse(http.StatusOK, currentOnlyVersionsJSON), nil
			}
			if req.Method == http.MethodPut {
				return nil, &mockOCIServiceError{statusCode: http.StatusConflict, code: "IncorrectState", message: "stage failed"}
			}
			return mockOCIJSONResponse(http.StatusOK, activeCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)

	resp, err := _renewOciCertificateVersionForVSACluster(svc, "ocid1.certificate.oc1..rot", 30, true)
	assert.Nil(t, resp)
	require.Error(t, err)
}

func Test_RenewOciCertificateVersionForVSACluster_PostStageWaitError(t *testing.T) {
	// A healthy PENDING version is reused (no minting); the post-stage wait then
	// hits a FAILED certificate and aborts.
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/versions") {
				return mockOCIJSONResponse(http.StatusOK, healthyPendingVersionsJSON), nil
			}
			return mockOCIJSONResponse(http.StatusOK, failedCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)

	resp, err := _renewOciCertificateVersionForVSACluster(svc, "ocid1.certificate.oc1..rot", 30, true)
	assert.Nil(t, resp)
	require.Error(t, err)
}

func Test_RenewOciCertificateVersionForVSACluster_GetPendingError(t *testing.T) {
	// Reuse a healthy PENDING version, post-stage wait is ACTIVE, but reading the
	// PENDING bundle fails.
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/versions") {
				return mockOCIJSONResponse(http.StatusOK, healthyPendingVersionsJSON), nil
			}
			return mockOCIJSONResponse(http.StatusOK, activeCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)

	orig := oci2.GetCertificateBundleForStageWithPrivateKey
	defer func() { oci2.GetCertificateBundleForStageWithPrivateKey = orig }()
	oci2.GetCertificateBundleForStageWithPrivateKey = func(_ *oci2.OciServices, _ string, _ certificates.GetCertificateBundleStageEnum) (*certificates.CertificateBundleWithPrivateKey, error) {
		return nil, &mockOCIServiceError{statusCode: http.StatusInternalServerError, code: "InternalError", message: "boom"}
	}

	resp, err := _renewOciCertificateVersionForVSACluster(svc, "ocid1.certificate.oc1..rot", 30, true)
	assert.Nil(t, resp)
	require.Error(t, err)
}

func Test_RenewOciCertificateVersionForVSACluster_NoPendingAfterStaging(t *testing.T) {
	// Reuse a healthy PENDING version, post-stage wait is ACTIVE, but the PENDING
	// bundle 404s so GetPendingCertificate returns nil → "no PENDING version".
	dispatcher := &mockOCIHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/versions") {
				return mockOCIJSONResponse(http.StatusOK, healthyPendingVersionsJSON), nil
			}
			return mockOCIJSONResponse(http.StatusOK, activeCertMetadataJSON), nil
		},
	}
	svc := newTestOciServicesWithCertMgmtForHyperscaler(t, dispatcher)

	orig := oci2.GetCertificateBundleForStageWithPrivateKey
	defer func() { oci2.GetCertificateBundleForStageWithPrivateKey = orig }()
	oci2.GetCertificateBundleForStageWithPrivateKey = func(_ *oci2.OciServices, _ string, _ certificates.GetCertificateBundleStageEnum) (*certificates.CertificateBundleWithPrivateKey, error) {
		return nil, &mockOCIServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no pending"}
	}

	resp, err := _renewOciCertificateVersionForVSACluster(svc, "ocid1.certificate.oc1..rot", 30, true)
	assert.Nil(t, resp)
	require.Error(t, err)
}
