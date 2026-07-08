package activities_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

func Test_getSMCLicenseFromCloud_Success(t *testing.T) {
	ctx := context.Background()
	origGetGCPService := hyperscaler2.ResolveCloudService
	originalGetSecret := activities.GetSecretWithVersion
	mockService := new(google.GcpServices)
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return mockService, nil
	}
	activities.GetSecretWithVersion = func(gcpService hyperscaler2.Services, gcpProjectId, secretID, versionID string) (*models.CustomSecret, error) {
		return &models.CustomSecret{
			SecretVersion: &models.CustomSecretVersion{
				Value: "test-secret-value",
			},
		}, nil
	}
	defer func() {
		hyperscaler2.ResolveCloudService = origGetGCPService
		activities.GetSecretWithVersion = originalGetSecret
	}()

	secret, err := activities.GetSmcLicenseFromCloud(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "test-secret-value", secret)
}

func Test_generateTokenForNode_Success(t *testing.T) {
	tokenValue := "test-token"
	node := &coremodels.Node{Name: "node1"}
	clientSecret := "secret"
	origGetProviderByNode := vsa.GetProviderByNode
	mockProvider := new(vsa.MockProvider)
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodels.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
	mockProvider.On("PostClusterLicenseAccessToken", context.Background(), clientSecret).Return(&tokenValue, nil)

	token, err := activities.GenerateTokenForNode(context.Background(), node, &clientSecret)
	assert.NoError(t, err)
	assert.NotNil(t, token)
	assert.Equal(t, tokenValue, *token)
}

func Test_generateTokenForNode_NilToken(t *testing.T) {
	node := &coremodels.Node{Name: "node1"}
	clientSecret := "secret"
	origGetProviderByNode := vsa.GetProviderByNode
	mockProvider := new(vsa.MockProvider)
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodels.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
	mockProvider.On("PostClusterLicenseAccessToken", context.Background(), clientSecret).Return(nil, nil)

	token, err := activities.GenerateTokenForNode(context.Background(), node, &clientSecret)
	assert.Error(t, err)
	assert.Nil(t, token)
}

func Test_generateTokenForNode_EmptyToken(t *testing.T) {
	tokenValue := ""
	node := &coremodels.Node{Name: "node1"}
	clientSecret := "secret"
	origGetProviderByNode := vsa.GetProviderByNode
	mockProvider := new(vsa.MockProvider)
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodels.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { vsa.GetProviderByNode = origGetProviderByNode }()
	mockProvider.On("PostClusterLicenseAccessToken", context.Background(), clientSecret).Return(&tokenValue, nil)

	token, err := activities.GenerateTokenForNode(context.Background(), node, &clientSecret)
	assert.Error(t, err)
	assert.Nil(t, token)
}

func Test_generateTokenForNode_GetProviderByNodeError(t *testing.T) {
	node := &coremodels.Node{Name: "node1"}
	clientSecret := "secret"
	origGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *coremodels.Node) (vsa.Provider, error) {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("getProviderByNode error"))
	}
	defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

	token, err := activities.GenerateTokenForNode(context.Background(), node, &clientSecret)
	assert.Error(t, err)
	assertTemporalApplicationError(t, err, "getProviderByNode error", "CustomError", false)
	assert.Nil(t, token)
}

// Test case: GetGCPService returns an error
func Test_getSMCLicenseFromCloud_GetGCPServiceError(t *testing.T) {
	ctx := context.Background()
	origGetGCPService := hyperscaler2.ResolveCloudService
	originalGetSecret := activities.GetSecretWithVersion
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("gcp service error"))
	}
	activities.GetSecretWithVersion = originalGetSecret
	defer func() {
		hyperscaler2.ResolveCloudService = origGetGCPService
		activities.GetSecretWithVersion = originalGetSecret
	}()

	secret, err := activities.GetSmcLicenseFromCloud(ctx)
	assert.Error(t, err)
	assert.Empty(t, secret)
}

