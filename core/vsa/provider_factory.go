package vsa

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	common2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/common"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// GetProviderByNode creates a VSA provider using CA fields from Node struct (with env var fallback)
var GetProviderByNode = _getProviderByNode

func _getProviderByNode(ctx context.Context, node *models.Node) (Provider, error) {
	// Validate that we have a valid node
	if node == nil {
		util.GetLogger(ctx).Errorf("Node is nil")
		return nil, errors.NewVCPError(errors.ErrResourceNotFound, fmt.Errorf("node is nil"))
	}

	if node.AuthType == env.USER_CERTIFICATE {
		// Validate that we have endpoint addresses for certificate auth
		if len(node.EndpointAddressesToHostNameMap) == 0 {
			util.GetLogger(ctx).Errorf("No endpoint addresses found in EndpointAddressesToHostNameMap for node %s", node.Name)
			return nil, errors.NewVCPError(errors.ErrVSAClusterNodeIPAddressNotFound, fmt.Errorf("VSA cluster node IP address not found"))
		}

		// Get the first IP address from the endpoint map for SSH connections
		var ipAddress string
		for endpointAddr := range node.EndpointAddressesToHostNameMap {
			ipAddress = endpointAddr
			break // Use the first available IP address
		}

		var password string
		var certificate *models.Certificate
		var err error

		switch env.GetHyperscaler() {
		case common.ProviderOCI:
			util.GetLogger(ctx).Infof("Using OCI Certificates Service and OCI Vault for cert-auth on node %s", node.Name)

			if node.ExternalCertificate == nil ||
				node.ExternalCertificate.Name == "" ||
				node.ExternalCertificate.ExternalIdentifier == "" {
				util.GetLogger(ctx).Errorf("OCI cert auth: node %s is missing ExternalCertificate handle", node.Name)
				return nil, errors.NewVCPError(errors.ErrInputValidationError,
					errors2.New("OCI cert auth requires node.ExternalCertificate.Name and ExternalIdentifier"))
			}

			certificate, err = GetCertificateFromCacheOrCAS(ctx, node.ExternalCertificate)
			if err != nil {
				util.GetLogger(ctx).Errorf("Failed to get OCI certificate for node %s: %v", node.Name, err)
				return nil, errors.NewVCPError(errors.ErrOCIResourceFetchError, err)
			}

			password = node.Password
			if password == "" && node.ExternalSecret != nil && node.ExternalSecret.Name != "" {
				secret, sshErr := GetPasswordFromCacheOrOCIVault(ctx, node.ExternalSecret)
				if sshErr != nil {
					util.GetLogger(ctx).Debugf("OCI cert auth: SSH password fallback unavailable for node %s: %v", node.Name, sshErr)
				} else if secret != "" {
					password = secret
					util.GetLogger(ctx).Debugf("Retrieved password from Vault for SSH authentication")
				} else {
					util.GetLogger(ctx).Warnf("Password retrieved from Vault for node %s is empty", node.Name)
				}
			}

		case common.ProviderGCP:
			poolCredentials := &datamodel.PoolCredentials{
				CaURI:         node.GetCaURIWithFallback(),
				CertificateID: node.CertificateID,
			}

			certificate, err = GetCertificateFromCacheOrSecretManager(ctx, poolCredentials)
			if err != nil {
				util.GetLogger(ctx).Errorf("Failed to get certificate for node %s: %v", node.Name, err)
				return nil, errors.NewVCPError(errors.ErrGCPResourceFetchError, err)
			}

			password = node.Password
			if password == "" && node.SecretID != "" {
				secret, GetPasswordFromCacheOrSecretManagerErr := GetPasswordFromCacheOrSecretManager(ctx, node.SecretID)
				if GetPasswordFromCacheOrSecretManagerErr != nil {
					util.GetLogger(ctx).Debugf("Failed to get password from Secret Manager for SSH: %v", GetPasswordFromCacheOrSecretManagerErr)
				} else if secret != "" {
					password = secret
					util.GetLogger(ctx).Debugf("Retrieved password from Secret Manager for SSH authentication")
				} else {
					util.GetLogger(ctx).Warnf("Password retrieved from Secret Manager for node %s is empty", node.Name)
				}
			}

		default:
			return nil, errors.NewVCPError(errors.ErrInputValidationError, fmt.Errorf("invalid hyperscaler: %s", env.GetHyperscaler()))
		}

		if password == "" {
			util.GetLogger(ctx).Debugf("No password available for SSH authentication with certificate-based auth")
			// Continue without password - SSH will fail but REST API will work
		}
		if certificate == nil {
			return nil, errors.NewVCPError(errors.ErrResourceNotFound, errors2.New("certificate is nil after provider lookup"))
		}

		// OCI SNI override: derive the synthetic SNI from cluster identity so
		// the REST client can connect by IP and still pass hostname
		// verification against the wildcard SAN baked into the cert. Mirrors
		// the write-path synthesis in _saveNodeDetails. GCP and password-auth
		// paths leave serverName empty and behave exactly as before.
		var serverName string
		if env.GetHyperscaler() == common.ProviderOCI &&
			env.OCIUseTLSSNIOverride &&
			node.DeploymentName != "" &&
			env.VsaDeployedDnsName != "" {
			serverName = fmt.Sprintf("mgmt.%s.%s", node.DeploymentName, env.VsaDeployedDnsName)
		}

		return NewProvider(ctx, ProviderDetails{
			IPAddress: ipAddress,
			Hosts:     node.EndpointAddressesToHostNameMap,
			Password:  password, // Set password for SSH connections
			Certificate: &Certificate{
				SignedCertificate:        certificate.SignedCertificate,
				InterMediateCertificates: certificate.InterMediateCertificates,
				CommonName:               certificate.CommonName,
				PrivateKey:               certificate.PrivateKey,
			},
			InsecureSkipVerify: false,
			AuthType:           node.AuthType, // Set authentication type
			ServerName:         serverName,
		}), nil
	}

	var password string
	if node.AuthType == env.USERNAME_PWD_SEC_MGR {
		switch env.GetHyperscaler() {
		case common.ProviderOCI:
			util.GetLogger(ctx).Infof("Using OCI Vault for credentials for node %s", node.Name)
			secret, err := GetPasswordFromCacheOrOCIVault(ctx, node.ExternalSecret)
			if err != nil {
				util.GetLogger(ctx).Errorf("Failed to get password from OCI Vault for node %s: %v", node.Name, err)
				return nil, errors.NewVCPError(errors.ErrOCIResourceFetchError, err)
			}
			password = secret
		default:
			secret, err := GetPasswordFromCacheOrSecretManager(ctx, node.SecretID)
			if err != nil {
				util.GetLogger(ctx).Errorf("Failed to get password for node %s: %v", node.Name, err)
				return nil, errors.NewVCPError(errors.ErrGCPResourceFetchError, err)
			}
			password = secret
		}
	} else {
		password = node.Password
	}
	// if ipAddress in empty, populate it with the node's endpoint address
	if len(node.EndpointAddressesToHostNameMap) == 0 {
		if node.EndpointAddress == "" {
			return nil, errors.NewVCPError(errors.ErrVSAClusterNodeIPAddressNotFound, errors2.New("node endpoint address is empty"))
		}
		node.EndpointAddressesToHostNameMap[node.EndpointAddress] = node.EndpointAddress
	}

	// Get the first IP address from the endpoint map for SSH connections
	var ipAddress string
	for endpointAddr := range node.EndpointAddressesToHostNameMap {
		ipAddress = endpointAddr
		break // Use the first available IP address
	}

	return NewProvider(ctx, ProviderDetails{
		IPAddress:          ipAddress,
		Hosts:              node.EndpointAddressesToHostNameMap,
		Password:           password,
		InsecureSkipVerify: true,
		AuthType:           node.AuthType, // Set authentication type
	}), nil
}

// GetProviderByNodeWithFastConnection creates a VSA provider with fast connection using CA fields from Node struct
var GetProviderByNodeWithFastConnection = _getProviderByNodeWithFastConnection

