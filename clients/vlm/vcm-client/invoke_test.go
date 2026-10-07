package vcmclient

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	cclient "github.com/cschleiden/go-workflows/client"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vlm "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
)

const e2eInvokeTimeout = 30 * time.Second

// TestMain sets the PROVIDER env so that the ProviderConfigWrapper embedded in
// VLMConfig can round-trip through JSON when the go-workflows engine serializes
// the child-workflow request/response payloads.
func TestMain(m *testing.M) {
	_ = os.Setenv("PROVIDER", vlm.GCPCloud)
	code := m.Run()
	_ = os.Unsetenv("PROVIDER")
	os.Exit(code)
}

// runInvokeE2E spins up an isolated in-memory go-workflows runner, lets the
// caller register the parent workflow (which invokes one of the vcmclient
// helpers) plus the stub child workflow it dispatches, runs the parent, and
// decodes the result into out.
func runInvokeE2E[T any](t *testing.T, register func(orchestrator.WorkflowRegistrar), parent interface{}, out *T, args ...any) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), e2eInvokeTimeout)
	defer cancel()

	r, err := gowfbackend.NewRunner(ctx)
	require.NoError(t, err)
	register(r.Registrar())

	require.NoError(t, r.Start(ctx))
	t.Cleanup(func() {
		cancel()
		_ = r.Stop()
	})

	wfi, err := r.Client().CreateWorkflowInstance(ctx, cclient.WorkflowInstanceOptions{
		InstanceID: uuid.NewString(),
	}, gowfbackend.FuncName(parent), args...)
	require.NoError(t, err)

	raw, err := cclient.GetWorkflowResult[json.RawMessage](ctx, r.Client(), wfi, e2eInvokeTimeout)
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 && string(raw) != "null" {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// --- parent workflows (drive the code under test) ---

func parentInvokeCluster(ctx *orchestrator.Context, req *vlm.CreateVSAClusterDeploymentRequest) (*vlm.CreateVSAClusterDeploymentResponse, error) {
	return InvokeCreateClusterDeployment(ctx, req)
}

func parentInvokeSVM(ctx *orchestrator.Context, req *vlm.CreateSVMRequest) (*vlm.CreateSVMResponse, error) {
	return InvokeCreateSVM(ctx, req)
}

// --- stub child workflows (registered under the VLM workflow names) ---

func childClusterEcho(_ *orchestrator.Context, req *vlm.CreateVSAClusterDeploymentRequest) (*vlm.CreateVSAClusterDeploymentResponse, error) {
	return &vlm.CreateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}, nil
}

// permanentErrChild returns a non-retryable error so the parent's retry policy
// (up to 3 attempts, 1-minute interval) does not stall the test.
func childClusterPermError(_ *orchestrator.Context, _ *vlm.CreateVSAClusterDeploymentRequest) (*vlm.CreateVSAClusterDeploymentResponse, error) {
	return nil, orchestrator.NewNonRetryableApplicationError("boom cluster failure", "TEST", nil)
}

func childSVMEcho(_ *orchestrator.Context, req *vlm.CreateSVMRequest) (*vlm.CreateSVMResponse, error) {
	return &vlm.CreateSVMResponse{VLMConfig: req.VLMConfig}, nil
}

func childSVMAlreadyExists(_ *orchestrator.Context, _ *vlm.CreateSVMRequest) (*vlm.CreateSVMResponse, error) {
	return nil, orchestrator.NewNonRetryableApplicationError("SVM already exists", "TEST", nil)
}

func childSVMPermError(_ *orchestrator.Context, _ *vlm.CreateSVMRequest) (*vlm.CreateSVMResponse, error) {
	return nil, orchestrator.NewNonRetryableApplicationError("boom svm failure", "TEST", nil)
}

func clusterRequest(deploymentID string, numHAPair int) *vlm.CreateVSAClusterDeploymentRequest {
	req := &vlm.CreateVSAClusterDeploymentRequest{}
	req.VLMConfig.Deployment.DeploymentID = deploymentID
	req.VLMConfig.Deployment.NumHAPair = numHAPair
	return req
}

