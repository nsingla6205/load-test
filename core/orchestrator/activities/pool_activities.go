package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"dario.cat/mergo"
	networkpriv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	privmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/hydrationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
	vmrs_config "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/config"
	vmrs_decision "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/decision"
	vmrs_oci "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalercommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/common"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	oci "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"gorm.io/gorm"
)

const (
	VMsPerHAPair                    = 2
	EnableServerAuthInCSR           = true  // server auth will be enabled in the CSR(Certificate Signing Request), by default client is enabled
	EnableServerAuthInCSRForExpMode = false // only client auth will be enabled in the CSR(Certificate Signing Request)
	certificate                     = "certificate"
	password                        = "password"

	// ociAdminCertUsernameSuffix mirrors factory/oci.VCP_ADMIN_CERT_UN_SUFFIX. It
	// is duplicated here (rather than imported) to avoid an activities→factory
	// import cycle; both must stay in sync. It is trimmed off the pool admin
	// username to recover the deployment-derived base username.
	ociAdminCertUsernameSuffix = "_admin"
	// ociServiceCertUsernameSuffix names the dedicated read-only ONTAP account
	// (and matching client-cert CN) minted for an external service:
	// "{base}_oci_svc". Distinct from the admin suffix so the service cert maps
	// to its own least-privilege identity.
	ociServiceCertUsernameSuffix = "_oci_svc"

	// ociReadonlyRoleName is the ONTAP built-in cluster-scoped read-only role
	// assigned to the service account (predefined on every cluster; no CreateRole).
	ociReadonlyRoleName = "readonly"
)

// Polling defaults for WaitForNodeDNS. Declared as `var` (not `const`) so unit
// tests can shrink them to keep the suite fast without forcing a poll/deadline
// parameter through the activity signature. Production code must never mutate
// these — they are package-private and intended as a test seam only.
// See pool_activities_internal_test.go for the override pattern.
var (
	waitForNodeDNSPollInterval       = 5 * time.Second  // WaitForNodeDNS poll interval
	waitForNodeDNSPerAttemptDeadline = 10 * time.Minute // WaitForNodeDNS per attempt deadline, capped inside the activity; outer Temporal retry still applies.

	// setupServiceEnsureReadOnlyAccount is a test seam over the read-only
	// certificate-auth ONTAP account helper used by SetupOciClientCertificateForOCI.
	setupServiceEnsureReadOnlyAccount = vsa.EnsureReadOnlyServiceAccount
)

var (
	DeploymentsInsert                        = common.DeploymentsInsert
	PrepareVlmConfig                         = _prepareVlmConfig
	ReadFile                                 = os.ReadFile
	SaveNodeDetails                          = _saveNodeDetails
	DeleteLIFs                               = _deleteLIFs
	DeleteSVMs                               = _deleteSVMs
	FailedSVMs                               = _failedSVMs
	DeleteNodes                              = _deleteNodes
	FailedNodes                              = _failedNodes
	DeletingNodes                            = _deletingNodes
	DeletingSVMs                             = _deletingSVMs
	CreateVPC                                = _createVPC
	InsertSubnet                             = _insertSubnet
	InsertFirewall                           = _insertFirewall
	GetTenantProject                         = _getTenantProject
	GetCreateDataSubnetworkOp                = _getCreateDataSubnetworkOp
	GetSubnetToBeUsed                        = getSubnetToBeUsed
	SetupNetworkFirewallsForIscsi            = setupNetworkFirewallsForIscsi
	SetupNetworkFirewallsForNFS              = setupNetworkFirewallsForNFS
	SetupNetworkFirewallsForIntercluster     = setupNetworkFirewallsForIntercluster
	SetupNetworkFirewallsForSMB              = setupNetworkFirewallsForSMB
	SetupNetworkFirewallsForNVMe             = setupNetworkFirewallsForNVMe
	SetupNetworkFirewallsForIlbHealthCheck   = setupNetworkFirewallsForIlbHealthCheck
	CreateGCPBucket                          = _createGCPBucket
	CheckReusableSubnet                      = _checkReusableSubnet
	CreateServiceAccountAndAttachRole        = _createServiceAccountAndAttachRole
	DeleteServiceAccountAndRemoveStorageRole = _deleteServiceAccountAndRemoveStorageRole
	DeleteGCPBucket                          = _deleteGCPBucket
	LoadVMRSConfig                           = vmrs_config.LoadConfig
	CreateDecisionMaker                      = vmrs_decision.NewDecisionMaker
	CreateDecisionMakerForFamily             = vmrs_decision.NewDecisionMakerForFamily
	MergeFirewallSourceRangesIfNeeded        = _mergeFirewallSourceRangesIfNeeded

	LoadOCIVMRSConfig = vmrs_oci.LoadConfig
	DecideOCIVM       = vmrs_oci.Decide
	// OCIVMRSConfigFilePath is the on-disk path of the OCI VMRS YAML
	// catalogue (see config/vmrs_oci.yaml). It reuses the same env var
	// the GCP workflow reads (VMRS_CONFIG_PATH); the OCI worker Helm
	// chart already wires that env to /config/vmrs_oci.yaml — see
	// kubernetes/OCI/vcp-worker-chart/templates/configMap.yaml. Each
	// worker binary runs against exactly one hyperscaler, so the single
	// env var carries the correct YAML for the running cloud.
	OCIVMRSConfigFilePath             = env.GetString("VMRS_CONFIG_PATH", "/config/vmrs_oci.yaml")
	CreateLargeVolumeVMRSConfig       = _createLargeVolumeVMRSConfig
	VlmConfigFilePath                 = env.GetString("VLM_CONFIG_FILE_PATH", "/common/vsa_config/vlm-config.json")
	ValidateVlmConfigInputs           = _validateVlmConfigInputs
	GetCreateSubnetworkOperation      = _getCreateSubnetworkOperation
	ReleaseSubnetOp                   = _releaseSubnetOp
	CheckAndUpdateFirewall            = _checkAndUpdateFirewall
	LoadVlmConfigFromFile             = loadVlmConfigFromFile
	GetServiceNetOpStatus             = _getServiceNetOpStatus
	GetComputeOpStatus                = _getComputeOpStatus
	GetSubnetFromOperation            = _getSubnetFromOperation
	GetGatewayFromIpCidrRange         = _getGatewayFromIpCidrRange
	ResolveZonesForCluster            = _resolveZonesForCluster
	GetInternalVSANetworkForFirewalls = _getInternalVSANetworkForFirewalls
	ListAddressesByDeployment         = _listAddressesByDeployment
	GetBucketFile                     = _getBucketFile

	// Feature flag to enforce minimum values for SPConfig throughput and IOPS.
	// Set ENFORCE_MIN_SP_CONFIG=true in the environment to enable.
	enforceMinSPConfig       = env.GetBool("ENFORCE_MIN_SP_CONFIG", false)
	EnableNfsOverTls         = env.GetBool("ENABLE_NFS_OVER_TLS", false)
	NfsTlsConnMaxLimit       = env.GetInt("NFS_TLS_CONN_MAX_LIMIT", 0)
	VsaImageProject          = env.GetString("VSA_IMAGE_PROJECT", "")
	MediatorImageProject     = env.GetString("VSA_MEDIATOR_IMAGE_PROJECT", "")
	IsIntegrationTest        = env.GetBool("INTEGRATION_TEST", false)
	maxNestedCloneLimit      = env.GetInt("MAX_NESTED_CLONE_LIMIT", 499)
	ExpertModeRbacBucketName = env.GetString("EXPERT_MODE_RBAC_BUCKET_NAME", "gcnv-autopush-images-bucket")
	ExpertModeRbacFilePath   = env.GetString("EXPERT_MODE_RBAC_FILE_PATH", "GCNV/%s/RBAC/gcnvadmin_create_cli")
	OntapModeRBACChecksums   = env.GetString("ONTAP_MODE_RBAC_CHECKSUMS", "{}")
	ValidateRbacHashFlag     = env.GetBool("VALIDATE_RBAC_HASH", false)

	ValidateImageDigestFlag = env.GetBool("VALIDATE_IMAGE_DIGEST", false)
	VsaImageChecksums       = env.GetString("VSA_IMAGE_CHECKSUMS", "")
	VsaImageName            = env.GetString("VSA_IMAGE_NAME", "")
	MediatorImageName       = env.GetString("VSA_MEDIATOR_IMAGE_NAME", "")
)

const (
	imageVerifiedLabel = "image_digest_verified"
	checksumLabel1     = "checksum1"
	checksumLabel2     = "checksum2"
)

// PoolVMRSSpec describes the capacity, performance, and topology of a pool
// for a VMRS decision. On create, the Current field in DecideOCIVMRSRequest is
// nil. On update it carries the pool's live values for context.
type PoolVMRSSpec struct {
	// SizeInBytes is the total pool capacity in bytes.
	SizeInBytes uint64
	// ThroughputMibps is the pool throughput. OCI: decimal MB/s (800, 1600, …) from
	// throughputGBps; GCP: MiB/s.
	ThroughputMibps int64
	// Iops is the requested pool IOPS (nil = use default).
	Iops *int64
	// HAPairs is the number of HA pairs.
	HAPairs int
	// IsRegionalHA, when true, means Active-Passive (one active VM per HA
	// pair). When false the pool is Active-Active (two active VMs per pair).
	IsRegionalHA bool
}

// DecideOCIVMRSRequest is the input to the DecideOCIVMRS activity.
type DecideOCIVMRSRequest struct {
	// PoolUUID identifies the pool (for logging and error messages).
	PoolUUID string
	// Current is nil on create. On update it carries the pool's live spec;
	// Current.ThroughputMibps and Current.Iops drive IOPS resolution:
	// same-tier update or downgrade with no explicit IOPS → clamp(Current.Iops,
	// DefaultIOPS, MaxIOPS) of the new tier; unknown (nil/0) → DefaultIOPS.
	Current *PoolVMRSSpec
	// Target is the desired pool spec that drives shape selection.
	Target PoolVMRSSpec
}

// ValidateVSAZonesForMachineType validates that primary and secondary zones support the VSA instance type
func ValidateVSAZonesForMachineType(cloudService hyperscaler2.Services, projectNumber, primaryZone, secondaryZone, instanceType string) error {
	// Validate primary zone supports the instance type
	isAvailable, err := cloudService.IsMachineTypeAvailable(projectNumber, primaryZone, instanceType)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to validate machine type availability in primary zone %s: %w", primaryZone, err))
	}
	if !isAvailable {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrZoneMachineTypeValidation, fmt.Errorf("primary zone %s does not support machine type %s", primaryZone, instanceType)))
	}

	// True-zonal pools use the same zone for both nodes; skip a duplicate GCP lookup.
	if secondaryZone == primaryZone {
		return nil
	}

	// Validate secondary zone supports the instance type
	isAvailable, err = cloudService.IsMachineTypeAvailable(projectNumber, secondaryZone, instanceType)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to validate machine type availability in secondary zone %s: %w", secondaryZone, err))
	}
	if !isAvailable {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrZoneMachineTypeValidation, fmt.Errorf("secondary zone %s does not support machine type %s", secondaryZone, instanceType)))
	}

	return nil
}

// ValidateZonesForMachineTypes is an activity method that validates VSA zones support the machine type
func (j *PoolActivity) ValidateZonesForMachineTypes(ctx context.Context, projectNumber, primaryZone, secondaryZone, instanceType string) error {
	activity.RecordHeartbeat(ctx, "Starting ValidateZonesForMachineTypes activity and Getting cloud service")
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to initialize cloud service: %w", err))
	}
	recordHeartbeat(ctx, fmt.Sprintf("Validating zones %s and %s for machine type %s", primaryZone, secondaryZone, instanceType))
	err = ValidateVSAZonesForMachineType(cloudService, projectNumber, primaryZone, secondaryZone, instanceType)
	if err != nil {
		return err
	}
	activity.RecordHeartbeat(ctx, "Finished ValidateZonesForMachineTypes activity")
	return nil
}

// DNSResolver is the narrow seam WaitForNodeDNS uses to look up node FQDNs.
// *net.Resolver satisfies this naturally, so production code does not need to
// inject anything explicit; tests inject a fake to drive the polling loop
// (NXDOMAIN / timeout / non-DNS-error / deadline-exceeded) without hitting the
// OS resolver. See pool_activities_internal_test.go.
type DNSResolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

type PoolActivity struct {
	SE database.Storage

	// DNSResolver is used by WaitForNodeDNS. Leave nil in production — it
	// falls back to &net.Resolver{PreferGo: true}. Tests inject a fake to
	// control the polling loop deterministically.
	DNSResolver DNSResolver
}

type InternalVSANetwork struct {
	VpcName     string
	SubnetName  string
	IpCidrRange string
	Firewall    hyperscaler_models.Firewall
}

const (
	FirewallPriority        = 1000
	IngressTrafficDirection = "INGRESS"

	volStyleFlexGroup = "flexgroup"
	volStyleFlexVol   = "flexvol"

	keyManagerBootarg          = "bootarg.keymanager.ekmip.svm_context=false"
	nfsTlsBootarg              = "bootarg.nfs.tls.enabled=true"
	nfsTlsConnLimitBootargKey  = "bootarg.nblade.nfs_tls_conn_max_limit"
	s3RestUserAuthCheckBootarg = "bootarg.s3.rest.user_auth_check.enabled=false"

	MgmtVpcName      = "mgmt-e0a-vpc-01"
	MgmtSubnetName   = "mgmt-e0a-subnet-01"
	MgmtFirewallName = "ingress-" + MgmtVpcName

	IcVpcName      = "ic-e0b-vpc-01"
	IcSubnet       = "ic-e0b-subnet-01"
	IcFirewallName = "ingress-" + IcVpcName

	RsmVpcName      = "rsm-e0c-vpc-01"
	RsmSubnetName   = "rsm-e0c-subnet-01"
	RsmFirewallName = "ingress-" + RsmVpcName

	iscsiDataFirewallName    = "ingress-data-iscsi"
	nfsDataFirewallName      = "ingress-data-nfs"
	interclusterFirewallName = "ingress-intercluster"
	nvmeDataFirewallName     = "ingress-data-nvme"

	AllowAllPorts = "all"
)

// Minimum allowed values for SPConfig throughput (in MiBs) and IOPS.
// These are enforced only if the feature flag above is enabled.
const (
	minSPConfigThroughput = 1120
	minSPConfigIOps       = 24000
)

var (
	totalIPPerHAPair          = env.GetInt("TOTAL_IP_PER_HA_PAIR", 6)
	mediatorVmInstanceType    = env.GetString("VSA_MEDIATOR_INSTANCE_TYPE", "e2-micro")
	mediatorVmDiskType        = env.GetString("VSA_MEDIATOR_DISK_TYPE", "pd-ssd")
	clusterSerialNumberPrefix = env.GetString("CLUSTER_SERIAL_NUMBER_PREFIX", "935")
	Region                    = env.GetString("LOCAL_REGION", "")
	regionMapJson             = env.GetString("REGION_NUMBER_MAP", utils.DefaultRegionNumberMap)
	AggregateName             = env.GetString("AGGREGATE_NAME", "aggr1")

	// addressSpaceMgmtEnabled is intentionally read per-call (not cached at startup)
	// so that t.Setenv in tests can control it without a process restart.
	addressSpaceMgmtEnabled = func() bool { return env.GetBool(env.EnvAddressSpaceMgmtEnabled, false) }

	MgmtFirewallSourceRanges = env.GetString("MGMT_FIREWALL_SOURCE_RANGES", "")
	RsmFirewallSourceRanges  = env.GetString("RSM_FIREWALL_SOURCE_RANGES", "")
	IcFirewallSourceRanges   = env.GetString("IC_FIREWALL_SOURCE_RANGES", "")
	DataFirewallSourceRanges = env.GetString("DATA_FIREWALL_SOURCE_RANGES", "")

	MgmtRegionalNatIP = env.GetString("MGMT_REGIONAL_NAT_IP", "")

	MgmtNetworkIpRange = env.GetString("MGMT_NETWORK_IP_RANGE", "198.18.0.0/20")
	RsmNetworkIpRange  = env.GetString("RSM_NETWORK_IP_RANGE", "198.18.16.0/20")
	IcNetworkIpRange   = env.GetString("IC_NETWORK_IP_RANGE", "198.18.32.0/20")

	MgmtFirewallPortRules         = env.GetString("MGMT_FIREWALL_PORT_RULES", "tcp,22,443")
	RSMFirewallPortRules          = env.GetString("RSM_FIREWALL_PORT_RULES", "tcp,udp")
	IcFirewallPortRules           = env.GetString("IC_FIREWALL_PORT_RULES", "tcp,udp")
	InterclusterFirewallPortRules = env.GetString("INTERCLUSTER_FIREWALL_PORT_RULES", "tcp,10566,11104,11105,icmp")

	IscsiFirewallPortRules                       = env.GetString("ISCSI_FIREWALL_PORT_RULES", "tcp,3260")
	NFSFirewallPortRules                         = env.GetString("NFS_FIREWALL_PORT_RULES", "tcp,111,635,2049,4045,63001-65000,udp,111,4046,4049")
	SmbFirewallAllowedPortRulesConfig            = env.GetString("SMB_FIREWALL_ALLOWED_PORT_RULES", "tcp,88,135,139,389,445,464,636,udp,53,88,389,464")
	NvmeFirewallPortRules                        = env.GetString("NVME_FIREWALL_PORT_RULES", "tcp,4420")
	IlbHealthCheckFirewallSourceRangesConfig     = env.GetString("ILB_HEALTH_CHECK_FIREWALL_SOURCE_RANGES", "130.211.0.0/22,35.191.0.0/16")
	IlbHealthCheckFirewallAllowedPortRulesConfig = env.GetString("ILB_HEALTH_CHECK_FIREWALL_ALLOWED_PORT_RULES", "tcp")
	RegionNumber                                 = getRegionNumber()
)

var InternalVSANetworks = map[string]InternalVSANetwork{
	MgmtVpcName: {VpcName: MgmtVpcName, SubnetName: MgmtSubnetName, IpCidrRange: MgmtNetworkIpRange,
		Firewall: hyperscaler_models.Firewall{Name: MgmtFirewallName, SourceRanges: []string{}, AllowedPortRules: strings.Split(MgmtFirewallPortRules, ",")}},
	IcVpcName: {VpcName: IcVpcName, SubnetName: IcSubnet, IpCidrRange: IcNetworkIpRange,
		Firewall: hyperscaler_models.Firewall{Name: IcFirewallName, SourceRanges: strings.Split(IcFirewallSourceRanges, ","), AllowedPortRules: strings.Split(IcFirewallPortRules, ",")}},
	RsmVpcName: {VpcName: RsmVpcName, SubnetName: RsmSubnetName, IpCidrRange: RsmNetworkIpRange,
		Firewall: hyperscaler_models.Firewall{Name: RsmFirewallName, SourceRanges: strings.Split(RsmFirewallSourceRanges, ","), AllowedPortRules: strings.Split(RSMFirewallPortRules, ",")}},
}

func (j *PoolActivity) CreatingPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	se := j.SE
	pool, err := se.CreatingPool(ctx, pool)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return pool, nil
}

