package oci

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/secrets"
	"github.com/oracle/oci-go-sdk/v65/vault"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// ──────────────────────────────────────────────────────────────────────────────
// Test-seam variables (same pattern as GCP's AddSecretVersion / GetSecretVersion)
// ──────────────────────────────────────────────────────────────────────────────
var (
	AddSecretVersion = _addSecretVersion
	GetSecretVersion = _getSecretVersion
)

type OCICustomSecret struct {
	Ocid    string
	Name    string
	Value   string
	Version int64
}

// ──────────────────────────────────────────────────────────────────────────────
//
// Key OCI docs:
//   - CreateSecret:   https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/CreateSecret
//   - GetSecret:      https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/GetSecret
//   - UpdateSecret:   https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/UpdateSecret
//   - DeleteSecret:   https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/ScheduleSecretDeletion
//   - GetSecretBundle: https://docs.oracle.com/en-us/iaas/api/#/en/secretretrieval/20190301/SecretBundle/GetSecretBundle
//   - GetSecretBundleByName: https://docs.oracle.com/en-us/iaas/api/#/en/secretretrieval/20190301/SecretBundle/GetSecretBundleByName
//   - Go SDK reference: https://docs.oracle.com/en-us/iaas/tools/go/65.108.3/vault/index.html
// ──────────────────────────────────────────────────────────────────────────────

// CreateSecret creates a secret in OCI Vault and stores the initial value.
// OCI's CreateSecret accepts the initial content inline — so a single
// API call replaces GCP's two-step create+addVersion flow.
//
// Parameters:
//   - compartmentID: OCID of the compartment (≈ GCP projectID)
//   - vaultID:       OCID of the vault where the secret will live
//   - keyID:         OCID of the master encryption key used to encrypt the secret
//   - secretName:    human-readable name (≈ GCP secretID)
//   - secretValue:   the plaintext value to store
//
// Docs: https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/CreateSecret
func (sdkWrapper *ociSDKWrapper) CreateSecret(compartmentID, vaultID, keyID, secretName, secretValue string) (*OCICustomSecret, error) {
	ociService := sdkWrapper.svc
	ociService.Logger.Infof("Calling CreateSecret for compartment: %s, secretName: %s", compartmentID, secretName)

	if secretValue == "" {
		ociService.Logger.Errorf("Secret value is empty")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, fmt.Errorf("secret value is empty"))
	}

	encodedContent := base64.StdEncoding.EncodeToString([]byte(secretValue))

	// OCI CreateSecret takes the first version's content inline.
	// Docs: https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/CreateSecret
	resp, err := ociService.AdminOCIService.vaultClient.CreateSecret(ociService.Ctx, vault.CreateSecretRequest{
		CreateSecretDetails: vault.CreateSecretDetails{
			CompartmentId: ocicommon.String(compartmentID),
			VaultId:       ocicommon.String(vaultID),
			KeyId:         ocicommon.String(keyID),
			SecretName:    ocicommon.String(secretName),
			SecretContent: vault.Base64SecretContentDetails{
				Content: ocicommon.String(encodedContent),
			},
			Description: ocicommon.String("VCP managed secret: " + secretName),
		},
	})
	if err != nil {
		ociService.Logger.Errorf("CreateSecret failed for compartment: %s, secret: %s, err: %s", compartmentID, secretName, err.Error())
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "already exists") || strings.Contains(errMsg, "name already") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIVaultNameConflict, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, err)
	}

	secretOCID := derefString(resp.Secret.Id)
	ociService.Logger.Infof("CreateSecret success — OCID: %s", secretOCID)

	return &OCICustomSecret{
		Ocid:    secretOCID,
		Name:    secretName,
		Value:   secretValue,
		Version: 1,
	}, nil
}

