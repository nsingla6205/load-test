package oci

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	vmrs_oci "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	defaultOCIVSAUserBootargs    = "bootarg.use.cam.nda=true;bootarg.vm.vnvram_jswap_enable=false;bootarg.vm.nvramdevice=/dev/da2;bootarg.vm.cluster_ports=e0a.pv1;bootarg.vm.vnvram.ephemeral=true;bootarg.vm.nvme.lssd_size_for_ec=4294967296;"
	password                     = "password"
	ociSerialNumberLeadingPrefix = "955"
	ociSerialNumberPrefix        = "000000000000000"
	ociSerialCounterWidth        = 13
	ociSerialPrefixLen           = 7
	ociSerialCounterMax          = int64(10_000_000_000_000)
)

var (
	ociVSAFlexOcpus                = float32(env.GetFloat64("OCI_VSA_FLEX_OCPUS", 8))
	ociVSAFlexMemoryInGBs          = float32(env.GetFloat64("OCI_VSA_FLEX_MEMORY_IN_GBS", 96))
	vsaImageName                   = strings.TrimSpace(env.GetString("VSA_IMAGE_NAME", ""))
	vsaMediatorImageName           = strings.TrimSpace(env.GetString("VSA_MEDIATOR_IMAGE_NAME", ""))
	ociDefinedTagNamespace         = env.GetString("OCI_DEFINED_TAG_NAMESPACE", "netapp_tags")
	ociVSAInstanceType             = env.GetString("OCI_VSA_INSTANCE_TYPE", "VM.DenseIO.E5.Flex")
	ociMediatorInstanceType        = env.GetString("OCI_MEDIATOR_INSTANCE_TYPE", "VM.Standard3.Flex")
	ociVSAUserBootargs             = env.GetString("OCI_VSA_USER_BOOTARGS", defaultOCIVSAUserBootargs)
	localRegion                    = env.GetString("LOCAL_REGION", "")
	ociCreator                     = env.GetString("OCI_CREATOR", "vcp")
	secretURI                      = env.GetString("SECRET_URI", "")
	smcLicenseSecretURI            = env.GetString("SMC_LICENSE_SECRET_URI", "")
	dbHeartbeatTimeoutSec          = env.GetUint64("DATABASE_HEARTBEAT_TIMEOUT_SEC", 10)
	sharedHADataDiskCount          = env.GetIntNotNegative("OCI_VSA_SHARED_HA_DATA_DISK_COUNT", 13)
	nonSharedHADataDiskCount       = env.GetIntNotNegative("OCI_VSA_NON_SHARED_HA_DATA_DISK_COUNT", 10)
	minPerDiskSizeGi               = env.GetIntNotNegative("OCI_VSA_MIN_PER_DISK_SIZE_GI", 50)
	extIPForNodeMgmt               = env.GetBool("OCI_VSA_EXT_IP_FOR_NODE_MGMT", false)
	allowNonDenseShapeForVSA       = env.GetBool("OCI_VSA_ALLOW_NON_DENSE_SHAPE_FOR_VSA", true)
	useSecondaryIPsForLIFs         = env.GetBool("OCI_VSA_USE_SECONDARY_IPS_FOR_LIFS", true)
	disableVsaCleanupOnVLMFailure  = env.GetBool("DISABLE_VSA_CLEANUP_ON_VLM_FAILURE", false)
	enableAdminTempPassword        = env.GetBool("ENABLE_ADMIN_TEMP_PASSWORD", false)
	ociExpertModeRbacURL           = env.GetString("OCI_EXPERT_MODE_RBAC_FILE_URL", "")
	ociExpertModeRbacHash          = env.GetString("OCI_EXPERT_MODE_RBAC_FILE_CHECKSUM", "")
	ociExpertModeUsername          = env.GetString("OCI_EXPERT_MODE_USERNAME", "ociadmin")
	ociExpertModePassword          = env.GetString("OCI_EXPERT_MODE_PASSWORD", "")
	ociEnableITCRecovery           = env.GetBool("OCI_ENABLE_ITC_RECOVERY", false)
	parallelNumberOfNodesForITCOCI = env.GetIntNotNegative("OCI_PARALLEL_NUMBER_OF_NODES_FOR_ITC", 4)
	ociCellNumber                  = env.GetString("LOCAL_CELL", "00")
	ociVSASerialAllocationEnabled  = env.GetBool("OCI_VSA_SERIAL_NUMBER_ALLOCATION_ENABLED", true)
	ociVMRSEnabled                 = env.GetBool("OCI_VMRS_ENABLED", false)
	ocidRegex                      = regexp.MustCompile(`^ocid1\.[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]*(\.[a-z0-9_-]*)?\.[a-z0-9_-]+$`)
)

// ociSharedHAAggrsPerPair is the number of aggregates an OCI shared-HA pair
// owns. Shared HA runs active-active, so each of the two nodes backs its own
// aggregate. Regional (non-shared) HA is active-passive and mirrors a single
// aggregate across the pair instead.
const ociSharedHAAggrsPerPair = 2

// catalogActivityOptions are used for VMRS catalogue operations. Tight
// timeout, no retry (deterministic catalogue read / selector run).
var catalogActivityOptions = workflow.ActivityOptions{
	StartToCloseTimeout: 1 * time.Minute,
	RetryPolicy: &temporal.RetryPolicy{
		MaximumAttempts:        1,
		NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
	},
}

func buildOCISerialPrefix(regionCode, cellCode string) (string, error) {
	if err := validateNumericCode("region", regionCode, 2); err != nil {
		return "", err
	}
	if err := validateNumericCode("cell", cellCode, 2); err != nil {
		return "", err
	}
	return ociSerialNumberLeadingPrefix + regionCode + cellCode, nil
}

func validateNumericCode(name, s string, want int) error {
	if len(s) != want {
		return utilserrors.NewUserInputValidationErr(
			fmt.Sprintf("invalid %s code %q: expected %d digits, got %d", name, s, want, len(s)),
		)
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return utilserrors.NewUserInputValidationErr(
				fmt.Sprintf("invalid %s code %q: must contain only digits", name, s),
			)
		}
	}
	return nil
}

// ValidateOCIWorkerStartupEnv ensures OCI worker startup has all required environment variables
// and validates their format. This is called from worker/main.go when HYPERSCALER=oci to fail
// fast before polling workflows, catching misconfiguration at container startup rather than at
// request time.
func ValidateOCIWorkerStartupEnv() error {
	var validationErrors []string

	missing := make([]string, 0, 5)
	if strings.TrimSpace(vsaImageName) == "" {
		missing = append(missing, "VSA_IMAGE_NAME")
	}
	if strings.TrimSpace(vsaMediatorImageName) == "" {
		missing = append(missing, "VSA_MEDIATOR_IMAGE_NAME")
	}
	if strings.TrimSpace(localRegion) == "" {
		missing = append(missing, "LOCAL_REGION")
	}
	if strings.TrimSpace(secretURI) == "" {
		missing = append(missing, "SECRET_URI")
	}
	if len(missing) > 0 {
		validationErrors = append(validationErrors, fmt.Sprintf("missing required env vars: %s", strings.Join(missing, ", ")))
	}
	if sharedHADataDiskCount <= 0 {
		validationErrors = append(validationErrors, fmt.Sprintf("OCI_VSA_SHARED_HA_DATA_DISK_COUNT must be > 0, got %d", sharedHADataDiskCount))
	}
	if nonSharedHADataDiskCount <= 0 {
		validationErrors = append(validationErrors, fmt.Sprintf("OCI_VSA_NON_SHARED_HA_DATA_DISK_COUNT must be > 0, got %d", nonSharedHADataDiskCount))
	}
	var invalid []string
	if strings.TrimSpace(vsaImageName) != "" && !isValidOCID(vsaImageName) {
		invalid = append(invalid, fmt.Sprintf("VSA_IMAGE_NAME=%q", vsaImageName))
	}
	if strings.TrimSpace(vsaMediatorImageName) != "" && !isValidOCID(vsaMediatorImageName) {
		invalid = append(invalid, fmt.Sprintf("VSA_MEDIATOR_IMAGE_NAME=%q", vsaMediatorImageName))
	}
	if len(invalid) > 0 {
		validationErrors = append(validationErrors, fmt.Sprintf("env vars are not valid OCIDs: %s", strings.Join(invalid, ", ")))
	}
	if len(validationErrors) > 0 {
		return fmt.Errorf("invalid OCI worker startup configuration: %s", strings.Join(validationErrors, "; "))
	}
	return nil
}

func isValidOCID(s string) bool {
	return ocidRegex.MatchString(strings.TrimSpace(s))
}

// GetVSAMediatorImageName returns the configured VSA_MEDIATOR_IMAGE_NAME env var value.
// Used by the factory layer as a fallback when the API request omits mediatorImagePath.
func GetVSAMediatorImageName() string {
	return vsaMediatorImageName
}

type ociCreatePoolWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &ociCreatePoolWorkflow{}

func logOCICreatePoolInfo(logger log.Logger, pool *datamodel.Pool, stage, msg string, extra ...any) {
	args := append([]any{
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"deploymentName", pool.DeploymentName,
		"stage", stage,
	}, extra...)
	logger.Info(msg, args...)
}

func logOCICreatePoolError(logger log.Logger, pool *datamodel.Pool, stage, msg string, err error, extra ...any) {
	args := append([]any{
		"error", err,
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"deploymentName", pool.DeploymentName,
		"stage", stage,
	}, extra...)
	logger.Error(msg, args...)
}

func logOCICreatePoolWarn(logger log.Logger, pool *datamodel.Pool, stage, msg string, extra ...any) {
	args := append([]any{
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"deploymentName", pool.DeploymentName,
		"stage", stage,
	}, extra...)
	logger.Warn(msg, args...)
}

// handleActivityError turns activity failures into VSA errors. Timeouts are logged and mapped to ErrWorkflowSupervisorTimeout.
func handleActivityError(logger log.Logger, err error, operation, resource string) *vsaerrors.CustomError {
	if err == nil {
		return nil
	}
	if temporal.IsTimeoutError(err) {
		logger.Error("Activity timed out",
			"operation", operation,
			"resource", resource,
			"error", err,
		)
		wrappedErr := fmt.Errorf("%s timed out for %s: %w", operation, resource, err)
		return workflows.ConvertToVSAError(
			vsaerrors.NewVCPError(vsaerrors.ErrWorkflowSupervisorTimeout, wrappedErr),
		)
	}
	return workflows.ConvertToVSAError(err)
}

// OCICreatePoolWorkflow processes pool related requests from a customer for OCI.
func OCICreatePoolWorkflow(ctx workflow.Context, params *common.CreatePoolParams, pool *datamodel.Pool) error {
	start := workflow.Now(ctx)
	createPoolWF := new(ociCreatePoolWorkflow)
	logger := util.GetLogger(ctx)
	err := createPoolWF.Setup(ctx, params)
	if err != nil {
		emitDuration(ctx, wfCreatePool, queueCustomer, start)
		return err
	}

	createPoolWF.Status = workflows.WorkflowStatusRunning
	_, errRun := createPoolWF.Run(ctx, params, pool)
	if errRun != nil {
		logger.Error("ociCreatePoolWorkflow failed",
			"error", errRun,
			"pool", pool.Name,
			"poolUUID", pool.UUID,
			"deploymentName", pool.DeploymentName,
			"account", params.AccountName,
		)
		createPoolWF.Status = workflows.WorkflowStatusFailed
		emitDuration(ctx, wfCreatePool, queueCustomer, start)
		return errRun
	}
	createPoolWF.Status = workflows.WorkflowStatusCompleted
	emitDuration(ctx, wfCreatePool, queueCustomer, start)
	return nil
}