func (j *PoolActivity) FailedPool(ctx context.Context, pool *datamodel.Pool, errMsg string) error {
	se := j.SE
	if commonparams.IsPOPool(pool) {
		alreadyDeleted, checkErr := poolAlreadySoftDeleted(ctx, se, pool)
		if checkErr != nil {
			return vsaerrors.WrapAsTemporalApplicationError(checkErr)
		}
		if alreadyDeleted {
			restored, restoreErr := se.RestoreDeletedPoolAndMarkError(ctx, pool.UUID, errMsg)
			if restoreErr != nil {
				return vsaerrors.WrapAsTemporalApplicationError(restoreErr)
			}
			if restored != nil {
				pool = restored
			}
			if err := FailedSVMs(ctx, se, pool); err != nil {
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
			if err := FailedNodes(ctx, se, pool); err != nil {
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
			return nil
		}
	}

	pool.State = datamodel.LifeCycleStateError
	_, err := se.ErroredResource(ctx, pool, errMsg)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// mark SVMs as failed SVMs
	if err := FailedSVMs(ctx, se, pool); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// mark nodes as failed nodes
	if err := FailedNodes(ctx, se, pool); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// poolAlreadySoftDeleted reports whether the pool row is already soft-deleted.
// Used by FailedPool for PO delete rollback after DeletePoolResources.
func poolAlreadySoftDeleted(ctx context.Context, se database.Storage, pool *datamodel.Pool) (bool, error) {
	if se == nil || pool == nil || strings.TrimSpace(pool.UUID) == "" {
		return false, nil
	}
	if (pool.DeletedAt != nil && pool.DeletedAt.Valid) || pool.State == datamodel.LifeCycleStateDeleted {
		return true, nil
	}
	_, err := se.GetPoolByUUID(ctx, pool.UUID)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func (j *PoolActivity) FailedPoolActivity(ctx context.Context, pool *datamodel.Pool, errMsg string) error {
	se := j.SE
	pool.State = datamodel.LifeCycleStateError
	_, err := se.ErroredResource(ctx, pool, errMsg)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func (j *PoolActivity) CreatedPool(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Starting CreatedPool activity")
	se := j.SE
	activity.RecordHeartbeat(ctx, "Marking Pool as ready in the database")
	pool, err := se.CreatedPool(ctx, pool)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if vlmConfig != nil {
		// Save VLMConfig here, so that it can be reused.
		marshalledVlmConfig, err := json.Marshal(*vlmConfig)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		pool.VLMConfig = string(marshalledVlmConfig)
		pool, err = se.UpdatedPool(ctx, pool)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	activity.RecordHeartbeat(ctx, "Finished CreatedPool activity")
	return pool, nil
}

// getPasswordFromPoolCredentials retrieves the password from pool credentials, fetching from secret manager
func getPasswordFromPoolCredentials(ctx context.Context, poolCredentials *datamodel.PoolCredentials) (string, error) {
	password := poolCredentials.Password
	if poolCredentials.AuthType == env.USERNAME_PWD_SEC_MGR || poolCredentials.AuthType == env.USER_CERTIFICATE {
		if poolCredentials.SecretID != "" {
			secret, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, poolCredentials.SecretID)
			if err != nil {
				return "", fmt.Errorf("failed to get password from secret manager: %w", err)
			}
			password = secret
		}
	}

	if password == "" {
		return "", fmt.Errorf("password is empty (authType=%d)", poolCredentials.AuthType)
	}

	return password, nil
}

// SetWaflMaxVolCloneHier sets the wafl.maxvolclonehier option on the ONTAP cluster
// For certificate-based auth, it uses password-based auth for admin CLI commands since certificate users lack admin privileges
func (j *PoolActivity) SetWaflMaxVolCloneHier(ctx context.Context, node *models.Node, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Initializing WAFL maxvolclonehier configuration")
	logger := util.GetLogger(ctx)
	if node == nil {
		logger.Warnf("SetWaflMaxVolCloneHier: node is nil, skipping")
		return nil
	}

	nodeCopy := *node
	// Deep copy the EndpointAddressesToHostNameMap to avoid sharing the map reference
	if node.EndpointAddressesToHostNameMap != nil {
		nodeCopy.EndpointAddressesToHostNameMap = make(map[string]string)
		for k, v := range node.EndpointAddressesToHostNameMap {
			nodeCopy.EndpointAddressesToHostNameMap[k] = v
		}
	}

	if nodeCopy.AuthType == env.USER_CERTIFICATE {
		activity.RecordHeartbeat(ctx, "Using password-based auth for admin CLI command")
		logger.Debugf("SetWaflMaxVolCloneHier: Certificate auth detected, falling back to password auth for admin command")

		if pool == nil || pool.PoolCredentials == nil {
			logger.Warnf("SetWaflMaxVolCloneHier: Pool or pool credentials are nil, cannot fallback to password auth")
			return fmt.Errorf("cannot fallback to password auth: pool or pool credentials are nil")
		}

		password, err := getPasswordFromPoolCredentials(ctx, pool.PoolCredentials)
		if err != nil {
			logger.Warnf("SetWaflMaxVolCloneHier failed to get password: %v", err)
			return fmt.Errorf("failed to get password for cert-auth fallback: %w", err)
		}

		// Override AuthType and set password on the node copy
		nodeCopy.AuthType = env.USERNAME_PWD
		nodeCopy.Password = password
		logger.Debugf("SetWaflMaxVolCloneHier: Overridden node AuthType to USERNAME_PWD for admin CLI command")
	}

	activity.RecordHeartbeat(ctx, "Getting ONTAP provider")
	provider, err := vsa.GetProviderByNode(ctx, &nodeCopy)
	if err != nil {
		logger.Errorf("SetWaflMaxVolCloneHier failed to get provider: %v", err)
		return nil
	}

	activity.RecordHeartbeat(ctx, "Creating REST client")
	restClient, err := provider.CreateRESTClient()
	if err != nil {
		logger.Errorf("SetWaflMaxVolCloneHier failed to create REST client: %v", err)
		return nil
	}
	if restClient == nil {
		logger.Warnf("SetWaflMaxVolCloneHier: REST client is nil")
		return nil
	}

	activity.RecordHeartbeat(ctx, "Getting networking client")
	networkingClient := restClient.Networking()
	if networkingClient == nil {
		logger.Warnf("SetWaflMaxVolCloneHier: networking client is nil")
		return nil
	}

	activity.RecordHeartbeat(ctx, "Executing CLI command to set WAFL maxvolclonehier")
	nodeName := "*" // Applying maxvolclonehier to all the available nodes
	cliInput := fmt.Sprintf("system node run -node %s -command options wafl.maxvolclonehier %d", nodeName, maxNestedCloneLimit)
	cliPrivilege := "admin"
	cliExecuteBody := &privmodels.CliExecute{
		Input:     &cliInput,
		Privilege: &cliPrivilege,
	}

	cliParams := networkpriv.NewCliExecuteParamsWithContext(ctx).
		WithBody(cliExecuteBody).
		WithTimeout(30 * time.Second)

	response, err := networkingClient.CliExecute(cliParams)
	if err != nil {
		logger.Errorf("SetWaflMaxVolCloneHier failed to execute CLI command: %v", err)
		return nil
	}
	if response == nil || response.Payload == nil {
		logger.Warnf("SetWaflMaxVolCloneHier received empty response")
		return nil
	}
	activity.RecordHeartbeat(ctx, "WAFL maxvolclonehier configured successfully")

	logger.Infof("wafl.maxvolclonehier updated successfully for node %s to %d, response: %s", nodeName, maxNestedCloneLimit, response.Payload.Output)
	return nil
}

func (j *PoolActivity) ErroredPool(ctx context.Context, pool *datamodel.Pool, errMessage string) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Starting ErroredPool activity")
	se := j.SE
	activity.RecordHeartbeat(ctx, "Marking Pool as error in the database")
	res, err := se.ErroredResource(ctx, pool, errMessage)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	dbPool := res.(*datamodel.Pool)
	activity.RecordHeartbeat(ctx, "Finished ErroredPool activity")
	return dbPool, nil
}

func (j *PoolActivity) DegradedPool(ctx context.Context, pool *datamodel.Pool, errMessage string) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Starting DegradedPool activity")
	logger := util.GetLogger(ctx)
	logger.Warn("Marking pool as degraded", "poolUUID", pool.UUID, "reason", errMessage)
	activity.RecordHeartbeat(ctx, "Marking Pool as degraded in the database")
	dbPool, err := j.SE.UpdatePoolState(ctx, pool, datamodel.LifeCycleStateDegraded, datamodel.LifeCycleStateDegradedDetails)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished DegradedPool activity")
	return dbPool, nil
}

func (j *PoolActivity) DeletePoolResourcesOnRollback(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Starting DeletePoolResourcesOnRollback activity")
	se := j.SE

	// Delete LIFs
	activity.RecordHeartbeat(ctx, "Deleting LIFs")
	if err := DeleteLIFs(ctx, se, pool); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Delete SVMs
	activity.RecordHeartbeat(ctx, "Deleting SVMs")
	if err := DeleteSVMs(ctx, se, pool); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Delete nodes
	activity.RecordHeartbeat(ctx, "Deleting Nodes")
	if err := DeleteNodes(ctx, se, pool); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func (j *PoolActivity) UpdatedPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Starting UpdatedPool activity")
	se := j.SE
	activity.RecordHeartbeat(ctx, "Updating Pool in the database")
	pool, err := se.UpdatedPool(ctx, pool)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished UpdatedPool activity")
	return pool, nil
}

// UpdateAutoTieringHotTierInfo atomically patches customer-owned hot tier fields in auto_tiering_config.
func (j *PoolActivity) UpdateAutoTieringHotTierInfo(ctx context.Context, poolUUID string, hotTierSizeInBytes *int64, enableHotTierAutoResize *bool) error {
	activity.RecordHeartbeat(ctx, "Starting UpdateAutoTieringHotTierInfo activity")
	se := j.SE
	err := se.UpdateAutoTieringHotTierInfo(ctx, poolUUID, hotTierSizeInBytes, enableHotTierAutoResize)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished UpdateAutoTieringHotTierInfo activity")
	return nil
}

func (j *PoolActivity) ParseVlmConfig(ctx context.Context, pool *datamodel.Pool) (*vlm.VLMConfig, error) {
	activity.RecordHeartbeat(ctx, "Starting ParseVlmConfig activity")
	log := util.GetLogger(ctx)

	currentVlmConfig := &vlm.VLMConfig{}

	activity.RecordHeartbeat(ctx, "Unmarshalling VLM config from pool")
	// First attempt: unmarshal as-is
	if err := json.Unmarshal([]byte(pool.VLMConfig), currentVlmConfig); err != nil {
		log.Errorf("VLM config unmarshal failed after patching for pool %s: %v", pool.Name, err)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError, err))
	}

	activity.RecordHeartbeat(ctx, "Finished ParseVlmConfig activity")
	return currentVlmConfig, nil
}

func (j *PoolActivity) SavePoolVLMConfig(ctx context.Context, pool *datamodel.Pool, vlmConfig vlm.VLMConfig) error {
	activity.RecordHeartbeat(ctx, "Starting SavePoolVLMConfig activity")
	marshalledVlmConfig, err := json.Marshal(vlmConfig)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Persisting VLM config to pool")
	if err := j.SE.UpdatePoolFields(ctx, pool.UUID, map[string]interface{}{"vlm_config": string(marshalledVlmConfig)}); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished SavePoolVLMConfig activity")
	return nil
}

func (j *PoolActivity) CompletePoolMaintenance(ctx context.Context, pool *datamodel.Pool, vlmConfig vlm.VLMConfig) error {
	activity.RecordHeartbeat(ctx, "Starting CompletePoolMaintenance activity")
	marshalledVlmConfig, err := json.Marshal(vlmConfig)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	updates := map[string]interface{}{
		"vlm_config":    string(marshalledVlmConfig),
		"state":         datamodel.LifeCycleStateREADY,
		"state_details": datamodel.LifeCycleStateAvailableDetails,
	}
	if err := j.SE.UpdatePoolFields(ctx, pool.UUID, updates); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished CompletePoolMaintenance activity")
	return nil
}

func (j *PoolActivity) UpdatedPoolWithVLMConfig(ctx context.Context, pool *datamodel.Pool, vlmConfig vlm.VLMConfig, updatePoolParams *commonparams.UpdatePoolParams) (*datamodel.Pool, error) {
	se := j.SE
	activity.RecordHeartbeat(ctx, "Starting UpdatedPoolWithVLMConfig activity")
	marshalledVlmConfig, err := json.Marshal(vlmConfig)
	if err != nil {
		return nil, err
	}

	pool.VLMConfig = string(marshalledVlmConfig)
	pool.SizeInBytes = int64(updatePoolParams.SizeInBytes)
	pool.Description = updatePoolParams.Description
	if pool.PoolAttributes == nil {
		pool.PoolAttributes = &datamodel.PoolAttributes{}
	}
	// ThroughputMibps field name is historical; value is decimal MB/s for OCI.
	pool.PoolAttributes.ThroughputMibps = updatePoolParams.TotalThroughputMibps
	if updatePoolParams.TotalIops != nil {
		pool.PoolAttributes.Iops = *updatePoolParams.TotalIops
	}
	if updatePoolParams.Labels != nil {
		pool.PoolAttributes.Labels = updatePoolParams.Labels
	}
	if pool.AutoTieringConfig == nil {
		pool.AutoTieringConfig = &datamodel.AutoTieringConfig{}
	}
	if updatePoolParams.AllowAutoTiering {
		pool.AllowAutoTiering = true
		pool.AutoTieringConfig.HotTierSizeInBytes = int64(updatePoolParams.HotTierSizeInBytes)
		pool.AutoTieringConfig.EnableHotTierAutoResize = updatePoolParams.EnableHotTierAutoResize
		if err := se.UpdateAutoTieringHotTierInfo(ctx, pool.UUID, nillable.GetInt64Ptr(pool.AutoTieringConfig.HotTierSizeInBytes), nillable.GetBoolPtr(pool.AutoTieringConfig.EnableHotTierAutoResize)); err != nil {
			return nil, err
		}
	} else {
		pool.AutoTieringConfig.HotTierSizeInBytes = int64(updatePoolParams.SizeInBytes)
		if err := se.UpdateAutoTieringHotTierInfo(ctx, pool.UUID, nillable.GetInt64Ptr(pool.AutoTieringConfig.HotTierSizeInBytes), nil); err != nil {
			return nil, err
		}
	}

	activity.RecordHeartbeat(ctx, "Starting pool update with new VLM config")
	updatedPool, err := se.UpdatedPool(ctx, pool)
	if err != nil {
		return nil, err
	}

	activity.RecordHeartbeat(ctx, "Finished UpdatedPoolWithVLMConfig activity")
	return updatedPool, nil
}

func buildOCINodeAttributeUpdates(vlmConfig vlm.VLMConfig) map[string]datamodel.NodeDetails {
	updates := make(map[string]datamodel.NodeDetails)
	instanceType := vlmConfig.Deployment.VSAInstanceType
	for _, haPair := range vlmConfig.Cloud.HAPairs {
		for _, vmConfig := range []vlm.VMConfig{haPair.VM1, haPair.VM2} {
			if vmConfig.HostName == "" {
				continue
			}
			var sizeInGiB int64
			for _, disk := range vmConfig.DataDisks {
				sizeInGiB += int64(disk.Size)
			}
			updates[vmConfig.HostName] = datamodel.NodeDetails{
				InstanceType: instanceType,
				SizeInGiB:    sizeInGiB,
			}
		}
	}
	return updates
}

func (j *PoolActivity) RestoreOCINodesPreUpdateFields(ctx context.Context, poolID int64, snapshotByNodeName map[string]datamodel.NodeDetails) error {
	activity.RecordHeartbeat(ctx, "Starting RestoreOCINodesPreUpdateFields activity")
	if len(snapshotByNodeName) == 0 {
		return nil
	}
	if err := j.SE.UpdateNodesSizeAndInstanceType(ctx, poolID, snapshotByNodeName); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished RestoreOCINodesPreUpdateFields activity")
	return nil
}

func (j *PoolActivity) UpdateOCINodesFromVLMConfig(ctx context.Context, poolID int64, vlmConfig vlm.VLMConfig) error {
	se := j.SE
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UpdateOCINodesFromVLMConfig activity")

	updates := buildOCINodeAttributeUpdates(vlmConfig)
	if len(updates) == 0 {
		logger.Infof("No node attribute updates derived from VLM config for pool ID %d; skipping", poolID)
		return nil
	}

	if err := se.UpdateNodesSizeAndInstanceType(ctx, poolID, updates); err != nil {
		logger.Errorf("Failed to update node size and instance type for pool ID %d: %v", poolID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully updated size and instance type for %d nodes in pool ID %d", len(updates), poolID)
	activity.RecordHeartbeat(ctx, "Finished UpdateOCINodesFromVLMConfig activity")
	return nil
}

type fabricPoolSecretPayload struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func (j *PoolActivity) ValidateOCIFabricPoolSecret(ctx context.Context, secretOCID string, secretVersion *int64) (*vlm.FabricPoolConfig, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting ValidateOCIFabricPoolSecret activity — secretOCID: %s", secretOCID))
	logger := util.GetLogger(ctx)

	ociService, err := hyperscaler2.GetOCIService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err))
	}

	var secret *oci.OCICustomSecret
	if secretVersion != nil {
		logger.Infof("Fetching fabric pool secret from OCI Vault (pinned version %d) — secretOCID: %s", *secretVersion, secretOCID)
		secret, err = ociService.GetSecretWithCustomVersion(secretOCID, *secretVersion)
	} else {
		logger.Infof("Fetching fabric pool secret from OCI Vault (latest version) — secretOCID: %s", secretOCID)
		secret, err = ociService.GetSecretWithLatestVersion(secretOCID)
	}

	if err != nil {
		logger.Errorf("Failed to fetch fabric pool secret from OCI Vault: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if secret == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
				fmt.Errorf("ValidateOCIFabricPoolSecret: secret is inactive, pending deletion, or version not found — OCID: %s", secretOCID)))
	}

	var payload fabricPoolSecretPayload
	if err := json.Unmarshal([]byte(secret.Value), &payload); err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError,
				fmt.Errorf("ValidateOCIFabricPoolSecret: secret payload is not valid JSON (OCID: %s, version: %d): %w",
					secretOCID, secret.Version, err)))
	}
	if payload.AccessKey == "" || payload.SecretKey == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
				fmt.Errorf("ValidateOCIFabricPoolSecret: secret payload must contain non-empty access_key and secret_key (OCID: %s, version: %d)",
					secretOCID, secret.Version)))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Finished ValidateOCIFabricPoolSecret activity — secretOCID: %s, resolvedVersion: %d", secretOCID, secret.Version))
	return &vlm.FabricPoolConfig{SecretOcid: secretOCID}, nil
}

func (j *PoolActivity) UpdateNodesInstanceTypeActivity(ctx context.Context, poolID int64, newInstanceType string) error {
	se := j.SE
	logger := util.GetLogger(ctx)

	logger.Debugf("Updating nodes instance type for pool ID %d to %s", poolID, newInstanceType)
	activity.RecordHeartbeat(ctx, "Starting UpdateNodesInstanceTypeActivity activity")

	err := se.UpdateNodesInstanceType(ctx, poolID, newInstanceType)
	if err != nil {
		logger.Errorf("Failed to update nodes instance type: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully updated nodes instance type for pool ID %d", poolID)
	activity.RecordHeartbeat(ctx, "Finished UpdateNodesInstanceTypeActivity activity")
	return nil
}

func (j *PoolActivity) UpdatingPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Initializing pool update operation")
	se := j.SE
	pool, err := se.UpdatingPool(ctx, pool)
	activity.RecordHeartbeat(ctx, "Updated pool state in database")
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return pool, nil
}

func (j *PoolActivity) UpdatePoolState(ctx context.Context, pool *datamodel.Pool, state string, stateDetails string) (*datamodel.Pool, error) {
	se := j.SE
	pool, err := se.UpdatePoolState(ctx, pool, state, stateDetails)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return pool, nil
}

// FindTenancy finds the tenancy unit for a customer
func (j *PoolActivity) FindTenancyProject(ctx context.Context, params commonparams.CreatePoolParams) (string, error) {
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Finding tenant project - consumer project: %s, VPC name: %s", params.AccountName, params.VendorSubNetID))
	return GetTenantProject(service, params)
}

func _getTenantProject(service hyperscaler2.Services, params commonparams.CreatePoolParams) (string, error) {
	tenantProjectNumber, err := service.GetTenantProject(params.VendorSubNetID, params.AccountName, params.Region)
	if err != nil {
		service.GetLogger().Errorf("Error finding tenancy unit. Project: %s vpc: %s Error: %v", params.AccountName, params.VendorSubNetID, err)
		return "", err
	}
	service.GetLogger().Debugf("Found tenancy: tenantProjectNumber: %s for consumer project: %s", tenantProjectNumber, params.AccountName)
	return tenantProjectNumber, nil
}

// GetAvailableSubnet identifies current available subnets and re-uses subnet if IP CIDR range is available
func (j *PoolActivity) GetAvailableSubnet(ctx context.Context, params commonparams.CreatePoolParams, tenantProjectNumber string) (*hyperscaler_models.Subnet, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Checking for reusable data subnet - tenantProject: %s, region: %s", tenantProjectNumber, params.Region))
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return CheckReusableSubnet(j.SE, service, params, tenantProjectNumber)
}

func _checkReusableSubnet(se database.Storage, service hyperscaler2.Services, params commonparams.CreatePoolParams, tenantProjectNumber string) (*hyperscaler_models.Subnet, error) {
	var subnet *hyperscaler_models.Subnet
	logger := service.GetLogger()
	recordHeartbeat(service.GetContext(), fmt.Sprintf("Fetching SN host project for tenantProject: %s", tenantProjectNumber))
	snHostProject, err := service.GetSnHost(tenantProjectNumber)
	if err != nil {
		if !strings.Contains(err.Error(), "not found") {
			service.GetLogger().Errorf("Error getting service networking host project for tenant project: %s Error: %v", tenantProjectNumber, err)
			return nil, err
		}
	}
	customerProjectNumber := params.AccountName
	tenantProjectRegion := params.Region
	isLargeCapacity := params.LargeCapacity
	if snHostProject != "" {
		addressRanges := fetchAddressRangesForSubnetFilter(service.GetContext(), se, params.VendorSubNetID)

		// if snHost is found, check if the subnetwork already exists in the SN host project and reuse it if applicable
		recordHeartbeat(service.GetContext(), fmt.Sprintf("Checking for reusable subnet in SN host: %s, region: %s", snHostProject, tenantProjectRegion))
		subnet, err = GetSubnetToBeUsed(service, se, customerProjectNumber, tenantProjectNumber, snHostProject, tenantProjectRegion, isLargeCapacity, addressRanges)
		if err != nil {
			logger.Errorf("Error getting data subnet for tenant project: %s, SN host : %s, Region %s. Error : %s", tenantProjectNumber, snHostProject, tenantProjectRegion, err.Error())
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
		}
		if subnet != nil {
			logger.Infof("_checkReusableSubnet: reusing existing subnet=%s CIDR=%s tenantProject=%s snHost=%s", subnet.Name, subnet.IpCidrRange, tenantProjectNumber, snHostProject)
		} else {
			logger.Infof("_checkReusableSubnet: no reusable subnet found — a new subnet will be created. tenantProject=%s snHost=%s region=%s largeCapacity=%v addressRangeCount=%d", tenantProjectNumber, snHostProject, tenantProjectRegion, isLargeCapacity, len(addressRanges))
		}
	}
	return subnet, nil
}

// fetchAddressRangesForSubnetFilter returns the registered address ranges for the VPC identified by
// vendorSubNetID when address space management is enabled. The returned slice is used by
// getSubnetToBeUsed to restrict subnet reuse to only subnets carved from a managed range.
// Returns nil (unrestricted reuse) when the feature is disabled, the network URL is absent or
// malformed, or the DB call fails — all cases are logged so silent bypasses are visible.
func fetchAddressRangesForSubnetFilter(ctx context.Context, se database.Storage, vendorSubNetID string) []*datamodel.AddressRange {
	logger := util.GetLogger(ctx)
	if !addressSpaceMgmtEnabled() || vendorSubNetID == "" {
		logger.Infof("fetchAddressRangesForSubnetFilter: address space management disabled or no network — subnet reuse unrestricted. addressSpaceMgmtEnabled=%v network=%s", addressSpaceMgmtEnabled(), vendorSubNetID)
		return nil
	}
	hostProjectNumber, vpcName, parseErr := utils.ParseProjectId(vendorSubNetID)
	if parseErr != nil {
		// A malformed VendorSubNetID means we cannot look up address ranges; log prominently so
		// the misconfiguration is visible rather than silently bypassing the range filter.
		logger.Warnf("fetchAddressRangesForSubnetFilter: could not parse VendorSubNetID — address range filter will not be applied and subnet reuse is unrestricted. network=%s error=%v", vendorSubNetID, parseErr)
		return nil
	}
	lifType := database.AddressRangeLifTypeDataLIF
	addressRanges, err := se.ListAddressRanges(ctx, hostProjectNumber, vpcName, nil, &lifType)
	if err != nil {
		logger.Warnf("fetchAddressRangesForSubnetFilter: failed to list address ranges for address space management; proceeding without range filter. network=%s error=%v", vendorSubNetID, err)
		return nil
	}
	rangeNames := make([]string, 0, len(addressRanges))
	for _, ar := range addressRanges {
		rangeNames = append(rangeNames, ar.Name)
	}
	logger.Infof("fetchAddressRangesForSubnetFilter: loaded %d address range(s) for subnet reuse filter: %v network=%s", len(addressRanges), rangeNames, vendorSubNetID)
	return addressRanges
}

// GetCreateDataSubnetOp creates a subnetwork for the tenant project
func (j *PoolActivity) GetCreateDataSubnetOp(ctx context.Context, params commonparams.CreatePoolParams, tenantProjectNumber string) (*string, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Creating data subnet via GCP Service Networking - pool: %s, tenantProject: %s, region: %s", params.Name, tenantProjectNumber, params.Region))
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return GetCreateDataSubnetworkOp(service, params, tenantProjectNumber)
}

func _getCreateDataSubnetworkOp(service hyperscaler2.Services, params commonparams.CreatePoolParams, tenantProjectNumber string) (*string, error) {
	tenantProjectRegion := params.Region
	consumerVPC := params.VendorSubNetID
	logger := service.GetLogger()
	// if snHost is not found or subnet found cannot be used, create a new subnetwork for the tenant project
	logger.Debugf("Handling creation of new subnetwork for pool : %s, tenant project: %s ", params.Name, tenantProjectNumber)
	logger.Debugf("CreateSubnetwork: passing requestedRanges=%v to GCP Service Networking for pool=%s network=%s", params.RequestedRanges, params.Name, consumerVPC)
	operationName, err := GetCreateSubnetworkOperation(service, tenantProjectNumber, consumerVPC, &tenantProjectRegion, params.LargeCapacity, params.RequestedRanges)
	if err != nil {
		logger.Errorf("Error creating subnetwork for pool: %s tenant project: %s, Region %s. Error : %s", params.Name, tenantProjectNumber, tenantProjectRegion, err.Error())
		return nil, err
	}
	return operationName, err
}

// GetSnHostProject returns the Shared-VPC (Service Networking) host project for a
// tenant project. Read-only.
func (j *PoolActivity) GetSnHostProject(ctx context.Context, tenantProjectNumber string) (string, error) {
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Getting SN host project for tenant project: %s", tenantProjectNumber))
	return service.GetSnHost(tenantProjectNumber)
}

// GetTenancyInfo gets the SN host and populates values in TenancyInfo struct
func (j *PoolActivity) GetTenancyInfo(ctx context.Context, tenantProjectNumber string, subnet *hyperscaler_models.Subnet) (*commonparams.TenancyInfo, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Fetching service-networking host project for tenantProject: %s", tenantProjectNumber))
	_, network, err := utils.ParseProjectId(subnet.Network)
	if err != nil {
		return nil, err
	}
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
	}
	logger := service.GetLogger()
	snHostProjectID, err := service.GetSnHost(tenantProjectNumber)
	if err != nil {
		return nil, err
	}
	if snHostProjectID == "" {
		logger.Errorf("Failed to find SN host project for tenant project: %s. IpCidrRange: %s, consumerPeeringNetwork: %s", tenantProjectNumber, subnet.IpCidrRange, subnet.Name)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("SN host project not found for tenant project : %s ", tenantProjectNumber))
	}
	logger.Infof("Subnet used for tenant project: tenantProjectNumber: %s SN host project : %s IpCidrRange: %s, consumerPeeringNetwork: %s", tenantProjectNumber, snHostProjectID, subnet.IpCidrRange, subnet.Name)
	return &commonparams.TenancyInfo{
		RegionalTenantProject: tenantProjectNumber,
		Network:               network,
		SubnetworkNames:       []string{subnet.Name},
		SnHostProject:         snHostProjectID,
		Gateway:               subnet.GatewayAddress,
		AllocatedSubnetCIDR:   subnet.IpCidrRange,
	}, nil
}

// UpdatePoolSubnet updates the subnet name for the pool in the database
func (j *PoolActivity) UpdatePoolSubnet(ctx context.Context, poolUUID string, tenancyDetails commonparams.TenancyInfo) error {
	recordHeartbeat(ctx, fmt.Sprintf("Updating subnet names for pool %s, snHostProject: %s", poolUUID, tenancyDetails.SnHostProject))
	err := j.SE.UpdatePoolSubnetNames(ctx, poolUUID, tenancyDetails.SnHostProject, tenancyDetails.SubnetworkNames)
	if err != nil {
		return err
	}
	return nil
}

// createSubnetwork generates a subnetwork name based on the tenant project number and region and triggers creation the subnet in SN host project. returns operation name
func _getCreateSubnetworkOperation(service hyperscaler2.Services, tenantProjectNumber, consumerVPC string, tenantProjectRegion *string, isLargeCapacity bool, requestedRanges []string) (*string, error) {
	subnetName := MakeSubnetName(tenantProjectNumber, isLargeCapacity)
	operationName, err := service.CreateTPSubnetOp(tenantProjectNumber, consumerVPC, *tenantProjectRegion, subnetName, isLargeCapacity, requestedRanges)
	if err != nil {
		service.GetLogger().Errorf("Error adding subnetwork: %v", err)
		return nil, err
	}
	return operationName, err
}

func (j *PoolActivity) CreateVPCs(ctx context.Context, project string) (*[]commonparams.Operations, error) {
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Record heartbeat to indicate progress to temporal server
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Setting up VPC's for VSA pool - tenant project: %s", project))
	operations := make([]commonparams.Operations, 0)
	op := ""
	for _, values := range InternalVSANetworks {
		recordHeartbeat(ctx, fmt.Sprintf("Creating VPC %s - project: %s", values.VpcName, project))
		// Create VPCs for management, cluster interconnect, and RSM
		op, err = CreateVPC(service, project, values.VpcName)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if op != "" {
			operations = append(operations, commonparams.Operations{
				OperationName:      op,
				OperationType:      "vpc",
				IsDone:             false,
				IsRegionalResource: false,
				Project:            project,
			})
		}
	}
	return &operations, nil
}

func (j *PoolActivity) CreateSubnets(ctx context.Context, project string) (*[]commonparams.Operations, error) {
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Record heartbeat to indicate progress to temporal server
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Setting up Subnets for VSA pool - tenant project: %s", project))
	operations := make([]commonparams.Operations, 0)
	op := ""
	for _, values := range InternalVSANetworks {
		recordHeartbeat(ctx, fmt.Sprintf("Creating subnet %s for VPC %s - project: %s", values.SubnetName, values.VpcName, project))
		op, err = InsertSubnet(service, project, &Region, values.SubnetName, values.VpcName, values.IpCidrRange)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if op != "" {
			operations = append(operations, commonparams.Operations{
				OperationName:      op,
				OperationType:      "subnet",
				IsDone:             false,
				IsRegionalResource: true,
				Project:            project,
			})
		}
	}
	return &operations, nil
}

func (j *PoolActivity) CreateFirewalls(ctx context.Context, project, snHostProject, network, poolMode string) (*[]commonparams.Operations, error) {
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Record heartbeat to indicate progress to temporal server
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Setting up Firewall for VSA pool - tenant project: %s, network: %s", project, network))
	operations := make([]commonparams.Operations, 0)
	op := ""
	internalVSANetworksLocal := PrepareInternalVSANetworksForFirewall()

	for _, values := range internalVSANetworksLocal {
		recordHeartbeat(ctx, fmt.Sprintf("Creating firewall %s for VPC %s - project: %s", values.Firewall.Name, values.VpcName, project))
		op, err = InsertFirewall(service, project, values.Firewall.Name, values.VpcName, values.Firewall.Priority, values.Firewall.Direction, values.Firewall.SourceRanges, values.Firewall.AllowedPortRules)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if op != "" {
			operations = append(operations, commonparams.Operations{
				OperationName:      op,
				OperationType:      "firewall",
				IsDone:             false,
				IsRegionalResource: false,
				Project:            project,
			})
		}
	}

	// Record heartbeat to indicate progress to temporal server
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Setting up network firewalls for iSCSI - tenant project: %s, SN host project: %s, network: %s", project, snHostProject, network))

	op, err = SetupNetworkFirewallsForIscsi(service, snHostProject, network)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "firewall",
			IsDone:             false,
			IsRegionalResource: false,
			Project:            snHostProject,
		})
	}

	op, err = SetupNetworkFirewallsForIntercluster(service, snHostProject, network)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "firewall",
			IsDone:             false,
			IsRegionalResource: false,
			Project:            snHostProject,
		})
	}

	// Setup NVMe firewall for expert mode (ONTAP mode) pools
	if poolMode == commonparams.ONTAPMode {
		// Record heartbeat to indicate progress to temporal server
		activity.RecordHeartbeat(ctx, "Setting up network firewalls for NVMe")
		op, err = SetupNetworkFirewallsForNVMe(service, snHostProject, network)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if op != "" {
			operations = append(operations, commonparams.Operations{
				OperationName:      op,
				OperationType:      "firewall",
				IsDone:             false,
				IsRegionalResource: false,
				Project:            snHostProject,
			})
		}
	}

	nasFirewallOps, err := j.SetupNasFirewalls(ctx, snHostProject, network)
	if err != nil {
		return nil, err
	}
	if nasFirewallOps != nil && len(*nasFirewallOps) > 0 {
		operations = append(operations, *nasFirewallOps...)
	}

	return &operations, nil
}

// PrepareInternalVSANetworksForFirewall adds private and public IPs for management VPC on top of the existing InternalVSANetworks
func PrepareInternalVSANetworksForFirewall() map[string]InternalVSANetwork {
	internalVSANetworksLocal := map[string]InternalVSANetwork{}
	mgmtValues := InternalVSANetworks[MgmtVpcName]

	// private firewall ned no restriction for port rules
	internalVSANetworksLocal[MgmtVpcName+"-1"] = GetInternalVSANetworkForFirewalls(mgmtValues.VpcName, mgmtValues.Firewall.Name+"-1", strings.Split(MgmtFirewallSourceRanges, ","), []string{AllowAllPorts}, FirewallPriority, IngressTrafficDirection)
	// public firewall needs to have restrictions using port rules
	internalVSANetworksLocal[MgmtVpcName+"-2"] = GetInternalVSANetworkForFirewalls(mgmtValues.VpcName, mgmtValues.Firewall.Name+"-2", strings.Split(MgmtRegionalNatIP, ","), mgmtValues.Firewall.AllowedPortRules, FirewallPriority, IngressTrafficDirection)
	internalVSANetworksLocal[IcVpcName] = InternalVSANetworks[IcVpcName]
	internalVSANetworksLocal[RsmVpcName] = InternalVSANetworks[RsmVpcName]
	return internalVSANetworksLocal
}

func _getInternalVSANetworkForFirewalls(vpcName, firewallName string, sourceRanges, portRules []string, priority int64, trafficDirection string) InternalVSANetwork {
	network := InternalVSANetworks[vpcName]
	return InternalVSANetwork{
		VpcName:     network.VpcName,
		SubnetName:  network.SubnetName,
		IpCidrRange: network.IpCidrRange,
		Firewall: hyperscaler_models.Firewall{
			Name:             firewallName,
			SourceRanges:     sourceRanges,
			AllowedPortRules: portRules,
			Priority:         priority,
			Direction:        trafficDirection,
		},
	}
}

