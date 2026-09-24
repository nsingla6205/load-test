package backgroundactivities

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

type certificateAuthorityCloud struct {
	hyperscaler.Services
	ca        *hyperscalermodels.CertificateAuthority
	err       error
	projectID string
	region    string
	poolName  string
	caName    string
}

func (c *certificateAuthorityCloud) GetCertificateAuthority(
	_ context.Context,
	projectID, region, poolName, caName string,
) (*hyperscalermodels.CertificateAuthority, error) {
	c.projectID = projectID
	c.region = region
	c.poolName = poolName
	c.caName = caName
	return c.ca, c.err
}

func TestPreflightCertificateIssuance_GetCertificateAuthorityFailure(t *testing.T) {
	cloudService := hyperscaler.NewMockServices(t)
	cloudService.On(
		"GetCertificateAuthority",
		mock.Anything,
		"project",
		"",
		"pool",
		"ca",
	).Return(nil, errors.New("CA lookup failed")).Once()
	activity := &RotateVcpToVsaCertificateActivity{
		deps: &certificateRotationDependencies{
			resolveCloudService: func(context.Context) (hyperscaler.Services, error) {
				return cloudService, nil
			},
			getHyperscaler: func() string { return "gcp" },
			now:            time.Now,
		},
	}

	err := activity.preflightCertificateIssuance(
		context.Background(),
		&datamodel.Pool{},
		"project/pool/ca",
		"bootstrap-user",
		"svc-user",
	)

	require.ErrorContains(t, err, "fetch target certificate authority: CA lookup failed")
	cloudService.AssertExpectations(t)
}

func TestCertificateRotationDependenciesFillMissingDefaults(t *testing.T) {
	activity := &RotateVcpToVsaCertificateActivity{
		deps: &certificateRotationDependencies{},
	}

	deps := activity.dependencies()

	require.NotNil(t, deps.resolveCloudService)
	require.NotNil(t, deps.getProvider)
	require.NotNil(t, deps.getCertificateAndPrivateKeyByID)
	require.NotNil(t, deps.generateExpertCertificate)
	require.NotNil(t, deps.installExpertClientTrust)
	require.NotNil(t, deps.getHyperscaler)
	require.NotNil(t, deps.now)
}