func (wf *ociCreatePoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createPoolParams := input.(*common.CreatePoolParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createPoolParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ociCreatePoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	if len(args) < 2 {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCICreatePoolWorkflow.Run: expected 2 args, got %d", len(args)))
	}
	params, ok := args[0].(*common.CreatePoolParams)
	if !ok {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCICreatePoolWorkflow.Run: args[0] has unexpected type %T, want *common.CreatePoolParams", args[0]))
	}
	if params == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCICreatePoolWorkflow.Run: args[0] (*common.CreatePoolParams) must not be nil")))
	}
	pool, ok := args[1].(*datamodel.Pool)
	if !ok {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCICreatePoolWorkflow.Run: args[1] has unexpected type %T, want *datamodel.Pool", args[1]))
	}
	if pool == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCICreatePoolWorkflow.Run: args[1] (*datamodel.Pool) must not be nil")))
	}

	poolActivity := &activities.PoolActivity{}
	rollbackManager := common.NewRollbackManager()
	rollbackManager.AddActivity(poolActivity.ErroredPool, pool)
	var err error

	logger := util.GetLogger(ctx)
	logOCICreatePoolInfo(logger, pool, "workflow_start", "OCI create pool workflow started",
		"account", params.AccountName,
		"region", params.Region,
		"haPairs", params.HAPairs,
	)

	// Set up activity options once and reuse for both normal and rollback paths.
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		logOCICreatePoolError(logger, pool, "workflow_setup", "Failed to populate retry policy params", err)
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackCtx := workflow.WithActivityOptions(disconnectedCtx, ao)
			rollbackManager.ExecuteRollback(rollbackCtx, err)
		}
	}()

	// Run OCI VMRS (when enabled) to derive VM shape, OCPUs, VPU band,
	// headroom throughput, and resolved IOPS from the pool's
	// (capacity, throughput, AA-vs-AP topology). DecideOCIVMRS owns the
	// catalogue load and all performance resolution; prepareVLMConfig
	// consumes the Decision directly.
	// When the toggle is false the activity is skipped entirely and
	// prepareVLMConfig passes the raw API params to VLM unchanged.
	//
	// The OCI_VMRS_ENABLED toggle is read via workflow.SideEffect to stay
	// replay-deterministic (see .cursor/agents/workflow-builder.mdc).
	var ociDecision *vmrs_oci.Decision
	var vmrsEnabledForRun bool
	if err = workflow.SideEffect(ctx, func(_ workflow.Context) interface{} {
		return ociVMRSEnabled
	}).Get(&vmrsEnabledForRun); err != nil {
		logOCICreatePoolError(logger, pool, stageVMRS, "Failed to read OCI VMRS toggle via SideEffect", err)
		return nil, workflows.ConvertToVSAError(err)
	}
	if vmrsEnabledForRun {
		vmrsCtx := workflow.WithActivityOptions(ctx, catalogActivityOptions)
		err = workflow.ExecuteActivity(vmrsCtx, poolActivity.DecideOCIVMRS, newOCICreateVMRSRequest(params, pool)).Get(vmrsCtx, &ociDecision)
		if err != nil {
			logOCICreatePoolError(logger, pool, stageVMRS, "OCI VMRS selection failed", err)
			emitStage(ctx, wfCreatePool, queueCustomer, stageVMRS, resultFailure)
			return nil, workflows.ConvertToVSAError(err)
		}
		logOCICreatePoolInfo(logger, pool, stageVMRS, "OCI VMRS decision completed",
			"vmShape", ociDecision.VMShape,
			"ocpus", ociDecision.OCPUs,
			"vpu", ociDecision.VPU,
			"throughputWithHeadroom", ociDecision.ThroughputWithHeadroom,
			"throughput", ociDecision.Throughput,
			"resolvedIOPS", ociDecision.ResolvedIOPS,
		)
		emitStage(ctx, wfCreatePool, queueCustomer, stageVMRS, resultSuccess)
	}

	// Resolve OCI fault domains before building the VLM config. Each leg uses the
	// caller-supplied fault domain when provided (validated in the activity);
	// otherwise the data VMs get the least-loaded fault domain in their
	// availability domain and the mediator gets a random one. Runs in an activity
	// because it reads node counts and uses randomness (non-deterministic).
	// Dedicated options: this is a fast DB read + in-memory pick, so a tight
	// timeout fails fast; invalid overrides surface as non-retryable, so the small
	// retry budget only covers transient DB errors.
	fdAO := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:        3,
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}
	fdCtx := workflow.WithActivityOptions(ctx, fdAO)
	fdReq := activities.SelectFaultDomainsRequest{
		PrimaryZone:          params.PrimaryZone,
		SecondaryZone:        params.SecondaryZone,
		MediatorZone:         params.MediatorZone,
		PrimaryFaultDomain:   params.PrimaryFaultDomain,
		SecondaryFaultDomain: params.SecondaryFaultDomain,
		MediatorFaultDomain:  params.MediatorFaultDomain,
	}
	var fdResult activities.SelectFaultDomainsResult
	logOCICreatePoolInfo(logger, pool, stageFaultDomains, "Starting OCI fault domain selection")
	if err = workflow.ExecuteActivity(fdCtx, poolActivity.SelectFaultDomains, fdReq).Get(fdCtx, &fdResult); err != nil {
		logOCICreatePoolError(logger, pool, stageFaultDomains, "Failed to select OCI fault domains", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageFaultDomains, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	params.PrimaryFaultDomain = fdResult.PrimaryFaultDomain
	params.SecondaryFaultDomain = fdResult.SecondaryFaultDomain
	params.MediatorFaultDomain = fdResult.MediatorFaultDomain
	logOCICreatePoolInfo(logger, pool, stageFaultDomains, "OCI fault domain selection completed",
		"primaryFaultDomain", fdResult.PrimaryFaultDomain,
		"secondaryFaultDomain", fdResult.SecondaryFaultDomain,
		"mediatorFaultDomain", fdResult.MediatorFaultDomain,
	)
	emitStage(ctx, wfCreatePool, queueCustomer, stageFaultDomains, resultSuccess)

	vlmConfig, err := prepareVLMConfig(params, pool, ociDecision)
	if err != nil {
		logOCICreatePoolError(logger, pool, stageVLMConfigPrep, "Failed to prepare VLM config", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageVLMConfigPrep, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	emitStage(ctx, wfCreatePool, queueCustomer, stageVLMConfigPrep, resultSuccess)

	if pool.PoolCredentials == nil {
		err = fmt.Errorf("pool credentials are required to create ONTAP admin credentials for pool %q", pool.Name)
		logOCICreatePoolError(logger, pool, stageOntapCredentials, "Pool credentials missing for ONTAP admin credential creation", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageOntapCredentials, resultFailure)
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, err))
	}
	logOCICreatePoolInfo(logger, pool, stageOntapCredentials, "Starting ONTAP credential creation for OCI pool")
	credConfig := &activities.OCICreatePoolCredentials{}
	err = workflow.ExecuteActivity(ctx, poolActivity.CreateOnTapCredentialsForOCI, pool).Get(ctx, credConfig)
	if err != nil {
		logOCICreatePoolError(logger, pool, stageOntapCredentials, "Failed to create ONTAP credentials for OCI pool", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageOntapCredentials, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	emitStage(ctx, wfCreatePool, queueCustomer, stageOntapCredentials, resultSuccess)
	logOCICreatePoolInfo(logger, pool, stageOntapCredentials, "ONTAP credentials created for OCI pool")
	if !disableVsaCleanupOnVLMFailure {
		rollbackManager.AddActivity(poolActivity.DeleteOnTapCredentialsForOCI, pool)
	}
	if credConfig.Certificate != nil {
		pool.PoolCredentials.ExternalCertificate = credConfig.Certificate
	}
	if credConfig.Secret != nil {
		pool.PoolCredentials.ExternalSecret = credConfig.Secret
	}

	// Resolve expert-mode password before VLM deploy so vault/config errors fail fast.
	// When ENABLE_ADMIN_TEMP_PASSWORD is true, the caller-supplied temporary admin
	// password takes precedence over both the OCI_EXPERT_MODE_PASSWORD env var and
	// the OCI Vault secret (even when OciAdminPassword is present). When the flag is
	// false, the temp password is ignored entirely and the env var / Vault path is
	// used. Never log the plain-text password value.
	var expertModeAdminPassword string
	var usedTemporaryAdminPassword bool
	if enableAdminTempPassword && params.OciAdminTempPassword != "" {
		expertModeAdminPassword = params.OciAdminTempPassword
		usedTemporaryAdminPassword = true
		logOCICreatePoolInfo(logger, pool, stageExpertModeCredentials, "Using caller-supplied temporary admin password")
		emitStage(ctx, wfCreatePool, queueCustomer, stageExpertModeCredentials, resultSuccess)
	} else {
		expertModeAdminPassword = ociExpertModePassword
		if expertModeAdminPassword == "" {
			if params.OciAdminPassword == nil || params.OciAdminPassword.Ocid == "" {
				err = vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCI admin password config is required when OCI_EXPERT_MODE_PASSWORD env var is not set"))
				logOCICreatePoolError(logger, pool, stageExpertModeCredentials, "OCI admin password config missing for expert mode", err)
				emitStage(ctx, wfCreatePool, queueCustomer, stageExpertModeCredentials, resultFailure)
				return nil, workflows.ConvertToVSAError(err)
			}
			pool.PoolCredentials.ExpertModeSecret = &datamodel.ExternalCredRef{
				ExternalIdentifier: params.OciAdminPassword.Ocid,
				Version:            params.OciAdminPassword.Version,
			}
			logOCICreatePoolInfo(logger, pool, stageExpertModeCredentials, "Fetching expert mode credentials from OCI Vault",
				"expertModeSecretOcid", params.OciAdminPassword.Ocid,
				"expertModeSecretVersion", params.OciAdminPassword.Version,
			)
			expertModeConfig := &vlm.OntapCredentials{}
			err = workflow.ExecuteActivity(ctx, poolActivity.GetExpertModeCredentialsForOCI, pool, params.OciAdminPassword).Get(ctx, expertModeConfig)
			if err != nil {
				logOCICreatePoolError(logger, pool, stageExpertModeCredentials, "Failed to get expert mode credentials for OCI pool", err)
				emitStage(ctx, wfCreatePool, queueCustomer, stageExpertModeCredentials, resultFailure)
				return nil, workflows.ConvertToVSAError(err)
			}
			expertModeAdminPassword = expertModeConfig.AdminPassword
			emitStage(ctx, wfCreatePool, queueCustomer, stageExpertModeCredentials, resultSuccess)
			logOCICreatePoolInfo(logger, pool, stageExpertModeCredentials, "Expert mode credentials fetched from OCI Vault")
		} else {
			logOCICreatePoolInfo(logger, pool, stageExpertModeCredentials, "Using expert mode password from environment configuration")
			emitStage(ctx, wfCreatePool, queueCustomer, stageExpertModeCredentials, resultSuccess)
		}
	}

	// Get VLM worker queue
	vlmWorkerQueue := vlm.GetVLMWorkerQueue(logger, pool.Account.Name)
	if vlmWorkerQueue == "" {
		err = vsaerrors.NewVCPError(vsaerrors.ErrWorkflowTaskQueueEmpty, fmt.Errorf("VLM worker queue cannot be empty"))
		logOCICreatePoolError(logger, pool, stageVLMDeploy, "VLM worker queue is empty", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageVLMDeploy, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}

	// Get VSA client workflow manager
	vsaClientWorkflowManager := workflows.GetNewVSAClientWorkflowManager()

	// Prepare CreateVSAClusterDeploymentRequest
	createVSAClusterDeploymentRequest := &vlm.CreateVSAClusterDeploymentRequest{}
	prepareCreateVSAClusterDeploymentRequest(createVSAClusterDeploymentRequest, *vlmConfig, credConfig.OntapCredentials, pool)

	if ociVSASerialAllocationEnabled {
		logOCICreatePoolInfo(logger, pool, stageSerialAllocation, "Starting OCI VM serial number allocation")
		if err = allocateOCIVMSerialNumbers(ctx, poolActivity, createVSAClusterDeploymentRequest); err != nil {
			logOCICreatePoolError(logger, pool, stageSerialAllocation, "Failed to allocate OCI VM serial numbers", err)
			emitStage(ctx, wfCreatePool, queueCustomer, stageSerialAllocation, resultFailure)
			return nil, workflows.ConvertToVSAError(err)
		}
		emitStage(ctx, wfCreatePool, queueCustomer, stageSerialAllocation, resultSuccess)
		logOCICreatePoolInfo(logger, pool, stageSerialAllocation, "OCI VM serial number allocation completed")
	}

	if !disableVsaCleanupOnVLMFailure {
		req := &vlm.DeleteVSAClusterDeploymentRequest{}
		prepareOCIDeleteVSAClusterDeploymentRequest(req, pool, params.AccountName)
		rollbackManager.AddWorkflow(vlmWorkerQueue, vlm.DeleteVSAClusterDeploymentWorkflowName, req)
	}
	// Call CreateVSAClusterDeployment (runs on VLM worker as child workflow).
	// VLM must branch on Deployment.Provider: for provider "oci" it must NOT call GCP APIs
	// (e.g. compute.v1.RegionAddressesService.Get). Calling GCP with empty/wrong project yields
	// RESOURCE_PROJECT_INVALID and "failed to get address".
	logOCICreatePoolInfo(logger, pool, stageVLMDeploy, "Starting VSA cluster deployment via VLM",
		"vlmWorkerQueue", vlmWorkerQueue,
		"deploymentID", createVSAClusterDeploymentRequest.VLMConfig.Deployment.DeploymentID,
	)
	createVSAClusterDeploymentResponse, err := vsaClientWorkflowManager.CreateVSAClusterDeployment(ctx, createVSAClusterDeploymentRequest, vlmWorkerQueue)
	if err != nil {
		logOCICreatePoolError(logger, pool, stageVLMDeploy, "Failed to create VSA cluster deployment", err,
			"vlmWorkerQueue", vlmWorkerQueue,
		)
		emitStage(ctx, wfCreatePool, queueCustomer, stageVLMDeploy, resultFailure)
		return nil, handleActivityError(logger, err, "CreateVSAClusterDeployment", pool.Name)
	}
	emitStage(ctx, wfCreatePool, queueCustomer, stageVLMDeploy, resultSuccess)
	logOCICreatePoolInfo(logger, pool, stageVLMDeploy, "VSA cluster deployment completed via VLM",
		"clusterName", createVSAClusterDeploymentResponse.VLMConfig.VsaCluster.ClusterName,
	)

	expertModeReq := &vlm.OntapExpertModeUserConfig{
		VLMConfig:          createVSAClusterDeploymentResponse.VLMConfig,
		OntapCredentials:   credConfig.OntapCredentials,
		RbacFileURL:        ociExpertModeRbacURL,
		RbacFileChecksum:   ociExpertModeRbacHash,
		Username:           ociExpertModeUsername,
		AuthenticationType: password,
		ExpertModeUserCredentials: vlm.OntapCredentials{
			AdminPassword: expertModeAdminPassword,
			Certificate:   vlm.OntapCertificate{},
		},
	}
	// TODO: change AuthenticationType once the certs are implemented for OCI expert mode users
	// if pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
	// 	expertModeReq.AuthenticationType = certificate
	// } else {
	// 	expertModeReq.AuthenticationType = password
	// }

	logOCICreatePoolInfo(logger, pool, stageExpertModeUser, "Starting expert mode user creation via VLM")
	if _, err = vsaClientWorkflowManager.CreateVSAExpertModeUser(ctx, expertModeReq); err != nil {
		logOCICreatePoolError(logger, pool, stageExpertModeUser, "Failed to create expert mode user for OCI pool", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageExpertModeUser, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	emitStage(ctx, wfCreatePool, queueCustomer, stageExpertModeUser, resultSuccess)
	logOCICreatePoolInfo(logger, pool, stageExpertModeUser, "Expert mode user created for OCI pool")

	//   - env.OCIUseTLSSNIOverride == true (DEFAULT on OCI): skip
	//     OCICreateCloudDNSRecords and WaitForNodeDNS for cert-auth pools
	//     and use a synthetic SNI string at TLS handshake time (see
	//     _saveNodeDetails + _getProviderByNode).
	//   - env.OCIUseTLSSNIOverride == false (explicit opt-out / fallback):
	//     run the legacy OCICreateCloudDNSRecords + WaitForNodeDNS path so
	//     SaveVSANodeDetails sees a populated IP → FQDN hostMap and the
	//     REST client can dial FQDNs whose DNS records have been
	//     published.
	hostMap := make(map[string]string)
	if !env.OCIUseTLSSNIOverride {
		logOCICreatePoolInfo(logger, pool, stageDNSSetup, "Starting OCI DNS record creation and readiness wait",
			"authType", pool.PoolCredentials.AuthType,
		)
		err = workflow.ExecuteActivity(ctx, poolActivity.OCICreateCloudDNSRecords,
			createVSAClusterDeploymentResponse.VLMConfig,
			pool.DeploymentName,
			pool.PoolCredentials.AuthType,
		).Get(ctx, &hostMap)
		if err != nil {
			logOCICreatePoolError(logger, pool, stageDNSSetup, "Failed to create OCI DNS records for cert-auth pool", err,
				"authType", pool.PoolCredentials.AuthType,
			)
			emitStage(ctx, wfCreatePool, queueCustomer, stageDNSSetup, resultFailure)
			return nil, workflows.ConvertToVSAError(err)
		}

		if !disableVsaCleanupOnVLMFailure && pool.PoolCredentials.AuthType == env.USER_CERTIFICATE && len(hostMap) > 0 {
			rollbackManager.AddActivity(poolActivity.OCIDeleteCloudDNSRecords, hostMap, pool.PoolCredentials.AuthType)
		}

		// Block until OCI private DNS has published dns-N.<deployment>.<dnsSuffix>.
		// Without this, SaveVSANodeDetails races VLM's DNS publish and frequently
		// fails with "no such host" on the cluster_get probe.
		err = workflow.ExecuteActivity(ctx, poolActivity.WaitForNodeDNS, pool, createVSAClusterDeploymentResponse.VLMConfig, pool.DeploymentName).Get(ctx, nil)
		if err != nil {
			logOCICreatePoolError(logger, pool, stageDNSSetup, "OCI node DNS is not ready", err)
			emitStage(ctx, wfCreatePool, queueCustomer, stageDNSSetup, resultFailure)
			return nil, workflows.ConvertToVSAError(err)
		}
		emitStage(ctx, wfCreatePool, queueCustomer, stageDNSSetup, resultSuccess)
		logOCICreatePoolInfo(logger, pool, stageDNSSetup, "OCI DNS records created and node DNS is ready",
			"hostMapSize", len(hostMap),
		)
	} else if pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
		// Only emit the bypass log for the path that actually changes
		// behaviour. Password-auth pools never went through the DNS
		// activities (they short-circuit inside both helpers), so logging
		// a "skipping DNS" message for them would be misleading.
		logOCICreatePoolInfo(logger, pool, stageDNSSetup, "Skipping OCI DNS activities because TLS SNI override is enabled",
			"authType", pool.PoolCredentials.AuthType,
			"ociUseTLSSNIOverride", true,
		)
	}

	// Create database heartbeat context for long-running DB operations
	// This inherits StartToCloseTimeout from parent context but uses shorter heartbeat timeout
	dbHbCtx := workflow.WithHeartbeatTimeout(ctx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)

	// Set up the dedicated client mTLS cert BEFORE saving node details so the
	// subsequent SaveVSANodeDetails exercises the client certificate as its
	// connectivity check (the cert is selected by the mTLS flag inside
	// GetProviderByNode). Endpoints are resolved from the VLM config because the
	// nodes are not yet persisted at this point.
	if env.OCISeparateMTLSClientCert && pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
		logOCICreatePoolInfo(logger, pool, stageOntapCredentials, "Setting up dedicated client mTLS certificate for OCI pool")
		clientCertRef := &datamodel.ExternalCredRef{}
		if err = workflow.ExecuteActivity(ctx, poolActivity.SetupVcpClientCertificateForOCI, pool, createVSAClusterDeploymentResponse.VLMConfig, pool.DeploymentName, hostMap).Get(ctx, clientCertRef); err != nil {
			logOCICreatePoolError(logger, pool, stageOntapCredentials, "Failed to set up client mTLS certificate for OCI pool", err)
			emitStage(ctx, wfCreatePool, queueCustomer, stageOntapCredentials, resultFailure)
			return nil, workflows.ConvertToVSAError(err)
		}
		if clientCertRef != nil && clientCertRef.ExternalIdentifier != "" {
			pool.PoolCredentials.ExternalClientCertificate = clientCertRef
		}
	}

	// Set up the dedicated read-only service certificate + ONTAP identity for an
	// external (non-VCP) service. Gated by OCI_ENABLE_SERVICE_ONTAP_CERT and
	// cert-auth pools. Fail-fast: any mint/install/account error fails the pool
	// create. The OCID reference (if minted) is persisted alongside the other creds.
	if env.OCIEnableServiceONTAPCert && pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
		logOCICreatePoolInfo(logger, pool, stageOntapCredentials, "Setting up dedicated read-only service certificate for OCI pool")
		serviceCertRef := &datamodel.ExternalCredRef{}
		if err = workflow.ExecuteActivity(ctx, poolActivity.SetupOciClientCertificateForOCI, pool, createVSAClusterDeploymentResponse.VLMConfig, pool.DeploymentName, hostMap).Get(ctx, serviceCertRef); err != nil {
			logOCICreatePoolError(logger, pool, stageOntapCredentials, "Failed to set up read-only service certificate for OCI pool", err)
			emitStage(ctx, wfCreatePool, queueCustomer, stageOntapCredentials, resultFailure)
			return nil, workflows.ConvertToVSAError(err)
		}
		if serviceCertRef != nil && serviceCertRef.ExternalIdentifier != "" {
			pool.PoolCredentials.ExternalServiceCertificate = serviceCertRef
		}
	}

	// Save VSA node details to database. When the separate client mTLS cert is
	// enabled, the node built here carries ExternalClientCertificate, so
	// GetProviderByNode authenticates with the client certificate — validating the
	// full mTLS trust chain (client cert + installed client CA) as part of the save.
	logOCICreatePoolInfo(logger, pool, stageSaveNodeDetails, "Saving VSA node details to database")
	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.SaveVSANodeDetails, pool, createVSAClusterDeploymentResponse.VLMConfig, pool.DeploymentName, &hostMap).Get(dbHbCtx, nil)
	if err != nil {
		logOCICreatePoolError(logger, pool, stageSaveNodeDetails, "Failed to save VSA node details to database", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageSaveNodeDetails, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	emitStage(ctx, wfCreatePool, queueCustomer, stageSaveNodeDetails, resultSuccess)
	logOCICreatePoolInfo(logger, pool, stageSaveNodeDetails, "VSA node details saved to database")

	// Persist the OCI ExternalSecret / ExternalCertificate / ExternalClientCertificate /
	// ExternalServiceCertificate / ExpertModeSecret references onto the pool_credentials JSONB column
	if pool.PoolCredentials.ExternalSecret != nil || pool.PoolCredentials.ExternalCertificate != nil || pool.PoolCredentials.ExternalClientCertificate != nil || pool.PoolCredentials.ExternalServiceCertificate != nil || pool.PoolCredentials.ExpertModeSecret != nil {
		logOCICreatePoolInfo(logger, pool, stagePersistPoolCreds, "Persisting pool credential references to database")
		if err = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatePoolFields, pool.UUID, map[string]interface{}{
			"pool_credentials": pool.PoolCredentials,
		}).Get(dbHbCtx, nil); err != nil {
			logOCICreatePoolError(logger, pool, stagePersistPoolCreds, "Failed to persist pool credentials", err)
			emitStage(ctx, wfCreatePool, queueCustomer, stagePersistPoolCreds, resultFailure)
			return nil, workflows.ConvertToVSAError(err)
		}
		emitStage(ctx, wfCreatePool, queueCustomer, stagePersistPoolCreds, resultSuccess)
		logOCICreatePoolInfo(logger, pool, stagePersistPoolCreds, "Pool credential references persisted to database")
	}

	// When the expert-mode credential is a caller-supplied temporary password, record
	// the temp-credential state on the pool so a reconciler can detect passwords that
	// are never changed within the allowed window. This runs before the pool is marked
	// ready so a failure fails the workflow and cleanly rolls back the incomplete pool
	// (the deferred rollback is still armed here); it never overwrites credentials.
	if usedTemporaryAdminPassword {
		logOCICreatePoolInfo(logger, pool, stageRecordTempCreds, "Recording temporary admin password state on pool")
		if err = workflow.ExecuteActivity(dbHbCtx, poolActivity.RecordTempPasswordState, pool).Get(dbHbCtx, nil); err != nil {
			logOCICreatePoolError(logger, pool, stageRecordTempCreds, "Failed to record temporary admin password state", err)
			emitStage(ctx, wfCreatePool, queueCustomer, stageRecordTempCreds, resultFailure)
			return nil, workflows.ConvertToVSAError(err)
		}
		emitStage(ctx, wfCreatePool, queueCustomer, stageRecordTempCreds, resultSuccess)
		logOCICreatePoolInfo(logger, pool, stageRecordTempCreds, "Temporary admin password state recorded on pool")
	}

	// Mark pool as ready and persist VLM config for future workflows.
	logOCICreatePoolInfo(logger, pool, stageMarkReady, "Marking OCI pool as ready")
	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.CreatedPool, pool, &createVSAClusterDeploymentResponse.VLMConfig).Get(dbHbCtx, nil)
	if err != nil {
		logOCICreatePoolError(logger, pool, stageMarkReady, "Failed to mark pool as created", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageMarkReady, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	emitStage(ctx, wfCreatePool, queueCustomer, stageMarkReady, resultSuccess)
	logOCICreatePoolInfo(logger, pool, stageMarkReady, "OCI pool marked as ready")

	// Pool is ready: clear compensation so a later DB write failure does not
	// ErroredPool / tear down an already-provisioned cluster (same rationale as
	// SVM update after the ONTAP mutation succeeds).
	rollbackManager.ClearAll()

	// Persist VMRS-resolved IOPS (and customer-visible throughput) into
	// pool_attributes after create, mirroring update-path persistence via
	// UpdatedPoolWithVLMConfig / params.TotalIops. Create inserts the row
	// before VMRS runs, so omitted IOPS would otherwise stay 0 in the DB.
	if err = persistOCIVMRSResolvedAttributes(dbHbCtx, poolActivity, pool, ociDecision); err != nil {
		logOCICreatePoolError(logger, pool, stageDBPersistFinal, "Failed to persist VMRS-resolved pool attributes", err)
		emitStage(ctx, wfCreatePool, queueCustomer, stageDBPersistFinal, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	if ociDecision != nil && ociDecision.ResolvedIOPS > 0 {
		emitStage(ctx, wfCreatePool, queueCustomer, stageDBPersistFinal, resultSuccess)
	}

	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatePoolFields, pool.UUID, map[string]interface{}{
		"build_info": NewPoolBuildInfo(workflow.Now(ctx), createVSAClusterDeploymentResponse.VLMConfig, params.AccountName),
	}).Get(dbHbCtx, nil)
	if err != nil {
		logOCICreatePoolWarn(logger, pool, stageMarkReady, "Failed to persist pool build info; continuing",
			"error", err,
			"nonFatal", true,
		)
		err = nil
	}

	logOCICreatePoolInfo(logger, pool, "workflow_complete", "OCI create pool workflow completed")
	return nil, nil
}

func NewPoolBuildInfo(now time.Time, vlmCfg vlm.VLMConfig, accountName string) *datamodel.PoolBuildInfo {
	return &datamodel.PoolBuildInfo{
		VSABuildImage:      vlmCfg.Deployment.Images.VSAImageName,
		MediatorBuildImage: vlmCfg.Deployment.Images.MediatorImageName,
		OntapVersion:       utils.ExtractOntapVersion(utils.GetOntapVersionBasedOnAllowlisting(accountName)),
		BuildTimestamp:     now,
	}
}

func allocateOCIVMSerialNumbers(ctx workflow.Context, poolActivity *activities.PoolActivity, req *vlm.CreateVSAClusterDeploymentRequest) error {
	if activities.RegionNumber == "" {
		return utilserrors.NewUserInputValidationErr(
			"region number is not set; ensure LOCAL_REGION and REGION_NUMBER_MAP are configured on the OCI worker")
	}
	if ociCellNumber == "" {
		return utilserrors.NewUserInputValidationErr(
			"cell number is not set; ensure LOCAL_CELL is configured on the OCI worker as a 2-digit code")
	}
	numHAPair := req.VLMConfig.Deployment.NumHAPair
	if numHAPair < 1 {
		return utilserrors.NewUserInputValidationErr(
			fmt.Sprintf("invalid VM count for serial allocation: NumHAPair=%d (must be >= 1)", numHAPair))
	}

	prefix, err := buildOCISerialPrefix(activities.RegionNumber, ociCellNumber)
	if err != nil {
		return err
	}

	numVMs := numHAPair * activities.VMsPerHAPair
	serials := make([]string, 0, numVMs)
	for range numVMs {
		var counter int64
		if err := workflow.ExecuteActivity(ctx, poolActivity.GetNextSerialNumber).Get(ctx, &counter); err != nil {
			return err
		}
		if counter < 0 || counter >= ociSerialCounterMax {
			return vsaerrors.NewVCPError(
				vsaerrors.ErrGeneratingUniqueSerialNumber,
				fmt.Errorf("OCI serial counter %d overflows %d-digit width (max %d)", counter, ociSerialCounterWidth, ociSerialCounterMax-1),
			)
		}
		serials = append(serials, fmt.Sprintf("%s%0*d", prefix, ociSerialCounterWidth, counter))
	}

	req.VLMConfig.Deployment.SerialNumberPrefix = ""
	req.VLMConfig.Deployment.VMSerialNumbers = serials
	return nil
}

// ociDefinedTags returns the OCI defined tags map (netapp-tags with deployment_id).
func ociDefinedTags(deploymentName string) map[string]map[string]interface{} {
	definedTags := make(map[string]map[string]interface{})
	definedTags[ociDefinedTagNamespace] = map[string]interface{}{
		"deployment_id": deploymentName,
	}
	return definedTags
}

func ociDataDiskCount(isRegionalHA bool) int {
	if isRegionalHA {
		return nonSharedHADataDiskCount
	}
	return sharedHADataDiskCount
}

// ociDeploymentConfig builds the VLM DeploymentConfig for OCI (provider, deployment ID, images, OCI config, SP config, etc.).
func ociDeploymentConfig(params *common.CreatePoolParams, pool *datamodel.Pool, sizeStr string, throughputMibps, iops int64, ociConfig vlm.OCIConfig) vlm.DeploymentConfig {
	var deploymentType string
	var enableAAConfig bool
	if params.IsRegionalHA {
		deploymentType = vlm.DeploymentTypeNonSharedHA
		enableAAConfig = false
	} else {
		deploymentType = vlm.DeploymentTypeSharedHA
		enableAAConfig = true
	}

	return vlm.DeploymentConfig{
		Provider:           vlm.OCICloud,
		DeploymentID:       pool.DeploymentName,
		SerialNumberPrefix: ociSerialNumberLeadingPrefix + ociSerialNumberPrefix,
		Region:             localRegion,
		Images: vlm.ImageConfig{
			VSAImageName:      vsaImageName,
			MediatorImageName: vsaMediatorImageName,
		},
		UserBootargs: ociVSAUserBootargs,
		Labels: map[string]string{
			"pool_name":  pool.Name,
			"pool_uuid":  pool.UUID,
			"account_id": params.AccountName,
		},
		DeploymentType: deploymentType,
		NumHAPair:      int(params.HAPairs),
		// Drive the deployment-level VSA instance type from the resolved
		// OCIConfig so the VMRS-selected shape (set in prepareVLMConfig)
		// flows here as well; otherwise this field would silently keep
		// the env default and override the VMRS choice downstream.
		VSAInstanceType:      ociConfig.VSAInstanceShape,
		MediatorInstanceType: ociMediatorInstanceType,
		DataDiskCount:        ociDataDiskCount(params.IsRegionalHA),
		ProviderConfig:       vlm.ProviderConfigWrapper{ProviderConfig: ociConfig},
		SPConfig: vlm.SPConfig{
			Size:       sizeStr,
			IOps:       iops,
			Throughput: throughputMibps,
		},
		DevFlags: vlm.DevFlags{
			ExtIPForNodeMgmt: extIPForNodeMgmt,
			ProviderDevFlags: vlm.ProviderDevFlagsWrapper{
				ProviderDevFlags: vlm.OCIDevFlags{
					AllowNonDenseShapeForVsa: allowNonDenseShapeForVSA,
					UseSecondaryIPsForLIFs:   useSecondaryIPsForLIFs,
				},
			},
		},
		DeploymentConfigFlags: vlm.DeploymentConfigFlags{
			EnableAAConfig: enableAAConfig,
		},
	}
}

func prepareVLMConfig(
	params *common.CreatePoolParams,
	pool *datamodel.Pool,
	decision *vmrs_oci.Decision,
) (*vlm.VLMConfig, error) {
	if params.HAPairs == 0 {
		return nil, utilserrors.NewUserInputValidationErr(
			"haPairs must be greater than 0; OCI pool creation requires CreatePoolParams.HAPairs to be set",
		)
	}

	sizeInGIB := utils.BytesToGigabytes(params.SizeInBytes)
	sizeStr := fmt.Sprintf("%dGi", sizeInGIB)

	// Reject pools whose per-disk size would be too small for VLM. VLM
	// attaches dataDiskCount data disks to every *active* VM (DataDisks is a
	// per-VM list, see vlm.VMConfig.DataDisks) and slices the pool's total
	// size across all of them, rejecting the deployment when the resulting
	// per-disk size is strictly below OCI_VSA_MIN_PER_DISK_SIZE_GI. Shared HA
	// (AA) deployments run 2 active VMs per HA pair instead of 1 (non-shared
	// HA/AP), so they carry twice as many data disks per pair — double the
	// disk count in the divisor for shared HA or this guard under-validates
	// AA pools by 2x and lets requests through that VLM will still reject.
	// Fail fast here instead of burning provisioning work only to hit the
	// same rejection in VLM.
	// Note: both topology-specific disk counts are validated at startup in
	// ValidateOCIWorkerStartupEnv.
	// The per-disk size check uses UserInputValidationErr (4xx) since total
	// size and HA pairs are caller-supplied.
	effectiveDataDiskCount := uint64(ociDataDiskCount(params.IsRegionalHA))
	if !params.IsRegionalHA {
		effectiveDataDiskCount *= 2
	}
	perDiskSizeGi := sizeInGIB / params.HAPairs / effectiveDataDiskCount
	if perDiskSizeGi < uint64(minPerDiskSizeGi) {
		return nil, utilserrors.NewUserInputValidationErr(
			fmt.Sprintf(
				"per-disk size %d Gi (total %d Gi / %d HA pair(s) / %d data disk(s)) must be at least %d Gi",
				perDiskSizeGi, sizeInGIB, params.HAPairs, effectiveDataDiskCount, minPerDiskSizeGi,
			),
		)
	}

	definedTags := ociDefinedTags(pool.DeploymentName)

	ociConfig := vlm.OCIConfig{
		CompartmentID:   params.CompartmentOCID,
		SubnetID:        params.VendorSubNetID,
		DataNICSubnetID: params.DataNICSubnetID,
		AvailabilityDomain: vlm.AvailabilityDomainInfo{
			AvailabilityDomain1:        params.PrimaryZone,
			AvailabilityDomain2:        params.SecondaryZone,
			MediatorAvailabilityDomain: params.MediatorZone,
		},
		FaultDomain: vlm.FaultDomainInfo{
			FaultDomain1:        params.PrimaryFaultDomain,
			FaultDomain2:        params.SecondaryFaultDomain,
			MediatorFaultDomain: params.MediatorFaultDomain,
		},
		VSAInstanceShape:           ociVSAInstanceType,
		VSAFlexOcpus:               ociVSAFlexOcpus,
		VSAFlexMemoryInGBs:         ociVSAFlexMemoryInGBs,
		Creator:                    ociCreator,
		DefinedTags:                definedTags,
		CmekOcid:                   params.KmsKeyId,
		RootBackupPolicyOcid:       params.RootBackupPolicyOcid,
		BootBackupPolicyOcid:       params.BootBackupPolicyOcid,
		CustomerNSGs:               params.NsgIds,
		CustomerSecurityAttributes: params.SecurityAttributes,
	}

	if params.FabricPoolConfig != nil {
		ociConfig.FabricPoolConfig = vlm.FabricPoolConfig{
			BucketName:    params.FabricPoolConfig.BucketName,
			SecretOcid:    params.FabricPoolConfig.SecretOcid,
			SecretVersion: params.FabricPoolConfig.SecretVersion,
			Namespace:     params.FabricPoolConfig.Namespace,
			ServerURL:     params.FabricPoolConfig.ServerURL,
		}
	}

	// Shape overrides: from VMRS decision when provided (VMRS enabled).
	if decision != nil {
		ociConfig.VSAInstanceShape = decision.VMShape
		ociConfig.VSAFlexOcpus = float32(decision.OCPUs)
		ociConfig.VSAFlexMemoryInGBs = float32(decision.MemoryGBs)
		vpu := int64(decision.VPU)
		ociConfig.DataDiskVpus = &vpu
	}

	// Throughput and IOPS:
	//   VMRS on  → DecideOCIVMRS resolved customer-visible throughput (MB/s) and IOPS.
	//   VMRS off → send CustomPerformanceParams.ThroughputMibps as MB/s unchanged.
	var throughputForVLM, iops int64
	if decision != nil {
		throughputForVLM = int64(decision.Throughput)
		iops = decision.ResolvedIOPS
	} else if params.CustomPerformanceParams != nil {
		throughputForVLM = params.CustomPerformanceParams.ThroughputMibps
		if params.CustomPerformanceParams.Iops != nil {
			iops = *params.CustomPerformanceParams.Iops
		}
	}

	deployment := ociDeploymentConfig(params, pool, sizeStr, throughputForVLM, iops, ociConfig)
	return &vlm.VLMConfig{Deployment: deployment}, nil
}

// prepareCreateVSAClusterDeploymentRequest prepares the CreateVSAClusterDeploymentRequest for OCI
func prepareCreateVSAClusterDeploymentRequest(createVSAClusterDeploymentRequest *vlm.CreateVSAClusterDeploymentRequest, vlmConfig vlm.VLMConfig, ontapCredentials vlm.OntapCredentials, pool *datamodel.Pool) {
	// Ensure labels are set
	if vlmConfig.Deployment.Labels == nil {
		vlmConfig.Deployment.Labels = make(map[string]string)
	}
	vlmConfig.Deployment.Labels["pool_name"] = pool.Name
	vlmConfig.Deployment.Labels["pool_ocid"] = pool.PoolExternalIdentifier
	if pool.Account != nil {
		vlmConfig.Deployment.Labels["account_id"] = pool.Account.Name
	}

	// Set images (already set in prepareVLMConfig, but ensure they're correct)
	vlmConfig.Deployment.Images.VSAImageName = vsaImageName
	vlmConfig.Deployment.Images.MediatorImageName = vsaMediatorImageName

	createVSAClusterDeploymentRequest.VLMConfig = vlmConfig
	createVSAClusterDeploymentRequest.OntapCredentials = ontapCredentials

	secretUris := []string{secretURI}
	if strings.TrimSpace(smcLicenseSecretURI) != "" {
		secretUris = append(secretUris, smcLicenseSecretURI)
	}
	createVSAClusterDeploymentRequest.OntapLicense = vlm.OntapLicense{
		SecretUri: secretUris,
	}
}

func prepareOCIDeleteVSAClusterDeploymentRequest(req *vlm.DeleteVSAClusterDeploymentRequest, pool *datamodel.Pool, tenancyOCID string) {
	req.CloudProvider = vlm.OCICloud
	req.DeploymentID = pool.DeploymentName
	req.ProjectID = tenancyOCID
	req.ProviderConfig = vlm.ProviderConfigWrapper{
		ProviderConfig: &vlm.OCIConfig{
			CompartmentID: pool.ClusterDetails.CompartmentOCID,
			DefinedTags:   ociDefinedTags(pool.DeploymentName),
		},
	}
}

type ociDeletePoolWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &ociDeletePoolWorkflow{}

// OCIDeletePoolWorkflow processes pool deletion requests for OCI.
func OCIDeletePoolWorkflow(ctx workflow.Context, params *common.DeletePoolParams, pool *datamodel.Pool) error {
	start := workflow.Now(ctx)
	deletePoolWF := new(ociDeletePoolWorkflow)
	log := util.GetLogger(ctx)
	err := deletePoolWF.Setup(ctx, params)
	if err != nil {
		emitDuration(ctx, wfDeletePool, queueCustomer, start)
		return err
	}

	deletePoolWF.Status = workflows.WorkflowStatusRunning
	_, errRun := deletePoolWF.Run(ctx, params, pool)
	if errRun != nil {
		log.Errorf("error in ociDeletePoolWorkflow: %v", errRun)
		deletePoolWF.Status = workflows.WorkflowStatusFailed
		emitDuration(ctx, wfDeletePool, queueCustomer, start)
		return errRun
	}
	deletePoolWF.Status = workflows.WorkflowStatusCompleted
	emitDuration(ctx, wfDeletePool, queueCustomer, start)
	return nil
}

func (wf *ociDeletePoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deletePoolParams := input.(*common.DeletePoolParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deletePoolParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ociDeletePoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	if len(args) < 2 {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIDeletePoolWorkflow.Run: expected 2 args, got %d", len(args)))
	}
	params, ok := args[0].(*common.DeletePoolParams)
	if !ok {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIDeletePoolWorkflow.Run: args[0] has unexpected type %T, want *common.DeletePoolParams", args[0]))
	}
	if params == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCIDeletePoolWorkflow.Run: args[0] (*common.DeletePoolParams) must not be nil")))
	}
	pool, ok := args[1].(*datamodel.Pool)
	if !ok {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIDeletePoolWorkflow.Run: args[1] has unexpected type %T, want *datamodel.Pool", args[1]))
	}
	if pool == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("OCIDeletePoolWorkflow.Run: args[1] (*datamodel.Pool) must not be nil")))
	}

	// Set up activity options
	poolActivity := &activities.PoolActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
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
	dbHbCtx := workflow.WithActivityOptions(ctx, ao)
	dbHbCtx = workflow.WithHeartbeatTimeout(dbHbCtx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)
	hyperscalerCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: utils.GetStartToCloseTimeoutHyperscaler(),
		HeartbeatTimeout:    utils.GetHeartbeatTimeoutForHyperscaler(),
		RetryPolicy:         utils.GetHyperscalerLRORetryPolicy(),
	})

	rollbackManager := common.NewRollbackManager()
	rollbackManager.AddActivity(poolActivity.ErroredPool, pool)
	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackCtx := workflow.WithActivityOptions(disconnectedCtx, ao)
			rollbackManager.ExecuteRollback(rollbackCtx, err)
		}
	}()

	// Call DeleteVSAClusterDeployment
	vsaClientWorkflowManager := workflows.GetNewVSAClientWorkflowManager()
	deleteRequest := &vlm.DeleteVSAClusterDeploymentRequest{}
	prepareOCIDeleteVSAClusterDeploymentRequest(deleteRequest, pool, params.AccountName)
	ontapVersion := workflows.ResolveVLMWorkerVersion(pool)
	if ontapVersion == "" {
		err = vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
			fmt.Errorf("could not resolve a VLM worker version to route the OCI pool delete; buildInfo=%+v", pool.BuildInfo))
		wf.Logger.Error("OCI pool delete stopped: could not resolve a VLM worker version",
			"pool", pool.Name, "poolOCID", pool.PoolExternalIdentifier, "buildInfo", pool.BuildInfo, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}
	err = vsaClientWorkflowManager.DeleteVSAClusterDeployment(hyperscalerCtx, deleteRequest, ontapVersion)
	if err != nil {
		emitStage(ctx, wfDeletePool, queueCustomer, stageVLMDelete, resultFailure)
		return nil, handleActivityError(wf.Logger, err, "DeleteVSAClusterDeployment", pool.Name)
	}
	emitStage(ctx, wfDeletePool, queueCustomer, stageVLMDelete, resultSuccess)

	if pool.DeploymentName != "" && (!disableVsaCleanupOnVLMFailure || pool.State != datamodel.LifeCycleStateError) {
		err = workflow.ExecuteActivity(hyperscalerCtx, poolActivity.DeleteOnTapCredentialsForOCI, pool).Get(hyperscalerCtx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Clean up DNS records for cert-auth pools in oci
	if pool.PoolCredentials != nil && pool.PoolCredentials.AuthType == env.USER_CERTIFICATE {
		hostMap := make(map[string]string)
		err = workflow.ExecuteActivity(hyperscalerCtx, poolActivity.GetCloudDNSRecords, pool.ID, pool.PoolCredentials.AuthType).Get(hyperscalerCtx, &hostMap)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		err = workflow.ExecuteActivity(hyperscalerCtx, poolActivity.OCIDeleteCloudDNSRecords, hostMap, pool.PoolCredentials.AuthType).Get(hyperscalerCtx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Mark pool as deleted
	err = workflow.ExecuteActivity(dbHbCtx, poolActivity.DeletePoolResources, pool).Get(dbHbCtx, nil)
	if err != nil {
		emitStage(ctx, wfDeletePool, queueCustomer, stageDBCleanup, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	emitStage(ctx, wfDeletePool, queueCustomer, stageDBCleanup, resultSuccess)

	return nil, nil
}

type ociUpdatePoolWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &ociUpdatePoolWorkflow{}

// OCIUpdatePoolWorkflow processes pool update requests for OCI.
func OCIUpdatePoolWorkflow(ctx workflow.Context, params *common.UpdatePoolParams, pool *datamodel.Pool) error {
	start := workflow.Now(ctx)
	updatePoolWF := new(ociUpdatePoolWorkflow)
	logger := util.GetLogger(ctx)
	err := updatePoolWF.Setup(ctx, params)
	if err != nil {
		emitDuration(ctx, wfUpdatePool, queueCustomer, start)
		return err
	}

	updatePoolWF.Status = workflows.WorkflowStatusRunning
	_, errRun := updatePoolWF.Run(ctx, params, pool)
	if errRun != nil {
		logger.Error("ociUpdatePoolWorkflow failed", "error", errRun)
		updatePoolWF.Status = workflows.WorkflowStatusFailed
		emitDuration(ctx, wfUpdatePool, queueCustomer, start)
		return errRun
	}
	updatePoolWF.Status = workflows.WorkflowStatusCompleted
	emitDuration(ctx, wfUpdatePool, queueCustomer, start)
	return nil
}

func (wf *ociUpdatePoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	updatePoolParams, ok := input.(*common.UpdatePoolParams)
	if !ok {
		return fmt.Errorf("invalid input type: expected *UpdatePoolParams, got %T", input)
	}
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = updatePoolParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ociUpdatePoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params, pool, argErr := extractOCIUpdatePoolArgs(args)
	if argErr != nil {
		return nil, workflows.ConvertToVSAError(argErr)
	}

	logger := util.GetLogger(ctx)
	logger.Info("OCI Update Pool Workflow Run method called",
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"deploymentName", pool.DeploymentName,
		"account", params.AccountName,
		"requestedSizeBytes", params.SizeInBytes,
		"requestedThroughputMibps", params.TotalThroughputMibps,
		"nodeCapacitiesCount", len(params.NodeCapacities),
		"kmsKeyId", params.KmsKeyId,
	)

	ao, err := buildOCIUpdatePoolActivityOptions()
	if err != nil {
		logger.Error("Failed to populate retry policy params", "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	poolActivity := &activities.PoolActivity{}
	rollbackManager := common.NewRollbackManager()
	rollbackManager.AddActivity(poolActivity.ErroredPool, pool)
	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackCtx := workflow.WithActivityOptions(disconnectedCtx, ao)
			rollbackManager.ExecuteRollback(rollbackCtx, err)
		}
	}()

	dbHbCtx := workflow.WithActivityOptions(ctx, ao)
	dbHbCtx = workflow.WithHeartbeatTimeout(dbHbCtx, time.Duration(dbHeartbeatTimeoutSec)*time.Second)

	currentVlmConfig, err := parseStoredVLMConfig(dbHbCtx, poolActivity, pool)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	if len(currentVlmConfig.Cloud.HAPairs) == 0 {
		logger.Error("Stored VLM config has empty cloud.ha_pair for OCI pool update",
			"pool", pool.Name,
			"poolUUID", pool.UUID,
		)
		err = utilserrors.NewUserInputValidationErr(
			fmt.Sprintf("stored VLM config for pool %q has empty cloud.ha_pair; cannot proceed with OCI pool update", pool.UUID))
		return nil, workflows.ConvertToVSAError(err)
	}
	currentNumHAPairs := len(currentVlmConfig.Cloud.HAPairs)
	targetNumHAPairs := currentNumHAPairs
	if int(params.HAPairs) > currentNumHAPairs {
		targetNumHAPairs = int(params.HAPairs)
	}
	if targetNumHAPairs != currentNumHAPairs {
		logger.Info("OCI pool update: HA pair scale requested",
			"pool", pool.Name,
			"poolUUID", pool.UUID,
			"currentHAPairs", currentNumHAPairs,
			"targetHAPairs", targetNumHAPairs,
		)
	}

	ociDecision, err := runOCIVMRSForUpdate(ctx, poolActivity, params, pool, currentVlmConfig)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Build the target SPConfig for VLM (empty when no perf/capacity field
	// changes). VMRS on → decision carries headroom + resolved IOPS; VMRS off
	// → raw API values are sent unchanged.
	targetSPConfig, err := buildOCIUpdateSPConfig(ociDecision, params, pool, currentVlmConfig)
	if err != nil {
		logger.Error("Failed to build target SPConfig for OCI pool update", "error", err, "pool", pool.Name)
		return nil, workflows.ConvertToVSAError(err)
	}
	if targetSPConfig.IOps > 0 {
		resolvedIOPS := targetSPConfig.IOps
		params.TotalIops = &resolvedIOPS
	}
	logger.Info("Resolved target SPConfig for OCI pool update",
		"pool", pool.Name,
		"targetSize", targetSPConfig.Size,
		"targetThroughput", targetSPConfig.Throughput,
		"targetIOps", targetSPConfig.IOps,
		"targetNumHAPairs", targetNumHAPairs,
	)

	credConfig := &vlm.OntapCredentials{}
	credCtx := workflow.WithActivityOptions(ctx, ao)
	if err = workflow.ExecuteActivity(credCtx, poolActivity.GetOnTapCredentialsForOCI, pool).Get(credCtx, credConfig); err != nil {
		logger.Error("Failed to fetch ONTAP credentials from OCI Vault", "error", err, "pool", pool.Name)
		return nil, workflows.ConvertToVSAError(err)
	}

	// TODO: The batch planner iterates every HA pair, including pairs whose target
	// size already equals their current size, which can produce wasted no-op VLM
	// calls. A future optimization can diff params.NodeCapacities against the
	// current node state and batch only the pairs that actually change. For now we
	// intentionally pass all HA pairs to VLM. This will be taken care of post GA.
	logger.Info("OCI pool update: batching all HA pairs with pool-level SPConfig",
		"pool", pool.Name,
		"totalPairsInCluster", targetNumHAPairs,
		"nodeCapacitiesCount", len(params.NodeCapacities),
	)

	batchPlan, err := calculateOCIUpdatePoolBatchPlan(dbHbCtx, poolActivity, pool, targetNumHAPairs, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	rollbackManager.AddActivity(poolActivity.RestorePoolPreUpdatePoolLevelFields, pool.UUID, pool)
	logger.Info("Registered DB compensation for pre-update pool fields and vlm_config",
		"pool", pool.Name,
		"poolUUID", pool.UUID,
	)

	vsaClientWorkflowManager := workflows.GetNewVSAClientWorkflowManager()

	ontapVersion := workflows.ResolveVLMWorkerVersion(pool)
	if ontapVersion == "" {
		err = vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError,
			fmt.Errorf("could not resolve a VLM worker version to route the OCI pool update; buildInfo=%+v", pool.BuildInfo))
		logger.Error("OCI pool update stopped: could not resolve a VLM worker version",
			"error", err,
			"pool", pool.Name,
			"poolOCID", pool.PoolExternalIdentifier,
			"buildInfo", pool.BuildInfo,
		)
		return nil, workflows.ConvertToVSAError(err)
	}
	logger.Info("Starting batched VLM update for OCI pool",
		"pool", pool.Name,
		"ontapVersion", ontapVersion,
		"numWorkflowCalls", batchPlan.NumWorkflowCalls,
	)
	updateResult, err := executeOCIUpdatePoolVLMInBatches(
		ctx, pool, batchPlan,
		currentVlmConfig, targetSPConfig, targetNumHAPairs,
		*credConfig, ontapVersion, vsaClientWorkflowManager,
		ociDecision,
		params,
	)
	if err != nil {
		logger.Error("Batched OCI pool update failed", "error", err, "pool", pool.Name)
		return nil, workflows.ConvertToVSAError(err)
	}
	if updateResult.ITCRecoverySucceeded {
		logger.Info("ITC recovery completed: VLM rolled the cluster back to its pre-update shape; the requested pool update was NOT applied",
			"pool", pool.Name,
			"poolUUID", pool.UUID,
			"deploymentName", pool.DeploymentName,
		)
		emitStage(ctx, wfUpdatePool, queueCustomer, stageVLMUpdate, resultFailure)
		if err = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdatedPool, pool).Get(dbHbCtx, nil); err != nil {
			logger.Error("Failed to transition pool back to READY after ITC recovery", "error", err, "pool", pool.Name)
			return nil, workflows.ConvertToVSAError(err)
		}
		logger.Info("Pool transitioned back to READY after ITC recovery; original update aborted",
			"pool", pool.Name,
			"poolUUID", pool.UUID,
		)
		return nil, nil
	}
	updateResp := updateResult.Response
	emitStage(ctx, wfUpdatePool, queueCustomer, stageVLMUpdate, resultSuccess)
	logger.Info("Batched VLM update completed for OCI pool",
		"pool", pool.Name,
		"numWorkflowCalls", batchPlan.NumWorkflowCalls,
	)

	if err = persistOCIPoolUpdate(dbHbCtx, poolActivity, pool, params, updateResp.VLMConfig); err != nil {
		logger.Error("Failed to persist updated pool", "error", err, "pool", pool.Name)
		emitStage(ctx, wfUpdatePool, queueCustomer, stageDBPersistFinal, resultFailure)
		return nil, workflows.ConvertToVSAError(err)
	}
	emitStage(ctx, wfUpdatePool, queueCustomer, stageDBPersistFinal, resultSuccess)

	commonActivity := &activities.CommonActivities{}
	var preUpdateNodes []*datamodel.Node
	if err = workflow.ExecuteActivity(dbHbCtx, commonActivity.GetNode, pool.ID).Get(dbHbCtx, &preUpdateNodes); err != nil {
		logger.Error("Failed to fetch nodes for OCI pool update node rollback snapshot", "error", err, "pool", pool.Name)
		return nil, workflows.ConvertToVSAError(err)
	}
	preUpdateNodeSnapshot := buildOCINodeAttributesSnapshot(preUpdateNodes)
	rollbackManager.AddActivity(poolActivity.RestoreOCINodesPreUpdateFields, pool.ID, preUpdateNodeSnapshot)

	if err = workflow.ExecuteActivity(dbHbCtx, poolActivity.UpdateOCINodesFromVLMConfig, pool.ID, updateResp.VLMConfig).Get(dbHbCtx, nil); err != nil {
		logger.Error("Failed to persist node size and instance type after OCI pool update", "error", err, "pool", pool.Name)
		return nil, workflows.ConvertToVSAError(err)
	}

	logger.Info("OCI Update Pool Workflow completed successfully",
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"deploymentName", pool.DeploymentName,
	)
	return nil, nil
}

func extractOCIUpdatePoolArgs(args []interface{}) (*common.UpdatePoolParams, *datamodel.Pool, error) {
	if len(args) < 2 {
		return nil, nil, fmt.Errorf("OCIUpdatePoolWorkflow.Run: expected 2 args, got %d", len(args))
	}
	params, ok := args[0].(*common.UpdatePoolParams)
	if !ok {
		return nil, nil, fmt.Errorf("OCIUpdatePoolWorkflow.Run: args[0] has unexpected type %T, want *common.UpdatePoolParams", args[0])
	}
	if params == nil {
		return nil, nil, vsaerrors.NewVCPError(
			vsaerrors.ErrResourceEmptyError,
			fmt.Errorf("OCIUpdatePoolWorkflow.Run: args[0] (*common.UpdatePoolParams) must not be nil"),
		)
	}
	pool, ok := args[1].(*datamodel.Pool)
	if !ok {
		return nil, nil, fmt.Errorf("OCIUpdatePoolWorkflow.Run: args[1] has unexpected type %T, want *datamodel.Pool", args[1])
	}
	if pool == nil {
		return nil, nil, vsaerrors.NewVCPError(
			vsaerrors.ErrResourceEmptyError,
			fmt.Errorf("OCIUpdatePoolWorkflow.Run: args[1] (*datamodel.Pool) must not be nil"),
		)
	}
	return params, pool, nil
}

func buildOCIUpdatePoolActivityOptions() (workflow.ActivityOptions, error) {
	rp, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return workflow.ActivityOptions{}, err
	}
	return workflow.ActivityOptions{
		StartToCloseTimeout: rp.StartToCloseTimeout,
		HeartbeatTimeout:    rp.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        rp.InitialInterval,
			BackoffCoefficient:     rp.BackoffCoefficient,
			MaximumInterval:        rp.MaximumInterval,
			MaximumAttempts:        int32(rp.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}, nil
}

func parseStoredVLMConfig(ctx workflow.Context, poolActivity *activities.PoolActivity, pool *datamodel.Pool) (vlm.VLMConfig, error) {
	logger := util.GetLogger(ctx)
	var raw *vlm.VLMConfig
	if err := workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, pool).Get(ctx, &raw); err != nil {
		logger.Error("Failed to parse stored VLM config", "error", err)
		return vlm.VLMConfig{}, err
	}
	cfg := *raw
	logger.Info("Parsed stored VLM config for OCI pool update",
		"pool", pool.Name,
		"numHAPairs", len(cfg.Cloud.HAPairs),
		"currentInstanceType", cfg.Deployment.VSAInstanceType,
		"currentPoolSize", cfg.Deployment.SPConfig.Size,
		"currentThroughput", cfg.Deployment.SPConfig.Throughput,
		"currentIOps", cfg.Deployment.SPConfig.IOps,
	)
	return cfg, nil
}

func runOCIVMRSForUpdate(
	ctx workflow.Context,
	poolActivity *activities.PoolActivity,
	params *common.UpdatePoolParams,
	pool *datamodel.Pool,
	currentVlmConfig vlm.VLMConfig,
) (*vmrs_oci.Decision, error) {
	logger := util.GetLogger(ctx)
	var enabled bool
	if err := workflow.SideEffect(ctx, func(_ workflow.Context) interface{} {
		return ociVMRSEnabled
	}).Get(&enabled); err != nil {
		logger.Error("Failed to read OCI VMRS toggle via SideEffect, check if OCI VMRS is enabled", "error", err)
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	// Metadata-only updates (kms/nsg/security attributes) carry no
	// capacity/throughput/IOPS change, so there is nothing for the selector
	// to resize — skip the VMRS activity (and its catalogue load) entirely.
	if !ociUpdateNeedsSPConfig(params) {
		return nil, nil
	}

	vmrsCtx := workflow.WithActivityOptions(ctx, catalogActivityOptions)
	req := newOCIUpdateVMRSRequest(params, pool, currentVlmConfig)
	var decision *vmrs_oci.Decision
	if err := workflow.ExecuteActivity(vmrsCtx, poolActivity.DecideOCIVMRS, req).Get(vmrsCtx, &decision); err != nil {
		logger.Error("OCI VMRS selection failed for pool update", "poolUUID", pool.UUID, "error", err)
		return nil, err
	}
	logger.Info("OCI VMRS decision for pool update",
		"poolUUID", pool.UUID,
		"shape", decision.VMShape,
		"ocpus", decision.OCPUs,
		"memoryGBs", decision.MemoryGBs,
		"throughputWithHeadroom", decision.ThroughputWithHeadroom,
		"throughput", decision.Throughput,
		"resolvedIOPS", decision.ResolvedIOPS,
	)
	return decision, nil
}

// newOCICreateVMRSRequest builds the DecideOCIVMRS request for a pool create.
// Current is nil (no live spec); Target is the requested pool spec.
func newOCICreateVMRSRequest(params *common.CreatePoolParams, pool *datamodel.Pool) activities.DecideOCIVMRSRequest {
	var throughputMibps int64
	var requestedIops *int64
	if params.CustomPerformanceParams != nil {
		throughputMibps = params.CustomPerformanceParams.ThroughputMibps
		requestedIops = params.CustomPerformanceParams.Iops
	}
	return activities.DecideOCIVMRSRequest{
		PoolUUID: pool.UUID,
		Target: activities.PoolVMRSSpec{
			SizeInBytes:     params.SizeInBytes,
			ThroughputMibps: throughputMibps,
			Iops:            requestedIops,
			HAPairs:         int(params.HAPairs),
			IsRegionalHA:    params.IsRegionalHA,
		},
	}
}

// iopsPtr returns a pointer to v when v > 0, or nil when v is zero
// (zero means "not set" in the stored VLM config).
func iopsPtr(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// newOCIUpdateVMRSRequest builds the DecideOCIVMRS request for a pool update
// from the request params, the pool's live state, and the stored VLM config.
// Unset target fields (size/throughput) fall back to the pool's current values
// so the selector always sees a complete target spec.
func newOCIUpdateVMRSRequest(
	params *common.UpdatePoolParams,
	pool *datamodel.Pool,
	currentVlmConfig vlm.VLMConfig,
) activities.DecideOCIVMRSRequest {
	currentHAPairs := len(currentVlmConfig.Cloud.HAPairs)
	targetHAPairs := currentHAPairs
	if int(params.HAPairs) > targetHAPairs {
		targetHAPairs = int(params.HAPairs)
	}

	targetSize := params.SizeInBytes
	if targetSize == 0 {
		targetSize = uint64(pool.SizeInBytes)
	}

	var currentThroughput int64
	var isRegionalHA bool
	var currentPoolIops int64
	if pool.PoolAttributes != nil {
		currentThroughput = pool.PoolAttributes.ThroughputMibps
		isRegionalHA = pool.PoolAttributes.IsRegionalHA
		currentPoolIops = pool.PoolAttributes.Iops
	}
	targetIops := params.TotalIops
	if targetIops == nil {
		targetIops = iopsPtr(currentPoolIops)
	}
	targetThroughput := params.TotalThroughputMibps
	if targetThroughput == 0 {
		targetThroughput = currentThroughput
	}

	return activities.DecideOCIVMRSRequest{
		PoolUUID: pool.UUID,
		Current: &activities.PoolVMRSSpec{
			SizeInBytes:     uint64(pool.SizeInBytes),
			ThroughputMibps: currentThroughput,
			Iops:            iopsPtr(currentVlmConfig.Deployment.SPConfig.IOps),
			HAPairs:         currentHAPairs,
			IsRegionalHA:    isRegionalHA,
		},
		Target: activities.PoolVMRSSpec{
			SizeInBytes:     targetSize,
			ThroughputMibps: targetThroughput,
			Iops:            targetIops,
			HAPairs:         targetHAPairs,
			IsRegionalHA:    isRegionalHA,
		},
	}
}

// buildOCIUpdateSPConfig builds the VLM SPConfig for a pool update.
//   - Returns the zero SPConfig when the update changes no perf/capacity field.
//   - VMRS on (decision != nil): customer-visible throughput (MB/s) and resolved IOPS from the decision.
//   - VMRS off (decision == nil): TotalThroughputMibps as MB/s, unchanged.
func buildOCIUpdateSPConfig(
	decision *vmrs_oci.Decision,
	params *common.UpdatePoolParams,
	pool *datamodel.Pool,
	currentVlmConfig vlm.VLMConfig,
) (vlm.SPConfig, error) {
	if !ociUpdateNeedsSPConfig(params) {
		return vlm.SPConfig{}, nil
	}

	sizeInGB := utils.BytesToGigabytes(params.SizeInBytes)
	if sizeInGB == 0 {
		sizeInGB = utils.BytesToGigabytes(uint64(pool.SizeInBytes))
	}
	sizeStr := fmt.Sprintf("%dGi", sizeInGB)

	var target vlm.SPConfig
	if decision != nil {
		target = vlm.SPConfig{
			Size:       sizeStr,
			Throughput: int64(decision.Throughput),
			IOps:       decision.ResolvedIOPS,
		}
	} else {
		throughput := params.TotalThroughputMibps
		if throughput == 0 && pool.PoolAttributes != nil {
			throughput = pool.PoolAttributes.ThroughputMibps
		}
		var iops int64
		if params.TotalIops != nil {
			iops = *params.TotalIops
		}
		target = vlm.SPConfig{
			Size:       sizeStr,
			Throughput: throughput,
			IOps:       iops,
		}
	}

	haPairConfigs, heterogeneous, err := buildOCIUpdateHAPairConfigs(params, pool, currentVlmConfig)
	if err != nil {
		return vlm.SPConfig{}, err
	}
	if heterogeneous {
		target.IsHeterogeneous = true
		target.HAPairConfigs = haPairConfigs
	}
	return target, nil
}

// buildOCIUpdateHAPairConfigs maps the requested per-node capacities onto the
// stored VLM HA-pair topology and returns the per-pair aggregate targets.
//
// OCI shared HA runs active-active, so each node in an HA pair owns its own
// aggregate and a pair carries one AggrConfig per node, ordered VM1 then VM2.
// Both nodes in a pair must request the same capacity; heterogeneity is
// supported between HA pairs only.
// VLM rejects the update outright when the requested aggregate count for a pair
// does not match the cluster's current layout, so the count is checked here.
// IOPS and throughput stay pool-level: VLM distributes them across aggregates.
func buildOCIUpdateHAPairConfigs(
	params *common.UpdatePoolParams,
	pool *datamodel.Pool,
	currentVlmConfig vlm.VLMConfig,
) ([]vlm.SPHAPairConfig, bool, error) {
	if len(params.NodeCapacities) == 0 {
		return nil, false, nil
	}

	var firstSize int64
	heterogeneous := false
	for i, nodeCapacity := range params.NodeCapacities {
		if i == 0 {
			firstSize = nodeCapacity.SizeInGiB
		} else if nodeCapacity.SizeInGiB != firstSize {
			heterogeneous = true
		}
	}
	if !heterogeneous {
		return nil, false, nil
	}
	if pool.PoolAttributes != nil && pool.PoolAttributes.IsRegionalHA {
		return nil, false, utilserrors.NewUserInputValidationErr(
			fmt.Sprintf("heterogeneous nodeCapacities are not supported for non-shared HA pool %q", pool.PoolExternalIdentifier))
	}
	if len(currentVlmConfig.Cloud.HAPairs) == 0 {
		return nil, false, utilserrors.NewUserInputValidationErr(
			fmt.Sprintf("stored VLM config for pool %q has no HA-pair topology", pool.PoolExternalIdentifier))
	}

	sizeByNodeName := make(map[string]int64, len(params.NodeCapacities))
	for i, nodeCapacity := range params.NodeCapacities {
		nodeName := strings.TrimSpace(nodeCapacity.Name)
		if nodeName == "" {
			return nil, false, utilserrors.NewUserInputValidationErr(
				fmt.Sprintf("nodeCapacities[%d].name is required to build heterogeneous HA-pair capacity", i))
		}
		sizeByNodeName[nodeName] = nodeCapacity.SizeInGiB
	}

	haPairConfigs := make([]vlm.SPHAPairConfig, 0, len(currentVlmConfig.Cloud.HAPairs))
	for pairIndex, haPair := range currentVlmConfig.Cloud.HAPairs {
		// HA pairs are reported to callers as ha_pair-<1-based index>, so label
		// them the same way here.
		haPairLabel := fmt.Sprintf("ha_pair-%d", pairIndex+1)
		vm1Name := strings.TrimSpace(haPair.VM1.HostName)
		vm2Name := strings.TrimSpace(haPair.VM2.HostName)
		vm1Size, vm1OK := sizeByNodeName[vm1Name]
		vm2Size, vm2OK := sizeByNodeName[vm2Name]
		if !vm1OK || !vm2OK {
			return nil, false, utilserrors.NewUserInputValidationErr(
				fmt.Sprintf("nodeCapacities do not match stored VLM config %s node names %q and %q",
					haPairLabel, vm1Name, vm2Name))
		}
		if vm1Size != vm2Size {
			return nil, false, utilserrors.NewUserInputValidationErr(
				fmt.Sprintf("nodeCapacities: both nodes in %s of pool %q must have the same sizeInGiB: node %q requested %d GiB, node %q requested %d GiB",
					haPairLabel, pool.PoolExternalIdentifier, vm1Name, vm1Size, vm2Name, vm2Size))
		}
		instanceType := currentVlmConfig.Deployment.VSAInstanceType
		var currentAggrConfigs []vlm.AggrConfig
		if pairIndex < len(currentVlmConfig.Deployment.SPConfig.HAPairConfigs) {
			currentPairConfig := currentVlmConfig.Deployment.SPConfig.HAPairConfigs[pairIndex]
			currentAggrConfigs = currentPairConfig.AggrConfigs
			if currentPairConfig.InstanceType != "" {
				instanceType = currentPairConfig.InstanceType
			}
		}
		if len(currentAggrConfigs) != ociSharedHAAggrsPerPair {
			return nil, false, utilserrors.NewUserInputValidationErr(
				fmt.Sprintf("pool %q %s has %d aggregate(s) in its stored VLM config; per-node capacity requires %d",
					pool.PoolExternalIdentifier, haPairLabel, len(currentAggrConfigs), ociSharedHAAggrsPerPair))
		}

		haPairConfigs = append(haPairConfigs, vlm.SPHAPairConfig{
			InstanceType: instanceType,
			AggrConfigs: []vlm.AggrConfig{
				ociUpdateAggrConfig(currentAggrConfigs[0], vm1Size),
				ociUpdateAggrConfig(currentAggrConfigs[1], vm2Size),
			},
		})
	}
	return haPairConfigs, true, nil
}

// ociUpdateAggrConfig builds the target aggregate for one node by carrying the
// stored aggregate forward and overriding only its size. Keeping the existing
// aggr_name lets VLM resize the aggregate it already owns rather than matching
// on slice position, and keeping the existing iops/throughput leaves the
// per-aggregate performance split to VLM — VCP only sets the pool-level totals.
// VLM rejects an AggrConfig with no IOPS, so these fields cannot be omitted.
func ociUpdateAggrConfig(current vlm.AggrConfig, sizeInGiB int64) vlm.AggrConfig {
	target := current
	target.Size = fmt.Sprintf("%dGi", sizeInGiB)
	return target
}

func validateStoredVLMConfigForUpdate(pool *datamodel.Pool, currentVlmConfig vlm.VLMConfig) error {
	if len(currentVlmConfig.Cloud.HAPairs) == 0 {
		return vsaerrors.NewVCPError(
			vsaerrors.ErrIncorrectVSAClusterState,
			fmt.Errorf(
				"VLM update requires non-empty cloud.ha_pair in stored pool VLM config; pool %q has none "+
					"(persist full post-create VLMConfig including ha_pair, or recreate the pool)",
				pool.Name,
			),
		)
	}
	return nil
}

func buildOCIOntapCredentials(pool *datamodel.Pool) (vlm.OntapCredentials, error) {
	if pool.PoolCredentials == nil {
		return vlm.OntapCredentials{}, vsaerrors.NewVCPError(
			vsaerrors.ErrResourceEmptyError,
			fmt.Errorf("pool credentials are required for ONTAP authentication during pool update %q", pool.Name),
		)
	}
	return vlm.OntapCredentials{
		AdminPassword: pool.PoolCredentials.Password,
		Certificate:   vlm.OntapCertificate{},
	}, nil
}

func calculateOCIUpdatePoolBatchPlan(
	ctx workflow.Context,
	poolActivity *activities.PoolActivity,
	pool *datamodel.Pool,
	numHAPairs int,
	haPairIndices []int,
) (*activities.CalculateBatchPlanActivityOutput, error) {
	logger := util.GetLogger(ctx)
	in := &activities.CalculateBatchPlanActivityInput{
		NumHAPairs:                  numHAPairs,
		HAPairIndices:               haPairIndices,
		ParallelNumberOfNodesForITC: parallelNumberOfNodesForITCOCI,
	}
	var out *activities.CalculateBatchPlanActivityOutput
	if err := workflow.ExecuteActivity(ctx, poolActivity.CalculateBatchPlanForUpdate, in).Get(ctx, &out); err != nil {
		logger.Error("Failed to calculate OCI pool update batch plan", "error", err)
		return nil, err
	}
	logger.Info("Calculated OCI pool update batch plan",
		"pool", pool.Name,
		"numHAPairs", out.NumHAPairs,
		"batchSize", out.BatchSize,
		"numWorkflowCalls", out.NumWorkflowCalls,
		"batchIndices", out.BatchIndices,
		"parallelNumberOfNodesForITC", parallelNumberOfNodesForITCOCI,
	)
	return out, nil
}

func buildOCINodeAttributesSnapshot(nodes []*datamodel.Node) map[string]datamodel.NodeDetails {
	snapshot := make(map[string]datamodel.NodeDetails, len(nodes))
	for _, node := range nodes {
		if node == nil || node.Name == "" {
			continue
		}
		if node.NodeAttributes != nil {
			snapshot[node.Name] = *node.NodeAttributes
			continue
		}
		snapshot[node.Name] = datamodel.NodeDetails{}
	}
	return snapshot
}

// persistOCIVMRSResolvedAttributes writes VMRS-resolved customer-visible
// performance into pool_attributes after the pool is marked ready.
// Mirrors the update path where decision.ResolvedIOPS is assigned to
// params.TotalIops and persisted via UpdatedPoolWithVLMConfig.
func persistOCIVMRSResolvedAttributes(
	ctx workflow.Context,
	poolActivity *activities.PoolActivity,
	pool *datamodel.Pool,
	decision *vmrs_oci.Decision,
) error {
	if decision == nil || decision.ResolvedIOPS <= 0 {
		return nil
	}
	if pool.PoolAttributes == nil {
		pool.PoolAttributes = &datamodel.PoolAttributes{}
	}
	pool.PoolAttributes.Iops = decision.ResolvedIOPS
	if decision.Throughput > 0 {
		pool.PoolAttributes.ThroughputMibps = int64(decision.Throughput)
	}

	logOCICreatePoolInfo(util.GetLogger(ctx), pool, stageDBPersistFinal, "Persisting VMRS-resolved pool attributes",
		"iops", decision.ResolvedIOPS,
		"throughput", decision.Throughput,
	)
	return workflow.ExecuteActivity(ctx, poolActivity.UpdatePoolFields, pool.UUID, map[string]interface{}{
		"pool_attributes": pool.PoolAttributes,
	}).Get(ctx, nil)
}

func persistOCIPoolUpdate(
	ctx workflow.Context,
	poolActivity *activities.PoolActivity,
	pool *datamodel.Pool,
	params *common.UpdatePoolParams,
	updatedVLMConfig vlm.VLMConfig,
) error {
	logger := util.GetLogger(ctx)
	dbWriteSizeBytes := params.SizeInBytes
	if dbWriteSizeBytes == 0 {
		logger.Info("Backfilling SizeInBytes from existing pool for DB persist",
			"pool", pool.Name,
			"backfilledSizeBytes", pool.SizeInBytes,
		)
		dbWriteSizeBytes = uint64(pool.SizeInBytes)
	}
	dbWriteThroughputMibps := params.TotalThroughputMibps
	if dbWriteThroughputMibps == 0 && pool.PoolAttributes != nil {
		logger.Info("Backfilling TotalThroughputMibps from existing pool for DB persist",
			"pool", pool.Name,
			"backfilledThroughputMibps", pool.PoolAttributes.ThroughputMibps,
		)
		dbWriteThroughputMibps = pool.PoolAttributes.ThroughputMibps
	}
	dbWriteIops := int64(0)
	if pool.PoolAttributes != nil {
		dbWriteIops = pool.PoolAttributes.Iops
	}
	if params.TotalIops != nil {
		dbWriteIops = *params.TotalIops
	}

	dbPersistParams := *params
	dbPersistParams.SizeInBytes = dbWriteSizeBytes
	dbPersistParams.TotalThroughputMibps = dbWriteThroughputMibps
	if params.TotalIops != nil {
		dbPersistParams.TotalIops = params.TotalIops
	} else if dbWriteIops > 0 {
		dbWriteIopsCopy := dbWriteIops
		dbPersistParams.TotalIops = &dbWriteIopsCopy
	}

	logger.Info("Persisting pool-level update with new VLM config",
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"sizeBytes", dbWriteSizeBytes,
		"throughputMibps", dbWriteThroughputMibps,
		"iops", dbWriteIops,
		"nodeCapacitiesCount", len(params.NodeCapacities),
	)
	return workflow.ExecuteActivity(ctx, poolActivity.UpdatedPoolWithVLMConfig, pool, updatedVLMConfig, &dbPersistParams).Get(ctx, nil)
}

func prepareOCIUpdateVSAClusterDeploymentRequest(
	req *vlm.UpdateVSAClusterDeploymentRequest,
	currentVlmConfig vlm.VLMConfig,
	targetSPConfig vlm.SPConfig,
	targetNumHAPair int,
	credentials vlm.OntapCredentials,
	decision *vmrs_oci.Decision,
	params *common.UpdatePoolParams,
) error {
	req.VLMConfig = currentVlmConfig
	req.NumHAPair = targetNumHAPair
	req.SPConfig = targetSPConfig
	req.OntapCredentials = credentials
	req.BucketName = ""
	// Match GCP sentinel: skip auto-tier threshold update when no bucket/object store path applies.
	req.AutoTierThreshold = -1
	ociCfg, err := currentVlmConfig.Deployment.ProviderConfig.AsOCI()
	if err != nil {
		return fmt.Errorf("failed to extract OCI provider config from stored VLMConfig: %w", err)
	}
	if params.NsgIds != nil {
		ociCfg.CustomerNSGs = params.NsgIds
	}
	if params.SecurityAttributes != nil {
		ociCfg.CustomerSecurityAttributes = params.SecurityAttributes
	}
	req.ProviderConfig = vlm.ProviderConfigWrapper{ProviderConfig: ociCfg}
	req.CmekOcid = params.KmsKeyId

	if decision != nil {
		req.NewInstanceType = decision.VMShape
		ocpus := float32(decision.OCPUs)
		memGBs := float32(decision.MemoryGBs)
		vpu := int64(decision.VPU)
		req.VSAFlexOcpus = &ocpus
		req.VSAFlexMemoryInGBs = &memGBs
		req.DataDiskVpus = &vpu
	}
	return nil
}

func prepareOCIITCRecoveryRequest(
	req *vlm.UpdateVSAClusterDeploymentRequest,
	preUpdateVlmConfig vlm.VLMConfig,
	credentials vlm.OntapCredentials,
	params *common.UpdatePoolParams,
) error {
	req.VLMConfig = preUpdateVlmConfig
	req.NumHAPair = len(preUpdateVlmConfig.Cloud.HAPairs)
	req.OntapCredentials = credentials
	req.BucketName = ""
	req.AutoTierThreshold = -1
	ociCfg, err := preUpdateVlmConfig.Deployment.ProviderConfig.AsOCI()
	if err != nil {
		return fmt.Errorf("failed to extract OCI provider config from pre-update VLMConfig: %w", err)
	}
	if params.NsgIds != nil {
		ociCfg.CustomerNSGs = params.NsgIds
	}
	if params.SecurityAttributes != nil {
		ociCfg.CustomerSecurityAttributes = params.SecurityAttributes
	}
	req.ProviderConfig = vlm.ProviderConfigWrapper{ProviderConfig: ociCfg}
	req.CmekOcid = params.KmsKeyId
	return nil
}

func executeOCIUpdatePoolVLMInBatches(
	ctx workflow.Context,
	pool *datamodel.Pool,
	batchPlan *activities.CalculateBatchPlanActivityOutput,
	initialVlmConfig vlm.VLMConfig,
	targetSPConfig vlm.SPConfig,
	targetNumHAPair int,
	credConfig vlm.OntapCredentials,
	ontapVersion string,
	vsaClientWorkflowManager vlm.VlmWorkflowClient,
	decision *vmrs_oci.Decision,
	params *common.UpdatePoolParams,
) (ociPoolUpdateVLMResult, error) {
	normalResult, err := runOCIUpdatePoolVLMBatchLoop(
		ctx, pool, batchPlan, initialVlmConfig, targetSPConfig, targetNumHAPair,
		credConfig, ontapVersion, vsaClientWorkflowManager, decision, params, false, batchPlan.NumWorkflowCalls,
	)
	if err == nil {
		return ociPoolUpdateVLMResult{Response: normalResult.Response}, nil
	}
	if !batchFailureIndicatesITCRecovery(err) {
		return ociPoolUpdateVLMResult{}, err
	}

	logger := util.GetLogger(ctx)
	if !ociEnableITCRecovery {
		logger.Info("VLM failure indicates ITC recovery but OCI_ENABLE_ITC_RECOVERY is disabled; returning original error",
			"pool", pool.Name,
			"poolUUID", pool.UUID,
			"vlmRecoveryStatus", extractVLMRecoveryStatus(err),
		)
		return ociPoolUpdateVLMResult{}, err
	}

	recoveryBatches := normalResult.FailedBatchNum + 1
	logger.Info("VLM failure indicates ITC recovery; re-running attempted batches with ITCRecovery=true",
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"failedBatch", recoveryBatches,
		"recoveryBatches", recoveryBatches,
		"vlmRecoveryStatus", extractVLMRecoveryStatus(err),
	)
	recoveryResult, recoveryErr := runOCIUpdatePoolVLMBatchLoop(
		ctx, pool, batchPlan, initialVlmConfig, targetSPConfig, targetNumHAPair,
		credConfig, ontapVersion, vsaClientWorkflowManager, decision, params, true, recoveryBatches,
	)
	if recoveryErr != nil {
		return ociPoolUpdateVLMResult{}, fmt.Errorf("ITC recovery batch loop failed: %w (original: %w)", recoveryErr, err)
	}

	logger.Info("ITC recovery batch loop completed successfully",
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"recoveryBatchesRun", recoveryBatches,
	)
	return ociPoolUpdateVLMResult{Response: recoveryResult.Response, ITCRecoverySucceeded: true}, nil
}

func runOCIUpdatePoolVLMBatchLoop(
	ctx workflow.Context,
	pool *datamodel.Pool,
	batchPlan *activities.CalculateBatchPlanActivityOutput,
	startVlmConfig vlm.VLMConfig,
	targetSPConfig vlm.SPConfig,
	targetNumHAPair int,
	credConfig vlm.OntapCredentials,
	ontapVersion string,
	vsaClientWorkflowManager vlm.VlmWorkflowClient,
	decision *vmrs_oci.Decision,
	params *common.UpdatePoolParams,
	itcRecovery bool,
	batchesToRun int,
) (ociBatchLoopResult, error) {
	logger := util.GetLogger(ctx)
	hyperscalerCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: utils.GetStartToCloseTimeoutHyperscaler(),
		HeartbeatTimeout:    utils.GetHeartbeatTimeoutForHyperscaler(),
		RetryPolicy:         utils.GetHyperscalerLRORetryPolicy(),
	})

	current := startVlmConfig
	var lastResp *vlm.UpdateVSAClusterDeploymentResponse
	completedBatches := make([]int, 0, batchPlan.NumWorkflowCalls)

	logger.Info("runOCIUpdatePoolVLMBatchLoop starting",
		"pool", pool.Name,
		"poolUUID", pool.UUID,
		"totalBatches", batchPlan.NumWorkflowCalls,
		"batchesToRun", batchesToRun,
		"itcRecovery", itcRecovery,
	)

	for batchNum := 0; batchNum < batchesToRun; batchNum++ {
		batchIndices := batchPlan.BatchIndices[batchNum]

		updateReq := &vlm.UpdateVSAClusterDeploymentRequest{}
		if itcRecovery {
			if err := prepareOCIITCRecoveryRequest(updateReq, startVlmConfig, credConfig, params); err != nil {
				return ociBatchLoopResult{FailedBatchNum: batchNum}, fmt.Errorf("batch %d: %w", batchNum+1, err)
			}
		} else {
			if err := prepareOCIUpdateVSAClusterDeploymentRequest(updateReq, current, targetSPConfig, targetNumHAPair, credConfig, decision, params); err != nil {
				return ociBatchLoopResult{FailedBatchNum: batchNum}, fmt.Errorf("batch %d: %w", batchNum+1, err)
			}
		}
		updateReq.HAPairIndices = batchIndices
		updateReq.ITCRecovery = itcRecovery

		logger.Info("OCI update pool VLM batch",
			"batchNumber", batchNum+1,
			"totalBatches", batchPlan.NumWorkflowCalls,
			"haPairIndices", batchIndices,
			"itcRecovery", itcRecovery,
		)

		resp, err := vsaClientWorkflowManager.UpdateVSAClusterDeployment(hyperscalerCtx, updateReq, ontapVersion)
		if err != nil {
			logOCIBatchFailure(ctx, logger, pool, batchPlan, batchNum, batchIndices, completedBatches, itcRecovery, err, nil, vlm.DeploymentUpdateStatus{})
			return ociBatchLoopResult{FailedBatchNum: batchNum}, err
		}
		if failedOps := updateStatusFailureFlags(resp.UpdateStatus); len(failedOps) > 0 {
			partialErr := vsaerrors.NewVCPError(
				vsaerrors.ErrIncorrectVSAClusterState,
				fmt.Errorf("%w: VLM reported partial update failure for batch %d (haPairIndices=%v) on pool %q: failed sub-operations %v",
					errOCIPartialVLMUpdateBatchFailure, batchNum+1, batchIndices, pool.Name, failedOps),
			)
			logOCIBatchFailure(ctx, logger, pool, batchPlan, batchNum, batchIndices, completedBatches, itcRecovery, partialErr, failedOps, resp.UpdateStatus)
			return ociBatchLoopResult{FailedBatchNum: batchNum}, partialErr
		}

		if !itcRecovery {
			current = resp.VLMConfig
		}
		lastResp = resp
		completedBatches = append(completedBatches, batchNum+1)
		logger.Info("OCI update pool VLM batch completed",
			"pool", pool.Name,
			"batchNumber", batchNum+1,
			"totalBatches", batchPlan.NumWorkflowCalls,
			"haPairIndices", batchIndices,
			"itcRecovery", itcRecovery,
		)
	}

	return ociBatchLoopResult{Response: lastResp, FailedBatchNum: -1}, nil
}

func logOCIBatchFailure(
	ctx workflow.Context,
	logger log.Logger,
	pool *datamodel.Pool,
	batchPlan *activities.CalculateBatchPlanActivityOutput,
	batchNum int,
	batchIndices []int,
	completedBatches []int,
	itcRecovery bool,
	err error,
	failedOps []string,
	updateStatus vlm.DeploymentUpdateStatus,
) {
	remainingBatches := make([]int, 0, batchPlan.NumWorkflowCalls-batchNum-1)
	for i := batchNum + 1; i < batchPlan.NumWorkflowCalls; i++ {
		remainingBatches = append(remainingBatches, i+1)
	}
	if len(failedOps) > 0 {
		logger.Error("OCI pool update VLM batch returned partial-failure UpdateStatus; cluster is in mixed-version state",
			"pool", pool.Name,
			"poolUUID", pool.UUID,
			"batchNumber", batchNum+1,
			"totalBatches", batchPlan.NumWorkflowCalls,
			"failingBatchHAPairIndices", batchIndices,
			"completedBatches", completedBatches,
			"remainingBatches", remainingBatches,
			"itcRecovery", itcRecovery,
			"failedSubOperations", failedOps,
			"updateStatus", updateStatus,
			"error", err,
		)
	} else {
		logger.Error("OCI pool update VLM batch failed; cluster is in mixed-version state",
			"pool", pool.Name,
			"poolUUID", pool.UUID,
			"batchNumber", batchNum+1,
			"totalBatches", batchPlan.NumWorkflowCalls,
			"failingBatchHAPairIndices", batchIndices,
			"completedBatches", completedBatches,
			"remainingBatches", remainingBatches,
			"itcRecovery", itcRecovery,
			"error", err,
		)
	}
	if !itcRecovery && ociEnableITCRecovery && batchFailureIndicatesITCRecovery(err) {
		return
	}
	emitStage(ctx, wfUpdatePool, queueCustomer, stageVLMUpdate, resultFailure)
}

// updateStatusFailureFlags returns the names of any sub-operation failure flags set on a
// VLM UpdateVSAClusterDeployment response. Returned slice is empty when status reports a
// fully-successful batch; non-empty when any of the documented sub-failures
// (DetachFail / SPUpdateFail / AttachFail / LifDownFail / AggrDownFail / AggrUpFail /
// LifUpFail) is set. Kept in declaration order so logs are stable.
func updateStatusFailureFlags(status vlm.DeploymentUpdateStatus) []string {
	failed := make([]string, 0, 7)
	if status.DetachFail {
		failed = append(failed, "detach")
	}
	if status.SPUpdateFail {
		failed = append(failed, "sp_update")
	}
	if status.AttachFail {
		failed = append(failed, "attach")
	}
	if status.LifDownFail {
		failed = append(failed, "lif_down")
	}
	if status.AggrDownFail {
		failed = append(failed, "aggr_down")
	}
	if status.AggrUpFail {
		failed = append(failed, "aggr_up")
	}
	if status.LifUpFail {
		failed = append(failed, "lif_up")
	}
	return failed
}

func ociUpdatePoolRequestCarriesCapacityChange(params *common.UpdatePoolParams) bool {
	if params == nil {
		return false
	}
	return params.SizeInBytes > 0 || len(params.NodeCapacities) > 0
}

// ociUpdateNeedsSPConfig reports whether the update carries any change that
// requires building an SPConfig (capacity, throughput, or IOPS). Metadata-only
// updates (kms, nsg, security attributes) return false so the workflow skips
// loading the VMRS catalogue.
func ociUpdateNeedsSPConfig(params *common.UpdatePoolParams) bool {
	if params == nil {
		return false
	}
	return ociUpdatePoolRequestCarriesCapacityChange(params) ||
		params.TotalThroughputMibps > 0 ||
		params.TotalIops != nil
}