// GetSecretByName retrieves a secret from OCI Vault by its human-readable name
// and vault OCID. Both outcomes are valid:
//   - Secret found     → decoded and returned as *CustomSecret.
//   - Secret not found → nil, nil (HTTP 404 is not treated as an error).
//
// Docs: https://docs.oracle.com/en-us/iaas/api/#/en/secretretrieval/20190301/SecretBundle/GetSecretBundleByName
func (sdkWrapper *ociSDKWrapper) GetSecretByName(secretName string, vaultID string) (*OCICustomSecret, error) {
	ociService := sdkWrapper.svc
	ociService.Logger.Infof("Calling GetSecretByName — secretName: %s", secretName)

	secretResp, err := ociService.AdminOCIService.secretsClient.GetSecretBundleByName(ociService.Ctx, secrets.GetSecretBundleByNameRequest{
		SecretName: ocicommon.String(secretName),
		VaultId:    ocicommon.String(vaultID),
	})
	if err != nil {
		ociService.Logger.Errorf("GetSecretByName failed for secretName: %s, err: %s", secretName, err.Error())
		// ociResourceNotFoundCheck returns nil for HTTP 404 so the caller gets (nil, nil).
		return nil, ociResourceNotFoundCheck(err)
	}

	if secretResp.SecretBundleContent == nil {
		ociService.Logger.Warnf("GetSecretByName: secret %s found but content is nil, treating as not found", secretName)
		return nil, nil
	}

	secretValue, err := extractSecretBundleContent(secretResp.SecretBundleContent)
	if err != nil {
		return nil, err
	}

	ociService.Logger.Infof("GetSecretByName success — secretName: %s", secretName)

	return &OCICustomSecret{
		Name:    secretName,
		Value:   secretValue,
		Version: derefInt64(secretResp.VersionNumber),
		Ocid:    derefString(secretResp.SecretId),
	}, nil
}

// GetSecretWithLatestVersion retrieves a secret and its current (latest) version
// content from OCI Vault.
//
// GCP equivalent: GcpServices.GetSecretWithLatestVersion(projectID, secretID)
//
// GCP takes projectID + secretID because it needs both to build the resource
// path. OCI only needs the secret OCID — it is globally unique and replaces
// both GCP params.
//
// Docs:
//   - GetSecret:      https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/GetSecret
//   - GetSecretBundle: https://docs.oracle.com/en-us/iaas/api/#/en/secretretrieval/20190301/SecretBundle/GetSecretBundle
func (sdkWrapper *ociSDKWrapper) GetSecretWithLatestVersion(secretID string) (*OCICustomSecret, error) {
	ociService := sdkWrapper.svc
	ociService.Logger.Infof("Calling GetSecretWithLatestVersion — secretID: %s", secretID)

	// Step 1: Fetch secret metadata (name, creation time, etc.) — no secret value.
	secretResp, err := ociService.AdminOCIService.vaultClient.GetSecret(ociService.Ctx, vault.GetSecretRequest{
		SecretId: ocicommon.String(secretID),
	})
	if err != nil {
		ociService.Logger.Errorf("GetSecret failed for secretID: %s, err: %s", secretID, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIVaultSecretFetchFailed, fmt.Errorf("failed to get secret metadata for OCID %s: %w", secretID, err))
	}

	if isSecretInDeletionState(secretResp.Secret.LifecycleState) {
		ociService.Logger.Warnf("GetSecretWithLatestVersion: secret %s is in %s state, treating as not found", secretID, secretResp.Secret.LifecycleState)
		return nil, nil
	}

	// Step 2: Fetch the actual secret value (latest version) via GetSecretBundle.
	version, err := GetSecretVersion(ociService, secretID)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err)
	}

	if version == nil {
		ociService.Logger.Warnf("GetSecretWithLatestVersion: no version found for secret: %s", secretID)
		return nil, nil
	}

	ociService.Logger.Infof("GetSecretWithLatestVersion success — secretID: %s", secretID)

	return &OCICustomSecret{
		Name:    derefString(secretResp.SecretName),
		Value:   version.Value,
		Version: version.Version,
		Ocid:    secretID,
	}, nil
}

// GetSecretWithCustomVersion retrieves a secret with a specific version number.
//
// OCI uses GetSecretBundle with a VersionNumber (int64).
//
// Docs: https://docs.oracle.com/en-us/iaas/api/#/en/secretretrieval/20190301/SecretBundle/GetSecretBundle
func (sdkWrapper *ociSDKWrapper) GetSecretWithCustomVersion(secretID string, versionNumber int64) (*OCICustomSecret, error) {
	ociService := sdkWrapper.svc
	ociService.Logger.Infof("Calling GetSecretWithCustomVersion — secretID: %s, version: %d", secretID, versionNumber)

	// Step 1: Fetch secret metadata (name, creation time, etc.) — no secret value.
	secretResp, err := ociService.AdminOCIService.vaultClient.GetSecret(ociService.Ctx, vault.GetSecretRequest{
		SecretId: ocicommon.String(secretID),
	})
	if err != nil {
		ociService.Logger.Errorf("GetSecret failed for secretID: %s, err: %s", secretID, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIVaultSecretFetchFailed, fmt.Errorf("failed to get secret metadata for OCID %s: %w", secretID, err))
	}

	if isSecretInDeletionState(secretResp.Secret.LifecycleState) {
		ociService.Logger.Warnf("GetSecretWithCustomVersion: secret %s is in %s state, treating as not found", secretID, secretResp.Secret.LifecycleState)
		return nil, nil
	}

	// Step 2: Fetch the actual secret value for the specific version.
	version, err := GetSecretVersion(ociService, secretID, versionNumber)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err)
	}

	if version == nil {
		ociService.Logger.Warnf("GetSecretWithCustomVersion: no version found for secret: %s, version: %d", secretID, versionNumber)
		return nil, nil
	}

	ociService.Logger.Debugf("GetSecretWithCustomVersion success — secretID: %s, version: %d", secretID, versionNumber)

	return &OCICustomSecret{
		Name:    derefString(secretResp.SecretName),
		Value:   version.Value,
		Version: versionNumber,
		Ocid:    secretID,
	}, nil
}

