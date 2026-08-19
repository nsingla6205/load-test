package workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// newPOTestEnvICLifFlag builds a workflow test env identical to newPOTestEnv
// but with ENABLE_IC_LIFS_FOR_PO_POOLS forced to the caller-supplied value.
// This is the pool-workflow analogue of newICLifEnsureEnv so create
// wiring can be exercised with the IC LIF flag on or off deterministically.
func newPOTestEnvICLifFlag(t *testing.T, icEnabled bool) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	if icEnabled {
		t.Setenv("ENABLE_IC_LIFS_FOR_PO_POOLS", "true")
	} else {
		t.Setenv("ENABLE_IC_LIFS_FOR_PO_POOLS", "false")
	}

	origVerifyKMS := verifyKmsConfigReachability
	verifyKmsConfigReachability = func(_ workflow.Context, _ string) error { return nil }
	t.Cleanup(func() { verifyKmsConfigReachability = origVerifyKMS })

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	env.RegisterActivity(&activities.CommonActivities{})
	env.RegisterActivity(&activities.PoolActivity{})
	env.RegisterActivity(&activities.SvmActivity{})
	env.RegisterActivity(&activities.POOntapActivity{})
	env.RegisterActivity(activities.NewCVNActivity())
	env.RegisterActivity(&activities.IPRangeActivity{})
	env.RegisterActivity(&activities.PODataLifActivity{})
	env.RegisterActivity(&activities.POICVNActivity{})
	env.RegisterActivity(&activities.POICLifActivity{})

	// Register POICLifEnsureWorkflow so ExecuteChildWorkflow from the parent
	// pool workflows can resolve it in the test env. The IC LIF ensure flow
	// runs its own activities through the same test env, so no extra wiring
	// is needed beyond activity registration above.
	env.RegisterWorkflow(POICLifEnsureWorkflow)

	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("GetJob", mock.Anything, "default-test-workflow-id").Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	return env
}

// stubPOCreateHappyPathForICOn wires the pool-datapath activities needed by
// runPOPoolCreate when the IC LIF flag is ON. It intentionally uses
// permissive matchers so both the IC path (its own IPspace/broadcast-domain
// on the IC VLAN) and the pool path (its own IPspace/broadcast-domain on
// the datapath VLAN) can share the same stubs.
func stubPOCreateHappyPathForICOn(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil)
	env.OnActivity("ResolvePONodeIDs", mock.Anything, mock.Anything).Return(map[string]int64{"node-1": 1, "node-2": 2}, nil)
	env.OnActivity("PreSelectDataLifIPRange", mock.Anything, mock.Anything).Return("range-1", nil)
	env.OnActivity("CreateDataPath", mock.Anything, mock.Anything).Return(poDatapathResponse(), nil)
	env.OnActivity("FindTenancyProject", mock.Anything, mock.Anything).Return("tenant-project-123", nil)
	env.OnActivity("GetSnHostProject", mock.Anything, "tenant-project-123").Return("sn-host-project-456", nil)
	env.OnActivity("SavePoolWithClusterDetails", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("AllocatePOSVMName", mock.Anything, mock.Anything).Return("svm_testaccountuuid_0a1b2c3d", nil)
	env.OnActivity("CreatePOSvm", mock.Anything, mock.Anything).Return(&activities.CreatePOSvmOutput{SvmName: "po-svm-01", SvmUUID: "svm-uuid"}, nil)
	env.OnActivity("EnsurePOSvmMgmtPolicies", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("SavePOSVMRecord", mock.Anything, mock.Anything).Return(&datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 99}, Name: "svm_testaccountuuid_0a1b2c3d"}, nil)
	env.OnActivity("AllocatePODataLifIPs", mock.Anything, mock.Anything).Return(poAllocatedLifs(), nil)
	env.OnActivity("CreatePODataLifs", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Pool{}, nil)
}

// stubICLifEnsureHappyPath wires the IC LIF ensure activities for a
// two-node cluster that starts with no existing IC LIFs. It provisions the
// full build path (CVN → ONTAP → DB → route) so the enclosing pool
// workflow can complete. The ONTAP scaffolding stubs (IPSpace / broadcast
// domain / VLAN ports) are permissive so this helper composes cleanly with
// tests that also drive the pool datapath through the same activity
// names.
//
// The parent-scope POICLifPrecheck activity is stubbed to return
// Complete=false so executePOICLifEnsureChild falls through to spawning
// the child workflow and the rest of the stubs drive the child's build
// path.
func stubICLifEnsureHappyPath(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("POICLifPrecheck", mock.Anything, mock.Anything).
		Return(&activities.POICLifPrecheckOutput{Complete: false}, nil).Maybe()

	env.OnActivity("ResolveHostID", mock.Anything, mock.Anything).Return(int64(77), nil).Maybe()

	env.OnActivity("GetPOInterclusterLifsForAccountHost", mock.Anything, mock.MatchedBy(func(in activities.GetPOInterclusterLifsForAccountHostInput) bool {
		return in.AccountID == 1 && in.HostID == 77
	})).Return([]*datamodel.Lif{}, nil).Once()

	env.OnActivity("EnsurePOICNetwork", mock.Anything, mock.Anything).Return(&activities.POICNetworkOutput{
		NetworkUUID: "net-1", IPRangeUUID: "iprange-ic-1", VXNetworkUUID: "vx-1",
		VlanID: 4001, Gateway: "10.10.0.1", Netmask: "255.255.255.240",
	}, nil).Once()

	// The IC ensure workflow reuses the ONTAP scaffolding activities
	// (EnsurePOIPSpace / EnsurePOBroadcastDomain / EnsurePOVlanPorts).
	// Permissive stubs let create-side tests share their existing pool
	// datapath stubs with the IC ensure path.
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything).Return([]*activities.POICLifAllocated{
		{UUID: "ic-lif-1", Name: "ic-lif-node-1", IPAddress: "10.10.0.10", SubnetMask: "255.255.255.240", NodeID: 1},
		{UUID: "ic-lif-2", Name: "ic-lif-node-2", IPAddress: "10.10.0.11", SubnetMask: "255.255.255.240", NodeID: 2},
	}, nil).Once()

	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.Anything).
		Return(&activities.POInterclusterLifResult{Name: "ic-lif", ExternalUUID: "ext-uuid"}, nil).Times(2)
	env.OnActivity("UpdatePOInterclusterLifExternalUUID", mock.Anything, mock.Anything).Return(nil).Times(2)

	// IC LIF ensure uses CreatePODefaultRoute with an empty SvmName.
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.SvmName == ""
	})).Return(nil).Once()
}

