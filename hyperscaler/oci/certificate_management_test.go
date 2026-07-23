package oci

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// newTestOciServicesWithCertMgmt wires a CertificatesManagement client backed
// by the supplied HTTP dispatcher. The certificate-read client is left
// zero-valued because tests that need it stub the GetCertificateBundleWithPrivateKey
// seam instead of hitting the polymorphic-JSON unmarshal path.
func newTestOciServicesWithCertMgmt(t *testing.T, mgmtDispatcher *mockHTTPDispatcher) *OciServices {
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

	mgmtCl, err := certificatesmanagement.NewCertificatesManagementClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)
	if mgmtDispatcher != nil {
		mgmtCl.HTTPClient = mgmtDispatcher
	}
	readCl, err := certificates.NewCertificatesClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)

	return &OciServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminOCIService: &AdminOCIService{
			certManagementClient: mgmtCl,
			certReadClient:       readCl,
		},
	}
}

func assertCustomErrTrackingID(t *testing.T, err error, want int) {
	t.Helper()
	cerr, ok := err.(*vsaerrors.CustomError)
	if assert.True(t, ok, "expected *CustomError, got %T", err) {
		assert.Equal(t, want, cerr.TrackingID)
	}
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

func TestIsCertInDeletionState(t *testing.T) {
	deletionStates := []certificatesmanagement.CertificateLifecycleStateEnum{
		certificatesmanagement.CertificateLifecycleStateSchedulingDeletion,
		certificatesmanagement.CertificateLifecycleStatePendingDeletion,
		certificatesmanagement.CertificateLifecycleStateDeleting,
		certificatesmanagement.CertificateLifecycleStateDeleted,
	}
	for _, s := range deletionStates {
		t.Run(string(s), func(t *testing.T) {
			assert.True(t, isCertInDeletionState(s))
		})
	}
	nonDeletion := []certificatesmanagement.CertificateLifecycleStateEnum{
		certificatesmanagement.CertificateLifecycleStateCreating,
		certificatesmanagement.CertificateLifecycleStateActive,
		certificatesmanagement.CertificateLifecycleStateUpdating,
		certificatesmanagement.CertificateLifecycleStateFailed,
		certificatesmanagement.CertificateLifecycleStateCancellingDeletion,
	}
	for _, s := range nonDeletion {
		t.Run("non-deletion "+string(s), func(t *testing.T) {
			assert.False(t, isCertInDeletionState(s))
		})
	}
}

func TestConvertOCICertificateMetadata(t *testing.T) {
	t.Run("nil cert returns empty struct", func(t *testing.T) {
		out := convertOCICertificateMetadata(nil)
		require.NotNil(t, out)
		assert.Equal(t, &OCICustomCertificate{}, out)
	})

	t.Run("fully populated cert", func(t *testing.T) {
		now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		cert := &certificatesmanagement.Certificate{
			Id:                           ocicommon.String("ocid1.certificate.oc1..abc"),
			Name:                         ocicommon.String("my-cert"),
			CompartmentId:                ocicommon.String("ocid1.compartment.oc1..x"),
			IssuerCertificateAuthorityId: ocicommon.String("ocid1.certauthority.oc1..ca"),
			LifecycleState:               certificatesmanagement.CertificateLifecycleStateActive,
			TimeCreated:                  &ocicommon.SDKTime{Time: now},
			Subject: &certificatesmanagement.CertificateSubject{
				CommonName:   ocicommon.String("admin"),
				Organization: ocicommon.String("Netapp"),
			},
			CurrentVersion: &certificatesmanagement.CertificateVersionSummary{
				VersionNumber: ocicommon.Int64(4),
				SerialNumber:  ocicommon.String("03:AC:FC"),
			},
		}
		out := convertOCICertificateMetadata(cert)
		assert.Equal(t, "ocid1.certificate.oc1..abc", out.Ocid)
		assert.Equal(t, "my-cert", out.Name)
		assert.Equal(t, "ocid1.compartment.oc1..x", out.CompartmentID)
		assert.Equal(t, "ocid1.certauthority.oc1..ca", out.IssuerCAOCID)
		assert.Equal(t, "ACTIVE", out.LifecycleState)
		assert.Equal(t, "admin", out.SubjectCommonName)
		assert.Equal(t, "Netapp", out.SubjectOrganization)
		assert.Equal(t, int64(4), out.VersionNumber)
		assert.Equal(t, "03:AC:FC", out.SerialNumber)
		require.NotNil(t, out.TimeCreated)
		assert.Equal(t, now, *out.TimeCreated)
	})
}

func TestConvertOCICertificateSummary(t *testing.T) {
	t.Run("nil summary returns empty struct", func(t *testing.T) {
		out := convertOCICertificateSummary(nil)
		require.NotNil(t, out)
		assert.Equal(t, &OCICustomCertificate{}, out)
	})

	t.Run("fully populated summary uses CurrentVersionSummary", func(t *testing.T) {
		now := time.Date(2025, 6, 7, 0, 0, 0, 0, time.UTC)
		summary := &certificatesmanagement.CertificateSummary{
			Id:                           ocicommon.String("ocid1.certificate.oc1..sum"),
			Name:                         ocicommon.String("sum-cert"),
			CompartmentId:                ocicommon.String("ocid1.compartment.oc1..c"),
			IssuerCertificateAuthorityId: ocicommon.String("ocid1.certauthority.oc1..ca2"),
			LifecycleState:               certificatesmanagement.CertificateLifecycleStateCreating,
			TimeCreated:                  &ocicommon.SDKTime{Time: now},
			Subject: &certificatesmanagement.CertificateSubject{
				CommonName:   ocicommon.String("cn"),
				Organization: ocicommon.String("org"),
			},
			CurrentVersionSummary: &certificatesmanagement.CertificateVersionSummary{
				VersionNumber: ocicommon.Int64(9),
				SerialNumber:  ocicommon.String("AA:BB"),
			},
		}
		out := convertOCICertificateSummary(summary)
		assert.Equal(t, "ocid1.certificate.oc1..sum", out.Ocid)
		assert.Equal(t, "sum-cert", out.Name)
		assert.Equal(t, "ocid1.compartment.oc1..c", out.CompartmentID)
		assert.Equal(t, "ocid1.certauthority.oc1..ca2", out.IssuerCAOCID)
		assert.Equal(t, "CREATING", out.LifecycleState)
		assert.Equal(t, "cn", out.SubjectCommonName)
		assert.Equal(t, "org", out.SubjectOrganization)
		assert.Equal(t, int64(9), out.VersionNumber)
		assert.Equal(t, "AA:BB", out.SerialNumber)
		require.NotNil(t, out.TimeCreated)
		assert.Equal(t, now, *out.TimeCreated)
	})
}

func TestMergeCertificateBundleIntoCustomCertificate(t *testing.T) {
	t.Run("nil cert no-op", func(t *testing.T) {
		mergeCertificateBundleIntoCustomCertificate(nil, &certificates.CertificateBundleWithPrivateKey{})
	})
	t.Run("nil bundle no-op", func(t *testing.T) {
		cert := &OCICustomCertificate{Name: "before"}
		mergeCertificateBundleIntoCustomCertificate(cert, nil)
		assert.Equal(t, "before", cert.Name)
		assert.Empty(t, cert.PemCertificate)
	})

	t.Run("populates pem, serial, version and validity", func(t *testing.T) {
		notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		notAfter := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
		bundleCreated := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		bundle := &certificates.CertificateBundleWithPrivateKey{
			CertificatePem: ocicommon.String("leaf-pem"),
			CertChainPem:   ocicommon.String("chain-pem"),
			PrivateKeyPem:  ocicommon.String("key-pem"),
			SerialNumber:   ocicommon.String("01:02"),
			VersionNumber:  ocicommon.Int64(5),
			TimeCreated:    &ocicommon.SDKTime{Time: bundleCreated},
			Validity: &certificates.Validity{
				TimeOfValidityNotBefore: &ocicommon.SDKTime{Time: notBefore},
				TimeOfValidityNotAfter:  &ocicommon.SDKTime{Time: notAfter},
			},
		}
		cert := &OCICustomCertificate{}
		mergeCertificateBundleIntoCustomCertificate(cert, bundle)
		assert.Equal(t, "leaf-pem", cert.PemCertificate)
		assert.Equal(t, "chain-pem", cert.PemCertificateChain)
		assert.Equal(t, "key-pem", cert.PrivateKeyPem)
		assert.Equal(t, "01:02", cert.SerialNumber)
		assert.Equal(t, int64(5), cert.VersionNumber)
		require.NotNil(t, cert.NotBefore)
		assert.Equal(t, notBefore, *cert.NotBefore)
		require.NotNil(t, cert.NotAfter)
		assert.Equal(t, notAfter, *cert.NotAfter)
		require.NotNil(t, cert.TimeCreated)
		assert.Equal(t, bundleCreated, *cert.TimeCreated)
	})

	t.Run("does not clobber TimeCreated when metadata already set", func(t *testing.T) {
		metaCreated := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
		bundleCreated := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		cert := &OCICustomCertificate{TimeCreated: &metaCreated}
		bundle := &certificates.CertificateBundleWithPrivateKey{
			TimeCreated: &ocicommon.SDKTime{Time: bundleCreated},
		}
		mergeCertificateBundleIntoCustomCertificate(cert, bundle)
		require.NotNil(t, cert.TimeCreated)
		assert.Equal(t, metaCreated, *cert.TimeCreated)
	})
}

// ---------------------------------------------------------------------------
// CreateCertificate
// ---------------------------------------------------------------------------

func TestCreateCertificate_InputValidation(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)
	cases := []struct {
		name                                      string
		compartment, issuer, certName, commonName string
	}{
		{"empty compartment", "", "ca", "n", "cn"},
		{"empty issuer", "comp", "", "n", "cn"},
		{"empty name", "comp", "ca", "", "cn"},
		{"empty commonName", "comp", "ca", "n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert, err := svc.CreateCertificate(tc.compartment, tc.issuer, tc.certName, tc.commonName, "Netapp", nil, false, 30)
			assert.Nil(t, cert)
			require.Error(t, err)
			assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
		})
	}
}

