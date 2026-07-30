package vsa

import (
	"context"

	ontaprestmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	expectedNodeCount  = 2
	ipSpaceName        = "Default"
	DefaultNetmask     = "255.255.255.255"
	iscsiServicePolicy = "default-data-iscsi"
	nfsServicePolicy   = "default-data-files"
	mgmtCifsServicePolicy = "default-mgmt-cifs"
	mgmtLdapServicePolicy = "default-mgmt-ldap"
)

type Provider interface {
	GetONTAPVersion() (*string, error)
	JobGet(jobUUID string) (*OntapJob, error)
	AreAllNodeUpAndRunning() (bool, error)
	IsAggregateOnline(aggregateName string) (bool, error)
	GetAggregates() ([]*Aggregate, error)
	GetAggregateByName(name string) (*Aggregate, error)
	UpdateAggregate(params UpdateAggregateParams) error
	GetNodes() ([]*Node, error)
	GetNodesWithClient(client ontapRest.RESTClient) ([]*Node, error)
	GetNodeByName(name string) (*Node, error)
	CreateSVM(params CreateSvmParams) (*ProviderResponse, error)
	CreateDataLIF(params CreateLifParams) (*Lif, error)
	CreateNetworkIpRoute(params CreateNetworkIPRouteParams) error
	CreateVolume(params CreateVolumeParams) (*VolumeResponse, error)
	CreateFlexCacheVolume(params CreateFlexCacheVolumeParams) (*VolumeResponse, error)
	UnmountVolume(volumeUUID string) (*OntapAsyncResponse, error)
	MountVolume(params MountVolumeParams) (*OntapAsyncResponse, error)
	DeleteVolume(volumeUUID, volumeName string) error
	DeleteFlexCacheVolume(volumeUUID, name string) (*OntapAsyncResponse, error)
	GetVolume(params GetVolumeParams) (*VolumeResponse, error)
	GetVolumeForExpertMode(params GetVolumeParams) (*VolumeResponse, error)
	GetCloneVolumeForExpertMode(params GetVolumeParams) (*VolumeResponse, error)
	GetVolumeNASDetails(volumeUUID string) (*VolumeNASDetails, error)
	GetVolumeSANDetails(svmName, volumeName string) (*VolumeSANDetails, error)
	GetVolumeEncryptionStatus(params GetVolumeParams) (*VolumeResponse, error)
	GetVolumes() ([]*Volume, error)
	UpdateVolume(params UpdateVolumeParams) error
	InitiateSplitVolume(volumeUUID string) (string, error)
	StopSplitVolume(volumeUUID string) error
	GetVolumeCloneInfo(volumeUUID string) (*VolumeResponseClone, error)
	UpdateFlexCacheVolume(params UpdateFlexCacheVolumeParams) (*OntapAsyncResponse, error)
	RevertVolume(params RevertVolumeParams) error
	UpdateVolumeEnableEncryption(params UpdateVolumeParams) error
	IgroupCreate(params IgroupCreateParams) (string, error)
	IgroupDelete(uuid string) error
	IgroupGet(name, svm *string) (*ontapRest.Igroup, error)
	IgroupExists(name string, svm *string) (bool, *ontapRest.Igroup, error)
	LunCreate(params LunCreateParams) (*LunResponse, error)
	LunGet(params LunGetParams) (*LunResponse, error)
	LunList(params LunGetParams) ([]*LunResponse, error)
	LunUpdate(params LunUpdateParams) error
	NamespaceList(params NvmeNamespaceGetParams) ([]*NvmeNamespaceResponse, error)
	IgroupAddInitiator(params IgroupAddInitiator) error
	IgroupDeleteInitiator(params IgroupDeleteInitiator) error
	LunMapCreate(params LunMapCreateParams) error
	LunMapDelete(params LunMapDeleteParams) error
	IscsiServiceCreate(svmUUID string) error
	CreateClusterPeer(params CreateClusterPeerParams) (*ClusterPeer, error)
	AcceptClusterPeer(params CreateClusterPeerParams) (*ClusterPeer, error)
	DeleteClusterPeer(clusterPeerUUID string) error
	GetClusterPeer(clusterPeerUUID string) (*ClusterPeer, error)
	ListClusterPeers() ([]*ClusterPeer, error)
	GetInterclusterLIFs(servicePolicyName string) ([]*InterclusterLif, error)
	CreateSvmPeering(srcClusterName, srcSVMName, dstSVMName string, snapmirrorApplication ontaprestmodels.SvmPeerApplications) error
	AcceptSvmPeering(srcSVMName, dstSVMName string) error
	GetSVMPeer(localSVMName, remoteSVMName *string) (*SvmPeer, error)
	ListSVMPeersByRemoteSVMName(remoteSVMName *string) ([]*SvmPeer, error)
	DeleteSVMPeer(svmPeerUUID string, force bool) error
	CreateSVMPeer(params CreateSVMPeerParams) (*SvmPeer, error)
	CreateVolumeReplication(params *CreateVolumeReplicationParams) (*VolumeReplication, error)
	AuthorizeVolumeReplication(params *CreateVolumeReplicationParams) (*VolumeReplication, error)
	DeleteVolumeReplication(params *DeleteVolumeReplicationParams) (*VolumeReplication, error)
	UpdateVolumeReplication(volRep *VolumeReplication) (*VolumeReplication, error)
	ReleaseVolumeReplication(params *ReleaseVolumeReplicationParams) (*VolumeReplication, error)
	ResyncVolumeReplication(volRep *VolumeReplication) (*VolumeReplication, error)
	ReverseVolumeReplication(volRep *VolumeReplication) (*SnapmirrorDestination, error)
	BreakVolumeReplication(volRep *VolumeReplication) (*VolumeReplication, error)
	AbortVolumeReplication(volRep *VolumeReplication) (*VolumeReplication, error)
	GetReplicationDetails(ctx context.Context, volRep *VolumeReplication) (*VolumeReplication, error)
	GetVolumeReplicationFromSrcAndDstPath(replication *VolumeReplication) (*VolumeReplication, error)
	GetVolumeReplication(replication *VolumeReplication) (*VolumeReplication, error)
	CreateSnapshot(params CreateSnapshotParams) (*SnapshotProviderResponse, error)
	DeleteSnapshot(snapshotUUID string, volumeUUID string) error
	GetSnapshot(snapshotUUID string, volumeUUID string) (*SnapshotProviderResponse, error)
	GetSnapshots(volumeUUID string) ([]*Snapshot, error)
	GetSnapshotsViaCLIAPI(ctx context.Context, volumeName string, svmName string) ([]*Snapshot, error)
	ListSnapmirrorSnapshots(volumeUUID string) ([]*SnapshotListResponse, error)
	CreateQuotaRule(ctx context.Context, params CreateQuotaRuleParams) (*JobStatus, error)
	GetQuotaRuleCollection(ctx context.Context, volumeUUID, svmName string) ([]*QuotaRuleCollectionItem, error)
	GetOntapQuotaUUIDAndType(ctx context.Context, volumeUUID, svmName, quotaType, target string) (string, string, error)
	UpdateQuotaRule(ctx context.Context, params *UpdateQuotaRuleParams) (*JobStatus, error)
	DeleteQuotaRule(ctx context.Context, quotaUUID string) (*JobStatus, error)
	GetQuotaStatus(ctx context.Context, volumeUUID string) (*QuotaStatus, error)
	QuotaEnableDisable(ctx context.Context, volumeUUID, svmName string, enable bool) (*JobStatus, error)
	UpdateSnapshotPolicy(ctx context.Context, params *UpdateSnapshotPolicyParams) error
	CloudTargetGet(name *string) (*ontapRest.CloudTarget, error)
	CloudTargetCreate(name, containerName string) (*ontapRest.CloudTarget, error)
	CloudTargetDelete(uuid string) (*OntapAsyncResponse, error)
	SnapmirrorRelationshipCreate(params *commonparams.SnapmirrorRelationshipParams, smcToken *string) (*ontapRest.SnapmirrorRelationship, error)
	SnapmirrorRelationshipGet(destinationPath, sourcePath string) (*ontapRest.SnapmirrorRelationship, error)
	ListSnapmirrorDestinations(params *ontapRest.SnapmirrorRelationshipListDestinationsParams) ([]*SnapmirrorDestination, error)
	SnapmirrorRelationshipTransferCreate(snapmirrorUUID, snapshotName string, smcToken *string) error
	SnapmirrorRelationshipTransferCreateWithFiles(snapmirrorUUID, snapshotName string, smcToken *string, files []*commonparams.SnapmirrorTransferFile) error
	SnapmirrorRelationshipTransferGet(snapmirrorUUID, snapshotName string) (*ontapRest.SnapmirrorTransfer, error)
	SnapmirrorRelationshipDelete(UUID string) (*OntapAsyncResponse, error)
	SnapmirrorObjectStoreEndpointDelete(objectStoreUUID, EndpointUUID string) (*OntapAsyncResponse, error)
	SnapmirrorObjectStoreSnapshotDelete(objectStoreUUID, EndpointUUID, snapshotUUID string) (*OntapAsyncResponse, error)
	SnapmirrorObjectStoreSnapshotGet(objectStoreUUID, EndpointUUID, snapshotUUID string) (*SmObjectStoreEndpointSnapshot, error)
	ObjectStoreEndpointInfoGet(objectStoreUUID, EndpointUUID string) (*SmObjectStoreEndpointt, error)
	CreateSnapshotPolicy(sp *SnapshotPolicy) error
	DeleteSnapshotPolicy(snapshotPolicyName string) error
	CreateKmsConfig(params CreateKmsConfigParams) (*CreateKmsConfigResponse, error)
	DeleteEkmConfig(params DeleteKmsConfigParams) error
	IsGcpKmsReachable(params GetKmsConfigParams) (bool, error)
	ModifyGcpKms(externalUUID string, credentials *log.Secret) (*ontapRest.GcpKms, *string, error)
	PostClusterLicenseAccessToken(ctx context.Context, clientSecret string) (*string, error)
	CreateDns(params CreateDnsParams) error
	CreateLdap(ad *datamodel.ActiveDirectory, volume *datamodel.Volume) error
	DeleteLdap(svmUUID string) error
	CreateQoSGroupPolicy(params CreateQoSGroupPolicyParams) (*QoSGroupPolicyResponse, error)
	ModifySVMWithQoSPolicy(params ModifySVMWithQoSPolicyParams) error
	ModifyRquota(ctx context.Context, svmUUID string, rquota bool) error
	FindQoSGroupPolicy(params FindQoSGroupPolicyParams) (*QoSGroupPolicyResponse, error)
	UpdateQoSGroupPolicy(params UpdateQoSGroupPolicyParams) error
	DeleteQoSGroupPolicy(params DeleteQoSGroupPolicyParams) error
	CreateExportPolicy(params *ExportPolicy) error
	UpdateExportPolicyRules(params UpdateExportPolicyRulesParams) error
	GetExportPolicyProtocols(policyName, svmName string) ([]string, error)
	DeleteExportPolicy(params *ExportPolicy) error
	CreateSecurityLogForwarding(params CreateSecurityLogForwardingParams) (*CreateSecurityLogForwardingResponse, error)
	GetSecurityLogForwarding(params GetSecurityLogForwardingParams) error
	CreateEMSEventForwarding(params CreateEMSEventForwardingParams) error
	GetEMSEventForwarding(destinationName string) (*EMSEventDestination, error)
	DeleteEMSEventForwarding(destinationName, filterName string) error
	UpdateSecurityAudit(params UpdateSecurityAuditParams) (*SecurityAudit, error)
	GetSecurityAudit() (*SecurityAudit, error)
	EnableAutoVolOfflineCronForGCPKMS() error
	GetClusterHealthStatus() (*ClusterHealthStatusResponse, error)
	GetClusterHealthStatusWithClient(client ontapRest.RESTClient) (*ClusterHealthStatusResponse, error)
	TriggerTakeoverCheck(targetNodeUUID string) (bool, error)
	TriggerTakeoverCheckWithClient(targetNodeUUID string, client ontapRest.RESTClient) (bool, error)
	UpdateJSwapMode(targetNodeUUID string, backingType JSWAPBackingType) (bool, error)
	UpdateJSwapModeWithClient(targetNodeUUID string, backingType JSWAPBackingType, client ontapRest.RESTClient) (bool, error)
	CreateRESTClient() (ontapRest.RESTClient, error)
	CreateRole(params CreateRoleParams) (string, error)
	GetRole(params GetRoleParams) (*Role, error)
	DeleteRole(params DeleteRoleParams) error
	GetRoleCollection(params GetRoleCollectionParams) ([]*Role, error)
	ModifyRolePrivilege(params ModifyRolePrivilegeParams) error
	DeleteRolePrivilege(params DeleteRolePrivilegeParams) error
	CreateRolePrivilege(params CreateRolePrivilegeParams) (string, error)
	GetCIFSService(svmName, externalSVMUUID string) (*ontapRest.CifsService, error)
	EnsureCIFSShare(params ConfigActiveDirectoryParams) (string, error)
	EnsureCifsServerNamePostFix(client ontapRest.RESTClient, ad *ActiveDirectory, svmName string) error
	CreateAndSetupCIFSServer(client ontapRest.RESTClient, ad *ActiveDirectory, externalSVMUUID, svmName string) (string, error)
	IsDDNSEnabled(client ontapRest.RESTClient, svmUUID string) bool
	CifsShareCollectionGet(svmUUID, shareName string, fields []string) ([]string, error)
	UpdateCIFSServer(svmUUID, shareName string, shareProperties []string) error
	UpdateActiveDirectoryCredentials(params UpdateActiveDirectoryCredentialsParams, cifs ontapRest.CifsService, svmName, svmExternalUUID string) error
	// Legacy certificate methods for backward compatibility
	InstallServerCertificate(params InstallServerCertificateParams) (*ServerCertificateResponse, error)
	InstallClientCA(params InstallClientCAParams) (*ServerCertificateResponse, error)
	InstallServerCA(params InstallServerCAParams) (*ServerCertificateResponse, error)
	GetServerCertificates(params GetServerCertificateParams) ([]*ServerCertificateResponse, error)
	DeleteServerCertificate(params DeleteServerCertificateParams) error
	ModifySSL(params ModifySSLParams) (*ModifySSLResponse, error)
}

