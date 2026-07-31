package workflows

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
	vmrs_decision "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/decision"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	vsaerror "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	logger "golang.org/x/exp/slog"
	"google.golang.org/api/iam/v1"
)

var (
	configureKmsConfigForSvmActivity     = _configureKmsConfigForSvmActivity
	isProberProject                      = utils.IsProberProject
	GetNewVSAClientWorkflowManager       = _getNewVSAClientWorkflowManager
	ExtractOntapVersion                  = utils.ExtractOntapVersion
	WaitForServiceNetworkOperationStatus = _waitForServiceNetworkOperationStatus
	WaitForGCPNetworkOperationStatus     = _waitForGCPNetworkOperationStatus
	verifyKmsConfigReachability          = _verifyKmsConfigReachability
	syncPoolZIZSDetailsWorkflow          = _syncPoolZIZSDetailsWorkflow
	syncActiveDirectoryInVcp             = _syncActiveDirectoryInVcp
	deepCopyPoolFn                       = utils.DeepCopyPool
)

var (
	_                       WorkflowInterface = &createPoolWorkflow{} // Enforcing the WorkflowInterface on createPoolWorkflow
	setupNwHeartbeatTimeout                   = env.GetUint64("SETUP_NW_HEARTBEAT_TIMEOUT_SEC", 300)
	vmrsConfigPath                            = env.GetString("VMRS_CONFIG_PATH", "/config/vmrs_gcp.yaml")
	// vmrsConfigPathV2 is the c3 + c4 catalog, used when ONTAP supports c4; the
	// legacy vmrsConfigPath above is c3-only. See resolveVMRSConfigPath.
	vmrsConfigPathV2                   = env.GetString("VMRS_CONFIG_PATH_V2", "/config/vmrs_gcp_v2.yaml")
	maxNodesPerGroup                   = env.GetInt("MAX_NODES_PER_GROUP", 200)
	enableMetrics                      = env.GetBool("ENABLE_METRICS", false)
	enableUniqueSerialNumberGeneration = env.GetBool("ENABLE_UNIQUE_SERIAL_NUMBER_GENERATION", false)
	Region                             = env.GetString("LOCAL_REGION", "")

	vsaImageName                 = env.GetString("VSA_IMAGE_NAME", "")
	mediatorImage                = env.GetString("VSA_MEDIATOR_IMAGE_NAME", "")
	vsaFilesImageName            = env.GetString("VSA_FILES_IMAGE_NAME", "")
	filesMediatorImage           = env.GetString("VSA_FILES_MEDIATOR_IMAGE_NAME", "")
	vsaExperimentalImageName     = env.GetString("VSA_IMAGE_EXPERIMENTAL", "")
	experimentalMediatorImage    = env.GetString("VSA_MEDIATOR_IMAGE_EXPERIMENTAL", "")
	waitTimeForGCPOperationInSec = env.GetInt("WAIT_TIME_FOR_GCP_OPERATION_IN_SEC", 10)
	parallelNumberOfNodesForITC  = env.GetInt("PARALLEL_NUMBER_OF_NODES_FOR_ITC", 4) // As of now it's 4 as per the VLM design document

	enableVpgEndpoints                = env.GetBool("ENABLE_VPG_ENDPOINTS", false)
	disableVsaCleanupOnVLMFailure     = env.GetBool("DISABLE_VSA_CLEANUP_ON_VLM_FAILURE", false)
	enableAutoVolOfflineCronForGCPKMS = env.GetBool("ENABLE_AUTO_VOL_OFFLINE_CRON_FOR_GCP_KMS", true)
	ginLoggingFeatureFlag             = env.GetBool("GIN_LOGGING_FEATURE", false)
	enableSyncPoolZIZS                = env.GetBool("ENABLE_SYNC_POOL_ZIZS", false)
	enableLdap                        = env.GetBool("ENABLE_LDAP", false)
	createNasLifDuringPoolCreation    = env.GetBool("CREATE_NAS_LIF_DURING_POOL_CREATION", false)
	poolSubnetSupervisorGracePeriod   = env.GetDuration("POOL_SUBNET_SUPERVISOR_GRACE_PERIOD", 30*time.Minute)
	zoneSwitchMetricActivityTimeout   = env.GetDuration("ZONE_SWITCH_METRIC_ACTIVITY_START_TO_CLOSE_TIMEOUT", 5)

	// PO (Performance Offering) pool config. Read as package vars so tests can
	// override them directly (matching the existing feature-flag test pattern).
	cvnDefaultSubnetMask = env.GetInt(env.EnvCVNDefaultSubnetMaskSize, common.PODefaultSubnetMaskSize)
	// poLifHomePort optionally pins the data port each PO data LIF is homed on.
	// When empty, the home port is derived as "<poLifBasePort>-<vlan>" from the
	// per-node VLAN port created at pool-create time.
	poLifHomePort = env.GetString(env.EnvPOLifHomePort, "")
	// poLifBasePort is the physical/LAG base port that per-node PO VLAN ports are
	// created on (e.g. "a0a"), matching SDE's ONTAP_REST_BASE_NETPORT default.
	poLifBasePort             = env.GetString(env.EnvPOLifBasePort, "a0a")
	poDatapathActivityTimeout = env.GetDuration(env.EnvPODatapathActivityTimeout, 30*time.Minute)
)

const (
	DefaultSvmName    = "gcnv"
	SaIdPrefix        = "vsa-sa-"
	statusDone        = "DONE"
	operationProgress = int64(100)
	CancelSignalName  = "cancel-pool-creation"
	ZoneSwitch        = "switch"
	ZoneRevert        = "revert"

	zoneSwitchMetricStatusSuccess   = "success"
	zoneSwitchMetricStatusFailure   = "failure"
	zoneSwitchMetricFailureStepNone = "none"

	zoneSwitchFailureStepUpdateAttributes = "update_zone_switch_attributes"
	zoneSwitchFailureStepParseVLMConfig   = "parse_vlm_config"
	zoneSwitchFailureStepGetCredentials   = "get_ontap_credentials"
	zoneSwitchFailureStepZoneSwitch       = "zone_switch"
	zoneSwitchFailureStepNilResponse      = "zone_switch_nil_response"
	zoneSwitchFailureStepPersistConfig    = "persist_pool_config"
	poolVLMFeatureLabelKey                = "vlm-feature"
	enableTrueZonalLVEnv                  = "ENABLE_TRUE_ZONAL_LV"
	defaultTrueZonalFeature               = "trueZonal"
)

const (
	// vmrsConfigVersionV1 labels the legacy c3-only VMRS catalog (vmrs_gcp.yaml).
	vmrsConfigVersionV1 = "vmrs_gcp.yaml"
	// vmrsConfigVersionV2 labels the c3 + c4 VMRS catalog (vmrs_gcp_v2.yaml).
	vmrsConfigVersionV2 = "vmrs_gcp_v2.yaml"
	// ontapVersionWithC4Support is the minimum ONTAP major.minor that supports c4.
	ontapVersionWithC4Support = "9.19.1"
)

// vmrsCatalogVersionLabel returns the catalog filename persisted to
// BuildInfo.VMRSVersion (the basename of configPath).
func vmrsCatalogVersionLabel(configPath string) string {
	return filepath.Base(configPath)
}

// ontapVersionForVMRS returns the ONTAP version used to select the VMRS catalog
// for an existing pool. Pool update uses the pool's deployed version
// (BuildInfo.OntapVersion), not the region's current ONTAP_VERSION_DETAILS.
func ontapVersionForVMRS(pool *datamodel.Pool) string {
	if pool != nil && pool.BuildInfo != nil && pool.BuildInfo.OntapVersion != "" {
		return pool.BuildInfo.OntapVersion
	}
	return env.CurrentOntapVersionDetails
}

// resolveVMRSConfigPath returns the VMRS config file path and its catalog
// filename label when the deployed ONTAP major.minor is >=
// ontapVersionWithC4Support; otherwise the legacy c3-only catalog.
func resolveVMRSConfigPath(ontapVersion string) (path string, version string) {
	parseMajorMinor := func(v string) (float64, bool) {
		parts := strings.Split(strings.TrimSpace(v), ".")
		if len(parts) < 2 {
			return 0, false
		}
		majorMinorVersion, err := strconv.ParseFloat(parts[0]+"."+parts[1], 64)
		if err != nil {
			return 0, false
		}
		return majorMinorVersion, true
	}
	currentVersion, currentOK := parseMajorMinor(ontapVersion)
	supportedVersion, supportedOK := parseMajorMinor(ontapVersionWithC4Support)
	if currentOK && supportedOK && currentVersion >= supportedVersion {
		return vmrsConfigPathV2, vmrsCatalogVersionLabel(vmrsConfigPathV2)
	}
	return vmrsConfigPath, vmrsCatalogVersionLabel(vmrsConfigPath)
}

type createPoolWorkflow struct {
	BaseWorkflow
	SE *database.Storage
}

type poolDataSubnetWorkFlow struct {
	BaseWorkflow
	SE             *database.Storage
	TenancyDetails *common.TenancyInfo
}

var _ WorkflowInterface = &poolDataSubnetWorkFlow{}

// CreatePoolWorkflow processes pool related requests from a customer.
func CreatePoolWorkflow(ctx workflow.Context, params *common.CreatePoolParams, pool *datamodel.Pool) error {
	createPoolWF := new(createPoolWorkflow)
	log := util.GetLogger(ctx)
	err := createPoolWF.Setup(ctx, params)
	if err != nil {
		return err
	}
	if err = createPoolWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return err
	}

	createPoolWF.Status = WorkflowStatusRunning
	err = createPoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("failed to update job status to PROCESSING: %v", err)
		return err
	}
	_, errRun := createPoolWF.Run(ctx, params, pool)
	if errRun != nil {
		log.Errorf("error in createPoolWorkflow: %v", errRun)
		createPoolWF.Status = WorkflowStatusFailed
		err2 := createPoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), errRun)
		if err2 != nil {
			log.Errorf("failed to update job with err and status to ERROR: %v", err2)
			return err2
		}
		return errRun
	}
	createPoolWF.Status = WorkflowStatusCompleted
	err = createPoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("failed to update job status to DONE: %v", err)
		return err
	}
	return nil
}

// runPOPoolCreate provisions a PO (Performance Offering) pool against a
// pre-existing ONTAP cluster networked via CVN, instead of deploying VSA VMs via
// VLM. All VLM-only steps (VMRS, cluster deployment, DNS, PSC, serial number,
// auto-tiering, RBAC/expert-mode, AD/LDAP, manual QoS, Regional HA) are skipped;
// invalid PO requests are rejected rather than routed to VLM.
func (wf *createPoolWorkflow) runPOPoolCreate(
	ctx workflow.Context,
	params *common.CreatePoolParams,
	dbPool *datamodel.Pool,
	rollbackManager *common.RollbackManager,
	cancellationHandler *common.WorkflowCancellationHandler,
	dbHbCtx workflow.Context,
) *vsaerrors.CustomError {
	poActivity := &activities.POOntapActivity{}
	cvnActivity := &activities.CVNActivity{}
	ipRangeActivity := &activities.IPRangeActivity{}
	poDataLifActivity := &activities.PODataLifActivity{}
	svmActivity := &activities.SvmActivity{}
	poolActivity := &activities.PoolActivity{}

	// PO pools must reference a registered ONTAP host (persisted on the pool row at create time).
	if hostErr := common.ValidatePOPoolHostUUID(dbPool); hostErr != nil {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, hostErr)))
	}
	poHostUUID := dbPool.HostUUID

	// KMS reachability up front (parity with VLM path), if requested.
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	if kmsErr := verifyKmsConfigReachability(ctx, params.KmsConfigId); kmsErr != nil {
		return ConvertToVSAError(kmsErr)
	}

	// PO pools require an account UUID for CVN client authentication.
	if dbPool.Account == nil || dbPool.Account.UUID == "" {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("pool account uuid must not be empty"))))
	}
	accountUUID := dbPool.Account.UUID

	// Enumerate cluster nodes before datapath create so we can pre-select an ip_range
	// with sufficient capacity (CVS preSelectIPRange parity).
	var nodeNames []string
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	if nErr := workflow.ExecuteActivity(ctx, poActivity.GetPONodeNames, activities.GetPONodeNamesInput{HostUUID: poHostUUID}).Get(ctx, &nodeNames); nErr != nil {
		return ConvertToVSAError(nErr)
	}

	// Create the CVN datapath (single zone — no Regional HA).
	datapathParams := common.BuildPODataPathParams(
		accountUUID,
		params.VendorSubNetID,
		poHostUUID,
		params.ServiceLevel,
		cvnDefaultSubnetMask,
		dbPool.UUID,
		dbPool.ID,
	)
	lifCount := common.PORequiredDataLifCount(len(nodeNames))
	var selectedIPRangeUUID string
	preSelectInput := activities.PreSelectDataLifIPRangeInput{
		AccountUUID:    accountUUID,
		VendorSubnetID: params.VendorSubNetID,
		HostUUID:       poHostUUID,
		SubnetMaskSize: int64(datapathParams.SubnetMaskSize),
		IPAddressCount: lifCount,
	}
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	if preErr := workflow.ExecuteActivity(ctx, ipRangeActivity.PreSelectDataLifIPRange, preSelectInput).Get(ctx, &selectedIPRangeUUID); preErr != nil {
		return ConvertToVSAError(preErr)
	}
	datapathParams.IpRangeUUID = selectedIPRangeUUID
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	var datapath common.DataPathResponse
	dpAO := workflow.GetActivityOptions(ctx)
	dpAO.StartToCloseTimeout = poDatapathActivityTimeout
	// CreateDataPath heartbeats only once and then blocks in the CVN SDK for the
	// full datapath build (can take ~5m+). The default heartbeat timeout (5m) is
	// shorter than this build, so it can spuriously time out and retry the
	// activity, producing a non-idempotent second pass (VL upgrade + VX 409).
	// Match the heartbeat timeout to the StartToClose timeout so the activity is
	// not retried mid-build.
	dpAO.HeartbeatTimeout = poDatapathActivityTimeout
	dpCtx := workflow.WithActivityOptions(ctx, dpAO)
	wlog := util.GetLogger(ctx)
	wlog.Infof("PO Data Path Params: %v", datapathParams)
	if dpErr := workflow.ExecuteActivity(dpCtx, cvnActivity.CreateDataPath, datapathParams).Get(dpCtx, &datapath); dpErr != nil {
		return ConvertToVSAError(dpErr)
	}

	// Reflect PO identity in the in-memory pool. ServiceLevel is already persisted
	// at insert; cluster name lives in ClusterDetails.ExternalName. Datapath
	// outputs stay in workflow memory and CVN/ip_ranges, not on the pool row.
	if dbPool.PoolAttributes == nil {
		dbPool.PoolAttributes = &datamodel.PoolAttributes{}
	}
	dbPool.PoolAttributes.ServiceLevel = params.ServiceLevel
	dbPool.ClusterDetails.ExternalName = params.ClusterName

	// Resolve the customer's tenancy (tenant) project and SN host, then persist them.
	// PO pools do not create any tenant-project resources; these are lookups only.
	// Both are hard requirements — fail the workflow if either cannot be resolved.
	var tenantProjectNumber string
	if tpErr := workflow.ExecuteActivity(ctx, poolActivity.FindTenancyProject, *params).
		Get(ctx, &tenantProjectNumber); tpErr != nil {
		return ConvertToVSAError(tpErr)
	}
	if strings.TrimSpace(tenantProjectNumber) == "" {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrPSAPeeringNotFoundError,
				fmt.Errorf("tenant project not found for PO pool %s", dbPool.UUID))))
	}

	var snHostProject string
	if snErr := workflow.ExecuteActivity(ctx, poolActivity.GetSnHostProject, tenantProjectNumber).
		Get(ctx, &snHostProject); snErr != nil {
		return ConvertToVSAError(snErr)
	}
	if strings.TrimSpace(snHostProject) == "" {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrPSAPeeringNotFoundError,
				fmt.Errorf("SN host project not found for tenant project %s (PO pool %s)", tenantProjectNumber, dbPool.UUID))))
	}

	// Update only the fields resolved in this step so any existing
	// ClusterDetails values (ExternalName set at insert, and any fields added
	// before this save in the future) are preserved rather than overwritten by
	// a fresh struct (SavePoolWithVsaDetails replaces the whole JSON).
	dbPool.ClusterDetails.ExternalName = params.ClusterName
	dbPool.ClusterDetails.RegionalTenantProject = tenantProjectNumber
	dbPool.ClusterDetails.SnHostProject = snHostProject
	dbPool.SnHostProject = snHostProject
	if saveErr := workflow.ExecuteActivity(dbHbCtx, poolActivity.SavePoolWithClusterDetails, dbPool, &dbPool.ClusterDetails).
		Get(dbHbCtx, nil); saveErr != nil {
		return ConvertToVSAError(saveErr)
	}

	// Ensure the per-pool ipspace, broadcast domain, and VLAN ports exist before
	// creating the SVM so it is provisioned in the same ipspace as the data LIFs.
	ipSpace := common.POIPSpaceName(accountUUID, datapath.VlanID)
	broadcastDomain := common.POBroadcastDomainName(accountUUID, datapath.VlanID)
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	ipSpaceInput := activities.EnsurePOIPSpaceInput{HostUUID: poHostUUID, Name: ipSpace}
	if iErr := workflow.ExecuteActivity(ctx, poActivity.EnsurePOIPSpace, ipSpaceInput).Get(ctx, nil); iErr != nil {
		return ConvertToVSAError(iErr)
	}
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	bdInput := activities.EnsurePOBroadcastDomainInput{HostUUID: poHostUUID, IPSpace: ipSpace, Name: broadcastDomain}
	if bErr := workflow.ExecuteActivity(ctx, poActivity.EnsurePOBroadcastDomain, bdInput).Get(ctx, nil); bErr != nil {
		return ConvertToVSAError(bErr)
	}

	// Create one VLAN port per node (bound to the broadcast domain) so each data
	// LIF can be homed on the per-node VLAN interface, matching SDE's per-node
	// VLAN model. The LIF home port is "<basePort>-<vlan>" unless explicitly
	// pinned via PO_LIF_HOME_PORT.
	vlanTag, vlanErr := strconv.Atoi(datapath.VlanID)
	if vlanErr != nil {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("invalid CVN VLAN id %q: %w", datapath.VlanID, vlanErr))))
	}
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	vlanInput := activities.EnsurePOVlanPortsInput{
		HostUUID:        poHostUUID,
		IPSpace:         ipSpace,
		BroadcastDomain: broadcastDomain,
		BasePort:        poLifBasePort,
		VlanTag:         vlanTag,
		NodeNames:       nodeNames,
	}
	if vErr := workflow.ExecuteActivity(ctx, poActivity.EnsurePOVlanPorts, vlanInput).Get(ctx, nil); vErr != nil {
		return ConvertToVSAError(vErr)
	}
	lifHomePort := poLifHomePort
	if lifHomePort == "" {
		lifHomePort = fmt.Sprintf("%s-%d", poLifBasePort, vlanTag)
	}

	// Allocate an SVM name (SDE convention "svm_<accountUUID>_<hex>") and create
	// the SVM in the pool ipspace; register the SVM delete rollback.
	var svmName string
	if nErr := workflow.ExecuteActivity(dbHbCtx, svmActivity.AllocatePOSVMName, dbPool).Get(dbHbCtx, &svmName); nErr != nil {
		return ConvertToVSAError(nErr)
	}
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	var createSvmOut activities.CreatePOSvmOutput
	createSvmInput := activities.CreatePOSvmInput{
		HostUUID:  poHostUUID,
		SvmName:   svmName,
		EnableNfs: true,
		IPSpace:   ipSpace,
	}
	if sErr := workflow.ExecuteActivity(ctx, poActivity.CreatePOSvm, createSvmInput).Get(ctx, &createSvmOut); sErr != nil {
		return ConvertToVSAError(sErr)
	}
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	mgmtInput := activities.EnsurePOSvmMgmtPoliciesInput{HostUUID: poHostUUID, SvmUUID: createSvmOut.SvmUUID}
	if mErr := workflow.ExecuteActivity(ctx, poActivity.EnsurePOSvmMgmtPolicies, mgmtInput).Get(ctx, nil); mErr != nil {
		return ConvertToVSAError(mErr)
	}
	rollbackManager.AddActivity(poActivity.DeletePOSvm, activities.DeletePOSvmInput{HostUUID: poHostUUID, SvmName: svmName})

	var poSvm *datamodel.Svm
	saveSvmInput := activities.SavePOSVMRecordInput{
		PoolID:    dbPool.ID,
		AccountID: dbPool.AccountID,
		SvmName:   svmName,
		SvmUUID:   createSvmOut.SvmUUID,
		IPSpace:   ipSpace,
	}
	if sErr := workflow.ExecuteActivity(dbHbCtx, svmActivity.SavePOSVMRecord, saveSvmInput).Get(dbHbCtx, &poSvm); sErr != nil {
		return ConvertToVSAError(sErr)
	}
	rollbackManager.AddActivity(svmActivity.SoftDeleteSvm, poSvm)

	// Generate the eager multi-LIF set: one NFS LIF per node, each with a
	// distinct real address reserved in the lifs table.
	lifPostfixes, pfErr := generatePODataLifPostfixes(ctx, lifCount)
	if pfErr != nil {
		return ConvertToVSAError(pfErr)
	}
	if datapath.IpRangeUUID == "" {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("CVN datapath returned empty ip range uuid for pool %s", dbPool.UUID))))
	}
	if poSvm == nil || poSvm.ID <= 0 {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("PO SVM record must have a positive id before allocating data LIF IPs"))))
	}
	var nodeIDs map[string]int64
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	if rErr := workflow.ExecuteActivity(ctx, poDataLifActivity.ResolvePONodeIDs, activities.ResolvePONodeIDsInput{
		HostUUID:  poHostUUID,
		NodeNames: nodeNames,
	}).Get(ctx, &nodeIDs); rErr != nil {
		return ConvertToVSAError(rErr)
	}
	reservations := make([]activities.PODataLifReservationInput, 0, lifCount)
	for i, node := range nodeNames {
		nodeID, ok := nodeIDs[node]
		if !ok {
			return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("missing node id for ONTAP node %q", node))))
		}
		reservations = append(reservations, activities.PODataLifReservationInput{
			Name:   common.PODataLifName(svmName, lifPostfixes[i]),
			NodeID: nodeID,
		})
	}
	var allocatedLifs []*datamodel.Lif
	allocInput := activities.AllocatePODataLifIPsInput{
		IPRangeUUID:  datapath.IpRangeUUID,
		AccountID:    dbPool.AccountID,
		SvmID:        poSvm.ID,
		Reservations: reservations,
	}
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	if aErr := workflow.ExecuteActivity(ctx, poDataLifActivity.AllocatePODataLifIPs, allocInput).Get(ctx, &allocatedLifs); aErr != nil {
		return ConvertToVSAError(aErr)
	}
	if len(allocatedLifs) != lifCount {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("allocated %d data LIF IPs, expected %d", len(allocatedLifs), lifCount))))
	}
	rollbackManager.AddActivity(poDataLifActivity.ReleaseDataLifReservations, activities.ReleaseDataLifReservationsInput{SvmID: poSvm.ID})

	lifs := make([]activities.PODataLifSpec, 0, lifCount)
	for i, node := range nodeNames {
		lifs = append(lifs, activities.PODataLifSpec{
			Name:          allocatedLifs[i].Name,
			IPAddress:     allocatedLifs[i].IPAddress,
			Netmask:       allocatedLifs[i].SubnetMask,
			HomeNode:      node,
			HomePort:      lifHomePort,
			ServicePolicy: activities.POServicePolicyNfs,
		})
	}

	// Create the data LIFs from the CVN datapath outputs.
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	lifInput := activities.CreatePODataLifsInput{
		HostUUID: poHostUUID,
		SvmName:  svmName,
		Lifs:     lifs,
	}
	if lErr := workflow.ExecuteActivity(ctx, poActivity.CreatePODataLifs, lifInput).Get(ctx, nil); lErr != nil {
		return ConvertToVSAError(lErr)
	}

	// Create the SVM's default (gateway) route from the CVN gateway.
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	routeInput := activities.CreatePODefaultRouteInput{HostUUID: poHostUUID, SvmName: svmName, Gateway: datapath.Gateway, IPSpace: ipSpace}
	if rErr := workflow.ExecuteActivity(ctx, poActivity.CreatePODefaultRoute, routeInput).Get(ctx, nil); rErr != nil {
		return ConvertToVSAError(rErr)
	}

	// Apply the per-tier QoS offering when enabled via ENABLE_PO_SVM_QOS.
	if common.POSvmQoSEnabled() {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return cancelErr
		}
		qosInput := activities.ApplyQoSPolicyInput{
			HostUUID:     poHostUUID,
			SvmName:      svmName,
			SvmUUID:      createSvmOut.SvmUUID,
			ServiceLevel: params.ServiceLevel,
			SizeInBytes:  params.SizeInBytes,
		}
		if qErr := workflow.ExecuteActivity(ctx, poActivity.ApplyQoSPolicy, qosInput).Get(ctx, nil); qErr != nil {
			return ConvertToVSAError(qErr)
		}
	}

	// Configure KMS on the SVM if requested. Resolve the full key fields so the
	// KMS config is applied correctly (not just the config id).
	if params.KmsConfigId != "" {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return cancelErr
		}
		kmsInput := activities.ConfigureKmsOnSvmInput{
			HostUUID:    poHostUUID,
			SvmName:     svmName,
			KmsConfigId: params.KmsConfigId,
		}
		if params.KmsConfig != nil {
			kmsInput.KeyName = params.KmsConfig.KeyName
			kmsInput.KeyRingName = params.KmsConfig.KeyRing
			kmsInput.KeyRingLocation = params.KmsConfig.KeyRingLocation
			kmsInput.ProjectID = params.KmsConfig.KeyProjectID
			kmsInput.PrivilegedAccount = params.KmsConfig.KmsAttributes.GetServiceAccountEmail()
		}
		if kErr := workflow.ExecuteActivity(ctx, poActivity.ConfigureKmsOnSvm, kmsInput).Get(ctx, nil); kErr != nil {
			return ConvertToVSAError(kErr)
		}
	}

	// Mark the pool READY.
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return cancelErr
	}
	if cErr := workflow.ExecuteActivity(dbHbCtx, poolActivity.CreatedPool, dbPool, (*vlm.VLMConfig)(nil)).Get(dbHbCtx, nil); cErr != nil {
		return ConvertToVSAError(cErr)
	}

	return nil
}

