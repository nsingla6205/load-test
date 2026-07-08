package google

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/common"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/secretmanager/v1"
)

var (
	AddSecretVersion = _addSecretVersion
	GetSecretVersion = _getSecretVersion
)

// CreateSecret creates a secret. Reference: https://cloud.google.com/secret-manager/docs/reference/rest/v1beta1/projects.secrets/create
// It is idempotent: if the secret already exists (409), it gets the secret and adds a version if none exists,
// or returns the existing latest version if the secret already has versions (e.g. after a retry following CreateSecretVersion failure).
func (gcpService *GcpServices) CreateSecret(projectID, region, secretID, secretValue string) (*models.CustomSecret, error) {
	gcpService.Logger.Debug(fmt.Sprintf("Calling CreateSecret for project id : %s, secret id : %s", projectID, secretID))

	// Define the parent resource
	parent := fmt.Sprintf("projects/%s", projectID)

	// Create the secret
	// TODO : Add expiration time and rotation for the secret
	secret := &secretmanager.Secret{
		Replication: &secretmanager.Replication{
			UserManaged: &secretmanager.UserManaged{
				Replicas: []*secretmanager.Replica{{Location: region}},
			},
		},
	}

	secret, err := gcpService.AdminGCPService.secretManagerService.Projects.Secrets.Create(parent, secret).SecretId(secretID).Context(gcpService.Ctx).Do()
	if err != nil {
		var gErr *googleapi.Error
		if errors.As(err, &gErr) && gErr.Code == http.StatusConflict { // 409 Already exists
			// Idempotent path: secret was created but a subsequent step (e.g. AddSecretVersion) may have failed (e.g. 503).
			// Get the existing secret and either return existing version or add one if none exists.
			return createSecretHandleAlreadyExists(gcpService, projectID, secretID, secretValue)
		}
		gcpService.Logger.Errorf("CreateSecret failed for project : %s, secretID : %s, err : %s", projectID, secretID, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}

	gcpService.Logger.Debugf("CreateSecret success with response :  %s", secret.Name)

	// Add secret version
	version, err := AddSecretVersion(gcpService, projectID, secretID, secretValue)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}

	customSecret, err := ConvertSecretToCustomSecret(secret, version)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
	}

	return customSecret, nil
}

// createSecretHandleAlreadyExists is called when Create returns 409. It gets the secret and either returns
// the existing latest version (idempotent) or adds a new version if the secret has no versions yet.
func createSecretHandleAlreadyExists(gcpService *GcpServices, projectID, secretID, secretValue string) (*models.CustomSecret, error) {
	name := fmt.Sprintf("projects/%s/secrets/%s", projectID, secretID)
	secret, err := gcpService.AdminGCPService.secretManagerService.Projects.Secrets.Get(name).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("CreateSecret get existing secret failed for : %s, err : %s", name, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	gcpService.Logger.Debugf("CreateSecret: secret already exists, ensuring version for : %s", name)

	// If secret already has a version, return it (idempotent).
	version, err := GetSecretVersion(gcpService, projectID, secretID, LatestVersion)
	if err == nil && version != nil {
		gcpService.Logger.Debugf("CreateSecret: existing secret has version, returning for : %s", name)
		customSecret, err := ConvertSecretToCustomSecret(secret, version)
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
		}
		return customSecret, nil
	}

	// No version yet (e.g. AddSecretVersion failed with 503); add version now.
	version, err = AddSecretVersion(gcpService, projectID, secretID, secretValue)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	customSecret, err := ConvertSecretToCustomSecret(secret, version)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
	}
	return customSecret, nil
}

// GetSecretWithLatestVersion retrieves a secret from the secret manager. Reference: https://cloud.google.com/secret-manager/docs/reference/rest/v1beta1/projects.secrets/get
func (gcpService *GcpServices) GetSecretWithLatestVersion(projectID, secretID string) (*models.CustomSecret, error) {
	gcpService.Logger.Debug(fmt.Sprintf("Calling GetSecretWithLatestVersion for project id : %s, secretID : %s", projectID, secretID))
	name := fmt.Sprintf("projects/%s/secrets/%s", projectID, secretID)

	secret, err := gcpService.AdminGCPService.secretManagerService.Projects.Secrets.Get(name).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("GetSecretWithLatestVersion failed for secret : %s, err : %s", name, err.Error())
		return nil, googleResourceNotFoundCheck(err)
	}

	version, err := GetSecretVersion(gcpService, projectID, secretID, LatestVersion)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}

	if version == nil {
		gcpService.Logger.Warnf("GetSecretWithLatestVersion failed as no versions found for secret : %s", name)
		return nil, nil
	}

	gcpService.Logger.Debug(fmt.Sprintf("GetSecretWithLatestVersion success with response :  %s", name))
	customSecret, err := ConvertSecretToCustomSecret(secret, version)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
	}
	return customSecret, nil
}

