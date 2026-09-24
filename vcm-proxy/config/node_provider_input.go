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
	applyVCMCertAuthNodeSNI(&input)
	return input, nil
}

// applyVCMCertAuthNodeSNI mirrors the VCM create-pool ONTAP dial path. VCM has
// no Cloud DNS records for per-node mgmt FQDNs, so cert-auth REST calls dial the
// nodemgmt IP directly and use an SNI override for server-cert verification: the
// TLS SNI / hostname is set to "mgmt.<deploymentId>.<VsaDeployedDnsName>", which
// matches the wildcard SAN on the pool certificate. Full mTLS with server-cert
// verification stays intact (no InsecureSkipVerify). Password auth is untouched.
func applyVCMCertAuthNodeSNI(input *vsa.NodeProviderInput) {
	if input == nil || input.OntapCredentials == nil || input.OntapCredentials.AuthType != env.USER_CERTIFICATE {
		return
	}
	input.ServerName = vsa.OntapSNIServerName(input.DeploymentName)
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

// Internal credential type labels used to tag the flattened VCMOntapCredential
// entries so downstream selection (commonparams.DeriveOntapCredentials) can pick
// the right one. Kept in sync with the constants in core/orchestrator/common.
const (
	vcmCredentialTypeAdmin     = "admin"      // built-in ONTAP admin user (adminPassSecretId)
	vcmCredentialTypeDefault   = "default"    // PURPOSE_VCM_ADMIN (defaultModeUserCredentials)
	vcmCredentialTypeOntapMode = "ontap-mode" // PURPOSE_ONTAP_MODE (ontapModeUserCredentials)
)

// poolCredentialsFromSetConfig builds the datamodel.PoolCredentials VCM uses to
// talk to ONTAP during pool create. The default-mode user (PURPOSE_VCM_ADMIN) is
// the identity VCM authenticates as; adminPassSecretId supplies the built-in
// admin password used to bootstrap the cluster (and during the cert-enable
// flow). On the cert path the certificate and its private key are provided as
// two Secret Manager secrets (certId + certPrivateKeySecret) - VCM does not mint
// them - so both resource paths are carried through unchanged.
func poolCredentialsFromSetConfig(opt vcmserver.OptOntapCredentialsV1) (*datamodel.PoolCredentials, error) {
	creds, ok := opt.Get()
	if !ok {
		return nil, fmt.Errorf("VCM ontapCredentials are not configured")
	}
	if err := validateOntapCredentials(creds); err != nil {
		return nil, err
	}

	def := creds.DefaultModeUserCredentials.Value
	adminPass := creds.AdminPassSecretId.Or("")
	username := def.Username.Or("")

	if userAuthIsCert(def) {
		return &datamodel.PoolCredentials{
			CertificateID:          def.CertId.Or(""),
			CertPrivateKeySecretID: def.CertPrivateKeySecret.Or(""),
			// adminPassSecretId is the built-in admin password used to bootstrap
			// the cluster and enable cert auth before the cert user is live.
			SecretID: adminPass,
			Username: username,
			AuthType: env.USER_CERTIFICATE,
			CaURI:    env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName),
		}, nil
	}

	// Password path: the default-mode (PURPOSE_VCM_ADMIN) user is not provisioned
	// as a password-authenticatable ONTAP account (VLM only sets a password on
	// the built-in "admin"; the configured username is created as a certificate
	// user by the cert-enable flow). Authenticate as the built-in admin so
	// password-mode direct ONTAP calls do not 401 against a non-existent user.
	return &datamodel.PoolCredentials{
		SecretID: def.PasswordSecret.Or(""),
		Username: env.Admin,
		AuthType: env.USERNAME_PWD_SEC_MGR,
	}, nil
}

// ToVCMOntapCredentials flattens the structured OntapCredentials payload into the
// list form consumers (VLM config rebuild, DeriveOntapCredentials) use. Each
// entry is tagged with its credential type and carries only the material for its
// effective auth mode so downstream re-derivation stays consistent.
func ToVCMOntapCredentials(opt vcmserver.OptOntapCredentialsV1) []commonparams.VCMOntapCredential {
	creds, ok := opt.Get()
	if !ok {
		return nil
	}
	out := make([]commonparams.VCMOntapCredential, 0, 3)
	if adminPass := creds.AdminPassSecretId.Or(""); adminPass != "" {
		out = append(out, commonparams.VCMOntapCredential{
			Type:     vcmCredentialTypeAdmin,
			SecretID: adminPass,
		})
	}
	if def, ok := creds.DefaultModeUserCredentials.Get(); ok {
		out = append(out, userToVCMOntapCredential(vcmCredentialTypeDefault, def))
	}
	if ontap, ok := creds.OntapModeUserCredentials.Get(); ok {
		out = append(out, userToVCMOntapCredential(vcmCredentialTypeOntapMode, ontap))
	}
	return out
}