// DeleteSecret schedules a secret for deletion in OCI Vault.
// OCI requires scheduling deletion via ScheduleSecretDeletion — the secret enters
// PENDING_DELETION state and is permanently removed after the configured
// retention period (minimum 1 day, default 30 days).
//
// The call is idempotent: if the secret is already in a deletion lifecycle state
// (SCHEDULING_DELETION / PENDING_DELETION / DELETING / DELETED) we skip the
// ScheduleSecretDeletion call to avoid the HTTP 409 Conflict that OCI returns
// for re-deletion attempts. This makes retries after partial failures safe.
//
// Docs: https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/ScheduleSecretDeletion
func (sdkWrapper *ociSDKWrapper) DeleteSecret(secretID string) error {
	ociService := sdkWrapper.svc
	retentionDays := env.OCISecretDeletionRetentionDays
	ociService.Logger.Infof("Calling DeleteSecret (ScheduleSecretDeletion) for secretID: %s, retentionDays: %d", secretID, retentionDays)

	// Pre-flight lifecycle check — avoid the conflict error when the secret is already
	// scheduled for / undergoing deletion. Treat 404 as "already gone" (no-op).
	secretResp, err := ociService.AdminOCIService.vaultClient.GetSecret(ociService.Ctx, vault.GetSecretRequest{
		SecretId: ocicommon.String(secretID),
	})
	if err != nil {
		if ociResourceNotFoundCheck(err) == nil {
			ociService.Logger.Infof("DeleteSecret: secret %s not found, treating as already deleted", secretID)
			return nil
		}
		ociService.Logger.Errorf("DeleteSecret: GetSecret failed for secretID: %s, err: %s", secretID, err.Error())
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err)
	}
	if isSecretInDeletionState(secretResp.Secret.LifecycleState) {
		ociService.Logger.Infof("DeleteSecret: secret %s already in %s state, skipping ScheduleSecretDeletion", secretID, secretResp.Secret.LifecycleState)
		return nil
	}

	deletionTime := time.Now().UTC().AddDate(0, 0, retentionDays)
	_, err = ociService.AdminOCIService.vaultClient.ScheduleSecretDeletion(ociService.Ctx, vault.ScheduleSecretDeletionRequest{
		SecretId: ocicommon.String(secretID),
		ScheduleSecretDeletionDetails: vault.ScheduleSecretDeletionDetails{
			TimeOfDeletion: &ocicommon.SDKTime{Time: deletionTime},
		},
	})
	if err != nil {
		ociService.Logger.Errorf("DeleteSecret (ScheduleSecretDeletion) failed for secretID: %s, err: %s", secretID, err.Error())
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceDeprovisionError, err)
	}

	ociService.Logger.Infof("DeleteSecret success — secretID: %s scheduled for deletion in %d days", secretID, retentionDays)
	return nil
}

