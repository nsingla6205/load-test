package models

// Pool describes a pool in the cloud volume model
type Pool struct {
	BaseModel
	Name                      string
	Description               string
	State                     string
	StateDetails              string
	ServiceLevel              string
	SizeInBytes               uint64
	AccountName               string
	VendorID                  string
	PoolOCID                  string
	Region                    string
	Zone                      string
	TotalThroughputMibps      float64
	UtilizedThroughputMibps   float64
	TotalIops                 int64
	UtilizedIops              int64
	Tags                      string
	AllowAutoTiering          bool
	VendorSubNetID            string
	QosType                   string
	PoolAttributes            *PoolAttributes
	ClusterDetails            *ClusterDetails
	CustomPerformanceParams   *CustomPerformanceParams
	AutoTieringConfig         *AutoTieringConfig
	SaAccountID               string
	DeploymentName            string
	SnHostProject             string
	LargeCapacity             bool
	IsPreview                 bool
	Account                   *Account
	KmsConfig                 *KmsConfig
	SatisfiesPzi              bool
	SatisfiesPzs              bool
	AssetMetadata             *AssetMetadata
	ActiveDirectoryConfigId   string
	ActiveDirectoryResourceId string
	ActiveDirectoryChangeId   string
	ActiveDirectory           *ActiveDirectory
	APIAccessMode             string
}

type AssetMetadata struct {
	ChildAssets []ChildAsset
}

// PoolAttributes describes the attributes of a pool model
type PoolAttributes struct {
	Events          string
	Features        string
	PrimaryZone     string
	SecondaryZone   string
	AllocatedBytes  float64
	NumberOfVolumes int64
	IsRegionalHA    bool
	IsZoneSwitched  bool
	ZoneSwitchState string
	Labels          map[string]string
	LdapEnabled     bool
	IsTrueZonal     bool
}

// ClusterDetails describes the attributes of a cluster model
type ClusterDetails struct {
	ExternalName      string
	OntapVersion      string
	InstanceType      string
	ExternalIpAddress string
	InternalIpAddress string
	InterClusterLifs  []string

	RegionalTenantProject string
	CompartmentOCID       string
	SnHostProject         string
	Network               string
	SubnetNames           []string
}

// CustomPerformanceParams is used to specify the custom performance parameters for a pool
type CustomPerformanceParams struct {
	Enabled    bool
	Throughput float64
	Iops       int64
}

// AutoTieringConfig describes the auto-tiering configuration for a pool
type AutoTieringConfig struct {
	HotTierSizeInBytes      uint64
	EnableHotTierAutoResize bool
	BucketName              string
	HotTierConsumption      int64
	ColdTierConsumption     int64
}

type PoolHydrateObject struct {
	OwnerID        string
	PoolID         string
	Name           string
	State          string
	Region         string
	HotTierSizeGib int64
}

type PoolUpdateCCFERequest struct {
	State          interface{} `json:"state"`
	HotTierSizeGib interface{} `json:"hot_tier_size_gib"`
}