// CreateOnTapCredentials creates ONTAP admin credentials for the pool based on the authentication type
func (j *PoolActivity) CreateOnTapCredentials(ctx context.Context, pool *datamodel.Pool) (*vlm.OntapCredentials, error) {
	consumerProject := ""
	if pool.Account != nil {
		consumerProject = pool.Account.Name
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting CreateOnTapCredentials activity - pool Name: %s, deployment: %s, consumer project: %s", pool.Name, pool.DeploymentName, consumerProject))
	credentials := &vlm.OntapCredentials{}
	cloudService, getGcpServiceErr := hyperscaler2.ResolveCloudService(ctx)
	if getGcpServiceErr != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, getGcpServiceErr))
	}

	switch pool.PoolCredentials.AuthType {
	case env.USER_CERTIFICATE:
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Generating and creating certificate for ONTAP credentials - pool Name: %s, deployment: %s, consumer project: %s", pool.Name, pool.DeploymentName, consumerProject))
		// Generate and create a certificate for the VSA cluster in CAS and fallthrough to generate and create the password for VSA cluster in Secret Manager as well
		certificate, err := vsa.GenerateAndCreateCertificateForVSACluster(cloudService, pool.DeploymentName, pool.PoolCredentials.Username, pool.PoolCredentials, EnableServerAuthInCSR)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		credentials = setPoolCredentials(certificate)
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Certificate generated and created successfully - pool Name: %s, deployment: %s, consumer project: %s", pool.Name, pool.DeploymentName, consumerProject))
		fallthrough
	case env.USERNAME_PWD_SEC_MGR:
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Generating password for ONTAP credentials in Secret Manager - pool Name: %s, deployment: %s, consumer project: %s", pool.Name, pool.DeploymentName, consumerProject))
		secret, err := vsa.GeneratePasswordForVSACluster(cloudService, pool.PoolCredentials.SecretID)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		credentials.AdminPassword = secret.SecretVersion.Value
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Password generated successfully - pool Name: %s, deployment: %s, consumer project: %s", pool.Name, pool.DeploymentName, consumerProject))
	default:
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Using default password for ONTAP credentials - pool Name: %s, deployment: %s, consumer project: %s", pool.Name, pool.DeploymentName, consumerProject))
		credentials.AdminPassword = pool.PoolCredentials.Password
	}
	activity.RecordHeartbeat(ctx, "Finished CreateOnTapCredentials activity")
	return credentials, nil
}

// OCICreatePoolCredentials wraps the ONTAP credentials together with the OCI
// Vault reference for the password secret and the OCI Certificates Service
// reference for the cluster certificate created during pool provisioning.
//
// OntapCredentials is intentionally a value (not a pointer) so that callers can
// safely access OntapCredentials.AdminPassword without a nil-check after
// workflow.Future.Get; a missing/zero payload yields a zero OntapCredentials,
// not a nil-deref panic inside the workflow goroutine.
type OCICreatePoolCredentials struct {
	OntapCredentials vlm.OntapCredentials       `json:"ontap_credentials"`
	Secret           *datamodel.ExternalCredRef `json:"secret,omitempty"`
	Certificate      *datamodel.ExternalCredRef `json:"certificate,omitempty"`
}

// CreateOnTapCredentialsForOCI creates ONTAP admin credentials for the OCI pool based on the authentication type
func (j *PoolActivity) CreateOnTapCredentialsForOCI(ctx context.Context, pool *datamodel.Pool) (*OCICreatePoolCredentials, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting CreateOnTapCredentialsForOCI activity - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))

	if pool.PoolCredentials == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool.PoolCredentials must not be nil")))
	}

	ociService, err := hyperscaler2.GetOCIService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err))
	}
	credentials := &OCICreatePoolCredentials{}

	switch pool.PoolCredentials.AuthType {
	case env.USER_CERTIFICATE:
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Generating and creating certificate for ONTAP credentials - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
		certName := ociOntapCertificateName(pool)
		certificate, err := vsa.CreateCertificateForVSAClusterOCI(ociService, pool.DeploymentName, certName, pool.PoolCredentials, EnableServerAuthInCSR)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if certificate == nil || certificate.Certificate == nil || certificate.Secret == nil || certificate.Secret.SecretVersion == nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
					fmt.Errorf("OCI Certificates Service returned an incomplete certificate response for deployment %q", pool.DeploymentName)))
		}
		credentials.OntapCredentials = *setPoolCredentials(certificate)
		credentials.Certificate = &datamodel.ExternalCredRef{
			Name:               certificate.Certificate.Name,
			Version:            certificate.Certificate.VersionNumber,
			ExternalIdentifier: certificate.Certificate.CertificateID,
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Certificate generated and created successfully - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
		fallthrough
	case env.USERNAME_PWD_SEC_MGR:
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Generating password for ONTAP credentials in OCI Vault - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
		secretName := ociOntapAdminSecretName(pool)
		secret, err := vsa.GeneratePasswordForVSAClusterOCI(ociService, secretName)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if secret == nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
					fmt.Errorf("OCI Vault returned nil secret without error for secret %q", secretName)))
		}
		credentials.OntapCredentials.AdminPassword = secret.Value
		credentials.Secret = &datamodel.ExternalCredRef{
			Name:               secret.Name,
			Version:            secret.Version,
			ExternalIdentifier: secret.Ocid,
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Password generated successfully in OCI Vault - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
	default:
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Using default password for ONTAP credentials, CreateOnTapCredentialsForOCI - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
		credentials.OntapCredentials.AdminPassword = pool.PoolCredentials.Password
	}

	if credentials.OntapCredentials.AdminPassword == "" && credentials.OntapCredentials.Certificate.Certificate == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrOCIOntapCredentialsNotFound,
				fmt.Errorf("ONTAP admin credentials could not be resolved for pool %q (deployment: %s, authType: %d): "+
					"the admin password is empty and no certificate was provisioned",
					pool.Name, pool.DeploymentName, pool.PoolCredentials.AuthType)))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Finished CreateOnTapCredentialsForOCI activity - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
	return credentials, nil
}

// ociBootstrapEndpointMap builds the endpoint-address → host/SNI map used by the
// OCI client-cert bootstrap provider, derived from the VLM deployment config.
func ociBootstrapEndpointMap(pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, deploymentName string, hostMap map[string]string) map[string]string {
	out := make(map[string]string)
	if vlmConfig == nil {
		return out
	}
	sniMode := env.GetHyperscaler() == commonparams.ProviderOCI &&
		env.OCIUseTLSSNIOverride &&
		pool.PoolCredentials != nil &&
		pool.PoolCredentials.AuthType == env.USER_CERTIFICATE
	sni := fmt.Sprintf("mgmt.%s.%s", deploymentName, env.VsaDeployedDnsName)
	for _, hp := range vlmConfig.Cloud.HAPairs {
		for _, vm := range []vlm.VMConfig{hp.VM1, hp.VM2} {
			ip := vm.SystemLIFs[vlm.LIFTypeNodeMgmt].IP
			if ip == "" {
				continue
			}
			switch {
			case sniMode:
				out[ip] = sni
			case hostMap[ip] != "":
				out[ip] = hostMap[ip]
			}
		}
	}
	return out
}

// ociClientCertConfig captures the only differences between the two OCI
// client-mTLS setup flows (VCP's own client cert vs the external OCI service's
// read-only client cert). The mint → bootstrap-provider → install-client_ca
// sequence is identical and lives in setupClientCertificateForOCI.
type ociClientCertConfig struct {
	label      string // used in heartbeats/errors, e.g. "VCP client" / "OCI service"
	certName   string // OCI certificate name (idempotent mint key)
	caCertName string // ONTAP client_ca trust name
	commonName string // cert CN, which maps to the ONTAP account identity
	// mint selects the issuer CA by delegating to the matching provider-factory
	// helper (VCP client issuer vs OCI service issuer).
	mint func(*oci.OciServices, string, string) (*hyperscaler_models.CustomCertificateResponse, error)
	// skipCAInstall skips the ONTAP client_ca install when the signing CA is
	// already trusted (e.g. the OCI service cert reuses the VCP client issuer CA).
	skipCAInstall bool
}

// setupClientCertificateForOCI is the shared core for both OCI client-mTLS
// flows: it mints an OCI-signed client certificate, builds a bootstrap provider
// authenticated with the server certificate, and (unless cfg.skipCAInstall)
// installs the certificate's issuing CA on ONTAP as a trusted client_ca. It
// returns the minted certificate and the provider so callers can perform
// flow-specific post-steps (e.g. creating a read-only identity).
func (j *PoolActivity) setupClientCertificateForOCI(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, deploymentName string, hostMap map[string]string, cfg ociClientCertConfig) (*hyperscaler_models.CustomCertificateResponse, vsa.Provider, error) {
	ociService, err := hyperscaler2.GetOCIService(ctx)
	if err != nil {
		return nil, nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err))
	}

	cert, err := cfg.mint(ociService, cfg.certName, cfg.commonName)
	if err != nil {
		return nil, nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if cert == nil || cert.Certificate == nil {
		return nil, nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
				fmt.Errorf("OCI returned an incomplete %s certificate for deployment %q", cfg.label, pool.DeploymentName)))
	}

	// Endpoints are resolved from the VLM deployment config because this activity
	// runs before the nodes are written to the database by SaveVSANodeDetails.
	endpointMap := ociBootstrapEndpointMap(pool, vlmConfig, deploymentName, hostMap)
	if len(endpointMap) == 0 {
		return nil, nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
				fmt.Errorf("no node endpoints resolved from VLM config for deployment %q; cannot install client_ca", pool.DeploymentName)))
	}

	// Bootstrap provider authenticates with the server certificate (ExternalClientCertificate
	// is intentionally left unset so _getProviderByNode does not select the not-yet-trusted client cert).
	node := &models.Node{
		EndpointAddressesToHostNameMap: endpointMap,
		DeploymentName:                 pool.DeploymentName,
		AuthType:                       env.USER_CERTIFICATE,
		Password:                       pool.PoolCredentials.Password,
		CaURI:                          pool.PoolCredentials.GetCaURIWithFallback(),
		ExternalCertificate:            pool.PoolCredentials.ExternalCertificate,
		ExternalSecret:                 pool.PoolCredentials.ExternalSecret,
	}
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if provider == nil {
		return nil, nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("provider is nil for pool %s", pool.UUID)))
	}

	// Install only the issuing CA (first entry in the chain). ONTAP rejects
	// intermediate certificates for client_ca installs, so the full chain must not
	// be passed. A deterministic CertificateName makes the install idempotent
	// across retries rather than relying solely on duplicate-error matching.
	chain := cert.Certificate.PemCertificateChain
	if len(chain) == 0 {
		return nil, nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
				fmt.Errorf("OCI %s certificate for deployment %q has an empty certificate chain; cannot install client_ca", cfg.label, pool.DeploymentName)))
	}

	if !cfg.skipCAInstall {
		if _, err := provider.InstallClientCA(vsa.InstallClientCAParams{
			CertificateName: cfg.caCertName,
			Certificate:     chain[0],
		}); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate") && !strings.Contains(strings.ToLower(err.Error()), "already exists") {
				return nil, nil, vsaerrors.WrapAsTemporalApplicationError(err)
			}
		}
	}

	return cert, provider, nil
}

// SetupVcpClientCertificateForOCI mints the VCP client mTLS cert, installs the
// client CA on ONTAP, and returns its reference. This is the certificate VCP
// itself presents when calling ONTAP.
func (j *PoolActivity) SetupVcpClientCertificateForOCI(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, deploymentName string, hostMap map[string]string) (*datamodel.ExternalCredRef, error) {
	if !env.OCISeparateMTLSClientCert {
		return nil, nil
	}
	if pool.PoolCredentials == nil || pool.PoolCredentials.AuthType != env.USER_CERTIFICATE {
		return nil, nil
	}
	activity.RecordHeartbeat(ctx, "Starting SetupVcpClientCertificateForOCI activity")

	cert, _, err := j.setupClientCertificateForOCI(ctx, pool, vlmConfig, deploymentName, hostMap, ociClientCertConfig{
		label:      "VCP client",
		certName:   ociOntapClientCertificateName(pool),
		caCertName: ociOntapClientCACertificateName(pool),
		commonName: pool.PoolCredentials.Username,
		mint:       vsa.CreateVcpClientCertificateForVSAClusterOCI,
	})
	if err != nil {
		return nil, err
	}

	activity.RecordHeartbeat(ctx, "Finished SetupVcpClientCertificateForOCI activity")
	return &datamodel.ExternalCredRef{
		Name:               cert.Certificate.Name,
		Version:            cert.Certificate.VersionNumber,
		ExternalIdentifier: cert.Certificate.CertificateID,
	}, nil
}

// SetupOciClientCertificateForOCI mints the dedicated read-only client mTLS cert
// used by an external (non-VCP) OCI service to call ONTAP, installs its issuing
// CA on ONTAP as a trusted client_ca (unless already trusted via the VCP client
// issuer), ensures the matching certificate-auth read-only ONTAP account, and
// returns the certificate reference. Gated by OCI_ENABLE_SERVICE_ONTAP_CERT and
// cert-auth pools (returns nil when disabled / non-cert-auth); otherwise
// fail-fast on mint, CA install, or account-create errors.
func (j *PoolActivity) SetupOciClientCertificateForOCI(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, deploymentName string, hostMap map[string]string) (*datamodel.ExternalCredRef, error) {
	if !env.OCIEnableServiceONTAPCert {
		return nil, nil
	}
	if pool.PoolCredentials == nil || pool.PoolCredentials.AuthType != env.USER_CERTIFICATE {
		return nil, nil
	}
	activity.RecordHeartbeat(ctx, "Starting SetupOciClientCertificateForOCI activity")

	// When the service cert reuses the VCP client issuer CA (and that path is
	// enabled), ONTAP already trusts the CA, so the client_ca install is skipped
	// to avoid a redundant, potentially conflicting trust entry.
	skipCAInstall := env.OCISeparateMTLSClientCert && env.OCIServiceIssuerCAOCID == env.OCIClientIssuerCAOCID

	accountName := ociOntapServiceAccountName(pool)
	cert, provider, err := j.setupClientCertificateForOCI(ctx, pool, vlmConfig, deploymentName, hostMap, ociClientCertConfig{
		label:         "OCI service",
		certName:      ociOntapServiceCertificateName(pool),
		caCertName:    ociOntapServiceCACertificateName(pool),
		commonName:    accountName,
		mint:          vsa.CreateOciClientCertificateForVSAClusterOCI,
		skipCAInstall: skipCAInstall,
	})
	if err != nil {
		return nil, err
	}

	// Bind the read-only ONTAP identity (CN → account, role=readonly) so the
	// service cert authenticates as a least-privilege account, distinct from the
	// pool admin even when the same issuer CA is shared.
	accountProvider, ok := provider.(vsa.ExpertModeAccountProvider)
	if !ok {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
				fmt.Errorf("provider for pool %s does not support security account management", pool.UUID)))
	}
	if err := setupServiceEnsureReadOnlyAccount(ctx, accountProvider, vsa.CreateReadOnlyServiceAccountParams{
		Username: accountName,
		RoleName: ociReadonlyRoleName,
	}); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished SetupOciClientCertificateForOCI activity")
	return &datamodel.ExternalCredRef{
		Name:               cert.Certificate.Name,
		Version:            cert.Certificate.VersionNumber,
		ExternalIdentifier: cert.Certificate.CertificateID,
	}, nil
}

// DeleteOnTapCredentialsForOCI deletes the ONTAP admin password secret stored in OCI Vault for the given pool.
// The secret name is derived from the pool DeploymentName.
// If the secret does not exist the activity succeeds silently (idempotent).
func (j *PoolActivity) DeleteOnTapCredentialsForOCI(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting DeleteOnTapCredentialsForOCI activity - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))

	if pool.PoolCredentials == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool.PoolCredentials must not be nil")))
	}

	ociService, err := hyperscaler2.GetOCIService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err))
	}
	switch pool.PoolCredentials.AuthType {
	case env.USER_CERTIFICATE:
		activity.RecordHeartbeat(ctx, "Revoking certificate from OCI Certificates Service")
		if err = vsa.RevokeCertificateFromCAS(ociService, pool.PoolCredentials); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if err = vsa.RevokeClientCertificateFromCAS(ociService, pool.PoolCredentials); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if err = vsa.RevokeServiceCertificateFromCAS(ociService, pool.PoolCredentials); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		activity.RecordHeartbeat(ctx, "Certificate revoked successfully")
		fallthrough
	case env.USERNAME_PWD_SEC_MGR:
		activity.RecordHeartbeat(ctx, "Deleting password from Vault")
		if err := vsa.DeletePasswordForVSAClusterOCI(ociService, pool.PoolCredentials); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		activity.RecordHeartbeat(ctx, "Password deleted successfully from Vault")
	default:
		activity.RecordHeartbeat(ctx, "No deletion needed for default password type")
		return nil
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Finished DeleteOnTapCredentialsForOCI activity - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
	return nil
}

// GetExpertModeCredentialsForOCI retrieves ONTAP expert mode credentials based on the authentication type for OCI
func (j *PoolActivity) GetExpertModeCredentialsForOCI(ctx context.Context, pool *datamodel.Pool, ociAdminPassword *commonparams.OciAdminPassword) (*vlm.OntapCredentials, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting GetExpertModeCredentialsForOCI activity - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
	credentials := &vlm.OntapCredentials{}

	ociService, err := hyperscaler2.GetOCIService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOCIClientInitializationError, err))
	}

	if ociAdminPassword == nil || ociAdminPassword.Ocid == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("ociAdminPassword is required for expert mode credentials")))
	}

	ociService.GetLogger().Infof("Fetching expert mode admin password from OCI Vault — secretOCID: %s, version: %d", ociAdminPassword.Ocid, ociAdminPassword.Version)
	secret, err := ociService.GetSecretWithCustomVersion(ociAdminPassword.Ocid, ociAdminPassword.Version)
	if err != nil {
		// 404 from OCI covers both "secret doesn't exist" and "caller lacks permission" —
		// neither resolves on retry, so mark non-retryable to surface the full OCI error.
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}
	if secret == nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrOCIVaultSecretInactive, fmt.Errorf("secret is inactive or pending deletion in OCI Vault — OCID: %s, version: %d", ociAdminPassword.Ocid, ociAdminPassword.Version)))
	}

	credentials.AdminPassword = secret.Value
	ociService.GetLogger().Infof("Expert mode admin password fetched successfully from OCI Vault for pool: %s", pool.PoolExternalIdentifier)
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Finished GetExpertModeCredentialsForOCI activity - pool Name: %s, deployment: %s", pool.Name, pool.DeploymentName))
	return credentials, nil
}

// GetOnTapCredentialsForOCI fetches ONTAP admin credentials for OCI pools.
// Mirrors GetOnTapCredentials (GCP) — thin activity wrapper around fetchOnTapCredentialsForOCI.
func (j *PoolActivity) GetOnTapCredentialsForOCI(ctx context.Context, pool *datamodel.Pool) (*vlm.OntapCredentials, error) {
	activity.RecordHeartbeat(ctx, "Starting GetOnTapCredentialsForOCI activity")
	credentials, err := fetchOnTapCredentialsForOCI(ctx, pool)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished GetOnTapCredentialsForOCI activity")
	return credentials, nil
}

// CreateExpertModeCredentials creates ONTAP expert mode credentials based on the authentication type
func (j *PoolActivity) CreateExpertModeCredentials(ctx context.Context, pool *datamodel.Pool, clusterName, username string) (*vlm.OntapCredentials, error) {
	activity.RecordHeartbeat(ctx, "Starting CreateExpertModeCredentials activity")
	credentials := &vlm.OntapCredentials{}
	cloudService, getGcpServiceErr := hyperscaler2.ResolveCloudService(ctx)
	if getGcpServiceErr != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, getGcpServiceErr))
	}

	if pool.ExpertModeCredentials == nil || pool.ExpertModeCredentials.ExpertModeCredential == nil || len(pool.ExpertModeCredentials.ExpertModeCredential) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("expert mode credentials are not provided")))
	}
	switch pool.ExpertModeCredentials.ExpertModeCredential[0].AuthType {
	case env.USER_CERTIFICATE:
		activity.RecordHeartbeat(ctx, "Generating and creating certificate for expert mode credentials")
		// Generate and create a certificate for the VSA cluster in CAS and fallthrough to generate and create the password for VSA cluster in Secret Manager as well
		expertPoolCredentials := &datamodel.PoolCredentials{
			CertificateID: pool.ExpertModeCredentials.ExpertModeCredential[0].CertificateID,
		}
		// Use pool's CaURI if available, otherwise it will fallback to env vars
		if pool.PoolCredentials != nil {
			expertPoolCredentials.CaURI = pool.PoolCredentials.CaURI
		}
		// Generate and create certificate for expert mode, that has only client auth in CSR - server auth is not needed for expert mode. Hence, passing EnableServerAuthInCSRForExpMode as false
		certificate, err := vsa.GenerateAndCreateCertificateForVSACluster(cloudService, clusterName, username, expertPoolCredentials, EnableServerAuthInCSRForExpMode)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		credentials = setPoolCredentials(certificate)
		credentials.AdminPassword = "" // Setting empty password as certificate is used for authentication
		activity.RecordHeartbeat(ctx, "Certificate generated and created successfully")
	case env.USERNAME_PWD_SEC_MGR:
		activity.RecordHeartbeat(ctx, "Generating password for expert mode credentials in Secret Manager")
		secret, err := vsa.GeneratePasswordForVSACluster(cloudService, pool.ExpertModeCredentials.ExpertModeCredential[0].SecretID)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		credentials.AdminPassword = secret.SecretVersion.Value
		activity.RecordHeartbeat(ctx, "Password generated successfully")
	default:
		activity.RecordHeartbeat(ctx, "Using default password for expert mode credentials")
		credentials.AdminPassword = pool.ExpertModeCredentials.ExpertModeCredential[0].Password
	}
	activity.RecordHeartbeat(ctx, "Finished CreateExpertModeCredentials activity")
	return credentials, nil
}

func setPoolCredentials(certificate *hyperscaler_models.CustomCertificateResponse) *vlm.OntapCredentials {
	credentials := &vlm.OntapCredentials{}
	credentials.Certificate.CommonName = certificate.Certificate.SubjectCommonName
	credentials.Certificate.Certificate = certificate.Certificate.PemCertificate
	credentials.Certificate.PrivateKey = certificate.Secret.SecretVersion.Value
	credentials.Certificate.InterMediateCertificate = certificate.Certificate.PemCertificateChain
	return credentials
}

// DeleteOnTapCredentials deletes ONTAP admin credentials for the pool based on the authentication type
func (j *PoolActivity) DeleteOnTapCredentials(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Starting DeleteOnTapCredentials activity")
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
	}
	switch pool.PoolCredentials.AuthType {
	case env.USER_CERTIFICATE:
		activity.RecordHeartbeat(ctx, "Revoking certificate and deleting from Secret Manager")
		// Revoke the certificates and delete the private key from secret manager and cache then fallthrough to delete the password from secret manager and cache
		err = vsa.RevokeCertificateAndDeleteFromCacheAndSecretManager(cloudService, pool.PoolCredentials)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		activity.RecordHeartbeat(ctx, "certificate revoked and deleted successfully")
		fallthrough
	case env.USERNAME_PWD_SEC_MGR:
		activity.RecordHeartbeat(ctx, "Deleting password from Secret Manager")
		err = vsa.DeletePasswordFromCacheAndSecretManager(cloudService, pool.PoolCredentials.SecretID)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		activity.RecordHeartbeat(ctx, "Password deleted successfully")
	default:
		activity.RecordHeartbeat(ctx, "No deletion needed for default password type")
		return nil
	}
	activity.RecordHeartbeat(ctx, "Finished DeleteOnTapCredentials activity")
	return nil
}

// DeleteExpertModeCredentials DeleteOnTapCredentials deletes ONTAP expert mode credentials for the pool based on the authentication type
func (j *PoolActivity) DeleteExpertModeCredentials(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Starting DeleteExpertModeCredentials activity")
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
	}
	if pool.ExpertModeCredentials == nil || pool.ExpertModeCredentials.ExpertModeCredential == nil || len(pool.ExpertModeCredentials.ExpertModeCredential) == 0 {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("expert mode credentials are not provided")))
	}
	switch pool.ExpertModeCredentials.ExpertModeCredential[0].AuthType {
	case env.USER_CERTIFICATE:
		activity.RecordHeartbeat(ctx, "Revoking certificate and deleting from Secret Manager for expert mode")
		// Revoke the certificates and delete the private key from secret manager and cache then fallthrough to delete the password from secret manager and cache
		// Create PoolCredentials from ExpertModeCredential, using pool's PoolCredentials for CaURI if available
		expertPoolCredentials := &datamodel.PoolCredentials{
			CertificateID: pool.ExpertModeCredentials.ExpertModeCredential[0].CertificateID,
		}
		if pool.PoolCredentials != nil {
			expertPoolCredentials.CaURI = pool.PoolCredentials.CaURI
		}
		err = vsa.RevokeCertificateAndDeleteFromCacheAndSecretManager(cloudService, expertPoolCredentials)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		activity.RecordHeartbeat(ctx, "certificate revoked and deleted successfully")
	case env.USERNAME_PWD_SEC_MGR:
		activity.RecordHeartbeat(ctx, "Deleting password from Secret Manager for expert mode")
		err = vsa.DeletePasswordFromCacheAndSecretManager(cloudService, pool.ExpertModeCredentials.ExpertModeCredential[0].SecretID)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		activity.RecordHeartbeat(ctx, "Password deleted successfully")
	default:
		activity.RecordHeartbeat(ctx, "No deletion needed for default password type")
		return nil
	}
	activity.RecordHeartbeat(ctx, "Finished DeleteExpertModeCredentials activity")
	return nil
}

// GetOnTapCredentials fetches ONTAP admin credentials for the pool based on the authentication type
func (j *PoolActivity) GetOnTapCredentials(ctx context.Context, pool *datamodel.Pool) (*vlm.OntapCredentials, error) {
	activity.RecordHeartbeat(ctx, "Starting GetOnTapCredentials activity")
	credentials, err := fetchOnTapCredentials(ctx, pool)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished GetOnTapCredentials activity")
	return credentials, nil
}

// GetExpertModeCredentials fetches ONTAP expert mode credentials based on the authentication type
func (j *PoolActivity) GetExpertModeCredentials(ctx context.Context, pool *datamodel.Pool) (*vlm.OntapCredentials, error) {
	activity.RecordHeartbeat(ctx, "Starting GetExpertModeCredentials activity")
	credentials, err := fetchExpertModeCredentials(ctx, pool)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished GetExpertModeCredentials activity")
	return credentials, nil
}

func (j *PoolActivity) PrepareCreateVSAExpertModeReq(ctx context.Context, vlmConfig vlm.VLMConfig, ontapCredentials vlm.OntapCredentials, expertModeCredentials vlm.OntapCredentials, pool *datamodel.Pool, bucketFileDetails *hyperscaler_models.BucketFileDetails) (*vlm.OntapExpertModeUserConfig, error) {
	recordHeartbeat(ctx, "Assembling expert mode VSA request")
	createVSAExpertModeRequest := &vlm.OntapExpertModeUserConfig{}
	createVSAExpertModeRequest.VLMConfig = vlmConfig
	createVSAExpertModeRequest.OntapCredentials = ontapCredentials
	createVSAExpertModeRequest.ExpertModeUserCredentials = expertModeCredentials
	if pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
		createVSAExpertModeRequest.AuthenticationType = certificate
	} else {
		createVSAExpertModeRequest.AuthenticationType = password
	}
	if pool.ExpertModeCredentials == nil || pool.ExpertModeCredentials.ExpertModeCredential == nil || len(pool.ExpertModeCredentials.ExpertModeCredential) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("expert mode credentials are not provided")))
	}
	createVSAExpertModeRequest.Username = pool.ExpertModeCredentials.ExpertModeCredential[0].Username

	if bucketFileDetails == nil || bucketFileDetails.FileHashSHA256 == "" || bucketFileDetails.FileUrl == "" || bucketFileDetails.BucketName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("exp mode rbac file details are missing")))
	}
	createVSAExpertModeRequest.RbacFileURL = fmt.Sprintf("gs://%s/%s", bucketFileDetails.BucketName, bucketFileDetails.FileUrl)
	createVSAExpertModeRequest.RbacFileChecksum = bucketFileDetails.FileHashSHA256
	return createVSAExpertModeRequest, nil
}

func (j *PoolActivity) GetRbacHash(ctx context.Context, ontapVersion string) (*hyperscaler_models.BucketFileDetails, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Fetching RBAC hash from GCS - ontapVersion: %s", ontapVersion))
	rbacFileurl := utils.GenerateRbacFilePath(ExpertModeRbacFilePath, ontapVersion)
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
	}
	bucketFileDetails, err := GetBucketFile(cloudService, ctx, ExpertModeRbacBucketName, rbacFileurl)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return bucketFileDetails, nil
}

