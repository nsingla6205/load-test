package activities

import (
	"context"
	"fmt"
	"strings"

	vlm "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
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

	projectID, secretID, err := parseSecretResourceName(req.SecretResourceName)
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

	return &vlm.OntapCredentials{AdminPassword: secret.SecretVersion.Value}, nil
}

// ResolveOntapCertificateRequest carries the cert-auth credential identifiers
// for a pool. The material itself is never in the request: PoolCredentials holds
// only the certificate id, the CA URI, and the admin-password secret id. The
// certificate lives in the tenant project's CA Service and its private key in
// Secret Manager; the admin password lives in Secret Manager. The VCM runtime
// service account is expected to already hold access (granted by CLH/Google).
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

	if req.PoolCredentials == nil || req.PoolCredentials.CertificateID == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("resolve ONTAP certificate: request has no certificate id")), true)
	}

	// Log the identifiers, never the resolved material.
	logger.Info("resolving ONTAP certificate material",
		"certificateId", req.PoolCredentials.CertificateID, "caUri", req.PoolCredentials.CaURI)

	cert, err := vsa.GetCertificateFromCacheOrSecretManager(ctx, req.PoolCredentials)
	if err != nil {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err), false)
	}
	if cert == nil || cert.SignedCertificate == "" || cert.PrivateKey == "" {
		return nil, apperror.WrapAsAppError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
				fmt.Errorf("resolved certificate material is incomplete for certificate id %q", req.PoolCredentials.CertificateID)), true)
	}

	creds := &vlm.OntapCredentials{}
	creds.Certificate.CommonName = cert.CommonName
	creds.Certificate.Certificate = cert.SignedCertificate
	creds.Certificate.PrivateKey = cert.PrivateKey
	creds.Certificate.InterMediateCertificate = cert.InterMediateCertificates

	// The ONTAP-side cert bootstrap authenticates with the admin password, so
	// resolve it too when an admin-password secret is present on the credential.
	if req.PoolCredentials.SecretID != "" {
		password, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, req.PoolCredentials.SecretID)
		if err != nil {
			return nil, apperror.WrapAsAppError(
				vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err), false)
		}
		creds.AdminPassword = password
	}

	return creds, nil
}

// parseSecretResourceName extracts the project and secret identifiers from a
// Secret Manager resource name. It accepts both the bare secret form and the
// versioned form:
//
//	projects/{project}/secrets/{secret}
//	projects/{project}/secrets/{secret}/versions/{version}
//
// The version segment is ignored; the latest version is always used.
func parseSecretResourceName(name string) (projectID, secretID string, err error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(name), "/"), "/")
	if len(parts) < 4 || parts[0] != "projects" || parts[2] != "secrets" || parts[1] == "" || parts[3] == "" {
		return "", "", fmt.Errorf("invalid secret resource name %q; expected projects/{project}/secrets/{secret}", name)
	}
	return parts[1], parts[3], nil
}
