// IMPORTANT: This is the VLM workflow datamodel file.
// We shouldn't edit this from the VCP side unless a newer version is shared by the VLM team.
package vlm

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"
)

const (
	GCPCloud                  string = "gcp"
	OCICloud                  string = "oci"
	AzureCloud                string = "azure"
	DeploymentTypeSharedHA    string = "shared_ha"
	DeploymentTypeNonSharedHA string = "non_shared_ha"

	CorrelationIDKey    string = "x-correlation-id"
	DeploymentIDKey     string = "x-deployment-id"
	OpcWorkRequestIDKey string = "opc-work-request-id"
	OpcRequestIDKey     string = "opc-request-id"
	DeploymentIDTagKey  string = "deployment_id"
)

// OCIRequestContext carries OCI-MP HTTP headers through Temporal workflow requests.
// Embedded in OCIConfig so workflow request structs remain cloud-agnostic.
type OCIRequestContext struct {
	OpcWorkRequestID string `json:"opc_work_request_id,omitempty"`
	OpcRequestID     string `json:"opc_request_id,omitempty"`
}

func (o OCIRequestContext) GetOpcWorkRequestID() string {
	return strings.TrimSpace(o.OpcWorkRequestID)
}

func (o OCIRequestContext) GetOpcRequestID() string {
	return strings.TrimSpace(o.OpcRequestID)
}

// OCIRequestContextIDs returns OCI-MP header values from an OCI provider config.
// Non-OCI or unset provider configs return empty strings and must not affect callers.
func OCIRequestContextIDs(pc ProviderConfigWrapper) (workRequestID, opcRequestID string) {
	oci, err := pc.AsOCI()
	if err != nil {
		return "", ""
	}
	return oci.GetOpcWorkRequestID(), oci.GetOpcRequestID()
}

// VLMWorkflowName is the name of the workflow
const (
	CreateVSAClusterDeploymentWorkflowName       = "vlm.CreateVSAClusterDeploymentWorkflow"
	CreateVSASVMWorkflowName                     = "vlm.CreateVSASVMWorkflow"
	UpgradeLifBackendForNasWorkflowName          = "vlm.UpgradeLifBackendForNasWorkflow"
	ModifyVSASVMWorkflowName                     = "vlm.ModifyVSASVMWorkflow"
	DeleteVSASVMWorkflowName                     = "vlm.DeleteVSASVMWorkflow"
	DeleteVSAClusterDeploymentWorkflowName       = "vlm.DeleteVSAClusterDeploymentWorkflow"
	UpdateVSAClusterDeploymentWorkflowName       = "vlm.UpdateVSAClusterDeploymentWorkflow"
	UpgradeVSAClusterDeploymentWorkflowName      = "vlm.UpgradeVSAClusterDeploymentWorkflow"
	PreUpgradeVSAClusterDeploymentWorkflowName   = "vlm.PreUpgradeVSAClusterDeploymentWorkflow"
	VSASvmUpgradeWorkflowName                    = "vlm.VSASvmUpgradeWorkflow"
	ClusterPowerCycleWorkflowName                = "vlm.ClusterPowerCycle"
	ClusterHealthCheckWorkflowName               = "vlm.ClusterHealthCheck"
	GetClusterZiZsDetailsWorkflowName            = "vlm.GetClusterZiZsDetails"
	UpdateVSAMediatorWorkflowName                = "vlm.UpdateVSAMediatorWorkflow"
	MediatorMaintenanceRebootMigrateWorkflowName = "vlm.MediatorMaintenanceRebootMigrateWorkflow"
	MediatorMaintenanceRecreateWorkflowName      = "vlm.MediatorMaintenanceRecreateWorkflow"
	CreateVSAExpertModeUserWorkflowName          = "vlm.CreateVSAExpertModeUserWorkflow"
	AddExpertModeUserWorkflowName                = "vlm.AddExpertModeUserWorkflow"
	UpdateLicenseWorkflowName                    = "vlm.UpdateLicenseWorkflow"
	ASUPTriggerWaitWorkflowName                  = "vlm.ASUPTriggerWaitWorkflow"
	ExpandVSAClusterWorkflowName                 = "vlm.ExpandVSAClusterWorkflow"
	CleanupExpansionWorkflowName                 = "vlm.CleanupExpansionWorkflow"
	ZoneSwitchWorkflowName                       = "vlm.ZoneSwitchWorkflow"
	AggregateDeleteWorkflowName                  = "vlm.AggregateDeleteWorkflow"
	RotateFabricPoolKeysWorkflowName             = "vlm.RotateFabricPoolKeysWorkflow"
	MaintenanceWorkflowName                      = "vlm.MaintenanceWorkflow"
	VSAMediatorMaintenanceWorkflowName           = "vlm.VSAMediatorMaintenanceWorkflow" // vcp-vlm merged, DONT USE THIS

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
	// RebootMigrate reboots a VM and relocates it to a healthy hypervisor. On OCI
	// this maps to the instance action REBOOTMIGRATE; it is used by the mediator
	// and the maintenance workflow to evacuate a node from a host undergoing
	// maintenance.
	RebootMigrate string = "reboot_migrate"

	ZiZsComputeInstanceKey string = "compute_instance"
	ZiZsComputeDiskKey     string = "compute_disk"

	ZoneSwitchActionSwitch      string = "switch"
	ZoneSwitchActionRevert      string = "revert"
	ZoneSwitchActionRevertForce string = "revert_force"

	// Single placement policy for all VMs with separate availability-domain assignment per leg
	GCPPlacementPolicySpreadSingle string = "spread_single"
	// Single compact placement policy for all VMs
	GCPPlacementPolicyCompactSingle string = "compact_single"
	// Separate placement policies per zone for regional (multi-zone) deployments
	GCPPlacementPolicySpreadMulti string = "spread_multi"
	// Separate compact placement policies per zone for regional deployments
	GCPPlacementPolicyCompactMulti string = "compact_multi"
	// No placement policy created/applied
	GCPPlacementPolicyNone string = "none"
)

