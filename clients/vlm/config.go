// IMPORTANT: This is the VLM workflow datamodel file.
// We shouldn't edit this from the VCP side unless a newer version is shared by the VLM team.
package vlm

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	GCPCloud   string = "gcp"
	OCICloud   string = "oci"
	AzureCloud string = "azure"

	DeploymentTypeSharedHA    string = "shared_ha"
	DeploymentTypeNonSharedHA string = "non_shared_ha"

	CorrelationIDKey    string = "x-correlation-id"
	OpcWorkRequestIDKey string = "opc-work-request-id"
	DeploymentIDKey     string = "x-deployment-id"
	DeploymentIDTagKey  string = "deployment_id"
)

// VLMWorkflowName is the name of the workflow
const (
	CreateVSAClusterDeploymentWorkflowName     = "vlm.CreateVSAClusterDeploymentWorkflow"
	CreateVSASVMWorkflowName                   = "vlm.CreateVSASVMWorkflow"
	ModifyVSASVMWorkflowName                   = "vlm.ModifyVSASVMWorkflow"
	DeleteVSASVMWorkflowName                   = "vlm.DeleteVSASVMWorkflow"
	DeleteVSAClusterDeploymentWorkflowName     = "vlm.DeleteVSAClusterDeploymentWorkflow"
	UpdateVSAClusterDeploymentWorkflowName     = "vlm.UpdateVSAClusterDeploymentWorkflow"
	UpgradeVSAClusterDeploymentWorkflowName    = "vlm.UpgradeVSAClusterDeploymentWorkflow"
	PreUpgradeVSAClusterDeploymentWorkflowName = "vlm.PreUpgradeVSAClusterDeploymentWorkflow"
	VSASvmUpgradeWorkflowName                  = "vlm.VSASvmUpgradeWorkflow"
	ClusterPowerCycleWorkflowName              = "vlm.ClusterPowerCycle"
	ClusterHealthCheckWorkflowName             = "vlm.ClusterHealthCheck"
	GetClusterZiZsDetailsWorkflowName          = "vlm.GetClusterZiZsDetails"
	UpdateVSAMediatorWorkflowName              = "vlm.UpdateVSAMediatorWorkflow"
	CreateVSAExpertModeUserWorkflowName        = "vlm.CreateVSAExpertModeUserWorkflow"
	AddExpertModeUserWorkflowName              = "vlm.AddExpertModeUserWorkflow"
	UpdateLicenseWorkflowName                  = "vlm.UpdateLicenseWorkflow"
	ASUPTriggerWaitWorkflowName                = "vlm.ASUPTriggerWaitWorkflow"
	ZoneSwitchWorkflowName                     = "vlm.ZoneSwitchWorkflow"
	RotateFabricPoolKeysWorkflowName           = "vlm.RotateFabricPoolKeysWorkflow"
	ExpandVSAClusterWorkflowName               = "vlm.ExpandVSAClusterWorkflow"
	CleanupExpansionWorkflowName               = "vlm.CleanupExpansionWorkflow"
	AggregateDeleteWorkflowName                = "vlm.AggregateDeleteWorkflow"

	GCP_DISK_PD_SSD              = "pd-ssd"
	GCP_DISK_HDB                 = "hyperdisk-balanced"
	ONTAP_CREDENTIAL_ENCRYPT_KEY = "ONTAP_CREDENTIAL_ENCRYPT_KEY"

	// Suffixes appended to DeploymentID to form the ONTAP cloud-target (object store) name.
	GCPObjectStoreSuffix = "-gcp-object-store"
	OCIObjectStoreSuffix = "-oci-object-store"

	ErrorTypeVLMError       string = "VLMError"
	ErrorTypeVLMClientError string = "VLMClientError"

	ClusterPowerOn    string = "start"
	ClusterPowerOff   string = "stop"
	ClusterPowerReset string = "reset"

	ZiZsComputeInstanceKey string = "compute_instance"
	ZiZsComputeDiskKey     string = "compute_disk"

	ZoneSwitchActionSwitch      string = "switch"
	ZoneSwitchActionRevert      string = "revert"
	ZoneSwitchActionRevertForce string = "revert_force"

	// Create a single placement policy for SPREAD with a separate AD active, passive and mediator VM sets
	GCPPlacementPolicySpreadSingle string = "spread_single"
	// Zonal Cluster only: Create a single placement policy for all VMs, to be implemented later
	GCPPlacementPolicyCompactSingle string = "compact_single"
	// Create a separate placement policy for active, passive and mediator VM sets and assign VMs to respective policies
	GCPPlacementPolicySpreadMulti string = "spread_multi"
	// Create a separate placement policy for active, passive and mediator VM sets and assign VMs to respective policies
	GCPPlacementPolicyCompactMulti string = "compact_multi"
	// No placement policy created/applied
	GCPPlacementPolicyNone string = "none"
)

// TODO: Need to revisit these values for Multi HA configurations
var WorkflowExecutionTimeoutMap map[string]time.Duration = map[string]time.Duration{
	"DefaultWorkflowExecutionTimeout":          10 * time.Minute,
	CreateVSAClusterDeploymentWorkflowName:     30 * time.Minute,
	CreateVSASVMWorkflowName:                   15 * time.Minute,
	ModifyVSASVMWorkflowName:                   15 * time.Minute,
	DeleteVSASVMWorkflowName:                   25 * time.Minute,
	DeleteVSAClusterDeploymentWorkflowName:     20 * time.Minute,
	UpdateVSAClusterDeploymentWorkflowName:     120 * time.Minute,
	UpgradeVSAClusterDeploymentWorkflowName:    300 * time.Minute,
	PreUpgradeVSAClusterDeploymentWorkflowName: 120 * time.Minute,
	ClusterPowerCycleWorkflowName:              40 * time.Minute,
	ClusterHealthCheckWorkflowName:             15 * time.Minute,
	GetClusterZiZsDetailsWorkflowName:          10 * time.Minute,
	UpdateVSAMediatorWorkflowName:              30 * time.Minute,
	UpdateLicenseWorkflowName:                  10 * time.Minute,
	CreateVSAExpertModeUserWorkflowName:        30 * time.Minute,
	VSASvmUpgradeWorkflowName:                  10 * time.Minute,
	ZoneSwitchWorkflowName:                     60 * time.Minute,
	RotateFabricPoolKeysWorkflowName:           5 * time.Minute,
	ExpandVSAClusterWorkflowName:               60 * time.Minute,
	CleanupExpansionWorkflowName:               30 * time.Minute,
	AggregateDeleteWorkflowName:                30 * time.Minute,
}

// MaxUpgradeWorkflowExecutionTimeout caps the HA-pair-scaled timeout for the
// cluster upgrade workflow so that a wedged upgrade on large clusters cannot
// run for many hours before failing.
const MaxUpgradeWorkflowExecutionTimeout = 5 * time.Hour

