package activities

import (
	"context"
	"fmt"

	vlm "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
)

// VCMCredentialActivity resolves ONTAP credential material for the stateless VCM
// (go-workflows) pool-create path. The VCM request carries ONTAP credentials as
// Secret Manager resource names in the tenant project; this activity dereferences
// them into the plaintext admin password that the VLM child workflows need to
// bootstrap the cluster and SVM.
type VCMCredentialActivity struct {
	// ResolveCloudService returns the cloud service for the current request. It
	// defaults to hyperscaler.ResolveCloudService when nil, so production wiring
	// needs no setup; tests substitute a fake.
	ResolveCloudService func(ctx context.Context) (hyperscaler.Services, error)
}

func (a *VCMCredentialActivity) cloudServiceResolver() func(ctx context.Context) (hyperscaler.Services, error) {
	if a.ResolveCloudService != nil {
		return a.ResolveCloudService
	}
	return hyperscaler.ResolveCloudService
}

// ResolveOntapAdminPasswordRequest carries the ONTAP admin credential's Secret
// Manager resource name (e.g. "projects/{project}/secrets/{secret}"). The secret
// lives in the tenant project; the VCM runtime service account is expected to
// already hold secretAccessor on it (granted by CLH/Google).
type ResolveOntapAdminPasswordRequest struct {
	SecretResourceName string
}

// ResolveOntapAdminPassword reads the latest version of the tenant Secret Manager
// secret referenced by the request and returns it as an *vlm.OntapCredentials.
//
// The password is returned wrapped in vlm.OntapCredentials (rather than as a
// plaintext string) so it is encrypted at rest in the go-workflows history:
// OntapCredentials.MarshalJSON encrypts via ONTAP_CREDENTIAL_ENCRYPT_KEY when
// that key is set on the process. This mirrors the VCP credential activities,
// which also hand back *vlm.OntapCredentials. The resolved value is never logged.
func (a *VCMCredentialActivity) ResolveOntapAdminPassword(ctx context.Context, req ResolveOntapAdminPasswordRequest) (*vlm.OntapCredentials, error) {
	logger := util.GetLogger(ctx)

	projectID, secretID, err := utils.ParseSecretResourceName(req.SecretResourceName)
	if err != nil {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err), true)
	}

	// Log the identifiers, never the resolved value.
	logger.Info("resolving ONTAP admin password from Secret Manager",
		"project", projectID, "secret", secretID)

	cloudService, err := a.cloudServiceResolver()(ctx)
	if err != nil {
		return nil, apperror.WrapAsAppError(err, false)
	}

	secret, err := cloudService.GetSecretWithLatestVersion(projectID, secretID)
	if err != nil {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err), false)
	}
	if secret == nil || secret.SecretVersion == nil || secret.SecretVersion.Value == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
				fmt.Errorf("secret %q in project %q has no accessible latest version", secretID, projectID)), true)
	}
	if err := utils.ValidateStrongPassword(secret.SecretVersion.Value); err != nil {
		return nil, apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("ONTAP admin password in secret %q is invalid: %w", secretID, err)), true)
	}

	return &vlm.OntapCredentials{AdminPassword: secret.SecretVersion.Value}, nil
}

// ResolveOntapCertificateRequest carries the cert-auth credential identifiers
// for a pool. The material itself is never in the request: PoolCredentials holds
// only the resource paths of the certificate (CertificateID, a full CAS
// certificate resource name), its private key (CertPrivateKeySecretID, a Secret
// Manager resource name), and the built-in admin password (SecretID, a Secret
// Manager resource name). The VCM runtime service account is expected to already
// hold access to all three (granted by CLH/Google).
type ResolveOntapCertificateRequest struct {
	PoolCredentials *datamodel.PoolCredentials
}