// TODO: Need to revisit these values for Multi HA configurations
var WorkflowExecutionTimeoutMap map[string]time.Duration = map[string]time.Duration{
	"DefaultWorkflowExecutionTimeout":            10 * time.Minute,
	CreateVSAClusterDeploymentWorkflowName:       30 * time.Minute,
	CreateVSASVMWorkflowName:                     30 * time.Minute,
	UpgradeLifBackendForNasWorkflowName:          30 * time.Minute,
	ModifyVSASVMWorkflowName:                     30 * time.Minute,
	DeleteVSASVMWorkflowName:                     25 * time.Minute,
	DeleteVSAClusterDeploymentWorkflowName:       30 * time.Minute,
	UpdateVSAClusterDeploymentWorkflowName:       120 * time.Minute,
	UpgradeVSAClusterDeploymentWorkflowName:      300 * time.Minute,
	PreUpgradeVSAClusterDeploymentWorkflowName:   120 * time.Minute,
	VSASvmUpgradeWorkflowName:                    10 * time.Minute,
	ClusterPowerCycleWorkflowName:                40 * time.Minute,
	ClusterHealthCheckWorkflowName:               15 * time.Minute,
	GetClusterZiZsDetailsWorkflowName:            10 * time.Minute,
	UpdateVSAMediatorWorkflowName:                30 * time.Minute,
	UpdateLicenseWorkflowName:                    10 * time.Minute,
	ExpandVSAClusterWorkflowName:                 60 * time.Minute,
	CleanupExpansionWorkflowName:                 30 * time.Minute,
	CreateVSAExpertModeUserWorkflowName:          45 * time.Minute,
	ZoneSwitchWorkflowName:                       60 * time.Minute,
	AggregateDeleteWorkflowName:                  30 * time.Minute,
	RotateFabricPoolKeysWorkflowName:             10 * time.Minute,
	MaintenanceWorkflowName:                      120 * time.Minute,
	MediatorMaintenanceRebootMigrateWorkflowName: 30 * time.Minute,
	MediatorMaintenanceRecreateWorkflowName:      30 * time.Minute,
	VSAMediatorMaintenanceWorkflowName:           30 * time.Minute, // vcp-vlm merged, DONT USE THIS
}

// MaxUpgradeWorkflowExecutionTimeout caps the HA-pair-scaled timeout for the
// cluster upgrade workflow so that a wedged upgrade on large clusters cannot
// run for many hours before failing. Other workflows retain their scaled
// timeout unchanged.
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

	Tags                 string            `json:"tags"`                   // Comma separated list of tags to be attached for the VMs created by the deployment
	Labels               map[string]string `json:"labels"`                 // List of labels to attach to resources
	UserBootargs         string            `json:"user_boot_args"`         // The input is a list of key-value pairs with semicolons as delimiters.
	UserCustomdata       map[string]string `json:"user_custom_data"`       // Additional Custom data to be passed to the VMs by user
	DeploymentType       string            `json:"deployment_type"`        // SingeNode or ShareHA or NonShareHA
	NumHAPair            int               `json:"num_ha_pair"`            // Number of HA pairs to be created
	VSAInstanceType      string            `json:"vsa_instance_type"`      // rename to VSAInstanceType
	MediatorInstanceType string            `json:"mediator_instance_type"` // rename to MediatorInstanceType
	DataDiskType         string            `json:"data_disk_type"`         // Move to GCP config ?
	SystemDiskType       string            `json:"system_disk_type"`       // Move to GCP config ?
	MediatorDiskType     string            `json:"mediator_disk_type"`     // Move to GCP config ?
	DataDiskCount        int               `json:"data_disk_count"`        // Number of data disks to be created

	VSASystemDiskConfig map[OntapDiskType]DiskConfig `json:"vsa_system_disk_config"` // System disk configuration for VSA

	// TODO: check if zone wise netconfig is required
	NetConfig      map[VSALIFType]NetworkConfig `json:"net_config"`      // Network configuration for the deployment
	GCPConfig      GCPConfig                    `json:"gcpconfig"`       // GCP specific configuration
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
	DisableAzureVNetCreation      bool                    `json:"disable_azure_vnet_creation,omitempty"`       // Skip Azure VNet/subnet/NSG creation and use existing network // VCP-VLM merged, added
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

