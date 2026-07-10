package vsa

import (
	"context"
	"fmt"
	"time"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// InstallServerCertificate installs a server certificate on the VSA cluster
func (rc *OntapRestProvider) InstallServerCertificate(params InstallServerCertificateParams) (*ServerCertificateResponse, error) {
	// Get the ONTAP client
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		rc.Logger.Errorf("Failed to get ONTAP client: %v", err)
		return nil, err
	}

	// Debug: Check if client and security client are properly initialized
	if client == nil {
		rc.Logger.Error("ONTAP client is nil")
		return nil, fmt.Errorf("ONTAP client is nil")
	}

	securityClient := client.Security()
	if securityClient == nil {
		rc.Logger.Error("Security client is nil")
		return nil, fmt.Errorf("Security client is nil")
	}

	rc.Logger.Debug("ONTAP client and security client initialized successfully")

	// Prepare the certificate installation parameters
	installParams := &ontapRest.ServerRootCAInstallParams{
		SvmName:         &params.SvmName,
		Certificate:     &params.Certificate,
		PrivateKey:      &params.PrivateKey,
		CertificateType: &params.CertificateType,
		CommonName:      &params.CommonName,
		Name:            &params.CertificateName,
	}

	// Install the certificate
	result, err := securityClient.ServerRootCACertificateInstall(installParams)
	if err != nil {
		return nil, err
	}

	// Check if result is nil (can happen if response has no records)
	if result == nil {
		rc.Logger.Error("ServerRootCACertificateInstall returned nil result without error")
		return nil, fmt.Errorf("certificate installation returned nil result")
	}

	// Convert the result to our response format
	response := &ServerCertificateResponse{
		UUID:            nillable.FromPointer(result.UUID),
		Name:            nillable.FromPointer(result.Name),
		CommonName:      nillable.FromPointer(result.CommonName),
		CertificateType: nillable.FromPointer(result.Type),
		ExpiryTime:      nillable.FromPointer(result.ExpiryTime),
		SerialNumber:    nillable.FromPointer(result.SerialNumber),
	}

	return response, nil
}

// InstallClientCA installs a client-ca trust certificate so ONTAP accepts client certs signed by that CA.
func (rc *OntapRestProvider) InstallClientCA(params InstallClientCAParams) (*ServerCertificateResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		rc.Logger.Errorf("Failed to get ONTAP client: %v", err)
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("ONTAP client is nil")
	}

	securityClient := client.Security()
	if securityClient == nil {
		return nil, fmt.Errorf("Security client is nil")
	}

	clientCAType := "client_ca"
	installParams := &ontapRest.ServerRootCAInstallParams{
		Certificate:     &params.Certificate,
		CertificateType: &clientCAType,
	}
	// Only scope to an SVM when a name is provided; when empty the client_ca is
	// installed at cluster (admin) scope, which is where the cluster-admin cert
	// login is validated.
	if params.SvmName != "" {
		installParams.SvmName = &params.SvmName
	}
	if params.CommonName != "" {
		installParams.CommonName = &params.CommonName
	}
	if params.CertificateName != "" {
		installParams.Name = &params.CertificateName
	}

	result, err := securityClient.ServerRootCACertificateInstall(installParams)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("client-ca installation returned nil result")
	}

	return &ServerCertificateResponse{
		UUID:            nillable.FromPointer(result.UUID),
		Name:            nillable.FromPointer(result.Name),
		CommonName:      nillable.FromPointer(result.CommonName),
		CertificateType: nillable.FromPointer(result.Type),
		ExpiryTime:      nillable.FromPointer(result.ExpiryTime),
		SerialNumber:    nillable.FromPointer(result.SerialNumber),
	}, nil
}

