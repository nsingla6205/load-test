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
	nodes, err := clusterNodesFromLayout(layout)
	if err != nil {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: %v", datamodel.ErrInvalidConfig, err)
	}
	if !seed.DeploymentId.IsSet() || seed.DeploymentId.Value == "" {
		return vsa.NodeProviderInput{}, fmt.Errorf("%w: VCM deploymentId is required", datamodel.ErrInvalidConfig)
	}
	input := vsa.NodeProviderInput{
		Nodes:            nodes,
		DeploymentName:   seed.DeploymentId.Value,
		OntapCredentials: creds,
	}
	applyVCMCertAuthNodeDialWorkaround(&input)
	return input, nil
}

// applyVCMCertAuthNodeDialWorkaround mirrors the VCM create-pool ONTAP dial path.
// VCM has no Cloud DNS records for per-node mgmt FQDNs, so cert-auth REST calls dial
// the nodemgmt IP directly (HostDNSName = IP) and skip server-cert verification.
// TODO(vcm-dns): remove once DNS / SNI handling lands.
func applyVCMCertAuthNodeDialWorkaround(input *vsa.NodeProviderInput) {
	if input == nil || input.OntapCredentials == nil || input.OntapCredentials.AuthType != env.USER_CERTIFICATE {
		return
	}
	input.InsecureSkipVerify = true
	for _, node := range input.Nodes {
		if node == nil || node.EndpointAddress == "" {
			continue
		}
		node.HostDNSName = node.EndpointAddress
	}
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
	switch effectiveAuthType(entry) {
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
// effective auth type (see effectiveAuthType) so every consumer of the stored
// SetConfig agrees on which secret material is used.
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
	// Only the material for the effective auth type is carried over, so
	// downstream consumers derive the same auth type from the fields present.
	switch effectiveAuthType(credential) {
	case vcmserver.OntapCredentialV1ActiveAuthTypePWD:
		pwd := credential.PasswordAuthCreds.Value
		mapped.SecretID = pwd.PasswordSecretId.Or("")
		mapped.Username = pwd.Username.Or("")
	default: // CERT (default when unset for non-admin roles)
		cert := credential.CertificateAuthCreds.Value
		mapped.CertificateID = cert.CertSecretId.Or("")
		mapped.SecretID = cert.CertPrivateKeySecretId.Or("")
	}
	return mapped
}

// effectiveAuthType returns the auth type VCM must use to resolve a credential.
//
// The ADMIN role is password-only: the ONTAP admin/bootstrap user is provisioned
// for password authentication, so an ADMIN credential is always resolved via its
// passwordAuthCreds regardless of the declared activeAuthType. Every other role
// honors the credential's activeAuthType (CERT or PWD), defaulting to CERT when
// unset.
func effectiveAuthType(credential vcmserver.OntapCredentialV1) vcmserver.OntapCredentialV1ActiveAuthType {
	if credential.Role == vcmserver.OntapCredentialV1RoleADMIN {
		return vcmserver.OntapCredentialV1ActiveAuthTypePWD
	}
	return activeAuthType(credential)
}

// validateOntapCredentials rejects a credential whose role is unset or whose
// effective auth type is not backed by the material needed to resolve it.
//
// The role is mandatory: it drives effectiveAuthType (ADMIN is password-only)
// and internalCredentialType, so an unset role would silently resolve as the
// wrong kind of credential downstream.
//
// This has to be checked up front because the declared auth type is not carried
// past mapOntapCredential: consumers re-derive it from which identifier is
// populated. An incomplete credential would therefore resolve as the wrong type
// instead of failing - a CERT entry missing certSecretId, for instance, leaves
// only the private-key secret set and would be dereferenced as a password.
func validateOntapCredentials(creds []vcmserver.OntapCredentialV1) error {
	for _, credential := range creds {
		role := string(credential.Role)
		if role == "" {
			return fmt.Errorf("VCM ontapCredentials: role is required and must be one of %v", vcmserver.OntapCredentialV1Role("").AllValues())
		}
		switch effectiveAuthType(credential) {
		case vcmserver.OntapCredentialV1ActiveAuthTypePWD:
			if credential.PasswordAuthCreds.Value.PasswordSecretId.Or("") == "" {
				return fmt.Errorf("VCM ontapCredentials[role=%s]: passwordAuthCreds.passwordSecretId is required to resolve this credential via password authentication", role)
			}
		default: // CERT
			if credential.CertificateAuthCreds.Value.CertSecretId.Or("") == "" {
				return fmt.Errorf("VCM ontapCredentials[role=%s]: certificateAuthCreds.certSecretId is required when activeAuthType is CERT", role)
			}
		}
	}
	return nil
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

// mediatorNodeName is the layout node name reserved for the mediator VM.
const mediatorNodeName = "mediator"

// clusterNodesFromLayout resolves the ONTAP management endpoints from the
// per-node layout. The mediator is skipped: it runs no ONTAP instance and so
// serves no management API.
func clusterNodesFromLayout(layout vcmserver.ClusterLayoutV1) ([]*datamodel.Node, error) {
	if len(layout.Nodes) == 0 {
		return nil, fmt.Errorf("VCM runtime layout has no nodes; cannot resolve ONTAP management endpoints")
	}
	nodes := make([]*datamodel.Node, 0, len(layout.Nodes))
	for _, node := range layout.Nodes {
		name := node.GceVmName.Or("")
		if name == "" || name == mediatorNodeName {
			continue
		}
		endpoint := nodeManagementEndpoint(node)
		if endpoint == "" {
			continue
		}
		nodes = append(nodes, &datamodel.Node{
			Name:            name,
			EndpointAddress: endpoint,
		})
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("VCM runtime layout has no node management IP for cluster nodes")
	}
	return nodes, nil
}

// nodeManagementEndpoint prefers the node-level management IP and falls back to
// the node-management LIF, which carries the same address.
func nodeManagementEndpoint(node vcmserver.VsaNodeV1) string {
	if ip := node.NodeManagementIp.Or(""); ip != "" {
		return ip
	}
	if ipConfig, ok := node.IpConfig.Get(); ok {
		return ipConfig.NodemgmtLifIp.Or("")
	}
	return ""
}
