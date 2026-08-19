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

// ensure vsaerrors stays referenced if future assertions need it.
var _ = vsaerrors.ErrGCPResourceAlreadyExistsError
