package vcm_workflows

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

// e2eJobStore is an in-memory vcmdb.VCMDatastore used to drive the durable
// job-state activities (GetJob / UpdateJobProgress) that the VCM pool workflow
// calls through the go-workflows engine. It records every UpdateJobProgress so
// tests can assert the terminal state the workflow persisted.
type e2eJobStore struct {
	mu      sync.Mutex
	jobs    map[string]*vcmdb.Job
	updates []vcmdb.UpdateJobProgressParams
}

func newE2EJobStore(seed ...*vcmdb.Job) *e2eJobStore {
	s := &e2eJobStore{jobs: map[string]*vcmdb.Job{}}
	for _, j := range seed {
		s.jobs[j.ID] = j
	}
	return s
}

func (s *e2eJobStore) CreateJob(_ context.Context, job *vcmdb.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
	return nil
}

func (s *e2eJobStore) GetJob(_ context.Context, jobID string) (*vcmdb.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return nil, nil
	}
	clone := *job
	return &clone, nil
}

func (s *e2eJobStore) GetOngoingJobByResource(_ context.Context, resourceID, jobType string) (*vcmdb.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.jobs {
		if job.ResourceID == resourceID && job.Type == jobType && !job.IsTerminal() {
			clone := *job
			return &clone, nil
		}
	}
	return nil, nil
}

func (s *e2eJobStore) ListOngoingJobs(context.Context) ([]vcmdb.Job, error) { return nil, nil }

func (s *e2eJobStore) UpdateJobProgress(_ context.Context, params vcmdb.UpdateJobProgressParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, params)
	if job, ok := s.jobs[params.JobID]; ok {
		job.State = params.State
		job.CurrentStep = params.Step
		if vcmdb.HasPersistablePayload(params.ResponsePayload) {
			job.ResponsePayload = params.ResponsePayload
		}
	}
	return nil
}

func (s *e2eJobStore) lastUpdate() (vcmdb.UpdateJobProgressParams, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.updates) == 0 {
		return vcmdb.UpdateJobProgressParams{}, false
	}
	return s.updates[len(s.updates)-1], true
}

func (s *e2eJobStore) allUpdates() []vcmdb.UpdateJobProgressParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]vcmdb.UpdateJobProgressParams(nil), s.updates...)
}

func (s *e2eJobStore) states() []vcmdb.JobState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]vcmdb.JobState, 0, len(s.updates))
	for _, u := range s.updates {
		out = append(out, u.State)
	}
	return out
}

// runPoolWorkflowE2E registers PoolWorkflow plus the durable job activities on
// an in-memory go-workflows runner, executes one instance, and returns the
// workflow's terminal error (nil on success).
func runPoolWorkflowE2E(t *testing.T, store vcmdb.VCMDatastore, params *commonparams.CreatePoolParams) error {
	return runPoolWorkflowE2EWithReg(t, store, params, nil)
}

// runPoolWorkflowE2EWithReg is runPoolWorkflowE2E with an optional hook to
// register extra stub activities/child workflows (e.g. for the happy path that
// drives provision() to completion).
func runPoolWorkflowE2EWithReg(t *testing.T, store vcmdb.VCMDatastore, params *commonparams.CreatePoolParams, extra func(orchestrator.WorkflowRegistrar)) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := r.Registrar()
	reg.RegisterWorkflow(PoolWorkflow)
	ja := &activities.JobActivity{Store: store}
	reg.RegisterActivity(ja.GetJob)
	reg.RegisterActivity(ja.UpdateJobProgress)
	if extra != nil {
		extra(reg)
	}

	require.NoError(t, r.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = r.Stop()
	})

	wfi, err := r.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: uuid.NewString(),
	}, gowfbackend.FuncName(PoolWorkflow), params)
	require.NoError(t, err)

	_, werr := cclient.GetWorkflowResult[json.RawMessage](ctx, r.Client(), wfi, 30*time.Second)
	return werr
}

func runPoolUpdateWorkflowE2E(
	t *testing.T,
	store vcmdb.VCMDatastore,
	params *commonparams.UpdatePoolParams,
	extra func(orchestrator.WorkflowRegistrar),
) error {
	t.Helper()
	t.Setenv("PROVIDER", vlm.GCPCloud)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := r.Registrar()
	reg.RegisterWorkflow(PoolUpdateWorkflow)
	ja := &activities.JobActivity{Store: store}
	reg.RegisterActivity(ja.UpdateJobProgress)
	extra(reg)

	require.NoError(t, r.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = r.Stop()
	})

	wfi, err := r.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: uuid.NewString(),
	}, gowfbackend.FuncName(PoolUpdateWorkflow), params)
	require.NoError(t, err)

	_, werr := cclient.GetWorkflowResult[json.RawMessage](ctx, r.Client(), wfi, 30*time.Second)
	return werr
}

