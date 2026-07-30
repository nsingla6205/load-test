package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

const testHostOnboardActivityHostUUID = "host-uuid-123"

func hostOnboardActivityTestResult() *orchcommon.HostOnboardResult {
	return &orchcommon.HostOnboardResult{
		HostUUID: testHostOnboardActivityHostUUID,
		DbHost: &datamodel.Host{
			BaseModel:    datamodel.BaseModel{UUID: testHostOnboardActivityHostUUID},
			Name:         "host-01",
			ExternalName: "external-svm",
			IPAddress:    "10.0.0.1",
			State:        datamodel.LifeCycleStateCreating,
		},
	}
}

func requireHostOnboardCertificateAuth(t *testing.T) {
	t.Helper()
	setHostOnboardAuthType(t, env.USER_CERTIFICATE)
}

func setHostOnboardAuthType(t *testing.T, authType int) {
	t.Helper()
	original := env.HostOnboardAuthType
	env.HostOnboardAuthType = authType
	t.Cleanup(func() { env.HostOnboardAuthType = original })
}

func patchHostOnboardGetProviderForHost(
	t *testing.T,
	getProvider func(context.Context, *datamodel.Host) (vsa.Provider, error),
) {
	t.Helper()
	original := HostOnboardGetProviderForHost
	HostOnboardGetProviderForHost = getProvider
	t.Cleanup(func() { HostOnboardGetProviderForHost = original })
}

func validHostServerCertificateResult(t *testing.T) *orchcommon.HostOnboardResult {
	t.Helper()

	result := hostOnboardActivityTestResult()
	result.HostCredentials = &datamodel.PoolCredentials{
		CertificateID: "server-certificate",
		Username:      "certificate-user",
	}
	result.CertificateResponse = &hyperscaler_models.CustomCertificateResponse{
		Certificate: &hyperscaler_models.CustomCertificate{
			PemCertificate: generateTestCertPEMForActivities(
				t,
				"server-cn",
				"server-cn",
				nil,
				1,
			),
			PemCertificateChain: []string{"intermediate-ca"},
		},
		Secret: &hyperscaler_models.CustomSecret{
			SecretVersion: &hyperscaler_models.CustomSecretVersion{Value: "private-key"},
		},
	}
	return result
}

func executeHostOnboardResultActivity(
	t *testing.T,
	activityFunction interface{},
	result *orchcommon.HostOnboardResult,
) (*orchcommon.HostOnboardResult, error) {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	testEnvironment := suite.NewTestActivityEnvironment()
	testEnvironment.RegisterActivity(activityFunction)
	encodedResult, err := testEnvironment.ExecuteActivity(activityFunction, result)
	if err != nil {
		return nil, err
	}

	var output orchcommon.HostOnboardResult
	require.NoError(t, encodedResult.Get(&output))
	return &output, nil
}

// executeHostOnboardActivityPreservingState invokes an activity inside a Temporal test
// activity context while keeping the input result in-memory (CloudService is not serialized).
func executeHostOnboardActivityPreservingState(
	t *testing.T,
	run func(context.Context, *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error),
	result *orchcommon.HostOnboardResult,
) (*orchcommon.HostOnboardResult, error) {
	t.Helper()

	var (
		output *orchcommon.HostOnboardResult
		runErr error
	)
	activityName := t.Name() + "-preserve-state"

	var suite testsuite.WorkflowTestSuite
	testEnvironment := suite.NewTestActivityEnvironment()
	testEnvironment.RegisterActivityWithOptions(
		func(ctx context.Context) error {
			output, runErr = run(ctx, result)
			return runErr
		},
		activity.RegisterOptions{Name: activityName},
	)

	_, err := testEnvironment.ExecuteActivity(activityName)
	if err != nil {
		return nil, err
	}
	return output, nil
}

func executeMarkHostOnboardFailedActivity(
	t *testing.T,
	hostActivity *HostOnboardActivity,
	hostUUID string,
	details string,
) error {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	testEnvironment := suite.NewTestActivityEnvironment()
	testEnvironment.RegisterActivity(hostActivity.MarkHostOnboardFailed)
	_, err := testEnvironment.ExecuteActivity(
		hostActivity.MarkHostOnboardFailed,
		hostUUID,
		details,
	)
	return err
}

