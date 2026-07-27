package datamodel

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"gorm.io/gorm"
)

type Pool struct {
	BaseModel
	Name                   string             `gorm:"column:name"`
	Description            string             `gorm:"column:description"`
	State                  string             `gorm:"column:state"`
	StateDetails           string             `gorm:"column:state_details"`
	VendorID               string             `gorm:"column:vendor_id"`
	PoolExternalIdentifier string             `gorm:"column:pool_external_identifier;type:text;uniqueIndex:idx_pools_pool_external_identifier_unique,where:pool_external_identifier <> ''"`
	ServiceLevel           string             `gorm:"column:service_level"`
	SizeInBytes            int64              `gorm:"column:size_in_bytes"`
	UsedBytes              int64              `gorm:"column:used_bytes"`
	Network                string             `gorm:"column:network;type:varchar(2048)"`
	AllowAutoTiering       bool               `gorm:"column:allow_auto_tiering;default:false"`
	AccountID              int64              `gorm:"column:account_id"`
	Account                *Account           `gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	PoolAttributes         *PoolAttributes    `gorm:"column:pool_attributes;type:jsonb"`
	ClusterDetails         ClusterDetails     `gorm:"column:cluster_details;type:jsonb"`
	QosType                string             `gorm:"column:qos_type"`
	AutoTieringConfig      *AutoTieringConfig `gorm:"column:auto_tiering_config;type:jsonb"`
	ServiceAccountId       string             `gorm:"column:service_account_id;type:text"`
	KmsConfigID            sql.NullInt64      `gorm:"index"`
	KmsConfig              *KmsConfig         `gorm:"ForeignKey:KmsConfigID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	DeploymentName         string             `gorm:"column:deployment_name;uniqueIndex:idx_account_deployment"`
	// HostUUID links PO pools to the hardware cluster (hosts.uuid).
	HostUUID        string           `gorm:"column:host_uuid;index"`
	PoolCredentials *PoolCredentials `gorm:"column:pool_credentials;type:jsonb"`
	SnHostProject   string           `gorm:"column:sn_host_project;index"`
	VLMConfig       string           `gorm:"vlm_config;type:text"`
	LargeCapacity   bool             `gorm:"column:large_capacity;default:false"`
	IsPreview       bool             `gorm:"column:is_preview;default:false"`
	SatisfyZI       bool             `gorm:"column:satisfy_zi;default:false"`
	SatisfyZS       bool             `gorm:"column:satisfy_zs;default:false"`
	AssetMetadata   *AssetMetadata   `gorm:"column:asset_metadata;type:jsonb"`
	// Build information - images used to create this pool
	BuildInfo               *PoolBuildInfo         `gorm:"column:build_info;type:jsonb" json:"buildInfo,omitempty"`
	ActiveDirectoryID       sql.NullInt64          `gorm:"column:active_directory_id"`
	ActiveDirectory         *ActiveDirectory       `gorm:"ForeignKey:ActiveDirectoryID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	ActiveDirectoryChangeId string                 `gorm:"column:active_directory_change_id;type:text"`
	APIAccessMode           string                 `gorm:"column:api_access_mode;type:text"`
	ExpertModeCredentials   *ExpertModeCredentials `gorm:"column:expert_mode_credentials;type:jsonb"`
	WorkflowID              string                 `gorm:"column:workflow_id;type:text"`
}

// IsActiveActive reports whether the pool is a true-zonal (active-active) deployment.
func (p *Pool) IsActiveActive() bool {
	return p != nil && p.PoolAttributes != nil && p.PoolAttributes.IsTrueZonal
}

type ExpertModeCredentials struct {
	ExpertModeCredential []*ExpertModeCredential `json:"expert_mode_credential"`
}

// Value implements the driver.Valuer interface for GORM
func (emc ExpertModeCredentials) Value() (driver.Value, error) {
	return json.Marshal(emc)
}

// Scan implements the sql.Scanner interface for GORM
func (emc *ExpertModeCredentials) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(bytes, emc)
}

type ExpertModeCredential struct {
	SecretID         string `json:"secret_id"`
	CertificateID    string `json:"certificate_id"`
	CertificateIDNew string `json:"certificate_id_new"`
	Password         string `json:"password"`
	Username         string `json:"username"`
	AuthType         int    `json:"auth_type"`
}

// Value implements the driver.Valuer interface for GORM
func (emc ExpertModeCredential) Value() (driver.Value, error) {
	return json.Marshal(emc)
}

// Scan implements the sql.Scanner interface for GORM
func (emc *ExpertModeCredential) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("cannot scan %T into ExpertModeCredential", value)
	}

	return json.Unmarshal(bytes, emc)
}

type PoolCredentials struct {
	SecretID         string `json:"secret_id"`
	SecretIDNew      string `json:"secret_id_new"`
	CertificateID    string `json:"certificate_id"`
	CertificateIDNew string `json:"certificate_id_new"`
	Password         string `json:"password"`
	AuthType         int    `json:"auth_type"`
	Username         string `json:"username"`

	// Certificate-related configuration (stored from environment variables during pool creation)
	// Format: ca_pool_deployed_project_id/ca_pool_name/ca_name
	// Note: Region and VCPAdmin remain as environment variables
	CaURI string `json:"ca_uri,omitempty"`

	ExternalSecret      *ExternalCredRef `json:"external_secret,omitempty"`
	ExternalCertificate *ExternalCredRef `json:"external_certificate,omitempty"`

	ExternalClientCertificate *ExternalCredRef `json:"external_client_certificate,omitempty"`

	ExpertModeSecret *ExternalCredRef `json:"expert_mode_secret,omitempty"`
}

// ExternalCredRef is a lookup-key reference to an external secret-store entry
// (currently always an OCI Vault secret on the OCI ONTAP path). It is the
// persistence shape used by PoolCredentials.ExternalSecret/ExternalCertificate
// and also referenced by core/models.Node for the in-memory representation.
// ExternalIdentifier holds the secret-store-native identifier (an OCID for OCI
// Vault). Name carries the human-readable secret name, and Version is the
// numeric secret version; together they form the lookup tuple.
type ExternalCredRef struct {
	ExternalIdentifier string `json:"external_identifier"`
	Name               string `json:"name"`
	Version            int64  `json:"version"`
}
type AssetMetadata struct {
	ChildAssets []ChildAsset `json:"child_assets"`
}

type ChildAsset struct {
	AssetNames []string `json:"asset_names"`
	AssetType  string   `json:"asset_type"`
}

// PoolBuildInfo represents the build information for a pool
type PoolBuildInfo struct {
	VSABuildImage      string    `json:"vsaBuildImage"`
	MediatorBuildImage string    `json:"mediatorBuildImage"`
	OntapVersion       string    `json:"ontapVersion"`
	BuildTimestamp     time.Time `json:"buildTimestamp,omitempty"`
	RbacFileHash       string    `json:"rbacFileHash"`
	RbacFileUrl        string    `json:"rbacFileUrl"`
	// VLMWorkerFeature is the VLM_WORKER_FEATURE_FLAG feature this pool was created with (empty when the
	// pool was created via the legacy account-allowlist path).
	VLMWorkerFeature string `json:"vlmWorkerFeature,omitempty"`
	// VLMWorkerVersion is the VLM worker task queue version resolved at create time. It is used to route
	// delete to the same VLM worker that created the pool.
	VLMWorkerVersion string `json:"vlmWorkerVersion,omitempty"`
}

// Scan implements the Scanner interface for PoolBuildInfo
func (pbi *PoolBuildInfo) Scan(value interface{}) error {
	if value == nil {
		*pbi = PoolBuildInfo{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, pbi)
}

// Value implements the Valuer interface for PoolBuildInfo
func (pbi PoolBuildInfo) Value() (driver.Value, error) {
	return json.Marshal(pbi)
}

// serviceLevelFLEX is the service level value for shared (non-dedicated) pools.
const serviceLevelFLEX = "FLEX"

// IsDedicatedCapacity reports whether the pool runs on dedicated hardware,
// derived from the service level: any service level other than FLEX is a
// dedicated-hardware tier (STANDARD, PREMIUM, EXTREME, etc.).
func (p *Pool) IsDedicatedCapacity() bool {
	return p.ServiceLevel != serviceLevelFLEX
}

type PoolView struct {
	Pool
	Throughput           float64 `json:"throughput"` // Stores the utilized throughput
	Iops                 int64   `json:"iops"`       // Stores the utilized iops
	QuotaInBytes         uint64  `json:"quotaInBytes"`
	VolumeCount          int64   `json:"volumeCount"`
	ThinCloneVolumeCount int64   `json:"cloneCount"`
}

type ClusterDetails struct {
	ExternalName          string         `json:"external_name"`
	OntapVersion          string         `json:"ontap_version"`
	RegionalTenantProject string         `json:"regional_tenant_project"`
	CompartmentOCID       string         `json:"compartment_ocid,omitempty"`
	SnHostProject         string         `json:"sn_host_project"`
	Network               string         `json:"network"`
	SubnetNames           []string       `json:"subnet_names"`
	AllocatedSubnetCIDR   string         `json:"allocated_subnet_cidr,omitempty"`
	InterclusterLifIPs    []string       `json:"intercluster_lifs,omitempty"`
	ReservedIPsInSubnet   *[]SubnetToIPs `json:"reserved_ips_in_subnet,omitempty"`
}

type SubnetToIPs struct {
	SubnetName  string `json:"subnet_name"`
	IPsReserved int64  `json:"ips_reserved"`
}

type PoolAttributes struct {
	ThroughputMibps                  int64    `json:"throughput"`
	Iops                             int64    `json:"iops"`
	PrimaryZone                      string   `json:"primary_zone"`
	SecondaryZone                    string   `json:"secondary_zone"`
	MediatorZone                     string   `json:"mediator_zone"`
	Labels                           *JSONB   `json:"labels"`
	IsRegionalHA                     bool     `json:"is_regional_ha"`
	IsZoneSwitched                   bool     `json:"is_zone_switched"`
	ZoneSwitchState                  string   `json:"zone_switch_state"`
	LdapEnabled                      bool     `json:"ldap_enabled"`
	AccountName                      string   `json:"account_name"`
	ServiceAccountPermissionProjects []string `json:"service_account_permission_projects,omitempty"`
	IsTrueZonal                      bool     `json:"is_true_zonal"`

	// ServiceLevel is the persisted PO tier (EXTREME or PREMIUM). It is retained
	// so per-tier QoS and describe/list stay consistent after create.
	ServiceLevel string `json:"service_level,omitempty"`
}

// Node represents the public.nodes table in the database
type Node struct {
	BaseModel
	Name            string       `gorm:"column:name;type:text"`
	Description     string       `gorm:"column:description;type:text"`
	State           string       `gorm:"column:state;type:text"`
	StateDetails    string       `gorm:"column:state_details;type:text"`
	EndpointAddress string       `gorm:"column:endpoint_Address;type:text"`
	HostDNSName     string       `gorm:"column:host_dns_name;type:text"`
	NodeAttributes  *NodeDetails `gorm:"column:node_attributes;type:jsonb"`
	PoolID          int64        `gorm:"column:pool_id;type:bigint"`
	ZoneName        string       `gorm:"column:zone_name;type:text"`
	FaultDomain     string       `gorm:"column:fault_domain;type:text"`
	AccountID       int64        `gorm:"column:account_id;type:bigint"`
}

// Scan implements the Scanner interface for PoolAttributes
func (pa *PoolAttributes) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, pa)
}

// Value implements the Valuer interface for PoolAttributes
func (pa LargeVolumeAttributes) Value() (driver.Value, error) {
	return json.Marshal(pa)
}

// Scan implements the Scanner interface for PoolAttributes
func (pa *LargeVolumeAttributes) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, pa)
}

// Value implements the Valuer interface for PoolAttributes
func (pa PoolAttributes) Value() (driver.Value, error) {
	return json.Marshal(pa)
}

// Scan implements the sql.Scanner interface for ClusterDetails
func (cd *ClusterDetails) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, cd)
}

// Value implements the driver.Valuer interface for ClusterDetails
func (cd ClusterDetails) Value() (driver.Value, error) {
	return json.Marshal(cd)
}

// Scan implements the Scanner interface for PoolCredentials
func (pc *PoolCredentials) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, pc)
}

// Value implements the Valuer interface for PoolCredentials
func (pc PoolCredentials) Value() (driver.Value, error) {
	return json.Marshal(pc)
}

// GetCaURIWithFallback gets ca_uri from PoolCredentials, falling back to environment variables if not set.
func (pc *PoolCredentials) GetCaURIWithFallback() string {
	if pc == nil || pc.CaURI == "" {
		return env.BuildCaURI("", "", "")
	}
	return pc.CaURI
}

// ParseCaURIWithFallback parses ca_uri from PoolCredentials, falling back to environment variables if not set.
func (pc *PoolCredentials) ParseCaURIWithFallback() (caPoolDeployedProjectID, caPoolName, caName string) {
	if pc == nil || pc.CaURI == "" {
		return env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName
	}
	return env.ParseCaURI(pc.CaURI)
}

// Scan implements the Scanner interface for AssetMetadata
func (am *AssetMetadata) Scan(value interface{}) error {
	if value == nil {
		*am = AssetMetadata{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, am)
}

// Value implements the Valuer interface for AssetMetadata
func (am AssetMetadata) Value() (driver.Value, error) {
	return json.Marshal(am)
}

type Volume struct {
	BaseModel
	Name                     string                  `gorm:"column:name"`
	Description              string                  `gorm:"column:description"`
	State                    string                  `gorm:"column:state"`
	StateDetails             string                  `gorm:"column:state_details"`
	Health                   string                  `gorm:"column:health"`
	MountPath                string                  `gorm:"column:mount_path"`
	SizeInBytes              int64                   `gorm:"column:size_in_bytes"`
	Throughput               int64                   `gorm:"column:throughput"`
	AccountID                int64                   `gorm:"column:account_id"`
	PoolID                   int64                   `gorm:"column:pool_id"`
	SvmID                    int64                   `gorm:"column:svm_id"`
	VolumePerformanceGroupID sql.NullInt64           `gorm:"column:volume_performance_group_id"`
	Account                  *Account                `gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	Pool                     *Pool                   `gorm:"ForeignKey:PoolID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	Svm                      *Svm                    `gorm:"ForeignKey:SvmID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	VolumePerformanceGroup   *VolumePerformanceGroup `gorm:"ForeignKey:VolumePerformanceGroupID;AssociationForeignKey:ID"`
	VolumeAttributes         *VolumeAttributes       `gorm:"column:volume_attributes;type:jsonb"`
	DataProtection           *DataProtection         `gorm:"column:data_protection;type:jsonb"`
	SnapshotPolicy           *SnapshotPolicy         `gorm:"column:snapshot_policy;type:jsonb"`
	UsedBytes                uint64                  `gorm:"column:used_bytes"`
	AutoTieringEnabled       bool                    `gorm:"column:auto_tiering_enabled"`
	AutoTieringPolicy        *AutoTieringPolicy      `gorm:"column:auto_tiering_policy;type:jsonb"`
	HotTierSizeGib           uint64                  `gorm:"column:hot_tier_size_gib"`
	ColdTierSizeGib          uint64                  `gorm:"column:cold_tier_size_gib"`
	CacheParameters          *CacheParameters        `gorm:"column:cache_parameters;type:jsonb"`
	LargeVolumeAttributes    *LargeVolumeAttributes  `gorm:"column:large_volume_attributes;type:jsonb"`
	ClonesSharedBytes        uint64                  `gorm:"column:clones_shared_bytes"`
	ClusterPeerID            sql.NullInt64           `gorm:"column:cluster_peer_id"`
}