// --- happy-path stubs ---
//
// The gowf backend dispatches activities/child workflows by their SHORT name
// (trailing identifier). These stubs therefore share the exact method name of
// the real activity so the workflow's dispatch-by-value resolves to them, while
// the real (cloud-touching) implementations stay unregistered.

// stubPoolActivity stands in for PoolActivity.IdentifyVMs in e2e tests; set
// identifyErr to simulate a provisioning failure without a separate stub type.
type stubPoolActivity struct {
	identifyErr        error
	validateErr        error
	targetInstanceType string
	targetSPConfig     vlm.SPConfig
}

func (a stubPoolActivity) IdentifyVMs(_ context.Context, req activities.IdentifyVMsRequest) (*vlm.VLMConfig, error) {
	if a.identifyErr != nil {
		return nil, a.identifyErr
	}
	cfg := &vlm.VLMConfig{}
	cfg.VsaCluster.ClusterName = "cluster-e2e"
	// InvokeCreateSVM requires a non-empty deployment id on the VLM config.
	cfg.Deployment.DeploymentID = req.DeploymentName
	cfg.Deployment.Provider = vlm.GCPCloud
	cfg.Deployment.VSAInstanceType = a.targetInstanceType
	cfg.Deployment.SPConfig = a.targetSPConfig
	return cfg, nil
}

func (a stubPoolActivity) ValidateZonesForMachineTypes(context.Context, string, string, string, string) error {
	return a.validateErr
}

type fakeCredentialActivity struct{}

func (fakeCredentialActivity) ResolveOntapAdminPassword(context.Context, activities.ResolveOntapAdminPasswordRequest) (*vlm.OntapCredentials, error) {
	return &vlm.OntapCredentials{AdminPassword: "resolved-password"}, nil
}

type fakeSvmActivity struct{}

func (fakeSvmActivity) CreateQoSPolicyAndApplyToSVM(_ context.Context, _ *datamodel.Pool, _ *datamodel.Svm, _ *coremodels.Node) error {
	return nil
}

func (fakeSvmActivity) UpdateNFSRWSizeOnSVM(_ context.Context, _ *datamodel.Svm, _ *coremodels.Node) error {
	return nil
}

type failingSvmActivity struct{}

func (failingSvmActivity) CreateQoSPolicyAndApplyToSVM(_ context.Context, _ *datamodel.Pool, _ *datamodel.Svm, _ *coremodels.Node) error {
	return errors.New("qos activity called")
}

type failingNfsSvmActivity struct{}

func (failingNfsSvmActivity) UpdateNFSRWSizeOnSVM(_ context.Context, _ *datamodel.Svm, _ *coremodels.Node) error {
	return errors.New("nfs activity called")
}

type fakeOntapModeCredentialActivity struct{}

func (fakeOntapModeCredentialActivity) ResolveOntapAdminPassword(
	_ context.Context,
	_ activities.ResolveOntapAdminPasswordRequest,
) (*vlm.OntapCredentials, error) {
	return &vlm.OntapCredentials{AdminPassword: "ModePassword123!"}, nil
}

func (fakeOntapModeCredentialActivity) ResolveOntapCertificate(
	_ context.Context,
	_ activities.ResolveOntapCertificateRequest,
) (*vlm.OntapCredentials, error) {
	return &vlm.OntapCredentials{
		Certificate: vlm.OntapCertificate{
			CommonName:  "mode-cert-user",
			Certificate: "signed-certificate",
			PrivateKey:  "private-key",
		},
	}, nil
}

// partialOntapModeCredentialActivity resolves the admin secret to a usable
// password but the ONTAP-mode secret to an empty one, the shape a
// present-but-unusable ONTAP-mode secret produces. Branching on the resource
// name lets admin resolution succeed so the failure is pinned to the ONTAP-mode
// credential rather than the admin one.
type partialOntapModeCredentialActivity struct{}

func (partialOntapModeCredentialActivity) ResolveOntapAdminPassword(
	_ context.Context,
	req activities.ResolveOntapAdminPasswordRequest,
) (*vlm.OntapCredentials, error) {
	if strings.Contains(req.SecretResourceName, "mode") {
		return &vlm.OntapCredentials{}, nil
	}
	return &vlm.OntapCredentials{AdminPassword: "AdminPassword123!"}, nil
}

