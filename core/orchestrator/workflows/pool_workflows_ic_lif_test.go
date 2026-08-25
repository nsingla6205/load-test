package workflows

import (
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// runEnsureInterclusterSecondaryRange drives the helper through a workflow environment so the
// activity ordering it enforces is exercised, not just the activity bodies.
func runEnsureInterclusterSecondaryRange(t *testing.T, env *testsuite.TestWorkflowEnvironment,
	poolActivity *activities.PoolActivity, params *common.CreatePoolParams, tenancyDetails *common.TenancyInfo) {
	t.Helper()

	const workflowName = "TestEnsureInterclusterSecondaryRangeWrapper"
	wrapper := func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		return ensureInterclusterSecondaryRange(ctx, ctx, poolActivity, params, "pool-uuid", "vsa-subnet",
			tenancyDetails, time.Minute)
	}
	env.RegisterWorkflowWithOptions(wrapper, workflow.RegisterOptions{Name: workflowName})
	env.ExecuteWorkflow(workflowName)
}

// A patch that never lands must release the persist-first ip_ranges claim.
func TestEnsureInterclusterSecondaryRange_PatchFailureDoesNotPersistIPRange(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	poolActivity := &activities.PoolActivity{}
	env.RegisterActivity(poolActivity)

	origWait := WaitForGCPNetworkOperationStatus
	t.Cleanup(func() { WaitForGCPNetworkOperationStatus = origWait })
	WaitForGCPNetworkOperationStatus = func(ctx workflow.Context, pa *activities.PoolActivity,
		operations *[]common.Operations, timeout time.Duration) error {
		return errors.New("patch operation failed")
	}

	env.OnActivity("EnsureInterclusterSecondaryRangeOp", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything).
		Return(&common.EnsureICSecondaryRangeResult{
			OperationName:              "patch-op-1",
			Project:                    "host-project",
			CarvedSecondaryCIDR:        "10.45.0.0/29",
			ParentInterclusterRangeID:  22,
			IPRangeID:                  77,
			ReleaseClaimOnPatchFailure: true,
		}, nil)

	releaseCalled := false
	env.OnActivity("ReleaseInterclusterIPRangeClaim", mock.Anything, int64(77)).
		Run(func(mock.Arguments) { releaseCalled = true }).
		Return(nil)

	persistCalled := false
	env.OnActivity("PersistInterclusterIPRange", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { persistCalled = true }).
		Return(int64(77), nil).
		Maybe()

	tenancyDetails := &common.TenancyInfo{SnHostProject: "host-project"}
	params := &common.CreatePoolParams{SecondaryIpRange: []string{"10.45.0.0/16"}}
	runEnsureInterclusterSecondaryRange(t, env, poolActivity, params, tenancyDetails)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.True(t, releaseCalled, "a failed patch wait must release the claimed ip_ranges row")
	assert.False(t, persistCalled, "a failed patch must not mark the parent IN_USE")
	assert.Zero(t, tenancyDetails.IPRangeID)
}

// The parent is marked IN_USE only once the patch operation has completed.
func TestEnsureInterclusterSecondaryRange_PersistsAfterPatchSucceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	poolActivity := &activities.PoolActivity{}
	env.RegisterActivity(poolActivity)

	origWait := WaitForGCPNetworkOperationStatus
	t.Cleanup(func() { WaitForGCPNetworkOperationStatus = origWait })
	waited := false
	WaitForGCPNetworkOperationStatus = func(ctx workflow.Context, pa *activities.PoolActivity,
		operations *[]common.Operations, timeout time.Duration) error {
		waited = true
		return nil
	}

	env.OnActivity("EnsureInterclusterSecondaryRangeOp", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything).
		Return(&common.EnsureICSecondaryRangeResult{
			OperationName:              "patch-op-1",
			Project:                    "host-project",
			CarvedSecondaryCIDR:        "10.45.0.0/29",
			ParentInterclusterRangeID:  22,
			IPRangeID:                  77,
			ReleaseClaimOnPatchFailure: true,
		}, nil)
	env.OnActivity("PersistInterclusterIPRange", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).
		Return(int64(77), nil)

	tenancyDetails := &common.TenancyInfo{SnHostProject: "host-project"}
	params := &common.CreatePoolParams{SecondaryIpRange: []string{"10.45.0.0/16"}}
	runEnsureInterclusterSecondaryRange(t, env, poolActivity, params, tenancyDetails)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.True(t, waited, "the patch operation must be awaited before the parent is marked IN_USE")
	assert.Equal(t, int64(77), tenancyDetails.IPRangeID)
}

