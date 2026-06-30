package oci

import (
	"net/http"
	"testing"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

// ---------------------------------------------------------------------------
// OCICertificateVersion stage helpers
// ---------------------------------------------------------------------------

func TestOCICertificateVersion_HasStage(t *testing.T) {
	v := OCICertificateVersion{Stages: []string{"CURRENT", "LATEST"}}

	assert.True(t, v.HasStage(CertVersionStageCurrent))
	assert.True(t, v.HasStage(CertVersionStageLatest))
	// case-insensitive
	assert.True(t, v.HasStage("current"))
	assert.False(t, v.HasStage(CertVersionStagePending))

	empty := OCICertificateVersion{}
	assert.False(t, empty.HasStage(CertVersionStageCurrent))
}

func TestOCICertificateVersion_StagePredicates(t *testing.T) {
	tests := []struct {
		name         string
		stages       []string
		isPending    bool
		isDeprecated bool
		isFailed     bool
	}{
		{name: "current+latest", stages: []string{"CURRENT", "LATEST"}},
		{name: "pending", stages: []string{"PENDING"}, isPending: true},
		{name: "deprecated", stages: []string{"DEPRECATED"}, isDeprecated: true},
		{name: "failed pending", stages: []string{"PENDING", "FAILED"}, isPending: true, isFailed: true},
		{name: "no stages"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := OCICertificateVersion{Stages: tt.stages}
			assert.Equal(t, tt.isPending, v.IsPending())
			assert.Equal(t, tt.isDeprecated, v.IsDeprecated())
			assert.Equal(t, tt.isFailed, v.IsFailed())
		})
	}
}

func TestOCICertificateVersion_IsScheduledForDeletion(t *testing.T) {
	assert.False(t, OCICertificateVersion{}.IsScheduledForDeletion())

	now := time.Now()
	assert.True(t, OCICertificateVersion{TimeOfDeletion: &now}.IsScheduledForDeletion())
}

// ---------------------------------------------------------------------------
// convertOCICertificateVersionSummary
// ---------------------------------------------------------------------------

func TestConvertOCICertificateVersionSummary(t *testing.T) {
	t.Run("nil summary keeps parent OCID only", func(t *testing.T) {
		out := convertOCICertificateVersionSummary("ocid1.certificate.oc1..parent", nil)
		assert.Equal(t, "ocid1.certificate.oc1..parent", out.CertificateOCID)
		assert.Zero(t, out.VersionNumber)
		assert.Nil(t, out.Stages)
	})

	t.Run("fully populated summary", func(t *testing.T) {
		created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		deletion := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		notAfter := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
		summary := &certificatesmanagement.CertificateVersionSummary{
			VersionNumber: ocicommon.Int64(7),
			VersionName:   ocicommon.String("rotation-123"),
			SerialNumber:  ocicommon.String("0A:0B"),
			Stages: []certificatesmanagement.VersionStageEnum{
				certificatesmanagement.VersionStageCurrent,
				certificatesmanagement.VersionStageLatest,
			},
			TimeCreated:    &ocicommon.SDKTime{Time: created},
			TimeOfDeletion: &ocicommon.SDKTime{Time: deletion},
			Validity: &certificatesmanagement.Validity{
				TimeOfValidityNotBefore: &ocicommon.SDKTime{Time: notBefore},
				TimeOfValidityNotAfter:  &ocicommon.SDKTime{Time: notAfter},
			},
		}
		out := convertOCICertificateVersionSummary("ocid1.certificate.oc1..parent", summary)
		assert.Equal(t, "ocid1.certificate.oc1..parent", out.CertificateOCID)
		assert.Equal(t, int64(7), out.VersionNumber)
		assert.Equal(t, "rotation-123", out.VersionName)
		assert.Equal(t, "0A:0B", out.SerialNumber)
		assert.Equal(t, []string{"CURRENT", "LATEST"}, out.Stages)
		require.NotNil(t, out.TimeCreated)
		assert.Equal(t, created, *out.TimeCreated)
		require.NotNil(t, out.TimeOfDeletion)
		assert.Equal(t, deletion, *out.TimeOfDeletion)
		require.NotNil(t, out.NotBefore)
		assert.Equal(t, notBefore, *out.NotBefore)
		require.NotNil(t, out.NotAfter)
		assert.Equal(t, notAfter, *out.NotAfter)
	})
}

// ---------------------------------------------------------------------------
// ListCertificateVersions
// ---------------------------------------------------------------------------

