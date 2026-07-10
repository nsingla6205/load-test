package ontap_rest

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	cr "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cloud"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	nas "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/n_a_s"
	nvme "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/n_v_m_e"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/name_services"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/networking"
	san "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/s_a_n"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/security"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/snapmirror"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/storage"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/svm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	priv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	privmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

const (
	hybridUser            = "hybrid_user"
	authMethodCertificate = "certificate"
)

var (
	returnTimeout = strconv.FormatInt(int64(utils.GetConstraintInteger(env.GetUint("ONTAP_REST_SYNC_RETURN_TIMEOUT_SECONDS", 15), 0, 15, 15)), 10)
	// MD: returnTimeoutNoJob signals that we are not interested in getting a job and the entire operation should instead time out
	// this is useful for resources that in most cases take very little time to delete but may sometimes take longer.
	returnTimeoutNoJob           = nillable.ToPointer(strconv.Itoa(utils.GetConstraintInteger(int(cr.DefaultTimeout), 15, 120, 30)))
	objStoreProviderType         = env.GetString("OBJECT_STORE_PROVIDER", "googlecloud")
	objStoreServer               = env.GetString("OBJECT_STORES_SERVER", "storage.googleapis.com")
	objStoreAuthenticationType   = env.GetString("OBJECT_STORE_AUTH_TYPE", "GCP_SA")
	objStoreSnapmirrorUse        = "data"
	objStoreOwner                = "snapmirror"
	defaultCIFSCAShareProperties = []string{utils.CIFSSharePropertyOplocks, utils.CIFSSharePropertyShowsnapshot}
	defaultCIFSShareProperties   = []string{utils.CIFSSharePropertyOplocks, utils.CIFSSharePropertyChangenotify, utils.CIFSSharePropertyShowsnapshot, utils.CIFSSharePropertyShowPreviousVersions}
)

// BaseParams contains all the common parameters that ONTAP REST supports
type BaseParams struct {
	Fields        []string
	ReturnRecords *bool
	MaxRecords    *int64
}

// JobAccepted contains the async job information from ONTAP
type JobAccepted struct {
	JobUUID      string
	ResourceUUID string
}

// CloudTargetCollectionGetParams is the input param struct for cloudClient.CloudTargetsGet
type CloudTargetCollectionGetParams struct {
	BaseParams
	Owner *string
	Name  *string
}

// CloudTargetCreateParams is the input param struct for cloudClient.CloudTargetCreate
type CloudTargetCreateParams struct {
	BaseParams
	Name           *string
	ProviderType   *string
	Server         *string
	Container      *string
	IpspaceName    *string
	Owner          *string
	AccessKey      *string
	SecretPassword *strfmt.Password
	SslEnabled     bool
}

type CloudTargetDeleteParams struct {
	UUID string
}

// CloudTargetModifyParams is the input param struct for cloudClient.CloudTargetModify
type CloudTargetModifyParams struct {
	BaseParams
	Name           *string
	ProviderType   *string
	Server         *string
	Container      *string
	AccessKey      *string
	SecretPassword *strfmt.Password
	Owner          *string
	UUID           string
}

// ClusterGetParams is the input param struct for clusterClient.ClusterGet
type ClusterGetParams struct {
	BaseParams
}

// Cluster is a simple wrapper of models.Cluster
type Cluster struct {
	models.Cluster
}

// JobGetParams is the input param struct for clusterClient.JobGet
type JobGetParams struct {
	BaseParams
	UUID string
}

// JobGetParamsToONTAP converts JobGetParams to ONTAP API parameters
func JobGetParamsToONTAP(params *JobGetParams) *cluster.JobGetParams {
	otParams := cluster.NewJobGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	if params.Fields != nil {
		otParams.SetFields(params.Fields)
	}

	return otParams
}

// Job is a simple wrapper of models.Job
type Job struct {
	models.Job
}

// JobCollectionGetParams is the input param struct for clusterClient.JobGet
type JobCollectionGetParams struct {
	BaseParams
	Fields      []string
	SvmUUID     string
	Description string
}

// NodesGetParams is the input param struct for clusterClient.NodesGet
type NodesGetParams struct {
	BaseParams
}

func nodesGetParamsToONTAP(params *NodesGetParams) *cluster.NodesGetParams {
	otParams := cluster.NewNodesGetParams()
	if params == nil {
		return otParams
	}

	otParams.Fields = params.Fields
	return otParams
}

// Node is a simple wrapper of models.NodeResponseInlineRecordsInlineArrayItem
type Node struct {
	models.NodeResponseInlineRecordsInlineArrayItem
}

// ScheduleCollectionGetParams is the input param struct for clusterClient.ScheduleCollectionGet
type ScheduleCollectionGetParams struct {
	BaseParams
	Name string
}

// Schedule is a simple wrapper of models.Schedule
type Schedule struct {
	models.Schedule
}

// DNSGetParams is the input param struct for nameServicesClient.DNSGet
type DNSGetParams struct {
	BaseParams
	SvmUUID string
}

// DNSCreateParams is the input param struct for nameServicesClient.DNSCreate
type DNSCreateParams struct {
	SvmUUID    string
	Domains    []string
	DNSServers []string
}

// DNS is a simple wrapper of models.DNS
type DNS struct {
	models.DNS
}

// DNSModifyParams is the input param struct for nameServicesClient.DNSModify
type DNSModifyParams struct {
	BaseParams
	SvmUUID          string
	Domains          []string
	NameServers      []string
	DDNSModifyParams DDNSModifyParams
}

// DDNSModifyParams is the input param struct for nameServicesClient.DNSModify.DynamicDNS
type DDNSModifyParams struct {
	UseSecure *bool
	Fqdn      *string
	Enabled   *bool
}

// CifsServiceCollectionGetGroupsParams is the input param struct for fetching cifs groups and users
type CifsServiceCollectionGetGroupsParams struct {
	BaseParams
	Sid     *string
	SvmUUID string
}

// CifsGroup is a CIFS group
type CifsGroup struct {
	Name    string
	Sid     string
	Members []string
}

// CifsServiceCollectionGetPrivilegedMembersParams is the input param struct for fetching privileged members
type CifsServiceCollectionGetPrivilegedMembersParams struct {
	BaseParams
	SvmUUID string
}

// CifsServiceModifyGroupMembersParams is the input param struct to add or remove members to CIFS groups
type CifsServiceModifyGroupMembersParams struct {
	Sid     string
	Members []string
	SvmUUID string
}

// CifsServiceDeleteParams is the input params struct for nas.CifsServiceDelete
type CifsServiceDeleteParams struct {
	BaseParams
	SvmUUID            string
	AdminPassword      string
	AdminUsername      string
	ClientID           string
	TenantID           string
	EntraIDCertificate strfmt.Password
	Force              bool
}

// CifsServiceModifySecurityPrivilegeParams is the input param struct to modify CIFS user privileges
type CifsServiceModifySecurityPrivilegeParams struct {
	Member  string
	SvmUUID string
}

// CifsShareCreateParams is the input param struct for nas.CifsShareCreate
type CifsShareCreateParams struct {
	BaseParams
	ShareProperties []string
	SvmName         *string
	Path            string
	Name            string
}

type CifsShareModifyParams struct {
	BaseParams
	ShareProperties []string
	SvmUUID         string
	ShareName       string
}

type CifsShareCollectionGetParams struct {
	BaseParams
	Fields    []string
	SvmUUID   string
	ShareName string
}

// CifsShareGetResponse will represent the response from CifsShareGet endpoint
type CifsShareGetResponse struct {
	ShareProperties []string
}

// HostRecordGetParams is the input param struct for nameServicesClient.HostRecordGet
type HostRecordGetParams struct {
	BaseParams
	Host     string
	SvmUUID  string
	UseCache *bool
}

// CifsDomainPreferredDCDeleteParams is the input param struct for nasClient.CifsDomainCifsDomainPreferredDCDelete
type CifsDomainPreferredDCDeleteParams struct {
	BaseParams
	Fqdn     *string
	ServerIP *string
	SvmUUID  string
}

// CifsDomainPreferredDCCreateParams is the input param struct for nasClient.CifsDomainCifsDomainPreferredDCCreate
type CifsDomainPreferredDCCreateParams struct {
	BaseParams
	CifsDomainPreferredDC *CifsDomainPreferredDC
	SkipConfigValidation  *bool
	SvmUUID               string
}

// CifsDomainPreferredDC in the input param for model.CifsDomainPreferredDCCreateParams
type CifsDomainPreferredDC struct {
	Fqdn     *string
	ServerIP *string
	Status   *CifsDomainPreferredDcInlineStatus
}

// CifsDomainPreferredDcInlineStatus is the input param for model.CifsDomainPreferredDC
type CifsDomainPreferredDcInlineStatus struct {
	Details   *string
	Reachable *bool
}

// SrvLookupParams is the input param struct for nasClient.DomainControllersSrvLookupGet
type SrvLookupParams struct {
	BaseParams
	LookupString string
	LookupType   *string
	NameServers  []string
	Node         string
	SVMName      string
}

// HostRecord is a simple wrapper of models.HostRecord
type HostRecord struct {
	models.HostRecord
}

// LdapCollectionGetParams is the input param struct for nameServicesClient.LdapCollectionGet
type LdapCollectionGetParams struct {
	BaseParams
	SvmUUID *string
}

// LdapService is a simple wrapper of models.LdapService
type LdapService struct {
	models.LdapService
}

// LdapGetParams is the input params struct for nameServicesClient.LdapGet
type LdapGetParams struct {
	BaseParams
	SvmUUID string
}

// LdapCreateParams is the input params struct for nameServicesClient.LdapCreate
type LdapCreateParams struct {
	BaseParams
	BindAsCifsServer              *bool
	DomainName                    *string
	BaseDN                        *string
	UserDn                        *string
	GroupDn                       *string
	GroupMembershipFilter         *string
	PreferredServersForLdapClient []*string
	TLSEnabled                    *bool
	Schema                        *string
	SessionSecurity               *string
	SvmUUID                       string
	LdapPort                      *int64
	LdapServers                   []*string
}

// LdapModifyParams is the input params struct for nameServicesClient.LdapModify
type LdapModifyParams struct {
	BaseParams
	UserDn                        *string
	GroupDn                       *string
	BaseDN                        *string
	GroupMembershipFilter         *string
	PreferredServersForLdapClient []*string
	TLSEnabled                    *bool
	Schema                        *string
	SvmUUID                       string
	Domain                        *string
	LdapServers                   []*string
}

// LdapDeleteParams is the input params struct for nameServicesClient.LdapDelete
type LdapDeleteParams struct {
	BaseParams
	SvmUUID string
}

// LdapSchemaCreateParams is the input params struct for nameServicesClient.LdapSchemaCreate
type LdapSchemaCreateParams struct {
	BaseParams
	Name     *string
	Template *string
	SvmUUID  *string
}

// LdapSchemaModifyParams is the input params struct for nameServicesClient.LdapSchemaModify
type LdapSchemaModifyParams struct {
	BaseParams
	MaximumGroups *int64
	SvmUUID       string
	SchemaName    string
}

// LocalHostCreateParams is the input param struct for nameServicesClient.LocalHostCreate
type LocalHostCreateParams struct {
	BaseParams
	Address  *string
	Hostname *string
	Owner    *string
	Timeout  time.Duration
}

// LocalHostDeleteParams is the input param struct for nameServicesClient.LocalHostDelete
type LocalHostDeleteParams struct {
	BaseParams
	Address string
	SvmUUID string
	Timeout time.Duration
}

// UnixGroupCollectionGetParams is the input param struct for nameServicesClient.UnixGroupCollectionGet
type UnixGroupCollectionGetParams struct {
	BaseParams
	SvmName   *string
	ID        *int64
	Name      *string
	UsersName *string
}

// UnixGroup is a simple wrapper of models.UnixGroup
type UnixGroup struct {
	models.UnixGroup
}

// UnixGroupCreateParams is the input param struct for nameServicesClient.UnixGroupCreate
type UnixGroupCreateParams struct {
	SvmName string
	SvmUUID string
	Name    string
	GID     uint32
}

// UnixGroupDeleteParams is the input param struct for nameServicesClient.UnixGroupDelete
type UnixGroupDeleteParams struct {
	SvmName string // FIXME: unused - remove?
	SvmUUID string
	Name    string
}

// UnixUserCollectionGetParams is the input param struct for nameServicesClient.UnixUserCollectionGet
type UnixUserCollectionGetParams struct {
	BaseParams
	SvmName    string
	SvmUUID    string
	Name       *string
	FullName   *string
	UID        *uint32
	PrimaryGID *uint32
}

// UnixUser is a simple wrapper of models.UnixUser
type UnixUser struct {
	models.UnixUser
}

// UnixUserCreateParams is the input param struct for nameServicesClient.UnixUserCreate
type UnixUserCreateParams struct {
	SvmName    string
	SvmUUID    string
	Name       string
	FullName   *string
	UID        uint32
	PrimaryGID uint32
}

// UnixUserDeleteParams is the input param struct for nameServicesClient.UnixUserDelete
type UnixUserDeleteParams struct {
	SvmName string // FIXME: unused - remove?
	SvmUUID string
	Name    string
}

// GetGroupIDsListParams is the input param struct for nameServicesClient.GetGroupIDsList
type GetGroupIDsListParams struct {
	BaseParams
	Username string
	SvmName  string
	Node     string
	UseCache string
}

// CifsDomainGetParams is the input param struct for nasClient.CifsDomainGet
type CifsDomainGetParams struct {
	BaseParams
	RediscoverTrusts       *bool
	ResetDiscoveredServers *bool
	SvmUUID                string
}

// CifsDomain is a simple wrapper of models.CifsDomain
type CifsDomain struct {
	models.CifsDomain
}

// NfsGetParams is the input params struct for nasClient.NfsGet
type NfsGetParams struct {
	BaseParams
	SvmUUID string
}

// NfsCreateParams is the input param struct for nasClient.NfsModify
type NfsCreateParams struct {
	SvmUUID               string
	NFSv41Enabled         bool
	NFSv364BitIdentifiers bool
	ShowmountEnabled      *bool
	NFSv4IDDomain         *string
	VstorageEnabled       bool
}

// Nfs is a simple wrapper of models.NfsService
type Nfs struct {
	models.NfsService
}

// NfsModifyParams is the input param struct for nasClient.NfsModify
type NfsModifyParams struct {
	SvmUUID                      string
	V4IDDomain                   *string
	ShowmountEnabled             *bool
	RquotaEnabled                *bool
	AllowLocalNFSUsersWithLdap   *bool
	ExtendedGroupsLimit          *int64
	Enabled                      *bool
	V3Enabled                    *bool
	V40Enabled                   *bool
	V41Enabled                   *bool
	VstorageEnabled              *bool
	FileSessionIoGroupingCount   *int64
	AuthSysExtendedGroupsEnabled *bool
}

// NfsClientsGetParams is the input param struct for nasClient.NfsClientsGet
type NfsClientsGetParams struct {
	BaseParams
	VolumeUUID *string
	SvmName    *string
	Protocol   *string
}

// NfsClients is a simple wrapper of models.NfsClients
type NfsClients struct {
	models.NfsClients
}

func nfsClientsGetParamsToONTAP(params *NfsClientsGetParams) *nas.NfsClientsGetParams {
	otParams := nas.NewNfsClientsGetParams()
	if params == nil {
		return otParams
	}
	otParams.SetVolumeUUID(params.VolumeUUID)
	otParams.SetSvmName(params.SvmName)
	otParams.SetFields(params.Fields)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	return otParams
}

// AuditCreateParams is the input param struct for nasClient.AuditCreate
type AuditCreateParams struct {
	BaseParams
	SvmName             *string
	Enabled             *bool
	AuthorizationPolicy *bool
	CapStaging          *bool
	CifsLogonLogoff     *bool
	FileOperations      *bool
	FileShare           *bool
	SecurityGroup       *bool
	UserAccount         *bool
	Format              *string
	LogSize             *int64
	LogPath             *string
	LogRotation         []*int64
	LogRetentionCount   *int64
	Guarantee           *bool
	ChargeQos           *bool
}

// AuditLogRedirectCreateParams is the input param struct for securityClient.AuditLogRedirectCreate
type AuditLogRedirectCreateParams struct {
	BaseParams
	SvmUUID *string
}

// AuditLogRedirectGetParams is the input param struct for securityClient.AuditLogRedirectGet
type AuditLogRedirectGetParams struct {
	BaseParams
}

// AuditLogRedirect is a simple wrapper of models.AuditLogRedirect
type AuditLogRedirect struct {
	models.AuditLogRedirect
}

// AuditLogRedirectDeleteParams is the input param struct for securityClient.AuditLogRedirectDelete
type AuditLogRedirectDeleteParams struct {
	BaseParams
}

// CifsLocalGroupMembersCreateParams is the input param struct for nasClient.CifsLocalGroupMembersCreate
type CifsLocalGroupMembersCreateParams struct {
	BaseParams
	SvmUUID string
	SID     string
	Users   []string
}

// CifsLocalGroupMembersBulkDeleteParams is the input param struct for nasClient.CifsLocalGroupMembersBulkDelete
type CifsLocalGroupMembersBulkDeleteParams struct {
	BaseParams
	SID     string
	SvmUUID string
	Users   []string
}

// CifsLocalGroupMember is a simple wrapper of models.LocalCifsGroupMembers
type CifsLocalGroupMember struct {
	models.LocalCifsGroupMembers
}

// CifsLocalGroupMembersCollectionGetParams is the input param struct for nasClient.CifsLocalGroupMembersCollectionGet
type CifsLocalGroupMembersCollectionGetParams struct {
	BaseParams
	SvmUUID string
	SID     string
}

// CifsUserGroupPrivilegesCreateParams is the input param struct for nasClient.CifsUserGroupPrivilegesCreate
type CifsUserGroupPrivilegesCreateParams struct {
	BaseParams
	Name       *string
	Privileges []*string
	SvmUUID    *string
}

// CifsUserGroupPrivileges is a simple wrapper of models.UserGroupPrivileges
type CifsUserGroupPrivileges struct {
	models.UserGroupPrivileges
}

// CifsUserGroupPrivilegesCollectionGetParams is the input param struct for nasClient.CifsUserGroupPrivilegesCollectionGet
type CifsUserGroupPrivilegesCollectionGetParams struct {
	BaseParams
	Privileges *string
	SvmUUID    *string
}

// CifsUserGroupPrivilegesModifyParams is the input param struct for nasClient.CifsUserGroupPrivilegesModify
type CifsUserGroupPrivilegesModifyParams struct {
	BaseParams
	Name       string
	Privileges []*string
	SvmUUID    string
}

// CifsSessionCollectionGetParams is the input param struct for nasClient.CifsSessionCollectionGet
type CifsSessionCollectionGetParams struct {
	BaseParams
	VolumeName *string
}

// CifsSession is a simple wrapper of models.CifsSession
type CifsSession struct {
	models.CifsSession
}

// BreakFileLocksParams is the input param struct for nasClient.BreakFileLocks
type BreakFileLocksParams struct {
	BaseParams
	VolumeUUID string
	SvmName    string
	ClientIP   *string
}

// NetworkIPInterfacesGetParams is the input param struct for networkingClient.NetworkIPInterfacesGet
type NetworkIPInterfacesGetParams struct {
	BaseParams
	SvmUUID           *string
	SvmName           *string
	Name              *string
	IPAddress         *string
	ServicePolicyName *string
}

func networkIPInterfacesGetParamsToONTAP(params *NetworkIPInterfacesGetParams) *networking.NetworkIPInterfacesGetParams {
	otParams := networking.NewNetworkIPInterfacesGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	otParams.SetFields(params.Fields)
	if params.SvmName != nil {
		otParams.SetSvmName(params.SvmName)
	}
	if params.Name != nil {
		otParams.SetName(params.Name)
	}
	if params.SvmUUID != nil {
		otParams.SetSvmUUID(params.SvmUUID)
	}
	if params.IPAddress != nil {
		otParams.SetIPAddress(params.IPAddress)
	}
	if params.ServicePolicyName != nil {
		otParams.SetServicePolicyName(params.ServicePolicyName)
	}
	return otParams
}

// NetworkIPInterfacesCreateParams is the input param struct for networkingClient.NetworkIPInterfacesGet
type NetworkIPInterfacesCreateParams struct {
	Name          string
	IPAddress     string
	Netmask       string
	SvmName       string
	HomePort      string
	HomeNode      string
	ServicePolicy string
}

// IPInterface is a simple wrapper of models.IPInterface
type IPInterface struct {
	models.IPInterface
}

// IPServicePolicy is a simple wrapper of models.IPServicePolicy
type IPServicePolicy struct {
	models.IPServicePolicy
}

// SecurityKeyManagerCollectionGetParams is the input param struct for securityClient.SecurityKeyManagerCollectionGet
type SecurityKeyManagerCollectionGetParams struct {
	BaseParams
	Timeout time.Duration
}

// SecurityKeyManager is a simple wrapper of models.SecurityKeyManager
type SecurityKeyManager struct {
	models.SecurityKeyManager
}

// SecurityKeyManagerMigrateParams is the input param struct for securityClient.SecurityKeyManagerMigrate
type SecurityKeyManagerMigrateParams struct {
	BaseParams
	SourceUUID      string
	DestinationUUID string
	Timeout         time.Duration
}