type poDataLifPostfixResult struct {
	Postfixes []string
	Err       string
}

// generatePODataLifPostfixes returns unique 8-char hex postfixes for PO data LIF
// names. Randomness is captured via workflow.SideEffect for replay safety.
func generatePODataLifPostfixes(ctx workflow.Context, count int) ([]string, error) {
	var encoded poDataLifPostfixResult
	if err := workflow.SideEffect(ctx, func(_ workflow.Context) interface{} {
		postfixes, genErr := common.GeneratePODataLifPostfixes(count)
		if genErr != nil {
			return poDataLifPostfixResult{Err: genErr.Error()}
		}
		return poDataLifPostfixResult{Postfixes: postfixes}
	}).Get(&encoded); err != nil {
		return nil, err
	}
	if encoded.Err != "" {
		return nil, errors.New(encoded.Err)
	}
	return encoded.Postfixes, nil
}

func (wf *createPoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createPoolParams := input.(*common.CreatePoolParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createPoolParams.AccountName
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *createPoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.CreatePoolParams)
	pool := args[1].(*datamodel.Pool)
	poolActivity := &activities.PoolActivity{}
	svmActivity := &activities.SvmActivity{}
	retryPolicy, err := PopulateRetryPolicyParams(params.LargeCapacity)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
			// add panic error as non-retriable types
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	dbHbCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)
	dbPool := pool

	rollbackManager := common.NewRollbackManager()

	// Set up cancellation handler using common framework
	cancellationHandler := common.NewWorkflowCancellationHandler(ctx, CancelSignalName, dbPool.UUID, "pool")

	defer func() {
		common.ExecuteDeferredCleanup(ctx, cancellationHandler, rollbackManager, err, wf.Logger, "pool", dbPool.UUID, nil, nil, nil)
	}()

	rollbackManager.AddActivity(poolActivity.ErroredPool, dbPool)
	rollbackManager.AddActivity(poolActivity.DeletePoolResourcesOnRollback, dbPool)

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	// PO (Performance Offering) pools take a dedicated branch and must never fall
	// through to the VLM path. Invalid PO requests are rejected (Section 5.1).
	if common.IsPORequest(params) {
		if poErr := wf.runPOPoolCreate(ctx, params, dbPool, rollbackManager, cancellationHandler, dbHbCtx); poErr != nil {
			err = poErr
			return nil, poErr
		}
		return nil, nil
	}

	// Verify if KMS config is reachable before proceeding with pool creation, if present
	err = verifyKmsConfigReachability(ctx, params.KmsConfigId)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if activities.ValidateImageDigestFlag {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		var verified bool
		err = workflow.ExecuteActivity(ctx, poolActivity.ValidateImageDigest).Get(ctx, &verified)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		if !verified {
			return nil, ConvertToVSAError(vsaerrors.New("image digest verification failed"))
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	tenantProjectNumber := new(string)
	err = workflow.ExecuteActivity(ctx, poolActivity.FindTenancyProject, params).Get(ctx, tenantProjectNumber)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	tenancyDetails := &common.TenancyInfo{}
	rollbackManager.AddWorkflow(workflowengine.CustomerTaskQueue, DataSubnetSequentialPoller, params, pool, tenantProjectNumber, models.ResourceOperationDelete)
	requestedRangesPresent := len(params.RequestedRanges) > 0
	wf.Logger.Infof("DataSubnetSequentialPoller: starting subnet allocation, requestedRangesPresent=%v requestedRanges=%v network=%s", requestedRangesPresent, params.RequestedRanges, params.VendorSubNetID)
	// Using REQUEST_CANCEL policy so child workflow is cancelled when parent is cancelled
	dataSubnetCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
	})
	err = workflow.ExecuteChildWorkflow(dataSubnetCtx, DataSubnetSequentialPoller, params, pool, tenantProjectNumber, models.ResourceOperationCreate).Get(ctx, &tenancyDetails)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Mark the address range as IN_USE now that GCP has allocated the subnet from it.
	// Use CIDR containment: find the registered range whose CIDR contains the allocated
	// subnet's IP (e.g. 10.55.55.16/29 is carved from 10.55.55.0/24). This correctly
	// Only runs when ADDRESS_SPACE_MGMT_ENABLED=true and a subnet was allocated.
	if len(params.RequestedRanges) > 0 && tenancyDetails != nil && tenancyDetails.AllocatedSubnetCIDR != "" {
		if markErr := workflow.ExecuteActivity(ctx, poolActivity.MarkAddressRangeInUse, tenancyDetails.AllocatedSubnetCIDR, params.VendorSubNetID).Get(ctx, nil); markErr != nil {
			util.GetLogger(ctx).Warnf("MarkAddressRangeInUse failed, continuing pool creation: %v", markErr)
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	// persist cluster details (tenancy details - as it's required for cleaning up the resources in case of failure)
	tenancyInfo := &datamodel.ClusterDetails{
		RegionalTenantProject: tenancyDetails.RegionalTenantProject,
		SnHostProject:         tenancyDetails.SnHostProject,
		Network:               tenancyDetails.Network,
		SubnetNames:           tenancyDetails.SubnetworkNames,
		AllocatedSubnetCIDR:   tenancyDetails.AllocatedSubnetCIDR,
	}
	dbPool.SnHostProject = tenancyDetails.SnHostProject
	dbPool.ClusterDetails = *tenancyInfo

	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.SavePoolWithClusterDetails, dbPool, tenancyInfo).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	setupNwCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(setupNwHeartbeatTimeout)*time.Second)
	// Using REQUEST_CANCEL policy so child workflow is cancelled when parent is cancelled
	setupNwCtx = workflow.WithChildOptions(setupNwCtx, workflow.ChildWorkflowOptions{
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
	})
	err = workflow.ExecuteChildWorkflow(setupNwCtx, ConfigureNetworkWorkflow, tenancyDetails, params.Mode).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	serviceAccount := &iam.ServiceAccount{}
	serviceAccountID := fmt.Sprintf("%s%s", SaIdPrefix, pool.DeploymentName)
	dbPool.ServiceAccountId = serviceAccountID

	rollbackManager.AddActivity(poolActivity.DeleteServiceAccount, tenancyDetails.RegionalTenantProject, serviceAccountID)

	// Get the service account specific retry policy
	saRetryPolicy, err := populateServiceAccountRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Create custom activity options for service account creation
	saActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: saRetryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        saRetryPolicy.InitialInterval,
			BackoffCoefficient:     saRetryPolicy.BackoffCoefficient,
			MaximumInterval:        saRetryPolicy.MaximumInterval,
			MaximumAttempts:        int32(saRetryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}

	// Create new context with custom activity options while preserving existing context
	saCtx := workflow.WithActivityOptions(ctx, saActivityOptions)

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// Use the new context for the service account creation activity
	err = workflow.ExecuteActivity(saCtx, poolActivity.CreateServiceAccountWithStorageRole, tenancyDetails.RegionalTenantProject, serviceAccountID, pool.Name).Get(ctx, serviceAccount)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	AutoTierBucketName := fmt.Sprintf("%s-%s", params.Region, dbPool.UUID)

	// Update AutoTieringConfig with bucket name
	if dbPool.AutoTieringConfig == nil {
		dbPool.AutoTieringConfig = &datamodel.AutoTieringConfig{}
	}
	dbPool.AutoTieringConfig.BucketName = AutoTierBucketName

	rollbackManager.AddActivity(poolActivity.DeleteAutoTierBucket, AutoTierBucketName, dbPool.Account.Name, dbPool.ID)
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, poolActivity.CreateAutoTierBucket, AutoTierBucketName, params.Region, tenancyDetails.RegionalTenantProject).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	credConfig := &vlm.OntapCredentials{}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, poolActivity.CreateOnTapCredentials, pool).Get(ctx, &credConfig)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Note: Password is now fetched from Secret Manager when needed during certificate rotation
	// No need to store it in pool_credentials as it's already stored in Secret Manager

	if !disableVsaCleanupOnVLMFailure {
		rollbackManager.AddActivity(poolActivity.DeleteOnTapCredentials, pool)
	}

	sizeInGB := utils.BytesToGigabytes(params.HotTierSizeInBytes)
	// Convert CustomPerformanceParams to CustomerRequestedPerformance.
	customerRequestedPerformance := &vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             *params.CustomPerformanceParams.Iops,
		DesiredThroughputInMiBs: params.CustomPerformanceParams.ThroughputMibps,
		DesiredCapacityInGiB:    int64(sizeInGB),
		ConfigForPoolInstanceScaling: &vmrs.PoolInstanceScalingConfig{
			CurrentVolCount:        int64(0), // new pool, so current vol count is 0
			VolLimitPerInstanceMap: nil,
			CurrentInstanceType:    "",
		},
	}

	vsaClientWorkflowManager := GetNewVSAClientWorkflowManager()

	// Find the optimal VMs based on the customer requested performance.
	vlmConfig := &vlm.VLMConfig{}

	bucketName := ""
	if pool.AutoTieringConfig != nil {
		bucketName = pool.AutoTieringConfig.BucketName
	}
	// For expert mode pools without auto-tiering, pass empty bucket name to VLM
	// to skip bucket attachment. The bucket is still created but not attached.
	if params.Mode == common.ONTAPMode && !params.AllowAutoTiering {
		bucketName = ""
	}

	// Calculate VLM worker queue once to use for both creation and rollback. The orchestrator derives
	// the feature name from the pool create body; VLM_WORKER_FEATURE_FLAG supplies queue/image overrides.
	// Falls back to account-allowlist based selection when no configured feature applies.
	log := util.GetLogger(ctx)
	vlmWorkerQueue, vlmFeature, vlmFeatureCfg := ResolveVLMWorkerQueueForPool(log, dbPool)
	if vlmFeature != "" {
		log.Infof("Selected VLM worker feature %q (version %s) for pool %s", vlmFeature, vlmFeatureCfg.VlmWorkerVersion, pool.Name)
	}

	if vlmWorkerQueue == "" {
		return nil, ConvertToVSAError(
			vsaerrors.NewVCPError(vsaerrors.ErrWorkflowTaskQueueEmpty, fmt.Errorf("VLM worker queue cannot be empty")))
	}

	if !disableVsaCleanupOnVLMFailure {
		deleteVSAClusterDeploymentRequest := &vlm.DeleteVSAClusterDeploymentRequest{}
		prepareDeleteVSAClusterDeployment(deleteVSAClusterDeploymentRequest, dbPool.DeploymentName, vlm.VLMCloudProvider, tenancyDetails.RegionalTenantProject)
		rollbackManager.AddWorkflow(vlmWorkerQueue, vlm.DeleteVSAClusterDeploymentWorkflowName, deleteVSAClusterDeploymentRequest)
	}

	locationInfo := &common.LocationInfo{
		PrimaryZone:   params.PrimaryZone,
		SecondaryZone: params.SecondaryZone,
		Region:        params.Region,
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	// Use resolved zones to identify VMs and build VLM config
	isOntapMode := params.Mode == common.ONTAPMode
	scalingTopology := vmrs_decision.ResolveScalingTopology(pool.IsActiveActive())
	customerRequestedPerformance.ScalingTopology = scalingTopology
	// Pick the VMRS catalog based on the deployed ONTAP version. The version label
	// is stored on the pool's BuildInfo below to record which catalog sized the VMs.
	createVMRSConfigPath, createVMRSConfigVersion := resolveVMRSConfigPath(env.CurrentOntapVersionDetails)
	identifyVMsRequest := activities.IdentifyVMsRequest{
		VMRSConfigPath:      createVMRSConfigPath,
		CustomerRequest:     *customerRequestedPerformance,
		DeploymentName:      dbPool.DeploymentName,
		LocationInfo:        locationInfo,
		TenancyInfo:         tenancyDetails,
		ServiceAccountEmail: serviceAccount.Email,
		AutoTierBucket:      bucketName,
		IsLargeCapacityPool: pool.LargeCapacity,
		IsOntapMode:         isOntapMode,
		AccountID:           params.AccountName,
	}
	err = workflow.ExecuteActivity(ctx, poolActivity.IdentifyVMs, identifyVMsRequest).Get(ctx, vlmConfig)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	var resolvedLocationInfo *common.LocationInfo
	if scalingTopology.Topology == vmrs.TopologyActiveActive {
		resolvedLocationInfo = locationInfo
		err = workflow.ExecuteActivity(ctx, poolActivity.ValidateZonesForMachineTypes,
			tenancyDetails.RegionalTenantProject, resolvedLocationInfo.PrimaryZone, resolvedLocationInfo.SecondaryZone, vlmConfig.Deployment.VSAInstanceType).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	} else {
		err = workflow.ExecuteActivity(ctx, poolActivity.IdentifySecondaryAndMediatorZone, tenancyDetails.RegionalTenantProject, locationInfo, vlmConfig.Deployment.VSAInstanceType, params.IsRegionalHA).Get(ctx, &resolvedLocationInfo)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}
	dbPool.PoolAttributes.SecondaryZone = resolvedLocationInfo.SecondaryZone
	dbPool.PoolAttributes.MediatorZone = resolvedLocationInfo.MediatorZone

	hostMap := make(map[string]string)

	createVSAClusterDeploymentRequest := &vlm.CreateVSAClusterDeploymentRequest{}
	prepareCreateVSAClusterDeploymentRequest(createVSAClusterDeploymentRequest, *vlmConfig, *credConfig, dbPool, resolvedLocationInfo, vlmFeatureCfg)

	// Allocate unique serial numbers in production
	// This is disabled by default (enableUniqueSerialNumberGeneration=false)
	// Serial number will only be allocated if the project is not a prober project.
	if enableUniqueSerialNumberGeneration && !isProberProject(params.AccountName) {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, poolActivity.AllocateClusterSerialNumber, createVSAClusterDeploymentRequest, params.AccountName).Get(ctx, createVSAClusterDeploymentRequest)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	// Use the pre-calculated queue to ensure consistency between creation and rollback
	createVSAClusterDeploymentResponse, err := vsaClientWorkflowManager.CreateVSAClusterDeployment(ctx, createVSAClusterDeploymentRequest, vlmWorkerQueue)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if vlmConfig.VsaCluster.ClusterName != "" {
		createVSAClusterDeploymentResponse.VLMConfig.VsaCluster.ClusterName = vlmConfig.VsaCluster.ClusterName
	}
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	err = workflow.ExecuteActivity(ctx, poolActivity.CreateCloudDNSRecords, createVSAClusterDeploymentResponse.VLMConfig, dbPool.DeploymentName, dbPool.PoolCredentials.AuthType).Get(ctx, &hostMap)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(poolActivity.DeleteCloudDNSRecords, hostMap, pool.PoolCredentials.AuthType)

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.SaveVSANodeDetails, dbPool, createVSAClusterDeploymentResponse.VLMConfig, pool.DeploymentName, &hostMap).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(dbHbCtx, activities.CommonActivities.GetNode, pool.ID).Get(dbHbCtx, &dbNodes)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	var ontapVersion string
	err = workflow.ExecuteActivity(ctx, poolActivity.GetOntapVersion, node).Get(ctx, &ontapVersion)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// Get intercluster LIF IPs from VLM config
	var interclusterLifIPs []string
	err = workflow.ExecuteActivity(ctx, poolActivity.GetInterClusterLifsFromVLMConfig, createVSAClusterDeploymentResponse.VLMConfig).Get(ctx, &interclusterLifIPs)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Add PSC Endpoint
	if ginLoggingFeatureFlag {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		rollbackManager.AddWorkflow(workflowengine.CustomerTaskQueue, ReleasePSCEndpointWorkflow, dbPool)
		setupPSCEndpoint := workflow.WithHeartbeatTimeout(ctx, time.Duration(setupNwHeartbeatTimeout)*time.Second)
		// Using REQUEST_CANCEL policy so child workflow is cancelled when parent is cancelled
		setupPSCEndpoint = workflow.WithChildOptions(setupPSCEndpoint, workflow.ChildWorkflowOptions{
			ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		})
		err = workflow.ExecuteChildWorkflow(setupPSCEndpoint, ConfigurePSCEndpointWorkflow, tenancyDetails.RegionalTenantProject, params.Region, node).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Create pool build info with current image details. The images come from the request (which carries
	// any feature-flag / allowlist overrides), and the resolved VLM worker feature/version is persisted
	// so pool delete routes to the same VLM worker.
	poolBuildInfo := &datamodel.PoolBuildInfo{
		VSABuildImage:      createVSAClusterDeploymentRequest.VLMConfig.Deployment.Images.VSAImageName,
		MediatorBuildImage: createVSAClusterDeploymentRequest.VLMConfig.Deployment.Images.MediatorImageName,
		OntapVersion:       utils.GetOntapVersionBasedOnAllowlisting(pool.Account.Name),
		BuildTimestamp:     time.Now(),
		VMRSVersion:        createVMRSConfigVersion,
		VLMWorkerFeature:   vlmFeature,
		VLMWorkerVersion:   vlmFeatureCfg.VlmWorkerVersion,
	}
	dbPool.BuildInfo = poolBuildInfo

	clusterDetails := &datamodel.ClusterDetails{
		ExternalName:          createVSAClusterDeploymentResponse.VLMConfig.VsaCluster.ClusterName,
		OntapVersion:          ontapVersion,
		InterclusterLifIPs:    interclusterLifIPs,
		RegionalTenantProject: tenancyDetails.RegionalTenantProject,
		SnHostProject:         tenancyDetails.SnHostProject,
		Network:               tenancyDetails.Network,
		SubnetNames:           tenancyDetails.SubnetworkNames,
		AllocatedSubnetCIDR:   dbPool.ClusterDetails.AllocatedSubnetCIDR,
	}
	dbPool.SnHostProject = tenancyDetails.SnHostProject
	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.SavePoolWithClusterDetails, dbPool, clusterDetails).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	defer func() {
		// Once the cluster is deployed, IPs are reserved from the subnet. Using this defer block we will fetch and
		// update the SubnetToIPsReserved details for pools including failed pools.
		var subnetToIPsReserved *[]datamodel.SubnetToIPs
		err1 := workflow.ExecuteActivity(ctx, poolActivity.GetIPsConsumedForSubnet, dbPool, tenancyDetails, params.Region).Get(ctx, &subnetToIPsReserved)
		if err1 != nil {
			wf.Logger.Errorf("Failed to get IPs consumed by deployment in the alloted subnet, error: %v", err1)
		}

		if subnetToIPsReserved != nil {
			clusterDetails.ReservedIPsInSubnet = subnetToIPsReserved
			err1 = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatePoolFields, dbPool.UUID, map[string]interface{}{
				"cluster_details": clusterDetails,
			}).Get(dbHbCtx, nil)
			if err1 != nil {
				wf.Logger.Errorf("Failed to save IPs consumed by deployment in the alloted subnet in DB, error: %v", err1)
			}
		} else {
			wf.Logger.Debugf("No subnet to IPs reserved found for pool %s", dbPool.Name)
		}
	}()

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	svmName := ""
	err = workflow.ExecuteActivity(ctx, svmActivity.AllocateSVMName, dbPool).Get(ctx, &svmName)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	createSVMRequest := &vlm.CreateSVMRequest{}
	prepareCreateSVMRequest(createSVMRequest, svmName, createVSAClusterDeploymentResponse.VLMConfig, *credConfig)
	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	createSVMCtx := vlm.WithCreateSVMWorkerQueueVersion(ctx, dbPool.BuildInfo.VLMWorkerVersion)
	createSVMResponse, err := vsaClientWorkflowManager.CreateVSASVM(createSVMCtx, createSVMRequest)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	svm := &datamodel.Svm{}
	err = workflow.ExecuteActivity(dbHbCtx, svmActivity.SaveSVMAndLifData, dbPool, createSVMResponse.VLMConfig, svmName).Get(dbHbCtx, svm)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// Create QoS policy and apply it to the SVM for auto QoS pools, and always for ONTAP-mode
	// pools (vserver-wide {svm}-qos-policy is the pool ceiling even when qosType is manual).
	if pool.QosType == utils.QosTypeAuto || params.Mode == common.ONTAPMode {
		err = workflow.ExecuteActivity(ctx, svmActivity.CreateQoSPolicyAndApplyToSVM, dbPool, svm, node).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	expertCredConfig := &vlm.OntapCredentials{}
	if params.Mode == common.ONTAPMode {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		rbacFileDetails := &hyperscalermodels.BucketFileDetails{}

		err = workflow.ExecuteActivity(ctx, poolActivity.GetRbacHash, dbPool.BuildInfo.OntapVersion).Get(ctx, &rbacFileDetails)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, poolActivity.ValidateRbacHash, dbPool.BuildInfo.OntapVersion, rbacFileDetails).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if len(pool.ExpertModeCredentials.ExpertModeCredential) == 0 || pool.ExpertModeCredentials.ExpertModeCredential[0].Username == "" {
			return nil, ConvertToVSAError(vsaerrors.New("expert mode username not found in request"))
		}
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, poolActivity.CreateExpertModeCredentials, pool, pool.DeploymentName, pool.ExpertModeCredentials.ExpertModeCredential[0].Username).Get(ctx, &expertCredConfig)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		if !disableVsaCleanupOnVLMFailure {
			rollbackManager.AddActivity(poolActivity.DeleteExpertModeCredentials, pool)
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		createVSAExpertModeReq := &vlm.OntapExpertModeUserConfig{}
		err = workflow.ExecuteActivity(ctx, poolActivity.PrepareCreateVSAExpertModeReq, createVSAClusterDeploymentResponse.VLMConfig, *credConfig, *expertCredConfig, dbPool, rbacFileDetails).Get(ctx, &createVSAExpertModeReq)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		ontapExpertModeUserResponse, err := vsaClientWorkflowManager.CreateVSAExpertModeUser(ctx, createVSAExpertModeReq)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		rbacFileDetails.FileHashSHA256 = ontapExpertModeUserResponse.RbacFileChecksum
		err = workflow.ExecuteActivity(ctx, poolActivity.UpdateRbacCheckSumInPool, dbPool, rbacFileDetails).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// Enable KMS for SVM if KMS config is provided
	err = configureKmsConfigForSvmActivity(ctx, *dbPool, node, svm, params)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// Sync Active Directory from CVP to VCP if AD exists in CVP but not in VCP
	if params.ActiveDirectoryId != "" && !params.ADExistsInVCP {
		adSyncParams := adSyncInput{
			ActiveDirectoryID: params.ActiveDirectoryId,
			AccountName:       params.AccountName,
			Region:            params.Region,
			XCorrelationID:    params.XCorrelationID,
			ActiveDirectory:   params.ActiveDirectory,
			LargeCapacity:     params.LargeCapacity,
		}

		err = syncActiveDirectoryInVcp(ctx, adSyncParams, dbPool)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}
	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.CreatedPool, dbPool, &createSVMResponse.VLMConfig).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Set wafl.maxvolclonehier option on the ONTAP cluster
	if thinCloneGASupport {
		setMaxVolCloneErr := workflow.ExecuteActivity(ctx, poolActivity.SetWaflMaxVolCloneHier, node, dbPool).Get(ctx, nil)
		if setMaxVolCloneErr != nil {
			wf.Logger.Errorf("Failed to set wafl.maxvolclonehier (non-critical, continuing): %v", setMaxVolCloneErr)
		}
	}

	syncPoolZIZSDetailsWorkflow(ctx, dbPool, wf)

	// Enable billing metrics related workflow(NodeToHarvestFarmWorkflow), when enableMetrics is true
	if enableMetrics {
		registerNodeToHarvestFarmWorkflowInput := RegisterNodeToHarvestFarmWorkflowInput{
			PoolID:            dbPool.ID,
			MaxNodesPerGroup:  maxNodesPerGroup,
			CustomerProjectID: params.AccountName,
			TenantProjectID:   *tenantProjectNumber,
			PoolUUID:          dbPool.UUID,
			AccountID:         dbPool.AccountID,
			DeploymentName:    dbPool.DeploymentName,
			PoolName:          dbPool.Name,
			IsRegionalHA:      dbPool.PoolAttributes != nil && dbPool.PoolAttributes.IsRegionalHA,
		}

		if childWfErr := _startRegisterNodeToHarvestFarmChild(ctx, dbPool, registerNodeToHarvestFarmWorkflowInput); childWfErr != nil {
			wf.Logger.Warnf("Failed to on-board poolId %d to harvest-farm due to error: %v", dbPool.ID, childWfErr)
		}
	}
	return nil, nil
}

