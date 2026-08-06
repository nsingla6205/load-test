package oci

import (
	"context"
	"time"

	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

// This file holds the public OciServices methods that callers use.
//
// Mutating operations (create/update/delete, PAR) delegate to the active OCI
// wrapper (proxy when the cloud proxy is enabled, otherwise SDK). Read (GET/LIST)
// operations always dispatch to the SDK wrapper directly.

// CreateSecret delegates to the active OCI wrapper.
func (s *OciServices) CreateSecret(compartmentID, vaultID, keyID, secretName, secretValue string) (*OCICustomSecret, error) {
	return s.activeOCIWrapper().CreateSecret(compartmentID, vaultID, keyID, secretName, secretValue)
}

// GetSecretByName always uses the SDK.
func (s *OciServices) GetSecretByName(secretName string, vaultID string) (*OCICustomSecret, error) {
	return (&ociSDKWrapper{svc: s}).GetSecretByName(secretName, vaultID)
}

// GetSecretWithLatestVersion (read) fetches secret metadata entirely via the SDK.
func (s *OciServices) GetSecretWithLatestVersion(secretID string) (*OCICustomSecret, error) {
	return (&ociSDKWrapper{svc: s}).GetSecretWithLatestVersion(secretID)
}

// GetSecretWithCustomVersion fetches secret metadata and the secret-bundle value entirely via the SDK.
func (s *OciServices) GetSecretWithCustomVersion(secretID string, versionNumber int64) (*OCICustomSecret, error) {
	return (&ociSDKWrapper{svc: s}).GetSecretWithCustomVersion(secretID, versionNumber)
}

// DeleteSecret (mutation) delegates to the active OCI wrapper.
func (s *OciServices) DeleteSecret(secretID string) error {
	return s.activeOCIWrapper().DeleteSecret(secretID)
}

// CreateCertificate delegates to the active OCI wrapper.
func (s *OciServices) CreateCertificate(
	compartmentID string,
	issuerCAOCID string,
	certName string,
	commonName string,
	organization string,
	dnsDomains []string,
	isServerAuthEnabled bool,
	validityDays int,
) (*OCICustomCertificate, error) {
	return s.activeOCIWrapper().CreateCertificate(
		compartmentID, issuerCAOCID, certName, commonName, organization, dnsDomains, isServerAuthEnabled, validityDays,
	)
}

// GetCertificate (read) always uses the SDK.
func (s *OciServices) GetCertificate(certOCID string) (*OCICustomCertificate, error) {
	return (&ociSDKWrapper{svc: s}).GetCertificate(certOCID)
}

// GetCertificateByName (read) always uses the SDK.
func (s *OciServices) GetCertificateByName(certName, compartmentOCID string) (*OCICustomCertificate, error) {
	return (&ociSDKWrapper{svc: s}).GetCertificateByName(certName, compartmentOCID)
}

// DeleteCertificate (mutation) delegates to the active OCI wrapper.
func (s *OciServices) DeleteCertificate(certOCID string) error {
	return s.activeOCIWrapper().DeleteCertificate(certOCID)
}

// WaitForCertificateActive (read/poll) always uses the SDK.
func (s *OciServices) WaitForCertificateActive(certOCID string, timeout time.Duration) error {
	return (&ociSDKWrapper{svc: s}).WaitForCertificateActive(certOCID, timeout)
}

// CreateOrUpdateDnsRecord (mutation) delegates to the active OCI wrapper.
func (s *OciServices) CreateOrUpdateDnsRecord(zoneOCID, recordName, ip string) (*hyperscalermodels.CustomCloudDNSRecord, error) {
	return s.activeOCIWrapper().CreateOrUpdateDnsRecord(zoneOCID, recordName, ip)
}

// GetDnsRecord (read) always uses the SDK.
func (s *OciServices) GetDnsRecord(zoneOCID, recordName string) (*hyperscalermodels.CustomCloudDNSRecord, error) {
	return (&ociSDKWrapper{svc: s}).GetDnsRecord(zoneOCID, recordName)
}

// DeleteDnsRecord (mutation) delegates to the active OCI wrapper.
func (s *OciServices) DeleteDnsRecord(zoneOCID, recordName string) error {
	return s.activeOCIWrapper().DeleteDnsRecord(zoneOCID, recordName)
}

// GenerateVSAPAR delegates to the active OCI wrapper.
func (s *OciServices) GenerateVSAPAR(ctx context.Context, vsaImagePath string) (string, error) {
	return s.activeOCIWrapper().GenerateVSAPAR(ctx, vsaImagePath)
}
