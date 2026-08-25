package hyperscaler

type ComputeOperation struct {
	Name string

	// for compute operation
	Status   string
	Progress int64

	// for service networking operation
	Done          bool
	Response      []byte
	ErrorResponse string
}

type VPCNetwork struct {
	ProjectName string
	Name        string
	Subnetworks []string
	SelfLink    string
}

// SecondaryIpRange is a GCP subnetwork secondary IP range (alias range).
type SecondaryIpRange struct {
	RangeName   string
	IpCidrRange string
}

type Subnet struct {
	ProjectName       string
	Name              string
	Region            *string
	Network           string
	IpCidrRange       string
	GatewayAddress    string
	SelfLink          string
	SecondaryIpRanges []SecondaryIpRange
}

type Firewall struct {
	ProjectName      string
	Name             string
	AllowedPortRules []string
	SourceRanges     []string
	VPCNetworkName   string
	Description      string
	Direction        string
	Priority         int64
}

type ServiceAccount struct {
	Description string `json:"description,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	ProjectId   string `json:"projectId,omitempty"`
	UniqueId    string `json:"uniqueId,omitempty"`
}

type CreateServiceAccountRequest struct {
	AccountId      string          `json:"accountId,omitempty"`
	ServiceAccount *ServiceAccount `json:"serviceAccount,omitempty"`
}

type ServiceAccountKey struct {
	DisableReason   string `json:"disableReason,omitempty"`
	Disabled        bool   `json:"disabled,omitempty"`
	KeyAlgorithm    string `json:"keyAlgorithm,omitempty"`
	KeyOrigin       string `json:"keyOrigin,omitempty"`
	KeyType         string `json:"keyType,omitempty"`
	Name            string `json:"name,omitempty"`
	PrivateKeyData  string `json:"privateKeyData,omitempty"`
	PrivateKeyType  string `json:"privateKeyType,omitempty"`
	PublicKeyData   string `json:"publicKeyData,omitempty"`
	ValidAfterTime  string `json:"validAfterTime,omitempty"`
	ValidBeforeTime string `json:"validBeforeTime,omitempty"`
}

type Address struct {
	ProjectId   string
	SelfLink    string
	Region      string
	Type        string
	SubnetURI   string
	AddressName string
}

type ForwardingRule struct {
	ProjectId string
	IPAddress string
	Network   string
	Region    string
	Target    string
	Name      string
	SelfLink  string
}
