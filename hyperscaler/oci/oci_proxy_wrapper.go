package oci

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	ocicommon "github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/dns"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"github.com/oracle/oci-go-sdk/v65/vault"
	ocicloudproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/oci-cloud-proxy-client"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ociProxyWrapper is the OCI cloud proxy REST API implementation of ociWrapper.
// Every operation is issued through the generated ogen client via the adapters
// in oci_proxy_ogen.go; this file maps proxy responses into the same DTOs
// returned by ociSDKWrapper.
type ociProxyWrapper struct {
	svc         *OciServices
	proxyClient *ocicloudproxyclient.Client
}

// Compile-time assertion that ociProxyWrapper satisfies the ociWrapper Strategy.
var _ ociWrapper = (*ociProxyWrapper)(nil)

// newOCIProxyWrapper constructs an ociProxyWrapper. OCI_PROXY_BASE_URL is required
// and is what enables the proxy in the first place. The generated ogen client is
// built once here and reused for all proxy operations (see proxyClient).
func newOCIProxyWrapper(svc *OciServices) (*ociProxyWrapper, error) {
	client, err := newOgenProxyClient()
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err)
	}
	return &ociProxyWrapper{svc: svc, proxyClient: client}, nil
}

// ociProxyResourceNotFoundCheck is the proxy-side analog of ociResourceNotFoundCheck.
// Returns nil when the error is HTTP 404 so callers can treat "not found" as (nil, nil).
func ociProxyResourceNotFoundCheck(statusCode int, err error) error {
	if err == nil && statusCode == http.StatusNotFound {
		return nil
	}
	if isOCIProxyNotFound(err) {
		return nil
	}
	return err
}

func (w *ociProxyWrapper) ctx() context.Context {
	if w.svc != nil && w.svc.Ctx != nil {
		return w.svc.Ctx
	}
	return context.Background()
}

func (w *ociProxyWrapper) CreateSecret(compartmentID, vaultID, keyID, secretName, secretValue string) (*OCICustomSecret, error) {
	ociService := w.svc
	ociService.Logger.Infof("Calling CreateSecret (proxy) for compartment: %s, secretName: %s", compartmentID, secretName)

	if secretValue == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, fmt.Errorf("secret value is empty"))
	}

	encodedContent := base64.StdEncoding.EncodeToString([]byte(secretValue))
	resp, err := ogenCreateSecret(w.ctx(), w.proxyClient, vault.CreateSecretRequest{
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
		ociService.Logger.Errorf("CreateSecret (proxy) failed for compartment: %s, secret: %s, err: %s", compartmentID, secretName, err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, err)
	}

	secretOCID := derefString(resp.Secret.Id)
	return &OCICustomSecret{
		Ocid:    secretOCID,
		Name:    secretName,
		Value:   secretValue,
		Version: 1,
	}, nil
}

func (w *ociProxyWrapper) DeleteSecret(secretID string) error {
	ociService := w.svc
	retentionDays := env.OCISecretDeletionRetentionDays
	ociService.Logger.Infof("Calling DeleteSecret (proxy) for secretID: %s, retentionDays: %d", secretID, retentionDays)

	// Pre-flight existence/lifecycle check goes directly to the OCI SDK.
	secretResp, err := w.svc.AdminOCIService.vaultClient.GetSecret(w.ctx(), vault.GetSecretRequest{
		SecretId: ocicommon.String(secretID),
	})
	if err != nil {
		if ociResourceNotFoundCheck(err) == nil {
			ociService.Logger.Infof("DeleteSecret (proxy): secret %s not found", secretID)
			return nil
		}
		ociService.Logger.Errorf("DeleteSecret (proxy): GetSecret pre-flight failed for secretID: %s, err: %s", secretID, err.Error())
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err)
	}
	if isSecretInDeletionState(secretResp.Secret.LifecycleState) {
		ociService.Logger.Infof("DeleteSecret (proxy): secret %s already in %s state, skipping ScheduleSecretDeletion", secretID, secretResp.Secret.LifecycleState)
		return nil
	}

	deletionTime := time.Now().UTC().AddDate(0, 0, retentionDays)
	err = ogenScheduleSecretDeletion(w.ctx(), w.proxyClient, vault.ScheduleSecretDeletionRequest{
		SecretId: ocicommon.String(secretID),
		ScheduleSecretDeletionDetails: vault.ScheduleSecretDeletionDetails{
			TimeOfDeletion: &ocicommon.SDKTime{Time: deletionTime},
		},
	})
	if err != nil {
		ociService.Logger.Errorf("DeleteSecret (proxy, ScheduleSecretDeletion) failed for secretID: %s, err: %s", secretID, err.Error())
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceDeprovisionError, err)
	}

	ociService.Logger.Infof("DeleteSecret (proxy) success — secretID: %s scheduled for deletion in %d days", secretID, retentionDays)
	return nil
}

