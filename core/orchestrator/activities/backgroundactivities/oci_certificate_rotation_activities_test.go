package backgroundactivities

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificates"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/secrets"
	"github.com/oracle/oci-go-sdk/v65/vault"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	wfe "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	workflowtemporal "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type ociActDispatcher struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *ociActDispatcher) Do(req *http.Request) (*http.Response, error) { return m.doFunc(req) }

type ociActServiceError struct {
	statusCode int
	code       string
	message    string
}

func (e *ociActServiceError) GetHTTPStatusCode() int  { return e.statusCode }
func (e *ociActServiceError) GetMessage() string      { return e.message }
func (e *ociActServiceError) GetCode() string         { return e.code }
func (e *ociActServiceError) GetOpcRequestID() string { return "" }
func (e *ociActServiceError) Error() string {
	return fmt.Sprintf("Service error: %s (status %d)", e.message, e.statusCode)
}

func ociActJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type":   []string{"application/json"},
			"Opc-Request-Id": []string{"test-req-id"},
		},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: &http.Request{URL: &url.URL{Path: "/mock"}},
	}
}

// newOciActService builds an OciServices whose certificate-management client is
// backed by the supplied dispatcher. Pass nil when the test's code path never
// calls a live OCI method (the dispatcher will never be invoked).
func newOciActService(t *testing.T, dispatcher *ociActDispatcher) *oci.OciServices {
	t.Helper()
	ctx := context.Background()

	der := x509.MarshalPKCS1PrivateKey(testPrivateKey)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))

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
	if dispatcher != nil {
		mgmtCl.HTTPClient = dispatcher
	}
	readCl, err := certificates.NewCertificatesClientWithConfigurationProvider(configProvider)
	require.NoError(t, err)

	return &oci.OciServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminOCIService: oci.NewAdminOCIServiceWithCertClients(
			vault.VaultsClient{},
			secrets.SecretsClient{},
			mgmtCl,
			readCl,
		),
	}
}

// overrideOCIServiceSeam swaps the package-level getOCIServiceForCertRotation
// seam and restores it on cleanup.
func overrideOCIServiceSeam(t *testing.T, svc *oci.OciServices, err error) {
	t.Helper()
	orig := getOCIServiceForCertRotation
	t.Cleanup(func() { getOCIServiceForCertRotation = orig })
	getOCIServiceForCertRotation = func(_ context.Context) (*oci.OciServices, error) {
		return svc, err
	}
}

func eligibleOCIPool(uuid string) *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{UUID: uuid, ID: 1},
		State:          "READY",
		DeploymentName: "deployment-foo",
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: env.USER_CERTIFICATE,
			ExternalCertificate: &datamodel.ExternalCredRef{
				ExternalIdentifier: "ocid1.certificate.oc1..rot",
				Name:               "deployment-foo-cert",
				Version:            3,
			},
		},
	}
}

func eligibleOCIPoolView(uuid string) *datamodel.PoolView {
	return &datamodel.PoolView{Pool: *eligibleOCIPool(uuid)}
}

func certRespForVersion(version int64) *hyperscalermodels.CustomCertificateResponse {
	return &hyperscalermodels.CustomCertificateResponse{
		Certificate: &hyperscalermodels.CustomCertificate{
			CertificateID:     "ocid1.certificate.oc1..rot",
			Name:              "deployment-foo-cert",
			VersionNumber:     version,
			SubjectCommonName: "deployment-foo_admin",
			PemCertificate:    "leaf-pem",
		},
		Secret: &hyperscalermodels.CustomSecret{
			SecretVersion: &hyperscalermodels.CustomSecretVersion{Value: "key-pem"},
		},
	}
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

func TestOciPoolEligibleForRotation(t *testing.T) {
	tests := []struct {
		name     string
		pool     *datamodel.Pool
		expected bool
	}{
		{name: "nil pool", pool: nil, expected: false},
		{name: "nil credentials", pool: &datamodel.Pool{State: "READY"}, expected: false},
		{
			name:     "creating state",
			pool:     func() *datamodel.Pool { p := eligibleOCIPool("u"); p.State = "CREATING"; return p }(),
			expected: false,
		},
		{
			name:     "deleting state",
			pool:     func() *datamodel.Pool { p := eligibleOCIPool("u"); p.State = "DELETING"; return p }(),
			expected: false,
		},
		{
			name:     "upgrading state",
			pool:     func() *datamodel.Pool { p := eligibleOCIPool("u"); p.State = "UPGRADING"; return p }(),
			expected: false,
		},
		{
			name: "not certificate auth",
			pool: func() *datamodel.Pool {
				p := eligibleOCIPool("u")
				p.PoolCredentials.AuthType = env.USERNAME_PWD
				return p
			}(),
			expected: false,
		},
		{
			name: "no external certificate",
			pool: func() *datamodel.Pool {
				p := eligibleOCIPool("u")
				p.PoolCredentials.ExternalCertificate = nil
				return p
			}(),
			expected: false,
		},
		{
			name: "empty external identifier",
			pool: func() *datamodel.Pool {
				p := eligibleOCIPool("u")
				p.PoolCredentials.ExternalCertificate.ExternalIdentifier = ""
				return p
			}(),
			expected: false,
		},
		{name: "eligible ready pool", pool: eligibleOCIPool("u"), expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ociPoolEligibleForRotation(tt.pool))
		})
	}
}

func TestOciRotationContext(t *testing.T) {
	t.Run("nil pool context", func(t *testing.T) {
		_, _, _, err := ociRotationContext(nil)
		require.Error(t, err)
	})
	t.Run("nil pool", func(t *testing.T) {
		_, _, _, err := ociRotationContext(&PoolContext{})
		require.Error(t, err)
	})
	t.Run("ineligible pool", func(t *testing.T) {
		p := eligibleOCIPool("u")
		p.State = "DELETING"
		_, _, _, err := ociRotationContext(&PoolContext{Pool: p})
		require.Error(t, err)
	})
	t.Run("eligible pool returns ocid and name", func(t *testing.T) {
		pool, ocid, name, err := ociRotationContext(&PoolContext{Pool: eligibleOCIPool("u")})
		require.NoError(t, err)
		assert.Equal(t, "u", pool.UUID)
		assert.Equal(t, "ocid1.certificate.oc1..rot", ocid)
		assert.Equal(t, "deployment-foo-cert", name)
	})
}

func TestOntapServerCertName(t *testing.T) {
	assert.Equal(t, "ocid1.certificate.oc1..rot-v3", ontapServerCertName("ocid1.certificate.oc1..rot", 3))
	assert.Equal(t, "abc-v0", ontapServerCertName("abc", 0))
}

func TestOciRotationUsername(t *testing.T) {
	t.Run("uses explicit username", func(t *testing.T) {
		p := eligibleOCIPool("u")
		p.PoolCredentials.Username = "custom_admin"
		assert.Equal(t, "custom_admin", ociRotationUsername(p))
	})
	t.Run("falls back to deployment name", func(t *testing.T) {
		p := eligibleOCIPool("u")
		p.PoolCredentials.Username = ""
		assert.Equal(t, "deployment-foo_admin", ociRotationUsername(p))
	})
}