// VolumePerformanceGroup represents a pool-scoped performance policy group used for manual QoS.
// It maps to the `volume_performance_groups` table.
type VolumePerformanceGroup struct {
	BaseModel
	Name             string `gorm:"column:name"`
	PoolID           int64  `gorm:"column:pool_id"`
	Pool             *Pool  `gorm:"ForeignKey:PoolID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	AllocationType   string `gorm:"column:allocation_type;not null"`
	IsAutoGen        bool   `gorm:"column:is_auto_gen;not null;default:false"`
	ThroughputMibps  int64  `gorm:"column:throughput_mibps"`
	Iops             int64  `gorm:"column:iops"`
	OntapQosPolicyID string `gorm:"column:ontap_qos_policy_id"`
	Description      string `gorm:"column:description;type:text"`
	State            string `gorm:"column:state"`
	StateDetails     string `gorm:"column:state_details;type:text"`
	Labels           *JSONB `json:"labels" gorm:"column:labels;type:jsonb"`
}

const (
	AllocationTypeShared    = "SHARED"
	AllocationTypePerVolume = "PER_VOLUME"
)

func (v *VolumePerformanceGroup) IsShared() bool {
	return v != nil && v.AllocationType == AllocationTypeShared
}

func (v *VolumePerformanceGroup) IsPerVolume() bool {
	return v != nil && v.AllocationType == AllocationTypePerVolume
}

// JSONB is a custom type to handle JSONB columns in PostgreSQL
type JSONB map[string]interface{}

// Scan implements the Scanner interface for JSONB
func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = make(JSONB) // Initialize to an empty map
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, j)
}

// Value implements the Valuer interface for JSONB
func (j JSONB) Value() (driver.Value, error) {
	return json.Marshal(j)
}

type ResourceAttributes struct {
	PoolID int64 `json:"pool_id"`
}

// Scan implements the Scanner interface for ResourceAttributes
func (ra *ResourceAttributes) Scan(value interface{}) error {
	if value == nil {
		*ra = ResourceAttributes{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, ra)
}

// Value implements the Valuer interface for ResourceAttributes
func (ra ResourceAttributes) Value() (driver.Value, error) {
	return json.Marshal(ra)
}

type PendingResourceDeletions struct {
	ID                 int64               `json:"id" gorm:"primaryKey"`
	CreatedAt          time.Time           `json:"createdAt"`
	UpdatedAt          time.Time           `json:"updatedAt"`
	DeletedAt          *gorm.DeletedAt     `gorm:"index" json:"deletedAt"`
	ResourceType       string              `gorm:"column:resource_type;type:text;not null"`
	ResourceName       string              `gorm:"column:resource_name;type:text;not null;uniqueIndex"`
	RetryCounter       int64               `gorm:"column:retry_counter;type:bigint"`
	Error              string              `gorm:"column:error;type:text"`
	AccountName        string              `gorm:"column:account_name;type:text"`
	ResourceAttributes *ResourceAttributes `gorm:"column:resource_attributes;type:jsonb"`
}

type VolumeAttributes struct {
	CreationToken      string           `json:"creation_token"`
	Protocols          []string         `json:"protocols"`
	VendorSubnetID     string           `json:"vendor_subnet_id"`
	ExternalUUID       string           `json:"external_uuid"`
	BlockProperties    *BlockProperties `json:"block_properties"`
	BlockDevices       *[]BlockDevice   `json:"block_devices"`
	FileProperties     *FileProperties  `json:"file_properties"`
	IsDataProtection   bool             `json:"is_data_protection"`
	Mounted            bool             `json:"mounted"`
	SnapReserve        int64            `json:"snap_reserve"`
	SnapshotDirectory  bool             `json:"snapshot_directory"`
	KerberosEnabled    bool             `json:"kerberos_enabled"`
	LdapEnabled        bool             `json:"ldap_enabled"`
	Labels             *JSONB           `json:"labels"`
	RestoredBackupID   string           `json:"restored_backup_id"`
	RestoredBackupPath string           `json:"restored_backup_path"`
	AccountName        string           `json:"account_name"`
	DeploymentName     string           `json:"deployment_name"`
	IsRegionalHA       bool             `json:"is_regional_ha"`
	CloneParentInfo    *CloneParentInfo `json:"clone_parent_info"`
	// OriginalSharedBytes captures the parent-snapshot logical size at clone
	// creation time. It is the immutable baseline used by the synchronous
	// splitStop API to derive a meaningful remaining-shared-bytes value after a
	// partial split: splitStart zeroes the volume row's clones_shared_bytes to
	// reserve pool capacity, so the at-creation value cannot be recovered from
	// that column alone once a split has been initiated.
	//
	// Placed as a sibling of CloneParentInfo (not inside it) so the shallow-
	// copy + selective-overwrite pattern used by the refresh activity and
	// UpdateCloneParentStateInDB preserves it automatically without explicit
	// handling.
	//
	// Pointer + omitempty so legacy rows (created before this field existed)
	// can be detected and degrade gracefully in splitStop.
	OriginalSharedBytes                *uint64 `json:"original_shared_bytes,omitempty"`
	SplitRegularVolumeHydrationPending bool    `json:"split_regular_volume_hydration_pending,omitempty"`
	SecurityStyle                      string  `json:"security_style"`
	// SplitJobUUID holds the ONTAP job UUID returned by InitiateSplitVolume.
	// It is written before starting the Temporal poll workflow so the orphan-job
	// processor can reconstruct the workflow arguments if Temporal was unavailable
	// when the split API call was made.
	SplitJobUUID      string   `json:"split_job_uuid,omitempty"`
	RestrictedActions []string `json:"restricted_actions"`
}

type BlockProperties struct {
	OSType           string            `json:"os_type"`
	HostGroupDetails []HostGroupDetail `json:"host_group_details"`
	LunName          string            `json:"lun_name"`
	LunSerialNumber  string            `json:"serial_number"`
	LunUUID          string            `json:"lun_uuid"`
}

// BlockDevice describes a block device within a volume
type BlockDevice struct {
	Name             string            `json:"name"`
	HostGroupDetails []HostGroupDetail `json:"host_group_details"`
	Identifier       string            `json:"identifier"`
	Size             int64             `json:"size"`
	OSType           string            `json:"os_type"`
	LunUUID          string            `json:"lun_uuid"`
}

type FileProperties struct {
	ExportPolicy     *ExportPolicy `json:"export_policy"`
	JunctionPath     string        `json:"junction_path"`
	Fqdn             string        `json:"fqdn"`
	SMBShareSettings []string      `json:"smb_share_settings"`
	SecurityStyle    string        `json:"security_style"`
	UnixPermissions  string        `json:"unix_permissions"`
}

type ExportPolicy struct {
	ExportPolicyName string        `json:"export_policy_name"`
	ExportRules      []*ExportRule `json:"export_rules"`
}

type ExportRule struct {
	AllowedClients      string `json:"allowed_clients"`
	AnonymousUser       string `json:"anonymous_user"`
	Index               int    `json:"index"`
	ChownMode           string `json:"chown_mode"`
	AccessType          string `json:"access_type"`
	CIFS                bool   `json:"cifs"`
	NFSv3               bool   `json:"nfsv3"`
	NFSv4               bool   `json:"nfsv4"`
	S3                  bool   `json:"s3"`
	UnixReadOnly        bool   `json:"unix_read_only"`
	UnixReadWrite       bool   `json:"unix_read_write"`
	Kerberos5ReadOnly   bool   `json:"kerberos_5_read_only"`
	Kerberos5ReadWrite  bool   `json:"kerberos_5_read_write"`
	Kerberos5iReadOnly  bool   `json:"kerberos_5_i_read_only"`
	Kerberos5iReadWrite bool   `json:"kerberos_5_i_read_write"`
	Kerberos5pReadOnly  bool   `json:"kerberos_5_p_read_only"`
	Kerberos5pReadWrite bool   `json:"kerberos_5_p_read_write"`
	Superuser           bool   `json:"superuser"`
	AllSquash           *bool  `json:"all_squash,omitempty"`
	AnonUid             *int64 `json:"anon_uid,omitempty"`
}

type CloneParentInfo struct {
	ParentVolumeUUID     string `json:"parent_volume_uuid"`
	ParentSnapshotUUID   string `json:"parent_snapshot_uuid"`
	SplitCompletePercent *int64 `json:"split_complete_percent,omitempty"`
	State                string `json:"state"`
	StateDetails         string `json:"state_details"`
}

type HostGroupDetail struct {
	HostGroupUUID string   `json:"host_group_uuid"`
	HostQNs       []string `json:"host_qns"`
}

type LargeVolumeAttributes struct {
	LargeCapacity               bool   `json:"large_capacity"`
	LargeVolumeConstituentCount *int32 `json:"large_volume_constituent_count"`
}

func (v *VolumeAttributes) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, v)
}

func (v *VolumeAttributes) Value() (driver.Value, error) {
	return json.Marshal(v)
}

type Svm struct {
	BaseModel
	Name                  string           `gorm:"column:name"`
	SvmExternalIdentifier string           `gorm:"column:svm_external_identifier;type:text;uniqueIndex:idx_svms_svm_external_identifier_unique,where:svm_external_identifier <> ''"`
	Description           string           `gorm:"column:description"`
	State                 string           `gorm:"column:state"`
	StateDetails          string           `gorm:"column:state_details"`
	WorkflowID            string           `gorm:"column:workflow_id;type:text"`
	SvmDetails            *SvmDetails      `gorm:"column:svm_details;type:jsonb"`
	AccountID             int64            `gorm:"column:account_id"`
	Account               *Account         `gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	PoolID                int64            `gorm:"column:pool_id"`
	Pool                  *Pool            `gorm:"ForeignKey:PoolID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	KmsConfigID           sql.NullInt64    `json:"kmsConfigID" gorm:"index"`
	KmsConfig             *KmsConfig       `json:"-" gorm:"ForeignKey:KmsConfigID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	ActiveDirectoryID     sql.NullInt64    `gorm:"column:active_directory_id"`
	ActiveDirectory       *ActiveDirectory `gorm:"ForeignKey:ActiveDirectoryID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
}

// SvmView represents an SVM enriched with API response fields that are not persisted.
type SvmView struct {
	Svm
	AccountUUID    string `json:"accountUUID,omitempty"`
	AccountName    string `json:"accountName,omitempty"`
	ClusterUUID    string `json:"clusterUUID,omitempty"`
	VendorSubnetID string `json:"vendorSubnetID,omitempty"`
	IPRangeUUID    string `json:"ipRangeUUID,omitempty"`
}

type Account struct {
	BaseModel
	Name            string           `gorm:"column:name"`
	Description     string           `gorm:"column:description"`
	State           string           `json:"state"`
	StateDetails    string           `gorm:"column:state_details"`
	Tags            string           `json:"tags" gorm:"type:text"`
	AccountMetadata *AccountMetadata `gorm:"column:account_metadata;type:jsonb"`
}

type AccountMetadata struct {
	VolumeRefreshWorkflowLastCompletionAt time.Time         `json:"volumeRefreshWorkflowLastCompletionAt"`
	TrialMode                             *AccountTrialMode `json:"trialMode,omitempty"`
}

// AccountTrialMode is the trial window stored under account_metadata.trialMode (API-aligned JSON).
type AccountTrialMode struct {
	StartTime  *time.Time `json:"startTime,omitempty"`
	EndTime    *time.Time `json:"endTime,omitempty"`
	ExitReason *string    `json:"exitReason,omitempty"`
}

// ApplyTo merges trial fields into meta.TrialMode.
// Nil StartTime, EndTime, or ExitReason clears that field within trialMode. Other metadata keys are preserved.
func (m *AccountTrialMode) ApplyTo(meta *AccountMetadata) {
	if m == nil || meta == nil {
		return
	}
	if meta.TrialMode == nil {
		meta.TrialMode = &AccountTrialMode{}
	}
	if m.StartTime != nil {
		t := *m.StartTime
		meta.TrialMode.StartTime = &t
	} else {
		meta.TrialMode.StartTime = nil
	}
	if m.EndTime != nil {
		t := *m.EndTime
		meta.TrialMode.EndTime = &t
	} else {
		meta.TrialMode.EndTime = nil
	}
	if m.ExitReason != nil {
		s := *m.ExitReason
		meta.TrialMode.ExitReason = &s
	} else {
		meta.TrialMode.ExitReason = nil
	}
}

// Scan method for AccountMetadata to handle JSONB data
func (am *AccountMetadata) Scan(value interface{}) error {
	if value == nil {
		*am = AccountMetadata{}
		return nil
	}

	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(b, am)
}

// Value method for AccountMetadata to handle JSONB data
func (am AccountMetadata) Value() (driver.Value, error) {
	return json.Marshal(am)
}

// BaseModel describes the base model shared by all other database models
type BaseModel struct {
	ID        int64           `json:"id" gorm:"primaryKey"`
	UUID      string          `json:"uuid" gorm:"unique"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	DeletedAt *gorm.DeletedAt `gorm:"index" json:"deletedAt"`
}