// FlexInstanceSpec captures flexible VM CPU/memory sizing for providers that support resizable shapes.
type FlexInstanceSpec struct {
	Cpu      float32 `json:"cpu,omitempty"`
	MemoryGB float32 `json:"memory_gb,omitempty"`
}

type OCIConfig struct {
	OCIRequestContext
	CompartmentID              string                            `json:"compartment_id"` // OCI Compartment ID
	SubnetID                   string                            `json:"subnet_id"`
	DataNICSubnetID            string                            `json:"data_nic_subnet_id"`
	DataDiskVpus               *int64                            `json:"data_disk_vpus"`                   // Data disk VPUs
	AvailabilityDomain         AvailabilityDomainInfo            `json:"availability_domain_info"`         // OCI Availability Domain Info
	FaultDomain                FaultDomainInfo                   `json:"fault_domain_info"`                // OCI Fault Domain Info (AD per FD key)
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

// GCPNetworkConfig GCP-specific network config for a NIC.
type GCPNetworkConfig struct {
	// IsSecondaryIP When true, the NIC uses a secondary/alias IP path (GCNV additional IP range). The reserved nic3 primary is tracked as dataprimary.
	IsSecondaryIP bool `json:"is_secondary_ip,omitempty"`
	// SkipIPReservation When true, skip IP reservation for this NIC (used with secondary IP / dataprimary flows).
	SkipIPReservation bool `json:"skip_ip_reservation,omitempty"`
	// SubnetProjectID Project ID for the subnet.
	SubnetProjectID string `json:"subnet_project_id,omitempty"`
}

type OCINetworkConfig struct {
	SubnetOCID string `json:"subnet_ocid"` // OCID for the subnet
}

type AzureNetworkConfig struct { // VCP-VLM merged, added full struct
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
	Size            string           `json:"size"`                        // Size of the storage pool
	IOps            int64            `json:"iops"`                        // IOPS for the storage pool
	Throughput      int64            `json:"tput"`                        // Throughput for the storage pool
	IsHeterogeneous bool             `json:"is_heterogeneous"`            // When true, HAPairConfigs carry per-HA-pair config set by VCP
	HAPairConfigs   []SPHAPairConfig `json:"sp_ha_pair_config,omitempty"` // Per-HA-pair configuration; populated via PopulateSPHAPairConfig before use
}

// SPHAPairConfig holds the storage pool configuration for a single HA pair.
// The HA pair index is the position in the HAPairConfigs slice (not stored).
// AggrConfigs contains one entry for active-passive (shared HA) deployments
// and two entries for active-active (non-shared HA) deployments where each
// node has its own aggregate.
type SPHAPairConfig struct {
	InstanceType string       `json:"instance_type"` // VM instance type for both nodes in this HA pair
	AggrConfigs  []AggrConfig `json:"aggr_configs"`
}

// AggrConfig holds the configuration for a single aggregate within an HA pair.
type AggrConfig struct {
	Size       string   `json:"size"`                // Aggregate size (e.g. "500Gi")
	IOps       int64    `json:"iops"`                // Aggregate IOPS
	Throughput int64    `json:"throughput"`          // Aggregate throughput
	AggrName   string   `json:"aggr_name,omitempty"` // Backing aggregate name, populated by VLM (e.g. "aggr1")
	SvmNames   []string `json:"svm_names,omitempty"` // SVM names for the aggregate
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
	IsUpdate                  bool             `json:"is_update"`                     // true if the expert mode role is being updated
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
	Region      string `json:"region"`                 // Added
	Zone        string `json:"zone"`                   // Added
	FaultDomain string `json:"fault_domain,omitempty"` // OCI fault domain for the VM within its availability domain (Zone)

	Name                  string                   `json:"name"`      // Name of the VM
	HostName              string                   `json:"host_name"` // Available during cluster create.
	SerialNumber          string                   `json:"serial_number"`
	NodeIndex             int                      `json:"node_index"` // Added
	IsMediator            bool                     `json:"is_mediator"`
	SystemLIFs            map[VSALIFType]LIFConfig `json:"lifs"`                    // List of IPs for the VM
	SystemDisks           []DiskConfig             `json:"system_disks"`            // List of system disks for the VM
	DataDisks             []DiskConfig             `json:"data_disks"`              // List of data disks for the VM
	VSAManagementIP       string                   `json:"vsa_management_ip"`       // VSA management IP for the VM
	AdditionalVmResources AdditionalVmResources    `json:"additional_vm_resources"` // additional resources
	VMInstanceID          string                   `json:"vm_instance_id"`          // VM instance ID
	BootDiskID            string                   `json:"boot_disk_id"`            // Boot disk ID (cloud-agnostic)   // VCP-VLM merged, added
	VMBootDiskID          string                   `json:"vm_boot_disk_id"`         // OCI Boot disk ID  // VCP-VLM merged, changed from boot_disk_id to vm_boot_disk_id
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
	LIFTypeCluster           VSALIFType = "clus"
	LIFTypeInterCluster      VSALIFType = "intercluster"
	LIFTypeRSM               VSALIFType = "rsm"
	LIFTypeSan               VSALIFType = "san"
	LIFTypeNas               VSALIFType = "nas"
	LIFTypeMediator          VSALIFType = "mediator"
	LIFTypeIlbNas            VSALIFType = "ilbnas"
	LIFTypeRbac              VSALIFType = "rbac"
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
	AzureNetworkConfig    AzureNetworkConfig           `json:"azure_network_config,omitempty"` // VCP-VLM merged, added
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

// SvmAttachedAggregate records one data aggregate assigned to an SVM at create time.
type SvmAttachedAggregate struct {
	AggrName string `json:"aggr_name"`
}

type SvmConfig struct {
	Svmname string `json:"svm_name"`
	Svmuuid string `json:"svm_uuid"`
	// Map of Lifs for SVM. Can be either nas or iscsi.
	// SVM can have multiple iSCSI lifs, hence it is maintained as a slice of LIFConfigs
	SVMLIFs SvmLIFConfigs `json:"svm_lifs"`
	// AttachedAggregates lists data aggregates this SVM may use. Empty means legacy attach-all.
	AttachedAggregates []SvmAttachedAggregate `json:"attached_aggregates,omitempty"`
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
	ResourceStatus     string                    `json:"resource_status,omitempty"`      // Status of the resource
	Zone               string                    `json:"zone,omitempty"`                 // Zone for the disk
	IsAttached         bool                      `json:"is_attached,omitempty"`          // True if the disk is currently attached to a VM instance
	GCPDiskConfig      GCPDiskConfig             `json:"gcp_disk_config,omitempty"`      // GCP specific disk configuration
	ProviderDiskConfig ProviderDiskConfigWrapper `json:"provider_disk_config,omitempty"` // Provider specific disk configuration
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
	DiskOciID           string                            `json:"disk_oci_id"`         // OCID for the disk
	CompartmentID       string                            `json:"compartment_id"`      // OCI Compartment ID
	FreeFormTags        map[string]string                 `json:"freeform_tags"`       // Free form tags for OCI resources
	DefinedTags         map[string]map[string]interface{} `json:"defined_tags"`        // Defined tags for OCI resources
	CmekOcid            string                            `json:"cmek_ocid,omitempty"` // CMEK OCID to encrypt the disk if given.
	// Add other OCI-specific fields here if needed
}

type CreateSVMRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	Name             string           `json:"name"` // SVM name
	DnsDomains       string           `json:"dns_ip"`
	NameServers      string           `json:"servers"`                      // List of servers
	OntapCredentials OntapCredentials `json:"ontap_credentials"`            // ONTAP credentials for the VSA cluster
	SvmAdminPassword string           `json:"svm_admin_password,omitempty"` // SVM Admin Password
	EnableNasLif     bool             `json:"enable_nas_lif"`               // When true, VLM creates NAS LIF + ILB during SVM creation
	// AggrNames selects data aggregates (sub-pools) for this SVM. Empty attaches all data_aggr.
	AggrNames []string `json:"aggr_names,omitempty"`
	// SanLif/NasLif enable reduced-LIF mode scoped by AggrNames. SanLif places a SAN
	// pair on each eligible HA pair; NasLif places NAS on each eligible node.
	SanLif bool `json:"san_lif,omitempty"`
	NasLif bool `json:"nas_lif,omitempty"`
}

type CreateSVMResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type UpgradeLifBackendForNasRequest struct {
	VLMConfig VLMConfig `json:"vlm_config"`
	SVMName   string    `json:"svm_name"` // SVM name
}

type UpgradeLifBackendForNasResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type ModifySVMRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	Name             string           `json:"name"`                         // SVM name
	OntapCredentials OntapCredentials `json:"ontap_credentials"`            // ONTAP credentials for the VSA cluster
	SvmAdminPassword string           `json:"svm_admin_password,omitempty"` // SVM Admin Password
}

