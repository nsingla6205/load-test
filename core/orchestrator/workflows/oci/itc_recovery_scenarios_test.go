package oci

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// itcScenarioHarness drives OCIUpdatePoolWorkflow ITC recovery scenario tests.
type itcScenarioHarness struct {
	t        *testing.T
	env      *testsuite.TestWorkflowEnvironment
	pool     *datamodel.Pool
	params   *common.UpdatePoolParams
	mockVlm  *vlm.MockVlmWorkflowClient
	haPairs  int
	origVlm  func() vlm.VlmWorkflowClient
	normal   atomic.Int32
	recovery atomic.Int32
}

func newITCScenarioHarness(t *testing.T, haPairs int, itcEnabled bool) *itcScenarioHarness {
	t.Helper()
	setTestOCIImageEnv(t)
	withOCIEnableITCRecovery(t, itcEnabled)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})

	h := &itcScenarioHarness{
		t:       t,
		env:     env,
		haPairs: haPairs,
		pool: &datamodel.Pool{
			BaseModel:              datamodel.BaseModel{UUID: "pool-uuid-itc-scenario"},
			Name:                   "p-itc-scenario",
			DeploymentName:         "dep-itc-scenario",
			PoolExternalIdentifier: "ocid1.pool.oc1..itcscenario",
			SizeInBytes:            1024 * 1024 * 1024 * 1024,
			Network:                "subnet-1",
			Account:                &datamodel.Account{Name: "test-account"},
			PoolAttributes:         &datamodel.PoolAttributes{ThroughputMibps: 128},
			PoolCredentials:        &datamodel.PoolCredentials{Password: "pw"},
		},
		params: &common.UpdatePoolParams{
			AccountName:          "test-account",
			SizeInBytes:          2 * 1024 * 1024 * 1024 * 1024,
			TotalThroughputMibps: 256,
		},
		mockVlm: vlm.NewMockVlmWorkflowClient(t),
	}
	h.env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(storedVLMConfigWithHAPairs(haPairs), nil)
	h.origVlm = workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return h.mockVlm }
	t.Cleanup(func() { workflows.GetNewVSAClientWorkflowManager = h.origVlm })
	return h
}

func (h *itcScenarioHarness) run() {
	h.t.Helper()
	h.env.ExecuteWorkflow(OCIUpdatePoolWorkflow, h.params, h.pool)
	require.True(h.t, h.env.IsWorkflowCompleted())
}

func (h *itcScenarioHarness) installDefaultMock() {
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{DeploymentID: "after-normal", ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				return nil
			},
		)
}

