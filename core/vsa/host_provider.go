package vsa

import (
	"context"
	"fmt"
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
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

// GetProviderForHost builds an ONTAP REST provider for a hardware host using admin credentials.
func GetProviderForHost(ctx context.Context, host *dbdatamodel.Host) (Provider, error) {
	conn, err := resolveHostConnection(host)
	if err != nil {
		return nil, err
	}

	decrypted, err := utils.DecryptPassword(log.Secret(host.Password))
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("failed to decrypt host admin password: %w", err))
	}

	adminUsername := strings.TrimSpace(host.Username)
	if adminUsername == "" {
		adminUsername = "admin"
	}

	return newProviderForHostConnection(ctx, conn, adminUsername, *decrypted), nil
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

var getProviderForHostServiceUser = GetProviderForHostServiceUser
