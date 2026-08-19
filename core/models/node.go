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
	InstanceType                   string
	ExternalUUID                   string
	Zone                           string
	State                          string
	DeploymentName                 string
	AuthType                       int
	// InsecureSkipVerify, when true, disables TLS server-certificate
	// verification for the ONTAP REST client on the cert-auth path (client
	// mTLS is unaffected). Defaults false so GCP/OCI keep full verification.
	// TODO(vcm-dns): temporary — set only by the VCM create-pool flow while VCM
	// has no Cloud DNS records. Remove once DNS handling / SNI-override for VCM
	// is finalized.
	InsecureSkipVerify bool
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