func childCreateCluster(_ *orchestrator.Context, req *vlm.CreateVSAClusterDeploymentRequest) (*vlm.CreateVSAClusterDeploymentResponse, error) {
	cfg := req.VLMConfig
	if len(cfg.Cloud.HAPairs) == 0 {
		cfg.Cloud.HAPairs = []vlm.HAPair{{
			VM1: vlm.VMConfig{
				Name:      "vm1",
				NodeIndex: 1,
				SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
					vlm.LIFTypeNodeMgmt: {IP: "10.0.0.10"},
				},
			},
			VM2: vlm.VMConfig{
				Name:      "vm2",
				NodeIndex: 2,
				SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
					vlm.LIFTypeNodeMgmt: {IP: "10.0.0.11"},
				},
			},
		}}
	}
	return &vlm.CreateVSAClusterDeploymentResponse{VLMConfig: cfg}, nil
}

func childCreateSVM(_ *orchestrator.Context, req *vlm.CreateSVMRequest) (*vlm.CreateSVMResponse, error) {
	cfg := req.VLMConfig
	svmName := req.Name
	cfg.Svm = map[string]vlm.SvmConfig{
		svmName: {
			Svmname: svmName,
			SVMLIFs: vlm.SvmLIFConfigs{
				vlm.LIFTypeSan: {
					{IP: "10.33.182.252"},
					{IP: "10.33.182.249"},
				},
				vlm.LIFTypeIlbNas: {
					{IP: "10.33.182.243"},
				},
			},
		},
	}
	return &vlm.CreateSVMResponse{VLMConfig: cfg}, nil
}

func childGetClusterForUpdate(_ *orchestrator.Context, req *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	cfg := vlm.VLMConfig{}
	cfg.Deployment.Provider = vlm.GCPCloud
	cfg.Deployment.DeploymentID = req.DeploymentID
	cfg.Deployment.NumHAPair = 1
	cfg.Deployment.VSAInstanceType = "n2-standard-8"
	cfg.Deployment.SPConfig = vlm.SPConfig{Size: "2TiB", IOps: 1000, Throughput: 64}
	return &vlm.GetVSAClusterDeploymentResponse{VLMConfig: cfg}, nil
}

// childUpdateClusterForPool models UpdateVSAClusterDeploymentWorkflow: the
// desired change is the top-level NewInstanceType and SPConfig, and
// req.VLMConfig stays the live cluster. The returned config is what VLM
// writes back after applying those top-level fields.
func childUpdateClusterForPool(_ *orchestrator.Context, req *vlm.UpdateVSAClusterDeploymentRequest) (*vlm.UpdateVSAClusterDeploymentResponse, error) {
	cfg := req.VLMConfig
	if req.NewInstanceType != "" {
		cfg.Deployment.VSAInstanceType = req.NewInstanceType
	}
	cfg.Deployment.SPConfig = req.SPConfig
	return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: cfg}, nil
}

func newUpdatePoolParams(requestID, changeType string) *commonparams.UpdatePoolParams {
	createParams := commonparams.VCMCreatePoolParams{
		DeploymentID:         "gcnv-e2e",
		RequestID:            requestID,
		ServiceTenantProject: "tenant-project",
		PrimaryZone:          "us-central1-a",
		SecondaryZone:        "us-central1-b",
		MediatorZone:         "us-central1-c",
		SizeInBytes:          4096 * 1024 * 1024 * 1024,
		ThroughputMibps:      128,
		Iops:                 2000,
		Derived: &commonparams.VCMDerivedPoolInputs{
			Region:               "us-central1",
			DesiredCapacityInGiB: 4096,
			OntapCredentials: &datamodel.PoolCredentials{
				SecretID: "projects/test/secrets/admin-password",
			},
		},
	}
	return &commonparams.UpdatePoolParams{VCM: &commonparams.VCMUpdatePoolParams{
		VCMCreatePoolParams: createParams,
		ChangeType:          changeType,
	}}
}

func registerPoolUpdateDependencies(reg orchestrator.WorkflowRegistrar, pool stubPoolActivity) {
	reg.RegisterActivity(fakeCredentialActivity{}.ResolveOntapAdminPassword)
	reg.RegisterActivity(pool.IdentifyVMs)
	reg.RegisterActivity(pool.ValidateZonesForMachineTypes)
	reg.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, childGetClusterForUpdate)
	reg.RegisterWorkflowWithName(vlm.UpdateVSAClusterDeploymentWorkflowName, childUpdateClusterForPool)
}