func TestListCertificateVersions_EmptyOCID(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)
	versions, err := svc.ListCertificateVersions("")
	assert.Nil(t, versions)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestListCertificateVersions_ServiceErrorWrapped(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	versions, err := svc.ListCertificateVersions("ocid1.certificate.oc1..x")
	assert.Nil(t, versions)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestListCertificateVersions_SinglePageSuccess(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{"items":[
				{"certificateId":"ocid1.certificate.oc1..x","versionNumber":2,"versionName":"v2","serialNumber":"02","stages":["CURRENT","LATEST"],"timeCreated":"2026-01-01T00:00:00.000Z"},
				{"certificateId":"ocid1.certificate.oc1..x","versionNumber":1,"versionName":"v1","serialNumber":"01","stages":["DEPRECATED"],"timeCreated":"2025-01-01T00:00:00.000Z"}
			]}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	versions, err := svc.ListCertificateVersions("ocid1.certificate.oc1..x")
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, int64(2), versions[0].VersionNumber)
	assert.True(t, versions[0].HasStage(CertVersionStageCurrent))
	assert.True(t, versions[1].IsDeprecated())
}

func TestListCertificateVersions_FollowsPagination(t *testing.T) {
	var calls int
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				resp := mockJSONResponse(http.StatusOK, `{"items":[
					{"certificateId":"ocid1.certificate.oc1..x","versionNumber":3,"stages":["CURRENT"],"timeCreated":"2026-01-01T00:00:00.000Z"}
				]}`)
				resp.Header.Set("opc-next-page", "page-2-token")
				return resp, nil
			}
			return mockJSONResponse(http.StatusOK, `{"items":[
				{"certificateId":"ocid1.certificate.oc1..x","versionNumber":2,"stages":["DEPRECATED"],"timeCreated":"2025-06-01T00:00:00.000Z"}
			]}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	versions, err := svc.ListCertificateVersions("ocid1.certificate.oc1..x")
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "must drain the second page")
	require.Len(t, versions, 2)
	assert.Equal(t, int64(3), versions[0].VersionNumber)
	assert.Equal(t, int64(2), versions[1].VersionNumber)
}

// ---------------------------------------------------------------------------
// RenewCertificateVersionPending
// ---------------------------------------------------------------------------

func TestRenewCertificateVersionPending_EmptyOCID(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)
	cert, err := svc.RenewCertificateVersionPending("", 30, true)
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
}

func TestRenewCertificateVersionPending_ServiceError(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusConflict, code: "IncorrectState", message: "not active"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.RenewCertificateVersionPending("ocid1.certificate.oc1..x", 30, true)
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
}

func TestRenewCertificateVersionPending_Success(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..x","name":"my-cert","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"UPDATING","configType":"ISSUED_BY_INTERNAL_CA",
				"issuerCertificateAuthorityId":"ca","subject":{"commonName":"admin"}
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.RenewCertificateVersionPending("ocid1.certificate.oc1..x", 0, false)
	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Equal(t, "ocid1.certificate.oc1..x", cert.Ocid)
	assert.Equal(t, "my-cert", cert.Name)
}

// ---------------------------------------------------------------------------
// GetPendingCertificate / getCertificateForStage
// ---------------------------------------------------------------------------

func TestGetPendingCertificate_EmptyOCID(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)
	cert, err := svc.GetPendingCertificate("")
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestGetPendingCertificate_Metadata404ReturnsNilNil(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "missing"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetPendingCertificate("ocid1.certificate.oc1..gone")
	assert.NoError(t, err)
	assert.Nil(t, cert)
}

func TestGetPendingCertificate_DeletionStateReturnsNilNil(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..pd","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"PENDING_DELETION","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	cert, err := svc.GetPendingCertificate("ocid1.certificate.oc1..pd")
	assert.NoError(t, err)
	assert.Nil(t, cert)
}

func TestGetPendingCertificate_NoPendingBundleReturnsNilNil(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..ok","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)

	orig := GetCertificateBundleForStageWithPrivateKey
	defer func() { GetCertificateBundleForStageWithPrivateKey = orig }()
	GetCertificateBundleForStageWithPrivateKey = func(_ *OciServices, _ string, _ certificates.GetCertificateBundleStageEnum) (*certificates.CertificateBundleWithPrivateKey, error) {
		return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "no pending"}
	}

	cert, err := svc.GetPendingCertificate("ocid1.certificate.oc1..ok")
	assert.NoError(t, err)
	assert.Nil(t, cert)
}

func TestGetPendingCertificate_BundleErrorPropagates(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..ok","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)

	orig := GetCertificateBundleForStageWithPrivateKey
	defer func() { GetCertificateBundleForStageWithPrivateKey = orig }()
	GetCertificateBundleForStageWithPrivateKey = func(_ *OciServices, _ string, _ certificates.GetCertificateBundleStageEnum) (*certificates.CertificateBundleWithPrivateKey, error) {
		return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
	}

	cert, err := svc.GetPendingCertificate("ocid1.certificate.oc1..ok")
	assert.Nil(t, cert)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceFetchError)
}

func TestGetPendingCertificate_Success(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..ok","name":"my-cert","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA",
				"issuerCertificateAuthorityId":"ca","subject":{"commonName":"admin","organization":"Netapp"}
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)

	orig := GetCertificateBundleForStageWithPrivateKey
	defer func() { GetCertificateBundleForStageWithPrivateKey = orig }()
	GetCertificateBundleForStageWithPrivateKey = func(_ *OciServices, _ string, stage certificates.GetCertificateBundleStageEnum) (*certificates.CertificateBundleWithPrivateKey, error) {
		assert.Equal(t, certificates.GetCertificateBundleStagePending, stage)
		return &certificates.CertificateBundleWithPrivateKey{
			CertificatePem: ocicommon.String("pending-leaf"),
			PrivateKeyPem:  ocicommon.String("pending-key"),
			SerialNumber:   ocicommon.String("AB:CD"),
			VersionNumber:  ocicommon.Int64(5),
		}, nil
	}

	cert, err := svc.GetPendingCertificate("ocid1.certificate.oc1..ok")
	require.NoError(t, err)
	require.NotNil(t, cert)
	assert.Equal(t, "pending-leaf", cert.PemCertificate)
	assert.Equal(t, "pending-key", cert.PrivateKeyPem)
	assert.Equal(t, int64(5), cert.VersionNumber)
}

// ---------------------------------------------------------------------------
// PromoteCertificateVersion
// ---------------------------------------------------------------------------

func TestPromoteCertificateVersion_Validation(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)

	t.Run("empty OCID", func(t *testing.T) {
		err := svc.PromoteCertificateVersion("", 1)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
	})
	t.Run("non-positive version", func(t *testing.T) {
		err := svc.PromoteCertificateVersion("ocid1.certificate.oc1..x", 0)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
	})
}

func TestPromoteCertificateVersion_ServiceError(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusConflict, code: "IncorrectState", message: "bad"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.PromoteCertificateVersion("ocid1.certificate.oc1..x", 3)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceProvisionError)
}

func TestPromoteCertificateVersion_Success(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..x","name":"n","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"UPDATING","configType":"ISSUED_BY_INTERNAL_CA"
			}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.PromoteCertificateVersion("ocid1.certificate.oc1..x", 3)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ScheduleCertificateVersionDeletion
// ---------------------------------------------------------------------------

func TestScheduleCertificateVersionDeletion_Validation(t *testing.T) {
	svc := newTestOciServicesWithCertMgmt(t, nil)

	t.Run("empty OCID", func(t *testing.T) {
		err := svc.ScheduleCertificateVersionDeletion("", 1)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceDeprovisionError)
	})
	t.Run("non-positive version", func(t *testing.T) {
		err := svc.ScheduleCertificateVersionDeletion("ocid1.certificate.oc1..x", -1)
		require.Error(t, err)
		assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceDeprovisionError)
	})
}

func TestScheduleCertificateVersionDeletion_Success(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return mockJSONResponse(http.StatusOK, `{}`), nil
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.ScheduleCertificateVersionDeletion("ocid1.certificate.oc1..x", 2)
	assert.NoError(t, err)
}

func TestScheduleCertificateVersionDeletion_NotFoundTreatedAsSuccess(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusNotFound, code: "NotFound", message: "gone"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.ScheduleCertificateVersionDeletion("ocid1.certificate.oc1..x", 2)
	assert.NoError(t, err)
}

func TestScheduleCertificateVersionDeletion_ConflictTreatedAsSuccess(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusConflict, code: "Conflict", message: "already scheduled"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.ScheduleCertificateVersionDeletion("ocid1.certificate.oc1..x", 2)
	assert.NoError(t, err)
}

func TestScheduleCertificateVersionDeletion_OtherErrorWrapped(t *testing.T) {
	dispatcher := &mockHTTPDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return nil, &mockServiceError{statusCode: http.StatusForbidden, code: "NotAuthorized", message: "denied"}
		},
	}
	svc := newTestOciServicesWithCertMgmt(t, dispatcher)
	err := svc.ScheduleCertificateVersionDeletion("ocid1.certificate.oc1..x", 2)
	require.Error(t, err)
	assertCustomErrTrackingID(t, err, vsaerrors.ErrOCIResourceDeprovisionError)
}
