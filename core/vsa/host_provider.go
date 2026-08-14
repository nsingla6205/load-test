package vsa

import (
	"context"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	dbdatamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type hostConnectionParams struct {
	hostUUID           string
	mgmtIP             string
	protocol           string
	port               int
	useHTTPS           bool
	insecureSkipVerify bool
	hosts              map[string]string
}

func resolveHostConnection(host *dbdatamodel.Host) (*hostConnectionParams, error) {
	if host == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("host is required"))
	}

	mgmtIP := strings.TrimSpace(host.IPAddress)
	if mgmtIP == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("host %s has no management IP", host.UUID))
	}

	port := 0
	if host.Port != nil {
		port = *host.Port
	}
	protocol, normalizedPort, normErr := dbdatamodel.NormalizeExternalClusterProtocolAndPort(host.Protocol, port)
	if normErr != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, normErr)
	}

	useHTTPS := protocol != dbdatamodel.ExternalClusterProtocolHTTP
	insecureSkipVerify := protocol == dbdatamodel.ExternalClusterProtocolInsecureHTTPS
	// Hosts values are used as the REST client hostname; map management IP to itself so we
	// never dial a non-resolvable host label stored in the hosts table.
	hosts := map[string]string{mgmtIP: mgmtIP}

	return &hostConnectionParams{
		hostUUID:           host.UUID,
		mgmtIP:             mgmtIP,
		protocol:           protocol,
		port:               normalizedPort,
		useHTTPS:           useHTTPS,
		insecureSkipVerify: insecureSkipVerify,
		hosts:              hosts,
	}, nil
}

func resolveHostCertificateConnection(host *dbdatamodel.Host) (*hostConnectionParams, error) {
	conn, err := resolveHostConnection(host)
	if err != nil {
		return nil, err
	}
	if conn.protocol == dbdatamodel.ExternalClusterProtocolHTTP {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("host %s must use HTTPS for certificate authentication", host.UUID))
	}

	externalClusterName := strings.TrimSpace(host.ExternalName)
	if externalClusterName == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("host %s external cluster name is required for certificate authentication", host.UUID))
	}
	if strings.Trim(strings.TrimSpace(env.VsaDeployedDnsName), ".") == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("VSA deployed DNS name is required for certificate authentication"))
	}

	// Same DNS hostname as CreateHostCloudDNSRecord / BuildHostCloudDNSRecordName
	// (without the trailing Cloud DNS FQDN dot).
	tlsName := orchcommon.BuildHostManagementTLSName(externalClusterName)
	conn.protocol = dbdatamodel.ExternalClusterProtocolHTTPS
	conn.useHTTPS = true
	conn.insecureSkipVerify = false
	conn.hosts = map[string]string{conn.mgmtIP: tlsName}
	return conn, nil
}

func newProviderForHostConnection(
	ctx context.Context,
	conn *hostConnectionParams,
	username, password string,
) Provider {
	util.GetLogger(ctx).Info("Creating ONTAP provider for host",
		"hostUUID", conn.hostUUID, "mgmtIP", conn.mgmtIP, "protocol", conn.protocol, "username", username)

	return NewProvider(ctx, ProviderDetails{
		IPAddress:          conn.mgmtIP,
		Hosts:              conn.hosts,
		Username:           username,
		Password:           password,
		Port:               &conn.port,
		UseHTTPS:           conn.useHTTPS,
		Protocol:           conn.protocol,
		InsecureSkipVerify: conn.insecureSkipVerify,
		AuthType:           env.USERNAME_PWD,
	})
}

// hostAdminCredentials decrypts the host admin password and resolves the username
// (defaulting to "admin" when unset).
func hostAdminCredentials(host *dbdatamodel.Host) (username, password string, err error) {
	decrypted, err := utils.DecryptPassword(log.Secret(host.Password))
	if err != nil {
		return "", "", vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("failed to decrypt host admin password: %w", err))
	}
	username = strings.TrimSpace(host.Username)
	if username == "" {
		username = "admin"
	}
	return username, *decrypted, nil
}

// CreateNodeForHost builds a models.Node suitable for GetProviderByNode from a
// hardware host (PO / extreme ONTAP-mode pools). The password is decrypted so
// AuthType USERNAME_PWD can be used directly.
func CreateNodeForHost(host *dbdatamodel.Host) (*models.Node, error) {
	conn, err := resolveHostConnection(host)
	if err != nil {
		return nil, err
	}
	username, password, err := hostAdminCredentials(host)
	if err != nil {
		return nil, err
	}
	return &models.Node{
		EndpointAddress:                conn.mgmtIP,
		EndpointAddressesToHostNameMap: conn.hosts,
		Username:                       username,
		Password:                       password,
		AuthType:                       env.USERNAME_PWD,
	}, nil
}

// GetProviderForHost builds an ONTAP REST provider for a hardware host using admin credentials.
var GetProviderForHost = _getProviderForHost

func _getProviderForHost(ctx context.Context, host *dbdatamodel.Host) (Provider, error) {
	conn, err := resolveHostConnection(host)
	if err != nil {
		return nil, err
	}

	adminUsername, decrypted, err := hostAdminCredentials(host)
	if err != nil {
		return nil, err
	}

	return newProviderForHostConnection(ctx, conn, adminUsername, decrypted), nil
}

