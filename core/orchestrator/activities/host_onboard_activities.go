package activities

import (
	"context"
	"fmt"
	"strings"

	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

var HostOnboardGetProviderForHost = vsa.GetProviderForHost

const (
	vcpAdminCertUsernameSuffix = "_admin"
	hostServerCertificateType  = "server"
)

// HostOnboardActivity implements Temporal activities for async host onboarding.
type HostOnboardActivity struct {
	SE database.Storage
}

// DiscoverHost connects to ONTAP, discovers cluster metadata, and validates host aggregates.
func (a *HostOnboardActivity) DiscoverHost(ctx context.Context, result *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
	activity.RecordHeartbeat(ctx, "discover-host")
	logger := util.GetLogger(ctx)
	if result == nil || result.DbHost == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("host onboard result requires db host")))
	}
	logger.Infof("Starting DiscoverHost activity for host %s (ip=%s)", result.HostUUID, result.DbHost.IPAddress)

	host := result.DbHost
	provider, err := vsa.GetProviderForHost(ctx, host)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}
	client, err := provider.CreateRESTClient()
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			customerrors.NewBadRequestErr(fmt.Sprintf("unable to connect to host: %v", err)))
	}

	if err := vsa.DiscoverHostSystemInfo(ctx, host, client); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			customerrors.NewBadRequestErr(fmt.Sprintf("unable to fetch cluster details: %v", err)))
	}
	nodes, err := vsa.ValidateHostAggregates(provider, host, client)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			customerrors.NewBadRequestErr(fmt.Sprintf("unable to validate host nodes: %v", err)))
	}
	if err := a.persistHostNodes(ctx, host, nodes); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			customerrors.NewBadRequestErr(fmt.Sprintf("unable to persist host nodes: %v", err)))
	}

	logger.Infof("Discovered host %s: externalName=%s externalUUID=%s", result.HostUUID, host.ExternalName, host.ExternalUUID)
	result.DbHost = host
	return result, nil
}

// persistHostNodes records ONTAP nodes discovered during host onboarding in the nodes table,
// tagging each row with node_attributes.host_id = hosts.id. PO pool create depends on these
// rows to resolve node IDs for data LIF reservations, so persistence is required.
func (a *HostOnboardActivity) persistHostNodes(ctx context.Context, dbHost *datamodel.Host, nodes []*vsa.Node) error {
	for _, node := range nodes {
		if node == nil || strings.TrimSpace(node.Name) == "" {
			continue
		}
		rec := &datamodel.Node{
			Name:            strings.TrimSpace(node.Name),
			EndpointAddress: dbHost.IPAddress,
			NodeAttributes: &datamodel.NodeDetails{
				ExternalUUID: node.ExternalUUID,
				HostID:       dbHost.ID,
			},
		}
		if _, err := a.SE.EnsureHostNode(ctx, rec); err != nil {
			return fmt.Errorf("node %q: %w", rec.Name, err)
		}
	}
	return nil
}

// GenerateHostCertificate creates admin certificate credentials in CAS/Secret Manager for the external cluster.
// This activity (and the rest of the certificate-based host onboarding steps) only applies when
// the deployment is configured for certificate authentication; username/password deployments skip it.
func (a *HostOnboardActivity) GenerateHostCertificate(ctx context.Context, result *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
	activity.RecordHeartbeat(ctx, "generate-host-certificate")
	logger := util.GetLogger(ctx)
	logger.Infof("Starting GenerateHostCertificate activity for host %s", result.HostUUID)

	if env.HostOnboardAuthType != env.USER_CERTIFICATE {
		logger.Infof("Skipping GenerateHostCertificate for host %s: deployment authType is not USER_CERTIFICATE", result.HostUUID)
		return result, nil
	}
	host := result.DbHost
	externalClusterName := strings.TrimSpace(host.ExternalName)
	hostCredentials := result.HostCredentials
	if hostCredentials == nil {
		userName := utils.GenerateUniqueUsername(externalClusterName)
		hostCredentials = &datamodel.PoolCredentials{
			SecretID:      fmt.Sprintf("%s-secret", externalClusterName),
			CertificateID: fmt.Sprintf("%s-cert", externalClusterName),
			Password:      "",
			AuthType:      env.USER_CERTIFICATE,
			CaURI:         env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName),
			Username:      fmt.Sprintf("%s%s", userName, vcpAdminCertUsernameSuffix),
		}
	}

	cloudService, getCloudServiceErr := resolveHostOnboardCloudService(ctx, result)
	if getCloudServiceErr != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, getCloudServiceErr))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("generating certificate for external cluster %s", externalClusterName))
	certificateResponse, err := vsa.GenerateAndCreateCertificateForVSACluster(
		cloudService,
		externalClusterName,
		hostCredentials.Username,
		hostCredentials,
		EnableServerAuthInCSR,
	)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	result.DbHost = host
	result.HostCredentials = hostCredentials
	result.CertificateResponse = certificateResponse
	logger.Infof("Generated host certificate for host %s (certificateID=%s)", result.HostUUID, hostCredentials.CertificateID)

	return result, nil
}