// _startRegisterNodeToHarvestFarmChild starts the register-node-to-harvest-farm child workflow with a deterministic WorkflowID
// (register-node-to-harvest-farm-{poolUUID}-{accountID}) so that workflow replay does not fail. Non-deterministic IDs
// would cause replay non-determinism errors.
func _startRegisterNodeToHarvestFarmChild(ctx workflow.Context, dbPool *datamodel.Pool, input RegisterNodeToHarvestFarmWorkflowInput) error {
	childWorkflowID := fmt.Sprintf("register-node-to-harvest-farm-%s-%d", dbPool.UUID, dbPool.AccountID)
	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: childWorkflowID,
		TaskQueue:  workflowengine.CustomerTaskQueue,
	})
	return workflow.ExecuteChildWorkflow(ctx, RegisterNodeToHarvestFarmWorkflow, input).Get(ctx, nil)
}

func _syncPoolZIZSDetailsWorkflow(ctx workflow.Context, dbPool *datamodel.Pool, wf *createPoolWorkflow) {
	// Execute SyncPoolZIZSDetailsWorkflow asynchronously
	// If sync fails, log warning but don't fail the main pool creation workflow
	if enableSyncPoolZIZS {
		// Start SyncPoolZIZSDetailsWorkflow as child workflow after successful pool creation
		poolIdentifier := &database.PoolIdentifier{
			UUID:      dbPool.UUID,
			Name:      dbPool.Name,
			AccountID: dbPool.AccountID,
			VendorID:  dbPool.VendorID,
		}

		// Create child workflow context for SyncPoolZIZSDetailsWorkflow
		syncPoolZIZSCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID:            fmt.Sprintf("sync-pool-zizs-%s-%d", dbPool.UUID, dbPool.AccountID),
			TaskQueue:             workflowengine.BackgroundTaskQueue,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_ABANDON, // Continue running even if parent closes
		})

		// Add extra logger fields for better traceability
		syncPoolZIZSCtx = util.AddExtraLoggerFields(syncPoolZIZSCtx, map[string]interface{}{
			"parentWorkflowID": workflow.GetInfo(ctx).WorkflowExecution.ID,
			"poolUUID":         dbPool.UUID,
			"poolName":         dbPool.Name,
		})

		if syncErr := workflow.ExecuteChildWorkflow(syncPoolZIZSCtx, SyncPoolComplianceForPoolWorkflow, poolIdentifier).Get(syncPoolZIZSCtx, nil); syncErr != nil {
			wf.Logger.Warnf("Failed to sync pool ZI/ZS compliance for pool %s (UUID: %s) due to error: %v", dbPool.Name, dbPool.UUID, syncErr)
		}
	} else {
		wf.Logger.Infof("SyncPoolZIZS workflow is disabled via ENABLE_SYNC_POOL_ZIZS environment variable for pool %s (UUID: %s)", dbPool.Name, dbPool.UUID)
	}
}

// runPOPoolUpdate persists PO pool metadata and size in VCP only (no VLM, CVN, QoS, or ONTAP policy changes).
func (wf *updatePoolWorkflow) runPOPoolUpdate(
	ctx workflow.Context,
	dbHbCtx workflow.Context,
	updatePoolParams *common.UpdatePoolParams,
	dbPool *datamodel.Pool,
	pool *datamodel.Pool,
	rollbackManager *common.RollbackManager,
) (interface{}, *vsaerrors.CustomError) {
	poolActivity := &activities.PoolActivity{}

	if hostErr := common.ValidatePOPoolHostUUID(dbPool); hostErr != nil {
		return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, hostErr)))
	}

	dbPool.Description = updatePoolParams.Description
	if updatePoolParams.Labels != nil {
		if dbPool.PoolAttributes == nil {
			dbPool.PoolAttributes = &datamodel.PoolAttributes{}
		}
		dbPool.PoolAttributes.Labels = updatePoolParams.Labels
	}
	if updatePoolParams.SizeInBytes > 0 {
		dbPool.SizeInBytes = int64(updatePoolParams.SizeInBytes)
	}

	rollbackManager.AddActivity(poolActivity.UpdatedPool, pool)
	if err := workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatedPool, dbPool).Get(dbHbCtx, nil); err != nil {
		return nil, ConvertToVSAError(err)
	}
	return nil, nil
}

type updatePoolWorkflow struct {
	BaseWorkflow
	SE *database.Storage
}

// Enforcing the WorkflowInterface on createPoolWorkflow
var _ WorkflowInterface = &updatePoolWorkflow{}

// const customerActionTimeout = 30 * time.Minute

// UpdatePoolWorkflow processes pool related requests from a customer.
func UpdatePoolWorkflow(ctx workflow.Context, params *common.UpdatePoolParams, pool *datamodel.Pool, autoscaleConfig *common.AutoPoolScalingParams) (gcpgenserver.V1betaDescribePoolRes, error) {
	updatePoolWF := new(updatePoolWorkflow)
	err := updatePoolWF.Setup(ctx, params)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err = updatePoolWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}

	// On panic, mark job ERROR so the operation reports done with error instead of staying "in progress"
	defer func() {
		if r := recover(); r != nil {
			updatePoolWF.Status = WorkflowStatusFailed
			_ = updatePoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), fmt.Errorf("workflow panic: %v", r))
			panic(r)
		}
	}()

	updatePoolWF.Status = WorkflowStatusRunning
	err = updatePoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	_, err = updatePoolWF.Run(ctx, params, pool, autoscaleConfig)
	if e, ok := err.(*vsaerrors.CustomError); ok && e != nil {
		updatePoolWF.Status = WorkflowStatusFailed
		err = updatePoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		return nil, ConvertToVSAError(err)
	}
	updatePoolWF.Status = WorkflowStatusCompleted
	err = updatePoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	return nil, nil
}

