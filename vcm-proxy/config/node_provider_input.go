package config

import (
	"fmt"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// NodeProviderInput builds vsa.NodeProviderInput from the stored SetConfig.
func (s *Store) NodeProviderInput() (vsa.NodeProviderInput, error) {
	setConfig := s.CurrentSetConfig()
	seed, layout, err := seedAndLayout(setConfig)
	if err != nil {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: %v", datamodel.ErrInvalidConfig, err)
	}
	creds, err := poolCredentialsFromSetConfig(seed.OntapCredentials)
	if err != nil {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: %v", datamodel.ErrInvalidConfig, err)
	}
	nodes, err := clusterNodesFromIpConfig(layout.IpConfig)
	if err != nil {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: %v", datamodel.ErrInvalidConfig, err)
	}
	if !seed.DeploymentId.IsSet() || seed.DeploymentId.Value == "" {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: VCM deploymentId is required", datamodel.ErrInvalidConfig)
	}
	return vsa.NodeProviderInput{
		Nodes:            nodes,
		DeploymentName:   seed.DeploymentId.Value,
		OntapCredentials: creds,
	}, nil
}

func seedAndLayout(config vcmserver.SetConfigRequestV1) (vcmserver.SeedConfigV1, vcmserver.ClusterLayoutV1, error) {
	if !config.ClusterConfig.IsSet() {
		return vcmserver.SeedConfigV1{}, vcmserver.ClusterLayoutV1{}, fmt.Errorf("VCM clusterConfig is not set")
	}
	clusterConfig := config.ClusterConfig.Value
	var seed vcmserver.SeedConfigV1
	if clusterConfig.SeedConfig.IsSet() {
		seed = clusterConfig.SeedConfig.Value
	}
	var layout vcmserver.ClusterLayoutV1
	if clusterConfig.ActualLayout.IsSet() {
		layout = clusterConfig.ActualLayout.Value
	}
	return seed, layout, nil
}

func poolCredentialsFromSetConfig(creds []vcmserver.OntapCredentialV1) (*datamodel.PoolCredentials, error) {
	entry, err := selectOntapCredentials(creds)
	if err != nil {
		return nil, err
	}
	mapped := mapOntapCredential(entry)
	switch activeAuthType(entry) {
	case vcmserver.OntapCredentialV1ActiveAuthTypePWD:
		if mapped.SecretID == "" {
			return nil, fmt.Errorf("VCM ontapCredentials.passwordAuthCreds.passwordSecretId is required when activeAuthType is PWD")
		}
		return &datamodel.PoolCredentials{
			SecretID: mapped.SecretID,
			Username: mapped.Username,
			AuthType: env.USERNAME_PWD_SEC_MGR,
		}, nil
	default: // CERT (default when unset)
		if mapped.CertificateID == "" {
			return nil, fmt.Errorf("VCM ontapCredentials.certificateAuthCreds.certSecretId is required when activeAuthType is CERT")
		}
		if mapped.SecretID == "" {
			return nil, fmt.Errorf("VCM ontapCredentials.certificateAuthCreds.certPrivateKeySecretId is required when activeAuthType is CERT")
		}
		return &datamodel.PoolCredentials{
			SecretID:      mapped.SecretID,
			CertificateID: mapped.CertificateID,
			AuthType:      env.USER_CERTIFICATE,
			CaURI:         env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName),
		}, nil
	}
}

// ToVCMOntapCredentials maps OntapCredential_v1 entries onto workflow credential
// identifiers, selecting CertificateID vs SecretID from each credential's
// activeAuthType so every consumer of the stored SetConfig agrees on which
// secret material is used.
func ToVCMOntapCredentials(creds []vcmserver.OntapCredentialV1) []commonparams.VCMOntapCredential {
	if len(creds) == 0 {
		return nil
	}
	out := make([]commonparams.VCMOntapCredential, 0, len(creds))
	for _, credential := range creds {
		out = append(out, mapOntapCredential(credential))
	}
	return out
}

func mapOntapCredential(credential vcmserver.OntapCredentialV1) commonparams.VCMOntapCredential {
	mapped := commonparams.VCMOntapCredential{
		Type: internalCredentialType(credential.Role),
	}
	switch activeAuthType(credential) {
	case vcmserver.OntapCredentialV1ActiveAuthTypePWD:
		pwd := credential.PasswordAuthCreds.Value
		mapped.SecretID = pwd.PasswordSecretId.Or("")
		mapped.Username = pwd.Username.Or("")
	default: // CERT (default when unset)
		cert := credential.CertificateAuthCreds.Value
		mapped.CertificateID = cert.CertSecretId.Or("")
		mapped.SecretID = cert.CertPrivateKeySecretId.Or("")
	}
	return mapped
}

// activeAuthType returns the credential's declared auth type, defaulting to CERT
// when unset.
func activeAuthType(credential vcmserver.OntapCredentialV1) vcmserver.OntapCredentialV1ActiveAuthType {
	if authType, ok := credential.ActiveAuthType.Get(); ok {
		return authType
	}
	return vcmserver.OntapCredentialV1ActiveAuthTypeCERT
}

func internalCredentialType(role vcmserver.OntapCredentialV1Role) string {
	switch role {
	case vcmserver.OntapCredentialV1RoleADMIN:
		return "admin"
	case vcmserver.OntapCredentialV1RoleVCMADMIN:
		return "default"
	case vcmserver.OntapCredentialV1RoleONTAPMODE:
		return "ontap-mode"
	default:
		return ""
	}
}

// selectOntapCredentials prefers the VCM_ADMIN credential entry (the default
// credential VCM uses to talk to ONTAP), else the first.
func selectOntapCredentials(creds []vcmserver.OntapCredentialV1) (vcmserver.OntapCredentialV1, error) {
	if len(creds) == 0 {
		return vcmserver.OntapCredentialV1{}, fmt.Errorf("VCM config has no ontapCredentials")
	}
	for _, entry := range creds {
		if entry.Role == vcmserver.OntapCredentialV1RoleVCMADMIN || entry.Role == "" {
			return entry, nil
		}
	}
	return creds[0], nil
}

func clusterNodesFromIpConfig(opt vcmserver.OptIpConfigV1) ([]*datamodel.Node, error) {
	if !opt.IsSet() {
		return nil, fmt.Errorf("VCM runtime ipConfig is not set; cannot resolve ONTAP management endpoints")
	}
	ipConfig := opt.Value
	nodes := make([]*datamodel.Node, 0, len(ipConfig.VmLifs))
	for _, lif := range ipConfig.VmLifs {
		name := lif.Name.Or("")
		if name == "" || name == "mediator" {
			continue
		}
		endpoint := lif.Nodemgmt.Or("")
		if endpoint == "" {
			continue
		}
		nodes = append(nodes, &datamodel.Node{
			Name:            name,
			EndpointAddress: endpoint,
		})
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("VCM ipConfig has no nodemgmt LIF for cluster nodes")
	}
	return nodes, nil
}