// GetSecretWithCustomVersion retrieves a secret with a specific version from the secret manager.
func (gcpService *GcpServices) GetSecretWithCustomVersion(projectID, secretID string, versionID string) (*models.CustomSecret, error) {
	gcpService.Logger.Debugf("Calling GetSecretWithCustomVersion for project id : %s, secretID : %s , versionID : %s", projectID, secretID, versionID)
	name := fmt.Sprintf("projects/%s/secrets/%s", projectID, secretID)

	secret, err := gcpService.AdminGCPService.secretManagerService.Projects.Secrets.Get(name).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("GetSecretWithCustomVersion failed for secret : %s, err : %s", name, err.Error())
		return nil, googleResourceNotFoundCheck(err)
	}

	version, err := GetSecretVersion(gcpService, projectID, secretID, versionID)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	gcpService.Logger.Debugf("GetSecretWithCustomVersion success with response :  %s", name)
	customSecret, err := ConvertSecretToCustomSecret(secret, version)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
	}
	return customSecret, nil
}

// DeleteSecret deletes a secret from the secret manager. Reference: https://cloud.google.com/secret-manager/docs/reference/rest/v1beta1/projects.secrets/delete
func (gcpService *GcpServices) DeleteSecret(projectID, secretID string) error {
	gcpService.Logger.Debug(fmt.Sprintf("Calling DeleteSecret for project id : %s, secretID : %s", projectID, secretID))
	name := fmt.Sprintf("projects/%s/secrets/%s", projectID, secretID)
	_, err := gcpService.AdminGCPService.secretManagerService.Projects.Secrets.Delete(name).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("DeleteSecret failed for secret : %s, err : %s", name, err.Error())
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceDeprovisionError, err)
	}
	return nil
}

// AddSecretVersion creates a secret version and stores the private key in the secret manager. Reference: https://cloud.google.com/secret-manager/docs/reference/rest/v1/projects.secrets/addVersion
func _addSecretVersion(gcpService *GcpServices, projectID, secretName, secretValue string) (*models.CustomSecretVersion, error) {
	gcpService.Logger.Debug(fmt.Sprintf("Calling CreateSecretVersion for project id : %s", projectID))

	// Validate secret value before processing
	if secretValue == "" {
		gcpService.Logger.Errorf("Secret value is empty")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("secret value is empty"))
	}

	encodedData := base64.StdEncoding.EncodeToString([]byte(secretValue))

	parent := fmt.Sprintf("projects/%s/secrets/%s", projectID, secretName)
	req := &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{
			Data: encodedData,
		},
	}
	secretVersion, err := gcpService.AdminGCPService.secretManagerService.Projects.Secrets.AddVersion(parent, req).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("CreateSecretVersion failed for project : %s, err : %s", projectID, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}

	customSecretVersion, err := common.ConvertToCustomSecretVersion(secretVersion.Name, secretValue)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
	}
	return customSecretVersion, nil
}

func (gcpService *GcpServices) AddSecretVersion(projectID, secretID, secretValue string) (*models.CustomSecretVersion, error) {
	return _addSecretVersion(gcpService, projectID, secretID, secretValue)
}

// GetSecretVersion retrieves a secret version from the secret manager. Reference: https://cloud.google.com/secret-manager/docs/reference/rest/v1/projects.secrets.versions/access
func _getSecretVersion(gcpService *GcpServices, projectID, secretName, versionID string) (*models.CustomSecretVersion, error) {
	gcpService.Logger.Debug(fmt.Sprintf("Calling GetSecretVersion for project id : %s, version id : %s", projectID, versionID))
	name := fmt.Sprintf("projects/%s/secrets/%s/versions/%s", projectID, secretName, versionID)

	secretVersion, err := gcpService.AdminGCPService.secretManagerService.Projects.Secrets.Versions.Access(name).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("GetSecretVersion failed for secret : %s, err : %s", name, err.Error())
		return nil, googleResourceNotFoundCheck(err)
	}

	gcpService.Logger.Debug(fmt.Sprintf("GetSecretVersion success with response :  %s", name))
	if secretVersion.Payload == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("secret version name is empty"))
	}
	secretValue, err := base64.StdEncoding.DecodeString(secretVersion.Payload.Data)
	if err != nil {
		gcpService.Logger.Errorf("unable to decode key-data with error: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrBase64DecodingError, err)
	}
	customSecretVersion, err := common.ConvertToCustomSecretVersion(secretVersion.Name, string(secretValue))
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
	}
	return customSecretVersion, nil
}
