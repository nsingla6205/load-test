// Package vcm_workflows hosts the workflow definitions backing the VCM-proxy
// endpoints.
package vcm_workflows

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	vcmclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm/vcm-client"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

const localEnv = "local"

// wfLogger resolves the engine-neutral workflow logger from the orchestrator
// context so log lines carry the workflow's correlation/deployment fields
// instead of coming from a package-global logger. Both workflow backends
// register a logger factory in init(), so at workflow runtime this always
// resolves; the error path is unreachable and panics to surface the
// misconfiguration immediately (mirroring the backend context helpers).
func wfLogger(ctx *orchestrator.Context) orchestrator.UnifiedLogger {
	logger, err := orchestrator.WorkflowLoggerFromContext(ctx.BackendCtx())
	if err != nil {
		panic("vcm_workflows: no workflow logger factory registered: " + err.Error())
	}
	return logger
}

// Durable job step labels surfaced as the operation's current_step.
const (
	jobStepStarting            = "starting"
	jobStepCreateCluster       = "create-cluster-deployment"
	jobStepCreateSVM           = "create-svm"
	jobStepNfsTransferSize     = "update-nfs-transfer-size"
	jobStepCreateQoSPolicy     = "create-qos-policy"
	jobStepCreateOntapModeUser = "create-ontap-mode-user"
	jobStepGetCluster          = "get-cluster-deployment"
	jobStepIdentifyVMs         = "identify-vms"
	jobStepUpdateCluster       = "update-cluster-deployment"
	jobStepCompleted           = "completed"
)

// Authentication types understood by the VLM expert-mode user workflow.
const (
	vlmAuthenticationTypePassword    = "password"
	vlmAuthenticationTypeCertificate = "certificate"
)

// VLM's AccessSecretVersion call rejects secret resource names without a
// version, so unversioned license URIs are pinned to the latest version.
const (
	secretVersionSegment      = "/versions/"
	latestSecretVersionSuffix = secretVersionSegment + "latest"
)

// Change types PATCH /v1/clusters accepts. COMPUTE rotates the cluster onto a
// new SKU; SCALE resizes the storage pool on the current SKU.
const (
	ClusterComputeChangeType = "COMPUTE"
	ClusterScaleChangeType   = "SCALE"
)

// vlmSkipAutoTierThreshold tells VLM not to rewrite the auto-tier threshold.
const vlmSkipAutoTierThreshold = int64(-1)

var (
	runningEnv            = env.GetString("ENV", "")
	vcmLocalOntapUsername = env.GetString("VCM_LOCAL_ONTAP_USERNAME", "")
	vcmLocalOntapPassword = env.GetString("VCM_LOCAL_ONTAP_PASSWORD", "")
	// extIPForNodeMgmt assigns an external IP to the node-management LIF.
	extIPForNodeMgmt = env.GetBool("VCM_EXT_IP_FOR_NODE_MGMT", false)

	vcmLocalOntapModeUsername = env.GetString("VCM_LOCAL_ONTAP_MODE_USERNAME", "")
	vcmLocalOntapModePassword = env.GetString("VCM_LOCAL_ONTAP_MODE_PASSWORD", "")

	vmrsConfigPath = env.GetString("VMRS_CONFIG_PATH", "/config/vmrs_gcp.yaml")

	vsaImageProject         = env.GetString("VSA_IMAGE_PROJECT", "")
	vsaMediatorImageProject = env.GetString("VSA_MEDIATOR_IMAGE_PROJECT", "")
	vsaImageName            = env.GetString("VSA_IMAGE_NAME", "")
	vsaMediatorImageName    = env.GetString("VSA_MEDIATOR_IMAGE_NAME", "")
)

// isLocalEnv reports whether the service is running in the local environment.
func isLocalEnv() bool { return runningEnv == localEnv }

// createPoolWorkflow holds the raw request and orchestrator-derived inputs plus
// the durable job identity.
type createPoolWorkflow struct {
	// baseWorkflow carries the durable job id and state helpers.
	baseWorkflow

	req    *commonparams.VCMCreatePoolParams
	inputs *commonparams.VCMDerivedPoolInputs

	// currentStep is the last entered step, recorded on terminal ERROR.
	currentStep string

	// vlmCfg is the latest VLM config returned by cluster/SVM child workflows.
	vlmCfg vlm.VLMConfig
	// svmName is the data SVM created during pool provisioning.
	svmName string
}

// updatePoolWorkflow holds the raw request and orchestrator-derived inputs plus
// the durable job identity for COMPUTE / SCALE pool updates.
type updatePoolWorkflow struct {
	// baseWorkflow carries the durable job id and state helpers.
	baseWorkflow

	req    *commonparams.VCMUpdatePoolParams
	inputs *commonparams.VCMDerivedPoolInputs

	// currentStep is the last entered step, recorded on terminal ERROR.
	currentStep string

	// vlmCfg is the latest VLM config returned by the cluster child workflows.
	vlmCfg vlm.VLMConfig
	// svmName is the data SVM associated with the pool being updated.
	svmName string
}