type VLMConfig struct {
	Cloud      CloudConfig          `json:"cloud"`
	Deployment DeploymentConfig     `json:"deployment"`
	Upgrade    OntapUpgradeConfig   `json:"upgrade"`
	VsaCluster VsaClusterConfig     `json:"vsa_cluster"`
	DataAggr   []DataAggrConfig     `json:"data_aggr"`
	Svm        map[string]SvmConfig `json:"svm"`
}

type CloudConfig struct {
	HAPairs         []HAPair `json:"ha_pair"`            // sde need not fill this
	LastMaxSeenNode int      `json:"last_max_seen_node"` // Highest node index ever assigned; new nodes start from here
}

type DeploymentConfigFlags struct {
	EnableAASupportSvm         bool   `json:"enable_aa_support_svm"`          // Enable AA support for svm
	EnableAAConfig             bool   `json:"enable_aa_config"`               // Enable AA Config for active-active deployments
	EnableIlbSupport           bool   `json:"enable_ilb_support"`             // Enable ILB support
	EnableNfsV364BitIdentifier string `json:"enable_nfs_v3_64bit_identifier"` // Enable NFS v3 64-bit identifier support
	EnableNonLssdInstanceType  bool   `json:"enable_non_lssd_instance_type"`  // Enable Non LSSD instance type support
	EnableTcpUdpBasedIlb       bool   `json:"enable_tcp_udp_based_ilb"`       // Enable TCP/UDP based ILB support
	EnableFlashCache           bool   `json:"enable_flash_cache"`             // Enable Flash Cache support
}

type DeploymentConfig struct {
	Provider     string `json:"provider"`      // (gcp/aws/azure)
	DeploymentID string `json:"deployment_id"` // Added
	// If the Serial number Prefix is provided then it will be used to generate serial numbers for the VMs.
	SerialNumberPrefix string      `json:"serial_number_prefix"` // used to generate serial number for all the VMs
	VMSerialNumbers    []string    `json:"vm_serial_numbers"`    // List of serial numbers for the VMs
	Region             string      `json:"region" `              // Added
	Zone               ZoneInfo    `json:"zone"`                 // Added
	Images             ImageConfig `json:"images"`               // Added

	Tags           string            `json:"tags"`             // Comma separated list of tags to be attached for the VMs created by the deployment
	Labels         map[string]string `json:"labels"`           // List of labels to attach to resources
	UserBootargs   string            `json:"user_boot_args"`   // The input is a list of key-value pairs with semicolons as delimiters.
	UserCustomdata map[string]string `json:"user_custom_data"` // Additional Custom data to be passed to the VMs by user

	DeploymentType       string                       `json:"deployment_type"`        // SingeNode or ShareHA or NonShareHA
	NumHAPair            int                          `json:"num_ha_pair"`            // Number of HA pairs to be created
	VSAInstanceType      string                       `json:"vsa_instance_type"`      // rename to VSAInstanceType
	MediatorInstanceType string                       `json:"mediator_instance_type"` // rename to MediatorInstanceType
	DataDiskType         string                       `json:"data_disk_type"`         // Move to GCP config ?
	SystemDiskType       string                       `json:"system_disk_type"`       // Move to GCP config ?
	MediatorDiskType     string                       `json:"mediator_disk_type"`     // Move to GCP config ?
	DataDiskCount        int                          `json:"data_disk_count"`        // Number of data disks to be created
	VSASystemDiskConfig  map[OntapDiskType]DiskConfig `json:"vsa_system_disk_config"` // System disk configuration for VSA

	// TODO: check if zone wise netconfig is required
	NetConfig      map[VSALIFType]NetworkConfig `json:"net_config"`      // Network configuration for the deployment
	GCPConfig      GCPConfig                    `json:"gcpconfig"`       // GCP specific configuration
	AzureConfig    AzureConfig                  `json:"azureconfig"`     // Azure specific configuration
	ProviderConfig ProviderConfigWrapper        `json:"provider_config"` // Hyperscaler specific configuration
	SPConfig       SPConfig                     `json:"spconfig"`        // Storagepool specific configuration
	DevFlags       DevFlags                     `json:"dev_flags"`       // Development flags
	NTPServers     []string                     `json:"ntp_servers"`     // NTP servers for time synchronization
	DNSServers     []string                     `json:"dns_servers"`     // DNS servers for name resolution
	// DeploymentConfigFlags added for future flags
	DeploymentConfigFlags DeploymentConfigFlags `json:"additional_deployment_config_flags"`
	PlacementPolicyConfig PlacementPolicyConfig `json:"placement_policy_config"`
}

type DevFlags struct {
	ExtIPForNodeMgmt              bool                    `json:"ext_ip_for_node_mgmt,omitempty"`     // External IP for node management
	DisableDataNicTier1           bool                    `json:"disable_data_nic_tier1,omitempty"`   // Disable Tier 1 for data NIC
	EnablePremiumTierData         bool                    `json:"enable_premium_tier_data,omitempty"` // Enable Premium Tier for data NIC
	DisableGVNIC                  bool                    `json:"disable_gvnic,omitempty"`
	EnableNfsV3Support            bool                    `json:"enable_nfs_v3_support,omitempty"`             // Enable NFS v3 support
	EnableIlbSupport              bool                    `json:"enable_ilb_support,omitempty"`                // Enable ILB support
	DisableBootDiskSnapshotPolicy bool                    `json:"disable_boot_disk_snapshot_policy,omitempty"` // Disable boot disk snapshot policy creation and attachment (default: false/enabled)
	DisableAzureVNetCreation      bool                    `json:"disable_azure_vnet_creation,omitempty"`       // Skip Azure VNet/subnet/NSG creation and use existing network
	ProviderDevFlags              ProviderDevFlagsWrapper `json:"provider_dev_flags,omitempty"`
}

type SnapshotConfig struct {
	PolicyName    string `json:"policy_name,omitempty"`    // Name of the snapshot policy
	RetentionDays int32  `json:"retention_days,omitempty"` // Number of days to retain snapshots (default: 7)
}

type SnapshotConfigList struct {
	BootDiskConfig SnapshotConfig `json:"boot_disk_config,omitempty"` // Snapshot configuration for boot disks
	// MrootDiskConfig SnapshotConfig `json:"mroot_disk_config,omitempty"` // TODO: Snapshot configuration for mroot disks (future use)
}

