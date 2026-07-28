package expertmodeactivities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func withBackupConfigCloudService(t *testing.T, fn func(context.Context) (hyperscaler.Services, error)) {
	t.Helper()
	orig := getCloudServiceForExpertModeBackupConfig
	getCloudServiceForExpertModeBackupConfig = fn
	t.Cleanup(func() { getCloudServiceForExpertModeBackupConfig = orig })
}

func TestCreateDedicatedCapacityBackupServiceAccount_Success(t *testing.T) {
	withBackupConfigCloudService(t, func(ctx context.Context) (hyperscaler.Services, error) {
		mockService := hyperscaler.NewMockServices(t)
		mockService.On(
			"CreateServiceAccount",
			mock.MatchedBy(func(req *hyperscalermodels.CreateServiceAccountRequest) bool {
				return req != nil &&
					req.AccountId == "bv-sa-id" &&
					req.ServiceAccount != nil &&
					req.ServiceAccount.DisplayName == "VSA hardware backup service account"
			}),
			"project-123",
			"sa@project.iam.gserviceaccount.com",
		).Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		return mockService, nil
	})

	activity := &ExpertModeBackupConfigActivity{}
	sa, err := activity.CreateDedicatedCapacityBackupServiceAccount(context.Background(),
		&commonparams.ResourceNames{
			Email:            "sa@project.iam.gserviceaccount.com",
			ServiceAccountId: "bv-sa-id",
		},
		&commonparams.TenancyInfo{RegionalTenantProject: "project-123"},
	)

	require.NoError(t, err)
	require.NotNil(t, sa)
	assert.Equal(t, "sa@project.iam.gserviceaccount.com", sa.Email)
}

func TestCreateDedicatedCapacityBackupServiceAccount_GetCloudServiceFails(t *testing.T) {
	withBackupConfigCloudService(t, func(ctx context.Context) (hyperscaler.Services, error) {
		return nil, errors.New("cloud service unavailable")
	})

	activity := &ExpertModeBackupConfigActivity{}
	_, err := activity.CreateDedicatedCapacityBackupServiceAccount(context.Background(),
		&commonparams.ResourceNames{Email: "sa@project.iam.gserviceaccount.com", ServiceAccountId: "bv-sa-id"},
		&commonparams.TenancyInfo{RegionalTenantProject: "project-123"},
	)
	assert.Error(t, err)
}