func TestHostOnboardActivity_DiscoverHost_InvalidResult_ReturnsNonRetryableError(t *testing.T) {
	hostActivity := &HostOnboardActivity{}

	_, err := executeHostOnboardResultActivity(t, hostActivity.DiscoverHost, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host onboard result requires db host")
	assert.Contains(t, err.Error(), "retryable: false")
}

func TestHostOnboardActivity_DiscoverHost_NilDbHost_ReturnsNonRetryableError(t *testing.T) {
	hostActivity := &HostOnboardActivity{}

	_, err := executeHostOnboardResultActivity(t, hostActivity.DiscoverHost, &orchcommon.HostOnboardResult{
		HostUUID: testHostOnboardActivityHostUUID,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host onboard result requires db host")
	assert.Contains(t, err.Error(), "retryable: false")
}

func TestHostOnboardActivity_DiscoverHost_InvalidEncryptedPassword_ReturnsNonRetryableError(t *testing.T) {
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()
	input.DbHost.Password = "not-encrypted"

	_, err := executeHostOnboardResultActivity(t, hostActivity.DiscoverHost, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decrypt host admin password")
	assert.Contains(t, err.Error(), "retryable: false")
}

func TestHostOnboardActivity_DiscoverHost_MissingManagementIP_ReturnsNonRetryableError(t *testing.T) {
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()
	input.DbHost.IPAddress = ""

	_, err := executeHostOnboardResultActivity(t, hostActivity.DiscoverHost, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no management IP")
	assert.Contains(t, err.Error(), "retryable: false")
}

func TestHostOnboardActivity_GenerateHostCertificate_NonCertificateAuth_Skips(t *testing.T) {
	setHostOnboardAuthType(t, env.USERNAME_PWD)
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()

	output, err := executeHostOnboardResultActivity(t, hostActivity.GenerateHostCertificate, input)

	require.NoError(t, err)
	assert.Equal(t, input.HostUUID, output.HostUUID)
	assert.Nil(t, output.HostCredentials)
	assert.Nil(t, output.CertificateResponse)
}

func TestHostOnboardActivity_CreateHostCloudDNSRecord_NonCertificateAuth_Skips(t *testing.T) {
	setHostOnboardAuthType(t, env.USERNAME_PWD)
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()

	output, err := executeHostOnboardResultActivity(t, hostActivity.CreateHostCloudDNSRecord, input)

	require.NoError(t, err)
	assert.Nil(t, output.CloudDNSRecord)
}

func TestHostOnboardActivity_InstallHostCACertificate_NonCertificateAuth_Skips(t *testing.T) {
	setHostOnboardAuthType(t, env.USERNAME_PWD)
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()

	output, err := executeHostOnboardResultActivity(t, hostActivity.InstallHostCACertificate, input)

	require.NoError(t, err)
	assert.Equal(t, input.HostUUID, output.HostUUID)
}

func TestHostOnboardActivity_InstallHostServerCertificate_NonCertificateAuth_Skips(t *testing.T) {
	setHostOnboardAuthType(t, env.USERNAME_PWD)
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()

	output, err := executeHostOnboardResultActivity(t, hostActivity.InstallHostServerCertificate, input)

	require.NoError(t, err)
	assert.Equal(t, input.HostUUID, output.HostUUID)
}

func TestHostOnboardActivity_InstallHostCACertificate_InvalidChain_ReturnsNonRetryableError(t *testing.T) {
	requireHostOnboardCertificateAuth(t)

	tests := []struct {
		name  string
		chain []string
	}{
		{name: "nil chain", chain: nil},
		{name: "single entry", chain: []string{"only-one-entry"}},
		{name: "blank subordinate", chain: []string{"", "root-ca"}},
		{name: "blank root", chain: []string{"subordinate-ca", " "}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hostActivity := &HostOnboardActivity{}
			input := hostOnboardActivityTestResult()
			input.CertificateResponse = &hyperscaler_models.CustomCertificateResponse{
				Certificate: &hyperscaler_models.CustomCertificate{
					PemCertificateChain: test.chain,
				},
			}

			_, err := executeHostOnboardResultActivity(t, hostActivity.InstallHostCACertificate, input)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "certificate chain entries at index 0 and 1 are required")
			assert.Contains(t, err.Error(), "retryable: false")
		})
	}
}

func TestHostOnboardActivity_InstallHostCACertificate_ProviderError_ReturnsNonRetryableError(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return nil, errors.New("provider init failed")
	})
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()
	input.CertificateResponse = &hyperscaler_models.CustomCertificateResponse{
		Certificate: &hyperscaler_models.CustomCertificate{
			PemCertificateChain: []string{"subordinate-ca", "root-ca"},
		},
	}

	_, err := executeHostOnboardResultActivity(t, hostActivity.InstallHostCACertificate, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider init failed")
}

func TestHostOnboardActivity_InstallHostCACertificate_InstallsRootFirst(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	provider := vsa.NewMockProvider(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return provider, nil
	})
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()
	input.CertificateResponse = &hyperscaler_models.CustomCertificateResponse{
		Certificate: &hyperscaler_models.CustomCertificate{
			PemCertificateChain: []string{"subordinate-ca", "root-ca"},
		},
	}

	var installOrder []string
	provider.On("InstallClientCA", mock.Anything).Run(func(arguments mock.Arguments) {
		installOrder = append(installOrder, arguments.Get(0).(vsa.InstallClientCAParams).Certificate)
	}).Return(&vsa.ServerCertificateResponse{}, nil).Twice()
	provider.On("InstallServerCA", mock.Anything).Run(func(arguments mock.Arguments) {
		installOrder = append(installOrder, arguments.Get(0).(vsa.InstallServerCAParams).Certificate)
	}).Return(&vsa.ServerCertificateResponse{}, nil).Twice()

	output, err := executeHostOnboardResultActivity(t, hostActivity.InstallHostCACertificate, input)

	require.NoError(t, err)
	require.NotNil(t, output.DbHost)
	assert.Equal(t, []string{
		"root-ca",
		"subordinate-ca",
		"root-ca",
		"subordinate-ca",
	}, installOrder)
	provider.AssertExpectations(t)
}

func TestHostOnboardActivity_InstallHostCACertificate_InstallErrors(t *testing.T) {
	requireHostOnboardCertificateAuth(t)

	tests := []struct {
		name                string
		clientInstallError  error
		serverInstallError  error
		expectedError       string
		expectedClientCalls int
	}{
		{
			name:                "client CA install failure",
			clientInstallError:  errors.New("client CA failed"),
			expectedError:       "client CA failed",
			expectedClientCalls: 1,
		},
		{
			name:                "server CA install failure",
			serverInstallError:  errors.New("server CA failed"),
			expectedError:       "server CA failed",
			expectedClientCalls: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := vsa.NewMockProvider(t)
			patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
				return provider, nil
			})
			input := hostOnboardActivityTestResult()
			input.CertificateResponse = &hyperscaler_models.CustomCertificateResponse{
				Certificate: &hyperscaler_models.CustomCertificate{
					PemCertificateChain: []string{"subordinate-ca", "root-ca"},
				},
			}

			provider.On("InstallClientCA", mock.Anything).
				Return(&vsa.ServerCertificateResponse{}, test.clientInstallError).
				Times(test.expectedClientCalls)
			if test.clientInstallError == nil {
				provider.On("InstallServerCA", mock.Anything).
					Return(&vsa.ServerCertificateResponse{}, test.serverInstallError).Once()
			}

			_, err := executeHostOnboardResultActivity(
				t,
				(&HostOnboardActivity{}).InstallHostCACertificate,
				input,
			)

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.expectedError)
			if test.clientInstallError != nil {
				provider.AssertNotCalled(t, "InstallServerCA", mock.Anything)
			}
			provider.AssertExpectations(t)
		})
	}
}