// Test case: GetSecretWithVersion returns an error
func Test_getSMCLicenseFromCloud_GetSecretWithVersionError(t *testing.T) {
	ctx := context.Background()
	origGetGCPService := hyperscaler2.ResolveCloudService
	originalGetSecret := activities.GetSecretWithVersion
	mockService := new(google.GcpServices)
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return mockService, nil
	}
	activities.GetSecretWithVersion = func(gcpService hyperscaler2.Services, gcpProjectId, secretID, versionID string) (*models.CustomSecret, error) {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("secret fetch error"))
	}
	defer func() {
		hyperscaler2.ResolveCloudService = origGetGCPService
		activities.GetSecretWithVersion = originalGetSecret
	}()

	secret, err := activities.GetSmcLicenseFromCloud(ctx)
	assert.Error(t, err)
	assert.Empty(t, secret)
}
func Test_getSecretWithVersion_Success(t *testing.T) {
	mockService := new(hyperscaler2.MockServices)
	expectedSecret := &models.CustomSecret{
		SecretVersion: &models.CustomSecretVersion{Value: "my-secret"},
	}
	mockService.On("GetSecretWithCustomVersion", "proj", "sid", "vid").Return(expectedSecret, nil)

	secret, err := activities.GetSecretWithVersion(mockService, "proj", "sid", "vid")
	assert.NoError(t, err)
	assert.Equal(t, expectedSecret, secret)
}

func Test_getSecretWithVersion_ErrorReturned(t *testing.T) {
	mockService := new(hyperscaler2.MockServices)
	mockService.On("GetSecretWithCustomVersion", "proj", "sid", "vid").Return(nil, fmt.Errorf("fetch error"))

	secret, err := activities.GetSecretWithVersion(mockService, "proj", "sid", "vid")
	assert.Error(t, err)
	assert.Nil(t, secret)
}

func Test_getSecretWithVersion_NilSecretReturned(t *testing.T) {
	mockService := new(hyperscaler2.MockServices)
	mockService.On("GetSecretWithCustomVersion", "proj", "sid", "vid").Return(nil, nil)

	secret, err := activities.GetSecretWithVersion(mockService, "proj", "sid", "vid")
	assert.Error(t, err)
	assert.Nil(t, secret)
}

func Test_getSecretWithVersion_NilSecretVersionReturned(t *testing.T) {
	mockService := new(hyperscaler2.MockServices)
	expectedSecret := &models.CustomSecret{SecretVersion: nil}
	mockService.On("GetSecretWithCustomVersion", "proj", "sid", "vid").Return(expectedSecret, nil)

	secret, err := activities.GetSecretWithVersion(mockService, "proj", "sid", "vid")
	assert.Error(t, err)
	assert.Nil(t, secret)
}

func TestSmcTokenRotationActivity_GetSMCLicenseFromCloud_Success(t *testing.T) {
	activity := &activities.SmcTokenRotationActivity{}
	origGetSmcLicenseFromCloud := activities.GetSmcLicenseFromCloud
	activities.GetSmcLicenseFromCloud = func(ctx context.Context) (string, error) {
		return "mock-secret", nil
	}
	defer func() { activities.GetSmcLicenseFromCloud = origGetSmcLicenseFromCloud }()

	secret, err := activity.GetSMCLicenseFromCloud(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "mock-secret", secret)
}

func TestSmcTokenRotationActivity_GetSMCLicenseFromCloud_Error(t *testing.T) {
	activity := &activities.SmcTokenRotationActivity{}
	origGetSmcLicenseFromCloud := activities.GetSmcLicenseFromCloud
	activities.GetSmcLicenseFromCloud = func(ctx context.Context) (string, error) {
		return "", fmt.Errorf("cloud error")
	}
	defer func() { activities.GetSmcLicenseFromCloud = origGetSmcLicenseFromCloud }()

	secret, err := activity.GetSMCLicenseFromCloud(context.Background())
	assert.Error(t, err)
	assert.Empty(t, secret)
}
