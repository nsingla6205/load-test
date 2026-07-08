package kms_activities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

const (
	vcpCmekSAPrefix = "cmek"
)

var (
	generateVCPServiceAccountID = _generateVCPServiceAccountID
)

// _generateVCPServiceAccountID generates a deterministic service account ID for VCP CMEK.
// Format: cmek-{shortRegion}-{projectNumber}  (max 30 chars for GCP SA ID limit)
// Matches the CVN naming convention (generateUniqueSAEmailFor1P).
func _generateVCPServiceAccountID(projectNumber, keyRingLocation string) (string, error) {
	shortRegion := utils.GetShortRegion(keyRingLocation)
	if shortRegion == "" {
		return "", fmt.Errorf("failed to derive short region from %q", keyRingLocation)
	}
	saID := fmt.Sprintf("%s-%s-%s", vcpCmekSAPrefix, shortRegion, projectNumber)
	if len(saID) > 30 {
		return "", fmt.Errorf("generated service account ID %q exceeds GCP 30 char limit", saID)
	}
	return saID, nil
}

// CreateGCPServiceAccountActivity creates a GCP service account in the CMEK global project for VCP-managed KMS configs.
// It sets the service account email on kmsConfig.KmsAttributes.SdeServiceAccountEmail so that downstream
// activities (CreateVSAKmsConfigSAKeyActivity) can reuse the same SA email field.
func (j *KmsConfigActivity) CreateGCPServiceAccountActivity(ctx context.Context, kmsConfig *datamodel.KmsConfig) (*datamodel.KmsConfig, error) {
	activity.RecordHeartbeat(ctx, "Starting CreateGCPServiceAccountActivity")
	defer activity.RecordHeartbeat(ctx, "Finished CreateGCPServiceAccountActivity")
	logger := util.GetLogger(ctx)

	cmekProjectID := utils.CmekGlobalProjectID
	if cmekProjectID == "" {
		// Permanent configuration error — retrying won't help
		return nil, errors2.WrapAsNonRetryableTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSInvalidConfiguration, fmt.Errorf("CMEK_GLOBAL_PROJECT_ID is not configured")))
	}

	saID, err := generateVCPServiceAccountID(kmsConfig.CustomerProjectID, kmsConfig.KeyRingLocation)
	if err != nil {
		// Region map or ID generation error — permanent, retrying won't help
		return nil, errors2.WrapAsNonRetryableTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSCreateGCPServiceAccount, fmt.Errorf("failed to generate VCP service account ID: %w", err)))
	}

	saEmail := utils.ConstructServiceAccountEmail(saID, cmekProjectID)
	logger.Info("Creating GCP service account for VCP CMEK",
		"sa_id", saID, "sa_email", saEmail, "cmek_project", cmekProjectID)

	cloudService, err := getGcpService(ctx)
	if err != nil {
		return nil, errors2.WrapAsTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSCreateGCPServiceAccount, err))
	}

	activity.RecordHeartbeat(ctx, "Creating service account in CMEK global project")
	createReq := &hyperscalermodels.CreateServiceAccountRequest{
		AccountId: saID,
		ServiceAccount: &hyperscalermodels.ServiceAccount{
			DisplayName: fmt.Sprintf("VCP CMEK SA for project %s", kmsConfig.CustomerProjectID),
			Description: fmt.Sprintf("VCP-managed CMEK service account for customer project %s, region %s",
				kmsConfig.CustomerProjectID, kmsConfig.KeyRingLocation),
		},
	}

	sa, err := cloudService.CreateServiceAccount(createReq, cmekProjectID, saEmail)
	if err != nil {
		logger.Error("Failed to create GCP service account for VCP CMEK", "error", err, "sa_email", saEmail)
		return nil, errors2.WrapAsTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSCreateGCPServiceAccount, fmt.Errorf("failed to create GCP service account %s: %w", saEmail, err)))
	}

	logger.Info("Successfully created GCP service account for VCP CMEK", "sa_email", sa.Email)

	kmsConfig.KmsAttributes.VcpServiceAccountEmail = sa.Email

	// Persist the updated attributes to DB
	activity.RecordHeartbeat(ctx, "Updating KMS config attributes with VCP SA email")
	se := j.SE
	_, err = se.UpdateKmsConfigAttributes(ctx, kmsConfig.UUID, kmsConfig.KmsAttributes)
	if err != nil {
		return nil, errors2.WrapAsTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSUpdateConfigAttributes, fmt.Errorf("failed to update KMS config attributes: %w", err)))
	}

	return kmsConfig, nil
}