func _getProviderByNodeWithFastConnection(ctx context.Context, node *models.Node) (Provider, error) {
	if node.AuthType == env.USER_CERTIFICATE {
		// Create PoolCredentials from Node's CA URI for certificate retrieval
		poolCredentials := &datamodel.PoolCredentials{
			CaURI:         node.GetCaURIWithFallback(),
			CertificateID: node.CertificateID,
		}

		certificate, err := GetCertificateFromCacheOrSecretManager(ctx, poolCredentials)

		if err != nil {
			util.GetLogger(ctx).Errorf("Failed to get certificate for node %s: %v", node.Name, err)
			return nil, errors.NewVCPError(errors.ErrGCPResourceFetchError, err)
		}

		return NewProvider(ctx, ProviderDetails{
			Hosts: node.EndpointAddressesToHostNameMap,
			Certificate: &Certificate{
				SignedCertificate:        certificate.SignedCertificate,
				InterMediateCertificates: certificate.InterMediateCertificates,
				CommonName:               certificate.CommonName,
				PrivateKey:               certificate.PrivateKey,
			},
			InsecureSkipVerify: false,
			FastConnection:     true, // Enable fast connection for quick health checks
		}), nil
	}

	var password string
	if node.AuthType == env.USERNAME_PWD_SEC_MGR {
		secret, err := GetPasswordFromCacheOrSecretManager(ctx, node.SecretID)
		if err != nil {
			util.GetLogger(ctx).Errorf("Failed to get password for node %s: %v", node.Name, err)
			return nil, errors.NewVCPError(errors.ErrGCPResourceFetchError, err)
		}
		password = secret
	} else {
		password = node.Password
	}
	// if ipAddress in empty, populate it with the node's endpoint address
	if len(node.EndpointAddressesToHostNameMap) == 0 {
		if node.EndpointAddress == "" {
			return nil, errors.NewVCPError(errors.ErrVSAClusterNodeIPAddressNotFound, errors2.New("node endpoint address is empty"))
		}
		node.EndpointAddressesToHostNameMap[node.EndpointAddress] = node.EndpointAddress
	}

	return NewProvider(ctx, ProviderDetails{
		Hosts:              node.EndpointAddressesToHostNameMap,
		Password:           password,
		InsecureSkipVerify: true,
		FastConnection:     true, // Enable fast connection for quick health checks
	}), nil
}

var GetCertificateFromCacheOrSecretManager = _getCertificateFromCacheOrSecretManager

// GetCertificateFromCacheOrCAS is the OCI counterpart to
// GetCertificateFromCacheOrSecretManager: it serves the cert-auth path on OCI
// by reading from the in-memory cert-auth cache first and falling back to OCI
// Certificates Service on a miss. Exposed as a package var so tests can stub
// it the same way the GCP variant is stubbed.
var GetCertificateFromCacheOrCAS = _getCertificateFromCacheOrCAS

var RevokeCertificateAndDeleteFromCacheAndSecretManager = _revokeCertificateAndDeleteFromCacheAndSecretManager

var RevokeCertificateFromCAS = _revokeCertificateFromCAS

var GenerateAndCreateCertificateForVSACluster = _generateAndCreateCertificateForVSACluster

var CreateCertificateForVSAClusterOCI = _createCertificateForVSAClusterOCI

var RenewOciCertificateVersionForVSACluster = _renewOciCertificateVersionForVSACluster

var GeneratePasswordForVSACluster = _generatePasswordForVSACluster

var GeneratePasswordForVSAClusterOCI = _generatePasswordForVSAClusterOCI

var DeletePasswordForVSAClusterOCI = _deletePasswordForVSAClusterOCI

var GetPasswordForVSACluster = _getPasswordForVSACluster

var GetPasswordForVSAClusterOCI = _getPasswordForVSAClusterOCI

var GetPasswordFromCacheOrSecretManager = _getPasswordFromCacheOrSecretManager

var GetPasswordFromCacheOrOCIVault = _getPasswordFromCacheOrOCIVault

var GenerateCSR = _generateCSR

var DeletePasswordFromCacheAndSecretManager = _deletePasswordFromSecretManagerAndCache

var DeleteCloudDNSRecord = _deleteCloudDNSRecord

var GetOrCreateCloudDNSRecord = _getOrCreateCloudDNSRecord

// GetOrCreateOCIDNSRecord is the OCI counterpart to GetOrCreateCloudDNSRecord.
// Exposed as a package-level var so the OCI pool activity tests can stub it
// the same way the GCP variant is stubbed.
var GetOrCreateOCIDNSRecord = _getOrCreateOCIDNSRecord

// DeleteOCIDNSRecord is the OCI counterpart to DeleteCloudDNSRecord. Exposed
// as a package-level var for the same test-stubbing reason.
var DeleteOCIDNSRecord = _deleteOCIDNSRecord

var GetCertificateAndPrivateKeyByID = _getCertificateAndPrivateKeyByID

var CreatePrivateKeyInSecretManager = _createPrivateKeyInSecretManager

var CreateCertificateInCAS = _createCertificateInCAS

var CreateCertificateInCASAndPrivateKeyInSM = _createCertificateInCASAndPrivateKeyInSM

var GetCertificateAndSecret = _getCertificateAndSecret

var DeleteCertificateAndSecret = _deleteCertificateAndSecret

// _generateAndCreateCertificateForVSACluster generates a CSR and creates a certificate in GCP Certificate Authority Service.
func _generateAndCreateCertificateForVSACluster(gcpService hyperscaler.Services, clusterName, username string, poolCredentials *datamodel.PoolCredentials, isServerAuthEnabled bool) (*hyperscalermodels.CustomCertificateResponse, error) {
	logger := gcpService.GetLogger()
	certificateID := poolCredentials.CertificateID
	// Get Both Certificate and Secret
	certificate, secret, err := GetCertificateAndSecret(gcpService, poolCredentials)
	if err != nil {
		logger.Errorf("Failed to get Certificate and Secret for certificateID: %s, err: %v", certificateID, err)
		return nil, err
	}

	// NotFoundError already handled and hence certificate and secret will be nil if not found
	if certificate != nil && secret != nil {
		common.AddToCertAuthCache(certificateID, &models.Certificate{
			CommonName:               certificate.SubjectCommonName,
			SignedCertificate:        certificate.PemCertificate,
			PrivateKey:               secret.SecretVersion.Value,
			InterMediateCertificates: certificate.PemCertificateChain,
		})

		return &hyperscalermodels.CustomCertificateResponse{
			Certificate: certificate,
			Secret:      secret,
		}, nil
	}

	// Delete the certificate and Secret if any exist
	err = DeleteCertificateAndSecret(gcpService, certificate, secret, poolCredentials)
	if err != nil {
		logger.Errorf("Failed to delete certificate and private key for certificateID: %s, err: %v", certificateID, err)
		return nil, err
	}

	// If certificate and secret are nil so create a CSR and request new certificate in CAS and store the private key in secret manager
	logger.Debugf("Generating and creating certificate for cluster: %s with certificateID: %s", clusterName, certificateID)
	certificate, secret, err = CreateCertificateInCASAndPrivateKeyInSM(gcpService, certificateID, clusterName, username, poolCredentials, isServerAuthEnabled)
	if err != nil {
		logger.Errorf("Failed to create certificate and store private key for cluster: %s with certificateID: %s", clusterName, certificateID)
		return nil, err
	}

	logger.Debugf("certificate created successfully for certificateID: %s", certificateID)
	// Add the certificate to the cache
	common.AddToCertAuthCache(certificateID, &models.Certificate{
		CommonName:               certificate.SubjectCommonName,
		SignedCertificate:        certificate.PemCertificate,
		PrivateKey:               secret.SecretVersion.Value,
		InterMediateCertificates: certificate.PemCertificateChain,
	})

	return &hyperscalermodels.CustomCertificateResponse{
		Certificate: certificate,
		Secret:      secret,
	}, nil
}

