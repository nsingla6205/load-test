package vsa

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	dbdatamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
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

func TestCreateNodeForHost_UsesDecryptedAdminCredentials(t *testing.T) {
	host := hostTestFixture(t)

	node, err := CreateNodeForHost(host)
	require.NoError(t, err)
	assert.Equal(t, "35.184.12.76", node.EndpointAddress)
	assert.Equal(t, map[string]string{"35.184.12.76": "35.184.12.76"}, node.EndpointAddressesToHostNameMap)
	assert.Equal(t, "admin", node.Username)
	assert.Equal(t, "admin-pass", node.Password)
	assert.Equal(t, 0, node.AuthType)
}

func TestCreateNodeForHost_DefaultsUsernameToAdmin(t *testing.T) {
	host := hostTestFixture(t)
	host.Username = ""

	node, err := CreateNodeForHost(host)
	require.NoError(t, err)
	assert.Equal(t, "admin", node.Username)
}

func TestCreateNodeForHost_NilHost(t *testing.T) {
	_, err := CreateNodeForHost(nil)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.Contains(t, customErr.Unwrap().Error(), "host is required")
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
