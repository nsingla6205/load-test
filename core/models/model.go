package models

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// Volume access type values (stored on ExportRule.AccessType JSON field).
const (
	ReadWrite = "READ_WRITE"
	ReadOnly  = "READ_ONLY"
	ReadNone  = "READ_NONE"
)
const (
	DefaultCode                  = 0
	ErrorDuringClusterPeerCode   = 100000
	ClusterPeeringExpiredCode    = 100001
	SourceClusterUnreachableCode = 100002
	WaitingForClusterPeeringCode = 100003
	ErrorDuringSVMPeeringCode    = 100004
	SVMPeeringExpiredCode        = 100005
	InitiatingSVMPeeringCode     = 100006
	WaitingForSVMPeeringCode     = 100007
	InitiatingClusterPeeringCode = 100008
	ErrorUnencryptedVolumeCode   = 100009
)

// ONTAP export-policy / protocol defaults
// used to build payloads sent to ONTAP and as sentinels inside core/vsa.
const (
	AnyAccessProtocol               = "any"
	NoneAccessProtocol              = "none"
	ExportAuthenticationFlavorNever = "never"
	ExportAuthenticationFlavorAny   = "any"
	ExportAuthenticationFlavorNone  = "none"
	ExportAuthenticationFlavorSys   = "Sys"
	ExportAuthenticationFlavorKrb5  = "krb5"
	ExportAuthenticationFlavorKrb5i = "krb5i"
	ExportAuthenticationFlavorKrb5p = "krb5p"
	RootAnonymousUser               = "root"
	ChownModeRestricted             = "restricted"
	DefaultExportPolicyName         = "default"
	AllowedAllClients               = "0.0.0.0/0"
	IgnoreNtfsUnixSecurity          = "ignore"
	DefaultIndexExportPolicyRule    = int64(7)
)

// SVM represents a single SVM resource
type SVM struct {
	BaseModel
	Name         string
	Description  string
	State        string
	StateDetails string
}

type Account struct {
	BaseModel
	Name  string
	State string
	Tags  string
}

// BaseModel describes the base model shared by all other models
type BaseModel struct {
	ID        int64
	UUID      string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

type UserCache struct {
	Time     time.Time
	SecretID string
	Password string
}

type CertCache struct {
	Time          time.Time
	CertificateID string
	Certificate   *Certificate
}

type Certificate struct {
	SignedCertificate        string
	PrivateKey               string
	InterMediateCertificates []string
	CommonName               string
}

type OntapEndpoint struct {
	IP  string `json:"ip"`
	DNS string `json:"dns"`
}

type UserCredentials struct {
	Username       string          `json:"username"`
	SecretID       string          `json:"secret_id"`
	CertificateID  string          `json:"certificate_id"`
	Password       string          `json:"password"`
	AuthType       int             `json:"auth_type"`
	OntapEndpoints []OntapEndpoint `json:"ontap_endpoints"`
	// Format: ca_pool_deployed_project_id/ca_pool_name/ca_name
	CaURI string `json:"ca_uri,omitempty"`
	// ServiceLevel is the pool service level (e.g. FLEX, STANDARD, PREMIUM, EXTREME).
	// The ontap-proxy uses this to select the correct cache namespace: any value other
	// than FLEX indicates a dedicated-hardware pool.
	ServiceLevel string `json:"service_level,omitempty"`
	AccountUUID  string `json:"account_uuid,omitempty"`
	// DeploymentName is the pool's deployment name. The ontap-proxy uses it to derive the
	// pool's data SVM ({deploymentName}-svm-01) and thus the reserved vserver-wide QoS policy.
	DeploymentName string `json:"deployment_name,omitempty"`
}

// GetCaURIWithFallback gets ca_uri from UserCredentials, falling back to environment variables if not set.
func (uc *UserCredentials) GetCaURIWithFallback() string {
	if uc == nil || uc.CaURI == "" {
		return env.BuildCaURI("", "", "")
	}
	return uc.CaURI
}

// ParseCaURIWithFallback parses ca_uri from UserCredentials, falling back to environment variables if not set.
func (uc *UserCredentials) ParseCaURIWithFallback() (caPoolDeployedProjectID, caPoolName, caName string) {
	if uc == nil || uc.CaURI == "" {
		return env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName
	}
	return env.ParseCaURI(uc.CaURI)
}