func (wf *updatePoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	updatePoolParams := input.(*common.UpdatePoolParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = updatePoolParams.AccountName
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *updatePoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	updatePoolParams := args[0].(*common.UpdatePoolParams)
	pool := args[1].(*datamodel.Pool)
	autoScalingParams := args[2].(*common.AutoPoolScalingParams)
	poolActivity := &activities.PoolActivity{}
	svmActivity := &activities.SvmActivity{}
	retryPolicy, err := PopulateRetryPolicyParams(pool.LargeCapacity)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	dbHbCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)
	rollbackManager := common.NewRollbackManager()

	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	dbPool, err := deepCopyPoolFn(pool)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if common.IsPOPool(dbPool) {
		return wf.runPOPoolUpdate(ctx, dbHbCtx, updatePoolParams, dbPool, pool, rollbackManager)
	}

	// In case of any errors, rollback to the old values.
	rollbackManager.AddActivity(poolActivity.UpdatedPool, pool)

	wf.Logger.Info("Updating pool with new parameters", "params", updatePoolParams) // Update the pool with the new parameters

	// Determine the size to provision in VLM (hot tier size for AutoTiering, full size otherwise)
	toProvisionPoolSizeInBytes := updatePoolParams.SizeInBytes
	if updatePoolParams.AllowAutoTiering {
		toProvisionPoolSizeInBytes = updatePoolParams.HotTierSizeInBytes
	}

	// Get current provisioned size - what was actually provisioned in VLM
	var currentProvisionedSize int64
	if !dbPool.AllowAutoTiering {
		currentProvisionedSize = dbPool.SizeInBytes
	} else {
		currentProvisionedSize = dbPool.AutoTieringConfig.HotTierSizeInBytes
	}

	// Check if expert mode pool is enabling auto-tiering (needs VLM call for bucket attachment)
	needsBucketAttachment := pool.APIAccessMode == common.ONTAPMode && updatePoolParams.AllowAutoTiering && !pool.AllowAutoTiering

	if updatePoolParams.ActiveDirectoryConfigId != "" && !pool.ActiveDirectoryID.Valid && !updatePoolParams.IfADExistsInVCP {
		adLargeCapacity := false
		if updatePoolParams.LargeCapacity != nil {
			adLargeCapacity = *updatePoolParams.LargeCapacity
		}
		adSyncParams := adSyncInput{
			ActiveDirectoryID: updatePoolParams.ActiveDirectoryConfigId,
			AccountName:       updatePoolParams.AccountName,
			Region:            updatePoolParams.Region,
			XCorrelationID:    updatePoolParams.XCorrelationID,
			ActiveDirectory:   updatePoolParams.ActiveDirectory,
			LargeCapacity:     adLargeCapacity,
		}
		if adSyncParams.ActiveDirectory == nil {
			return nil, ConvertToVSAError(vsaerrors.New("ActiveDirectory is nil, cannot sync"))
		}
		if err = syncActiveDirectoryInVcp(ctx, adSyncParams, dbPool); err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Check if Zone Switch is needed.
	// True zonal stores SecondaryZone == PrimaryZone; without IsRegionalHA that would match every update.
	needsZoneSwitch := updatePoolParams.CurrentZone != "" && dbPool.PoolAttributes != nil &&
		dbPool.PoolAttributes.IsRegionalHA &&
		updatePoolParams.CurrentZone == dbPool.PoolAttributes.SecondaryZone

	if needsZoneSwitch {
		zoneSwitchRequest := &vlm.ZoneSwitchRequest{}
		vsaClientWorkflowManager := GetNewVSAClientWorkflowManager()
		var zoneSwitchAction string
		if !dbPool.PoolAttributes.IsZoneSwitched {
			zoneSwitchAction = ZoneSwitch
		} else {
			zoneSwitchAction = ZoneRevert
		}

		emitZoneSwitchMetric := func(status, failureStep string) {
			metricAO := workflow.ActivityOptions{
				StartToCloseTimeout: time.Duration(zoneSwitchMetricActivityTimeout) * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					MaximumAttempts: 1,
				},
			}
			metricCtx := workflow.WithActivityOptions(ctx, metricAO)
			_ = workflow.ExecuteActivity(metricCtx, backgroundactivities.EmitZoneSwitchWorkflowMetric,
				dbPool.UUID, dbPool.Name, zoneSwitchAction, status, failureStep).Get(metricCtx, nil)
		}

		rollbackZoneSwitchPool := func() {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			if rbErr := workflow.ExecuteActivity(disconnectedCtx, poolActivity.UpdatedPool, pool).Get(disconnectedCtx, nil); rbErr != nil {
				wf.Logger.Errorf("Failed to rollback pool after zone switch failure: %v", rbErr)
			}
		}

		failZoneSwitch := func(failureStep string, failureErr error) *vsaerrors.CustomError {
			emitZoneSwitchMetric(zoneSwitchMetricStatusFailure, failureStep)
			rollbackZoneSwitchPool()
			err = nil // rollback handled explicitly; avoid duplicate UpdatedPool in defer
			return ConvertToVSAError(failureErr)
		}

		err = workflow.ExecuteActivity(ctx, poolActivity.UpdateZoneSwitchPoolAttributes, dbPool, datamodel.ZoneSwitching).Get(ctx, nil)
		if err != nil {
			return nil, failZoneSwitch(zoneSwitchFailureStepUpdateAttributes, err)
		}

		// Retrieve the last known VLM config to be used for zone switching workflow.
		vlmConfig := &vlm.VLMConfig{}
		err = workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, pool).Get(ctx, &vlmConfig)
		if err != nil {
			return nil, failZoneSwitch(zoneSwitchFailureStepParseVLMConfig, err)
		}

		credentials := &vlm.OntapCredentials{}
		err = workflow.ExecuteActivity(ctx, poolActivity.GetOnTapCredentials, pool).Get(ctx, &credentials)
		if err != nil {
			return nil, failZoneSwitch(zoneSwitchFailureStepGetCredentials, err)
		}

		// Use post-batch deployment config (cluster already updated above).
		prepareZoneSwitchRequest(zoneSwitchRequest, *vlmConfig, *credentials, zoneSwitchAction)

		zoneSwitchResponse, err := vsaClientWorkflowManager.ZoneSwitch(ctx, zoneSwitchRequest)
		if err != nil {
			return nil, failZoneSwitch(zoneSwitchFailureStepZoneSwitch, err)
		}
		if zoneSwitchResponse == nil {
			wf.Logger.Infof("zone switch returned nil response")
			return nil, failZoneSwitch(zoneSwitchFailureStepNilResponse, fmt.Errorf("zone switch returned nil response"))
		}
		wf.Logger.Info("Zone switch completed ", "response", zoneSwitchResponse)

		currentPrimaryZone := dbPool.PoolAttributes.PrimaryZone
		dbPool.PoolAttributes.PrimaryZone = updatePoolParams.CurrentZone
		dbPool.PoolAttributes.SecondaryZone = currentPrimaryZone
		dbPool.PoolAttributes.IsZoneSwitched = !dbPool.PoolAttributes.IsZoneSwitched
		if zoneSwitchAction == ZoneSwitch {
			dbPool.PoolAttributes.ZoneSwitchState = datamodel.ZoneSwitched
		}
		if zoneSwitchAction == ZoneRevert {
			dbPool.PoolAttributes.ZoneSwitchState = datamodel.ZonePrimary
		}

		// Persist swapped zones (and same deployment snapshot as pre-switch persist; matches prior behavior).
		err = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatedPoolWithVLMConfig, dbPool, zoneSwitchResponse.VLMConfig, updatePoolParams).Get(dbHbCtx, nil)
		if err != nil {
			return nil, failZoneSwitch(zoneSwitchFailureStepPersistConfig, err)
		}
		emitZoneSwitchMetric(zoneSwitchMetricStatusSuccess, zoneSwitchMetricFailureStepNone)
		return nil, nil
	}

	// Do not short-circuit when qosType is explicitly changing (auto↔manual); transition branch handles it.
	// Use a single robust check: requested type is valid (auto|manual) and differs from current.
	qosTypeTransition := (updatePoolParams.QosType == utils.QosTypeAuto || updatePoolParams.QosType == utils.QosTypeManual) && updatePoolParams.QosType != dbPool.QosType

	// if there is no need of vlm workflow, just perform update pool in db
	// Note: For expert mode pools enabling auto-tiering, we must call VLM to attach the bucket
	if currentProvisionedSize == int64(toProvisionPoolSizeInBytes) &&
		dbPool.PoolAttributes.ThroughputMibps == int64(updatePoolParams.TotalThroughputMibps) &&
		dbPool.PoolAttributes.Iops == *updatePoolParams.TotalIops && autoScalingParams == nil &&
		!needsBucketAttachment && !qosTypeTransition {
		if dbPool.Description != updatePoolParams.Description {
			dbPool.Description = updatePoolParams.Description
		}
		if updatePoolParams.Labels != nil {
			dbPool.PoolAttributes.Labels = updatePoolParams.Labels
		}

		// Always update SizeInBytes for metadata/billing (even with AutoTiering)
		dbPool.SizeInBytes = int64(updatePoolParams.SizeInBytes)

		// Apply qosType when provided so it is persisted by UpdatedPool (handles case where
		// qosTypeTransition was false due to params serialization but request included qosType)
		if updatePoolParams.QosType != "" {
			dbPool.QosType = updatePoolParams.QosType
		}

		// Register a compensating rollback for the hot-tier patch before applying it, so a
		// later failure restores the original values. UpdatedPool omits auto_tiering_config
		// and cannot revert this patch on its own.
		origHot, origEnable := originalHotTierInfo(pool)
		rollbackManager.AddActivity(poolActivity.UpdateAutoTieringHotTierInfo, pool.UUID, origHot, origEnable)
		if err := persistAutoTieringHotTierInfo(dbHbCtx, poolActivity, dbPool, updatePoolParams); err != nil {
			return nil, ConvertToVSAError(err)
		}

		rollbackManager.AddActivity(poolActivity.UpdatedPool, pool)
		err = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatedPool, dbPool).Get(dbHbCtx, nil)
		return nil, ConvertToVSAError(err)
	}

	// qosType transition: auto → manual
	if updatePoolParams.QosType == utils.QosTypeManual && dbPool.QosType == utils.QosTypeAuto {
		if customErr := runPoolQosTypeTransitionAutoToManual(ctx, dbHbCtx, wf, rollbackManager, poolActivity, svmActivity, dbPool, updatePoolParams, pool); customErr != nil {
			err = customErr // ensure defer runs ExecuteRollback (e.g. restore QPG on vserver)
			return nil, customErr
		}
		return nil, nil
	}

	// qosType transition: manual → auto
	if updatePoolParams.QosType == utils.QosTypeAuto && dbPool.QosType == utils.QosTypeManual {
		if customErr := runPoolQosTypeTransitionManualToAuto(ctx, dbHbCtx, wf, rollbackManager, poolActivity, svmActivity, dbPool, updatePoolParams, pool); customErr != nil {
			err = customErr
			return nil, customErr
		}
		return nil, nil
	}

	bucketName := ""
	if pool.AutoTieringConfig != nil {
		bucketName = pool.AutoTieringConfig.BucketName
	}

	// Set bucket name for VLM update if we need to attach bucket for expert mode pool enabling auto-tiering
	bucketNameForVLMUpdate := ""
	if needsBucketAttachment {
		bucketNameForVLMUpdate = bucketName
		wf.Logger.Info("Expert mode pool enabling auto-tiering - will attach bucket to ONTAP", "bucketName", bucketNameForVLMUpdate)
	}

	saEmail := utils.ConstructServiceAccountEmail(pool.ServiceAccountId, pool.ClusterDetails.RegionalTenantProject)

	// Retrieve the last known VLM config that was shared with us.
	currentVlmConfig := &vlm.VLMConfig{}
	err = workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, pool).Get(ctx, &currentVlmConfig)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Determine VM scaling direction to decide the order of operations
	currentInstanceType := currentVlmConfig.Deployment.VSAInstanceType

	var poolInstanceScalingConfig *vmrs.PoolInstanceScalingConfig = nil
	if autoScalingParams != nil {
		poolInstanceScalingConfig = &vmrs.PoolInstanceScalingConfig{
			CurrentVolCount:        autoScalingParams.CurrentVolumeCount,
			VolLimitPerInstanceMap: autoScalingParams.VolLimitPerInstanceMap,
			CurrentInstanceType:    currentInstanceType,
		}
	}

	// Find the optimal VMs based on the customer requested performance.
	// Use toProvisionPoolSizeInBytes which accounts for AutoTiering (hot tier size vs full size)
	// CurrentInstanceType keeps the pool on the machine family it was created with:
	// VM selection is scoped to the existing instance's family rather than the
	// region's current MACHINE_FAMILY_TYPE (e.g. a c3-lssd pool stays c3-lssd even
	// after the region is re-pointed to c4-lssd).
	customerRequestedPerformance := &vmrs.CustomerRequestedPerformance{
		DesiredIOPS:                  *updatePoolParams.TotalIops,
		DesiredThroughputInMiBs:      updatePoolParams.TotalThroughputMibps,
		DesiredCapacityInGiB:         int64(utils.BytesToGigabytes(toProvisionPoolSizeInBytes)),
		CurrentInstanceType:          currentInstanceType,
		ConfigForPoolInstanceScaling: poolInstanceScalingConfig,
	}

	// Identify secondary and mediator zones first
	locationInfo := &common.LocationInfo{
		PrimaryZone:   pool.PoolAttributes.PrimaryZone,
		SecondaryZone: pool.PoolAttributes.SecondaryZone,
		Region:        updatePoolParams.Region,
		MediatorZone:  pool.PoolAttributes.PrimaryZone, // this will be updated later to use the mediator zone
	}

	newVlmConfig := &vlm.VLMConfig{}
	// Create tenancy info from pool cluster details
	poolTenancyInfo := &common.TenancyInfo{
		RegionalTenantProject: pool.ClusterDetails.RegionalTenantProject,
		Network:               pool.ClusterDetails.Network,
		SubnetworkNames:       pool.ClusterDetails.SubnetNames,
		SnHostProject:         pool.ClusterDetails.SnHostProject,
	}
	isOntapMode := pool.APIAccessMode == common.ONTAPMode
	scalingTopology := vmrs_decision.ResolveScalingTopology(pool.IsActiveActive())
	customerRequestedPerformance.ScalingTopology = scalingTopology
	// Pick the VMRS catalog from the pool's deployed ONTAP version, not the
	// region's current ONTAP_VERSION_DETAILS. VMRSVersion is set at create and
	// cluster upgrade only; it is not modified on pool update.
	updateVMRSConfigPath, _ := resolveVMRSConfigPath(ontapVersionForVMRS(dbPool))
	identifyVMsRequest := activities.IdentifyVMsRequest{
		VMRSConfigPath:      updateVMRSConfigPath,
		CustomerRequest:     *customerRequestedPerformance,
		DeploymentName:      dbPool.DeploymentName,
		LocationInfo:        locationInfo,
		TenancyInfo:         poolTenancyInfo,
		ServiceAccountEmail: saEmail,
		AutoTierBucket:      bucketName,
		IsLargeCapacityPool: pool.LargeCapacity,
		IsOntapMode:         isOntapMode,
		AccountID:           updatePoolParams.AccountName,
	}
	err = workflow.ExecuteActivity(ctx, poolActivity.IdentifyVMs, identifyVMsRequest).Get(ctx, newVlmConfig)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Update the mediator zone in the VLM config
	if scalingTopology.Topology.RequiresMediator() {
		newVlmConfig.Deployment.Zone.MediatorZone = locationInfo.MediatorZone
	} else {
		newVlmConfig.Deployment.Zone.MediatorZone = ""
	}
	newInstanceType := newVlmConfig.Deployment.VSAInstanceType

	// Only validate zones for machine type if the instance type is changing
	if currentInstanceType != newInstanceType {
		wf.Logger.Info("Instance type is changing, validating zone compatibility", "currentType", currentInstanceType, "newType", newInstanceType)
		// Validate that primary and secondary zones support the VSA instance type
		err = workflow.ExecuteActivity(ctx, poolActivity.ValidateZonesForMachineTypes, pool.ClusterDetails.RegionalTenantProject, locationInfo.PrimaryZone, locationInfo.SecondaryZone, newInstanceType).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	} else {
		wf.Logger.Info("Instance type unchanged, skipping zone validation", "instanceType", currentInstanceType)
	}

	var isScalingUp bool
	err = workflow.ExecuteActivity(ctx, poolActivity.DetermineVMScalingDirection, updateVMRSConfigPath, currentInstanceType, newInstanceType).Get(ctx, &isScalingUp)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	credentials := &vlm.OntapCredentials{}
	err = workflow.ExecuteActivity(ctx, poolActivity.GetOnTapCredentials, pool).Get(ctx, &credentials)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Get nodes for the pool to modify QoS policy
	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(dbHbCtx, activities.CommonActivities.GetNode, pool.ID).Get(dbHbCtx, &dbNodes)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	// Execute activities based on scaling direction
	wf.Logger.Info("VM scaling direction determined", "isScalingUp", isScalingUp)

	// Execute QoS modification before deployment update if scaling down
	if !isScalingUp {
		wf.Logger.Info("Scaling down detected - modifying QoS policy first")
		err = workflow.ExecuteActivity(ctx, svmActivity.ModifyQoSPolicyAndApplyToSVM, dbPool, node, updatePoolParams).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		updatedPoolAttributes := &datamodel.PoolAttributes{
			ThroughputMibps: int64(updatePoolParams.TotalThroughputMibps),
			Iops:            *updatePoolParams.TotalIops,
			PrimaryZone:     dbPool.PoolAttributes.PrimaryZone,
			SecondaryZone:   dbPool.PoolAttributes.SecondaryZone,
			MediatorZone:    dbPool.PoolAttributes.MediatorZone,
			Labels:          dbPool.PoolAttributes.Labels,
			IsRegionalHA:    dbPool.PoolAttributes.IsRegionalHA,
			LdapEnabled:     dbPool.PoolAttributes.LdapEnabled,
			AccountName:     getPoolAttributesAccountName(dbPool.PoolAttributes),
			IsTrueZonal:     dbPool.PoolAttributes.IsTrueZonal,
		}
		// Update pool in DB to reflect QoS changes
		err = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatePoolFields, dbPool.UUID, map[string]interface{}{
			"pool_attributes": updatedPoolAttributes,
		}).Get(dbHbCtx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		dbPool.PoolAttributes.ThroughputMibps = int64(updatePoolParams.TotalThroughputMibps)
		dbPool.PoolAttributes.Iops = *updatePoolParams.TotalIops
	}

	vsaClientWorkflowManager := GetNewVSAClientWorkflowManager()
	ontapVersion := ResolveVLMWorkerVersionForDelete(dbPool)

	// Calculate batch plan using activity
	batchPlanInput := &activities.CalculateBatchPlanActivityInput{
		NumHAPairs:                  newVlmConfig.Deployment.NumHAPair,
		ParallelNumberOfNodesForITC: parallelNumberOfNodesForITC,
	}
	var batchPlan *activities.CalculateBatchPlanActivityOutput
	err = workflow.ExecuteActivity(ctx, poolActivity.CalculateBatchPlanForUpdate, batchPlanInput).Get(ctx, &batchPlan)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Execute batch updates using reusable function
	updateVSAClusterDeploymentResponse, err := executePoolBatchUpdates(ctx, batchPlan, currentVlmConfig, newVlmConfig, credentials, ontapVersion, vsaClientWorkflowManager, wf.Logger, bucketNameForVLMUpdate)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Execute QoS modification after deployment update if scaling up
	if isScalingUp {
		wf.Logger.Info("Scaling up detected - modifying QoS policy after deployment update")
		err = workflow.ExecuteActivity(ctx, svmActivity.ModifyQoSPolicyAndApplyToSVM, dbPool, node, updatePoolParams).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Update nodes table with new instance type if it changed
	if currentInstanceType != newInstanceType {
		wf.Logger.Info("Instance type changed - updating nodes table", "from", currentInstanceType, "to", newInstanceType)
		err = workflow.ExecuteActivity(ctx, poolActivity.UpdateNodesInstanceTypeActivity, dbPool.ID, newInstanceType).Get(ctx, nil)
		if err != nil {
			wf.Logger.Errorf("Failed to update nodes instance type in database: %v", err)
			return nil, ConvertToVSAError(err)
		}
	}

	// Only hydrate to CCFE if this update was triggered by auto-tiering hot tier auto-resize.
	if updatePoolParams.AutoResizeTriggeredUpdate {
		err = workflow.ExecuteActivity(ctx, poolActivity.HydrateUpdatedPoolToCCFE, pool).Get(ctx, nil)
		if err != nil {
			wf.Logger.Errorf("Failed to hydrate pool to CCFE as part of auto-tiering hot tier auto-resize, error: %v", err)
			// TODO: Add error handling for hydration failure when auto-tiering feature integration is complete
			// return nil, ConvertToVSAError(err)
		}
	}

	// Update pool with VLM config
	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatedPoolWithVLMConfig, dbPool, updateVSAClusterDeploymentResponse.VLMConfig, updatePoolParams).Get(dbHbCtx, nil)
	return nil, ConvertToVSAError(err)
}

type deletePoolWorkflow struct {
	BaseWorkflow
	SE *database.Storage
}

// Enforcing the WorkflowInterface on deletePoolWorkflow
var _ WorkflowInterface = &deletePoolWorkflow{}

// DeletePoolWorkflow runs delete workflow for a pool.
func DeletePoolWorkflow(ctx workflow.Context, params *common.DeletePoolParams, pool *datamodel.Pool) (gcpgenserver.V1betaDescribePoolRes, error) {
	deletePoolWF := new(deletePoolWorkflow)
	err := deletePoolWF.Setup(ctx, params)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err = deletePoolWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}

	deletePoolWF.Status = WorkflowStatusRunning
	err = deletePoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	_, errRun := deletePoolWF.Run(ctx, params, pool)
	if errRun != nil {
		deletePoolWF.Status = WorkflowStatusFailed
		err = deletePoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), errRun)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		return nil, errRun
	}
	deletePoolWF.Status = WorkflowStatusCompleted
	err = deletePoolWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	return nil, nil
}

// DeletePoolWorkflowInternal runs the core delete pool logic without job management.
// This is used when called as a child workflow where the parent manages job status.
func DeletePoolWorkflowInternal(ctx workflow.Context, params *common.DeletePoolParams, pool *datamodel.Pool) (interface{}, error) {
	deletePoolWF := new(deletePoolWorkflow)

	// Setup without job management
	info := workflow.GetInfo(ctx)
	deletePoolWF.CustomerID = params.AccountName
	deletePoolWF.Status = WorkflowStatusCreated
	deletePoolWF.ID = info.WorkflowExecution.ID
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": deletePoolWF.ID, "customerID": deletePoolWF.CustomerID})
	logger := util.GetLogger(ctx)
	deletePoolWF.Logger = logger

	err := workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         deletePoolWF.ID,
			Status:     deletePoolWF.Status,
			CustomerID: deletePoolWF.CustomerID,
		}, nil
	})
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Run the core logic without job status updates
	deletePoolWF.Status = WorkflowStatusRunning
	_, errRun := deletePoolWF.Run(ctx, params, pool)
	if errRun != nil {
		deletePoolWF.Status = WorkflowStatusFailed
		return nil, errRun
	}
	deletePoolWF.Status = WorkflowStatusCompleted
	return nil, nil
}