// ValidateRbacHash validates if the hash from GetRbacHash matches the configured checksum in ConfigMap."
func (j *PoolActivity) ValidateRbacHash(ctx context.Context, ontapVersion string, bucketFileDetails *hyperscaler_models.BucketFileDetails) error {
	recordHeartbeat(ctx, fmt.Sprintf("Validating RBAC hash for ONTAP version %s", ontapVersion))
	logger := util.GetLogger(ctx)

	// Skip validation if flag is disabled
	if !ValidateRbacHashFlag {
		logger.Infof("RBAC hash validation is disabled, ontapVersion : %s", ontapVersion)
		return nil
	}

	// If bucketFileDetails is nil or hash is empty, return error
	if bucketFileDetails == nil || bucketFileDetails.FileHashSHA256 == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("bucket file details or hash is empty")))
	}

	// Read configured checksums from environment
	checksumsConfig := OntapModeRBACChecksums
	if checksumsConfig == "" || checksumsConfig == "{}" {
		errMsg := "ONTAP_MODE_RBAC_CHECKSUMS not configured"
		logger.Error(errMsg, "for ontapVersion", ontapVersion)
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New(errMsg)))
	}

	// Parse JSON configuration
	var checksumsMap map[string]string
	if err := json.Unmarshal([]byte(checksumsConfig), &checksumsMap); err != nil {
		logger.Errorf("Failed to parse ONTAP_MODE_RBAC_CHECKSUMS configuration, error : %v", err)
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrJSONParsingError, fmt.Errorf("failed to parse ONTAP_MODE_RBAC_CHECKSUMS configuration: %w", err)))
	}

	// Check if ONTAP version is configured
	configuredChecksum, exists := checksumsMap[ontapVersion]
	if !exists {
		errMsg := fmt.Sprintf("ONTAP version %s not found in ONTAP_MODE_RBAC_CHECKSUMS configuration", ontapVersion)
		logger.Error(errMsg)
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New(errMsg)))
	}

	// Compare checksums
	if configuredChecksum != bucketFileDetails.FileHashSHA256 {
		errMsg := fmt.Sprintf("RBAC hash mismatch for ONTAP version %s: expected %s, got %s", ontapVersion, configuredChecksum, bucketFileDetails.FileHashSHA256)
		logger.Error(errMsg)
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New(errMsg)))
	}

	logger.Info("RBAC hash validation passed", "ontapVersion", ontapVersion, "hash", bucketFileDetails.FileHashSHA256)
	return nil
}

func _getBucketFile(service hyperscaler2.Services, ctx context.Context, bucketName string, fileUrl string) (*hyperscaler_models.BucketFileDetails, error) {
	bucketFileDetails, err := service.GetFileFromBucket(ctx, bucketName, fileUrl)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return bucketFileDetails, nil
}

// UpdateRbacInPoolWithURL is the OCI counterpart to UpdateRbacCheckSumInPool:
// it stores the RBAC file URL exactly as provided (no gs:// synthesis) along
// with the checksum into Pool.BuildInfo. Used by the single-pool OCI RBAC
// refresh flow where the URL is sourced from the API caller (PAR).
func (j *PoolActivity) UpdateRbacInPoolWithURL(ctx context.Context, pool *datamodel.Pool, rbacFileURL string, rbacFileChecksum string) error {
	recordHeartbeat(ctx, fmt.Sprintf("Updating RBAC file URL and checksum for pool %s", pool.UUID))
	se := j.SE
	latestPool, err := se.GetPoolByUUID(ctx, pool.UUID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	vsaBuildInfo := latestPool.BuildInfo
	if vsaBuildInfo == nil {
		vsaBuildInfo = &datamodel.PoolBuildInfo{}
	}
	if strings.TrimSpace(rbacFileURL) == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, errors.New("rbacFileURL is required")))
	}
	vsaBuildInfo.RbacFileHash = rbacFileChecksum
	vsaBuildInfo.RbacFileUrl = rbacFileURL
	updates := map[string]interface{}{
		"build_info": vsaBuildInfo,
	}
	if err := se.UpdatePoolFields(ctx, pool.UUID, updates); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func (j *PoolActivity) UpdateRbacCheckSumInPool(ctx context.Context, pool *datamodel.Pool, bucketFileDetails *hyperscaler_models.BucketFileDetails) error {
	se := j.SE
	// Fetch the latest pool data to avoid overwriting concurrent changes to BuildInfo
	// (e.g., from upgrade workflows that may update VSABuildImage, MediatorBuildImage, etc.)
	latestPool, err := se.GetPoolByUUID(ctx, pool.UUID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Use the latest BuildInfo to preserve any concurrent updates
	vsaBuildInfo := latestPool.BuildInfo
	if vsaBuildInfo == nil {
		return vsaerrors.WrapAsTemporalApplicationError(errors.New("vsaBuildInfo is nil"))
	}
	vsaBuildInfo.RbacFileHash = bucketFileDetails.FileHashSHA256
	vsaBuildInfo.RbacFileUrl = fmt.Sprintf("gs://%s/%s", bucketFileDetails.BucketName, bucketFileDetails.FileUrl)

	updates := map[string]interface{}{
		"build_info": vsaBuildInfo,
	}
	err = se.UpdatePoolFields(ctx, pool.UUID, updates)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func (j *PoolActivity) UpdateZoneSwitchPoolAttributes(ctx context.Context, pool *datamodel.Pool, zoneSwitchState string) error {
	se := j.SE
	logger := util.GetLogger(ctx)

	if pool.PoolAttributes == nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("pool %s has nil PoolAttributes", pool.UUID))
	}

	recordHeartbeat(ctx, fmt.Sprintf("Updating zone switch state to %s for pool %s", zoneSwitchState, pool.UUID))

	pool.PoolAttributes.ZoneSwitchState = zoneSwitchState

	updates := map[string]interface{}{
		"pool_attributes": pool.PoolAttributes,
	}

	if err := se.UpdatePoolFields(ctx, pool.UUID, updates); err != nil {
		logger.Errorf("Failed to update pool %s with pool attribute zone switch state: %v", pool.UUID, err)
		return err
	}

	logger.Infof("Successfully updated zone switch state %s for pool %s", zoneSwitchState, pool.UUID)
	return nil
}

func setupNetworkFirewallsForIntercluster(service hyperscaler2.Services, snHostProject, network string) (string, error) {
	return InsertFirewall(service, snHostProject, interclusterFirewallName, network, FirewallPriority, IngressTrafficDirection, strings.Split(DataFirewallSourceRanges, ","), strings.Split(InterclusterFirewallPortRules, ","))
}

// setupNetworkFirewallsForIscsi sets up a firewall for iSCSI traffic in GCP
func setupNetworkFirewallsForIscsi(service hyperscaler2.Services, snHostProject, network string) (string, error) {
	return InsertFirewall(service, snHostProject, iscsiDataFirewallName, network, FirewallPriority, IngressTrafficDirection, strings.Split(DataFirewallSourceRanges, ","), strings.Split(IscsiFirewallPortRules, ","))
}

// setupNetworkFirewallsForNFS sets up a firewall for NFS traffic in GCP
func setupNetworkFirewallsForNFS(service hyperscaler2.Services, snHostProject, network string) (string, error) {
	return InsertFirewall(service, snHostProject, nfsDataFirewallName, network, FirewallPriority, IngressTrafficDirection, strings.Split(DataFirewallSourceRanges, ","), strings.Split(NFSFirewallPortRules, ","))
}

// setupNetworkFirewallsForSMB sets up a firewall for SMB traffic in GCP
func setupNetworkFirewallsForSMB(service hyperscaler2.Services, snHostProject, network string) (string, error) {
	return InsertFirewall(service, snHostProject, SmbFirewallName, network, FirewallPriority, IngressTrafficDirection, strings.Split(DataFirewallSourceRanges, ","), strings.Split(SmbFirewallAllowedPortRulesConfig, ","))
}

// setupNetworkFirewallsForNVMe sets up a firewall for NVMe traffic in GCP
// This is used for expert mode (ONTAP mode) pools to allow NVMe over TCP on port 4420
func setupNetworkFirewallsForNVMe(service hyperscaler2.Services, snHostProject, network string) (string, error) {
	return InsertFirewall(service, snHostProject, nvmeDataFirewallName, network, FirewallPriority, IngressTrafficDirection, strings.Split(DataFirewallSourceRanges, ","), strings.Split(NvmeFirewallPortRules, ","))
}

// setupNetworkFirewallsForIlbHealthCheck sets up a firewall for ILB health check traffic in GCP
func setupNetworkFirewallsForIlbHealthCheck(service hyperscaler2.Services, snHostProject, network string) (string, error) {
	return InsertFirewall(service, snHostProject, ILBHealthCheckFirewallName, network, FirewallPriority, IngressTrafficDirection, strings.Split(IlbHealthCheckFirewallSourceRangesConfig, ","), strings.Split(IlbHealthCheckFirewallAllowedPortRulesConfig, ","))
}

// SetupNasFirewalls sets up NAS-related firewalls (NFS, SMB, and ILB health check) for a pool.
// This is used when NAS infrastructure is being enabled for the first time (e.g., during upgrade from 9.17 to 9.18).
// The function is idempotent - it will not create duplicate firewalls if they already exist.
func (j *PoolActivity) SetupNasFirewalls(ctx context.Context, snHostProject, network string) (*[]commonparams.Operations, error) {
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	} // Record heartbeat to indicate progress to temporal server
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Setting up NAS firewalls (NFS, SMB, ILB health check) - project: %s, network: %s", snHostProject, network))
	operations := make([]commonparams.Operations, 0)
	op := ""

	// Setup NFS firewall
	recordHeartbeat(ctx, fmt.Sprintf("Setting up NFS firewall - project: %s, network: %s", snHostProject, network))
	op, err = SetupNetworkFirewallsForNFS(service, snHostProject, network)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "firewall",
			IsDone:             false,
			IsRegionalResource: false,
			Project:            snHostProject,
		})
	}

	// Setup SMB firewall
	recordHeartbeat(ctx, fmt.Sprintf("Setting up SMB firewall - project: %s, network: %s", snHostProject, network))
	op, err = SetupNetworkFirewallsForSMB(service, snHostProject, network)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "firewall",
			IsDone:             false,
			IsRegionalResource: false,
			Project:            snHostProject,
		})
	}

	// Setup ILB health check firewall
	recordHeartbeat(ctx, fmt.Sprintf("Setting up ILB health check firewall - project: %s, network: %s", snHostProject, network))
	op, err = SetupNetworkFirewallsForIlbHealthCheck(service, snHostProject, network)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if op != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      op,
			OperationType:      "firewall",
			IsDone:             false,
			IsRegionalResource: false,
			Project:            snHostProject,
		})
	}

	return &operations, nil
}
func (j *PoolActivity) DeployDeploymentManager(ctx context.Context, deploymentName, region, zone, network, subnet, projectId, snHostProject string, size int) (*[]map[string]string, error) {
	return DeploymentsInsert(ctx, deploymentName, region, zone, network, subnet, projectId, snHostProject, size)
}

func (j *PoolActivity) SavePoolWithClusterDetails(ctx context.Context, dbPool *datamodel.Pool, cluster *datamodel.ClusterDetails) error {
	activity.RecordHeartbeat(ctx, "Starting SavePoolWithClusterDetails activity")
	se := j.SE

	activity.RecordHeartbeat(ctx, "Saving pool with VSA details to database")
	err := se.SavePoolWithVsaDetails(ctx, dbPool, cluster)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished SavePoolWithClusterDetails activity")
	return nil
}

func (j *PoolActivity) GetIPsConsumedForSubnet(ctx context.Context, pool datamodel.Pool, tenancyDetails *commonparams.TenancyInfo, region string) (*[]datamodel.SubnetToIPs, error) {
	consumerProject := ""
	if pool.Account != nil {
		consumerProject = pool.Account.Name
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Starting GetIPsConsumedForSubnet activity - pool: %s, consumer project: %s", pool.Name, consumerProject))
	logger := util.GetLogger(ctx)
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
	}

	// Fetch all addresses with deployment ID filter only (no subnet filter)
	// This avoids the issue with incomplete subnet information in the API response
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Fetching addresses for deployment: %s - pool: %s, consumer project: %s", pool.DeploymentName, pool.Name, consumerProject))
	addresses, err := ListAddressesByDeployment(cloudService, tenancyDetails.RegionalTenantProject, region, pool.DeploymentName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	addressCount := 0
	if addresses != nil {
		addressCount = len(*addresses)
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Fetched %d addresses, filtering by subnet - pool: %s, consumer project: %s", addressCount, pool.Name, consumerProject))

	// If no subnetworkNAme
	if len(tenancyDetails.SubnetworkNames) == 0 {
		logger.Debugf("No subnetwork found for the pool: %s", pool.Name)
		activity.RecordHeartbeat(ctx, fmt.Sprintf("No subnetwork names provided, returning nil - pool: %s, consumer project: %s", pool.Name, consumerProject))
		return nil, nil
	}

	// Build result with only the target subnet
	subnetToIps := make([]datamodel.SubnetToIPs, 0)
	// Iterate through addresses and filter by the specific subnetwork
	if addresses != nil {
		for _, targetSubnetName := range tenancyDetails.SubnetworkNames {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Filtering addresses for subnet: %s - pool: %s, consumer project: %s", targetSubnetName, pool.Name, consumerProject))
			logger.Debugf("Filtering addresses for target subnet: %s", targetSubnetName)
			totalIPs := int64(0)
			for _, address := range *addresses {
				logger.Debugf("Address: %s, SubnetURI: %s, SelfLink: %s", address.AddressName, address.SubnetURI, address.SelfLink)

				// Check if this address belongs to our target subnet
				// Match by subnet name in the SubnetURI or SelfLink
				if strings.HasSuffix(address.SubnetURI, "/"+targetSubnetName) {
					totalIPs++
					logger.Debugf("Address %s matched target subnet %s", address.AddressName, targetSubnetName)
				}
			}
			subnetToIps = append(subnetToIps, datamodel.SubnetToIPs{
				SubnetName:  targetSubnetName,
				IPsReserved: totalIPs,
			})
			logger.Infof("Target subnet %s has %d reserved IPs", targetSubnetName, totalIPs)
		}
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Finished GetIPsConsumedForSubnet activity - pool: %s, consumer project: %s", pool.Name, consumerProject))
	return &subnetToIps, nil
}

func (j *PoolActivity) GetOntapVersion(ctx context.Context, node *models.Node) (*string, error) {
	activity.RecordHeartbeat(ctx, "Starting GetOntapVersion activity")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Fetching ONTAP version from provider")
	version, err := provider.GetONTAPVersion()
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished GetOntapVersion activity")
	return version, nil
}

// ValidateImageDigest validates that configured VSA and mediator image checksums match the ones in the image repository.
func (j *PoolActivity) ValidateImageDigest(ctx context.Context) (bool, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Validating VSA and mediator image checksums")
	activity.RecordHeartbeat(ctx, "Validating VSA and mediator image checksums")

	vsaCfg, medCfg, err := GetImageConfigChecksums()
	if err != nil {
		logger.Error("Failed to get image config checksums", "error", err)
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Fetching image checksums from repository")
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	vsaRepo, medRepo, err := GetImageRepoChecksums(ctx, cloudService)
	if err != nil {
		logger.Error("Failed to get image repo checksums", "error", err)
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if vsaCfg != vsaRepo || medCfg != medRepo {
		logger.Error("VSA and mediator image checksums do not match the ones in the image repository")
		return false, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("VSA image verification failed"))
	}
	logger.Info("Successfully verified VSA and mediator images")
	activity.RecordHeartbeat(ctx, "VSA and mediator image checksums verified")

	return true, nil
}

// GetImageConfigChecksums reads configured checksums from env
func GetImageConfigChecksums() (vsaChecksum string, mediatorChecksum string, err error) {
	if strings.TrimSpace(VsaImageChecksums) != "" && strings.TrimSpace(VsaImageChecksums) != "{}" {
		var payload struct {
			VSAImageChecksum         string `json:"VSA_IMAGE_CHECKSUM"`
			VSAMediatorImageChecksum string `json:"VSA_MEDIATOR_IMAGE_CHECKSUM"`
		}
		if err := json.Unmarshal([]byte(VsaImageChecksums), &payload); err != nil {
			return "", "", fmt.Errorf("failed to unmarshal configured VSA image checksums: %w", err)
		}
		vsaChecksum = strings.TrimSpace(payload.VSAImageChecksum)
		mediatorChecksum = strings.TrimSpace(payload.VSAMediatorImageChecksum)
	}

	if vsaChecksum == "" || mediatorChecksum == "" {
		return "", "", fmt.Errorf("VSA or mediator image checksums are not configured")
	}

	return vsaChecksum, mediatorChecksum, nil
}

// GetImageRepoChecksums fetches md5sum labels for VSA and mediator images from GCE Images API.
func GetImageRepoChecksums(ctx context.Context, cloudService hyperscaler2.Services) (vsaChecksum string, mediatorChecksum string, err error) {
	if VsaImageProject == "" || VsaImageName == "" {
		return "", "", fmt.Errorf("vsa image details are not configured")
	}

	if MediatorImageProject == "" || MediatorImageName == "" {
		return "", "", fmt.Errorf("mediator image details are not configured")
	}

	vsaCtx, vsaCancel := context.WithTimeout(ctx, 60*time.Second)
	defer vsaCancel()
	vsaLabels, err := cloudService.GetImageLabels(vsaCtx, VsaImageProject, VsaImageName)
	if err != nil {
		return "", "", fmt.Errorf("failed to get VSA image details from repo: %w", err)
	}
	vsaChecksum, err = GetImageChecksum(vsaLabels)
	if err != nil {
		return "", "", fmt.Errorf("failed to get VSA image checksum from repo: %w", err)
	}

	mediatorCtx, mediatorCancel := context.WithTimeout(ctx, 60*time.Second)
	defer mediatorCancel()
	mediatorLabels, err := cloudService.GetImageLabels(mediatorCtx, MediatorImageProject, MediatorImageName)
	if err != nil {
		return "", "", fmt.Errorf("failed to get mediator image details from repo: %w", err)
	}
	mediatorChecksum, err = GetImageChecksum(mediatorLabels)
	if err != nil {
		return "", "", fmt.Errorf("failed to get mediator image checksum from repo: %w", err)
	}

	return vsaChecksum, mediatorChecksum, nil
}

// GetImageChecksum extracts and validates the checksum from image labels.
func GetImageChecksum(labels map[string]string) (string, error) {
	if len(labels) == 0 {
		return "", fmt.Errorf("image labels are empty")
	}

	if v, ok := labels[imageVerifiedLabel]; !ok || strings.ToLower(v) != "true" {
		return "", fmt.Errorf("image digest is not verified in repo")
	}

	checksum1 := labels[checksumLabel1]
	if checksum1 == "" || len(checksum1) != 32 {
		return "", fmt.Errorf("appropriate checksumLabel1 not found in image labels")
	}
	checksum2 := labels[checksumLabel2]
	if checksum2 == "" || len(checksum2) != 32 {
		return "", fmt.Errorf("appropriate checksumLabel2 not found in image labels")
	}
	return checksum1 + checksum2, nil
}

// IdentifyVMsRequest carries the inputs for the IdentifyVMs activity.
type IdentifyVMsRequest struct {
	VMRSConfigPath      string
	CustomerRequest     vmrs.CustomerRequestedPerformance
	DeploymentName      string
	LocationInfo        *commonparams.LocationInfo
	TenancyInfo         *commonparams.TenancyInfo
	ServiceAccountEmail string
	AutoTierBucket      string
	IsLargeCapacityPool bool
	IsOntapMode         bool
	AccountID           string
}

// validate ensures the pointer inputs of the IdentifyVMs activity are non-nil before they are
// dereferenced, guarding against a nil-pointer panic.
func (req IdentifyVMsRequest) validate() error {
	if req.LocationInfo == nil {
		return errors.New("LocationInfo must not be nil")
	}
	if req.TenancyInfo == nil {
		return errors.New("TenancyInfo must not be nil")
	}
	return nil
}

// IdentifyVMs takes the VMRS configuration, the customer-requested performance parameters, and
// the current VLM configuration to identify the optimal VMs to use for the VSA cluster.
func (j *PoolActivity) IdentifyVMs(ctx context.Context, req IdentifyVMsRequest) (*vlm.VLMConfig, error) {
	activity.RecordHeartbeat(ctx, "Starting IdentifyVMs activity")
	logger := util.GetLogger(ctx)
	logger.Debug("Identifying VMs to use for VSA cluster")

	if err := req.validate(); err != nil {
		logger.Error("Invalid IdentifyVMs request", "error", err)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, err))
	}

	activity.RecordHeartbeat(ctx, "Loading VMRS Config")
	// Parse VMRS config.
	vmrsConfig, err := LoadVMRSConfig(req.VMRSConfigPath)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Scope VM selection to the pool's machine family. On an update we keep the
	// pool on the family it was created with (derived from the existing instance
	// type) even if the region's MACHINE_FAMILY_TYPE has since been re-pointed at
	// a different family. On create (no current instance type) we fall back to the
	// process-wide MACHINE_FAMILY_TYPE.
	machineFamily := vmrs.MachineFamily
	if existingFamily := vmrs.MachineFamilyForVMType(req.CustomerRequest.CurrentInstanceType); existingFamily.IsValid() {
		machineFamily = existingFamily
		logger.Debug("Scoping VM selection to existing pool machine family", "currentInstanceType", req.CustomerRequest.CurrentInstanceType, "machineFamily", machineFamily)
	} else if req.CustomerRequest.CurrentInstanceType != "" {
		// Non-empty but unrecognized current instance type (e.g. a corrupted DB
		// value or a not-yet-supported family). We cannot infer the pool's
		// original family, so we fall back to the global MACHINE_FAMILY_TYPE.
		// Surface this as a warning since it may indicate unexpected state.
		logger.Warn("Could not infer machine family from current instance type; falling back to global MACHINE_FAMILY_TYPE", "currentInstanceType", req.CustomerRequest.CurrentInstanceType, "machineFamily", machineFamily)
	}

	// A true-zonal pool is only qualified on c3-lssd, so its topology
	// overrides the region's family: in a c4 region the family filter and the
	// topology's VM selection policy would otherwise exclude each other and leave no
	// candidates at all.
	if topologyFamily := vmrs_decision.MachineFamilyForTopology(req.CustomerRequest.ScalingTopology.Topology, machineFamily); topologyFamily != machineFamily {
		logger.Debug("Overriding machine family with the family required by the pool topology", "topology", req.CustomerRequest.ScalingTopology.Topology, "configuredMachineFamily", machineFamily, "machineFamily", topologyFamily)
		machineFamily = topologyFamily
	}

	// Identify the right VMs to use using the selection strategy defined in the VMRS config.
	// For large capacity pools, force the use of the large volume cluster strategy.
	var decisionMaker vmrs.DecisionMaker
	if req.IsLargeCapacityPool {
		// Force large volume cluster strategy for large capacity pools
		largeVolumeConfig := CreateLargeVolumeVMRSConfig(vmrsConfig)
		decisionMaker, err = CreateDecisionMakerForFamily(largeVolumeConfig, machineFamily)
	} else {
		decisionMaker, err = CreateDecisionMakerForFamily(vmrsConfig, machineFamily)
	}
	if err != nil {
		logger.Error("Failed to create decision maker", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finding optimal VMs")
	vlmConfig := &vlm.VLMConfig{}
	if req.AccountID != "" {
		if vlmConfig.Deployment.Labels == nil {
			vlmConfig.Deployment.Labels = make(map[string]string)
		}
		vlmConfig.Deployment.Labels["account_id"] = req.AccountID
	}
	decision, err := decisionMaker.FindOptimalVMs(vmrsConfig, req.CustomerRequest, vlmConfig)
	if err != nil {
		logger.Error("Failed to identify optimal VMs", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	subnet := ""
	if len(req.TenancyInfo.SubnetworkNames) > 0 {
		subnet = req.TenancyInfo.SubnetworkNames[len(req.TenancyInfo.SubnetworkNames)-1]
	}

	activity.RecordHeartbeat(ctx, "Preparing VLM config")
	// Convert the decision to a VLMConfig.
	err = PrepareVlmConfig(vlmConfig, PrepareVlmConfigParams{
		DeploymentID:            req.DeploymentName,
		Region:                  req.LocationInfo.Region,
		PrimaryZone:             req.LocationInfo.PrimaryZone,
		SecondaryZone:           req.LocationInfo.SecondaryZone,
		Network:                 req.TenancyInfo.Network,
		Subnet:                  subnet,
		RegionalTenantProjectID: req.TenancyInfo.RegionalTenantProject,
		SnHostProject:           req.TenancyInfo.SnHostProject,
		Decision:                decision,
		VSAClusterSaEmail:       req.ServiceAccountEmail,
		AutoTierBucket:          req.AutoTierBucket,
		IsOntapMode:             req.IsOntapMode,
	})
	if err != nil {
		logger.Error("Failed to prepare VLM config", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if req.IsLargeCapacityPool {
		vlmConfig.Deployment.NumHAPair = decision.ClusterMetadata.NumHAPairs
	}
	// Derive region identifier from REGION_NUMBER_MAP (worker configmap, same as proxy regionNumberMap) and append to cluster name
	regionIdentifier := getRegionNumber()
	if regionIdentifier != "" {
		vlmConfig.VsaCluster.ClusterName = req.DeploymentName + "-r" + regionIdentifier
	} else {
		vlmConfig.VsaCluster.ClusterName = req.DeploymentName
	}

	activity.RecordHeartbeat(ctx, "Finished IdentifyVMs activity")
	return vlmConfig, nil
}

// DecideOCIVMRS selects the OCI instance shape, OCPU count, memory, and VPU
// band for a pool create or update using the VMRS catalogue.
func (j *PoolActivity) DecideOCIVMRS(ctx context.Context, req DecideOCIVMRSRequest) (*vmrs_oci.Decision, error) {
	activity.RecordHeartbeat(ctx, "Loading OCI VMRS catalogue")
	logger := util.GetLogger(ctx)

	cfg, err := LoadOCIVMRSConfig(OCIVMRSConfigFilePath)
	if err != nil {
		logger.Error("Failed to load OCI VMRS config", "path", OCIVMRSConfigFilePath, "error", err)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError, err))
	}

	if req.Target.HAPairs <= 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrBadRequest,
				fmt.Errorf("target haPairs must be > 0 for OCI VMRS, pool %q", req.PoolUUID)))
	}
	if req.Target.SizeInBytes == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrBadRequest,
				fmt.Errorf("target sizeInBytes must be > 0 for OCI VMRS, pool %q", req.PoolUUID)))
	}
	if req.Target.ThroughputMibps <= 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrBadRequest,
				fmt.Errorf("target throughputMibps must be > 0 for OCI VMRS, pool %q", req.PoolUUID)))
	}

	// AA (IsRegionalHA=false): both VMs per pair serve → 2 active VMs per pair.
	// AP (IsRegionalHA=true): only primary VM serves → 1 active VM per pair.
	activeVMsPerPair := 2
	if req.Target.IsRegionalHA {
		activeVMsPerPair = 1
	}
	totalActiveVMs := req.Target.HAPairs * activeVMsPerPair

	// Capacity: bytes → binary TiB divided by totalActiveVMs so the per-VM
	// capacity floor in the catalogue compares correctly.
	// Throughput is already decimal MB/s from the API boundary (throughputGBps * 1000).
	perVMCapacityTiB := float64(req.Target.SizeInBytes) / float64(utils.TiBInBytes) / float64(totalActiveVMs)
	throughputMBps := float64(req.Target.ThroughputMibps)

	// DecideOCIVM selects the shape/VPU AND resolves IOPS into
	// Decision.ResolvedIOPS (explicit → validate; same-tier or downgrade with no
	// explicit → clamp(current, defaultIOPS, maxIOPS); else → defaultIOPS).
	var currentThroughputMBps int64
	var currentIOPS int64
	if req.Current != nil {
		currentThroughputMBps = req.Current.ThroughputMibps
		if req.Current.Iops != nil {
			currentIOPS = *req.Current.Iops
		}
	}

	activity.RecordHeartbeat(ctx, "Running OCI VMRS selector")
	decision, err := DecideOCIVM(cfg, vmrs_oci.CustomerRequest{
		DesiredCapacityTiB:    perVMCapacityTiB,
		DesiredThroughputMBps: throughputMBps,
		DesiredIOPS:           req.Target.Iops,
		CurrentThroughputMBps: currentThroughputMBps,
		CurrentIOPS:           currentIOPS,
	})
	if err != nil {
		logger.Error("OCI VMRS selection failed",
			"poolUUID", req.PoolUUID,
			"perVMCapacityTiB", perVMCapacityTiB,
			"throughputMBps", throughputMBps,
			"error", err)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, err))
	}

	logger.Infof("OCI VMRS decided shape=%s ocpus=%d vpu=%s throughputWithHeadroom=%d throughput=%d resolvedIOPS=%d for pool %q",
		decision.VMShape, decision.OCPUs, decision.VPUName, decision.ThroughputWithHeadroom, decision.Throughput, decision.ResolvedIOPS, req.PoolUUID)

	activity.RecordHeartbeat(ctx, "Finished DecideOCIVMRS activity")
	return decision, nil
}