// GetID returns a sql.NullInt64 representation of the base model's ID
func (bm *BaseModel) GetID() (id sql.NullInt64) {
	if bm.ID > 0 {
		id.Int64 = bm.ID
		id.Valid = true
	}
	return
}

// Job is a struct that represents the job data model.
type Job struct {
	BaseModel
	CorrelationID string         `json:"correlationID"`
	RequestID     string         `json:"requestID"`
	Type          string         `json:"type"`
	State         string         `json:"state" gorm:"index"`
	TrackingID    int            `json:"trackingID"`
	ErrorDetails  string         `json:"errorDetails"`
	AccountID     sql.NullInt64  `json:"-" gorm:"index"`
	IsAdminJob    bool           `json:"isAdminJob" gorm:"default:false"`
	WorkflowID    string         `json:"workflowID"`
	ScheduledAt   time.Time      `json:"scheduledAt"`
	ResourceName  string         `json:"resourceName"`
	ResourceUUID  string         `json:"resourceUUID" gorm:"column:resource_uuid;size:255;index"`
	JobAttributes *JobAttributes `gorm:"column:job_attributes;type:jsonb"`
}

type JobAttributes struct {
	ResourceUUID         string                 `json:"resource_uuid"`
	PoolUUID             string                 `json:"pool_uuid"`
	VolumeUUID           string                 `json:"volume_uuid,omitempty"`
	CurrentRetryCount    int                    `json:"current_retry_count"`
	Location             string                 `json:"location"`
	PreviousState        string                 `json:"previous_state,omitempty"`         // For UPDATE/DELETE operations
	PreviousStateDetails string                 `json:"previous_state_details,omitempty"` // For UPDATE/DELETE operations
	KmsAttributes        *JobKmsAttributes      `json:"kms_attributes,omitempty"`
	SupervisorAttributes *SupervisorAttributes  `json:"supervisor_attributes,omitempty"`
	PayloadAttributes    map[string]interface{} `json:"payload_attributes,omitempty"`
}