func TestCreateCertificate_Success(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..created",
				"name":           "my-cert",
				"compartmentId":  "ocid1.compartment.oc1..test",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "CREATING",
				"configType":     "ISSUED_BY_INTERNAL_CA",
				"issuerCertificateAuthorityId": "ocid1.certauthority.oc1..ca",
				"subject":        {"commonName":"admin","organization":"Netapp"}
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.CreateCertificate("ocid1.compartment.oc1..test", "ocid1.certauthority.oc1..ca",
		"my-cert", "admin", "Netapp", []string{"*.foo.example.", ""}, true, 30)
	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Equal(t, "ocid1.certificate.oc1..created", cert.Ocid)
	assert.Equal(t, "my-cert", cert.Name)
	assert.Equal(t, CertLifecycleStateCreating, cert.LifecycleState)
	assert.Equal(t, "admin", cert.SubjectCommonName)
	assert.Equal(t, "Netapp", cert.SubjectOrganization)
	assert.Equal(t, "ocid1.certauthority.oc1..ca", cert.IssuerCAOCID)
}

func TestCreateCertificate_IssuerBackfillWhenOmitted(t *testing.T) {
	// Returned cert body omits issuerCertificateAuthorityId — the activity
	// must backfill it from the requested issuerCAOCID.
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..no-issuer",
				"name":           "n",
				"compartmentId":  "c",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "CREATING",
				"configType":     "ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.CreateCertificate("c", "ocid1.certauthority.oc1..fallback", "n", "cn", "Netapp", nil, false, 30)
	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Equal(t, "ocid1.certauthority.oc1..fallback", cert.IssuerCAOCID)
}