func _resolveZonesForCluster(cloudService hyperscaler2.Services, projectNumber, region, primaryZone, secondaryZone, mediatorZone, instanceType string, isRegionalHA bool) (string, string, error) {
	if primaryZone == "" || projectNumber == "" || region == "" {
		return "", "", vsaerrors.WrapAsTemporalApplicationError(errors.New("primary zone is not set or project number is empty or region is empty"))
	}
	recordHeartbeat(cloudService.GetContext(), fmt.Sprintf("Fetching zones for project: %s, region: %s", projectNumber, region))
	zones, err := cloudService.GetZones(projectNumber, region)
	if err != nil {
		return "", "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Remove primaryZone from the list
	var availableZones []string
	for _, zone := range zones {
		if zone != primaryZone {
			availableZones = append(availableZones, zone)
		}
	}
	if len(availableZones) < 1 {
		return "", "", vsaerrors.WrapAsTemporalApplicationError(errors.New("no zones available besides primary"))
	}

	// Validate that primary zone supports the instance type
	recordHeartbeat(cloudService.GetContext(), fmt.Sprintf("Validating machine type %s in primary zone: %s", instanceType, primaryZone))
	isAvailable, err := cloudService.IsMachineTypeAvailable(projectNumber, primaryZone, instanceType)
	if err != nil {
		return "", "", vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to validate machine type availability in primary zone %s: %w", primaryZone, err))
	}
	if !isAvailable {
		return "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrZoneMachineTypeValidation, fmt.Errorf("primary zone %s does not support machine type %s", primaryZone, instanceType)))
	}

	// If secondaryZone is not set, pick the first available zone that supports the instance type as secondary
	if secondaryZone == "" {
		// Find a secondary zone that supports the instance type
		var validSecondaryZone string
		for _, zone := range availableZones {
			recordHeartbeat(cloudService.GetContext(), fmt.Sprintf("Checking machine type %s in zone %s for secondary", instanceType, zone))
			isAvailable, err := cloudService.IsMachineTypeAvailable(projectNumber, zone, instanceType)
			if err != nil {
				return "", "", vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to validate machine type availability in zone %s: %w", zone, err))
			}
			if isAvailable {
				validSecondaryZone = zone
				break
			}
		}
		if validSecondaryZone == "" {
			return "", "", vsaerrors.WrapAsTemporalApplicationError(errors.New("no secondary zone found that supports the instance type"))
		}
		secondaryZone = validSecondaryZone
	} else {
		// If secondaryZone is set, validate it supports the instance type
		recordHeartbeat(cloudService.GetContext(), fmt.Sprintf("Validating machine type %s in secondary zone: %s", instanceType, secondaryZone))
		isAvailable, err := cloudService.IsMachineTypeAvailable(projectNumber, secondaryZone, instanceType)
		if err != nil {
			return "", "", vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to validate machine type availability in secondary zone %s: %w", secondaryZone, err))
		}
		if !isAvailable {
			return "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrZoneMachineTypeValidation, fmt.Errorf("secondary zone %s does not support machine type %s", secondaryZone, instanceType)))
		}
	}

	if !isRegionalHA {
		mediatorZone = primaryZone
		// Validate that primary zone supports the mediator instance type when used as mediator
		recordHeartbeat(cloudService.GetContext(), fmt.Sprintf("Validating mediator machine type %s in primary zone: %s", mediatorVmInstanceType, primaryZone))
		isAvailable, err := cloudService.IsMachineTypeAvailable(projectNumber, primaryZone, mediatorVmInstanceType)
		if err != nil {
			return "", "", vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to validate mediator machine type availability in primary zone %s: %w", primaryZone, err))
		}
		if !isAvailable {
			return "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrZoneMachineTypeValidation, fmt.Errorf("primary zone %s does not support mediator machine type %s", primaryZone, mediatorVmInstanceType)))
		}
	}
	// If mediatorZone is not set, find one that supports the instance type and is different from secondary
	if mediatorZone == "" {
		for _, zone := range availableZones {
			if zone != secondaryZone {
				recordHeartbeat(cloudService.GetContext(), fmt.Sprintf("Checking mediator machine type %s in zone %s", mediatorVmInstanceType, zone))
				isAvailable, err := cloudService.IsMachineTypeAvailable(projectNumber, zone, mediatorVmInstanceType)
				if err != nil {
					return "", "", vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to validate mediator machine type availability in zone %s: %w", zone, err))
				}
				if isAvailable {
					mediatorZone = zone
					break
				}
			}
		}
		if mediatorZone == "" {
			return "", "", vsaerrors.WrapAsTemporalApplicationError(errors.New("no mediator zone found that supports the instance type"))
		}
	} else {
		// If mediatorZone is set, validate it supports the instance type and is different from secondary
		if mediatorZone == secondaryZone {
			return "", "", vsaerrors.WrapAsTemporalApplicationError(errors.New("mediator zone cannot be the same as secondary zone"))
		}
		// Validate that the set mediator zone supports the mediator instance type
		recordHeartbeat(cloudService.GetContext(), fmt.Sprintf("Validating mediator machine type %s in mediator zone: %s", mediatorVmInstanceType, mediatorZone))
		isAvailable, err := cloudService.IsMachineTypeAvailable(projectNumber, mediatorZone, mediatorVmInstanceType)
		if err != nil {
			return "", "", vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to validate mediator machine type availability in mediator zone %s: %w", mediatorZone, err))
		}
		if !isAvailable {
			return "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrZoneMachineTypeValidation, fmt.Errorf("mediator zone %s does not support machine type %s", mediatorZone, mediatorVmInstanceType)))
		}
	}

	return secondaryZone, mediatorZone, nil
}

func _mockVlmConfig(vlmConfig *vlm.VLMConfig) (*vlm.VLMConfig, error) {
	mockOntapIP := env.GetString("MOCK_ONTAP_IP", "")
	if mockOntapIP == "" {
		return vlmConfig, errors.New("MOCK_ONTAP_IP environment variable is not set for integration tests")
	}
	ogConfig := vlmConfig.Cloud.HAPairs[0].VM1.SystemLIFs[vlm.LIFTypeNodeMgmt]
	ogConfig.IP = mockOntapIP
	vlmConfig.Cloud.HAPairs[0].VM1.SystemLIFs[vlm.LIFTypeNodeMgmt] = ogConfig
	ogConfig = vlmConfig.Cloud.HAPairs[0].VM2.SystemLIFs[vlm.LIFTypeNodeMgmt]
	ogConfig.IP = mockOntapIP
	vlmConfig.Cloud.HAPairs[0].VM2.SystemLIFs[vlm.LIFTypeNodeMgmt] = ogConfig
	return vlmConfig, nil
}

// PrepareVlmConfigParams groups the inputs used to build a VLMConfig from a VMRS decision.
type PrepareVlmConfigParams struct {
	DeploymentID            string
	Region                  string
	PrimaryZone             string
	SecondaryZone           string
	Network                 string
	Subnet                  string
	RegionalTenantProjectID string
	SnHostProject           string
	// Decision is read to size the deployment and is mutated in place by PrepareVlmConfig:
	// StoragePoolRequirements may be clamped to the configured SPConfig minimums.
	Decision          *vmrs.Decision
	VSAClusterSaEmail string
	AutoTierBucket    string
	IsOntapMode       bool
}

func _prepareVlmConfig(vlmConfig *vlm.VLMConfig, params PrepareVlmConfigParams) error {
	if err := ValidateVlmConfigInputs(vlmConfig, params); err != nil {
		return err
	}

	// Load the base VLM config from file
	baseConfig, err := LoadVlmConfigFromFile()
	if err != nil {
		return err
	}

	// Merge in base/loaded VLM config to fill out any missing zero fields.
	if err := mergo.Merge(vlmConfig, *baseConfig); err != nil {
		return err
	}

	vsaImageProjectID := VsaImageProject
	if vsaImageProjectID == "" {
		vsaImageProjectID = params.RegionalTenantProjectID
	}

	mediatorImageProjectID := MediatorImageProject
	if mediatorImageProjectID == "" {
		mediatorImageProjectID = params.RegionalTenantProjectID
	}

	vlmConfig.Deployment.GCPConfig = vlm.GCPConfig{
		ProjectID:              params.RegionalTenantProjectID,
		ImageProjectID:         vsaImageProjectID,
		MediatorImageProjectID: mediatorImageProjectID,
		ServiceAccountEmail:    params.VSAClusterSaEmail,
		BucketName:             params.AutoTierBucket,
	}

	vlmConfig.Deployment.Region = params.Region

	// Mock ONTAP server if integration tests
	if IsIntegrationTest {
		vlmConfig, err = _mockVlmConfig(vlmConfig)
		if err != nil {
			return err
		}
	}

	// Enforce minimum values for SPConfig throughput and IOPS if the feature flag is enabled.
	// This ensures that the values do not fall below the required thresholds for VLM worker compatibility.
	if enforceMinSPConfig {
		if params.Decision.StoragePoolRequirements.DesiredThroughputInMiBs < minSPConfigThroughput {
			params.Decision.StoragePoolRequirements.DesiredThroughputInMiBs = minSPConfigThroughput
		}
		if params.Decision.StoragePoolRequirements.DesiredIOPS < minSPConfigIOps {
			params.Decision.StoragePoolRequirements.DesiredIOPS = minSPConfigIOps
		}
	}
	vlmConfig.Deployment.SPConfig.Throughput = params.Decision.StoragePoolRequirements.DesiredThroughputInMiBs
	vlmConfig.Deployment.SPConfig.IOps = params.Decision.StoragePoolRequirements.DesiredIOPS

	vlmConfig.Deployment.SPConfig.Size = fmt.Sprintf("%dGi", params.Decision.StoragePoolRequirements.DesiredCapacityInGiB)
	vlmConfig.Deployment.VSAInstanceType = params.Decision.ChosenVMs[0] // VLM currently only supports a single VM type for VSA clusters (homogeneous clusters).

	// Tell VLM to provision non-LSSD instances when the chosen VM belongs to a
	// non-LSSD family (e.g. c3-non-lssd / c4-non-lssd). We derive this from the
	// instance type that was actually selected rather than the process-wide
	// MACHINE_FAMILY_TYPE so it stays correct on updates that keep an existing
	// pool on its original family (e.g. a c3-non-lssd pool updated while the
	// region has been re-pointed to c4-lssd). On create the chosen VM always
	// belongs to MACHINE_FAMILY_TYPE, so this matches the prior behavior.
	//
	// The LSSD vs non-LSSD distinction follows the canonical vm_type naming
	// convention (vmrs.LssdVMTypeSuffix), the same contract used for catalog
	// filtering and resolution. If the chosen VM does not map to a recognized
	// family the flag defaults to false (LSSD topology); surface that as a
	// warning since it would point to an unexpected/unconventional vm_type.
	chosenFamily := vmrs.MachineFamilyForVMType(params.Decision.ChosenVMs[0])
	if chosenFamily.IsValid() {
		if chosenFamily.IsNonLssd() {
			vlmConfig.Deployment.DeploymentConfigFlags.EnableNonLssdInstanceType = true
		}
	} else {
		util.GetLogger(context.Background()).Warn("Could not infer machine family from chosen VM type; defaulting to LSSD disk topology (EnableNonLssdInstanceType=false)", "chosenVMType", params.Decision.ChosenVMs[0])
	}

	vlmConfig.Deployment.DeploymentID = params.DeploymentID
	vlmConfig.Deployment.Zone.Zone1 = params.PrimaryZone
	vlmConfig.Deployment.Zone.Zone2 = params.SecondaryZone

	networkConfigs := map[vlm.VSALIFType]struct {
		VPC             string
		Subnet          string
		SubnetProjectID string
	}{
		vlm.LIFTypeNodeMgmt: {MgmtVpcName, MgmtSubnetName, params.RegionalTenantProjectID},
		vlm.LIFTypeIC:       {IcVpcName, IcSubnet, params.RegionalTenantProjectID},
		vlm.LIFTypeRSM:      {RsmVpcName, RsmSubnetName, params.RegionalTenantProjectID},
	}

	// assign network configurations for each LIF type
	for lifType, config := range networkConfigs {
		assignNetworkConfig(vlmConfig, lifType, config.VPC, config.Subnet, config.SubnetProjectID)
	}

	// assign network configuration for data LIF from snHostProject
	assignNetworkConfig(vlmConfig, vlm.LIFTypeInterCluster, params.Network, params.Subnet, params.SnHostProject)

	bootargs := keyManagerBootarg
	if EnableNfsOverTls {
		bootargs += ";" + nfsTlsBootarg
		if NfsTlsConnMaxLimit > 0 {
			bootargs += ";" + nfsTlsConnLimitBootargKey + "=" + strconv.Itoa(NfsTlsConnMaxLimit)
		}
	}
	if params.IsOntapMode {
		bootargs += ";" + s3RestUserAuthCheckBootarg
	}
	vlmConfig.Deployment.UserBootargs = bootargs

	vlmConfig.Deployment.MediatorInstanceType = mediatorVmInstanceType
	vlmConfig.Deployment.MediatorDiskType = mediatorVmDiskType

	return nil
}

func assignNetworkConfig(vlmConfig *vlm.VLMConfig, lifType vlm.VSALIFType, vpc, subnet, subnetProjectID string) {
	if vlmConfig.Deployment.NetConfig == nil {
		vlmConfig.Deployment.NetConfig = make(map[vlm.VSALIFType]vlm.NetworkConfig)
	}

	vlmConfig.Deployment.NetConfig[lifType] = vlm.NetworkConfig{
		VPC:              vpc,
		Subnet:           subnet,
		GCPNetworkConfig: vlm.GCPNetworkConfig{SubnetProjectID: subnetProjectID},
	}
}

func _validateVlmConfigInputs(vlmConfig *vlm.VLMConfig, params PrepareVlmConfigParams) error {
	if vlmConfig == nil {
		return errors.New("vlmConfig is nil")
	}

	if params.Decision == nil {
		return errors.New("decision is nil")
	}

	// _prepareVlmConfig indexes Decision.ChosenVMs[0], so guard against an empty selection here.
	if len(params.Decision.ChosenVMs) == 0 {
		return errors.New("decision has no chosen VMs")
	}

	if params.DeploymentID == "" || params.Region == "" || params.PrimaryZone == "" || params.Network == "" || params.Subnet == "" || params.RegionalTenantProjectID == "" || params.SnHostProject == "" || params.VSAClusterSaEmail == "" {
		return errors.New("one or more required string parameters are empty")
	}

	return nil
}

func loadVlmConfigFromFile() (*vlm.VLMConfig, error) {
	vlmConfig := &vlm.VLMConfig{}

	vlmContent, err := ReadFile(VlmConfigFilePath)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrFileReadError, err)
	}

	err = json.Unmarshal(vlmContent, vlmConfig)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrFileReadError, err)
	}

	return vlmConfig, nil
}

// AllocateClusterSerialNumber generates and assigns a unique 20-digit serial number for the VSA cluster.
// It retrieves the next serial number from the database and sets it in the VLMConfig.
// The serial number is 20 digits: the first 3 digits are a fixed prefix (935), the next 2 digits are the region code (up to 99 regions, currently 42 in use),
// and the remaining 15 digits are a per-region counter. All 20 digits are generated and assigned by the control plane; there is no reservation for VLM.
func (j *PoolActivity) AllocateClusterSerialNumber(ctx context.Context, cfg *vlm.CreateVSAClusterDeploymentRequest) (*vlm.CreateVSAClusterDeploymentRequest, error) {
	activity.RecordHeartbeat(ctx, "Starting AllocateClusterSerialNumber activity")
	logger := util.GetLogger(ctx)
	se := j.SE

	activity.RecordHeartbeat(ctx, "Generating unique serial number for VSA cluster")
	// generate unique serial number for the cluster
	err := assignUniqueSerialNumber(ctx, se, cfg)
	if err != nil {
		logger.Error("Failed to assign unique serial number for VSA cluster", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGeneratingUniqueSerialNumber, err)
	}

	activity.RecordHeartbeat(ctx, "Finished AllocateClusterSerialNumber activity")
	return cfg, nil
}

func (j *PoolActivity) GetNextSerialNumber(ctx context.Context) (int64, error) {
	activity.RecordHeartbeat(ctx, "Allocating next serial number")
	n, err := j.SE.GetNextSerialNumber(ctx)
	if err != nil {
		util.GetLogger(ctx).Error("Failed to get next serial number", "error", err)
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrGeneratingUniqueSerialNumber, err)
	}
	return n, nil
}

// CreateCloudDNSRecords creates DNS records for the VSA cluster's nodes in the cloud DNS service
func (j *PoolActivity) CreateCloudDNSRecords(ctx context.Context, vlmConfig *vlm.VLMConfig, clusterName string, authType int) (*map[string]string, error) {
	activity.RecordHeartbeat(ctx, "Initializing CreateCloudDNSRecords activity")
	hostMap := make(map[string]string)
	if authType == env.USER_CERTIFICATE {
		if len(vlmConfig.Cloud.HAPairs) == 0 {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("no cluster details provided")))
		}
		for i, details := range vlmConfig.Cloud.HAPairs {
			if len(details.VM1.SystemLIFs) == 0 || len(details.VM2.SystemLIFs) == 0 {
				return nil, vsaerrors.WrapAsTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("no system LIFs provided for VMs")))
			}
			cloudService, err := hyperscaler2.ResolveCloudService(ctx)
			if err != nil {
				return nil, vsaerrors.WrapAsTemporalApplicationError(err)
			}

			activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating DNS records for HA pair %d - cluster: %s", i+1, clusterName))
			IpaddressVm1 := details.VM1.SystemLIFs[vlm.LIFTypeNodeMgmt].IP
			haPairNode1 := fmt.Sprintf("%s-%d.%s.%s.", "dns", (2*i)+1, clusterName, env.VsaDeployedDnsName)
			record1, err := vsa.GetOrCreateCloudDNSRecord(cloudService, haPairNode1, IpaddressVm1)
			if err != nil {
				return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err))
			}
			hostMap[IpaddressVm1] = record1.RecordName

			IpaddressVm2 := details.VM2.SystemLIFs[vlm.LIFTypeNodeMgmt].IP
			haPairNode2 := fmt.Sprintf("%s-%d.%s.%s.", "dns", (2*i)+2, clusterName, env.VsaDeployedDnsName)
			record2, err := vsa.GetOrCreateCloudDNSRecord(cloudService, haPairNode2, IpaddressVm2)
			if err != nil {
				return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, err))
			}
			hostMap[IpaddressVm2] = record2.RecordName
		}
		return &hostMap, nil
	}
	return &hostMap, nil
}

func (j *PoolActivity) DeleteCloudDNSRecords(ctx context.Context, hostMap map[string]string, authType int) error {
	if authType == env.USER_CERTIFICATE {
		cloudService, err := hyperscaler2.ResolveCloudService(ctx)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		// Delete entries for each node
		for _, host := range hostMap {
			// Record heartbeat before deleting DNS record to track progress
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Deleting DNS record for host: %s", host))
			// Check if the node is already deleted
			err = vsa.DeleteCloudDNSRecord(cloudService, host)
			if err != nil {
				util.GetLogger(ctx).Errorf("Failed to delete DNS record for host %s: %v", host, err)
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
		}
	}
	return nil
}

func (j *PoolActivity) GetCloudDNSRecords(ctx context.Context, poolId int64, authType int) (*map[string]string, error) {
	hostMap := make(map[string]string)
	if authType == env.USER_CERTIFICATE {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Retrieving nodes from database - poolId: %d", poolId))
		se := j.SE
		nodes, err := se.GetNodesByPoolID(ctx, poolId)
		if err != nil {
			return &hostMap, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err))
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Building host map from %d nodes", len(nodes)))
		for _, node := range nodes {
			hostMap[node.EndpointAddress] = node.HostDNSName
		}
	}
	return &hostMap, nil
}

// OCICreateCloudDNSRecords. For each VM in vlmConfig.Cloud.HAPairs
// it registers an A record `dns-N.<clusterName>.<env.VsaDeployedDnsName>`
// pointing at the VM's management LIF IP, and returns the IP→FQDN map
// that SaveVSANodeDetails persists as node.HostDNSName.
func (j *PoolActivity) OCICreateCloudDNSRecords(ctx context.Context, vlmConfig *vlm.VLMConfig, clusterName string, authType int) (*map[string]string, error) {
	activity.RecordHeartbeat(ctx, "Initializing OCICreateCloudDNSRecords activity")
	hostMap := make(map[string]string)
	if authType != env.USER_CERTIFICATE {
		return &hostMap, nil
	}

	if vlmConfig == nil || len(vlmConfig.Cloud.HAPairs) == 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
				errors.New("no cluster details provided")))
	}

	ociService, err := hyperscaler2.GetOCIService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	for i, details := range vlmConfig.Cloud.HAPairs {
		if len(details.VM1.SystemLIFs) == 0 || len(details.VM2.SystemLIFs) == 0 {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
					errors.New("no system LIFs provided for VMs")))
		}

		activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating OCI DNS records for HA pair %d - cluster: %s", i+1, clusterName))

		ipVM1 := details.VM1.SystemLIFs[vlm.LIFTypeNodeMgmt].IP
		nameVM1 := fmt.Sprintf("%s-%d.%s.%s.", "dns", (2*i)+1, clusterName, env.VsaDeployedDnsName)
		rec1, err := vsa.GetOrCreateOCIDNSRecord(ociService, nameVM1, ipVM1)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, err))
		}
		hostMap[ipVM1] = rec1.RecordName

		ipVM2 := details.VM2.SystemLIFs[vlm.LIFTypeNodeMgmt].IP
		nameVM2 := fmt.Sprintf("%s-%d.%s.%s.", "dns", (2*i)+2, clusterName, env.VsaDeployedDnsName)
		rec2, err := vsa.GetOrCreateOCIDNSRecord(ociService, nameVM2, ipVM2)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, err))
		}
		hostMap[ipVM2] = rec2.RecordName
	}
	return &hostMap, nil
}

// WaitForNodeDNS polls the OS resolver until every per-node management FQDN
// for the given pool resolves to at least one address, or the deadline elapses.
// It's an idempotent, side-effect-free probe that fits cleanly into Temporal's
// activity retry semantics (heartbeats, cancellation).
func (j *PoolActivity) WaitForNodeDNS(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, clusterName string) error {
	activity.RecordHeartbeat(ctx, "Initializing WaitForNodeDNS activity")
	if pool == nil {
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, errors.New("WaitForNodeDNS: pool is nil")))
	}
	if pool.PoolCredentials == nil || pool.PoolCredentials.AuthType != env.USER_CERTIFICATE {
		activity.RecordHeartbeat(ctx, "WaitForNodeDNS activity completed - no-op for non-cert auth types")
		return nil
	}
	if pool.DeploymentName == "" {
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, errors.New("WaitForNodeDNS: pool/deploymentName missing")))
	}
	if vlmConfig == nil {
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("WaitForNodeDNS: vlmConfig is nil")))
	}
	if len(vlmConfig.Cloud.HAPairs) == 0 {
		return vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("no cluster details provided")))
	}
	logger := util.GetLogger(ctx)

	fqdns := make([]string, 0, 2*len(vlmConfig.Cloud.HAPairs))
	for i := range vlmConfig.Cloud.HAPairs {
		fqdns = append(fqdns,
			fmt.Sprintf("%s-%d.%s.%s", "dns", (2*i)+1, clusterName, env.VsaDeployedDnsName),
			fmt.Sprintf("%s-%d.%s.%s", "dns", (2*i)+2, clusterName, env.VsaDeployedDnsName),
		)
	}

	deadline := time.Now().Add(waitForNodeDNSPerAttemptDeadline)
	// Resolver is injected via PoolActivity.DNSResolver in tests; production
	// callers leave it nil and we fall back to the Go-pure resolver here so
	// behaviour matches the previous inline construction byte-for-byte.
	resolver := j.DNSResolver
	if resolver == nil {
		resolver = &net.Resolver{PreferGo: true}
	}

	for _, host := range fqdns {
		logger.Infof("DNS lookup for %s started for cluster: %s", host, clusterName)
		for {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("WaitForNodeDNS: polling %s", host))
			ips, err := resolver.LookupHost(ctx, host)
			if err == nil && len(ips) > 0 {
				logger.Infof("DNS ready for %s -> %v for cluster: %s", host, ips, clusterName)
				break
			}
			// logger.Infof("DNS lookup for %s failed: %v", host, err)
			// NXDOMAIN -> keep polling. Other errors -> propagate; not our problem.
			var dnsErr *net.DNSError
			if !(errors.As(err, &dnsErr) && (dnsErr.IsNotFound || dnsErr.IsTemporary || dnsErr.IsTimeout)) {
				logger.Errorf("DNS lookup for %s failed: %v, for cluster: %s", host, err, clusterName)
				return vsaerrors.WrapAsTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, fmt.Errorf("DNS lookup for %s failed: %w", host, err)))
			}

			if time.Now().After(deadline) {
				return vsaerrors.WrapAsTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError, fmt.Errorf("timed out waiting for OCI DNS to publish %s", host)))
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitForNodeDNSPollInterval):
			}
		}
	}
	activity.RecordHeartbeat(ctx, "WaitForNodeDNS activity completed - all DNS records resolved")
	return nil
}