// _createCertificateForVSAClusterOCI is the OCI counterpart to
// _generateAndCreateCertificateForVSACluster. It uses OCI's internally-managed
// certificate flow: OCI generates the RSA key pair, builds the CSR, and signs
// the cert against env.OCIIssuerCAOCID — all in one call. The private key is
// pulled back inline via GetCertificateBundle(WITH_PRIVATE_KEY).
//
// Idempotency:
//
//	On retry we first look up the cert by name (poolCredentials.CertificateID)
//	in env.OCICompartmentOCID. If a usable certificate is already there we
//	return its bundle without re-creating; if it's stuck in FAILED we schedule
//	it for deletion and fall through to a fresh create. This makes the
//	enclosing Temporal activity safe to retry.
//
// Return contract:
//
//	Returns *hyperscalermodels.CustomCertificateResponse with the OCI-managed
//	private key wedged into Secret.SecretVersion.Value, so the existing
//	setPoolCredentials consumer at pool_activities.go works unchanged.
func _createCertificateForVSAClusterOCI(ociService *oci.OciServices, clusterName string, certName string, poolCredentials *datamodel.PoolCredentials, isServerAuthEnabled bool) (*hyperscalermodels.CustomCertificateResponse, error) {
	if ociService == nil {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_createCertificateForVSAClusterOCI: ociService must not be nil"))
	}
	logger := ociService.GetLogger()

	if err := _validateOCICertConfig(); err != nil {
		logger.Errorf("OCI certificate config invalid: %v", err)
		return nil, err
	}
	if poolCredentials == nil {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_createCertificateForVSAClusterOCI: poolCredentials must not be nil"))
	}
	if clusterName == "" {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_createCertificateForVSAClusterOCI: clusterName is required"))
	}
	if certName == "" {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_createCertificateForVSAClusterOCI: certName is required"))
	}

	compartmentOCID := env.OCICompartmentOCID
	issuerCAOCID := env.OCIIssuerCAOCID

	existing, err := ociService.GetCertificateByName(certName, compartmentOCID)
	if err != nil {
		logger.Errorf("Failed to look up existing OCI certificate by name: %s, err: %v", certName, err)
		return nil, err
	}

	if existing != nil {
		switch existing.LifecycleState {
		case oci.CertLifecycleStateActive, oci.CertLifecycleStateUpdating:
			// Certificate is already usable, fall through to the bundle fetch below.
		case oci.CertLifecycleStateCreating:
			logger.Infof("OCI certificate %s exists in CREATING state — waiting for ACTIVE", certName)
			if waitErr := ociService.WaitForCertificateActive(existing.Ocid, 0); waitErr != nil {
				return nil, waitErr
			}
		case oci.CertLifecycleStateFailed:
			logger.Warnf("OCI certificate %s found in FAILED state — scheduling deletion and creating a fresh one", certName)
			if delErr := ociService.DeleteCertificate(existing.Ocid); delErr != nil {
				logger.Errorf("Failed to delete failed OCI certificate %s before recreate: %v", certName, delErr)
				return nil, delErr
			}
			existing = nil
		default:
			logger.Warnf("OCI certificate %s in unexpected state %s — treating as missing and creating fresh",
				certName, existing.LifecycleState)
			existing = nil
		}
	}

	if existing != nil {
		cert, getErr := ociService.GetCertificate(existing.Ocid)
		if getErr != nil {
			logger.Errorf("Failed to fetch bundle for existing OCI certificate %s (OCID: %s): %v",
				certName, existing.Ocid, getErr)
			return nil, getErr
		}
		if cert == nil {
			logger.Warnf("OCI certificate %s vanished between lookup and bundle fetch — falling through to create",
				certName)
		} else {
			response := buildCustomCertificateResponseFromOCI(cert)
			common.AddToCertAuthCache(certName, &models.Certificate{
				CommonName:               cert.SubjectCommonName,
				SignedCertificate:        cert.PemCertificate,
				PrivateKey:               cert.PrivateKeyPem,
				InterMediateCertificates: ociPemChainToSlice(cert.PemCertificateChain),
			})
			logger.Debugf("Reusing existing OCI certificate %s (OCID: %s)", certName, cert.Ocid)
			return response, nil
		}
	}

	domains := []string{fmt.Sprintf("*.%s.%s", clusterName, env.VsaDeployedDnsName)}
	logger.Debugf("Creating OCI certificate — certName: %s, commonName: %s, domains: %v, clusterName: %s",
		certName, poolCredentials.Username, domains, clusterName)

	created, err := ociService.CreateCertificate(
		compartmentOCID,
		issuerCAOCID,
		certName,
		poolCredentials.Username,
		ontapCertSubjectOrganization,
		domains,
		isServerAuthEnabled,
		env.OCICertificateValidityDays,
	)
	if err != nil {
		logger.Errorf("Failed to create OCI certificate %s for cluster %s: %v", certName, clusterName, err)
		return nil, err
	}

	if waitErr := ociService.WaitForCertificateActive(created.Ocid, 0); waitErr != nil {
		logger.Errorf("OCI certificate %s (OCID: %s) did not become ACTIVE: %v", certName, created.Ocid, waitErr)
		return nil, waitErr
	}

	cert, err := ociService.GetCertificate(created.Ocid)
	if err != nil {
		logger.Errorf("Failed to fetch bundle for newly-created OCI certificate %s (OCID: %s): %v",
			certName, created.Ocid, err)
		return nil, err
	}
	if cert == nil {
		return nil, errors.NewVCPError(errors.ErrOCIResourceFetchError,
			fmt.Errorf("OCI certificate %s reached ACTIVE but bundle is unavailable", certName))
	}

	common.AddToCertAuthCache(certName, &models.Certificate{
		CommonName:               cert.SubjectCommonName,
		SignedCertificate:        cert.PemCertificate,
		PrivateKey:               cert.PrivateKeyPem,
		InterMediateCertificates: ociPemChainToSlice(cert.PemCertificateChain),
	})
	logger.Debugf("Created OCI certificate %s (OCID: %s) for cluster %s", certName, cert.Ocid, clusterName)

	return buildCustomCertificateResponseFromOCI(cert), nil
}

// _renewOciCertificateVersionForVSACluster mints the next version of an existing
// OCI certificate for the cert-rotation flow and returns its bundle, without
// cutting over to it. It is the rotation-time counterpart to
// _createCertificateForVSAClusterOCI: instead of creating a whole certificate
// resource, it adds a new *version* staged as PENDING (same OCID, same name),
// using the Step-2 hyperscaler primitives.
//
// Reuse-or-create (idempotency, see doc/design-doc/oci-certificate-rotation.md
// §4 step 2a and §5):
//
//	On retry (or after a prior install/verify failure) a healthy PENDING version
//	may already exist. We reuse it rather than minting another, because PENDING
//	versions cannot be deleted and do not auto-deprecate (§2.5) — re-minting would
//	just accumulate versions. A FAILED staging is treated as "none" and we mint a
//	fresh PENDING version. This makes the enclosing Temporal activity safe to retry.
//
// Scope (Option B — pure mint + return):
//
//	This function does NOT promote the version to CURRENT, does NOT install it on
//	ONTAP, and deliberately does NOT touch the cert-auth cache. The cache is keyed
//	by cert name (stable across versions) and writing the new version before it is
//	actually live on ONTAP would risk an auth mismatch if a later step fails. The
//	caller (rotation activity) owns promotion, ONTAP install/verify, and the
//	post-promote cache refresh.
//
// Return contract:
//
//	Returns *hyperscalermodels.CustomCertificateResponse for the ACTIVE PENDING
//	version (OCI-managed private key wedged into Secret.SecretVersion.Value), in
//	the same shape as _createCertificateForVSAClusterOCI, so the existing ONTAP
//	install path consumes it unchanged. Certificate.VersionNumber carries the new
//	(PENDING) version number the caller promotes once verified.
func _renewOciCertificateVersionForVSACluster(ociService *oci.OciServices, certOCID string, validityDays int, isServerAuthEnabled bool) (*hyperscalermodels.CustomCertificateResponse, error) {
	if ociService == nil {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_renewOciCertificateVersionForVSACluster: ociService must not be nil"))
	}
	logger := ociService.GetLogger()

	if certOCID == "" {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_renewOciCertificateVersionForVSACluster: certOCID is required"))
	}

	// Reuse a leftover healthy PENDING version if one exists; otherwise mint one.
	versions, err := ociService.ListCertificateVersions(certOCID)
	if err != nil {
		logger.Errorf("Failed to list versions for OCI certificate %s: %v", certOCID, err)
		return nil, err
	}

	reusePending := false
	for _, v := range versions {
		if v.IsPending() && !v.IsFailed() {
			logger.Infof("Reusing existing healthy PENDING version %d for OCI certificate %s", v.VersionNumber, certOCID)
			reusePending = true
			break
		}
	}

	if !reusePending {
		// RenewCertificateVersionPending issues UpdateCertificate, which OCI only
		// accepts when the certificate is ACTIVE. A preceding cleanup sweep
		// (ScheduleCertificateVersionDeletion) transitions the parent certificate
		// into a transient UPDATING state, so minting immediately afterward is
		// rejected with 409 IncorrectState. Wait for the certificate to settle
		// back to ACTIVE before staging the new version.
		if waitErr := ociService.WaitForCertificateActive(certOCID, 0); waitErr != nil {
			logger.Errorf("OCI certificate %s did not become ACTIVE before staging a new version: %v", certOCID, waitErr)
			return nil, waitErr
		}
		logger.Debugf("Staging a new PENDING version for OCI certificate %s (validityDays: %d, isServerAuthEnabled: %t)",
			certOCID, validityDays, isServerAuthEnabled)
		if _, err = ociService.RenewCertificateVersionPending(certOCID, validityDays, isServerAuthEnabled); err != nil {
			logger.Errorf("Failed to stage new PENDING version for OCI certificate %s: %v", certOCID, err)
			return nil, err
		}
	}

	// The (re)staged version provisions asynchronously; wait for ACTIVE before
	// reading its bundle.
	if waitErr := ociService.WaitForCertificateActive(certOCID, 0); waitErr != nil {
		logger.Errorf("OCI certificate %s did not become ACTIVE after staging a new version: %v", certOCID, waitErr)
		return nil, waitErr
	}

	pending, err := ociService.GetPendingCertificate(certOCID)
	if err != nil {
		logger.Errorf("Failed to fetch PENDING bundle for OCI certificate %s: %v", certOCID, err)
		return nil, err
	}
	if pending == nil {
		return nil, errors.NewVCPError(errors.ErrOCIResourceFetchError,
			fmt.Errorf("OCI certificate %s has no PENDING version after staging", certOCID))
	}

	logger.Debugf("Staged PENDING version %d for OCI certificate %s", pending.VersionNumber, certOCID)
	return buildCustomCertificateResponseFromOCI(pending), nil
}

