package vcm_workflows

import (
	"context"
	"encoding/json"
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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

// recordingExpertModeChild captures the request the refresh hands the VLM
// expert-mode user workflow and answers with a fixed applied checksum, or with
// failErr when set.
type recordingExpertModeChild struct {
	mu      sync.Mutex
	got     *vlm.OntapExpertModeUserConfig
	failErr error
}

func (c *recordingExpertModeChild) CreateExpertModeUser(
	_ *orchestrator.Context,
	req *vlm.OntapExpertModeUserConfig,
) (*vlm.OntapExpertModeUserResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	clone := *req
	c.got = &clone
	if c.failErr != nil {
		return nil, c.failErr
	}
	return &vlm.OntapExpertModeUserResponse{RbacFileChecksum: "applied-checksum"}, nil
}

func (c *recordingExpertModeChild) request() *vlm.OntapExpertModeUserConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got
}

const (
	refreshTestNodeMgmtIP  = "10.0.0.10"
	refreshTestClusterName = "ontap-cluster-e2e"
)

// childGetClusterWithNodeMgmt returns the live cluster config with a node mgmt
// LIF, which the refresh dials to read the ONTAP cluster name.
func childGetClusterWithNodeMgmt(ctx *orchestrator.Context, req *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	resp, err := childGetClusterForUpdate(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.VLMConfig.Cloud.HAPairs = []vlm.HAPair{{
		VM1: vlm.VMConfig{SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
			vlm.LIFTypeNodeMgmt: {IP: refreshTestNodeMgmtIP},
		}},
	}}
	return resp, nil
}

// stubClusterNameActivity answers GetOntapClusterName with name, or failErr.
type stubClusterNameActivity struct {
	mu      sync.Mutex
	name    string
	failErr error
	gotNode *coremodels.Node
}

func (s *stubClusterNameActivity) GetOntapClusterName(_ context.Context, node *coremodels.Node) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gotNode = node
	if s.failErr != nil {
		return "", s.failErr
	}
	return s.name, nil
}

func (s *stubClusterNameActivity) node() *coremodels.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gotNode
}

func newRbacRefreshParams(requestID string) *commonparams.RefreshRbacForPoolParams {
	vcm := &commonparams.VCMCreatePoolParams{
		DeploymentID:          "gcnv-e2e",
		RequestID:             requestID,
		ServiceTenantProject:  "tenant-project",
		PrimaryZone:           "us-central1-a",
		Deadline:              time.Now().UTC().Add(time.Hour),
		OntapModeRbacFileURL:  "gs://bucket/rbac-v2",
		OntapModeRbacChecksum: "checksum-v2",
		OntapCredentials: []commonparams.VCMOntapCredential{
			{Type: "admin", SecretID: "projects/project/secrets/admin", Username: "root"},
			{Type: "ontap-mode", SecretID: "projects/project/secrets/mode", Username: "mode-user"},
		},
	}
	return &commonparams.RefreshRbacForPoolParams{PoolID: vcm.DeploymentID, VCM: vcm}
}

func runRbacRefreshWorkflowE2E(
	t *testing.T,
	store vcmdb.VCMDatastore,
	params *commonparams.RefreshRbacForPoolParams,
	child *recordingExpertModeChild,
	clusterNames *stubClusterNameActivity,
	getCluster func(*orchestrator.Context, *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error),
) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := r.Registrar()
	reg.RegisterWorkflow(OntapModeRbacRefreshWorkflow)
	ja := &activities.JobActivity{Store: store}
	reg.RegisterActivity(ja.UpdateJobProgress)
	reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapAdminPassword)
	reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapCertificate)
	reg.RegisterActivity(clusterNames.GetOntapClusterName)
	reg.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, getCluster)
	reg.RegisterWorkflowWithName(vlm.CreateVSAExpertModeUserWorkflowName, child.CreateExpertModeUser)

	require.NoError(t, r.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = r.Stop()
	})

	wfi, err := r.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: uuid.NewString(),
	}, gowfbackend.FuncName(OntapModeRbacRefreshWorkflow), params)
	require.NoError(t, err)

	_, werr := cclient.GetWorkflowResult[json.RawMessage](ctx, r.Client(), wfi, 30*time.Second)
	return werr
}