func TestCreateCertificate_ServiceError(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.CreateCertificate("c", "ca", "n", "cn", "Netapp", nil, false, 30)
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
}

// ---------------------------------------------------------------------------
// GetCertificate
// ---------------------------------------------------------------------------

func TestGetCertificate_EmptyOCID(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)
	cert, err := svc.GetCertificate("")
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestGetCertificate_Metadata404ReturnsNilNil(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotAuthorizedOrNotFound", message: "missing"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetCertificate("ocid1.certificate.oc1..gone")
	assert.NoError(t, err)
	assert.Nil(t, cert)
}

func TestGetCertificate_MetadataServiceErrorPropagates(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetCertificate("ocid1.certificate.oc1..x")
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestGetCertificate_CertInDeletionStateReturnsNilNil(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..pending",
				"name":           "n",
				"compartmentId":  "c",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "PENDING_DELETION",
				"configType":     "ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetCertificate("ocid1.certificate.oc1..pending")
	assert.NoError(t, err)
	assert.Nil(t, cert)
}

func TestGetCertificate_BundleNotFoundReturnsNilNil(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..creating",
				"name":           "n",
				"compartmentId":  "c",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "CREATING",
				"configType":     "ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)

	origBundle := GetCertificateBundleWithPrivateKey
	defer func() { GetCertificateBundleWithPrivateKey = origBundle }()
	GetCertificateBundleWithPrivateKey = func(_ *OciServices, _ string) (*certificates.CertificateBundleWithPrivateKey, error) {
		return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no bundle yet"}
	}

	cert, err := svc.GetCertificate("ocid1.certificate.oc1..creating")
	assert.NoError(t, err)
	assert.Nil(t, cert)
}

func TestGetCertificate_BundleNonNotFoundErrorPropagates(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..ok",
				"name":           "n",
				"compartmentId":  "c",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "ACTIVE",
				"configType":     "ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)

	origBundle := GetCertificateBundleWithPrivateKey
	defer func() { GetCertificateBundleWithPrivateKey = origBundle }()
	GetCertificateBundleWithPrivateKey = func(_ *OciServices, _ string) (*certificates.CertificateBundleWithPrivateKey, error) {
		return nil, errors.New("transport boom")
	}

	cert, err := svc.GetCertificate("ocid1.certificate.oc1..ok")
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestGetCertificate_Success(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..ok",
				"name":           "my-cert",
				"compartmentId":  "c",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "ACTIVE",
				"configType":     "ISSUED_BY_INTERNAL_CA",
				"issuerCertificateAuthorityId": "ca",
				"subject":        {"commonName":"admin","organization":"Netapp"},
				"currentVersion": {"versionNumber":3, "serialNumber":"02:00"}
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)

	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	origBundle := GetCertificateBundleWithPrivateKey
	defer func() { GetCertificateBundleWithPrivateKey = origBundle }()
	GetCertificateBundleWithPrivateKey = func(_ *OciServices, _ string) (*certificates.CertificateBundleWithPrivateKey, error) {
		return &certificates.CertificateBundleWithPrivateKey{
			CertificatePem: ocicommon.String("leaf"),
			CertChainPem:   ocicommon.String("chain"),
			PrivateKeyPem:  ocicommon.String("key"),
			SerialNumber:   ocicommon.String("FF:00"),
			VersionNumber:  ocicommon.Int64(3),
			Validity: &certificates.Validity{
				TimeOfValidityNotBefore: &ocicommon.SDKTime{Time: notBefore},
				TimeOfValidityNotAfter:  &ocicommon.SDKTime{Time: notAfter},
			},
		}, nil
	}

	cert, err := svc.GetCertificate("ocid1.certificate.oc1..ok")
	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Equal(t, "ocid1.certificate.oc1..ok", cert.Ocid)
	assert.Equal(t, "my-cert", cert.Name)
	assert.Equal(t, "leaf", cert.PemCertificate)
	assert.Equal(t, "chain", cert.PemCertificateChain)
	assert.Equal(t, "key", cert.PrivateKeyPem)
	assert.Equal(t, "FF:00", cert.SerialNumber)
	assert.Equal(t, int64(3), cert.VersionNumber)
	require.NotNil(t, cert.NotBefore)
	require.NotNil(t, cert.NotAfter)
}

// ---------------------------------------------------------------------------
// DeleteCertificate
// ---------------------------------------------------------------------------

func TestDeleteCertificate_EmptyOCID(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)
	err := svc.DeleteCertificate("")
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceDeprovisionError)
}

