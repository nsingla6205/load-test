package vsa

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// Test InstallServerCertificate
func TestInstallServerCertificate_Success(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := InstallServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		Certificate:     "test-cert-pem",
		PrivateKey:      "test-private-key",
		CertificateType: "server",
		CommonName:      "test-cn",
	}

	result := &ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			UUID:         nillable.GetStringPtr("test-uuid"),
			Name:         nillable.GetStringPtr("test-cert"),
			CommonName:   nillable.GetStringPtr("test-cn"),
			Type:         nillable.GetStringPtr("server"),
			ExpiryTime:   nillable.GetStringPtr(time.Now().Add(24 * time.Hour).Format(time.RFC3339)),
			SerialNumber: nillable.GetStringPtr("test-serial"),
		},
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateInstall", mock.MatchedBy(func(p *ontapRest.ServerRootCAInstallParams) bool {
		return *p.SvmName == "test-svm" && *p.Name == "test-cert"
	})).Return(result, nil)

	resp, err := ontapProvider.InstallServerCertificate(params)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "test-uuid", resp.UUID)
	assert.Equal(t, "test-cert", resp.Name)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

// Test InstallClientCA — installs the client-ca trust cert (cluster/admin scope by default).
func TestInstallClientCA_Success_ClusterScope(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	// No SvmName -> cluster (admin) scope; deterministic CertificateName.
	params := InstallClientCAParams{
		CertificateName: "dep-client-ca",
		Certificate:     "ca-pem",
		CommonName:      "cn",
	}

	result := &ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			UUID:         nillable.GetStringPtr("uuid"),
			Name:         nillable.GetStringPtr("dep-client-ca"),
			Type:         nillable.GetStringPtr("client_ca"),
			SerialNumber: nillable.GetStringPtr("serial"),
		},
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateInstall", mock.MatchedBy(func(p *ontapRest.ServerRootCAInstallParams) bool {
		return p.SvmName == nil && p.CertificateType != nil && *p.CertificateType == "client_ca" &&
			p.Name != nil && *p.Name == "dep-client-ca" && p.CommonName != nil && *p.CommonName == "cn"
	})).Return(result, nil)

	resp, err := ontapProvider.InstallClientCA(params)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "dep-client-ca", resp.Name)
	assert.Equal(t, "client_ca", resp.CertificateType)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

func TestInstallClientCA_Success_SvmScoped(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	params := InstallClientCAParams{
		SvmName:     "svm1",
		Certificate: "ca-pem",
	}

	result := &ontapRest.ServerRootCACertificate{
		SecurityCertificate: models.SecurityCertificate{
			UUID: nillable.GetStringPtr("uuid"),
			Type: nillable.GetStringPtr("client_ca"),
		},
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateInstall", mock.MatchedBy(func(p *ontapRest.ServerRootCAInstallParams) bool {
		return p.SvmName != nil && *p.SvmName == "svm1"
	})).Return(result, nil)

	resp, err := ontapProvider.InstallClientCA(params)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

func TestInstallClientCA_GetOntapClientError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, fmt.Errorf("getOntapClient error")
	}

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	resp, err := ontapProvider.InstallClientCA(InstallClientCAParams{Certificate: "ca-pem"})
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, "getOntapClient error", err.Error())
}

func TestInstallClientCA_NilClient(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, nil
	}

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	resp, err := ontapProvider.InstallClientCA(InstallClientCAParams{Certificate: "ca-pem"})
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "ONTAP client is nil")
}

func TestInstallClientCA_NilSecurityClient(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}
	mockClient.On("Security").Return(nil)

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	resp, err := ontapProvider.InstallClientCA(InstallClientCAParams{Certificate: "ca-pem"})
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "Security client is nil")
	mockClient.AssertExpectations(t)
}

func TestInstallClientCA_InstallError(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateInstall", mock.Anything).Return(nil, fmt.Errorf("install failed"))

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	resp, err := ontapProvider.InstallClientCA(InstallClientCAParams{Certificate: "ca-pem", CertificateName: "n"})
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "install failed")
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

func TestInstallClientCA_NilResult(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateInstall", mock.Anything).Return(nil, nil)

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	resp, err := ontapProvider.InstallClientCA(InstallClientCAParams{Certificate: "ca-pem"})
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "nil result")
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

func TestInstallServerCertificate_GetOntapClientError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, fmt.Errorf("getOntapClient error")
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := InstallServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		Certificate:     "test-cert-pem",
		PrivateKey:      "test-private-key",
		CertificateType: "server",
		CommonName:      "test-cn",
	}

	resp, err := ontapProvider.InstallServerCertificate(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, "getOntapClient error", err.Error())
}