// CreateHostCloudDNSRecord creates the Cloud DNS A-record for the external cluster host.
func (a *HostOnboardActivity) CreateHostCloudDNSRecord(ctx context.Context, result *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
	activity.RecordHeartbeat(ctx, "create-host-cloud-dns-record")
	logger := util.GetLogger(ctx)
	logger.Infof("Starting CreateHostCloudDNSRecord activity for host %s", result.HostUUID)
	if env.HostOnboardAuthType != env.USER_CERTIFICATE {
		logger.Infof("Skipping CreateHostCloudDNSRecord for host %s: deployment authType is not USER_CERTIFICATE", result.HostUUID)
		return result, nil
	}
	host := result.DbHost
	cloudService, getCloudServiceErr := resolveHostOnboardCloudService(ctx, result)
	if getCloudServiceErr != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, getCloudServiceErr))
	}

	recordName := fmt.Sprintf("%s-%d.%s.%s.", "dns", 1, strings.TrimSpace(host.ExternalName), env.VsaDeployedDnsName)
	activity.RecordHeartbeat(ctx, fmt.Sprintf("creating Cloud DNS record %s for host %s", recordName, result.HostUUID))
	dnsRecord, err := vsa.GetOrCreateCloudDNSRecord(cloudService, recordName, host.IPAddress)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err))
	}
	logger.Infof("Created Cloud DNS record %s for host %s", recordName, result.HostUUID)

	result.CloudDNSRecord = dnsRecord
	return result, nil
}