// TestITCRecoveryScenario3_AllBatchesSucceedNoRecovery — scenario 3: no failure, no recovery, update persisted.
func TestITCRecoveryScenario3_AllBatchesSucceedNoRecovery(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.installDefaultMock()
	h.env.OnActivity("UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(h.pool, nil)
	h.env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{}, nil)
	h.env.OnActivity("UpdateOCINodesFromVLMConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(2), h.normal.Load(), "both batches complete on normal path")
	assert.Equal(t, int32(0), h.recovery.Load())
	h.env.AssertCalled(t, "UpdatedPoolWithVLMConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	h.env.AssertNotCalled(t, "UpdatedPool", mock.Anything, mock.Anything)
	h.env.AssertNotCalled(t, "ErroredPool", mock.Anything, mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario4_NonMatchingErrorFailsWorkflow — scenario 4: non-recoverable error, pool ERROR.
func TestITCRecoveryScenario4_NonMatchingErrorFailsWorkflow(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
				} else {
					h.normal.Add(1)
				}
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				return errors.New("simulated non-recoverable VLM failure")
			},
		)
	h.env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	h.env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(h.pool, nil)

	before := testutil.ToFloat64(workflowStageTotal.WithLabelValues(wfUpdatePool, queueCustomer, stageVLMUpdate, resultFailure))
	h.run()

	require.Error(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(1), h.normal.Load())
	assert.Equal(t, int32(0), h.recovery.Load())
	assert.InDelta(t, 1.0, testutil.ToFloat64(workflowStageTotal.WithLabelValues(wfUpdatePool, queueCustomer, stageVLMUpdate, resultFailure))-before, 0)
	h.env.AssertCalled(t, "ErroredPool", mock.Anything, mock.Anything, mock.Anything)
	h.env.AssertNotCalled(t, "UpdatedPool", mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario1_MixedStateRecoveryRerunsAttemptedBatches — scenario 1: batch 2 fails after batch 1 OK.
func TestITCRecoveryScenario1_MixedStateRecoveryRerunsAttemptedBatches(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{DeploymentID: "after-normal", ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				if h.normal.Load() == 2 {
					return vlmITCRecoveryStatusError()
				}
				return nil
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)

	before := testutil.ToFloat64(workflowStageTotal.WithLabelValues(wfUpdatePool, queueCustomer, stageVLMUpdate, resultFailure))
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(2), h.normal.Load())
	assert.Equal(t, int32(2), h.recovery.Load())
	assert.InDelta(t, 1.0, testutil.ToFloat64(workflowStageTotal.WithLabelValues(wfUpdatePool, queueCustomer, stageVLMUpdate, resultFailure))-before, 0)
	h.env.AssertCalled(t, "UpdatedPool", mock.Anything, mock.Anything)
	h.env.AssertNotCalled(t, "ErroredPool", mock.Anything, mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario2_FirstBatchFailureScopedRecovery — scenario 2: batch 1 fails, batch 2 never runs.
func TestITCRecoveryScenario2_FirstBatchFailureScopedRecovery(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				return vlmITCRecoveryStatusError()
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(1), h.normal.Load())
	assert.Equal(t, int32(1), h.recovery.Load())
	h.env.AssertCalled(t, "UpdatedPool", mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario_PartialFailureFirstBatchScopedRecovery — partial UpdateStatus on batch 1 only.
func TestITCRecoveryScenario_PartialFailureFirstBatchScopedRecovery(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{
					VLMConfig:    vlm.VLMConfig{Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()}},
					UpdateStatus: vlm.DeploymentUpdateStatus{SPUpdateFail: true},
				}
			},
			func(_ workflow.Context, _ *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				return nil
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(1), h.normal.Load())
	assert.Equal(t, int32(1), h.recovery.Load())
}

// TestITCRecoveryScenario_PartialFailureMixedStateRerunsBothBatches — partial failure on batch 2 after batch 1 OK.
func TestITCRecoveryScenario_PartialFailureMixedStateRerunsBothBatches(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				if h.normal.Load() == 1 {
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
						Deployment: vlm.DeploymentConfig{DeploymentID: "batch-1-ok", ProviderConfig: testOCIProviderConfig()},
					}}
				}
				return &vlm.UpdateVSAClusterDeploymentResponse{
					VLMConfig:    vlm.VLMConfig{Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()}},
					UpdateStatus: vlm.DeploymentUpdateStatus{LifUpFail: true},
				}
			},
			func(_ workflow.Context, _ *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				return nil
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(2), h.normal.Load())
	assert.Equal(t, int32(2), h.recovery.Load())
}

// TestITCRecoveryScenario_CaseInsensitiveCauseTriggersRecovery — "OUT OF CAPACITY" in cause triggers recovery.
func TestITCRecoveryScenario_CaseInsensitiveCauseTriggersRecovery(t *testing.T) {
	h := newITCScenarioHarness(t, 1, true)
	capacityErr := temporal.NewApplicationError(
		"ITC batch failed",
		vlm.ErrorTypeVLMClientError,
		vlm.VLMClientError{
			HttpCode: 500,
			Code:     "INTERNAL_ERROR",
			Message:  "An internal system error occurred",
			Cause:    []string{"ScaleOntapCluster: OUT OF CAPACITY in AD-2"},
		},
	)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				return capacityErr
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(1), h.normal.Load())
	assert.Equal(t, int32(1), h.recovery.Load())
}