func TestValidateCertificateAuthority(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		ca        *hyperscalermodels.CertificateAuthority
		floor     time.Duration
		wantError string
	}{
		{
			name:  "remaining lifetime equals floor",
			ca:    &hyperscalermodels.CertificateAuthority{State: "ENABLED", ExpireTime: now.Add(24 * time.Hour)},
			floor: 24 * time.Hour,
		},
		{
			name:      "remaining lifetime below floor",
			ca:        &hyperscalermodels.CertificateAuthority{State: "ENABLED", ExpireTime: now.Add(24*time.Hour - time.Nanosecond)},
			floor:     24 * time.Hour,
			wantError: "below required floor",
		},
		{
			name:      "disabled CA",
			ca:        &hyperscalermodels.CertificateAuthority{State: "DISABLED", ExpireTime: now.Add(48 * time.Hour)},
			floor:     24 * time.Hour,
			wantError: "expected ENABLED",
		},
		{
			name:      "expired CA",
			ca:        &hyperscalermodels.CertificateAuthority{State: "ENABLED", ExpireTime: now},
			floor:     time.Hour,
			wantError: "expired at",
		},
		{name: "nil CA", floor: time.Hour, wantError: "metadata is nil"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCertificateAuthority(test.ca, now, test.floor)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateStagedCertificateForReuse(t *testing.T) {
	now := time.Now()
	t.Run("nil certificate", func(t *testing.T) {
		require.ErrorContains(t, validateStagedCertificateForReuse(nil, now, 0.75), "staged certificate is nil")
	})
	t.Run("invalid PEM", func(t *testing.T) {
		err := validateStagedCertificateForReuse(
			&hyperscalermodels.CustomCertificate{PemCertificate: "not-pem"},
			now,
			0.75,
		)
		require.ErrorContains(t, err, "not exactly one PEM certificate")
	})
	t.Run("invalid DER", func(t *testing.T) {
		err := validateStagedCertificateForReuse(
			&hyperscalermodels.CustomCertificate{
				PemCertificate: string(pem.EncodeToMemory(&pem.Block{
					Type:  "CERTIFICATE",
					Bytes: []byte("not-der"),
				})),
			},
			now,
			0.75,
		)
		require.ErrorContains(t, err, "parse staged certificate")
	})
	tests := []struct {
		name      string
		notBefore time.Time
		notAfter  time.Time
		threshold float64
		wantError string
	}{
		{
			name:      "valid and below threshold",
			notBefore: now.Add(-time.Hour),
			notAfter:  now.Add(3 * time.Hour),
			threshold: 0.75,
		},
		{
			name:      "expired",
			notBefore: now.Add(-2 * time.Hour),
			notAfter:  now,
			threshold: 0.75,
			wantError: "expired",
		},
		{
			name:      "not valid yet",
			notBefore: now.Add(time.Hour),
			notAfter:  now.Add(2 * time.Hour),
			threshold: 0.75,
			wantError: "not valid before",
		},
		{
			name:      "zero threshold",
			notBefore: now.Add(-time.Hour),
			notAfter:  now.Add(3 * time.Hour),
			threshold: 0,
			wantError: "invalid certificate rotation threshold",
		},
		{
			name:      "threshold above one",
			notBefore: now.Add(-time.Hour),
			notAfter:  now.Add(3 * time.Hour),
			threshold: 1.1,
			wantError: "invalid certificate rotation threshold",
		},
		{
			name:      "normal rotation threshold reached",
			notBefore: now.Add(-3 * time.Hour),
			notAfter:  now.Add(time.Hour),
			threshold: 0.75,
			wantError: "rotation threshold",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateStagedCertificateForReuse(
				&hyperscalermodels.CustomCertificate{
					PemCertificate: generateTestCertificate(test.notBefore, test.notAfter),
				},
				now,
				test.threshold,
			)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateCertificateForExpectedIssuer(t *testing.T) {
	const (
		expectedCA     = "project/pool/expected-ca"
		expectedRegion = "us-east4"
	)
	tests := []struct {
		name        string
		certificate *hyperscalermodels.CustomCertificate
		wantError   string
	}{
		{
			name: "complete structured identity",
			certificate: &hyperscalermodels.CustomCertificate{
				CertOwningEntity: "project",
				CaGroupName:      "pool",
				CaName:           "expected-ca",
				Region:           expectedRegion,
			},
		},
		{
			name: "full issuer resource identity",
			certificate: &hyperscalermodels.CustomCertificate{
				IssuerCertificateAuthority: "projects/project/locations/us-east4/caPools/pool/certificateAuthorities/expected-ca",
			},
		},
		{
			name: "same pool wrong CA name",
			certificate: &hyperscalermodels.CustomCertificate{
				CertOwningEntity: "project",
				CaGroupName:      "pool",
				CaName:           "wrong-ca",
				Region:           expectedRegion,
			},
			wantError: "CA name",
		},
		{
			name: "empty identity cannot prove issuer",
			certificate: &hyperscalermodels.CustomCertificate{
				PemCertificate: "certificate-only",
			},
			wantError: "cannot be proven",
		},
		{
			name:      "nil certificate",
			wantError: "metadata is nil",
		},
		{
			name:        "invalid expected CA URI",
			certificate: &hyperscalermodels.CustomCertificate{},
			wantError:   "invalid expected CA URI",
		},
		{
			name: "invalid issuer resource format",
			certificate: &hyperscalermodels.CustomCertificate{
				IssuerCertificateAuthority: "projects/project/caPools/pool",
			},
			wantError: "invalid format",
		},
		{
			name: "issuer resource does not match expected CA",
			certificate: &hyperscalermodels.CustomCertificate{
				IssuerCertificateAuthority: "projects/project/locations/us-east4/caPools/pool/certificateAuthorities/wrong-ca",
			},
			wantError: "does not match expected CA",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expectedCAForTest := expectedCA
			if test.name == "invalid expected CA URI" {
				expectedCAForTest = "project//ca"
			}
			err := validateCertificateForExpectedIssuer(
				test.certificate,
				expectedCAForTest,
				expectedRegion,
			)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateCertificateForReuse(t *testing.T) {
	now := time.Now()
	valid := &hyperscalermodels.CustomCertificate{
		CertOwningEntity: "project",
		CaGroupName:      "pool",
		CaName:           "ca",
		Region:           "us-east4",
		PemCertificate:   generateTestCertificate(now.Add(-time.Hour), now.Add(3*time.Hour)),
	}

	require.NoError(t, validateCertificateForReuse(valid, "project/pool/ca", "us-east4", now, 0.75))
	require.ErrorContains(
		t,
		validateCertificateForReuse(valid, "project/pool/wrong-ca", "us-east4", now, 0.75),
		"CA name",
	)
}

func TestDetachAndRevokeStagedPoolCertificate_OrdersDetachBeforeRevoke(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType:         2,
			CertificateIDNew: "staged-cert",
			StagedCaURI:      "project/pool/ca",
		},
	}
	order := make([]string, 0, 2)
	storage.On(
		"CompareAndSwapPoolCredentials",
		ctx,
		pool.UUID,
		2,
		"staged-cert",
		"project/pool/ca",
		mock.Anything,
		mock.Anything,
	).Run(func(mock.Arguments) {
		order = append(order, "detach")
	}).Return(nil).Once()
	originalRevoke := revokeCertificateAndDeleteFromCacheAndSecretManager
	t.Cleanup(func() { revokeCertificateAndDeleteFromCacheAndSecretManager = originalRevoke })
	revokeCertificateAndDeleteFromCacheAndSecretManager = func(
		hyperscaler.Services,
		*datamodel.PoolCredentials,
	) error {
		order = append(order, "revoke")
		return nil
	}

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
		detachAndRevokeStagedPoolCertificate(
			ctx,
			pool,
			"staged-cert",
			"project/pool/ca",
			&certificateAuthorityCloud{},
		)

	require.NoError(t, err)
	require.Equal(t, []string{"detach", "revoke"}, order)
	require.Empty(t, pool.PoolCredentials.CertificateIDNew)
	require.Empty(t, pool.PoolCredentials.StagedCaURI)
}

func TestDetachAndRevokeStagedExpertCertificate_OrdersDetachBeforeRevoke(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		ExpertModeCredentials: &datamodel.ExpertModeCredentials{
			ExpertModeCredential: []*datamodel.ExpertModeCredential{
				{
					CertificateIDNew: "staged-expert-cert",
					StagedCaURI:      "project/pool/ca",
				},
			},
		},
	}
	order := make([]string, 0, 2)
	storage.On("ListPools", ctx, mock.Anything).
		Return([]*datamodel.PoolView{{Pool: *pool}}, nil).
		Once()
	storage.On("CompareAndSwapExpertModeCredentials", ctx, pool.UUID, mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { order = append(order, "detach") }).
		Return(nil).
		Once()
	originalRevoke := revokeCertificateAndDeleteFromCacheAndSecretManager
	t.Cleanup(func() { revokeCertificateAndDeleteFromCacheAndSecretManager = originalRevoke })
	revokeCertificateAndDeleteFromCacheAndSecretManager = func(
		hyperscaler.Services,
		*datamodel.PoolCredentials,
	) error {
		order = append(order, "revoke")
		return nil
	}

	err := (&RotateVcpToVsaCertificateActivity{SE: storage}).
		detachAndRevokeStagedExpertCertificate(
			ctx,
			pool.UUID,
			0,
			"staged-expert-cert",
			"project/pool/ca",
			&certificateAuthorityCloud{},
		)

	require.NoError(t, err)
	require.Equal(t, []string{"detach", "revoke"}, order)
}

func TestPreflightCertificateIssuanceRejectsDisabledCABeforeONTAP(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	cloud := &certificateAuthorityCloud{
		ca: &hyperscalermodels.CertificateAuthority{
			State:      "DISABLED",
			ExpireTime: now.Add(4 * 365 * 24 * time.Hour),
		},
	}
	providerCalled := false
	activity := &RotateVcpToVsaCertificateActivity{
		SE: database.NewMockStorage(t),
		deps: &certificateRotationDependencies{
			resolveCloudService: func(context.Context) (hyperscaler.Services, error) {
				return cloud, nil
			},
			getProvider: func(
				context.Context,
				*datamodel.Pool,
				[]*datamodel.Node,
				int,
				string,
			) (vsa.Provider, error) {
				providerCalled = true
				return nil, errors.New("must not create ONTAP provider")
			},
			now: func() time.Time { return now },
		},
	}

	err := activity.preflightCertificateIssuance(
		context.Background(),
		&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-1"}},
		"project/pool/ca",
		"bootstrap-user",
		"svc-user",
	)

	require.ErrorContains(t, err, "expected ENABLED")
	require.False(t, providerCalled)
}

func TestParseTargetCAURI(t *testing.T) {
	t.Run("trims whitespace around components", func(t *testing.T) {
		projectID, poolName, caName, err := parseTargetCAURI("  project / pool / ca  ")
		require.NoError(t, err)
		require.Equal(t, "project", projectID)
		require.Equal(t, "pool", poolName)
		require.Equal(t, "ca", caName)
	})
	t.Run("rejects blank component after trim", func(t *testing.T) {
		_, _, _, err := parseTargetCAURI("project/ /ca")
		require.ErrorContains(t, err, "invalid target CA URI")
	})
	t.Run("rejects wrong number of parts", func(t *testing.T) {
		_, _, _, err := parseTargetCAURI("project/pool")
		require.ErrorContains(t, err, "invalid target CA URI")
	})
}

func TestPreflightCertificateIssuanceTrimsCAURIComponents(t *testing.T) {
	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	cloud := &certificateAuthorityCloud{
		ca: &hyperscalermodels.CertificateAuthority{
			State:      "DISABLED",
			ExpireTime: now.Add(4 * 365 * 24 * time.Hour),
		},
	}
	activity := &RotateVcpToVsaCertificateActivity{
		deps: &certificateRotationDependencies{
			resolveCloudService: func(context.Context) (hyperscaler.Services, error) {
				return cloud, nil
			},
			now: func() time.Time { return now },
		},
	}

	err := activity.preflightCertificateIssuance(
		context.Background(),
		&datamodel.Pool{},
		"  project / pool / ca ",
		"bootstrap-user",
		"svc-user",
	)

	require.ErrorContains(t, err, "expected ENABLED")
	require.Equal(t, "project", cloud.projectID)
	require.Equal(t, "pool", cloud.poolName)
	require.Equal(t, "ca", cloud.caName)
}

func createCAChain(t *testing.T) (rootPEM, intermediatePEM string) {
	t.Helper()
	now := time.Now()
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	root := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "root-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(48 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	require.NoError(t, err)
	root, err = x509.ParseCertificate(rootDER)
	require.NoError(t, err)

	intermediateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	intermediate := &x509.Certificate{
		SerialNumber:          big.NewInt(101),
		Subject:               pkix.Name{CommonName: "intermediate-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	intermediateDER, err := x509.CreateCertificate(
		rand.Reader,
		intermediate,
		root,
		&intermediateKey.PublicKey,
		rootKey,
	)
	require.NoError(t, err)
	return encodeCertificate(rootDER), encodeCertificate(intermediateDER)
}

func encodeCertificate(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestNormalizeCertificateAuthorityChain(t *testing.T) {
	rootPEM, intermediatePEM := createCAChain(t)

	ordered, err := normalizeCertificateAuthorityChain([]string{intermediatePEM, rootPEM})
	require.NoError(t, err)
	require.Len(t, ordered, 2)
	require.Equal(t, "root-ca", ordered[0].cert.Subject.CommonName)
	require.Equal(t, "intermediate-ca", ordered[1].cert.Subject.CommonName)

	ordered, err = normalizeCertificateAuthorityChain([]string{rootPEM, rootPEM, intermediatePEM})
	require.NoError(t, err)
	require.Len(t, ordered, 2)

	_, err = normalizeCertificateAuthorityChain([]string{"not-pem"})
	require.ErrorContains(t, err, "not exactly one PEM certificate")

	unrelatedPEM, _ := createCAChain(t)
	_, err = normalizeCertificateAuthorityChain([]string{rootPEM, intermediatePEM, unrelatedPEM})
	require.Error(t, err)

	_, err = normalizeCertificateAuthorityChain(nil)
	require.ErrorContains(t, err, "PEM chain is empty")

	_, err = normalizeCertificateAuthorityChain([]string{encodeCertificate([]byte("not-der"))})
	require.ErrorContains(t, err, "parse certificate authority chain entry")

	_, err = normalizeCertificateAuthorityChain([]string{intermediatePEM})
	require.ErrorContains(t, err, "exactly one self-signed root")
}

func TestInstallAndVerifyCertificateAuthorityChain(t *testing.T) {
	rootPEM, intermediatePEM := createCAChain(t)
	chain, err := normalizeCertificateAuthorityChain([]string{intermediatePEM, rootPEM})
	require.NoError(t, err)

	t.Run("installs root to leaf as client and server CA and verifies exact DER", func(t *testing.T) {
		provider := vsa.NewMockProvider(t)
		provider.On("InstallClientCA", mock.MatchedBy(func(params vsa.InstallClientCAParams) bool {
			return params.SvmName == "" &&
				params.CommonName == "root-ca" &&
				strings.HasPrefix(params.CertificateName, "vcp-ca-")
		})).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		provider.On("GetServerCertificates", mock.MatchedBy(func(params vsa.GetServerCertificateParams) bool {
			return params.CertificateType == vsa.TrustCertificateTypeClientCA &&
				strings.HasPrefix(params.CertificateName, "vcp-ca-")
		})).Return(
			[]*vsa.ServerCertificateResponse{{PublicCertificate: rootPEM}},
			nil,
		).Once()
		provider.On("InstallServerCA", mock.MatchedBy(func(params vsa.InstallServerCAParams) bool {
			return params.SvmName == "" &&
				params.CommonName == "root-ca" &&
				strings.HasPrefix(params.CertificateName, "vcp-server-ca-")
		})).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		provider.On("GetServerCertificates", mock.MatchedBy(func(params vsa.GetServerCertificateParams) bool {
			return params.CertificateType == vsa.TrustCertificateTypeServerCA &&
				strings.HasPrefix(params.CertificateName, "vcp-server-ca-")
		})).Return(
			[]*vsa.ServerCertificateResponse{{PublicCertificate: rootPEM}},
			nil,
		).Once()
		provider.On("InstallClientCA", mock.MatchedBy(func(params vsa.InstallClientCAParams) bool {
			return params.SvmName == "" &&
				params.CommonName == "intermediate-ca" &&
				strings.HasPrefix(params.CertificateName, "vcp-ca-")
		})).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		provider.On("GetServerCertificates", mock.MatchedBy(func(params vsa.GetServerCertificateParams) bool {
			return params.CertificateType == vsa.TrustCertificateTypeClientCA &&
				strings.HasPrefix(params.CertificateName, "vcp-ca-")
		})).Return(
			[]*vsa.ServerCertificateResponse{{PublicCertificate: intermediatePEM}},
			nil,
		).Once()
		provider.On("InstallServerCA", mock.MatchedBy(func(params vsa.InstallServerCAParams) bool {
			return params.SvmName == "" &&
				params.CommonName == "intermediate-ca" &&
				strings.HasPrefix(params.CertificateName, "vcp-server-ca-")
		})).Return(&vsa.ServerCertificateResponse{}, nil).Once()
		provider.On("GetServerCertificates", mock.MatchedBy(func(params vsa.GetServerCertificateParams) bool {
			return params.CertificateType == vsa.TrustCertificateTypeServerCA &&
				strings.HasPrefix(params.CertificateName, "vcp-server-ca-")
		})).Return(
			[]*vsa.ServerCertificateResponse{{PublicCertificate: intermediatePEM}},
			nil,
		).Once()

		require.NoError(t, installAndVerifyCertificateAuthorityChain(provider, chain))
		provider.AssertExpectations(t)
	})

	t.Run("duplicate install remains idempotent", func(t *testing.T) {
		provider := vsa.NewMockProvider(t)
		provider.On("InstallClientCA", mock.Anything).
			Return(nil, errors.New("certificate already exists")).
			Twice()
		provider.On("InstallServerCA", mock.Anything).
			Return(nil, errors.New("certificate already exists")).
			Twice()
		provider.On("GetServerCertificates", mock.Anything).
			Return([]*vsa.ServerCertificateResponse{{PublicCertificate: rootPEM}}, nil).
			Twice()
		provider.On("GetServerCertificates", mock.Anything).
			Return([]*vsa.ServerCertificateResponse{{PublicCertificate: intermediatePEM}}, nil).
			Twice()

		require.NoError(t, installAndVerifyCertificateAuthorityChain(provider, chain))
	})

	t.Run("server CA install failure is returned", func(t *testing.T) {
		provider := vsa.NewMockProvider(t)
		provider.On("InstallClientCA", mock.Anything).
			Return(&vsa.ServerCertificateResponse{}, nil).
			Once()
		provider.On("GetServerCertificates", mock.Anything).
			Return([]*vsa.ServerCertificateResponse{{PublicCertificate: rootPEM}}, nil).
			Once()
		provider.On("InstallServerCA", mock.Anything).
			Return(nil, errors.New("server CA install failed")).
			Once()

		err := installAndVerifyCertificateAuthorityChain(provider, chain)
		require.ErrorContains(t, err, "install server_ca")
	})

	t.Run("server CA readback failure is returned", func(t *testing.T) {
		provider := vsa.NewMockProvider(t)
		provider.On("InstallClientCA", mock.Anything).
			Return(&vsa.ServerCertificateResponse{}, nil).
			Once()
		provider.On("GetServerCertificates", mock.MatchedBy(func(params vsa.GetServerCertificateParams) bool {
			return params.CertificateType == vsa.TrustCertificateTypeClientCA
		})).Return([]*vsa.ServerCertificateResponse{{PublicCertificate: rootPEM}}, nil).Once()
		provider.On("InstallServerCA", mock.Anything).
			Return(&vsa.ServerCertificateResponse{}, nil).
			Once()
		provider.On("GetServerCertificates", mock.MatchedBy(func(params vsa.GetServerCertificateParams) bool {
			return params.CertificateType == vsa.TrustCertificateTypeServerCA
		})).Return(nil, errors.New("server CA readback failed")).Once()

		err := installAndVerifyCertificateAuthorityChain(provider, chain)
		require.ErrorContains(t, err, "read back server_ca")
	})

	t.Run("mismatch fails verification", func(t *testing.T) {
		provider := vsa.NewMockProvider(t)
		provider.On("InstallClientCA", mock.Anything).
			Return(&vsa.ServerCertificateResponse{}, nil).
			Once()
		provider.On("GetServerCertificates", mock.Anything).
			Return([]*vsa.ServerCertificateResponse{{PublicCertificate: intermediatePEM}}, nil).
			Once()

		err := installAndVerifyCertificateAuthorityChain(provider, chain)
		require.ErrorContains(t, err, "does not match expected fingerprint")
	})

	t.Run("missing readback fails verification", func(t *testing.T) {
		provider := vsa.NewMockProvider(t)
		provider.On("InstallClientCA", mock.Anything).
			Return(&vsa.ServerCertificateResponse{}, nil).
			Once()
		provider.On("GetServerCertificates", mock.Anything).
			Return([]*vsa.ServerCertificateResponse{}, nil).
			Once()

		err := installAndVerifyCertificateAuthorityChain(provider, chain)
		require.ErrorContains(t, err, "does not match expected fingerprint")
	})

	t.Run("install failure is returned", func(t *testing.T) {
		provider := vsa.NewMockProvider(t)
		provider.On("InstallClientCA", mock.Anything).
			Return(nil, errors.New("install failed")).
			Once()

		err := installAndVerifyCertificateAuthorityChain(provider, chain)
		require.ErrorContains(t, err, "install client_ca")
	})

	t.Run("readback failure is returned", func(t *testing.T) {
		provider := vsa.NewMockProvider(t)
		provider.On("InstallClientCA", mock.Anything).
			Return(&vsa.ServerCertificateResponse{}, nil).
			Once()
		provider.On("GetServerCertificates", mock.Anything).
			Return(nil, errors.New("readback failed")).
			Once()

		err := installAndVerifyCertificateAuthorityChain(provider, chain)
		require.ErrorContains(t, err, "read back client_ca")
	})
}

func TestVerifyInstalledCertificateSkipsInvalidCandidates(t *testing.T) {
	rootPEM, _ := createCAChain(t)
	block, _ := pem.Decode([]byte(rootPEM))
	require.NotNil(t, block)
	expected, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	err = verifyInstalledCertificate(expected, []*vsa.ServerCertificateResponse{
		nil,
		{},
		{PublicCertificate: "not-pem"},
	})
	require.ErrorContains(t, err, "does not match expected fingerprint")
}

type accountCapableProvider struct {
	*vsa.MockProvider
	accounts      []*vsa.SecurityAccount
	getErrors     []error
	modifyError   error
	modifyParams  []vsa.ModifySecurityAccountParams
	clusterUUID   string
	clusterIDErr  error
	getCallNumber int
}

func (p *accountCapableProvider) GetRoleOwnerUUID(string) (string, error) {
	return "", nil
}

func (p *accountCapableProvider) GetClusterUUID() (string, error) {
	return p.clusterUUID, p.clusterIDErr
}

func (p *accountCapableProvider) GetSecurityAccount(vsa.GetSecurityAccountParams) (*vsa.SecurityAccount, error) {
	index := p.getCallNumber
	p.getCallNumber++
	if index < len(p.getErrors) && p.getErrors[index] != nil {
		return nil, p.getErrors[index]
	}
	if index >= len(p.accounts) {
		return nil, nil
	}
	return p.accounts[index], nil
}

func (p *accountCapableProvider) CreateSecurityAccount(vsa.CreateSecurityAccountParams) error {
	return nil
}

func (p *accountCapableProvider) ModifySecurityAccount(params vsa.ModifySecurityAccountParams) error {
	p.modifyParams = append(p.modifyParams, params)
	return p.modifyError
}

func (p *accountCapableProvider) DeleteSecurityAccount(vsa.DeleteSecurityAccountParams) error {
	return nil
}

func certificateLoginAccount(ownerUUID string) *vsa.SecurityAccount {
	return &vsa.SecurityAccount{
		Name:      "svc-user",
		OwnerUUID: ownerUUID,
		Applications: []ontaprest.SecurityAccountApplication{
			{Application: "http", AuthenticationMethods: []string{"password", "certificate"}},
			{Application: "ontapi", AuthenticationMethods: []string{"password", "certificate"}},
		},
	}
}

func TestEnsureAndVerifyCertificateLogin(t *testing.T) {
	t.Run("provider without account support", func(t *testing.T) {
		err := ensureAndVerifyCertificateLogin(vsa.NewMockProvider(t), "svc-user")
		require.ErrorContains(t, err, "does not support security account verification")
	})

	t.Run("initial account lookup failure", func(t *testing.T) {
		provider := &accountCapableProvider{
			MockProvider: vsa.NewMockProvider(t),
			getErrors:    []error{errors.New("lookup failed")},
		}
		err := ensureAndVerifyCertificateLogin(provider, "svc-user")
		require.ErrorContains(t, err, "get security account")
	})

	t.Run("missing account", func(t *testing.T) {
		provider := &accountCapableProvider{MockProvider: vsa.NewMockProvider(t)}
		err := ensureAndVerifyCertificateLogin(provider, "svc-user")
		require.ErrorContains(t, err, "does not exist")
	})

	t.Run("already configured", func(t *testing.T) {
		provider := &accountCapableProvider{
			MockProvider: vsa.NewMockProvider(t),
			accounts:     []*vsa.SecurityAccount{certificateLoginAccount("owner")},
		}
		require.NoError(t, ensureAndVerifyCertificateLogin(provider, "svc-user"))
		require.Empty(t, provider.modifyParams)
	})

	t.Run("repairs and verifies", func(t *testing.T) {
		existingApplications := []ontaprest.SecurityAccountApplication{
			{Application: "ssh", AuthenticationMethods: []string{"password", "publickey"}},
			{Application: "http", AuthenticationMethods: []string{"saml"}},
		}
		mergedApplications := mergeCertificateApplications(existingApplications)
		provider := &accountCapableProvider{
			MockProvider: vsa.NewMockProvider(t),
			accounts: []*vsa.SecurityAccount{
				{Name: "svc-user", Applications: existingApplications},
				{Name: "svc-user", OwnerUUID: "cluster-owner", Applications: mergedApplications},
			},
			clusterUUID: "cluster-owner",
		}
		require.NoError(t, ensureAndVerifyCertificateLogin(provider, "svc-user"))
		require.Len(t, provider.modifyParams, 1)
		require.Equal(t, "cluster-owner", provider.modifyParams[0].OwnerUUID)
		require.Contains(t, provider.modifyParams[0].Applications, existingApplications[0])
		require.Contains(
			t,
			provider.modifyParams[0].Applications,
			ontaprest.SecurityAccountApplication{
				Application:           "http",
				AuthenticationMethods: []string{"saml", "password", "certificate"},
			},
		)
	})

	t.Run("repair failure", func(t *testing.T) {
		provider := &accountCapableProvider{
			MockProvider: vsa.NewMockProvider(t),
			accounts:     []*vsa.SecurityAccount{{Name: "svc-user", OwnerUUID: "owner"}},
			modifyError:  errors.New("modify failed"),
		}
		err := ensureAndVerifyCertificateLogin(provider, "svc-user")
		require.ErrorContains(t, err, "modify failed")
	})

	t.Run("cluster UUID lookup failure", func(t *testing.T) {
		provider := &accountCapableProvider{
			MockProvider: vsa.NewMockProvider(t),
			accounts:     []*vsa.SecurityAccount{{Name: "svc-user"}},
			clusterIDErr: errors.New("cluster lookup failed"),
		}
		err := ensureAndVerifyCertificateLogin(provider, "svc-user")
		require.ErrorContains(t, err, "get cluster UUID")
	})

	t.Run("readback failure", func(t *testing.T) {
		provider := &accountCapableProvider{
			MockProvider: vsa.NewMockProvider(t),
			accounts:     []*vsa.SecurityAccount{{Name: "svc-user", OwnerUUID: "owner"}},
			getErrors:    []error{nil, errors.New("readback failed")},
		}
		err := ensureAndVerifyCertificateLogin(provider, "svc-user")
		require.ErrorContains(t, err, "read back repaired security account")
	})

	t.Run("readback still missing certificate auth", func(t *testing.T) {
		provider := &accountCapableProvider{
			MockProvider: vsa.NewMockProvider(t),
			accounts: []*vsa.SecurityAccount{
				{Name: "svc-user", OwnerUUID: "owner"},
				{Name: "svc-user", OwnerUUID: "owner"},
			},
		}
		err := ensureAndVerifyCertificateLogin(provider, "svc-user")
		require.ErrorContains(t, err, "does not enable certificate authentication")
	})
}

func TestHasCertificateApplications(t *testing.T) {
	require.False(t, hasCertificateApplications(nil))
	require.False(t, hasCertificateApplications(&vsa.SecurityAccount{
		Applications: []ontaprest.SecurityAccountApplication{
			{Application: "ssh", AuthenticationMethods: []string{"password"}},
			{Application: "http", AuthenticationMethods: []string{"password"}},
		},
	}))
	require.True(t, hasCertificateApplications(certificateLoginAccount("owner")))
}

func TestMergeCertificateApplications_PreservesAndIsIdempotent(t *testing.T) {
	existing := []ontaprest.SecurityAccountApplication{
		{Application: "ssh", AuthenticationMethods: []string{"password", "publickey"}},
		{Application: "console", AuthenticationMethods: []string{"password"}},
		{Application: "http", AuthenticationMethods: []string{"saml", "certificate"}},
		{Application: "ontapi", AuthenticationMethods: []string{"certificate", "token"}},
	}

	merged := mergeCertificateApplications(existing)
	mergedAgain := mergeCertificateApplications(merged)

	require.Equal(t, merged, mergedAgain)
	require.Contains(t, merged, existing[0])
	require.Contains(t, merged, existing[1])
	require.Contains(t, merged, ontaprest.SecurityAccountApplication{
		Application:           "http",
		AuthenticationMethods: []string{"saml", "certificate", "password"},
	})
	require.Contains(t, merged, ontaprest.SecurityAccountApplication{
		Application:           "ontapi",
		AuthenticationMethods: []string{"certificate", "token", "password"},
	})
}
