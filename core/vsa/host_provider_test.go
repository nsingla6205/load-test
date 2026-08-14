package vsa

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	dbdatamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const testHostServiceUsername = "gcnv_u_abcdefghij"

func hostTestFixture(t *testing.T) *dbdatamodel.Host {
	t.Helper()
	encryptedPassword, err := utils.EncryptPassword("admin-pass")
	require.NoError(t, err)

	return &dbdatamodel.Host{
		BaseModel: dbdatamodel.BaseModel{UUID: "715038a2-eb3d-0c5a-e3f0-92b923ba3b4d"},
		Name:      "ext-ontap-35-184-12-76",
		Username:  "admin",
		Password:  *encryptedPassword,
		Protocol:  dbdatamodel.ExternalClusterProtocolInsecureHTTPS,
		IPAddress: "35.184.12.76",
	}
}

func TestGetProviderForHost_UsesManagementIPAsRESTHost(t *testing.T) {
	host := hostTestFixture(t)

	provider, err := GetProviderForHost(context.Background(), host)
	require.NoError(t, err)

	ontapProvider, ok := provider.(*OntapRestProvider)
	require.True(t, ok)
	assert.Equal(t, "35.184.12.76", ontapProvider.Provider.IPAddress)
	assert.Equal(t, "35.184.12.76", ontapProvider.ClientParams.Host)
	assert.Equal(t, map[string]string{"35.184.12.76": "35.184.12.76"}, ontapProvider.ClientParams.Hosts)
	assert.Equal(t, "admin", ontapProvider.ClientParams.Username)
}

func TestGetProviderForHostServiceUser_UsesServiceCredentials(t *testing.T) {
	host := hostTestFixture(t)

	provider, err := GetProviderForHostServiceUser(
		context.Background(),
		host,
		testHostServiceUsername,
		"service-pass",
	)
	require.NoError(t, err)

	ontapProvider, ok := provider.(*OntapRestProvider)
	require.True(t, ok)
	assert.Equal(t, testHostServiceUsername, ontapProvider.ClientParams.Username)
	assert.Equal(t, "35.184.12.76", ontapProvider.Provider.IPAddress)
}

func TestGetProviderForHost_NilHost(t *testing.T) {
	_, err := GetProviderForHost(context.Background(), nil)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "host is required")
}

func TestGetProviderForHost_NoManagementIP(t *testing.T) {
	host := hostTestFixture(t)
	host.IPAddress = ""

	_, err := GetProviderForHost(context.Background(), host)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "no management IP")
}

func TestVerifyHostServiceCredentials_EmptySvmPattern(t *testing.T) {
	host := hostTestFixture(t)

	err := VerifyHostServiceCredentials(
		context.Background(),
		host,
		"",
		testHostServiceUsername,
		"service-pass",
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "svm pattern is required")
}

func TestGetProviderForHostServiceUser_RequiresCredentials(t *testing.T) {
	host := hostTestFixture(t)

	_, err := GetProviderForHostServiceUser(context.Background(), host, "", "password")
	require.Error(t, err)

	_, err = GetProviderForHostServiceUser(context.Background(), host, "svc-user", "")
	require.Error(t, err)
}

type verifySVMListProvider struct {
	*MockProvider
	svms []*ontapRest.Svm
	err  error
}

func (p *verifySVMListProvider) ListSVMs(params ListSvmsParams) ([]*ontapRest.Svm, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.svms, nil
}

func TestVerifyHostServiceCredentials_EmptySvmListSucceeds(t *testing.T) {
	host := hostTestFixture(t)
	const svmPattern = "svm_abc12345-6789-4abc-8def-0123456789ab_*"
	origGetServiceUserProvider := getProviderForHostServiceUser
	getProviderForHostServiceUser = func(
		_ context.Context,
		_ *dbdatamodel.Host,
		username, password string,
	) (Provider, error) {
		assert.Equal(t, testHostServiceUsername, username)
		assert.Equal(t, "service-pass", password)
		return &verifySVMListProvider{
			MockProvider: NewMockProvider(t),
			svms:         nil,
		}, nil
	}
	t.Cleanup(func() { getProviderForHostServiceUser = origGetServiceUserProvider })

	err := VerifyHostServiceCredentials(
		context.Background(),
		host,
		svmPattern,
		testHostServiceUsername,
		"service-pass",
	)
	require.NoError(t, err)
}

