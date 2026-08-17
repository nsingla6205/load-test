package workflows

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const nfsTransferSizeTestSvmName = "svm_test"

// testApplyNFSTcpMaxTransferSizeWorkflow wraps applyNFSTcpMaxTransferSizeOnPoolCreate so the helper
// can be driven by the Temporal test environment.
func testApplyNFSTcpMaxTransferSizeWorkflow(
	ctx workflow.Context,
	params *common.CreatePoolParams,
	createSVMResponse *vlm.CreateSVMResponse,
	svmName string,
	svm *datamodel.Svm,
	node *models.Node,
) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	return applyNFSTcpMaxTransferSizeOnPoolCreate(ctx, params, createSVMResponse, svmName, svm, node, &activities.SvmActivity{})
}

// nfsTransferSizeCreateSVMResponse builds a CreateVSASVM response holding one LIF per given type
// under svmName.
func nfsTransferSizeCreateSVMResponse(svmName string, lifTypes ...vlm.VSALIFType) *vlm.CreateSVMResponse {
	lifs := vlm.SvmLIFConfigs{}
	for _, lifType := range lifTypes {
		lifs[lifType] = []vlm.LIFConfig{{Name: string(lifType) + "-lif", VSALIFType: lifType}}
	}
	return &vlm.CreateSVMResponse{
		VLMConfig: vlm.VLMConfig{
			Svm: map[string]vlm.SvmConfig{
				svmName: {Svmname: svmName, SVMLIFs: lifs},
			},
		},
	}
}

// runApplyNFSTcpMaxTransferSizeWorkflow executes the wrapper workflow with a mocked
// UpdateNFSRWSizeOnSVM activity and reports whether that activity was invoked.
func runApplyNFSTcpMaxTransferSizeWorkflow(
	t *testing.T,
	params *common.CreatePoolParams,
	createSVMResponse *vlm.CreateSVMResponse,
	activityErr error,
) (activityCalled bool, workflowErr error) {
	t.Helper()

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(testApplyNFSTcpMaxTransferSizeWorkflow)

	svmActivity := &activities.SvmActivity{}
	env.RegisterActivity(svmActivity.UpdateNFSRWSizeOnSVM)
	env.OnActivity(svmActivity.UpdateNFSRWSizeOnSVM, mock.Anything, mock.Anything, mock.Anything).
		Return(activityErr).
		Run(func(mock.Arguments) { activityCalled = true }).
		Maybe()

	svm := &datamodel.Svm{
		Name:       nfsTransferSizeTestSvmName,
		SvmDetails: &datamodel.SvmDetails{ExternalUUID: "svm-uuid"},
	}

	env.ExecuteWorkflow(
		testApplyNFSTcpMaxTransferSizeWorkflow,
		params,
		createSVMResponse,
		nfsTransferSizeTestSvmName,
		svm,
		&models.Node{Name: "test-node"},
	)

	require.True(t, env.IsWorkflowCompleted())
	return activityCalled, env.GetWorkflowError()
}

func TestApplyNFSTcpMaxTransferSizeOnPoolCreate(t *testing.T) {
	tests := []struct {
		name              string
		params            *common.CreatePoolParams
		createSVMResponse *vlm.CreateSVMResponse
		wantApplied       bool
	}{
		{
			name:              "ontap mode applies even without a create SVM response",
			params:            &common.CreatePoolParams{Mode: common.ONTAPMode},
			createSVMResponse: nil,
			wantApplied:       true,
		},
		{
			name:              "ontap mode applies even when no NAS LIF was created",
			params:            &common.CreatePoolParams{Mode: common.ONTAPMode},
			createSVMResponse: nfsTransferSizeCreateSVMResponse(nfsTransferSizeTestSvmName, vlm.LIFTypeSan),
			wantApplied:       true,
		},
		{
			name:              "large capacity applies when a NAS LIF was created",
			params:            &common.CreatePoolParams{Mode: common.DEFAULTMode, LargeCapacity: true},
			createSVMResponse: nfsTransferSizeCreateSVMResponse(nfsTransferSizeTestSvmName, vlm.LIFTypeNas),
			wantApplied:       true,
		},
		{
			name:              "large capacity applies when an ILB NAS LIF was created",
			params:            &common.CreatePoolParams{Mode: common.DEFAULTMode, LargeCapacity: true},
			createSVMResponse: nfsTransferSizeCreateSVMResponse(nfsTransferSizeTestSvmName, vlm.LIFTypeIlbNas),
			wantApplied:       true,
		},
		{
			name:              "large capacity skips when only non-NAS LIFs were created",
			params:            &common.CreatePoolParams{Mode: common.DEFAULTMode, LargeCapacity: true},
			createSVMResponse: nfsTransferSizeCreateSVMResponse(nfsTransferSizeTestSvmName, vlm.LIFTypeSan, vlm.LIFTypeInterCluster),
			wantApplied:       false,
		},
		{
			name:              "large capacity skips when the SVM is missing from the response",
			params:            &common.CreatePoolParams{Mode: common.DEFAULTMode, LargeCapacity: true},
			createSVMResponse: nfsTransferSizeCreateSVMResponse("other-svm", vlm.LIFTypeNas),
			wantApplied:       false,
		},
		{
			name:              "large capacity skips when the create SVM response is nil",
			params:            &common.CreatePoolParams{Mode: common.DEFAULTMode, LargeCapacity: true},
			createSVMResponse: nil,
			wantApplied:       false,
		},
		{
			name:              "default mode without large capacity skips even when a NAS LIF exists",
			params:            &common.CreatePoolParams{Mode: common.DEFAULTMode},
			createSVMResponse: nfsTransferSizeCreateSVMResponse(nfsTransferSizeTestSvmName, vlm.LIFTypeNas),
			wantApplied:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applied, err := runApplyNFSTcpMaxTransferSizeWorkflow(t, tt.params, tt.createSVMResponse, nil)

			require.NoError(t, err)
			assert.Equal(t, tt.wantApplied, applied)
		})
	}
}

func TestApplyNFSTcpMaxTransferSizeOnPoolCreate_ActivityFailurePropagates(t *testing.T) {
	applied, err := runApplyNFSTcpMaxTransferSizeWorkflow(
		t,
		&common.CreatePoolParams{Mode: common.ONTAPMode},
		nil,
		errors.New("nfs update failed"),
	)

	assert.True(t, applied)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nfs update failed")
}