type GCPConfig struct {
	ProjectID              string             `json:"project_id"`                // GCP project ID
	ImageProjectID         string             `json:"image_project_id"`          // Image project ID for GCP        `json:"gcp_image_config"`      // GCP image configuration
	MediatorImageProjectID string             `json:"mediator_image_project_id"` // Mediator image project ID for GCP
	ServiceAccountEmail    string             `json:"service_account_email"`     // Service account email for GCP
	BucketName             string             `json:"bucket_name"`               // GCP bucket name for storing data
	SnapshotConfigList     SnapshotConfigList `json:"snapshot_config_list"`      // Snapshot configuration container for different disk types
}

type OCIConfig struct {
	CompartmentID              string                            `json:"compartment_id"` // OCI Compartment ID
	SubnetID                   string                            `json:"subnet_id"`
	DataNICSubnetID            string                            `json:"data_nic_subnet_id"`
	DataDiskVpus               *int64                            `json:"data_disk_vpus"`                   // Data disk VPUs (nil = leave to VLM default)
	AvailabilityDomain         AvailabilityDomainInfo            `json:"availability_domain_info"`         // OCI Availability Domain Info
	FaultDomain                FaultDomainInfo                   `json:"fault_domain_info"`                // OCI Fault Domain Info (per-leg fault domain within each AD)
	VSAInstanceShape           string                            `json:"vsa_instance_shape"`               // Instance shape for VSA
	VSAFlexOcpus               float32                           `json:"vsa_flex_ocpus,omitempty"`         // OCPUs for VSA flex (non-mediator); 0 = default 4
	VSAFlexMemoryInGBs         float32                           `json:"vsa_flex_memory_in_gbs,omitempty"` // Memory in GB for VSA flex (non-mediator); 0 = default 32
	Creator                    string                            `json:"creator"`                          // Creator for OCI mandatory tags (netapp_tags); overridable by CLI --creator
	FreeFormTags               map[string]string                 `json:"freeform_tags"`                    // Free form tags for OCI resources
	DefinedTags                map[string]map[string]interface{} `json:"defined_tags"`                     // Defined tags for OCI resources
	SubnetDomainName           string                            `json:"subnet_domain_name"`
	CmekOcid                   string                            `json:"cmek_ocid,omitempty"`          // OCID for CMEK to encrypt data block volumes
	CustomerNSGs               []string                          `json:"customer_nsgs"`                // Customer NSGs to be attached to the customer vnic
	CustomerSecurityAttributes map[string]map[string]interface{} `json:"customer_security_attributes"` // Customer security attributes to be attached to the customer vnic
	FabricPoolConfig           FabricPoolConfig                  `json:"fabric_pool_config"`           // Fabric pool configuration
}

type FabricPoolConfig struct {
	BucketName string `json:"bucket_name"`
	SecretOcid string `json:"secret_ocid"`
	Namespace  string `json:"namespace"`
	ServerURL  string `json:"server_url"`
}

type AzureConfig struct {
	ResourceGroup        string     `json:"resource_group"`
	VNetName             string     `json:"vnet_name"`
	SubnetName           string     `json:"subnet_name"`
	AddressSpace         []string   `json:"address_space"`
	SubnetCIDR           string     `json:"subnet_cidr"`
	NetworkSecurityGroup string     `json:"network_security_group"`
	AdminUsername        string     `json:"admin_username"`
	AdminPassword        string     `json:"admin_password"`
	SSHPublicKey         string     `json:"ssh_public_key"`
	AvailabilityMode     string     `json:"availability_mode,omitempty"` // Azure availability mode: "zrs" (default) or "lrs".
	DataStorageType      string     `json:"data_storage_type"`           // "esan", "pv2", or "both" (default: "both")
	RootStorageType      string     `json:"root_storage_type"` // "esan", "pv2", or "both" (default: "both")
	ESANConfig           ESANConfig `json:"esan_config"`
}

// ESANConfig holds Azure Elastic SAN configuration.
// Names are derived from deployment_id if not provided.
type ESANConfig struct {
	ESANName                     string           `json:"esan_name"`                                    // derived: {deployment_id}-esan
	VolumeGroupName              string           `json:"volume_group_name"`                            // derived: {deployment_id}-vg
	CreatePrivateEndpoint        *bool            `json:"create_private_endpoint,omitempty"`
	PrivateEndpointName          string           `json:"private_endpoint_name,omitempty"`               // name of the private endpoint
	PrivateEndpointResourceGroup string           `json:"private_endpoint_resource_group,omitempty"`     // resource group for the private endpoint
	PrivateEndpointSubnetID      string           `json:"private_endpoint_subnet_id,omitempty"`          // subnet for the private endpoint
	PrivateEndpointID            string           `json:"private_endpoint_id,omitempty"`                 // set by the create workflow when CreatePrivateEndpoint is used
	PrivateEndpointIPAddress     string           `json:"private_endpoint_ip_address,omitempty"`         // PE NIC private IP for iSCSI when using private link
	Volumes                      []ESANVolumeInfo `json:"volumes"`                                      // populated after creation
}

// ESANVolumeInfo holds iSCSI target info for a single ESAN volume.
type ESANVolumeInfo struct {
	VolumeName       string `json:"volume_name"`
	TargetIQN        string `json:"target_iqn"`
	TargetPortalHost string `json:"target_portal_host"`
	TargetPortalPort int32  `json:"target_portal_port"`
	SizeGiB          int64  `json:"size_gib"`
	VolumeType       string `json:"volume_type"` // "data" or "root"
}

// AzureAvailabilityMode constants
const (
	AzureAvailabilityModeLRS = "lrs"
	AzureAvailabilityModeZRS = "zrs"
)

// Storage type constants for Azure data/root disk configuration.
const (
	StorageTypeESAN = "esan"
	StorageTypePV2  = "pv2"
	StorageTypeBoth = "both"
)

type GCPNetworkConfig struct {
	SubnetProjectID string `json:"subnet_project_id"` // Project ID for the subnet
}

type OCINetworkConfig struct {
	SubnetOCID string `json:"subnet_ocid"` // OCID for the subnet
}

type AzureNetworkConfig struct {
	SubnetID   string `json:"subnet_id"`
	VNetID     string `json:"vnet_id"`
	NICID      string `json:"nic_id"`
	PublicIPID string `json:"public_ip_id"`
	NSGID      string `json:"nsg_id"`
}

type ZoneInfo struct {
	Zone1        string `json:"zone1"`
	Zone2        string `json:"zone2"`
	MediatorZone string `json:"mediator_zone"`
}

type AvailabilityDomainInfo struct {
	AvailabilityDomain1        string `json:"availability_domain1"`
	AvailabilityDomain2        string `json:"availability_domain2"`
	MediatorAvailabilityDomain string `json:"mediator_availability_domain"`
}

type FaultDomainInfo struct {
	FaultDomain1        string `json:"fault_domain1"`
	FaultDomain2        string `json:"fault_domain2"`
	MediatorFaultDomain string `json:"mediator_fault_domain"`
}