func TestHostOnboardActivity_InstallHostCACertificate_DuplicateEntriesSucceed(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	provider := vsa.NewMockProvider(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return provider, nil
	})
	input := hostOnboardActivityTestResult()
	input.CertificateResponse = &hyperscaler_models.CustomCertificateResponse{
		Certificate: &hyperscaler_models.CustomCertificate{
			PemCertificateChain: []string{"subordinate-ca", "root-ca"},
		},
	}
	provider.On("InstallClientCA", mock.Anything).
		Return(nil, errors.New("duplicate entry")).Twice()
	provider.On("InstallServerCA", mock.Anything).
		Return(nil, errors.New("already exists")).Twice()

	_, err := executeHostOnboardResultActivity(
		t,
		(&HostOnboardActivity{}).InstallHostCACertificate,
		input,
	)

	require.NoError(t, err)
	provider.AssertExpectations(t)
}

func TestHostOnboardActivity_InstallHostServerCertificate_MissingLeafPEM_ReturnsNonRetryableError(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	provider := vsa.NewMockProvider(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return provider, nil
	})
	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()
	input.CertificateResponse = &hyperscaler_models.CustomCertificateResponse{
		Certificate: &hyperscaler_models.CustomCertificate{
			PemCertificateChain: []string{"intermediate-ca"},
		},
		Secret: &hyperscaler_models.CustomSecret{
			SecretVersion: &hyperscaler_models.CustomSecretVersion{Value: "private-key"},
		},
	}

	_, err := executeHostOnboardResultActivity(t, hostActivity.InstallHostServerCertificate, input)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "leaf certificate PEM is required")
	assert.Contains(t, err.Error(), "retryable: false")
	provider.AssertNotCalled(t, "InstallServerCertificate", mock.Anything)
}

func TestHostOnboardActivity_InstallHostServerCertificate_ValidationErrors(t *testing.T) {
	requireHostOnboardCertificateAuth(t)

	tests := []struct {
		name          string
		mutate        func(*orchcommon.HostOnboardResult)
		expectedError string
	}{
		{
			name: "missing private key",
			mutate: func(result *orchcommon.HostOnboardResult) {
				result.CertificateResponse.Secret = nil
			},
			expectedError: "private key is required",
		},
		{
			name: "missing certificate chain",
			mutate: func(result *orchcommon.HostOnboardResult) {
				result.CertificateResponse.Certificate.PemCertificateChain = nil
			},
			expectedError: "certificate chain is required",
		},
		{
			name: "missing common name",
			mutate: func(result *orchcommon.HostOnboardResult) {
				result.CertificateResponse.Certificate.PemCertificate = "invalid-pem"
				result.HostCredentials.Username = ""
				result.CertificateResponse.Certificate.SubjectCommonName = ""
			},
			expectedError: "server certificate common name is required",
		},
		{
			name: "missing certificate name",
			mutate: func(result *orchcommon.HostOnboardResult) {
				result.HostCredentials.CertificateID = ""
				result.CertificateResponse.Certificate.CertificateID = ""
			},
			expectedError: "certificate name is required",
		},
		{
			name: "invalid certificate serial",
			mutate: func(result *orchcommon.HostOnboardResult) {
				result.CertificateResponse.Certificate.PemCertificate = "invalid-pem"
				result.HostCredentials.Username = "fallback-cn"
			},
			expectedError: "unusable serial number",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := vsa.NewMockProvider(t)
			patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
				return provider, nil
			})
			input := validHostServerCertificateResult(t)
			test.mutate(input)

			_, err := executeHostOnboardResultActivity(
				t,
				(&HostOnboardActivity{}).InstallHostServerCertificate,
				input,
			)

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.expectedError)
			assert.Contains(t, err.Error(), "retryable: false")
			provider.AssertNotCalled(t, "InstallServerCertificate", mock.Anything)
		})
	}
}