type ModifySVMResponse struct {
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

type DeleteSVMRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	Name             string           `json:"name"`              // SVM name
	OntapCredentials OntapCredentials `json:"ontap_credentials"` // ONTAP credentials for the VSA cluster
}

type DeleteSVMResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type UpdateVSAClusterDeploymentRequest struct {
	VLMConfig                VLMConfig             `json:"vlm_config"`                       // VLM configuration
	NumHAPair                int                   `json:"num_ha_pair"`                      // Number of HA pairs to be created
	SPConfig                 SPConfig              `json:"spconfig"`                         // Storagepool specific configuration
	OntapCredentials         OntapCredentials      `json:"ontap_credentials"`                // ONTAP credentials for the VSA cluster
	NewInstanceType          string                `json:"new_instance_type"`                // Instance type for the storage pool
	DataDiskVpus             *int64                `json:"data_disk_vpus,omitempty"`         // Data Disk VPUs // VCP-VLM merged, added omitempty
	VSAFlexOcpus             *float32              `json:"vsa_flex_ocpus,omitempty"`         // OCI only: VSA flex OCPUs for update (applied to VLMConfig when set)
	VSAFlexMemoryInGBs       *float32              `json:"vsa_flex_memory_in_gbs,omitempty"` // OCI only: VSA flex memory for update (applied to VLMConfig when set)
	OntapUpgrade             OntapUpgradeConfig    `json:"ontap_upgrade"`                    // ONTAP upgrade configuration
	HAPairIndices            []int                 `json:"ha_pair_indices"`                  // Selected HA pair indices for targeted operations
	ITCRecovery              bool                  `json:"itc_recovery"`                     // Flag to indicate if this is a recovery operation (ITC)
	DisableNativeITC         bool                  `json:"disable_native_itc"`               // GCP only: default false uses in-place native ITC; true selects VM-replacement ITC. Ignored on OCI (always replacement).
	BucketName               string                `json:"bucket_name"`                      // GCP Bucket Name
	AutoTierThreshold        int64                 `json:"auto_tier_threshold"`              // Auto tiering threshold percentage (0-100)
	AutoTierThresholdFlag    bool                  `json:"auto_tier_threshold_flag"`         // Auto tiering threshold flag
	AllowHAPairLimitOverride bool                  `json:"allow_ha_pair_limit_override"`     // Allow selected callers (e.g. CLI) to bypass HA pair selection limit
	CmekOcid                 string                `json:"cmek_ocid"`                        // Update the CMEK encryption key
	ProviderConfig           ProviderConfigWrapper `json:"provider_config,omitempty"`        // Provider specific configuration. The new values to be updated are in this field. // VCP-VLM merged, added omitempty
}

