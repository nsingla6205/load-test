package google

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	certutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/certificate"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/privateca/v1"
)

const (
	LatestVersion                            = "latest"
	PrivilegeWithdrawn                       = "PRIVILEGE_WITHDRAWN"
	defaultCertificateLifetime               = "94608000s"
	defaultMinimumIssuingCARemainingLifetime = "2160h" // 90 days / 3 months
	// defaultIssuedCertificateLifetimeSlack is Go duration syntax (for example 1m, 5s).
	defaultIssuedCertificateLifetimeSlack = "1m"

	// errMsgMaxUnexpiredRevokedCerts is the GCP CAS error message returned when the CA has
	// reached its quota of unexpired revoked certificates. Revocation is best-effort during
	// pool deletion, so this error is treated as non-fatal: the certificate will expire
	// naturally and the pool deletion should continue.
	errMsgMaxUnexpiredRevokedCerts = "Maximum number of unexpired revoked certificates per CA reached"
)

// issuingCACertificateExpireTime returns the not-after time of the issuing CA
// certificate. CaCertificateDescriptions is ordered self-to-root, so index 0 is
// the selected CA. Google's expireTime field is the DELETED-state purge time
// and is empty for ENABLED CAs.
func issuingCACertificateExpireTime(ca *privateca.CertificateAuthority, resourceName string) (time.Time, error) {
	if ca == nil {
		return time.Time{}, fmt.Errorf("certificate authority is nil")
	}
	if len(ca.CaCertificateDescriptions) == 0 ||
		ca.CaCertificateDescriptions[0] == nil ||
		ca.CaCertificateDescriptions[0].SubjectDescription == nil {
		return time.Time{}, fmt.Errorf("certificate authority %s has no CA certificate notAfterTime", resourceName)
	}
	notAfter := strings.TrimSpace(ca.CaCertificateDescriptions[0].SubjectDescription.NotAfterTime)
	if notAfter == "" {
		return time.Time{}, fmt.Errorf("certificate authority %s has no CA certificate notAfterTime", resourceName)
	}
	expireTime, err := time.Parse(time.RFC3339Nano, notAfter)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse certificate authority notAfterTime %q: %w", notAfter, err)
	}
	return expireTime, nil
}

func validateIssuingCAForLeafCreation(
	ca *models.CertificateAuthority,
	now time.Time,
	minimumRemaining time.Duration,
) error {
	if ca == nil {
		return errors.New("certificate authority metadata is nil")
	}
	if ca.State != "ENABLED" {
		return fmt.Errorf("certificate authority state is %q, expected ENABLED", ca.State)
	}
	if !ca.ExpireTime.After(now) {
		return fmt.Errorf("certificate authority expired at %s", ca.ExpireTime.UTC().Format(time.RFC3339))
	}
	if remaining := ca.ExpireTime.Sub(now); remaining < minimumRemaining {
		return fmt.Errorf(
			"certificate authority remaining lifetime %s is below required floor %s",
			remaining,
			minimumRemaining,
		)
	}
	return nil
}

// GetCertificateAuthority retrieves GCP Private CA metadata and its PEM chain.
func (gcpService *GcpServices) GetCertificateAuthority(
	ctx context.Context,
	projectID, region, poolName, caName string,
) (*models.CertificateAuthority, error) {
	if gcpService == nil {
		return nil, fmt.Errorf("GCP service is nil")
	}
	if gcpService.AdminGCPService == nil || gcpService.AdminGCPService.privateCaService == nil {
		if err := gcpService.InitializeClients(); err != nil {
			return nil, err
		}
	}
	resourceName := fmt.Sprintf(
		"projects/%s/locations/%s/caPools/%s/certificateAuthorities/%s",
		projectID,
		region,
		poolName,
		caName,
	)
	ca, err := gcpService.AdminGCPService.privateCaService.Projects.Locations.CaPools.CertificateAuthorities.
		Get(resourceName).Context(ctx).Do()
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	if ca == nil {
		return nil, vsaerrors.NewVCPError(
			vsaerrors.ErrGCPResourceFetchError,
			fmt.Errorf("certificate authority %s returned an empty response", resourceName),
		)
	}
	// Google Private CA expireTime is only set when the CA is DELETED (purge
	// time). Leaf-issuance lifetime must come from the CA certificate itself.
	expireTime, err := issuingCACertificateExpireTime(ca, resourceName)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
	}
	if len(ca.PemCaCertificates) == 0 {
		return nil, vsaerrors.NewVCPError(
			vsaerrors.ErrModelConversionError,
			fmt.Errorf("certificate authority %s has no PEM certificates", resourceName),
		)
	}
	return &models.CertificateAuthority{
		Name:                ca.Name,
		State:               ca.State,
		ExpireTime:          expireTime,
		PemCertificateChain: append([]string(nil), ca.PemCaCertificates...),
	}, nil
}