// _validateOCICertConfig returns a typed configuration error if any of the
// environment values required for OCI certificate provisioning are unset.
// Mirrors _validateOCIVaultConfig — fail fast at the entry point rather than
// deep inside the SDK with an opaque "required field is empty" error.
func _validateOCICertConfig() error {
	var missing []string
	if env.OCICompartmentOCID == "" {
		missing = append(missing, "OCI_COMPARTMENT_OCID")
	}
	if env.OCIIssuerCAOCID == "" {
		missing = append(missing, "OCI_ISSUER_CA_OCID")
	}
	if env.VsaDeployedDnsName == "" {
		missing = append(missing, "VSA_DEPLOYED_DNS_NAME")
	}
	if len(missing) == 0 {
		return nil
	}
	return errors.NewVCPError(
		errors.ErrOCIClientInitializationError,
		fmt.Errorf("OCI certificate configuration incomplete; missing env var(s): %s", strings.Join(missing, ", ")),
	)
}

// ociPemChainToSlice converts OCI's concatenated-PEM chain string into the
// []string shape expected by hyperscaler/models and core/models, producing
// one PEM block per slice element to match the GCP convention.
//
// Behavior:
//   - Empty or whitespace-only input returns nil so the field stays
//     zero-valued instead of `[]string{""}`.
//   - Each decoded CERTIFICATE block is re-encoded canonically so callers
//     get well-formed PEM regardless of input formatting.
//   - Non-CERTIFICATE blocks (e.g. a stray PRIVATE KEY) are skipped — the
//     downstream consumer is strictly a cert chain.
//   - Non-empty input that decodes to zero CERTIFICATE blocks falls back to
//     the original single-element shape so the downstream parser raises the
//     same "Failed to parse certificate" error it does today rather than a
//     less precise empty-input error.
func ociPemChainToSlice(pemChain string) []string {
	if strings.TrimSpace(pemChain) == "" {
		return nil
	}

	var out []string
	rest := []byte(pemChain)
	for {
		block, remainder := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = remainder
		if block.Type != "CERTIFICATE" {
			continue
		}
		out = append(out, string(pem.EncodeToMemory(block)))
	}

	if len(out) == 0 {
		return []string{pemChain}
	}
	return out
}

// Functional fields (read by code downstream):
//   - Certificate.SubjectCommonName        → setPoolCredentials → vlm Certificate.CommonName
//   - Certificate.PemCertificate           → setPoolCredentials → vlm Certificate.Certificate
//   - Certificate.PemCertificateChain      → setPoolCredentials → vlm Certificate.InterMediateCertificate
//   - Secret.SecretVersion.Value           → setPoolCredentials → vlm Certificate.PrivateKey
//     (this is where the OCI-managed private key is wedged so the
//     GCP-shaped consumer keeps working unchanged — on OCI there is
//     no separate Vault entry for the key)
//
// Identity field used by OCI-side cleanup / rotation flows:
//
//   - Certificate.CertificateID            → OCI cert OCID. Set to cert.Ocid (not
//     cert.Name) because OCI requires the OCID for every subsequent operation
//     (GetCertificate, DeleteCertificate). Any future OCI revoke/rotate path
//     can pull the handle straight from here without a second
//     GetCertificateByName lookup.
//
// GCP-shape filler (not read by anyone on the OCI side; preserved so JSON
// dumps and logs still carry a readable identifier rather than ""):
//
//   - Certificate.Name, Certificate.SubjectOrganization,
//     Certificate.SerialNumber, Certificate.IssuerCertificateAuthority,
//     Certificate.CertOwningEntity, Certificate.CreateTime
//   - Secret.Name, Secret.SecretOwningEntity,
//     SecretVersion.Name, SecretVersion.SecretOwningEntity
func buildCustomCertificateResponseFromOCI(cert *oci.OCICustomCertificate) *hyperscalermodels.CustomCertificateResponse {
	return &hyperscalermodels.CustomCertificateResponse{
		Certificate: &hyperscalermodels.CustomCertificate{
			Name:                       cert.Name,
			CertificateID:              cert.Ocid,
			SubjectCommonName:          cert.SubjectCommonName,
			SubjectOrganization:        cert.SubjectOrganization,
			PemCertificate:             cert.PemCertificate,
			PemCertificateChain:        ociPemChainToSlice(cert.PemCertificateChain),
			SerialNumber:               cert.SerialNumber,
			IssuerCertificateAuthority: cert.IssuerCAOCID,
			CertOwningEntity:           cert.CompartmentID,
			CreateTime:                 cert.TimeCreated,
			VersionNumber:              cert.VersionNumber,
		},
		Secret: &hyperscalermodels.CustomSecret{
			Name:               cert.Name,
			SecretOwningEntity: cert.CompartmentID,
			SecretVersion: &hyperscalermodels.CustomSecretVersion{
				Name:               cert.Name,
				Value:              cert.PrivateKeyPem,
				SecretOwningEntity: cert.CompartmentID,
			},
		},
	}
}

// GetPendingOciCertificateResponse is a test seam for _getPendingOciCertificateResponse.
var GetPendingOciCertificateResponse = _getPendingOciCertificateResponse

// _getPendingOciCertificateResponse reads the PENDING version's bundle (certificate
// plus private key from Vault) for an OCI certificate and shapes it as a
// CustomCertificateResponse. Unlike RenewOciCertificateVersionForVSACluster it is
// strictly read-only — it never stages, reuses, or creates a version. It lets a
// rotation step that only has the version number (the value that crosses the
// Temporal workflow boundary) re-fetch the bundle without re-minting, keeping
// private-key material out of workflow history. Returns (nil, nil) when the
// certificate has no PENDING version.
func _getPendingOciCertificateResponse(ociService *oci.OciServices, certOCID string) (*hyperscalermodels.CustomCertificateResponse, error) {
	if ociService == nil {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_getPendingOciCertificateResponse: ociService must not be nil"))
	}
	if certOCID == "" {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_getPendingOciCertificateResponse: certOCID is required"))
	}

	pending, err := ociService.GetPendingCertificate(certOCID)
	if err != nil {
		return nil, err
	}
	if pending == nil {
		return nil, nil
	}
	return buildCustomCertificateResponseFromOCI(pending), nil
}

func _deleteCertificateAndSecret(gcpService hyperscaler.Services, certificate *hyperscalermodels.CustomCertificate, secret *hyperscalermodels.CustomSecret, poolCredentials *datamodel.PoolCredentials) error {
	logger := gcpService.GetLogger()
	certificateID := poolCredentials.CertificateID
	if certificate != nil {
		// Use environment variables for Region (always from env)
		region := env.Region

		// Parse CA URI from poolCredentials, fallback to environment variables
		caPoolDeployedProjectID, caPoolName, _ := poolCredentials.ParseCaURIWithFallback()

		certObject := &hyperscalermodels.CustomCertificate{
			CertOwningEntity: caPoolDeployedProjectID,
			Region:           region,
			CaGroupName:      caPoolName,
			CertificateID:    certificateID,
		}

		// delete the certificate from CAS
		_, err := gcpService.RevokeCertificate(certObject)
		if err != nil {
			logger.Errorf("Failed to revoke certificate for projectID: %s, region: %s and certificateID: %s", caPoolDeployedProjectID, region, certificateID)
			return err
		}
	}

	if secret != nil {
		// delete the private key from secret manager
		err := gcpService.DeleteSecret(env.SecretManagerProjectID, certificateID)
		if err != nil {
			logger.Errorf("Failed to delete private key from secret manager for projectID: %s and certificate: %s", env.SecretManagerProjectID, certificateID)
			return err
		}
	}
	return nil
}