func TestPoolUpdateWorkflowE2E_ComputeSuccess_MarksJobDone(t *testing.T) {
	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	params := newUpdatePoolParams(reqID, ClusterComputeChangeType)
	params.VCM.Deadline = time.Now().UTC().Add(time.Hour)

	err := runPoolUpdateWorkflowE2E(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
		registerPoolUpdateDependencies(reg, stubPoolActivity{
			targetInstanceType: "c3-standard-8-lssd",
			targetSPConfig:     vlm.SPConfig{Size: "4TiB", IOps: 2000, Throughput: 128},
		})
	})

	require.NoError(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateDone, last.State)
	assert.Equal(t, jobStepCompleted, last.Step)
	assert.NotEmpty(t, last.ResponsePayload)
}

func TestPoolUpdateWorkflowE2E_ScaleSuccess_MarksJobDone(t *testing.T) {
	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	params := newUpdatePoolParams(reqID, ClusterScaleChangeType)

	err := runPoolUpdateWorkflowE2E(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
		registerPoolUpdateDependencies(reg, stubPoolActivity{
			targetInstanceType: "n2-standard-8",
			targetSPConfig:     vlm.SPConfig{Size: "4TiB", IOps: 2000, Throughput: 128},
		})
	})

	require.NoError(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateDone, last.State)
	assert.Equal(t, jobStepCompleted, last.Step)
}

func TestPoolUpdateWorkflowE2E_NoOpUpdates_MarkJobDone(t *testing.T) {
	tests := []struct {
		name       string
		changeType string
		target     stubPoolActivity
	}{
		{
			name:       "compute keeps current instance type",
			changeType: ClusterComputeChangeType,
			target: stubPoolActivity{
				targetInstanceType: "n2-standard-8",
				targetSPConfig:     vlm.SPConfig{Size: "4TiB", IOps: 2000, Throughput: 128},
			},
		},
		{
			name:       "scale calls VLM when storage config already matches",
			changeType: ClusterScaleChangeType,
			target: stubPoolActivity{
				targetInstanceType: "n2-standard-8",
				targetSPConfig:     vlm.SPConfig{Size: "2TiB", IOps: 1000, Throughput: 64},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqID := uuid.NewString()
			store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
			params := newUpdatePoolParams(reqID, tt.changeType)

			err := runPoolUpdateWorkflowE2E(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
				registerPoolUpdateDependencies(reg, tt.target)
			})

			require.NoError(t, err)
			last, ok := store.lastUpdate()
			require.True(t, ok)
			assert.Equal(t, vcmdb.JobStateDone, last.State)
			assert.Equal(t, jobStepCompleted, last.Step)
		})
	}
}

func TestPoolUpdateWorkflowE2E_UnsupportedChangeType_MarksJobError(t *testing.T) {
	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	params := newUpdatePoolParams(reqID, "CONFIG")

	err := runPoolUpdateWorkflowE2E(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
		registerPoolUpdateDependencies(reg, stubPoolActivity{
			targetInstanceType: "n2-standard-8",
			targetSPConfig:     vlm.SPConfig{Size: "2TiB", IOps: 1000, Throughput: 64},
		})
	})

	require.Error(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateError, last.State)
	assert.Contains(t, last.ErrorMessage, "unsupported changeType")
}

func TestPoolUpdateWorkflowE2E_ExpiredDeadline_MarksJobErrorBeforeWork(t *testing.T) {
	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	params := newUpdatePoolParams(reqID, ClusterComputeChangeType)
	params.VCM.Deadline = time.Now().UTC().Add(-time.Minute)

	err := runPoolUpdateWorkflowE2E(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
		registerPoolUpdateDependencies(reg, stubPoolActivity{
			targetInstanceType: "c3-standard-8-lssd",
			targetSPConfig:     vlm.SPConfig{Size: "4TiB", IOps: 2000, Throughput: 128},
		})
	})

	require.Error(t, err)
	updates := store.allUpdates()
	require.Len(t, updates, 1, "an expired job must fail before mark-processing")
	assert.Equal(t, vcmdb.JobStateError, updates[0].State)
	assert.Equal(t, jobStepStarting, updates[0].Step)
	assert.Equal(t, vsaerrors.ErrOperationDeadlineExceeded, updates[0].TrackingID)
	assert.Contains(t, updates[0].ErrorMessage, "mark-processing")
	for _, update := range updates {
		assert.NotEqual(t, vcmdb.JobStateProcessing, update.State)
		assert.NotEqual(t, jobStepGetCluster, update.Step)
	}
}