type JobKmsAttributes struct {
	NewKmsKeyURL      string `json:"new_kms_key_url,omitempty"`
	AccountIdentifier string `json:"account_identifier,omitempty"`
}

type SupervisorAttributes struct {
	OverrideGracePeriod time.Duration `json:"override_grace_period"`
}

// Scan method for JobAttributes to handle JSONB data
func (ka *JobAttributes) Scan(value interface{}) error {
	if value == nil {
		*ka = JobAttributes{}
		return nil
	}

	b, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}

	return json.Unmarshal(b, ka)
}

// Value method for JobAttributes to handle JSONB data
func (ka JobAttributes) Value() (driver.Value, error) {
	return json.Marshal(ka)
}

// ExpertModeVolumes represents expert mode volumes in the database
type ExpertModeVolumes struct {
	BaseModel
	Name             string                      `gorm:"column:name"`
	Description      string                      `gorm:"column:description"`
	SizeInBytes      int64                       `gorm:"column:size_in_bytes"`
	AccountID        int64                       `gorm:"column:account_id"`
	PoolID           int64                       `gorm:"column:pool_id"`
	SvmID            int64                       `gorm:"column:svm_id"`
	ExternalUUID     string                      `gorm:"column:external_uuid"`
	State            string                      `gorm:"column:state"`
	Account          *Account                    `gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	Pool             *Pool                       `gorm:"ForeignKey:PoolID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	Svm              *Svm                        `gorm:"ForeignKey:SvmID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	Style            string                      `gorm:"column:style"`                      // {flexvol|flexgroup}
	BackupConfig     *DataProtection             `gorm:"column:data_protection;type:jsonb"` // DataProtection is an existing type which holds backup config in Volume
	VolumeAttributes *ExpertModeVolumeAttributes `gorm:"column:volume_attributes;type:jsonb"`
	SharedBytes      int64                       `gorm:"column:shared_bytes"`
}

type ExpertModeVolumeAttributes struct {
	Protocols   []string             `json:"protocols"`
	IsFlexclone bool                 `json:"isFlexclone,omitempty"`
	Clone       *ExpertModeCloneInfo `json:"clone,omitempty"`
}

func (v *ExpertModeVolumeAttributes) Scan(value interface{}) error {
	if value == nil {
		*v = ExpertModeVolumeAttributes{}
		return nil
	}

	var b []byte
	switch x := value.(type) {
	case []byte:
		b = x
	case string:
		b = []byte(x)
	default:
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(b, v)
}

func (v *ExpertModeVolumeAttributes) Value() (driver.Value, error) {
	return json.Marshal(v)
}

type ExpertModeCloneInfo struct {
	ParentVolume   *ExpertModeCloneParent `json:"parentVolume,omitempty"`
	ParentSnapshot *ExpertModeCloneParent `json:"parentSnapshot,omitempty"`
}

// ExpertModeCloneParent identifies a clone parent volume or snapshot by external UUID and/or name.
type ExpertModeCloneParent struct {
	UUID string `json:"uuid,omitempty"`
	Name string `json:"name,omitempty"`
}

type Lif struct {
	BaseModel
	Name        string        `gorm:"column:name;type:text"`
	Description string        `gorm:"column:description;type:text"`
	LifDetails  *LifDetails   `gorm:"column:lif_details;type:jsonb"`
	AccountID   int64         `gorm:"column:account_id;type:bigint"`
	Type        string        `gorm:"column:type;type:text"`
	IPAddress   string        `gorm:"column:ip_address;type:text"`
	NodeID      int64         `gorm:"column:node_id;type:bigint"`
	SubnetMask  string        `gorm:"column:subnet_mask;type:text"`
	IPRangeID   sql.NullInt64 `gorm:"column:ip_range_id;index"`
	SvmID       sql.NullInt64 `gorm:"column:svm_id;index"`
}

type SvmDetails struct {
	ExternalUUID          string `json:"external_uuid"`
	IPSpace               string `json:"ip_space"`
	NFSv364BitIdentifiers string `json:"nf_sv364_bit_identifiers"`
	ExternalKmsConfigUUID string `json:"external_kms_config_uuid"`
	CurrentKmsKeyID       string `json:"current_kms_key_id,omitempty"` // Tracks which service account key this SVM is currently using during rotation
}

func (sd *SvmDetails) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, &sd)
}