func TestOciEndpointMap(t *testing.T) {
	nodes := []*datamodel.Node{
		{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"},
		{EndpointAddress: "10.0.0.2", HostDNSName: "node-2.example.com"},
		nil,
		{EndpointAddress: "", HostDNSName: "skipped"},
	}
	out := ociEndpointMap(nodes)
	assert.Equal(t, map[string]string{
		"10.0.0.1": "node-1.example.com",
		"10.0.0.2": "node-2.example.com",
	}, out)
}

// ---------------------------------------------------------------------------
// GetPoolContext
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_GetPoolContext(t *testing.T) {
	ctx := context.Background()
	poolUUID := "pool-uuid"

	t.Run("success", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{eligibleOCIPoolView(poolUUID)}, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		pc, err := a.GetPoolContext(ctx, poolUUID)
		require.NoError(t, err)
		require.NotNil(t, pc)
		assert.Equal(t, poolUUID, pc.PoolUUID)
		assert.Equal(t, poolUUID, pc.Pool.UUID)
		mockSE.AssertExpectations(t)
	})

	t.Run("not found", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{}, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		pc, err := a.GetPoolContext(ctx, poolUUID)
		assert.Nil(t, pc)
		require.Error(t, err)
	})

	t.Run("db error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return(nil, errors.New("db down")).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		pc, err := a.GetPoolContext(ctx, poolUUID)
		assert.Nil(t, pc)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// ListOciPoolsWithCertificateAuth
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_ListOciPoolsWithCertificateAuth(t *testing.T) {
	ctx := context.Background()

	t.Run("full batch reports hasMore", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		views := []*datamodel.PoolView{eligibleOCIPoolView("a"), eligibleOCIPoolView("b")}
		mockSE.On("ListPoolsWithFilterAndPaginationOrderedByUUID", ctx,
			mock.AnythingOfType("*utils.Filter"), mock.AnythingOfType("*utils.Pagination")).
			Return(views, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		res, err := a.ListOciPoolsWithCertificateAuth(ctx, 0, 2)
		require.NoError(t, err)
		require.Len(t, res.Pools, 2)
		assert.True(t, res.HasMore)
	})

	t.Run("partial batch ends pagination", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		views := []*datamodel.PoolView{eligibleOCIPoolView("a")}
		mockSE.On("ListPoolsWithFilterAndPaginationOrderedByUUID", ctx,
			mock.AnythingOfType("*utils.Filter"), mock.AnythingOfType("*utils.Pagination")).
			Return(views, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		res, err := a.ListOciPoolsWithCertificateAuth(ctx, 0, 2)
		require.NoError(t, err)
		require.Len(t, res.Pools, 1)
		assert.False(t, res.HasMore)
	})

	t.Run("db error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPoolsWithFilterAndPaginationOrderedByUUID", ctx,
			mock.AnythingOfType("*utils.Filter"), mock.AnythingOfType("*utils.Pagination")).
			Return(nil, errors.New("db down")).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		res, err := a.ListOciPoolsWithCertificateAuth(ctx, 0, 2)
		assert.Nil(t, res)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// OciONTAPCertificateNeedsRotation (early-return paths)
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_OciONTAPCertificateNeedsRotation(t *testing.T) {
	ctx := context.Background()
	poolUUID := "pool-uuid"

	t.Run("pool not found returns false", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{}, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		needs, err := a.OciONTAPCertificateNeedsRotation(ctx, poolUUID)
		require.NoError(t, err)
		assert.False(t, needs)
	})

	t.Run("ineligible pool returns false", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		view := eligibleOCIPoolView(poolUUID)
		view.Pool.PoolCredentials.ExternalCertificate = nil
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{view}, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		needs, err := a.OciONTAPCertificateNeedsRotation(ctx, poolUUID)
		require.NoError(t, err)
		assert.False(t, needs)
	})

	t.Run("db error propagates", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return(nil, errors.New("db down")).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		_, err := a.OciONTAPCertificateNeedsRotation(ctx, poolUUID)
		require.Error(t, err)
	})

	t.Run("get oci service error propagates", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{eligibleOCIPoolView(poolUUID)}, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}
		overrideOCIServiceSeam(t, nil, errors.New("no oci service"))

		_, err := a.OciONTAPCertificateNeedsRotation(ctx, poolUUID)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// CleanupAndStageOciONTAPCertificateVersion
// ---------------------------------------------------------------------------

func overrideRenewSeam(t *testing.T, fn func() (*hyperscalermodels.CustomCertificateResponse, error)) {
	t.Helper()
	orig := renewOciCertificateVersion
	t.Cleanup(func() { renewOciCertificateVersion = orig })
	renewOciCertificateVersion = func(_ *oci.OciServices, _ string, _ int, _ bool) (*hyperscalermodels.CustomCertificateResponse, error) {
		return fn()
	}
}

func overridePendingSeam(t *testing.T, fn func() (*hyperscalermodels.CustomCertificateResponse, error)) {
	t.Helper()
	orig := getPendingOciCertificateBundle
	t.Cleanup(func() { getPendingOciCertificateBundle = orig })
	getPendingOciCertificateBundle = func(_ *oci.OciServices, _ string) (*hyperscalermodels.CustomCertificateResponse, error) {
		return fn()
	}
}

// emptyVersionsDispatcher returns an empty version list so sweepDeprecatedVersions
// is a clean no-op.
func emptyVersionsDispatcher() *ociActDispatcher {
	return &ociActDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return ociActJSONResponse(http.StatusOK, `{"items":[]}`), nil
		},
	}
}

func TestOciCertificateRotationActivity_CleanupAndStageOciONTAPCertificateVersion(t *testing.T) {
	ctx := context.Background()

	t.Run("ineligible pool returns error before service", func(t *testing.T) {
		a := &OciCertificateRotationActivity{}
		p := eligibleOCIPool("u")
		p.State = "DELETING"
		_, err := a.CleanupAndStageOciONTAPCertificateVersion(ctx, &PoolContext{Pool: p})
		require.Error(t, err)
	})

	t.Run("success returns staged version", func(t *testing.T) {
		a := &OciCertificateRotationActivity{}
		overrideOCIServiceSeam(t, newOciActService(t, emptyVersionsDispatcher()), nil)
		overrideRenewSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})

		version, err := a.CleanupAndStageOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")})
		require.NoError(t, err)
		assert.Equal(t, int64(4), version)
	})

	t.Run("renew error propagates", func(t *testing.T) {
		a := &OciCertificateRotationActivity{}
		overrideOCIServiceSeam(t, newOciActService(t, emptyVersionsDispatcher()), nil)
		overrideRenewSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return nil, errors.New("renew failed")
		})

		_, err := a.CleanupAndStageOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")})
		require.Error(t, err)
	})

	t.Run("get oci service error propagates", func(t *testing.T) {
		a := &OciCertificateRotationActivity{}
		overrideOCIServiceSeam(t, nil, errors.New("no service"))

		_, err := a.CleanupAndStageOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")})
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// fetchPendingForVersion
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_fetchPendingForVersion(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}
	svc := newOciActService(t, nil)

	t.Run("seam error propagates", func(t *testing.T) {
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return nil, errors.New("read failed")
		})
		_, err := a.fetchPendingForVersion(ctx, svc, "ocid", 4)
		require.Error(t, err)
	})

	t.Run("nil bundle is not found", func(t *testing.T) {
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return nil, nil
		})
		_, err := a.fetchPendingForVersion(ctx, svc, "ocid", 4)
		require.Error(t, err)
	})

	t.Run("version mismatch errors", func(t *testing.T) {
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(9), nil
		})
		_, err := a.fetchPendingForVersion(ctx, svc, "ocid", 4)
		require.Error(t, err)
	})

	t.Run("match returns bundle", func(t *testing.T) {
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		resp, err := a.fetchPendingForVersion(ctx, svc, "ocid", 4)
		require.NoError(t, err)
		assert.Equal(t, int64(4), resp.Certificate.VersionNumber)
	})
}