// CreateCertificate creates a new certificate in the specified CA. Reference: https://cloud.google.com/certificate-authority-service/docs/reference/rest/v1/projects.locations.caPools.certificates/create
func (gcpService *GcpServices) CreateCertificate(cert *models.CustomCertificate) (*models.CustomCertificate, error) {
	if cert == nil {
		return nil, vsaerrors.NewVCPError(
			vsaerrors.ErrGCPResourceProvisionError,
			errors.New("certificate request is nil"),
		)
	}
	leafLifetime := env.GetString("CERTIFICATE_LIFETIME", defaultCertificateLifetime)
	configuredFloor := env.GetString(
		"MINIMUM_ISSUING_CA_REMAINING_LIFETIME",
		defaultMinimumIssuingCARemainingLifetime,
	)
	floor, err := certutils.IssuingCALifetimeFloor(configuredFloor, leafLifetime)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	lifetimeSlack, err := parseIssuedCertificateLifetimeSlack(env.GetString(
		"ISSUED_CERTIFICATE_LIFETIME_SLACK",
		defaultIssuedCertificateLifetimeSlack,
	))
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}

	gcpService.Logger.Debug(fmt.Sprintf("Calling CreateCertificate for project name : %s, region : %s, pool : %s, certificate id : %s", cert.CertOwningEntity, cert.Region, cert.CaGroupName, cert.CertificateID))

	caResourceName := fmt.Sprintf("projects/%s/locations/%s/caPools/%s/certificateAuthorities/%s", cert.CertOwningEntity, cert.Region, cert.CaGroupName, cert.CaName)
	parent := fmt.Sprintf("projects/%s/locations/%s/caPools/%s", cert.CertOwningEntity, cert.Region, cert.CaGroupName)
	ca, err := gcpService.GetCertificateAuthority(
		gcpService.Ctx,
		cert.CertOwningEntity,
		cert.Region,
		cert.CaGroupName,
		cert.CaName,
	)
	if err != nil {
		return nil, err
	}
	if err := validateIssuingCAForLeafCreation(ca, time.Now(), floor); err != nil {
		gcpService.Logger.Errorf("Issuing CA validation failed before leaf creation: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}

	certificate := &privateca.Certificate{
		PemCsr:                     cert.PemCsr,
		Lifetime:                   leafLifetime,
		IssuerCertificateAuthority: caResourceName,
		CreateTime:                 time.Now().UTC().Format(time.RFC3339),
	}

	certificate, err = gcpService.AdminGCPService.privateCaService.Projects.Locations.CaPools.Certificates.Create(parent, certificate).CertificateId(cert.CertificateID).Context(gcpService.Ctx).Do()
	if err != nil {
		gcpService.Logger.Errorf("Failed to create certificate: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}

	if err := validateIssuedCertificateLifetime(leafLifetime, certificate, lifetimeSlack); err != nil {
		gcpService.Logger.Errorf("Issued certificate lifetime does not match the request: %v", err)
		if _, revokeErr := gcpService.RevokeCertificate(cert); revokeErr != nil {
			gcpService.Logger.Errorf(
				"Failed to revoke truncated issued certificate %s: %v",
				cert.CertificateID,
				revokeErr,
			)
			err = fmt.Errorf("%w; also failed to revoke issued certificate: %w", err, revokeErr)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err)
	}
	customCertificate, err := ValidateAndConvertPrivateCACertificateToCustomCertificate(cert.CertificateID, certificate)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrModelConversionError, err)
	}
	return customCertificate, nil
}

func issuedCertificateValidityWindow(issued *privateca.Certificate) (notAfter time.Time, notBefore time.Time, err error) {
	if issued == nil {
		return time.Time{}, time.Time{}, errors.New("issued certificate is nil")
	}
	if issued.CertificateDescription == nil || issued.CertificateDescription.SubjectDescription == nil {
		return time.Time{}, time.Time{}, errors.New("issued certificate has no subject validity window")
	}
	subject := issued.CertificateDescription.SubjectDescription
	notAfterRaw := strings.TrimSpace(subject.NotAfterTime)
	if notAfterRaw == "" {
		return time.Time{}, time.Time{}, errors.New("issued certificate has no notAfterTime")
	}
	notAfter, err = time.Parse(time.RFC3339Nano, notAfterRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse issued certificate notAfterTime %q: %w", notAfterRaw, err)
	}
	notBeforeRaw := strings.TrimSpace(subject.NotBeforeTime)
	if notBeforeRaw == "" {
		notBeforeRaw = strings.TrimSpace(issued.CreateTime)
	}
	if notBeforeRaw == "" {
		return time.Time{}, time.Time{}, errors.New("issued certificate has no notBeforeTime")
	}
	notBefore, err = time.Parse(time.RFC3339Nano, notBeforeRaw)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse issued certificate notBeforeTime %q: %w", notBeforeRaw, err)
	}
	if !notAfter.After(notBefore) {
		return time.Time{}, time.Time{}, fmt.Errorf(
			"issued certificate notAfter %s is not after notBefore %s",
			notAfter.UTC().Format(time.RFC3339),
			notBefore.UTC().Format(time.RFC3339),
		)
	}
	return notAfter, notBefore, nil
}

