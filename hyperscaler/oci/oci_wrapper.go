package oci

import (
	"context"

	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

// ociWrapper is the Strategy interface for the OCI mutating operations that can
// be fronted by the cloud proxy. The concrete wrapper is swapped at runtime:
//
//   - ociSDKWrapper   — calls OCI directly via the OCI Go SDK (default, existing behavior)
//   - ociProxyWrapper — routes mutations through the OCI cloud proxy REST API
//
// Selection is driven by whether OCI_PROXY_BASE_URL is set (see provider.go InitializeClients).
type ociWrapper interface {
	// Secrets / Vault (mutations).
	CreateSecret(compartmentID, vaultID, keyID, secretName, secretValue string) (*OCICustomSecret, error)
	DeleteSecret(secretID string) error

	// Certificates (mutations).
	CreateCertificate(
		compartmentID string,
		issuerCAOCID string,
		certName string,
		commonName string,
		organization string,
		dnsDomains []string,
		isServerAuthEnabled bool,
		validityDays int,
	) (*OCICustomCertificate, error)
	DeleteCertificate(certOCID string) error

	// Certificate versions / rotation (mutations). These take OCI SDK request
	// types so the complex rotation business logic (version naming, promote
	// guards, 404/409 idempotency) stays in one place in the OciServices methods;
	// only the outbound transport call is abstracted here.
	//
	//   - UpdateCertificate backs both RenewCertificateVersionPending (Stage=PENDING)
	//     and PromoteCertificateVersion (CurrentVersionNumber cutover).
	//   - ScheduleCertificateVersionDeletion backs the DEPRECATED-version sweep.
	UpdateCertificate(req certificatesmanagement.UpdateCertificateRequest) (certificatesmanagement.UpdateCertificateResponse, error)
	ScheduleCertificateVersionDeletion(req certificatesmanagement.ScheduleCertificateVersionDeletionRequest) error

	// DNS (mutations).
	CreateOrUpdateDnsRecord(zoneOCID, recordName, ip string) (*hyperscalermodels.CustomCloudDNSRecord, error)
	DeleteDnsRecord(zoneOCID, recordName string) error

	// Object Storage.
	GenerateVSAPAR(ctx context.Context, vsaImagePath string) (string, error)
}