// ---------------------------------------------------------------------------
// InstallAndVerifyOciONTAPCertificateVersion
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_InstallAndVerifyOciONTAPCertificateVersion(t *testing.T) {
	ctx := context.Background()
	nodes := []*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}

	baseActivity := func(mockSE database.Storage, installErr, verifyErr error) *OciCertificateRotationActivity {
		return &OciCertificateRotationActivity{
			SE: mockSE,
			installOciCertFunc: func(context.Context, *datamodel.Pool, []*datamodel.Node, *hyperscalermodels.CustomCertificateResponse) error {
				return installErr
			},
			verifyOciCertFunc: func(context.Context, *datamodel.Pool, []*datamodel.Node, *hyperscalermodels.CustomCertificateResponse) error {
				return verifyErr
			},
		}
	}

	t.Run("success", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil).Once()
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		a := baseActivity(mockSE, nil, nil)

		err := a.InstallAndVerifyOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")}, 4)
		require.NoError(t, err)
		mockSE.AssertExpectations(t)
	})

	t.Run("install failure leaves pending", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil).Once()
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		a := baseActivity(mockSE, errors.New("install failed"), nil)

		err := a.InstallAndVerifyOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")}, 4)
		require.Error(t, err)
	})

	t.Run("verify failure leaves pending", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil).Once()
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		a := baseActivity(mockSE, nil, errors.New("verify failed"))

		err := a.InstallAndVerifyOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")}, 4)
		require.Error(t, err)
	})

	t.Run("get nodes error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nil, errors.New("db down")).Once()
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		a := baseActivity(mockSE, nil, nil)

		err := a.InstallAndVerifyOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")}, 4)
		require.Error(t, err)
	})

	t.Run("pending version mismatch", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(9), nil
		})
		a := baseActivity(mockSE, nil, nil)

		err := a.InstallAndVerifyOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")}, 4)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// PromoteAndCommitOciONTAPCertificateVersion
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_PromoteAndCommitOciONTAPCertificateVersion(t *testing.T) {
	ctx := context.Background()
	poolUUID := "u"

	t.Run("success promotes and commits then prunes prior version", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		// poolStillEligibleForPromote re-reads the pool.
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{eligibleOCIPoolView(poolUUID)}, nil).Once()
		mockSE.On("UpdatePoolFields", ctx, poolUUID, mock.Anything).Return(nil).Once()
		// Post-promote best-effort cleanup re-reads nodes to build a provider and
		// prune the now-demoted prior version (pool starts at version 3).
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).
			Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}, nil).Once()

		// PromoteCertificateVersion issues a single UpdateCertificate (PUT).
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return ociActJSONResponse(http.StatusOK, `{
					"id":"ocid1.certificate.oc1..rot","name":"deployment-foo-cert","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"UPDATING","configType":"ISSUED_BY_INTERNAL_CA"
				}`), nil
			},
		}
		overrideOCIServiceSeam(t, newOciActService(t, dispatcher), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})

		mp := &vsa.MockProvider{}
		// Cluster-scoped cert: deleted by version-unique name + type only, no svm filter.
		mp.On("DeleteServerCertificate", vsa.DeleteServerCertificateParams{
			CertificateName: "ocid1.certificate.oc1..rot-v3",
			CertificateType: "server",
		}).Return(nil).Once()
		overrideGetProviderByNode(t, mp, nil)

		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.NoError(t, err)
		mockSE.AssertExpectations(t)
		mp.AssertExpectations(t)
	})

	t.Run("no pending treated as already promoted - recommit only", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("UpdatePoolFields", ctx, poolUUID, mock.Anything).Return(nil).Once()
		// Best-effort cleanup runs after the re-commit; a node-read failure is
		// swallowed so it cannot fail the rotation.
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nil, errors.New("db down")).Once()
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return nil, nil
		})
		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.NoError(t, err)
		mockSE.AssertExpectations(t)
	})

	t.Run("version mismatch errors without promote", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(9), nil
		})
		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.Error(t, err)
	})

	t.Run("pool no longer eligible skips promote", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		deletingView := eligibleOCIPoolView(poolUUID)
		deletingView.Pool.State = "DELETING"
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{deletingView}, nil).Once()
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.NoError(t, err)
		mockSE.AssertExpectations(t)
	})

	t.Run("get pending error propagates", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return nil, errors.New("read failed")
		})
		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.Error(t, err)
	})

	t.Run("ineligible pool errors before service", func(t *testing.T) {
		a := &OciCertificateRotationActivity{}
		p := eligibleOCIPool(poolUUID)
		p.State = "DELETING"
		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: p}, 4)
		require.Error(t, err)
	})

	t.Run("get oci service error propagates", func(t *testing.T) {
		a := &OciCertificateRotationActivity{}
		overrideOCIServiceSeam(t, nil, errors.New("no service"))
		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.Error(t, err)
	})

	t.Run("promote call error propagates", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{eligibleOCIPoolView(poolUUID)}, nil).Once()
		// UpdateCertificate (PUT) fails the promote.
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &ociActServiceError{statusCode: http.StatusConflict, code: "Conflict", message: "promote failed"}
			},
		}
		overrideOCIServiceSeam(t, newOciActService(t, dispatcher), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.Error(t, err)
	})

	t.Run("commit error after promote propagates", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{eligibleOCIPoolView(poolUUID)}, nil).Once()
		mockSE.On("UpdatePoolFields", ctx, poolUUID, mock.Anything).Return(errors.New("db down")).Once()
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return ociActJSONResponse(http.StatusOK, `{
					"id":"ocid1.certificate.oc1..rot","name":"deployment-foo-cert","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"UPDATING","configType":"ISSUED_BY_INTERNAL_CA"
				}`), nil
			},
		}
		overrideOCIServiceSeam(t, newOciActService(t, dispatcher), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.Error(t, err)
	})

	t.Run("commit error in already-promoted branch propagates", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("UpdatePoolFields", ctx, poolUUID, mock.Anything).Return(errors.New("db down")).Once()
		overrideOCIServiceSeam(t, newOciActService(t, nil), nil)
		overridePendingSeam(t, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return nil, nil
		})
		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.PromoteAndCommitOciONTAPCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool(poolUUID)}, 4)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// poolStillEligibleForPromote
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_poolStillEligibleForPromote(t *testing.T) {
	ctx := context.Background()
	poolUUID := "u"

	t.Run("read error does not block", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return(nil, errors.New("transient")).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		ok, err := a.poolStillEligibleForPromote(ctx, poolUUID)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("missing pool aborts", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{}, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		ok, err := a.poolStillEligibleForPromote(ctx, poolUUID)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("ineligible state aborts", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		view := eligibleOCIPoolView(poolUUID)
		view.Pool.State = "UPGRADING"
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{view}, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		ok, err := a.poolStillEligibleForPromote(ctx, poolUUID)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("eligible pool proceeds", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListPools", ctx, mock.AnythingOfType("*utils.Filter")).
			Return([]*datamodel.PoolView{eligibleOCIPoolView(poolUUID)}, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		ok, err := a.poolStillEligibleForPromote(ctx, poolUUID)
		require.NoError(t, err)
		assert.True(t, ok)
	})
}

// ---------------------------------------------------------------------------
// updateOciCertificateVersion
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_updateOciCertificateVersion(t *testing.T) {
	ctx := context.Background()

	t.Run("nil external certificate errors", func(t *testing.T) {
		a := &OciCertificateRotationActivity{}
		pool := eligibleOCIPool("u")
		pool.PoolCredentials.ExternalCertificate = nil
		err := a.updateOciCertificateVersion(ctx, pool, 5)
		require.Error(t, err)
	})

	t.Run("success persists new version", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("UpdatePoolFields", ctx, "u", mock.Anything).Return(nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}
		pool := eligibleOCIPool("u")

		err := a.updateOciCertificateVersion(ctx, pool, 5)
		require.NoError(t, err)
		assert.Equal(t, int64(5), pool.PoolCredentials.ExternalCertificate.Version)
		mockSE.AssertExpectations(t)
	})

	t.Run("update error wrapped", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("UpdatePoolFields", ctx, "u", mock.Anything).Return(errors.New("db down")).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		err := a.updateOciCertificateVersion(ctx, eligibleOCIPool("u"), 5)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// sweepDeprecatedVersions (best-effort, never returns)
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_sweepDeprecatedVersions(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}

	t.Run("list error is swallowed", func(t *testing.T) {
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &ociActServiceError{statusCode: http.StatusForbidden, message: "denied"}
			},
		}
		svc := newOciActService(t, dispatcher)
		// Must not panic or block.
		a.sweepDeprecatedVersions(ctx, svc, "ocid1.certificate.oc1..rot")
	})

	t.Run("skips versions already scheduled for deletion", func(t *testing.T) {
		var scheduleCalled bool
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "scheduleDeletion") || req.Method == http.MethodPost {
					scheduleCalled = true
					return ociActJSONResponse(http.StatusOK, `{}`), nil
				}
				return ociActJSONResponse(http.StatusOK, `{"items":[
					{"certificateId":"ocid1.certificate.oc1..rot","versionNumber":1,"stages":["DEPRECATED"],"timeCreated":"2025-01-01T00:00:00Z","timeOfDeletion":"2025-02-01T00:00:00Z"}
				]}`), nil
			},
		}
		svc := newOciActService(t, dispatcher)
		a.sweepDeprecatedVersions(ctx, svc, "ocid1.certificate.oc1..rot")
		assert.False(t, scheduleCalled, "already-scheduled DEPRECATED version must be skipped")
	})
}

