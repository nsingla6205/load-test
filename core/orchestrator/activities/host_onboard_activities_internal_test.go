package activities

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func hostOnboardTestLogger(t *testing.T) log.Logger {
	t.Helper()
	logger := log.NewMockLogger(t)
	logger.On("Infof", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Maybe()
	return logger
}

func TestInstallHostTrustCertificate(t *testing.T) {
	tests := []struct {
		name            string
		certificateType string
		installError    error
		expectedError   string
	}{
		{
			name:            "installs client CA",
			certificateType: vsa.TrustCertificateTypeClientCA,
		},
		{
			name:            "installs server CA",
			certificateType: vsa.TrustCertificateTypeServerCA,
		},
		{
			name:            "treats duplicate as success",
			certificateType: vsa.TrustCertificateTypeClientCA,
			installError:    errors.New("duplicate entry"),
		},
		{
			name:            "returns provider error",
			certificateType: vsa.TrustCertificateTypeServerCA,
			installError:    errors.New("ONTAP install failed"),
			expectedError:   "ONTAP install failed",
		},
		{
			name:            "rejects unsupported certificate type",
			certificateType: "unsupported",
			expectedError:   `unsupported trust certificate type "unsupported"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := vsa.NewMockProvider(t)
			params := hostCACertificateParams{
				SvmName:         "external-svm",
				CertificateName: "trust-certificate",
				Certificate:     "certificate-pem",
				CommonName:      "trust-ca",
				CertificateType: tt.certificateType,
			}

			switch tt.certificateType {
			case vsa.TrustCertificateTypeClientCA:
				provider.EXPECT().InstallClientCA(vsa.InstallClientCAParams{
					SvmName:         params.SvmName,
					CertificateName: params.CertificateName,
					Certificate:     params.Certificate,
					CommonName:      params.CommonName,
				}).Return(&vsa.ServerCertificateResponse{}, tt.installError)
			case vsa.TrustCertificateTypeServerCA:
				provider.EXPECT().InstallServerCA(vsa.InstallServerCAParams{
					SvmName:         params.SvmName,
					CertificateName: params.CertificateName,
					Certificate:     params.Certificate,
					CommonName:      params.CommonName,
				}).Return(&vsa.ServerCertificateResponse{}, tt.installError)
			}

			err := installHostCACertificate(
				hostOnboardTestLogger(t),
				provider,
				"host-uuid",
				params,
			)

			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func TestHostOnboardActivity_PersistHostNodes(t *testing.T) {
	dbHost := &datamodel.Host{
		BaseModel: datamodel.BaseModel{ID: 42},
		IPAddress: "10.0.0.1",
	}

	t.Run("persists each named node with host id", func(t *testing.T) {
		storage := database.NewMockStorage(t)
		storage.EXPECT().EnsureHostNode(mock.Anything, &datamodel.Node{
			Name:            "node-1",
			EndpointAddress: "10.0.0.1",
			NodeAttributes:  &datamodel.NodeDetails{ExternalUUID: "uuid-1", HostID: 42},
		}).Return(&datamodel.Node{}, nil)
		storage.EXPECT().EnsureHostNode(mock.Anything, &datamodel.Node{
			Name:            "node-2",
			EndpointAddress: "10.0.0.1",
			NodeAttributes:  &datamodel.NodeDetails{ExternalUUID: "uuid-2", HostID: 42},
		}).Return(&datamodel.Node{}, nil)

		hostActivity := &HostOnboardActivity{SE: storage}
		err := hostActivity.persistHostNodes(context.Background(), dbHost, []*vsa.Node{
			{Name: "node-1", ExternalUUID: "uuid-1"},
			{Name: "node-2", ExternalUUID: "uuid-2"},
		})
		require.NoError(t, err)
	})

	t.Run("skips nil and unnamed nodes", func(t *testing.T) {
		storage := database.NewMockStorage(t)

		hostActivity := &HostOnboardActivity{SE: storage}
		err := hostActivity.persistHostNodes(context.Background(), dbHost, []*vsa.Node{
			nil,
			{Name: "  "},
		})
		require.NoError(t, err)
	})

	t.Run("returns error when persistence fails", func(t *testing.T) {
		storage := database.NewMockStorage(t)
		storage.EXPECT().EnsureHostNode(mock.Anything, mock.Anything).
			Return(nil, errors.New("db unavailable"))

		hostActivity := &HostOnboardActivity{SE: storage}
		err := hostActivity.persistHostNodes(context.Background(), dbHost, []*vsa.Node{
			{Name: "node-1"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `node "node-1"`)
		assert.Contains(t, err.Error(), "db unavailable")
	})
}

func TestTrustChainInstallIndices(t *testing.T) {
	tests := []struct {
		name     string
		chainLen int
		expected []int
	}{
		{name: "empty chain", chainLen: 0, expected: nil},
		{name: "negative length", chainLen: -1, expected: nil},
		{name: "single certificate", chainLen: 1, expected: []int{0}},
		{name: "root first order", chainLen: 3, expected: []int{2, 1, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, trustChainInstallIndices(tt.chainLen))
		})
	}
}

func TestHostServerInstallIntermediatePEMs(t *testing.T) {
	assert.Nil(t, hostServerInstallIntermediatePEMs(nil))
	assert.Nil(t, hostServerInstallIntermediatePEMs([]string{"", "  "}))
	assert.Equal(
		t,
		[]string{"intermediate-one", "intermediate-two"},
		hostServerInstallIntermediatePEMs([]string{
			" intermediate-one ",
			"",
			"intermediate-two",
		}),
	)
}

func TestIsHostDuplicateCertificateError(t *testing.T) {
	assert.False(t, isHostDuplicateCertificateError(nil))
	assert.True(t, isHostDuplicateCertificateError(errors.New("duplicate entry detected")))
	assert.True(t, isHostDuplicateCertificateError(errors.New("certificate already exists")))
	assert.False(t, isHostDuplicateCertificateError(errors.New("ONTAP install failed")))
}

func TestHostTrustCertificateCommonName(t *testing.T) {
	caPEM := generateTestCertPEMForActivities(t, "ca-subject", "root-ca", nil, 1)

	assert.Equal(t, "ca-subject", hostTrustCertificateCommonName(caPEM, nil))
	assert.Equal(t, "model-ca", hostTrustCertificateCommonName("", &hyperscaler_models.CustomCertificate{
		CaName: "model-ca",
	}))
	assert.Equal(t, "issuer-ca", hostTrustCertificateCommonName("", &hyperscaler_models.CustomCertificate{
		IssuerCertificateAuthority: "issuer-ca",
	}))
	assert.Empty(t, hostTrustCertificateCommonName("", nil))
	assert.Empty(t, hostTrustCertificateCommonName("", &hyperscaler_models.CustomCertificate{}))
}

func generateTestCertPEMForActivities(t *testing.T, subjectCN, issuerCN string, dnsNames []string, serial int64) string {
	t.Helper()

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: subjectCN},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	parent := template
	signer := leafKey
	if issuerCN != subjectCN {
		parentKey, parentKeyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, parentKeyErr)
		parentTemplate := &x509.Certificate{
			SerialNumber:          big.NewInt(serial + 1),
			Subject:               pkix.Name{CommonName: issuerCN},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(time.Hour),
			KeyUsage:              x509.KeyUsageCertSign,
			BasicConstraintsValid: true,
			IsCA:                  true,
		}
		parentDER, parentErr := x509.CreateCertificate(
			rand.Reader,
			parentTemplate,
			parentTemplate,
			&parentKey.PublicKey,
			parentKey,
		)
		require.NoError(t, parentErr)
		parent, parentErr = x509.ParseCertificate(parentDER)
		require.NoError(t, parentErr)
		signer = parentKey
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parent, &leafKey.PublicKey, signer)
	require.NoError(t, err)

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