func childCreateExpertModeUser(
	_ *orchestrator.Context,
	req *vlm.OntapExpertModeUserConfig,
) (*vlm.OntapExpertModeUserResponse, error) {
	if req.RbacFileURL != "gs://bucket/rbac" || req.RbacFileChecksum != "checksum" {
		return nil, orchestrator.NewNonRetryableApplicationError("unexpected RBAC file", "TEST", nil)
	}
	switch req.AuthenticationType {
	case vlmAuthenticationTypePassword:
		if req.Username != "mode-user" || req.ExpertModeUserCredentials.AdminPassword != "ModePassword123!" {
			return nil, orchestrator.NewNonRetryableApplicationError("unexpected password credentials", "TEST", nil)
		}
	case vlmAuthenticationTypeCertificate:
		if req.Username != "mode-cert-user" ||
			req.ExpertModeUserCredentials.Certificate.Certificate != "signed-certificate" {
			return nil, orchestrator.NewNonRetryableApplicationError("unexpected certificate credentials", "TEST", nil)
		}
	default:
		return nil, orchestrator.NewNonRetryableApplicationError("unexpected authentication type", "TEST", nil)
	}
	return &vlm.OntapExpertModeUserResponse{RbacFileChecksum: req.RbacFileChecksum}, nil
}

func TestPoolWorkflowE2E_HappyPath_MarksJobDone(t *testing.T) {
	// The ProviderConfigWrapper inside VLMConfig only round-trips through JSON
	// when PROVIDER is set (the go-workflows engine serializes activity/child
	// results).
	t.Setenv("PROVIDER", vlm.GCPCloud)

	// Drive the local-env credential branch so provision() resolves ONTAP
	// credentials from env instead of the (unregistered) Secret Manager activity.
	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})

	err := runPoolWorkflowE2EWithReg(t, store, newPoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
	})
	require.NoError(t, err, "the full provision path must succeed with stubbed activities/child workflows")

	// The job walked NEW -> PROCESSING -> (step updates) -> DONE.
	states := store.states()
	require.NotEmpty(t, states)
	assert.Contains(t, states, vcmdb.JobStateProcessing)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateDone, last.State, "a successful provision must leave the job DONE")
	assert.Equal(t, jobStepCompleted, last.Step)
	assert.JSONEq(t, `{
		"clusterShape": {
			"provisionedThroughputMibps": 0,
			"provisionedIops": 0,
			"provisionedSizeInBytes": 0,
			"qosType": "",
			"mode": "",
			"regionIdentifier": "",
			"autoTieringConfig": {
				"autoTierBucketName": ""
			}
		},
		"clusterLayout": {
			"nodes": [
				{
					"gceVmName": "vm1",
					"gceInstanceId": "",
					"nodeManagementIp": "10.0.0.10",
					"gceVmLocation": "",
					"ipConfig": {
						"clusterManagementLifIp": "",
						"haInterconnectLifIp": "",
						"interclusterLifIp": "",
						"nodemgmtLifIp": "10.0.0.10",
						"rsmLifIp": "",
						"dataLif": {
							"nasIlbIp": "10.33.182.243",
							"sanIps": ["10.33.182.252","10.33.182.249"]
						}
					}
				},
				{
					"gceVmName": "vm2",
					"gceInstanceId": "",
					"nodeManagementIp": "10.0.0.11",
					"gceVmLocation": "",
					"ipConfig": {
						"clusterManagementLifIp": "",
						"haInterconnectLifIp": "",
						"interclusterLifIp": "",
						"nodemgmtLifIp": "10.0.0.11",
						"rsmLifIp": "",
						"dataLif": {
							"nasIlbIp": "10.33.182.243",
							"sanIps": ["10.33.182.252","10.33.182.249"]
						}
					}
				}
			],
			"disks": [],
			"activeZone": "",
			"standbyZone": ""
		}
	}`, last.ResponsePayload)
}

func TestPoolWorkflowE2E_ResponsePayloadOnlyWrittenOnDone(t *testing.T) {
	// The operation response must not be readable while the operation is still
	// running, so no intermediate job update may carry a response payload: the
	// payload is written exactly once, on the terminal DONE transition, after
	// every activity and child workflow has succeeded.
	t.Setenv("PROVIDER", vlm.GCPCloud)

	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})

	err := runPoolWorkflowE2EWithReg(t, store, newPoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
	})
	require.NoError(t, err)

	updates := store.allUpdates()
	require.Greater(t, len(updates), 1, "the run must record step updates before the terminal one")

	payloadCarriers := 0
	for i, update := range updates {
		if update.State == vcmdb.JobStateDone {
			payloadCarriers++
			assert.NotEmpty(t, update.ResponsePayload,
				"the DONE transition must carry the operation response")
			continue
		}
		assert.Empty(t, update.ResponsePayload,
			"update %d (state %s, step %q) must not carry a response payload before DONE",
			i, update.State, update.Step)
	}
	assert.Equal(t, 1, payloadCarriers, "exactly one DONE transition must write the payload")
}

