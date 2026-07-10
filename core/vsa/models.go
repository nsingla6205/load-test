package vsa

import (
	"fmt"
	"time"

	"github.com/go-openapi/strfmt"
	ontaprestmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

type ProviderDetails struct {
	IPAddress          string            `json:"ipAddress"`
	Hosts              map[string]string `json:"host"`
	Username           string            `json:"username"`
	Password           string            `json:"password"`
	Port               *int              `json:"port"`
	UseHTTPS           bool              `json:"useHTTPS"`
	Protocol           string            `json:"protocol"`
	InsecureSkipVerify bool              `json:"insecureSkipVerify"`
	Certificate        *Certificate      `json:"certificate"`
	AuthType           int               `json:"authType"`       // 0=password, 2=certificate
	FastConnection     bool              `json:"fastConnection"` // When true, bypasses retries and uses shorter timeout for test connections
	// ServerName, when non-empty, is used as the TLS SNI value AND for
	// hostname verification of the server certificate. The REST client
	// dials Hosts by IP (keys) instead of by FQDN (values) when this is
	// set. Used by the OCI SNI-override path so we can keep full mTLS
	// without registering Cloud DNS records per node.
	ServerName string `json:"serverName,omitempty"`
}

type Certificate struct {
	SignedCertificate        string   `json:"signed_certificate"`
	PrivateKey               string   `json:"private_key"`
	InterMediateCertificates []string `json:"intermediate_certificate"`
	CommonName               string   `json:"common_name"`
	RootCaCertificate        string   `json:"root_ca_certificate"`
}

type CreateSvmParams struct {
	Name      string
	Protocols Protocols
	// IPSpace is the ONTAP ipspace for the SVM. Empty defaults to "Default".
	IPSpace string
}

type GetSvmParams struct {
	Name string
}

// ListSvmsParams filters SVM collection GET (/api/svm/svms).
type ListSvmsParams struct {
	NamePattern *string
	Fields      []string
}

type Protocols struct {
	EnableIscsi bool
	EnableNfs   bool
}

type VolumeInlineEncryptionStatus struct {
	Code    *string
	Message *string
}

type Encryption struct {
	Action              *string
	Enabled             *bool
	KeyCreateTime       *strfmt.DateTime
	KeyID               *string
	KeyManagerAttribute *string
	Rekey               *bool
	State               *string
	Status              *VolumeInlineEncryptionStatus
	Type                *string
}

type ProviderResponse struct {
	Name         string
	ExternalUUID string
}

// VolumeResponseClone mirrors ONTAP volume.clone (expert-mode GET with clone.*).
type VolumeResponseClone struct {
	ParentVolumeName     string
	ParentVolumeUUID     string
	ParentSnapshotName   string
	ParentSnapshotUUID   string
	IsFlexclone          *bool
	SplitInitiated       *bool
	SplitCompletePercent *int64
}

type VolumeResponse struct {
	ProviderResponse
	AvailableSpace                 int64
	Size                           int64
	State                          string
	SnapshotPolicyName             string
	SnapReserve                    int64
	UsedBytes                      int64
	Type                           string
	AFSSize                        int64
	MetadataSize                   int64
	SnapshotDirectoryAccessEnabled bool
	ConstituentCount               *int32
	Style                          string
	Encryption
	// JunctionPath is the fully-qualified path in the SVM namespace at which the volume is mounted (from volume.nas.path). Empty for unmounted or block volumes.
	JunctionPath string
	// Clone is set when ONTAP returns volume.clone (e.g. GetCloneVolumeForExpertMode).
	Clone *VolumeResponseClone
}

type CreateLifParams struct {
	Name      string
	SvmName   string
	IpAddress string
	NodeName  string
	HomePort  string
	// Netmask is the dotted-decimal mask for the LIF address. When empty it
	// defaults to DefaultNetmask (a /32 host route) for backward compatibility.
	Netmask string
	// ServicePolicy selects the LIF's data service policy (e.g. default-data-iscsi
	// for block, default-data-files for NFS). When empty it defaults to the iSCSI
	// data policy for backward compatibility.
	ServicePolicy string
}

type Lif struct {
	Name         string
	ExternalUUID string
	IPAddress    string
	SubnetMask   string
}

type CreateSVMPeerParams struct {
	LocalSVMName    string
	PeerSVMName     string
	PeerClusterName string
	Applications    []ontaprestmodel.SvmPeerApplications
}

var (
	SvmPeerStatePeered       = "peered"
	SvmPeerStateRejected     = "rejected"
	SvmPeerStateSuspended    = "suspended"
	SvmPeerStateInitiated    = "initiated"
	SvmPeerStatePending      = "pending"
	SvmPeerStateInitializing = "initializing"
)

// SvmPeer describes SvmPeer information retrieved from ONTAP
type SvmPeer struct {
	UUID            string
	Applications    []string
	State           string
	LocalSvmName    string
	LocalSvmUUID    string
	PeerSvmName     string
	PeerSvmUUID     string
	PeerClusterName string
}

type CreateNetworkIPRouteParams struct {
	SvmName string
	Gateway string
	// IPSpace is the ONTAP ipspace for the route. Empty defaults to "Default".
	IPSpace string
}

type Node struct {
	Name         string
	State        string
	ExternalUUID string
}

// Takeover state constants
const (
	// TakeoverStateNotAttempted indicates the takeover operation hasn't started yet, and it's possible to initiate a takeover
	TakeoverStateNotAttempted = "not_attempted"

	// TakeoverStateNotPossible indicates the takeover operation can't be initiated. Check the failure message for more details
	TakeoverStateNotPossible = "not_possible"

	// TakeoverStateInProgress indicates the takeover operation is currently happening; the node is taking over its partner
	TakeoverStateInProgress = "in_progress"

	// TakeoverStateInTakeover indicates the takeover operation is complete
	TakeoverStateInTakeover = "in_takeover"

	// TakeoverStateFailed indicates the takeover operation failed. Check the failure message for more details
	TakeoverStateFailed = "failed"
)

type TakeoverFailure struct {
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

type TakeoverState struct {
	State   string           `json:"state,omitempty"`
	Failure *TakeoverFailure `json:"failure,omitempty"`
}

// Helper methods for TakeoverState
func (ts *TakeoverState) IsNotAttempted() bool {
	return ts != nil && ts.State == TakeoverStateNotAttempted
}

func (ts *TakeoverState) IsNotPossible() bool {
	return ts != nil && ts.State == TakeoverStateNotPossible
}

func (ts *TakeoverState) IsInProgress() bool {
	return ts != nil && ts.State == TakeoverStateInProgress
}

func (ts *TakeoverState) IsInTakeover() bool {
	return ts != nil && ts.State == TakeoverStateInTakeover
}

func (ts *TakeoverState) IsFailed() bool {
	return ts != nil && ts.State == TakeoverStateFailed
}

func (ts *TakeoverState) IsHealthy() bool {
	return ts.IsNotAttempted() || ts.IsInTakeover()
}

func (ts *TakeoverState) RequiresAttention() bool {
	return ts.IsNotPossible() || ts.IsFailed()
}

type HAInfo struct {
	Takeover *TakeoverState `json:"takeover,omitempty"`
}

type NodeWithHA struct {
	UUID string  `json:"uuid"`
	Name string  `json:"name"`
	Ha   *HAInfo `json:"ha,omitempty"`
}

type TakeoverStateResponse struct {
	Records []NodeWithHA `json:"records"`
}

// Takeover check structures for detailed takeover reasons
type TakeoverCheck struct {
	TakeoverPossible bool     `json:"takeover_possible"`
	Reasons          []string `json:"reasons,omitempty"`
}

type HAInfoWithReasons struct {
	TakeoverCheck *TakeoverCheck `json:"takeover_check,omitempty"`
}

type NodeWithTakeoverReasons struct {
	UUID string             `json:"uuid"`
	Name string             `json:"name"`
	Ha   *HAInfoWithReasons `json:"ha,omitempty"`
}

type TakeoverReasonResponse struct {
	Records []NodeWithTakeoverReasons `json:"records"`
}

// JSWAPBackingType represents the type of backing storage for JSWAP
type JSWAPBackingType string

// JSWAP backing type constants
const (
	// JSWAPBackingTypeEphemeralMemory indicates JSWAP is using ephemeral memory
	JSWAPBackingTypeEphemeralMemory JSWAPBackingType = "ephemeral_memory"

	// JSWAPBackingTypeEphemeralDisk indicates JSWAP is using ephemeral disk
	JSWAPBackingTypeEphemeralDisk JSWAPBackingType = "ephemeral_disk"
)

// JSWAP swap mode constants
const (
	// JSWAPSwapModeDynamic indicates dynamic swap mode
	JSWAPSwapModeDynamic = "dynamic"

	// JSWAPSwapModeStatic indicates static swap mode
	JSWAPSwapModeStatic = "static"
)

// Node action constants
const (
	// NodeActionTakeoverCheck indicates triggering a takeover check operation
	NodeActionTakeoverCheck = "takeover_check"
)

type NVLog struct {
	SwapMode    string `json:"swap_mode"`
	BackingType string `json:"backing_type"`
}

type NodeWithJSWAP struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	NVLog *NVLog `json:"nvlog,omitempty"`
}

type JSWAPStatusResponse struct {
	Records    []NodeWithJSWAP `json:"records"`
	NumRecords int             `json:"num_records"`
}

// Consolidated cluster health structures
type HAHealthInfo struct {
	Takeover      *TakeoverState `json:"takeover,omitempty"`
	TakeoverCheck *TakeoverCheck `json:"takeover_check,omitempty"`
}

type NodeHealthStatus struct {
	UUID  string        `json:"uuid"`
	Name  string        `json:"name"`
	Ha    *HAHealthInfo `json:"ha,omitempty"`
	NVLog *NVLog        `json:"nvlog,omitempty"`
}

type ClusterHealthStatusResponse struct {
	Records    []NodeHealthStatus `json:"records"`
	NumRecords int                `json:"num_records"`
}

func (nvlog *NVLog) IsDynamicMode() bool {
	return nvlog != nil && nvlog.SwapMode == JSWAPSwapModeDynamic
}

func (nvlog *NVLog) IsStaticMode() bool {
	return nvlog != nil && nvlog.SwapMode == JSWAPSwapModeStatic
}

type Aggregate struct {
	Name                 string
	State                string
	VolumeCount          int64
	Size                 int64
	AvailableSize        int64
	UsedSize             int64
	UUID                 string
	TotalProvisionedSize int64
}

type UpdateAggregateParams struct {
	UUID                     string
	TieringFullnessThreshold int64
}

type RestoreFromSnapshotParams struct {
	ParentVolumeExternalUUID string // External UUID of the source/parent volume
	ParentVolumeName         string // Name of the Volume
	SnapshotUUID             string // UUID of the snapshot to restore from
	SnapshotName             string // Name of the snapshot to restore from
	ParentVolumeSvmName      string // Name of the SVM where the parent volume resides
	// Add more fields as needed
}

type CreateVolumeParams struct {
	VolumeName               string
	SvmName                  string
	Aggregates               []string
	ConstituentsPerAggregate *int64
	Size                     int64
	VolumeType               string
	SnapshotPolicyName       string
	// Reference to a snapshot for restore/clone
	RestoreFromSnapshot *RestoreFromSnapshotParams // Optional: parameters for restoring from a snapshot
	SnapReserve         int64
	SnapshotDirectory   bool
	TieringPolicy       *TieringPolicy
	ExportPolicy        *string
	Protocol            string
	JunctionPath        *string
	Style               *string // Volume style, e.g., "flexvol", "flexgroup"
	TieringSupported    *bool
	SecurityStyle       *string
	QosPolicy           *string // Optional: QoS policy name to assign to the volume
	UnixPermissions     *string
	// AllSquashUid is the UNIX UID to assign to the volume NAS owner when AllSquash is enabled.
	// ONTAP does not accept nas.uid during volume create, so CreateVolume issues a post-create
	// VolumeModify to set it. Only populated when AllSquash is active and AnonUid is explicitly set.
	AllSquashUid *int64
}

type CreateFlexCacheVolumeParams struct {
	Name                     string
	Size                     int64
	OriginSVMName            string
	OriginVolumeName         string
	AggregateName            string
	SvmName                  string
	JunctionPath             *string
	ExportPolicy             *string
	WritebackEnabled         *bool
	AtimeScrubEnabled        *bool
	AtimeScrubDays           *int16
	CifsChangeNotifyEnabled  *bool
	GlobalFileLockingEnabled *bool
}

type ExportPolicy struct {
	ExportPolicyName string
	SvmName          string
	ExportRules      []*ExportRule `json:"export_rules"`
}

type ConfigActiveDirectoryParams struct {
	ActiveDirectory *ActiveDirectory
	ExternalSVMUUID string
	SVMName         string
	JunctionPath    string
}

type ActiveDirectory struct {
	UUID                          string
	PrimaryAD                     *bool
	ManagedAD                     *bool
	Label                         string
	Username                      string
	Password                      log.Secret
	Domain                        string
	DNS                           string
	NetBIOS                       string
	Region                        string
	OrganizationalUnit            string
	Site                          *string
	Status                        string
	CIFSServers                   []*CIFSServer
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
	DeletedAt                     *time.Time
	Users                         map[string][]string
	AdName                        string
	KdcIP                         string
	UserDN                        *string
	GroupDN                       *string
	GroupMembershipFilter         *string
	AesEncryption                 *bool
	EncryptDCConnections          *bool
	ServerRootCaCertificate       *string
	LdapSigning                   *bool
	AllowLocalNFSUsersWithLdap    *bool
	LdapOverTLS                   *bool
	PreferredServersForLdapClient *string
	Description                   *string
	Name                          *string
}

type CIFSServer struct {
	SVMUUID           string
	SVMName           string
	ServerNamePostfix string
	HasLdapConfig     bool
}

type UpdateActiveDirectoryCredentialsParams struct {
	NewCredentials *ActiveDirectory
	OldCredentials *ActiveDirectory
}

type ExportRule struct {
	AllowedClients      string
	AnonymousUser       string
	Index               int
	ChownMode           string
	AccessType          string
	CIFS                bool
	NFSv3               bool
	NFSv4               bool
	S3                  bool
	UnixReadOnly        bool
	UnixReadWrite       bool
	Kerberos5ReadOnly   bool
	Kerberos5ReadWrite  bool
	Kerberos5iReadOnly  bool
	Kerberos5iReadWrite bool
	Kerberos5pReadOnly  bool
	Kerberos5pReadWrite bool
	Superuser           bool
	AllSquash           *bool
	AnonUid             *int64
}

// TieringPolicy describes the auto tiering policy for a volume
type TieringPolicy struct {
	CoolnessPeriod            int64
	CoolAccessRetrievalPolicy string
	CoolAccessTieringPolicy   string
	CloudWriteModeEnabled     *bool // Only supported for file volumes
}

type UpdateVolumeParams struct {
	UUID                    string
	VolumeName              string
	SvmName                 string
	AggregateName           string
	Size                    int64
	SnapshotPolicyName      string
	InitiateSplit           bool // Indicates whether to initiate a split for volume restore or clone
	TieringPolicy           *TieringPolicy
	SnapReserve             *int64
	EncryptionEnable        bool
	JunctionPath            *string
	ExportPolicy            *string
	SnapshotDirectoryAccess *bool
	QosPolicyName           *string // QoS policy group name to assign to volume. Use "none" to unassign (no policy). If nil, policy is not changed.
	UnixPermissions         *string
	// NasUid sets the volume NAS owner UID via nas.uid in ONTAP. Used when AllSquash is enabled
	// to align the volume root directory inode UID with the squash target.
	NasUid *int64
}

type UpdateFlexCacheVolumeParams struct {
	UUID                       string
	PrepopulateDirPaths        []*string
	PrepopulateExcludeDirPaths []*string
	IsRecursionEnabled         *bool
	WritebackEnabled           *bool
	RelativeSizeEnabled        *bool
	RelativeSizePercentage     *int16
	AtimeScrubEnabled          *bool
	AtimeScrubPeriod           *int16
	CifsChangeNotifyEnabled    *bool
}

// RevertVolumeParams describes parameters supplied to Provider.RevertVolume
type RevertVolumeParams struct {
	VolumeID        string
	SnapshotID      string
	SnapshotName    string
	SvmName         string
	PreRevertVolume *datamodel.Volume
}

type GetVolumeParams struct {
	UUID              string
	VolumeName        string
	SvmName           string
	IsRestore         bool
	SnapshotDirectory bool
}

type VolumeNASDetails struct {
	NASPath          string
	ExportPolicyName string
	SecurityStyle    string
}

type VolumeSANDetails struct {
	HasLUNs       bool
	HasNamespaces bool
}

type IgroupCreateParams struct {
	IgroupName string
	SvmName    string
	OsType     string
	Initiator  []string
}

type IgroupDeleteParams struct {
	UUID string
}

type IgroupModifyParams struct {
	IgroupName string
	SvmName    string
	Initiator  []string
}

type IgroupAddInitiator struct {
	Initiator  []string
	IgroupUUID string
}

type IgroupDeleteInitiator struct {
	InitiatorName string
	IgroupUUID    string
}

type LunCreateParams struct {
	LunName    string
	SvmName    string
	OsType     string
	VolumeName string
	Size       int64
}

type LunGetParams struct {
	SvmName    string
	VolumeName string
	LunName    string
}

type NvmeNamespaceGetParams struct {
	SvmName       string
	VolumeName    string
	NamespaceName string
}

type NvmeNamespaceResponse struct {
	ProviderResponse
}

type LunUpdateParams struct {
	UUID       string
	LunName    string
	VolumeName string
	SvmName    string
	Size       int64
}

type LunMapCreateParams struct {
	LunName    string
	SvmName    string
	IGroupName []string
}

type LunMapDeleteParams struct {
	LunUUID    string
	IGroupUUID string
}

// CreateVolumeReplicationParams describes parameters supplied to Provider.CreateVolumeReplication
type CreateVolumeReplicationParams struct {
	VolumeReplication *VolumeReplication
	ReverseResync     bool
}

// ReleaseVolumeReplicationParams describes parameters supplied to Provider.ReleaseVolumeReplication
type ReleaseVolumeReplicationParams struct {
	VolumeReplication *VolumeReplication
	ReverseResync     bool
}

// DeleteVolumeReplicationParams describes parameters supplied to Provider.DeleteVolumeReplication
type DeleteVolumeReplicationParams struct {
	VolumeReplication *VolumeReplication
	DestinationOnly   *bool
	SourceOnly        *bool
}

// VolumeReplication describes a Volume Replication relationship object in the cloud volumes model
type VolumeReplication struct {
	UUID                          string
	AccountUUID                   string
	AccountName                   string
	ClusterPeerID                 *uint64
	Name                          *string
	EndpointType                  string
	RemoteRegion                  string
	RemoteResourceID              string
	SourceHostName                string
	SourceSVMName                 string
	SourceVolumeName              string
	DestinationHostName           string
	DestinationSVMName            string
	DestinationVolumeName         string
	DestinationVolumeUUID         string
	DestinationVolumeExternalUUID string
	ReplicationPolicy             string
	ReplicationSchedule           string
	LifeCycleState                string
	LifeCycleStateDetails         string
	MirrorState                   string
	RelationshipStatus            string
	Healthy                       bool
	UnhealthyReason               string
	Volume                        *Volume
	Jobs                          []*ontaprestmodel.Job
	TotalTransferBytes            int64
	TotalTransferTimeSecs         int64
	LastTransferSize              int64
	LastTransferError             string
	LastTransferDuration          int64
	LastTransferEndTime           *time.Time
	LagTime                       int64
	Mounted                       bool
	MaxTransferRate               int64
	RelationshipID                string
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
	DeletedAt                     *time.Time
	Description                   *string
	DestinationOnly               *bool
	SourceOnly                    *bool
	Force                         *bool
	Tags                          *string
	ExternalUUID                  string
	DestinationVolumeStorageClass string
	SkipPeeringCleanup            *bool
	ReplicationType               string
	TotalProgress                 int64
	CurrentTransferType           string
	CurrentTransferError          string
	ProgressLastUpdated           *time.Time
	TransferUUID                  string
	IsCleanup                     bool
}

// SnapmirrorDestination describes SnapmirrorDestination information retrieved from ONTAP
type SnapmirrorDestination struct {
	DestinationPath    string
	DestinationSVMName string
	SourcePath         string
	SourceSVMName      string
	RelationshipUUID   string
}

type Volume struct {
	ontaprestmodel.Volume
	ExternalUUID      string
	IsOnPremMigration bool
	ProtocolTypes     []string
	JunctionPath      string
}

// SourcePath returns the source path of an ONTAP snapmirror relationship in a <svm_name>:<volume_name> format
func (v *VolumeReplication) SourcePath() string {
	return fmt.Sprintf("%s:%s", v.SourceSVMName, v.SourceVolumeName)
}

// DestinationPath returns the destination path of an ONTAP snapmirror relationship in a <svm_name>:<volume_name> format
func (v *VolumeReplication) DestinationPath() string {
	return fmt.Sprintf("%s:%s", v.DestinationSVMName, v.DestinationVolumeName)
}

type CreateClusterPeerParams struct {
	PeerAddresses       []string
	PeerName            string
	IPSpace             string
	VolumeUUID          string
	AccountUUID         string
	InterclusterLifList []string
	ExpiryTime          *strfmt.DateTime
	GeneratePassphrase  bool
	Passphrase          *string
	LocalRole           *string
}

const (
	ClusterPeerAuthenticationStateOK      = "ok"
	ClusterPeerAuthenticationStateAbsent  = "absent"
	ClusterPeerAuthenticationStatePending = "pending"
	ClusterPeerAuthenticationStateProblem = "problem"

	ClusterPeerAvailabilityStateAvailable    = "available"
	ClusterPeerAvailabilityStatePartial      = "partial"
	ClusterPeerAvailabilityStateUnavailable  = "unavailable"
	ClusterPeerAvailabilityStatePending      = "pending"
	ClusterPeerAvailabilityStateUnidentified = "unidentified"
)

type ClusterPeer struct {
	UUID                string
	PeerAddresses       []string
	PeerClusterName     string
	Availability        string
	AuthenticationState string
	Passphrase          *log.Secret
	IPSpace             string
	ExternalUUID        string
	HostUUID            string
	AccountUUID         string
	AccountName         string
	ExpiryTime          *strfmt.DateTime
	LocalRole           *string
}

// InterclusterLif describes the storage model for intercluster LIFs
type InterclusterLif struct {
	Name     string
	Address  ontaprestmodel.IPAddress
	NetMask  string
	IPSpace  string
	NodeUUID string
	UUID     string
}

type Snapshot struct {
	ontaprestmodel.Snapshot
	ExternalUUID           string
	ExternalVersionUUID    string
	ExternalVolumeUUID     string
	SizeInBytes            int64
	LogicalSizeUsedInBytes int64
	CreationTime           *strfmt.DateTime
	Type                   string
}

type CreateSnapshotParams struct {
	VolumeUUID string
	Name       string
	Comment    string
}

type CreateQuotaRuleParams struct {
	VolumeUUID     string
	SVMName        string
	QuotaTarget    string
	QuotaType      string
	DiskLimitInKib int64
	RQuota         bool
}

type UpdateQuotaRuleParams struct {
	ExternalQuotaRuleUUID string
	DiskLimitInKibs       int64
}

type SnapshotProviderResponse struct {
	ProviderResponse
	SizeInBytes        int64
	LogicalSizeInBytes int64
}

// QuotaRuleProviderResponse contains the response from ONTAP quota rule operations.
// It includes the operation state and any error messages.
//
// The State field should use the JobRespSuccess or JobRespFailure constants defined
// in quota_rule.go for consistency across the codebase.
//
// Example usage:
//
//	response, err := provider.CreateQuotaRule(params)
//	if err != nil {
//	    return err
//	}
//	if response.IsFailure() {
//	    return fmt.Errorf("quota rule creation failed: %s", response.Message)
//	}
type QuotaRuleProviderResponse struct {
	ProviderResponse
	State   string // Operation state: "success" or "failure" (use JobRespSuccess/JobRespFailure constants)
	Message string // Error message if State is "failure", empty on success
}

// IsSuccess returns true if the quota rule operation completed successfully.
// This is the preferred way to check for success instead of comparing State directly.
func (r *QuotaRuleProviderResponse) IsSuccess() bool {
	return r.State == JobRespSuccess
}

// IsFailure returns true if the quota rule operation failed.
// This is the preferred way to check for failure instead of comparing State directly.
func (r *QuotaRuleProviderResponse) IsFailure() bool {
	return r.State == JobRespFailure
}

// HasError returns true if there's an error message present.
// This can be used to check if detailed error information is available.
func (r *QuotaRuleProviderResponse) HasError() bool {
	return r.Message != ""
}

type QuotaRuleInfo struct {
	UUID            string
	Type            string
	Target          string
	DiskLimitInKibs int64
}

// QuotaRuleCollectionItem represents a single quota rule from GetQuotaRuleCollection
type QuotaRuleCollectionItem struct {
	UUID                  string
	Name                  string
	LifeCycleState        string
	LifeCycleStateDetails string
	QuotaTarget           string
	DiskLimitInKibs       int64
	VolumeUUID            string
	QuotaType             string
	RQuota                bool
	CreatedAt             time.Time
	UpdatedAt             time.Time
	DeletedAt             *time.Time
	Jobs                  []*datamodel.Job
	QuotaRuleInlineGroup  *QuotaRuleInlineGroup
	QuotaRuleInlineUsers  []*QuotaRuleInlineUser
	Description           *string
}

// QuotaRuleInlineGroup represents group information in a quota rule
type QuotaRuleInlineGroup struct {
	ID   *string // Group ID (GID or group name)
	Name *string // Group name
}

// QuotaRuleInlineUser represents user information in a quota rule
type QuotaRuleInlineUser struct {
	ID   *string // User ID (UID or username)
	Name *string // User name
}

// QuotaStatus represents the current state of the quota system on a volume
type QuotaStatus struct {
	Enabled bool   // Whether quota is enabled
	State   string // Quota state: "off", "on", "initializing", "resizing", "corrupt"
}

// QuotaEnableDisableResponse represents the response from enabling/disabling quota
// QuotaEnableDisableResponse contains the response from ONTAP quota enable/disable operations.
// It includes the operation state and any error messages.
//
// The State field should use the JobRespSuccess or JobRespFailure constants defined
// in quota_rule.go for consistency across the codebase.
type QuotaEnableDisableResponse struct {
	State   string // Job state: "success" or "failure" (use JobRespSuccess/JobRespFailure constants)
	Message string // Error message if state is "failure", empty on success
}

// IsSuccess returns true if the quota enable/disable operation completed successfully.
// This is the preferred way to check for success instead of comparing State directly.
func (r *QuotaEnableDisableResponse) IsSuccess() bool {
	return r.State == JobRespSuccess
}

// IsFailure returns true if the quota enable/disable operation failed.
// This is the preferred way to check for failure instead of comparing State directly.
func (r *QuotaEnableDisableResponse) IsFailure() bool {
	return r.State == JobRespFailure
}

// HasError returns true if there's an error message present.
// This can be used to check if detailed error information is available.
func (r *QuotaEnableDisableResponse) HasError() bool {
	return r.Message != ""
}

// JobStatus represents the status of an asynchronous ONTAP job operation.
// According to spec (create-quota-cvs-job-function.md), this matches storage.JobStatus
// and includes Code, State, and Message fields from the ONTAP job response.
//
// The State field should use the JobRespSuccess or JobRespFailure constants defined
// in quota_rule.go for consistency across the codebase.
type JobStatus struct {
	Code    int64  // Job error code (0 on success, non-zero on failure)
	State   string // Job state: "success" or "failure" (use JobRespSuccess/JobRespFailure constants)
	Message string // Error message if state is "failure", empty on success
}

// IsSuccess returns true if the job completed successfully.
// This is the preferred way to check for success instead of comparing State directly.
func (r *JobStatus) IsSuccess() bool {
	return r.State == JobRespSuccess
}

// IsFailure returns true if the job failed.
// This is the preferred way to check for failure instead of comparing State directly.
func (r *JobStatus) IsFailure() bool {
	return r.State == JobRespFailure
}

// HasError returns true if there's an error message present.
// This can be used to check if detailed error information is available.
func (r *JobStatus) HasError() bool {
	return r.Message != ""
}

type SnapshotListResponse struct {
	ProviderResponse
	VolumeExternalUUID string
}

type LunResponse struct {
	ProviderResponse
	SerialNumber string
	OSType       string
	Size         int64
}

type CloudTargetCreateParams struct {
	Name      *string
	Container *string
}

type CloudTargetModifyParams struct {
	Name *string
}

type CloudTargeCollectiontGetParams struct {
	Name *string
}

type CloudTarget struct {
	Name *string
	UUID *string
}

// SnapshotPolicy describes a snapshot policy in the cloud volume model
type SnapshotPolicy struct {
	Name      string
	Comment   string
	IsEnabled bool
	Schedules []*SnapshotPolicySchedule
}

// SnapshotPolicySchedule describes a snapshot policy schedule in the cloud volume model
type SnapshotPolicySchedule struct {
	Schedule        *Schedule
	Prefix          string
	Count           int64
	SnapmirrorLabel string
}

// Schedule describes a schedule in the cloud volume model
type Schedule struct {
	Name        string
	Description string
	Type        string
	Months      []int
	DaysOfMonth []int
	DaysOfWeek  []int
	Hours       []int
	Minutes     []int
}

type OntapAsyncResponse struct {
	JobUUID string
}

type OntapJob struct {
	UUID  string
	State string
	Error *OntapError
}

type OntapError struct {
	Code    string
	Message string
}

// UpdateSnapshotPolicyParams describes parameters supplied to Provider.UpdateSnapshotPolicy
type UpdateSnapshotPolicyParams struct {
	CurrentSnapshotPolicy  *SnapshotPolicy
	UpdatingSnapshotPolicy *SnapshotPolicy
}

// SnapshotPolicyScheduleUpdate describes a snapshot policy schedule update payload
type SnapshotPolicyScheduleUpdate struct {
	Action                 action
	SnapshotPolicySchedule SnapshotPolicySchedule
}

type CreateDnsParams struct {
	Domains []string
	Servers []string
}

// CreateQoSGroupPolicyParams is the input struct for Provider.CreateQoSGroupPolicy
// Throughput in MiB/s, IOPS as input, applied to a specific SVM
// Not for adaptive QoS
type CreateQoSGroupPolicyParams struct {
	Name          string // Name of the QoS policy group
	SvmName       string // SVM to apply the policy on
	MaxThroughput int64  // Throughput in MiBps
	MaxIOPS       int64  // Max IOPS
	IsShared      *bool  // Whether volumes share QoS budget (capacity-shared). If nil, ONTAP defaults to false
}

// QoSGroupPolicyResponse is the output struct for Provider.CreateQoSGroupPolicy
type QoSGroupPolicyResponse struct {
	Name          string
	UUID          string
	SvmName       string
	MaxThroughput int64
	MaxIOPS       int64
	IsShared      bool // Whether volumes share QoS budget (capacity-shared)
}

// ModifySVMWithQoSPolicyParams is the input struct for Provider.ModifySVMWithQoSPolicy
// Used to apply a QoS policy group to an existing SVM, or to clear it.
// When QoSPolicyName is empty string, the SVM's QoS policy is cleared (removed from vserver).
type ModifySVMWithQoSPolicyParams struct {
	SvmUUID       string // UUID of the SVM to modify
	QoSPolicyName string // Name of the QoS policy group to apply; empty string clears the policy from the SVM
}

// FindQoSGroupPolicyParams is the input struct for Provider.FindQoSGroupPolicy
// Used to find an existing QoS policy group by UUID or by name
type FindQoSGroupPolicyParams struct {
	UUID    string // UUID of the QoS policy group to find (preferred)
	Name    string // Name of the QoS policy group to find (alternative to UUID)
	SvmName string // SVM name to filter by (optional, passed through for backwards compatibility)
}

// UpdateQoSGroupPolicyParams is the input struct for Provider.UpdateQoSGroupPolicy
// Used to update an existing QoS policy group with new throughput and IOPS values
type UpdateQoSGroupPolicyParams struct {
	UUID          string // UUID of the QoS policy group to update
	Name          string // Name of the QoS policy group
	SvmName       string // SVM name
	MaxThroughput int64  // New throughput in MiBps
	MaxIOPS       int64  // New max IOPS
}

// DeleteQoSGroupPolicyParams is the input struct for Provider.DeleteQoSGroupPolicy
// Used to delete an existing QoS policy group
type DeleteQoSGroupPolicyParams struct {
	UUID    string // UUID of the QoS policy group to delete (preferred)
	Name    string // Name of the QoS policy group to delete (alternative to UUID)
	SvmName string // SVM name (optional, passed through for backwards compatibility)
}

type SmObjectStoreEndpointSnapshot struct {
	// Indicates whether or not the snapshot has objects in the archival storage.
	ArchivedObjects *bool
	CreateTime      *strfmt.DateTime
	// Indicates the group member count if the snapshot is from a FlexGroup object store endpoint.
	GroupMemberCount *int64
	// Logical size of the snapshot in bytes.
	LogicalSize     *int64
	Name            *string
	SnapmirrorLabel *string
	// ["not_locked","locked","cannot_be_locked","lock_expired"]
	SnapshotLockState *string
	// ["in_transfer","transferred","deleted","delete_cleanup","recyclable"]
	SnapshotState *string
	UUID          *strfmt.UUID
}

type UpdateSecurityAuditParams struct {
	Cli    bool
	HTTP   bool
	Ontapi bool
}

type SecurityAudit struct {
	Cli    bool
	HTTP   bool
	Ontapi bool
}

// InstallServerCertificateParams represents the parameters for installing a server certificate
type InstallServerCertificateParams struct {
	SvmName         string
	CertificateName string
	Certificate     string // PEM-encoded certificate
	PrivateKey      string // PEM-encoded private key
	CertificateType string // "server"
	CommonName      string
}

// InstallClientCAParams represents the parameters for installing a client-ca trust certificate.
type InstallClientCAParams struct {
	SvmName         string
	CertificateName string
	Certificate     string
	CommonName      string
}

// GetServerCertificateParams represents the parameters for getting a server certificate
type GetServerCertificateParams struct {
	SvmName         string
	CertificateName string
	CertificateType string
}

// DeleteServerCertificateParams represents the parameters for deleting a server certificate
type DeleteServerCertificateParams struct {
	SvmName         string
	CertificateName string
	CertificateType string
	SerialNumber    string // optional, narrows the match when provided
}

// ServerCertificateResponse represents the response from certificate operations
type ServerCertificateResponse struct {
	UUID            string
	Name            string
	CommonName      string
	CertificateType string
	ExpiryTime      string
	SerialNumber    string
}

// ModifySSLParams represents the parameters for modifying SSL configuration
type ModifySSLParams struct {
	SvmName       string
	ServerEnabled bool
	CA            string // Certificate Authority name
	Serial        string // Certificate serial number
}

// ModifySSLResponse represents the response from SSL modification
type ModifySSLResponse struct {
	Success bool
	Message string
}

type SmObjectStoreEndpointt struct {
	LogicalSize *int64
	UUID        *strfmt.UUID
}

// UpdateVolumeJunctionPathParams describes parameters for updating a volume's junction path
type UpdateVolumeJunctionPathParams struct {
	UUID         string
	VolumeName   string
	SvmName      string
	JunctionPath string
}

// MountVolumeParams describes parameters for mounting a volume
type MountVolumeParams struct {
	UUID         string
	JunctionPath string
}

// UpdateExportPolicyRulesParams describes parameters for updating export policy rules for a volume
type UpdateExportPolicyRulesParams struct {
	VolumeName   string
	SvmName      string
	ExportPolicy *ExportPolicy
}
