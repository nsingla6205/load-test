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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

// fakeRebuildStore is an in-memory activities.VLMConfigStore capturing the
// finalize write-back so the terminal outcome can be asserted.
type fakeRebuildStore struct {
	mu         sync.Mutex
	setJSON    string
	setCalls   int
	clearCalls int
}

func (f *fakeRebuildStore) SetVLMConfig(_ context.Context, jsonStr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	f.setJSON = jsonStr
	return nil
}

func (f *fakeRebuildStore) ClearVLMConfigFetching(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clearCalls++
	return nil
}

// fakeRebuildCredActivity stubs the Secret Manager credential activity so the
// non-local password branch resolves without touching the cloud.
type fakeRebuildCredActivity struct{}

func (fakeRebuildCredActivity) ResolveOntapAdminPassword(_ context.Context, _ activities.ResolveOntapAdminPasswordRequest) (*vlm.OntapCredentials, error) {
	return &vlm.OntapCredentials{AdminPassword: "secret-admin-pw"}, nil
}

func (fakeRebuildCredActivity) ResolveOntapCertificate(_ context.Context, req activities.ResolveOntapCertificateRequest) (*vlm.OntapCredentials, error) {
	creds := &vlm.OntapCredentials{}
	creds.Certificate.CommonName = req.PoolCredentials.CertificateID
	creds.Certificate.Certificate = "signed-cert"
	creds.Certificate.PrivateKey = "private-key"
	return creds, nil
}

func childGetClusterWF(_ *orchestrator.Context, req *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	cfg := vlm.VLMConfig{}
	cfg.Deployment.DeploymentID = req.DeploymentID
	return &vlm.GetVSAClusterDeploymentResponse{VLMConfig: cfg}, nil
}

func childGetClusterWFPermError(_ *orchestrator.Context, _ *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	return nil, orchestrator.NewNonRetryableApplicationError("boom get-cluster failure", "TEST", nil)
}

func rebuildWFParams(deploymentID string) *commonparams.GetVLMConfigParams {
	return &commonparams.GetVLMConfigParams{
		DeploymentID:         deploymentID,
		ServiceTenantProject: "tenant-1",
		Zone1:                "us-central1-a",
		Zone2:                "us-central1-b",
		MediatorZone:         "us-central1-c",
		OntapCredentials: []commonparams.VCMOntapCredential{
			{Type: "admin", SecretID: "projects/p/secrets/s"},
		},
	}
}

// runRebuildWFE2E registers RebuildVLMConfigWorkflow plus the config activities
// backed by store, lets the caller register extra stubs (child workflow, cred
// activity), runs one instance, and returns the terminal error.
func runRebuildWFE2E(t *testing.T, store activities.VLMConfigStore, params *commonparams.GetVLMConfigParams, extra func(orchestrator.WorkflowRegistrar)) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	r, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)

	reg := r.Registrar()
	reg.RegisterWorkflow(RebuildVLMConfigWorkflow)
	ca := &activities.VLMConfigActivity{Store: store}
	reg.RegisterActivity(ca.ValidateGetVLMConfigInputs)
	reg.RegisterActivity(ca.FinalizeVLMConfig)
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
	}, gowfbackend.FuncName(RebuildVLMConfigWorkflow), params)
	require.NoError(t, err)

	_, werr := cclient.GetWorkflowResult[json.RawMessage](ctx, r.Client(), wfi, 30*time.Second)
	return werr
}

func TestRebuildVLMConfigWorkflowE2E_HappyPath_LocalEnv(t *testing.T) {
	t.Setenv("PROVIDER", vlm.GCPCloud)
	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	store := &fakeRebuildStore{}
	err := runRebuildWFE2E(t, store, rebuildWFParams("dep-1"), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, childGetClusterWF)
	})

	require.NoError(t, err)
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, 1, store.setCalls, "a successful rebuild stores the resolved VLM config")
	assert.Equal(t, 0, store.clearCalls)
	assert.NotEmpty(t, store.setJSON)
}

func TestRebuildVLMConfigWorkflowE2E_SecretCredPath(t *testing.T) {
	t.Setenv("PROVIDER", vlm.GCPCloud)
	origEnv := runningEnv
	runningEnv = "prod"
	t.Cleanup(func() { runningEnv = origEnv })

	store := &fakeRebuildStore{}
	err := runRebuildWFE2E(t, store, rebuildWFParams("dep-2"), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(fakeRebuildCredActivity{}.ResolveOntapAdminPassword)
		reg.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, childGetClusterWF)
	})

	require.NoError(t, err)
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, 1, store.setCalls, "the secret-resolved credential path must complete the rebuild")
}

func TestRebuildVLMConfigWorkflowE2E_CertCredPath(t *testing.T) {
	t.Setenv("PROVIDER", vlm.GCPCloud)
	origEnv := runningEnv
	runningEnv = "prod"
	t.Cleanup(func() { runningEnv = origEnv })

	params := rebuildWFParams("dep-cert")
	params.CaURI = "123456789012/pool/intermediate"
	params.OntapCredentials = []commonparams.VCMOntapCredential{
		{Type: "admin", CertificateID: "cert-1", SecretID: "projects/p/secrets/s"},
	}

	store := &fakeRebuildStore{}
	err := runRebuildWFE2E(t, store, params, func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterActivity(fakeRebuildCredActivity{}.ResolveOntapCertificate)
		reg.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, childGetClusterWF)
	})

	require.NoError(t, err)
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, 1, store.setCalls, "the certificate-resolved credential path must complete the rebuild")
}

func TestRebuildVLMConfigWorkflowE2E_ChildError_FinalizesFailure(t *testing.T) {
	t.Setenv("PROVIDER", vlm.GCPCloud)
	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	store := &fakeRebuildStore{}
	err := runRebuildWFE2E(t, store, rebuildWFParams("dep-3"), func(reg orchestrator.WorkflowRegistrar) {
		reg.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, childGetClusterWFPermError)
	})

	require.Error(t, err, "a failed child workflow must fail the rebuild")
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, 0, store.setCalls)
	assert.Equal(t, 1, store.clearCalls, "failure must clear the fetching flag via FinalizeVLMConfig")
}

func TestRebuildVLMConfigWorkflowE2E_NilParams(t *testing.T) {
	store := &fakeRebuildStore{}
	err := runRebuildWFE2E(t, store, nil, nil)

	require.Error(t, err, "nil params must fail the workflow")
}

// The credential branches asserted here short-circuit before the orchestrator
// context is used, so a nil context is safe.

func TestResolveVLMConfigCredentials_LocalEnv_UsesLocalPassword(t *testing.T) {
	origEnv, origPw := runningEnv, vcmLocalOntapPassword
	runningEnv, vcmLocalOntapPassword = localEnv, "local-admin-pw"
	t.Cleanup(func() { runningEnv, vcmLocalOntapPassword = origEnv, origPw })

	creds, err := resolveVLMConfigCredentials(nil, &commonparams.GetVLMConfigParams{DeploymentID: "dep-1"})
	require.NoError(t, err)
	assert.Equal(t, "local-admin-pw", creds.AdminPassword)
}

func TestResolveVLMConfigCredentials_NoAdminSecret_Errors(t *testing.T) {
	origEnv := runningEnv
	runningEnv = "prod"
	t.Cleanup(func() { runningEnv = origEnv })

	// No ONTAP credential with a secret id: the secret path fails fast before
	// dispatching the Secret Manager activity.
	_, err := resolveVLMConfigCredentials(nil, &commonparams.GetVLMConfigParams{DeploymentID: "dep-1"})
	require.Error(t, err)
}
