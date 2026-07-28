package expertmodeactivities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const dedicatedBackupHMACSecretPrefix = "hardware-bv-hmac-"

type dedicatedBackupHmacSecretPayload struct {
	AccessKeyID string `json:"access_key_id"`
	Secret      string `json:"secret"`
}

// ExpertModeBackupConfigActivity supports dedicated-capacity expert-mode backup credential provisioning.
type ExpertModeBackupConfigActivity struct{}

var getCloudServiceForExpertModeBackupConfig = hyperscaler.ResolveCloudService

func dedicatedBackupHMACSecretID(resourceName *commonparams.ResourceNames) string {
	return dedicatedBackupHMACSecretPrefix + resourceName.ServiceAccountId
}

// CreateDedicatedCapacityBackupServiceAccount creates the backup service account for dedicated-capacity pools.
func (a *ExpertModeBackupConfigActivity) CreateDedicatedCapacityBackupServiceAccount(ctx context.Context, resourceName *commonparams.ResourceNames, tenancyDetails *commonparams.TenancyInfo) (*hyperscalermodels.ServiceAccount, error) {
	projectNumber := tenancyDetails.RegionalTenantProject
	saEmail := resourceName.Email

	cloudService, err := getCloudServiceForExpertModeBackupConfig(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	createReq := &hyperscalermodels.CreateServiceAccountRequest{
		AccountId: resourceName.ServiceAccountId,
		ServiceAccount: &hyperscalermodels.ServiceAccount{
			DisplayName: "VSA hardware backup service account",
		},
	}
	sa, err := cloudService.CreateServiceAccount(createReq, projectNumber, saEmail)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return sa, nil
}

// GetDedicatedCapacityBackupHmacSecret returns the existing deterministic HMAC secret, if present.
func (a *ExpertModeBackupConfigActivity) GetDedicatedCapacityBackupHmacSecret(ctx context.Context, resourceName *commonparams.ResourceNames) (*commonparams.BucketDetails, error) {
	gcpService, err := getCloudServiceForExpertModeBackupConfig(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	secretID := dedicatedBackupHMACSecretID(resourceName)
	secret, err := gcpService.GetSecretWithLatestVersion(env.SecretManagerProjectID, secretID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if secret == nil || secret.SecretVersion == nil {
		return nil, nil
	}

	var payload dedicatedBackupHmacSecretPayload
	if err = json.Unmarshal([]byte(secret.SecretVersion.Value), &payload); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to decode existing HMAC secret: %w", err))
	}
	if payload.AccessKeyID == "" || payload.Secret == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("existing HMAC secret is missing credentials"))
	}

	return &commonparams.BucketDetails{
		ServiceAccountName: resourceName.Email,
		HmacSecretID:       secretID,
	}, nil
}

// StoreDedicatedCapacityBackupHmacSecret stores HMAC credentials in Secret Manager for dedicated-capacity pools.
// Bucket creation is handled separately via the shared CreateBucketForBackupVault activity.
func (a *ExpertModeBackupConfigActivity) StoreDedicatedCapacityBackupHmacSecret(
	ctx context.Context,
	resourceName *commonparams.ResourceNames,
	hmacKeys *commonparams.HmacKeys,
) (*commonparams.BucketDetails, error) {
	if hmacKeys == nil || hmacKeys.AccessKey == "" || hmacKeys.SecretKey == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("HMAC keys are required to store dedicated-capacity backup credentials"))
	}

	accessKeyBytes, err := base64.StdEncoding.DecodeString(hmacKeys.AccessKey)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to decode access key: %w", err))
	}
	secretKeyBytes, err := base64.StdEncoding.DecodeString(hmacKeys.SecretKey)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to decode secret key: %w", err))
	}
	accessKey := string(accessKeyBytes)
	secretKey := string(secretKeyBytes)

	gcpService, err := getCloudServiceForExpertModeBackupConfig(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	secretID := dedicatedBackupHMACSecretID(resourceName)
	payload, err := json.Marshal(dedicatedBackupHmacSecretPayload{
		AccessKeyID: accessKey,
		Secret:      secretKey,
	})
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if _, err = gcpService.CreateSecret(env.SecretManagerProjectID, env.Region, secretID, string(payload)); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return &commonparams.BucketDetails{
		ServiceAccountName: resourceName.Email,
		HmacSecretID:       secretID,
	}, nil
}