func (w *ociProxyWrapper) CreateCertificate(
	compartmentID string,
	issuerCAOCID string,
	certName string,
	commonName string,
	organization string,
	dnsDomains []string,
	isServerAuthEnabled bool,
	validityDays int,
) (*OCICustomCertificate, error) {
	ociService := w.svc
	ociService.Logger.Infof("Calling CreateCertificate (proxy) — compartment: %s, certName: %s", compartmentID, certName)

	if compartmentID == "" || issuerCAOCID == "" || certName == "" || commonName == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError,
			fmt.Errorf("CreateCertificate: compartmentID, issuerCAOCID, certName and commonName are required"))
	}

	profileType := certificatesmanagement.CertificateProfileTypeTlsClient
	if isServerAuthEnabled {
		profileType = certificatesmanagement.CertificateProfileTypeTlsServerOrClient
	}

	sans := make([]certificatesmanagement.CertificateSubjectAlternativeName, 0, len(dnsDomains))
	for _, domain := range dnsDomains {
		if domain == "" {
			continue
		}
		sans = append(sans, certificatesmanagement.CertificateSubjectAlternativeName{
			Type:  certificatesmanagement.CertificateSubjectAlternativeNameTypeDns,
			Value: ocicommon.String(domain),
		})
	}

	if validityDays <= 0 {
		validityDays = env.OCICertificateValidityDays
	}
	notAfter := time.Now().UTC().AddDate(0, 0, validityDays)

	resp, err := ogenCreateCertificate(w.ctx(), w.proxyClient, certificatesmanagement.CreateCertificateRequest{
		CreateCertificateDetails: certificatesmanagement.CreateCertificateDetails{
			Name:          ocicommon.String(certName),
			CompartmentId: ocicommon.String(compartmentID),
			Description:   ocicommon.String("VCP managed internally-managed certificate: " + certName),
			CertificateConfig: certificatesmanagement.CreateCertificateIssuedByInternalCaConfigDetails{
				IssuerCertificateAuthorityId: ocicommon.String(issuerCAOCID),
				CertificateProfileType:       profileType,
				Subject: &certificatesmanagement.CertificateSubject{
					CommonName:   ocicommon.String(commonName),
					Organization: ocicommon.String(organization),
				},
				SubjectAlternativeNames: sans,
				KeyAlgorithm:            certificatesmanagement.KeyAlgorithmRsa4096,
				SignatureAlgorithm:      certificatesmanagement.SignatureAlgorithmSha256WithRsa,
				Validity: &certificatesmanagement.Validity{
					TimeOfValidityNotAfter: &ocicommon.SDKTime{Time: notAfter},
				},
			},
		},
	})
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, err)
	}

	cert := convertOCICertificateMetadata(&resp.Certificate)
	if cert.IssuerCAOCID == "" {
		cert.IssuerCAOCID = issuerCAOCID
	}
	return cert, nil
}

func (w *ociProxyWrapper) DeleteCertificate(certOCID string) error {
	ociService := w.svc
	retentionDays := env.OCICertificateDeletionRetentionDays
	ociService.Logger.Infof("Calling DeleteCertificate (proxy) — certOCID: %s", certOCID)

	if certOCID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceDeprovisionError, fmt.Errorf("DeleteCertificate: certOCID is required"))
	}

	// Pre-flight existence/lifecycle check is a read, so it goes directly to the
	// OCI SDK (reads never use the proxy). Only the schedule-deletion mutation
	// below is routed through the proxy.
	getResp, err := w.svc.AdminOCIService.certManagementClient.GetCertificate(w.ctx(), certificatesmanagement.GetCertificateRequest{
		CertificateId: ocicommon.String(certOCID),
	})
	if err != nil {
		if ociResourceNotFoundCheck(err) == nil {
			return nil
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err)
	}
	if isCertInDeletionState(getResp.Certificate.LifecycleState) {
		return nil
	}

	deletionTime := time.Now().UTC().AddDate(0, 0, retentionDays)
	err = ogenScheduleCertificateDeletion(w.ctx(), w.proxyClient, certificatesmanagement.ScheduleCertificateDeletionRequest{
		CertificateId: ocicommon.String(certOCID),
		ScheduleCertificateDeletionDetails: certificatesmanagement.ScheduleCertificateDeletionDetails{
			TimeOfDeletion: &ocicommon.SDKTime{Time: deletionTime},
		},
	})
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceDeprovisionError, err)
	}
	return nil
}

// UpdateCertificate routes the certificate update mutation through the proxy.
// Backs both RenewCertificateVersionPending and PromoteCertificateVersion.
func (w *ociProxyWrapper) UpdateCertificate(req certificatesmanagement.UpdateCertificateRequest) (certificatesmanagement.UpdateCertificateResponse, error) {
	return ogenUpdateCertificate(w.ctx(), w.proxyClient, req)
}