func (wf *deletePoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deletePoolParams := input.(*common.DeletePoolParams)
	info := workflow.GetInfo(ctx)
	wf.CustomerID = deletePoolParams.AccountName
	wf.Status = WorkflowStatusCreated
	wf.ID = info.WorkflowExecution.ID
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *deletePoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.DeletePoolParams)
	poolActivity := &activities.PoolActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	dbHbCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)
	hyperscalerCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: utils.GetStartToCloseTimeoutHyperscaler(),
		HeartbeatTimeout:    utils.GetHeartbeatTimeoutForHyperscaler(),
		RetryPolicy:         utils.GetHyperscalerLRORetryPolicy(),
	})

	rollbackManager := common.NewRollbackManager()

	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	dbPool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: params.PoolID},
	}
	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.GetPool, dbPool).Get(dbHbCtx, &dbPool)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Handle cancellation only if pool is in CREATING state
	cancellationActivity := &activities.CancellationActivity{}
	commonActivity := &activities.CommonActivities{}
	ackTimeout, forceTimeout := common.GetCancellationTimeouts("POOL")
	if cancelErr := common.HandleCancellationForCreatingResource(ctx, wf.Logger,
		common.HandleCancellationForCreatingResourceParams{
			ResourceUUID:               dbPool.UUID,
			ResourceState:              dbPool.State,
			CreateJobType:              datamodel.JobTypeCreatePool,
			SignalName:                 CancelSignalName,
			CancellationAckTimeout:     ackTimeout,
			ForceTerminationAckTimeout: forceTimeout,
		},
		poolActivity.GetCreateJobByResourceUUID,
		cancellationActivity,
		commonActivity,
	); cancelErr != nil {
		wf.Logger.Warnf("Error handling cancellation: %v, proceeding with deletion", cancelErr)
	}

	// Add the cleanup / rollback activity using this rollback.AddActivity() method instead of writing multiple defer statements,
	// this rollback manager will be invoked whenever there is an error, and it will start calling clean up activities in LIFO manner ***/
	rollbackManager.AddActivity(poolActivity.FailedPool, dbPool, "Failed to delete pool")

	// PO (Performance Offering) pools take a dedicated teardown path: delete the
	// SVM on the pre-existing cluster and tear down the CVN datapath. No VLM/VSA
	// cluster, PSC, DNS, auto-tier or service-account cleanup applies.
	if common.IsPOPool(dbPool) {
		if poErr := wf.runPOPoolDelete(ctx, dbHbCtx, hyperscalerCtx, dbPool); poErr != nil {
			err = poErr
			return nil, poErr
		}
		return nil, nil
	}

	// For pools in CREATING state, skip cleanup activities that require resources that haven't been created yet
	hasDeploymentName := dbPool.DeploymentName != ""
	hasClusterDetails := dbPool.ClusterDetails.RegionalTenantProject != ""
	hasPoolCredentials := dbPool.PoolCredentials != nil

	// Cascade-delete all VPGs before infrastructure teardown (ONTAP is still alive at this point).
	// Only for manual QoS pools with VPG endpoints enabled — auto QoS pools do not have VPGs.
	if enableVpgEndpoints && hasPoolCredentials && dbPool.QosType == utils.QosTypeManual {
		if vpgErr := workflow.ExecuteActivity(hyperscalerCtx, poolActivity.DeleteAllPoolVPGs, dbPool).Get(ctx, nil); vpgErr != nil {
			wf.Logger.Warnf("Failed to delete VPGs for pool %s: %v, proceeding with pool deletion", dbPool.UUID, vpgErr)
		}
	}

	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.DeletingPoolResources, dbPool).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Only perform DNS cleanup if pool credentials exist (indicates pool was at least partially created)
	if hasPoolCredentials {
		hostMap := make(map[string]string)
		err = workflow.ExecuteActivity(hyperscalerCtx, poolActivity.GetCloudDNSRecords, dbPool.ID, dbPool.PoolCredentials.AuthType).Get(ctx, &hostMap)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(hyperscalerCtx, poolActivity.DeleteCloudDNSRecords, hostMap, dbPool.PoolCredentials.AuthType).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Only perform VSA cluster deployment cleanup if deployment name and cluster details exist
	if hasDeploymentName && hasClusterDetails {
		vsaClientWorkflowManager := GetNewVSAClientWorkflowManager()

		// Resolve the VLM worker version (feature-flag aware) so delete routes to the same VLM worker
		// that created the pool.
		ontapVersion := ResolveVLMWorkerVersionForDelete(dbPool)

		if !disableVsaCleanupOnVLMFailure {
			deleteVSAClusterDeploymentRequest := &vlm.DeleteVSAClusterDeploymentRequest{}
			prepareDeleteVSAClusterDeployment(deleteVSAClusterDeploymentRequest, dbPool.DeploymentName, vlm.VLMCloudProvider, dbPool.ClusterDetails.RegionalTenantProject)
			err = vsaClientWorkflowManager.DeleteVSAClusterDeployment(ctx, deleteVSAClusterDeploymentRequest, ontapVersion)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		} else if dbPool.State != datamodel.LifeCycleStateError {
			deleteVSAClusterDeploymentRequest := &vlm.DeleteVSAClusterDeploymentRequest{}
			prepareDeleteVSAClusterDeployment(deleteVSAClusterDeploymentRequest, dbPool.DeploymentName, vlm.VLMCloudProvider, dbPool.ClusterDetails.RegionalTenantProject)
			err = vsaClientWorkflowManager.DeleteVSAClusterDeployment(ctx, deleteVSAClusterDeploymentRequest, ontapVersion)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}
	}

	bucketName := ""
	if dbPool.AutoTieringConfig != nil {
		bucketName = dbPool.AutoTieringConfig.BucketName
	}

	err = workflow.ExecuteActivity(hyperscalerCtx, poolActivity.DeleteAutoTierBucket, bucketName, dbPool.Account.Name, dbPool.ID).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Only delete service account if cluster details exist
	if hasClusterDetails && dbPool.ServiceAccountId != "" {
		saRetryPolicy, err := populateServiceAccountRetryPolicyParams()
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Create custom activity options for service account deletion
		saActivityOptions := workflow.ActivityOptions{
			StartToCloseTimeout: saRetryPolicy.StartToCloseTimeout,
			HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:        saRetryPolicy.InitialInterval,
				BackoffCoefficient:     saRetryPolicy.BackoffCoefficient,
				MaximumInterval:        saRetryPolicy.MaximumInterval,
				MaximumAttempts:        int32(saRetryPolicy.MaximumAttempts),
				NonRetryableErrorTypes: []string{"PanicError"},
			},
		}

		saCtx := workflow.WithActivityOptions(ctx, saActivityOptions)

		err = workflow.ExecuteActivity(saCtx, poolActivity.DeleteServiceAccount, dbPool.ClusterDetails.RegionalTenantProject, dbPool.ServiceAccountId).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		// Best-effort cleanup of IAM permissions as an orphan child workflow.
		// Runs after SA deletion — the matching logic in GetServiceAccountRoles
		// and RemoveRolesFromServiceAccounts handles "deleted:serviceAccount:"
		// prefixes so cleanup works even after the SA is gone.
		cleanupCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			ParentClosePolicy:        enums.PARENT_CLOSE_POLICY_ABANDON,
			WorkflowExecutionTimeout: workflowengine.GetWorkflowGlobalTimeout(),
		})

		childWorkflowFuture := workflow.ExecuteChildWorkflow(cleanupCtx, CleanupServiceAccountPermissionsWorkflow, dbPool, retryPolicy)

		if err := childWorkflowFuture.GetChildWorkflowExecution().Get(cleanupCtx, &workflow.Execution{}); err != nil {
			wf.Logger.Warnf("Failed to start cleanup IAM permissions workflow for pool %s: %v", dbPool.UUID, err)
		}
	}

	if !disableVsaCleanupOnVLMFailure || dbPool.State != datamodel.LifeCycleStateError {
		if dbPool.APIAccessMode == common.ONTAPMode {
			err = workflow.ExecuteActivity(hyperscalerCtx, poolActivity.DeleteExpertModeCredentials, dbPool).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}
		if hasPoolCredentials {
			err = workflow.ExecuteActivity(hyperscalerCtx, poolActivity.DeleteOnTapCredentials, dbPool).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}
	}

	if ginLoggingFeatureFlag {
		err = workflow.ExecuteChildWorkflow(hyperscalerCtx, ReleasePSCEndpointWorkflow, dbPool).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}

	// Only execute data subnet cleanup if cluster details exist
	if hasClusterDetails {
		deletedSubnetDetails := &common.TenancyInfo{}
		err = workflow.ExecuteChildWorkflow(hyperscalerCtx, DataSubnetSequentialPoller, params, dbPool, dbPool.ClusterDetails.RegionalTenantProject, models.ResourceOperationDelete).Get(ctx, &deletedSubnetDetails)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		if deletedSubnetDetails != nil && deletedSubnetDetails.AllocatedSubnetCIDR != "" {
			dbPool.ClusterDetails.AllocatedSubnetCIDR = deletedSubnetDetails.AllocatedSubnetCIDR
		}

		// Release the address range back to CREATED state now that the subnet has been deleted.
		util.GetLogger(ctx).Infof("MarkAddressRangesCreated: invoking activity for pool deletion, poolUUID=%s network=%s allocatedSubnetCIDR=%s", dbPool.UUID, dbPool.Network, dbPool.ClusterDetails.AllocatedSubnetCIDR)
		if markErr := workflow.ExecuteActivity(ctx, poolActivity.MarkAddressRangesCreated, dbPool).Get(ctx, nil); markErr != nil {
			util.GetLogger(ctx).Warnf("MarkAddressRangesCreated failed, continuing pool deletion: %v", markErr)
		}
	}

	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.DeletePoolResources, dbPool).Get(dbHbCtx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if enableMetrics {
		// Execute Child Work to start poller on harvest farm
		childWorkflowOptions := workflow.ChildWorkflowOptions{}
		childCtx := workflow.WithChildOptions(ctx, childWorkflowOptions)
		unregisterParams := &unRegisterNodeFromHarvestFarmParams{
			PoolID: dbPool.ID,
		}
		// If off-boarding to harvest-farm fails log warning message
		// TODO: Need to emit a metric to alert on delete pool off-boarding to harvest-farm
		childWfError := workflow.ExecuteChildWorkflow(ctx, UnRegisterNodeFromHarvestFarmWorkflow, unregisterParams).Get(childCtx, nil)
		if childWfError != nil {
			wf.Logger.Warnf("Failed to off-board poolId %d to harvest-farm due to error: %v", dbPool.ID, childWfError)
		}
	}

	if dbPool.KmsConfig != nil {
		// Check if the KMS config is reachable and update the kms appropriately i.e. from in-use to created when last pool/svm is deleted
		kmsConfigActivity := &kms_activities.KmsConfigActivity{}
		err = workflow.ExecuteActivity(hyperscalerCtx, kmsConfigActivity.VerifyVsaKmsReachabilityActivity, dbPool.KmsConfig.UUID, false).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	}
	return nil, nil
}

// runPOPoolDelete tears down a PO (Performance Offering) pool: it deletes the
// SVM created on the pre-existing ONTAP cluster, then releases KMS state.
// The ONTAP delete is idempotent so the teardown is safe to retry or run
// against partially-created pools.
func (wf *deletePoolWorkflow) runPOPoolDelete(ctx, dbHbCtx, hyperscalerCtx workflow.Context, dbPool *datamodel.Pool) *vsaerrors.CustomError {
	poActivity := &activities.POOntapActivity{}
	poDataLifActivity := &activities.PODataLifActivity{}
	poolActivity := &activities.PoolActivity{}
	svmActivity := &activities.SvmActivity{}

	if dErr := workflow.ExecuteActivity(dbHbCtx, poolActivity.DeletingPoolResources, dbPool).Get(dbHbCtx, nil); dErr != nil {
		return ConvertToVSAError(dErr)
	}

	// PO pools must reference the ONTAP host they were created on.
	if hostErr := common.ValidatePOPoolHostUUID(dbPool); hostErr != nil {
		return ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, hostErr)))
	}
	poHostUUID := dbPool.HostUUID

	// Delete the SVM on the pre-existing cluster (idempotent), then soft-delete the DB row.
	var poSvm *datamodel.Svm
	if gErr := workflow.ExecuteActivity(dbHbCtx, svmActivity.GetPOSvmForPoolID, dbPool.ID).Get(dbHbCtx, &poSvm); gErr != nil {
		return ConvertToVSAError(gErr)
	}
	if poSvm != nil && poSvm.Name != "" {
		delSvm := activities.DeletePOSvmInput{HostUUID: poHostUUID, SvmName: poSvm.Name}
		if sErr := workflow.ExecuteActivity(ctx, poActivity.DeletePOSvm, delSvm).Get(ctx, nil); sErr != nil {
			return ConvertToVSAError(sErr)
		}
		if poSvm.ID > 0 {
			releaseInput := activities.ReleaseDataLifReservationsInput{SvmID: poSvm.ID}
			if lErr := workflow.ExecuteActivity(dbHbCtx, poDataLifActivity.ReleaseDataLifReservations, releaseInput).Get(dbHbCtx, nil); lErr != nil {
				return ConvertToVSAError(lErr)
			}
		}
		if dErr := workflow.ExecuteActivity(dbHbCtx, svmActivity.SoftDeleteSvm, poSvm).Get(dbHbCtx, nil); dErr != nil {
			return ConvertToVSAError(dErr)
		}
	}

	// Release KMS state (in-use -> created) when applicable.
	if dbPool.KmsConfig != nil {
		kmsConfigActivity := &kms_activities.KmsConfigActivity{}
		if kErr := workflow.ExecuteActivity(hyperscalerCtx, kmsConfigActivity.VerifyVsaKmsReachabilityActivity, dbPool.KmsConfig.UUID, false).Get(ctx, nil); kErr != nil {
			return ConvertToVSAError(kErr)
		}
	}

	// Finalize pool deletion in the database.
	if fErr := workflow.ExecuteActivity(dbHbCtx, poolActivity.DeletePoolResources, dbPool).Get(dbHbCtx, nil); fErr != nil {
		return ConvertToVSAError(fErr)
	}
	return nil
}

func _verifyKmsConfigReachability(ctx workflow.Context, kmsConfigId string) error {
	if kmsConfigId == "" {
		return nil // No KMS config provided, nothing to verify
	}

	kmsConfigActivity := &kms_activities.KmsConfigActivity{}
	kmsConfig := &datamodel.KmsConfig{}

	// Get KMS config from VSA database
	err := workflow.ExecuteActivity(ctx, kmsConfigActivity.GetKmsConfigActivity, kmsConfigId).Get(ctx, kmsConfig)
	if err != nil {
		return err
	}

	// Create the service account key for the KMS configuration, if required
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CreateVSAKmsConfigSAKeyActivity, kmsConfig).Get(ctx, kmsConfig)
	if err != nil {
		return err
	}

	// VCP-created configs: skip GrantRoleActivity — the VCP SA has direct access to the customer's KMS key
	// and does not need impersonation role grants.
	// SDE-created configs: grant the necessary roles to the service account for impersonation.
	if kmsConfig.KmsAttributes == nil || !kmsConfig.KmsAttributes.IsVCPCreated() {
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.GrantRoleActivity, kmsConfig).Get(ctx, nil)
		if err != nil {
			return err
		}
	}

	// Access a crypto key using the KMS config in the VSA database to make sure key is reachable and update the kms config state based on the reachability
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.VerifyVsaKmsReachabilityActivity, kmsConfig.UUID, true).Get(ctx, nil)
	if err != nil {
		return err
	}
	return nil
}

func _configureKmsConfigForSvmActivity(ctx workflow.Context, pool datamodel.Pool, node *models.Node, svm *datamodel.Svm, params *common.CreatePoolParams) error {
	if params.KmsConfigId == "" {
		return nil // No KMS config provided, nothing to configure
	}

	kmsConfigActivity := &kms_activities.KmsConfigActivity{}
	kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}

	// Check if service account is in UPDATING state and wait for it to transition to ENABLED
	logger := workflow.GetLogger(ctx)

	// Get KMS config to check initial state
	err := workflow.ExecuteActivity(ctx, kmsConfigActivity.GetKmsConfigActivity, params.KmsConfigId).Get(ctx, kmsConfig)
	if err != nil {
		return err
	}

	// Creates DNS to reach google KMS from the VSA cluster
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CreateDnsActivity, node).Get(ctx, nil)
	if err != nil {
		return err
	}

	// Enable the ontap scheduler to take the volume offline in case the KMS key is not reachable/disabled.
	if enableAutoVolOfflineCronForGCPKMS {
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.EnableAutoVolOfflineCronForGCPKMSActivity, node).Get(ctx, nil)
		if err != nil {
			return err
		}
	}

	// Acquire the KMS rotation lock at service-account scope (resolved from the KMS config) before
	// configuring KMS for SVM, so this cannot race with a rotation of the same service account's key.
	rotateKmsSAKeyActivity := &backgroundactivities.RotateKmsSAKeyActivity{}
	logger.Info("Acquiring KMS rotation lock for pool KMS config", "kmsConfigUUID", params.KmsConfigId)
	_, releaseLock, err := WithKmsRotationLockForKmsConfig(ctx, rotateKmsSAKeyActivity, params.KmsConfigId)
	if err != nil {
		return err
	}
	defer releaseLock()

	// Configure KMS for SVM if KMS config is provided
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.ConfigureKmsForSvmActivity, svm, node, params).Get(ctx, svm)
	if err != nil {
		return err
	}

	// Release lock so we don't hold it during reachability check; defer handles release only on early return above
	releaseLock()

	// Check if the KMS config is reachable from the VSA cluster
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CheckVsaKmsConfigReachableActivity, svm, node).Get(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

func _getNewVSAClientWorkflowManager() vlm.VlmWorkflowClient {
	return vlm.NewVSAClientWorkflowManager()
}

type subnetWorkflowResult struct {
	WorkflowStatus *WorkflowStatus
	TenancyDetails *common.TenancyInfo
}

// PoolDataSubnetWorkFlow processes get pr create subnet for the pool related requests from a customer.
func PoolDataSubnetWorkFlow(ctx workflow.Context, params *common.CreatePoolParams, poolUUID, tenantProjectNumber string, accountID int64, actionType models.ResourceOperation) (gcpgenserver.V1betaDescribePoolRes, error) {
	CreateOrGetSubnetworkWF := new(poolDataSubnetWorkFlow)
	err := CreateOrGetSubnetworkWF.Setup(ctx, params)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err = CreateOrGetSubnetworkWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	CreateOrGetSubnetworkWF.Status = WorkflowStatusRunning
	err = CreateOrGetSubnetworkWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	_, err = CreateOrGetSubnetworkWF.Run(ctx, params, poolUUID, tenantProjectNumber, accountID, actionType)
	if e, ok := err.(*vsaerrors.CustomError); ok && e != nil {
		CreateOrGetSubnetworkWF.Status = WorkflowStatusFailed
		upErr := CreateOrGetSubnetworkWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if upErr != nil {
			return nil, upErr
		}
		return nil, ConvertToVSAError(err)
	}
	CreateOrGetSubnetworkWF.Status = WorkflowStatusCompleted
	err = CreateOrGetSubnetworkWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	return nil, nil
}

func (wf *poolDataSubnetWorkFlow) Setup(ctx workflow.Context, input interface{}) error {
	createPoolParams := input.(*common.CreatePoolParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createPoolParams.AccountName
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	// Set up the query handler for the workflow status and tenancy details.
	// This will allow the caller to query the workflow status and fetch
	// tenancy details after the workflow is completed.
	return workflow.SetQueryHandler(ctx, StatusQueryName, func() (*subnetWorkflowResult, error) {
		return &subnetWorkflowResult{
			WorkflowStatus: &WorkflowStatus{
				ID:         wf.ID,
				Status:     wf.Status,
				CustomerID: wf.CustomerID,
			},
			TenancyDetails: wf.TenancyDetails,
		}, nil
	})
}

func getStartToCloseTimeoutDataSubnet(actionType models.ResourceOperation) string {
	if actionType == models.ResourceOperationCreate {
		return StartToCloseTimeoutDataSubnetCreate
	}
	return StartToCloseTimeoutDataSubnetDelete
}

func (wf *poolDataSubnetWorkFlow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.CreatePoolParams)
	poolUUID := args[1].(string)
	tenantProjectNumber := args[2].(string)
	accountID := args[3].(int64)
	actionType := args[4].(models.ResourceOperation)

	poolActivity := &activities.PoolActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	// Parse the configurable timeout for GetCreateDataSubnetOp (long-running subnet creation)
	subnetOpsTimeout, err := time.ParseDuration(getStartToCloseTimeoutDataSubnet(actionType))
	if err != nil {
		// Fallback to default 20 minutes if parsing fails
		subnetOpsTimeout = 20 * time.Minute
	}
	defaultActivityTimeout, err := time.ParseDuration(StartToCloseTimeoutDataSubnetActivities)
	if err != nil {
		// Fallback to default 5 minutes if parsing fails
		defaultActivityTimeout = 5 * time.Minute
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: defaultActivityTimeout,
		HeartbeatTimeout:    utils.GetHeartbeatTimeoutForHyperscaler(),
		RetryPolicy:         utils.GetHyperscalerRetryPolicy(),
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	lroRetryPolicy := utils.GetHyperscalerLRORetryPolicy()
	subnetCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: subnetOpsTimeout,
		HeartbeatTimeout:    subnetOpsTimeout / 2,
		RetryPolicy:         lroRetryPolicy,
	})
	switch actionType {
	case models.ResourceOperationCreate:
		subnet := new(hyperscalermodels.Subnet)
		err = workflow.ExecuteActivity(ctx, poolActivity.GetAvailableSubnet, params, tenantProjectNumber).Get(ctx, subnet)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		if subnet.Name == "" {
			var operationName string
			err = workflow.ExecuteActivity(subnetCtx, poolActivity.GetCreateDataSubnetOp, params, tenantProjectNumber).Get(ctx, &operationName)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			if operationName == "" {
				return nil, ConvertToVSAError(fmt.Errorf("failed to create subnet for tenant project: %s, operation name is empty", tenantProjectNumber))
			}
			// add retry only for Google timeout : strings.Contains(err.Error(), "Timeout while confirming service network google components")
			opSubnetInBytes, err := WaitForServiceNetworkOperationStatus(subnetCtx, poolActivity, operationName, retryPolicy.StartToCloseTimeout)
			if err != nil {
				return nil, ConvertToVSAError(fmt.Errorf("failed to create subnet for tenant project while waiting to get operation status: %s: %w", tenantProjectNumber, err))
			}
			err = workflow.ExecuteActivity(ctx, poolActivity.GetSubnetFromOperation, opSubnetInBytes).Get(ctx, &subnet)
			if err != nil {
				return nil, ConvertToVSAError(fmt.Errorf("failed to get subnet from operation for tenant project: %s: %w", tenantProjectNumber, err))
			}
		}
		tenancyDetails := &common.TenancyInfo{}
		err = workflow.ExecuteActivity(ctx, poolActivity.GetTenancyInfo, tenantProjectNumber, subnet).Get(ctx, &tenancyDetails)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, poolActivity.UpdatePoolSubnet, poolUUID, tenancyDetails).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		// Adding the result to the workflow, which will be returned to the caller as Query after workflow completion
		wf.TenancyDetails = tenancyDetails
		return tenancyDetails, nil

	case models.ResourceOperationDelete:
		// check the cases thoroughly when the accountID is empty like in case of delete pool
		dbPool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, AccountID: accountID}
		err = workflow.ExecuteActivity(ctx, poolActivity.GetPool, dbPool).Get(ctx, &dbPool)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		tenancyDetails := &common.TenancyInfo{}
		err = workflow.ExecuteActivity(ctx, poolActivity.GetPoolTenancyInfo, dbPool).Get(ctx, &tenancyDetails)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		deleteSubnetOp := make([]common.Operations, 0)
		err = workflow.ExecuteActivity(subnetCtx, poolActivity.ReleaseDataSubnetOp, dbPool).Get(ctx, &deleteSubnetOp)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		err = WaitForGCPNetworkOperationStatus(subnetCtx, poolActivity, &deleteSubnetOp, retryPolicy.StartToCloseTimeout)
		if err != nil {
			return nil, ConvertToVSAError(vsaerror.Errorf("failed to release data subnet for pool: %s project: %s with error : %w", dbPool.Name, dbPool.Account.Name, err))
		}
		// Expose tenancy details (including pre-deletion AllocatedSubnetCIDR) via the query
		// handler so DataSubnetSequentialPoller can return the CIDR to the delete workflow for
		// address-range state reset.
		wf.TenancyDetails = tenancyDetails
		return tenancyDetails, nil
	default:
		// throw error for invalid action type
		return nil, ConvertToVSAError(fmt.Errorf("invalid action type for pool data subnet workflow. Please send either Create or Delete. Current actionType: %s. poolUUID: %s", actionType, poolUUID))
	}
}

// SubnetActivity is a struct used for subnet related activities.
type SubnetActivity struct {
	SE database.Storage
}

var fetchTemporalClient = _fetchTemporalClient

func _fetchTemporalClient(ctx context.Context) client.Client {
	return activity.GetClient(ctx)
}