func (sd SvmDetails) Value() (driver.Value, error) {
	return json.Marshal(sd)
}

type LifDetails struct {
	ExternalUUID string `json:"external_uuid"`
	ProtocolType string `json:"protocol_type"`
}

func (nd *LifDetails) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, &nd)
}

func (nd LifDetails) Value() (driver.Value, error) {
	return json.Marshal(nd)
}

type NodeDetails struct {
	ExternalUUID string `json:"external_uuid"`
	InstanceType string `json:"instance_type"`
	SizeInGiB    int64  `json:"size_in_gib,omitempty"`
	// HostID links a node to the hardware cluster (hosts.id) that created it via
	// the /v1/host API. It stays 0 (omitted) for existing rows and for nodes
	// created by any other flow (e.g. pool provisioning).
	HostID int64 `json:"host_id,omitempty"`
}

type VolumeReplication struct {
	BaseModel
	Name                        string                      `gorm:"column:name"`
	Description                 string                      `gorm:"column:description"`
	State                       string                      `gorm:"column:state"`
	StateDetails                string                      `gorm:"column:state_details"`
	Uri                         string                      `gorm:"column:uri"`
	RemoteUri                   string                      `gorm:"column:remote_uri"`
	ReplicationAttributes       *ReplicationDetails         `gorm:"column:replication_attributes;type:jsonb"`
	MirrorState                 *string                     `gorm:"column:mirror_state"`
	RelationshipStatus          *string                     `gorm:"column:relationship_status"`
	TotalProgress               int64                       `gorm:"column:total_progress"`
	TotalTransferBytes          int64                       `gorm:"column:total_transfer_bytes"`
	TotalTransferTimeSecs       int64                       `gorm:"column:total_transfer_time_secs"`
	LastTransferSize            int64                       `gorm:"column:last_transfer_size"`
	LastTransferError           string                      `gorm:"column:last_transfer_error"`
	LastTransferDuration        int64                       `gorm:"column:last_transfer_duration"`
	LastTransferEndTime         *time.Time                  `gorm:"column:last_transfer_end_time"`
	ProgressLastUpdated         *time.Time                  `gorm:"column:progress_last_updated"`
	LastUpdatedFromOntap        time.Time                   `gorm:"column:last_updated_from_ontap"`
	Healthy                     bool                        `gorm:"column:healthy;default:true"`
	UnhealthyReason             string                      `gorm:"column:unhealthy_reason"`
	LagTime                     int64                       `gorm:"column:lag_time"`
	AccountID                   int64                       `gorm:"column:account_id"`
	Account                     *Account                    `gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	VolumeID                    int64                       `gorm:"column:volume_id"`
	Volume                      *Volume                     `gorm:"ForeignKey:VolumeID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	ClusterPeerId               sql.NullInt64               `gorm:"column:cluster_peer_id"`
	ClusterPeer                 *ClusterPeerings            `gorm:"ForeignKey:ClusterPeerId;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	HybridReplicationAttributes *HybridReplicationAttribute `gorm:"column:hybrid_replication_attributes;type:jsonb"`
}

type ReplicationDetails struct {
	EndpointType               string `json:"endpoint_type"`
	ReplicationType            string `json:"replication_type"`
	ReplicationSchedule        string `json:"replication_schedule"`
	SourcePoolUUID             string `json:"source_pool_uuid"`
	SourceVolumeUUID           string `json:"source_volume_uuid"`
	SourceLocation             string `json:"source_location"`
	SourceHostName             string `json:"source_host_name"`
	SourceReplicationUUID      string `json:"source_replication_uuid"`
	SourceSvmName              string `json:"source_svm_name"`
	SourceVolumeName           string `json:"source_volume_name"`
	DestinationPoolUUID        string `json:"destination_pool_uuid"`
	DestinationVolumeUUID      string `json:"destination_volume_uuid"`
	DestinationLocation        string `json:"destination_location"`
	DestinationHostName        string `json:"destination_host_name"`
	DestinationReplicationUUID string `json:"destination_replication_uuid"`
	DestinationSvmName         string `json:"destination_svm_name"`
	DestinationVolumeName      string `json:"destination_volume_name"`
	ExternalUUID               string `json:"external_uuid"`
	Labels                     *JSONB `json:"labels"`
}

type HybridReplicationAttribute struct {
	SvmPeerCommand                *string                 `json:"svm_peer_command,omitempty"`
	SvmPeerExpiryTime             *time.Time              `json:"svm_peer_expiry_time,omitempty"`
	Description                   string                  `json:"description"`
	Labels                        map[string]string       `json:"labels"`
	PeerVolumeName                string                  `json:"peer_volume_name"`
	PeerSvmName                   string                  `json:"peer_svm_name"`
	ReplicationSchedule           string                  `json:"replication_schedule"`
	HybridReplicationType         *string                 `json:"hybrid_replication_type,omitempty"`
	HybridReplicationUserCommands []string                `json:"hybrid_replication_user_commands,omitempty"`
	StateDetailsCode              int32                   `json:"state_details_code"`
	Status                        HybridReplicationStatus `json:"status"`
	StatusDetails                 string                  `json:"status_details"`
}

// Scan implements the sql.Scanner interface for JSONB deserialization
func (h *HybridReplicationAttribute) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, h)
}

// Value implements the driver.Valuer interface for JSONB serialization
func (h HybridReplicationAttribute) Value() (driver.Value, error) {
	return json.Marshal(h)
}

type ClusterPeerings struct {
	BaseModel
	State                    ClusterPeeringStatus      `gorm:"column:state"`
	StateDetails             string                    `gorm:"column:state_details"`
	OnprempCluster           string                    `gorm:"column:onpremp_cluster"`
	OntapPeerUUID            string                    `gorm:"column:ontap_peer_uuid"`
	AccountID                int64                     `gorm:"column:account_id"`
	PoolID                   int64                     `gorm:"column:pool_id"`
	ClusterPeeringAttributes *ClusterPeeringAttributes `gorm:"column:cluster_peering_attributes;type:jsonb"`
}

type ClusterPeeringAttributes struct {
	PassPhrase      *string    `json:"pass_phrase,omitempty"`
	Command         *string    `json:"command,omitempty"`
	ExpiryTime      *time.Time `json:"expiry_time,omitempty"`
	ClusterLocation *string    `json:"cluster_location,omitempty"`
}

// Scan implements the sql.Scanner interface for ClusterPeeringRowAttributes
func (cpr *ClusterPeeringAttributes) Scan(value interface{}) error {
	if value == nil {
		*cpr = ClusterPeeringAttributes{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, cpr)
}

// Value implements the driver.Valuer interface for ClusterPeeringRowAttributes
func (cpr ClusterPeeringAttributes) Value() (driver.Value, error) {
	return json.Marshal(cpr)
}

func (rd *ReplicationDetails) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, rd)
}

func (rd ReplicationDetails) Value() (driver.Value, error) {
	return json.Marshal(rd)
}

func (nd *NodeDetails) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, &nd)
}

func (nd NodeDetails) Value() (driver.Value, error) {
	return json.Marshal(nd)
}

type HostGroup struct {
	BaseModel
	Name          string   `gorm:"column:name"`
	Description   string   `gorm:"column:description"`
	HostGroupType string   `gorm:"column:host_group_type"`
	OSType        string   `gorm:"column:os_type"`
	Hosts         Hosts    `gorm:"column:hosts;type:jsonb"`
	State         string   `gorm:"column:state"`
	StateDetails  string   `gorm:"column:state_details"`
	AccountID     int64    `gorm:"column:account_id"`
	Account       *Account `gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
}

type Hosts struct {
	Hosts []string `json:"hosts"`
}

func (h *Hosts) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, &h)
}

func (h Hosts) Value() (driver.Value, error) {
	return json.Marshal(h)
}