// SecurityKeystoreModifyParams is the input param struct for securityClient.SecurityKeystoreModify
type SecurityKeystoreModifyParams struct {
	BaseParams
	KeystoreUUID string
	Enabled      bool
	Timeout      time.Duration
}

// SecurityKeystoreDeleteParams is the input param struct for securityClient.SecurityKeystoreDelete
type SecurityKeystoreDeleteParams struct {
	BaseParams
	KeystoreUUID string
	Timeout      time.Duration
}

// IpsecPolicyEndpoint describes an endpoint for IpsecPolicy parameters
type IpsecPolicyEndpoint struct {
	Address string
	Netmask string
	Port    string
}

// IpsecPolicyCreateParams is the input param struct for securityClient.IpsecPolicyCreate
type IpsecPolicyCreateParams struct {
	BaseParams
	Action         *string
	Enabled        *bool
	LocalEndpoint  *IpsecPolicyEndpoint
	Name           *string
	Protocol       *string
	RemoteEndpoint *IpsecPolicyEndpoint
	SecretKey      *string
	LocalIdentity  *string
	RemoteIdentity *string
	SvmName        *string
}

// IpsecPolicyModifyParams is the input param struct for securityClient.IpsecPolicyModify
type IpsecPolicyModifyParams struct {
	BaseParams
	UUID           string
	RemoteEndpoint *IpsecPolicyEndpoint
	LocalIdentity  *string
	RemoteIdentity *string
}

// IpsecPolicyDeleteParams is the input param struct for securityClient.IpsecPolicyDelete
type IpsecPolicyDeleteParams struct {
	BaseParams
	UUID string
}

// IpsecPolicyCollectionGetParams is the input param struct for securityClient.IpsecPolicyCollectionGet
type IpsecPolicyCollectionGetParams struct {
	BaseParams
	Name    *string
	SvmName *string
}

// IpsecPolicy is a simple wrapper of models.IpsecPolicyResponseInlineRecordsInlineArrayItem
type IpsecPolicy struct {
	models.IpsecPolicyResponseInlineRecordsInlineArrayItem
}

// GcpKmsCreateParams is the input param struct for securityClient.GcpKmsCreate
type GcpKmsCreateParams struct {
	BaseParams
	KeyName                *string
	KeyRingLocation        *string
	KeyRingName            *string
	ProjectID              *string
	ApplicationCredentials *strfmt.Password
	SvmName                *string
	PrivilegedAccount      *string
}

type SecurityAuditUpdateParams struct {
	Cli    bool
	HTTP   bool
	Ontapi bool
}

type SecurityAudit struct {
	models.SecurityAudit
}

func securityAuditModifyParamsToONTAP(params *SecurityAuditUpdateParams) *security.SecurityAuditModifyParams {
	otParams := security.NewSecurityAuditModifyParams()
	if params == nil {
		return otParams
	}

	audit := models.SecurityAudit{
		Cli:    &params.Cli,
		HTTP:   &params.HTTP,
		Ontapi: &params.Ontapi,
	}
	otParams.SetInfo(&audit)

	return otParams
}

type SecurityLogForwardingCreateParams struct {
	BaseParams
	Address      *string
	Port         *int64
	Protocol     *string
	Facility     *string
	VerifyServer *bool
}

type SecurityLogForwardingGetParams struct {
	Address string
	Port    int64
}

type SecurityAuditLogForward struct {
	models.SecurityAuditLogForward
}

func securityLogForwardingGetParamsToONTAP(params *SecurityLogForwardingGetParams) *security.SecurityLogForwardingGetParams {
	otParams := security.NewSecurityLogForwardingGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetAddress(params.Address)
	otParams.SetPort(params.Port)

	return otParams
}

func securityLogForwardingCreateParamsToONTAP(params *SecurityLogForwardingCreateParams) *security.SecurityLogForwardingCreateParams {
	otParams := security.NewSecurityLogForwardingCreateParams()
	if params == nil {
		return otParams
	}

	rr := true

	otParams.SetReturnRecords(&rr)
	otParams.SetForce(&rr)
	otParams.SetInfo(
		&models.SecurityAuditLogForward{
			Address:      params.Address,
			Port:         params.Port,
			Protocol:     params.Protocol,
			Facility:     params.Facility,
			VerifyServer: params.VerifyServer,
		})
	return otParams
}

// EMS Event Notification types and parameters

type EMSEventDestinationCreateParams struct {
	BaseParams
	Name                  *string
	Type                  *string // "syslog", "email", "snmp", "rest_api"
	SyslogHost            *string
	SyslogPort            *int64
	SyslogTransport       *string // "udp_unencrypted", "tcp_unencrypted", "tcp_encrypted"
	SyslogTimestampFormat *string // "rfc-3164", "rfc-5424", "no-override"
	SyslogMessageFormat   *string // "legacy-netapp", "rfc-5424"
}

type EMSEventFilterCreateParams struct {
	BaseParams
	Name *string
}

type EMSEventFilterRuleAddParams struct {
	BaseParams
	FilterName *string
	Type       *string  // "include", "exclude"
	Severity   []string // "EMERGENCY", "ALERT", "ERROR", "NOTICE", "INFORMATIONAL"
}

type EMSEventNotificationCreateParams struct {
	BaseParams
	FilterName   *string
	Destinations []string
}

type EMSEventDestinationModifyParams struct {
	BaseParams
	Filters []string // Filter names to link to destination
}

type EMSEventDestination struct {
	Name   string
	Type   string
	Syslog *EMSEventDestinationSyslog
}

type EMSEventDestinationSyslog struct {
	Host            string
	Port            int64
	Transport       string
	TimestampFormat string
	MessageFormat   string
}

func gcpKmsCreateParamsToONTAP(params *GcpKmsCreateParams) *security.GcpKmsCreateParams {
	otParams := security.NewGcpKmsCreateParams()
	if params == nil {
		return otParams
	}

	rr := "true"
	otParams.SetReturnRecords(&rr)
	otParams.SetInfo(
		&models.GcpKms{
			KeyName:                params.KeyName,
			KeyRingLocation:        params.KeyRingLocation,
			KeyRingName:            params.KeyRingName,
			ProjectID:              params.ProjectID,
			ApplicationCredentials: params.ApplicationCredentials,
			Svm:                    &models.GcpKmsInlineSvm{Name: params.SvmName},
			PrivilegedAccount:      params.PrivilegedAccount,
		})
	return otParams
}

type KeyManagerConfig struct {
	models.KeyManagerConfig
}

type KeyManagerConfigGCPModifyParams struct {
}

type KeyManagerConfigModifyParams struct {
	BaseParams
	Info *models.KeyManagerConfig
}

func getGCPKeyManagerConfigModifyParamsToOntap() *security.KeyManagerConfigModifyParams {
	otParams := security.NewKeyManagerConfigModifyParams()
	tt := true
	otParams.SetInfo(
		&models.KeyManagerConfig{
			HealthMonitorPolicy: &models.KeyManagerConfigInlineHealthMonitorPolicy{
				Gcp: &models.KeyManagerConfigInlineHealthMonitorPolicyInlineGcp{
					Enabled:             &tt,
					ManageVolumeOffline: &tt,
				},
			},
		},
	)
	return otParams
}

func serverRootCAGetParamsToONTAPCollectionGet(params *ServerRootCAGetParams) *security.SecurityCertificateCollectionGetParams {
	otParams := security.NewSecurityCertificateCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetSvmName(params.SvmName)
	otParams.SetName(params.Name)
	otParams.SetType(params.CertificateType)
	otParams.SetFields(params.Fields)
	return otParams
}

func serverRootCAInstallParamsToONTAP(params *ServerRootCAInstallParams) *security.SecurityCertificateCreateParams {
	otParams := security.NewSecurityCertificateCreateParams()
	if params == nil {
		return otParams
	}

	// Set ReturnRecords to true so the API returns the created certificate in the response
	// This is required to get the certificate details (UUID, SerialNumber, etc.) after creation
	otParams.SetReturnRecords(nillable.ToPointer("true"))

	info := &models.SecurityCertificate{
		PrivateKey:        params.PrivateKey,
		PublicCertificate: params.Certificate,
		Type:              params.CertificateType,
		CommonName:        params.CommonName,
		Name:              params.Name,
	}
	// Only scope the certificate to an SVM when a name is provided. When empty,
	// leave svm unset so ONTAP installs at cluster (admin) scope — required for a
	// client_ca that authenticates the cluster-admin account, and it avoids a
	// dependency on a data SVM existing at install time.
	if params.SvmName != nil && *params.SvmName != "" {
		info.Svm = &models.SecurityCertificateInlineSvm{Name: params.SvmName}
	}
	otParams.SetInfo(info)
	return otParams
}

func serverRootCADeleteParamsToONTAPCollectionDelete(params *ServerRootCADeleteParams) *security.SecurityCertificateDeleteCollectionParams {
	otParams := security.NewSecurityCertificateDeleteCollectionParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	otParams.SetSvmName(params.SvmName)
	otParams.SetSerialNumber(params.SerialNumber)
	otParams.SetCommonName(params.CommonName)
	otParams.SetCa(params.CertificateAuthority)
	return otParams
}

// GcpKms is a simple wrapper of models.GcpKms
type GcpKms struct {
	models.GcpKms
}

// GcpKmsPriv is a simple wrapper of models.GcpKms
type GcpKmsPriv struct {
	privmodels.GcpKms
}

// GcpKmsDeleteParams is the input param struct for securityClient.GcpKmsDelete
type GcpKmsDeleteParams struct {
	BaseParams
	UUID string
}

// GcpKmsModifyParams is the input param struct for securityClient.GcpKmsModify
type GcpKmsModifyParams struct {
	BaseParams
	UUID                   string
	ApplicationCredentials *log.Secret
}

// GcpKmsGetParams is the input param struct for securityClient.GcpKmsGet
type GcpKmsGetParams struct {
	BaseParams
	UUID string
}

func gcpKmsGetParamsToONTAP(params *GcpKmsGetParams) *security.GcpKmsGetParams {
	otParams := security.NewGcpKmsGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	otParams.SetFields(params.Fields)
	return otParams
}

func gcpKmsDeleteParamsToOntap(params *GcpKmsDeleteParams) *security.GcpKmsDeleteParams {
	otParams := security.NewGcpKmsDeleteParams()
	if params == nil {
		return otParams
	}
	otParams.SetUUID(params.UUID)
	return otParams
}

// AggregateCollectionGetParams is the input param struct for storageClient.AggregateCollectionGet
type AggregateCollectionGetParams struct {
	BaseParams
	Name *string
}

func aggregateCollectionGetParamsToONTAP(params *AggregateCollectionGetParams) *storage.AggregateCollectionGetParams {
	otParams := storage.NewAggregateCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(params.Name)
	otParams.SetFields(params.Fields)
	return otParams
}

// Aggregate is a simple wrapper of models.Aggregate
type Aggregate struct {
	models.Aggregate
}

// IsHome return true if the aggregate is currently on its home node
func (a *Aggregate) IsHome() bool {
	if a.HomeNode == nil || a.HomeNode.UUID == nil || a.Node == nil || a.Node.UUID == nil {
		return false
	}

	return *a.HomeNode.UUID == *a.Node.UUID
}

// IsOnline returns true if an aggregate is online.
func (a *Aggregate) IsOnline() bool {
	if a.State == nil || *a.State != models.AggregateStateOnline {
		return false
	}

	return true
}

// AggregateModifyParams is the input param struct for storageClient.AggregateModify
type AggregateModifyParams struct {
	BaseParams
	UUID                     string
	TieringFullnessThreshold *int64
}

func aggregateModifyParamsToONTAP(params *AggregateModifyParams) *storage.AggregateModifyParams {
	otParams := storage.NewAggregateModifyParams()
	if params == nil {
		return otParams
	}
	otParams.SetUUID(params.UUID)
	otParams.SetInfo(&models.Aggregate{
		CloudStorage: &models.AggregateInlineCloudStorage{
			TieringFullnessThreshold: params.TieringFullnessThreshold,
		},
	})
	return otParams
}

func qosPolicyGroupCollectionModifyParamsToONTAP(qosPolicyGroupParams []*QosPolicyGroupModifyCollectionParams) *storage.QosPolicyModifyCollectionParams {
	otParams := storage.NewQosPolicyModifyCollectionParams()

	var qosPolicyList []*models.QosPolicy
	for _, qosPolicy := range qosPolicyGroupParams {
		if qosPolicy.UUID == "" {
			continue
		}

		qosPolicyGroup := &models.QosPolicy{
			UUID: &qosPolicy.UUID,
			Fixed: &models.QosPolicyInlineFixed{
				MaxThroughputMbps: &qosPolicy.Throughput,
				MaxThroughputIops: &qosPolicy.Iops,
			},
		}
		if qosPolicy.Name != "" {
			qosPolicyGroup.Name = &qosPolicy.Name
		}

		qosPolicyList = append(qosPolicyList, qosPolicyGroup)
	}

	if len(qosPolicyList) == 0 {
		return otParams
	}
	otParams.SetInfo(storage.QosPolicyModifyCollectionBody{
		QosPolicyResponseInlineRecords: qosPolicyList,
	})
	continueOnFailure := "true"
	otParams.SetContinueOnFailure(&continueOnFailure)
	return otParams
}

// AggregateSimulate is a simple wrapper of models.Aggregate
type AggregateSimulate struct {
	models.AggregateSimulate
}

// QosPolicyModifyCollection is a simple wrapper of models.QosPolicyModifyCollection
type QosPolicyModifyCollection struct {
	models.QosPolicyJobLinkResponse
}

// QosPolicyGroupModifyCollectionParams is the input param struct for storageClient.VolumeModifyCollectionParams
type QosPolicyGroupModifyCollectionParams struct {
	BaseParams
	Throughput int64
	Iops       int64
	UUID       string
	Name       string // New name for the policy (optional; non-empty to rename in ONTAP)
}

// VolumeModifyParams is the input param struct for storageClient.VolumeModify
type VolumeModifyParams struct {
	BaseParams
	UUID                           string
	QuotaEnabled                   *bool
	ReKey                          *bool
	SplitInitiated                 *bool
	MatchParentStorageTier         bool
	RestoreToSnapshotUUID          *string
	State                          *string
	Path                           *string
	SnapshotPolicyName             *string
	Movement                       *VolumeMovementParams
	Comment                        *string
	SecurityStyle                  *string
	UnixPermissions                *string
	SnapReserve                    *int64
	MaxFiles                       *uint64
	MaxAutoSize                    *uint64
	Size                           *uint64
	LogicalSpaceEnforcement        *bool
	SnapshotDirectoryAccessEnabled *bool
	SetAtTimeEnabled               *bool
	ExportPolicy                   *string
	QosPolicy                      *string
	AntiRansomwareState            *string
	TieringPolicy                  *TieringPolicy
	EncryptionEnable               *bool
	NasUid                         *int64
}

// FlexcacheModifyParams is the input param struct for storageClient.FlexcacheModify
type FlexcacheModifyParams struct {
	BaseParams
	UUID                       string
	PrepopulateDirPaths        []*string
	PrepopulateExcludeDirPaths []*string
	PrepopulateRecurse         *bool
	WritebackEnabled           *bool
	RelativeSizeEnabled        *bool
	RelativeSizePercentage     *int16
	AtimeScrubEnabled          *bool
	AtimeScrubPeriod           *int16
	CifsChangeNotifyEnabled    *bool
}