func TestPoolWorkflowE2E_MarshalFailureStillMarksJobDone(t *testing.T) {
	t.Setenv("PROVIDER", vlm.GCPCloud)

	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	origPayloadFn := poolCreateOperationResponsePayloadFn
	poolCreateOperationResponsePayloadFn = func(*commonparams.VCMCreatePoolParams, vlm.VLMConfig, string) (*string, error) {
		return nil, errors.New("marshal failed")
	}
	t.Cleanup(func() { poolCreateOperationResponsePayloadFn = origPayloadFn })

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})

	err := runPoolWorkflowE2EWithReg(t, store, newPoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
	})
	require.NoError(t, err, "marshal failure must not fail the workflow after successful provision")

	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateDone, last.State, "provision success must leave the job DONE even when payload marshal fails")
	assert.Equal(t, jobStepCompleted, last.Step)

	job, _ := store.GetJob(context.Background(), reqID)
	require.NotNil(t, job)
	assert.Equal(t, vcmdb.JobStateDone, job.State)
	assert.Empty(t, job.ResponsePayload)
}

func TestPoolWorkflowE2E_CreateClusterOntapLicense(t *testing.T) {
	tests := []struct {
		name          string
		licenseURI    string
		wantSecretURI []string
	}{
		{
			name:          "configured secret uri is passed to VLM pinned to latest version",
			licenseURI:    "projects/237071556299/secrets/ontap-license-secret",
			wantSecretURI: []string{"projects/237071556299/secrets/ontap-license-secret/versions/latest"},
		},
		{
			name:          "empty secret uri leaves VLM on its default license",
			licenseURI:    "",
			wantSecretURI: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PROVIDER", vlm.GCPCloud)

			origEnv, origPw := runningEnv, vcmLocalOntapPassword
			runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
			t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

			var mu sync.Mutex
			var gotLicense vlm.OntapLicense
			captureCreateCluster := func(ctx *orchestrator.Context, req *vlm.CreateVSAClusterDeploymentRequest) (*vlm.CreateVSAClusterDeploymentResponse, error) {
				mu.Lock()
				gotLicense = req.OntapLicense
				mu.Unlock()
				return childCreateCluster(ctx, req)
			}

			reqID := uuid.NewString()
			store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
			params := newPoolParams(reqID)
			params.VCM.OntapLicenseSecretURI = tt.licenseURI

			err := runPoolWorkflowE2EWithReg(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
				reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
				reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
				reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, captureCreateCluster)
				reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
			})
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tt.wantSecretURI, gotLicense.SecretUri)
		})
	}
}

func newPoolParams(requestID string) *commonparams.CreatePoolParams {
	return &commonparams.CreatePoolParams{
		VCM: &commonparams.VCMCreatePoolParams{
			DeploymentID: "gcnv-e2e",
			RequestID:    requestID,
			PrimaryZone:  "us-central1-a",
			Deadline:     time.Now().UTC().Add(time.Hour),
			// No OntapCredentials: the password path in resolveOntapCredentials
			// then fails fast (no admin secret id) before any cloud activity.
		},
	}
}

func newOntapModePoolParams(requestID string) *commonparams.CreatePoolParams {
	params := newPoolParams(requestID)
	params.VCM.Mode = "ONTAP"
	params.VCM.QosType = "manual"
	params.VCM.OntapModeRbacFileURL = "gs://bucket/rbac"
	params.VCM.OntapModeRbacChecksum = "checksum"
	params.VCM.OntapCredentials = []commonparams.VCMOntapCredential{
		{Type: "admin", SecretID: "projects/project/secrets/admin", Username: "root"},
		{Type: "ontap-mode", SecretID: "projects/project/secrets/mode", Username: "mode-user"},
	}
	return params
}

func withLocalOntapEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PROVIDER", vlm.GCPCloud)
	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })
}

// withNonLocalOntapEnv drives the production credential path: both
// resolveOntapCredentials and resolveOntapModeCredentials dispatch the Secret
// Manager activities instead of using the local flags, so callers must register
// those activities. This is the path that validates activity-based resolution
// (password, certificate common name, empty-secret handling).
func withNonLocalOntapEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PROVIDER", vlm.GCPCloud)
	origEnv := runningEnv
	runningEnv = "staging"
	t.Cleanup(func() { runningEnv = origEnv })
}