func TestCreateDedicatedCapacityBackupServiceAccount_CreateServiceAccountFails(t *testing.T) {
	withBackupConfigCloudService(t, func(ctx context.Context) (hyperscaler.Services, error) {
		mockService := hyperscaler.NewMockServices(t)
		mockService.On("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("create service account failed"))
		return mockService, nil
	})

	activity := &ExpertModeBackupConfigActivity{}
	_, err := activity.CreateDedicatedCapacityBackupServiceAccount(context.Background(),
		&commonparams.ResourceNames{Email: "sa@project.iam.gserviceaccount.com", ServiceAccountId: "bv-sa-id"},
		&commonparams.TenancyInfo{RegionalTenantProject: "project-123"},
	)
	assert.Error(t, err)
}

func TestGetDedicatedCapacityBackupHmacSecret_NotFound(t *testing.T) {
	withBackupConfigCloudService(t, func(ctx context.Context) (hyperscaler.Services, error) {
		mockService := hyperscaler.NewMockServices(t)
		mockService.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, "hardware-bv-hmac-bv-sa-id").
			Return(nil, nil)
		return mockService, nil
	})

	activity := &ExpertModeBackupConfigActivity{}
	result, err := activity.GetDedicatedCapacityBackupHmacSecret(context.Background(), &commonparams.ResourceNames{
		Email:            "sa@project.iam.gserviceaccount.com",
		ServiceAccountId: "bv-sa-id",
	})

	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetDedicatedCapacityBackupHmacSecret_Existing(t *testing.T) {
	withBackupConfigCloudService(t, func(ctx context.Context) (hyperscaler.Services, error) {
		mockService := hyperscaler.NewMockServices(t)
		mockService.On("GetSecretWithLatestVersion", env.SecretManagerProjectID, "hardware-bv-hmac-bv-sa-id").
			Return(&hyperscalermodels.CustomSecret{
				SecretVersion: &hyperscalermodels.CustomSecretVersion{
					Value: `{"access_key_id":"access-key","secret":"secret-key"}`,
				},
			}, nil)
		return mockService, nil
	})

	activity := &ExpertModeBackupConfigActivity{}
	result, err := activity.GetDedicatedCapacityBackupHmacSecret(context.Background(), &commonparams.ResourceNames{
		Email:            "sa@project.iam.gserviceaccount.com",
		ServiceAccountId: "bv-sa-id",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "sa@project.iam.gserviceaccount.com", result.ServiceAccountName)
	assert.Equal(t, "hardware-bv-hmac-bv-sa-id", result.HmacSecretID)
}

func TestStoreDedicatedCapacityBackupHmacSecret_RequiresHMACKeys(t *testing.T) {
	activity := &ExpertModeBackupConfigActivity{}
	_, err := activity.StoreDedicatedCapacityBackupHmacSecret(context.Background(), &commonparams.ResourceNames{
		ServiceAccountId: "bv-sa-id",
	}, nil)
	assert.Error(t, err)
}

func TestStoreDedicatedCapacityBackupHmacSecret_InvalidAccessKeyBase64(t *testing.T) {
	activity := &ExpertModeBackupConfigActivity{}
	_, err := activity.StoreDedicatedCapacityBackupHmacSecret(context.Background(), &commonparams.ResourceNames{
		ServiceAccountId: "bv-sa-id",
	}, &commonparams.HmacKeys{AccessKey: "not-base64", SecretKey: base64.StdEncoding.EncodeToString([]byte("secret"))})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode access key")
}

func TestStoreDedicatedCapacityBackupHmacSecret_InvalidSecretKeyBase64(t *testing.T) {
	activity := &ExpertModeBackupConfigActivity{}
	_, err := activity.StoreDedicatedCapacityBackupHmacSecret(context.Background(), &commonparams.ResourceNames{
		ServiceAccountId: "bv-sa-id",
	}, &commonparams.HmacKeys{
		AccessKey: base64.StdEncoding.EncodeToString([]byte("access")),
		SecretKey: "not-base64",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode secret key")
}

func TestStoreDedicatedCapacityBackupHmacSecret_GetCloudServiceFails(t *testing.T) {
	withBackupConfigCloudService(t, func(ctx context.Context) (hyperscaler.Services, error) {
		return nil, errors.New("cloud service unavailable")
	})

	activity := &ExpertModeBackupConfigActivity{}
	_, err := activity.StoreDedicatedCapacityBackupHmacSecret(context.Background(),
		&commonparams.ResourceNames{
			Email:            "sa@project.iam.gserviceaccount.com",
			ServiceAccountId: "bv-sa-id",
		},
		validEncodedHmacKeys(),
	)
	assert.Error(t, err)
}

func TestStoreDedicatedCapacityBackupHmacSecret_CreateSecretFails(t *testing.T) {
	withBackupConfigCloudService(t, func(ctx context.Context) (hyperscaler.Services, error) {
		mockService := hyperscaler.NewMockServices(t)
		mockService.On("CreateSecret", env.SecretManagerProjectID, env.Region, "hardware-bv-hmac-bv-sa-id", mock.Anything).
			Return(nil, errors.New("create secret failed"))
		return mockService, nil
	})

	activity := &ExpertModeBackupConfigActivity{}
	_, err := activity.StoreDedicatedCapacityBackupHmacSecret(context.Background(),
		&commonparams.ResourceNames{
			Email:            "sa@project.iam.gserviceaccount.com",
			ServiceAccountId: "bv-sa-id",
		},
		validEncodedHmacKeys(),
	)
	assert.Error(t, err)
}

func TestStoreDedicatedCapacityBackupHmacSecret_Success(t *testing.T) {
	withBackupConfigCloudService(t, func(ctx context.Context) (hyperscaler.Services, error) {
		mockService := hyperscaler.NewMockServices(t)
		mockService.On(
			"CreateSecret",
			env.SecretManagerProjectID,
			env.Region,
			"hardware-bv-hmac-bv-sa-id",
			mock.MatchedBy(func(payload string) bool {
				var stored dedicatedBackupHmacSecretPayload
				if err := json.Unmarshal([]byte(payload), &stored); err != nil {
					return false
				}
				return stored.AccessKeyID == "access-key" && stored.Secret == "secret-key"
			}),
		).Return(&hyperscalermodels.CustomSecret{}, nil)
		return mockService, nil
	})

	activity := &ExpertModeBackupConfigActivity{}
	result, err := activity.StoreDedicatedCapacityBackupHmacSecret(context.Background(),
		&commonparams.ResourceNames{
			Email:            "sa@project.iam.gserviceaccount.com",
			ServiceAccountId: "bv-sa-id",
		},
		validEncodedHmacKeys(),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "sa@project.iam.gserviceaccount.com", result.ServiceAccountName)
	assert.Equal(t, "hardware-bv-hmac-bv-sa-id", result.HmacSecretID)
}

func validEncodedHmacKeys() *commonparams.HmacKeys {
	return &commonparams.HmacKeys{
		AccessKey: base64.StdEncoding.EncodeToString([]byte("access-key")),
		SecretKey: base64.StdEncoding.EncodeToString([]byte("secret-key")),
	}
}

func TestStoreDedicatedCapacityBackupHmacSecret_EmptyKeyFields(t *testing.T) {
	activity := &ExpertModeBackupConfigActivity{}
	tests := []struct {
		name string
		keys *commonparams.HmacKeys
	}{
		{name: "empty access key", keys: &commonparams.HmacKeys{AccessKey: "", SecretKey: "c2VjcmV0"}},
		{name: "empty secret key", keys: &commonparams.HmacKeys{AccessKey: "YWNjZXNz", SecretKey: ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := activity.StoreDedicatedCapacityBackupHmacSecret(context.Background(),
				&commonparams.ResourceNames{ServiceAccountId: "bv-sa-id"},
				tt.keys,
			)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "HMAC keys are required")
		})
	}
}