// OCIDeleteCloudDNSRecords is the OCI counterpart to DeleteCloudDNSRecords.
// Used by both the create-pool rollback path (where hostMap is fresh from
// OCICreateCloudDNSRecords) and the delete-pool path (where hostMap is loaded
// from the DB via GetCloudDNSRecords).
//
// Each underlying DeleteRRSet call is idempotent: OCI 404s are silenced inside
// _deleteOCIDNSRecord so partial-create rollbacks don't fail on records that
// never made it into the zone.
func (j *PoolActivity) OCIDeleteCloudDNSRecords(ctx context.Context, hostMap map[string]string, authType int) error {
	if authType != env.USER_CERTIFICATE {
		return nil
	}
	if len(hostMap) == 0 {
		util.GetLogger(ctx).Infof("No OCI DNS records to delete for pool")
		return nil
	}

	ociService, err := hyperscaler2.GetOCIService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	for _, host := range hostMap {
		// Skip synthetic TLS-SNI values: pools created on the SNI-override path
		// never registered an OCI DNS record, so there is nothing to delete.
		// Determined from persisted HostDNSName, not env.OCIUseTLSSNIOverride,
		// so it stays correct even if the flag changed after pool creation.
		if isOCISyntheticSNI(host) {
			util.GetLogger(ctx).Infof(
				"Skipping OCI DNS delete for %s (synthetic SNI; no record was created at pool create)", host)
			continue
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Deleting OCI DNS record for host: %s", host))
		if err = vsa.DeleteOCIDNSRecord(ociService, host); err != nil {
			util.GetLogger(ctx).Errorf("Failed to delete OCI DNS record for host %s: %v", host, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}
	return nil
}

func (j *PoolActivity) SaveVSANodeDetails(ctx context.Context, pool *datamodel.Pool, vlmConfig *vlm.VLMConfig, deploymentName string, hostMap map[string]string) (node1 *datamodel.Node, err error) {
	activity.RecordHeartbeat(ctx, "Starting SaveVSANodeDetails activity")
	if len(vlmConfig.Cloud.HAPairs) == 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("no cluster details provided")))
	}
	// Resolve the per-leg fault domains assigned for this pool. For OCI deployments a
	// missing or wrong-type provider config is a real configuration bug, so fail fast
	// instead of silently persisting empty fault domains. For non-OCI deployments there
	// are no OCI fault domains, so FaultDomain1/2 stay empty by design.
	var ociCfg vlm.OCIConfig
	if vlmConfig.Deployment.Provider == vlm.OCICloud {
		ociCfg, err = vlmConfig.Deployment.ProviderConfig.AsOCI()
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError,
					fmt.Errorf("failed to extract OCI provider config for pool %q: %w", pool.UUID, err)))
		}
	}
	for _, details := range vlmConfig.Cloud.HAPairs {
		activity.RecordHeartbeat(ctx, "Saving node details for VM1")
		details.VM1.FaultDomain = ociCfg.FaultDomain.FaultDomain1
		node1, err = SaveNodeDetails(ctx, j.SE, details.VM1, vlmConfig.Deployment, pool, deploymentName, hostMap)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		activity.RecordHeartbeat(ctx, "Saving node details for VM2")
		details.VM2.FaultDomain = ociCfg.FaultDomain.FaultDomain2
		_, err = SaveNodeDetails(ctx, j.SE, details.VM2, vlmConfig.Deployment, pool, deploymentName, hostMap)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}
	activity.RecordHeartbeat(ctx, "Finished SaveVSANodeDetails activity")
	return node1, nil
}

func _saveNodeDetails(ctx context.Context, se database.Storage, vmConfig vlm.VMConfig, deploymentConfig vlm.DeploymentConfig, pool *datamodel.Pool, deploymentName string, hostMap map[string]string) (*datamodel.Node, error) {
	// Build CA URI from pool credentials
	caURI := env.BuildCaURI("", "", "")
	if pool.PoolCredentials != nil {
		caURI = pool.PoolCredentials.GetCaURIWithFallback()
	}

	endpointIP := vmConfig.SystemLIFs[vlm.LIFTypeNodeMgmt].IP

	// OCI SNI override mode: skip Cloud DNS, dial the IP, override TLS SNI.
	// Synthesise a stable SNI string from cluster identity that matches the
	// wildcard SAN issued by _createCertificateForVSAClusterOCI
	// (`*.<deploymentName>.<env.VsaDeployedDnsName>`). The same SNI is used
	// for every node in the cluster. Re-purposes the existing HostDNSName
	// column so no schema change is required; the value is never resolved
	// in DNS, only used as tls.Config.ServerName at handshake time.
	ociSNIMode := env.GetHyperscaler() == commonparams.ProviderOCI &&
		env.OCIUseTLSSNIOverride &&
		pool.PoolCredentials != nil &&
		pool.PoolCredentials.AuthType == env.USER_CERTIFICATE
	var ociSNI string
	if ociSNIMode {
		ociSNI = fmt.Sprintf("mgmt.%s.%s", deploymentName, env.VsaDeployedDnsName)
		if hostMap == nil {
			hostMap = make(map[string]string)
		}
		hostMap[endpointIP] = ociSNI
	}

	node := &models.Node{
		Name:                           vmConfig.HostName,
		EndpointAddress:                endpointIP,
		Zone:                           vmConfig.Zone,
		InstanceType:                   deploymentConfig.VSAInstanceType,
		DeploymentName:                 deploymentName,
		EndpointAddressesToHostNameMap: hostMap,
		CertificateID:                  pool.PoolCredentials.CertificateID,
		SecretID:                       pool.PoolCredentials.SecretID,
		Password:                       pool.PoolCredentials.Password,
		AuthType:                       pool.PoolCredentials.AuthType,
		CaURI:                          caURI,
		// External-secret-store credential references are populated only on the
		// OCI path (the OCI workflow writes them onto pool.PoolCredentials before
		// calling this activity). On GCP they are nil and have no effect.
		// Downstream provider builders (_getProviderByNode) need them on the
		// in-memory Node so the OCI Vault password lookup can resolve.
		ExternalSecret:            pool.PoolCredentials.ExternalSecret,
		ExternalCertificate:       pool.PoolCredentials.ExternalCertificate,
		ExternalClientCertificate: pool.PoolCredentials.ExternalClientCertificate,
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	vsaNode, err := provider.GetNodeByName(node.Name)
	if err != nil {
		return nil, err
	}
	var nodeSizeInGiB int64
	for _, disk := range vmConfig.DataDisks {
		nodeSizeInGiB += int64(disk.Size)
	}
	rec := &datamodel.Node{
		Name:            node.Name,
		EndpointAddress: node.EndpointAddress,
		PoolID:          pool.ID,
		State:           datamodel.LifeCycleStateAvailable,
		StateDetails:    datamodel.LifeCycleStateAvailableDetails,
		NodeAttributes: &datamodel.NodeDetails{
			ExternalUUID: vsaNode.ExternalUUID,
			InstanceType: node.InstanceType,
			SizeInGiB:    nodeSizeInGiB,
		},
		AccountID:   pool.AccountID,
		ZoneName:    node.Zone,
		FaultDomain: vmConfig.FaultDomain,
	}
	switch {
	case ociSNIMode:
		// Persist the synthetic SNI in HostDNSName so the read path
		// (_createNodeForProvider) materialises the same IP → SNI map.
		rec.HostDNSName = ociSNI
	case pool.PoolCredentials.AuthType == env.USER_CERTIFICATE:
		rec.HostDNSName = hostMap[node.EndpointAddress]
	default:
		rec.HostDNSName = node.EndpointAddress
	}
	if _, err = se.CreateNode(ctx, rec); err != nil && !utilErrors.IsConflictErr(err) {
		return nil, err
	}
	return rec, nil
}

func (j *PoolActivity) GetPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Starting GetPool activity")
	se := j.SE
	poolView, err := se.GetPool(ctx, pool.UUID, pool.AccountID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	dbPool := database.ConvertPoolViewToPool(poolView)
	activity.RecordHeartbeat(ctx, "Finished GetPool activity")
	return dbPool, nil
}

func (j *PoolActivity) GetPoolView(ctx context.Context, pool *datamodel.Pool) (*datamodel.PoolView, error) {
	se := j.SE
	poolView, err := se.GetPool(ctx, pool.UUID, pool.AccountID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrPoolNotFound, errors.New("pool not found"))
		}
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return poolView, nil
}

func (j *PoolActivity) GetPoolsByAccountName(ctx context.Context, accountName string) ([]*datamodel.Pool, error) {
	se := j.SE
	pools, err := se.GetPoolsByAccountName(ctx, accountName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return pools, err
}

func (j *PoolActivity) DeletingPoolResources(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Starting DeletingPoolResources activity")
	se := j.SE

	activity.RecordHeartbeat(ctx, "Deleting SVMs")
	// Update SVM, and Pool States to Deleting
	if err := DeletingSVMs(ctx, se, pool); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Deleting Nodes")
	if err := DeletingNodes(ctx, se, pool); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished DeletingPoolResources activity")
	return pool, nil
}

func (j *PoolActivity) ReleaseDataSubnetOp(ctx context.Context, pool *datamodel.Pool) (*[]commonparams.Operations, error) {
	logger := util.GetLogger(ctx)
	consumerProject := pool.Account.Name
	logger.Infof("Handling conditions for releasing data subnet for pool: %s Account : %s Network : %s", pool.Name, pool.Account.Name, pool.Network)
	// identify the subnet having totalIPPerHAPair IPs and release it
	if len(pool.ClusterDetails.SubnetNames) == 0 {
		logger.Infof("Subnet is not associated with the pool: %s. Skipping release for network: Account : %s Network : %s", pool.Name, pool.Account.Name, pool.Network)
		return nil, nil
	}
	se := j.SE
	subnetName := pool.ClusterDetails.SubnetNames[len(pool.ClusterDetails.SubnetNames)-1]
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Checking pools using subnet: %s - pool: %s, consumer project: %s", subnetName, pool.Name, consumerProject))
	poolsUsingSubnet, err := getPoolsBySubnetwork(ctx, se, strconv.Itoa(int(pool.Account.ID)), subnetName, pool.Network)
	if err != nil {
		logger.Errorf("Failed to list pools for pool: %s subnetwork: %s for account: %s, network: %s, error: %s", pool.Name, subnetName, pool.Account.Name, pool.Network, err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Infof("Found %d pools using the same subnetwork: %s for account: %s, network: %s", len(poolsUsingSubnet), subnetName, pool.Account.Name, pool.Network)
	allPoolsForDeleting := allPoolsDeleting(poolsUsingSubnet)
	if len(poolsUsingSubnet) > 1 && !allPoolsForDeleting {
		logger.Infof("Skipping release subnetwork as there are other pools using the same subnetwork: %s for account: %s, network: %s, pool : %s", subnetName, pool.Account.Name, pool.Network, pool.Name)
		return nil, nil
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Getting cloud service for subnet release - pool: %s, consumer project: %s", pool.Name, consumerProject))
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Releasing subnet: %s - pool: %s, consumer project: %s", subnetName, pool.Name, consumerProject))
	operations := make([]commonparams.Operations, 0)
	operationName, err := ReleaseSubnetOp(service, poolsUsingSubnet[0].ClusterDetails.SnHostProject, subnetName)
	if err != nil {
		logger.Errorf("Failed to create operation for release subnetwork: %s for account: %s, pool: %s, network: %s, error: %s", subnetName, pool.Account.Name, pool.Name, pool.Network, err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if operationName != "" {
		operations = append(operations, commonparams.Operations{
			OperationName:      operationName,
			Project:            poolsUsingSubnet[0].ClusterDetails.SnHostProject,
			IsDone:             false,
			IsRegionalResource: true,
		})
	}
	return &operations, nil
}

func _releaseSubnetOp(service hyperscaler2.Services, projectId, subnetName string) (string, error) {
	return service.ReleaseSubnetworkOp(Region, projectId, subnetName)
}

// DeletePoolResources deletes all pool resources and the pool record from the database.
func (j *PoolActivity) DeletePoolResources(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	se := j.SE
	activity.RecordHeartbeat(ctx, "Starting DeletePoolResources activity")

	activity.RecordHeartbeat(ctx, "Deleting LIFs")
	// Delete LIFs
	if err := DeleteLIFs(ctx, se, pool); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Deleting SVMs")
	// Delete SVMs
	if err := DeleteSVMs(ctx, se, pool); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Deleting Nodes")
	// Delete nodes
	if err := DeleteNodes(ctx, se, pool); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Deleting Pool")
	// Delete the pool itself from a database
	if err := se.DeletePool(ctx, pool); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished DeletePoolResources activity")
	return pool, nil
}

// DeleteAllPoolVPGs deletes all VPGs for a pool by removing their ONTAP QoS policies and
// hard-deleting the VPG records from the database. This is called during pool deletion to
// cascade-clean VPGs before the VSA cluster is destroyed.
// The caller (workflow) is responsible for gating on manual QoS type and feature flags.
func (j *PoolActivity) DeleteAllPoolVPGs(ctx context.Context, pool *datamodel.Pool) error {
	logger := util.GetLogger(ctx)
	se := j.SE
	activity.RecordHeartbeat(ctx, "Starting DeleteAllPoolVPGs activity")

	vpgs, err := se.ListVolumePerformanceGroupsByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(vpgs) == 0 {
		activity.RecordHeartbeat(ctx, "No VPGs to clean up")
		return nil
	}

	logger.Info("Deleting VPGs for pool", "pool_id", pool.ID, "vpg_count", len(vpgs))

	svm, err := se.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		logger.Warn("Failed to get SVM, will skip ONTAP QoS cleanup", "error", err)
	}

	dbNodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Warn("Failed to get nodes, will skip ONTAP QoS cleanup", "error", err)
	}

	var provider vsa.Provider
	if len(dbNodes) > 0 {
		node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes:            dbNodes,
			DeploymentName:   pool.DeploymentName,
			OntapCredentials: pool.PoolCredentials,
		})
		if node != nil {
			var providerErr error
			provider, providerErr = vsa.GetProviderByNode(ctx, node)
			if providerErr != nil {
				logger.Warn("Failed to get ONTAP provider, will skip QoS cleanup", "error", providerErr)
			}
		}
	}

	for _, vpg := range vpgs {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Deleting VPG %s", vpg.UUID))

		if vpg.OntapQosPolicyID != "" && provider != nil && svm != nil {
			deleteParams := vsa.DeleteQoSGroupPolicyParams{
				UUID:    vpg.OntapQosPolicyID,
				SvmName: svm.Name,
			}
			if err := provider.DeleteQoSGroupPolicy(deleteParams); err != nil {
				if !utilErrors.IsNotFoundErr(err) {
					logger.Warn("Failed to delete QoS policy from ONTAP", "vpg_uuid", vpg.UUID, "policy", vpg.OntapQosPolicyID, "error", err)
				}
			}
		}

		if err := se.HardDeleteVolumePerformanceGroup(ctx, vpg); err != nil {
			if utilErrors.IsNotFoundErr(err) {
				logger.Info("VPG already deleted, skipping", "vpg_uuid", vpg.UUID)
			} else {
				logger.Error("Failed to hard-delete VPG", "vpg_uuid", vpg.UUID, "error", err)
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
		} else {
			logger.Info("Deleted VPG", "vpg_uuid", vpg.UUID, "vpg_name", vpg.Name)
		}
	}

	activity.RecordHeartbeat(ctx, "Finished DeleteAllPoolVPGs activity")
	return nil
}

// CreateAutoTierBucket creates a GCP bucket for auto-tiering in the specified project and region.
// Parameters:
// - ctx: The context for managing request-scoped values, deadlines, and cancellation signals.
// - params: Contains the pool parameters, including the name and region of the pool.
// - projectId: The ID of the GCP project where the bucket will be created.
// Returns:
// - An error if the bucket creation fails or if there is an issue initializing the cloud service.
func (j *PoolActivity) CreateAutoTierBucket(ctx context.Context, autoTierBucketName string, region string, projectId string) error {
	activity.RecordHeartbeat(ctx, "Initializing auto-tier bucket creation")
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Creating auto-tier bucket in the cloud provider")
	err = CreateGCPBucket(ctx, projectId, autoTierBucketName, region, cloudService)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Auto-tier bucket created successfully")

	return nil
}

// DeleteAutoTierBucket deletes the specified GCP bucket used for auto-tiering.
// It initializes the cloud service client and attempts to delete the bucket.
// Returns an error if the deletion fails or if cloud service initialization fails.
func (j *PoolActivity) DeleteAutoTierBucket(ctx context.Context, autoTierBucketName string, accountName string, poolID int64) error {
	activity.RecordHeartbeat(ctx, "Initializing auto-tier bucket deletion")
	logger := util.GetLogger(ctx)
	if autoTierBucketName == "" {
		// If the bucket name is empty, log a warning and return nil
		logger.Warnf("Skipping autoTiering bucket deletion,cannot delete autoTiering bucket: bucket name is empty")
		return nil
	}
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Deleting auto-tier bucket from the cloud provider")
	logger.Debugf("Deleting autoTiering bucket %v", autoTierBucketName)
	isDeleted, err := DeleteGCPBucket(ctx, autoTierBucketName, cloudService)
	if !isDeleted {
		activity.RecordHeartbeat(ctx, "Bucket deletion pending, creating pending resource deletion entry")
		var errorMessage string
		if err != nil {
			errorMessage = err.Error()
		} else {
			errorMessage = ""
		}
		_, err := j.SE.CreatePendingResourceDeletion(ctx, models.ResourceTypeStringBucket, autoTierBucketName, errorMessage, accountName, poolID)
		if err != nil {
			logger.Errorf("Failed to insert the bucket entry which needs to be cleaned up for bucket %s: %v",
				autoTierBucketName, err)
			// TODO: Alert about persistent failure to insert pending resource deletion for auto-tiering bucket.
		}
	}
	activity.RecordHeartbeat(ctx, "DeleteAutoTierBucket activity completed successfully")

	return nil
}

func _createGCPBucket(ctx context.Context, projectId, bucketName, region string, cloudService hyperscaler2.Services) error {
	logger := cloudService.GetLogger()
	err := cloudService.CreateBucketIfNotExists(ctx, projectId, bucketName, region, nil)
	if err != nil {
		logger.Errorf("error creating bucket: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceAlreadyExistsError, err)
	}
	logger.Infof("Bucket created successfully %s", bucketName)

	return nil
}

func _deleteGCPBucket(ctx context.Context, bucketName string, cloudService hyperscaler2.Services) (bool, error) {
	logger := cloudService.GetLogger()
	isDeleted, err := cloudService.DeleteBucketWithLifecyclePolicy(ctx, bucketName)
	if err != nil {
		logger.Errorf("error deleting bucket: %v", err)
		return isDeleted, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceDeprovisionError, err)
	}
	logger.Infof("Bucket deleted successfully %s", bucketName)

	return isDeleted, nil
}

// CreateServiceAccountWithStorageRole creates a GCP service account with the specified ID and display name,
// and attaches the "roles/storage.objectUser" role to it in the given project.
// Parameters:
// - ctx: Context for request-scoped values and cancellation.
// - projectID: The GCP project ID where the service account will be created.
// - saAccountID: The unique ID for the new service account.
// - saDisplayName: The display name for the new service account.
// Returns:
// - The created *iam.ServiceAccount, or an error if creation or role attachment fails.
func (j *PoolActivity) CreateServiceAccountWithStorageRole(ctx context.Context, projectID string, saAccountID string, saDisplayName string) (*hyperscaler_models.ServiceAccount, error) {
	activity.RecordHeartbeat(ctx, "Initializing service account creation with storage role")
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Creating service account and attaching storage role")
	sa, err := CreateServiceAccountAndAttachRole(ctx, projectID, saAccountID, saDisplayName, cloudService)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Service account created successfully with storage role")
	return sa, nil
}

func (j *PoolActivity) DeleteServiceAccount(ctx context.Context, projectID string, saAccountID string) error {
	activity.RecordHeartbeat(ctx, "Initializing service account deletion")
	logger := util.GetLogger(ctx)
	if saAccountID == "" || projectID == "" {
		// If the service account ID or project ID is empty, log a warning and return nil
		logger.Warnf("Skipping service account deletion,cannot delete service account without service account ID or project ID: saAccountID=%s, projectID=%s", saAccountID, projectID)
		return nil
	}
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Removing storage role and deleting service account")
	err = DeleteServiceAccountAndRemoveStorageRole(ctx, projectID, saAccountID, cloudService)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Service account deleted successfully")
	return nil
}

// getRegionNumber retrieves the region number from the region map JSON string.
func getRegionNumber() string {
	var regionMap map[string]string
	_ = json.Unmarshal([]byte(regionMapJson), &regionMap)
	return regionMap[Region]
}

// assignUniqueSerialNumber assigns a unique serial number to the VLMConfig based on the region.
func assignUniqueSerialNumber(ctx context.Context, se database.Storage, cfg *vlm.CreateVSAClusterDeploymentRequest) error {
	if RegionNumber == "" {
		return errors.New("region number is not set")
	}

	if cfg.VLMConfig.Deployment.NumHAPair < 1 {
		return errors.New("HA pairs count must be at least 1")
	}

	// Generate serial number prefix for number of ha pairs * VMsPerHAPair (for each VM in the HA pair).
	regionalPrefix := clusterSerialNumberPrefix + RegionNumber
	var serials []string
	for range cfg.VLMConfig.Deployment.NumHAPair * VMsPerHAPair {
		seq, err := se.GetNextSerialNumber(ctx)
		if err != nil {
			util.GetLogger(ctx).Error("Failed to get next regional cluster serial number", "error", err)
			return err
		}
		serials = append(serials, fmt.Sprintf("%s%015d", regionalPrefix, seq))
	}

	// Need to set the SerialNumberPrefix to empty otherwise VMSerialNumbers will be ignored by VLM.
	cfg.VLMConfig.Deployment.SerialNumberPrefix = ""
	cfg.VLMConfig.Deployment.VMSerialNumbers = serials

	return nil
}

func _deleteServiceAccountAndRemoveStorageRole(ctx context.Context, projectNumber string, saAccountID string, cloudService hyperscaler2.Services) error {
	logger := cloudService.GetLogger()

	saEmail := utils.ConstructServiceAccountEmail(saAccountID, projectNumber)
	logger.Infof("Deleting service account %s in project %s", saEmail, projectNumber)

	roles := []string{"roles/storage.objectUser"}
	recordHeartbeat(ctx, fmt.Sprintf("Removing storage roles from service account: %s", saEmail))
	err := cloudService.RemoveRolesFromServiceAccounts(roles, saEmail, projectNumber)
	if err != nil {
		logger.Errorf("Failed to remove roles from service account %s: %v.", saEmail, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully removed roles from service account %s", saEmail)

	recordHeartbeat(ctx, fmt.Sprintf("Deleting service account: %s in project: %s", saEmail, projectNumber))
	err = cloudService.DeleteServiceAccount(projectNumber, saEmail)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func _createServiceAccountAndAttachRole(ctx context.Context, projectID string, saAccountID string, saDisplayName string, cloudService hyperscaler2.Services) (*hyperscaler_models.ServiceAccount, error) {
	logger := cloudService.GetLogger()
	createReq := &hyperscaler_models.CreateServiceAccountRequest{
		AccountId: saAccountID,
		ServiceAccount: &hyperscaler_models.ServiceAccount{
			DisplayName: saDisplayName,
		},
	}
	saEmail := utils.ConstructServiceAccountEmail(saAccountID, projectID)

	logger.Infof("Creating service account with object user role %s", saEmail)
	recordHeartbeat(ctx, fmt.Sprintf("Creating service account: %s in project: %s", saEmail, projectID))
	sa, err := cloudService.CreateServiceAccount(createReq, projectID, saEmail)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Infof("Created service account %s", saAccountID)
	roles := []string{"roles/storage.objectUser"}

	recordHeartbeat(ctx, fmt.Sprintf("Attaching storage role to service account: %s", saEmail))
	err = cloudService.AttachOrUpdateRolesForServiceAccounts(roles, saEmail, projectID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Infof("Successfully created service account %s with roles %v", saEmail, roles)

	return sa, nil
}

// deletingNodes updates nodes status to deleting.
func _deletingNodes(ctx context.Context, se database.Storage, pool *datamodel.Pool) error {
	// Retrieve the nodes associated with the pool
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, fmt.Errorf("failed to retrieve nodes for pool %d: %w", pool.ID, err))
	}

	// Delete each node
	for _, node := range nodes {
		// Check if the node is already marked for deletion
		if node.State == datamodel.LifeCycleStateDeleting {
			continue
		}
		recordHeartbeat(ctx, fmt.Sprintf("Marking node %s as deleting for pool %d", node.Name, pool.ID))
		// Delete the node record from the database
		if err := se.DeletingNode(ctx, node); err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("failed to delete node record %s: %w", node.Name, err))
		}
	}
	return nil
}

// _deleteLIFs deletes LIFs database records associated with the given Nodes.
func _deleteLIFs(ctx context.Context, se database.Storage, pool *datamodel.Pool) error {
	// Retrieve the nodes associated with the pool
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, fmt.Errorf("failed to retrieve nodes for pool %d: %w", pool.ID, err))
	}

	// return if there are no nodes(that means no lifs are there
	if len(nodes) == 0 {
		return nil
	}

	nodeIds := make([]int64, 0, len(nodes))
	for _, node := range nodes {
		nodeIds = append(nodeIds, node.ID)
	}
	// Retrieve the LIFs associated with the Node
	lifs, err := se.GetLifsForNodesWithProtocol(ctx, nodeIds, pool.AccountID, "")
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, fmt.Errorf("failed to retrieve LIFs for pool %d: %w", pool.ID, err))
	}
	// Loop over to delete each LIF
	for _, lif := range lifs {
		if lif.DeletedAt != nil && lif.DeletedAt.Valid {
			continue
		}
		recordHeartbeat(ctx, fmt.Sprintf("Deleting LIF record %s for pool %d", lif.Name, pool.ID))
		// Delete the LIF record from the database
		if err := se.DeleteLif(ctx, lif); err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("failed to delete LIF record %s: %w", lif.Name, err))
		}
	}
	return nil
}

// deleteNodes deletes node database records associated with the given pool.
func _deleteNodes(ctx context.Context, se database.Storage, pool *datamodel.Pool) error {
	if pool == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool cannot be nil"))
	}
	if se == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("storage cannot be nil"))
	}

	// Retrieve the nodes associated with the pool
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, fmt.Errorf("failed to retrieve nodes for pool %d: %w", pool.ID, err))
	}

	// Delete each node
	for _, node := range nodes {
		// Check if the node is already deleted
		if node.DeletedAt != nil && node.DeletedAt.Valid {
			continue
		}
		recordHeartbeat(ctx, fmt.Sprintf("Deleting node record %s for pool %d", node.Name, pool.ID))
		// Delete the node record from the database
		if err := se.DeleteNode(ctx, node); err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("failed to update node record to deleting %s: %w", node.Name, err))
		}
	}

	return nil
}

// _failedNodes updates nodes status to error.
func _failedNodes(ctx context.Context, se database.Storage, pool *datamodel.Pool) error {
	// Retrieve the nodes associated with the pool
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, fmt.Errorf("failed to retrieve nodes for pool %d: %w", pool.ID, err))
	}

	// Delete each node
	for _, node := range nodes {
		// Check if the node is already marked for deletion
		if node.State == datamodel.LifeCycleStateDeleting {
			node.State = datamodel.LifeCycleStateError
			node.StateDetails = datamodel.LifeCycleStateDeletionErrorDetails
			recordHeartbeat(ctx, fmt.Sprintf("Marking node %s as errored for pool %d", node.Name, pool.ID))
			err = se.ErroredNode(ctx, node, datamodel.LifeCycleStateDeletionErrorDetails)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func resourceNotFoundCheck(errorString string, projectName, vpcName, subnetName, addressName, firewall string) (bool, error) {
	if !strings.Contains(errorString, "not found") {
		errorMessage := fmt.Sprintf("Error getting vpc for project: %s and vpc name: %s. Error : %s", projectName, vpcName, errorString)
		if subnetName != "" {
			errorMessage = fmt.Sprintf("Error getting subnet for project: %s, vpc name: %s, subnet name: %s. Error : %s", projectName, vpcName, subnetName, errorString)
		}
		if firewall != "" {
			errorMessage = fmt.Sprintf("Error getting subnet for project: %s, vpc name: %s, firewall name: %s. Error : %s", projectName, vpcName, firewall, errorString)
		}
		if addressName != "" {
			errorMessage = fmt.Sprintf("Error getting address/forwarding rule for project: %s, address name: %s. Error : %s", projectName, addressName, errorString)
		}
		return false, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, errors.New(errorMessage))
	}
	return true, nil
}

// _createVPC invokes create VPC call from orchestrator. It is used for creating a VPC network in GCP for a project with the specified vpc name
func _createVPC(gService hyperscaler2.Services, projectName, vpcName string) (string, error) {
	logger := gService.GetLogger()
	logger.Debugf("Checking if VPC already exists before creating VPC for project : %s network name : %s", projectName, vpcName)
	vpcNetworkReceived, err := gService.GetVPCNetwork(projectName, vpcName)
	if err != nil {
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, vpcName, "", "", "")
		if !resourceNotFound {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, errReceived)
		}
	}
	if vpcNetworkReceived != nil {
		logger.Infof("VPC already exists. Skipping creation. project name : %s , vpc name : %s", projectName, vpcName)
		return "", nil
	}
	vpcNetwork := &hyperscaler_models.VPCNetwork{Name: vpcName, ProjectName: projectName}

	logger.Infof("Creating VPC for project name : %s , vpc name : %s", projectName, vpcName)
	return gService.CreateVPC(vpcNetwork)
}