func TestInstallServerCertificate_NilClient(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, nil
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := InstallServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		Certificate:     "test-cert-pem",
		PrivateKey:      "test-private-key",
		CertificateType: "server",
		CommonName:      "test-cn",
	}

	resp, err := ontapProvider.InstallServerCertificate(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "ONTAP client is nil")
}

func TestInstallServerCertificate_NilSecurityClient(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	mockClient.On("Security").Return(nil)

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := InstallServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		Certificate:     "test-cert-pem",
		PrivateKey:      "test-private-key",
		CertificateType: "server",
		CommonName:      "test-cn",
	}

	resp, err := ontapProvider.InstallServerCertificate(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "Security client is nil")
	mockClient.AssertExpectations(t)
}

func TestInstallServerCertificate_InstallError(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := InstallServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		Certificate:     "test-cert-pem",
		PrivateKey:      "test-private-key",
		CertificateType: "server",
		CommonName:      "test-cn",
	}

	expectedError := fmt.Errorf("installation failed")
	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateInstall", mock.Anything).Return(nil, expectedError)

	resp, err := ontapProvider.InstallServerCertificate(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, expectedError, err)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

func TestInstallServerCertificate_NilResult(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := InstallServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		Certificate:     "test-cert-pem",
		PrivateKey:      "test-private-key",
		CertificateType: "server",
		CommonName:      "test-cn",
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateInstall", mock.Anything).Return(nil, nil)

	resp, err := ontapProvider.InstallServerCertificate(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "certificate installation returned nil result")
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

// Test GetServerCertificates
func TestGetServerCertificates_Success(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := GetServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		CertificateType: "server",
	}

	results := []*ontapRest.ServerRootCACertificate{
		{
			SecurityCertificate: models.SecurityCertificate{
				UUID:         nillable.GetStringPtr("test-uuid-1"),
				Name:         nillable.GetStringPtr("test-cert-1"),
				CommonName:   nillable.GetStringPtr("test-cn-1"),
				Type:         nillable.GetStringPtr("server"),
				ExpiryTime:   nillable.GetStringPtr(time.Now().Add(24 * time.Hour).Format(time.RFC3339)),
				SerialNumber: nillable.GetStringPtr("test-serial-1"),
			},
		},
		{
			SecurityCertificate: models.SecurityCertificate{
				UUID:         nillable.GetStringPtr("test-uuid-2"),
				Name:         nillable.GetStringPtr("test-cert-2"),
				CommonName:   nillable.GetStringPtr("test-cn-2"),
				Type:         nillable.GetStringPtr("server"),
				ExpiryTime:   nillable.GetStringPtr(time.Now().Add(48 * time.Hour).Format(time.RFC3339)),
				SerialNumber: nillable.GetStringPtr("test-serial-2"),
			},
		},
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateCollectionGet", mock.MatchedBy(func(p *ontapRest.ServerRootCAGetCollectionParams) bool {
		return *p.SvmName == "test-svm" && *p.Name == "test-cert"
	})).Return(results, nil)

	resp, err := ontapProvider.GetServerCertificates(params)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp, 2)
	assert.Equal(t, "test-uuid-1", resp[0].UUID)
	assert.Equal(t, "test-uuid-2", resp[1].UUID)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

func TestGetServerCertificates_GetOntapClientError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, fmt.Errorf("getOntapClient error")
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := GetServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		CertificateType: "server",
	}

	resp, err := ontapProvider.GetServerCertificates(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, "getOntapClient error", err.Error())
}

func TestGetServerCertificates_NilClient(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, nil
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := GetServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		CertificateType: "server",
	}

	resp, err := ontapProvider.GetServerCertificates(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "ONTAP client is nil")
}

func TestGetServerCertificates_NilSecurityClient(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	mockClient.On("Security").Return(nil)

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := GetServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		CertificateType: "server",
	}

	resp, err := ontapProvider.GetServerCertificates(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "Security client is nil")
	mockClient.AssertExpectations(t)
}

func TestGetServerCertificates_GetError(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{
		Logger: &mockLogger{},
	}
	params := GetServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		CertificateType: "server",
	}

	expectedError := fmt.Errorf("get failed")
	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("ServerRootCACertificateCollectionGet", mock.Anything).Return(nil, expectedError)

	resp, err := ontapProvider.GetServerCertificates(params)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, expectedError, err)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