// PoolUpdateWorkflow applies COMPUTE / SCALE pool updates and drives the
// durable job through NEW -> PROCESSING -> DONE/ERROR. The job row is created
// (in NEW) by the orchestrator factory before the workflow starts.
func PoolUpdateWorkflow(ctx *orchestrator.Context, params *commonparams.UpdatePoolParams) (*commonparams.VCMPoolWorkflowResult, error) {
	if params == nil || params.VCM == nil {
		return nil, fmt.Errorf("pool update workflow: VCM params missing from request")
	}
	// Prefer orchestrator-derived inputs; fall back to deriving here.
	inputs := params.VCM.Derived
	if inputs == nil {
		inputs = params.VCM.DeriveInputs()
	}

	wf := &updatePoolWorkflow{req: params.VCM, inputs: inputs}
	wf.ID = params.VCM.RequestID

	logger := wfLogger(ctx)
	logger.Info("workflow: starting update pool",
		"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "deadline", wf.req.Deadline,
		"updateMask", wf.req.UpdateMask, "changeType", wf.req.ChangeType)

	// Fail an already-expired job before marking it PROCESSING. Crash recovery
	// re-dispatches NEW/PROCESSING jobs; without this gate an expired COMPUTE
	// rotation would resume past the client's timeout.
	wf.Status = workflowStatusRunning
	wf.currentStep = jobStepStarting
	if err := checkDeadline(ctx, wf.req.Deadline, "mark-processing"); err != nil {
		return wf.fail(ctx, err)
	}
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateProcessing, jobStepStarting, nil, nil); err != nil {
		logger.Error("workflow: failed to mark job PROCESSING",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
		return nil, err
	}

	if err := wf.apply(ctx); err != nil {
		return wf.fail(ctx, err)
	}

	donePayload, marshalErr := poolCreateOperationResponsePayloadFn(&wf.req.VCMCreatePoolParams, wf.vlmCfg, wf.svmName)
	if marshalErr != nil {
		logger.Warn("workflow: failed to marshal pool operation response; marking job DONE without response payload",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", marshalErr)
		donePayload = nil
	}
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateDone, jobStepCompleted, nil, donePayload); err != nil {
		logger.Error("workflow: failed to mark job DONE", "deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
		return nil, err
	}
	wf.Status = workflowStatusCompleted

	return &commonparams.VCMPoolWorkflowResult{
		DeploymentID: wf.req.DeploymentID,
		Status:       "READY",
		Message:      "pool update workflow completed",
	}, nil
}

// fail records a terminal ERROR on the durable job and returns that error.
func (wf *updatePoolWorkflow) fail(ctx *orchestrator.Context, err error) (*commonparams.VCMPoolWorkflowResult, error) {
	logger := wfLogger(ctx)
	logger.Error("workflow: update pool failed",
		"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
	wf.Status = workflowStatusFailed
	if uerr := wf.UpdateJobStatus(ctx, vcmdb.JobStateError, wf.currentStep, err, nil); uerr != nil {
		logger.Error("workflow: failed to mark job ERROR",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", uerr)
		return nil, uerr
	}
	return nil, err
}

// apply loads the live cluster, selects the target SKU from the desired shape,
// then runs exactly one of COMPUTE or SCALE. VCM does not chain the two.
func (wf *updatePoolWorkflow) apply(ctx *orchestrator.Context) error {
	if wf.inputs == nil {
		return fmt.Errorf("pool update workflow: derived inputs missing")
	}
	logger := wfLogger(ctx)
	p := wf.req
	deploymentID := p.DeploymentID

	ontapCreds, err := resolveOntapCredentials(ctx, wf.inputs.OntapCredentials)
	if err != nil {
		logger.Error("workflow: resolve ONTAP admin password failed",
			"deploymentName", deploymentID, "error", err)
		return err
	}

	if err := checkDeadline(ctx, p.Deadline, "get-cluster"); err != nil {
		return err
	}
	wf.setStep(ctx, jobStepGetCluster)
	current, err := wf.getCurrentVLMConfig(ctx, ontapCreds)
	if err != nil {
		return err
	}
	wf.vlmCfg = current
	if svmName, derr := commonparams.DeriveVCMSvmName(deploymentID); derr == nil {
		wf.svmName = svmName
	}

	if err := checkDeadline(ctx, p.Deadline, "identify-vms"); err != nil {
		return err
	}
	wf.setStep(ctx, jobStepIdentifyVMs)
	target, err := wf.identifyTargetVLMConfig(ctx, current.Deployment.VSAInstanceType)
	if err != nil {
		return err
	}

	updateReq, skipVLMUpdate, err := buildVCMUpdateDeploymentRequest(current, target, ontapCreds, p.ChangeType)
	if err != nil {
		return err
	}

	switch p.ChangeType {
	case ClusterComputeChangeType:
		// Same SKU: VLM has nothing to do.
		if skipVLMUpdate {
			logger.Info("workflow: COMPUTE no-op; current SKU already hosts the desired shape",
				"deploymentName", deploymentID, "instanceType", current.Deployment.VSAInstanceType)
			return nil
		}
		return wf.applyCompute(ctx, target, updateReq)
	case ClusterScaleChangeType:
		return wf.applyScale(ctx, current, updateReq)
	default:
		return fmt.Errorf("pool update workflow: unsupported changeType %q", p.ChangeType)
	}
}

// getCurrentVLMConfig reads the live cluster's VLM config, which is the only
// source for what the pool is currently running with.
func (wf *updatePoolWorkflow) getCurrentVLMConfig(ctx *orchestrator.Context, creds vlm.OntapCredentials) (vlm.VLMConfig, error) {
	return getLiveVLMConfig(ctx, vcmclient.InvokeGetClusterDeployment, &wf.req.VCMCreatePoolParams, wf.inputs, creds)
}

// getClusterInvoker runs the VLM get-cluster child workflow. Callers pick the
// child workflow ID through the invoker they pass.
type getClusterInvoker func(*orchestrator.Context, *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error)

// getLiveVLMConfig reads the live cluster's VLM config from VLM and applies
// the VCM update flags to it.
func getLiveVLMConfig(
	ctx *orchestrator.Context,
	invoke getClusterInvoker,
	p *commonparams.VCMCreatePoolParams,
	inputs *commonparams.VCMDerivedPoolInputs,
	creds vlm.OntapCredentials,
) (vlm.VLMConfig, error) {
	region := ""
	if inputs != nil {
		region = inputs.Region
	}
	if region == "" {
		region = env.Region
	}

	resp, err := invoke(ctx, &vlm.GetVSAClusterDeploymentRequest{
		DeploymentID:     p.DeploymentID,
		CloudProvider:    env.Hyperscaler,
		ProjectID:        p.ServiceTenantProject,
		Region:           region,
		Zone1:            p.PrimaryZone,
		Zone2:            p.SecondaryZone,
		MediatorZone:     p.MediatorZone,
		DeploymentType:   vlm.DeploymentTypeNonSharedHA,
		OntapCredentials: creds,
	})
	if err != nil {
		wfLogger(ctx).Error("workflow: VLM get-cluster failed",
			"deploymentName", p.DeploymentID, "error", err)
		return vlm.VLMConfig{}, fmt.Errorf("VLM get cluster deployment: %w", err)
	}

	configureVCMVLMConfigForPoolUpdate(&resp.VLMConfig)
	return resp.VLMConfig, nil
}

// identifyTargetVLMConfig asks VMRS for the shape that satisfies the requested
// performance, passing the current SKU so the selection can stay on it.
func (wf *updatePoolWorkflow) identifyTargetVLMConfig(ctx *orchestrator.Context, currentInstanceType string) (vlm.VLMConfig, error) {
	if wf.inputs == nil {
		return vlm.VLMConfig{}, fmt.Errorf("pool update workflow: derived inputs missing")
	}
	var pa *activities.PoolActivity
	p := wf.req

	var target vlm.VLMConfig
	if err := orchestrator.ExecuteActivity(ctx, pa.IdentifyVMs, activities.IdentifyVMsRequest{
		VMRSConfigPath: vmrsConfigPath,
		CustomerRequest: vmrs.CustomerRequestedPerformance{
			DesiredThroughputInMiBs: p.ThroughputMibps,
			DesiredIOPS:             p.Iops,
			DesiredCapacityInGiB:    wf.inputs.DesiredCapacityInGiB,
			CurrentInstanceType:     currentInstanceType,
		},
		DeploymentName:      p.DeploymentID,
		RegionIdentifier:    p.RegionIdentifier,
		LocationInfo:        wf.inputs.LocationInfo,
		TenancyInfo:         wf.inputs.TenancyInfo,
		ServiceAccountEmail: p.ServiceAccountEmail,
		AutoTierBucket:      p.AutoTierBucketName,
		IsLargeCapacityPool: wf.inputs.IsLargeCapacityPool,
		IsOntapMode:         wf.inputs.IsOntapMode,
		AccountID:           p.ProjectID,
	}).Get(ctx, &target); err != nil {
		wfLogger(ctx).Error("workflow: identify VMs / populate VLM config failed",
			"deploymentName", p.DeploymentID, "vmrsConfigPath", vmrsConfigPath, "error", err)
		return vlm.VLMConfig{}, fmt.Errorf("identify VMs / populate VLM config: %w", err)
	}
	return target, nil
}

// applyCompute rotates the cluster onto target.Deployment.VSAInstanceType.
// Disks stay on the current cluster until a later SCALE call.
func (wf *updatePoolWorkflow) applyCompute(ctx *orchestrator.Context, target vlm.VLMConfig, updateReq *vlm.UpdateVSAClusterDeploymentRequest) error {
	logger := wfLogger(ctx)
	newType := target.Deployment.VSAInstanceType
	logger.Info("workflow: COMPUTE pool update",
		"deploymentName", wf.req.DeploymentID, "newInstanceType", newType)

	var pa *activities.PoolActivity
	if err := orchestrator.ExecuteActivity(ctx, pa.ValidateZonesForMachineTypes,
		wf.req.ServiceTenantProject, wf.req.PrimaryZone, wf.req.SecondaryZone, newType).Get(ctx, nil); err != nil {
		logger.Error("workflow: zone validation for new instance type failed",
			"deploymentName", wf.req.DeploymentID, "newInstanceType", newType, "error", err)
		return fmt.Errorf("validate zones for machine type: %w", err)
	}

	return wf.invokeVLMUpdate(ctx, updateReq)
}

// applyScale sends the SPConfig update to VLM on the current SKU.
func (wf *updatePoolWorkflow) applyScale(ctx *orchestrator.Context, current vlm.VLMConfig, updateReq *vlm.UpdateVSAClusterDeploymentRequest) error {
	logger := wfLogger(ctx)
	logger.Info("workflow: SCALE pool update",
		"deploymentName", wf.req.DeploymentID, "instanceType", current.Deployment.VSAInstanceType)

	// TODO: Rewrite SVM-wide QoS ({svm}-qos-policy) on SCALE to match
	// requested throughput/IOPS, including auto->manual (remove QPG) for
	// vcp-worker parity. Create still installs the QPG when qosType is auto.
	return wf.invokeVLMUpdate(ctx, updateReq)
}

func (wf *updatePoolWorkflow) invokeVLMUpdate(ctx *orchestrator.Context, updateReq *vlm.UpdateVSAClusterDeploymentRequest) error {
	if err := checkDeadline(ctx, wf.req.Deadline, "update-cluster"); err != nil {
		return err
	}
	wf.setStep(ctx, jobStepUpdateCluster)
	resp, err := vcmclient.InvokeUpdateClusterDeployment(ctx, updateReq)
	if err != nil {
		wfLogger(ctx).Error("workflow: VLM cluster update failed",
			"deploymentName", wf.req.DeploymentID, "changeType", wf.req.ChangeType, "error", err)
		return fmt.Errorf("VLM cluster update: %w", err)
	}
	wf.vlmCfg = resp.VLMConfig
	return nil
}

// setStep updates the durable job's current_step (best-effort; a failed update
// is logged but does not abort the update).
func (wf *updatePoolWorkflow) setStep(ctx *orchestrator.Context, step string) {
	wf.currentStep = step
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateProcessing, step, nil, nil); err != nil {
		wfLogger(ctx).Warn("workflow: failed to update job step",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "step", step, "error", err)
	}
}

// buildVCMUpdateDeploymentRequest builds a single VLM update from the live
// cluster plus the IdentifyVMs target.
//
// UpdateVSAClusterDeploymentWorkflow treats req.VLMConfig as the live cluster
// and reads the desired change only from the top-level fields:
//   - NewInstanceType against VLMConfig.Deployment.VSAInstanceType
//   - SPConfig against VLMConfig.Deployment.SPConfig (CapacityOrPerfChanged)
//
// The embedded deployment must keep the current SKU and SPConfig. Writing the
// target into VLMConfig.Deployment would make both sides of that diff equal,
// so COMPUTE would skip instance-type change and SCALE would skip the disk
// update.
//
// COMPUTE sets NewInstanceType and keeps the current SPConfig so disks stay
// put. SCALE sets SPConfig to the target and leaves NewInstanceType empty so
// VLM does not rotate VMs. The SCALE size is first rounded up to whole GiB
// per data disk, which is the size VLM will provision. SCALE always calls
// VLM; VLM is responsible for treating an unchanged layout as a no-op.
// skipVLMUpdate is true only for COMPUTE when the SKU is already current.
func buildVCMUpdateDeploymentRequest(current, target vlm.VLMConfig, creds vlm.OntapCredentials, changeType string) (*vlm.UpdateVSAClusterDeploymentRequest, bool, error) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{
		VLMConfig:         current,
		NumHAPair:         current.Deployment.NumHAPair,
		SPConfig:          current.Deployment.SPConfig,
		OntapCredentials:  creds,
		HAPairIndices:     haPairIndices(current.Deployment.NumHAPair),
		AutoTierThreshold: vlmSkipAutoTierThreshold,
	}
	if req.NumHAPair <= 0 {
		req.NumHAPair = 1
		req.HAPairIndices = haPairIndices(1)
	}

	switch changeType {
	case ClusterComputeChangeType:
		if current.Deployment.VSAInstanceType == target.Deployment.VSAInstanceType {
			return req, true, nil
		}
		req.NewInstanceType = target.Deployment.VSAInstanceType
		return req, false, nil
	case ClusterScaleChangeType:
		req.SPConfig = target.Deployment.SPConfig
		// VLM sizes each data disk in whole GiB and stores the sum. Sending the
		// pre-round VMRS size (1229Gi vs 1232Gi on disk) is rejected as a shrink.
		// TODO: move this rounding into VLM so the update request can keep the
		// VMRS size and VLM compares the size it will actually provision.
		if rounded, ok := roundSPSizeToDataDisks(target.Deployment.SPConfig.Size, scaleDataDiskCount(current)); ok {
			req.SPConfig.Size = rounded
		}
		return req, false, nil
	default:
		return nil, false, fmt.Errorf("pool update workflow: unsupported changeType %q", changeType)
	}
}

// haPairIndices returns the 1-based indices VLM expects for a targeted update
// across all HA pairs in the cluster.
func haPairIndices(n int) []int {
	if n <= 0 {
		n = 1
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

// vlmDefaultDataDisksPerNode matches vlmconfig.DefaultDisksPerNode. Get-cluster
// can report 0 when the live deployment never persisted a disk count.
const vlmDefaultDataDisksPerNode = 8

// scaleDataDiskCount is the disk count used to round a SCALE size. Multi-HA
// and heterogeneous layouts are left unchanged; VLM owns those splits.
func scaleDataDiskCount(current vlm.VLMConfig) int {
	if current.Deployment.SPConfig.IsHeterogeneous || current.Deployment.NumHAPair > 1 {
		return 0
	}
	if current.Deployment.DataDiskCount > 0 {
		return current.Deployment.DataDiskCount
	}
	return vlmDefaultDataDisksPerNode
}

// roundSPSizeToDataDisks rounds a VMRS SP size up to whole GiB per data disk,
// matching VLM getPerDiskSizeGB. 1229Gi across 8 disks becomes 1232Gi.
func roundSPSizeToDataDisks(size string, diskCount int) (string, bool) {
	if diskCount <= 0 {
		return "", false
	}
	spBytes, err := parseSPSizeBytes(size)
	if err != nil {
		return "", false
	}
	rounded := roundedDataDiskTotalBytes(spBytes, diskCount)
	if rounded <= 0 {
		return "", false
	}
	return fmt.Sprintf("%dGi", rounded/int64(bytesPerGiB)), true
}

// roundedDataDiskTotalBytes is VLM getPerDiskSizeGB applied across every data
// disk: each disk is a whole number of GiB, rounded up.
func roundedDataDiskTotalBytes(spBytes int64, diskCount int) int64 {
	if diskCount <= 0 || spBytes <= 0 {
		return 0
	}
	bytesPerDiskSet := int64(diskCount) * int64(bytesPerGiB)
	perDiskGiB := spBytes / bytesPerDiskSet
	if spBytes%bytesPerDiskSet != 0 {
		perDiskGiB++
	}
	return perDiskGiB * int64(diskCount) * int64(bytesPerGiB)
}

// parseSPSizeBytes parses a VLM size such as "1229Gi" or "2Ti".
func parseSPSizeBytes(size string) (int64, error) {
	if len(size) < 3 || size[len(size)-1] != 'i' {
		return 0, fmt.Errorf("invalid size %q", size)
	}
	var multiplier int64
	switch size[len(size)-2] {
	case 'G':
		multiplier = int64(bytesPerGiB)
	case 'T':
		multiplier = int64(bytesPerGiB) << 10
	default:
		return 0, fmt.Errorf("invalid size %q", size)
	}
	n, err := strconv.ParseInt(size[:len(size)-2], 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size %q", size)
	}
	return n * multiplier, nil
}

// PoolWorkflow provisions an ONTAP cluster (storage pool) and drives the
// durable job through NEW -> PROCESSING -> DONE/ERROR. The job row is created
// (in NEW) by the orchestrator factory before the workflow starts.
func PoolWorkflow(ctx *orchestrator.Context, params *commonparams.CreatePoolParams) (*commonparams.VCMPoolWorkflowResult, error) {
	if params == nil || params.VCM == nil {
		return nil, fmt.Errorf("pool workflow: VCM params missing from request")
	}
	// Prefer orchestrator-derived inputs; fall back to deriving here.
	inputs := params.VCM.Derived
	if inputs == nil {
		inputs = params.VCM.DeriveInputs()
	}

	wf := &createPoolWorkflow{req: params.VCM, inputs: inputs}
	wf.ID = params.VCM.RequestID

	logger := wfLogger(ctx)
	logger.Info("workflow: starting create pool",
		"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "deadline", wf.req.Deadline,
		"region", wf.inputs.Region, "primaryZone", wf.req.PrimaryZone)

	// Move the job to PROCESSING before doing any work. Two callers reach this
	// point: the API path (CreatePool), which has already created a fresh job
	// and rejected duplicates via ErrJobAlreadyExists, and crash recovery, which
	// re-dispatches an existing NEW or PROCESSING job to resume it. The workflow
	// therefore does not re-check job state; the transition below is idempotent
	// when the job is already PROCESSING, and the provision steps are safe to
	// replay.
	wf.Status = workflowStatusRunning
	wf.currentStep = jobStepStarting
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateProcessing, jobStepStarting, nil, nil); err != nil {
		logger.Error("workflow: failed to mark job PROCESSING",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
		return nil, err
	}

	if err := wf.provision(ctx); err != nil {
		logger.Error("workflow: create pool provisioning failed",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
		wf.Status = workflowStatusFailed
		// Record the terminal ERROR state.
		if uerr := wf.UpdateJobStatus(ctx, vcmdb.JobStateError, wf.currentStep, err, nil); uerr != nil {
			logger.Error("workflow: failed to mark job ERROR",
				"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", uerr)
			return nil, uerr
		}
		return nil, err
	}

	donePayload, marshalErr := poolCreateOperationResponsePayloadFn(wf.req, wf.vlmCfg, wf.svmName)
	if marshalErr != nil {
		logger.Warn("workflow: failed to marshal pool operation response; marking job DONE without response payload",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", marshalErr)
		donePayload = nil
	}
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateDone, jobStepCompleted, nil, donePayload); err != nil {
		logger.Error("workflow: failed to mark job DONE", "deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "error", err)
		return nil, err
	}
	wf.Status = workflowStatusCompleted

	return &commonparams.VCMPoolWorkflowResult{
		DeploymentID: wf.req.DeploymentID,
		Status:       "READY",
		Message:      "pool workflow completed",
	}, nil
}

// poolCreateOperationResponsePayloadFn marshals the pool-create operation response JSON.
var poolCreateOperationResponsePayloadFn = poolOperationResponsePayload

// provision runs the activities and child workflows in order, recording each
// step on the durable job and returning the first error.
func (wf *createPoolWorkflow) provision(ctx *orchestrator.Context) error {
	var pa *activities.PoolActivity
	var sa *activities.SvmActivity

	if err := wf.validateOntapModeInputs(); err != nil {
		return err
	}

	logger := wfLogger(ctx)
	p := wf.req
	deploymentID := p.DeploymentID

	if err := checkDeadline(ctx, wf.req.Deadline, "start"); err != nil {
		return err
	}

	ontapCreds, err := resolveOntapCredentials(ctx, wf.inputs.OntapCredentials)
	if err != nil {
		logger.Error("workflow: resolve ONTAP admin password failed",
			"deploymentName", deploymentID, "error", err)
		return err
	}

	var ontapModeUsername, ontapModeAuthenticationType string
	var ontapModeCreds vlm.OntapCredentials
	if wf.inputs.IsOntapMode {
		ontapModeUsername, ontapModeAuthenticationType, ontapModeCreds, err =
			resolveOntapModeCredentials(ctx, wf.inputs.OntapModeCredentials)
		if err != nil {
			logger.Error("workflow: resolve ONTAP mode credentials failed",
				"deploymentName", deploymentID, "error", err)
			return err
		}
	}

	if err := checkDeadline(ctx, wf.req.Deadline, "identify-vms"); err != nil {
		return err
	}

	var vlmCfg vlm.VLMConfig
	if err := orchestrator.ExecuteActivity(ctx, pa.IdentifyVMs, activities.IdentifyVMsRequest{
		VMRSConfigPath: vmrsConfigPath,
		CustomerRequest: vmrs.CustomerRequestedPerformance{
			DesiredThroughputInMiBs: p.ThroughputMibps,
			DesiredIOPS:             p.Iops,
			DesiredCapacityInGiB:    wf.inputs.DesiredCapacityInGiB,
		},
		DeploymentName:      deploymentID,
		RegionIdentifier:    p.RegionIdentifier,
		LocationInfo:        wf.inputs.LocationInfo,
		TenancyInfo:         wf.inputs.TenancyInfo,
		ServiceAccountEmail: p.ServiceAccountEmail,
		AutoTierBucket:      p.AutoTierBucketName,
		IsLargeCapacityPool: wf.inputs.IsLargeCapacityPool,
		IsOntapMode:         wf.inputs.IsOntapMode,
		AccountID:           p.ProjectID,
	}).Get(ctx, &vlmCfg); err != nil {
		logger.Error("workflow: identify VMs / populate VLM config failed",
			"deploymentName", deploymentID, "vmrsConfigPath", vmrsConfigPath, "error", err)
		return fmt.Errorf("identify VMs / populate VLM config: %w", err)
	}
	vlmCfg.Deployment.Zone.MediatorZone = p.MediatorZone
	if vsaImageProject != "" {
		vlmCfg.Deployment.GCPConfig.ImageProjectID = vsaImageProject
	}
	if vsaMediatorImageProject != "" {
		vlmCfg.Deployment.GCPConfig.MediatorImageProjectID = vsaMediatorImageProject
	}
	if vsaImageName != "" {
		vlmCfg.Deployment.Images.VSAImageName = vsaImageName
	}
	if vsaMediatorImageName != "" {
		vlmCfg.Deployment.Images.MediatorImageName = vsaMediatorImageName
	}
	logger.Info("workflow: VLM config built",
		"clusterName", vlmCfg.VsaCluster.ClusterName,
		"imageProject", vlmCfg.Deployment.GCPConfig.ImageProjectID,
		"vsaImage", vlmCfg.Deployment.Images.VSAImageName,
		"mediatorImageProject", vlmCfg.Deployment.GCPConfig.MediatorImageProjectID,
		"mediatorImage", vlmCfg.Deployment.Images.MediatorImageName)

	configureVCMVLMConfigForPoolCreate(&vlmCfg)

	if err := checkDeadline(ctx, wf.req.Deadline, "create-cluster-deployment"); err != nil {
		return err
	}

	wf.setStep(ctx, jobStepCreateCluster)
	ontapLicense := ontapLicenseForCreate(p.OntapLicenseSecretURI)
	logger.Info("workflow: invoking VLM create-cluster deployment child workflow",
		"deploymentName", deploymentID, "numHAPair", vlmCfg.Deployment.NumHAPair)
	depRes, err := vcmclient.InvokeCreateClusterDeployment(ctx,
		&vlm.CreateVSAClusterDeploymentRequest{
			VLMConfig:        vlmCfg,
			OntapCredentials: ontapCreds,
			OntapLicense:     ontapLicense,
		},
	)
	if err != nil {
		logger.Error("workflow: VLM cluster deployment failed",
			"deploymentName", deploymentID, "error", err)
		return fmt.Errorf("VLM cluster deployment: %w", err)
	}
	vlmCfg = depRes.VLMConfig
	wf.vlmCfg = vlmCfg
	forwardingRuleIP := nodeMgmtIPFromVLMConfig(vlmCfg)
	logger.Info("workflow: VLM cluster deployment completed",
		"clusterName", vlmCfg.VsaCluster.ClusterName, "nodeMgmtIp", forwardingRuleIP)

	if err := checkDeadline(ctx, wf.req.Deadline, "create-svm"); err != nil {
		return err
	}

	wf.setStep(ctx, jobStepCreateSVM)
	svmName, err := commonparams.DeriveVCMSvmName(deploymentID)
	if err != nil {
		logger.Error("workflow: derive SVM name failed", "deploymentName", deploymentID, "error", err)
		return fmt.Errorf("derive SVM name: %w", err)
	}
	logger.Info("workflow: invoking VLM create-SVM child workflow",
		"deploymentName", deploymentID, "svmName", svmName)
	svmRes, err := vcmclient.InvokeCreateSVM(ctx,
		&vlm.CreateSVMRequest{
			VLMConfig:        vlmCfg,
			Name:             svmName,
			OntapCredentials: ontapCreds,
		},
	)
	if err != nil {
		logger.Error("workflow: VLM SVM creation failed",
			"deploymentName", deploymentID, "svmName", svmName, "error", err)
		return fmt.Errorf("VLM SVM creation: %w", err)
	}
	if svmRes != nil {
		vlmCfg = svmRes.VLMConfig
	}
	wf.vlmCfg = vlmCfg
	wf.svmName = svmName
	logger.Info("workflow: VLM SVM created", "svmName", svmName)

	qosType := mapQosType(wf.req.QosType)
	// Both SVM-level steps below are needed whenever QoS is not manual, and
	// always for ONTAP-mode pools: the SVM-wide QoS policy is the ceiling that
	// bounds what a customer can drive through direct ONTAP access.
	if qosType != utils.QosTypeManual || wf.inputs.IsOntapMode {
		svm := &datamodel.Svm{
			Name: svmName,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: svmUUIDFromVLMConfig(vlmCfg, svmName),
			},
		}
		// VCM does not create Cloud DNS records (a GCP resource owned by CLH), so
		// we cannot dial a per-node FQDN. Instead use the SNI-override approach
		// (same as OCI): dial the node mgmt IP directly and set the TLS SNI /
		// hostname-verification name to "mgmt.<deploymentID>.<VsaDeployedDnsName>",
		// which matches the wildcard SAN ("*.<deploymentID>.<VsaDeployedDnsName>")
		// baked into the pool's certificate. This keeps full mTLS with server-cert
		// verification intact — no InsecureSkipVerify, no DNS record.
		node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes: []*datamodel.Node{
				{EndpointAddress: forwardingRuleIP, HostDNSName: forwardingRuleIP},
			},
			DeploymentName:   deploymentID,
			OntapCredentials: wf.ontapNodeCredentials(),
			ServerName:       vsa.OntapSNIServerName(deploymentID),
		})

		if wf.inputs.IsOntapMode {
			if err := wf.applyNfsTransferSize(ctx, sa, svm, node); err != nil {
				return err
			}
		}

		dbPool := &datamodel.Pool{
			Name:    deploymentID,
			QosType: qosType,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: wf.req.ThroughputMibps,
				Iops:            wf.req.Iops,
			},
		}
		// CreateQoSPolicyAndApplyToSVM skips the SVM-wide ceiling for manual QoS
		// unless the pool is ONTAP mode, because ONTAP-mode tenants reach the
		// cluster directly and are bounded only by that ceiling.
		if wf.inputs.IsOntapMode {
			dbPool.APIAccessMode = commonparams.ONTAPMode
		}

		if err := checkDeadline(ctx, wf.req.Deadline, "create-qos-policy"); err != nil {
			return err
		}

		wf.setStep(ctx, jobStepCreateQoSPolicy)
		if err := orchestrator.ExecuteActivity(ctx, sa.CreateQoSPolicyAndApplyToSVM, dbPool, svm, node).Get(ctx, nil); err != nil {
			logger.Error("workflow: create QoS policy and apply to SVM failed",
				"deploymentName", deploymentID, "svmName", svmName, "error", err)
			return fmt.Errorf("create QoS policy and apply to SVM: %w", err)
		}
		logger.Info("workflow: QoS policy created and applied to SVM", "svmName", svmName, "qosType", qosType)
	}

	if wf.inputs.IsOntapMode {
		if err := checkDeadline(ctx, wf.req.Deadline, "create-ontap-mode-user"); err != nil {
			return err
		}
		if err := wf.provisionOntapModeUser(ctx, ontapCreds, ontapModeCreds,
			ontapModeUsername, ontapModeAuthenticationType); err != nil {
			return err
		}
	}

	return nil
}

// validateOntapModeInputs rejects an ONTAP-mode pool whose dedicated user or
// RBAC role file is not fully specified, before any provisioning starts.
//
// CreatePoolParams already rejects these at the API boundary. The check is
// repeated here because crash recovery re-dispatches a persisted request
// straight into the workflow, which does not pass through that boundary again.
func (wf *createPoolWorkflow) validateOntapModeInputs() error {
	if !wf.inputs.IsOntapMode {
		return nil
	}
	if wf.inputs.OntapModeCredentials == nil {
		return fmt.Errorf("provision ONTAP mode user: ONTAP_MODE credential is missing")
	}
	if strings.TrimSpace(wf.req.OntapModeRbacFileURL) == "" {
		return fmt.Errorf("provision ONTAP mode user: RBAC file URL is missing")
	}
	if strings.TrimSpace(wf.req.OntapModeRbacChecksum) == "" {
		return fmt.Errorf("provision ONTAP mode user: RBAC file checksum is missing")
	}
	return nil
}

// applyNfsTransferSize raises the SVM's NFS TCP max transfer size.
//
// On the managed path the control plane does this when the first NFS volume is
// created. ONTAP-mode tenants create volumes directly on the cluster, so VCM
// never observes that first create and pool create is the only opportunity —
// the same reason VCP applies it here for ONTAP-mode pools. The setting is
// SVM-scoped and independent of whether a NAS LIF exists yet.
func (wf *createPoolWorkflow) applyNfsTransferSize(
	ctx *orchestrator.Context,
	sa *activities.SvmActivity,
	svm *datamodel.Svm,
	node *coremodels.Node,
) error {
	if err := checkDeadline(ctx, wf.req.Deadline, jobStepNfsTransferSize); err != nil {
		return err
	}

	wf.setStep(ctx, jobStepNfsTransferSize)
	logger := wfLogger(ctx)
	if err := orchestrator.ExecuteActivity(ctx, sa.UpdateNFSRWSizeOnSVM, svm, node).Get(ctx, nil); err != nil {
		logger.Error("workflow: update NFS TCP max transfer size on SVM failed",
			"deploymentName", wf.req.DeploymentID, "svmName", svm.Name, "error", err)
		return fmt.Errorf("update NFS TCP max transfer size on SVM: %w", err)
	}
	logger.Info("workflow: NFS TCP max transfer size applied to SVM", "svmName", svm.Name)
	return nil
}

// provisionOntapModeUser creates the dedicated ONTAP-mode user and applies its
// restricted RBAC role through the VLM expert-mode child workflow. The admin
// credentials authenticate the call; the mode credentials are the material the
// new user is created with.
//
// The guards below cover this function's arguments, which are resolved material
// rather than the request fields validateOntapModeInputs already checked.
func (wf *createPoolWorkflow) provisionOntapModeUser(
	ctx *orchestrator.Context,
	adminCredentials vlm.OntapCredentials,
	modeCredentials vlm.OntapCredentials,
	username string,
	authenticationType string,
) error {
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("provision ONTAP mode user: username is missing")
	}
	switch authenticationType {
	case vlmAuthenticationTypeCertificate:
		if modeCredentials.Certificate.Certificate == "" {
			return fmt.Errorf("provision ONTAP mode user: resolved certificate is empty")
		}
	case vlmAuthenticationTypePassword:
		if modeCredentials.AdminPassword == "" {
			return fmt.Errorf("provision ONTAP mode user: resolved password is empty")
		}
	default:
		return fmt.Errorf("provision ONTAP mode user: unsupported authentication type %q", authenticationType)
	}

	wf.setStep(ctx, jobStepCreateOntapModeUser)
	logger := wfLogger(ctx)
	logger.Info("workflow: invoking VLM ONTAP mode user child workflow",
		"deploymentName", wf.req.DeploymentID,
		"username", username,
		"authenticationType", authenticationType)

	_, err := vcmclient.InvokeCreateExpertModeUser(ctx, &vlm.OntapExpertModeUserConfig{
		VLMConfig:                 wf.vlmCfg,
		OntapCredentials:          adminCredentials,
		ExpertModeUserCredentials: modeCredentials,
		Username:                  username,
		AuthenticationType:        authenticationType,
		RbacFileURL:               wf.req.OntapModeRbacFileURL,
		RbacFileChecksum:          wf.req.OntapModeRbacChecksum,
	})
	if err != nil {
		logger.Error("workflow: VLM ONTAP mode user creation failed",
			"deploymentName", wf.req.DeploymentID,
			"username", username,
			"authenticationType", authenticationType,
			"error", err)
		return fmt.Errorf("VLM ONTAP mode user creation: %w", err)
	}
	logger.Info("workflow: VLM ONTAP mode user created",
		"deploymentName", wf.req.DeploymentID,
		"username", username,
		"authenticationType", authenticationType)
	return nil
}