// A subnet that already carries a carved range short-circuits: no patch, no second ip_ranges row.
// This is what makes the reuse path and Temporal retries no-ops.
func TestEnsureInterclusterSecondaryRange_ReusesExistingCarve(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	poolActivity := &activities.PoolActivity{}
	env.RegisterActivity(poolActivity)

	env.OnActivity("EnsureInterclusterSecondaryRangeOp", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything).
		Return(&common.EnsureICSecondaryRangeResult{IPRangeID: 55}, nil)

	persistCalled := false
	env.OnActivity("PersistInterclusterIPRange", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { persistCalled = true }).
		Return(int64(77), nil).
		Maybe()

	tenancyDetails := &common.TenancyInfo{SnHostProject: "host-project"}
	params := &common.CreatePoolParams{SecondaryIpRange: []string{"10.45.0.0/16"}}
	runEnsureInterclusterSecondaryRange(t, env, poolActivity, params, tenancyDetails)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.False(t, persistCalled, "an existing carve must not be persisted again")
	assert.Equal(t, int64(55), tenancyDetails.IPRangeID)
}

func TestEnsureInterclusterSecondaryRange_PatchFailureDoesNotReleaseSharedRow(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	poolActivity := &activities.PoolActivity{}
	env.RegisterActivity(poolActivity)

	origWait := WaitForGCPNetworkOperationStatus
	t.Cleanup(func() { WaitForGCPNetworkOperationStatus = origWait })
	WaitForGCPNetworkOperationStatus = func(ctx workflow.Context, pa *activities.PoolActivity,
		operations *[]common.Operations, timeout time.Duration) error {
		return errors.New("patch operation failed")
	}

	env.OnActivity("EnsureInterclusterSecondaryRangeOp", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything).
		Return(&common.EnsureICSecondaryRangeResult{
			OperationName:              "patch-op-shared",
			Project:                    "host-project",
			CarvedSecondaryCIDR:        "10.45.0.0/29",
			ParentInterclusterRangeID:  22,
			IPRangeID:                  55,
			ReleaseClaimOnPatchFailure: false,
		}, nil)

	releaseCalled := false
	env.OnActivity("ReleaseInterclusterIPRangeClaim", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { releaseCalled = true }).
		Return(nil).
		Maybe()

	tenancyDetails := &common.TenancyInfo{SnHostProject: "host-project"}
	params := &common.CreatePoolParams{SecondaryIpRange: []string{"10.45.0.0/16"}}
	runEnsureInterclusterSecondaryRange(t, env, poolActivity, params, tenancyDetails)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.False(t, releaseCalled, "a failed patch wait must not release a shared pre-existing ip_ranges row")
}

func TestPoolDataSubnetWorkFlow_Delete_FailsWhenInterclusterLifCleanupFails(t *testing.T) {
	origFlag := icLifAddressSpaceEnabled
	t.Cleanup(func() { icLifAddressSpaceEnabled = origFlag })
	icLifAddressSpaceEnabled = true

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(logrus.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	commonActivity := &activities.CommonActivities{}
	env.RegisterActivity(&activities.PoolActivity{})
	env.RegisterActivity(&activities.SvmActivity{})
	env.RegisterActivity(commonActivity)

	const poolID int64 = 45
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid", ID: poolID},
		Name:      "test-pool",
		AccountID: 1,
		Account:   &datamodel.Account{Name: "test-account"},
		ClusterDetails: datamodel.ClusterDetails{
			SubnetNames:           []string{"test-subnet"},
			RegionalTenantProject: "test-tenant-123",
		},
	}

	origWait := WaitForGCPNetworkOperationStatus
	t.Cleanup(func() { WaitForGCPNetworkOperationStatus = origWait })
	WaitForGCPNetworkOperationStatus = func(workflow.Context, *activities.PoolActivity, *[]common.Operations, time.Duration) error {
		return nil
	}

	env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Once()
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Twice()
	env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("GetPoolTenancyInfo", mock.Anything, mock.Anything).Return(&common.TenancyInfo{}, nil)
	env.OnActivity("ReleaseDataSubnetOp", mock.Anything, mock.Anything).Return(&[]common.Operations{
		{OperationName: "delete-op", Project: "test-tenant-123", IsRegionalResource: true},
	}, nil)
	env.OnActivity("DeleteInterclusterLifsForPool", mock.Anything, poolID).
		Return(errors.New("db cleanup failed")).Once()

	ipRangeDeleteCalled := false
	env.OnActivity("DeleteInterclusterIPRangeForSubnet", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { ipRangeDeleteCalled = true }).
		Return(nil).
		Maybe()

	params := &common.CreatePoolParams{AccountName: "test-account", Region: "us-central1"}
	env.ExecuteWorkflow(PoolDataSubnetWorkFlow, params, "pool-uuid", "test-tenant-123", int64(1), models.ResourceOperationDelete)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.False(t, ipRangeDeleteCalled, "ip_ranges delete must not run when LIF cleanup fails")
}