// Test DeleteServerCertificate
func TestDeleteServerCertificate_Success(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	params := DeleteServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		CertificateType: "server",
		SerialNumber:    "test-serial",
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("SecurityCertificateDeleteCollection", mock.MatchedBy(func(p *ontapRest.SecurityCertificateDeleteCollectionParams) bool {
		return *p.Name == "test-cert" && *p.SvmName == "test-svm" && *p.Type == "server" &&
			p.SerialNumber != nil && *p.SerialNumber == "test-serial"
	})).Return(nil)

	err := ontapProvider.DeleteServerCertificate(params)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

func TestDeleteServerCertificate_OmitsSerialWhenEmpty(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	params := DeleteServerCertificateParams{
		SvmName:         "test-svm",
		CertificateName: "test-cert",
		CertificateType: "server",
	}

	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("SecurityCertificateDeleteCollection", mock.MatchedBy(func(p *ontapRest.SecurityCertificateDeleteCollectionParams) bool {
		return *p.Name == "test-cert" && p.SerialNumber == nil
	})).Return(nil)

	err := ontapProvider.DeleteServerCertificate(params)
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

func TestDeleteServerCertificate_GetOntapClientError(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, fmt.Errorf("getOntapClient error")
	}

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	err := ontapProvider.DeleteServerCertificate(DeleteServerCertificateParams{SvmName: "s", CertificateName: "c", CertificateType: "server"})
	assert.Error(t, err)
	assert.Equal(t, "getOntapClient error", err.Error())
}

func TestDeleteServerCertificate_NilClient(t *testing.T) {
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return nil, nil
	}

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	err := ontapProvider.DeleteServerCertificate(DeleteServerCertificateParams{SvmName: "s", CertificateName: "c", CertificateType: "server"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ONTAP client is nil")
}

func TestDeleteServerCertificate_NilSecurityClient(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	mockClient.On("Security").Return(nil)

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	err := ontapProvider.DeleteServerCertificate(DeleteServerCertificateParams{SvmName: "s", CertificateName: "c", CertificateType: "server"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Security client is nil")
	mockClient.AssertExpectations(t)
}

func TestDeleteServerCertificate_DeleteError(t *testing.T) {
	mockClient := new(ontapRest.MockRESTClient)
	mockSecurityClient := new(ontapRest.MockSecurityClient)
	originalgetOntapClientFunc := getOntapClientFunc
	defer func() { getOntapClientFunc = originalgetOntapClientFunc }()

	getOntapClientFunc = func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
		return mockClient, nil
	}

	expectedError := fmt.Errorf("delete failed")
	mockClient.On("Security").Return(mockSecurityClient)
	mockSecurityClient.On("SecurityCertificateDeleteCollection", mock.Anything).Return(expectedError)

	ontapProvider := &OntapRestProvider{Logger: &mockLogger{}}
	err := ontapProvider.DeleteServerCertificate(DeleteServerCertificateParams{SvmName: "s", CertificateName: "c", CertificateType: "server"})
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockClient.AssertExpectations(t)
	mockSecurityClient.AssertExpectations(t)
}

// Test ModifySSL
// Note: These tests are skipped because NewSSHClient cannot be mocked directly.
// To properly test ModifySSL, we would need to make NewSSHClient mockable by introducing
// a variable similar to getOntapClientFunc, or these would need to be integration tests.
func TestModifySSL_Success(t *testing.T) {
	t.Skip("Skipping ModifySSL tests - NewSSHClient is not mockable")
}

func TestModifySSL_Success_WithoutCAAndSerial(t *testing.T) {
	t.Skip("Skipping ModifySSL tests - NewSSHClient is not mockable")
}

func TestModifySSL_SSHClientError(t *testing.T) {
	t.Skip("Skipping ModifySSL tests - NewSSHClient is not mockable")
}

func TestModifySSL_CommandExecutionError(t *testing.T) {
	t.Skip("Skipping ModifySSL tests - NewSSHClient is not mockable")
}

// mockLogger is a simple mock logger for testing
type mockLogger struct {
	mock.Mock
}

func (m *mockLogger) Debug(format string, args ...any)                          {}
func (m *mockLogger) Debugf(format string, args ...any)                         {}
func (m *mockLogger) Info(format string, args ...any)                           {}
func (m *mockLogger) Infof(format string, args ...any)                          {}
func (m *mockLogger) Warn(format string, args ...any)                           {}
func (m *mockLogger) Warnf(format string, args ...any)                          {}
func (m *mockLogger) Error(format string, args ...any)                          {}
func (m *mockLogger) Errorf(format string, args ...any)                         {}
func (m *mockLogger) InfoContext(ctx context.Context, msg string, args ...any)  {}
func (m *mockLogger) WarnContext(ctx context.Context, msg string, args ...any)  {}
func (m *mockLogger) ErrorContext(ctx context.Context, msg string, args ...any) {}
func (m *mockLogger) DebugContext(ctx context.Context, msg string, args ...any) {}
func (m *mockLogger) WithFields(fieldName string, fields log.Fields) log.Logger { return m }
func (m *mockLogger) With(fields log.Fields) log.Logger                         { return m }