// _insertSubnet invokes create subnetwork call from orchestrator. It is used for creating a subnetwork in GCP for a project with the specified subnet name
func _insertSubnet(gService hyperscaler2.Services, projectName string, Region *string, subnetName string, vpcName string, ipCidrRange string) (string, error) {
	logger := gService.GetLogger()
	logger.Debugf("Checking if subnet already exists before creating subnet for project : %s  network name : %s firewall name : %s", projectName, vpcName, subnetName)
	subnetReceived, err := gService.GetSubnetwork(projectName, *Region, subnetName)
	if err != nil {
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, vpcName, subnetName, "", "")
		if !resourceNotFound {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errReceived)
		}
	}
	if subnetReceived != nil {
		logger.Infof("Subnet already exists. Skipping creation. project name : %s , vpc name : %s, subnet name : %s", projectName, vpcName, subnetName)
		return "", nil
	}
	subnetRequest := &hyperscaler_models.Subnet{
		Name:        subnetName,
		Network:     fmt.Sprintf("projects/%s/global/networks/%s", projectName, vpcName),
		IpCidrRange: ipCidrRange,
		Region:      Region,
		ProjectName: projectName,
	}
	logger.Infof("Creating Subnet for project name : %s , vpc name : %s, subnet name : %s", projectName, vpcName, subnetName)
	return gService.CreateSubnetwork(subnetRequest)
}

// _insertFirewall invokes create firewall call from orchestrator. It is used for creating a firewall in GCP for a project with the specified firewall name
func _insertFirewall(gService hyperscaler2.Services, projectName, firewallName, vpcName string, priority int64, direction string, firewallSourceRanges, firewallAllowedPortRules []string) (string, error) {
	firewallRequest := &hyperscaler_models.Firewall{
		Name:             firewallName,
		AllowedPortRules: firewallAllowedPortRules,
		SourceRanges:     firewallSourceRanges,
		VPCNetworkName:   vpcName,
		ProjectName:      projectName,
		Priority:         priority,
		Direction:        direction, // can be INGRESS or EGRESS
	}
	logger := gService.GetLogger()
	logger.Debugf("Checking if firewall already exists before creating firewall for project : %s  network name : %s firewall name : %s", projectName, vpcName, firewallName)
	existingFirewall, err := gService.GetFirewall(projectName, firewallName)
	if err != nil {
		logger.Debugf("Error getting firewall for project : %s and network name : %s firewall name : %s . Error : %v", projectName, vpcName, firewallName, err)
		resourceNotFound, errReceived := resourceNotFoundCheck(err.Error(), projectName, vpcName, "", "", firewallName)
		logger.Debugf("Error getting firewall for project : %s and network name : %s firewall name : %s . Error : %v resourceNotFound : %t", projectName, vpcName, firewallName, err, resourceNotFound)
		if !resourceNotFound {
			return "", vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errReceived)
		}
	}
	if existingFirewall != nil {
		return CheckAndUpdateFirewall(gService, existingFirewall, firewallRequest)
	}

	logger.Infof("Creating firewall for project : %s and network name : %s ", projectName, vpcName)
	return gService.InsertFirewall(firewallRequest)
}

// _mergeFirewallSourceRangesIfNeeded appends any requested source ranges missing from existing (append-only; never removes).
// Returns nil when no new ranges are needed; otherwise returns existing ranges plus the new requested ranges.
func _mergeFirewallSourceRangesIfNeeded(requestedSourceRanges, existingSourceRanges []string) []string {
	newFirewallSourceRanges := append([]string(nil), existingSourceRanges...)
	hasNewRange := false
	for _, requestedRange := range requestedSourceRanges {
		if !utils.ContainsString(newFirewallSourceRanges, requestedRange) {
			newFirewallSourceRanges = append(newFirewallSourceRanges, requestedRange)
			hasNewRange = true
		}
	}
	if !hasNewRange {
		return nil
	}
	return newFirewallSourceRanges
}

// _checkAndUpdateFirewall updates an existing firewall only when the request includes new SourceRanges not already present in the existing firewall (append-only; does not remove existing ranges).
func _checkAndUpdateFirewall(gService hyperscaler2.Services, existingFirewall, firewallRequest *hyperscaler_models.Firewall) (string, error) {
	newFirewallSourceRanges := MergeFirewallSourceRangesIfNeeded(firewallRequest.SourceRanges, existingFirewall.SourceRanges)
	if newFirewallSourceRanges != nil {
		firewallRequest.SourceRanges = newFirewallSourceRanges
		gService.GetLogger().Infof("Updating firewall for project : %s and network name : %s, firewall name : %s ", firewallRequest.ProjectName, firewallRequest.VPCNetworkName, firewallRequest.Name)
		op, err := gService.UpdateFirewall(firewallRequest)
		if err != nil {
			gService.GetLogger().Errorf("Error updating firewall for project : %s and network name : %s firewall name : %s. Error : %v ", firewallRequest.ProjectName, firewallRequest.VPCNetworkName, firewallRequest.Name, err)
			return "", vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("error updating firewall for project: %s and network name: %s firewall name: %s. Error : %v", firewallRequest.ProjectName, firewallRequest.VPCNetworkName, firewallRequest.Name, err))
		}
		return op, err
	}
	gService.GetLogger().Infof("Firewall already exists. Skipping creation. project name : %s , vpc name : %s, firewall name : %s", firewallRequest.ProjectName, firewallRequest.VPCNetworkName, firewallRequest.Name)
	return "", nil
}

// GetServiceNetOpStatus returns the status (and result) of a Google's service networking operation
func (j *PoolActivity) GetServiceNetOpStatus(ctx context.Context, operation string) (*hyperscaler_models.ComputeOperation, error) {
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Data Subnet creation in Hyperscaler: %s", operation))
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}
	return GetServiceNetOpStatus(cloudService, operation)
}

func _getServiceNetOpStatus(cloudService hyperscaler2.Services, operation string) (*hyperscaler_models.ComputeOperation, error) {
	return cloudService.GetServiceNetOpStatus(operation)
}

// GetSubnetFromOperation returns the status (and result) of a Google's service networking operation
func (j *PoolActivity) GetSubnetFromOperation(ctx context.Context, subnetInBytes []byte) (*hyperscaler_models.Subnet, error) {
	recordHeartbeat(ctx, "Parsing subnet from GCP operation response")
	return GetSubnetFromOperation(ctx, subnetInBytes)
}

func _getSubnetFromOperation(ctx context.Context, subnetInBytes []byte) (*hyperscaler_models.Subnet, error) {
	logger := util.GetLogger(ctx)
	if subnetInBytes == nil {
		logger.Error("Operation response is nil, cannot extract subnet")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, vsaerrors.New("operation response is nil"))
	}
	logger.Debugf("subnetInBytes %s", string(subnetInBytes))

	subnetCreated, err := hyperscalercommon.ParseSubnetFromOperation(subnetInBytes)
	if err != nil {
		logger.Debugf("subnetInBytes json unmarshal error %s", err.Error())
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrJSONParsingError, err)
	}
	gateway, err := GetGatewayFromIpCidrRange(subnetCreated.IpCidrRange)
	if err != nil {
		logger.Errorf("Failed to get gateway from IP CIDR range %s: %v", subnetCreated.IpCidrRange, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, err)
	}
	subnetCreated.GatewayAddress = gateway
	return subnetCreated, nil
}

func _getGatewayFromIpCidrRange(ipCidrRange string) (string, error) {
	ip, _, err := net.ParseCIDR(ipCidrRange)
	if err != nil {
		return "", err
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "", fmt.Errorf("IP CIDR range is not an IPv4 address")
	}
	ip4[3] += 1
	return ip4.String(), nil
}

// IdentifySecondaryAndMediatorZone identifies the secondary and mediator zones for a cluster
// and returns the resolved zones.
func (j *PoolActivity) IdentifySecondaryAndMediatorZone(ctx context.Context, projectNumber string, locationInfo *commonparams.LocationInfo, instanceType string, isRegionalHA bool) (*commonparams.LocationInfo, error) {
	activity.RecordHeartbeat(ctx, "Starting IdentifySecondaryAndMediatorZone activity")
	logger := util.GetLogger(ctx)
	logger.Debug("Identifying secondary and mediator zones for cluster")

	activity.RecordHeartbeat(ctx, "Getting cloud service")
	// Get cloud service
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Getting secondary and mediator zones")
	// Use ResolveZonesForCluster to get the secondary and mediator zones
	resolvedSecondaryZone, resolvedMediatorZone, err := ResolveZonesForCluster(cloudService, projectNumber, locationInfo.Region, locationInfo.PrimaryZone, locationInfo.SecondaryZone, locationInfo.MediatorZone, instanceType, isRegionalHA)
	if err != nil {
		logger.Error("Failed to resolve zones for cluster", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Create and return the updated location info
	updatedLocationInfo := &commonparams.LocationInfo{
		PrimaryZone:   locationInfo.PrimaryZone,
		SecondaryZone: resolvedSecondaryZone,
		Region:        locationInfo.Region,
		MediatorZone:  resolvedMediatorZone,
	}

	logger.Debug("Successfully identified secondary and mediator zones",
		"secondaryZone", resolvedSecondaryZone,
		"mediatorZone", resolvedMediatorZone)

	activity.RecordHeartbeat(ctx, "Finished IdentifySecondaryAndMediatorZone activity")
	return updatedLocationInfo, nil
}

// GetComputeOpStatus returns the status (and result) of a Google's compute networking operation for global and regional operations
func (j *PoolActivity) GetComputeOpStatus(ctx context.Context, project string, isRegionalResource bool, operation string) (*hyperscaler_models.ComputeOperation, error) {
	// Record heartbeat to indicate progress during polling
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Hyperscaler operation status for operation name: %s in project: %s", operation, project))
	cloudService, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}
	return GetComputeOpStatus(cloudService, project, isRegionalResource, operation)
}

func _getComputeOpStatus(cloudService hyperscaler2.Services, project string, isRegionalResource bool, operation string) (*hyperscaler_models.ComputeOperation, error) {
	if !isRegionalResource {
		return cloudService.GetComputeGlobalOpStatus(project, operation)
	}
	return cloudService.GetComputeRegionalOpStatus(project, Region, operation)
}

func fetchOnTapCredentials(ctx context.Context, pool *datamodel.Pool) (*vlm.OntapCredentials, error) {
	credentials := &vlm.OntapCredentials{}
	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP credentials for pool: %s, authType: %d", pool.Name, pool.PoolCredentials.AuthType))
	switch pool.PoolCredentials.AuthType {
	case env.USER_CERTIFICATE:
		certificate, err := vsa.GetCertificateFromCacheOrSecretManager(ctx, pool.PoolCredentials)
		if err != nil {
			return nil, err
		}
		credentials.Certificate.CommonName = certificate.CommonName
		credentials.Certificate.Certificate = certificate.SignedCertificate
		credentials.Certificate.PrivateKey = certificate.PrivateKey
		credentials.Certificate.InterMediateCertificate = certificate.InterMediateCertificates
		fallthrough
	case env.USERNAME_PWD_SEC_MGR:
		secret, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.PoolCredentials.SecretID)
		if err != nil {
			return nil, err
		}
		credentials.AdminPassword = secret
	default:
		credentials.AdminPassword = pool.PoolCredentials.Password
	}
	return credentials, nil
}

// fetchOnTapCredentialsForOCI resolves the ONTAP admin credentials (cert and/or
// password) for an OCI pool based on the configured auth type.
// USER_CERTIFICATE falls through to USERNAME_PWD_SEC_MGR so the SSH password
// is also fetched alongside the cert;
// where cert-auth pools still require a vault-backed password for SSH while
// the certificate is used for the ONTAP REST/TLS handshake.
func fetchOnTapCredentialsForOCI(ctx context.Context, pool *datamodel.Pool) (*vlm.OntapCredentials, error) {
	if pool.PoolCredentials == nil {
		return nil, fmt.Errorf("pool credentials are nil for pool %s", pool.Name)
	}
	credentials := &vlm.OntapCredentials{}
	recordHeartbeat(ctx, fmt.Sprintf("Fetching OCI ONTAP credentials for pool: %s, authType: %d", pool.Name, pool.PoolCredentials.AuthType))
	switch pool.PoolCredentials.AuthType {
	case env.USER_CERTIFICATE:
		certificate, err := vsa.GetCertificateFromCacheOrCAS(ctx, pool.PoolCredentials.ExternalCertificate)
		if err != nil {
			util.GetLogger(ctx).Errorf("Failed to get certificate for pool %s: %v", pool.Name, err)
			return nil, err
		}
		credentials.Certificate.CommonName = certificate.CommonName
		credentials.Certificate.Certificate = certificate.SignedCertificate
		credentials.Certificate.PrivateKey = certificate.PrivateKey
		credentials.Certificate.InterMediateCertificate = certificate.InterMediateCertificates
		fallthrough
	case env.USERNAME_PWD_SEC_MGR:
		secret, err := vsa.GetPasswordFromCacheOrOCIVault(ctx, pool.PoolCredentials.ExternalSecret)
		if err != nil {
			return nil, err
		}
		credentials.AdminPassword = secret
	default:
		credentials.AdminPassword = pool.PoolCredentials.Password
	}

	if credentials.AdminPassword == "" && credentials.Certificate.Certificate == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrOCIOntapCredentialsNotFound,
				fmt.Errorf("ONTAP admin credentials could not be resolved for pool %q (authType: %d): "+
					"the admin password is empty and no certificate was provisioned",
					pool.Name, pool.PoolCredentials.AuthType)))
	}

	return credentials, nil
}

func fetchExpertModeCredentials(ctx context.Context, pool *datamodel.Pool) (*vlm.OntapCredentials, error) {
	credentials := &vlm.OntapCredentials{}
	if pool.ExpertModeCredentials == nil || pool.ExpertModeCredentials.ExpertModeCredential == nil || len(pool.ExpertModeCredentials.ExpertModeCredential) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("expert mode credentials are not provided")))
	}
	switch pool.ExpertModeCredentials.ExpertModeCredential[0].AuthType {
	case env.USER_CERTIFICATE:
		// Create PoolCredentials from ExpertModeCredential, using pool's PoolCredentials for CaURI if available
		expertPoolCredentials := &datamodel.PoolCredentials{
			CertificateID: pool.ExpertModeCredentials.ExpertModeCredential[0].CertificateID,
		}
		if pool.PoolCredentials != nil {
			expertPoolCredentials.CaURI = pool.PoolCredentials.CaURI
		}
		certificate, err := vsa.GetCertificateFromCacheOrSecretManager(ctx, expertPoolCredentials)
		if err != nil {
			return nil, err
		}
		credentials.Certificate.CommonName = certificate.CommonName
		credentials.Certificate.Certificate = certificate.SignedCertificate
		credentials.Certificate.PrivateKey = certificate.PrivateKey
		credentials.Certificate.InterMediateCertificate = certificate.InterMediateCertificates
	case env.USERNAME_PWD_SEC_MGR:
		secret, err := vsa.GetPasswordFromCacheOrSecretManager(ctx, pool.ExpertModeCredentials.ExpertModeCredential[0].SecretID)
		if err != nil {
			return nil, err
		}
		credentials.AdminPassword = secret
	default:
		credentials.AdminPassword = pool.ExpertModeCredentials.ExpertModeCredential[0].Password
	}
	return credentials, nil
}

// GetInterClusterLifsFromVLMConfig retrieves intercluster LIF IP addresses from VLM config
func (j *PoolActivity) GetInterClusterLifsFromVLMConfig(ctx context.Context, vlmConfig *vlm.VLMConfig) ([]string, error) {
	activity.RecordHeartbeat(ctx, "Starting GetInterClusterLifsFromVLMConfig activity")
	logger := util.GetLogger(ctx)

	logger.Info("Getting intercluster LIFs from VLM config")

	// Extract intercluster LIF IP addresses from VLM config's systemLifs
	var lifIPs []string

	// Iterate through all HA pairs to find intercluster LIFs
	if vlmConfig != nil && len(vlmConfig.Cloud.HAPairs) > 0 {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Processing %d HA pairs to extract intercluster LIFs", len(vlmConfig.Cloud.HAPairs)))
		for _, haPair := range vlmConfig.Cloud.HAPairs {
			// Check VM1 for intercluster LIFs
			if vm1Lif, exists := haPair.VM1.SystemLIFs[vlm.LIFTypeInterCluster]; exists {
				lifIPs = append(lifIPs, vm1Lif.IP)
				logger.Debug("Found intercluster LIF on VM1", "vmName", haPair.VM1.Name, "ipAddress", vm1Lif.IP)
			}

			// Check VM2 for intercluster LIFs
			if vm2Lif, exists := haPair.VM2.SystemLIFs[vlm.LIFTypeInterCluster]; exists {
				lifIPs = append(lifIPs, vm2Lif.IP)
				logger.Debug("Found intercluster LIF on VM2", "vmName", haPair.VM2.Name, "ipAddress", vm2Lif.IP)
			}
		}
	}

	logger.Info("Extracted intercluster LIF IPs from VLM config", "lifCount", len(lifIPs))
	activity.RecordHeartbeat(ctx, "Finished GetInterClusterLifsFromVLMConfig activity")
	return lifIPs, nil
}

