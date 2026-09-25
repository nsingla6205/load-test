package models

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// Node represents a single Node resource
type Node struct {
	Name                           string
	EndpointAddress                string
	EndpointAddressesToHostNameMap map[string]string // for multiple host failover
	Username                       string
	Password                       string
	SecretID                       string
	CertificateID                  string
	// CertPrivateKeySecretID is the Secret Manager resource path of a
	// caller-provided certificate private key (VCM path). When set, the cert
	// and key are loaded directly from Secret Manager (CertificateID + this
	// field) instead of being derived from CAS. Empty on the VCP path.
	CertPrivateKeySecretID string
	InstanceType           string
	ExternalUUID           string
	Zone                   string
	State                  string
	DeploymentName         string
	AuthType               int
	// ServerName, when non-empty, is used as the TLS SNI value AND for
	// hostname verification of the cert-auth ONTAP REST client. It lets the
	// client dial the node by IP while still verifying the server certificate
	// against its wildcard SAN, so no Cloud DNS record is required. Used by the
	// VCM flows (mirrors the OCI SNI-override path). Empty for the GCP DNS path
	// and password auth, which keep their existing behaviour.
	ServerName string
	// FastHostSelect, when true, opts this node's ONTAP REST client into the
	// two-phase fast host-selection path (a cheap, single-attempt probe picks a
	// reachable management LIF, then the real client is built for that host with
	// the full retry budget). It is a per-call opt-in and only takes effect when
	// the ONTAP_REST_FAST_HOST_SELECT deployment flag is also enabled. Set today
	// only by the OCI reset-password path (buildOCIProviderNode).
	FastHostSelect bool
	// Certificate-related configuration (from database with env var fallback)
	// Format: ca_pool_deployed_project_id/ca_pool_name/ca_name
	CaURI string `json:"ca_uri,omitempty"`

	ExternalSecret      *datamodel.ExternalCredRef `json:"external_secret,omitempty"`
	ExternalCertificate *datamodel.ExternalCredRef `json:"external_certificate,omitempty"`

	ExternalClientCertificate *datamodel.ExternalCredRef `json:"external_client_certificate,omitempty"`
}

// GetCaURIWithFallback gets ca_uri from Node, falling back to environment variables if not set.
func (n *Node) GetCaURIWithFallback() string {
	if n == nil || n.CaURI == "" {
		return env.BuildCaURI("", "", "")
	}
	return n.CaURI
}

// ParseCaURIWithFallback parses ca_uri from Node, falling back to environment variables if not set.
func (n *Node) ParseCaURIWithFallback() (caPoolDeployedProjectID, caPoolName, caName string) {
	if n == nil || n.CaURI == "" {
		return env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName
	}
	return env.ParseCaURI(n.CaURI)
}