func TestHostOnboardActivity_InstallHostServerCertificate_ProviderError(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return nil, errors.New("provider failed")
	})

	_, err := executeHostOnboardResultActivity(
		t,
		(&HostOnboardActivity{}).InstallHostServerCertificate,
		validHostServerCertificateResult(t),
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider failed")
}

func TestHostOnboardActivity_InstallHostServerCertificate_Success(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	provider := vsa.NewMockProvider(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return provider, nil
	})
	input := validHostServerCertificateResult(t)
	provider.On("InstallServerCertificate", mock.MatchedBy(func(params vsa.InstallServerCertificateParams) bool {
		return params.SvmName == "external-svm" &&
			params.CertificateName == "server-certificate" &&
			params.PrivateKey == "private-key" &&
			params.CertificateType == hostServerCertificateType &&
			params.CommonName == "certificate-user" &&
			assert.ObjectsAreEqual([]string{"intermediate-ca"}, params.IntermediateCertificates)
	})).Return(&vsa.ServerCertificateResponse{}, nil).Once()
	provider.On("ModifySSL", vsa.ModifySSLParams{
		SvmName:       "external-svm",
		ServerEnabled: true,
		CA:            "server-cn",
		Serial:        "01",
	}).Return(&vsa.ModifySSLResponse{Success: true}, nil).Once()

	output, err := executeHostOnboardResultActivity(
		t,
		(&HostOnboardActivity{}).InstallHostServerCertificate,
		input,
	)

	require.NoError(t, err)
	require.NotNil(t, output)
	provider.AssertExpectations(t)
}

func TestHostOnboardActivity_InstallHostServerCertificate_UsesCertificateFallbackFields(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	provider := vsa.NewMockProvider(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return provider, nil
	})
	input := validHostServerCertificateResult(t)
	input.HostCredentials.CertificateID = ""
	input.CertificateResponse.Certificate.PemCertificate = generateTestCertPEMForActivities(
		t,
		"server-cn",
		"",
		nil,
		2,
	)
	input.CertificateResponse.Certificate.CertificateID = "certificate-model-id"
	input.CertificateResponse.Certificate.CaName = "fallback-ca"
	provider.On("InstallServerCertificate", mock.MatchedBy(func(params vsa.InstallServerCertificateParams) bool {
		return params.CertificateName == "certificate-model-id" && params.CommonName == "certificate-user"
	})).Return(&vsa.ServerCertificateResponse{}, nil).Once()
	provider.On("ModifySSL", vsa.ModifySSLParams{
		SvmName:       "external-svm",
		ServerEnabled: true,
		CA:            "fallback-ca",
		Serial:        "02",
	}).Return(&vsa.ModifySSLResponse{Success: true}, nil).Once()

	_, err := executeHostOnboardResultActivity(
		t,
		(&HostOnboardActivity{}).InstallHostServerCertificate,
		input,
	)

	require.NoError(t, err)
	provider.AssertExpectations(t)
}

func TestHostOnboardActivity_InstallHostServerCertificate_DuplicateInstallContinuesToSSL(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	provider := vsa.NewMockProvider(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return provider, nil
	})
	provider.On("InstallServerCertificate", mock.Anything).
		Return(nil, errors.New("duplicate entry")).Once()
	provider.On("ModifySSL", mock.Anything).
		Return(&vsa.ModifySSLResponse{Success: true}, nil).Once()

	_, err := executeHostOnboardResultActivity(
		t,
		(&HostOnboardActivity{}).InstallHostServerCertificate,
		validHostServerCertificateResult(t),
	)

	require.NoError(t, err)
	provider.AssertExpectations(t)
}

func TestHostOnboardActivity_InstallHostServerCertificate_ErrorPaths(t *testing.T) {
	requireHostOnboardCertificateAuth(t)

	tests := []struct {
		name          string
		installError  error
		modifyResult  *vsa.ModifySSLResponse
		modifyError   error
		expectedError string
	}{
		{
			name:          "install failure",
			installError:  errors.New("install failed"),
			expectedError: "install failed",
		},
		{
			name:          "SSL request failure",
			modifyError:   errors.New("SSL request failed"),
			expectedError: "SSL request failed",
		},
		{
			name:          "SSL response reports failure",
			modifyResult:  &vsa.ModifySSLResponse{Success: false, Message: "SSL rejected"},
			expectedError: "SSL configuration failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := vsa.NewMockProvider(t)
			patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
				return provider, nil
			})
			provider.On("InstallServerCertificate", mock.Anything).
				Return(&vsa.ServerCertificateResponse{}, test.installError).Once()
			if test.installError == nil {
				provider.On("ModifySSL", mock.Anything).
					Return(test.modifyResult, test.modifyError).Once()
			}

			_, err := executeHostOnboardResultActivity(
				t,
				(&HostOnboardActivity{}).InstallHostServerCertificate,
				validHostServerCertificateResult(t),
			)

			require.Error(t, err)
			assert.Contains(t, err.Error(), test.expectedError)
			provider.AssertExpectations(t)
		})
	}
}