type ImageConfig struct {
	VSAImageName      string `json:"vsa_image_name"`      // Image name for VSA
	MediatorImageName string `json:"mediator_image_name"` // Image name for Mediator
}

type SPConfig struct {
	Size            string           `json:"size"`                        // Size of the storage pool in GB (homogeneous, pool-level)
	IOps            int64            `json:"iops"`                        // IOPS for the storage pool (pool-level)
	Throughput      int64            `json:"tput"`                        // Throughput for the storage pool (pool-level)
	IsHeterogeneous bool             `json:"is_heterogeneous"`            // When true, HAPairConfigs carry per-HA-pair config set by VCP
	HAPairConfigs   []SPHAPairConfig `json:"sp_ha_pair_config,omitempty"` // Per-HA-pair configuration; populated via PopulateSPHAPairConfig before use
}

func (s SPConfig) SizeGiB() int64 { return ParseSizeStringGiB(s.Size) }

func ParseSizeStringGiB(s string) int64 {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.ParseInt(s[:end], 10, 64)
	if err != nil {
		return 0
	}
	switch strings.ToLower(s[end:]) {
	case "", "g", "gi", "gb", "gib":
		return n
	default:
		return 0
	}
}

type SPHAPairConfig struct {
	InstanceType string       `json:"instance_type"` // VM instance type for both nodes in this HA pair
	AggrConfigs  []AggrConfig `json:"aggr_configs"`  // Aggr configurations for the HA pair
}

type AggrConfig struct {
	Size       string `json:"size"`                // Aggregate size (e.g. "500Gi")
	IOps       int64  `json:"iops"`                // Aggregate IOPS
	Throughput int64  `json:"throughput"`          // Aggregate throughput
	AggrName   string `json:"aggr_name,omitempty"` // Backing aggregate name, populated by VLM (e.g. "aggr1")
}

type OntapCertificate struct {
	Certificate             string   `json:"certificate"`              // Certificate for ONTAP
	PrivateKey              string   `json:"private_key"`              // Private key for ONTAP
	InterMediateCertificate []string `json:"intermediate_certificate"` // Intermediate certificate for ONTAP
	CommonName              string   `json:"common_name"`              // Common name for ONTAP
	DNSName                 string   `json:"cert_dns,omitempty"`       // DNS name for the certificate
}

// OntapCredentials holds the credentials for ONTAP, including the certificate and username/password.
type OntapCredentials struct {
	Certificate   OntapCertificate `json:"certificate"`    // Certificate for ONTAP
	AdminPassword string           `json:"admin_password"` // Password for ONTAP
}

// Will be revisted during multi svm support
type GCPILBHaResources struct {
	ForwardingRules  string `json:"forwarding_rules"`   // Added for backward compatibility with vcp, deprecated
	BackendServices  string `json:"backend_services"`   // Added for backward compatibility with vcp, deprecated
	HealthChecks     string `json:"health_checks"`      // Added for backward compatibility with vcp, deprecated
	HealthCheckPorts int32  `json:"health_check_ports"` // Added for backward compatibility with vcp, deprecated
}

type OntapExpertModeUserConfig struct {
	VLMConfig                 VLMConfig        `json:"vlm_config"`                    // VLM configuration for expert mode
	OntapCredentials          OntapCredentials `json:"ontap_credentials"`             // ONTAP credentials for expert mode
	ExpertModeUserCredentials OntapCredentials `json:"expert_mode_credentials"`       // expert mode credentials
	Username                  string           `json:"username,omitempty"`            // expert mode username
	AuthenticationType        string           `json:"authentication_type,omitempty"` // "password" or "certificate", default is password
	RbacFileURL               string           `json:"rbac_file_url,omitempty"`       // URL for the RBAC file
	RbacFileChecksum          string           `json:"rbac_file_checksum,omitempty"`  // Checksum of the RBAC file
}

type AddExpertModeUserRequest struct {
	OntapCredential      OntapCredentials `json:"ontap_credential"`       // ONTAP admin credentials
	ExpertModeCredential OntapCredentials `json:"expert_mode_credential"` // expert mode user credentials
	Username             string           `json:"username"`               // expert mode username
	Vserver              string           `json:"vserver"`                // cluster name / vserver
	NodeMgmtIP           string           `json:"node_mgmt_ip"`           // cluster management IP
	AuthenticationType   string           `json:"authentication_type"`    // "password" or "certificate"
	RoleName             string           `json:"role_name"`              // RBAC role name to assign
	Provider             string           `json:"provider"`               // cloud provider (gcp, oci, etc.)
}

type OntapExpertModeUserResponse struct {
	RbacFileChecksum string `json:"rbac_checksum,omitempty"` // Checksum of the applied RBAC file
}

type GCPILBVmResources struct {
	Negs string `json:"negs"` // Added for backward compatibility with vcp, deprecated
}

type AdditionalVmResources struct {
	GCPILBVmResources GCPILBVmResources `json:"gcp_ilb_vm_resources"` // Stores gcp ilb vm resources
}

type AdditionalHaResources struct {
	GCPILBHaResources GCPILBHaResources `json:"gcp_ilb_ha_resources"` // Stores gcp ilb resources
}

type HAPair struct {
	VM1                   VMConfig              `json:"vm1"`
	VM2                   VMConfig              `json:"vm2"`
	Mediator              VMConfig              `json:"mediator"`                // Added
	AdditionalHaResources AdditionalHaResources `json:"additional_ha_resources"` // Added
}

type VMConfig struct {
	Region                string                   `json:"region"`              // Added
	Zone                  string                   `json:"zone"`                // Added
	FaultDomain           string                   `json:"fault_domain,omitempty"` // OCI fault domain for the VM within its availability domain (Zone)
	Name                  string                   `json:"name"`                // Name of the VM
	HostName              string                   `json:"host_name"` // Available during cluster create.
	SerialNumber          string                   `json:"serial_number"`
	NodeIndex             int                      `json:"node_index"` // Added
	IsMediator            bool                     `json:"is_mediator"`
	SystemLIFs            map[VSALIFType]LIFConfig `json:"lifs"`                    // List of IPs for the VM
	SystemDisks           []DiskConfig             `json:"system_disks"`            // List of system disks for the VM
	DataDisks             []DiskConfig             `json:"data_disks"`              // List of data disks for the VM
	VSAManagementIP       string                   `json:"vsa_management_ip"`       // VSA management IP for the VM
	AdditionalVmResources AdditionalVmResources    `json:"additional_vm_resources"` // additional resources
	VMInstanceID          string                   `json:"vm_instance_id"`          // VM instance ID (cloud-agnostic)
	BootDiskID            string                   `json:"boot_disk_id"`            // Boot disk ID (cloud-agnostic)
}