// Ensure the OCI service seam default is the hyperscaler provider (compile-time
// guard that the seam wiring matches production).
func TestOciServiceSeamDefault(t *testing.T) {
	assert.NotNil(t, hyperscaler2.GetOCIService)
	assert.NotNil(t, vsa.RenewOciCertificateVersionForVSACluster)
}

// ---------------------------------------------------------------------------
// Additional helpers for install/verify/dispatch/needs-rotation coverage
// ---------------------------------------------------------------------------

// overrideGetProviderByNode swaps the vsa.GetProviderByNode seam so the install
// path can be unit-tested with a MockProvider, restoring it on cleanup.
func overrideGetProviderByNode(t *testing.T, provider vsa.Provider, err error) {
	t.Helper()
	orig := vsa.GetProviderByNode
	t.Cleanup(func() { vsa.GetProviderByNode = orig })
	vsa.GetProviderByNode = func(_ context.Context, _ *models.Node) (vsa.Provider, error) {
		return provider, err
	}
}

// overrideTemporalClientSeam swaps workflowtemporal.FetchTemporalClient and
// restores it on cleanup.
func overrideTemporalClientSeam(t *testing.T, c client.Client, err error) {
	t.Helper()
	orig := workflowtemporal.FetchTemporalClient
	t.Cleanup(func() { workflowtemporal.FetchTemporalClient = orig })
	workflowtemporal.FetchTemporalClient = func() (client.Client, error) {
		return c, err
	}
}

// fakeWorkflowRun is a minimal client.WorkflowRun for dispatch success tests.
type fakeWorkflowRun struct{ runID string }

func (f *fakeWorkflowRun) GetID() string    { return "wf-id" }
func (f *fakeWorkflowRun) GetRunID() string { return f.runID }
func (f *fakeWorkflowRun) Get(context.Context, interface{}) error {
	return nil
}
func (f *fakeWorkflowRun) GetWithOptions(context.Context, interface{}, client.WorkflowRunGetOptions) error {
	return nil
}

// ociCertRespWithPEM builds a cert response carrying a real, parseable PEM so the
// install path's serial/CA derivation succeeds. caName seeds the
// IssuerCertificateAuthority fallback used when the PEM has no issuer CN.
func ociCertRespWithPEM(t *testing.T, version int64, caName string) *hyperscalermodels.CustomCertificateResponse {
	t.Helper()
	pemCert := generateTestCertificate(time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	r := certRespForVersion(version)
	r.Certificate.PemCertificate = pemCert
	r.Certificate.IssuerCertificateAuthority = caName
	return r
}

// overrideCertBundleSeam swaps oci.GetCertificateBundleWithPrivateKey so
// ociService.GetCertificate returns a full cert (optionally with a validity
// window) without reaching the live certificates read client.
func overrideCertBundleSeam(t *testing.T, notBefore, notAfter *time.Time) {
	t.Helper()
	orig := oci.GetCertificateBundleWithPrivateKey
	t.Cleanup(func() { oci.GetCertificateBundleWithPrivateKey = orig })
	oci.GetCertificateBundleWithPrivateKey = func(_ *oci.OciServices, _ string) (*certificates.CertificateBundleWithPrivateKey, error) {
		b := &certificates.CertificateBundleWithPrivateKey{
			CertificatePem: ocicommon.String("leaf"),
			PrivateKeyPem:  ocicommon.String("key"),
			VersionNumber:  ocicommon.Int64(3),
		}
		if notBefore != nil && notAfter != nil {
			b.Validity = &certificates.Validity{
				TimeOfValidityNotBefore: &ocicommon.SDKTime{Time: *notBefore},
				TimeOfValidityNotAfter:  &ocicommon.SDKTime{Time: *notAfter},
			}
		}
		return b, nil
	}
}

// activeCertMetadataDispatcher returns ACTIVE certificate metadata for the
// management GetCertificate call.
func activeCertMetadataDispatcher() *ociActDispatcher {
	return &ociActDispatcher{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return ociActJSONResponse(http.StatusOK, `{
				"id":"ocid1.certificate.oc1..rot","name":"deployment-foo-cert","compartmentId":"c",
				"timeCreated":"2026-01-01T00:00:00.000Z","lifecycleState":"ACTIVE","configType":"ISSUED_BY_INTERNAL_CA",
				"subject":{"commonName":"deployment-foo_admin"},"currentVersion":{"versionNumber":3}
			}`), nil
		},
	}
}

// ---------------------------------------------------------------------------
// DispatchOciPoolCertificateRotation
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_DispatchOciPoolCertificateRotation(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}

	t.Run("temporal client fetch error propagates", func(t *testing.T) {
		overrideTemporalClientSeam(t, nil, errors.New("no client"))
		err := a.DispatchOciPoolCertificateRotation(ctx, "pool-1", time.Minute)
		require.Error(t, err)
	})

	t.Run("execute workflow error propagates", func(t *testing.T) {
		mockTemp := wfe.NewMockTemporalTestClient(t)
		mockTemp.EXPECT().
			ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("execute boom")).Once()
		overrideTemporalClientSeam(t, mockTemp, nil)

		err := a.DispatchOciPoolCertificateRotation(ctx, "pool-1", time.Minute)
		require.Error(t, err)
	})

	t.Run("success dispatches workflow", func(t *testing.T) {
		mockTemp := wfe.NewMockTemporalTestClient(t)
		mockTemp.EXPECT().
			ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&fakeWorkflowRun{runID: "run-1"}, nil).Once()
		overrideTemporalClientSeam(t, mockTemp, nil)

		err := a.DispatchOciPoolCertificateRotation(ctx, "pool-1", time.Minute)
		require.NoError(t, err)
	})
}