func TestHostOnboardActivity_InstallHostServerCertificate_MissingCANameForSSLModify(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	provider := vsa.NewMockProvider(t)
	patchHostOnboardGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return provider, nil
	})
	input := validHostServerCertificateResult(t)
	input.CertificateResponse.Certificate.PemCertificate = generateTestCertPEMForActivities(
		t,
		"server-cn",
		"",
		nil,
		3,
	)
	input.CertificateResponse.Certificate.CaName = ""
	provider.On("InstallServerCertificate", mock.Anything).
		Return(&vsa.ServerCertificateResponse{}, nil).Once()

	_, err := executeHostOnboardResultActivity(
		t,
		(&HostOnboardActivity{}).InstallHostServerCertificate,
		input,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate CA name is required for SSL modify")
	assert.Contains(t, err.Error(), "retryable: false")
	provider.AssertNotCalled(t, "ModifySSL", mock.Anything)
}

func TestHostOnboardActivity_GenerateHostCertificate_UsesInjectedCloudService(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGenerate := vsa.GenerateAndCreateCertificateForVSACluster
	t.Cleanup(func() { vsa.GenerateAndCreateCertificateForVSACluster = originalGenerate })

	cloudService := hyperscaler.NewMockServices(t)
	certificateResponse := &hyperscaler_models.CustomCertificateResponse{
		Certificate: &hyperscaler_models.CustomCertificate{CertificateID: "external-svm-cert"},
	}
	vsa.GenerateAndCreateCertificateForVSACluster = func(
		gotCloudService hyperscaler.Services,
		clusterName string,
		username string,
		credentials *datamodel.PoolCredentials,
		serverAuthEnabled bool,
	) (*hyperscaler_models.CustomCertificateResponse, error) {
		assert.Same(t, cloudService, gotCloudService)
		assert.Equal(t, "external-svm", clusterName)
		assert.NotEmpty(t, username)
		assert.Equal(t, "external-svm-cert", credentials.CertificateID)
		assert.True(t, serverAuthEnabled)
		return certificateResponse, nil
	}

	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()
	input.CloudService = cloudService

	output, err := executeHostOnboardActivityPreservingState(t, hostActivity.GenerateHostCertificate, input)

	require.NoError(t, err)
	require.NotNil(t, output.HostCredentials)
	assert.Equal(t, "external-svm-cert", output.HostCredentials.CertificateID)
	assert.Equal(t, "external-svm-cert", output.CertificateResponse.Certificate.CertificateID)
}

func TestHostOnboardActivity_GenerateHostCertificate_PreservesExistingCredentials(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGenerate := vsa.GenerateAndCreateCertificateForVSACluster
	t.Cleanup(func() { vsa.GenerateAndCreateCertificateForVSACluster = originalGenerate })

	cloudService := hyperscaler.NewMockServices(t)
	existingCredentials := &datamodel.PoolCredentials{
		CertificateID: "existing-cert",
		SecretID:      "existing-secret",
		Username:      "existing-user",
	}
	vsa.GenerateAndCreateCertificateForVSACluster = func(
		_ hyperscaler.Services,
		_ string,
		username string,
		credentials *datamodel.PoolCredentials,
		_ bool,
	) (*hyperscaler_models.CustomCertificateResponse, error) {
		assert.Equal(t, "existing-user", username)
		assert.Same(t, existingCredentials, credentials)
		return &hyperscaler_models.CustomCertificateResponse{
			Certificate: &hyperscaler_models.CustomCertificate{CertificateID: "existing-cert"},
		}, nil
	}
	input := hostOnboardActivityTestResult()
	input.CloudService = cloudService
	input.HostCredentials = existingCredentials

	output, err := executeHostOnboardActivityPreservingState(
		t,
		(&HostOnboardActivity{}).GenerateHostCertificate,
		input,
	)

	require.NoError(t, err)
	assert.Same(t, existingCredentials, output.HostCredentials)
}