// BackupVault represents the backup vault entity with associated attributes and relationships.
type BackupVault struct {
	BaseModel
	Name                       string               `json:"name" gorm:"index"`
	Account                    *Account             `json:"-" gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:RESTRICT,OnUpdate:RESTRICT;"`
	AccountID                  int64                `gorm:"column:account_id"`
	RegionName                 string               `json:"regionName" gorm:"-"`
	BackupRegionName           *string              `json:"backupRegionName" gorm:"type:text"`
	SourceRegionName           *string              `json:"sourceRegionName" gorm:"type:text"`
	LifeCycleState             string               `json:"lifeCycleState"`
	LifeCycleStateDetails      string               `json:"lifeCycleStateDetails" gorm:"type:text"`
	BackupVaultType            string               `json:"backupVaultType" gorm:"type:varchar(255)"`
	AccountVendorID            string               `json:"accountVendorID"`
	Description                *string              `json:"description" gorm:"type:text"`
	ImmutableAttributes        *ImmutableAttributes `gorm:"column:immutable_attributes;type:jsonb"`
	CmekAttributes             *CmekAttributes      `gorm:"column:cmek_attributes;type:jsonb"`
	CrossRegionBackupVaultName *string              `json:"crossRegionBackupVaultName" gorm:"type:text"`
	ExternalUUID               *string              `json:"externalUuid" gorm:"column:external_uuid;type:text;index"`
	BucketDetails              BucketDetailsArray   `gorm:"column:bucket_details;type:jsonb"`
	ServiceType                string               `json:"serviceType" gorm:"column:service_type;type:varchar(20);default:'GCNV'"`
}

func (b *BackupVault) UpdateBucketDetails(updatedBucketDetails BucketDetailsArray) {
	if b == nil || len(updatedBucketDetails) == 0 {
		return
	}

	if b.ServiceType == ServiceTypeCrossProject {
		if updatedBucketDetails[0] != nil && updatedBucketDetails[0].BucketName != "" {
			b.BucketDetails = updatedBucketDetails
		}
		return
	}

	merged := make(map[string]*BucketDetails)
	order := make([]*BucketDetails, 0, len(b.BucketDetails)+len(updatedBucketDetails))
	for _, bucketDetail := range b.BucketDetails {
		if !bucketDetail.IsValid() {
			continue
		}
		if _, exists := merged[bucketDetail.BucketName]; exists {
			continue
		}
		merged[bucketDetail.BucketName] = bucketDetail
		order = append(order, bucketDetail)
	}

	for _, bucketDetail := range updatedBucketDetails {
		if !bucketDetail.IsValid() {
			continue
		}
		if existing, ok := merged[bucketDetail.BucketName]; ok {
			existing.SatisfiesPzi = bucketDetail.SatisfiesPzi
			existing.SatisfiesPzs = bucketDetail.SatisfiesPzs
			continue
		}
		merged[bucketDetail.BucketName] = bucketDetail
		order = append(order, bucketDetail)
	}
	b.BucketDetails = BucketDetailsArray(order)
}

type BucketDetails struct {
	BucketName          string `json:"bucket_name"`
	ServiceAccountName  string `json:"service_account_name"`
	VendorSubnetID      string `json:"vendor_subnet_id"`
	TenantProjectNumber string `json:"tenant_project_number"`
	SatisfiesPzi        bool   `json:"satisfies_pzi"`
	SatisfiesPzs        bool   `json:"satisfies_pzs"`
}

func (b *BucketDetails) IsValid() bool {
	return b != nil && b.BucketName != ""
}

type BucketDetailsArray []*BucketDetails

func (b *BucketDetailsArray) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, b)
}

func (b BucketDetailsArray) Value() (driver.Value, error) {
	return json.Marshal(b)
}

type ImmutableAttributes struct {
	BackupMinimumEnforcedRetentionDuration *int64 `json:"backupMinimumEnforcedRetentionDuration" gorm:"default:0"`
	IsDailyBackupImmutable                 bool   `json:"isDailyBackupImmutable" gorm:"default:false"`
	IsWeeklyBackupImmutable                bool   `json:"isWeeklyBackupImmutable" gorm:"default:false"`
	IsMonthlyBackupImmutable               bool   `json:"isMonthlyBackupImmutable" gorm:"default:false"`
	IsAdhocBackupImmutable                 bool   `json:"isAdhocBackupImmutable" gorm:"default:false"`
}

// Scan implements the sql.Scanner interface for ImmutableAttributes
func (immutableAttributes *ImmutableAttributes) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, immutableAttributes)
}

// Value implements the driver.Valuer interface for ImmutableAttributes
func (immutableAttributes ImmutableAttributes) Value() (driver.Value, error) {
	return json.Marshal(immutableAttributes)
}

type CmekAttributes struct {
	KmsConfigResourcePath    *string `json:"kmsConfigResourcePath"`
	EncryptionState          *string `json:"encryptionState"`
	BackupsPrimaryKeyVersion *string `json:"backupsPrimaryKeyVersion"`
}

// Scan implements the sql.Scanner interface for CmekAttributes
func (cmekAttributes *CmekAttributes) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, cmekAttributes)
}

// Value implements the driver.Valuer interface for CmekAttributes
func (cmekAttributes CmekAttributes) Value() (driver.Value, error) {
	return json.Marshal(cmekAttributes)
}

type DataProtection struct {
	ScheduledBackupEnabled *bool   `json:"scheduled_backup_enabled"`
	BackupVaultID          string  `json:"backup_vault_id"`
	BackupPolicyID         string  `json:"backup_policy_id"`
	BackupChainBytes       *int64  `json:"backup_chain_bytes"`
	KmsGrant               *string `json:"kms_grant"`
}

func (dp *DataProtection) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, dp)
}

func (dp *DataProtection) Value() (driver.Value, error) {
	return json.Marshal(dp)
}

// BackupPolicy represents the backup policy entity with associated attributes and relationships.
type BackupPolicy struct {
	BaseModel
	Name                  string   `json:"name" gorm:"index"`
	Account               *Account `json:"-" gorm:"ForeignKey:AccountID;AssociationForeignKey:ID;constraint:OnDelete:RESTRICT,OnUpdate:RESTRICT;"`
	AccountID             int64    `gorm:"column:account_id"`
	Description           *string  `json:"description" gorm:"type:text"`
	DailyBackupsToKeep    int64    `json:"dailyBackupsToKeep" gorm:"type:bigint;default:0"`
	WeeklyBackupsToKeep   int64    `json:"weeklyBackupsToKeep" gorm:"type:bigint;default:0"`
	MonthlyBackupsToKeep  int64    `json:"monthlyBackupsToKeep" gorm:"type:bigint;default:0"`
	PolicyEnabled         bool     `json:"policyEnabled" gorm:"default:0"`
	LifeCycleState        string   `json:"lifeCycleState"`
	LifeCycleStateDetails string   `json:"lifeCycleStateDetails" gorm:"type:text"`
}