// ---------------------------------------------------------------------------
// ociCertificateNeedsRotation (validity-window logic)
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_ociCertificateNeedsRotation(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}
	pool := eligibleOCIPool("u")

	t.Run("get certificate error propagates", func(t *testing.T) {
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return nil, &ociActServiceError{statusCode: http.StatusForbidden, code: "Forbidden", message: "denied"}
			},
		}
		svc := newOciActService(t, dispatcher)
		_, err := a.ociCertificateNeedsRotation(ctx, svc, pool)
		require.Error(t, err)
	})

	t.Run("missing certificate needs rotation", func(t *testing.T) {
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return ociActJSONResponse(http.StatusNotFound, `{"code":"NotAuthorizedOrNotFound","message":"not found"}`), nil
			},
		}
		svc := newOciActService(t, dispatcher)
		needs, err := a.ociCertificateNeedsRotation(ctx, svc, pool)
		require.NoError(t, err)
		assert.True(t, needs)
	})

	t.Run("no validity window needs rotation", func(t *testing.T) {
		svc := newOciActService(t, activeCertMetadataDispatcher())
		overrideCertBundleSeam(t, nil, nil)
		needs, err := a.ociCertificateNeedsRotation(ctx, svc, pool)
		require.NoError(t, err)
		assert.True(t, needs)
	})

	t.Run("expired certificate needs rotation", func(t *testing.T) {
		svc := newOciActService(t, activeCertMetadataDispatcher())
		notBefore := time.Now().Add(-48 * time.Hour)
		notAfter := time.Now().Add(-1 * time.Hour)
		overrideCertBundleSeam(t, &notBefore, &notAfter)
		needs, err := a.ociCertificateNeedsRotation(ctx, svc, pool)
		require.NoError(t, err)
		assert.True(t, needs)
	})

	t.Run("past threshold needs rotation", func(t *testing.T) {
		svc := newOciActService(t, activeCertMetadataDispatcher())
		notBefore := time.Now().Add(-364 * 24 * time.Hour)
		notAfter := time.Now().Add(24 * time.Hour)
		overrideCertBundleSeam(t, &notBefore, &notAfter)
		needs, err := a.ociCertificateNeedsRotation(ctx, svc, pool)
		require.NoError(t, err)
		assert.True(t, needs)
	})

	t.Run("fresh certificate does not need rotation", func(t *testing.T) {
		svc := newOciActService(t, activeCertMetadataDispatcher())
		notBefore := time.Now().Add(-1 * time.Minute)
		notAfter := time.Now().Add(365 * 24 * time.Hour)
		overrideCertBundleSeam(t, &notBefore, &notAfter)
		needs, err := a.ociCertificateNeedsRotation(ctx, svc, pool)
		require.NoError(t, err)
		assert.False(t, needs)
	})
}

// ---------------------------------------------------------------------------
// installOciCertificateOnVSA (real path via MockProvider)
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_installOciCertificateOnVSA(t *testing.T) {
	ctx := context.Background()
	nodes := []*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}
	a := &OciCertificateRotationActivity{}

	t.Run("no nodes errors before provider", func(t *testing.T) {
		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nil, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.Error(t, err)
	})

	t.Run("get provider error propagates", func(t *testing.T) {
		overrideGetProviderByNode(t, nil, errors.New("provider boom"))
		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.Error(t, err)
	})

	t.Run("nil provider errors", func(t *testing.T) {
		overrideGetProviderByNode(t, nil, nil)
		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.Error(t, err)
	})

	t.Run("success installs and configures ssl", func(t *testing.T) {
		mp := &vsa.MockProvider{}
		mp.On("InstallServerCertificate", mock.Anything).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		mp.On("ModifySSL", mock.Anything).Return(&vsa.ModifySSLResponse{Success: true}, nil).Once()
		overrideGetProviderByNode(t, mp, nil)

		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.NoError(t, err)
		mp.AssertExpectations(t)
	})

	t.Run("duplicate install treated as success", func(t *testing.T) {
		mp := &vsa.MockProvider{}
		mp.On("InstallServerCertificate", mock.Anything).Return(nil, errors.New("duplicate entry")).Once()
		mp.On("ModifySSL", mock.Anything).Return(&vsa.ModifySSLResponse{Success: true}, nil).Once()
		overrideGetProviderByNode(t, mp, nil)

		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.NoError(t, err)
		mp.AssertExpectations(t)
	})

	t.Run("non-duplicate install error propagates", func(t *testing.T) {
		mp := &vsa.MockProvider{}
		mp.On("InstallServerCertificate", mock.Anything).Return(nil, errors.New("install boom")).Once()
		overrideGetProviderByNode(t, mp, nil)

		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.Error(t, err)
		mp.AssertExpectations(t)
	})

	t.Run("svm not found retries with deployment name", func(t *testing.T) {
		// VLMConfig cluster name differs from DeploymentName so the first install
		// targets the cluster name and the retry targets the deployment name.
		pool := eligibleOCIPool("u")
		pool.VLMConfig = `{"vsa_cluster":{"cluster_name":"cluster-foo"}}`

		mp := &vsa.MockProvider{}
		mp.On("InstallServerCertificate", mock.Anything).
			Return(nil, errors.New("SVM cluster-foo does not exist")).Once()
		mp.On("InstallServerCertificate", mock.Anything).
			Return(&vsa.ServerCertificateResponse{}, nil).Once()
		mp.On("ModifySSL", mock.Anything).Return(&vsa.ModifySSLResponse{Success: true}, nil).Once()
		overrideGetProviderByNode(t, mp, nil)

		err := a.installOciCertificateOnVSA(ctx, pool, nodes, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.NoError(t, err)
		mp.AssertExpectations(t)
	})

	t.Run("unparseable serial errors", func(t *testing.T) {
		mp := &vsa.MockProvider{}
		mp.On("InstallServerCertificate", mock.Anything).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		overrideGetProviderByNode(t, mp, nil)

		// certRespForVersion carries an invalid PEM ("leaf-pem") so serial
		// derivation fails after a successful install.
		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, certRespForVersion(4))
		require.Error(t, err)
		mp.AssertExpectations(t)
	})

	t.Run("empty CA errors", func(t *testing.T) {
		mp := &vsa.MockProvider{}
		mp.On("InstallServerCertificate", mock.Anything).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		overrideGetProviderByNode(t, mp, nil)

		// Real PEM (serial derivable) but no issuer CN and empty CA fallback.
		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, ociCertRespWithPEM(t, 4, ""))
		require.Error(t, err)
		mp.AssertExpectations(t)
	})

	t.Run("modify ssl error propagates", func(t *testing.T) {
		mp := &vsa.MockProvider{}
		mp.On("InstallServerCertificate", mock.Anything).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		mp.On("ModifySSL", mock.Anything).Return(nil, errors.New("ssl boom")).Once()
		overrideGetProviderByNode(t, mp, nil)

		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.Error(t, err)
		mp.AssertExpectations(t)
	})

	t.Run("modify ssl unsuccessful response errors", func(t *testing.T) {
		mp := &vsa.MockProvider{}
		mp.On("InstallServerCertificate", mock.Anything).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		mp.On("ModifySSL", mock.Anything).Return(&vsa.ModifySSLResponse{Success: false, Message: "nope"}, nil).Once()
		overrideGetProviderByNode(t, mp, nil)

		err := a.installOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nodes, ociCertRespWithPEM(t, 4, "vsa-ca"))
		require.Error(t, err)
		mp.AssertExpectations(t)
	})
}