// GCP Only: Used for ZiZs workflow
type ResourceInformation struct {
	GCPRI map[string][]GCPResourceInformation `json:"gcp_resource_information"`
}

// GCP Only: Used for ZiZs workflow
type GCPResourceInformation struct {
	SatisfiesPzi bool   `json:"satisfies_pzi"`
	SatisfiesPzs bool   `json:"satisfies_pzs"`
	AssetType    string `json:"asset_type"`
	AssetLink    string `json:"asset_link"`
}

type OntapDiskType string

const (
	OntapDiskBoot     OntapDiskType = "boot"
	OntapDiskNvram    OntapDiskType = "nvram"
	OntapDiskCore     OntapDiskType = "core"
	OntapDiskRoot     OntapDiskType = "root"
	OntapDiskRootCopy OntapDiskType = "rootcopy"
	OntapDiskData     OntapDiskType = "data"
)

type VSALIFType string

const (
	LIFTypeNodeMgmt          VSALIFType = "nodemgmt"
	LIFTypeNodeMgmtInternal  VSALIFType = "nodemgmtinternal"
	LIFTypeNodeMgmtSecondary VSALIFType = "nodemgmtsecondary"
	LIFTypeIC                VSALIFType = "ic"
	LIFTypeCluster          VSALIFType = "clus"
	LIFTypeInterCluster     VSALIFType = "intercluster"
	LIFTypeRSM              VSALIFType = "rsm"
	LIFTypeSan              VSALIFType = "san"
	LIFTypeNas              VSALIFType = "nas"
	LIFTypeMediator         VSALIFType = "mediator"
	LIFTypeIlbNas           VSALIFType = "ilbnas"
	LIFTypeRbac             VSALIFType = "rbac"
)

type LIFConfig struct {
	Name          string        `json:"lif_name"`       // Name of the LIF
	VSALIFType    VSALIFType    `json:"vsa_ip_type"`    // Type of VSA LIF
	IP            string        `json:"ip"`             // IP for the LIF
	Uuid          string        `json:"lif_uuid"`       // UUID of the LIF
	NetworkConfig NetworkConfig `json:"network_config"` // Network configuration for the LIF
	Region        string        `json:"region"`         // Region for the LIF
	HomeNode      string        `json:"home_node"`      // Home node for the LIF
	ProbePort     int32         `json:"probe_port"`     // NFS probe port for the LIF only used for nas lif
}

type NetworkConfig struct {
	Subnet                string                       `json:"subnet,omitempty"`  //Subnet for the NIC
	VPC                   string                       `json:"vpc,omitempty"`     //VPC for the NIC
	Gateway               string                       `json:"gateway,omitempty"` //Gateway for the subnet
	Netmask               string                       `json:"netmask,omitempty"` //Netmask for the subnet
	GCPNetworkConfig      GCPNetworkConfig             `json:"gcp_network_config,omitempty"`
	ProviderNetworkConfig ProviderNetworkConfigWrapper `json:"provider_network_config,omitempty"`
	AzureNetworkConfig    AzureNetworkConfig           `json:"azure_network_config,omitempty"`
}

type VsaClusterConfig struct {
	ClusterMgmtNetmask    string `json:"cluster_mgmt_netmask"`
	ClusterMgmtGateway    string `json:"cluster_mgmt_gateway"`
	CustBroadcastDomain   string `json:"cust_broadcast_domain"`
	CustIPSpace           string `json:"cust_ip_space"`
	ObjectStoreName       string `json:"object_store_name"`
	ClusterName           string `json:"cluster_name"` // Name of the VSA cluster
	AutoTierThreshold     int64  `json:"auto_tier_threshold"`
	AutoTierThresholdFlag bool   `json:"auto_tier_threshold_flag"`
}

type SvmLIFConfigs map[VSALIFType][]LIFConfig

type SvmConfig struct {
	Svmname string `json:"svm_name"`
	Svmuuid string `json:"svm_uuid"`
	// Map of Lifs for SVM. Can be either nas or iscsi.
	// SVM can have multiple iSCSI lifs, hence it is maintained as a slice of LIFConfigs
	SVMLIFs SvmLIFConfigs `json:"svm_lifs"`
}

type DataAggrConfig struct {
	Name     string `json:"name"`
	Aggruuid string `json:"uuid"`
	Size     uint64 `json:"size"`      // in GB ?
	HomeNode string `json:"home_node"` // Home node for the aggregate
}

type DiskConfig struct {
	Name               string                    `json:"name,omitempty"`
	Size               uint64                    `json:"size,omitempty"`        // in GB
	AccessMode         string                    `json:"access_mode,omitempty"` // READ_WRITE or READ_WRITE_MANY
	Type               string                    `json:"type,omitempty"`        // Disk type (e.g., pd-standard, pd-ssd)
	DiskIops           int64                     `json:"disk_iops,omitempty"`
	DiskThroughput     int64                     `json:"disk_throughput,omitempty"`
	ResourceStatus     string                    `json:"resource_status,omitempty"` // Status of the resource
	Zone               string                    `json:"zone,omitempty"`            // Zone for the disk
	IsAttached         bool                      `json:"is_attached,omitempty"`     // True if the disk is currently attached to a VM instance
	GCPDiskConfig      GCPDiskConfig             `json:"gcp_disk_config,omitempty"` // GCP specific disk configuration
	ProviderDiskConfig ProviderDiskConfigWrapper `json:"provider_disk_config,omitempty"`
	// TODO: Add resource status
}

type GCPDiskConfig struct {
	DeviceName string `json:"device_name,omitempty"` // Device name for the disk (only when attached)
	// Add other GCP-specific fields here if needed
}

type OCIDiskConfig struct {
	DeviceName          string                            `json:"device_name,omitempty"` // Device name for the disk (only when attached)
	AvailabilityDomain  string                            `json:"availability_domain"`   // Availability Domain for the disk
	Vpus                int64                             `json:"vpus"`                  // Vpus for the disk
	DiskMinVPUMultiPath int64                             `json:"disk_min_vpu_multi_path"`
	DevicePath          string                            `json:"device_path"`
	DiskOciID           string                            `json:"disk_oci_id"`    // OCID for the disk
	CompartmentID       string                            `json:"compartment_id"` // OCI Compartment ID
	FreeFormTags        map[string]string                 `json:"freeform_tags"`       // Free form tags for OCI resources
	DefinedTags         map[string]map[string]interface{} `json:"defined_tags"`        // Defined tags for OCI resources
	CmekOcid            string                            `json:"cmek_ocid,omitempty"` // CMEK OCID to encrypt the disk if given.
}

type CreateSVMRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	Name             string           `json:"name"` // SVM name
	DnsDomains       string           `json:"dns_ip"`
	NameServers      string           `json:"servers"`                      // List of servers
	OntapCredentials OntapCredentials `json:"ontap_credentials"`            // ONTAP credentials for the VSA cluster
	EnableNasLif     bool             `json:"enable_nas_lif"`               // When true, VLM creates NAS LIF + ILB during SVM creation
	SvmAdminPassword string           `json:"svm_admin_password,omitempty"` // SVM Admin Password
}

type CreateSVMResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type DeleteSVMRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	Name             string           `json:"name"`              // SVM name
	OntapCredentials OntapCredentials `json:"ontap_credentials"` // ONTAP credentials for the VSA cluster
}

type DeleteSVMResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}
type ModifySVMRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	Name             string           `json:"name"`                          // SVM name
	OntapCredentials OntapCredentials `json:"ontap_credentials"`             // ONTAP credentials for the VSA cluster
	SvmAdminPassword string           `json:"svm_admin_password,omitempty"` // SVM Admin Password (OCI only)
}

type ModifySVMResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type UpdateVSAClusterDeploymentRequest struct {
	VLMConfig                VLMConfig             `json:"vlm_config"`                       // VLM configuration
	NumHAPair                int                   `json:"num_ha_pair"`                      // Number of HA pairs to be created
	SPConfig                 SPConfig              `json:"spconfig"`                         // Storagepool specific configuration
	OntapCredentials         OntapCredentials      `json:"ontap_credentials"`                // ONTAP credentials for the VSA cluster
	NewInstanceType          string                `json:"new_instance_type"`                // Instance type for the storage pool
	DataDiskVpus             *int64                `json:"data_disk_vpus,omitempty"`         // OCI only: per-data-disk VPU override (1..120, step 10); nil = leave unchanged
	VSAFlexOcpus             *float32              `json:"vsa_flex_ocpus,omitempty"`         // OCI only: VSA flex OCPUs for update (applied to VLMConfig when set); nil = leave unchanged
	VSAFlexMemoryInGBs       *float32              `json:"vsa_flex_memory_in_gbs,omitempty"` // OCI only: VSA flex memory in GB for update (applied to VLMConfig when set); nil = leave unchanged
	OntapUpgrade             OntapUpgradeConfig    `json:"ontap_upgrade"`                    // ONTAP upgrade configuration
	HAPairIndices            []int                 `json:"ha_pair_indices"`                  // Selected HA pair indices for targeted operations
	ITCRecovery              bool                  `json:"itc_recovery"`                     // Flag to indicate if this is a recovery operation (ITC)
	DisableNativeITC         bool                  `json:"disable_native_itc"`               // GCP only: default false uses in-place native ITC; true selects VM-replacement ITC. Ignored on OCI.
	BucketName               string                `json:"bucket_name"`                      // GCP Bucket Name
	AutoTierThreshold        int64                 `json:"auto_tier_threshold"`              // Auto tiering threshold percentage (0-100)
	AllowHAPairLimitOverride bool                  `json:"allow_ha_pair_limit_override"`     // Allow selected callers (e.g. CLI) to bypass HA pair selection limit
	AutoTierThresholdFlag    bool                  `json:"auto_tier_threshold_flag"`         // Auto tiering threshold flag
	ProviderConfig           ProviderConfigWrapper `json:"provider_config,omitempty"`        // Hyperscaler-specific configuration
	CmekOcid                 string                `json:"cmek_ocid"`                        // Update the CMEK encryption key
}

type UpdateMediatorRequest struct {
	VLMConfig        VLMConfig            `json:"vlm_config"`        // VLM configuration
	MediatorUpdate   MediatorUpdateConfig `json:"mediator_update"`   // Mediator update configuration
	OntapCredentials OntapCredentials     `json:"ontap_credentials"` // ONTAP credentials for the VSA cluster
	HAPairIndices    []int                `json:"ha_pair_indices"`   // Indices of the HA pairs to update (empty or nil to update all)
}

type UpdateMediatorResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type OntapUpgradeConfig struct {
	SkipOntapImageVersionMatch     bool   `json:"skip_ontap_image_version_match"`     // Skip Image version match for upgrade
	OntapUpgradeTargetImageVersion string `json:"ontap_upgrade_target_image_version"` // Image version for upgrade
	OntapUpgradeImagePath          string `json:"ontap_upgrade_image_path"`           // Image path for upgrade
	RunPreUpgrade                  bool   `json:"run_preupgrade"`                     // Run pre-upgrade workflow before upgrade
}

type MediatorUpdateConfig struct {
	MediatorImageName      string `json:"mediator_image_name"`
	MediatorImageProjectId string `json:"mediator_image_project_id"`
}

type DeploymentUpdateStatus struct {
	DetachFail   bool `json:"detach_fail"`
	SPUpdateFail bool `json:"sp_update_fail"`
	AttachFail   bool `json:"attach_fail"`
	LifDownFail  bool `json:"lif_down_fail"`
	AggrDownFail bool `json:"aggr_down_fail"`
	AggrUpFail   bool `json:"aggr_up_fail"`
	LifUpFail    bool `json:"lif_up_fail"`
}

// Used for error propagation to VCP
type VLMClientError struct {
	HttpCode       int      `json:"vlmclient_http_code"`
	Code           string   `json:"vlmclient_code"`
	Message        string   `json:"vlmclient_message"`
	OntapErrorCode string   `json:"vlmclient_ontap_error_code,omitempty"`
	Component      string   `json:"vlmclient_component"`
	Retryable      bool     `json:"vlmclient_retryable"`
	External       bool     `json:"vlmclient_external"`
	Cause          []string `json:"vlmclient_error_string"`
}

type UpdateVSAClusterDeploymentResponse struct {
	VLMConfig    VLMConfig              `json:"vlm_config"`
	UpdateStatus DeploymentUpdateStatus `json:"update_status"`
}

type UpgradeVSAClusterDeploymentResponse struct {
	VLMConfig     VLMConfig              `json:"vlm_config"`
	UpgradeStatus DeploymentUpdateStatus `json:"upgrade_status"`
	OntapVersion  string                 `json:"ontap_version"`
}

type DeleteVSAClusterDeploymentRequest struct {
	CloudProvider  string                `json:"cloud_provider,omitempty"`
	DeploymentID   string                `json:"deployment_id,omitempty"`
	ProjectID      string                `json:"project_id,omitempty"`
	ProviderConfig ProviderConfigWrapper `json:"provider_config,omitempty"`
}