// EnableGCPServiceAccountActivity enables the GCP service account in IAM for VCP-managed KMS configs.
// This is called during KMS config creation to ensure the SA is active (it may have been disabled by a
// previous delete flow if the same SA is being reused).
func (j *KmsConfigActivity) EnableGCPServiceAccountActivity(ctx context.Context, kmsConfig *datamodel.KmsConfig) error {
	activity.RecordHeartbeat(ctx, "Starting EnableGCPServiceAccountActivity")
	defer activity.RecordHeartbeat(ctx, "Finished EnableGCPServiceAccountActivity")
	logger := util.GetLogger(ctx)

	if kmsConfig.KmsAttributes == nil {
		logger.Info("No KMS attributes found, skipping GCP SA enable")
		return nil
	}

	saEmail := kmsConfig.KmsAttributes.VcpServiceAccountEmail
	if saEmail == "" {
		logger.Info("No VCP service account email found, skipping GCP SA enable")
		return nil
	}

	logger.Info("Enabling GCP service account for VCP CMEK", "sa_email", saEmail)

	cloudService, err := getGcpService(ctx)
	if err != nil {
		return errors2.WrapAsTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSEnableGCPServiceAccount, err))
	}

	activity.RecordHeartbeat(ctx, "Enabling service account in GCP IAM")
	err = gcpEnableServiceAccount(cloudService, saEmail)
	if err != nil {
		logger.Error("Failed to enable GCP service account", "error", err, "sa_email", saEmail)
		return errors2.WrapAsTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSEnableGCPServiceAccount, fmt.Errorf("failed to enable GCP service account %s: %w", saEmail, err)))
	}

	logger.Info("Successfully enabled GCP service account for VCP CMEK", "sa_email", saEmail)
	return nil
}

// DisableGCPServiceAccountActivity disables the GCP service account in IAM for VCP-managed KMS configs.
// This is called during KMS config deletion to revoke the SA's ability to access the customer's KMS key
// without fully deleting the SA (allowing potential recovery).
func (j *KmsConfigActivity) DisableGCPServiceAccountActivity(ctx context.Context, kmsConfig *datamodel.KmsConfig) error {
	activity.RecordHeartbeat(ctx, "Starting DisableGCPServiceAccountActivity")
	defer activity.RecordHeartbeat(ctx, "Finished DisableGCPServiceAccountActivity")
	logger := util.GetLogger(ctx)

	if kmsConfig.KmsAttributes == nil {
		logger.Info("No KMS attributes found, skipping GCP SA disable")
		return nil
	}

	saEmail := kmsConfig.KmsAttributes.VcpServiceAccountEmail
	if saEmail == "" {
		logger.Info("No VCP service account email found, skipping GCP SA disable")
		return nil
	}

	logger.Info("Disabling GCP service account for VCP CMEK", "sa_email", saEmail)

	cloudService, err := getGcpService(ctx)
	if err != nil {
		return errors2.WrapAsTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSDisableGCPServiceAccount, err))
	}

	activity.RecordHeartbeat(ctx, "Disabling service account in GCP IAM")
	err = gcpDisableServiceAccount(cloudService, saEmail)
	if err != nil {
		logger.Error("Failed to disable GCP service account", "error", err, "sa_email", saEmail)
		return errors2.WrapAsTemporalApplicationError(
			errors2.NewVCPError(errors2.ErrKMSDisableGCPServiceAccount, fmt.Errorf("failed to disable GCP service account %s: %w", saEmail, err)))
	}

	logger.Info("Successfully disabled GCP service account for VCP CMEK", "sa_email", saEmail)
	return nil
}