func TestDeleteCertificate_PreFlight404TreatedAsDeleted(t *testing.T) {
	var scheduleCalled bool
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "scheduleDeletion") {
				scheduleCalled = true
				return mockJSONResponse(http.StatusOK, `{}`), nil
			}
			return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "missing"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.DeleteCertificate("ocid1.certificate.oc1..gone")
	assert.NoError(t, err)
	assert.False(t, scheduleCalled, "schedule must not run after 404 pre-flight")
}

func TestDeleteCertificate_AlreadyInDeletionStateSkipsSchedule(t *testing.T) {
	var scheduleCalled bool
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "scheduleDeletion") {
				scheduleCalled = true
				return mockJSONResponse(http.StatusOK, `{}`), nil
			}
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..pd",
				"name":           "n",
				"compartmentId":  "c",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "PENDING_DELETION",
				"configType":     "ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.DeleteCertificate("ocid1.certificate.oc1..pd")
	assert.NoError(t, err)
	assert.False(t, scheduleCalled)
}

func TestDeleteCertificate_ScheduleFails(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "scheduleDeletion") {
				return nil, &mockServiceError{statusCode: http.StatusConflict, code: "Conflict", message: "race"}
			}
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..ok",
				"name":           "n",
				"compartmentId":  "c",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "ACTIVE",
				"configType":     "ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.DeleteCertificate("ocid1.certificate.oc1..ok")
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceDeprovisionError)
}