// userToVCMOntapCredential maps a single UserCredentials entry, carrying only the
// material for its effective auth mode.
func userToVCMOntapCredential(credType string, u vcmserver.UserCredentialsV1) commonparams.VCMOntapCredential {
	mapped := commonparams.VCMOntapCredential{
		Type:     credType,
		Username: u.Username.Or(""),
	}
	if userAuthIsCert(u) {
		mapped.CertificateID = u.CertId.Or("")
		mapped.CertPrivateKeySecretID = u.CertPrivateKeySecret.Or("")
		mapped.IsCertAuth = true
	} else {
		mapped.SecretID = u.PasswordSecret.Or("")
	}
	return mapped
}

// userAuthIsCert reports whether a user credential resolves via certificate auth.
// AUTHMODE_UNSPECIFIED and AUTHMODE_CERT select certificate auth; only
// AUTHMODE_PASSWORD selects password auth.
func userAuthIsCert(u vcmserver.UserCredentialsV1) bool {
	mode := u.ActiveAuthMode.Or(vcmserver.UserCredentialsV1ActiveAuthModeAUTHMODEUNSPECIFIED)
	return mode != vcmserver.UserCredentialsV1ActiveAuthModeAUTHMODEPASSWORD
}

// validateOntapCredentials enforces the credential contract:
//   - adminPassSecretId (built-in ADMIN user password) is always required.
//   - defaultModeUserCredentials (PURPOSE_VCM_ADMIN) is required.
//   - ontapModeUserCredentials (PURPOSE_ONTAP_MODE) is optional; validated when present.
//   - for cert auth (AUTHMODE_CERT / AUTHMODE_UNSPECIFIED) certId and
//     certPrivateKeySecret are required; for AUTHMODE_PASSWORD passwordSecret is
//     required.
func validateOntapCredentials(creds vcmserver.OntapCredentialsV1) error {
	if creds.AdminPassSecretId.Or("") == "" {
		return fmt.Errorf("VCM ontapCredentials.adminPassSecretId is required (built-in ADMIN user password)")
	}
	def, ok := creds.DefaultModeUserCredentials.Get()
	if !ok {
		return fmt.Errorf("VCM ontapCredentials.defaultModeUserCredentials is required")
	}
	if err := validateUserCredentials("defaultModeUserCredentials", def, vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN); err != nil {
		return err
	}
	if ontap, ok := creds.OntapModeUserCredentials.Get(); ok {
		if err := validateUserCredentials("ontapModeUserCredentials", ontap, vcmserver.UserCredentialsV1UserPurposePURPOSEONTAPMODE); err != nil {
			return err
		}
	}
	return nil
}

// validateUserCredentials checks a single user's purpose and the material
// required by its active auth mode.
func validateUserCredentials(field string, u vcmserver.UserCredentialsV1, expected vcmserver.UserCredentialsV1UserPurpose) error {
	if purpose, ok := u.UserPurpose.Get(); ok &&
		purpose != vcmserver.UserCredentialsV1UserPurposePURPOSEUNSPECIFIED &&
		purpose != expected {
		return fmt.Errorf("VCM ontapCredentials.%s.userPurpose must be %s, got %s", field, expected, purpose)
	}
	if userAuthIsCert(u) {
		if u.CertId.Or("") == "" {
			return fmt.Errorf("VCM ontapCredentials.%s.certId is required when activeAuthMode is AUTHMODE_CERT", field)
		}
		if u.CertPrivateKeySecret.Or("") == "" {
			return fmt.Errorf("VCM ontapCredentials.%s.certPrivateKeySecret is required when activeAuthMode is AUTHMODE_CERT", field)
		}
		return nil
	}
	if u.PasswordSecret.Or("") == "" {
		return fmt.Errorf("VCM ontapCredentials.%s.passwordSecret is required when activeAuthMode is AUTHMODE_PASSWORD", field)
	}
	return nil
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