type Backup struct {
	BaseModel
	ExternalUUID            string            `gorm:"column:external_uuid;type:text"`
	Name                    string            `gorm:"column:name;type:text"`
	Description             string            `gorm:"column:description;type:text"`
	State                   string            `gorm:"column:state;type:text"`
	StateDetails            string            `gorm:"column:state_details;type:text"`
	Attributes              *BackupAttributes `gorm:"column:attributes;type:jsonb"`
	Type                    string            `gorm:"column:type;type:text"`
	ScheduleTag             *string           `gorm:"column:schedule_tag;type:text"`
	VolumeUUID              string            `gorm:"column:volume_uuid;type:text"`
	SizeInBytes             int64             `gorm:"column:size_in_bytes;type:bigint"`
	BackupVaultID           int64             `gorm:"column:backup_vault_id;type:bigint"`
	BackupVault             *BackupVault      `gorm:"ForeignKey:BackupVaultID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	LatestLogicalBackupSize int64             `gorm:"column:latest_logical_backup_size;type:bigint"`
	AssetMetadata           *AssetMetadata    `gorm:"column:asset_metadata;type:jsonb"`
}

// BackupAttributes represents the structure of the JSONB data for Backup
type BackupAttributes struct {
	BackupPolicyName               string    `json:"backup_policy_name"`
	SnapshotID                     string    `json:"snapshot_id"`
	SnapshotName                   string    `json:"snapshot_name"`
	SnapshotCreationTime           string    `json:"snapshot_creation_time"`
	CompletionTime                 string    `json:"completion_time"`
	LifeCycleTrackingID            string    `json:"life_cycle_tracking_id"`
	ConstituentVolumesPerAggregate string    `json:"constituent_volumes_per_aggregate"`
	UseExistingSnapshot            bool      `json:"use_existing_snapshot"`
	NumberOfAggregates             int       `json:"number_of_aggregates"`
	OntapVolumeStyle               string    `json:"ontap_volume_style"`
	ServiceAccountName             string    `json:"service_account_name"`
	EndpointUUID                   string    `json:"endpoint_uuid"`
	BucketName                     string    `json:"bucket_name"`
	Protocols                      []string  `json:"protocols"`
	VolumeName                     string    `json:"volume_name"`
	AccountIdentifier              string    `json:"account_identifier"`
	EnforcedRetentionDuration      time.Time `json:"enforced_retention_duration"`
	DeleteInitiated                bool      `json:"delete_initiated"`
	ObjectStoreUUID                string    `json:"object_store_uuid"`
	SourceVolumeZone               string    `json:"source_volume_zone"`
	ConstituentCountOfBackup       int32     `json:"constituent_count_of_backup"`
	IsRegionalHA                   bool      `json:"is_regional_ha"`
	IsExpertModeBackup             bool      `json:"is_expert_mode_backup"`
	RestoreVolumeCount             int       `json:"restore_volume_count"`
	TotalTransferBytes             int64     `json:"total_transfer_bytes"`
	VolumeAccountName              string    `json:"volume_account_name"`
}

func (b *BackupAttributes) GetTotalTransferBytes() int64 {
	if b != nil {
		return b.TotalTransferBytes
	}
	return 0
}

func (b *BackupAttributes) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, b)
}

func (b *BackupAttributes) Value() (driver.Value, error) {
	return json.Marshal(b)
}

// SfrMetricsAggregate holds aggregated SFR metrics for a volume in given range
type SfrMetricsAggregate struct {
	TotalSize  int64
	TotalCount int64
}

// SfrMetadata represents metadata for Single File Restore operations
type SfrMetadata struct {
	ID         int64         `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	CreatedAt  time.Time     `gorm:"column:created_at" json:"created_at"`
	FilesSize  int64         `gorm:"column:files_size;type:bigint" json:"files_size"`
	FileCount  int           `gorm:"column:file_count;type:integer" json:"file_count"`
	VolumeName string        `gorm:"column:volume_name;type:text" json:"volume_name"`
	VolumeUUID string        `gorm:"column:volume_uuid;type:text;index" json:"volume_uuid"`
	BackupUUID string        `gorm:"column:backup_uuid;type:text" json:"backup_uuid"`
	AccountID  sql.NullInt64 `gorm:"column:account_id;type:bigint;index" json:"account_id"`
	JobID      sql.NullInt64 `gorm:"column:job_id;type:bigint;index" json:"job_id"`
}

type BackupMetadata struct {
	BaseModel
	VolumeUUID string `json:"volume_uuid" gorm:"type:text"`
	Labels     *JSONB `json:"labels" gorm:"type:jsonb"`
}

type AdminJobSpec struct {
	BaseModel
	JobType        string `gorm:"column:job_type;unique"`
	CronExpression string `gorm:"column:cron_expression"`
	State          string `gorm:"column:state"`
}

// AppConfig is a reusable key-value store for operational state.
type AppConfig struct {
	BaseModel
	Key   string `gorm:"column:key;uniqueIndex;not null"`
	Value string `gorm:"column:value;not null"`
}

type HarvestConfig struct {
	PORT                string
	SERVICE_CONTROL_URL string
	SERVICE_NAME        string
	POLLER_NAME         string
	DATACENTER          string
	NODE_IP             string
	AUTH_STYLE          string
	USERNAME            string
	PASSWORD            string
	PROJECT             string
	LEASE_NAME          string
	FILE_NAME           string
	AUTH_TYPE           int
	SECRET_ID           string
	SECRET_PROJECT      string
	TENANT_PROJECT      string
	DEPLOYMENT_NAME     string
	POOL_NAME           string
	IS_REGIONAL_HA      bool
}

// NodeNodeGroupMap represents the mapping between a node and a node group
// TableName: node_nodegroup_map
type NodeNodeGroupMap struct {
	BaseModel
	NodeID        int64              `gorm:"not null;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;foreignKey:NodeID;references:ID"`
	NodeGroupID   int64              `gorm:"not null;index;column:node_group_id;type:bigint"`
	NodeGroup     *NodeGroup         `gorm:"ForeignKey:NodeGroupID;AssociationForeignKey:ID;constraint:OnDelete:CASCADE,OnUpdate:RESTRICT;"`
	HarvestConfig *HarvestConfig     `gorm:"column:harvest_config;type:jsonb"`
	RetryState    *HarvestRetryState `gorm:"column:harvest_retry_state;type:jsonb"`
}

// HarvestRetryState stores per-flow retry metadata for a node-node-group map.
type HarvestRetryState struct {
	Create *HarvestFlowRetryState `json:"create,omitempty"`
	Delete *HarvestFlowRetryState `json:"delete,omitempty"`
}

// HarvestFlowRetryState stores last-attempt retry metadata for one reconcile flow (create or delete).
type HarvestFlowRetryState struct {
	Retries       int       `json:"retries"`
	LastRunMsg    string    `json:"last_run_msg"`
	LastAttemptAt time.Time `json:"last_attempt_at"`
	ShouldRetry   bool      `json:"should_retry"`
}

// Scan implements the Scanner interface for HarvestConfig
func (hc *HarvestConfig) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, hc)
}

// Value implements the Valuer interface for HarvestConfig
func (hc HarvestConfig) Value() (driver.Value, error) {
	return json.Marshal(hc)
}