// DataSubnetSequentialPoller is a workflow that polls for the completion of subnet creation or deletion jobs. Hence making sure only one subnet operation
// is in progress for a given account and VPC at any given time.
// This is important because concurrent subnet creation or deletion requests for the same VPC causes race conditions and failures.
// This workflow is invoked as a child workflow from Pool creation or deletion workflows.
func DataSubnetSequentialPoller(ctx workflow.Context, params *common.CreatePoolParams, pool *datamodel.Pool, tenantProjectNumber string, actionType models.ResourceOperation) (*common.TenancyInfo, error) {
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: utils.GetStartToCloseTimeoutHyperscaler(),
		HeartbeatTimeout:    utils.GetHeartbeatTimeoutForHyperscaler(),
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}
	subnetActivity := SubnetActivity{}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rollbackManager := common.NewRollbackManager()

	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	logger := util.GetLogger(ctx)
	createDeleteSubnetJobUUID := new(string)
	err = workflow.ExecuteActivity(ctx, subnetActivity.CreateDeleteDataSubnetJob, params, pool, tenantProjectNumber, actionType).Get(ctx, createDeleteSubnetJobUUID)
	if err != nil {
		logger.Errorf("Failed to start %s subnet workflow for account: %s, pool name: %s vpc: %s, error: %v", actionType, params.AccountName, params.Name, params.VendorSubNetID, err)
		return nil, ConvertToVSAError(err)
	}

	// Wait for the subnet creation job to complete using workflow.sleep.
	err = PollOnDBJob(ctx, *createDeleteSubnetJobUUID, retryPolicy.StartToCloseTimeout)
	if err != nil {
		logger.Errorf("Failed to wait for create subnet job %s to complete, error: %s", *createDeleteSubnetJobUUID, err.Error())
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Fetch tenancy details for both create and delete: on create it carries the allocated subnet
	// CIDR needed for MarkAddressRangeInUse; on delete it carries the pre-deletion CIDR needed
	// for MarkAddressRangesCreated to reset the address-range state.
	tenancyDetails := &common.TenancyInfo{}
	err = workflow.ExecuteActivity(ctx, subnetActivity.GetTenancyDetails, createDeleteSubnetJobUUID).Get(ctx, &tenancyDetails)
	if err != nil {
		logger.Errorf("Failed to get tenancy details for job %s, error: %v", *createDeleteSubnetJobUUID, err)
		return nil, ConvertToVSAError(err)
	}
	return tenancyDetails, nil
}

// CreateDeleteDataSubnetJob is an activity that triggers PoolDataSubnetWorkFlow for the pool
// in a serialized way. Since we are using the SequenceWorkflow from the workflows pkg for queueing, we
// have kept the activity implementation here to avoid cyclic imports.
func (sa *SubnetActivity) CreateDeleteDataSubnetJob(ctx context.Context, params *common.CreatePoolParams, pool *datamodel.Pool, tenantProjectNumber string, actionType models.ResourceOperation) (string, error) {
	logger := util.GetLogger(ctx)
	se := sa.SE
	temporalClient := fetchTemporalClient(ctx)
	vpcName := utils.GetVPCNameFromSubnetID(params.VendorSubNetID)

	// Use appropriate job type based on pool capacity
	poolCategory := models.GetPoolCategory(pool.LargeCapacity)
	jobType := models.GetResourceJobType(models.ResourceTypeSubnet, actionType, poolCategory)

	activity.RecordHeartbeat(ctx, "Creating subnet job in database")
	job := &datamodel.Job{
		Type:          string(jobType),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  pool.Name + "-subnet",
		AccountID:     sql.NullInt64{Int64: pool.Account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			SupervisorAttributes: &datamodel.SupervisorAttributes{
				OverrideGracePeriod: poolSubnetSupervisorGracePeriod,
			},
		},
	}
	// Create a job in the database to track the creation of subnet activity
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return "", err
	}
	logger.Infof("correlationID: %s, requestID: %s - Creating subnet job for account: %s & vpc: %s, pool name : %s", job.CorrelationID, job.RequestID, params.AccountName, vpcName, params.Name)

	// controlWorkflowID defines the workflow ID for the control workflow
	// This control workflow will be common per same Account & same VPC level.
	controlWorkflowID := fmt.Sprintf(PoolDataSubnetCreateDelete, pool.Account.ID, vpcName)
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Executing subnet workflow sequentially for account: %s, vpc: %s", params.AccountName, vpcName))
	err = ExecuteWorkflowSequentially(
		temporalClient,
		ctx,
		client.StartWorkflowOptions{
			TaskQueue: workflowengine.CustomerTaskQueue,
			ID:        controlWorkflowID,
		},
		PoolDataSubnetWorkFlow,
		workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowID:            createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		params,
		pool.UUID,
		tenantProjectNumber,
		pool.Account.ID,
		actionType,
	)
	if err != nil {
		logger.Errorf("Failed to start create subnet workflow for account: %s & vpc: %s, job: %s, error: %v", params.AccountName, vpcName, createdJob.UUID, err.Error())
		return "", err
	}

	activity.RecordHeartbeat(ctx, "CreateDeleteDataSubnetJob activity completed successfully")
	return createdJob.WorkflowID, nil
}

// GetTenancyDetails retrieves the tenancy details from the completed subnet workflow.
func (sa *SubnetActivity) GetTenancyDetails(ctx context.Context, workflowID string) (*common.TenancyInfo, error) {
	temporalClient := fetchTemporalClient(ctx)

	var subnetWfRes subnetWorkflowResult
	// Sending runID as empty string will query the latest workflow run execution.
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Querying workflow %s for tenancy details", workflowID))
	encVal, err := temporalClient.QueryWorkflow(ctx, workflowID, "", StatusQueryName)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	activity.RecordHeartbeat(ctx, "Decoding workflow query result")
	err = encVal.Get(&subnetWfRes)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if subnetWfRes.WorkflowStatus == nil {
		return nil, vsaerror.Errorf("subnet create workflow %s status is nil", workflowID)
	}
	if subnetWfRes.WorkflowStatus.Status != WorkflowStatusCompleted {
		return nil, vsaerror.Errorf("subnet create workflow %s is not completed, current status: %s", workflowID, subnetWfRes.WorkflowStatus.Status)
	}

	if subnetWfRes.TenancyDetails == nil {
		return nil, vsaerror.Errorf("subnet create workflow %s returned tenancy details as nil", workflowID)
	}

	activity.RecordHeartbeat(ctx, "GetTenancyDetails activity completed successfully")
	return subnetWfRes.TenancyDetails, nil
}

// resolvePoolVLMFeatureName selects the first orchestrator-derived feature candidate that is configured
// in VLM_WORKER_FEATURE_FLAG. Returns empty when no configured feature applies.
func resolvePoolVLMFeatureName(pool *datamodel.Pool) string {
	for _, feature := range candidatePoolVLMFeatureNames(pool) {
		if _, ok := utils.GetVLMWorkerFeatureConfig(feature); ok {
			return feature
		}
	}
	return ""
}

// candidatePoolVLMFeatureNames returns ordered feature keys derived from the pool create body.
func candidatePoolVLMFeatureNames(pool *datamodel.Pool) []string {
	if pool == nil || pool.PoolAttributes == nil {
		return nil
	}

	candidates := make([]string, 0, 2)

	if pool.PoolAttributes.Labels != nil {
		if feature := strings.TrimSpace(utils.ConvertJSONBToMap(pool.PoolAttributes.Labels)[poolVLMFeatureLabelKey]); feature != "" {
			candidates = append(candidates, feature)
		}
	}

	if env.GetBool(enableTrueZonalLVEnv, false) && pool.LargeCapacity && pool.PoolAttributes != nil && pool.PoolAttributes.IsTrueZonal {
		candidates = append(candidates, defaultTrueZonalFeature)
	}

	return candidates
}

// ResolveVLMWorkerQueueForPool selects the VLM worker task queue for a pool. The orchestrator derives
// the feature name from the pool create body and looks up VLM_WORKER_FEATURE_FLAG for queue/image
// overrides. Otherwise it falls back to the legacy account-allowlist based selection.
func ResolveVLMWorkerQueueForPool(logger log.Logger, pool *datamodel.Pool) (queue string, feature string, cfg utils.VLMWorkerFeatureConfig) {
	if feature = resolvePoolVLMFeatureName(pool); feature != "" {
		if c, ok := utils.GetVLMWorkerFeatureConfig(feature); ok && c.VlmWorkerVersion != "" {
			return vlm.GetVLMWorkerQueueForVersion(c.VlmWorkerVersion), feature, c
		}
	}

	account := ""
	if pool != nil && pool.Account != nil {
		account = pool.Account.Name
	}
	return vlm.GetVLMWorkerQueue(logger, account), "", utils.VLMWorkerFeatureConfig{}
}

// ResolveVLMWorkerVersionForDelete returns the VLM worker task queue version used to route a pool
// delete to the same VLM worker that created the pool. It prefers the version persisted in BuildInfo,
// then the persisted feature name or a re-resolved feature against the current feature-flag map, and
// finally falls back to the legacy account-allowlist ONTAP version.
func ResolveVLMWorkerVersionForDelete(pool *datamodel.Pool) string {
	if pool != nil && pool.BuildInfo != nil && pool.BuildInfo.VLMWorkerVersion != "" {
		return pool.BuildInfo.VLMWorkerVersion
	}

	if pool != nil && pool.BuildInfo != nil && pool.BuildInfo.VLMWorkerFeature != "" {
		if cfg, ok := utils.GetVLMWorkerFeatureConfig(pool.BuildInfo.VLMWorkerFeature); ok && cfg.VlmWorkerVersion != "" {
			return cfg.VlmWorkerVersion
		}
	}

	if feature := resolvePoolVLMFeatureName(pool); feature != "" {
		if cfg, ok := utils.GetVLMWorkerFeatureConfig(feature); ok && cfg.VlmWorkerVersion != "" {
			return cfg.VlmWorkerVersion
		}
	}

	account := ""
	if pool != nil && pool.Account != nil {
		account = pool.Account.Name
	}

	version := ""
	if pool != nil && pool.BuildInfo != nil {
		version = ExtractOntapVersion(pool.BuildInfo.OntapVersion)
	}
	if version == "" {
		version = utils.ExtractOntapVersion(utils.GetOntapVersionBasedOnAllowlisting(account))
	}
	return version
}

func prepareCreateVSAClusterDeploymentRequest(createVSAClusterDeploymentRequest *vlm.CreateVSAClusterDeploymentRequest, vlmConfig vlm.VLMConfig, ontapCredentials vlm.OntapCredentials, pool *datamodel.Pool, resolvedLocationInfo *common.LocationInfo, vlmFeatureCfg utils.VLMWorkerFeatureConfig) {
	log := util.GetLogger(context.Background())
	// resolve location assigment
	vlmConfig.Deployment.Zone = vlm.ZoneInfo{
		Zone1: resolvedLocationInfo.PrimaryZone,
		Zone2: resolvedLocationInfo.SecondaryZone,
	}

	// Set default images
	vlmConfig.Deployment.Images.VSAImageName = vsaImageName
	scalingTopology := vmrs_decision.ResolveScalingTopology(pool.IsActiveActive())
	if scalingTopology.Topology.RequiresMediator() {
		vlmConfig.Deployment.Zone.MediatorZone = resolvedLocationInfo.MediatorZone
		vlmConfig.Deployment.Images.MediatorImageName = mediatorImage
	} else {
		// Topologies without a mediator (e.g. active-active) must not carry any mediator
		clearMediatorConfig(&vlmConfig.Deployment)
	}

	if vlmConfig.Deployment.Labels == nil {
		vlmConfig.Deployment.Labels = make(map[string]string)
	}
	vlmConfig.Deployment.Labels["pool_name"] = pool.Name
	vlmConfig.Deployment.Labels["pool_uuid"] = pool.UUID
	vlmConfig.Deployment.DeploymentConfigFlags.EnableAAConfig = scalingTopology.Topology.EnableAAConfig()
	if scalingTopology.Topology == vmrs.TopologyActiveActive {
		vlmConfig.Deployment.DeploymentType = vlm.DeploymentTypeSharedHA
	}
	if pool.Account != nil {
		vlmConfig.Deployment.Labels["account_id"] = pool.Account.Name
		ontapVersion := ExtractOntapVersion(utils.GetOntapVersionBasedOnAllowlisting(pool.Account.Name))

		var enableIlb bool
		if createNasLifDuringPoolCreation {
			enableIlb = utils.IsOntapVersionGreaterOrEqual(ontapVersion, env.FileSupportOntapVersion) || utils.IsFileProtocolSupportedV2(ontapVersion)
		} else {
			enableIlb = utils.IsOntapVersionGreaterOrEqual(ontapVersion, env.FileSupportOntapVersion) && (pool.APIAccessMode == common.ONTAPMode) || (utils.IsFileProtocolSupportedV2(ontapVersion) && pool.LargeCapacity)
		}

		if enableIlb {
			vlmConfig.Deployment.DevFlags.EnableIlbSupport = true
			vlmConfig.Deployment.DeploymentConfigFlags.EnableNfsV364BitIdentifier = "true"
		} else {
			log.Debugf("File support is disabled for pool: %s", pool.Name)
		}

		// 2. Image selection is based on account allowlisting (independent of file support)
		if utils.IsAccountAllowlisted(pool.Account.Name) {
			// Use experimental images if account is allowlisted
			if vsaExperimentalImageName != "" {
				vlmConfig.Deployment.Images.VSAImageName = vsaExperimentalImageName
				log.Debugf("Using experimental VSA image for allowlisted account: %s", pool.Account.Name)
			}
			if scalingTopology.Topology.RequiresMediator() && experimentalMediatorImage != "" {
				vlmConfig.Deployment.Images.MediatorImageName = experimentalMediatorImage
				log.Debugf("Using experimental mediator image for allowlisted account: %s", pool.Account.Name)
			}
		}
	}

	// 3. Feature-flag driven image override (takes precedence when configured via VLM_WORKER_FEATURE_FLAG).
	if vlmFeatureCfg.VSAImageName != "" {
		vlmConfig.Deployment.Images.VSAImageName = vlmFeatureCfg.VSAImageName
		log.Debugf("Using feature-flag VSA image for pool: %s", pool.Name)
	}
	if vlmFeatureCfg.VSAMediatorImageName != "" {
		vlmConfig.Deployment.Images.MediatorImageName = vlmFeatureCfg.VSAMediatorImageName
		log.Debugf("Using feature-flag mediator image for pool: %s", pool.Name)
	}
	createVSAClusterDeploymentRequest.VLMConfig = vlmConfig
	createVSAClusterDeploymentRequest.OntapCredentials = ontapCredentials

	// send empty secretUri if license secret path or project id is not provided -> VLM will use default legacy license
	secretUri := utils.GetNLFSecretPath()
	if secretUri != "" {
		createVSAClusterDeploymentRequest.OntapLicense = vlm.OntapLicense{
			SecretUri: []string{secretUri},
		}
	}
}

// clearMediatorConfig removes all mediator-related settings from a deployment.
func clearMediatorConfig(d *vlm.DeploymentConfig) {
	d.Zone.MediatorZone = ""
	d.Images.MediatorImageName = ""
	d.MediatorInstanceType = ""
	d.MediatorDiskType = ""
}

func prepareCreateSVMRequest(createSVMRequest *vlm.CreateSVMRequest, svmName string, vlmConfig vlm.VLMConfig, ontapCredentials vlm.OntapCredentials) {
	createSVMRequest.Name = svmName
	createSVMRequest.VLMConfig = vlmConfig
	createSVMRequest.OntapCredentials = ontapCredentials
	if createNasLifDuringPoolCreation {
		createSVMRequest.EnableNasLif = vlmConfig.Deployment.DevFlags.EnableIlbSupport
	}
}

func prepareDeleteVSAClusterDeployment(deleteVSAClusterDeploymentRequest *vlm.DeleteVSAClusterDeploymentRequest, deploymentID string, cloudProvider string, projectID string) {
	deleteVSAClusterDeploymentRequest.DeploymentID = deploymentID
	deleteVSAClusterDeploymentRequest.ProjectID = projectID
	deleteVSAClusterDeploymentRequest.CloudProvider = cloudProvider
}

func prepareUpdateVSAClusterDeploymentRequest(updateVSAClusterDeploymentRequest *vlm.UpdateVSAClusterDeploymentRequest, currentVlmConfig vlm.VLMConfig, newVLMConfig vlm.VLMConfig, credentials vlm.OntapCredentials, bucketName string) {
	updateVSAClusterDeploymentRequest.VLMConfig = currentVlmConfig
	updateVSAClusterDeploymentRequest.NumHAPair = newVLMConfig.Deployment.NumHAPair
	updateVSAClusterDeploymentRequest.SPConfig = newVLMConfig.Deployment.SPConfig
	updateVSAClusterDeploymentRequest.OntapCredentials = credentials
	if newVLMConfig.Deployment.VSAInstanceType != currentVlmConfig.Deployment.VSAInstanceType {
		// If we set this all the time, VLM will trigger a VM rotation even if we use the same instance type.
		updateVSAClusterDeploymentRequest.NewInstanceType = newVLMConfig.Deployment.VSAInstanceType
	}
	// Set bucket name for auto-tiering attachment (used when enabling auto-tiering on expert mode pools)
	updateVSAClusterDeploymentRequest.BucketName = bucketName
	// Set AutoTierThreshold to -1 to signal VLM to skip auto-tiering threshold update.
	// This is a workaround until VLM properly handles the case where object store doesn't exist.
	// Valid threshold values are 0-100, so -1 is used as a sentinel value meaning "do not update".
	updateVSAClusterDeploymentRequest.AutoTierThreshold = -1
	// Note: HAPairIndices should be set by the caller based on the update sequence
}

func prepareZoneSwitchRequest(zoneSwitchRequest *vlm.ZoneSwitchRequest, currentVlmConfig vlm.VLMConfig, credentials vlm.OntapCredentials, action string) {
	zoneSwitchRequest.VLMConfig = currentVlmConfig
	zoneSwitchRequest.OntapCredentials = credentials
	// Set action to zone switch or revert back
	zoneSwitchRequest.Action = action
}

// executePoolBatchUpdates processes HA pair updates in batches sequentially
func executePoolBatchUpdates(ctx workflow.Context, batchPlan *activities.CalculateBatchPlanActivityOutput, currentVlmConfig *vlm.VLMConfig, newVlmConfig *vlm.VLMConfig, credentials *vlm.OntapCredentials, ontapVersion string, vsaClientWorkflowManager vlm.VlmWorkflowClient, logger log.Logger, bucketName string) (*vlm.UpdateVSAClusterDeploymentResponse, error) {
	currentConfig := currentVlmConfig
	var updateVSAClusterDeploymentResponse *vlm.UpdateVSAClusterDeploymentResponse

	// Track completed batches for error reporting in case of partial completion
	completedBatches := make([]int, 0, batchPlan.NumWorkflowCalls)

	for batchNum := 0; batchNum < batchPlan.NumWorkflowCalls; batchNum++ {
		// Get batch indices from the pre-calculated batch plan
		batchIndices := batchPlan.BatchIndices[batchNum]

		// Prepare update request
		updateRequest := &vlm.UpdateVSAClusterDeploymentRequest{}
		prepareUpdateVSAClusterDeploymentRequest(updateRequest, *currentConfig, *newVlmConfig, *credentials, bucketName)
		updateRequest.HAPairIndices = batchIndices

		logger.Info("Starting update batch", "batchNumber", batchNum+1, "totalBatches", batchPlan.NumWorkflowCalls, "indices", batchIndices, "totalHAPairs", batchPlan.NumHAPairs, "batchSize", batchPlan.BatchSize)

		// Execute the update
		response, err := vsaClientWorkflowManager.UpdateVSAClusterDeployment(ctx, updateRequest, ontapVersion)
		if err != nil {
			// Log detailed partial completion state
			logger.Errorf(
				"Pool update failed at batch %d of %d. Partial completion detected - cluster is in mixed-version state. "+
					"Completed batches: %v. Original error: %v",
				batchNum+1, batchPlan.NumWorkflowCalls, completedBatches, err)

			return nil, err
		}

		// Track successful batch completion
		completedBatches = append(completedBatches, batchNum+1)

		// Use the updated VLM config from this response as the current config for the next batch
		currentConfig = &response.VLMConfig
		updateVSAClusterDeploymentResponse = response

		logger.Info("Completed update batch", "batchNumber", batchNum+1, "totalBatches", batchPlan.NumWorkflowCalls, "indices", batchIndices)
	}

	return updateVSAClusterDeploymentResponse, nil
}

func _waitForServiceNetworkOperationStatus(ctx workflow.Context, poolActivity *activities.PoolActivity, op string, timeout time.Duration) ([]byte, error) {
	startTime := workflow.Now(ctx)
	for {
		// Check if the timeout has been reached.
		if workflow.Now(ctx).Sub(startTime) > timeout {
			return nil, vsaerror.Errorf("timeout while confirming compute network google components: %v", timeout)
		}

		// Get the status of the GCP Operation.
		operation := &hyperscalermodels.ComputeOperation{}
		err := workflow.ExecuteActivity(ctx, poolActivity.GetServiceNetOpStatus, op).Get(ctx, &operation)
		if err != nil && !vsaerror.IsNotReadyErr(err) && !vsaerror.IsNotFoundErr(err) {
			return nil, vsaerror.Errorf("failed to get GCP Operation %s: %w", op, err)
		}

		// check the state of the operation
		if operation.Done && string(operation.Response) != "" {
			return operation.Response, nil
		}

		// Sleep for a some duration before checking again.
		err = workflow.Sleep(ctx, time.Second*time.Duration(waitTimeForGCPOperationInSec))
		if err != nil {
			return nil, vsaerror.Errorf("failed to sleep while waiting for GCP Operation %s: %w", op, err)
		}
	}
}