func _getCertificateAndSecret(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) (*hyperscalermodels.CustomCertificate, *hyperscalermodels.CustomSecret, error) {
	logger := gcpService.GetLogger()
	certificateID := poolCredentials.CertificateID

	// Use environment variables for Region (always from env)
	region := env.Region

	// Parse CA URI from poolCredentials, fallback to environment variables
	caPoolDeployedProjectID, caPoolName, _ := poolCredentials.ParseCaURIWithFallback()

	cert, getErr := gcpService.GetCertificate(caPoolDeployedProjectID, region, caPoolName, certificateID)
	if getErr != nil {
		logger.Errorf("Failed to get certificate for certificateID: %s, err: %v", certificateID, getErr)
		return nil, nil, getErr
	}
	secret, getErr := gcpService.GetSecretWithLatestVersion(env.SecretManagerProjectID, certificateID)
	if getErr != nil {
		logger.Errorf("Failed to get secret for certificateID: %s, err: %v", certificateID, getErr)
		return nil, nil, getErr
	}
	return cert, secret, nil
}

func _createCertificateInCASAndPrivateKeyInSM(gcpService hyperscaler.Services, certificateID string, clusterName string, username string, poolCredentials *datamodel.PoolCredentials, isServerAuthEnabled bool) (*hyperscalermodels.CustomCertificate, *hyperscalermodels.CustomSecret, error) {
	logger := gcpService.GetLogger()

	// Use environment variables for Region (always from env)
	region := env.Region

	// Parse CA URI from poolCredentials, fallback to environment variables
	caPoolDeployedProjectID, caPoolName, caName := poolCredentials.ParseCaURIWithFallback()

	domains := fmt.Sprintf("*.%s.%s", clusterName, env.VsaDeployedDnsName)
	certObj := &hyperscalermodels.CustomCertificateParam{
		Region:           region,
		CertificateID:    certificateID,
		CaPoolName:       caPoolName,
		CaName:           caName,
		CommonName:       username,
		Domains:          []string{domains},
		CertOwningEntity: caPoolDeployedProjectID,
	}
	// Generate CSR
	csrDER, key, err := GenerateCSR(certObj.CommonName, certObj.Domains, isServerAuthEnabled)
	if err != nil {
		logger.Errorf("failed to generate CSR for commonName: %s, certificateId : %s, err : %v", certObj.CommonName, certObj.CertificateID, err)
		return nil, nil, err
	}

	pemBlock := pem.Block{
		Type:  CsrType,
		Bytes: csrDER,
	}
	logger.Debugf("Generate CSR for commonName: %s, certificateId : %s", certObj.CommonName, certObj.CertificateID)

	customCertificate, err := common2.ValidateAndConvertCertParams(certObj, pemBlock)
	if err != nil {
		return nil, nil, err
	}

	// Create the private key in Secret Manager
	secret, err := CreatePrivateKeyInSecretManager(gcpService, customCertificate, key)
	if err != nil {
		logger.Errorf("failed to create private key in Secret Manager for projectID: %s, certificateId : %s, err : %v", env.SecretManagerProjectID, certObj.CertificateID, err)
		return nil, nil, err
	}

	// Create the Certificate in CAS
	cert, err := CreateCertificateInCAS(gcpService, customCertificate)
	if err != nil {
		logger.Errorf("failed to create Certificate in CAS for projectID: %s, certificateId : %s, err : %v", certObj.CertOwningEntity, certObj.CertificateID, err)
		return nil, nil, err
	}
	return cert, secret, nil
}

// _createCertificateInCAS creates a certificate in GCP Certificate Authority Service and stores the private key in Secret Manager.
func _createCertificateInCAS(gcpService hyperscaler.Services, certificate *hyperscalermodels.CustomCertificate) (*hyperscalermodels.CustomCertificate, error) {
	logger := gcpService.GetLogger()
	logger.Debugf("Creating certificate in CAS for commonName: %s, certificateId : %s", certificate.SubjectCommonName, certificate.CertificateID)
	var cert *hyperscalermodels.CustomCertificate
	// Create the Certificate
	cert, err := gcpService.CreateCertificate(certificate)
	if err != nil {
		logger.Errorf("failed to create certificate in CAS for commonName: %s, certificateId : %s, err : %v", certificate.SubjectCommonName, certificate.CertificateID, err)
		return nil, err
	}
	logger.Debugf("created certificate in CAS for commonName: %s, certificateId : %s", certificate.SubjectCommonName, certificate.CertificateID)
	return cert, nil
}

func _createPrivateKeyInSecretManager(gcpService hyperscaler.Services, certificate *hyperscalermodels.CustomCertificate, key *rsa.PrivateKey) (*hyperscalermodels.CustomSecret, error) {
	logger := gcpService.GetLogger()
	logger.Debugf("Creating private key in Secret Manager for commonName: %s, certificateId : %s", certificate.SubjectCommonName, certificate.CertificateID)

	// Store the private key in Secret Manager using the certificate ID directly
	secretValue := common2.ConvertPrivateKeyToString(key, RsaKeyType)
	secret, err := gcpService.CreateSecret(env.SecretManagerProjectID, certificate.Region, certificate.CertificateID, secretValue)
	if err != nil {
		logger.Errorf("failed to create secret in SM for commonName: %s, certificateId : %s, err : %v", certificate.SubjectCommonName, certificate.CertificateID, err)
		return nil, err
	}
	logger.Debugf("created secret in SM for commonName: %s, certificateId : %s", certificate.SubjectCommonName, certificate.CertificateID)
	return secret, nil
}

// _getCertificateAndPrivateKeyByID retrieves the certificate for a VSA cluster from GCP Certificate Authority Service and Private key from Secret Manager.
func _getCertificateAndPrivateKeyByID(gcpService hyperscaler.Services, caDeployedProjectID, secretManagerProjectID, region, caPoolName, certificateID string) (*hyperscalermodels.CustomCertificateResponse, error) {
	certificate, err := gcpService.GetCertificate(caDeployedProjectID, region, caPoolName, certificateID)
	if err != nil || certificate == nil {
		return nil, fmt.Errorf("failed to get certficate for project: %s, region: %s, caPoolName : %s, certificateID : %s, err: %s", caDeployedProjectID, region, caPoolName, certificateID, err)
	}

	// Get the private key from Secret Manager using the certificate ID directly
	secret, err := gcpService.GetSecretWithLatestVersion(secretManagerProjectID, certificateID)
	if err != nil || secret == nil || secret.SecretVersion == nil {
		return nil, fmt.Errorf("failed to get secret for project: %s, certificateID: %s, err: %s", secretManagerProjectID, certificateID, err)
	}
	return &hyperscalermodels.CustomCertificateResponse{
		Certificate: certificate,
		Secret:      secret,
	}, nil
}

// _generatePasswordForVSACluster generates a strong password and creates a secret in GCP Secret Manager.
func _generatePasswordForVSACluster(gcpService hyperscaler.Services, secretID string) (*hyperscalermodels.CustomSecret, error) {
	logger := gcpService.GetLogger()

	var secret *hyperscalermodels.CustomSecret
	projectID := env.SecretManagerProjectID
	secret, getSecretError := gcpService.GetSecretWithLatestVersion(projectID, secretID)
	if getSecretError != nil {
		return nil, getSecretError
	}
	if secret == nil {
		password, err := utils.GenerateStrongPassword(12)
		if err != nil {
			logger.Errorf("failed to generate password for secretID: %s, err : %v", secretID, err)
			return nil, err
		}

		secret, err = gcpService.CreateSecret(projectID, env.Region, secretID, password)
		if err != nil {
			return nil, err
		}

		common.AddToUserAuthCache(secretID, secret.SecretVersion.Value)
	}
	return secret, nil
}