// InstallHostCACertificate installs client-ca and server-ca trust certificates on the external cluster.
func (a *HostOnboardActivity) InstallHostCACertificate(ctx context.Context, result *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
	activity.RecordHeartbeat(ctx, "install-host-trust-certificates")
	logger := util.GetLogger(ctx)
	logger.Infof("Starting InstallHostCACertificate activity for host %s", result.HostUUID)
	if env.HostOnboardAuthType != env.USER_CERTIFICATE {
		logger.Infof("Skipping InstallHostCACertificate for host %s: deployment authType is not USER_CERTIFICATE", result.HostUUID)
		return result, nil
	}
	host := result.DbHost
	chain := result.CertificateResponse.Certificate.PemCertificateChain
	if len(chain) < 2 || strings.TrimSpace(chain[0]) == "" || strings.TrimSpace(chain[1]) == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s certificate chain entries at index 0 and 1 are required before trust certificate installation", result.HostUUID)))
	}
	cert := result.CertificateResponse.Certificate

	provider, err := HostOnboardGetProviderForHost(ctx, host)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}

	vserverName := result.DbHost.ExternalName
	// Install root CA before issuing CA: ONTAP validates each trust anchor against its
	// issuer, so the subordinate (chain[0]) cannot be installed until root (chain[1]) is trusted.
	for _, chainIndex := range trustChainInstallIndices(len(chain)) {
		chainPEM := chain[chainIndex]
		activity.RecordHeartbeat(ctx, fmt.Sprintf("installing client-ca chain[%d] on host %s svm %s", chainIndex, result.HostUUID, vserverName))
		err = installHostCACertificate(logger, provider, result.HostUUID, hostCACertificateParams{
			SvmName:         vserverName,
			CertificateName: orchcommon.BuildHostClientCACertificateNameForChainIndex(host.ExternalName, chainIndex),
			Certificate:     chainPEM,
			CommonName:      hostTrustCertificateCommonName(chainPEM, cert),
			CertificateType: vsa.TrustCertificateTypeClientCA,
		})
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	for _, chainIndex := range trustChainInstallIndices(len(chain)) {
		chainPEM := chain[chainIndex]
		activity.RecordHeartbeat(ctx, fmt.Sprintf("installing server-ca chain[%d] on host %s svm %s", chainIndex, result.HostUUID, vserverName))
		err = installHostCACertificate(logger, provider, result.HostUUID, hostCACertificateParams{
			SvmName:         vserverName,
			CertificateName: orchcommon.BuildHostServerCACertificateNameForChainIndex(host.ExternalName, chainIndex),
			Certificate:     chainPEM,
			CommonName:      hostTrustCertificateCommonName(chainPEM, cert),
			CertificateType: vsa.TrustCertificateTypeServerCA,
		})
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	logger.Infof("Installed trust certificates for host %s (svm=%s)", result.HostUUID, vserverName)
	result.DbHost = host
	return result, nil
}

// InstallHostServerCertificate installs the generated server certificate on the external
// cluster and configures ONTAP SSL to use it.
func (a *HostOnboardActivity) InstallHostServerCertificate(ctx context.Context, result *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
	activity.RecordHeartbeat(ctx, "install-host-server-certificate")
	logger := util.GetLogger(ctx)
	logger.Infof("Starting InstallHostServerCertificate activity for host %s", result.HostUUID)
	if env.HostOnboardAuthType != env.USER_CERTIFICATE {
		logger.Infof("Skipping GenerateHostCertificate for host %s: deployment authType is not USER_CERTIFICATE", result.HostUUID)
		return result, nil
	}
	host := result.DbHost
	cert := result.CertificateResponse.Certificate
	provider, err := HostOnboardGetProviderForHost(ctx, host)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}

	vserverName := result.DbHost.ExternalName
	certificatePEM := strings.TrimSpace(cert.PemCertificate)
	if certificatePEM == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s leaf certificate PEM is required for server certificate installation", result.HostUUID)))
	}
	privateKeyPEM := ""
	if result.CertificateResponse.Secret != nil && result.CertificateResponse.Secret.SecretVersion != nil {
		privateKeyPEM = strings.TrimSpace(result.CertificateResponse.Secret.SecretVersion.Value)
	}
	if privateKeyPEM == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s private key is required for server certificate installation", result.HostUUID)))
	}
	chain := cert.PemCertificateChain
	if len(chain) == 0 || strings.TrimSpace(chain[0]) == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s certificate chain is required for server certificate installation", result.HostUUID)))
	}

	// Pass PemCertificateChain as intermediate_certificates so ONTAP also creates the server_chain row
	// (same as pool certificate rotation). InstallHostCACertificate must run first.
	intermediatePEMs := hostServerInstallIntermediatePEMs(chain)
	commonName := ""
	if result.HostCredentials != nil {
		commonName = strings.TrimSpace(result.HostCredentials.Username)
	}
	if commonName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s server certificate common name is required", result.HostUUID)))
	}
	certificateName := ""
	if result.HostCredentials != nil {
		certificateName = strings.TrimSpace(result.HostCredentials.CertificateID)
	}
	if certificateName == "" {
		certificateName = strings.TrimSpace(cert.CertificateID)
	}
	if certificateName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s certificate name is required for server certificate installation", result.HostUUID)))
	}

	serialForSSL, serialErr := utils.SerialHexFromPEM(certificatePEM)
	if serialErr != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s installed certificate has unusable serial number for SSL modify: %w", result.HostUUID, serialErr)))
	}

	installParams := vsa.InstallServerCertificateParams{
		SvmName:                  vserverName,
		CertificateName:          certificateName,
		Certificate:              certificatePEM,
		PrivateKey:               privateKeyPEM,
		IntermediateCertificates: intermediatePEMs,
		CertificateType:          hostServerCertificateType,
		CommonName:               commonName,
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("installing server certificate on host %s svm %s", result.HostUUID, vserverName))
	_, err = provider.InstallServerCertificate(installParams)
	if err != nil {
		if !isHostDuplicateCertificateError(err) {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		logger.Infof("Server certificate already installed on host %s (svm=%s); treating duplicate entry as success", result.HostUUID, vserverName)
	} else {
		logger.Infof("Installed server certificate on host %s (svm=%s)", result.HostUUID, vserverName)
	}
	// SSL modify must reference the certificate actually installed above (certificatePEM),
	// not cert.PemCertificate from CAS which may differ when hardcoded test PEMs are used.

	caForSSL := utils.DeriveCANameFromPEM(certificatePEM)
	if caForSSL == "" {
		caForSSL = cert.CaName
	}
	if caForSSL == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("host %s certificate CA name is required for SSL modify", result.HostUUID)))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("configuring SSL on host %s svm %s", result.HostUUID, vserverName))
	sslResponse, sslErr := provider.ModifySSL(vsa.ModifySSLParams{
		SvmName:       vserverName,
		ServerEnabled: true,
		CA:            caForSSL,
		Serial:        serialForSSL,
	})
	if sslErr != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(sslErr)
	}
	if sslResponse != nil && !sslResponse.Success {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("SSL configuration failed for host %s: %s", result.HostUUID, sslResponse.Message))
	}
	return result, nil
}

func resolveHostOnboardCloudService(ctx context.Context, result *orchcommon.HostOnboardResult) (hyperscaler2.Services, error) {
	if result != nil && result.CloudService != nil {
		return result.CloudService, nil
	}
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}
	if result != nil {
		result.CloudService = cloudService
	}
	return cloudService, nil
}

func isHostDuplicateCertificateError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "duplicate entry") || strings.Contains(errMsg, "already exists")
}