func ReleasePSCEndpointWorkflow(ctx workflow.Context, pool *datamodel.Pool) error {
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(setupNwHeartbeatTimeout) * time.Second,
		RetryPolicy:         utils.GetHyperscalerLRORetryPolicy(),
	}
	poolActivity := &activities.PoolActivity{}
	pscActivity := &activities.PSCActivity{}
	setupPscCtx := workflow.WithActivityOptions(ctx, ao)

	if pool == nil {
		logger.Warn("pool is nil, unable to release PSC Endpoint")
		return nil
	}
	if pool.ClusterDetails.RegionalTenantProject == "" {
		logger := util.GetLogger(ctx)
		logger.Warnf("Regional tenant project is not set for pool: %s, fetching tenanct project number", pool.UUID)

		tenantProjectNumber := new(string)
		params := &common.CreatePoolParams{
			AccountName:    pool.Account.Name,
			VendorSubNetID: pool.Network,
			Region:         Region,
		}
		err = workflow.ExecuteActivity(setupPscCtx, poolActivity.FindTenancyProject, params).Get(ctx, tenantProjectNumber)
		if err != nil {
			logger.Warnf("Failed to fetch tenancy project number for pool: %s, error: %v", pool.UUID, err)
			return nil
		}
		pool.ClusterDetails.RegionalTenantProject = *tenantProjectNumber
	}

	deleteForwardingRuleOperation := make([]common.Operations, 0)
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.DeleteForwardingRule, pool).Get(ctx, &deleteForwardingRuleOperation)
	if err != nil {
		return ConvertToVSAError(err)
	}
	if deleteForwardingRuleOperation == nil {
		util.GetLogger(ctx).Infof("Unable to delete forwarding rule.")
		return nil
	}
	err = WaitForGCPNetworkOperationStatus(setupPscCtx, poolActivity, &deleteForwardingRuleOperation, retryPolicy.StartToCloseTimeout)
	if err != nil {
		return vsaerror.Errorf("failed to release PSC endpoint for tenant project: %s: %w", pool.ClusterDetails.RegionalTenantProject, err)
	}
	deleteAddressOperation := make([]common.Operations, 0)
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.DeleteAddress, pool).Get(ctx, &deleteAddressOperation)
	if err != nil {
		return ConvertToVSAError(err)
	}
	err = WaitForGCPNetworkOperationStatus(setupPscCtx, poolActivity, &deleteAddressOperation, retryPolicy.StartToCloseTimeout)
	if err != nil {
		return vsaerror.Errorf("failed to release PSC endpoint for tenant project: %s: %w", pool.ClusterDetails.RegionalTenantProject, err)
	}

	return nil
}

func ConfigurePSCEndpointWorkflow(ctx workflow.Context, projectName string, region string, node *models.Node) error {
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: utils.GetStartToCloseTimeoutHyperscaler(),
		HeartbeatTimeout:    time.Duration(setupNwHeartbeatTimeout) * time.Second,
		RetryPolicy:         utils.GetHyperscalerRetryPolicy(),
	}
	// poolActivity is used for GCP network operations
	poolActivity := &activities.PoolActivity{}
	pscActivity := &activities.PSCActivity{}
	ctx = workflow.WithActivityOptions(ctx, ao)

	setupPscCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(setupNwHeartbeatTimeout)*time.Second)

	// CreateInternalInfraSubnet
	subnetFirewallOperations := make([]common.Operations, 0)
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.CreateInternalInfraSubnet, projectName).Get(ctx, &subnetFirewallOperations)
	if err != nil {
		return ConvertToVSAError(err)
	}
	err = WaitForGCPNetworkOperationStatus(setupPscCtx, poolActivity, &subnetFirewallOperations, retryPolicy.StartToCloseTimeout)
	if err != nil {
		return vsaerror.Errorf("failed to create internal infra subnet for tenant project: %s: %w", projectName, err)
	}
	var forwardingRuleIpAddress string
	var addressURI string
	pscEndpointName := region + "-rg-fluent-bit-psc"
	createAddressOperation := make([]common.Operations, 0)
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.CreateAddressForPSCEndpoint, projectName, region, pscEndpointName).Get(ctx, &createAddressOperation)
	if err != nil {
		return ConvertToVSAError(err)
	}
	err = WaitForGCPNetworkOperationStatus(setupPscCtx, poolActivity, &createAddressOperation, retryPolicy.StartToCloseTimeout)
	if err != nil {
		return vsaerror.Errorf("failed to create PSC endpoint for tenant project: %s: %w", projectName, err)
	}
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.GetAddressURI, projectName, region, pscEndpointName).Get(ctx, &addressURI)
	if err != nil {
		return ConvertToVSAError(err)
	}
	if addressURI == "" {
		return vsaerror.Errorf("failed to get IP address of PSC endpoint from create address operation in tenant project: %s: %w", projectName, err)
	}

	createForwardingRuleOperation := make([]common.Operations, 0)
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.CreateForwardingRuleForPSCEndpoint, projectName, region, pscEndpointName, addressURI).Get(ctx, &createForwardingRuleOperation)
	if err != nil {
		return ConvertToVSAError(err)
	}
	err = WaitForGCPNetworkOperationStatus(setupPscCtx, poolActivity, &createForwardingRuleOperation, retryPolicy.StartToCloseTimeout)
	if err != nil {
		return vsaerror.Errorf("failed to create forwarding rule subnet for tenant project: %s: %w", projectName, err)
	}
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.GetForwardingRuleIPAddress, projectName, region, pscEndpointName).Get(ctx, &forwardingRuleIpAddress)
	if err != nil {
		return ConvertToVSAError(err)
	}
	if forwardingRuleIpAddress == "" {
		return vsaerror.Errorf("failed to get forwarding rule from operation for tenant project: %s: %w", projectName, err)
	}

	// Update audit log
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.UpdateSecurityAudit, node).Get(ctx, nil)
	if err != nil {
		return ConvertToVSAError(err)
	}

	// forward ontap logging to PSC Endpoint
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.CreateClusterLogForwarding, node, forwardingRuleIpAddress).Get(ctx, nil)
	if err != nil {
		return ConvertToVSAError(err)
	}

	// forward ontap EMS events to PSC Endpoint
	err = workflow.ExecuteActivity(setupPscCtx, pscActivity.CreateEMSEventForwarding, node, forwardingRuleIpAddress).Get(ctx, nil)
	if err != nil {
		return ConvertToVSAError(err)
	}
	return nil
}

func ConfigureNetworkWorkflow(ctx workflow.Context, tenancyDetails *common.TenancyInfo, poolMode string) (interface{}, error) {
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: utils.GetStartToCloseTimeoutHyperscaler(),
		HeartbeatTimeout:    time.Duration(setupNwHeartbeatTimeout) * time.Second,
		RetryPolicy:         utils.GetHyperscalerRetryPolicy(),
	}
	poolActivity := &activities.PoolActivity{}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rollbackManager := common.NewRollbackManager()

	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()
	vpcOperations := make([]common.Operations, 0)
	tenantProjectNumber := tenancyDetails.RegionalTenantProject
	err = workflow.ExecuteActivity(ctx, poolActivity.CreateVPCs, tenantProjectNumber).Get(ctx, &vpcOperations)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	err = WaitForGCPNetworkOperationStatus(ctx, poolActivity, &vpcOperations, retryPolicy.StartToCloseTimeout)
	if err != nil {
		return nil, vsaerror.Errorf("failed to create VPC for tenant project while waiting to get operation status: %s: %w", tenantProjectNumber, err)
	}

	subnetFirewallOperations := make([]common.Operations, 0)
	err = workflow.ExecuteActivity(ctx, poolActivity.CreateSubnets, tenantProjectNumber).Get(ctx, &subnetFirewallOperations)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	firewallOperations := make([]common.Operations, 0)
	err = workflow.ExecuteActivity(ctx, poolActivity.CreateFirewalls, tenantProjectNumber, tenancyDetails.SnHostProject, tenancyDetails.Network, poolMode).Get(ctx, &firewallOperations)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	subnetFirewallOperations = append(subnetFirewallOperations, firewallOperations...)
	err = WaitForGCPNetworkOperationStatus(ctx, poolActivity, &subnetFirewallOperations, retryPolicy.StartToCloseTimeout)
	if err != nil {
		return nil, vsaerror.Errorf("failed to create firewall for tenant project while waiting to get operation status: %s: %w", tenantProjectNumber, err)
	}
	return nil, nil
}

func _waitForGCPNetworkOperationStatus(ctx workflow.Context, poolActivity *activities.PoolActivity, operations *[]common.Operations, timeout time.Duration) error {
	if operations == nil {
		return nil
	}
	startTime := workflow.Now(ctx)
	var err error
	var operationsDone int
	operation := &hyperscalermodels.ComputeOperation{}
	for {
		operationsDone = 0
		for i := 0; i < len(*operations); i++ {
			op := &(*operations)[i] // Get a pointer to the original element
			if !op.IsDone {
				// Check if the timeout has been reached.
				if workflow.Now(ctx).Sub(startTime) > timeout {
					return vsaerror.Errorf("timeout while confirming compute network google components: %v", timeout)
				}

				// Get the status of the GCP Operation.
				err = workflow.ExecuteActivity(ctx, poolActivity.GetComputeOpStatus, op.Project, op.IsRegionalResource, op.OperationName).Get(ctx, &operation)
				if err != nil && !vsaerror.IsNotReadyErr(err) {
					return vsaerror.Errorf("failed to get GCP Operation %s: %w", op.OperationName, err)
				}
			}
			if (operation.Status == statusDone && operation.Progress == operationProgress) || op.IsDone {
				operationsDone++
				op.IsDone = true // this modifies the original element in the slice
			}
		}
		// If all operations are done, exit the loop
		if operationsDone == len(*operations) {
			return nil
		}
		err = workflow.Sleep(ctx, time.Second*time.Duration(waitTimeForGCPOperationInSec))
		if err != nil {
			return vsaerror.Errorf("failed to sleep while waiting for GCP Operation %s: %w", operation.Name, err)
		}
	}
}

// runPoolQosTypeTransitionAutoToManual runs the auto→manual qosType transition: remove QPG from vserver,
// create converted VPG (ONTAP + DB), assign all pool volumes to it, update pool qos_type to manual.
// Registers rollback after each step so failure restores pre-transition state.
func runPoolQosTypeTransitionAutoToManual(
	ctx workflow.Context,
	dbHbCtx workflow.Context,
	wf *updatePoolWorkflow,
	rollbackManager *common.RollbackManager,
	poolActivity *activities.PoolActivity,
	svmActivity *activities.SvmActivity,
	dbPool *datamodel.Pool,
	updatePoolParams *common.UpdatePoolParams,
	pool *datamodel.Pool,
) *vsaerrors.CustomError {
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	vpgActivity := &activities.VolumePerformanceGroupActivity{}
	updateActivity := &activities.VolumeUpdateActivity{}
	volumeDeleteActivity := &activities.VolumeDeleteActivity{}

	wf.Logger.Info("qosType transition started", "direction", "auto_to_manual", "poolUUID", dbPool.UUID)

	// Rollback params to re-apply QPG to vserver (restore auto state)
	rollbackParamsForAuto := &common.UpdatePoolParams{
		QosType:              utils.QosTypeAuto,
		TotalThroughputMibps: dbPool.PoolAttributes.ThroughputMibps,
		TotalIops:            &dbPool.PoolAttributes.Iops,
	}

	// 1. Get node for pool
	var dbNodes []*datamodel.Node
	if err := workflow.ExecuteActivity(dbHbCtx, activities.CommonActivities.GetNode, pool.ID).Get(dbHbCtx, &dbNodes); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	// 2. Remove QPG from vserver
	wf.Logger.Info("qosType auto→manual: removing QPG from vserver")
	if err := workflow.ExecuteActivity(ctx, svmActivity.RemoveQoSPolicyFromSVM, dbPool, node).Get(ctx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	// Rollback must re-apply QPG; use immutable snapshot with QosType=auto so ModifyQoSPolicyAndApplyToSVM does not skip
	rollbackPoolSnapshot := *dbPool
	rollbackPoolSnapshot.QosType = utils.QosTypeAuto
	rollbackManager.AddActivity(svmActivity.ModifyQoSPolicyAndApplyToSVM, &rollbackPoolSnapshot, node, rollbackParamsForAuto)

	// 2b. Delete any existing QoS policy with the transition name (idempotent create: leftover from previous run)
	// TransitionVPGNameFromPoolName is defined in pool_vpg_helpers.go.
	transitionName := TransitionVPGNameFromPoolName(dbPool.Name)
	if err := workflow.ExecuteActivity(ctx, vpgActivity.DeleteQoSPolicyInONTAP, transitionName, "", pool.ID, node).Get(ctx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}

	// 3. Create transition VPG (shared, not auto-generated) with pool's total throughput/IOPS; name = pool name + -vpg
	convertedVPGName := transitionName
	convertedVPG := &datamodel.VolumePerformanceGroup{
		Name:            convertedVPGName,
		PoolID:          dbPool.ID,
		ThroughputMibps: dbPool.PoolAttributes.ThroughputMibps,
		Iops:            dbPool.PoolAttributes.Iops,
		AllocationType:  models.AllocationTypeShared,
		IsAutoGen:       false,
	}
	var qosPolicyID string
	if err := workflow.ExecuteActivity(ctx, vpgActivity.CreateQoSPolicyInONTAP, convertedVPG, node).Get(ctx, &qosPolicyID); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	convertedVPG.OntapQosPolicyID = qosPolicyID
	rollbackManager.AddActivity(vpgActivity.DeleteQoSPolicyInONTAP, qosPolicyID, "", pool.ID, node)

	var createdVPG *datamodel.VolumePerformanceGroup
	if err := workflow.ExecuteActivity(ctx, vpgActivity.CreateVPGInDB, convertedVPG).Get(ctx, &createdVPG); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(volumeDeleteActivity.DeleteVPGByID, createdVPG.ID, node)

	// 4. Get volumes in pool
	var volumes []*datamodel.Volume
	if err := workflow.ExecuteActivity(dbHbCtx, volumeCreateActivity.GetPoolVolumesForQosTransition, pool.ID).Get(dbHbCtx, &volumes); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}

	// 5. Assign each volume to the converted VPG (ONTAP + DB).
	// Use VPG name (policy name in ONTAP), not OntapQosPolicyID (UUID): ONTAP volume update expects qos.policy.name.
	for _, vol := range volumes {
		if err := workflow.ExecuteActivity(ctx, updateActivity.AssignQoSPolicyToVolume, vol, createdVPG.Name, node).Get(ctx, nil); err != nil {
			wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
			return ConvertToVSAError(err)
		}
		rollbackManager.AddActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, vol.UUID, nil)
		rollbackManager.AddActivity(updateActivity.UnassignQoSPolicyFromVolume, vol, node)

		if err := workflow.ExecuteActivity(dbHbCtx, updateActivity.UpdateVolumePerformanceGroupInDBForVolume, vol.UUID, createdVPG).Get(dbHbCtx, nil); err != nil {
			wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
			return ConvertToVSAError(err)
		}
	}

	// 6. Apply all applicable update params (description, labels, size, auto-tiering, throughput, iops) so they are persisted with the pool
	applyUpdatePoolParamsToDbPool(dbPool, updatePoolParams)
	// Update pool qos_type to manual in DB
	dbPool.QosType = utils.QosTypeManual
	if err := workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatePoolFields, dbPool.UUID, map[string]interface{}{
		"qos_type": utils.QosTypeManual,
	}).Get(dbHbCtx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	// Register a compensating rollback for the hot-tier patch before applying it, so a later
	// failure restores the original values (UpdatedPool omits auto_tiering_config).
	origHot, origEnable := originalHotTierInfo(pool)
	rollbackManager.AddActivity(poolActivity.UpdateAutoTieringHotTierInfo, pool.UUID, origHot, origEnable)
	if err := persistAutoTieringHotTierInfo(dbHbCtx, poolActivity, dbPool, updatePoolParams); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	if err := workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatedPool, dbPool).Get(dbHbCtx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	wf.Logger.Info("qosType transition completed", "direction", "auto_to_manual", "poolUUID", dbPool.UUID)
	return nil
}

// runPoolQosTypeTransitionManualToAuto runs the manual→auto qosType transition: unassign volumes from VPGs,
// delete auto-generated VPGs, apply QPG to vserver, update pool qos_type to auto.
// Registers rollback after each step so failure restores pre-transition state.
func runPoolQosTypeTransitionManualToAuto(
	ctx workflow.Context,
	dbHbCtx workflow.Context,
	wf *updatePoolWorkflow,
	rollbackManager *common.RollbackManager,
	poolActivity *activities.PoolActivity,
	svmActivity *activities.SvmActivity,
	dbPool *datamodel.Pool,
	updatePoolParams *common.UpdatePoolParams,
	pool *datamodel.Pool,
) *vsaerrors.CustomError {
	vpgActivity := &activities.VolumePerformanceGroupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeDeleteActivity := &activities.VolumeDeleteActivity{}
	updateActivity := &activities.VolumeUpdateActivity{}

	wf.Logger.Info("qosType transition started", "direction", "manual_to_auto", "poolUUID", dbPool.UUID)

	rollbackParamsForAuto := &common.UpdatePoolParams{
		QosType:              utils.QosTypeAuto,
		TotalThroughputMibps: dbPool.PoolAttributes.ThroughputMibps,
		TotalIops:            &dbPool.PoolAttributes.Iops,
	}

	// 1. Get node for pool
	var dbNodes []*datamodel.Node
	if err := workflow.ExecuteActivity(dbHbCtx, activities.CommonActivities.GetNode, pool.ID).Get(dbHbCtx, &dbNodes); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	// 2. Get volumes in pool (lightweight query — no association preloads)
	var volumes []*datamodel.Volume
	if err := workflow.ExecuteActivity(dbHbCtx, volumeCreateActivity.GetPoolVolumesForQosTransition, pool.ID).Get(dbHbCtx, &volumes); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}

	// 3. List all VPGs for the pool (manual→auto deletes every pool VPG, not only auto-generated)
	var allVPGs []*datamodel.VolumePerformanceGroup
	if err := workflow.ExecuteActivity(dbHbCtx, vpgActivity.ListVolumePerformanceGroupsByPoolID, pool.ID).Get(dbHbCtx, &allVPGs); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}

	// Build volumes-per-VPG for rollback (before we delete VPGs)
	volumesByVPGID := make(map[int64][]*datamodel.Volume)
	for _, vol := range volumes {
		if vol.VolumePerformanceGroupID.Valid {
			vpgID := vol.VolumePerformanceGroupID.Int64
			volumesByVPGID[vpgID] = append(volumesByVPGID[vpgID], vol)
		}
	}

	// 4. Unassign each volume from its VPG (ONTAP + DB) and register rollback to re-assign on failure
	wf.Logger.Info("qosType manual→auto: unassigning volumes from VPGs", "direction", "manual_to_auto", "poolUUID", dbPool.UUID)
	for _, vpg := range allVPGs {
		for _, vol := range volumesByVPGID[vpg.ID] {
			if err := workflow.ExecuteActivity(ctx, updateActivity.UnassignQoSPolicyFromVolume, vol, node).Get(ctx, nil); err != nil {
				wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
				return ConvertToVSAError(err)
			}
			if err := workflow.ExecuteActivity(dbHbCtx, updateActivity.UpdateVolumePerformanceGroupInDBForVolume, vol.UUID, nil).Get(dbHbCtx, nil); err != nil {
				wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
				return ConvertToVSAError(err)
			}
			rollbackManager.AddActivity(updateActivity.UpdateVolumePerformanceGroupInDBForVolume, vol.UUID, vpg)
			rollbackManager.AddActivity(updateActivity.AssignQoSPolicyToVolume, vol, vpg.Name, node)
		}
	}

	// 5. Bulk dereference any remaining pool volumes from VPGs (e.g. soft-deleted volumes) to avoid FK on VPG delete
	wf.Logger.Info("qosType manual→auto: dereferencing pool volumes from VPGs in DB", "direction", "manual_to_auto", "poolUUID", dbPool.UUID)
	if err := workflow.ExecuteActivity(dbHbCtx, volumeDeleteActivity.DereferencePoolVolumesFromVPGs, pool.ID).Get(dbHbCtx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}

	// 6. Delete every pool VPG (QPG in ONTAP + hard-delete VPG in DB); register rollback to restore
	for _, vpg := range allVPGs {
		if err := workflow.ExecuteActivity(ctx, volumeDeleteActivity.DeleteVPGByID, vpg.ID, node).Get(ctx, nil); err != nil {
			wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
			return ConvertToVSAError(err)
		}
		volumesInVpg := volumesByVPGID[vpg.ID]
		rollbackManager.AddActivity(vpgActivity.RestoreAutoGeneratedVPG, vpg, volumesInVpg, node)
	}

	// 6b. Delete QoS policy by transition name so no leftover policy remains (e.g. from failed DeleteVPGByID or wrong UUID)
	transitionName := TransitionVPGNameFromPoolName(dbPool.Name)
	if err := workflow.ExecuteActivity(ctx, vpgActivity.DeleteQoSPolicyInONTAP, transitionName, "", pool.ID, node).Get(ctx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}

	// 7. Apply QPG to vserver. Use request params when present so the activity reliably sees QosType auto
	// (avoids serialization/deserialization issues); otherwise use rollbackParamsForAuto (pool capacity).
	paramsForApply := rollbackParamsForAuto
	if updatePoolParams != nil && updatePoolParams.QosType == utils.QosTypeAuto {
		paramsForApply = &common.UpdatePoolParams{
			QosType:              utils.QosTypeAuto,
			TotalThroughputMibps: updatePoolParams.TotalThroughputMibps,
			TotalIops:            updatePoolParams.TotalIops,
		}
		if paramsForApply.TotalThroughputMibps == 0 && dbPool.PoolAttributes != nil {
			paramsForApply.TotalThroughputMibps = dbPool.PoolAttributes.ThroughputMibps
			paramsForApply.TotalIops = &dbPool.PoolAttributes.Iops
		}
	}
	wf.Logger.Info("qosType manual→auto: applying QPG to vserver")
	// Pass pool with QosType=auto so ModifyQoSPolicyAndApplyToSVM does not early-return for manual pools
	poolForSVM := *dbPool
	poolForSVM.QosType = utils.QosTypeAuto
	if err := workflow.ExecuteActivity(ctx, svmActivity.ModifyQoSPolicyAndApplyToSVM, &poolForSVM, node, paramsForApply).Get(ctx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(svmActivity.RemoveQoSPolicyFromSVM, &poolForSVM, node)

	// 8. Apply all applicable update params (description, labels, size, auto-tiering, throughput, iops) so they are persisted with the pool
	applyUpdatePoolParamsToDbPool(dbPool, updatePoolParams)
	// Update pool qos_type to auto in DB
	dbPool.QosType = utils.QosTypeAuto
	if err := workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatePoolFields, dbPool.UUID, map[string]interface{}{
		"qos_type": utils.QosTypeAuto,
	}).Get(dbHbCtx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	// Register a compensating rollback for the hot-tier patch before applying it, so a later
	// failure restores the original values (UpdatedPool omits auto_tiering_config).
	origHot, origEnable := originalHotTierInfo(pool)
	rollbackManager.AddActivity(poolActivity.UpdateAutoTieringHotTierInfo, pool.UUID, origHot, origEnable)
	if err := persistAutoTieringHotTierInfo(dbHbCtx, poolActivity, dbPool, updatePoolParams); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	if err := workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatedPool, dbPool).Get(dbHbCtx, nil); err != nil {
		wf.Logger.Error("qosType transition failed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID, "error", err)
		return ConvertToVSAError(err)
	}
	wf.Logger.Info("qosType transition completed", "direction", "manual_to_auto", "poolUUID", dbPool.UUID)
	return nil
}