// _validateOCIVaultConfig returns a typed configuration error if any of the OCI
// vault identifiers required for password create/lookup/delete are empty.
func _validateOCIVaultConfig(requireWriteConfig bool) error {
	var missing []string
	if env.OCIVaultOCID == "" {
		missing = append(missing, "OCI_VAULT_OCID")
	}
	if requireWriteConfig {
		if env.OCICompartmentOCID == "" {
			missing = append(missing, "OCI_COMPARTMENT_OCID")
		}
		if env.OCIMasterKeyOCID == "" {
			missing = append(missing, "OCI_MASTER_KEY_OCID")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return errors.NewVCPError(
		errors.ErrOCIClientInitializationError,
		fmt.Errorf("OCI vault configuration incomplete; missing env var(s): %s", strings.Join(missing, ", ")),
	)
}

// _generatePasswordForVSAClusterOCI generates a strong password and creates a secret in OCI Vault
// if one with the given name does not already exist. The function is idempotent: when GetSecretByName
// returns an existing secret it is returned unchanged, which makes activity retries safe.
//
// Requires OCI_COMPARTMENT_OCID, OCI_VAULT_OCID, and OCI_MASTER_KEY_OCID to be set; missing config
// is surfaced as ErrOCIClientInitializationError before any OCI API call is made.
func _generatePasswordForVSAClusterOCI(ociService *oci.OciServices, secretName string) (*oci.OCICustomSecret, error) {
	if ociService == nil {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_generatePasswordForVSAClusterOCI: ociService must not be nil"))
	}
	logger := ociService.GetLogger()

	if err := _validateOCIVaultConfig(true); err != nil {
		logger.Errorf("OCI vault config invalid for secretName %s: %v", secretName, err)
		return nil, err
	}

	compartmentOCID := env.OCICompartmentOCID
	vaultOCID := env.OCIVaultOCID
	masterKeyOCID := env.OCIMasterKeyOCID

	var ociSecret *oci.OCICustomSecret
	ociSecret, getSecretError := ociService.GetSecretByName(secretName, vaultOCID)
	if getSecretError != nil {
		return nil, getSecretError
	}
	if ociSecret == nil {
		password, err := utils.GenerateStrongPassword(12)
		if err != nil {
			logger.Errorf("failed to generate password for secretName: %s, err : %v", secretName, err)
			return nil, err
		}

		ociSecret, err = ociService.CreateSecret(compartmentOCID, vaultOCID, masterKeyOCID, secretName, password)
		if err != nil {
			return nil, err
		}

		common.AddToUserAuthCache(secretName, ociSecret.Value)
	}
	return ociSecret, nil
}

// _deletePasswordForVSAClusterOCI deletes the ONTAP admin password secret from OCI Vault
func _deletePasswordForVSAClusterOCI(ociService *oci.OciServices, poolCredentials *datamodel.PoolCredentials) error {
	if ociService == nil {
		return errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_deletePasswordForVSAClusterOCI: ociService must not be nil"))
	}
	logger := ociService.GetLogger()

	if poolCredentials == nil {
		return errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_deletePasswordForVSAClusterOCI: poolCredentials must not be nil"))
	}

	if poolCredentials.ExternalSecret == nil ||
		poolCredentials.ExternalSecret.ExternalIdentifier == "" {
		logger.Infof("DeletePasswordForVSAClusterOCI: no persisted OCI vault secret handle on poolCredentials — skipping delete")
		return nil
	}

	secretOCID := poolCredentials.ExternalSecret.ExternalIdentifier
	secretName := poolCredentials.ExternalSecret.Name

	if err := ociService.DeleteSecret(secretOCID); err != nil {
		logger.Errorf("Failed to schedule OCI vault secret deletion — secretName: %s, secretOCID: %s, err: %v",
			secretName, secretOCID, err)
		return err
	}

	if secretName != "" {
		if !common.RemoveFromUserAuthCache(secretName) {
			logger.Debugf("DeletePasswordForVSAClusterOCI: secret %s not present in user-auth cache, nothing to evict", secretName)
		}
	}
	return nil
}

// _getPasswordForVSACluster retrieves the password for a VSA cluster from GCP Secret Manager.
func _getPasswordForVSACluster(gcpService hyperscaler.Services, secretID string) (*hyperscalermodels.CustomSecret, error) {
	secret, err := gcpService.GetSecretWithLatestVersion(env.SecretManagerProjectID, secretID)
	if err != nil || secret == nil || secret.SecretVersion == nil {
		return nil, fmt.Errorf("failed to get secret for project: %s, userName: %s, err: %s", env.SecretManagerProjectID, secretID, err)
	}
	return secret, nil
}

// _getPasswordFromCacheOrSecretManager retrieves the password for a VSA cluster from cache or GCP Secret Manager if not found in cache.
func _getPasswordFromCacheOrSecretManager(ctx context.Context, secretID string) (string, error) {
	password := ""
	userCache, exist := common.GetFromUserAuthCache(secretID)
	if !exist || userCache.Password == "" {
		gcpService, err := hyperscaler.GetGCPService(ctx)
		if err != nil {
			return "", err
		}
		secret, err := GetPasswordForVSACluster(gcpService, secretID)
		if err != nil {
			return "", err
		}
		password = secret.SecretVersion.Value
		common.AddToUserAuthCache(secretID, password)
		return password, nil
	}
	password = userCache.Password
	return password, nil
}

// _getPasswordForVSAClusterOCI retrieves the password for a VSA cluster from OCI Vault using the secret OCID.
func _getPasswordForVSAClusterOCI(ctx context.Context, secretID string) (*oci.OCICustomSecret, error) {
	ociService, err := hyperscaler.GetOCIService(ctx)
	if err != nil {
		return nil, err
	}
	secret, err := ociService.GetSecretWithLatestVersion(secretID)
	if err != nil || secret == nil || secret.Value == "" {
		return nil, fmt.Errorf("failed to get secret for External Identifier: %s, err: %v", secretID, err)
	}
	return secret, nil
}

// _getPasswordFromCacheOrOCIVault Cache is keyed by the OCI secret Name;
// on miss it fetches the latest version from OCI Vault.
func _getPasswordFromCacheOrOCIVault(ctx context.Context, ref *datamodel.ExternalCredRef) (string, error) {
	if ref == nil || ref.Name == "" {
		return "", errors2.New("OCI vault reference is empty")
	}
	if userCache, exist := common.GetFromUserAuthCache(ref.Name); exist && userCache.Password != "" {
		return userCache.Password, nil
	}
	ociService, err := hyperscaler.GetOCIService(ctx)
	if err != nil {
		return "", err
	}
	if err := _validateOCIVaultConfig(false); err != nil {
		ociService.Logger.Errorf("OCI vault config invalid for secretName %s: %v", ref.Name, err)
		return "", err
	}
	secret, err := GetPasswordForVSAClusterOCI(ctx, ref.ExternalIdentifier)
	if err != nil {
		return "", err
	}
	if secret == nil || secret.Value == "" {
		return "", fmt.Errorf("OCI vault secret %s is empty or not found", ref.Name)
	}
	common.AddToUserAuthCache(ref.Name, secret.Value)
	return secret.Value, nil
}

// _deletePasswordFromSecretManagerAndCache generates a strong password and creates a secret in GCP Secret Manager.
func _deletePasswordFromSecretManagerAndCache(gcpService hyperscaler.Services, secretID string) error {
	logger := gcpService.GetLogger()
	secret, err := gcpService.GetSecretWithLatestVersion(env.SecretManagerProjectID, secretID)
	if err != nil {
		return err
	}
	if secret != nil {
		err = gcpService.DeleteSecret(env.SecretManagerProjectID, secretID)
		if err != nil {
			logger.Errorf("failed to delete password for secretID: %s, err : %v", secretID, err)
			return err
		}
	}

	done := common.RemoveFromUserAuthCache(secretID)
	if !done {
		logger.Errorf("failed to remove password from cache for secretID: %s", secretID)
	}
	return nil
}

// _getCertificateFromCacheOrSecretManager retrieves the certificate from cache or GCP Certificate and Secret Manager.
func _getCertificateFromCacheOrSecretManager(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (*models.Certificate, error) {
	certificateID := poolCredentials.CertificateID
	certCache, exist := common.GetCertAuthCache(certificateID)
	// If not found in cache, fetch from GCP Certificate and Secret Manager
	if !exist || certCache.Certificate == nil {
		gcpService, err := hyperscaler.GetGCPService(ctx)
		if err != nil {
			return nil, err
		}

		// Use environment variables for Region (always from env)
		region := env.Region

		// Parse CA URI from poolCredentials, fallback to environment variables
		caPoolDeployedProjectID, caPoolName, _ := poolCredentials.ParseCaURIWithFallback()

		certificateResponse, err := GetCertificateAndPrivateKeyByID(gcpService, caPoolDeployedProjectID, env.SecretManagerProjectID, region, caPoolName, certificateID)
		if err != nil {
			return nil, err
		}
		cert := &models.Certificate{
			SignedCertificate:        certificateResponse.Certificate.PemCertificate,
			PrivateKey:               certificateResponse.Secret.SecretVersion.Value,
			CommonName:               certificateResponse.Certificate.SubjectCommonName,
			InterMediateCertificates: certificateResponse.Certificate.PemCertificateChain,
		}
		common.AddToCertAuthCache(certificateID, cert)
		return cert, nil
	}
	return certCache.Certificate, nil
}

// _getCertificateFromCacheOrCAS retrieves an OCI-managed certificate (signed
// cert, private key, chain) from the in-memory cert-auth cache, falling back
// to an OCI Certificates Service round-trip on a miss.
//
// Cache key: ref.Name. _createCertificateForVSAClusterOCI populates the same
// key (= ociOntapCertificateName(pool), e.g. "<deploymentName>-cert") when the
// cert is first minted, so warm-path callers (every ONTAP REST request after
// pool create) hit the cache and never call OCI.
//
// Lookup-by-OCID: ref.ExternalIdentifier holds the OCI-assigned cert OCID,
// which OCI requires for every read (GetCertificate accepts OCID, not name).
//
// Returns the same *models.Certificate shape as the GCP variant
// (_getCertificateFromCacheOrSecretManager) so the cert-auth code paths stay
// unified across hyperscalers.
func _getCertificateFromCacheOrCAS(ctx context.Context, ref *datamodel.ExternalCredRef) (*models.Certificate, error) {
	if ref == nil || ref.Name == "" || ref.ExternalIdentifier == "" {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("OCI certificate reference is empty or incomplete"))
	}

	cacheKey := ref.Name
	if certCache, exist := common.GetCertAuthCache(cacheKey); exist && certCache.Certificate != nil {
		return certCache.Certificate, nil
	}

	ociService, err := hyperscaler.GetOCIService(ctx)
	if err != nil {
		return nil, err
	}

	cert, err := ociService.GetCertificate(ref.ExternalIdentifier)
	if err != nil {
		return nil, err
	}
	// OciServices.GetCertificate returns (nil, nil) for the 404/deletion-state
	// cases (mirrors googleResourceNotFoundCheck). Surface those as a typed
	// fetch error so the cert-auth code path can fail fast instead of trying
	// to build a vsa.Provider with a nil Certificate.
	if cert == nil {
		return nil, errors.NewVCPError(errors.ErrOCIResourceFetchError,
			fmt.Errorf("OCI certificate %q (OCID %s) not found in OCI Certificates Service",
				ref.Name, ref.ExternalIdentifier))
	}
	if cert.PrivateKeyPem == "" || cert.PemCertificate == "" {
		return nil, errors.NewVCPError(errors.ErrOCIResourceFetchError,
			fmt.Errorf("OCI certificate %q (OCID %s) bundle is missing required PEM material",
				ref.Name, ref.ExternalIdentifier))
	}

	out := &models.Certificate{
		CommonName:               cert.SubjectCommonName,
		SignedCertificate:        cert.PemCertificate,
		PrivateKey:               cert.PrivateKeyPem,
		InterMediateCertificates: ociPemChainToSlice(cert.PemCertificateChain),
	}
	common.AddToCertAuthCache(cacheKey, out)
	return out, nil
}

// _getOrCreateCloudDNSRecord checks if a Cloud DNS record exists, and if not, creates it.
func _getOrCreateCloudDNSRecord(gcpService hyperscaler.Services, recordName, ipAddress string) (*hyperscalermodels.CustomCloudDNSRecord, error) {
	gcpService.GetLogger().Debugf("Get and Create Cloud DNS for projectID: %s, record: %s, managedZone: %s", env.SecretManagerProjectID, recordName, env.VsaManagedZone)
	record, err := gcpService.GetResourceRecordSet(env.SecretManagerProjectID, env.VsaManagedZone, recordName)
	if err != nil {
		gcpService.GetLogger().Errorf("Failed to get Cloud DNS record: %s, err: %v", recordName, err)
		return nil, err
	}
	if record == nil {
		gcpService.GetLogger().Debugf("Creating Cloud DNS record: %s, managedzone %s", recordName, env.VsaManagedZone)
		record, err = gcpService.CreateResourceRecordSet(env.SecretManagerProjectID, env.VsaManagedZone, ipAddress, recordName)
		if err != nil {
			gcpService.GetLogger().Errorf("Failed to create Cloud DNS record: %s, err: %v", recordName, err)
			return nil, err
		}
	}

	// If the record already exists, return it
	return record, nil
}

func _deleteCloudDNSRecord(gcpService hyperscaler.Services, recordName string) error {
	logger := gcpService.GetLogger()
	record, err := gcpService.GetResourceRecordSet(env.SecretManagerProjectID, env.VsaManagedZone, recordName)
	if err != nil {
		logger.Errorf("Failed to get Cloud DNS record: %v", err)
		return err
	}
	if record != nil {
		logger.Debugf("Deleting Cloud DNS record: %s.%s", recordName, env.VsaManagedZone)
		err = gcpService.DeleteResourceRecordSet(env.SecretManagerProjectID, env.VsaManagedZone, recordName)
		if err != nil {
			return err
		}
	}
	return nil
}

// _getOrCreateOCIDNSRecord is the OCI counterpart to _getOrCreateCloudDNSRecord.
// It looks up the A record first, and on miss falls through to an upsert via
// OCI DNS UpdateRRSet (which is naturally idempotent — it REPLACES the RRSet
// for (domain, A) rather than appending). The GCP path uses GET-then-CREATE
// because the GCP API rejects duplicate creates with 409; OCI does not have
// that quirk, but we keep the GET-first shape so the retry/error behaviour
// stays identical across hyperscalers.
//
// All zone/scope context is taken from env vars so the caller stays
// hyperscaler-agnostic at the activity layer:
//   - env.OCIVsaDnsZoneOCID — zone OCID (mandatory)
//   - env.OCIVsaDnsScope    — "PRIVATE" (default) | "GLOBAL"
//   - env.CloudDNSCacheTTL  — shared with GCP
func _getOrCreateOCIDNSRecord(ociService *oci.OciServices, recordName, ipAddress string) (*hyperscalermodels.CustomCloudDNSRecord, error) {
	if ociService == nil {
		return nil, errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_getOrCreateOCIDNSRecord: ociService must not be nil"))
	}
	zoneOCID := env.OCIVsaDnsZoneOCID
	logger := ociService.GetLogger()
	logger.Debugf("Get-or-create OCI DNS record — zoneOCID: %s, record: %s, ip: %s",
		zoneOCID, recordName, ipAddress)

	record, err := ociService.GetDnsRecord(zoneOCID, recordName)
	if err != nil {
		logger.Errorf("Failed to get OCI DNS record %s: %v", recordName, err)
		return nil, err
	}
	if record != nil {
		// Idempotency: if a record already exists for this name, return it as-is.
		// We intentionally do NOT verify Data == ipAddress here — the GCP path
		// matches that behaviour, and a mismatch on retry is handled by the
		// surrounding workflow's rollback (DeleteCloudDNSRecords) rather than
		// silently overwriting.
		return record, nil
	}

	logger.Debugf("Creating OCI DNS record %s → %s in zone %s",
		recordName, ipAddress, zoneOCID)
	record, err = ociService.CreateOrUpdateDnsRecord(zoneOCID, recordName, ipAddress)
	if err != nil {
		logger.Errorf("Failed to create OCI DNS record %s: %v", recordName, err)
		return nil, err
	}
	return record, nil
}

// _deleteOCIDNSRecord deletes the dns records from oci private dns.
func _deleteOCIDNSRecord(ociService *oci.OciServices, recordName string) error {
	if ociService == nil {
		return errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_deleteOCIDNSRecord: ociService must not be nil"))
	}
	zoneOCID := env.OCIVsaDnsZoneOCID
	logger := ociService.GetLogger()

	logger.Infof("Deleting OCI DNS record %s in zone %s", recordName, zoneOCID)
	if err := ociService.DeleteDnsRecord(zoneOCID, recordName); err != nil {
		logger.Errorf("Failed to delete OCI DNS record %s: %v", recordName, err)
		return err
	}
	return nil
}

func _revokeCertificateAndDeleteFromCacheAndSecretManager(gcpService hyperscaler.Services, poolCredentials *datamodel.PoolCredentials) error {
	logger := gcpService.GetLogger()
	certificateID := poolCredentials.CertificateID
	certificate, secret, err := GetCertificateAndSecret(gcpService, poolCredentials)

	if err != nil {
		// Cannot get certificate (e.g. revoked, permission denied, permission not assigned, 404, network).
		// Do best-effort cleanup of secret and cache so pool deletion can complete and resources are not left stale.
		logger.Warnf("Cannot get certificate %s (err: %v), proceeding with best-effort cleanup of secret and cache so pool deletion can continue", certificateID, err)

		secret, secretErr := gcpService.GetSecretWithLatestVersion(env.SecretManagerProjectID, certificateID)
		if secretErr != nil {
			logger.Debugf("Secret not found for certificate %s, skipping secret deletion", certificateID)
		}
		if secretErr == nil && secret != nil {
			if delErr := gcpService.DeleteSecret(env.SecretManagerProjectID, certificateID); delErr != nil {
				logger.Warnf("Failed to delete private key from secret manager for certificate %s: %v", certificateID, delErr)
			}
		}

		done := common.RemoveFromCertAuthCache(certificateID)
		if !done {
			logger.Errorf("Failed to remove certificate %s from cache", certificateID)
		}
		return nil
	}

	// Successfully got certificate and secret; revoke certificate and delete secret.
	err = DeleteCertificateAndSecret(gcpService, certificate, secret, poolCredentials)
	if err != nil {
		logger.Errorf("Failed to delete certificate and private key for certificateID: %s, err: %v", certificateID, err)
		return err
	}

	done := common.RemoveFromCertAuthCache(certificateID)
	if !done {
		logger.Errorf("Failed to remove certificate %s from cache", certificateID)
	}
	return nil
}

// _revokeCertificateFromCAS schedules the OCI-managed cluster
// certificate for deletion in OCI Certificates Service
// and evicts it from the in-memory cert-auth cache.
//
// Idempotency:
//
//	The function tolerates the three "nothing to do" shapes that the OCI
//	pool delete flow can legitimately produce:
//	  1. poolCredentials.ExternalCertificate == nil — pool create failed
//	     before the cert handle was persisted; nothing was ever provisioned.
//	  2. ExternalIdentifier == "" — same shape as (1) but with a partially
//	     populated ref.
//	  3. Cert already 404 / SCHEDULING_DELETION / PENDING_DELETION /
//	     DELETING / DELETED in OCI — absorbed by OciServices.DeleteCertificate
//	     itself, which returns nil for these states.
//	All three return nil so the surrounding Temporal activity stays
//	re-runnable without surfacing spurious failures.
//
// Cache eviction is best-effort: a missing entry is logged at debug and the
// activity continues — the OCI-side deletion is already scheduled at that
// point and any stale cache entry would be filtered by the lifecycle state
// on the next certificate lookup anyway.
func _revokeCertificateFromCAS(ociService *oci.OciServices, poolCredentials *datamodel.PoolCredentials) error {
	if ociService == nil {
		return errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_revokeCertificateFromCAS: ociService must not be nil"))
	}
	logger := ociService.GetLogger()

	if poolCredentials == nil {
		return errors.NewVCPError(errors.ErrInputValidationError,
			errors2.New("_revokeCertificateFromCAS: poolCredentials must not be nil"))
	}

	if poolCredentials.ExternalCertificate == nil ||
		poolCredentials.ExternalCertificate.ExternalIdentifier == "" {
		logger.Infof("RevokeCertificateFromCAS: no persisted OCI certificate handle on poolCredentials — skipping revoke")
		return nil
	}

	certOCID := poolCredentials.ExternalCertificate.ExternalIdentifier
	certName := poolCredentials.ExternalCertificate.Name

	if err := ociService.DeleteCertificate(certOCID); err != nil {
		logger.Errorf("Failed to schedule OCI certificate deletion — certName: %s, certOCID: %s, err: %v",
			certName, certOCID, err)
		return err
	}

	if certName != "" {
		if !common.RemoveFromCertAuthCache(certName) {
			logger.Debugf("RevokeCertificateFromCAS: certificate %s not present in auth cache, nothing to evict", certName)
		}
	}
	return nil
}

// _generateCSR generates a Certificate Signing Request (CSR) with the specified common name and domains.
func _generateCSR(commonName string, domains []string, isServerAuthEnabled bool) ([]byte, *rsa.PrivateKey, error) {
	// Generate an RSA private key.
	key, err := rsa.GenerateKey(rand.Reader, env.PrivateKeyBits)
	if err != nil {
		return nil, nil, err
	}

	// Build Key Usage extension. We want DigitalSignature and KeyEncipherment set.
	keyUsageVal := DigitalSignature | KeyEncipherment // Should be 0x80 | 0x20 = 0xA0 (10100000)

	// Create the ASN.1 BIT STRING for key usage.
	bitString := asn1.BitString{
		Bytes:     []byte{byte(keyUsageVal)},
		BitLength: 8, // We are encoding one full byte.
	}
	rawKeyUsage, err := asn1.Marshal(bitString)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal key usage: %s", err.Error())
	}

	// --- Build Extended Key Usage extension ---
	// We want clientAuth for all certificates, and serverAuth for VCP_ADMIN_CERT_UN_SUFFIX certificates.
	ekuOIDs := []asn1.ObjectIdentifier{
		{1, 3, 6, 1, 5, 5, 7, 3, 2},
	}

	// If it is VCP_ADMIN_CERT_UN_SUFFIX, add serverAuth as well.
	if isServerAuthEnabled {
		ekuOIDs = append(ekuOIDs, asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 1})
	}
	rawEKU, err := asn1.Marshal(ekuOIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal extended key usage: %v", err)
	}

	// Prepare the extensions.
	extensions := []pkix.Extension{
		{
			Id:       asn1.ObjectIdentifier{2, 5, 29, 15}, // Key Usage
			Critical: true,
			Value:    rawKeyUsage,
		},
		{
			Id:       asn1.ObjectIdentifier{2, 5, 29, 37}, // Extended Key Usage
			Critical: false,
			Value:    rawEKU,
		},
	}

	// Build the certificate request template.
	template := x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Netapp"},
		},
		SignatureAlgorithm: x509.SHA256WithRSA,
		ExtraExtensions:    extensions,
		DNSNames:           domains,
	}

	// Create the CSR in DER format.
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &template, key)
	if err != nil {
		return nil, nil, err
	}

	return csrDER, key, nil
}