// DetermineVMScalingDirection determines whether the new VM decision represents scaling up or down
// by using the decision maker's comparison method.
// Returns true if scaling up (new VM is more expensive), false if scaling down (new VM is cheaper).
func (j *PoolActivity) DetermineVMScalingDirection(ctx context.Context, vmrsConfigPath string, currentInstanceType string, newInstanceType string) (bool, error) {
	activity.RecordHeartbeat(ctx, "Starting DetermineVMScalingDirection activity")
	logger := util.GetLogger(ctx)
	logger.Debug("Determining VM scaling direction", "currentType", currentInstanceType, "newType", newInstanceType)

	activity.RecordHeartbeat(ctx, "Load VMRS config")
	// Parse VMRS config to get access to decision maker
	vmrsConfig, err := LoadVMRSConfig(vmrsConfigPath)
	if err != nil {
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Create decision maker")
	// Create decision maker to access the comparison method
	decisionMaker, err := CreateDecisionMaker(vmrsConfig)
	if err != nil {
		logger.Error("Failed to create decision maker", "error", err)
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Comparing VM scaling direction")
	// Use the decision maker's comparison method directly
	// This eliminates the need for type casting and makes the code more maintainable
	isScalingUp, err := decisionMaker.CompareVMScalingDirection(currentInstanceType, newInstanceType)
	if err != nil {
		logger.Error("Failed to compare VM scaling direction", "error", err)
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("VM scaling direction determined",
		"currentType", currentInstanceType,
		"newType", newInstanceType,
		"isScalingUp", isScalingUp)

	activity.RecordHeartbeat(ctx, "Finished DetermineVMScalingDirection activity")
	return isScalingUp, nil
}

// UpdatePoolFields updates specific fields of a pool without changing its state
// This is a generic method that can be used to update any combination of pool fields
func (j *PoolActivity) UpdatePoolFields(ctx context.Context, poolUUID string, updates map[string]interface{}) error {
	activity.RecordHeartbeat(ctx, "Starting UpdatePoolFields activity")
	se := j.SE

	activity.RecordHeartbeat(ctx, "Updating pool fields")
	err := se.UpdatePoolFields(ctx, poolUUID, updates)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished UpdatePoolFields activity")
	return nil
}

// _createLargeVolumeVMRSConfig creates a copy of the provided VMRS configuration and
// modifies it to use the large volume cluster selection strategy.
// This ensures that large capacity pools always use the appropriate decision maker
// regardless of the original configuration strategy.
func _createLargeVolumeVMRSConfig(originalConfig *vmrs.VMRSConfig) *vmrs.VMRSConfig {
	// Create a copy of the configuration to avoid modifying the original
	configCopy := *originalConfig

	// Override the VM selection strategy for large volume deployments
	configCopy.HyperscalerPerfLimits.VMSelectionStrategy = vmrs.LeastCostLargeVolumeCluster

	return &configCopy
}

func (j *PoolActivity) HydrateUpdatedPoolToCCFE(ctx context.Context, dbPool datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Initializing pool hydration to CCFE")
	logger := util.GetLogger(ctx)

	if !hydrationEnabled {
		logger.Warn("Hydration is disabled, skipping pool hydration to CCFE")
		return nil
	}

	activity.RecordHeartbeat(ctx, "Hydrating updated pool to CCFE")
	err := hydrationActivities.HydrateUpdatedPoolToCCFE(ctx, dbPool)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Pool hydrated to CCFE successfully")
	return nil
}

// Add this function
func _listAddressesByDeployment(cloudService hyperscaler2.Services, projectName, region, deploymentID string) (*[]hyperscaler_models.Address, error) {
	return cloudService.ListAddressesByDeployment(projectName, region, deploymentID)
}

// FetchPoolData fetches pool data from database and parses VLM config
func (a *PoolActivity) FetchPoolData(ctx context.Context, input FetchPoolDataActivityInput) (*FetchPoolDataActivityOutput, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Starting pool data fetch", "poolUUID", input.PoolUUID, "accountID", input.AccountID)

	// Record activity heartbeat
	activity.RecordHeartbeat(ctx, "Starting pool data fetch")

	// Fetch the pool from database
	poolView, err := a.SE.GetPool(ctx, input.PoolUUID, input.AccountID)
	if err != nil {
		logger.Error("Failed to fetch pool", "poolUUID", input.PoolUUID, "error", err)
		return &FetchPoolDataActivityOutput{
			PoolUUID: input.PoolUUID,
			Success:  false,
			Error:    fmt.Sprintf("Failed to fetch pool: %v", err),
		}, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Record activity heartbeat
	activity.RecordHeartbeat(ctx, "Pool fetched, parsing VLM config")

	if commonparams.IsPOPool(&poolView.Pool) {
		logger.Info("PO pool detected; skipping VLM config parsing", "poolUUID", input.PoolUUID)
		return &FetchPoolDataActivityOutput{
			PoolUUID: input.PoolUUID,
			Success:  true,
			IsPOPool: true,
		}, nil
	}

	// Parse VLM config from pool
	var vlmConfig vlm.VLMConfig
	if poolView.VLMConfig != "" {
		err = json.Unmarshal([]byte(poolView.VLMConfig), &vlmConfig)
		if err != nil {
			logger.Error("Failed to parse VLM config", "poolUUID", input.PoolUUID, "error", err)
			return &FetchPoolDataActivityOutput{
				PoolUUID: input.PoolUUID,
				Success:  false,
				Error:    fmt.Sprintf("Failed to parse VLM config: %v", err),
			}, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
		}
	} else {
		logger.Error("Failed to get VLM config", "poolUUID", input.PoolUUID, "error", err)
		return &FetchPoolDataActivityOutput{
			PoolUUID: input.PoolUUID,
			Success:  false,
			Error:    fmt.Sprintf("Failed to get VLM config: %v", err),
		}, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
	}

	logger.Info("Pool data fetch completed successfully", "poolUUID", input.PoolUUID)

	var accountName string
	if poolView.Account != nil {
		accountName = poolView.Account.Name
	}
	return &FetchPoolDataActivityOutput{
		PoolUUID:              input.PoolUUID,
		VLMConfig:             vlmConfig,
		Success:               true,
		AccountName:           accountName,
		AutoTieringEnabled:    poolView.AllowAutoTiering,
		AutoTieringBucketName: poolView.AutoTieringConfig.BucketName,
	}, nil
}

// UpdatePoolCompliance updates the pool with compliance data
func (a *PoolActivity) UpdatePoolCompliance(ctx context.Context, input UpdatePoolComplianceActivityInput) (*UpdatePoolComplianceActivityOutput, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Starting pool compliance update", "poolUUID", input.PoolUUID)

	// Record activity heartbeat
	activity.RecordHeartbeat(ctx, "Updating pool compliance fields")

	// Update the pool with compliance data
	updates := map[string]interface{}{
		"satisfy_zi": input.SatisfyZI,
		"satisfy_zs": input.SatisfyZS,
	}

	// Add asset metadata if provided
	if input.AssetMetadata != nil {
		updates["asset_metadata"] = input.AssetMetadata
	}

	err := a.SE.UpdatePoolFields(ctx, input.PoolUUID, updates)
	// Record heartbeat before the update call to signal we've started persisting
	activity.RecordHeartbeat(ctx, "Persisting asset metadata")
	logger.Info("Committing pool updates", "poolUUID", input.PoolUUID, "satisfyZI", input.SatisfyZI, "satisfyZS", input.SatisfyZS)
	if err != nil {
		logger.Error("Failed to update pool compliance fields", "poolUUID", input.PoolUUID, "error", err)
		return &UpdatePoolComplianceActivityOutput{
			PoolUUID: input.PoolUUID,
			Success:  false,
			Error:    fmt.Sprintf("Failed to update pool compliance fields: %v", err),
		}, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	logger.Info("Pool compliance update completed successfully",
		"poolUUID", input.PoolUUID,
		"satisfyZI", input.SatisfyZI,
		"satisfyZS", input.SatisfyZS)

	return &UpdatePoolComplianceActivityOutput{
		PoolUUID: input.PoolUUID,
		Success:  true,
	}, nil
}

func (a *PoolActivity) GetBucketCompliance(ctx context.Context, bucketName string) (*datamodel.BucketDetails, error) {
	activity.RecordHeartbeat(ctx, "Initializing bucket compliance check")
	logger := util.GetLogger(ctx)

	if bucketName == "" {
		logger.Errorf("Bucket name parameter is empty, required to fetch zi/zs compliance")
		return nil, fmt.Errorf("bucket name parameter is required to fetch zi/zs compliance")
	}

	activity.RecordHeartbeat(ctx, "Getting cloud service")
	// Get cloud service
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		logger.Errorf("Failed to get cloud service during AT bucket compliance check: %v", err)
		return nil, err
	}

	activity.RecordHeartbeat(ctx, "Retrieving bucket compliance details from the cloud provider")
	// Get bucket details from the cloud provider API
	cloudBucketDetails, err := cloudService.GetBucket(ctx, bucketName)
	if err != nil {
		logger.Errorf("Failed to get bucket details from the cloud provider for fetching zi/zs compliance, error: %v", err)
		return nil, err
	}

	activity.RecordHeartbeat(ctx, "Bucket compliance check completed successfully")
	logger.Infof("Successfully retrieved bucket details from the cloud provider for fetching zi/zs compliance, bucketName: %s", bucketName)
	logger.Infof("Received bucket compliance details from the cloud provider - satisfiesPzi: %t, satisfiesPzs: %t", cloudBucketDetails.SatisfiesPzi, cloudBucketDetails.SatisfiesPzs)

	return &datamodel.BucketDetails{
		BucketName:   bucketName,
		SatisfiesPzi: cloudBucketDetails.SatisfiesPzi,
		SatisfiesPzs: cloudBucketDetails.SatisfiesPzs,
	}, nil
}

// FetchPoolDataActivityInput represents the input for fetching pool data
type FetchPoolDataActivityInput struct {
	PoolUUID  string `json:"pool_uuid"`
	AccountID int64  `json:"account_id"`
}

// FetchPoolDataActivityOutput represents the output for fetching pool data
type FetchPoolDataActivityOutput struct {
	PoolUUID              string        `json:"pool_uuid"`
	VLMConfig             vlm.VLMConfig `json:"vlm_config"`
	Success               bool          `json:"success"`
	Error                 string        `json:"error,omitempty"`
	IsPOPool              bool          `json:"is_po_pool,omitempty"`
	AccountName           string        `json:"account_name"`
	AutoTieringEnabled    bool          `json:"auto_tiering_enabled"`
	AutoTieringBucketName string        `json:"auto_tiering_bucket_name"`
}

// UpdatePoolComplianceActivityInput represents the input for updating pool compliance
type UpdatePoolComplianceActivityInput struct {
	PoolUUID      string                   `json:"pool_uuid"`
	SatisfyZI     bool                     `json:"satisfy_zi"`
	SatisfyZS     bool                     `json:"satisfy_zs"`
	AssetMetadata *datamodel.AssetMetadata `json:"asset_metadata,omitempty"`
}

// UpdatePoolComplianceActivityOutput represents the output for updating pool compliance
type UpdatePoolComplianceActivityOutput struct {
	PoolUUID string `json:"pool_uuid"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

// CalculateBatchPlanActivityInput represents input for calculating batch plan
type CalculateBatchPlanActivityInput struct {
	NumHAPairs                  int   `json:"num_ha_pairs"`
	HAPairIndices               []int `json:"ha_pair_indices,omitempty"` // 1-based; empty means all pairs in the cluster
	ParallelNumberOfNodesForITC int   `json:"parallel_number_of_nodes_for_itc"`
}

// CalculateBatchPlanActivityOutput represents output for batch plan calculation
type CalculateBatchPlanActivityOutput struct {
	NumHAPairs       int     `json:"num_ha_pairs"`
	BatchSize        int     `json:"batch_size"`
	NumWorkflowCalls int     `json:"num_workflow_calls"`
	BatchIndices     [][]int `json:"batch_indices"` // Each inner slice contains HA pair indices for that batch
}

// normalizeHAPairIndicesForBatchPlan returns sorted unique 1-based HA pair indices to include in
// the batch plan. An empty requested slice means all pairs in the cluster.
func normalizeHAPairIndicesForBatchPlan(requested []int, clusterHAPairCount int) ([]int, error) {
	if clusterHAPairCount <= 0 {
		return nil, fmt.Errorf("invalid cluster HA pair count: %d", clusterHAPairCount)
	}
	if len(requested) == 0 {
		out := make([]int, clusterHAPairCount)
		for i := range out {
			out[i] = i + 1
		}
		return out, nil
	}
	seen := make(map[int]struct{}, len(requested))
	out := make([]int, 0, len(requested))
	for _, idx := range requested {
		if idx < 1 || idx > clusterHAPairCount {
			return nil, fmt.Errorf("HA pair index %d out of range [1,%d]", idx, clusterHAPairCount)
		}
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		out = append(out, idx)
	}
	slices.Sort(out)
	return out, nil
}

// GetNodeUUIDToNameForPool maps node UUID to node name for a pool.
func (j *PoolActivity) GetNodeUUIDToNameForPool(ctx context.Context, poolID int64) (map[string]string, error) {
	activity.RecordHeartbeat(ctx, "Starting GetNodeUUIDToNameForPool activity")
	nodes, err := j.SE.GetNodesByPoolID(ctx, poolID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	uuidToName := make(map[string]string, len(nodes))
	for _, node := range nodes {
		if node == nil || node.UUID == "" {
			continue
		}
		if node.Name == "" {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
					fmt.Errorf("node %q has empty name", node.UUID)))
		}
		uuidToName[node.UUID] = node.Name
	}
	activity.RecordHeartbeat(ctx, "Finished GetNodeUUIDToNameForPool activity")
	return uuidToName, nil
}

// CalculateBatchPlanForUpdate calculates the batch plan for HA pair updates
func (a *PoolActivity) CalculateBatchPlanForUpdate(ctx context.Context, input CalculateBatchPlanActivityInput) (*CalculateBatchPlanActivityOutput, error) {
	activity.RecordHeartbeat(ctx, "Starting CalculateBatchPlanForUpdate activity")
	logger := util.GetLogger(ctx)
	logger.Info("Calculating update batch plan as per the parallel number of nodes for ITC", "numHAPairs", input.NumHAPairs, "parallelNumberOfNodesForITC", input.ParallelNumberOfNodesForITC)

	if input.NumHAPairs <= 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("invalid number of HA pairs: %d", input.NumHAPairs))
	}

	pairIndices, err := normalizeHAPairIndicesForBatchPlan(input.HAPairIndices, input.NumHAPairs)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(pairIndices) == 0 {
		return &CalculateBatchPlanActivityOutput{
			NumHAPairs:       0,
			BatchSize:        0,
			NumWorkflowCalls: 0,
			BatchIndices:     nil,
		}, nil
	}

	numHAPairs := len(pairIndices)
	parallelNumberOfNodesForITC := input.ParallelNumberOfNodesForITC

	if parallelNumberOfNodesForITC <= 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("invalid parallel number of nodes for ITC: %d", parallelNumberOfNodesForITC))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Calculating batch size for %d HA pairs with %d parallel nodes", numHAPairs, parallelNumberOfNodesForITC))
	batchSize := max(1, (numHAPairs*2)/parallelNumberOfNodesForITC)

	// numWorkflowCalls = ceil(numHAPairs / batchSize)
	numWorkflowCalls := (numHAPairs + batchSize - 1) / batchSize

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Generating batch indices for %d workflow calls with batch size %d", numWorkflowCalls, batchSize))
	batchIndices := make([][]int, 0, numWorkflowCalls)
	for batchNum := 0; batchNum < numWorkflowCalls; batchNum++ {
		startIdx := batchNum * batchSize
		endIdx := startIdx + batchSize
		if endIdx > numHAPairs {
			endIdx = numHAPairs
		}
		batchIndices = append(batchIndices, slices.Clone(pairIndices[startIdx:endIdx]))
	}

	logger.Info("Batch plan calculated", "numHAPairs", numHAPairs, "batchSize", batchSize, "numWorkflowCalls", numWorkflowCalls, "haPairIndices", pairIndices)
	activity.RecordHeartbeat(ctx, "Finished CalculateBatchPlanForUpdate activity")

	return &CalculateBatchPlanActivityOutput{
		NumHAPairs:       numHAPairs,
		BatchSize:        batchSize,
		NumWorkflowCalls: numWorkflowCalls,
		BatchIndices:     batchIndices,
	}, nil
}

func (j *PoolActivity) RestorePoolPreUpdatePoolLevelFields(ctx context.Context, poolUUID string, snapshot *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Starting RestorePoolPreUpdatePoolLevelFields activity")
	if snapshot == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			fmt.Errorf("restore pool pre-update fields: snapshot is nil"))
	}

	updates := map[string]interface{}{
		"size_in_bytes":      snapshot.SizeInBytes,
		"description":        snapshot.Description,
		"allow_auto_tiering": snapshot.AllowAutoTiering,
		"vlm_config":         snapshot.VLMConfig,
	}
	if snapshot.PoolAttributes != nil {
		updates["pool_attributes"] = snapshot.PoolAttributes
	}
	if snapshot.AutoTieringConfig != nil {
		updates["auto_tiering_config"] = snapshot.AutoTieringConfig
	}

	se := j.SE
	activity.RecordHeartbeat(ctx, "Restoring pre-update pool snapshot fields")
	if err := se.UpdatePoolFields(ctx, poolUUID, updates); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Finished RestorePoolPreUpdatePoolLevelFields activity")
	return nil
}

// GetCreateJobByResourceUUID retrieves the create job for a resource by resource UUID and validates correlation ID
// Returns CreateJobResult with job UUID and workflow ID if found and correlation ID matches
func (j *PoolActivity) GetCreateJobByResourceUUID(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*commonparams.CreateJobResult, error) {
	logger := util.GetLogger(ctx)
	se := j.SE

	// Get the create job for this resource by resource UUID
	createJob, err := se.GetJobByResourceUUID(ctx, resourceUUID, jobType)
	if err != nil {
		logger.Warnf("Could not find create job for resource %s with job type %s: %v", resourceUUID, jobType, err)
		return nil, err
	}

	// Validate correlation ID matches
	if correlationID != "" && createJob.CorrelationID != correlationID {
		logger.Warnf("Correlation ID mismatch: create job correlation ID %s does not match delete request correlation ID %s",
			createJob.CorrelationID, correlationID)
		return nil, fmt.Errorf("correlation ID mismatch")
	}

	logger.Infof("Found matching create job %s with workflow ID %s for job type %s", createJob.UUID, createJob.WorkflowID, jobType)

	return &commonparams.CreateJobResult{
		JobUUID:    createJob.UUID,
		WorkflowID: createJob.WorkflowID,
	}, nil
}

// HasNasLifInVLMConfig checks if the VLMConfig has naslif (ilbnas) details in any SVM.
// This checks for LIFTypeIlbNas in the SVMLIFs of any SVM in the config.
func (j *PoolActivity) HasNasLifInVLMConfig(ctx context.Context, vlmConfig vlm.VLMConfig) (bool, error) {
	activity.RecordHeartbeat(ctx, "Checking for NAS LIF in VLM config")
	if vlmConfig.Svm == nil || len(vlmConfig.Svm) == 0 {
		return false, nil
	}

	// Iterate through all SVMs
	for _, svmConfig := range vlmConfig.Svm {
		if svmConfig.SVMLIFs == nil {
			continue
		}

		// Check if ilbnas exists in svm_lifs and has at least one LIF
		if ilbNasLifs, exists := svmConfig.SVMLIFs[vlm.LIFTypeIlbNas]; exists && len(ilbNasLifs) > 0 {
			return true, nil
		}
	}

	return false, nil
}

// MarshalVLMConfig marshals a VLMConfig to JSON string.
func (j *PoolActivity) MarshalVLMConfig(ctx context.Context, vlmConfig vlm.VLMConfig) (string, error) {
	activity.RecordHeartbeat(ctx, "Marshaling VLM config")
	marshalledVlmConfig, err := json.Marshal(vlmConfig)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return string(marshalledVlmConfig), nil
}

// addServiceAccountPermissionProject adds a tenant project to the pool's service account permission projects list.
// When IAM roles are granted to pool service accounts in tenant projects (e.g., for cross-region backup),
// we track those projects so we can proactively clean up the IAM policies when the pool is deleted.
// This prevents orphaned IAM policies that would otherwise persist for 60 days.
func addServiceAccountPermissionProject(ctx context.Context, se database.Storage, poolUUID string, tenantProject string) error {
	logger := util.GetLogger(ctx)
	pool, err := se.GetPoolByUUID(ctx, poolUUID)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return err
	}

	if pool.PoolAttributes == nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("pool %s has nil PoolAttributes", poolUUID))
	}

	// Initializing the ServiceAccountPermissionProjects
	if pool.PoolAttributes.ServiceAccountPermissionProjects == nil {
		pool.PoolAttributes.ServiceAccountPermissionProjects = []string{}
	}

	// Check if a tenant project is already tracked (idempotent)
	if slices.Contains(pool.PoolAttributes.ServiceAccountPermissionProjects, tenantProject) {
		logger.Infof("Tenant project %s already tracked for pool %s", tenantProject, poolUUID)
		return nil
	}

	pool.PoolAttributes.ServiceAccountPermissionProjects = append(pool.PoolAttributes.ServiceAccountPermissionProjects, tenantProject)

	updates := map[string]interface{}{
		"pool_attributes": pool.PoolAttributes,
	}

	if err := se.UpdatePoolFields(ctx, poolUUID, updates); err != nil {
		logger.Errorf("Failed to update pool %s with tenant project: %v", poolUUID, err)
		return err
	}

	logger.Infof("Successfully tracked tenant project %s for pool %s", tenantProject, poolUUID)
	return nil
}

// CleanupServiceAccountPermissionsInTenantProjects removes all IAM roles assigned to the pool's service account
func (j *PoolActivity) CleanupServiceAccountPermissionsInTenantProjects(ctx context.Context, pool *datamodel.Pool) error {
	logger := util.GetLogger(ctx)
	activityStartTime := time.Now()
	activity.RecordHeartbeat(ctx, "Starting service account permissions cleanup in tenant projects")

	if pool.PoolAttributes == nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("pool %s has nil PoolAttributes", pool.UUID))
	}

	if len(pool.PoolAttributes.ServiceAccountPermissionProjects) == 0 {
		logger.Debugf("No tenant projects to cleanup for pool %s", pool.UUID)
		return nil
	}

	// Initialize cloud service and construct service account email
	cloudService, err := GetCloudService(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get cloud service: %w", err))
	}

	saEmail := utils.ConstructServiceAccountEmail(pool.ServiceAccountId, pool.ClusterDetails.RegionalTenantProject)
	tenantProjects := pool.PoolAttributes.ServiceAccountPermissionProjects
	totalProjects := len(tenantProjects)

	logger.Infof("Cleaning up all IAM roles for service account %s from %d tenant projects", saEmail, totalProjects)
	var failures []string

	// Cleanup IAM roles from each tenant project
	for i, tenantProject := range tenantProjects {
		projectStartTime := time.Now()
		logger.Debugf("Starting cleanup for tenant project %s (%d/%d)", tenantProject, i+1, totalProjects)

		// Fetch all roles assigned to the service account in this tenant project
		roles, err := getServiceAccountRolesInProject(cloudService, saEmail, tenantProject)
		if err != nil {
			projectDuration := time.Since(projectStartTime)
			logger.Errorf("Failed to fetch IAM roles for service account %s in tenant project %s: %v (duration: %v)", saEmail, tenantProject, err, projectDuration)
			failures = append(failures, fmt.Sprintf("project %s: failed to fetch roles: %v", tenantProject, err))
			continue
		}

		if len(roles) == 0 {
			logger.Debugf("No IAM roles found for service account %s in tenant project %s", saEmail, tenantProject)
		} else {
			logger.Debugf("Found %d IAM role(s) for service account %s in tenant project %s: %v", len(roles), saEmail, tenantProject, roles)
			if err := cloudService.RemoveRolesFromServiceAccounts(roles, saEmail, tenantProject); err != nil {
				projectDuration := time.Since(projectStartTime)
				logger.Errorf("Failed to remove IAM roles from tenant project %s: %v (duration: %v)", tenantProject, err, projectDuration)
				failures = append(failures, fmt.Sprintf("project %s: failed to remove roles: %v", tenantProject, err))
				continue
			}
			projectDuration := time.Since(projectStartTime)
			logger.Debugf("Successfully removed %d IAM role(s) from tenant project %s (duration: %v)", len(roles), tenantProject, projectDuration)
		}

		// Record heartbeat every 5 projects or at the end
		if (i+1)%5 == 0 || i == totalProjects-1 {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Cleaned up %d/%d tenant projects", i+1, totalProjects))
		}
	}

	totalActivityDuration := time.Since(activityStartTime)

	if len(failures) > 0 {
		errMsg := fmt.Sprintf("IAM cleanup completed with %d/%d failures: %s", len(failures), totalProjects, strings.Join(failures, "; "))
		logger.Errorf("Activity completed with failures - total duration: %v, projects processed: %d", totalActivityDuration, totalProjects)
		return vsaerrors.WrapAsTemporalApplicationError(errors.New(errMsg))
	}

	logger.Infof("Successfully cleaned up IAM roles from all %d tenant projects - total activity duration: %v (avg per project: %v)",
		totalProjects, totalActivityDuration, totalActivityDuration/time.Duration(totalProjects))
	return nil
}

// getServiceAccountRolesInProject fetches all IAM roles assigned to a service account in a specific project.
func getServiceAccountRolesInProject(cloudService hyperscaler2.Services, saEmail string, projectID string) ([]string, error) {
	return cloudService.GetServiceAccountRoles(saEmail, projectID)
}

// MarkAddressRangeInUse transitions the address range that contains allocatedSubnetCIDR to IN_USE.
// It uses CIDR containment to identify the correct range: the registered CIDR that contains the
// IP of the GCP-allocated subnet (e.g. 10.55.55.16/29 is contained within 10.55.55.0/24).
// Only runs when ADDRESS_SPACE_MGMT_ENABLED=true.
// If the matched range is already IN_USE (shared by multiple pools on the same network), this is a no-op.
func (j *PoolActivity) MarkAddressRangeInUse(ctx context.Context, allocatedSubnetCIDR string, network string) error {
	if !addressSpaceMgmtEnabled() {
		return nil
	}
	if allocatedSubnetCIDR == "" || network == "" {
		return nil
	}

	hostProjectNumber, vpcName, _ := utils.ParseProjectId(network)
	if hostProjectNumber == "" || vpcName == "" {
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Marking address range in use - subnet: %s, network: %s", allocatedSubnetCIDR, network))
	logger := util.GetLogger(ctx)

	// Parse the IP from the allocated subnet CIDR (e.g. "10.55.55.16" from "10.55.55.16/29").
	allocatedIP, _, err := net.ParseCIDR(allocatedSubnetCIDR)
	if err != nil {
		return fmt.Errorf("MarkAddressRangeInUse: invalid allocatedSubnetCIDR %q: %w", allocatedSubnetCIDR, err)
	}

	lifType := database.AddressRangeLifTypeDataLIF
	addressRanges, err := j.SE.ListAddressRanges(ctx, hostProjectNumber, vpcName, nil, &lifType)
	if err != nil {
		return err
	}

	for _, ar := range addressRanges {
		_, registeredNet, err := net.ParseCIDR(ar.AddressRangeCidr)
		if err != nil {
			continue
		}
		if registeredNet.Contains(allocatedIP) {
			switch ar.AddressRangeState {
			case database.AddressRangeStateCreated:
				if _, err = j.SE.UpdateAddressRangeState(ctx, ar.UUID, database.AddressRangeStateInUse, nil); err != nil {
					return err
				}
				logger.Infof("MarkAddressRangeInUse: transitioned address range %s (%s) to IN_USE — allocated subnet %s is within this range, network=%s", ar.UUID, ar.AddressRangeCidr, allocatedSubnetCIDR, network)
				return nil
			case database.AddressRangeStateInUse:
				// Another pool is already sharing this range — no state change needed.
				logger.Infof("MarkAddressRangeInUse: address range %s (%s) already IN_USE — allocated subnet %s is within this range, skipping update, network=%s", ar.UUID, ar.AddressRangeCidr, allocatedSubnetCIDR, network)
				return nil
			default:
				// Range matches by CIDR but is in an unexpected state (DISABLED/DELETED) — skip and keep searching.
				logger.Warnf("MarkAddressRangeInUse: address range %s (%s) matches subnet %s but is in unexpected state %q — skipping, network=%s", ar.UUID, ar.AddressRangeCidr, allocatedSubnetCIDR, ar.AddressRangeState, network)
			}
		}
	}
	logger.Infof("MarkAddressRangeInUse: no registered address range found containing allocated subnet %s, network=%s", allocatedSubnetCIDR, network)
	return nil
}

// MarkAddressRangesCreated resets IN_USE address ranges for a VPC back to CREATED state,
// but only for the specific range used by the deleted pool when no remaining active pool
// still has a subnet carved from that same registered range.
// Only runs when ADDRESS_SPACE_MGMT_ENABLED=true.
func (j *PoolActivity) MarkAddressRangesCreated(ctx context.Context, pool *datamodel.Pool) error {
	if !addressSpaceMgmtEnabled() {
		return nil
	}
	if pool == nil || pool.Network == "" {
		return nil
	}
	recordHeartbeat(ctx, fmt.Sprintf("Marking address ranges as created for pool %s, network: %s", pool.UUID, pool.Network))
	logger := util.GetLogger(ctx)
	logger.Infof("MarkAddressRangesCreated: starting, poolUUID=%s network=%s allocatedSubnetCIDR=%s", pool.UUID, pool.Network, pool.ClusterDetails.AllocatedSubnetCIDR)
	hostProjectNumber, vpcName, err := utils.ParseProjectId(pool.Network)
	if err != nil {
		return err
	}

	deletedPoolSubnetCIDR, err := j.getPoolAllocatedSubnetCIDR(ctx, pool)
	if err != nil {
		return err
	}
	logger.Infof("MarkAddressRangesCreated: resolved deletedPoolSubnetCIDR=%q poolUUID=%s", deletedPoolSubnetCIDR, pool.UUID)

	lifType := database.AddressRangeLifTypeDataLIF
	addressRanges, err := j.SE.ListAddressRanges(ctx, hostProjectNumber, vpcName, nil, &lifType)
	if err != nil {
		return err
	}

	if deletedPoolSubnetCIDR == "" {
		// CIDR is unknown (legacy pool without AllocatedSubnetCIDR in DB, or subnet was
		// already deleted before we could read it). Use CVN-style fallback: if no other
		// active pools remain on this network, reset all IN_USE address ranges for the VPC.
		remaining, countErr := j.SE.CountActivePoolsByNetwork(ctx, pool.Network, pool.UUID)
		if countErr != nil {
			return countErr
		}
		if remaining > 0 {
			logger.Infof("MarkAddressRangesCreated: skipping reset (fallback path) — %d active pool(s) still on network %s, poolUUID=%s", remaining, pool.Network, pool.UUID)
			return nil
		}
		logger.Infof("MarkAddressRangesCreated: no active pools remain on network %s, resetting all IN_USE ranges for vpc=%s hostProject=%s, poolUUID=%s", pool.Network, vpcName, hostProjectNumber, pool.UUID)
		return j.SE.ResetAddressRangesInUseToCreated(ctx, hostProjectNumber, vpcName)
	}

	targetRange, err := findAddressRangeContainingSubnet(addressRanges, deletedPoolSubnetCIDR)
	if err != nil || targetRange == nil {
		logger.Infof("MarkAddressRangesCreated: no registered address range found containing subnet %s for poolUUID=%s network=%s", deletedPoolSubnetCIDR, pool.UUID, pool.Network)
		return err
	}

	if targetRange.AddressRangeState == database.AddressRangeStateInUse {
		// Atomically reset to CREATED only when no other active pool on this network has
		// an allocated subnet CIDR within the same registered range. The conditional UPDATE
		// with a NOT EXISTS subquery serialises concurrent deletions at the DB level,
		// preventing the race where two pools each see the other as still-active and both skip the revert.
		updated, err := j.SE.UpdateAddressRangeStateToCreatedIfLastPool(
			ctx, targetRange.UUID, pool.Network, pool.UUID, targetRange.AddressRangeCidr,
		)
		if err != nil {
			return err
		}
		if updated {
			logger.Infof("MarkAddressRangesCreated: reset address range %s (%s) to CREATED — last pool using this range deleted, poolUUID=%s", targetRange.UUID, targetRange.AddressRangeCidr, pool.UUID)
		} else {
			logger.Infof("MarkAddressRangesCreated: skipping reset of address range %s (%s) — other active pool(s) still have subnets within this range, poolUUID=%s", targetRange.UUID, targetRange.AddressRangeCidr, pool.UUID)
		}
	}
	return nil
}

func (j *PoolActivity) GetPoolTenancyInfo(ctx context.Context, pool *datamodel.Pool) (*commonparams.TenancyInfo, error) {
	if pool != nil {
		recordHeartbeat(ctx, fmt.Sprintf("Fetching pool tenancy info for pool %s", pool.UUID))
	}
	allocatedSubnetCIDR, err := j.getPoolAllocatedSubnetCIDR(ctx, pool)
	if err != nil {
		return nil, err
	}
	return &commonparams.TenancyInfo{
		AllocatedSubnetCIDR: allocatedSubnetCIDR,
	}, nil
}

func (j *PoolActivity) getPoolAllocatedSubnetCIDR(ctx context.Context, pool *datamodel.Pool) (string, error) {
	if pool == nil {
		return "", nil
	}
	logger := util.GetLogger(ctx)
	if pool.ClusterDetails.AllocatedSubnetCIDR != "" {
		logger.Debugf("getPoolAllocatedSubnetCIDR: using AllocatedSubnetCIDR from DB=%s poolUUID=%s", pool.ClusterDetails.AllocatedSubnetCIDR, pool.UUID)
		return pool.ClusterDetails.AllocatedSubnetCIDR, nil
	}
	if len(pool.ClusterDetails.SubnetNames) == 0 || pool.ClusterDetails.SnHostProject == "" || Region == "" {
		logger.Debugf("getPoolAllocatedSubnetCIDR: no AllocatedSubnetCIDR in DB and missing subnet details, returning empty poolUUID=%s subnetNames=%v snHostProject=%s region=%s", pool.UUID, pool.ClusterDetails.SubnetNames, pool.ClusterDetails.SnHostProject, Region)
		return "", nil
	}
	service, err := hyperscaler2.ResolveCloudService(ctx)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPClientInitializationError, err))
	}
	if len(pool.ClusterDetails.SubnetNames) == 0 {
		return "", nil
	}
	subnetName := pool.ClusterDetails.SubnetNames[len(pool.ClusterDetails.SubnetNames)-1]
	logger.Debugf("getPoolAllocatedSubnetCIDR: looking up subnet from the cloud provider, subnetName=%s snHostProject=%s poolUUID=%s", subnetName, pool.ClusterDetails.SnHostProject, pool.UUID)
	subnet, err := service.GetSubnetwork(pool.ClusterDetails.SnHostProject, Region, subnetName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			// Subnet was already deleted (common on the delete workflow path); CIDR unavailable.
			logger.Debugf("getPoolAllocatedSubnetCIDR: subnet not found in the cloud provider (already deleted), returning empty poolUUID=%s subnetName=%s", pool.UUID, subnetName)
			return "", nil
		}
		return "", err
	}
	if subnet == nil {
		logger.Debugf("getPoolAllocatedSubnetCIDR: cloud provider returned nil subnet, returning empty poolUUID=%s subnetName=%s", pool.UUID, subnetName)
		return "", nil
	}
	logger.Debugf("getPoolAllocatedSubnetCIDR: resolved CIDR=%s from the cloud provider for poolUUID=%s subnetName=%s", subnet.IpCidrRange, pool.UUID, subnetName)
	return subnet.IpCidrRange, nil
}

func findAddressRangeContainingSubnet(addressRanges []*datamodel.AddressRange, subnetCIDR string) (*datamodel.AddressRange, error) {
	if _, _, err := net.ParseCIDR(subnetCIDR); err != nil {
		return nil, err
	}
	for _, ar := range addressRanges {
		if subnetCIDRWithinRange(subnetCIDR, ar.AddressRangeCidr) {
			return ar, nil
		}
	}
	return nil, nil
}

func subnetCIDRWithinRange(subnetCIDR string, rangeCIDR string) bool {
	subnetIP, _, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return false
	}
	_, rangeNet, err := net.ParseCIDR(rangeCIDR)
	if err != nil {
		return false
	}
	return rangeNet.Contains(subnetIP)
}

func ociOntapAdminSecretName(pool *datamodel.Pool) string {
	return fmt.Sprintf("%s-secret", pool.DeploymentName)
}

func ociOntapCertificateName(pool *datamodel.Pool) string {
	return fmt.Sprintf("%s-cert", pool.DeploymentName)
}

func ociOntapClientCertificateName(pool *datamodel.Pool) string {
	return fmt.Sprintf("%s-client-cert", pool.DeploymentName)
}

func ociOntapClientCACertificateName(pool *datamodel.Pool) string {
	return fmt.Sprintf("%s-client-ca", pool.DeploymentName)
}

// ociOntapServiceCertificateName is the OCI cert name for the dedicated
// read-only service certificate ("{deployment}-svc-cert").
func ociOntapServiceCertificateName(pool *datamodel.Pool) string {
	return fmt.Sprintf("%s-svc-cert", pool.DeploymentName)
}

// ociOntapServiceCACertificateName is the ONTAP client_ca trust name used when
// the service issuer CA must be installed ("{deployment}-svc-ca").
func ociOntapServiceCACertificateName(pool *datamodel.Pool) string {
	return fmt.Sprintf("%s-svc-ca", pool.DeploymentName)
}

// ociOntapServiceAccountName derives the read-only ONTAP account name (and the
// service cert CN) as "{base}_oci_svc", where base is recovered from the admin
// username ("{base}_admin"). This guarantees a distinct identity from the admin
// account even when the same issuer CA is shared.
func ociOntapServiceAccountName(pool *datamodel.Pool) string {
	base := strings.TrimSuffix(pool.PoolCredentials.Username, ociAdminCertUsernameSuffix)
	return base + ociServiceCertUsernameSuffix
}

// isOCISyntheticSNI reports whether host is a synthetic TLS-SNI value rather
// than a real OCI Private DNS A record.
// On the SNI-override path, _saveNodeDetails persists
// "mgmt.<deploymentName>.<env.VsaDeployedDnsName>" into Node.HostDNSName for
// every node — a value that is NEVER published in DNS (it is only fed to
// tls.Config.ServerName). On the legacy DNS path it persists per-node
// "dns-N.<deploymentName>.<env.VsaDeployedDnsName>." A records that DO exist
// in OCI Private DNS.
// Pool-delete must only issue OCI DNS deletes for the latter. We derive this
// from persisted node state (HostDNSName), not env.OCIUseTLSSNIOverride, so the
// decision stays correct for a given pool even if the flag was flipped between
// pool create and delete.
func isOCISyntheticSNI(host string) bool {
	return strings.HasPrefix(strings.TrimSpace(host), "mgmt.")
}