// setStep updates the durable job's current_step (best-effort; a failed update
// is logged but does not abort provisioning).
func (wf *createPoolWorkflow) setStep(ctx *orchestrator.Context, step string) {
	wf.currentStep = step
	if err := wf.UpdateJobStatus(ctx, vcmdb.JobStateProcessing, step, nil, nil); err != nil {
		wfLogger(ctx).Warn("workflow: failed to update job step",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID, "step", step, "error", err)
	}
}

// jobErrorFields extracts durable job error metadata from a workflow failure.
func jobErrorFields(err error) (trackingID int, message string, retriable bool) {
	trackingID, retriable, message = vsaerrors.ExtractJobFailureDetails(err)
	return trackingID, message, retriable
}

// resolveOntapCredentials returns the ONTAP admin credentials for the VLM child
// workflows: from env locally, else via a Secret Manager activity keyed on the
// credential's auth type. The credentials are carried as vlm.OntapCredentials so
// the resolved material is encrypted in the workflow history (see the activity
// doc). Never logged.
//
// Certificate-auth path resolves the cert material (signed cert, key, chain, CN)
// plus the bootstrap admin password. VLM's CreateVSAClustersWorkflow runs
// CheckAndEnableCertAuthWorkflow whenever OntapCredentials.Certificate is set:
// it installs the server cert, installs the issuing CA as client-ca (so ONTAP
// trusts our client cert), and creates the cert-mapped ONTAP user. Its live
// ONTAP calls (e.g. GetVSAClusterDeployment) authenticate with the cert too, so
// a cert-auth cluster must resolve the certificate rather than a password.
func resolveOntapCredentials(ctx *orchestrator.Context, poolCreds *datamodel.PoolCredentials) (vlm.OntapCredentials, error) {
	if isLocalEnv() {
		return vlm.OntapCredentials{AdminPassword: vcmLocalOntapPassword}, nil
	}

	var ca *activities.VCMCredentialActivity

	if poolCreds != nil && poolCreds.AuthType == env.USER_CERTIFICATE && poolCreds.CertificateID != "" {
		var creds vlm.OntapCredentials
		if err := orchestrator.ExecuteActivity(ctx, ca.ResolveOntapCertificate, activities.ResolveOntapCertificateRequest{
			PoolCredentials: poolCreds,
		}).Get(ctx, &creds); err != nil {
			return vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP certificate: %w", err)
		}
		if creds.Certificate.Certificate == "" {
			return vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP certificate: resolved certificate is empty")
		}
		return creds, nil
	}

	if poolCreds == nil || poolCreds.SecretID == "" {
		return vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP admin password: request has no admin credential secret id")
	}

	var creds vlm.OntapCredentials
	if err := orchestrator.ExecuteActivity(ctx, ca.ResolveOntapAdminPassword, activities.ResolveOntapAdminPasswordRequest{
		SecretResourceName: poolCreds.SecretID,
	}).Get(ctx, &creds); err != nil {
		return vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP admin password: %w", err)
	}
	if creds.AdminPassword == "" {
		return vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP admin password: resolved value is empty")
	}
	return creds, nil
}