type OntapRestProvider struct {
	Provider           ProviderDetails            `json:"Provider"`
	ClientParams       ontapRest.RESTClientParams `json:"ClientParams"`
	InsecureSkipVerify bool                       `json:"insecureSkipVerify"`
	Logger             log.Logger                 `json:"-"`
}

func NewProvider(ctx context.Context, provider ProviderDetails) *OntapRestProvider {
	logger := util.GetLogger(ctx)
	ontapRestProvider := &OntapRestProvider{
		Provider: provider,
		ClientParams: ontapRest.RESTClientParams{
			Hosts:              provider.Hosts,
			Host:               provider.IPAddress,
			Username:           provider.Username,
			InsecureSkipVerify: provider.InsecureSkipVerify,
			FastConnection:     provider.FastConnection,
			ServerName:         provider.ServerName,
			Trace:              logger,
			Ctx:                ctx,
		},
		Logger: logger,
	}
	if provider.Certificate != nil {
		ontapRestProvider.ClientParams.Certificate = &models.Certificate{
			SignedCertificate:        provider.Certificate.SignedCertificate,
			PrivateKey:               provider.Certificate.PrivateKey,
			InterMediateCertificates: provider.Certificate.InterMediateCertificates,
			CommonName:               provider.Certificate.CommonName,
		}
		ontapRestProvider.ClientParams.CertificateBasedAuthEnabled = true
	} else {
		ontapRestProvider.ClientParams.Password = log.Secret(provider.Password)
		ontapRestProvider.ClientParams.CertificateBasedAuthEnabled = false
	}
	return ontapRestProvider
}

// CreateRESTClient creates a new ONTAP REST client that can be reused across multiple API calls
func (rc *OntapRestProvider) CreateRESTClient() (ontapRest.RESTClient, error) {
	return getOntapClientFunc(rc.ClientParams)
}