// Scan implements the Scanner interface for HarvestRetryState.
func (hrs *HarvestRetryState) Scan(value interface{}) error {
	if value == nil {
		*hrs = HarvestRetryState{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, hrs)
}

// Value implements the Valuer interface for HarvestRetryState.
func (hrs HarvestRetryState) Value() (driver.Value, error) {
	return json.Marshal(hrs)
}

type NodeGroup struct {
	BaseModel
	Name      string `gorm:"column:name;not null;unique"`
	LeaseName string `gorm:"column:lease_name"`
}

// NodeGroupPollerCount is one row from aggregate poller counts per node group (rebalance planning).
type NodeGroupPollerCount struct {
	NodeGroupID int64  `gorm:"column:node_group_id"`
	LeaseName   string `gorm:"column:lease_name"`
	Count       int64  `gorm:"column:cnt"`
}

// NodeGroupAssignmentParams holds parameters for node group assignment
type NodeGroupAssignmentParams struct {
	Node1            *Node
	Node2            *Node
	MaxNodesPerGroup int
	CustomerProject  string
	TenantProject    string // Adding this for future extensibility
	DeploymentName   string
	PoolName         string
	IsRegionalHA     bool
}

// TieringStatus represents the state of tiering for an auto-tiering config
type TieringStatus string

const (
	TieringStatusPaused           TieringStatus = "PAUSED"
	TieringStatusResumed          TieringStatus = "RESUMED"
	TieringStatusPartiallyPaused  TieringStatus = "PARTIALLY_PAUSED"
	TieringStatusPartiallyResumed TieringStatus = "PARTIALLY_RESUMED"
)

type AutoTieringConfig struct {
	HotTierSizeInBytes       int64         `json:"hot_tier_size_in_bytes"`
	EnableHotTierAutoResize  bool          `json:"enable_hot_tier_auto_resize"`
	BucketName               string        `json:"bucket_name"`
	TieringStatus            TieringStatus `json:"tiering_status"`
	HotTierConsumption       int64         `json:"hot_tier_consumption"`
	ColdTierConsumption      int64         `json:"cold_tier_consumption"`
	TieringFullnessThreshold int64         `json:"tiering_fullness_threshold"`
}

// Scan implements the sql.Scanner interface for AutoTieringConfig
func (atc *AutoTieringConfig) Scan(value interface{}) error {
	if value == nil {
		*atc = AutoTieringConfig{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, atc)
}

// Value implements the driver.Valuer interface for AutoTieringConfig
func (atc AutoTieringConfig) Value() (driver.Value, error) {
	return json.Marshal(atc)
}

type AutoTieringPolicy struct {
	TieringPolicy            string `json:"tiering_policy"`
	CoolingThresholdDays     int32  `json:"cooling_threshold_days"`
	RetrievalPolicy          string `json:"retrieval_policy"`
	HotTierBypassModeEnabled bool   `json:"hot_tier_bypass_mode_enabled"`
	CloudWriteModeEnabled    *bool  `json:"cloud_write_mode_enabled,omitempty"`
}

// Scan implements the sql.Scanner interface for AutoTieringPolicy
func (atp *AutoTieringPolicy) Scan(value interface{}) error {
	if value == nil {
		*atp = AutoTieringPolicy{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, atp)
}

// Value implements the driver.Valuer interface for AutoTieringPolicy
func (atp AutoTieringPolicy) Value() (driver.Value, error) {
	return json.Marshal(atp)
}

type CachePrePopulate struct {
	ExcludePathList []string `json:"exclude_path_list"`
	PathList        []string `json:"path_list"`
	Recursion       *bool    `json:"recursion"`
}
type CacheConfig struct {
	AtimeScrubEnabled       *bool  `json:"atime_scrub_enabled"`
	AtimeScrubDays          *int16 `json:"atime_scrub_days"`
	CifsChangeNotifyEnabled *bool  `json:"cifs_change_notify_enabled"`
	WritebackEnabled        *bool  `json:"writeback_enabled"`

	CachePrePopulate      *CachePrePopulate `json:"cache_pre_populate,omitempty"`
	CachePrePopulateState string            `json:"cache_pre_populate_state,omitempty"`
}

type CacheParameters struct {
	PeerClusterName      string   `json:"peer_cluster_name"`
	PeerSvmName          string   `json:"peer_svm_name"`
	PeerVolumeName       string   `json:"peer_volume_name"`
	PeerIpAddresses      []string `json:"peer_ip_addresses"`
	EnableGlobalFileLock *bool    `json:"enable_global_file_lock,omitempty"`

	CacheConfig *CacheConfig `json:"cache_config,omitempty"`

	CacheState            string `json:"cache_state"`
	PreviousCacheState    string `json:"previous_cache_state"`
	CacheStateDetails     string `json:"cache_state_details"`
	CacheStateDetailsCode int    `json:"cache_state_details_code"`

	Passphrase        *string    `json:"passphrase"`
	Command           *string    `json:"command"`
	CommandExpiryTime *time.Time `json:"peer_command_expiry"`
}

func (cp *CacheParameters) Scan(value interface{}) error {
	if value == nil {
		*cp = CacheParameters{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, cp)
}

func (cp CacheParameters) Value() (driver.Value, error) {
	return json.Marshal(cp)
}

// ClusterUpgradeJob represents a VSA cluster upgrade job
type ClusterUpgradeJob struct {
	BaseModel
	ClusterID          string               `gorm:"column:cluster_id;index" json:"clusterId"`
	PoolID             string               `gorm:"column:pool_id;index" json:"poolId"`
	TargetVersion      string               `gorm:"column:target_version" json:"targetVersion"`
	CurrentVersion     string               `gorm:"column:current_version" json:"currentVersion"`
	VSABuildImage      string               `gorm:"column:vsa_build_image" json:"vsaBuildImage"`
	MediatorBuildImage string               `gorm:"column:mediator_build_image" json:"mediatorBuildImage"`
	Status             string               `gorm:"column:status" json:"status"`
	ErrorDetails       *UpgradeErrorDetails `gorm:"column:error_details;type:jsonb" json:"errorDetails,omitempty"`
	StartedAt          *time.Time           `gorm:"column:started_at" json:"startedAt,omitempty"`
	CompletedAt        *time.Time           `gorm:"column:completed_at" json:"completedAt,omitempty"`
	Metadata           *JSONB               `gorm:"column:metadata;type:jsonb" json:"metadata,omitempty"`
	BatchUpgradeID     *string              `gorm:"column:batch_upgrade_id;index" json:"batchUpgradeId,omitempty"`
	ForceUpgrade       bool                 `gorm:"column:force_upgrade;default:false" json:"forceUpgrade"`
}

// UpgradeErrorDetails represents error details for upgrade jobs
type UpgradeErrorDetails struct {
	ErrorCode    string            `json:"errorCode"`
	ErrorMessage string            `json:"errorMessage"`
	ErrorType    string            `json:"errorType"`
	Retryable    bool              `json:"retryable"`
	Details      map[string]string `json:"details,omitempty"`
	StackTrace   string            `json:"stackTrace,omitempty"`
}

// Scan implements the Scanner interface for UpgradeErrorDetails
func (ued *UpgradeErrorDetails) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("type assertion to []byte failed")
	}
	return json.Unmarshal(bytes, ued)
}

// Value implements the driver Valuer interface for UpgradeErrorDetails
func (ued UpgradeErrorDetails) Value() (driver.Value, error) {
	return json.Marshal(ued)
}

// ImageVersion represents supported ONTAP versions and their corresponding images
type ImageVersion struct {
	BaseModel
	OntapVersion string `gorm:"column:ontap_version;uniqueIndex;not null" json:"ontapVersion"`
	VSAImagePath string `gorm:"column:vsa_image_path;not null" json:"vsaImagePath"`
	VSAName      string `gorm:"column:vsa_name;not null" json:"vsaName"`
	MediatorName string `gorm:"column:mediator_name;not null" json:"mediatorName"`
	IsActive     bool   `gorm:"column:is_active;default:true" json:"isActive"`
}

// VolumeLatestBackup represents a volume with its latest backup.
// For regular backups Volume is set and ExpertModeVolume is nil.
// For expert mode backups ExpertModeVolume is set and Volume is nil.
type VolumeLatestBackup struct {
	Volume           *Volume
	ExpertModeVolume *ExpertModeVolumes
	LatestBackup     *Backup
}

// VolumeFieldUpdate represents a targeted update for specific volume fields
type VolumeFieldUpdate struct {
	UUID   string                 `json:"uuid"`
	Fields map[string]interface{} `json:"fields"`
}

// VolumeTieringUpdate represents tiering field updates for a volume
type VolumeTieringUpdate struct {
	HotTierSizeGib  uint64 `json:"hot_tier_size_gib"`
	ColdTierSizeGib uint64 `json:"cold_tier_size_gib"`
}

type BackupChainHistory struct {
	BaseModel
	ResourceName       string  `gorm:"column:resource_name;type:text" json:"resource_name"`
	Size               int64   `gorm:"column:size;not null;default:0" json:"size"`
	ResourceUUID       string  `gorm:"column:resource_uuid;size:255" json:"resource_uuid"`
	ConsumerID         string  `gorm:"column:consumer_id;type:text" json:"consumer_id"`
	DeploymentName     string  `gorm:"column:deployment_name;type:text" json:"deployment_name"`
	IsExpertModeBackup bool    `gorm:"column:is_expert_mode_backup;default:false" json:"is_expert_mode_backup"`
	EndpointUUID       *string `gorm:"column:endpoint_uuid;type:text" json:"endpoint_uuid,omitempty"`
}

type AddressRange struct {
	BaseModel
	Name                      string     `gorm:"column:name"`
	AddressRangeCidr          string     `gorm:"column:address_range_cidr"`
	Network                   string     `gorm:"column:network"`
	VpcName                   string     `gorm:"column:vpc_name;index"`
	HostProjectNumber         string     `gorm:"column:host_project_number;index"`
	LifType                   string     `gorm:"column:lif_type;default:'dataLIF'"`
	AddressRangeState         string     `gorm:"column:address_range_state;default:'CREATED'"`
	AddressRangeStateDetails  string     `gorm:"column:address_range_state_details;type:text"`
	ApplyRouteAggregation     bool       `gorm:"column:apply_route_aggregation;default:false"`
	RouteAggregationApplied   bool       `gorm:"column:route_aggregation_applied;default:false"`
	RouteAggregationAppliedAt *time.Time `gorm:"column:route_aggregation_applied_at"`
}

type IPRange struct {
	BaseModel
	Network        *string       `json:"network" gorm:"size:2048"`
	StartIP        string        `json:"startIp"`
	EndIP          string        `json:"endIp"`
	Gateway        string        `json:"gateway"`
	Netmask        string        `json:"netmask"`
	RegionID       sql.NullInt64 `json:"-" gorm:"index"`
	RegionUUID     string        `json:"regionUUID" gorm:"-"`
	RegionName     string        `json:"regionName" gorm:"-"`
	HostID         sql.NullInt64 `json:"-" gorm:"index"`
	HostUUID       string        `json:"hostUUID" gorm:"-"`
	HostName       string        `json:"hostName" gorm:"-"`
	AccountID      sql.NullInt64 `json:"-" gorm:"index"`
	AccountUUID    string        `json:"accountUUID" gorm:"-"`
	AccountName    string        `json:"accountName" gorm:"-"`
	LifType        string        `json:"lifType" gorm:"default:'dataLIF'"`
	AddressRangeID sql.NullInt64 `json:"-" gorm:"index"`
}