// applyUpdatePoolParamsToDbPool applies all applicable update params to dbPool so that
// when UpdatedPool is called, every request field (description, labels, size, auto-tiering,
// throughput, iops) is persisted. Used by qosType transition paths so that a request
// that includes qosType plus other fields updates everything.
func applyUpdatePoolParamsToDbPool(dbPool *datamodel.Pool, updatePoolParams *common.UpdatePoolParams) {
	dbPool.Description = updatePoolParams.Description
	if updatePoolParams.Labels != nil {
		if dbPool.PoolAttributes == nil {
			dbPool.PoolAttributes = &datamodel.PoolAttributes{}
		}
		dbPool.PoolAttributes.Labels = updatePoolParams.Labels
	}
	dbPool.SizeInBytes = int64(updatePoolParams.SizeInBytes)
	hotTierInfoFromUpdatePoolParams(dbPool, updatePoolParams)
	if dbPool.PoolAttributes == nil {
		dbPool.PoolAttributes = &datamodel.PoolAttributes{}
	}
	dbPool.PoolAttributes.ThroughputMibps = updatePoolParams.TotalThroughputMibps
	if updatePoolParams.TotalIops != nil {
		dbPool.PoolAttributes.Iops = *updatePoolParams.TotalIops
	}
}

// hotTierInfoFromUpdatePoolParams updates in-memory hot tier fields on dbPool and returns values for DB patch.
func hotTierInfoFromUpdatePoolParams(dbPool *datamodel.Pool, updatePoolParams *common.UpdatePoolParams) (hotTierSizeInBytes *int64, enableHotTierAutoResize *bool) {
	if dbPool.AutoTieringConfig == nil {
		dbPool.AutoTieringConfig = &datamodel.AutoTieringConfig{}
	}
	if updatePoolParams.AllowAutoTiering {
		dbPool.AllowAutoTiering = true
		dbPool.AutoTieringConfig.HotTierSizeInBytes = int64(updatePoolParams.HotTierSizeInBytes)
		dbPool.AutoTieringConfig.EnableHotTierAutoResize = updatePoolParams.EnableHotTierAutoResize
		return nillable.GetInt64Ptr(dbPool.AutoTieringConfig.HotTierSizeInBytes), nillable.GetBoolPtr(dbPool.AutoTieringConfig.EnableHotTierAutoResize)
	}
	dbPool.AutoTieringConfig.HotTierSizeInBytes = int64(updatePoolParams.SizeInBytes)
	return nillable.GetInt64Ptr(dbPool.AutoTieringConfig.HotTierSizeInBytes), nil
}

func persistAutoTieringHotTierInfo(
	dbHbCtx workflow.Context,
	poolActivity *activities.PoolActivity,
	dbPool *datamodel.Pool,
	updatePoolParams *common.UpdatePoolParams,
) error {
	hot, enable := hotTierInfoFromUpdatePoolParams(dbPool, updatePoolParams)
	return workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdateAutoTieringHotTierInfo, dbPool.UUID, hot, enable).Get(dbHbCtx, nil)
}

// originalHotTierInfo snapshots the pre-update hot-tier values from the original pool so a
// rollback can restore them exactly (zero values included). Returns nil pointers when the
// pool had no auto_tiering_config, which makes the rollback patch a no-op.
func originalHotTierInfo(pool *datamodel.Pool) (hotTierSizeInBytes *int64, enableHotTierAutoResize *bool) {
	if pool == nil || pool.AutoTieringConfig == nil {
		return nil, nil
	}
	return nillable.GetInt64Ptr(pool.AutoTieringConfig.HotTierSizeInBytes), nillable.GetBoolPtr(pool.AutoTieringConfig.EnableHotTierAutoResize)
}

// SyncPoolComplianceForPoolWorkflow orchestrates the pool ZI/ZS compliance sync process for a single pool
func SyncPoolComplianceForPoolWorkflow(ctx workflow.Context, pool *database.PoolIdentifier) error {
	syncPoolCompliancePoolWF := new(syncPoolComplianceForPoolWorkflow)
	err := syncPoolCompliancePoolWF.Setup(ctx, pool)
	if err != nil {
		return err
	}
	syncPoolCompliancePoolWF.Status = WorkflowStatusRunning
	_, errRun := syncPoolCompliancePoolWF.Run(ctx, pool)
	if errRun != nil {
		syncPoolCompliancePoolWF.Status = WorkflowStatusFailed
		syncPoolCompliancePoolWF.Logger.Error("Failed to sync pool compliance for pool", "PoolName", pool.Name, "Error", errRun)
		return errRun
	}
	syncPoolCompliancePoolWF.Status = WorkflowStatusCompleted
	syncPoolCompliancePoolWF.Logger.Info("Sync pool compliance completed successfully for pool", "PoolName", pool.Name)
	return nil
}

type syncPoolComplianceForPoolWorkflow struct {
	BaseWorkflow
}

var _ WorkflowInterface = &syncPoolComplianceForPoolWorkflow{}

func (wf *syncPoolComplianceForPoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	pool := input.(*database.PoolIdentifier)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = strconv.FormatInt(pool.AccountID, 10)
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *syncPoolComplianceForPoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := wf.Logger
	poolIdentifier := args[0].(*database.PoolIdentifier)

	retryPolicy, err := PopulateRetryPolicyParams(false) // Use default retry policy
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	poolActivity := &activities.PoolActivity{}
	logger.Infof("Starting synchronization of pool compliance for pool: %s", poolIdentifier.Name)

	// Step 1: Fetch pool data
	fetchInput := activities.FetchPoolDataActivityInput{
		PoolUUID:  poolIdentifier.UUID,
		AccountID: poolIdentifier.AccountID,
	}

	var fetchResult activities.FetchPoolDataActivityOutput
	err = workflow.ExecuteActivity(ctx, poolActivity.FetchPoolData, fetchInput).Get(ctx, &fetchResult)
	if err != nil {
		logger.Error("FetchPoolData activity execution failed.", "Error", err, "PoolName", poolIdentifier.Name)
		return nil, ConvertToVSAError(err)
	}

	if !fetchResult.Success {
		logger.Error("Pool data fetch failed", "PoolName", poolIdentifier.Name, "Error", fetchResult.Error)
		return nil, &vsaerrors.CustomError{Message: fetchResult.Error}
	}

	logger.Info("Pool data fetched successfully", "PoolName", poolIdentifier.Name)

	if fetchResult.IsPOPool {
		logger.Info("PO pool: marking ZI/ZS as false without VLM check", "PoolName", poolIdentifier.Name)
		updateInput := activities.UpdatePoolComplianceActivityInput{
			PoolUUID:  poolIdentifier.UUID,
			SatisfyZI: false,
			SatisfyZS: false,
		}
		var updateResult activities.UpdatePoolComplianceActivityOutput
		if err := workflow.ExecuteActivity(ctx, poolActivity.UpdatePoolCompliance, updateInput).Get(ctx, &updateResult); err != nil {
			logger.Error("UpdatePoolCompliance (PO false) failed", "Error", err, "PoolName", poolIdentifier.Name)
			return nil, ConvertToVSAError(err)
		}
		if !updateResult.Success {
			return nil, &vsaerrors.CustomError{Message: updateResult.Error}
		}
		return nil, nil
	}

	// Step 2: Call VLM workflow to get compliance data
	logger.Info("Calling VLM workflow for compliance check", "PoolName", poolIdentifier.Name)

	// Create compliance request
	req := &vlm.GetResourceInfoReq{
		ProjectID:    fetchResult.VLMConfig.Deployment.GCPConfig.ProjectID,
		DeploymentID: fetchResult.VLMConfig.Deployment.DeploymentID,
	}
	ctx = workflow.WithValue(ctx, "AccountName", fetchResult.AccountName)
	// Call VLM workflow to get compliance data
	vlmClient := GetNewVSAClientWorkflowManager()
	complianceResponse, err := vlmClient.GetClusterZiZsDetails(ctx, req)
	if err != nil {
		logger.Error("Failed to get pool compliance from VLM", "PoolName", poolIdentifier.Name, "Error", err)
		return nil, ConvertToVSAError(err)
	}

	// Extract compliance data from response
	satisfyZI := true
	satisfyZS := true
	var assetMetadata *datamodel.AssetMetadata
	if complianceResponse.ResourceInfo.GCPRI != nil {
		// Maps asset types to lists of asset names (grouped by asset_type)
		mapsByType := make(map[string][]string)
		for _, resources := range complianceResponse.ResourceInfo.GCPRI {
			for _, resource := range resources {
				if !resource.SatisfiesPzi {
					satisfyZI = false
				}
				if !resource.SatisfiesPzs {
					satisfyZS = false
				}
				// Accumulate asset_names grouped by asset_type
				if resource.AssetType != "" && len(resource.AssetLink) > 0 {
					mapsByType[resource.AssetType] = append(mapsByType[resource.AssetType], resource.AssetLink)
				}
			}
		}
		// Construct AssetMetadata
		if len(mapsByType) > 0 {
			childAssets := make([]datamodel.ChildAsset, 0)
			for assetType, assetNames := range mapsByType {
				childAssets = append(childAssets, datamodel.ChildAsset{AssetType: assetType, AssetNames: assetNames})
			}
			assetMetadata = &datamodel.AssetMetadata{
				ChildAssets: childAssets,
			}
		}
	}

	logger.Info("VLM compliance check completed",
		"PoolName", poolIdentifier.Name,
		"satisfyZI", satisfyZI,
		"satisfyZS", satisfyZS)

	// Step 3: If AutoTiering feature is enabled as well its enabled for the pool,
	// then only fetch AT bucket compliance from GCP & include in pool compliance calculation
	if utils.AutoTieringEnabled && fetchResult.AutoTieringEnabled {
		var bucketComplianceResult datamodel.BucketDetails
		err = workflow.ExecuteActivity(ctx, poolActivity.GetBucketCompliance, fetchResult.AutoTieringBucketName).Get(ctx, &bucketComplianceResult)
		if err != nil {
			logger.Error("GetBucketCompliance for auto tiering bucket activity execution failed.", "Error", err, "PoolName", poolIdentifier.Name)
			return nil, ConvertToVSAError(err)
		}

		logger.Info("Auto tiering bucket compliance fetched,",
			"PoolName", poolIdentifier.Name,
			"BucketName", fetchResult.AutoTieringBucketName,
			"satisfyZI", bucketComplianceResult.SatisfiesPzi,
			"satisfyZS", bucketComplianceResult.SatisfiesPzs,
		)

		// Logical AND of bucket compliance with cluster compliance
		// Pool is ZI/ZS compliant only if both cluster and bucket are compliant
		satisfyZI = satisfyZI && bucketComplianceResult.SatisfiesPzi
		satisfyZS = satisfyZS && bucketComplianceResult.SatisfiesPzs
	}

	// Step 4: Update pool with compliance data
	updateInput := activities.UpdatePoolComplianceActivityInput{
		PoolUUID:      poolIdentifier.UUID,
		SatisfyZI:     satisfyZI,
		SatisfyZS:     satisfyZS,
		AssetMetadata: assetMetadata,
	}

	var updateResult activities.UpdatePoolComplianceActivityOutput
	err = workflow.ExecuteActivity(ctx, poolActivity.UpdatePoolCompliance, updateInput).Get(ctx, &updateResult)
	if err != nil {
		logger.Error("UpdatePoolCompliance activity execution failed.", "Error", err, "PoolName", poolIdentifier.Name)
		return nil, ConvertToVSAError(err)
	}

	if !updateResult.Success {
		logger.Error("Pool compliance update failed", "PoolName", poolIdentifier.Name, "Error", updateResult.Error)
		return nil, &vsaerrors.CustomError{Message: updateResult.Error}
	}

	logger.Info("Pool compliance sync completed successfully",
		"PoolName", poolIdentifier.Name,
		"satisfyZI", satisfyZI,
		"satisfyZS", satisfyZS)

	return nil, nil
}

// getPoolAttributesAccountName safely gets the account name from pool attributes, returning empty string if pool attributes is nil
func getPoolAttributesAccountName(poolAttributes *datamodel.PoolAttributes) string {
	if poolAttributes == nil {
		return ""
	}
	return poolAttributes.AccountName
}

type adSyncInput struct {
	ActiveDirectoryID string
	AccountName       string
	Region            string
	XCorrelationID    string
	ActiveDirectory   *models.ActiveDirectory
	LargeCapacity     bool
}

// _syncActiveDirectoryInVcp syncs Active Directory from CVP to VCP when AD exists in CVP but not in VCP
func _syncActiveDirectoryInVcp(ctx workflow.Context, input adSyncInput, pool *datamodel.Pool) error {
	logger := util.GetLogger(ctx)
	logger.Infof("Syncing Active Directory from CVP to VCP for pool: %s, AD ID: %s", pool.Name, input.ActiveDirectoryID)

	if input.ActiveDirectory == nil {
		return vsaerrors.New("ActiveDirectory is nil, cannot sync")
	}

	retryPolicy := PopulateADSyncRetryPolicyParams()

	scheduleToCloseTimeout, err := time.ParseDuration(AdScheduleToCloseTimeout)
	if err != nil {
		logger.Errorf("an error occurred while parsing schedule-to-close timeout from value %q: %v; using default", AdScheduleToCloseTimeout, err)
		scheduleToCloseTimeout = 35 * time.Minute
	}

	ao := workflow.ActivityOptions{
		ScheduleToCloseTimeout: scheduleToCloseTimeout,
		StartToCloseTimeout:    retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:       retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	ctx, err = FetchAndSetAuthToken(ctx, input.AccountName, logger)
	if err != nil {
		return ConvertToVSAError(err)
	}

	adSyncActivity := &active_directory_activities.ActiveDirectorySyncActivity{}

	// Prepare sync parameters
	syncParams := &active_directory_activities.SyncActiveDirectoryParams{
		ActiveDirectoryID: input.ActiveDirectoryID,
		AccountName:       input.AccountName,
		LocationID:        input.Region,
		XCorrelationID:    input.XCorrelationID,
		PoolUUID:          pool.UUID,
		ActiveDirectory:   input.ActiveDirectory,
	}

	// Step 1: Call CVP API V1betaPushActiveDirectoryPassword
	var pushPasswordResult *active_directory_activities.PushActiveDirectoryPasswordResult
	err = workflow.ExecuteActivity(ctx, adSyncActivity.PushActiveDirectoryPasswordActivity, syncParams).Get(ctx, &pushPasswordResult)
	if err != nil {
		logger.Errorf("Failed to push Active Directory password to CVP: %v", err)
		return ConvertToVSAError(err)
	}

	if pushPasswordResult == nil || pushPasswordResult.Operation == nil {
		logger.Errorf("Failed to push Active Directory password to cvp: %v", pushPasswordResult)
		return ConvertToVSAError(fmt.Errorf("failed to push Active Directory password to cvp: %v", pushPasswordResult))
	}

	// Step 2: Poll for job to complete
	if pushPasswordResult != nil && pushPasswordResult.Operation != nil {
		// Prepare polling options
		pollingOptions := workflow.ActivityOptions{
			ScheduleToCloseTimeout: scheduleToCloseTimeout,
			StartToCloseTimeout:    retryPolicy.StartToCloseTimeout,
			HeartbeatTimeout:       retryPolicy.HeartBeatTimeout,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:        retryPolicy.InitialInterval,
				BackoffCoefficient:     retryPolicy.BackoffCoefficient,
				MaximumInterval:        retryPolicy.MaximumInterval,
				MaximumAttempts:        int32(maxRetryAttemptsForSDEPollJob),
				NonRetryableErrorTypes: []string{"NonRetryableError", "PanicError"},
			},
		}
		pollingCtx := workflow.WithActivityOptions(ctx, pollingOptions)

		// Poll the push password operation until completion
		err = workflow.ExecuteActivity(pollingCtx, adSyncActivity.PollPushPasswordOperationActivity, syncParams, pushPasswordResult.Operation).Get(pollingCtx, nil)
		if err != nil {
			logger.Errorf("Failed to poll push password operation: %v", err)
			return ConvertToVSAError(err)
		}
		logger.Info("Push password operation completed successfully")
	}

	// Step 3: Create ActiveDirectory entry in VCP
	var createdAD *datamodel.ActiveDirectory
	err = workflow.ExecuteActivity(ctx, adSyncActivity.CreateActiveDirectoryInVCPActivity, syncParams, pushPasswordResult.SecretName).Get(ctx, &createdAD)
	if err != nil {
		logger.Errorf("Failed to create Active Directory in VCP: %v", err)
		return ConvertToVSAError(err)
	}

	if createdAD == nil {
		return vsaerrors.New("Created ActiveDirectory is nil")
	}

	// Step 4: Update pool table's activedirectoryID with newly created AD Int ID
	err = workflow.ExecuteActivity(ctx, adSyncActivity.UpdatePoolActiveDirectoryIDActivity, syncParams, createdAD.ID).Get(ctx, nil)
	if err != nil {
		logger.Errorf("Failed to update pool ActiveDirectory ID: %v", err)
		return ConvertToVSAError(err)
	}

	logger.Infof("Successfully synced Active Directory from CVP to VCP for pool: %s", pool.Name)
	return nil
}

// CleanupServiceAccountPermissionsWorkflow is an orphan child workflow that cleans up
// service account IAM permissions from tenant projects.
func CleanupServiceAccountPermissionsWorkflow(ctx workflow.Context, pool *datamodel.Pool, retryPolicy *WorkflowRetryPolicy) error {
	logger := util.GetLogger(ctx)
	info := workflow.GetInfo(ctx)

	logger.Infof("Starting CleanupServiceAccountPermissionsWorkflow for pool %s (UUID: %s), WorkflowID: %s",
		pool.Name, pool.UUID, info.WorkflowExecution.ID)

	// Get the service-account-specific retry policy
	saRetryPolicy, err := populateServiceAccountRetryPolicyParams()
	if err != nil {
		return ConvertToVSAError(err)
	}

	saActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: saRetryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        saRetryPolicy.InitialInterval,
			BackoffCoefficient:     saRetryPolicy.BackoffCoefficient,
			MaximumInterval:        saRetryPolicy.MaximumInterval,
			MaximumAttempts:        int32(saRetryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}

	activityCtx := workflow.WithActivityOptions(ctx, saActivityOptions)
	poolActivity := &activities.PoolActivity{}

	err = workflow.ExecuteActivity(activityCtx, poolActivity.CleanupServiceAccountPermissionsInTenantProjects, pool).Get(activityCtx, nil)
	if err != nil {
		return ConvertToVSAError(err)
	}
	return nil
}