func TestRbacRefreshWorkflowE2E_Success_InvokesVLMInUpdateModeAndMarksDone(t *testing.T) {
	withNonLocalOntapEnv(t)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	child := &recordingExpertModeChild{}
	names := &stubClusterNameActivity{name: refreshTestClusterName}

	require.NoError(t, runRbacRefreshWorkflowE2E(t, store, newRbacRefreshParams(reqID), child, names, childGetClusterWithNodeMgmt))

	node := names.node()
	require.NotNil(t, node, "the cluster name must be read from ONTAP")
	assert.Contains(t, node.EndpointAddressesToHostNameMap, refreshTestNodeMgmtIP)
	assert.Equal(t, vsa.OntapSNIServerName("gcnv-e2e"), node.ServerName)

	got := child.request()
	require.NotNil(t, got, "the VLM expert-mode user workflow must be invoked")
	assert.Equal(t, refreshTestClusterName, got.VLMConfig.VsaCluster.ClusterName,
		"VLM must receive the cluster name ONTAP reported")
	assert.True(t, got.IsUpdate)
	assert.Equal(t, "gs://bucket/rbac-v2", got.RbacFileURL)
	assert.Equal(t, "checksum-v2", got.RbacFileChecksum)
	assert.Equal(t, "mode-user", got.Username)
	assert.Equal(t, vlmAuthenticationTypePassword, got.AuthenticationType)
	assert.Equal(t, "ModePassword123!", got.OntapCredentials.AdminPassword)
	assert.Equal(t, "gcnv-e2e", got.VLMConfig.Deployment.DeploymentID,
		"the live VLM config must be passed so VLM can reach the cluster")

	job, err := store.GetJob(context.Background(), reqID)
	require.NoError(t, err)
	assert.Equal(t, vcmdb.JobStateDone, job.State)
	assert.Equal(t, jobStepCompleted, job.CurrentStep)

	var resp rbacRefreshOperationResponse
	require.NoError(t, json.Unmarshal([]byte(job.ResponsePayload), &resp))
	assert.Equal(t, "gs://bucket/rbac-v2", resp.OntapModeRbac.RbacFileURL)
	assert.Equal(t, "applied-checksum", resp.OntapModeRbac.RbacFileCheckSum)

	steps := make([]string, 0)
	for _, u := range store.allUpdates() {
		steps = append(steps, u.Step)
	}
	assert.Contains(t, steps, jobStepGetCluster)
	assert.Contains(t, steps, jobStepGetOntapClusterName)
	assert.Contains(t, steps, jobStepRefreshOntapModeRbac)
}

func TestRbacRefreshWorkflowE2E_ClusterNameFailures_MarkJobErrorBeforeVLM(t *testing.T) {
	tests := []struct {
		name       string
		names      *stubClusterNameActivity
		getCluster func(*orchestrator.Context, *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error)
	}{
		{
			name:       "ONTAP lookup fails",
			names:      &stubClusterNameActivity{failErr: orchestrator.NewNonRetryableApplicationError("cluster get failed", "TEST", nil)},
			getCluster: childGetClusterWithNodeMgmt,
		},
		{
			name:       "ONTAP returns empty name",
			names:      &stubClusterNameActivity{},
			getCluster: childGetClusterWithNodeMgmt,
		},
		{
			name:       "no node mgmt IP in VLM config",
			names:      &stubClusterNameActivity{name: refreshTestClusterName},
			getCluster: childGetClusterForUpdate,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withNonLocalOntapEnv(t)

			reqID := uuid.NewString()
			store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
			child := &recordingExpertModeChild{}

			require.Error(t, runRbacRefreshWorkflowE2E(t, store, newRbacRefreshParams(reqID), child, tc.names, tc.getCluster))

			assert.Nil(t, child.request(), "VLM must not be invoked without the cluster name")
			job, err := store.GetJob(context.Background(), reqID)
			require.NoError(t, err)
			assert.Equal(t, vcmdb.JobStateError, job.State)
			assert.Equal(t, jobStepGetOntapClusterName, job.CurrentStep)
		})
	}
}

func TestRbacRefreshWorkflowE2E_NoModeGating_DefaultModeParamsStillRefresh(t *testing.T) {
	withNonLocalOntapEnv(t)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	child := &recordingExpertModeChild{}
	params := newRbacRefreshParams(reqID)
	params.VCM.Mode = "DEFAULT"
	names := &stubClusterNameActivity{name: refreshTestClusterName}

	require.NoError(t, runRbacRefreshWorkflowE2E(t, store, params, child, names, childGetClusterWithNodeMgmt))
	require.NotNil(t, child.request(), "VCM must not gate the refresh on cluster mode")
}

func TestRbacRefreshWorkflowE2E_VLMFailure_MarksJobError(t *testing.T) {
	withNonLocalOntapEnv(t)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	child := &recordingExpertModeChild{
		failErr: orchestrator.NewNonRetryableApplicationError("checksum mismatch", "TEST", nil),
	}

	names := &stubClusterNameActivity{name: refreshTestClusterName}
	err := runRbacRefreshWorkflowE2E(t, store, newRbacRefreshParams(reqID), child, names, childGetClusterWithNodeMgmt)

	require.Error(t, err)
	job, gerr := store.GetJob(context.Background(), reqID)
	require.NoError(t, gerr)
	assert.Equal(t, vcmdb.JobStateError, job.State)
	assert.Equal(t, jobStepRefreshOntapModeRbac, job.CurrentStep)
	assert.Empty(t, job.ResponsePayload)
}