// ontapCertSubjectOrganization is the X.509 Subject "O" (Organization) used for
// all VSA-cluster certificates. It mirrors the value baked into the GCP CSR
// template in _generateCSR (Subject.Organization = []string{"Netapp"}).
const ontapCertSubjectOrganization = "Netapp"

const CsrType = "CERTIFICATE REQUEST"

const RsaKeyType = "RSA PRIVATE KEY"

const DigitalSignature = 0x80 // 10000000 in binary (bit 0)

const KeyEncipherment = 0x20

var CreateNodeForProvider = _createNodeForProvider

// _createNodeForProvider creates a node for a given provider using the provided information.
func _createNodeForProvider(inp NodeProviderInput) *models.Node {
	if inp.OntapCredentials == nil {
		// This should not happen in practice, but handle gracefully
		return &models.Node{
			DeploymentName: inp.DeploymentName,
		}
	}

	// Populate CA URI from OntapCredentials, fall back to environment variables if not set
	caURI := inp.OntapCredentials.GetCaURIWithFallback()

	endpointAddressToHostNameMap := make(map[string]string)
	if inp.OntapCredentials != nil && inp.OntapCredentials.AuthType == env.USER_CERTIFICATE {
		for _, node := range inp.Nodes {
			if node.EndpointAddress != "" {
				endpointAddressToHostNameMap[node.EndpointAddress] = node.HostDNSName
			}
		}
		return &models.Node{
			EndpointAddressesToHostNameMap: endpointAddressToHostNameMap,
			Password:                       inp.OntapCredentials.Password, // Include password for SSH authentication
			DeploymentName:                 inp.DeploymentName,
			CertificateID:                  inp.OntapCredentials.CertificateID,
			SecretID:                       inp.OntapCredentials.SecretID,
			AuthType:                       inp.OntapCredentials.AuthType,
			CaURI:                          caURI,
		}
	}

	for _, node := range inp.Nodes {
		if node.EndpointAddress != "" {
			endpointAddressToHostNameMap[node.EndpointAddress] = node.EndpointAddress
		}
	}

	return &models.Node{
		EndpointAddressesToHostNameMap: endpointAddressToHostNameMap,
		Password:                       inp.OntapCredentials.Password,
		DeploymentName:                 inp.DeploymentName,
		SecretID:                       inp.OntapCredentials.SecretID,
		AuthType:                       inp.OntapCredentials.AuthType,
		CaURI:                          caURI,
	}
}

