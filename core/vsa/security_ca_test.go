package vsa

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func setupCertificateInstallTest(
	t *testing.T,
) (*OntapRestProvider, *ontapRest.MockRESTClient, *ontapRest.MockSecurityClient) {
	t.Helper()

	client := new(ontapRest.MockRESTClient)
	securityClient := new(ontapRest.MockSecurityClient)
	originalGetOntapClient := getOntapClientFunc
	getOntapClientFunc = func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		getOntapClientFunc = originalGetOntapClient
	})

	client.On("Security").Return(securityClient)
	return &OntapRestProvider{Logger: &mockLogger{}}, client, securityClient
}

func certificateInstallResult(name, certificateType string) *ontapRest.ServerRootCACertificate {
	return &ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			UUID: nillable.GetStringPtr("certificate-uuid"),
			Name: nillable.GetStringPtr(name),
			Type: nillable.GetStringPtr(certificateType),
		},
	}
}

func TestInstallServerCertificate_IntermediateCertificatesAreOptional(t *testing.T) {
	tests := []struct {
		name                  string
		intermediateCerts     []string
		expectedIntermediates []string
	}{
		{
			name: "omits intermediates when none are supplied",
		},
		{
			name:                  "includes supplied non-empty intermediates",
			intermediateCerts:     []string{" intermediate-one ", "", "intermediate-two"},
			expectedIntermediates: []string{"intermediate-one", "intermediate-two"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, client, securityClient := setupCertificateInstallTest(t)
			securityClient.On(
				"ServerRootCACertificateInstall",
				mock.MatchedBy(func(params *ontapRest.ServerRootCAInstallParams) bool {
					if len(params.IntermediateCertificates) != len(tt.expectedIntermediates) {
						return false
					}
					for i, expected := range tt.expectedIntermediates {
						if params.IntermediateCertificates[i] == nil ||
							*params.IntermediateCertificates[i] != expected {
							return false
						}
					}
					return true
				}),
			).Return(certificateInstallResult("server-cert", "server"), nil)

			response, err := provider.InstallServerCertificate(InstallServerCertificateParams{
				SvmName:                  "svm-one",
				CertificateName:          "server-cert",
				Certificate:              "leaf-pem",
				PrivateKey:               "private-key-pem",
				IntermediateCertificates: tt.intermediateCerts,
				CertificateType:          "server",
			})

			require.NoError(t, err)
			require.NotNil(t, response)
			assert.Equal(t, "server", response.CertificateType)
			client.AssertExpectations(t)
			securityClient.AssertExpectations(t)
		})
	}
}

func TestInstallServerCA_Success(t *testing.T) {
	provider, client, securityClient := setupCertificateInstallTest(t)
	securityClient.On(
		"ServerRootCACertificateInstall",
		mock.MatchedBy(func(params *ontapRest.ServerRootCAInstallParams) bool {
			return params.SvmName != nil && *params.SvmName == "svm-one" &&
				params.Name != nil && *params.Name == "server-ca-name" &&
				params.Certificate != nil && *params.Certificate == "server-ca-pem" &&
				params.CommonName != nil && *params.CommonName == "server-ca-cn" &&
				params.CertificateType != nil &&
				*params.CertificateType == TrustCertificateTypeServerCA
		}),
	).Return(certificateInstallResult("server-ca-name", TrustCertificateTypeServerCA), nil)

	response, err := provider.InstallServerCA(InstallServerCAParams{
		SvmName:         "svm-one",
		CertificateName: "server-ca-name",
		Certificate:     "server-ca-pem",
		CommonName:      "server-ca-cn",
	})

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, TrustCertificateTypeServerCA, response.CertificateType)
	client.AssertExpectations(t)
	securityClient.AssertExpectations(t)
}

func TestInstallServerCA_Errors(t *testing.T) {
	t.Run("returns install error", func(t *testing.T) {
		provider, client, securityClient := setupCertificateInstallTest(t)
		securityClient.On("ServerRootCACertificateInstall", mock.Anything).
			Return(nil, fmt.Errorf("install failed"))

		response, err := provider.InstallServerCA(InstallServerCAParams{Certificate: "server-ca-pem"})

		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "install failed")
		client.AssertExpectations(t)
		securityClient.AssertExpectations(t)
	})

	t.Run("rejects nil install result", func(t *testing.T) {
		provider, client, securityClient := setupCertificateInstallTest(t)
		securityClient.On("ServerRootCACertificateInstall", mock.Anything).Return(nil, nil)

		response, err := provider.InstallServerCA(InstallServerCAParams{Certificate: "server-ca-pem"})

		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "server-ca installation returned nil result")
		client.AssertExpectations(t)
		securityClient.AssertExpectations(t)
	})
}