type UpdateMediatorRequest struct {
	VLMConfig              VLMConfig                     `json:"vlm_config"`                         // VLM configuration
	MediatorUpdate         MediatorUpdateConfig          `json:"mediator_update"`                    // Mediator update configuration
	OntapCredentials       OntapCredentials              `json:"ontap_credentials"`                  // ONTAP credentials for the VSA cluster
	HAPairIndices          []int                         `json:"ha_pair_indices"`                    // Indices of the HA pairs to update (empty or nil to update all)
	MediatorProviderConfig MediatorProviderConfigWrapper `json:"mediator_provider_config,omitempty"` // Mediator provider configuration // VCP-VLM merged, added modified data type
}

type UpdateMediatorResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

// RotateFabricPoolKeysRequest is the input to vlm.RotateFabricPoolKeysWorkflow.
// The OCI control plane mints a new access_key/secret_key pair, writes it under
// a new OCI Vault Secret OCID, and asks VLM to swap the cloud target on ONTAP
// over to the new credentials.
type RotateFabricPoolKeysRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`                   // VLM configuration
	NewSecretOcid    string           `json:"new_secret_ocid"`              // OCI Vault Secret OCID holding the new access_key/secret_key
	NewSecretVersion *int64           `json:"new_secret_version,omitempty"` // OCI Vault Secret version; nil fetches the current version
	OntapCredentials OntapCredentials `json:"ontap_credentials"`            // ONTAP credentials for the VSA cluster
}

// RotateFabricPoolKeysResponse returns the updated VLMConfig with
// Deployment.OCIConfig.FabricPoolConfig.SecretOcid and SecretVersion updated
// once ONTAP has accepted the new credentials.
type RotateFabricPoolKeysResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type OntapUpgradeConfig struct {
	SkipOntapImageVersionMatch     bool   `json:"skip_ontap_image_version_match"`     // Skip Image version match for upgrade
	OntapUpgradeTargetImageVersion string `json:"ontap_upgrade_target_image_version"` // Image version for upgrade
	OntapUpgradeImagePath          string `json:"ontap_upgrade_image_path"`           // Image path for upgrade
	RunPreUpgrade                  bool   `json:"run_preupgrade"`                     // Run pre-upgrade workflow before upgrade
	// VSAImageName is the post-upgrade VSA image name the caller (VCP /
	// vlmcli) wants recorded in VLMConfig.Deployment.Images.VSAImageName
	// once the upgrade workflow completes successfully. It is a required
	// input; UpgradeVSAClusterDeploymentWorkflow stamps it onto
	// upgradeResp.VLMConfig.Deployment.Images before returning. The mediator
	// image name is owned by MediatorUpdateConfig and is supplied via the
	// separate UpdateVSAMediatorWorkflow path.
	VSAImageName string `json:"vsa_image_name,omitempty"` // Post-upgrade VSA image name // VCP-VLM merged, added omitempty
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
	ProviderConfig ProviderConfigWrapper `json:"provider_config,omitempty"` // Hyperscaler specific configuration
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

type ZoneSwitchRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	Action           string           `json:"action"` // "switch", "revert", or "revert_force"
	// AggrNames selects which HA pair(s) to zone-switch (by data aggregate name).
	// Empty means the first aggregate name found in the deployment (first HA pair).
	AggrNames []string `json:"aggr_names,omitempty"`
}

type ZoneSwitchResponse struct {
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
	OntapLicense     OntapLicense          `json:"ontap_license"`
	OntapCredentials OntapCredentials      `json:"ontap_credentials"`
	VSAManagementIP  string                `json:"vsa_management_ip"` // VSA management IP for the VM
	Provider         string                `json:"provider"`          // Provider for the license
	ProviderConfig   ProviderConfigWrapper `json:"provider_config,omitempty"`
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

// ----------------------------- Provider abstraction -----------------------------

func GetProviderFromEnv() string {
	provider := os.Getenv("PROVIDER")
	if provider == "" {
		return ""
	}
	return provider
}

// AsProviderType extracts a concrete provider type from an interface value.
func AsProviderType[T any](p any) (T, error) {
	var zero T
	if p == nil {
		return zero, nil
	}
	rv := reflect.ValueOf(p)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if rv.IsNil() {
			return zero, nil
		}
	}
	v, ok := p.(T)
	if !ok {
		// Accept a non-nil pointer to T and return its value so callers get the
		// same zero-value/defaulting behavior as the pre-generic helpers.
		if rv.Kind() == reflect.Ptr && rv.Elem().IsValid() {
			if deref, ok := rv.Elem().Interface().(T); ok {
				return deref, nil
			}
		}
		return zero, fmt.Errorf("expected %T, got %T", zero, p)
	}
	return v, nil
}

// The provider-specific config types below use a small interface + wrapper
// pattern so request structs can stay provider-agnostic while still preserving
// concrete OCI/GCP types across JSON marshal/unmarshal. The wrappers serialize
// the embedded concrete value and rebuild it via the factory selected from the
// current provider environment.
//
// ProviderFactory holds factory functions for all provider-specific types.
// To add a new provider, add a single entry to providerFactories.
type ProviderFactory struct {
	NewProviderConfig         func() any
	NewProviderDiskConfig     func() any
	NewProviderNetworkConfig  func() any
	NewProviderDevFlags       func() any
	NewMediatorProviderConfig func() any
}

var providerFactories = map[string]ProviderFactory{
	OCICloud: {
		NewProviderConfig:         func() any { return &OCIConfig{} },
		NewProviderDiskConfig:     func() any { return &OCIDiskConfig{} },
		NewProviderNetworkConfig:  func() any { return &OCINetworkConfig{} },
		NewProviderDevFlags:       func() any { return &OCIDevFlags{} },
		NewMediatorProviderConfig: func() any { return &OCIMediatorProviderConfig{} },
	},
	GCPCloud: {
		NewProviderConfig:         func() any { return &GCPConfig{} },
		NewProviderDiskConfig:     func() any { return &GCPDiskConfig{} },
		NewProviderNetworkConfig:  func() any { return &GCPNetworkConfig{} },
		NewProviderDevFlags:       func() any { return &GCPDevFlags{} },
		NewMediatorProviderConfig: func() any { return &GCPMediatorProviderConfig{} },
	},
}

// unmarshalWithFactory unmarshals raw JSON into a concrete type created by the
// given factory function. The factory returns a pointer; the dereferenced value
// is returned so callers receive a value type (e.g. OCIConfig, not *OCIConfig).
func unmarshalWithFactory(factory func() any, data []byte) (any, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	v := factory()
	if err := json.Unmarshal(data, v); err != nil {
		return nil, fmt.Errorf("failed to unmarshal as %T: %w", v, err)
	}
	return reflect.ValueOf(v).Elem().Interface(), nil
}

// getFactory returns the ProviderFactory for the current provider or an error.
// Wrapper unmarshaling depends on the active provider environment rather than a
// provider discriminator embedded in the JSON payload.
func getFactory() (ProviderFactory, error) {
	provider := GetProviderFromEnv()
	f, ok := providerFactories[provider]
	if !ok {
		return ProviderFactory{}, fmt.Errorf("provider %q is not supported", provider)
	}
	return f, nil
}

// --- ProviderConfig ---

// ProviderConfig is implemented by provider-specific deployment config structs.
type ProviderConfig interface {
	GetProvider() string
}

func (g GCPConfig) GetProvider() string { return GCPCloud }
func (o OCIConfig) GetProvider() string { return OCICloud }

// ProviderConfigWrapper stores provider-specific config behind an interface and
// provides custom JSON handling so the concrete type survives serialization.
type ProviderConfigWrapper struct {
	ProviderConfig
}

// UnmarshalJSON rebuilds the concrete provider config selected by getFactory.
func (w *ProviderConfigWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewProviderConfig, data)
	if err != nil {
		return err
	}
	if pc, ok := v.(ProviderConfig); ok {
		w.ProviderConfig = pc
	}
	return nil
}

func (w ProviderConfigWrapper) MarshalJSON() ([]byte, error) {
	if w.ProviderConfig == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.ProviderConfig)
}

// AsOCI and AsGCP expose the embedded provider config as a concrete type.
func (w ProviderConfigWrapper) AsOCI() (OCIConfig, error) {
	return AsProviderType[OCIConfig](w.ProviderConfig)
}

func (w ProviderConfigWrapper) AsGCP() (GCPConfig, error) {
	return AsProviderType[GCPConfig](w.ProviderConfig)
}

// --- ProviderDiskConfig ---

// ProviderDiskConfig is implemented by provider-specific disk config structs.
type ProviderDiskConfig interface {
	GetDiskConfigProvider() string
}

func (g GCPDiskConfig) GetDiskConfigProvider() string { return GCPCloud }
func (o OCIDiskConfig) GetDiskConfigProvider() string { return OCICloud }

// ProviderDiskConfigWrapper preserves the concrete disk config type across JSON
// marshal/unmarshal while keeping request structs provider-agnostic.
type ProviderDiskConfigWrapper struct {
	ProviderDiskConfig
}

// UnmarshalJSON rebuilds the concrete provider disk config selected by getFactory.
func (w *ProviderDiskConfigWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewProviderDiskConfig, data)
	if err != nil {
		return err
	}
	if dc, ok := v.(ProviderDiskConfig); ok {
		w.ProviderDiskConfig = dc
	}
	return nil
}

func (w ProviderDiskConfigWrapper) MarshalJSON() ([]byte, error) {
	if w.ProviderDiskConfig == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.ProviderDiskConfig)
}

// AsOCI and AsGCP expose the embedded provider disk config as a concrete type.
func (w ProviderDiskConfigWrapper) AsOCI() (OCIDiskConfig, error) {
	return AsProviderType[OCIDiskConfig](w.ProviderDiskConfig)
}

func (w ProviderDiskConfigWrapper) AsGCP() (GCPDiskConfig, error) {
	return AsProviderType[GCPDiskConfig](w.ProviderDiskConfig)
}

// --- ProviderNetworkConfig ---

// ProviderNetworkConfig is implemented by provider-specific network config structs.
type ProviderNetworkConfig interface {
	GetNetConfigProvider() string
}

func (g GCPNetworkConfig) GetNetConfigProvider() string { return GCPCloud }
func (o OCINetworkConfig) GetNetConfigProvider() string { return OCICloud }

// ProviderNetworkConfigWrapper preserves the concrete network config type across
// JSON marshal/unmarshal while keeping request structs provider-agnostic.
type ProviderNetworkConfigWrapper struct {
	ProviderNetworkConfig
}

// UnmarshalJSON rebuilds the concrete provider network config selected by getFactory.
func (w *ProviderNetworkConfigWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewProviderNetworkConfig, data)
	if err != nil {
		return err
	}
	if nc, ok := v.(ProviderNetworkConfig); ok {
		w.ProviderNetworkConfig = nc
	}
	return nil
}

func (w ProviderNetworkConfigWrapper) MarshalJSON() ([]byte, error) {
	if w.ProviderNetworkConfig == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.ProviderNetworkConfig)
}

// AsOCI and AsGCP expose the embedded provider network config as a concrete type.
func (w ProviderNetworkConfigWrapper) AsOCI() (OCINetworkConfig, error) {
	return AsProviderType[OCINetworkConfig](w.ProviderNetworkConfig)
}

func (w ProviderNetworkConfigWrapper) AsGCP() (GCPNetworkConfig, error) {
	return AsProviderType[GCPNetworkConfig](w.ProviderNetworkConfig)
}

// --- ProviderDevFlags ---

// ProviderDevFlags is implemented by provider-specific development flag structs.
type ProviderDevFlags interface {
	GetDevFlagsProvider() string
}

type OCIDevFlags struct {
	AllowNonDenseShapeForVsa bool `json:"allow_non_dense_shape_for_vsa,omitempty"` // Allow using non DenseIO shapes for VSA Node VMs
	UseSecondaryIPsForLIFs   bool `json:"use_secondary_ips_for_lifs,omitempty"`    // OCI: use secondary IPs for all LIFs (movable between VMs); false = use primary IPs (existing behaviour)
	// Optional OCI QoS set-value scaler for ONTAP node tuning payloads.
	// Unset keeps existing behavior (100% of computed max values).
	MaxQoSPercentage *float64 `json:"max_qos_percentage,omitempty"`
}

type GCPDevFlags struct{}

func (o OCIDevFlags) GetDevFlagsProvider() string { return OCICloud }
func (g GCPDevFlags) GetDevFlagsProvider() string { return GCPCloud }

// ProviderDevFlagsWrapper preserves the concrete provider dev flags type across
// JSON marshal/unmarshal while keeping the parent config provider-agnostic.
type ProviderDevFlagsWrapper struct {
	ProviderDevFlags
}

// UnmarshalJSON rebuilds the concrete provider dev flags selected by getFactory.
func (w *ProviderDevFlagsWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewProviderDevFlags, data)
	if err != nil {
		return err
	}
	if df, ok := v.(ProviderDevFlags); ok {
		w.ProviderDevFlags = df
	}
	return nil
}

func (w ProviderDevFlagsWrapper) MarshalJSON() ([]byte, error) {
	if w.ProviderDevFlags == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.ProviderDevFlags)
}

// AsOCI and AsGCP expose the embedded provider dev flags as a concrete type.
func (w ProviderDevFlagsWrapper) AsOCI() (OCIDevFlags, error) {
	return AsProviderType[OCIDevFlags](w.ProviderDevFlags)
}

func (w ProviderDevFlagsWrapper) AsGCP() (GCPDevFlags, error) {
	return AsProviderType[GCPDevFlags](w.ProviderDevFlags)
}

// --- MediatorProviderConfig ---

type MediatorProviderConfig interface {
	GetMediatorProviderConfigProvider() string
}

type OCIMediatorProviderConfig struct {
	MediatorImageOcid string `json:"mediator_image_ocid,omitempty"`
}

type GCPMediatorProviderConfig struct{}

func (o OCIMediatorProviderConfig) GetMediatorProviderConfigProvider() string { return OCICloud }
func (g GCPMediatorProviderConfig) GetMediatorProviderConfigProvider() string { return GCPCloud }

// MediatorProviderConfigWrapper preserves the concrete Mediator Provider config type across
// JSON marshal/unmarshal while keeping the parent config provider-agnostic.
type MediatorProviderConfigWrapper struct {
	MediatorProviderConfig
}

// UnmarshalJSON rebuilds the concrete Mediator Provider config selected by getFactory.
func (w *MediatorProviderConfigWrapper) UnmarshalJSON(data []byte) error {
	f, err := getFactory()
	if err != nil {
		return err
	}
	v, err := unmarshalWithFactory(f.NewMediatorProviderConfig, data)
	if err != nil {
		return err
	}
	if mpc, ok := v.(MediatorProviderConfig); ok {
		w.MediatorProviderConfig = mpc
	}
	return nil
}

func (w MediatorProviderConfigWrapper) MarshalJSON() ([]byte, error) {
	if w.MediatorProviderConfig == nil {
		return []byte("null"), nil
	}
	return json.Marshal(w.MediatorProviderConfig)
}

// AsOCI, AsGCP, and AsAzure expose the embedded provider dev flags as a concrete type.
func (w MediatorProviderConfigWrapper) AsOCI() (OCIMediatorProviderConfig, error) {
	return AsProviderType[OCIMediatorProviderConfig](w.MediatorProviderConfig)
}

func (w MediatorProviderConfigWrapper) AsGCP() (GCPMediatorProviderConfig, error) {
	return AsProviderType[GCPMediatorProviderConfig](w.MediatorProviderConfig)
}

// AggregateDeleteWorkflowRequest contains the parameters for deleting a set of
// aggregates from the cluster.  Aggregates are classified by HA pair: if every
// data aggregate in an HA pair is being deleted the entire HA pair is removed;
// otherwise only the targeted aggregates are offlined and deleted.
type AggregateDeleteWorkflowRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	AggrNames        []string         `json:"aggr_names"`
}

// AggregateDeleteWorkflowResponse contains the updated VLMConfig after
// aggregate deletion.  Fully-removed HA pairs and their resources are pruned;
// partially-removed aggregates are stripped from DataAggr.
type AggregateDeleteWorkflowResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

type UpdateDeploymentAttributesRequest struct {
	VLMConfig      VLMConfig             `json:"vlm_config"`
	ProviderConfig ProviderConfigWrapper `json:"provider_config"`
}

type FabricPoolConfig struct {
	BucketName    string `json:"bucket_name"`
	SecretOcid    string `json:"secret_ocid"`
	SecretVersion *int64 `json:"secret_version,omitempty"` // nil fetches the current OCI Vault Secret version
	Namespace     string `json:"namespace"`
	ServerURL     string `json:"server_url"`
}

type MediatorMaintenanceRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	HAPairIndices    []int            `json:"ha_pair_indices"`
}

type ModifyMediatorDisksOwnershipAndCheckFailoverStateRequest struct {
	VLMConfig        VLMConfig        `json:"vlm_config"` // vcp-vlm merged, dont use
	HAPair           HAPair           `json:"ha_pair"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	HAPairIndex      int              `json:"ha_pair_index"` // vcp-vlm merged, dont use
}