type NodeProviderInput struct {
	Nodes          []*datamodel.Node
	DeploymentName string
	// Required: OntapCredentials from database (contains Password, SecretID, CertificateID, AuthType, and CA fields with env var fallback)
	OntapCredentials *datamodel.PoolCredentials
}

// GetOntapNode resolves an ONTAP provider node from pool DB nodes, matching workflow usage:
// CommonActivities.GetNode (GetNodesByPoolID) then CreateNodeForProvider.
func GetOntapNode(ctx context.Context, se database.Storage, volume *datamodel.Volume) (*models.Node, error) {
	if volume == nil || volume.Pool == nil {
		return nil, errors2.NewUserInputValidationErr("volume pool is required to resolve ONTAP node")
	}
	dbNodes, err := se.GetNodesByPoolID(ctx, volume.Pool.ID)
	if err != nil {
		return nil, err
	}
	if len(dbNodes) == 0 {
		return nil, errors.NewVCPError(
			errors.ErrUnexpectedNodeCountForPool,
			fmt.Errorf("no nodes found for pool %s", volume.Pool.UUID),
		)
	}
	return CreateNodeForProvider(NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   volume.Pool.DeploymentName,
		OntapCredentials: volume.Pool.PoolCredentials,
	}), nil
}

// PrepareOperationID constructs a GCP operation ID from the provided project number, location ID, and job ID.
func PrepareOperationID(projectNumber, locationId, jobId string) string {
	if projectNumber == "" || locationId == "" || jobId == "" {
		return ""
	}
	return "/v1beta/projects/" + projectNumber + "/locations/" + locationId + "/operations/" + jobId
}
