package vcm_workflows

import (
	"context"
	"encoding/json"
	"errors"
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
	vcmactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/vcm"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
	"gorm.io/datatypes"
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

func (s *e2eJobStore) ListOngoingJobs(context.Context) ([]vcmdb.Job, error) { return nil, nil }

func (s *e2eJobStore) UpdateJobProgress(_ context.Context, params vcmdb.UpdateJobProgressParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, params)
	if job, ok := s.jobs[params.JobID]; ok {
		job.State = params.State
		job.CurrentStep = params.Step
		if vcmdb.HasPersistableResponsePayload(params.ResponsePayload) {
			job.ResponsePayload = params.ResponsePayload
		}
	}
	return nil
}

func (s *e2eJobStore) SetJobCleanupMetadata(_ context.Context, _ string, _ datatypes.JSON) error {
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
	ja := &vcmactivities.JobActivity{Store: store}
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

// --- happy-path stubs ---
//
// The gowf backend dispatches activities/child workflows by their SHORT name
// (trailing identifier). These stubs therefore share the exact method name of
// the real activity so the workflow's dispatch-by-value resolves to them, while
// the real (cloud-touching) implementations stay unregistered.

// stubPoolActivity stands in for PoolActivity.IdentifyVMs in e2e tests; set
// identifyErr to simulate a provisioning failure without a separate stub type.
type stubPoolActivity struct {
	identifyErr error
}

func (a stubPoolActivity) IdentifyVMs(_ context.Context, req activities.IdentifyVMsRequest) (*vlm.VLMConfig, error) {
	if a.identifyErr != nil {
		return nil, a.identifyErr
	}
	cfg := &vlm.VLMConfig{}
	cfg.VsaCluster.ClusterName = "cluster-e2e"
	// InvokeCreateSVM requires a non-empty deployment id on the VLM config.
	cfg.Deployment.DeploymentID = req.DeploymentName
	return cfg, nil
}

type fakeSvmActivity struct{}

func (fakeSvmActivity) CreateQoSPolicyAndApplyToSVM(_ context.Context, _ *datamodel.Pool, _ *datamodel.Svm, _ *coremodels.Node) error {
	return nil
}

func childCreateCluster(_ *orchestrator.Context, req *vlm.CreateVSAClusterDeploymentRequest) (*vlm.CreateVSAClusterDeploymentResponse, error) {
	return &vlm.CreateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}, nil
}

func childCreateSVM(_ *orchestrator.Context, req *vlm.CreateSVMRequest) (*vlm.CreateSVMResponse, error) {
	return &vlm.CreateSVMResponse{VLMConfig: req.VLMConfig}, nil
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
	assert.JSONEq(t, `{"pool":{"deploymentId":"gcnv-e2e"}}`, last.ResponsePayload)
}

func TestPoolWorkflowE2E_MarshalFailureStillMarksJobDone(t *testing.T) {
	t.Setenv("PROVIDER", vlm.GCPCloud)

	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	origPayloadFn := poolCreateOperationResponsePayloadFn
	poolCreateOperationResponsePayloadFn = func(string) (*string, error) {
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

func TestPoolWorkflowE2E_JobNotInNewState_FailsEnsureJobState(t *testing.T) {
	reqID := uuid.NewString()
	// A job that already advanced past NEW models a resumed/duplicate run; the
	// workflow must refuse to proceed.
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateProcessing})

	err := runPoolWorkflowE2E(t, store, newPoolParams(reqID))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid state")
	// EnsureJobState fails before the workflow marks anything, so no state
	// transition should have been persisted.
	_, ok := store.lastUpdate()
	assert.False(t, ok, "no job update expected when the NEW-state precondition fails")
}

func TestPoolWorkflowE2E_JobNotFound_FailsEnsureJobState(t *testing.T) {
	reqID := uuid.NewString()
	// No seeded job: GetJob returns nil, so EnsureJobState reports not-found.
	store := newE2EJobStore()

	err := runPoolWorkflowE2E(t, store, newPoolParams(reqID))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
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