type MediatorRecreateRequest struct {
	DeployConfig     DeploymentConfig `json:"deploy_config"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
	HAPair           HAPair           `json:"ha_pair"`
}

type MediatorRecreateResponse struct {
	VM VMConfig `json:"vm"`
}

// MaintenanceTarget is one entry of the MaintenanceWorkflow input contract: the
// HA pair to service and the node name(s) in that pair. NodeNames may name one or
// both legs of the pair; when both are present the per-pair child services them
// strictly in sequence (partner-health-gated) so the pair is never fully evacuated
// at once. The node's ONTAP UUID is intentionally not part of it: VCP does not
// provide it, so VLM resolves it at runtime (via the healthy HA partner).
type MaintenanceTarget struct {
	HaPairIndex int      `json:"ha_pair_index"`
	NodeNames   []string `json:"node_names"`
}

// MaintenanceRequest is the input to MaintenanceWorkflow. VLMConfig carries the
// cluster topology (HA pairs) used by the safety gates and to resolve each
// target's partner node. MaintenanceType selects the recovery strategy and
// applies to every target node in the request.
type MaintenanceRequest struct {
	VLMConfig        VLMConfig           `json:"vlm_config"`
	OntapCredentials OntapCredentials    `json:"ontap_credentials"`
	MaintenanceType  string              `json:"maintenance_type"`
	Targets          []MaintenanceTarget `json:"targets"`
}

// MaintenanceResponse aggregates the per-HA-pair results. VLMConfig is the
// cluster config after recovery: recreate refreshes OCID/management IP for
// reconstructed nodes so the caller (e.g. vlmcli) can write the current state
// back to its --json_output_file, mirroring every other deployment workflow.
type MaintenanceResponse struct {
	Results   []PairMaintenanceResult `json:"results"`
	VLMConfig VLMConfig               `json:"vlm_config"`
}

// PairMaintenanceResult is the per-node outcome surfaced to the caller.
type PairMaintenanceResult struct {
	HaPairIndex int    `json:"ha_pair_index"`
	NodeName    string `json:"node_name"`
	Succeeded   bool   `json:"succeeded"`
	Error       string `json:"error,omitempty"`
}