func TestToRetryPolicy_MapsAllFields(t *testing.T) {
	got := toRetryPolicy(&vlm.WorkflowRetryPolicy{
		InitialInterval:    2 * time.Second,
		BackoffCoefficient: 1.5,
		MaximumInterval:    30 * time.Second,
		MaximumAttempts:    7,
	})
	require.NotNil(t, got)
	assert.Equal(t, 2*time.Second, got.InitialInterval)
	assert.Equal(t, 1.5, got.BackoffCoefficient)
	assert.Equal(t, 30*time.Second, got.MaximumInterval)
	assert.Equal(t, int32(7), got.MaximumAttempts)
}

func TestInvokeCreateSVM_Validation(t *testing.T) {
	// Both validation branches short-circuit before the orchestrator context is
	// used, so a nil context is safe here.
	tests := []struct {
		name string
		req  *vlm.CreateSVMRequest
	}{
		{
			name: "missing deployment id",
			req:  &vlm.CreateSVMRequest{Name: "svm-1"},
		},
		{
			name: "missing svm name",
			req: func() *vlm.CreateSVMRequest {
				r := &vlm.CreateSVMRequest{}
				r.VLMConfig.Deployment.DeploymentID = "dep-1"
				return r
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := InvokeCreateSVM(nil, tc.req)
			require.Error(t, err)
			// Validation failures are non-retryable input errors.
			appErr, ok := orchestrator.AsApplicationError(err)
			require.True(t, ok, "expected an orchestrator application error, got %T", err)
			assert.True(t, appErr.NonRetryable())
		})
	}
}

func TestInvokeCreateClusterDeployment_Success(t *testing.T) {
	var resp vlm.CreateVSAClusterDeploymentResponse
	// NumHAPair >= MinLvHAPair exercises the large-capacity execution-timeout branch.
	err := runInvokeE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeCluster)
		r.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childClusterEcho)
	}, parentInvokeCluster, &resp, clusterRequest("dep-large", vlm.MinLvHAPair))

	require.NoError(t, err)
	assert.Equal(t, "dep-large", resp.VLMConfig.Deployment.DeploymentID)
}

func TestInvokeCreateClusterDeployment_ChildError_IsWrapped(t *testing.T) {
	var resp vlm.CreateVSAClusterDeploymentResponse
	err := runInvokeE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeCluster)
		r.RegisterWorkflowWithName(vlm.CreateVSAClusterDeploymentWorkflowName, childClusterPermError)
	}, parentInvokeCluster, &resp, clusterRequest("dep-1", 1))

	require.Error(t, err)
}

func TestInvokeCreateSVM_Success(t *testing.T) {
	req := &vlm.CreateSVMRequest{Name: "svm-1"}
	req.VLMConfig.Deployment.DeploymentID = "dep-1"

	var resp vlm.CreateSVMResponse
	err := runInvokeE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeSVM)
		r.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childSVMEcho)
	}, parentInvokeSVM, &resp, req)

	require.NoError(t, err)
	assert.Equal(t, "dep-1", resp.VLMConfig.Deployment.DeploymentID)
}

func TestInvokeCreateSVM_AlreadyExists_IsTreatedAsSuccess(t *testing.T) {
	// An "already exists" SVM error must be swallowed (idempotent re-run), so
	// the invoke returns (nil, nil) and the parent workflow completes cleanly.
	req := &vlm.CreateSVMRequest{Name: "svm-1"}
	req.VLMConfig.Deployment.DeploymentID = "dep-1"

	err := runInvokeE2E[vlm.CreateSVMResponse](t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeSVM)
		r.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childSVMAlreadyExists)
	}, parentInvokeSVM, nil, req)

	require.NoError(t, err, "an already-exists SVM must be treated as success")
}

func TestInvokeCreateSVM_ChildError_IsWrapped(t *testing.T) {
	req := &vlm.CreateSVMRequest{Name: "svm-1"}
	req.VLMConfig.Deployment.DeploymentID = "dep-1"

	err := runInvokeE2E[vlm.CreateSVMResponse](t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeSVM)
		r.RegisterWorkflowWithName(vlm.CreateVSASVMWorkflowName, childSVMPermError)
	}, parentInvokeSVM, nil, req)

	require.Error(t, err)
}