// TestCreatePOPoolWorkflow_ICLifsFlagOn_InvokesEnsureICNetwork covers the
// create-side wiring end to end: when ENABLE_IC_LIFS_FOR_PO_POOLS=true and
// the pool is EXTREME (a PO service level), CreatePoolWorkflow must
// invoke EnsurePOICNetwork with the pool's account UUID, host UUID and
// vendor subnet identifier. The IC LIF path finishes with a route being
// created; IC LIF IPs are not mirrored onto pool.ClusterDetails (readers
// live-query them from the lifs table), so this test asserts only that
// the ensure activities ran.
func TestCreatePOPoolWorkflow_ICLifsFlagOn_InvokesEnsureICNetwork(t *testing.T) {
	env := newPOTestEnvICLifFlag(t, true)
	stubPOCreateHappyPathForICOn(env)
	stubICLifEnsureHappyPath(env)

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	env.AssertCalled(t, "EnsurePOICNetwork", mock.Anything, mock.MatchedBy(func(in activities.EnsurePOICNetworkInput) bool {
		return in.AccountUUID == "test-account-uuid" &&
			in.RemoteAccountID == "projects/p/global/networks/customer-net" &&
			in.HostUUID == "host-uuid-1" &&
			in.MicrozoneUUID == "host-uuid-1" &&
			in.NodeCount == 2
	}))
	env.AssertCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
	env.AssertCalled(t, "CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.SvmName == ""
	}))
}

// TestCreatePOPoolWorkflow_ICLifsFlagOff_SkipsEnsure covers the current
// gating behavior for the create side: when the flag is off, the ensure
// workflow short-circuits (Skipped=true) before touching CVN, ONTAP or
// the IC LIF lock. The pool workflow still completes because IC LIF
// ensure is invoked (and skipped) rather than bypassed at the call site.
func TestCreatePOPoolWorkflow_ICLifsFlagOff_SkipsEnsure(t *testing.T) {
	env := newPOTestEnvICLifFlag(t, false)
	stubPOCreateHappyPathForICOn(env)

	env.ExecuteWorkflow(CreatePoolWorkflow, newPOParams(), newPOPool())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Flag off short-circuits before any IC LIF activity runs (parent-scope
	// precheck, node resolution, CVN, DB read/write, ONTAP LIF create, or
	// route).
	env.AssertNotCalled(t, "POICLifPrecheck", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOICNetwork", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "GetPOInterclusterLifsForAccountHost", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.SvmName == ""
	}))
	env.AssertNotCalled(t, "ResolveHostID", mock.Anything, mock.Anything)
}

// TestDeletePOPoolWorkflow_DoesNotInvokeICLifCleanup guards the "IC LIF
// resources are account+host lifecycle, not pool lifecycle" design
// non-goal: PO pool delete must never delete IC LIFs, routes, ports,
// broadcast domains, IPspaces, VX networks, or secondary ranges. If a
// future refactor accidentally wires IC cleanup into delete, this test
// will fail.
func TestDeletePOPoolWorkflow_DoesNotInvokeICLifCleanup(t *testing.T) {
	env := newPOTestEnvICLifFlag(t, true)
	env.RegisterActivity(&kms_activities.KmsConfigActivity{})
	env.RegisterActivity(&activities.CancellationActivity{})

	pool := newPODeletePool()
	env.OnActivity("GetPool", mock.Anything, mock.Anything).Return(pool, nil)
	env.OnActivity("DeletingPoolResources", mock.Anything, mock.Anything).Return(pool, nil).Once()
	env.OnActivity("GetPOSvmForPoolID", mock.Anything, int64(1)).Return(poDeleteSVM(), nil).Once()
	env.OnActivity("DeletePOSvm", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("ReleaseDataLifReservations", mock.Anything, activities.ReleaseDataLifReservationsInput{SvmID: 99}).Return(nil).Once()
	env.OnActivity("SoftDeleteSvm", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("DeletePoolResources", mock.Anything, mock.Anything).Return(pool, nil).Once()
	env.OnActivity("CleanupPOAccountClusterNetworkIfIPRangeUnused", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("VerifyVsaKmsReachabilityActivity", mock.Anything, "kms-1", false).Return(nil).Once()

	params := &common.DeletePoolParams{PoolID: "po-pool-uuid", AccountName: "test-account"}
	env.ExecuteWorkflow(DeletePoolWorkflow, params, pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// None of these are legitimate for a PO pool delete under v1.
	env.AssertNotCalled(t, "EnsurePOICNetwork", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "GetPOInterclusterLifsForAccountHost", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "RollbackPOInterclusterLifs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.SvmName == ""
	}))
	// A workflow-level regression that starts calling POICLifEnsureWorkflow
	// from delete would surface through the node/host resolution activities.
	env.AssertNotCalled(t, "ResolveHostID", mock.Anything, mock.Anything)

	assert.Nil(t, env.GetWorkflowError())
}