// ResolveOntapCertificate dereferences the cert-auth credential identifiers into
// the ONTAP certificate material (signed cert, private key, intermediate chain,
// common name) plus the bootstrap admin password, returned as a
// *vlm.OntapCredentials.
//
// Why both cert and password: VLM's CreateVSAClustersWorkflow runs
// CheckAndEnableCertAuthWorkflow whenever OntapCredentials.Certificate.Certificate
// is non-empty. That flow installs the server cert, installs the issuing CA as
// client-ca (so ONTAP trusts VCM's client certificate), and creates the
// cert-mapped ONTAP user. It authenticates to ONTAP with the admin password
// during that bootstrap, before cert auth is live — so the password must travel
// alongside the certificate.
//
// The material is dereferenced through the hyperscaler.Services interface (via
// core/vsa), the same abstraction ResolveOntapAdminPassword already uses, so no
// cloud-specific SDK is imported here. It mirrors the GCP fetchOnTapCredentials
// cert path. Cert + password are wrapped in vlm.OntapCredentials so both are
// encrypted at rest in the go-workflows history (OntapCredentials.MarshalJSON);
// resolved values are never logged.
func (a *VCMCredentialActivity) ResolveOntapCertificate(ctx context.Context, req ResolveOntapCertificateRequest) (*vlm.OntapCredentials, error) {
	logger := util.GetLogger(ctx)

	pc := req.PoolCredentials
	if pc == nil || pc.CertificateID == "" || pc.CertPrivateKeySecretID == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("resolve ONTAP certificate: request is missing certificate or private-key secret reference")), true)
	}

	// Log the identifiers, never the resolved material. CLH provisions the
	// certificate in CAS and its private key in Secret Manager; VCM loads the
	// cert from CAS and the key from Secret Manager (it does not mint them like
	// VCP).
	logger.Info("resolving ONTAP certificate material",
		"certId", pc.CertificateID, "certPrivateKeySecret", pc.CertPrivateKeySecretID)

	cert, err := vsa.GetProvidedCertificate(ctx, pc.CertificateID, pc.CertPrivateKeySecretID)
	if err != nil {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err), false)
	}
	if cert == nil || cert.SignedCertificate == "" || cert.PrivateKey == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
				fmt.Errorf("resolved certificate material is incomplete for certId %q", pc.CertificateID)), true)
	}

	creds := &vlm.OntapCredentials{}
	creds.Certificate.CommonName = cert.CommonName
	creds.Certificate.Certificate = cert.SignedCertificate
	creds.Certificate.PrivateKey = cert.PrivateKey
	creds.Certificate.InterMediateCertificate = cert.InterMediateCertificates

	// The ONTAP-side cert bootstrap authenticates with the built-in admin
	// password (adminPassSecretId), so resolve it too when present.
	if pc.SecretID != "" {
		password, err := a.resolveSecretValue(ctx, pc.SecretID)
		if err != nil {
			return nil, err
		}
		if err := utils.ValidateStrongPassword(password); err != nil {
			return nil, apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("ONTAP admin password in secret %q is invalid: %w", pc.SecretID, err)), true)
		}
		creds.AdminPassword = password
	}

	return creds, nil
}

// resolveSecretValue reads the latest version of a tenant Secret Manager secret
// addressed by its full resource name (projects/{project}/secrets/{secret}).
func (a *VCMCredentialActivity) resolveSecretValue(ctx context.Context, resourceName string) (string, error) {
	projectID, secretID, err := utils.ParseSecretResourceName(resourceName)
	if err != nil {
		return "", apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err), true)
	}
	cloudService, err := a.cloudServiceResolver()(ctx)
	if err != nil {
		return "", apperror.WrapAsAppError(err, false)
	}
	secret, err := cloudService.GetSecretWithLatestVersion(projectID, secretID)
	if err != nil {
		return "", apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err), false)
	}
	if secret == nil || secret.SecretVersion == nil || secret.SecretVersion.Value == "" {
		return "", apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
				fmt.Errorf("secret %q in project %q has no accessible latest version", secretID, projectID)), true)
	}
	return secret.SecretVersion.Value, nil
}