func parentInvokeExpertModeUser(ctx *orchestrator.Context, req *vlm.OntapExpertModeUserConfig) (*vlm.OntapExpertModeUserResponse, error) {
	return InvokeCreateExpertModeUser(ctx, req)
}

func childExpertModeUserEcho(_ *orchestrator.Context, req *vlm.OntapExpertModeUserConfig) (*vlm.OntapExpertModeUserResponse, error) {
	return &vlm.OntapExpertModeUserResponse{RbacFileChecksum: req.RbacFileChecksum}, nil
}

func childExpertModeUserPermError(_ *orchestrator.Context, _ *vlm.OntapExpertModeUserConfig) (*vlm.OntapExpertModeUserResponse, error) {
	return nil, orchestrator.NewNonRetryableApplicationError("boom ONTAP mode user failure", "TEST", nil)
}

func TestInvokeCreateExpertModeUser_Validation(t *testing.T) {
	tests := []struct {
		name string
		req  *vlm.OntapExpertModeUserConfig
	}{
		{name: "nil request"},
		{name: "missing deployment id", req: &vlm.OntapExpertModeUserConfig{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := InvokeCreateExpertModeUser(nil, tt.req)
			require.Error(t, err)
			appErr, ok := orchestrator.AsApplicationError(err)
			require.True(t, ok)
			assert.True(t, appErr.NonRetryable())
		})
	}
}

func TestInvokeCreateExpertModeUser_Success(t *testing.T) {
	req := &vlm.OntapExpertModeUserConfig{RbacFileChecksum: "checksum"}
	req.VLMConfig.Deployment.DeploymentID = "dep-1"

	var resp vlm.OntapExpertModeUserResponse
	err := runInvokeE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeExpertModeUser)
		r.RegisterWorkflowWithName(vlm.CreateVSAExpertModeUserWorkflowName, childExpertModeUserEcho)
	}, parentInvokeExpertModeUser, &resp, req)

	require.NoError(t, err)
	assert.Equal(t, "checksum", resp.RbacFileChecksum)
}

func TestInvokeCreateExpertModeUser_ChildError_IsWrapped(t *testing.T) {
	req := &vlm.OntapExpertModeUserConfig{}
	req.VLMConfig.Deployment.DeploymentID = "dep-1"

	err := runInvokeE2E[vlm.OntapExpertModeUserResponse](t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeExpertModeUser)
		r.RegisterWorkflowWithName(vlm.CreateVSAExpertModeUserWorkflowName, childExpertModeUserPermError)
	}, parentInvokeExpertModeUser, nil, req)

	require.Error(t, err)
}

// childExpertModeUserReportsID returns its own workflow ID in RbacFileChecksum
// so tests can assert the ID the parent dispatched the child under.
func childExpertModeUserReportsID(ctx *orchestrator.Context, _ *vlm.OntapExpertModeUserConfig) (*vlm.OntapExpertModeUserResponse, error) {
	return &vlm.OntapExpertModeUserResponse{RbacFileChecksum: orchestrator.GetInfo(ctx).WorkflowExecution.ID}, nil
}

func TestInvokeCreateExpertModeUser_ChildIDKeyedOnDeployment(t *testing.T) {
	req := &vlm.OntapExpertModeUserConfig{}
	req.VLMConfig.Deployment.DeploymentID = "dep-1"

	var resp vlm.OntapExpertModeUserResponse
	err := runInvokeE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeExpertModeUser)
		r.RegisterWorkflowWithName(vlm.CreateVSAExpertModeUserWorkflowName, childExpertModeUserReportsID)
	}, parentInvokeExpertModeUser, &resp, req)

	require.NoError(t, err)
	assert.Equal(t, "dep-1-exp-mode", resp.RbacFileChecksum)
}

func parentInvokeGetCluster(ctx *orchestrator.Context, req *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	return InvokeGetClusterDeployment(ctx, req)
}

func childGetClusterEcho(_ *orchestrator.Context, req *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	cfg := vlm.VLMConfig{}
	cfg.Deployment.DeploymentID = req.DeploymentID
	return &vlm.GetVSAClusterDeploymentResponse{VLMConfig: cfg}, nil
}