// ---------------------------------------------------------------------------
// cleanupPreviousOntapCertificateVersion (best-effort prior-version pruning)
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_cleanupPreviousOntapCertificateVersion(t *testing.T) {
	ctx := context.Background()
	nodes := []*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}
	const certOCID = "ocid1.certificate.oc1..rot"

	t.Run("no prior version is a no-op", func(t *testing.T) {
		// previousVersion <= 0: nothing was ever promoted, so there is nothing to
		// prune. SE/provider must not be touched.
		mockSE := database.NewMockStorage(t)
		a := &OciCertificateRotationActivity{SE: mockSE}
		overrideGetProviderByNode(t, nil, errors.New("should not be called"))

		a.cleanupPreviousOntapCertificateVersion(ctx, eligibleOCIPool("u"), certOCID, 0, 3)
		mockSE.AssertNotCalled(t, "GetNodesByPoolID", mock.Anything, mock.Anything)
	})

	t.Run("previous equals current is a no-op (never delete CURRENT)", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		a := &OciCertificateRotationActivity{SE: mockSE}
		overrideGetProviderByNode(t, nil, errors.New("should not be called"))

		a.cleanupPreviousOntapCertificateVersion(ctx, eligibleOCIPool("u"), certOCID, 3, 3)
		mockSE.AssertNotCalled(t, "GetNodesByPoolID", mock.Anything, mock.Anything)
	})

	t.Run("get nodes error is swallowed", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nil, errors.New("db down")).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		a.cleanupPreviousOntapCertificateVersion(ctx, eligibleOCIPool("u"), certOCID, 2, 3)
		mockSE.AssertExpectations(t)
	})

	t.Run("no nodes is swallowed", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nil, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		a.cleanupPreviousOntapCertificateVersion(ctx, eligibleOCIPool("u"), certOCID, 2, 3)
		mockSE.AssertExpectations(t)
	})

	t.Run("provider build error is swallowed", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}
		overrideGetProviderByNode(t, nil, errors.New("provider boom"))

		a.cleanupPreviousOntapCertificateVersion(ctx, eligibleOCIPool("u"), certOCID, 2, 3)
		mockSE.AssertExpectations(t)
	})

	t.Run("success deletes prior version under version-unique name", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		mp := &vsa.MockProvider{}
		// Cluster-scoped cert: deleted by version-unique name + type only, no svm filter.
		mp.On("DeleteServerCertificate", vsa.DeleteServerCertificateParams{
			CertificateName: "ocid1.certificate.oc1..rot-v2",
			CertificateType: "server",
		}).Return(nil).Once()
		overrideGetProviderByNode(t, mp, nil)

		a.cleanupPreviousOntapCertificateVersion(ctx, eligibleOCIPool("u"), certOCID, 2, 3)
		mockSE.AssertExpectations(t)
		mp.AssertExpectations(t)
	})

	t.Run("delete error is swallowed (best-effort)", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}

		mp := &vsa.MockProvider{}
		mp.On("DeleteServerCertificate", mock.Anything).Return(errors.New("ontap boom")).Once()
		overrideGetProviderByNode(t, mp, nil)

		a.cleanupPreviousOntapCertificateVersion(ctx, eligibleOCIPool("u"), certOCID, 2, 3)
		mockSE.AssertExpectations(t)
		mp.AssertExpectations(t)
	})
}

// ---------------------------------------------------------------------------
// buildOciVerifyProvider / verifyOciCertificateOnVSA
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_buildOciVerifyProvider(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}
	nodes := []*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}

	t.Run("no nodes errors", func(t *testing.T) {
		_, err := a.buildOciVerifyProvider(ctx, eligibleOCIPool("u"), nil, certRespForVersion(4))
		require.Error(t, err)
	})

	t.Run("builds provider", func(t *testing.T) {
		provider, err := a.buildOciVerifyProvider(ctx, eligibleOCIPool("u"), nodes, certRespForVersion(4))
		require.NoError(t, err)
		require.NotNil(t, provider)
	})

	t.Run("builds provider with sni override", func(t *testing.T) {
		origSNI := env.OCIUseTLSSNIOverride
		origDNS := env.VsaDeployedDnsName
		t.Cleanup(func() {
			env.OCIUseTLSSNIOverride = origSNI
			env.VsaDeployedDnsName = origDNS
		})
		env.OCIUseTLSSNIOverride = true
		env.VsaDeployedDnsName = "example.com"

		provider, err := a.buildOciVerifyProvider(ctx, eligibleOCIPool("u"), nodes, certRespForVersion(4))
		require.NoError(t, err)
		require.NotNil(t, provider)
	})
}

func TestOciCertificateRotationActivity_verifyOciCertificateOnVSA_noNodes(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}

	err := a.verifyOciCertificateOnVSA(ctx, eligibleOCIPool("u"), nil, certRespForVersion(4))
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// refreshOciCertCache / sweepDeprecatedVersions extra branches
// ---------------------------------------------------------------------------

func TestOciCertificateRotationActivity_refreshOciCertCache(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}

	t.Run("empty name is a no-op", func(t *testing.T) {
		a.refreshOciCertCache(ctx, "", certRespForVersion(4))
	})

	t.Run("populates cache for named cert", func(t *testing.T) {
		certName := "deployment-foo-cert-refresh"
		t.Cleanup(func() { removeFromCertAuthCache(certName) })
		a.refreshOciCertCache(ctx, certName, certRespForVersion(4))
	})
}

func TestOciCertificateRotationActivity_sweepDeprecatedVersions_extra(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}

	t.Run("skips non-deprecated versions", func(t *testing.T) {
		var scheduleCalled bool
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost || strings.Contains(req.URL.Path, "scheduleDeletion") {
					scheduleCalled = true
					return ociActJSONResponse(http.StatusOK, `{}`), nil
				}
				return ociActJSONResponse(http.StatusOK, `{"items":[
					{"certificateId":"ocid1.certificate.oc1..rot","versionNumber":1,"stages":["CURRENT"],"timeCreated":"2025-01-01T00:00:00Z"}
				]}`), nil
			},
		}
		svc := newOciActService(t, dispatcher)
		a.sweepDeprecatedVersions(ctx, svc, "ocid1.certificate.oc1..rot")
		assert.False(t, scheduleCalled, "non-deprecated version must not be scheduled for deletion")
	})

	t.Run("schedules deletion for unscheduled deprecated version", func(t *testing.T) {
		var scheduleCalled bool
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost || strings.Contains(req.URL.Path, "scheduleDeletion") {
					scheduleCalled = true
					return ociActJSONResponse(http.StatusOK, `{}`), nil
				}
				return ociActJSONResponse(http.StatusOK, `{"items":[
					{"certificateId":"ocid1.certificate.oc1..rot","versionNumber":2,"stages":["DEPRECATED"],"timeCreated":"2025-01-01T00:00:00Z"}
				]}`), nil
			},
		}
		svc := newOciActService(t, dispatcher)
		a.sweepDeprecatedVersions(ctx, svc, "ocid1.certificate.oc1..rot")
		assert.True(t, scheduleCalled, "unscheduled DEPRECATED version must be scheduled for deletion")
	})

	t.Run("schedule deletion error is swallowed", func(t *testing.T) {
		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPost || strings.Contains(req.URL.Path, "scheduleDeletion") {
					return nil, &ociActServiceError{statusCode: http.StatusTooManyRequests, code: "TooManyRequests", message: "rate limited"}
				}
				return ociActJSONResponse(http.StatusOK, `{"items":[
					{"certificateId":"ocid1.certificate.oc1..rot","versionNumber":2,"stages":["DEPRECATED"],"timeCreated":"2025-01-01T00:00:00Z"}
				]}`), nil
			},
		}
		svc := newOciActService(t, dispatcher)
		// Must not panic or return.
		a.sweepDeprecatedVersions(ctx, svc, "ocid1.certificate.oc1..rot")
	})
}

func clientCertPool(uuid string) *datamodel.Pool {
	p := eligibleOCIPool(uuid)
	p.PoolCredentials.ExternalClientCertificate = &datamodel.ExternalCredRef{
		ExternalIdentifier: "ocid1.certificate.oc1..cli",
		Name:               "deployment-foo-client-cert",
		Version:            2,
	}
	return p
}