// GetServerCertificates retrieves server certificates from the VSA cluster
func (rc *OntapRestProvider) GetServerCertificates(params GetServerCertificateParams) ([]*ServerCertificateResponse, error) {
	// Get the ONTAP client
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		rc.Logger.Errorf("Failed to get ONTAP client: %v", err)
		return nil, err
	}

	// Debug: Check if client and security client are properly initialized
	if client == nil {
		rc.Logger.Error("ONTAP client is nil")
		return nil, fmt.Errorf("ONTAP client is nil")
	}

	securityClient := client.Security()
	if securityClient == nil {
		rc.Logger.Error("Security client is nil")
		return nil, fmt.Errorf("Security client is nil")
	}

	rc.Logger.Debug("ONTAP client and security client initialized successfully")

	// Prepare the certificate collection get parameters
	getParams := &ontapRest.ServerRootCAGetCollectionParams{
		SvmName:         &params.SvmName,
		Name:            &params.CertificateName,
		CertificateType: &params.CertificateType,
	}

	// Get the certificates
	results, err := securityClient.ServerRootCACertificateCollectionGet(getParams)
	if err != nil {
		return nil, err
	}

	// Convert the results to our response format
	responses := make([]*ServerCertificateResponse, len(results))
	for i, result := range results {
		responses[i] = &ServerCertificateResponse{
			UUID:            nillable.FromPointer(result.UUID),
			Name:            nillable.FromPointer(result.Name),
			CommonName:      nillable.FromPointer(result.CommonName),
			CertificateType: nillable.FromPointer(result.Type),
			ExpiryTime:      nillable.FromPointer(result.ExpiryTime),
			SerialNumber:    nillable.FromPointer(result.SerialNumber),
		}
	}

	return responses, nil
}

// DeleteServerCertificate deletes a server certificate from the VSA cluster,
// matching on name (and optionally serial number) within the given SVM.
func (rc *OntapRestProvider) DeleteServerCertificate(params DeleteServerCertificateParams) error {
	// Get the ONTAP client
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		rc.Logger.Errorf("Failed to get ONTAP client: %v", err)
		return err
	}

	if client == nil {
		rc.Logger.Error("ONTAP client is nil")
		return fmt.Errorf("ONTAP client is nil")
	}

	securityClient := client.Security()
	if securityClient == nil {
		rc.Logger.Error("Security client is nil")
		return fmt.Errorf("Security client is nil")
	}

	deleteParams := &ontapRest.SecurityCertificateDeleteCollectionParams{
		Name:    &params.CertificateName,
		SvmName: &params.SvmName,
		Type:    &params.CertificateType,
	}
	if params.SerialNumber != "" {
		deleteParams.SerialNumber = &params.SerialNumber
	}

	if err := securityClient.SecurityCertificateDeleteCollection(deleteParams); err != nil {
		return err
	}
	return nil
}

// ModifySSL modifies the SSL configuration for a VSA using SSH CLI
func (rc *OntapRestProvider) ModifySSL(params ModifySSLParams) (*ModifySSLResponse, error) {
	logger := rc.Logger
	logger.Debugf("Starting SSL modify operation for SVM: %s", params.SvmName)

	// Get SSH client parameters from the provider
	// Use default ONTAP username since ProviderDetails doesn't have Username field
	// NOTE: ONTAP SSH always requires password authentication, even for certificate-based REST API auth
	sshParams := ontapRest.SSHClientParams{
		Host:     rc.Provider.IPAddress,
		Username: "admin", // Default ONTAP admin username
		Password: rc.Provider.Password,
		AuthType: 0, // Always use password authentication for SSH (ONTAP requirement)
		Timeout:  30 * time.Second,
	}

	logger.Debug("Using password authentication for SSH (ONTAP requirement)")

	// Create SSH client
	sshClient, err := ontapRest.NewSSHClient(context.Background(), sshParams)
	if err != nil {
		logger.Errorf("Failed to create SSH client: %v", err)
		return &ModifySSLResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to create SSH client: %v", err),
		}, err
	}
	defer func() {
		if closeErr := sshClient.Close(); closeErr != nil {
			logger.Warnf("Failed to close SSH client: %v", closeErr)
		}
	}()

	// Construct the SSL modify command
	command := fmt.Sprintf("security ssl modify -vserver %s -server-enabled %t",
		params.SvmName, params.ServerEnabled)

	if params.CA != "" {
		command += fmt.Sprintf(" -ca %s", params.CA)
	}

	if params.Serial != "" {
		command += fmt.Sprintf(" -serial %s", params.Serial)
	}

	// Execute the command
	output, err := sshClient.ExecuteCommand(context.Background(), command)
	if err != nil {
		logger.Errorf("SSL modify command failed: %v, output: %s", err, output)
		return &ModifySSLResponse{
			Success: false,
			Message: fmt.Sprintf("SSL modify command failed: %v", err),
		}, err
	}

	return &ModifySSLResponse{
		Success: true,
		Message: "SSL configuration modified successfully via SSH",
	}, nil
}