// resolveOntapModeCredentials dereferences the dedicated ONTAP-mode credential,
// returning the user's name, its authentication type and the resolved material.
//
// Locally it uses VCM_LOCAL_ONTAP_MODE_USERNAME / VCM_LOCAL_ONTAP_MODE_PASSWORD
// instead of Secret Manager, mirroring the admin path (resolveOntapCredentials)
// and the local passthrough. The invariant is preserved either way: the ONTAP
// Mode passthrough authenticates from the same source on every request, so pool
// creation must provision the user from identical material or the two paths
// disagree and requests fail with 401. Local therefore forces password auth for
// the user, exactly as the admin path does.
func resolveOntapModeCredentials(
	ctx *orchestrator.Context,
	poolCreds *datamodel.PoolCredentials,
) (string, string, vlm.OntapCredentials, error) {
	if isLocalEnv() {
		return vcmLocalOntapModeUsername, vlmAuthenticationTypePassword,
			vlm.OntapCredentials{AdminPassword: vcmLocalOntapModePassword}, nil
	}
	if poolCreds == nil {
		return "", "", vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP mode credentials: credential is missing")
	}

	var ca *activities.VCMCredentialActivity
	if poolCreds.AuthType == env.USER_CERTIFICATE && poolCreds.CertificateID != "" {
		var creds vlm.OntapCredentials
		if err := orchestrator.ExecuteActivity(ctx, ca.ResolveOntapCertificate, activities.ResolveOntapCertificateRequest{
			PoolCredentials: poolCreds,
		}).Get(ctx, &creds); err != nil {
			return "", "", vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP mode certificate: %w", err)
		}
		// The cert-mapped ONTAP user must be named for the subject the client
		// certificate presents: ONTAP maps an incoming client certificate to an
		// account by common name, so a user created under the separately
		// configured username would never authenticate.
		if strings.TrimSpace(creds.Certificate.CommonName) == "" {
			return "", "", vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP mode certificate: resolved common name is empty")
		}
		if creds.Certificate.Certificate == "" {
			return "", "", vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP mode certificate: resolved certificate is empty")
		}
		return creds.Certificate.CommonName, vlmAuthenticationTypeCertificate, creds, nil
	}

	if poolCreds.SecretID == "" {
		return "", "", vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP mode password: credential secret id is missing")
	}
	if strings.TrimSpace(poolCreds.Username) == "" {
		return "", "", vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP mode password: username is missing")
	}

	var creds vlm.OntapCredentials
	if err := orchestrator.ExecuteActivity(ctx, ca.ResolveOntapAdminPassword, activities.ResolveOntapAdminPasswordRequest{
		SecretResourceName: poolCreds.SecretID,
	}).Get(ctx, &creds); err != nil {
		return "", "", vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP mode password: %w", err)
	}
	if creds.AdminPassword == "" {
		return "", "", vlm.OntapCredentials{}, fmt.Errorf("resolve ONTAP mode password: resolved value is empty")
	}
	return poolCreds.Username, vlmAuthenticationTypePassword, creds, nil
}

