package models

const (
	USERNAME_PWD         = 0
	USERNAME_PWD_SEC_MGR = 1
	USER_CERTIFICATE     = 2
)

type ContextKey string

const (
	AuthDataKey    ContextKey = "authDataKey"
	RuleContextKey ContextKey = "ruleContext"

	// ServiceLevelFLEX is the pool service level for software-defined (FLEX) pools.
	// Any service level other than this indicates a dedicated-hardware pool.
	ServiceLevelFLEX = "FLEX"
)

type Certificate struct {
	SignedCertificate        string   `json:"signed_certificate"`
	PrivateKey               string   `json:"private_key"`
	InterMediateCertificates []string `json:"intermediate_certificate"`
	CommonName               string   `json:"common_name"`
	RootCaCertificate        string   `json:"root_ca_certificate"`
}

type AuthData struct {
	AuthType       int
	SecretID       string
	CertificateID  string
	Password       string
	Username       string
	PoolID         string
	AccountName    string
	Certificate    *Certificate
	OntapEndpoints []OntapEndpoint
	// Format: ca_pool_deployed_project_id/ca_pool_name/ca_name
	CaURI string
	// ServiceLevel is the pool service level (e.g. FLEX, STANDARD, PREMIUM, EXTREME).
	// Entries with a non-FLEX service level are dedicated-hardware pools; they live in
	// the dedicated cache namespace and connect to the pinned cluster's management endpoint.
	ServiceLevel string
	// ClusterName is the ONTAP cluster name (from the x-netapp-cluster-name header)
	// associated with a dedicated-hardware credential. Empty otherwise.
	ClusterName string
	AccountUUID string
	// DeploymentName is the pool's deployment name. Used to derive the pool's data SVM
	// ({deploymentName}-svm-01) and thus the reserved vserver-wide QoS policy
	// ({svmName}-qos-policy), so it can be denied even when a CLI command omits -vserver.
	DeploymentName string
}

type OntapEndpoint struct {
	IP  string `json:"ip"`
	DNS string `json:"dns"`
}

type PoolDetails struct {
	ProjectNumber string
	PoolID        string
	AccountName   string
	UserName      string
	// ClusterName carries the x-netapp-cluster-name header value, forwarded to Core
	// so dedicated-capacity pools resolve credentials for the requested cluster.
	ClusterName string
}