func TestRbacRefreshWorkflowE2E_ExpiredDeadline_MarksJobErrorBeforeWork(t *testing.T) {
	withNonLocalOntapEnv(t)

	reqID := uuid.NewString()
	store := newE2EJobStore(&vcmdb.Job{ID: reqID, State: vcmdb.JobStateNew})
	child := &recordingExpertModeChild{}
	params := newRbacRefreshParams(reqID)
	params.VCM.Deadline = time.Now().UTC().Add(-time.Minute)

	names := &stubClusterNameActivity{name: refreshTestClusterName}
	require.Error(t, runRbacRefreshWorkflowE2E(t, store, params, child, names, childGetClusterWithNodeMgmt))
	assert.Nil(t, child.request(), "an expired refresh must not reach VLM")
	job, err := store.GetJob(context.Background(), reqID)
	require.NoError(t, err)
	assert.Equal(t, vcmdb.JobStateError, job.State)
}

// childIDExpertModeChild reports its own workflow ID as the applied checksum,
// so each refresh's result shows which VLM child served it. It must not block:
// workflow code that waits outside the engine trips the coroutine timeout.
func childIDExpertModeChild(
	ctx *orchestrator.Context,
	_ *vlm.OntapExpertModeUserConfig,
) (*vlm.OntapExpertModeUserResponse, error) {
	return &vlm.OntapExpertModeUserResponse{RbacFileChecksum: orchestrator.GetInfo(ctx).WorkflowExecution.ID}, nil
}

func TestRbacRefreshWorkflowE2E_ConcurrentRequestIDs_BothReachVLM(t *testing.T) {
	withNonLocalOntapEnv(t)

	reqA, reqB := uuid.NewString(), uuid.NewString()
	store := newE2EJobStore(
		&vcmdb.Job{ID: reqA, State: vcmdb.JobStateNew},
		&vcmdb.Job{ID: reqB, State: vcmdb.JobStateNew},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)
	reg := r.Registrar()
	reg.RegisterWorkflow(OntapModeRbacRefreshWorkflow)
	ja := &activities.JobActivity{Store: store}
	reg.RegisterActivity(ja.UpdateJobProgress)
	reg.RegisterActivity(fakeOntapModeCredentialActivity{}.ResolveOntapAdminPassword)
	reg.RegisterActivity((&stubClusterNameActivity{name: refreshTestClusterName}).GetOntapClusterName)
	reg.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, childGetClusterWithNodeMgmt)
	reg.RegisterWorkflowWithName(vlm.CreateVSAExpertModeUserWorkflowName, childIDExpertModeChild)
	require.NoError(t, r.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = r.Stop()
	})

	wfiA, err := r.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{InstanceID: reqA},
		gowfbackend.FuncName(OntapModeRbacRefreshWorkflow), newRbacRefreshParams(reqA))
	require.NoError(t, err)
	wfiB, err := r.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{InstanceID: reqB},
		gowfbackend.FuncName(OntapModeRbacRefreshWorkflow), newRbacRefreshParams(reqB))
	require.NoError(t, err)

	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, r.Client(), wfiA, 30*time.Second)
	require.NoError(t, err)
	_, err = cclient.GetWorkflowResult[json.RawMessage](ctx, r.Client(), wfiB, 30*time.Second)
	require.NoError(t, err)

	for _, reqID := range []string{reqA, reqB} {
		job, gerr := store.GetJob(context.Background(), reqID)
		require.NoError(t, gerr)
		assert.Equal(t, vcmdb.JobStateDone, job.State, "refresh %s must complete", reqID)

		var resp rbacRefreshOperationResponse
		require.NoError(t, json.Unmarshal([]byte(job.ResponsePayload), &resp))
		assert.Equal(t, reqID+"-exp-mode", resp.OntapModeRbac.RbacFileCheckSum,
			"each refresh must dispatch its own VLM child keyed on its request id")
	}
}

func TestRbacRefreshOperationResponsePayload(t *testing.T) {
	payload, err := rbacRefreshOperationResponsePayload("gs://bucket/rbac", "sum")
	require.NoError(t, err)
	require.NotNil(t, payload)
	assert.JSONEq(t, `{"ontapModeRbac":{"rbacFileUrl":"gs://bucket/rbac","rbacFileCheckSum":"sum"}}`, *payload)
}
