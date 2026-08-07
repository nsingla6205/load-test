package config

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// NodeProviderInput builds vsa.NodeProviderInput from the stored SetConfig.
func (s *Store) NodeProviderInput() (vsa.NodeProviderInput, error) {
	setConfig := s.CurrentSetConfig()
	creds, err := poolCredentialsFromSetConfig(setConfig.OntapCredentials)
	if err != nil {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: %v", datamodel.ErrInvalidConfig, err)
	}
	nodes, err := clusterNodesFromIpConfig(setConfig.IpConfig)
	if err != nil {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: %v", datamodel.ErrInvalidConfig, err)
	}
	if !setConfig.DeploymentId.IsSet() || setConfig.DeploymentId.Value == "" {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: VCM deploymentId is required", datamodel.ErrInvalidConfig)
	}
	return vsa.NodeProviderInput{
		Nodes:            nodes,
		DeploymentName:   setConfig.DeploymentId.Value,
		OntapCredentials: creds,
	}, nil
}

func poolCredentialsFromSetConfig(creds []vcmserver.OntapCredentialsV1) (*datamodel.PoolCredentials, error) {
	entry, err := selectOntapCredentials(creds)
	switch env.AuthType {
	case env.USER_CERTIFICATE:
		if err != nil {
			return nil, err
		}
		secretID := entry.SecretId.Or("")
		if secretID == "" {
			return nil, fmt.Errorf("VCM ontapCredentials.secretId is required when VSA_AUTH_TYPE is certificate")
		}
		certificateID := entry.CertificateId.Or("")
		if certificateID == "" {
			return nil, fmt.Errorf("VCM ontapCredentials.certificateId is required when VSA_AUTH_TYPE is certificate")
		}
		return &datamodel.PoolCredentials{
			SecretID:      secretID,
			CertificateID: certificateID,
			Password:      "",
			AuthType:      env.USER_CERTIFICATE,
			CaURI:         env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName),
		}, nil
	case env.USERNAME_PWD_SEC_MGR:
		if err != nil {
			return nil, err
		}
		secretID := entry.SecretId.Or("")
		if secretID == "" {
			return nil, fmt.Errorf("VCM ontapCredentials.secretId is required when VSA_AUTH_TYPE is secret manager")
		}
		return &datamodel.PoolCredentials{
			SecretID:      secretID,
			CertificateID: "",
			Password:      "",
			AuthType:      env.USERNAME_PWD_SEC_MGR,
		}, nil
	default: // USERNAME_PWD
		if env.NodePassword == "" {
			return nil, fmt.Errorf("VSA_NODE_PASSWORD is required when VSA_AUTH_TYPE is username/password")
		}
		return &datamodel.PoolCredentials{
			SecretID:      "",
			CertificateID: "",
			Password:      env.NodePassword,
			AuthType:      env.USERNAME_PWD,
		}, nil
	}
}

// selectOntapCredentials prefers the "default" credential entry, else the first.
func selectOntapCredentials(creds []vcmserver.OntapCredentialsV1) (vcmserver.OntapCredentialsV1, error) {
	if len(creds) == 0 {
		return vcmserver.OntapCredentialsV1{}, fmt.Errorf("VCM config has no ontapCredentials")
	}
	for _, entry := range creds {
		if entry.Type == vcmserver.OntapCredentialsV1TypeDefault || entry.Type == "" {
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
	nodes := make([]*datamodel.Node, 0, len(ipConfig.Lifs))
	for _, lif := range ipConfig.Lifs {
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