// ontapLicenseForCreate returns the license VLM installs at cluster create.
// An empty secret URI yields an empty license so VLM falls back to its
// built-in default license. A URI that already names a version is passed
// through unchanged; an unversioned URI is pinned to its latest version.
func ontapLicenseForCreate(secretURI string) vlm.OntapLicense {
	if secretURI == "" {
		return vlm.OntapLicense{}
	}
	if !strings.Contains(secretURI, secretVersionSegment) {
		secretURI += latestSecretVersionSuffix
	}
	return vlm.OntapLicense{SecretUri: []string{secretURI}}
}

// mapQosType maps FIXED/MANUAL to manual QoS and everything else to auto.
func mapQosType(apiQosType string) string {
	switch strings.ToUpper(strings.TrimSpace(apiQosType)) {
	case "MANUAL", "FIXED":
		return utils.QosTypeManual
	default:
		return utils.QosTypeAuto
	}
}

// nodeMgmtIPFromVLMConfig returns the first non-empty node-management LIF IP
// across the HA-pair VMs.
func nodeMgmtIPFromVLMConfig(cfg vlm.VLMConfig) string {
	for _, hp := range cfg.Cloud.HAPairs {
		for _, vm := range []vlm.VMConfig{hp.VM1, hp.VM2} {
			if lif, ok := vm.SystemLIFs[vlm.LIFTypeNodeMgmt]; ok && lif.IP != "" {
				return lif.IP
			}
		}
	}
	return ""
}