func flexCacheModifyParamsToONTAP(params *FlexcacheModifyParams) *storage.FlexcacheModifyParams {
	otParams := storage.NewFlexcacheModifyParams()
	if params == nil {
		return otParams
	}

	flexCache := &models.Flexcache{}

	// Atime scrub
	if params.AtimeScrubEnabled != nil || params.AtimeScrubPeriod != nil {
		flexCache.AtimeScrub = &models.FlexcacheInlineAtimeScrub{
			Enabled: params.AtimeScrubEnabled,
			Period:  params.AtimeScrubPeriod,
		}
	}

	// CIFS change notify
	if params.CifsChangeNotifyEnabled != nil {
		flexCache.CifsChangeNotify = &models.FlexcacheInlineCifsChangeNotify{
			Enabled: params.CifsChangeNotifyEnabled,
		}
	}

	// Writeback
	if params.WritebackEnabled != nil {
		flexCache.Writeback = &models.FlexcacheInlineWriteback{
			Enabled: params.WritebackEnabled,
		}
	}

	// Prepopulate
	if len(params.PrepopulateDirPaths) > 0 ||
		len(params.PrepopulateExcludeDirPaths) > 0 ||
		params.PrepopulateRecurse != nil {
		flexCache.Prepopulate = &models.FlexcacheInlinePrepopulate{
			DirPaths:        params.PrepopulateDirPaths,
			ExcludeDirPaths: params.PrepopulateExcludeDirPaths,
			Recurse:         params.PrepopulateRecurse,
		}
	}

	otParams.SetUUID(params.UUID)
	otParams.SetInfo(flexCache)
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

// VolumeMovementParams is the param struct which is a part of VolumeModifyParams
type VolumeMovementParams struct {
	VolumeMovementDestinationAggregate *VolumeMovementDestinationAggregate
	TieringPolicy                      *string
	State                              *string
}

// VolumeMovementDestinationAggregate is the param struct which is a part of VolumeMovementParams
type VolumeMovementDestinationAggregate struct {
	DestinationAggregateUUID *string
	DestinationAggregateName *string
}

func volumeModifyCloudWriteParamToONTAP(params *VolumeModifyParams) *storage.VolumeModifyParams {
	otParams := storage.NewVolumeModifyParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	info := &models.Volume{}
	info.CloudWriteEnabled = params.TieringPolicy.CloudWriteModeEnabled
	otParams.SetInfo(info)
	return otParams
}

func volumeModifyParamsToONTAP(params *VolumeModifyParams) *storage.VolumeModifyParams {
	otParams := storage.NewVolumeModifyParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	info := &models.Volume{}
	if params.QuotaEnabled != nil {
		info.Quota = &models.VolumeInlineQuota{Enabled: params.QuotaEnabled}
	}
	if params.ReKey != nil {
		info.Encryption = &models.VolumeInlineEncryption{Rekey: params.ReKey}
	}

	useReturnTimeout := true
	if params.SplitInitiated != nil {
		useReturnTimeout = false
		info.Clone = &models.VolumeInlineClone{
			SplitInitiated: params.SplitInitiated,
		}
		if params.MatchParentStorageTier {
			mpst := "true"
			otParams.CloneMatchParentStorageTier = &mpst
		}
	}
	if useReturnTimeout {
		otParams.SetReturnTimeout(&returnTimeout)
	}
	if params.State != nil {
		info.State = params.State
	}
	if params.SnapshotPolicyName != nil && *params.SnapshotPolicyName != "" {
		info.SnapshotPolicy = &models.VolumeInlineSnapshotPolicy{Name: params.SnapshotPolicyName}
	}
	if params.Movement != nil {
		info.Movement = &models.VolumeInlineMovement{
			TieringPolicy: params.Movement.TieringPolicy,
			State:         params.Movement.State,
		}
		if params.Movement.VolumeMovementDestinationAggregate != nil {
			info.Movement.DestinationAggregate = &models.VolumeInlineMovementInlineDestinationAggregate{
				UUID: params.Movement.VolumeMovementDestinationAggregate.DestinationAggregateUUID,
				Name: params.Movement.VolumeMovementDestinationAggregate.DestinationAggregateName,
			}
		}
	}
	if params.Comment != nil {
		info.Comment = params.Comment
	}

	if params.Size != nil || params.LogicalSpaceEnforcement != nil || params.SnapReserve != nil || params.MaxAutoSize != nil {
		info.Space = &models.VolumeInlineSpace{}
		if params.Size != nil {
			info.Space.Size = nillable.ToPointer(utils.ConstrainedCastUint64(*params.Size))
		}
		if params.MaxAutoSize != nil {
			info.Autosize = &models.VolumeInlineAutosize{Maximum: nillable.ToPointer(utils.ConstrainedCastUint64(*params.MaxAutoSize))}
		}
		if params.LogicalSpaceEnforcement != nil {
			info.Space.LogicalSpace = &models.VolumeInlineSpaceInlineLogicalSpace{Enforcement: params.LogicalSpaceEnforcement}
		}
		if params.SnapReserve != nil {
			info.Space.Snapshot = &models.VolumeInlineSpaceInlineSnapshot{ReservePercent: nillable.ToPointer(*params.SnapReserve)}
		}
	}

	if params.SnapshotDirectoryAccessEnabled != nil {
		info.SnapshotDirectoryAccessEnabled = params.SnapshotDirectoryAccessEnabled
	}

	if params.SetAtTimeEnabled != nil {
		info.AccessTimeEnabled = params.SetAtTimeEnabled
	}

	if params.TieringPolicy != nil {
		if params.TieringPolicy.CoolAccessTieringPolicy != "" {
			var minCoolingDays *int64

			if params.TieringPolicy.MinCoolingDays != 0 {
				minCoolingDays = nillable.ToPointer(params.TieringPolicy.MinCoolingDays)
			}
			// skip assigning the cooling days if the policy is none
			if params.TieringPolicy.CoolAccessTieringPolicy == models.VolumeInlineTieringPolicyNone || params.TieringPolicy.CoolAccessTieringPolicy == models.VolumeInlineTieringPolicyAll {
				minCoolingDays = nil
			}

			info.Tiering = &models.VolumeInlineTiering{
				Policy:         &params.TieringPolicy.CoolAccessTieringPolicy,
				MinCoolingDays: minCoolingDays,
			}
		}

		if params.TieringPolicy.CloudRetrievalPolicy != "" {
			info.CloudRetrievalPolicy = &params.TieringPolicy.CloudRetrievalPolicy
		}

		if params.TieringPolicy.CloudWriteModeEnabled != nil {
			info.CloudWriteEnabled = params.TieringPolicy.CloudWriteModeEnabled
		}
	}

	if params.QosPolicy != nil {
		// Pass through QosPolicy as-is. Use "none" to unassign QoS policy per ONTAP API.
		// Empty string will be rejected by ONTAP with an appropriate error.
		info.Qos = &models.VolumeInlineQos{
			Policy: &models.VolumeInlineQosInlinePolicy{
				Name: params.QosPolicy,
			},
		}
	}

	if params.RestoreToSnapshotUUID != nil {
		otParams.SetRestoreToSnapshotUUID(params.RestoreToSnapshotUUID)
	}
	if params.AntiRansomwareState != nil {
		info.AntiRansomware = &models.VolumeInlineAntiRansomware{State: params.AntiRansomwareState}
	}
	if params.EncryptionEnable != nil {
		info.Encryption = &models.VolumeInlineEncryption{Enabled: params.EncryptionEnable}
	}

	if params.ExportPolicy != nil || params.Path != nil || params.UnixPermissions != nil || params.NasUid != nil {
		info.Nas = &models.VolumeInlineNas{}
		if params.ExportPolicy != nil {
			info.Nas.ExportPolicy = &models.VolumeInlineNasInlineExportPolicy{Name: params.ExportPolicy}
		}
		if params.Path != nil {
			info.Nas.Path = params.Path
		}
		if params.UnixPermissions != nil {
			if unixPermissions, err := strconv.Atoi(*params.UnixPermissions); err == nil {
				info.Nas.UnixPermissions = nillable.ToPointer(int64(unixPermissions))
			}
		}
		if params.NasUid != nil {
			info.Nas.UID = params.NasUid
		}
	}

	otParams.SetInfo(info)
	return otParams
}

// VolumeRevertParams is the input param struct for tunnelledStorageClient.VolumeRevert
type VolumeRevertParams struct {
	UUID                  string
	RestoreToSnapshotUUID string
}

// VolumeUnmountParams is the input params struct for tunnelledStorageClient.VolumeUnmount
type VolumeUnmountParams struct {
	UUID string
}

func volumeUnmountParamsToONTAP(params *VolumeUnmountParams) *storage.VolumeModifyParams {
	otParams := storage.NewVolumeModifyParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	otParams.SetInfo(&models.Volume{Nas: &models.VolumeInlineNas{Path: nillable.GetStringPtr("")}})
	return otParams
}

// volumeMountParamsToONTAP converts VolumeMountParams to ONTAP VolumeModifyParams for mounting
func volumeMountParamsToONTAP(params *VolumeMountParams) *storage.VolumeModifyParams {
	otParams := storage.NewVolumeModifyParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	otParams.SetInfo(&models.Volume{Nas: &models.VolumeInlineNas{Path: &params.JunctionPath}})
	return otParams
}

// VolumeMountParams is the input params struct for tunnelledStorageClient.VolumeMount
type VolumeMountParams struct {
	UUID         string
	JunctionPath string
}

// SnapshotCollectionGetParams is the input param struct for storageClient.SnapshotCollectionGet
type SnapshotCollectionGetParams struct {
	BaseParams
	VolumeUUID      string
	SvmName         string
	UUID            *string
	Name            *string
	SnapmirrorLabel *string
}

func snapshotCollectionGetParamsToONTAP(params *SnapshotCollectionGetParams) *storage.SnapshotCollectionGetParams {
	otParams := storage.NewSnapshotCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetFields(params.Fields)
	otParams.SetUUID(params.UUID)
	otParams.SetMaxRecords(nillable.ToStringPtr(params.MaxRecords))
	otParams.SetSnapmirrorLabel(params.SnapmirrorLabel)
	otParams.SetVolumeUUID(params.VolumeUUID)
	if params.Name != nil {
		otParams.SetName(params.Name)
	}
	return otParams
}

// Snapshot is a simple wrapper of models.Snapshot
type Snapshot struct {
	models.Snapshot
}

// SnapshotCreateParams is the input param struct for storageClient.SnapshotCreate
type SnapshotCreateParams struct {
	VolumeUUID string
	Name       string
	Comment    *string
}

// SnapshotPolicyGetParams is the input param struct for storageClient.SnapshotPolicyGet
type SnapshotPolicyGetParams struct {
	BaseParams
	UUID string
}

func snapshotPolicyGetParamsToONTAP(params *SnapshotPolicyGetParams) *storage.SnapshotPolicyGetParams {
	otParams := storage.NewSnapshotPolicyGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetFields(params.Fields)
	otParams.SetUUID(params.UUID)
	return otParams
}

// SnapshotPolicyDeleteParams is the input param struct for storageClient.SnapshotPolicyDelete
type SnapshotPolicyDeleteParams struct {
	BaseParams
	Name string
}

// snapshotPolicyDeleteParamsToONTAPCollectionDelete converts SnapshotPolicyDeleteParams to ONTAP storage.SnapshotPolicyDeleteCollectionParams
func snapshotPolicyDeleteParamsToONTAPCollectionDelete(params *SnapshotPolicyDeleteParams) *storage.SnapshotPolicyDeleteCollectionParams {
	otParams := storage.NewSnapshotPolicyDeleteCollectionParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(&params.Name)
	return otParams
}

// SnapshotPolicy is a simple wrapper of models.SnapshotPolicy
type SnapshotPolicy struct {
	models.SnapshotPolicy
}

// SnapshotPolicySchedule describes the schedules in SnapshotPolicyCreateParams
type SnapshotPolicySchedule struct {
	Prefix          string
	Count           int64
	SnapmirrorLabel string
	Name            string
	Months          []int
	DaysOfMonth     []int
	DaysOfWeek      []int
	Hours           []int
	Minutes         []int
}

// SnapshotPolicyCreateParams is the input param struct SnapshotPolicyCreate
type SnapshotPolicyCreateParams struct {
	BaseParams
	Name      *string
	Comment   *string
	Enabled   *bool
	Schedules []*SnapshotPolicySchedule
}

// SnapshotPolicyModifyParams is the input param struct SnapshotPolicyModify
type SnapshotPolicyModifyParams struct {
	BaseParams
	UUID    string
	Comment *string
	Enabled *bool
}

// convertSnapshotPolicyModifyParamsToOntap converts SnapshotPolicyCreateParams to ONTAP storage.SnapshotPolicyCreateParams
func convertSnapshotPolicyModifyParamsToOntap(params *SnapshotPolicyModifyParams) *storage.SnapshotPolicyModifyParams {
	otParams := storage.NewSnapshotPolicyModifyParams()
	if params == nil {
		return otParams
	}

	otParams.UUID = params.UUID
	otParams.Info = &models.SnapshotPolicy{
		Comment: params.Comment,
		Enabled: params.Enabled,
	}
	return otParams
}

// SnapshotPolicyScheduleCreateParams is the input param struct SnapshotPolicyScheduleCreate
type SnapshotPolicyScheduleCreateParams struct {
	SnapshotPolicyUUID string
	ScheduleName       string
	Count              int64
	SnapmirrorLabel    string
}

// convertSnapshotPolicyScheduleCreateParamsToONTAP converts SnapshotPolicyScheduleCreateParams to ONTAP storage.SnapshotPolicyScheduleCreateParams
func convertSnapshotPolicyScheduleCreateParamsToONTAP(params *SnapshotPolicyScheduleCreateParams) *storage.SnapshotPolicyScheduleCreateParams {
	otParams := storage.NewSnapshotPolicyScheduleCreateParams()
	if params == nil {
		return otParams
	}

	otParams.SnapshotPolicyUUID = params.SnapshotPolicyUUID
	count := params.Count
	otParams.Info = &models.SnapshotPolicySchedule{
		Count:           &count,
		SnapmirrorLabel: &params.SnapmirrorLabel,
		Prefix:          &params.SnapmirrorLabel,
		Schedule:        &models.SnapshotPolicyScheduleInlineSchedule{Name: &params.ScheduleName},
	}
	return otParams
}

// SnapshotPolicyScheduleModifyParams is the input param struct SnapshotPolicyScheduleModify
type SnapshotPolicyScheduleModifyParams struct {
	ScheduleUUID       string
	SnapshotPolicyUUID string
	Count              int
	SnapmirrorLabel    string
}

// convertSnapshotPolicyScheduleModifyParamsToONTAP converts SnapshotPolicyScheduleCreateParams to ONTAP storage.SnapshotPolicyScheduleCreateParams
func convertSnapshotPolicyScheduleModifyParamsToONTAP(params *SnapshotPolicyScheduleModifyParams) *storage.SnapshotPolicyScheduleModifyParams {
	otParams := storage.NewSnapshotPolicyScheduleModifyParams()
	if params == nil {
		return otParams
	}

	otParams.SnapshotPolicyUUID = params.SnapshotPolicyUUID
	otParams.ScheduleUUID = params.ScheduleUUID

	count := int64(params.Count)
	otParams.Info = &models.SnapshotPolicySchedule{
		Count:           &count,
		SnapmirrorLabel: &params.SnapmirrorLabel,
	}
	return otParams
}

// SnapshotPolicyScheduleDeleteParams is the input param struct SnapshotPolicyScheduleDelete
type SnapshotPolicyScheduleDeleteParams struct {
	ScheduleUUID       string
	SnapshotPolicyUUID string
}

// convertSnapshotPolicyScheduleDeleteParamsToONTAP converts SnapshotPolicyScheduleDeleteParams to ONTAP storage.SnapshotPolicyScheduleDeleteParams
func convertSnapshotPolicyScheduleDeleteParamsToONTAP(params *SnapshotPolicyScheduleDeleteParams) *storage.SnapshotPolicyScheduleDeleteParams {
	otParams := storage.NewSnapshotPolicyScheduleDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.ScheduleUUID = params.ScheduleUUID
	otParams.SnapshotPolicyUUID = params.SnapshotPolicyUUID
	return otParams
}

type SnapshotPolicyFindParams struct {
	Name   string
	Fields []string
}

// ScheduleCreateParams is the input param struct ScheduleCreate
type ScheduleCreateParams struct {
	Name        string
	Months      []int
	DaysOfMonth []int
	DaysOfWeek  []int
	Hours       []int
	Minutes     []int
}

// VolumeCollectionGetParams is the input param struct for storageClient.VolumeCollectionGet
type VolumeCollectionGetParams struct {
	BaseParams
	UUID    *string
	Name    *string
	SvmName *string
}

func volumeCollectionGetParamsToONTAP(params *VolumeCollectionGetParams) *storage.VolumeCollectionGetParams {
	otParams := storage.NewVolumeCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetFields(params.Fields)
	otParams.SetMaxRecords(nillable.ToStringPtr(params.MaxRecords))
	otParams.SetUUID(params.UUID)
	otParams.SetName(params.Name)
	otParams.SetSvmName(params.SvmName)
	return otParams
}

// Volume is a simple wrapper of models.Volume
type Volume struct {
	models.Volume
}

// Flexcache is a simple wrapper of models.Flexcache
type Flexcache struct {
	models.Flexcache
}

// VolumeGetParams is the input param struct for storageClient.VolumeGet
type VolumeGetParams struct {
	BaseParams
	UUID                           string
	Name                           string
	SvmName                        *string
	SnapReserve                    *int64
	SnapshotDirectoryAccessEnabled *bool
}

func volumeGetParamsToONTAP(params *VolumeGetParams) *storage.VolumeGetParams {
	otParams := storage.NewVolumeGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetFields(params.Fields)
	otParams.SetUUID(params.UUID)
	return otParams
}

// VolumeGetParamsToONTAPQuotaRules converts VolumeGetParams to storage.VolumeGetParams with context support
// This is used specifically for quota rule operations that require context.
func VolumeGetParamsToONTAPQuotaRules(ctx context.Context, params *VolumeGetParams) *storage.VolumeGetParams {
	otParams := storage.NewVolumeGetParams().WithContext(ctx)
	if params == nil {
		return otParams
	}

	otParams.SetFields(params.Fields)
	otParams.SetUUID(params.UUID)
	return otParams
}

// SnapshotModifyParams is the input param struct for storageClient.SnapshotModify
type SnapshotModifyParams struct {
	UUID       string
	VolumeUUID string
	Name       string
}

// SnapshotGetParams is the input param struct for storageClient.SnapshotGet
type SnapshotGetParams struct {
	BaseParams
	UUID       string
	Name       string
	VolumeUUID string
}

// RevertVolumeParams describes parameters supplied to Provider.RevertVolume
type RevertVolumeParams struct {
	VolumeID        string
	SnapshotID      string
	SnapshotName    string
	SvmName         string
	PreRevertVolume *Volume
}

// SnapshotDeleteParams is the input param struct for storageClient.SnapshotDelete
type SnapshotDeleteParams struct {
	UUID       string
	VolumeUUID string
}

// Svm is a simple wrapper of models.Svm
type Svm struct {
	models.Svm
}

// SvmGetParams is the input params struct for svm_client.SvmGet
type SvmGetParams struct {
	BaseParams
	SvmName string
}

func svmGetParamsToONTAP(params *SvmGetParams) *svm.SvmCollectionGetParams {
	otParams := svm.NewSvmCollectionGetParams()
	otParams.SetName(&params.SvmName)
	otParams.SetFields(params.Fields)
	return otParams
}

// SvmGetCollectionParams is the input params struct for svm_client.SvmCollectionGet
type SvmGetCollectionParams struct {
	BaseParams
	SvmName     *string
	IpspaceName *string
}

func svmGetCollectionParamsToONTAP(params *SvmGetCollectionParams) *svm.SvmCollectionGetParams {
	otParams := svm.NewSvmCollectionGetParams()
	if params == nil {
		return otParams
	}
	otParams.SetIpspaceName(params.IpspaceName)
	otParams.SetName(params.SvmName)
	otParams.SetFields(params.Fields)
	return otParams
}

// SvmPeer represents an svm peer
type SvmPeer struct {
	models.SvmPeer
}

// NsSwitchSource contains slice of nsSwitchSource db values
type NsSwitchSource struct {
	NsSwitchSourceGroup    []*models.NsswitchSource
	NsSwitchSourcePasswd   []*models.NsswitchSource
	NsSwitchSourceNetgroup []*models.NsswitchSource
	NsSwitchSourceNamemap  []*models.NsswitchSource
}

// SvmModifyParams is the input params struct for svm_client.SvmModify
type SvmModifyParams struct {
	BaseParams
	SvmUUID              string
	NsSwitch             *NsSwitchSource
	NfsAllowed           *bool
	CifsAllowed          *bool
	IscsiAllowed         *bool
	RetentionPeriodHours *int64
	QoSPolicyName        *string
}

func svmModifyParamsToOntap(params *SvmModifyParams) *svm.SvmModifyParams {
	otParams := svm.NewSvmModifyParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.SvmUUID)
	svmParam := &models.Svm{
		RetentionPeriod: params.RetentionPeriodHours,
	}

	if params.NsSwitch != nil {
		nsSwitchParams := &models.SvmInlineNsswitch{}
		if params.NsSwitch.NsSwitchSourceGroup != nil {
			nsSwitchParams.Group = params.NsSwitch.NsSwitchSourceGroup
		}
		if params.NsSwitch.NsSwitchSourcePasswd != nil {
			nsSwitchParams.Passwd = params.NsSwitch.NsSwitchSourcePasswd
		}
		if params.NsSwitch.NsSwitchSourceNamemap != nil {
			nsSwitchParams.Namemap = params.NsSwitch.NsSwitchSourceNamemap
		}
		if params.NsSwitch.NsSwitchSourceNetgroup != nil {
			nsSwitchParams.Netgroup = params.NsSwitch.NsSwitchSourceNetgroup
		}
		svmParam.Nsswitch = nsSwitchParams
	}
	if params.CifsAllowed != nil {
		svmParam.Cifs = &models.SvmInlineCifs{Allowed: params.CifsAllowed}
	}
	if params.NfsAllowed != nil {
		svmParam.Nfs = &models.SvmInlineNfs{Allowed: params.NfsAllowed}
	}
	if params.QoSPolicyName != nil {
		svmParam.QosPolicy = &models.SvmInlineQosPolicy{Name: params.QoSPolicyName}
	}

	otParams.SetInfo(svmParam)
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

// ClusterPeerCreateParams is the input parameter for cluster peer create
type ClusterPeerCreateParams struct {
	Name               string
	IPAddresses        []string
	GeneratePassphrase bool
	IPSpace            string
	ExpiryTime         *string
	Passphrase         *string
	LocalRole          *string
}

// ClusterPeer is a simple wrapper of models.ClusterPeer
type ClusterPeer struct {
	models.ClusterPeer
}

// ClusterPeerResponse will represent the response from ListClusterPeer endpoint
type ClusterPeerResponse struct {
	IPAddresses         []string
	PeerClusterName     string
	AuthenticationState string
	Availability        string
	UUID                string
	ExpiryTime          string
}

// ClusterPeerCreateResponse will represent the response from cluster peer creation
type ClusterPeerCreateResponse struct {
	GeneratedPassphrase *string
	ClusterPeerUUID     string
	ExpiryTime          *strfmt.DateTime
}

// VolumeDeleteParams describes the params to invoke volume Delete
type VolumeDeleteParams struct {
	UUID string
	Name string
}

func volumeDeleteParamsToONTAP(params *VolumeDeleteParams) *storage.VolumeDeleteParams {
	otParams := storage.NewVolumeDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	force := "true"
	otParams.SetForce(&force)
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

func volumeDeleteParamsToONTAPCollectionDelete(params *VolumeDeleteParams) *storage.VolumeDeleteCollectionParams {
	otParams := storage.NewVolumeDeleteCollectionParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(&params.Name)
	otParams.SetForce(nillable.ToPointer("true"))
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

// ServerRootCACertificate is a simple wrapper of models.SecurityCertificate
type ServerRootCACertificate struct {
	models.SecurityCertificate
}

// ServerRootCAGetParams is the input param struct for securityClient.SecurityCertificateCollectionGet
type ServerRootCAGetParams struct {
	BaseParams
	Name            *string
	CertificateType *string
	SvmName         *string
}

// ServerRootCAGetCollectionParams is the input param struct for securityClient.SecurityCertificateCollectionGet
type ServerRootCAGetCollectionParams struct {
	BaseParams
	CertificateType *string
	SvmName         *string
	Name            *string
}

// ServerRootCAGenerateParams is the input param struct for securityClient.ServerRootCAGenerateParams
type ServerRootCAGenerateParams struct {
	BaseParams
	SvmName         *string
	CertificateType *string
	CommonName      *string
	Name            *string
	KeySize         *int64
}

// ServerRootCAInstallParams is the input param struct for securityClient.ServerRootCAInstallParams
type ServerRootCAInstallParams struct {
	BaseParams
	PrivateKey      *string
	Certificate     *string
	SvmName         *string
	CertificateType *string
	CommonName      *string
	Name            *string
}

// ServerRootCADeleteParams is the input param struct for securityClient.ServerRootCADeleteParams
type ServerRootCADeleteParams struct {
	UUID                 *string
	SvmName              *string
	SerialNumber         *string
	CommonName           *string
	CertificateAuthority *string
}

// SnapmirrorRelationshipCreateParams describes the params to invoke snapmirror relationship create
type SnapmirrorRelationshipCreateParams struct {
	DestinationPath string
	SourcePath      string
	Policy          string
	Schedule        *string
	AccessToken     *string
	SourceUUID      *string
	IsRestore       bool
}

// SnapmirrorRelationshipDeleteParams describes the params to invoke snapmirror relationship delete
type SnapmirrorRelationshipDeleteParams struct {
	DestinationOnly *bool
	SourceOnly      *bool
	UUID            string
}

// SnapmirrorRelationshipReleaseParams describes the params to invoke snapmirror relationship release
type SnapmirrorRelationshipReleaseParams struct {
	SourceInfoOnly *bool
	UUID           string
}

// SnapmirrorRelationshipModifyParams represents snapmirror relationship modify parameters
type SnapmirrorRelationshipModifyParams struct {
	UUID             string
	TransferSchedule *string
	State            *string
	Source           *models.SnapmirrorSourceEndpoint
	Destination      *models.SnapmirrorEndpoint
}

// SnapmirrorRelationshipModifyParams represents snapmirror relationship modify parameters
type SnapmirrorRelationshipTransferModifyParams struct {
	UUID         string
	TransferUUID string
	State        *string
}

// SnapmirrorRelationshipReverseParams represents snapmirror relationship reverse parameters
type SnapmirrorRelationshipReverseParams struct {
	UUID            string
	SourcePath      string
	DestinationPath string
}

// SnapmirrorRelationship represents a snapmirror relationship object
type SnapmirrorRelationship struct {
	models.SnapmirrorRelationship
}

// SnapmirrorRelationshipListParams represents snapmirror relationship list parameters
type SnapmirrorRelationshipListParams struct {
	DestinationPath string
	SourcePath      string
}

// SnapmirrorRelationshipListDestinationsParams represents snapmirror relationship list destination parameters
type SnapmirrorRelationshipListDestinationsParams struct {
	DestinationPath    *string
	SourcePath         *string
	DestinationSVMName *string
	SourceSVMName      *string
}

// SnapmirrorRelationshipGetParams represents snapmirror relationship get parameters
type SnapmirrorRelationshipGetParams struct {
	UUID            string
	DestinationPath *string
	SourcePath      *string
}

// SnapmirrorPolicyDeleteCollectionParams is the input param struct for storageClient.
type SnapmirrorPolicyDeleteCollectionParams struct {
	BaseParams
	Name    string
	SvmUUID string
}

// SnapmirrorRelationshipTransferCreateParams describes the params to invoke snapmirror relationship resync
type SnapmirrorRelationshipTransferCreateParams struct {
	UUID             string
	SnapshotName     string
	AccessToken      *string
	Files            []*commonparams.SnapmirrorTransferFile
	CleanUpOnFailure bool
}

// SnapmirrorRelationshipTransferGetParams describes the params to invoke snapmirror relationship transfer get
type SnapmirrorRelationshipTransferGetParams struct {
	BaseParams
	SnapmirrorUUID string
	SnapshotName   string
}

// SnapmirrorCloudEndpointDeleteParams describes the params to invoke Snapmirror Cloud Endpoint Delete
type SnapmirrorCloudEndpointDeleteParams struct {
	ObjectStoreUUID string
	EndpointUUID    string
}

// SnapmirrorCloudSnapshotDeleteParams describes the params to invoke Snapmirror Cloud Snapshot Delete
type SnapmirrorCloudSnapshotDeleteParams struct {
	ObjectStoreUUID string
	EndpointUUID    string
	SnapshotUUID    string
}

// NetworkIPDefaultRouteCreateParams describes the params to invoke Network Route Creation
type NetworkIPDefaultRouteCreateParams struct {
	IPSpace string
	SvmName string
	Gateway string
	Timeout *time.Duration
}

func networkIPRouteCreateParamsToONTAP(params *NetworkIPDefaultRouteCreateParams) *networking.NetworkIPRoutesCreateParams {
	otParams := networking.NewNetworkIPRoutesCreateParams()

	info := &models.NetworkRoute{
		Destination: &models.IPInfo{
			Address: nillable.ToPointer(models.IPAddress("0.0.0.0")),
			Netmask: nillable.ToPointer(models.IPNetmask("0")),
		},
		Gateway: nillable.ToPointer(params.Gateway),
		Metric:  nillable.ToPointer(int64(20)),
	}

	if params.IPSpace != "" {
		info.Ipspace = &models.NetworkRouteInlineIpspace{
			Name: nillable.ToPointer(params.IPSpace),
		}
	}

	if params.SvmName != "" {
		info.Svm = &models.NetworkRouteInlineSvm{
			Name: nillable.ToPointer(params.SvmName),
		}
	}

	otParams.SetInfo(info)
	if params.Timeout != nil {
		otParams.SetTimeout(*params.Timeout)
	}
	return otParams
}

// NetworkEthernetBroadcastDomainsGetParams describes the params to invoke network ethernet port get
type NetworkEthernetBroadcastDomainsGetParams struct {
	BaseParams
	Name    string
	IPSpace string
}

func networkEthernetBroadcastDomainsGetParamsToONTAP(params *NetworkEthernetBroadcastDomainsGetParams) *networking.NetworkEthernetBroadcastDomainsGetParams {
	otParams := networking.NewNetworkEthernetBroadcastDomainsGetParams()
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	otParams.SetFields(params.Fields)
	if params.IPSpace != "" {
		otParams.SetIpspaceName(&params.IPSpace)
	}
	if params.Name != "" {
		otParams.SetName(&params.Name)
	}
	return otParams
}

// BroadcastDomain is a simple wrapper of models.BroadcastDomain
type BroadcastDomain struct {
	models.BroadcastDomain
}

// NetworkEthernetBroadcastDomainCreateParams is the params for NetworkEthernetBroadcastDomainCreate call
type NetworkEthernetBroadcastDomainCreateParams struct {
	Name    string
	IPSpace string
}

// NetworkEthernetBroadcastDomainDeleteParams describes the params to invoke network ethernet Broadcast Domain delete
type NetworkEthernetBroadcastDomainDeleteParams struct {
	Name string
}

func networkEthernetBroadcastDomainDeleteParamsToONTAP(params *NetworkEthernetBroadcastDomainDeleteParams) *networking.NetworkEthernetBroadcastDomainDeleteCollectionParams {
	otParams := networking.NewNetworkEthernetBroadcastDomainDeleteCollectionParams()
	otParams.WithName(&params.Name)
	return otParams
}

// IpspaceDeleteParams describes the params to invoke ipspace delete
type IpspaceDeleteParams struct {
	Name string
}

func ipspaceDeleteParamsToONTAP(params *IpspaceDeleteParams) *networking.IpspaceDeleteCollectionParams {
	otParams := networking.NewIpspaceDeleteCollectionParams()
	otParams.WithName(&params.Name)
	return otParams
}

// Role is a simple wrapper of models.Role
type Role struct {
	models.Role
}

// RolePrivilege describes the privilege level of a Role
type RolePrivilege struct {
	Path   string
	Access string
	Query  string
}

// RoleGetParams is the input param struct for securityClient.RoleGet
type RoleGetParams struct {
	BaseParams
	Name      string
	OwnerUUID *string
}

// RoleCollectionGetParams is the input param struct for securityClient.RoleCollectionGet
type RoleCollectionGetParams struct {
	BaseParams
	Name *string
}

// RoleCollectionGetResponse is the response struct for securityClient.RoleCollectionGet
type RoleCollectionGetResponse struct {
	*security.RoleCollectionGetOK
}

// RoleCreateParams is the input param struct for securityClient.RoleCreate
type RoleCreateParams struct {
	Name       string
	Privileges []*RolePrivilege
}

// RolePrivilegeModifyParams is the input param struct for securityClient.RoleModify
type RolePrivilegeModifyParams struct {
	OwnerID string
	Name    string
	Access  string
	Query   string
	Path    string
}

// RolePrivilegeCreateParams is the input param struct for securityClient.RolePrivilegeCreate
type RolePrivilegeCreateParams struct {
	OwnerID string
	Name    string
	Path    string
	Access  string
	Query   string
}

// RolePrivilegeDeleteParams is the input param struct for securityClient.RolePrivilegeDelete
type RolePrivilegeDeleteParams struct {
	OwnerID string
	Name    string
	Path    string
}

// RoleDeleteParams is the input param struct for securityClient.RoleDelete
type RoleDeleteParams struct {
	Name      string
	OwnerUUID *string
}

// QosPolicy is the model for QosPolicy
type QosPolicy struct {
	models.QosPolicy
}

// QosPolicyDeleteCollectionParams is the input params for storageClient.QosPolicyDeleteCollection
type QosPolicyDeleteCollectionParams struct {
	UUID    string // UUID of the QoS policy group to delete (preferred)
	Name    string // Name of the QoS policy group to delete (alternative to UUID)
	SvmName string // SVM name (optional, passed through for backwards compatibility)
}

// Conversion function for QosPolicyDeleteCollectionParams to ONTAP SDK params
func qosPolicyDeleteCollectionParamsToONTAP(params *QosPolicyDeleteCollectionParams) *storage.QosPolicyDeleteCollectionParams {
	otParams := storage.NewQosPolicyDeleteCollectionParams()
	if params == nil {
		return otParams
	}
	if params.Name != "" {
		otParams.SetName(&params.Name)
	}
	if params.SvmName != "" {
		otParams.SetSvmName(&params.SvmName)
	}
	if params.UUID != "" {
		otParams.SetUUID(&params.UUID)
	}
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

// QosPolicyCreateParams is the input params for storageClient.QosPolicyCreate
type QosPolicyCreateParams struct {
	CapacityShared    *bool
	MaxThroughputMbps *int64
	MinThroughputMbps *int64
	Name              *string
	SvmUUID           *string
}

// QosPolicyGroupCollectionGetParams is the input params for storageClient.QosPolicyGroupCollectionGet
type QosPolicyGroupCollectionGetParams struct {
	BaseParams
	Name string
}

func qosPolicyGroupCollectionGetParamsToONTAPCollectionGet(params *QosPolicyGroupCollectionGetParams) *storage.QosPolicyCollectionGetParams {
	otParams := storage.NewQosPolicyCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetFields(params.Fields)
	otParams.SetName(&params.Name)
	return otParams
}

// QoSPolicyGroupFindParams is the input param struct for StorageClient.QoSPolicyGroupFind
// Used for finding an existing QoS policy group by UUID or by name
type QoSPolicyGroupFindParams struct {
	UUID    string // UUID of the QoS policy group to find (preferred)
	Name    string // Name of the QoS policy group to find (alternative to UUID)
	SvmName string // SVM name to filter by (optional, passed through for backwards compatibility)
}

// QoSPolicyGroupUpdateParams is the input param struct for StorageClient.QoSPolicyGroupUpdate
// Used for updating an existing QoS policy group with new throughput and IOPS values
type QoSPolicyGroupUpdateParams struct {
	UUID          string // UUID of the QoS policy group to update
	Name          string // Name of the QoS policy group
	SvmName       string // SVM name
	MaxThroughput int64  // New throughput in MiBps
	MaxIOPS       int64  // New max IOPS
}

// QoSPolicyGroupCreateParams is the input param struct for StorageClient.QoSPolicyGroupCreate
// Used for creating a shared QoS policy group on ONTAP
// Throughput in MiBps, IOPS as input, applied to a specific SVM
// Not for adaptive QoS
type QoSPolicyGroupCreateParams struct {
	Name          string // Name of the QoS policy group
	SvmName       string // SVM to apply the policy on
	MaxThroughput int64  // Throughput in MiBps
	MaxIOPS       int64  // Max IOPS
	IsShared      *bool  // Whether volumes share QoS budget (capacity-shared). If nil, ONTAP defaults to false
}

// Conversion function for QoSPolicyGroupCreateParams to ONTAP SDK params
func qosPolicyGroupCreateParamsToONTAP(params *QoSPolicyGroupCreateParams) *storage.QosPolicyCreateParams {
	otParams := storage.NewQosPolicyCreateParams()
	if params == nil {
		return otParams
	}
	// Pass IsShared through to ONTAP (nil means ONTAP will default to false)
	info := &models.QosPolicy{
		Name: &params.Name,
		Svm: &models.QosPolicyInlineSvm{
			Name: &params.SvmName,
		},
		Fixed: &models.QosPolicyInlineFixed{
			MaxThroughputMbps: &params.MaxThroughput,
			MaxThroughputIops: &params.MaxIOPS,
			CapacityShared:    params.IsShared,
		},
	}
	otParams.Info = info
	otParams.SetReturnRecords(nillable.ToPointer("true"))
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

// QuotaReportResponse is a simple wrapper of storage.QuotaReportCollectionGetOK
type QuotaReportResponse struct {
	storage.QuotaReportCollectionGetOK
}

// SvmCreateParams is the params to create a svm
type SvmCreateParams struct {
	Name      string
	IPSpace   string
	Protocols Protocols
}

type Protocols struct {
	EnableIscsi bool
}

// RestoreFromSnapshotParams contains parameters for restoring a volume from a snapshot
type RestoreFromSnapshotParams struct {
	ParentVolumeExternalUUID string // External UUID of the source/parent volume
	ParentVolumeName         string // Name of the Volume
	SnapshotUUID             string // UUID of the snapshot to restore from
	SnapshotName             string // Name of the snapshot to restore from
	ParentVolumeSvmName      string // Name of the SVM where the parent volume resides
	// Add more fields as needed
}

// VolumeCreateParams is the params to create a volume
type VolumeCreateParams struct {
	Aggregates                     []string
	ConstituentsPerAggregate       *int64
	Name                           string
	Comment                        string
	Type                           string
	Size                           int64
	QosPolicy                      string
	SnapshotPolicy                 string
	ExportPolicy                   *string
	SecurityStyle                  string
	SnapshotReservePercent         int64
	JunctionPath                   *string
	SnapshotDirectoryAccessEnabled bool
	Encrypt                        bool
	UnixPermissions                *string
	Language                       *string
	Svm                            string
	Style                          *string
	RestoreFromSnapshot            *RestoreFromSnapshotParams
	TieringPolicy                  *TieringPolicy
	TieringSupported               *bool
	GranularData                   *bool   // Enable granular data on the volume (auto-set for FlexGroup)
	GranularDataMode               *string // Mode of granular data: "disabled", "basic", "advanced" (auto-set for FlexGroup)
}

type FlexCacheVolumeCreateParams struct {
	Name                     string
	SvmName                  string
	Size                     int64
	Aggregates               []string
	OriginSvmName            string
	OriginVolumeName         string
	Path                     *string
	AtimeScrubEnabled        *bool
	AtimeScrubPeriod         *int16
	CifsChangeNotifyEnabled  *bool
	GlobalFileLockingEnabled *bool
	Prepopulate              *PrepopulateConfig
	WritebackEnabled         *bool
}

type FlexCacheVolumeDeleteParams struct {
	UUID string
	Name string
}

func flexCacheVolumeDeleteParamsToONTAP(params *FlexCacheVolumeDeleteParams) *storage.FlexcacheDeleteParams {
	otParams := storage.NewFlexcacheDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

func flexCacheVolumeDeleteParamsToONTAPCollectionDelete(params *FlexCacheVolumeDeleteParams) *storage.FlexcacheDeleteCollectionParams {
	otParams := storage.NewFlexcacheDeleteCollectionParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(&params.Name)
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

type PrepopulateConfig struct {
	DirPaths        []*string
	ExcludeDirPaths []*string
	Recurse         *bool
}

// TieringPolicy describes the auto tiering policy for a volume
type TieringPolicy struct {
	CoolAccessTieringPolicy string
	MinCoolingDays          int64
	CloudRetrievalPolicy    string
	CloudWriteModeEnabled   *bool // Only supported for file volumes
}

const (
	VolumeStateOnline        = "online"
	GuaranteeTypeNone        = "none"
	GranularDataModeAdvanced = "advanced"
	VolumeStyleFlexGroup     = "flexgroup"
)

func volumeCreateFromSnapshotParamsToONTAP(params *VolumeCreateParams) *storage.VolumeCreateParams {
	otParams := storage.NewVolumeCreateParams()

	otParams.SetInfo(&models.Volume{
		Name: &params.Name,
		Type: &params.Type,
		Svm: &models.VolumeInlineSvm{
			Name: &params.Svm,
		},
		Guarantee: &models.VolumeInlineGuarantee{
			Type: nillable.ToPointer(GuaranteeTypeNone),
		},
		Space: &models.VolumeInlineSpace{
			Snapshot: &models.VolumeInlineSpaceInlineSnapshot{
				ReservePercent: &params.SnapshotReservePercent,
			},
			LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
				Enforcement: nillable.ToPointer(true),
				Reporting:   nillable.ToPointer(true),
			},
		},
		Autosize: &models.VolumeInlineAutosize{
			Mode: nillable.ToPointer("off"),
		},
		Clone: &models.VolumeInlineClone{
			ParentSnapshot: &models.SnapshotReference{
				Name: &params.RestoreFromSnapshot.SnapshotName,
			},
			ParentSvm: &models.VolumeInlineCloneInlineParentSvm{
				Name: &params.RestoreFromSnapshot.ParentVolumeSvmName,
			},
			ParentVolume: &models.VolumeInlineCloneInlineParentVolume{
				Name: &params.RestoreFromSnapshot.ParentVolumeName,
			},
			IsFlexclone: nillable.ToPointer(true),
		},
	})

	otParams.SetReturnTimeout(&returnTimeout)
	otParams.SetReturnRecords(nillable.ToPointer("true"))

	return otParams
}

func volumeCreateParamsToONTAP(params *VolumeCreateParams) *storage.VolumeCreateParams {
	if params.RestoreFromSnapshot != nil {
		return volumeCreateFromSnapshotParamsToONTAP(params)
	}

	otParams := storage.NewVolumeCreateParams()
	vol := &models.Volume{
		Name:  &params.Name,
		Type:  &params.Type,
		State: nillable.ToPointer(VolumeStateOnline),
		Size:  &params.Size,
		Style: params.Style,
		Svm: &models.VolumeInlineSvm{
			Name: &params.Svm,
		},
		Nas: &models.VolumeInlineNas{
			ExportPolicy: &models.VolumeInlineNasInlineExportPolicy{
				Name: params.ExportPolicy,
			},
			Path: params.JunctionPath,
		},
		Guarantee: &models.VolumeInlineGuarantee{
			Type: nillable.ToPointer(GuaranteeTypeNone),
		},
		Space: &models.VolumeInlineSpace{
			Snapshot: &models.VolumeInlineSpaceInlineSnapshot{
				ReservePercent: &params.SnapshotReservePercent,
			},
			LogicalSpace: &models.VolumeInlineSpaceInlineLogicalSpace{
				Enforcement: nillable.ToPointer(true),
				Reporting:   nillable.ToPointer(true),
			},
		},
		Autosize: &models.VolumeInlineAutosize{
			Mode: nillable.ToPointer("off"),
		},
		SnapshotPolicy: &models.VolumeInlineSnapshotPolicy{
			Name: &params.SnapshotPolicy,
		},
		ConstituentsPerAggregate: params.ConstituentsPerAggregate,
	}
	if params.SecurityStyle != "" {
		normalized := strings.ToLower(params.SecurityStyle)
		vol.Nas.SecurityStyle = &normalized
	}
	if params.UnixPermissions != nil {
		if unixPermissions, err := strconv.Atoi(*params.UnixPermissions); err == nil {
			vol.Nas.UnixPermissions = nillable.ToPointer(int64(unixPermissions))
		}
	}
	otParams.SetInfo(vol)

	otParams.Info.SnapshotDirectoryAccessEnabled = &params.SnapshotDirectoryAccessEnabled

	for _, aggregate := range params.Aggregates {
		otParams.Info.VolumeInlineAggregates = append(otParams.Info.VolumeInlineAggregates,
			&models.VolumeInlineAggregatesInlineArrayItem{
				Name: nillable.ToPointer(aggregate),
			})
	}

	otParams.SetReturnTimeout(&returnTimeout)
	otParams.SetReturnRecords(nillable.ToPointer("true"))

	if params.TieringPolicy != nil {
		otParams.Info.Tiering = &models.VolumeInlineTiering{
			Policy:         nillable.ToPointer(params.TieringPolicy.CoolAccessTieringPolicy),
			MinCoolingDays: nil,
			Supported:      params.TieringSupported,
		}
		if params.TieringPolicy.CoolAccessTieringPolicy == models.VolumeInlineTieringPolicyAuto || params.TieringPolicy.CoolAccessTieringPolicy == models.VolumeInlineTieringPolicySnapshotOnly {
			otParams.Info.Tiering.MinCoolingDays = &params.TieringPolicy.MinCoolingDays
			otParams.Info.CloudRetrievalPolicy = &params.TieringPolicy.CloudRetrievalPolicy
		}

		if params.TieringPolicy.CloudWriteModeEnabled != nil {
			otParams.Info.CloudWriteEnabled = params.TieringPolicy.CloudWriteModeEnabled
		}
	}

	// Set the tiering supported flag only for the case when auto provisioning flex-group volumes
	if params.TieringSupported != nil && otParams.Info.Tiering == nil {
		otParams.Info.Tiering = &models.VolumeInlineTiering{
			Supported: params.TieringSupported,
		}
	}

	// Set QoS policy if provided
	if params.QosPolicy != "" {
		otParams.Info.Qos = &models.VolumeInlineQos{
			Policy: &models.VolumeInlineQosInlinePolicy{
				Name: &params.QosPolicy,
			},
		}
	}

	// Set granular data to advanced mode automatically for FlexGroup volumes
	if params.Style != nil && *params.Style == VolumeStyleFlexGroup {
		otParams.Info.GranularData = nillable.ToPointer(true)
		advancedMode := GranularDataModeAdvanced
		otParams.Info.GranularDataMode = &advancedMode
	}

	return otParams
}

func flexCacheVolumeCreateParamsToONTAP(params *FlexCacheVolumeCreateParams) *storage.FlexcacheCreateParams {
	otParams := storage.NewFlexcacheCreateParams()
	if params == nil {
		return otParams
	}

	flexCache := &models.Flexcache{
		Name: &params.Name,
		Svm: &models.FlexcacheInlineSvm{
			Name: &params.SvmName,
		},
		Size: &params.Size,
		FlexcacheInlineOrigins: []*models.FlexcacheRelationship{
			{
				Svm: &models.FlexcacheRelationshipInlineSvm{
					Name: &params.OriginSvmName,
				},
				Volume: &models.FlexcacheRelationshipInlineVolume{
					Name: &params.OriginVolumeName,
				},
			},
		},
		Path: params.Path,
		AtimeScrub: &models.FlexcacheInlineAtimeScrub{
			Enabled: params.AtimeScrubEnabled,
			Period:  params.AtimeScrubPeriod,
		},
		CifsChangeNotify: &models.FlexcacheInlineCifsChangeNotify{
			Enabled: params.CifsChangeNotifyEnabled,
		},
		GlobalFileLockingEnabled: params.GlobalFileLockingEnabled,
		Writeback: &models.FlexcacheInlineWriteback{
			Enabled: params.WritebackEnabled,
		},
	}

	if params.Prepopulate != nil {
		flexCache.Prepopulate = &models.FlexcacheInlinePrepopulate{
			DirPaths:        params.Prepopulate.DirPaths,
			ExcludeDirPaths: params.Prepopulate.ExcludeDirPaths,
			Recurse:         params.Prepopulate.Recurse,
		}
	}

	otParams.SetInfo(flexCache)

	for _, aggregate := range params.Aggregates {
		otParams.Info.FlexcacheInlineAggregates = append(otParams.Info.FlexcacheInlineAggregates,
			&models.FlexcacheInlineAggregatesInlineArrayItem{
				Name: nillable.ToPointer(aggregate),
			})
	}

	otParams.SetReturnTimeout(&returnTimeout)
	otParams.SetReturnRecords(nillable.ToPointer("true"))
	return otParams
}

// NetworkIPServicePoliciesGetParams is the input parameter for getting ip service policies
type NetworkIPServicePoliciesGetParams struct {
	SvmName *string
	Name    *string
}

// IPServicePolicyCreateParams is the input parameter for ip service policy creation
type IPServicePolicyCreateParams struct {
	IPServicePolicyInlineServices []*string
	Name                          *string
	Scope                         *string
	SvmName                       *string
}

// IPServicePolicyModifyParams is the input parameter for modifying an IP service policy
type IPServicePolicyModifyParams struct {
	UUID     string
	Services []string
}

// NetworkIPInterfaceModifyParams is the input parameter for modifying the network ip interface
type NetworkIPInterfaceModifyParams struct {
	ServicePolicyName *string
	UUID              *string
}

// NetworkIPInterfacesDeleteParams is the input parameter for deleting network ip interfaces
type NetworkIPInterfacesDeleteParams struct {
	SvmName *string
	Name    *string
}

// NameMapping is a simple wrapper of models.NameMapping
type NameMapping struct {
	models.NameMapping
}

// NameMappingDeleteParams is the input params for name_services.NameMappingDeleteParams
type NameMappingDeleteParams struct {
	Direction string
	Index     int64
	SvmUUID   string
}

// NameMappingCreateParams is the input params for name_services.NameMappingCreateParams
type NameMappingCreateParams struct {
	BaseParams
	SvmUUID     *string
	Pattern     *string
	Replacement *string
	Direction   *string
	Index       int64
	SvmName     *string
}

// NameMappingModifyParams is the input params for name_services.NameMappingUpdateParams
type NameMappingModifyParams struct {
	BaseParams
	Direction string
	Index     int64
	SwapIndex *int64
	SvmUUID   string
	Body      *NameMappingModifyBodyParam
}

// NameMappingModifyBodyParam is the info param for name_services.NameMappingUpdateParams
type NameMappingModifyBodyParam struct {
	Direction   *string
	Index       *int64
	Pattern     *string
	Replacement *string
}

// NameMappingCollectionGetParams is the input params for name_services.NameMappingCollectionGetParams
type NameMappingCollectionGetParams struct {
	BaseParams
	SvmUUID     *string
	Pattern     *string
	Replacement *string
	Direction   *string
	SvmName     *string
}

// KerberosRealm is a simple wrapper of models.KerberosRealm
type KerberosRealm struct {
	models.KerberosRealm
}

// KerberosRealmGetParams is the input params for nas.KerberosRealmGetParams
type KerberosRealmGetParams struct {
	SvmUUID string
	Realm   string
}

// KerberosRealmCreateParams is the input params for nas.KerberosRealmCreateParams
type KerberosRealmCreateParams struct {
	SvmUUID            string
	Realm              string
	KdcIP              string
	RealmKDCPort       *int64
	RealmClockSkew     *int64
	RealmKDCVendor     *string
	AdminServerIP      *string
	AdminServerPort    *int64
	PasswordServerIP   *string
	PasswordServerPort *int64
	ADServerIP         *string
	ADServerName       *string
}

// KerberosInterface is a simple wrapper of models.KerberosInterface
type KerberosInterface struct {
	models.KerberosInterface
}

// KerberosInterfaceCollectionGetParams is the input params for nas.KerberosInterfaceCollectionGetParams
type KerberosInterfaceCollectionGetParams struct {
	SvmUUID       *string
	SvmName       *string
	InterfaceName *string
}

// KerberosInterfaceModifyParams is the input params for nas.KerberosInterfaceModifyParams
type KerberosInterfaceModifyParams struct {
	SvmUUID           string
	InterfaceUUID     *string
	IsKerberosEnabled *bool
	Spn               *string
	MachineAccount    *string
	AdminUsername     *string
	AdminPassword     *string
	OU                *string
}

// Iscsi is the iscsi service
type Iscsi struct {
	models.IscsiService
}

// IscsiGetParams is the input parameter for getting the iscsi service
type IscsiGetParams struct {
	BaseParams
	SvmUUID string
}

func iscsiServiceGetParamsToONTAP(params *IscsiGetParams) *san.IscsiServiceCollectionGetParams {
	otParams := san.NewIscsiServiceCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetSvmUUID(&params.SvmUUID)
	otParams.SetFields(params.Fields)

	// MD: It's a GET call why is there return_timeout ??
	otParams.SetReturnTimeout(returnTimeoutNoJob)
	return otParams
}

// IscsiCreateParams is the input parameter for creating the iscsi service
type IscsiCreateParams struct {
	BaseParams
	SvmUUID string
	// TODO models.IscsiServiceInlineSvm needs to support target-alias
	// TargetAlias string
}

func iscsiServiceCreateParamsToONTAP(params *IscsiCreateParams) *san.IscsiServiceCreateParams {
	otParams := san.NewIscsiServiceCreateParams()
	if params == nil {
		return otParams
	}

	otParams.SetInfo(&models.IscsiService{
		Svm: &models.IscsiServiceInlineSvm{
			UUID: &params.SvmUUID,
		},
	})
	return otParams
}

func convertListClusterPeerFromREST(resp *cluster.ClusterPeerCollectionGetOK) []*ClusterPeerResponse {
	var clusterPeers []*ClusterPeerResponse
	for _, peer := range resp.Payload.ClusterPeerResponseInlineRecords {
		var ipAddresses []string
		for _, ipAddress := range peer.Remote.IPAddresses {
			ipAddresses = append(ipAddresses, string(*ipAddress))
		}
		clusterPeer := ClusterPeerResponse{
			UUID:                nillable.FromPointer(peer.UUID),
			PeerClusterName:     nillable.FromPointer(peer.Remote.Name),
			AuthenticationState: nillable.FromPointer(peer.Authentication.State),
			Availability:        nillable.FromPointer(peer.Status.State),
			IPAddresses:         ipAddresses,
			ExpiryTime:          nillable.FromPointer(peer.Authentication.ExpiryTime),
		}
		clusterPeers = append(clusterPeers, &clusterPeer)
	}
	return clusterPeers
}

func convertClusterPeerCreateFromREST(created *priv.ClusterPeerCreateCreated) *ClusterPeerCreateResponse {
	uuid := ""
	if created.Payload.ClusterPeerResponseInlineRecords[0].Links.Self != nil {
		theLink := created.Payload.ClusterPeerResponseInlineRecords[0].Links.Self.Href
		parts := strings.Split(*theLink, "/")

		uuid = parts[len(parts)-1]
	}

	clusterPeerResponse := &ClusterPeerCreateResponse{
		ClusterPeerUUID: uuid,
	}
	if created.Payload.ClusterPeerResponseInlineRecords[0].Authentication != nil && created.Payload.ClusterPeerResponseInlineRecords[0].Authentication.Passphrase != nil {
		clusterPeerResponse.GeneratedPassphrase = created.Payload.ClusterPeerResponseInlineRecords[0].Authentication.Passphrase
	}
	if created.Payload.ClusterPeerResponseInlineRecords[0].Authentication != nil && created.Payload.ClusterPeerResponseInlineRecords[0].Authentication.ExpiryTime != nil {
		clusterPeerResponse.ExpiryTime = created.Payload.ClusterPeerResponseInlineRecords[0].Authentication.ExpiryTime
	}
	return clusterPeerResponse
}

func clusterPeerIDToONTAPDelete(clusterPeerID string, timeout time.Duration) *cluster.ClusterPeerDeleteParams {
	otDeleteParams := cluster.ClusterPeerDeleteParams{}
	otDeleteParams.SetTimeout(timeout)
	return otDeleteParams.WithUUID(clusterPeerID)
}

func clusterPeerIDToONTAPGet(clusterPeerID string) *cluster.ClusterPeerGetParams {
	otGetParams := cluster.NewClusterPeerGetParams()
	otGetParams.SetUUID(clusterPeerID)
	return otGetParams
}

func convertClusterPeerFromREST(resp *cluster.ClusterPeerGetOK) *ClusterPeerResponse {
	if resp == nil {
		return nil
	}
	peer := resp.Payload
	if peer.Remote == nil {
		return nil
	}
	var ipAddresses []string
	for _, ipAddress := range peer.Remote.IPAddresses {
		ipAddresses = append(ipAddresses, string(*ipAddress))
	}
	clusterPeer := ClusterPeerResponse{
		UUID:                nillable.FromPointer(peer.UUID),
		PeerClusterName:     nillable.FromPointer(peer.Remote.Name),
		AuthenticationState: nillable.FromPointer(peer.Authentication.State),
		Availability:        nillable.FromPointer(peer.Status.State),
		IPAddresses:         ipAddresses,
		ExpiryTime:          nillable.FromPointer(peer.Authentication.ExpiryTime),
	}
	return &clusterPeer
}

func clusterPeerToONTAPCreate(params ClusterPeerCreateParams) *priv.ClusterPeerCreateParams {
	otParams := priv.NewClusterPeerCreateParams()
	var ipAddresses []*privmodels.IPAddress
	for _, address := range params.IPAddresses {
		ipAddresses = append(ipAddresses, nillable.ToPointer(privmodels.IPAddress(address)))
	}

	// Ensure that there is always a local role is assigned for all external cluster peers.
	// GCNV-GCNV cluster peers should not have any local role assigned
	var localRole *string
	if params.LocalRole != nil {
		localRole = params.LocalRole
	}
	clusterPeer := &privmodels.ClusterPeer{
		Name: &params.Name,
		Remote: &privmodels.ClusterPeerInlineRemote{
			IPAddresses: ipAddresses,
		},
		Authentication: &privmodels.ClusterPeerInlineAuthentication{
			GeneratePassphrase: &params.GeneratePassphrase,
			ExpiryTime:         params.ExpiryTime,
			Passphrase:         params.Passphrase,
		},
		LocalRole: localRole,
		Ipspace: &privmodels.ClusterPeerInlineIpspace{
			Name: &params.IPSpace,
		},
	}

	otParams.SetReturnRecords(nillable.ToPointer(true))
	otParams.SetInfo(clusterPeer)
	return otParams
}

func clusterPeerToONTAPAccept(params ClusterPeerCreateParams) *priv.ClusterPeerCreateParams {
	otParams := priv.NewClusterPeerCreateParams()
	var ipAddresses []*privmodels.IPAddress
	for _, address := range params.IPAddresses {
		ipAddresses = append(ipAddresses, nillable.ToPointer(privmodels.IPAddress(address)))
	}

	clusterPeer := &privmodels.ClusterPeer{
		Name: &params.Name,
		Remote: &privmodels.ClusterPeerInlineRemote{
			IPAddresses: ipAddresses,
		},
		Authentication: &privmodels.ClusterPeerInlineAuthentication{
			ExpiryTime: params.ExpiryTime,
			Passphrase: params.Passphrase,
		},
		Ipspace: &privmodels.ClusterPeerInlineIpspace{
			Name: &params.IPSpace,
		},
	}

	otParams.SetReturnRecords(nillable.ToPointer(true))
	otParams.SetInfo(clusterPeer)
	return otParams
}

// Lun is the lun
type Lun struct {
	models.Lun
}

// LunCreateParams is the input parameter for creating a Lun
type LunCreateParams struct {
	SvmName                        string
	Name                           string
	OsType                         string
	VolumeName                     string
	Size                           int64
	ThinProvisioningSupportEnabled *bool
}

const lunNamePrefix = "/vol/"

// lunCreateParamsToONTAP converts LunCreateParams to ONTAP API parameters.
func lunCreateParamsToONTAP(params *LunCreateParams) *san.LunCreateParams {
	otParams := san.NewLunCreateParams()
	if params == nil {
		return otParams
	}

	otParams.SetInfo(&models.Lun{
		Svm:  &models.LunInlineSvm{Name: &params.SvmName},
		Name: constructLunName(&params.VolumeName, &params.Name),
		Location: &models.LunInlineLocation{
			Volume: &models.LunInlineLocationInlineVolume{Name: &params.VolumeName},
		},
		Space: &models.LunInlineSpace{
			Size:                               &params.Size,
			ScsiThinProvisioningSupportEnabled: params.ThinProvisioningSupportEnabled,
		},
	})

	customOSTypeMap := map[string]string{
		"ESXI": "VMWARE",
	}
	if mappedType, exists := customOSTypeMap[params.OsType]; exists {
		otParams.Info.OsType = nillable.ToPointer(mappedType)
	} else {
		otParams.Info.OsType = &params.OsType
	}

	otParams.SetReturnTimeout(&returnTimeout)
	otParams.SetReturnRecords(nillable.ToPointer("true"))
	return otParams
}

// LunUpdateParams is the input parameter for updating a Lun
type LunUpdateParams struct {
	UUID       string
	SvmName    string
	Name       string
	VolumeName string
	Size       int64
}

// lunModifyParamsToONTAP converts LunModifyParams to ONTAP API parameters.
func lunModifyParamsToONTAP(params *LunUpdateParams) *san.LunModifyParams {
	otParams := san.NewLunModifyParams()
	if params == nil {
		return otParams
	}
	otParams.SetInfo(&models.Lun{
		Name: constructLunName(&params.VolumeName, &params.Name),
		Space: &models.LunInlineSpace{
			Size: &params.Size,
		},
	})
	otParams.UUID = params.UUID
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

type LunMap struct {
	models.LunMap
}

// LunMapCreateParams is the input parameter for creating a LunMap
type LunMapCreateParams struct {
	IGroupName string
	LunName    string
	SvmName    string
}

// LunMapDeleteParams is the input parameter for deleting a LunMap
type LunMapDeleteParams struct {
	IGroupUUID string
	LunUUID    string
}

// lunMapCreateParamsToONTAP converts LunMapCreateParams to ONTAP API parameters.
func lunMapCreateParamsToONTAP(params *LunMapCreateParams) *san.LunMapCreateParams {
	otParams := san.NewLunMapCreateParams()
	if params == nil {
		return otParams
	}

	otParams.SetInfo(&models.LunMap{
		Igroup: &models.LunMapInlineIgroup{
			Name: &params.IGroupName,
		},
		Lun: &models.LunMapInlineLun{
			Name: &params.LunName,
		},

		Svm: &models.LunMapInlineSvm{
			Name: &params.SvmName,
		},
	})
	return otParams
}

// lunMapDeleteParamsToONTAP converts LunMapDeleteParams to ONTAP API parameters.
func lunMapDeleteParamsToONTAP(params *LunMapDeleteParams) *san.LunMapDeleteParams {
	otParams := san.NewLunMapDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetLunUUID(params.LunUUID)
	otParams.SetIgroupUUID(params.IGroupUUID)

	return otParams
}

// LunMapGetParams is the input parameter for getting a LunMap
type LunMapGetParams struct {
	BaseParams
	LunUUID string
}

type LunGetParams struct {
	BaseParams
	SvmName    *string
	VolumeName *string
	LunName    *string
}

// lunGetParamsToONTAP converts LunGetParams to ONTAP API parameters.
func lunGetParamsToONTAP(params *LunGetParams) *san.LunCollectionGetParams {
	otParams := san.NewLunCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetSvmName(params.SvmName)
	otParams.SetLocationVolumeName(params.VolumeName)
	if params.LunName != nil && *params.LunName != "" {
		// For regular get, we need to pass the lun name as well
		otParams.SetName(constructLunName(params.VolumeName, params.LunName))
	}
	otParams.SetFields(params.Fields)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	return otParams
}

func constructLunName(volumeName, lunName *string) *string {
	if volumeName == nil || lunName == nil {
		return nil
	}
	return nillable.ToPointer(lunNamePrefix + *volumeName + "/" + *lunName)
}

// Igroup is the igroup
type Igroup struct {
	models.Igroup
}

// IgroupCreateParams is the input parameter for creating an Igroup
type IgroupCreateParams struct {
	SvmName    string
	Name       string
	OsType     string
	Initiators []string
	JobID      string
}

// IgroupDeleteParams is the input parameter for deleting an Igroup
type IgroupDeleteParams struct {
	UUID string
}

// igroupCreateParamsToONTAP converts IgroupCreateParams to ONTAP API parameters.
func igroupCreateParamsToONTAP(params *IgroupCreateParams) *san.IgroupCreateParams {
	otParams := san.NewIgroupCreateParams()
	if params == nil {
		return otParams
	}

	initiators := make([]*models.IgroupInlineInitiatorsInlineArrayItem, len(params.Initiators))
	for i := range params.Initiators {
		initiators[i] = &models.IgroupInlineInitiatorsInlineArrayItem{
			Name: &params.Initiators[i],
		}
	}

	otParams.SetInfo(&models.Igroup{
		Comment:                &params.JobID,
		IgroupInlineInitiators: initiators,
		Name:                   &params.Name,
		OsType:                 &params.OsType,
		Protocol:               nillable.ToPointer(models.IgroupProtocolIscsi),
		Svm:                    &models.IgroupInlineSvm{Name: &params.SvmName},
	})

	customOSTypeMap := map[string]string{
		"ESXI": "VMWARE",
	}
	if mappedType, exists := customOSTypeMap[params.OsType]; exists {
		otParams.Info.OsType = nillable.ToPointer(mappedType)
	} else {
		otParams.Info.OsType = &params.OsType
	}

	otParams.SetReturnRecords(nillable.ToPointer("true"))
	return otParams
}

// igroupDeleteParamsToONTAP converts IgroupDeleteParams to ONTAP API parameters.
func igroupDeleteParamsToONTAP(params *IgroupDeleteParams) *san.IgroupDeleteParams {
	otParams := san.NewIgroupDeleteParams()
	if params == nil {
		return otParams
	}
	otParams.SetUUID(params.UUID)
	return otParams
}

// IgroupAddInitiatorParams is the input parameter for modifying an IgroupInitiators
type IgroupAddInitiatorParams struct {
	Name         string
	InitiatorQNs []string
	IgroupUUID   string
}

// IgroupDeleteInitiatorParams is the input parameter for deleting an IgroupInitiator
type IgroupDeleteInitiatorParams struct {
	InitiatorIQNName string
	IgroupUUID       string
}

// igroupAddInitiatorParamsToONTAP converts IgroupAddInitiatorParams to ONTAP API parameters.
func igroupAddInitiatorParamsToONTAP(params *IgroupAddInitiatorParams) *san.IgroupInitiatorCreateParams {
	otParams := san.NewIgroupInitiatorCreateParams()
	if params == nil {
		return otParams
	}

	initiators := make([]*models.IgroupInitiatorInlineRecordsInlineArrayItem, len(params.InitiatorQNs))
	for i := range params.InitiatorQNs {
		initiators[i] = &models.IgroupInitiatorInlineRecordsInlineArrayItem{
			Name: &params.InitiatorQNs[i],
		}
	}

	otParams.SetInfo(&models.IgroupInitiator{
		IgroupInitiatorInlineRecords: initiators,
	})
	otParams.SetIgroupUUID(params.IgroupUUID)
	return otParams
}

// igroupDeleteInitiatorParamsToONTAP converts IgroupAddInitiatorParams to ONTAP API parameters.
func igroupDeleteInitiatorParamsToONTAP(params *IgroupDeleteInitiatorParams) *san.IgroupInitiatorDeleteParams {
	otParams := san.NewIgroupInitiatorDeleteParams()
	if params == nil {
		return otParams
	}
	otParams.SetAllowDeleteWhileMapped(nillable.GetStringPtr("true"))
	otParams.SetIgroupUUID(params.IgroupUUID)
	otParams.SetName(params.InitiatorIQNName)

	otParams.SetIgroupUUID(params.IgroupUUID)
	return otParams
}

// IgroupGetParams is the input parameter for getting Igroups
type IgroupGetParams struct {
	BaseParams
	SvmName *string
	Name    *string
}

// igroupGetParamsToONTAP converts IgroupGetParams to ONTAP API parameters.
func igroupGetParamsToONTAP(params *IgroupGetParams) *san.IgroupCollectionGetParams {
	otParams := san.NewIgroupCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(params.Name)
	otParams.SetSvmName(params.SvmName)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	otParams.SetFields(params.Fields)
	// MD: It's a GET call why is there return_timeout ??
	otParams.SetReturnTimeout(returnTimeoutNoJob)
	return otParams
}

// scheduleCreateParamsToONTAP converts ScheduleCreateParams to ONTAP API parameters.
func scheduleCreateParamsToONTAP(params *ScheduleCreateParams) *cluster.ScheduleCreateParams {
	otParams := cluster.NewScheduleCreateParams()
	if params == nil {
		return otParams
	}

	hours := make([]*int64, len(params.Hours))
	for i, val := range params.Hours {
		int64Val := int64(val)
		hours[i] = &int64Val
	}
	minutes := make([]*int64, len(params.Minutes))
	for i, val := range params.Minutes {
		int64Val := int64(val)
		minutes[i] = &int64Val
	}
	months := make([]*int64, len(params.Months))
	for i, val := range params.Months {
		int64Val := int64(val)
		months[i] = &int64Val
	}
	weekdays := make([]*int64, len(params.DaysOfWeek))
	for i, val := range params.DaysOfWeek {
		int64Val := int64(val)
		weekdays[i] = &int64Val
	}
	days := make([]*int64, len(params.DaysOfMonth))
	for i, val := range params.DaysOfMonth {
		int64Val := int64(val)
		days[i] = &int64Val
	}

	otParams.Info = &models.Schedule{
		Name: &params.Name,
		Cron: &models.ScheduleInlineCron{
			Hours:    hours,
			Months:   months,
			Days:     days,
			Weekdays: weekdays,
			Minutes:  minutes,
		},
	}
	return otParams
}

// scheduleCollectionGetParamsToONTAP converts ScheduleCollectionGetParams to ONTAP API parameters.
func scheduleCollectionGetParamsToONTAP(params *ScheduleCollectionGetParams) *cluster.ScheduleCollectionGetParams {
	otParams := cluster.NewScheduleCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.Fields = params.Fields
	otParams.Name = &params.Name
	return otParams
}

// InterclusterLif is a simple wrapper for models.IPInterface
type InterclusterLif struct {
	models.IPInterface
}

// SvmPeerGetCollectionParams is the input params struct for svm_client.SvmPeerCollectionGet
type SvmPeerGetCollectionParams struct {
	BaseParams
	SvmName     *string
	PeerSvmName *string
}

func svmPeerGetCollectionParamsToONTAP(params *SvmPeerGetCollectionParams) *svm.SvmPeerCollectionGetParams {
	otParams := svm.NewSvmPeerCollectionGetParams()
	if params == nil {
		return otParams
	}
	if params.SvmName != nil {
		otParams.SetSvmName(params.SvmName)
	}
	if params.PeerSvmName != nil {
		otParams.SetPeerSvmName(params.PeerSvmName)
	}
	otParams.SetFields(params.Fields)

	return otParams
}

// SvmPeerCreateParams is the input params struct for svm_client.SvmPeerCreate
type SvmPeerCreateParams struct {
	BaseParams
	LocalSVMName    string
	PeerSVMName     string
	PeerClusterName string
	Applications    []models.SvmPeerApplications
}

func svmPeerCreateParamsToONTAP(params *SvmPeerCreateParams) *svm.SvmPeerCreateParams {
	otParams := svm.NewSvmPeerCreateParams()
	if params == nil {
		return otParams
	}

	svmPeerInlineApplications := make([]*models.SvmPeerApplications, len(params.Applications))
	for i, app := range params.Applications {
		svmPeerInlineApplications[i] = &app
	}

	svmPeerInfo := &models.SvmPeer{
		Svm: &models.SvmPeerInlineSvm{
			Name: &params.LocalSVMName,
		},
		Peer: &models.SvmPeerInlinePeer{
			Svm: &models.SvmPeerInlinePeerInlineSvm{
				Name: &params.PeerSVMName,
			},
			Cluster: &models.SvmPeerInlinePeerInlineCluster{
				Name: &params.PeerClusterName,
			},
		},
		SvmPeerInlineApplications: svmPeerInlineApplications,
	}

	otParams.SetInfo(svmPeerInfo)
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

// SvmPeerModifyParams is the input params struct for svm_client.SvmPeerModify
type SvmPeerModifyParams struct {
	BaseParams
	UUID    string
	SvmPeer models.SvmPeer
}

func svmPeerModifyParamsToONTAP(params *SvmPeerModifyParams) *svm.SvmPeerModifyParams {
	otParams := svm.NewSvmPeerModifyParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	otParams.SetInfo(&params.SvmPeer)
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

// SvmPeerDeleteParams is the input params struct for svm_client.SvmPeerDelete
type SvmPeerDeleteParams struct {
	BaseParams
	SvmPeerUUID string
	Force       bool
}

func svmPeerDeleteParamsToONTAP(params *SvmPeerDeleteParams) *svm.SvmPeerDeleteParams {
	otParams := svm.NewSvmPeerDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.SvmPeerUUID)
	otParams.SetReturnTimeout(&returnTimeout)

	if params.Force {
		otParams.SetInfo(&models.SvmPeerDelete{
			Force: nillable.ToPointer(true),
		})
	}
	return otParams
}

func snapmirrorRelationshipCreateParamsToONTAP(params *SnapmirrorRelationshipCreateParams) *snapmirror.SnapmirrorRelationshipCreateParams {
	otParams := snapmirror.NewSnapmirrorRelationshipCreateParams()
	if params == nil {
		return otParams
	}

	var srcUUID *strfmt.UUID
	if params != nil && params.SourceUUID != nil && *params.SourceUUID != "" {
		srcUUID = nillable.ToPointer(strfmt.UUID(*params.SourceUUID))
	}

	dest := &models.SnapmirrorEndpoint{
		Path: &params.DestinationPath,
	}

	sm := &models.SnapmirrorRelationship{
		Destination: dest,
		Source: &models.SnapmirrorSourceEndpoint{
			Path: &params.SourcePath,
			UUID: srcUUID,
		},
		Restore: &params.IsRestore,
	}
	if params.Policy != "" {
		sm.Policy = &models.SnapmirrorRelationshipInlinePolicy{
			Name: &params.Policy,
		}
	}
	if params.Schedule != nil {
		sm.TransferSchedule = &models.SnapmirrorRelationshipInlineTransferSchedule{
			Name: params.Schedule,
		}
	}
	if params.AccessToken != nil && *params.AccessToken != "" {
		xNetappAuthorization := "Bearer " + *params.AccessToken
		otParams.WithXNetappAuthorization(&xNetappAuthorization)
	}
	otParams.SetInfo(sm)
	returnRecords := "true"
	otParams.SetReturnRecords(&returnRecords)
	return otParams
}

func snapmirrorRelationshipSetStateParamsToONTAP(snapmirrorUUID string, state string) *snapmirror.SnapmirrorRelationshipModifyParams {
	otParams := snapmirror.NewSnapmirrorRelationshipModifyParams()
	if snapmirrorUUID == "" {
		return otParams
	}

	sm := &models.SnapmirrorRelationship{
		State: &state,
	}

	otParams.SetUUID(snapmirrorUUID)
	otParams.SetInfo(sm)
	return otParams
}

func snapmirrorRelationshipListParamsToONTAP(params *SnapmirrorRelationshipListParams) *snapmirror.SnapmirrorRelationshipsGetParams {
	otParams := snapmirror.NewSnapmirrorRelationshipsGetParams()
	if params == nil {
		return otParams
	}
	otParams.SetDestinationPath(&params.DestinationPath)
	otParams.SetSourcePath(&params.SourcePath)
	if strings.Contains(params.DestinationPath, ":/objstore/") || strings.Contains(params.SourcePath, ":/objstore/") {
		otParams.WithFields([]string{"destination.uuid", "healthy", "unhealthy_reason.code", "unhealthy_reason.message", "state", "total_transfer_bytes"})
	}
	return otParams
}

func snapmirrorRelationshipModifyParamsToONTAP(params *SnapmirrorRelationshipModifyParams) *snapmirror.SnapmirrorRelationshipModifyParams {
	otParams := snapmirror.NewSnapmirrorRelationshipModifyParams()
	if params == nil {
		return otParams
	}

	info := &models.SnapmirrorRelationship{}
	if params.TransferSchedule != nil {
		info.TransferSchedule = &models.SnapmirrorRelationshipInlineTransferSchedule{
			Name: params.TransferSchedule,
		}
	}
	if params.State != nil {
		info.State = params.State
	}
	if params.Source != nil {
		info.Source = params.Source
	}
	if params.Destination != nil {
		info.Destination = params.Destination
	}

	otParams.SetUUID(params.UUID)
	otParams.SetInfo(info)
	return otParams
}

func snapmirrorRelationshipTransferModifyParamsToONTAP(params *SnapmirrorRelationshipTransferModifyParams) *snapmirror.SnapmirrorRelationshipTransferModifyParams {
	otParams := snapmirror.NewSnapmirrorRelationshipTransferModifyParams()
	if params == nil {
		return otParams
	}

	info := &models.SnapmirrorTransfer{}
	info.State = params.State
	otParams.SetUUID(params.TransferUUID)
	otParams.SetRelationshipUUID(params.UUID)
	otParams.SetInfo(info)
	return otParams
}

func snapmirrorRelationshipListDestinationsParamsToONTAP(params *SnapmirrorRelationshipListDestinationsParams) *snapmirror.SnapmirrorRelationshipsGetParams {
	otParams := snapmirror.NewSnapmirrorRelationshipsGetParams()
	otParams.SetListDestinationsOnly(nillable.ToPointer("true"))
	if params == nil {
		return otParams
	}
	if params.DestinationPath != nil {
		otParams.SetDestinationPath(params.DestinationPath)
	}
	if params.SourcePath != nil {
		otParams.SetSourcePath(params.SourcePath)
	}
	if params.DestinationSVMName != nil {
		otParams.SetDestinationSvmName(params.DestinationSVMName)
	}
	if params.SourceSVMName != nil {
		otParams.SetSourceSvmName(params.SourceSVMName)
	}
	return otParams
}

func convertSnapmirrorRelationshipListFromREST(response *snapmirror.SnapmirrorRelationshipsGetOK) []*SnapmirrorRelationship {
	var snapmirrorRelationships []*SnapmirrorRelationship
	if response != nil && response.Payload != nil {
		for _, record := range response.Payload.SnapmirrorRelationshipResponseInlineRecords {
			snapmirrorRelationship := SnapmirrorRelationship{
				*record,
			}
			snapmirrorRelationships = append(snapmirrorRelationships, &snapmirrorRelationship)
		}
	}
	return snapmirrorRelationships
}

func convertSnapmirrorRelationshipGetFromREST(response *snapmirror.SnapmirrorRelationshipGetOK) *SnapmirrorRelationship {
	if response != nil && response.Payload != nil {
		snapmirrorRelationship := SnapmirrorRelationship{
			*response.Payload,
		}
		return &snapmirrorRelationship
	}
	return nil
}

func snapmirrorRelationshipDeleteParamsToONTAP(params *SnapmirrorRelationshipDeleteParams) *snapmirror.SnapmirrorRelationshipDeleteParams {
	otParams := snapmirror.NewSnapmirrorRelationshipDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	otParams.SetDestinationOnly(nillable.ToStringPtr(params.DestinationOnly))
	otParams.SetSourceOnly(nillable.ToStringPtr(params.SourceOnly))
	return otParams
}

func snapmirrorRelationshipReleaseParamsToONTAP(params *SnapmirrorRelationshipReleaseParams) *snapmirror.SnapmirrorRelationshipDeleteParams {
	otParams := snapmirror.NewSnapmirrorRelationshipDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetUUID(params.UUID)
	if params.SourceInfoOnly != nil {
		otParams.SetSourceInfoOnly(nillable.ToStringPtr(params.SourceInfoOnly))
	} else {
		otParams.SetSourceOnly(nillable.ToPointer("true"))
	}
	return otParams
}

func snapmirrorRelationshipGetParamsToONTAP(params *SnapmirrorRelationshipGetParams) *snapmirror.SnapmirrorRelationshipGetParams {
	otParams := snapmirror.NewSnapmirrorRelationshipGetParams()
	if params == nil {
		return otParams
	}
	otParams.SetUUID(params.UUID)
	return otParams
}

type CloudTarget struct {
	models.CloudTarget
}

func cloudTargetCreateParamsToONTAP(params *CloudTargetCreateParams) *cloud.CloudTargetCreateParams {
	otParams := cloud.NewCloudTargetCreateParams()
	if params == nil {
		return otParams
	}
	otParams.Info = &models.CloudTarget{
		ProviderType:       &objStoreProviderType,
		AuthenticationType: &objStoreAuthenticationType,
		Server:             &objStoreServer,
		Owner:              &objStoreOwner,
		SnapmirrorUse:      &objStoreSnapmirrorUse,
		Name:               params.Name,
		Container:          params.Container,
	}
	return otParams
}

func cloudTargetDeleteParamsToONTAP(params *CloudTargetDeleteParams) *cloud.CloudTargetDeleteParams {
	otParams := cloud.NewCloudTargetDeleteParams()
	if params == nil {
		return otParams
	}
	otParams.SetUUID(params.UUID)
	return otParams
}

func cloudTargetCollectionGetParamsToONTAP(params *CloudTargetCollectionGetParams) *cloud.CloudTargetCollectionGetParams {
	otParams := cloud.NewCloudTargetCollectionGetParams()
	if params == nil {
		return otParams
	}
	otParams.SetName(params.Name)
	return otParams
}

// SnapmirrorTransfer is a simple wrapper of models.SnapmirrorTransfer
type SnapmirrorTransfer struct {
	models.SnapmirrorTransfer
}

func snapmirrorRelationshipTransferCreateParamsToONTAP(params *SnapmirrorRelationshipTransferCreateParams) *snapmirror.SnapmirrorRelationshipTransferCreateParams {
	otParams := snapmirror.NewSnapmirrorRelationshipTransferCreateParams()
	if params == nil {
		return otParams
	}
	otParams.SetRelationshipUUID(params.UUID)

	transferInfo := &models.SnapmirrorTransfer{}
	if params.SnapshotName != "" {
		transferInfo.SourceSnapshot = &params.SnapshotName
	}

	// Add files if provided
	if params.Files != nil && len(params.Files) > 0 {
		files := make([]*models.SnapmirrorTransferInlineFilesInlineArrayItem, 0, len(params.Files))
		for _, file := range params.Files {
			files = append(files, &models.SnapmirrorTransferInlineFilesInlineArrayItem{
				SourcePath:      &file.SourcePath,
				DestinationPath: &file.DestinationPath,
			})
		}
		transferInfo.SnapmirrorTransferInlineFiles = files
	}

	otParams.SetInfo(transferInfo)

	if params.AccessToken != nil && *params.AccessToken != "" {
		xNetappAuthorization := "Bearer " + *params.AccessToken
		otParams.WithXNetappAuthorization(&xNetappAuthorization)
	}
	return otParams
}

func snapmirrorRelationshipTransferGetParamsToONTAP(params *SnapmirrorRelationshipTransferGetParams) *snapmirror.SnapmirrorRelationshipTransfersGetParams {
	otParams := snapmirror.NewSnapmirrorRelationshipTransfersGetParams()
	if params == nil {
		return otParams
	}
	otParams.SetRelationshipUUID(params.SnapmirrorUUID)
	if params.SnapshotName != "" {
		otParams.SetSnapshot(&params.SnapshotName)
	}
	if params.Fields != nil {
		otParams.SetFields(params.Fields)
	}
	return otParams
}

func snapmirrorCloudEndpointDeleteParamsToONTAP(params *SnapmirrorCloudEndpointDeleteParams) *snapmirror.SnapmirrorObjstoreEpDeleteParams {
	otParams := snapmirror.NewSnapmirrorObjstoreEpDeleteParams()
	if params.ObjectStoreUUID != "" {
		otParams.SetObjectStoreUUID(params.ObjectStoreUUID)
	}
	if params.EndpointUUID != "" {
		otParams.SetUUID(params.EndpointUUID)
	}
	return otParams
}

func snapmirrorCloudSnapshotDeleteParamsToONTAP(params *SnapmirrorCloudSnapshotDeleteParams) *snapmirror.SnapmirrorObjstoreEpSnapshotDeleteParams {
	otParams := snapmirror.NewSnapmirrorObjstoreEpSnapshotDeleteParams()
	if params.ObjectStoreUUID != "" {
		otParams.SetObjectStoreUUID(params.ObjectStoreUUID)
	}
	if params.EndpointUUID != "" {
		otParams.SetEndpointUUID(params.EndpointUUID)
	}
	if params.SnapshotUUID != "" {
		otParams.SetUUID(params.SnapshotUUID)
	}
	return otParams
}

func dnsGetParamsToONTAP(params *DNSGetParams) *name_services.DNSGetParams {
	otParams := name_services.NewDNSGetParams()
	if params == nil {
		return otParams
	}

	otParams.Fields = params.Fields
	otParams.UUID = params.SvmUUID
	return otParams
}

func dnsModifyParamsToONTAP(params *DNSModifyParams) *name_services.DNSModifyParams {
	otParams := name_services.NewDNSModifyParams()
	if params == nil {
		return otParams
	}
	otParams.WithUUID(params.SvmUUID)

	info := &models.DNS{}

	for i := range params.Domains {
		info.Domains = append(info.Domains, &params.Domains[i])
	}

	for i := range params.NameServers {
		info.Servers = append(info.Servers, &params.NameServers[i])
	}

	if params.DDNSModifyParams.UseSecure != nil || params.DDNSModifyParams.Fqdn != nil {
		info.DynamicDNS = &models.DNSInlineDynamicDNS{
			Fqdn:      params.DDNSModifyParams.Fqdn,
			UseSecure: params.DDNSModifyParams.UseSecure,
			Enabled:   params.DDNSModifyParams.Enabled,
		}
	}

	otParams.SetInfo(info)
	return otParams
}

func dnsCreateParamsToONTAP(params *DNSCreateParams) *name_services.DNSCreateParams {
	otParams := name_services.NewDNSCreateParams()
	if params == nil {
		return otParams
	}

	rr := "true"
	otParams.SetReturnRecords(&rr)

	dnsDomains := make(models.DNSDomainsArrayInline, len(params.Domains))
	for i, domain := range params.Domains {
		dnsDomains[i] = nillable.ToPointer(domain)
	}

	dnsServers := make(models.NameServersArrayInline, len(params.DNSServers))
	for i, server := range params.DNSServers {
		dnsServers[i] = nillable.ToPointer(server)
	}
	if params.SvmUUID != "" {
		otParams.SetInfo(
			&models.DNS{
				Svm:     &models.DNSInlineSvm{UUID: &params.SvmUUID},
				Domains: dnsDomains,
				Servers: dnsServers,
			})
	} else {
		otParams.SetInfo(
			&models.DNS{
				Domains: dnsDomains,
				Servers: dnsServers,
			})
	}

	return otParams
}

func gcpKmsModifyParamsToONTAP(params *GcpKmsModifyParams) *security.GcpKmsModifyParams {
	otParams := security.NewGcpKmsModifyParams()
	if params == nil {
		return otParams
	}

	if params.ApplicationCredentials != nil {
		otParams.SetInfo(&models.GcpKms{
			ApplicationCredentials: nillable.ToPointer(strfmt.Password(*params.ApplicationCredentials)),
		})
	}
	otParams.SetUUID(params.UUID)
	return otParams
}

func ldapGetParamsToONTAP(params *LdapGetParams) *name_services.LdapGetParams {
	otParams := name_services.NewLdapGetParams()
	if params == nil {
		return otParams
	}

	if params.SvmUUID != "" {
		otParams.SetSvmUUID(params.SvmUUID)
	}
	otParams.SetFields(params.Fields)
	return otParams
}

func ldapCreateParamsToONTAP(params *LdapCreateParams) *name_services.LdapCreateParams {
	var skipConfigValidation = false
	var minBindLevel = "anonymous"
	var baseScope = "subtree"
	var referralEnabled = false
	var queryTimeout int64 = 10
	otParams := name_services.NewLdapCreateParams()
	if params == nil {
		return otParams
	}

	rr := "true"
	otParams.SetReturnRecords(&rr)

	otParams.SetInfo(
		&models.LdapService{
			AdDomain:                            params.DomainName,
			MinBindLevel:                        &minBindLevel,
			BaseScope:                           &baseScope,
			QueryTimeout:                        &queryTimeout,
			ReferralEnabled:                     &referralEnabled,
			BindAsCifsServer:                    params.BindAsCifsServer,
			SkipConfigValidation:                &skipConfigValidation,
			UserDn:                              params.UserDn,
			GroupDn:                             params.GroupDn,
			GroupMembershipFilter:               params.GroupMembershipFilter,
			LdapServiceInlinePreferredAdServers: params.PreferredServersForLdapClient,
			UseStartTLS:                         params.TLSEnabled,
			Schema:                              params.Schema,
			SessionSecurity:                     params.SessionSecurity,
			Port:                                params.LdapPort,
			BaseDn:                              params.BaseDN,
			Svm:                                 &models.LdapServiceInlineSvm{UUID: &params.SvmUUID},
			LdapServiceInlineServers:            params.LdapServers,
		})
	return otParams
}

func ldapSchemaCreateParamsToONTAP(params *LdapSchemaCreateParams) *name_services.LdapSchemaCreateParams {
	otParams := name_services.NewLdapSchemaCreateParams()
	if params == nil {
		return otParams
	}

	rr := "true"
	otParams.SetReturnRecords(&rr)

	otParams.SetInfo(
		&models.LdapSchema{
			Name:     params.Name,
			Template: &models.LdapSchemaInlineTemplate{Name: params.Template},
			Owner:    &models.LdapSchemaInlineOwner{UUID: params.SvmUUID},
		})
	return otParams
}

func ldapSchemaModifyParamsToONTAP(params *LdapSchemaModifyParams) *name_services.LdapSchemaModifyParams {
	otParams := name_services.NewLdapSchemaModifyParams()
	enabled := true
	if params == nil {
		return otParams
	}
	otParams.SetOwnerUUID(params.SvmUUID)
	otParams.SetName(params.SchemaName)
	otParams.SetInfo(
		&models.LdapSchema{
			Rfc2307bis: &models.Rfc2307bis{MaximumGroups: params.MaximumGroups, Enabled: &enabled},
		})
	return otParams
}

func ldapDeleteParamsToONTAP(params *LdapDeleteParams) *name_services.LdapDeleteParams {
	otParams := name_services.NewLdapDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetSvmUUID(params.SvmUUID)
	return otParams
}

func roleCreateParamsToONTAP(params *RoleCreateParams) *security.RoleCreateParams {
	otParams := security.NewRoleCreateParams()
	if params == nil {
		return otParams
	}

	privileges := make([]*models.RolePrivilege, 0)
	for _, privilege := range params.Privileges {
		access := models.RolePrivilegeLevel(privilege.Access)
		privileges = append(privileges, &models.RolePrivilege{
			Path:   &privilege.Path,
			Access: &access,
			Query:  &privilege.Query,
		})
	}
	otParams.SetInfo(&models.Role{
		Name:                 &params.Name,
		RoleInlinePrivileges: privileges,
	})
	return otParams
}

func roleGetParamsToONTAP(params *RoleGetParams) *security.RoleGetParams {
	otParams := security.NewRoleGetParams()
	if params == nil {
		return otParams
	}

	otParams.WithName(params.Name)
	otParams.WithOwnerUUID(nillable.FromPointer(params.OwnerUUID))
	otParams.WithFields(params.Fields)
	return otParams
}

func roleCollectionGetParamsToONTAP(params *RoleCollectionGetParams) *security.RoleCollectionGetParams {
	otParams := security.NewRoleCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.WithName(params.Name)
	otParams.WithFields(params.Fields)
	return otParams
}

func roleDeleteParamsToONTAP(params *RoleDeleteParams) *security.RoleDeleteParams {
	otParams := security.NewRoleDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.WithName(params.Name)
	otParams.WithOwnerUUID(nillable.FromPointer(params.OwnerUUID))
	return otParams
}

func rolePrivilegeModifyParamsToONTAP(params *RolePrivilegeModifyParams) *security.RolePrivilegeModifyParams {
	otParams := security.NewRolePrivilegeModifyParams()
	if params == nil {
		return otParams
	}

	otParams.WithOwnerUUID(params.OwnerID)
	otParams.WithName(params.Name)
	otParams.WithPath(params.Path)

	access := models.RolePrivilegeLevel(params.Access)
	otParams.WithInfo(&models.RolePrivilege{
		Access: &access,
		Query:  &params.Query,
	})
	return otParams
}

func rolePrivilegeCreateParamsToONTAP(params *RolePrivilegeCreateParams) *security.RolePrivilegeCreateParams {
	otParams := security.NewRolePrivilegeCreateParams()
	if params == nil {
		return otParams
	}

	otParams.WithOwnerUUID(params.OwnerID)
	otParams.WithName(params.Name)

	access := models.RolePrivilegeLevel(params.Access)
	otParams.WithInfo(&models.RolePrivilege{
		Path:   &params.Path,
		Access: &access,
		Query:  nillable.ToPointer(params.Query),
	})
	return otParams
}

func rolePrivilegeDeleteParamsToONTAP(params *RolePrivilegeDeleteParams) *security.RolePrivilegeDeleteParams {
	otParams := security.NewRolePrivilegeDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.WithOwnerUUID(params.OwnerID)
	otParams.WithName(params.Name)
	otParams.WithPath(params.Path)
	return otParams
}

func accountCreateParamsToONTAP(params *AccountCreateParams) *security.AccountCreateParams {
	otParams := security.NewAccountCreateParams()
	if params == nil {
		return otParams
	}

	password := strfmt.Password(params.Password)
	otParams.SetInfo(&models.Account{
		Name:     nillable.ToPointer(params.Name),
		Password: &password,
		Role: &models.AccountInlineRole{
			Name: nillable.ToPointer(params.RoleName),
		},
		Owner: &models.AccountInlineOwner{
			UUID: nillable.ToPointer(params.OwnerUUID),
		},
		AccountInlineApplications: []*models.AccountApplication{
			{
				Application: nillable.ToPointer("http"),
				AuthenticationMethods: []*string{
					nillable.ToPointer("password"),
				},
			},
			{
				Application: nillable.ToPointer("ontapi"),
				AuthenticationMethods: []*string{
					nillable.ToPointer("password"),
				},
			},
		},
	})

	return otParams
}

func accountCollectionGetParamsToONTAP(params *AccountGetParams) *security.AccountCollectionGetParams {
	otParams := security.NewAccountCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(nillable.ToPointer(params.Name))
	return otParams
}

func accountDeleteParamsToONTAP(params *AccountDeleteParams) *security.AccountDeleteParams {
	otParams := security.NewAccountDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(params.Name)
	otParams.SetOwnerUUID(params.OwnerUUID)
	return otParams
}

// =============================================================================
// NAS Client Models and Parameters
// =============================================================================

// ExportPolicy is a simple wrapper of models.ExportPolicy
type ExportPolicy struct {
	models.ExportPolicy
}

// ExportPolicyCreateParams is the input param struct for nasClient.ExportPolicyCreate
type ExportPolicyCreateParams struct {
	BaseParams
	Name    string
	SvmName string
	Rules   []*ExportRule
}

// ExportRule describes an export policy rule
type ExportRule struct {
	ChownMode        string
	ClientMatch      string
	ReadOnlyRule     string
	ReadWriteRule    string
	SuperUserRule    string
	AnonymousUser    string
	Index            int64
	NtfsUnixSecurity string
	Protocols        []string
}

// ExportPolicyGetParams is the input param struct for nasClient.ExportPolicyGet and ExportPoliciesGet
type ExportPolicyGetParams struct {
	BaseParams
	Name    *string
	SvmName *string
}

// ExportPolicyModifyParams is the input param struct for nasClient.ExportPolicyModify
type ExportPolicyModifyParams struct {
	BaseParams
	ID      int64
	Name    *string
	SvmName string
	Rules   []*ExportRule
}

// ExportPolicyDeleteParams is the input param struct for nasClient.ExportPolicyDelete
type ExportPolicyDeleteParams struct {
	BaseParams
	Name    string
	SvmName string
}

// NfsService is a simple wrapper of models.NfsService
type NfsService struct {
	models.NfsService
}

// NfsServiceGetParams is the input param struct for nasClient.NfsServiceGet
type NfsServiceGetParams struct {
	BaseParams
	SvmUUID string
}

// NfsServiceCreateParams is the input param struct for nasClient.NfsServiceCreate
type NfsServiceCreateParams struct {
	BaseParams
	SvmUUID string
	Enabled *bool
	V3      *bool
	V4      *bool
	V41     *bool
}

// NfsServiceModifyParams is the input param struct for nasClient.NfsServiceModify
type NfsServiceModifyParams struct {
	BaseParams
	SvmUUID                    string
	V4IDDomain                 *string
	AllowLocalNFSUsersWithLdap *bool
	ExtendedGroupsLimit        *int64
	Enabled                    *bool
	V3                         *bool
	V4                         *bool
	V41                        *bool
}

// CifsService is a simple wrapper of models.CifsService
type CifsService struct {
	models.CifsService
}

// CifsServiceGetParams is the input param struct for nasClient.CifsServiceGet
type CifsServiceGetParams struct {
	BaseParams
	SvmName *string
	SvmUUID *string
}

// CifsServiceCreateParams is the input param struct for nasClient.CifsServiceCreate
type CifsServiceCreateParams struct {
	SvmName                 *string
	Name                    *string
	Domain                  *string
	OrganizationalUnit      *string
	Username                *string
	Password                *string
	Force                   *bool
	Site                    *string
	ServerRootCaCertificate *string
	TLSEnabled              *bool
	TenantID                *string
	ClientID                *string
	EntraIDCertificate      *log.Secret
	AuthUserType            *string
}

// CifsServiceModifyParams is the input param struct for nasClient.CifsServiceModify
type CifsServiceModifyParams struct {
	Enabled                  *bool
	AesEncryptionEnabled     *bool
	TLSEnabled               *bool
	DacEnabled               *bool
	EncryptDCConnections     *bool
	SessionSecurityForAdLdap *string
	Name                     *string
	Username                 *string
	Password                 *string
	SmbEncryption            *bool
	Site                     *string
	CompatibilityLevel       *string
	CopyOffload              *bool
	Multichannel             *bool
	RestrictAnonymous        *string
	SvmUUID                  *string
}

// CifsDomainModifyParams is the input params struct for nas.CifsServiceModify
type CifsDomainModifyParams struct {
	SvmUUID               string
	DiscoveryMode         *string
	ScheduleEnabled       *bool
	CifsPasswordOperation *string
	AdUserName            *string
	AdPassword            *string
	ClientID              *string
	TenantID              *string
	ClientCertificate     *strfmt.Password
}

// CifsShareACLDeleteParams is the input params for nas.CifsShareACLDelete
type CifsShareACLDeleteParams struct {
	ShareName string
	User      string
	SvmUUID   string
}

// CifsShareDeleteParams is the input params for nas.CifsShareDelete
type CifsShareDeleteParams struct {
	BaseParams
	ShareName string
	SvmUUID   string
}

// =============================================================================
// NAS Client Parameter Conversion Functions
// =============================================================================

// defaultExportAuthFlavorForEmptyRule returns "sys" when the rule string is empty.
// ONTAP treats rorule/rwrule "none" as mapping unlisted AUTH_SYS clients to anonymous; empty must not imply that.
func defaultExportAuthFlavorForEmptyRule(s string) models.ExportAuthenticationFlavor {
	if strings.TrimSpace(s) == "" {
		return models.ExportAuthenticationFlavorSys
	}
	return models.ExportAuthenticationFlavor(s)
}

// exportPolicyCreateParamsToONTAP converts ExportPolicyCreateParams to ONTAP API parameters
func exportPolicyCreateParamsToONTAP(params *ExportPolicyCreateParams, trace log.Logger) *nas.ExportPolicyCreateParams {
	otParams := nas.NewExportPolicyCreateParams()
	if params == nil {
		return otParams
	}

	rules := make([]*models.ExportRules, len(params.Rules))
	for i, rule := range params.Rules {
		// Convert rule protocols to string pointers
		protocols := make([]*string, len(rule.Protocols))
		for j, protocol := range rule.Protocols {
			protocols[j] = &protocol
		}

		// Convert authentication flavors
		readOnlyRules := strings.Split(strings.TrimSpace(rule.ReadOnlyRule), ",")
		roRules := make([]*models.ExportAuthenticationFlavor, 0)
		for _, readOnlyRule := range readOnlyRules {
			if readOnlyRule != "" {
				readOnlyRuleExportAuthFlavor := models.ExportAuthenticationFlavor(readOnlyRule)
				roRules = append(roRules, &readOnlyRuleExportAuthFlavor)
			}
		}
		if len(roRules) == 0 {
			sysRO := defaultExportAuthFlavorForEmptyRule("")
			roRules = append(roRules, &sysRO)
		}

		readWriteRules := strings.Split(strings.TrimSpace(rule.ReadWriteRule), ",")
		rwRules := make([]*models.ExportAuthenticationFlavor, 0)
		for _, readWriteRule := range readWriteRules {
			if readWriteRule == "" {
				continue
			}
			readWriteRuleExportAuthFlavor := models.ExportAuthenticationFlavor(readWriteRule)
			rwRules = append(rwRules, &readWriteRuleExportAuthFlavor)
		}
		if len(rwRules) == 0 {
			sysRW := defaultExportAuthFlavorForEmptyRule("")
			rwRules = append(rwRules, &sysRW)
		}
		superuser := make([]*models.ExportAuthenticationFlavor, 1)
		superuser[0] = (*models.ExportAuthenticationFlavor)(&rule.SuperUserRule)

		rules[i] = &models.ExportRules{
			ExportRulesInlineClients:   buildExportClients(rule.ClientMatch),
			ExportRulesInlineRoRule:    roRules,
			ExportRulesInlineRwRule:    rwRules,
			ExportRulesInlineSuperuser: superuser,
			Protocols:                  protocols,
			Index:                      &rule.Index,
		}
		if rule.AnonymousUser != "" {
			rules[i].AnonymousUser = &rule.AnonymousUser
		}
	}

	otParams.SetInfo(&models.ExportPolicy{
		Name:                    &params.Name,
		Svm:                     &models.ExportPolicyInlineSvm{Name: &params.SvmName},
		ExportPolicyInlineRules: rules,
	})

	otParams.SetReturnRecords(nillable.ToPointer("true"))
	return otParams
}

func buildExportClients(clientMatch string) []*models.ExportClients {
	matches := utils.SplitAndTrimString(clientMatch, ",")
	clients := make([]*models.ExportClients, 0, len(matches))
	for _, match := range matches {
		clients = append(clients, &models.ExportClients{Match: &match})
	}
	if len(clients) == 0 {
		return []*models.ExportClients{{Match: &clientMatch}}
	}
	return clients
}

// exportPolicyGetParamsToONTAP converts ExportPolicyGetParams to ONTAP API parameters
func exportPolicyGetParamsToONTAP(params *ExportPolicyGetParams) *nas.ExportPolicyCollectionGetParams {
	otParams := nas.NewExportPolicyCollectionGetParams()
	if params == nil {
		return otParams
	}

	if params.Name != nil {
		otParams.SetName(params.Name)
	}
	if params.SvmName != nil {
		otParams.SetSvmName(params.SvmName)
	}
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	otParams.SetFields(params.Fields)
	otParams.SetReturnTimeout(returnTimeoutNoJob)
	return otParams
}

// exportPolicyModifyParamsToONTAP converts ExportPolicyModifyParams to ONTAP API parameters
func exportPolicyModifyParamsToONTAP(params *ExportPolicyModifyParams) *nas.ExportPolicyModifyParams {
	otParams := nas.NewExportPolicyModifyParams()
	if params == nil {
		return otParams
	}

	rules := make([]*models.ExportRules, len(params.Rules))
	for i, rule := range params.Rules {
		// Convert rule protocols to string pointers
		protocols := make([]*string, len(rule.Protocols))
		for j, protocol := range rule.Protocols {
			protocols[j] = &protocol
		}

		// Convert authentication flavors
		roFlavor := defaultExportAuthFlavorForEmptyRule(rule.ReadOnlyRule)
		rwFlavor := defaultExportAuthFlavorForEmptyRule(rule.ReadWriteRule)
		roRule := []*models.ExportAuthenticationFlavor{&roFlavor}
		rwRule := []*models.ExportAuthenticationFlavor{&rwFlavor}

		superuser := make([]*models.ExportAuthenticationFlavor, 1)
		superuser[0] = (*models.ExportAuthenticationFlavor)(&rule.SuperUserRule)

		rules[i] = &models.ExportRules{
			ExportRulesInlineClients:   buildExportClients(rule.ClientMatch),
			ExportRulesInlineRoRule:    roRule,
			ExportRulesInlineRwRule:    rwRule,
			ExportRulesInlineSuperuser: superuser,
			Protocols:                  protocols,
		}
		if rule.AnonymousUser != "" {
			rules[i].AnonymousUser = &rule.AnonymousUser
		}
	}

	otParams.SetID(params.ID)
	otParams.SetInfo(&models.ExportPolicy{
		Name:                    params.Name,
		ExportPolicyInlineRules: rules,
	})
	return otParams
}

// exportPolicyDeleteParamsToONTAP converts ExportPolicyDeleteParams to ONTAP API parameters
func exportPolicyDeleteParamsToONTAP(params *ExportPolicyDeleteParams) *nas.ExportPolicyDeleteCollectionParams {
	otParams := nas.NewExportPolicyDeleteCollectionParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(&params.Name)
	otParams.SetSvmName(&params.SvmName)
	otParams.SetReturnTimeout(returnTimeoutNoJob)
	return otParams
}

// nfsServiceGetParamsToONTAP converts NfsServiceGetParams to ONTAP API parameters
func nfsServiceGetParamsToONTAP(params *NfsServiceGetParams) *nas.NfsGetParams {
	otParams := nas.NewNfsGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetSvmUUID(params.SvmUUID)
	otParams.SetFields(params.Fields)
	return otParams
}

// nfsServiceCreateParamsToONTAP converts NfsServiceCreateParams to ONTAP API parameters
func nfsServiceCreateParamsToONTAP(params *NfsServiceCreateParams) *nas.NfsCreateParams {
	otParams := nas.NewNfsCreateParams()
	if params == nil {
		return otParams
	}

	nfsInfo := &models.NfsService{
		Svm: &models.NfsServiceInlineSvm{UUID: &params.SvmUUID},
	}

	if params.Enabled != nil {
		nfsInfo.Enabled = params.Enabled
	}

	if params.V3 != nil || params.V4 != nil || params.V41 != nil {
		nfsInfo.Protocol = &models.NfsServiceInlineProtocol{}
		if params.V3 != nil {
			nfsInfo.Protocol.V3Enabled = params.V3
		}
		if params.V4 != nil {
			nfsInfo.Protocol.V40Enabled = params.V4
		}
		if params.V41 != nil {
			nfsInfo.Protocol.V41Enabled = params.V41
		}
	}

	otParams.SetInfo(nfsInfo)
	otParams.SetReturnRecords(nillable.ToPointer("true"))
	return otParams
}

// nfsServiceModifyParamsToONTAP converts NfsServiceModifyParams to ONTAP API parameters
func nfsServiceModifyParamsToONTAP(params *NfsServiceModifyParams) *nas.NfsModifyParams {
	otParams := nas.NewNfsModifyParams()
	if params == nil {
		return otParams
	}

	nfsInfo := &models.NfsService{
		Svm: &models.NfsServiceInlineSvm{UUID: &params.SvmUUID},
	}

	if params.Enabled != nil {
		nfsInfo.Enabled = params.Enabled
	}

	if params.V3 != nil || params.V4 != nil || params.V41 != nil {
		nfsInfo.Protocol = &models.NfsServiceInlineProtocol{}
		if params.V3 != nil {
			nfsInfo.Protocol.V3Enabled = params.V3
		}
		if params.V4 != nil {
			nfsInfo.Protocol.V40Enabled = params.V4
		}
		if params.V41 != nil {
			nfsInfo.Protocol.V41Enabled = params.V41
		}
		if params.V4IDDomain != nil {
			nfsInfo.Protocol.V4IDDomain = params.V4IDDomain
		}
		if params.AllowLocalNFSUsersWithLdap != nil {
			nfsInfo.AuthSysExtendedGroupsEnabled = params.AllowLocalNFSUsersWithLdap
		}
		if params.ExtendedGroupsLimit != nil {
			nfsInfo.ExtendedGroupsLimit = params.ExtendedGroupsLimit
		}
	}

	otParams.SetInfo(nfsInfo)
	otParams.SetSvmUUID(params.SvmUUID)
	return otParams
}

// nfsParamsModifyToONTAP converts NfsModifyParams to ONTAP API parameters
func nfsParamsModifyToONTAP(ctx context.Context, params *NfsModifyParams) *nas.NfsModifyParams {
	otParams := nas.NewNfsModifyParams().WithContext(ctx)
	if params == nil {
		return otParams
	}

	nfsInfo := &models.NfsService{
		ShowmountEnabled: params.ShowmountEnabled,
		Protocol:         &models.NfsServiceInlineProtocol{},
		Enabled:          params.Enabled,
	}

	if params.V4IDDomain != nil {
		nfsInfo.Protocol.V4IDDomain = params.V4IDDomain
	}

	if params.AllowLocalNFSUsersWithLdap != nil {
		nfsInfo.AuthSysExtendedGroupsEnabled = params.AllowLocalNFSUsersWithLdap
	}

	if params.ExtendedGroupsLimit != nil {
		nfsInfo.ExtendedGroupsLimit = params.ExtendedGroupsLimit
	}

	if params.V3Enabled != nil {
		nfsInfo.Protocol.V3Enabled = params.V3Enabled
	}

	if params.V40Enabled != nil {
		nfsInfo.Protocol.V40Enabled = params.V40Enabled
	}

	if params.V41Enabled != nil {
		nfsInfo.Protocol.V41Enabled = params.V41Enabled
	}

	if params.RquotaEnabled != nil {
		nfsInfo.RquotaEnabled = params.RquotaEnabled
	}

	if params.VstorageEnabled != nil {
		nfsInfo.VstorageEnabled = params.VstorageEnabled
	}

	if params.FileSessionIoGroupingCount != nil {
		nfsInfo.FileSessionIoGroupingCount = params.FileSessionIoGroupingCount
	}

	otParams.SetInfo(nfsInfo)
	otParams.SetSvmUUID(params.SvmUUID)
	return otParams
}

// cifsServiceGetParamsToONTAP converts CifsServiceGetParams to ONTAP API parameters
func cifsServiceGetParamsToONTAP(params *CifsServiceGetParams) *nas.CifsServiceCollectionGetParams {
	otParams := nas.NewCifsServiceCollectionGetParams()
	if params == nil {
		return otParams
	}

	if params.SvmName != nil {
		otParams.SetSvmName(params.SvmName)
	}
	if params.SvmUUID != nil {
		otParams.SetSvmUUID(params.SvmUUID)
	}
	otParams.SetFields(params.Fields)
	otParams.SetReturnTimeout(returnTimeoutNoJob)
	return otParams
}

// cifsServiceCreateParamsToONTAP converts CifsServiceCreateParams to ONTAP API parameters
func cifsServiceCreateParamsToONTAP(params *CifsServiceCreateParams) *nas.CifsServiceCreateParams {
	otParams := nas.NewCifsServiceCreateParams()
	if params == nil {
		return otParams
	}

	var adDomain *models.AdDomain
	otParams.SetForce(nillable.ToStringPtr(params.Force))
	if params.AuthUserType != nil && *params.AuthUserType == hybridUser {
		authMethod := authMethodCertificate
		adDomain = &models.AdDomain{
			Fqdn: params.Domain,
		}
		cifsInfo := &models.CifsService{
			Name:                 params.Name,
			Svm:                  &models.CifsServiceInlineSvm{Name: params.SvmName},
			Security:             &models.CifsServiceSecurity{UseStartTLS: params.TLSEnabled},
			ClientID:             params.ClientID,
			TenantID:             params.TenantID,
			AuthUserType:         params.AuthUserType,
			AuthenticationMethod: &authMethod,
			AdDomain:             adDomain,
		}
		if params.EntraIDCertificate != nil {
			cifsInfo.ClientCertificate = nillable.ToPointer(strfmt.Password(*params.EntraIDCertificate))
		}
		otParams.SetInfo(cifsInfo)
	} else {
		if params.Site != nil || params.OrganizationalUnit != nil || params.Username != nil || params.Password != nil {
			adDomain = &models.AdDomain{
				DefaultSite:        params.Site,
				OrganizationalUnit: params.OrganizationalUnit,
				User:               params.Username,
				Password:           params.Password,
				Fqdn:               params.Domain,
			}
		}
		otParams.SetInfo(&models.CifsService{
			Name:     params.Name,
			Security: &models.CifsServiceSecurity{UseStartTLS: params.TLSEnabled},
			Svm:      &models.CifsServiceInlineSvm{Name: params.SvmName},
			AdDomain: adDomain,
		})
	}

	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

// cifsServiceModifyParamsToONTAP converts CifsServiceModifyParams to ONTAP API parameters
func cifsServiceModifyParamsToONTAP(params *CifsServiceModifyParams) *nas.CifsServiceModifyParams {
	otParams := nas.NewCifsServiceModifyParams()
	if params == nil {
		return otParams
	}

	var cifsSecurity *models.CifsServiceSecurity
	if params.TLSEnabled != nil || params.EncryptDCConnections != nil || params.SessionSecurityForAdLdap != nil || params.AesEncryptionEnabled != nil || params.SmbEncryption != nil || params.CompatibilityLevel != nil || params.RestrictAnonymous != nil {
		cifsSecurity = &models.CifsServiceSecurity{
			UseStartTLS:          params.TLSEnabled,
			EncryptDcConnection:  params.EncryptDCConnections,
			SessionSecurity:      params.SessionSecurityForAdLdap,
			KdcEncryption:        params.AesEncryptionEnabled,
			SmbEncryption:        params.SmbEncryption,
			LmCompatibilityLevel: params.CompatibilityLevel,
			RestrictAnonymous:    params.RestrictAnonymous,
		}
	}

	var adDomain *models.AdDomain
	if params.Username != nil || params.Password != nil || params.Site != nil {
		adDomain = &models.AdDomain{
			User:        params.Username,
			Password:    params.Password,
			DefaultSite: params.Site,
		}
	}

	var cifsOptions *models.CifsServiceOptions
	if params.CopyOffload != nil || params.Multichannel != nil || params.DacEnabled != nil {
		cifsOptions = &models.CifsServiceOptions{
			CopyOffload:  params.CopyOffload,
			Multichannel: params.Multichannel,
			DacEnabled:   params.DacEnabled,
		}
	}

	info := &models.CifsService{
		Name:     params.Name,
		Enabled:  params.Enabled,
		Security: cifsSecurity,
		AdDomain: adDomain,
		Options:  cifsOptions,
	}

	otParams.SetSvmUUID(*params.SvmUUID)
	otParams.SetInfo(info)
	otParams.WithReturnTimeout(&returnTimeout)
	return otParams
}

func cifsDomainGetParamsToONTAP(params *CifsDomainGetParams) *nas.CifsDomainGetParams {
	otParams := nas.NewCifsDomainGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetFields(params.Fields)
	otParams.SetSvmUUID(params.SvmUUID)
	otParams.SetResetDiscoveredServers(nillable.ToStringPtr(params.ResetDiscoveredServers))
	otParams.SetRediscoverTrusts(nillable.ToStringPtr(params.RediscoverTrusts))
	return otParams
}

func cifsDomainModifyParamsToONTAP(params *CifsDomainModifyParams) *nas.CifsDomainModifyParams {
	otParams := nas.NewCifsDomainModifyParams()
	if params == nil {
		return otParams
	}

	info := &models.CifsDomain{
		ServerDiscoveryMode: params.DiscoveryMode,
	}
	if params.ScheduleEnabled != nil {
		info.PasswordSchedule = &models.CifsDomainInlinePasswordSchedule{ScheduleEnabled: params.ScheduleEnabled}
	}
	if params.CifsPasswordOperation != nil {
		otParams.SetCifsPasswordOperation(params.CifsPasswordOperation)
	}

	if params.AdUserName != nil && params.AdPassword != nil {
		info.AdDomain = &models.CifsDomainInlineAdDomain{
			User:     params.AdUserName,
			Password: params.AdPassword,
		}
	}

	if params.ClientID != nil && params.TenantID != nil && params.ClientCertificate != nil {
		info.ClientID = params.ClientID
		info.TenantID = params.TenantID
		info.ClientCertificate = params.ClientCertificate
	}

	otParams.SetSvmUUID(params.SvmUUID)
	otParams.SetInfo(info)
	return otParams
}

func cifsShareCreateParamsToONTAP(params *CifsShareCreateParams) *nas.CifsShareCreateParams {
	otParams := nas.NewCifsShareCreateParams()
	if params == nil {
		return otParams
	}

	cifsShareParams := calculateShareProperties(params.ShareProperties)
	cifsShareParams.Name = &params.Name
	cifsShareParams.Path = &params.Path
	cifsShareParams.Svm = &models.CifsShareInlineSvm{Name: params.SvmName}

	otParams.SetInfo(cifsShareParams)
	return otParams
}

func cifsShareModifyParamsToONTAP(params *CifsShareModifyParams) *nas.CifsShareModifyParams {
	otParams := nas.NewCifsShareModifyParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(params.ShareName)
	otParams.SetSvmUUID(params.SvmUUID)

	otParams.SetInfo(calculateShareProperties(params.ShareProperties))
	return otParams
}

func cifsShareDeleteParamsToONTAP(params *CifsShareDeleteParams) *nas.CifsShareDeleteParams {
	otParams := nas.NewCifsShareDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetName(params.ShareName)
	otParams.SetSvmUUID(params.SvmUUID)
	return otParams
}

func cifsShareCollectionGetParamsToONTAP(params *CifsShareCollectionGetParams) *nas.CifsShareCollectionGetParams {
	otParams := nas.NewCifsShareCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetSvmUUID(&params.SvmUUID)
	otParams.SetName(&params.ShareName)
	otParams.Fields = params.Fields
	return otParams
}

func calculateShareProperties(shareProperties []string) *models.CifsShare {
	cifsShareParams := &models.CifsShare{
		ShowPreviousVersions:   nillable.ToPointer(false),
		Browsable:              nillable.ToPointer(false),
		ChangeNotify:           nillable.ToPointer(false),
		Oplocks:                nillable.ToPointer(false),
		ShowSnapshot:           nillable.ToPointer(false),
		ContinuouslyAvailable:  nillable.ToPointer(false),
		Encryption:             nillable.ToPointer(false),
		AccessBasedEnumeration: nillable.ToPointer(false),
	}
	shareProperties = ExtendSharePropertiesWithDefaults(shareProperties)
	for _, sp := range shareProperties {
		switch sp {
		case utils.CIFSSharePropertyShowPreviousVersions:
			cifsShareParams.ShowPreviousVersions = nillable.ToPointer(true)
		case utils.CIFSSharePropertyBrowsable:
			cifsShareParams.Browsable = nillable.ToPointer(true)
		case utils.CIFSSharePropertyChangenotify:
			cifsShareParams.ChangeNotify = nillable.ToPointer(true)
		case utils.CIFSSharePropertyOplocks:
			cifsShareParams.Oplocks = nillable.ToPointer(true)
		case utils.CIFSSharePropertyShowsnapshot:
			cifsShareParams.ShowSnapshot = nillable.ToPointer(true)
		case utils.CIFSSharePropertyCA:
			cifsShareParams.ContinuouslyAvailable = nillable.ToPointer(true)
		case utils.CIFSSharePropertyEncryptData:
			cifsShareParams.Encryption = nillable.ToPointer(true)
		case utils.CIFSAccessBasedEnumeration:
			cifsShareParams.AccessBasedEnumeration = nillable.ToPointer(true)
		}
	}
	return cifsShareParams
}

// ExtendSharePropertiesWithDefaults extends the share properties with default values
func ExtendSharePropertiesWithDefaults(shareProperties []string) []string {
	var extendedShareProperties []string
	if slices.Contains(shareProperties, utils.CIFSSharePropertyCA) {
		extendedShareProperties = defaultCIFSCAShareProperties
	} else {
		extendedShareProperties = defaultCIFSShareProperties
	}

	if !slices.Contains(shareProperties, utils.CIFSSharePropertyNonBrowsable) && !slices.Contains(shareProperties, utils.CIFSSharePropertyBrowsable) {
		shareProperties = append(shareProperties, utils.CIFSSharePropertyBrowsable)
	}

	for _, shareProperty := range shareProperties {
		if !slices.Contains(extendedShareProperties, shareProperty) && strings.ToLower(shareProperty) != utils.CIFSSharePropertyNonBrowsable {
			extendedShareProperties = append(extendedShareProperties, shareProperty)
		}
	}
	return extendedShareProperties
}

func cifsShareACLDeleteParamsToONTAP(params *CifsShareACLDeleteParams) *nas.CifsShareACLDeleteParams {
	otParams := nas.NewCifsShareACLDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.SetShare(params.ShareName)
	otParams.SetUserOrGroup(params.User)
	otParams.SetSvmUUID(params.SvmUUID)
	return otParams
}

func cifsServiceDeleteParamsToONTAP(params *CifsServiceDeleteParams) *nas.CifsServiceDeleteParams {
	otParams := nas.NewCifsServiceDeleteParams()
	if params == nil {
		return otParams
	}

	otParams.WithForce(nillable.ToStringPtr(&params.Force))
	otParams.WithSvmUUID(params.SvmUUID)
	if params.ClientID != "" || params.TenantID != "" || params.EntraIDCertificate != "" {
		otParams.WithInfo(&models.CifsServiceDelete{
			ClientID:          &params.ClientID,
			TenantID:          &params.TenantID,
			ClientCertificate: &params.EntraIDCertificate,
		})
	} else {
		otParams.SetInfo(&models.CifsServiceDelete{
			AdDomain: &models.AdDomainDelete{
				User:     &params.AdminUsername,
				Password: &params.AdminPassword,
			},
		})
	}
	return otParams
}

// SnapmirrorCloudSnapshotGetParams describes the params to invoke Snapmirror Cloud Snapshot Get
type SnapmirrorCloudSnapshotGetParams struct {
	ObjectStoreUUID string
	EndpointUUID    string
	SnapshotUUID    string
}

type SnapmirrorEndpointSnapshot struct {
	models.SnapmirrorObjectStoreEndpointSnapshot
}

func snapmirrorCloudSnapshotGetParamsToONTAP(params *SnapmirrorCloudSnapshotGetParams) *snapmirror.SnapmirrorObjectStoreEndpointSnapshotGetParams {
	otParams := snapmirror.NewSnapmirrorObjectStoreEndpointSnapshotGetParams()
	if params.ObjectStoreUUID != "" {
		otParams.SetObjectStoreUUID(params.ObjectStoreUUID)
	}
	if params.EndpointUUID != "" {
		otParams.SetEndpointUUID(params.EndpointUUID)
	}
	if params.SnapshotUUID != "" {
		otParams.SetUUID(params.SnapshotUUID)
	}
	return otParams
}

// ObjectStoreEndpointInfoGetParams describes the params to invoke Object Store Endpoint Info Get
type ObjectStoreEndpointInfoGetParams struct {
	ObjectStoreUUID string
	UUID            string
}

// ObjectStoreEndpointInfo is a simple wrapper of models.ObjectStoreEndpointInfo
type ObjectStoreEndpointInfo struct {
	models.ObjectStoreEndpointInfo
}

func objectStoreEndpointInfoGetParamsToONTAP(params *ObjectStoreEndpointInfoGetParams) *snapmirror.ObjectStoreEndpointInfoGetParams {
	otParams := snapmirror.NewObjectStoreEndpointInfoGetParams()
	if params.ObjectStoreUUID != "" {
		otParams.SetObjectStoreUUID(params.ObjectStoreUUID)
	}
	if params.UUID != "" {
		otParams.SetUUID(params.UUID)
	}
	return otParams
}

func kerberosRealmGetParamsToONTAP(params *KerberosRealmGetParams) *nas.KerberosRealmGetParams {
	otParams := nas.NewKerberosRealmGetParams()
	if params == nil {
		return otParams
	}
	otParams.SetSvmUUID(params.SvmUUID)
	otParams.SetName(params.Realm)
	return otParams
}

func kerberosRealmCreateParamsToONTAP(params *KerberosRealmCreateParams) *nas.KerberosRealmCreateParams {
	otParams := nas.NewKerberosRealmCreateParams()
	if params == nil {
		return otParams
	}
	info := &models.KerberosRealm{
		Name: &params.Realm,
		Svm: &models.KerberosRealmInlineSvm{
			UUID: &params.SvmUUID,
		},
		Kdc: &models.KerberosRealmInlineKdc{
			IP:     &params.KdcIP,
			Port:   params.RealmKDCPort,
			Vendor: params.RealmKDCVendor,
		},
		ClockSkew: params.RealmClockSkew,
	}
	if params.AdminServerIP != nil {
		info.AdminServer = &models.KerberosRealmInlineAdminServer{
			Address: params.AdminServerIP,
			Port:    params.AdminServerPort,
		}
	}
	if params.PasswordServerIP != nil {
		info.PasswordServer = &models.KerberosRealmInlinePasswordServer{
			Address: params.PasswordServerIP,
			Port:    params.PasswordServerPort,
		}
	}
	if params.ADServerIP != nil && params.ADServerName != nil {
		info.AdServer = &models.KerberosRealmInlineAdServer{
			Address: params.ADServerIP,
			Name:    params.ADServerName,
		}
	}
	otParams.SetInfo(info)
	return otParams
}

func kerberosInterfaceCollectionGetParamsToONTAP(params *KerberosInterfaceCollectionGetParams) *nas.KerberosInterfaceCollectionGetParams {
	otParams := nas.NewKerberosInterfaceCollectionGetParams()
	if params == nil {
		return otParams
	}
	if params.SvmUUID != nil {
		otParams.SetSvmUUID(params.SvmUUID)
	}
	if params.SvmName != nil {
		otParams.SetSvmName(params.SvmName)
	}
	if params.InterfaceName != nil {
		otParams.SetInterfaceName(params.InterfaceName)
	}
	return otParams
}

func kerberosInterfaceModifyParamsToONTAP(params *KerberosInterfaceModifyParams) *nas.KerberosInterfaceModifyParams {
	otParams := nas.NewKerberosInterfaceModifyParams()
	if params == nil {
		return otParams
	}
	otParams.SetInterfaceUUID(*params.InterfaceUUID)
	info := &models.KerberosInterface{
		Enabled:            params.IsKerberosEnabled,
		Spn:                params.Spn,
		MachineAccount:     params.MachineAccount,
		OrganizationalUnit: params.OU,
		User:               params.AdminUsername,
		Password:           params.AdminPassword,
	}
	otParams.SetInfo(info)
	return otParams
}

// TransferState returns the transfer status on a Snapmirror relationship if it exists
func (s *SnapmirrorRelationship) TransferState() string {
	if s == nil || s.Transfer == nil || s.Transfer.State == nil {
		return ""
	}
	return *s.Transfer.State
}

func ldapModifyParamsToONTAP(params *LdapModifyParams) *name_services.LdapModifyParams {
	skipConfigValidation := false
	otParams := name_services.NewLdapModifyParams()
	if params == nil {
		return otParams
	}
	otParams.SetSvmUUID(params.SvmUUID)
	otParams.SetInfo(
		&models.LdapService{
			SkipConfigValidation:                &skipConfigValidation,
			UserDn:                              params.UserDn,
			GroupDn:                             params.GroupDn,
			BaseDn:                              params.BaseDN,
			GroupMembershipFilter:               params.GroupMembershipFilter,
			LdapServiceInlinePreferredAdServers: params.PreferredServersForLdapClient,
			UseStartTLS:                         params.TLSEnabled,
			Schema:                              params.Schema,
			LdapServiceInlineServers:            params.LdapServers,
		})
	return otParams
}

// SecurityCertificateDeleteCollectionParams represents parameters for deleting certificates
type SecurityCertificateDeleteCollectionParams struct {
	Name         *string
	SvmName      *string
	Type         *string
	SerialNumber *string
}

// securityCertificateDeleteCollectionParamsToONTAP converts internal parameters to ONTAP REST API parameters
func securityCertificateDeleteCollectionParamsToONTAP(params *SecurityCertificateDeleteCollectionParams) *security.SecurityCertificateDeleteCollectionParams {
	otParams := security.NewSecurityCertificateDeleteCollectionParams()

	if params.Name != nil {
		otParams.SetName(params.Name)
	}
	if params.SvmName != nil {
		otParams.SetSvmName(params.SvmName)
	}
	if params.Type != nil {
		otParams.SetType(params.Type)
	}
	if params.SerialNumber != nil {
		otParams.SetSerialNumber(params.SerialNumber)
	}

	return otParams
}

func nameMappingCollectionGetParamsToONTAP(params *NameMappingCollectionGetParams) *name_services.NameMappingCollectionGetParams {
	otParams := name_services.NewNameMappingCollectionGetParams()
	if params == nil {
		return otParams
	}
	if params.SvmUUID != nil {
		otParams.SetSvmUUID(params.SvmUUID)
	}
	if params.SvmName != nil {
		otParams.SetSvmName(params.SvmName)
	}
	if params.Pattern != nil {
		otParams.SetPattern(params.Pattern)
	}
	if params.Replacement != nil {
		otParams.SetReplacement(params.Replacement)
	}
	if params.Direction != nil {
		otParams.SetDirection(params.Direction)
	}
	otParams.SetFields(params.Fields)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	return otParams
}

func nameMappingCreateParamsToONTAP(params *NameMappingCreateParams) *name_services.NameMappingCreateParams {
	otParams := name_services.NewNameMappingCreateParams()
	if params == nil {
		return otParams
	}
	info := &models.NameMapping{
		Index:       &params.Index,
		Pattern:     params.Pattern,
		Replacement: params.Replacement,
		Direction:   params.Direction,
	}
	if params.SvmUUID != nil {
		info.Svm = &models.NameMappingInlineSvm{
			UUID: params.SvmUUID,
		}
	}
	if params.SvmName != nil {
		if info.Svm == nil {
			info.Svm = &models.NameMappingInlineSvm{}
		}
		info.Svm.Name = params.SvmName
	}
	otParams.SetInfo(info)
	return otParams
}

// NvmeNamespace is a wrapper around the generated NvmeNamespace model.
type NvmeNamespace struct {
	models.NvmeNamespace
}

// NvmeNamespaceGetParams is the input parameter for listing NVMe namespaces.
type NvmeNamespaceGetParams struct {
	BaseParams
	SvmName       *string
	VolumeName    *string
	NamespaceName *string
}

// nvmeNamespaceGetParamsToONTAP converts NvmeNamespaceGetParams to ONTAP API parameters.
func nvmeNamespaceGetParamsToONTAP(params *NvmeNamespaceGetParams) *nvme.NvmeNamespaceCollectionGetParams {
	otParams := nvme.NewNvmeNamespaceCollectionGetParams()
	if params == nil {
		return otParams
	}

	otParams.SetSvmName(params.SvmName)
	otParams.SetLocationVolumeName(params.VolumeName)
	if params.NamespaceName != nil && *params.NamespaceName != "" {
		otParams.SetName(constructNamespaceName(params.VolumeName, params.NamespaceName))
	}
	otParams.SetFields(params.Fields)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	return otParams
}

// constructNamespaceName builds the full namespace path from volume and namespace name.
// NVMe namespace names follow the same "/vol/<volume>/<name>" convention as LUNs.
func constructNamespaceName(volumeName, namespaceName *string) *string {
	if volumeName == nil || namespaceName == nil {
		return nil
	}
	return nillable.ToPointer(lunNamePrefix + *volumeName + "/" + *namespaceName)
}
