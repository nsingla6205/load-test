// Package vcm_workflows hosts the workflow definitions backing the VCM-proxy
// endpoints.
package vcm_workflows

import (
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	vcmclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm/vcm-client"
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
	jobStepStarting        = "starting"
	jobStepCreateCluster   = "create-cluster-deployment"
	jobStepCreateSVM       = "create-svm"
	jobStepCreateQoSPolicy = "create-qos-policy"
	jobStepCompleted       = "completed"
)

var (
	runningEnv            = env.GetString("ENV", "")
	vcmLocalOntapUsername = env.GetString("VCM_LOCAL_ONTAP_USERNAME", "")
	vcmLocalOntapPassword = env.GetString("VCM_LOCAL_ONTAP_PASSWORD", "")

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

	logger := wfLogger(ctx)
	p := wf.req
	deploymentID := p.DeploymentID

	if err := wf.checkDeadline(ctx, "start"); err != nil {
		return err
	}

	ontapCreds, err := resolveOntapCredentials(ctx, wf.inputs.OntapCredentials)
	if err != nil {
		logger.Error("workflow: resolve ONTAP admin password failed",
			"deploymentName", deploymentID, "error", err)
		return err
	}

	if err := wf.checkDeadline(ctx, "identify-vms"); err != nil {
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

	if err := wf.checkDeadline(ctx, "create-cluster-deployment"); err != nil {
		return err
	}

	wf.setStep(ctx, jobStepCreateCluster)
	logger.Info("workflow: invoking VLM create-cluster deployment child workflow",
		"deploymentName", deploymentID, "numHAPair", vlmCfg.Deployment.NumHAPair)
	depRes, err := vcmclient.InvokeCreateClusterDeployment(ctx,
		&vlm.CreateVSAClusterDeploymentRequest{
			VLMConfig:        vlmCfg,
			OntapCredentials: ontapCreds,
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

	if err := wf.checkDeadline(ctx, "create-svm"); err != nil {
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
	if qosType != utils.QosTypeManual {
		dbPool := &datamodel.Pool{
			Name:    deploymentID,
			QosType: qosType,
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: wf.req.ThroughputMibps,
				Iops:            wf.req.Iops,
			},
		}
		svm := &datamodel.Svm{
			Name: svmName,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: svmUUIDFromVLMConfig(vlmCfg, svmName),
			},
		}
		// TODO(vcm-dns): Temporary workaround until DNS handling for VCM is decided.
		// VCM does not create Cloud DNS records (a GCP resource owned by CLH), so we
		// cannot dial a per-node FQDN whose TLS SNI matches the cert's wildcard SAN.
		// Instead dial the node mgmt IP directly (HostDNSName = IP => hosts map
		// {IP: IP}) and disable server-cert verification (InsecureSkipVerify = true),
		// mirroring the password/insecure path. Client mTLS (ONTAP verifying our
		// client cert) is unaffected; only server-identity verification is skipped.
		// Revert to FQDN+DNS (or an SNI-override) once the DNS decision lands.
		node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes: []*datamodel.Node{
				{EndpointAddress: forwardingRuleIP, HostDNSName: forwardingRuleIP},
			},
			DeploymentName:     deploymentID,
			OntapCredentials:   wf.ontapNodeCredentials(),
			InsecureSkipVerify: true,
		})

		if err := wf.checkDeadline(ctx, "create-qos-policy"); err != nil {
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

// checkDeadline aborts the workflow before entering step when the request's
// deadline has already passed, so a create-pool that can no longer finish in
// time fails fast instead of provisioning further resources. The clock comes
// from the workflow backend to stay deterministic across replays.
//
// The check only runs at step boundaries: a step that overruns the deadline
// while in flight is not interrupted, it is caught at the next boundary.
func (wf *createPoolWorkflow) checkDeadline(ctx *orchestrator.Context, step string) error {
	now := orchestrator.Now(ctx).UTC()
	err := deadlineExceeded(now, wf.req.Deadline, step)
	if err != nil {
		wfLogger(ctx).Error("workflow: create-pool deadline exceeded",
			"deploymentName", wf.req.DeploymentID, "requestId", wf.req.RequestID,
			"step", step, "deadline", wf.req.Deadline, "now", now)
	}
	return err
}

// deadlineExceeded reports a deadline-exceeded failure for step when deadline
// has elapsed at now. A zero deadline means no deadline was requested and is
// never enforced.
func deadlineExceeded(now, deadline time.Time, step string) error {
	if deadline.IsZero() || now.Before(deadline.UTC()) {
		return nil
	}
	return vsaerrors.NewVCPError(vsaerrors.ErrOperationDeadlineExceeded,
		fmt.Errorf("create-pool deadline exceeded before %s: deadline=%s now=%s",
			step, deadline.UTC().Format(time.RFC3339), now.Format(time.RFC3339)))
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
// orchestrator-derived credentials (identifiers only) whose secret identifiers
// have been reduced to bare secret names.
//
// The QoS step reaches Secret Manager through vsa.GetProviderByNode, which
// formats projects/{SECRET_MANAGER_PROJECT_ID}/secrets/{id} from the node's
// identifiers. VCM receives its credentials as fully qualified resource names,
// so handing one through unchanged yields
// projects/{p}/secrets/projects/{q}/secrets/... and a 404. Reducing to the bare
// name keeps that formatting valid.
//
// This assumes the secret lives in SECRET_MANAGER_PROJECT_ID: the project
// segment of the resource name is dropped here, not honoured. Cluster and SVM
// create are unaffected — resolveOntapCredentials reads the fully qualified name
// straight from wf.inputs.OntapCredentials.
func (wf *createPoolWorkflow) ontapNodeCredentials() *datamodel.PoolCredentials {
	if isLocalEnv() {
		return &datamodel.PoolCredentials{
			Username: vcmLocalOntapUsername,
			Password: vcmLocalOntapPassword,
			AuthType: env.USERNAME_PWD,
		}
	}
	if wf.inputs == nil || wf.inputs.OntapCredentials == nil {
		return nil
	}

	// Copy: the same pointer feeds resolveOntapCredentials, which needs the
	// fully qualified names.
	nodeCreds := *wf.inputs.OntapCredentials
	nodeCreds.SecretID = utils.BareSecretName(nodeCreds.SecretID)
	nodeCreds.CertificateID = utils.BareSecretName(nodeCreds.CertificateID)
	return &nodeCreds
}