func TestPoolWorkflowE2E_OntapModeCreatesUser(t *testing.T) {
	withNonLocalOntapEnv(t)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	err := runPoolWorkflowE2EWithReg(t, store, newOntapModePoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterActivity(fakeSvmActivity{}.UpdateNFSRWSizeOnSVM)
		reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapAdminPassword)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
		reg.RegisterWorkflowWithName(vlm.CreateVSAExpertModeUserWorkflowName, childCreateExpertModeUser)
	})

	require.NoError(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateDone, last.State)
	assert.Contains(t, store.states(), vcmdb.JobStateProcessing)
}

type recordingQoSSvmActivity struct {
	pools chan *datamodel.Pool
}

func (a *recordingQoSSvmActivity) CreateQoSPolicyAndApplyToSVM(_ context.Context, pool *datamodel.Pool, _ *datamodel.Svm, _ *coremodels.Node) error {
	a.pools <- pool
	return nil
}

// The QoS activity skips the SVM-wide ceiling for manual QoS unless the pool is
// ONTAP mode, so the synthetic pool must carry the access mode.
func TestPoolWorkflowE2E_OntapModeQoSPoolCarriesAccessMode(t *testing.T) {
	withNonLocalOntapEnv(t)

	qos := &recordingQoSSvmActivity{pools: make(chan *datamodel.Pool, 1)}
	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	err := runPoolWorkflowE2EWithReg(t, store, newOntapModePoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(qos.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterActivity(fakeSvmActivity{}.UpdateNFSRWSizeOnSVM)
		reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapAdminPassword)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
		reg.RegisterWorkflowWithName(vlm.CreateVSAExpertModeUserWorkflowName, childCreateExpertModeUser)
	})
	require.NoError(t, err)

	select {
	case pool := <-qos.pools:
		assert.Equal(t, commonparams.ONTAPMode, pool.APIAccessMode)
	default:
		t.Fatal("QoS activity was not called")
	}
}

// ONTAP maps an incoming client certificate to an account by common name, so
// the user must be created under the resolved CN rather than the configured
// username.
func TestPoolWorkflowE2E_OntapModeCertificateUsesResolvedCommonName(t *testing.T) {
	withNonLocalOntapEnv(t)

	reqID := uuid.NewString()
	params := newOntapModePoolParams(reqID)
	params.VCM.OntapCredentials[1] = commonparams.VCMOntapCredential{
		Type:                   "ontap-mode",
		Username:               "ignored-username",
		CertificateID:          "mode-certificate",
		CertPrivateKeySecretID: "mode-private-key",
		IsCertAuth:             true,
	}
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	err := runPoolWorkflowE2EWithReg(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterActivity(fakeSvmActivity{}.UpdateNFSRWSizeOnSVM)
		// Admin resolves by password; the ONTAP-mode user resolves by
		// certificate, so both activities are registered.
		reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapAdminPassword)
		reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapCertificate)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
		reg.RegisterWorkflowWithName(vlm.CreateVSAExpertModeUserWorkflowName, childCreateExpertModeUser)
	})

	require.NoError(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateDone, last.State)
}

// A present-but-empty ONTAP-mode secret must fail the job before the
// expert-mode child workflow runs, not create a user with no password.
func TestPoolWorkflowE2E_OntapModeUnusableCredentialFailsJob(t *testing.T) {
	withNonLocalOntapEnv(t)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	err := runPoolWorkflowE2EWithReg(t, store, newOntapModePoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		// Admin resolves to a usable password; the ONTAP-mode secret resolves
		// empty, so the job must fail on the ONTAP-mode credential.
		reg.RegisterActivity(partialOntapModeCredentialActivity{}.ResolveOntapAdminPassword)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
	})

	require.Error(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateError, last.State)
	assert.Contains(t, last.ErrorMessage, "resolved value is empty")
}

// ONTAP-mode pools need the SVM QoS ceiling even under manual QoS, because the
// tenant can create volumes directly and would otherwise be unbounded.
func TestPoolWorkflowE2E_OntapModeManualQosStillCreatesCeiling(t *testing.T) {
	withLocalOntapEnv(t)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	err := runPoolWorkflowE2EWithReg(t, store, newOntapModePoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(failingSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterActivity(fakeSvmActivity{}.UpdateNFSRWSizeOnSVM)
		reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapAdminPassword)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
	})

	require.Error(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateError, last.State)
	assert.Equal(t, jobStepCreateQoSPolicy, last.Step)
	assert.Contains(t, last.ErrorMessage, "qos activity called")
}