// ScheduleCertificateVersionDeletion routes the version-deletion mutation through
// the proxy. Backs the rotation sweep of DEPRECATED versions.
func (w *ociProxyWrapper) ScheduleCertificateVersionDeletion(req certificatesmanagement.ScheduleCertificateVersionDeletionRequest) error {
	return ogenScheduleCertificateVersionDeletion(w.ctx(), w.proxyClient, req)
}

func (w *ociProxyWrapper) CreateOrUpdateDnsRecord(zoneOCID, recordName, ip string) (*hyperscalermodels.CustomCloudDNSRecord, error) {
	if err := validateDnsArgs(zoneOCID, recordName); err != nil {
		return nil, err
	}
	if ip == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("CreateOrUpdateDnsRecord: ip is required"))
	}

	ttl := int(env.CloudDNSCacheTTL)
	resp, err := ogenUpdateRRSet(w.ctx(), w.proxyClient, dns.UpdateRRSetRequest{
		ZoneNameOrId: ocicommon.String(zoneOCID),
		Domain:       ocicommon.String(recordName),
		Rtype:        ocicommon.String(dnsRecordTypeA),
		Scope:        dns.UpdateRRSetScopeEnum(env.OCIVsaDnsScope),
		UpdateRrSetDetails: dns.UpdateRrSetDetails{
			Items: []dns.RecordDetails{
				{
					Domain: ocicommon.String(recordName),
					Rdata:  ocicommon.String(ip),
					Rtype:  ocicommon.String(dnsRecordTypeA),
					Ttl:    ocicommon.Int(ttl),
				},
			},
		},
	})
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, err)
	}

	return convertOCIRecordsToCustomRecord(resp.Items, zoneOCID, recordName, ip, ttl), nil
}

func (w *ociProxyWrapper) DeleteDnsRecord(zoneOCID, recordName string) error {
	if err := validateDnsArgs(zoneOCID, recordName); err != nil {
		return err
	}

	err := ogenDeleteRRSet(w.ctx(), w.proxyClient, dns.DeleteRRSetRequest{
		ZoneNameOrId: ocicommon.String(zoneOCID),
		Domain:       ocicommon.String(recordName),
		Rtype:        ocicommon.String(dnsRecordTypeA),
		Scope:        dns.DeleteRRSetScopeEnum(env.OCIVsaDnsScope),
	})
	if err != nil {
		if isOCIProxyNotFound(err) {
			return nil
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceDeprovisionError, err)
	}
	return nil
}

func (w *ociProxyWrapper) GenerateVSAPAR(ctx context.Context, vsaImagePath string) (string, error) {
	logger := util.GetLogger(ctx)

	parsed, err := ParseObjectStoragePath(vsaImagePath)
	if err != nil {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("parse vsaImagePath: %w", err))
	}

	durationHours := env.GetInt("VSA_SIGNED_URL_DURATION_HOURS", 12)
	duration := time.Duration(durationHours) * time.Hour
	if durationHours < 1 || durationHours > 24 {
		duration = 12 * time.Hour
	}

	expiresAt := ocicommon.SDKTime{Time: time.Now().Add(duration)}
	parName := fmt.Sprintf("%s%d", defaultVSAPARNamePrefix, time.Now().UnixNano())

	resp, err := ogenCreatePreauthenticatedRequest(ctx, w.proxyClient, objectstorage.CreatePreauthenticatedRequestRequest{
		NamespaceName: ocicommon.String(parsed.Namespace),
		BucketName:    ocicommon.String(parsed.BucketName),
		CreatePreauthenticatedRequestDetails: objectstorage.CreatePreauthenticatedRequestDetails{
			Name:        ocicommon.String(parName),
			ObjectName:  ocicommon.String(parsed.ObjectName),
			AccessType:  objectstorage.CreatePreauthenticatedRequestDetailsAccessTypeObjectread,
			TimeExpires: &expiresAt,
		},
	})
	if err != nil {
		logger.Error("Failed to create OCI pre-authenticated request (proxy)",
			"namespace", parsed.Namespace, "bucket", parsed.BucketName, "object", parsed.ObjectName, "error", err)
		return "", vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, fmt.Errorf("create pre-authenticated request: %w", err))
	}

	accessURI := derefString(resp.AccessUri)
	if accessURI == "" {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, fmt.Errorf("OCI proxy returned empty AccessUri for PAR"))
	}

	if isAbsoluteAccessURI(accessURI) {
		return accessURI, nil
	}

	host, err := resolvePARObjectStorageHost()
	if err != nil {
		return "", vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError,
			fmt.Errorf("object storage host for PAR URL: %w", err))
	}

	return buildPARURL(host, accessURI), nil
}