// hostServerInstallIntermediatePEMs returns CAS PemCertificateChain for ONTAP server cert install.
// Supplying intermediate_certificates creates the companion server_chain certificate (pool rotation parity).
// Trust anchors from InstallHostCACertificate must already be installed.
func hostServerInstallIntermediatePEMs(chain []string) []string {
	if len(chain) == 0 {
		return nil
	}
	out := make([]string, 0, len(chain))
	for _, pem := range chain {
		if trimmed := strings.TrimSpace(pem); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type hostCACertificateParams struct {
	SvmName         string
	CertificateName string
	Certificate     string
	CommonName      string
	CertificateType string
}

func installHostCACertificate(
	logger log.Logger,
	provider vsa.Provider,
	hostUUID string,
	params hostCACertificateParams,
) error {
	var err error
	switch params.CertificateType {
	case vsa.TrustCertificateTypeClientCA:
		_, err = provider.InstallClientCA(vsa.InstallClientCAParams{
			SvmName:         params.SvmName,
			CertificateName: params.CertificateName,
			Certificate:     params.Certificate,
			CommonName:      params.CommonName,
		})
	case vsa.TrustCertificateTypeServerCA:
		_, err = provider.InstallServerCA(vsa.InstallServerCAParams{
			SvmName:         params.SvmName,
			CertificateName: params.CertificateName,
			Certificate:     params.Certificate,
			CommonName:      params.CommonName,
		})
	default:
		return fmt.Errorf("unsupported trust certificate type %q", params.CertificateType)
	}
	if err != nil {
		if isHostDuplicateCertificateError(err) {
			logger.Infof("%s certificate %q already installed on host %s (svm=%s); treating duplicate entry as success",
				params.CertificateType, params.CertificateName, hostUUID, params.SvmName)
			return nil
		}
		return err
	}
	logger.Infof("Installed %s certificate %q on host %s (svm=%s)",
		params.CertificateType, params.CertificateName, hostUUID, params.SvmName)
	return nil
}

// trustChainInstallIndices returns PemCertificateChain indices root-first so each CA's
// issuer is already trusted when ONTAP validates the install.
func trustChainInstallIndices(chainLen int) []int {
	if chainLen <= 0 {
		return nil
	}
	indices := make([]int, chainLen)
	for i := range indices {
		indices[i] = chainLen - 1 - i
	}
	return indices
}

func hostTrustCertificateCommonName(caPEM string, cert *hyperscaler_models.CustomCertificate) string {
	// Trust anchors are keyed by the CA cert's subject CN (not issuer CN).
	// Example: subordinate CA subject=vsa-pool-ca-subordinate, issuer=vsa-ca.
	// The leaf issuer is vsa-pool-ca-subordinate, so server-ca must register that subject.
	if cn := utils.DeriveSubjectCNFromPEM(caPEM); cn != "" {
		return cn
	}
	if cert != nil {
		if cn := strings.TrimSpace(cert.CaName); cn != "" {
			return cn
		}
		if cn := strings.TrimSpace(cert.IssuerCertificateAuthority); cn != "" {
			return cn
		}
	}
	return ""
}

// OnboardHost marks the host CREATED and persists all accumulated host onboard changes.
func (a *HostOnboardActivity) OnboardHost(ctx context.Context, result *orchcommon.HostOnboardResult) (*orchcommon.HostOnboardResult, error) {
	activity.RecordHeartbeat(ctx, "onboard-host")
	logger := util.GetLogger(ctx)
	if result == nil || result.DbHost == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("host onboard result requires db host")))
	}
	logger.Infof("Starting OnboardHost activity for host %s", result.HostUUID)

	host := result.DbHost
	host.State = datamodel.LifeCycleStateCreated
	host.StateDetails = datamodel.LifeCycleStateCreatedDetails
	updatedHost, err := a.SE.UpdateHost(ctx, host)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Infof("Host %s onboarded successfully: state=%s", result.HostUUID, updatedHost.State)
	result.DbHost = updatedHost
	return result, nil
}

// MarkHostOnboardFailed sets the host lifecycle state to ERROR after onboarding fails.
func (a *HostOnboardActivity) MarkHostOnboardFailed(ctx context.Context, hostUUID, details string) error {
	activity.RecordHeartbeat(ctx, "mark-host-onboard-failed")
	logger := util.GetLogger(ctx)
	logger.Infof("Starting MarkHostOnboardFailed activity for host %s", hostUUID)
	host, err := a.SE.GetHost(ctx, hostUUID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if host.State == datamodel.LifeCycleStateCreated {
		return nil
	}
	host.State = datamodel.LifeCycleStateError
	if details == "" {
		details = datamodel.LifeCycleStateCreationErrorDetails
	}
	host.StateDetails = details
	_, err = a.SE.UpdateHost(ctx, host)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Infof("Marked host %s onboarding as failed: %s", hostUUID, details)
	return nil
}