func TestOciCertificateRotationActivity_OciVCPClientCertificateNeedsRotation(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}

	origSep := env.OCISeparateMTLSClientCert
	t.Cleanup(func() { env.OCISeparateMTLSClientCert = origSep })

	t.Run("disabled returns false", func(tt *testing.T) {
		env.OCISeparateMTLSClientCert = false
		needs, err := a.OciVCPClientCertificateNeedsRotation(ctx, &PoolContext{Pool: clientCertPool("u")})
		require.NoError(tt, err)
		assert.False(tt, needs)
	})

	t.Run("nil pool context returns false", func(tt *testing.T) {
		env.OCISeparateMTLSClientCert = true
		needs, err := a.OciVCPClientCertificateNeedsRotation(ctx, nil)
		require.NoError(tt, err)
		assert.False(tt, needs)
	})

	t.Run("non-cert auth returns false", func(tt *testing.T) {
		env.OCISeparateMTLSClientCert = true
		p := clientCertPool("u")
		p.PoolCredentials.AuthType = env.USERNAME_PWD_SEC_MGR
		needs, err := a.OciVCPClientCertificateNeedsRotation(ctx, &PoolContext{Pool: p})
		require.NoError(tt, err)
		assert.False(tt, needs)
	})

	t.Run("no client cert ref returns false", func(tt *testing.T) {
		env.OCISeparateMTLSClientCert = true
		// eligibleOCIPool sets ExternalCertificate but not ExternalClientCertificate.
		needs, err := a.OciVCPClientCertificateNeedsRotation(ctx, &PoolContext{Pool: eligibleOCIPool("u")})
		require.NoError(tt, err)
		assert.False(tt, needs)
	})

	t.Run("get oci service error propagates", func(tt *testing.T) {
		env.OCISeparateMTLSClientCert = true
		overrideOCIServiceSeam(tt, nil, errors.New("oci service boom"))
		_, err := a.OciVCPClientCertificateNeedsRotation(ctx, &PoolContext{Pool: clientCertPool("u")})
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "oci service boom")
	})

	t.Run("expired cert needs rotation", func(tt *testing.T) {
		env.OCISeparateMTLSClientCert = true
		overrideOCIServiceSeam(tt, newOciActService(tt, activeCertMetadataDispatcher()), nil)
		notBefore := time.Now().Add(-48 * time.Hour)
		notAfter := time.Now().Add(-1 * time.Hour)
		overrideCertBundleSeam(tt, &notBefore, &notAfter)

		needs, err := a.OciVCPClientCertificateNeedsRotation(ctx, &PoolContext{Pool: clientCertPool("u")})
		require.NoError(tt, err)
		assert.True(tt, needs)
	})

	t.Run("not due reconciles stale version and returns false", func(tt *testing.T) {
		env.OCISeparateMTLSClientCert = true
		p := clientCertPool("u") // client ref version 2; CURRENT is 3 → reconcile persists 3
		overrideOCIServiceSeam(tt, newOciActService(tt, activeCertMetadataDispatcher()), nil)
		notBefore := time.Now().Add(-1 * time.Hour)
		notAfter := time.Now().Add(1000 * time.Hour)
		overrideCertBundleSeam(tt, &notBefore, &notAfter)

		mockSE := database.NewMockStorage(tt)
		mockSE.On("UpdatePoolFields", ctx, "u", mock.Anything).Return(nil).Once()
		act := &OciCertificateRotationActivity{SE: mockSE}

		needs, err := act.OciVCPClientCertificateNeedsRotation(ctx, &PoolContext{Pool: p})
		require.NoError(tt, err)
		assert.False(tt, needs)
		assert.Equal(tt, int64(3), p.PoolCredentials.ExternalClientCertificate.Version)
		mockSE.AssertExpectations(tt)
	})
}

func TestOciCertificateRotationActivity_CleanupAndStageOciVCPClientCertificateVersion(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}

	origSep := env.OCISeparateMTLSClientCert
	t.Cleanup(func() { env.OCISeparateMTLSClientCert = origSep })
	env.OCISeparateMTLSClientCert = true

	t.Run("ineligible pool errors before service", func(tt *testing.T) {
		_, err := a.CleanupAndStageOciVCPClientCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")})
		require.Error(tt, err)
	})

	t.Run("success returns staged version", func(tt *testing.T) {
		overrideOCIServiceSeam(tt, newOciActService(tt, emptyVersionsDispatcher()), nil)
		overrideRenewSeam(tt, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})
		version, err := a.CleanupAndStageOciVCPClientCertificateVersion(ctx, &PoolContext{Pool: clientCertPool("u")})
		require.NoError(tt, err)
		assert.Equal(tt, int64(4), version)
	})
}

func TestOciCertificateRotationActivity_PromoteAndCommitOciVCPClientCertificateVersion(t *testing.T) {
	ctx := context.Background()

	origSep := env.OCISeparateMTLSClientCert
	t.Cleanup(func() { env.OCISeparateMTLSClientCert = origSep })
	env.OCISeparateMTLSClientCert = true

	t.Run("ineligible pool errors", func(tt *testing.T) {
		err := (&OciCertificateRotationActivity{}).PromoteAndCommitOciVCPClientCertificateVersion(ctx, &PoolContext{Pool: eligibleOCIPool("u")}, 4)
		require.Error(tt, err)
	})

	t.Run("version mismatch errors without promote", func(tt *testing.T) {
		overrideOCIServiceSeam(tt, newOciActService(tt, nil), nil)
		overridePendingSeam(tt, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(9), nil
		})
		err := (&OciCertificateRotationActivity{}).PromoteAndCommitOciVCPClientCertificateVersion(ctx, &PoolContext{Pool: clientCertPool("u")}, 4)
		require.Error(tt, err)
	})

	t.Run("success promotes persists and refreshes cache", func(tt *testing.T) {
		p := clientCertPool("u")
		mockSE := database.NewMockStorage(tt)
		mockSE.On("UpdatePoolFields", ctx, "u", mock.Anything).Return(nil).Once()

		dispatcher := &ociActDispatcher{
			doFunc: func(req *http.Request) (*http.Response, error) {
				return ociActJSONResponse(http.StatusOK, `{
					"id":"ocid1.certificate.oc1..cli","name":"deployment-foo-client-cert","compartmentId":"c",
					"timeCreated":"2026-01-01T00:00:00Z","lifecycleState":"UPDATING","configType":"ISSUED_BY_INTERNAL_CA"
				}`), nil
			},
		}
		overrideOCIServiceSeam(tt, newOciActService(tt, dispatcher), nil)
		overridePendingSeam(tt, func() (*hyperscalermodels.CustomCertificateResponse, error) {
			return certRespForVersion(4), nil
		})

		err := (&OciCertificateRotationActivity{SE: mockSE}).PromoteAndCommitOciVCPClientCertificateVersion(ctx, &PoolContext{Pool: p}, 4)
		require.NoError(tt, err)
		assert.Equal(tt, int64(4), p.PoolCredentials.ExternalClientCertificate.Version)
		mockSE.AssertExpectations(tt)
	})
}

func TestOciCertificateRotationActivity_VerifyOciVCPClientCertificatePending(t *testing.T) {
	ctx := context.Background()

	origSep := env.OCISeparateMTLSClientCert
	t.Cleanup(func() { env.OCISeparateMTLSClientCert = origSep })
	env.OCISeparateMTLSClientCert = true

	t.Run("ineligible pool errors", func(tt *testing.T) {
		err := (&OciCertificateRotationActivity{}).VerifyOciVCPClientCertificatePending(ctx, &PoolContext{Pool: eligibleOCIPool("u")}, 4)
		require.Error(tt, err)
	})

	t.Run("no pending version errors", func(tt *testing.T) {
		overrideOCIServiceSeam(tt, newOciActService(tt, nil), nil)
		overridePendingSeam(tt, func() (*hyperscalermodels.CustomCertificateResponse, error) { return nil, nil })
		err := (&OciCertificateRotationActivity{}).VerifyOciVCPClientCertificatePending(ctx, &PoolContext{Pool: clientCertPool("u")}, 4)
		require.Error(tt, err)
	})

	t.Run("version mismatch errors before nodes", func(tt *testing.T) {
		overrideOCIServiceSeam(tt, newOciActService(tt, nil), nil)
		overridePendingSeam(tt, func() (*hyperscalermodels.CustomCertificateResponse, error) { return certRespForVersion(9), nil })
		err := (&OciCertificateRotationActivity{}).VerifyOciVCPClientCertificatePending(ctx, &PoolContext{Pool: clientCertPool("u")}, 4)
		require.Error(tt, err)
	})

	t.Run("get nodes error propagates", func(tt *testing.T) {
		overrideOCIServiceSeam(tt, newOciActService(tt, nil), nil)
		overridePendingSeam(tt, func() (*hyperscalermodels.CustomCertificateResponse, error) { return certRespForVersion(4), nil })
		mockSE := database.NewMockStorage(tt)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nil, errors.New("db down")).Once()
		err := (&OciCertificateRotationActivity{SE: mockSE}).VerifyOciVCPClientCertificatePending(ctx, &PoolContext{Pool: clientCertPool("u")}, 4)
		require.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("delegates to verify helper with pending bundle", func(tt *testing.T) {
		nodes := []*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}
		overrideOCIServiceSeam(tt, newOciActService(tt, nil), nil)
		overridePendingSeam(tt, func() (*hyperscalermodels.CustomCertificateResponse, error) { return certRespForVersion(4), nil })
		mockSE := database.NewMockStorage(tt)
		mockSE.On("GetNodesByPoolID", ctx, int64(1)).Return(nodes, nil).Once()
		var gotVersion int64
		act := &OciCertificateRotationActivity{
			SE: mockSE,
			verifyOciClientCertFunc: func(_ context.Context, _ *datamodel.Pool, _ []*datamodel.Node, certResp *hyperscalermodels.CustomCertificateResponse) error {
				gotVersion = certResp.Certificate.VersionNumber
				return nil
			},
		}
		require.NoError(tt, act.VerifyOciVCPClientCertificatePending(ctx, &PoolContext{Pool: clientCertPool("u")}, 4))
		assert.Equal(tt, int64(4), gotVersion)
		mockSE.AssertExpectations(tt)
	})
}