// TestITCRecoveryScenario_FlagDisabledMatchingErrorSkipsRecovery — flag off: capacity error still fails workflow.
func TestITCRecoveryScenario_FlagDisabledMatchingErrorSkipsRecovery(t *testing.T) {
	h := newITCScenarioHarness(t, 2, false)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
				} else {
					h.normal.Add(1)
				}
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				return vlmITCRecoveryStatusError()
			},
		)
	h.env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	h.env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.Error(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(1), h.normal.Load())
	assert.Equal(t, int32(0), h.recovery.Load())
	h.env.AssertCalled(t, "ErroredPool", mock.Anything, mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario_FlagDisabledPartialFailureSkipsRecovery — flag off: partial failure fails without recovery.
func TestITCRecoveryScenario_FlagDisabledPartialFailureSkipsRecovery(t *testing.T) {
	h := newITCScenarioHarness(t, 2, false)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if !req.ITCRecovery {
					h.normal.Add(1)
				}
				return &vlm.UpdateVSAClusterDeploymentResponse{
					VLMConfig:    vlm.VLMConfig{Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()}},
					UpdateStatus: vlm.DeploymentUpdateStatus{AttachFail: true},
				}
			},
			func(_ workflow.Context, _ *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				return nil
			},
		)
	h.env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(h.pool, nil)
	h.env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	h.run()

	require.Error(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(1), h.normal.Load())
	assert.Equal(t, int32(0), h.recovery.Load())
	h.env.AssertCalled(t, "ErroredPool", mock.Anything, mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario_RecoveryLoopFailureMarksPoolError — normal triggers recovery but recovery VLM call fails.
func TestITCRecoveryScenario_RecoveryLoopFailureMarksPoolError(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return errors.New("ITC recovery VLM failure")
				}
				if h.normal.Load() == 1 {
					return vlmITCRecoveryStatusError()
				}
				return nil
			},
		)
	h.env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	h.env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.Error(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(1), h.normal.Load())
	assert.Equal(t, int32(1), h.recovery.Load())
	h.env.AssertCalled(t, "ErroredPool", mock.Anything, mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario_RecoveryFailsOnSecondBatchAfterMixedState — batch 1 OK, batch 2 capacity fail; recovery fails on batch 2.
func TestITCRecoveryScenario_RecoveryFailsOnSecondBatchAfterMixedState(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					if h.recovery.Load() == 2 {
						return errors.New("recovery batch 2 failed")
					}
					return nil
				}
				if h.normal.Load() == 2 {
					return vlmITCRecoveryStatusError()
				}
				return nil
			},
		)
	h.env.OnActivity("RestorePoolPreUpdatePoolLevelFields", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	h.env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.Error(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(2), h.normal.Load())
	assert.Equal(t, int32(2), h.recovery.Load())
	h.env.AssertCalled(t, "ErroredPool", mock.Anything, mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario_FourHAPairsFirstBatchFailureScopedRecovery — 4 HA pairs (2 batches); fail on batch 1.
func TestITCRecoveryScenario_FourHAPairsFirstBatchFailureScopedRecovery(t *testing.T) {
	h := newITCScenarioHarness(t, 4, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				return vlmITCRecoveryStatusError()
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(1), h.normal.Load(), "only first batch attempted before failure")
	assert.Equal(t, int32(1), h.recovery.Load(), "only first batch recovered")
}

// TestITCRecoveryScenario_ThreeBatchesFailOnThirdRerunsAllAttempted — 3 HA pairs → 3 batches; fail batch 3 after 1–2 OK.
func TestITCRecoveryScenario_ThreeBatchesFailOnThirdRerunsAllAttempted(t *testing.T) {
	h := newITCScenarioHarness(t, 3, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				if h.normal.Load() == 3 {
					return vlmITCRecoveryStatusError()
				}
				return nil
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(3), h.normal.Load())
	assert.Equal(t, int32(3), h.recovery.Load())
}

// TestITCRecoveryScenario_FourBatchesFailOnFourthRerunsAllAttempted — 4 HA pairs in 4 batches (P=8);
// batches 1–3 OK, batch 4 fails → ITC recovery re-runs all 4 attempted batches.
func TestITCRecoveryScenario_FourBatchesFailOnFourthRerunsAllAttempted(t *testing.T) {
	setTestOCIImageEnv(t)
	withOCIEnableITCRecovery(t, true)
	withOCIParallelNodesForITC(t, 8) // 4 HA pairs → batchSize=1 → 4 workflow batches

	h := newITCScenarioHarness(t, 4, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{DeploymentID: "after-normal", ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				if h.normal.Load() == 4 {
					return vlmITCRecoveryStatusError()
				}
				return nil
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(4), h.normal.Load(), "batches 1–3 succeed then batch 4 fails on normal path")
	assert.Equal(t, int32(4), h.recovery.Load(), "recovery must re-run all 4 attempted batches")
	h.env.AssertCalled(t, "UpdatedPool", mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario_FourBatchesFailOnSecondScopedRecovery — 4 HA pairs in 4 batches (P=8);
// batch 2 fails after batch 1 OK; batches 3–4 never run → ITC recovery re-runs batches 1–2 only.
func TestITCRecoveryScenario_FourBatchesFailOnSecondScopedRecovery(t *testing.T) {
	setTestOCIImageEnv(t)
	withOCIEnableITCRecovery(t, true)
	withOCIParallelNodesForITC(t, 8)

	h := newITCScenarioHarness(t, 4, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				if h.normal.Load() == 2 {
					return vlmITCRecoveryStatusError()
				}
				return nil
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.Equal(t, int32(2), h.normal.Load(), "only batches 1–2 attempted before failure")
	assert.Equal(t, int32(2), h.recovery.Load(), "recovery scoped to attempted batches 1–2; batches 3–4 skipped")
	h.env.AssertCalled(t, "UpdatedPool", mock.Anything, mock.Anything)
}

// TestITCRecoveryScenario_RecoverableFailureSkipsIntermediateStageEmit — recoverable path emits stage failure once total.
func TestITCRecoveryScenario_RecoverableFailureSkipsIntermediateStageEmit(t *testing.T) {
	h := newITCScenarioHarness(t, 2, true)
	h.mockVlm.On("UpdateVSAClusterDeployment", mock.Anything, mock.Anything, mock.Anything).
		Return(
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) *vlm.UpdateVSAClusterDeploymentResponse {
				if req.ITCRecovery {
					h.recovery.Add(1)
					return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: req.VLMConfig}
				}
				h.normal.Add(1)
				return &vlm.UpdateVSAClusterDeploymentResponse{VLMConfig: vlm.VLMConfig{
					Deployment: vlm.DeploymentConfig{ProviderConfig: testOCIProviderConfig()},
				}}
			},
			func(_ workflow.Context, req *vlm.UpdateVSAClusterDeploymentRequest, _ string) error {
				if req.ITCRecovery {
					return nil
				}
				return vlmITCRecoveryStatusError()
			},
		)
	h.env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).Return(h.pool, nil)

	before := testutil.ToFloat64(workflowStageTotal.WithLabelValues(wfUpdatePool, queueCustomer, stageVLMUpdate, resultFailure))
	h.run()

	require.NoError(t, h.env.GetWorkflowError())
	assert.InDelta(t, 1.0, testutil.ToFloat64(workflowStageTotal.WithLabelValues(wfUpdatePool, queueCustomer, stageVLMUpdate, resultFailure))-before, 0,
		"intermediate recoverable failure must not increment stage failure before post-recovery abort")
}