func childGetClusterPermError(_ *orchestrator.Context, _ *vlm.GetVSAClusterDeploymentRequest) (*vlm.GetVSAClusterDeploymentResponse, error) {
	return nil, orchestrator.NewNonRetryableApplicationError("boom get-cluster failure", "TEST", nil)
}

func TestInvokeGetClusterDeployment_Success(t *testing.T) {
	var resp vlm.GetVSAClusterDeploymentResponse
	err := runInvokeE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeGetCluster)
		r.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, childGetClusterEcho)
	}, parentInvokeGetCluster, &resp, &vlm.GetVSAClusterDeploymentRequest{DeploymentID: "dep-get"})

	require.NoError(t, err)
	assert.Equal(t, "dep-get", resp.VLMConfig.Deployment.DeploymentID)
}

func TestInvokeGetClusterDeployment_ChildError_IsWrapped(t *testing.T) {
	var resp vlm.GetVSAClusterDeploymentResponse
	err := runInvokeE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeGetCluster)
		r.RegisterWorkflowWithName(vlm.GetVSAClusterDeploymentWorkflowName, childGetClusterPermError)
	}, parentInvokeGetCluster, &resp, &vlm.GetVSAClusterDeploymentRequest{DeploymentID: "dep-1"})

	require.Error(t, err)
}

func parentInvokeUpdateCluster(ctx *orchestrator.Context, req *vlm.UpdateVSAClusterDeploymentRequest) (*vlm.UpdateVSAClusterDeploymentResponse, error) {
	return InvokeUpdateClusterDeployment(ctx, req)
}

func childUpdateClusterEcho(_ *orchestrator.Context, req *vlm.UpdateVSAClusterDeploymentRequest) (*vlm.UpdateVSAClusterDeploymentResponse, error) {
	cfg := req.VLMConfig
	if req.NewInstanceType != "" {
		cfg.Deployment.VSAInstanceType = req.NewInstanceType
	}
	cfg.Deployment.SPConfig = req.SPConfig
	return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: cfg}, nil
}

func childUpdateClusterPermError(_ *orchestrator.Context, _ *vlm.UpdateVSAClusterDeploymentRequest) (*vlm.UpdateVSAClusterDeploymentResponse, error) {
	return nil, orchestrator.NewNonRetryableApplicationError("boom update-cluster failure", "TEST", nil)
}

func TestInvokeUpdateClusterDeployment_Success(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{
		NewInstanceType: "c3-standard-88-lssd",
		SPConfig:        vlm.SPConfig{Size: "1024Gi"},
	}
	req.VLMConfig.Deployment.DeploymentID = "dep-update"
	req.VLMConfig.Deployment.VSAInstanceType = "c3-standard-44-lssd"

	var resp vlm.UpdateVSAClusterDeploymentResponse
	err := runInvokeE2E(t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeUpdateCluster)
		r.RegisterWorkflowWithName(vlm.UpdateVSAClusterDeploymentWorkflowName, childUpdateClusterEcho)
	}, parentInvokeUpdateCluster, &resp, req)

	require.NoError(t, err)
	assert.Equal(t, "dep-update", resp.VLMConfig.Deployment.DeploymentID)
	assert.Equal(t, "c3-standard-88-lssd", resp.VLMConfig.Deployment.VSAInstanceType)
}

func TestInvokeUpdateClusterDeployment_ChildError_IsWrapped(t *testing.T) {
	req := &vlm.UpdateVSAClusterDeploymentRequest{}
	req.VLMConfig.Deployment.DeploymentID = "dep-1"

	err := runInvokeE2E[vlm.UpdateVSAClusterDeploymentResponse](t, func(r orchestrator.WorkflowRegistrar) {
		r.RegisterWorkflow(parentInvokeUpdateCluster)
		r.RegisterWorkflowWithName(vlm.UpdateVSAClusterDeploymentWorkflowName, childUpdateClusterPermError)
	}, parentInvokeUpdateCluster, nil, req)

	require.Error(t, err)
}

// ensure vsaerrors stays referenced if future assertions need it.
var _ = vsaerrors.ErrGCPResourceAlreadyExistsError