// DeployVSACluster deploys a VSA cluster using the provided deployment configuration.
type CreateVSAClusterDeploymentRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`        // VLM configuration
	OntapCredentials OntapCredentials `json:"ontap_credentials"` // ONTAP credentials for the VSA cluster
	OntapLicense     OntapLicense     `json:"ontap_license"`
}

type CreateVSAClusterDeploymentResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type ZoneSwitchRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	Action           string           `json:"action"` // "switch" (VM1→VM2) or "revert" (VM2→VM1)
	AggrNames        []string         `json:"aggr_names,omitempty"`
}

type ZoneSwitchResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type RotateFabricPoolKeysRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`        // VLM configuration
	NewSecretOcid    string           `json:"new_secret_ocid"`   // OCI Vault Secret OCID holding the new access_key/secret_key
	OntapCredentials OntapCredentials `json:"ontap_credentials"` // ONTAP credentials for the VSA cluster
}

type RotateFabricPoolKeysResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type SVMExpansionRequest struct {
	VLMConfig                VLMConfig          `json:"vlm_config"`
	OntapCredentials         OntapCredentials   `json:"ontap_credentials"`
	NewSPHAPairConfig        []SPHAPairConfig   `json:"new_sp_hapair_config"`                   // Aggregate configs for only the newly added HA pairs
	AllocatedProbePortsBySVM map[string][]int32 `json:"allocated_probe_ports_by_svm,omitempty"` // Per-SVM ILB probe ports, keyed by SVM name.
}

type SVMExpansionResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type ExpandVSAClusterRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	NewHAPairConfigs []SPHAPairConfig `json:"new_ha_pair_configs"` // Per-HA-pair configs for the new HA pairs only
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
}

type ExpandVSAClusterResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

// CleanupExpansionResponse carries the pruned VLMConfig after a failed
// expansion has been cleaned up. The new HA pairs, their cloud resources,
// any partially-created aggregates, and associated config entries are removed.
type CleanupExpansionResponse struct {
	Error VLMClientError `json:"error,omitempty"`
}

type AggregateDeleteWorkflowRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	AggrNames        []string         `json:"aggr_names"`
}

type AggregateDeleteWorkflowResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type AsupReq struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	Message          string           `json:"message"`
	VmConfig         VMConfig         `json:"vm_config"`
}

type ValidateClusterHealthRequest struct {
	VLMConfig            VLMConfig        `json:"vlm_config"`
	OntapCredentials     OntapCredentials `json:"ontap_credentials"`
	TriggerASUPOnFailure bool             `json:"trigger_asup_on_failure"`
}

type ClusterPowerOpReq struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	Operation        string           `json:"operation"`
}

type ClusterPowerOpResp struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

// GCP only
type GetResourceInfoReq struct {
	ProjectID    string `json:"project_id"`
	DeploymentID string `json:"deployment_id"`
}

// GCP only
type GetResourceInfoResp struct {
	ProjectID    string              `json:"project_id"`
	DeploymentID string              `json:"deployment_id"`
	ResourceInfo ResourceInformation `json:"resource_info"`
}

type OntapLicense struct {
	SecretUri []string `json:"secret_uri"`
}

