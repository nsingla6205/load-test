package activities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

type SmcTokenRotationActivity struct {
	SE database.Storage
}

var (
	gcpProjectId           = env.GetString("SMC_GCP_PROJECT_ID", "")
	GetSmcLicenseFromCloud = _getSMCLicenseFromCloud
	GenerateTokenForNode   = _generateTokenForNode
	GetSecretWithVersion   = _getSecretWithVersion
)

// ToDo: Add encryption and decryption of the SMC license for SMC token rotation
func (a *SmcTokenRotationActivity) GetSMCLicenseFromCloud(ctx context.Context) (string, error) {
	secret, err := GetSmcLicenseFromCloud(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get SMC license from cloud: %w", err)
	}
	return secret, nil
}

func _getSMCLicenseFromCloud(ctx context.Context) (string, error) {
	var gcpService hyperscaler2.Services
	gcpService, err := hyperscaler2.GetGCPService(ctx)
	if err != nil {
		return "", err
	}
	secretID := utils.GetSMCSecretName()
	versionID := utils.GetSMCSecretVersionName()
	secret, err := GetSecretWithVersion(gcpService, gcpProjectId, secretID, versionID)
	if err != nil {
		return "", err
	}
	return secret.SecretVersion.Value, nil
}

func _getSecretWithVersion(gcpService hyperscaler2.Services, gcpProjectId, secretID, versionID string) (*hyperscalermodels.CustomSecret, error) {
	secret, err := gcpService.GetSecretWithCustomVersion(gcpProjectId, secretID, versionID)
	if err != nil || secret == nil || secret.SecretVersion == nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to get secret for project: %s, secretID: %s, versionName: %s, err: %s", gcpProjectId, secretID, versionID, err))
	}
	return secret, nil
}

func _generateTokenForNode(ctx context.Context, node *models.Node, clientSecret *string) (*string, error) {
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	token, err := provider.PostClusterLicenseAccessToken(ctx, *clientSecret)
	if err != nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("failed to generate token for node %s: %w", node.Name, err))
	}
	if token == nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("generated token is nil for node %s", node.Name))
	}
	if *token == "" {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("generated token is empty for node %s", node.Name))
	}
	return token, nil
}