// GetProviderForHostServiceUser builds an ONTAP REST provider for a hardware host
// using the provisioned service-user credentials.
func GetProviderForHostServiceUser(
	ctx context.Context,
	host *dbdatamodel.Host,
	username, password string,
) (Provider, error) {
	conn, err := resolveHostConnection(host)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("service username and password are required"))
	}
	return newProviderForHostConnection(ctx, conn, username, password), nil
}

func newProviderForHostConnectionWithCertificate(
	ctx context.Context,
	conn *hostConnectionParams,
	username string,
	certificate *Certificate,
) Provider {
	util.GetLogger(ctx).Info("Creating ONTAP provider for host with certificate auth",
		"hostUUID", conn.hostUUID, "mgmtIP", conn.mgmtIP, "protocol", conn.protocol, "username", username)

	return NewProvider(ctx, ProviderDetails{
		IPAddress:          conn.mgmtIP,
		Hosts:              conn.hosts,
		Username:           username,
		Port:               &conn.port,
		UseHTTPS:           conn.useHTTPS,
		Protocol:           conn.protocol,
		InsecureSkipVerify: conn.insecureSkipVerify,
		AuthType:           env.USER_CERTIFICATE,
		Certificate:        certificate,
	})
}

// GetProviderForHostServiceUserWithCertificate builds an ONTAP REST provider for a hardware host
// using the provisioned service-user client certificate.
func GetProviderForHostServiceUserWithCertificate(
	ctx context.Context,
	host *dbdatamodel.Host,
	username string,
	poolCredentials *dbdatamodel.PoolCredentials,
) (Provider, error) {
	conn, err := resolveHostCertificateConnection(host)
	if err != nil {
		return nil, err
	}
	if username == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("service username is required"))
	}
	if poolCredentials == nil || poolCredentials.CertificateID == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("service certificate credentials are required"))
	}

	certificate, err := GetCertificateFromCacheOrSecretManager(ctx, poolCredentials)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError,
			fmt.Errorf("failed to load service user certificate: %w", err))
	}
	if certificate == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("service user certificate not found for %s", poolCredentials.CertificateID))
	}

	return newProviderForHostConnectionWithCertificate(ctx, conn, username, &Certificate{
		SignedCertificate:        certificate.SignedCertificate,
		PrivateKey:               certificate.PrivateKey,
		InterMediateCertificates: certificate.InterMediateCertificates,
		CommonName:               certificate.CommonName,
	}), nil
}

// hostSVMLister lists SVMs via GET /api/svm/svms (scoped by RBAC vserver modify).
type hostSVMLister interface {
	ListSVMs(params ListSvmsParams) ([]*ontapRest.Svm, error)
}

// VerifyHostServiceCredentials validates service-user credentials by listing SVMs
// scoped to svmPattern (GET /api/svm/svms). Success is an HTTP 200 from the SVM collection
// API using only the vserver-modify RBAC privileges granted to the service role.
func VerifyHostServiceCredentials(
	ctx context.Context,
	host *dbdatamodel.Host,
	svmPattern, username, password string,
) error {
	if strings.TrimSpace(svmPattern) == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("svm pattern is required for service credential verification"))
	}

	provider, err := getProviderForHostServiceUser(ctx, host, username, password)
	if err != nil {
		return err
	}

	svmLister, ok := provider.(hostSVMLister)
	if !ok {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("ONTAP provider does not support SVM list"))
	}

	namePattern := svmPattern
	svms, err := svmLister.ListSVMs(ListSvmsParams{NamePattern: &namePattern})
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("failed to verify host service credentials: %w", err))
	}

	util.GetLogger(ctx).Info("Verified host service credentials via GET /api/svm/svms",
		"hostUUID", host.UUID, "svmPattern", svmPattern, "svmCount", len(svms), "username", username)
	return nil
}

// VerifyHostServiceCredentialsWithCertificate validates service-user certificate credentials
// by listing SVMs scoped to svmPattern (GET /api/svm/svms).
func VerifyHostServiceCredentialsWithCertificate(
	ctx context.Context,
	host *dbdatamodel.Host,
	svmPattern, username string,
	poolCredentials *dbdatamodel.PoolCredentials,
) error {
	if strings.TrimSpace(svmPattern) == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("svm pattern is required for service credential verification"))
	}

	provider, err := getProviderForHostServiceUserWithCertificate(ctx, host, username, poolCredentials)
	if err != nil {
		return err
	}

	svmLister, ok := provider.(hostSVMLister)
	if !ok {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("ONTAP provider does not support SVM list"))
	}

	namePattern := svmPattern
	svms, err := svmLister.ListSVMs(ListSvmsParams{NamePattern: &namePattern})
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("failed to verify host service credentials with certificate: %w", err))
	}

	util.GetLogger(ctx).Info("Verified host service credentials with certificate via GET /api/svm/svms",
		"hostUUID", host.UUID, "svmPattern", svmPattern, "svmCount", len(svms), "username", username)
	return nil
}

var (
	getProviderForHostServiceUser                = GetProviderForHostServiceUser
	getProviderForHostServiceUserWithCertificate = GetProviderForHostServiceUserWithCertificate
)