// svmUUIDFromVLMConfig resolves the SVM external UUID from the VLM config,
// matching on map key, then SVM name, then the sole entry.
func svmUUIDFromVLMConfig(cfg vlm.VLMConfig, svmName string) string {
	if c, ok := cfg.Svm[svmName]; ok {
		return c.Svmuuid
	}
	for _, c := range cfg.Svm {
		if c.Svmname == svmName {
			return c.Svmuuid
		}
	}
	for _, c := range cfg.Svm {
		return c.Svmuuid
	}
	return ""
}

// ontapNodeCredentials returns the ONTAP node credentials for the
// create-qos-policy step: env username/password locally, else a copy of the
// orchestrator-derived credentials (identifiers only).
//
// The QoS step reaches Secret Manager through vsa.GetProviderByNode. VCM
// receives its password credential as a fully qualified resource name
// (projects/{tenant}/secrets/{secret}) in the tenant project; the password
// lookup is resource-name-aware and reads the project from the name, so the
// full resource name is passed through unchanged. Cluster and SVM create are
// also unaffected — resolveOntapCredentials reads the fully qualified name
// straight from wf.inputs.OntapCredentials.
func (wf *createPoolWorkflow) ontapNodeCredentials() *datamodel.PoolCredentials {
	return nodeCredentialsFromInputs(wf.inputs)
}