func TestOciCertificateRotationActivity_verifyOciClientCertificateOnVSA(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}
	pool := clientCertPool("u")
	nodes := []*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}
	certResp := certRespForVersion(4)

	t.Run("test seam bypasses provider", func(t *testing.T) {
		called := false
		act := &OciCertificateRotationActivity{
			verifyOciClientCertFunc: func(context.Context, *datamodel.Pool, []*datamodel.Node, *hyperscalermodels.CustomCertificateResponse) error {
				called = true
				return nil
			},
		}
		require.NoError(t, act.verifyOciClientCertificateOnVSA(ctx, pool, nodes, certResp))
		assert.True(t, called)
	})

	t.Run("server cert fetch error propagates", func(t *testing.T) {
		origGetCert := vsa.GetCertificateFromCacheOrCAS
		t.Cleanup(func() { vsa.GetCertificateFromCacheOrCAS = origGetCert })
		vsa.GetCertificateFromCacheOrCAS = func(context.Context, *datamodel.ExternalCredRef) (*models.Certificate, error) {
			return nil, errors.New("server cert boom")
		}
		err := a.verifyOciClientCertificateOnVSA(ctx, pool, nodes, certResp)
		require.Error(t, err)
	})
}

// buildOciClientVerifyProvider presents the PENDING client bundle as identity while
// trusting ONTAP's server cert via the server chain. Only construction is unit-tested
// (a live GetONTAPVersion round-trip requires a real ONTAP), mirroring the server-cert
// buildOciVerifyProvider test.
func TestOciCertificateRotationActivity_buildOciClientVerifyProvider(t *testing.T) {
	ctx := context.Background()
	a := &OciCertificateRotationActivity{}
	pool := clientCertPool("u")
	certResp := certRespForVersion(4)

	t.Run("no nodes errors", func(t *testing.T) {
		_, err := a.buildOciClientVerifyProvider(ctx, pool, nil, certResp)
		require.Error(t, err)
	})

	t.Run("server cert fetch error propagates", func(t *testing.T) {
		origGetCert := vsa.GetCertificateFromCacheOrCAS
		t.Cleanup(func() { vsa.GetCertificateFromCacheOrCAS = origGetCert })
		vsa.GetCertificateFromCacheOrCAS = func(context.Context, *datamodel.ExternalCredRef) (*models.Certificate, error) {
			return nil, errors.New("server cert boom")
		}
		nodes := []*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}
		_, err := a.buildOciClientVerifyProvider(ctx, pool, nodes, certResp)
		require.Error(t, err)
	})

	t.Run("success returns provider", func(t *testing.T) {
		origGetCert := vsa.GetCertificateFromCacheOrCAS
		t.Cleanup(func() { vsa.GetCertificateFromCacheOrCAS = origGetCert })
		vsa.GetCertificateFromCacheOrCAS = func(context.Context, *datamodel.ExternalCredRef) (*models.Certificate, error) {
			return &models.Certificate{InterMediateCertificates: []string{"server-ca"}}, nil
		}
		nodes := []*datamodel.Node{{EndpointAddress: "10.0.0.1", HostDNSName: "node-1.example.com"}}
		provider, err := a.buildOciClientVerifyProvider(ctx, pool, nodes, certResp)
		require.NoError(t, err)
		require.NotNil(t, provider)
	})
}

// reconcileVCPCertificateVersion is the retry-idempotency guard: when a prior
// attempt promoted a new client-cert version but crashed before persisting it,
// a later not-due check must converge the DB with the CURRENT OCI version.
func TestOciCertificateRotationActivity_reconcileVCPCertificateVersion(t *testing.T) {
	ctx := context.Background()

	newClientRef := func(version int64) *datamodel.ExternalCredRef {
		return &datamodel.ExternalCredRef{
			ExternalIdentifier: "ocid1.certificate.oc1..cli",
			Name:               "deployment-foo-client-cert",
			Version:            version,
		}
	}

	t.Run("get certificate error propagates", func(tt *testing.T) {
		dispatcher := &ociActDispatcher{
			doFunc: func(_ *http.Request) (*http.Response, error) {
				return nil, &ociActServiceError{statusCode: http.StatusForbidden, code: "Forbidden", message: "denied"}
			},
		}
		svc := newOciActService(tt, dispatcher)
		a := &OciCertificateRotationActivity{}
		pool := eligibleOCIPool("u")
		ref := newClientRef(1)
		pool.PoolCredentials.ExternalClientCertificate = ref

		err := a.reconcileVCPCertificateVersion(ctx, svc, pool, ref)
		require.Error(tt, err)
	})

	t.Run("in-sync version is a no-op", func(tt *testing.T) {
		svc := newOciActService(tt, activeCertMetadataDispatcher())
		overrideCertBundleSeam(tt, nil, nil)
		// nil SE: a stray persist call would panic, proving no DB write happens.
		a := &OciCertificateRotationActivity{}
		pool := eligibleOCIPool("u")
		ref := newClientRef(3) // matches CURRENT version from the dispatcher
		pool.PoolCredentials.ExternalClientCertificate = ref

		require.NoError(tt, a.reconcileVCPCertificateVersion(ctx, svc, pool, ref))
		assert.Equal(tt, int64(3), ref.Version)
	})

	t.Run("stale version converges and persists", func(tt *testing.T) {
		svc := newOciActService(tt, activeCertMetadataDispatcher())
		overrideCertBundleSeam(tt, nil, nil)
		mockSE := database.NewMockStorage(tt)
		mockSE.On("UpdatePoolFields", ctx, "u", mock.Anything).Return(nil).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}
		pool := eligibleOCIPool("u")
		ref := newClientRef(1) // stale: CURRENT is 3
		pool.PoolCredentials.ExternalClientCertificate = ref

		require.NoError(tt, a.reconcileVCPCertificateVersion(ctx, svc, pool, ref))
		assert.Equal(tt, int64(3), ref.Version, "db version must converge to CURRENT")
		mockSE.AssertExpectations(tt)
	})

	t.Run("persist error propagates", func(tt *testing.T) {
		svc := newOciActService(tt, activeCertMetadataDispatcher())
		overrideCertBundleSeam(tt, nil, nil)
		mockSE := database.NewMockStorage(tt)
		mockSE.On("UpdatePoolFields", ctx, "u", mock.Anything).Return(errors.New("db down")).Once()
		a := &OciCertificateRotationActivity{SE: mockSE}
		pool := eligibleOCIPool("u")
		ref := newClientRef(1)
		pool.PoolCredentials.ExternalClientCertificate = ref

		require.Error(tt, a.reconcileVCPCertificateVersion(ctx, svc, pool, ref))
	})
}
