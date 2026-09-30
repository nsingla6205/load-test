package prepoolinfra

import (
	"fmt"
	"log/slog"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vcpDataModel "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vcpUtils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func _generatePasswordForVSACluster(gcpService *google.GcpServices, secretID, password string) (*models.CustomSecret, error) {
	var secret *models.CustomSecret
	var err error
	projectID := env.SecretManagerProjectID
	secret, err = gcpService.GetSecretWithLatestVersion(projectID, secretID)
	if err != nil {
		return nil, err
	}
	if secret == nil {
		if password == "" {
			password, err = vcpUtils.GenerateStrongPassword(12)
			if err != nil {
				return nil, err
			}
		}
		secret, err = gcpService.CreateSecret(projectID, env.Region, secretID, password)
		if err != nil {
			return nil, err
		}
	}
	return secret, nil
}

func (p *provisioner) createONTAPCredentials(deploymentName string) (vlm.OntapCredentials, error) {
	credentials := &vlm.OntapCredentials{}
	svc := p.GCPService
	poolCredentials := &vcpDataModel.PoolCredentials{}

	userName := vcpUtils.GenerateUniqueUsername(deploymentName)
	switch env.AuthType {
	case env.USER_CERTIFICATE:
		poolCredentials = &vcpDataModel.PoolCredentials{
			SecretID:      fmt.Sprintf("%s-secret", deploymentName),
			CertificateID: fmt.Sprintf("%s-cert", deploymentName),
			Password:      "",
			AuthType:      env.USER_CERTIFICATE,
			CaURI:         env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName),
			Username:      fmt.Sprintf("%s%s", userName, VCP_ADMIN_CERT_UN_SUFFIX),
		}
	case env.USERNAME_PWD_SEC_MGR:
		poolCredentials = &vcpDataModel.PoolCredentials{
			SecretID:      fmt.Sprintf("%s-secret", deploymentName),
			CertificateID: "",
			Password:      "",
			AuthType:      env.USERNAME_PWD_SEC_MGR,
			Username:      AdminUserName,
		}
	default:
		poolCredentials = &vcpDataModel.PoolCredentials{
			SecretID:      "",
			CertificateID: "",
			Password:      env.NodePassword,
			AuthType:      env.USERNAME_PWD,
			Username:      AdminUserName,
		}
	}

	switch env.AuthType {
	case env.USER_CERTIFICATE:
		certificate, err := vsa.GenerateAndCreateCertificateForVSACluster(svc, deploymentName, poolCredentials.Username, poolCredentials, EnableServerAuthInCSR)
		if err != nil {
			return vlm.OntapCredentials{}, fmt.Errorf("generate and create certificate for VSA cluster: %w", err)
		}
		credentials = setPoolCredentials(certificate)
		fallthrough
	case env.USERNAME_PWD_SEC_MGR:
		secret, err := _generatePasswordForVSACluster(svc, poolCredentials.SecretID, env.NodePassword)
		if err != nil {
			return vlm.OntapCredentials{}, fmt.Errorf("generate password for VSA cluster: %w", err)
		}
		credentials.AdminPassword = secret.SecretVersion.Value
	default:
		credentials.AdminPassword = poolCredentials.Password
	}
	return *credentials, nil
}

func (p *provisioner) deleteONTAPCredentials(deploymentName string) error {
	poolCredentials := &vcpDataModel.PoolCredentials{}

	switch env.AuthType {
	case env.USER_CERTIFICATE:
		poolCredentials = &vcpDataModel.PoolCredentials{
			SecretID:      fmt.Sprintf("%s-secret", deploymentName),
			CertificateID: fmt.Sprintf("%s-cert", deploymentName),
			AuthType:      env.USER_CERTIFICATE,
			CaURI:         env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName),
		}
	case env.USERNAME_PWD_SEC_MGR:
		poolCredentials = &vcpDataModel.PoolCredentials{
			SecretID: fmt.Sprintf("%s-secret", deploymentName),
			AuthType: env.USERNAME_PWD_SEC_MGR,
		}
	default:
		slog.Info("no ONTAP credentials to delete for default password type", "deploymentName", deploymentName)
		return nil
	}

	switch env.AuthType {
	case env.USER_CERTIFICATE:
		err := vsa.RevokeCertificateAndDeleteFromCacheAndSecretManager(p.GCPService, poolCredentials)
		if err != nil {
			return fmt.Errorf("revoke certificate and delete from secret manager: %w", err)
		}
		slog.Info("ONTAP certificate revoked", "certificateID", poolCredentials.CertificateID)
		fallthrough
	case env.USERNAME_PWD_SEC_MGR:
		err := vsa.DeletePasswordFromCacheAndSecretManager(p.GCPService, poolCredentials.SecretID)
		if err != nil {
			return fmt.Errorf("delete password from secret manager: %w", err)
		}
		slog.Info("ONTAP password secret deleted", "secretID", poolCredentials.SecretID)
	}

	return nil
}

func setPoolCredentials(certificate *models.CustomCertificateResponse) *vlm.OntapCredentials {
	credentials := &vlm.OntapCredentials{}
	credentials.Certificate.CommonName = certificate.Certificate.SubjectCommonName
	credentials.Certificate.Certificate = certificate.Certificate.PemCertificate
	credentials.Certificate.PrivateKey = certificate.Secret.SecretVersion.Value
	credentials.Certificate.InterMediateCertificate = certificate.Certificate.PemCertificateChain
	return credentials
}