func TestHostOnboardActivity_GenerateHostCertificate_GeneratorError(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGenerate := vsa.GenerateAndCreateCertificateForVSACluster
	t.Cleanup(func() { vsa.GenerateAndCreateCertificateForVSACluster = originalGenerate })

	vsa.GenerateAndCreateCertificateForVSACluster = func(
		hyperscaler.Services,
		string,
		string,
		*datamodel.PoolCredentials,
		bool,
	) (*hyperscaler_models.CustomCertificateResponse, error) {
		return nil, errors.New("certificate generation failed")
	}
	input := hostOnboardActivityTestResult()
	input.CloudService = hyperscaler.NewMockServices(t)

	_, err := executeHostOnboardActivityPreservingState(
		t,
		(&HostOnboardActivity{}).GenerateHostCertificate,
		input,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate generation failed")
}

func TestHostOnboardActivity_GenerateHostCertificate_CloudResolverError(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGenerate := vsa.GenerateAndCreateCertificateForVSACluster
	originalResolve := hyperscaler.ResolveCloudService
	t.Cleanup(func() {
		vsa.GenerateAndCreateCertificateForVSACluster = originalGenerate
		hyperscaler.ResolveCloudService = originalResolve
	})

	generatorCalled := false
	vsa.GenerateAndCreateCertificateForVSACluster = func(
		hyperscaler.Services,
		string,
		string,
		*datamodel.PoolCredentials,
		bool,
	) (*hyperscaler_models.CustomCertificateResponse, error) {
		generatorCalled = true
		return nil, errors.New("generator should not run")
	}
	hyperscaler.ResolveCloudService = func(context.Context) (hyperscaler.Services, error) {
		return nil, errors.New("cloud resolver failed")
	}

	input := hostOnboardActivityTestResult()
	input.CloudService = nil

	_, err := executeHostOnboardActivityPreservingState(
		t,
		(&HostOnboardActivity{}).GenerateHostCertificate,
		input,
	)

	require.Error(t, err)
	assert.False(t, generatorCalled)
	assert.Contains(t, err.Error(), "internal error")
}

func TestHostOnboardActivity_GenerateHostCertificate_ResolvesCloudService(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGenerate := vsa.GenerateAndCreateCertificateForVSACluster
	originalResolve := hyperscaler.ResolveCloudService
	t.Cleanup(func() {
		vsa.GenerateAndCreateCertificateForVSACluster = originalGenerate
		hyperscaler.ResolveCloudService = originalResolve
	})

	cloudService := hyperscaler.NewMockServices(t)
	hyperscaler.ResolveCloudService = func(context.Context) (hyperscaler.Services, error) {
		return cloudService, nil
	}
	vsa.GenerateAndCreateCertificateForVSACluster = func(
		gotCloudService hyperscaler.Services,
		_ string,
		_ string,
		_ *datamodel.PoolCredentials,
		_ bool,
	) (*hyperscaler_models.CustomCertificateResponse, error) {
		assert.Same(t, cloudService, gotCloudService)
		return &hyperscaler_models.CustomCertificateResponse{
			Certificate: &hyperscaler_models.CustomCertificate{CertificateID: "resolved-cert"},
		}, nil
	}

	input := hostOnboardActivityTestResult()
	input.CloudService = nil

	output, err := executeHostOnboardActivityPreservingState(
		t,
		(&HostOnboardActivity{}).GenerateHostCertificate,
		input,
	)

	require.NoError(t, err)
	assert.Same(t, cloudService, output.CloudService)
}

func TestHostOnboardActivity_CreateHostCloudDNSRecord_UsesInjectedCloudService(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGetOrCreate := vsa.GetOrCreateCloudDNSRecord
	t.Cleanup(func() { vsa.GetOrCreateCloudDNSRecord = originalGetOrCreate })

	cloudService := hyperscaler.NewMockServices(t)
	expectedRecord := &hyperscaler_models.CustomCloudDNSRecord{
		RecordName: "dns-1.external-svm.example.",
		Data:       "10.0.0.1",
	}
	vsa.GetOrCreateCloudDNSRecord = func(
		gotCloudService hyperscaler.Services,
		recordName string,
		ipAddress string,
	) (*hyperscaler_models.CustomCloudDNSRecord, error) {
		assert.Same(t, cloudService, gotCloudService)
		assert.Contains(t, recordName, "dns-1.external-svm.")
		assert.Equal(t, "10.0.0.1", ipAddress)
		return expectedRecord, nil
	}

	hostActivity := &HostOnboardActivity{}
	input := hostOnboardActivityTestResult()
	input.CloudService = cloudService

	output, err := executeHostOnboardActivityPreservingState(t, hostActivity.CreateHostCloudDNSRecord, input)

	require.NoError(t, err)
	require.NotNil(t, output.CloudDNSRecord)
	assert.Equal(t, expectedRecord.RecordName, output.CloudDNSRecord.RecordName)
}

func TestHostOnboardActivity_CreateHostCloudDNSRecord_DNSError(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGetOrCreate := vsa.GetOrCreateCloudDNSRecord
	t.Cleanup(func() { vsa.GetOrCreateCloudDNSRecord = originalGetOrCreate })

	vsa.GetOrCreateCloudDNSRecord = func(
		hyperscaler.Services,
		string,
		string,
	) (*hyperscaler_models.CustomCloudDNSRecord, error) {
		return nil, errors.New("DNS create failed")
	}
	input := hostOnboardActivityTestResult()
	input.CloudService = hyperscaler.NewMockServices(t)

	_, err := executeHostOnboardActivityPreservingState(
		t,
		(&HostOnboardActivity{}).CreateHostCloudDNSRecord,
		input,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal error")
}

func TestHostOnboardActivity_CreateHostCloudDNSRecord_CloudResolverError(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGetOrCreate := vsa.GetOrCreateCloudDNSRecord
	originalResolve := hyperscaler.ResolveCloudService
	t.Cleanup(func() {
		vsa.GetOrCreateCloudDNSRecord = originalGetOrCreate
		hyperscaler.ResolveCloudService = originalResolve
	})

	dnsCalled := false
	vsa.GetOrCreateCloudDNSRecord = func(
		hyperscaler.Services,
		string,
		string,
	) (*hyperscaler_models.CustomCloudDNSRecord, error) {
		dnsCalled = true
		return nil, errors.New("dns should not run")
	}
	hyperscaler.ResolveCloudService = func(context.Context) (hyperscaler.Services, error) {
		return nil, errors.New("cloud resolver failed")
	}

	input := hostOnboardActivityTestResult()
	input.CloudService = nil

	_, err := executeHostOnboardActivityPreservingState(
		t,
		(&HostOnboardActivity{}).CreateHostCloudDNSRecord,
		input,
	)

	require.Error(t, err)
	assert.False(t, dnsCalled)
	assert.Contains(t, err.Error(), "internal error")
}

func TestHostOnboardActivity_CreateHostCloudDNSRecord_TrimsExternalName(t *testing.T) {
	requireHostOnboardCertificateAuth(t)
	originalGetOrCreate := vsa.GetOrCreateCloudDNSRecord
	t.Cleanup(func() { vsa.GetOrCreateCloudDNSRecord = originalGetOrCreate })

	cloudService := hyperscaler.NewMockServices(t)
	vsa.GetOrCreateCloudDNSRecord = func(
		gotCloudService hyperscaler.Services,
		recordName string,
		ipAddress string,
	) (*hyperscaler_models.CustomCloudDNSRecord, error) {
		assert.Same(t, cloudService, gotCloudService)
		assert.Contains(t, recordName, "dns-1.trimmed-svm.")
		assert.NotContains(t, recordName, " trimmed-svm")
		assert.Equal(t, "10.0.0.1", ipAddress)
		return &hyperscaler_models.CustomCloudDNSRecord{RecordName: recordName}, nil
	}

	input := hostOnboardActivityTestResult()
	input.DbHost.ExternalName = "  trimmed-svm  "
	input.CloudService = cloudService

	output, err := executeHostOnboardActivityPreservingState(
		t,
		(&HostOnboardActivity{}).CreateHostCloudDNSRecord,
		input,
	)

	require.NoError(t, err)
	require.NotNil(t, output.CloudDNSRecord)
}

func TestResolveHostOnboardCloudService_ReturnsCachedService(t *testing.T) {
	cloudService := hyperscaler.NewMockServices(t)
	result := hostOnboardActivityTestResult()
	result.CloudService = cloudService

	actual, err := resolveHostOnboardCloudService(context.Background(), result)

	require.NoError(t, err)
	assert.Same(t, cloudService, actual)
}

func TestResolveHostOnboardCloudService_ResolvesAndCachesService(t *testing.T) {
	originalResolve := hyperscaler.ResolveCloudService
	t.Cleanup(func() { hyperscaler.ResolveCloudService = originalResolve })

	cloudService := hyperscaler.NewMockServices(t)
	hyperscaler.ResolveCloudService = func(context.Context) (hyperscaler.Services, error) {
		return cloudService, nil
	}

	result := hostOnboardActivityTestResult()

	actual, err := resolveHostOnboardCloudService(context.Background(), result)

	require.NoError(t, err)
	require.NotNil(t, actual)
	assert.Same(t, cloudService, actual)
	assert.Same(t, cloudService, result.CloudService)
}

func TestResolveHostOnboardCloudService_NilResultStillResolvesService(t *testing.T) {
	originalResolve := hyperscaler.ResolveCloudService
	t.Cleanup(func() { hyperscaler.ResolveCloudService = originalResolve })

	cloudService := hyperscaler.NewMockServices(t)
	hyperscaler.ResolveCloudService = func(context.Context) (hyperscaler.Services, error) {
		return cloudService, nil
	}

	actual, err := resolveHostOnboardCloudService(context.Background(), nil)

	require.NoError(t, err)
	assert.Same(t, cloudService, actual)
}

func TestResolveHostOnboardCloudService_ResolverError(t *testing.T) {
	originalResolve := hyperscaler.ResolveCloudService
	t.Cleanup(func() { hyperscaler.ResolveCloudService = originalResolve })

	hyperscaler.ResolveCloudService = func(context.Context) (hyperscaler.Services, error) {
		return nil, errors.New("cloud resolver failed")
	}

	actual, err := resolveHostOnboardCloudService(context.Background(), hostOnboardActivityTestResult())

	require.Error(t, err)
	assert.Nil(t, actual)
	assert.Contains(t, err.Error(), "cloud resolver failed")
}

func TestHostOnboardActivity_OnboardHost_InvalidResult_ReturnsNonRetryableError(t *testing.T) {
	hostActivity := &HostOnboardActivity{}

	_, err := executeHostOnboardResultActivity(t, hostActivity.OnboardHost, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host onboard result requires db host")
	assert.Contains(t, err.Error(), "retryable: false")
}

func TestHostOnboardActivity_OnboardHost_UpdatesLifecycleState(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	hostActivity := &HostOnboardActivity{SE: mockStorage}
	input := hostOnboardActivityTestResult()
	mockStorage.On("UpdateHost", mock.Anything, mock.MatchedBy(func(host *datamodel.Host) bool {
		return host.State == datamodel.LifeCycleStateCreated &&
			host.StateDetails == datamodel.LifeCycleStateCreatedDetails
	})).Return(func(_ context.Context, host *datamodel.Host) *datamodel.Host {
		return host
	}, nil).Once()

	output, err := executeHostOnboardResultActivity(t, hostActivity.OnboardHost, input)

	require.NoError(t, err)
	require.NotNil(t, output.DbHost)
	assert.Equal(t, datamodel.LifeCycleStateCreated, output.DbHost.State)
	assert.Equal(t, datamodel.LifeCycleStateCreatedDetails, output.DbHost.StateDetails)
	mockStorage.AssertExpectations(t)
}

func TestHostOnboardActivity_OnboardHost_StorageError_ReturnsError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	hostActivity := &HostOnboardActivity{SE: mockStorage}
	mockStorage.On("UpdateHost", mock.Anything, mock.Anything).
		Return(nil, errors.New("update failed")).Once()

	_, err := executeHostOnboardResultActivity(
		t,
		hostActivity.OnboardHost,
		hostOnboardActivityTestResult(),
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
	mockStorage.AssertExpectations(t)
}

func TestHostOnboardActivity_MarkHostOnboardFailed_UpdatesLifecycleState(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	hostActivity := &HostOnboardActivity{SE: mockStorage}
	host := hostOnboardActivityTestResult().DbHost
	mockStorage.On("GetHost", mock.Anything, testHostOnboardActivityHostUUID).
		Return(host, nil).Once()
	mockStorage.On("UpdateHost", mock.Anything, mock.MatchedBy(func(updatedHost *datamodel.Host) bool {
		return updatedHost.State == datamodel.LifeCycleStateError &&
			updatedHost.StateDetails == "certificate installation failed"
	})).Return(host, nil).Once()

	err := executeMarkHostOnboardFailedActivity(
		t,
		hostActivity,
		testHostOnboardActivityHostUUID,
		"certificate installation failed",
	)

	require.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestHostOnboardActivity_MarkHostOnboardFailed_EmptyDetailsUsesDefault(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	hostActivity := &HostOnboardActivity{SE: mockStorage}
	host := hostOnboardActivityTestResult().DbHost
	mockStorage.On("GetHost", mock.Anything, testHostOnboardActivityHostUUID).
		Return(host, nil).Once()
	mockStorage.On("UpdateHost", mock.Anything, mock.MatchedBy(func(updatedHost *datamodel.Host) bool {
		return updatedHost.StateDetails == datamodel.LifeCycleStateCreationErrorDetails
	})).Return(host, nil).Once()

	err := executeMarkHostOnboardFailedActivity(
		t,
		hostActivity,
		testHostOnboardActivityHostUUID,
		"",
	)

	require.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestHostOnboardActivity_MarkHostOnboardFailed_CreatedHostIsUnchanged(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	hostActivity := &HostOnboardActivity{SE: mockStorage}
	host := hostOnboardActivityTestResult().DbHost
	host.State = datamodel.LifeCycleStateCreated
	mockStorage.On("GetHost", mock.Anything, testHostOnboardActivityHostUUID).
		Return(host, nil).Once()

	err := executeMarkHostOnboardFailedActivity(
		t,
		hostActivity,
		testHostOnboardActivityHostUUID,
		"late failure",
	)

	require.NoError(t, err)
	mockStorage.AssertNotCalled(t, "UpdateHost", mock.Anything, mock.Anything)
	mockStorage.AssertExpectations(t)
}

func TestHostOnboardActivity_MarkHostOnboardFailed_GetHostError_ReturnsError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	hostActivity := &HostOnboardActivity{SE: mockStorage}
	mockStorage.On("GetHost", mock.Anything, testHostOnboardActivityHostUUID).
		Return(nil, errors.New("host lookup failed")).Once()

	err := executeMarkHostOnboardFailedActivity(
		t,
		hostActivity,
		testHostOnboardActivityHostUUID,
		"failure",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host lookup failed")
	mockStorage.AssertNotCalled(t, "UpdateHost", mock.Anything, mock.Anything)
	mockStorage.AssertExpectations(t)
}

func TestHostOnboardActivity_MarkHostOnboardFailed_UpdateError_ReturnsError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	hostActivity := &HostOnboardActivity{SE: mockStorage}
	host := hostOnboardActivityTestResult().DbHost
	mockStorage.On("GetHost", mock.Anything, testHostOnboardActivityHostUUID).
		Return(host, nil).Once()
	mockStorage.On("UpdateHost", mock.Anything, mock.Anything).
		Return(nil, errors.New("host update failed")).Once()

	err := executeMarkHostOnboardFailedActivity(
		t,
		hostActivity,
		testHostOnboardActivityHostUUID,
		"failure",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "host update failed")
	mockStorage.AssertExpectations(t)
}