func TestVerifyHostServiceCredentials_Success(t *testing.T) {
	host := hostTestFixture(t)
	const svmPattern = "svm_abc12345-6789-4abc-8def-0123456789ab_*"
	origGetServiceUserProvider := getProviderForHostServiceUser
	getProviderForHostServiceUser = func(
		_ context.Context,
		_ *dbdatamodel.Host,
		username, password string,
	) (Provider, error) {
		assert.Equal(t, testHostServiceUsername, username)
		assert.Equal(t, "service-pass", password)
		return &verifySVMListProvider{
			MockProvider: NewMockProvider(t),
			svms:         []*ontapRest.Svm{},
		}, nil
	}
	t.Cleanup(func() { getProviderForHostServiceUser = origGetServiceUserProvider })

	err := VerifyHostServiceCredentials(
		context.Background(),
		host,
		svmPattern,
		testHostServiceUsername,
		"service-pass",
	)
	require.NoError(t, err)
}

func TestVerifyHostServiceCredentials_ListSVMsError(t *testing.T) {
	host := hostTestFixture(t)
	const svmPattern = "svm_abc12345-6789-4abc-8def-0123456789ab_*"
	origGetServiceUserProvider := getProviderForHostServiceUser
	getProviderForHostServiceUser = func(
		_ context.Context,
		_ *dbdatamodel.Host,
		_, _ string,
	) (Provider, error) {
		return &verifySVMListProvider{
			MockProvider: NewMockProvider(t),
			err:          errors.New("svm list api failed"),
		}, nil
	}
	t.Cleanup(func() { getProviderForHostServiceUser = origGetServiceUserProvider })

	err := VerifyHostServiceCredentials(
		context.Background(),
		host,
		svmPattern,
		testHostServiceUsername,
		"service-pass",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid input parameters provided")
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "failed to verify host service credentials")
}