// _addSecretVersion creates a new version of an existing secret in OCI Vault.
//
// OCI uses UpdateSecret with SecretContent containing the new base64 payload,
// which atomically creates a new version.
//
// Docs: https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/UpdateSecret
func _addSecretVersion(ociService *OciServices, secretID, secretValue string) (*OCICustomSecret, error) {
	ociService.Logger.Infof("Calling AddSecretVersion for secretID: %s", secretID)

	if secretValue == "" {
		ociService.Logger.Errorf("Secret value is empty")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, fmt.Errorf("secret value is empty"))
	}

	encodedData := base64.StdEncoding.EncodeToString([]byte(secretValue))

	// OCI creates a new version by updating the secret content.
	// Docs: https://docs.oracle.com/en-us/iaas/api/#/en/secretmgmt/20180608/Secret/UpdateSecret
	resp, err := ociService.AdminOCIService.vaultClient.UpdateSecret(ociService.Ctx, vault.UpdateSecretRequest{
		SecretId: ocicommon.String(secretID),
		UpdateSecretDetails: vault.UpdateSecretDetails{
			SecretContent: vault.Base64SecretContentDetails{
				Content: ocicommon.String(encodedData),
			},
		},
	})
	if err != nil {
		ociService.Logger.Errorf("AddSecretVersion (UpdateSecret) failed for secretID: %s, err: %s", secretID, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, err)
	}

	return &OCICustomSecret{
		Name:    derefString(resp.SecretName),
		Value:   secretValue,
		Version: derefInt64(resp.CurrentVersionNumber),
		Ocid:    secretID,
	}, nil
}

// _getSecretVersion retrieves a secret's content from OCI Vault via the OCI SDK.
// OCI mirrors this with a single GetSecretBundle API —
// pass versionNumber=0 for the current (latest) version,
// or a specific int64 for a pinned version.
//
// This is a read, so it always uses the OCI SDK directly.
//
// Docs: https://docs.oracle.com/en-us/iaas/api/#/en/secretretrieval/20190301/SecretBundle/GetSecretBundle
func _getSecretVersion(ociService *OciServices, secretID string, versionNumber ...int64) (*OCICustomSecret, error) {
	req := secrets.GetSecretBundleRequest{
		SecretId: ocicommon.String(secretID),
	}

	// versionNumber is variadic: omitted or 0 → latest (CURRENT stage);
	// any positive value → that specific version number.
	if len(versionNumber) > 0 && versionNumber[0] > 0 {
		req.VersionNumber = ocicommon.Int64(versionNumber[0])
		ociService.Logger.Infof("Calling GetSecretVersion — secretID: %s, version: %d", secretID, versionNumber[0])
	} else {
		req.Stage = secrets.GetSecretBundleStageCurrent
		ociService.Logger.Infof("Calling GetSecretVersion — secretID: %s, version: current", secretID)
	}

	resp, err := ociService.AdminOCIService.secretsClient.GetSecretBundle(ociService.Ctx, req)
	if err != nil {
		ociService.Logger.Errorf("GetSecretBundle failed for secretID: %s, err: %s", secretID, err.Error())
		return nil, ociResourceNotFoundCheck(err)
	}

	secretValue, err := extractSecretBundleContent(resp.SecretBundleContent)
	if err != nil {
		return nil, err
	}

	ociService.Logger.Infof("GetSecretVersion success for secret ocid — %s", secretID)

	return &OCICustomSecret{
		Value:   secretValue,
		Version: derefInt64(resp.VersionNumber),
		Ocid:    secretID,
	}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// extractSecretBundleContent decodes the base64-encoded payload from an OCI
// SecretBundleContent interface. OCI always returns Base64SecretBundleContentDetails.
func extractSecretBundleContent(content secrets.SecretBundleContentDetails) (string, error) {
	if content == nil {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("secret bundle content is nil"))
	}

	base64Content, ok := content.(secrets.Base64SecretBundleContentDetails)
	if !ok {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("unexpected secret bundle content type: %T", content))
	}

	if base64Content.Content == nil {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("secret bundle content value is nil"))
	}

	decoded, err := base64.StdEncoding.DecodeString(*base64Content.Content)
	if err != nil {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrBase64DecodingError, err)
	}

	return string(decoded), nil
}

// isSecretInDeletionState returns true when the secret's lifecycle state
// indicates the secret has been deleted or is in the process of being deleted.
// OCI uses a two-phase deletion model (ScheduleSecretDeletion → PENDING_DELETION
// → permanent removal), unlike GCP which deletes immediately. During these
// states, secret content is no longer accessible via GetSecretBundle.
func isSecretInDeletionState(state vault.SecretLifecycleStateEnum) bool {
	switch state {
	case vault.SecretLifecycleStatePendingDeletion,
		vault.SecretLifecycleStateSchedulingDeletion,
		vault.SecretLifecycleStateDeleting,
		vault.SecretLifecycleStateDeleted:
		return true
	default:
		return false
	}
}

// derefInt64 safely dereferences an *int64, returning 0 if nil.
func derefInt64(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}

// derefString safely dereferences an *string, returning "" if nil.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