// ONTAP-mode tenants create NFS volumes directly on the cluster, so the SVM's
// NFS TCP max transfer size must be raised at pool create. Failing the NFS
// activity while the QoS activity would succeed also pins the ordering: the NFS
// step runs first, so its error is the one that fails the job.
func TestPoolWorkflowE2E_OntapModeAppliesNfsTransferSizeBeforeQos(t *testing.T) {
	withLocalOntapEnv(t)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	err := runPoolWorkflowE2EWithReg(t, store, newOntapModePoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterActivity(failingNfsSvmActivity{}.UpdateNFSRWSizeOnSVM)
		reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapAdminPassword)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
	})

	require.Error(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateError, last.State)
	assert.Equal(t, jobStepNfsTransferSize, last.Step)
	assert.Contains(t, last.ErrorMessage, "nfs activity called")
}

// A DEFAULT-mode pool must not touch the NFS transfer size: the managed volume
// path owns that setting. Registering no NFS activity proves it is never called.
func TestPoolWorkflowE2E_DefaultModeSkipsNfsTransferSize(t *testing.T) {
	withLocalOntapEnv(t)

	reqID := uuid.NewString()
	params := newOntapModePoolParams(reqID)
	params.VCM.Mode = "DEFAULT"
	params.VCM.QosType = "auto"
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	err := runPoolWorkflowE2EWithReg(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
	})

	require.NoError(t, err)
	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateDone, last.State)
}

func TestPoolWorkflowE2E_ProcessingJob_ResumesToDone(t *testing.T) {
	// A crash-recovery re-dispatch hands the workflow a job that is already
	// PROCESSING. With the NEW-state guard gone, the workflow must resume and
	// drive the (idempotent) provision path to DONE instead of rejecting it.
	t.Setenv("PROVIDER", vlm.GCPCloud)

	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateProcessing})

	err := runPoolWorkflowE2EWithReg(t, store, newPoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{}.IdentifyVMs)
		reg.RegisterActivity(fakeSvmActivity{}.CreateQoSPolicyAndApplyToSVM)
		reg.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childCreateCluster)
		reg.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childCreateSVM)
	})
	require.NoError(t, err, "a PROCESSING job must resume and complete, not be rejected")

	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateDone, last.State, "a resumed provision must leave the job DONE")
	assert.Equal(t, jobStepCompleted, last.Step)
}

func TestPoolWorkflowE2E_ProvisionFails_MarksJobError(t *testing.T) {
	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})

	err := runPoolWorkflowE2E(t, store, newPoolParams(reqID))

	require.Error(t, err, "provisioning must fail without ONTAP credentials")

	// The workflow moves the job to PROCESSING, then records the terminal ERROR.
	states := store.states()
	require.NotEmpty(t, states)
	assert.Contains(t, states, vcmdb.JobStateProcessing, "job must be marked PROCESSING before work starts")

	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateError, last.State, "a failed provision must leave the job in ERROR")
	assert.Contains(t, last.ErrorMessage, "resolve ONTAP admin password",
		"the terminal ERROR must carry the underlying failure text, not a catalog message")
}

func TestPoolWorkflowE2E_ProvisionFails_PersistsRawProviderError(t *testing.T) {
	t.Setenv("PROVIDER", vlm.GCPCloud)

	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	originalErr := errors.New("snapshot with that name already exists")
	customErr := vsaerrors.NewVCPError(vsaerrors.ErrCreateSnapshotConflict, originalErr)
	catalogMsg := vsaerrors.GetErrorMessageByTrackingID(vsaerrors.ErrCreateSnapshotConflict).Message
	activityErr := apperror.WrapAsAppError(customErr, true)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})

	err := runPoolWorkflowE2EWithReg(t, store, newPoolParams(reqID), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(stubPoolActivity{identifyErr: activityErr}.IdentifyVMs)
	})
	require.Error(t, err)

	last, ok := store.lastUpdate()
	require.True(t, ok)
	assert.Equal(t, vcmdb.JobStateError, last.State)
	assert.Equal(t, vsaerrors.ErrCreateSnapshotConflict, last.TrackingID)
	assert.Equal(t, originalErr.Error(), last.ErrorMessage,
		"job error_message must store raw provider text")
	assert.NotEqual(t, catalogMsg, last.ErrorMessage,
		"job error_message must not store the customer-facing catalog text")
	assert.False(t, last.Retriable)
}