func TestDeleteCertificate_PreFlightTransportErrorPropagates(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.DeleteCertificate("ocid1.certificate.oc1..err")
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestDeleteCertificate_Success(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "scheduleDeletion") {
				return mockJSONResponse(http.StatusOK, `{}`), nil
			}
			return mockJSONResponse(http.StatusOK, `{
				"id":             "ocid1.certificate.oc1..ok",
				"name":           "n",
				"compartmentId":  "c",
				"timeCreated":    "2026-01-01T00:00:00.000Z",
				"lifecycleState": "ACTIVE",
				"configType":     "ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.DeleteCertificate("ocid1.certificate.oc1..ok")
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// GetCertificateByName
// ---------------------------------------------------------------------------

func TestGetCertificateByName_InputValidation(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)
	t.Run("empty name", func(t *testing.T) {
		cert, err := svc.GetCertificateByName("", "c")
		assert.Nil(t, cert)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
	})
	t.Run("empty compartment", func(t *testing.T) {
		cert, err := svc.GetCertificateByName("n", "")
		assert.Nil(t, cert)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
	})
}

func TestGetCertificateByName_ListFailureWrapped(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetCertificateByName("n", "c")
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestGetCertificateByName_NoMatchReturnsNilNil(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{"items": []}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetCertificateByName("missing", "c")
	assert.NoError(t, err)
	assert.Nil(t, cert)
}

func TestGetCertificateByName_SkipsDeletionStateAndPicksUsable(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{"items":[
				{"id":"ocid1.certificate.oc1..pd","name":"my-cert","compartmentId":"c","timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"PENDING_DELETION","configType":"ISSUED_BY_INTERNAL_CA"},
				{"id":"ocid1.certificate.oc1..active","name":"my-cert","compartmentId":"c","timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA","issuerCertificateAuthorityId":"ca","currentVersionSummary":{"versionNumber":2,"serialNumber":"01"}}
			]}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetCertificateByName("my-cert", "c")
	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Equal(t, "ocid1.certificate.oc1..active", cert.Ocid)
	assert.Equal(t, int64(2), cert.VersionNumber)
	assert.Equal(t, "01", cert.SerialNumber)
}

func TestGetCertificateByName_NameMismatchSkipped(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{"items":[
				{"id":"ocid1.certificate.oc1..diff","name":"other-cert","compartmentId":"c","timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA"}
			]}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetCertificateByName("wanted-cert", "c")
	assert.NoError(t, err)
	assert.Nil(t, cert)
}

// ---------------------------------------------------------------------------
// WaitForCertificateActive
// ---------------------------------------------------------------------------

func TestWaitForCertificateActive_EmptyOCID(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)
	err := svc.WaitForCertificateActive("", 0)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestWaitForCertificateActive_ActiveImmediately(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..ok","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.WaitForCertificateActive("ocid1.certificate.oc1..ok", 5*time.Second)
	assert.NoError(t, err)
}

func TestWaitForCertificateActive_Failed(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..bad","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"FAILED","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.WaitForCertificateActive("ocid1.certificate.oc1..bad", 5*time.Second)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
	require.NotNil(t, errors.Unwrap(err))
	assert.Contains(t, errors.Unwrap(err).Error(), "terminal state")
}

func TestWaitForCertificateActive_DeletionStatesShortCircuit(t *testing.T) {
	for _, state := range []string{"DELETING", "DELETED", "PENDING_DELETION", "SCHEDULING_DELETION"} {
		t.Run(state, func(t *testing.T) {
			dispatcher := &mockHTTPDispatcher{
				doFunc: func(req *http.Request) (*http.Response, error) {
					return mockJSONResponse(http.StatusOK, `{
						"id":"ocid1.certificate.oc1..x","name":"n","compartmentId":"c",
						"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"`+state+`","configType":"ISSUED_BY_INTERNAL_CA"
					}`), nil
				},
			}
			svc := newTestOciServicesWithCertMgmt(t, dispatcher)
			err := svc.WaitForCertificateActive("ocid1.certificate.oc1..x", 5*time.Second)
			require.Error(t, err)
			assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
			require.NotNil(t, errors.Unwrap(err))
			assert.Contains(t, errors.Unwrap(err).Error(), "being deleted")
		})
	}
}

func TestWaitForCertificateActive_GetFailurePropagates(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.WaitForCertificateActive("ocid1.certificate.oc1..x", 5*time.Second)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestWaitForCertificateActive_TimeoutWhileStuckInCreating(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..creating","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"CREATING","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.WaitForCertificateActive("ocid1.certificate.oc1..creating", time.Nanosecond)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
	require.NotNil(t, errors.Unwrap(err))
	assert.Contains(t, errors.Unwrap(err).Error(), "timeout")
}

// withFastCertPolling shrinks the package-private poll interval for the
// duration of the test so multi-poll scenarios complete in milliseconds rather
// than 3s per iteration. Restores the original value on cleanup. Tests using
// this helper must not run with t.Parallel — see the comment on
// defaultCertActivePollInterval for the rationale.
func withFastCertPolling(t *testing.T, interval time.Duration) {
	t.Helper()
	orig := defaultCertActivePollInterval
	defaultCertActivePollInterval = interval
	t.Cleanup(func() { defaultCertActivePollInterval = orig })
}

// TestWaitForCertificateActive_PollsThroughCreatingThenActive exercises the
// multi-poll path: the cert reports CREATING twice before transitioning to
// ACTIVE. Without the defaultCertActivePollInterval test seam this would take
// ~6s; with the seam it completes in a few ms.
func TestWaitForCertificateActive_PollsThroughCreatingThenActive(t *testing.T) {
	withFastCertPolling(t, time.Millisecond)

	var calls int
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			calls++
			state := "CREATING"
			if calls >= 3 {
				state = "ACTIVE"
			}
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..multipoll","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"`+state+`","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.WaitForCertificateActive("ocid1.certificate.oc1..multipoll", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 3, calls, "must poll past two CREATING responses before observing ACTIVE")
}

// TestWaitForCertificateActive_UpdatingTreatedAsTransient locks in the
// documented behaviour that UPDATING is treated identically to CREATING —
// keep polling, do not error. Guards against future regressions where the
// explicit case is removed and UPDATING accidentally falls into the default
// branch (which would only log a WARN, but still mask the intent).
func TestWaitForCertificateActive_UpdatingTreatedAsTransient(t *testing.T) {
	withFastCertPolling(t, time.Millisecond)

	var calls int
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			calls++
			state := "UPDATING"
			if calls >= 2 {
				state = "ACTIVE"
			}
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..updating","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"`+state+`","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.WaitForCertificateActive("ocid1.certificate.oc1..updating", 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "UPDATING must be polled through to ACTIVE")
}