type UpdateLicenseRequest struct {
	OntapLicense     OntapLicense     `json:"ontap_license"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	VSAManagementIP  string           `json:"vsa_management_ip"` // VSA management IP for the VM
	Provider         string           `json:"provider"`          // Provider for the license
}

type PlacementPolicyConfig struct {
	GCPPlacementPolicyConfig GCPPlacementPolicyConfig `json:"gcp_placement_policy_config"`
}

type GCPPlacementPolicyConfig struct {
	PolicyConfig       string `json:"policy_config"`         // SPREAD_SINGLE, COMPACT_SINGLE, SPREAD_MULTI, COMPACT_MULTI
	CompactMaxDistance int32  `json:"compact_max_distance"`  // Compact configs only: Max distance for COMPACT configs
	SpreadMultiADCount int32  `json:"spread_multi_ad_count"` // When > 0, copied into spread_ad_count via SyncSpreadADCountFromMulti
	SpreadADCount      int32  `json:"spread_ad_count"`       // Canonical AD count used for all spread placement policies
}

// Cloud-agnostic provider config wrappers.
//
// Each wrapper hides the concrete provider type (GCP, OCI, Azure) behind an
// interface so shared structs like DeploymentConfig don't leak provider-
// specific fields. JSON round-tripping is handled by a factory registered
// at startup via SetActiveProvider.
//
// Usage:
//
//	vlm.SetActiveProvider(vlm.OCICloud) // once, at process start
//	oci, err := cfg.ProviderConfig.AsOCI()

// providerFactory wires up constructors for the active cloud provider.
// Registered once at startup; used by every wrapper's UnmarshalJSON.
type providerFactory struct {
	NewProviderConfig     func() interface{}
	NewProviderDiskConfig func() interface{}
	NewProviderNetConfig  func() interface{}
	NewProviderDevFlags   func() interface{}
}

var (
	activeFactory *providerFactory
	factoryOnce   sync.Once
)

// SetActiveProvider registers the concrete types for the running cloud
// provider. Must be called once at process startup before any JSON
// unmarshalling of wrapper types (e.g. in main() or TestMain()).
func SetActiveProvider(provider string) {
	factories := map[string]*providerFactory{
		OCICloud: {
			NewProviderConfig:     func() interface{} { return &OCIConfig{} },
			NewProviderDiskConfig: func() interface{} { return &OCIDiskConfig{} },
			NewProviderNetConfig:  func() interface{} { return &OCINetworkConfig{} },
			NewProviderDevFlags:   func() interface{} { return &OCIDevFlags{} },
		},
		GCPCloud: {
			NewProviderConfig:     func() interface{} { return &GCPConfig{} },
			NewProviderDiskConfig: func() interface{} { return &GCPDiskConfig{} },
			NewProviderNetConfig:  func() interface{} { return &GCPNetworkConfig{} },
			NewProviderDevFlags:   func() interface{} { return &GCPDevFlags{} },
		},
	}
	f, ok := factories[provider]
	if !ok {
		return
	}
	factoryOnce.Do(func() {
		activeFactory = f
	})
}

// ResetActiveProvider tears down the factory so a different provider can be
// set. Test-only -- production code calls SetActiveProvider exactly once.
func ResetActiveProvider() {
	factoryOnce = sync.Once{}
	activeFactory = nil
}

func getFactory() (*providerFactory, error) {
	if activeFactory == nil {
		return nil, fmt.Errorf("vlm: no active provider set; call SetActiveProvider first")
	}
	return activeFactory, nil
}

func unmarshalWithFactory(newFn func() interface{}, data []byte) (interface{}, error) {
	if newFn == nil {
		return nil, fmt.Errorf("vlm: factory function is nil for this wrapper type")
	}
	v := newFn()
	if err := json.Unmarshal(data, v); err != nil {
		return nil, err
	}
	return v, nil
}

// AsProviderType extracts a concrete provider type from an interface value.
// Handles both pointer and value receivers from the factory.
func AsProviderType[T any](v interface{}) (T, error) {
	if v == nil {
		var zero T
		return zero, fmt.Errorf("vlm: provider config is nil")
	}
	if ptr, ok := v.(*T); ok {
		return *ptr, nil
	}
	if val, ok := v.(T); ok {
		return val, nil
	}
	var zero T
	return zero, fmt.Errorf("vlm: cannot convert %T to %T", v, zero)
}

// ProviderConfig abstracts hyperscaler-specific deployment configuration.
// Implemented by GCPConfig, OCIConfig (and AzureConfig when onboarded).
type ProviderConfig interface {
	GetProvider() string
}

func (g GCPConfig) GetProvider() string { return GCPCloud }
func (o OCIConfig) GetProvider() string { return OCICloud }

// ProviderConfigWrapper holds a ProviderConfig with custom JSON handling
// so the concrete type survives serialization across Temporal boundaries.
type ProviderConfigWrapper struct {
	ProviderConfig
}

func (w *ProviderConfigWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewProviderConfig, data)
	if err != nil {
		return err
	}
	pc, ok := v.(ProviderConfig)
	if !ok {
		return fmt.Errorf("vlm: factory returned %T which does not implement ProviderConfig", v)
	}
	w.ProviderConfig = pc
	return nil
}

func (w ProviderConfigWrapper) MarshalJSON() ([]byte, error) {
	if w.ProviderConfig == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.ProviderConfig)
}

func (w ProviderConfigWrapper) AsOCI() (OCIConfig, error) {
	return AsProviderType[OCIConfig](w.ProviderConfig)
}

func (w ProviderConfigWrapper) AsGCP() (GCPConfig, error) {
	return AsProviderType[GCPConfig](w.ProviderConfig)
}

// ProviderDiskConfig abstracts hyperscaler-specific disk configuration.
// Implemented by GCPDiskConfig, OCIDiskConfig.
type ProviderDiskConfig interface {
	GetDiskConfigProvider() string
}

func (g GCPDiskConfig) GetDiskConfigProvider() string { return GCPCloud }
func (o OCIDiskConfig) GetDiskConfigProvider() string { return OCICloud }

// ProviderDiskConfigWrapper holds a ProviderDiskConfig with custom JSON
// handling, same pattern as ProviderConfigWrapper.
type ProviderDiskConfigWrapper struct {
	ProviderDiskConfig
}

func (w *ProviderDiskConfigWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewProviderDiskConfig, data)
	if err != nil {
		return err
	}
	dc, ok := v.(ProviderDiskConfig)
	if !ok {
		return fmt.Errorf("vlm: factory returned %T which does not implement ProviderDiskConfig", v)
	}
	w.ProviderDiskConfig = dc
	return nil
}

func (w ProviderDiskConfigWrapper) MarshalJSON() ([]byte, error) {
	if w.ProviderDiskConfig == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.ProviderDiskConfig)
}

func (w ProviderDiskConfigWrapper) AsOCI() (OCIDiskConfig, error) {
	return AsProviderType[OCIDiskConfig](w.ProviderDiskConfig)
}

func (w ProviderDiskConfigWrapper) AsGCP() (GCPDiskConfig, error) {
	return AsProviderType[GCPDiskConfig](w.ProviderDiskConfig)
}

// ProviderNetworkConfig abstracts hyperscaler-specific network configuration.
// Implemented by GCPNetworkConfig, OCINetworkConfig.
type ProviderNetworkConfig interface {
	GetNetConfigProvider() string
}

func (g GCPNetworkConfig) GetNetConfigProvider() string { return GCPCloud }
func (o OCINetworkConfig) GetNetConfigProvider() string { return OCICloud }

// ProviderNetworkConfigWrapper holds a ProviderNetworkConfig with custom JSON
// handling, same pattern as ProviderConfigWrapper.
type ProviderNetworkConfigWrapper struct {
	ProviderNetworkConfig
}

func (w *ProviderNetworkConfigWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewProviderNetConfig, data)
	if err != nil {
		return err
	}
	nc, ok := v.(ProviderNetworkConfig)
	if !ok {
		return fmt.Errorf("vlm: factory returned %T which does not implement ProviderNetworkConfig", v)
	}
	w.ProviderNetworkConfig = nc
	return nil
}

func (w ProviderNetworkConfigWrapper) MarshalJSON() ([]byte, error) {
	if w.ProviderNetworkConfig == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.ProviderNetworkConfig)
}

func (w ProviderNetworkConfigWrapper) AsOCI() (OCINetworkConfig, error) {
	return AsProviderType[OCINetworkConfig](w.ProviderNetworkConfig)
}

func (w ProviderNetworkConfigWrapper) AsGCP() (GCPNetworkConfig, error) {
	return AsProviderType[GCPNetworkConfig](w.ProviderNetworkConfig)
}

// ProviderDevFlags abstracts hyperscaler-specific development/debug flags.
// Implemented by OCIDevFlags, GCPDevFlags.
type ProviderDevFlags interface {
	GetDevFlagsProvider() string
}

// OCIDevFlags captures OCI-only dev flags that were previously on DevFlags
// directly. Moved here so they don't leak into GCP/Azure configs.
type OCIDevFlags struct {
	AllowNonDenseShapeForVsa bool `json:"allow_non_dense_shape_for_vsa,omitempty"`
	UseSecondaryIPsForLIFs   bool `json:"use_secondary_ips_for_lifs,omitempty"`
}

// GCPDevFlags is a placeholder; GCP has no provider-specific dev flags today.
type GCPDevFlags struct{}

func (o OCIDevFlags) GetDevFlagsProvider() string { return OCICloud }
func (g GCPDevFlags) GetDevFlagsProvider() string { return GCPCloud }

// ProviderDevFlagsWrapper holds a ProviderDevFlags with custom JSON handling,
// same pattern as ProviderConfigWrapper.
type ProviderDevFlagsWrapper struct {
	ProviderDevFlags
}

func (w *ProviderDevFlagsWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewProviderDevFlags, data)
	if err != nil {
		return err
	}
	df, ok := v.(ProviderDevFlags)
	if !ok {
		return fmt.Errorf("vlm: factory returned %T which does not implement ProviderDevFlags", v)
	}
	w.ProviderDevFlags = df
	return nil
}

func (w ProviderDevFlagsWrapper) MarshalJSON() ([]byte, error) {
	if w.ProviderDevFlags == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.ProviderDevFlags)
}

func (w ProviderDevFlagsWrapper) AsOCI() (OCIDevFlags, error) {
	return AsProviderType[OCIDevFlags](w.ProviderDevFlags)
}

func (w ProviderDevFlagsWrapper) AsGCP() (GCPDevFlags, error) {
	return AsProviderType[GCPDevFlags](w.ProviderDevFlags)
}