func parseIssuedCertificateLifetimeSlack(configured string) (time.Duration, error) {
	slack, err := time.ParseDuration(configured)
	if err != nil {
		return 0, fmt.Errorf("invalid ISSUED_CERTIFICATE_LIFETIME_SLACK %q: %w", configured, err)
	}
	if slack < 0 {
		return 0, fmt.Errorf("ISSUED_CERTIFICATE_LIFETIME_SLACK %q must not be negative", configured)
	}
	return slack, nil
}

func validateIssuedCertificateLifetime(requestedLifetime string, issued *privateca.Certificate, slack time.Duration) error {
	requested, err := time.ParseDuration(requestedLifetime)
	if err != nil {
		return fmt.Errorf("invalid requested CERTIFICATE_LIFETIME %q: %w", requestedLifetime, err)
	}
	if requested <= 0 {
		return errors.New("requested certificate lifetime must be positive")
	}
	if slack < 0 {
		return errors.New("issued certificate lifetime slack must not be negative")
	}
	notAfter, notBefore, err := issuedCertificateValidityWindow(issued)
	if err != nil {
		return err
	}
	actual := notAfter.Sub(notBefore)
	if actual+slack < requested {
		return fmt.Errorf(
			"issued certificate expires at %s after %s, shorter than requested lifetime %s; CAS truncates leaf lifetime to the issuing CA remaining validity",
			notAfter.UTC().Format(time.RFC3339),
			actual.Round(time.Second),
			requested,
		)
	}
	return nil
}

// RevokeCertificate revokes a certificate in the specified CA. Reference: https://cloud.google.com/certificate-authority-service/docs/reference/rest/v1/projects.locations.caPools.certificates/revoke
func (gcpService *GcpServices) RevokeCertificate(cert *models.CustomCertificate) (string, error) {
	gcpService.Logger.Debug(fmt.Sprintf("Calling RevokeCertificate for project name : %s, region : %s, pool : %s, certificate id : %s", cert.CertOwningEntity, cert.Region, cert.CaGroupName, cert.CertificateID))

	resourceName := fmt.Sprintf("projects/%s/locations/%s/caPools/%s/certificates/%s", cert.CertOwningEntity, cert.Region, cert.CaGroupName, cert.CertificateID)
	revokeCertificateRequest := &privateca.RevokeCertificateRequest{
		Reason: PrivilegeWithdrawn,
	}

	_, err := gcpService.AdminGCPService.privateCaService.Projects.Locations.CaPools.Certificates.Revoke(resourceName, revokeCertificateRequest).Context(gcpService.Ctx).Do()
	if err != nil {
		var gErr *googleapi.Error
		if errors.As(err, &gErr) && gErr.Code == 400 && strings.Contains(gErr.Message, errMsgMaxUnexpiredRevokedCerts) {
			gcpService.Logger.Warnf("CA revocation quota reached for certificate %s; certificate will expire naturally, continuing pool deletion: %v", cert.CertificateID, err)
			return resourceName, nil
		}
		gcpService.Logger.Errorf("Failed to revoke certificate: %v", err)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceDeprovisionError, err)
	}

	return resourceName, nil
}

// GetCertificate retrieves a certificate in the specified CA. Reference: https://cloud.google.com/certificate-authority-service/docs/reference/rest/v1/projects.locations.caPools.certificates/get
func (gcpService *GcpServices) GetCertificate(projectID, region, poolName, certificateID string) (*models.CustomCertificate, error) {
	gcpService.Logger.Debug(fmt.Sprintf("Calling GetCertificate for project name : %s, region : %s, pool : %s, certificate id : %s", projectID, region, poolName, certificateID))

	certificateName := fmt.Sprintf("projects/%s/locations/%s/caPools/%s/certificates/%s", projectID, region, poolName, certificateID)
	certificate, err := gcpService.AdminGCPService.privateCaService.Projects.Locations.CaPools.Certificates.Get(certificateName).Context(gcpService.Ctx).Do()

	if err != nil {
		gcpService.Logger.Errorf("GetCertificate failed for certificate : %s, err: %s", certificateName, err.Error())
		return nil, googleResourceNotFoundCheck(err)
	}

	// Check if the certificate has revocation details implies certificate is revoked
	if certificate.RevocationDetails != nil && certificate.RevocationDetails.RevocationState == PrivilegeWithdrawn {
		gcpService.Logger.Debug(fmt.Sprintf("Certificate :%s is in revoked state already", certificateName))
		return nil, fmt.Errorf("certificate %s is revoked and cannot be used", certificateID)
	}

	gcpService.Logger.Debug(fmt.Sprintf("GetCertificate success with response :  %s", certificateID))
	customCertificate, err := ValidateAndConvertPrivateCACertificateToCustomCertificate(certificateID, certificate)
	if err != nil {
		return nil, err
	}
	return customCertificate, nil
}

func googleResourceNotFoundCheck(err error) error {
	var gErr *googleapi.Error
	if errors.As(err, &gErr) && gErr.Code == 404 {
		return nil
	}
	return err
}