func TestGetProviderForHostServiceUserWithCertificate_UsesStrictHostDNS(t *testing.T) {
	host := hostTestFixture(t)
	host.ExternalName = "external-cluster"

	originalDNSName := env.VsaDeployedDnsName
	originalGetCertificate := GetCertificateFromCacheOrSecretManager
	t.Cleanup(func() {
		env.VsaDeployedDnsName = originalDNSName
		GetCertificateFromCacheOrSecretManager = originalGetCertificate
	})
	env.VsaDeployedDnsName = "example.com"
	GetCertificateFromCacheOrSecretManager = func(
		_ context.Context,
		_ *dbdatamodel.PoolCredentials,
	) (*coremodels.Certificate, error) {
		return &coremodels.Certificate{
			SignedCertificate:        "signed-certificate",
			PrivateKey:               "private-key",
			InterMediateCertificates: []string{"ca-certificate"},
		}, nil
	}

	provider, err := GetProviderForHostServiceUserWithCertificate(
		context.Background(),
		host,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.NoError(t, err)

	ontapProvider, ok := provider.(*OntapRestProvider)
	require.True(t, ok)
	assert.Equal(t, "35.184.12.76", ontapProvider.Provider.IPAddress)
	assert.Equal(t,
		map[string]string{"35.184.12.76": "dns-1.external-cluster.example.com"},
		ontapProvider.ClientParams.Hosts)
	assert.Equal(t, dbdatamodel.ExternalClusterProtocolHTTPS, ontapProvider.Provider.Protocol)
	assert.True(t, ontapProvider.Provider.UseHTTPS)
	assert.False(t, ontapProvider.ClientParams.InsecureSkipVerify)
	assert.True(t, ontapProvider.ClientParams.CertificateBasedAuthEnabled)
}

func TestGetProviderForHostServiceUserWithCertificate_RejectsHTTP(t *testing.T) {
	host := hostTestFixture(t)
	host.ExternalName = "external-cluster"
	host.Protocol = dbdatamodel.ExternalClusterProtocolHTTP

	_, err := GetProviderForHostServiceUserWithCertificate(
		context.Background(),
		host,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "must use HTTPS for certificate authentication")
}

func TestGetProviderForHostServiceUserWithCertificate_RequiresDNSConfiguration(t *testing.T) {
	host := hostTestFixture(t)
	host.ExternalName = "external-cluster"

	originalDNSName := env.VsaDeployedDnsName
	t.Cleanup(func() { env.VsaDeployedDnsName = originalDNSName })
	env.VsaDeployedDnsName = ""

	_, err := GetProviderForHostServiceUserWithCertificate(
		context.Background(),
		host,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "VSA deployed DNS name is required")
}

func TestGetProviderForHostServiceUserWithCertificate_PassthroughConnectionError(t *testing.T) {
	host := hostTestFixture(t)
	host.IPAddress = ""
	host.ExternalName = "external-cluster"

	originalDNSName := env.VsaDeployedDnsName
	t.Cleanup(func() { env.VsaDeployedDnsName = originalDNSName })
	env.VsaDeployedDnsName = "example.com"

	_, err := GetProviderForHostServiceUserWithCertificate(
		context.Background(),
		host,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "no management IP")
}

func TestGetProviderForHostServiceUserWithCertificate_RequiresExternalClusterName(t *testing.T) {
	host := hostTestFixture(t)
	host.ExternalName = ""

	originalDNSName := env.VsaDeployedDnsName
	t.Cleanup(func() { env.VsaDeployedDnsName = originalDNSName })
	env.VsaDeployedDnsName = "example.com"

	_, err := GetProviderForHostServiceUserWithCertificate(
		context.Background(),
		host,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "external cluster name is required")
}

func TestGetProviderForHostServiceUserWithCertificate_CertificateLoadError(t *testing.T) {
	host := hostTestFixture(t)
	host.ExternalName = "external-cluster"

	originalDNSName := env.VsaDeployedDnsName
	originalGetCertificate := GetCertificateFromCacheOrSecretManager
	t.Cleanup(func() {
		env.VsaDeployedDnsName = originalDNSName
		GetCertificateFromCacheOrSecretManager = originalGetCertificate
	})
	env.VsaDeployedDnsName = "example.com"
	GetCertificateFromCacheOrSecretManager = func(context.Context, *dbdatamodel.PoolCredentials) (*coremodels.Certificate, error) {
		return nil, errors.New("secret manager unavailable")
	}

	_, err := GetProviderForHostServiceUserWithCertificate(
		context.Background(),
		host,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Equal(t, vsaerrors.ErrGCPResourceFetchError, customErr.TrackingID)
	assert.Contains(t, customErr.Unwrap().Error(), "failed to load service user certificate")
}

func TestGetProviderForHostServiceUserWithCertificate_CertificateNotFound(t *testing.T) {
	host := hostTestFixture(t)
	host.ExternalName = "external-cluster"

	originalDNSName := env.VsaDeployedDnsName
	originalGetCertificate := GetCertificateFromCacheOrSecretManager
	t.Cleanup(func() {
		env.VsaDeployedDnsName = originalDNSName
		GetCertificateFromCacheOrSecretManager = originalGetCertificate
	})
	env.VsaDeployedDnsName = "example.com"
	GetCertificateFromCacheOrSecretManager = func(context.Context, *dbdatamodel.PoolCredentials) (*coremodels.Certificate, error) {
		return nil, nil
	}

	_, err := GetProviderForHostServiceUserWithCertificate(
		context.Background(),
		host,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "missing-cert-id"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Equal(t, vsaerrors.ErrResourceNotFound, customErr.TrackingID)
	assert.Contains(t, customErr.Unwrap().Error(), "missing-cert-id")
}

func TestVerifyHostServiceCredentials_ProviderNotSVMLister(t *testing.T) {
	host := hostTestFixture(t)
	const svmPattern = "svm_abc12345-6789-4abc-8def-0123456789ab_*"

	origGetServiceUserProvider := getProviderForHostServiceUser
	getProviderForHostServiceUser = func(context.Context, *dbdatamodel.Host, string, string) (Provider, error) {
		return nil, nil
	}
	t.Cleanup(func() { getProviderForHostServiceUser = origGetServiceUserProvider })

	err := VerifyHostServiceCredentials(
		context.Background(),
		host,
		svmPattern,
		testHostServiceUsername,
		"service-pass",
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "does not support SVM list")
}

func TestVerifyHostServiceCredentialsWithCertificate_EmptySvmPattern(t *testing.T) {
	host := hostTestFixture(t)

	err := VerifyHostServiceCredentialsWithCertificate(
		context.Background(),
		host,
		"",
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "svm pattern is required")
}

func TestVerifyHostServiceCredentialsWithCertificate_ProviderError(t *testing.T) {
	host := hostTestFixture(t)
	const svmPattern = "svm_abc12345-6789-4abc-8def-0123456789ab_*"

	orig := getProviderForHostServiceUserWithCertificate
	getProviderForHostServiceUserWithCertificate = func(
		context.Context,
		*dbdatamodel.Host,
		string,
		*dbdatamodel.PoolCredentials,
	) (Provider, error) {
		return nil, errors.New("provider build failed")
	}
	t.Cleanup(func() { getProviderForHostServiceUserWithCertificate = orig })

	err := VerifyHostServiceCredentialsWithCertificate(
		context.Background(),
		host,
		svmPattern,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider build failed")
}

func TestVerifyHostServiceCredentialsWithCertificate_ProviderNotSVMLister(t *testing.T) {
	host := hostTestFixture(t)
	const svmPattern = "svm_abc12345-6789-4abc-8def-0123456789ab_*"

	orig := getProviderForHostServiceUserWithCertificate
	getProviderForHostServiceUserWithCertificate = func(
		context.Context,
		*dbdatamodel.Host,
		string,
		*dbdatamodel.PoolCredentials,
	) (Provider, error) {
		return nil, nil
	}
	t.Cleanup(func() { getProviderForHostServiceUserWithCertificate = orig })

	err := VerifyHostServiceCredentialsWithCertificate(
		context.Background(),
		host,
		svmPattern,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "does not support SVM list")
}

func TestVerifyHostServiceCredentialsWithCertificate_ListSVMsError(t *testing.T) {
	host := hostTestFixture(t)
	const svmPattern = "svm_abc12345-6789-4abc-8def-0123456789ab_*"

	orig := getProviderForHostServiceUserWithCertificate
	getProviderForHostServiceUserWithCertificate = func(
		context.Context,
		*dbdatamodel.Host,
		string,
		*dbdatamodel.PoolCredentials,
	) (Provider, error) {
		return &verifySVMListProvider{
			MockProvider: NewMockProvider(t),
			err:          errors.New("svm list api failed"),
		}, nil
	}
	t.Cleanup(func() { getProviderForHostServiceUserWithCertificate = orig })

	err := VerifyHostServiceCredentialsWithCertificate(
		context.Background(),
		host,
		svmPattern,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "failed to verify host service credentials with certificate")
}

func TestVerifyHostServiceCredentialsWithCertificate_Success(t *testing.T) {
	host := hostTestFixture(t)
	const svmPattern = "svm_abc12345-6789-4abc-8def-0123456789ab_*"

	orig := getProviderForHostServiceUserWithCertificate
	getProviderForHostServiceUserWithCertificate = func(
		_ context.Context,
		_ *dbdatamodel.Host,
		username string,
		_ *dbdatamodel.PoolCredentials,
	) (Provider, error) {
		assert.Equal(t, testHostServiceUsername, username)
		return &verifySVMListProvider{
			MockProvider: NewMockProvider(t),
			svms:         []*ontapRest.Svm{{}},
		}, nil
	}
	t.Cleanup(func() { getProviderForHostServiceUserWithCertificate = orig })

	err := VerifyHostServiceCredentialsWithCertificate(
		context.Background(),
		host,
		svmPattern,
		testHostServiceUsername,
		&dbdatamodel.PoolCredentials{CertificateID: "account-host-cert"},
	)
	require.NoError(t, err)
}