// nodeCredentialsFromInputs builds the ONTAP node credentials described on
// createPoolWorkflow.ontapNodeCredentials from derived pool inputs, so every
// workflow that calls ONTAP directly resolves them the same way.
func nodeCredentialsFromInputs(inputs *commonparams.VCMDerivedPoolInputs) *datamodel.PoolCredentials {
	if isLocalEnv() {
		return &datamodel.PoolCredentials{
			Username: vcmLocalOntapUsername,
			Password: vcmLocalOntapPassword,
			AuthType: env.USERNAME_PWD,
		}
	}
	if inputs == nil || inputs.OntapCredentials == nil {
		return nil
	}

	// Copy: the same pointer feeds resolveOntapCredentials, which needs the
	// fully qualified names.
	nodeCreds := *inputs.OntapCredentials
	// VCM-provided certs carry the full CAS certificate resource name in
	// CertificateID (and the private key's full Secret Manager resource name in
	// CertPrivateKeySecretID), so vsa.GetProvidedCertificate can parse both.
	// Only bare-name the certificate id on the legacy self-managed path (no
	// provided private key), where vsa formats
	// projects/{SECRET_MANAGER_PROJECT_ID}/secrets/{id}.
	if nodeCreds.CertPrivateKeySecretID == "" {
		nodeCreds.CertificateID = utils.BareSecretName(nodeCreds.CertificateID)
	}
	return &nodeCreds
}
