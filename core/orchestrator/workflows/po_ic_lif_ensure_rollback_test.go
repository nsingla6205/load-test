package workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// injectedErr builds a non-retryable Temporal application error carrying msg.
// The rollback matrix relies on the workflow returning after a single
// failed attempt so mocks with .Once() are not blown out by retries.
func injectedErr(msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, "InjectedTestError", nil)
}

// ---------------------------------------------------------------------------
// Rollback matrix for EnsurePOInterclusterLifs.
//
// Per the design (po-pool-intercluster-lifs.md §Ensure Algorithm step 14),
// any failure after step 8 (naming derivation) triggers a best-effort
// ONTAP/VCP rollback. Step 8 sits between EnsurePOICNetwork (CVN, step 7,
// no delete) and EnsurePOIPSpace (step 9). So EnsurePOICNetwork failure
// must NOT invoke rollback; every ONTAP scaffolding and allocation
// failure from step 9 onward MUST invoke it with whatever accrued state
// exists. From CreatePOInterclusterLif onward, createdLifs carries the
// actual ONTAP resources so the rollback can undo them.
//
// These tests inject a failure at each step, assert:
//
//  1. The workflow surfaces the injected error unchanged.
//  2. RollbackPOInterclusterLifs is called (or not) as the design mandates,
//     with the scaffolding fields (IPSpace/BroadcastDomain/BasePort/VlanID/
//     Gateway/NodeNames) populated and CreatedLifs reflecting what
//     succeeded before the failure.
//  3. Activities that would only run after the failure are NOT invoked.
// ---------------------------------------------------------------------------

// stubICOntapNoOp registers permissive success stubs for the ONTAP
// scaffolding activities (EnsurePOIPSpace / EnsurePOBroadcastDomain /
// EnsurePOVlanPorts) so rollback-matrix cases can advance past the ONTAP
// pre-work without repeating boilerplate. Individual tests override these
// with .Once() and an injected error where they need the workflow to fail.
func stubICOntapNoOp(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil).Maybe()
}

// stubICPreOntapSuccess wires the deterministic prefix every rollback case
// shares: successful node/host resolution, an empty existing IC LIF set
// (so the workflow enters the build path), and a fresh CVN network
// output. Callers layer their step-specific failure on top.
func stubICPreOntapSuccess(env *testsuite.TestWorkflowEnvironment) {
	stubICNodeAndHostResolution(env)
	env.OnActivity("GetPOInterclusterLifsForAccountHost", mock.Anything,
		activities.GetPOInterclusterLifsForAccountHostInput{AccountID: testICAccountID, HostID: testICHostID}).
		Return([]*datamodel.Lif{}, nil).Once()
}

// stubICCVNSuccess plugs a canonical CVN output onto env so downstream
// steps see stable ipspace / VLAN identifiers when they inspect the
// rollback input.
func stubICCVNSuccess(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("EnsurePOICNetwork", mock.Anything, mock.Anything).Return(&activities.POICNetworkOutput{
		NetworkUUID:   "net-1",
		IPRangeUUID:   "iprange-ic-1",
		VXNetworkUUID: "vx-1",
		VlanID:        4001,
		Gateway:       "10.10.0.1",
		Netmask:       "255.255.255.240",
	}, nil).Once()
}

// stubICAllocationSuccess returns the two-node allocation result used by
// every case that reaches the LIF create loop.
func stubICAllocationSuccess(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything).
		Return([]*activities.POICLifAllocated{
			{UUID: "ic-lif-uuid-1", Name: "ic-lif-node-1", IPAddress: "10.10.0.10", SubnetMask: "255.255.255.240", NodeID: 1},
			{UUID: "ic-lif-uuid-2", Name: "ic-lif-node-2", IPAddress: "10.10.0.11", SubnetMask: "255.255.255.240", NodeID: 2},
		}, nil).Once()
}

// TestEnsurePOICLifs_Rollback_EnsurePOICNetworkFailure asserts that a CVN
// network failure aborts before any ONTAP or DB writes. Because nothing
// has been written yet the workflow returns without calling the
// centralized rollback activity (creation-order invariant: rollback only
// runs when the LIF create loop has begun).
func TestEnsurePOICLifs_Rollback_EnsurePOICNetworkFailure(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	env.OnActivity("EnsurePOICNetwork", mock.Anything, mock.Anything).
		Return((*activities.POICNetworkOutput)(nil), injectedErr("cvn network failed")).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "cvn network failed")

	env.AssertNotCalled(t, "EnsurePOIPSpace", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOBroadcastDomain", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOVlanPorts", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "RollbackPOInterclusterLifs", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_EnsurePOIPSpaceFailure asserts that an
// IPspace-create failure — the first step after naming derivation —
// invokes RollbackPOInterclusterLifs with the derived scaffolding fields
// populated and CreatedLifs empty. This is the earliest post-step-8
// failure per the design.
func TestEnsurePOICLifs_Rollback_EnsurePOIPSpaceFailure(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).
		Return(injectedErr("ipspace create failed")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(nil).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "ipspace create failed")

	require.NotNil(t, capturedRB, "rollback must be invoked from EnsurePOIPSpace onward")
	assert.Empty(t, capturedRB.CreatedLifs)
	assert.Equal(t, int64(testICAccountID), capturedRB.AccountID)
	assert.Equal(t, int64(testICHostID), capturedRB.HostID)
	assert.Equal(t, testICHostUUID, capturedRB.HostUUID)
	assert.Equal(t, 4001, capturedRB.VlanID)
	assert.Equal(t, "10.10.0.1", capturedRB.Gateway)
	assert.NotEmpty(t, capturedRB.BasePort)
	assert.Contains(t, capturedRB.IPSpace, "4001")
	assert.Contains(t, capturedRB.BroadcastDomain, "4001")
	assert.ElementsMatch(t, []string{"node-1", "node-2"}, capturedRB.NodeNames)

	env.AssertNotCalled(t, "EnsurePOBroadcastDomain", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOVlanPorts", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_EnsurePOBroadcastDomainFailure asserts that
// a broadcast-domain-create failure invokes rollback carrying the same
// scaffolding fields (one step further into the ONTAP setup chain than
// IPspace, but no LIFs yet).
func TestEnsurePOICLifs_Rollback_EnsurePOBroadcastDomainFailure(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).
		Return(injectedErr("bd create failed")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(nil).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "bd create failed")

	require.NotNil(t, capturedRB, "rollback must be invoked from EnsurePOBroadcastDomain onward")
	assert.Empty(t, capturedRB.CreatedLifs)
	assert.Equal(t, int64(testICAccountID), capturedRB.AccountID)
	assert.Equal(t, int64(testICHostID), capturedRB.HostID)
	assert.Equal(t, testICHostUUID, capturedRB.HostUUID)
	assert.Equal(t, 4001, capturedRB.VlanID)
	assert.Equal(t, "10.10.0.1", capturedRB.Gateway)
	assert.NotEmpty(t, capturedRB.BasePort)
	assert.Contains(t, capturedRB.IPSpace, "4001")
	assert.Contains(t, capturedRB.BroadcastDomain, "4001")
	assert.ElementsMatch(t, []string{"node-1", "node-2"}, capturedRB.NodeNames)

	env.AssertNotCalled(t, "EnsurePOVlanPorts", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_EnsurePOVlanPortsFailure covers the VLAN
// port creation step (the actual activity name is EnsurePOVlanPorts, not
// CreatePOVlanNetPort — this is the pre-existing per-node VLAN scaffold
// call). Rollback fires with full mid-tier scaffolding: the IPspace and
// broadcast domain both succeeded before this step and must be torn down.
func TestEnsurePOICLifs_Rollback_EnsurePOVlanPortsFailure(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).
		Return(injectedErr("vlan port create failed")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(nil).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "vlan port create failed")

	require.NotNil(t, capturedRB, "rollback must be invoked from EnsurePOVlanPorts onward")
	assert.Empty(t, capturedRB.CreatedLifs)
	assert.Equal(t, int64(testICAccountID), capturedRB.AccountID)
	assert.Equal(t, int64(testICHostID), capturedRB.HostID)
	assert.Equal(t, testICHostUUID, capturedRB.HostUUID)
	assert.Equal(t, 4001, capturedRB.VlanID)
	assert.Equal(t, "10.10.0.1", capturedRB.Gateway)
	assert.NotEmpty(t, capturedRB.BasePort)
	assert.Contains(t, capturedRB.IPSpace, "4001")
	assert.Contains(t, capturedRB.BroadcastDomain, "4001")
	assert.ElementsMatch(t, []string{"node-1", "node-2"}, capturedRB.NodeNames)

	env.AssertNotCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_AllocateInterclusterLifIPsFailure covers
// the DB reservation step: no ONTAP LIFs exist yet, but the full ONTAP
// scaffolding (IPspace / broadcast domain / VLAN ports) is up and must
// be torn down. CreatedLifs stays empty.
func TestEnsurePOICLifs_Rollback_AllocateInterclusterLifIPsFailure(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	stubICOntapNoOp(env)
	env.OnActivity("AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything).
		Return(([]*activities.POICLifAllocated)(nil), injectedErr("allocation failed")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(nil).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "allocation failed")

	require.NotNil(t, capturedRB, "rollback must be invoked when allocation fails")
	assert.Empty(t, capturedRB.CreatedLifs)
	assert.Equal(t, int64(testICAccountID), capturedRB.AccountID)
	assert.Equal(t, int64(testICHostID), capturedRB.HostID)
	assert.Equal(t, testICHostUUID, capturedRB.HostUUID)
	assert.Equal(t, 4001, capturedRB.VlanID)
	assert.Equal(t, "10.10.0.1", capturedRB.Gateway)
	assert.NotEmpty(t, capturedRB.BasePort)
	assert.Contains(t, capturedRB.IPSpace, "4001")
	assert.Contains(t, capturedRB.BroadcastDomain, "4001")
	assert.ElementsMatch(t, []string{"node-1", "node-2"}, capturedRB.NodeNames)

	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "UpdatePOInterclusterLifExternalUUID", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePODefaultRoute", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_CreatePOInterclusterLifFailure_FirstNode
// asserts that when the very first ONTAP LIF create fails, rollback is
// invoked with:
//
//   - CreatedLifs empty (nothing succeeded).
//   - IPSpace / BroadcastDomain / BasePort / VlanID / Gateway / NodeNames
//     all populated so the rollback activity can still tear down whatever
//     scaffolding did succeed before this failure.
//
// This is the canonical "resource NOT yet created must not get a cleanup
// call" boundary: RollbackPOInterclusterLifs is unit-tested separately for
// the ordering of the actual delete calls; here we only verify the
// workflow hands it the correct pre-failure state.
func TestEnsurePOICLifs_Rollback_CreatePOInterclusterLifFailure_FirstNode(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	stubICOntapNoOp(env)
	stubICAllocationSuccess(env)
	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.Anything).
		Return((*activities.POInterclusterLifResult)(nil), injectedErr("ontap lif create failed")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(nil).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "ontap lif create failed")

	require.NotNil(t, capturedRB, "rollback must be invoked")
	assert.Empty(t, capturedRB.CreatedLifs, "no LIF succeeded, so nothing to clean up on the ONTAP side")
	assert.Equal(t, int64(testICAccountID), capturedRB.AccountID)
	assert.Equal(t, int64(testICHostID), capturedRB.HostID)
	assert.Equal(t, testICHostUUID, capturedRB.HostUUID)
	assert.Equal(t, 4001, capturedRB.VlanID)
	assert.Equal(t, "10.10.0.1", capturedRB.Gateway)
	assert.Contains(t, capturedRB.IPSpace, "4001", "IC LIF ipspace name must include the IC VLAN")
	assert.Contains(t, capturedRB.BroadcastDomain, "4001")
	assert.ElementsMatch(t, []string{"node-1", "node-2"}, capturedRB.NodeNames)

	env.AssertNotCalled(t, "UpdatePOInterclusterLifExternalUUID", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePODefaultRoute", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_CreatePOInterclusterLifFailure_SecondNode
// asserts that a second-node LIF create failure hands the ONTAP result of
// the successful first node to rollback, so exactly one — not two — LIFs
// are cleaned up.
func TestEnsurePOICLifs_Rollback_CreatePOInterclusterLifFailure_SecondNode(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	stubICOntapNoOp(env)
	stubICAllocationSuccess(env)

	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.MatchedBy(func(in activities.CreatePOInterclusterLifInput) bool {
		return in.Lif.Name == "ic-lif-node-1"
	})).Return(&activities.POInterclusterLifResult{Name: "ic-lif-node-1", ExternalUUID: "ext-uuid-1"}, nil).Once()
	env.OnActivity("UpdatePOInterclusterLifExternalUUID", mock.Anything,
		activities.UpdatePOInterclusterLifExternalUUIDInput{LifUUID: "ic-lif-uuid-1", ExternalUUID: "ext-uuid-1"}).
		Return(nil).Once()
	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.MatchedBy(func(in activities.CreatePOInterclusterLifInput) bool {
		return in.Lif.Name == "ic-lif-node-2"
	})).Return((*activities.POInterclusterLifResult)(nil), injectedErr("second lif create failed")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(nil).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "second lif create failed")

	require.NotNil(t, capturedRB)
	require.Len(t, capturedRB.CreatedLifs, 1, "only the first LIF was created")
	assert.Equal(t, "ext-uuid-1", capturedRB.CreatedLifs[0].ExternalUUID)

	env.AssertNotCalled(t, "CreatePODefaultRoute", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_UpdateExternalUUIDFailure asserts that a DB
// failure between the ONTAP LIF create and the UUID write-back rolls back
// with the just-created LIF included in CreatedLifs. This is what
// guarantees the ONTAP resource is cleaned up even if we could not
// persist its handle.
func TestEnsurePOICLifs_Rollback_UpdateExternalUUIDFailure(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	stubICOntapNoOp(env)
	stubICAllocationSuccess(env)

	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.Anything).
		Return(&activities.POInterclusterLifResult{Name: "ic-lif-node-1", ExternalUUID: "ext-uuid-1"}, nil).Once()
	env.OnActivity("UpdatePOInterclusterLifExternalUUID", mock.Anything, mock.Anything).
		Return(injectedErr("db persist failed")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(nil).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "db persist failed")

	require.NotNil(t, capturedRB)
	require.Len(t, capturedRB.CreatedLifs, 1)
	assert.Equal(t, "ext-uuid-1", capturedRB.CreatedLifs[0].ExternalUUID)

	env.AssertNotCalled(t, "CreatePODefaultRoute", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_CreatePODefaultRouteFailure asserts
// the tail case: all LIFs succeeded but the route ensure failed. Both
// LIFs must appear in CreatedLifs. Route cleanup itself is a no-op today
// (documented at poClusterClient.DeleteIPSpaceDefaultRoute), so the
// rollback just needs to be invoked with the accrued state.
func TestEnsurePOICLifs_Rollback_CreatePODefaultRouteFailure(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	stubICOntapNoOp(env)
	stubICAllocationSuccess(env)

	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.MatchedBy(func(in activities.CreatePOInterclusterLifInput) bool {
		return in.Lif.Name == "ic-lif-node-1"
	})).Return(&activities.POInterclusterLifResult{Name: "ic-lif-node-1", ExternalUUID: "ext-uuid-1"}, nil).Once()
	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.MatchedBy(func(in activities.CreatePOInterclusterLifInput) bool {
		return in.Lif.Name == "ic-lif-node-2"
	})).Return(&activities.POInterclusterLifResult{Name: "ic-lif-node-2", ExternalUUID: "ext-uuid-2"}, nil).Once()
	env.OnActivity("UpdatePOInterclusterLifExternalUUID", mock.Anything, mock.Anything).Return(nil).Times(2)

	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.SvmName == ""
	})).Return(injectedErr("route create failed")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(nil).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "route create failed")

	require.NotNil(t, capturedRB)
	require.Len(t, capturedRB.CreatedLifs, 2, "both LIFs must be cleaned up on route failure")
	externalUUIDs := []string{capturedRB.CreatedLifs[0].ExternalUUID, capturedRB.CreatedLifs[1].ExternalUUID}
	assert.ElementsMatch(t, []string{"ext-uuid-1", "ext-uuid-2"}, externalUUIDs)

	env.AssertCalled(t, "UpdatePOInterclusterLifExternalUUID", mock.Anything, mock.Anything)
}

// TestEnsurePOICLifs_Rollback_RollbackActivityFailurePropagatesOriginal
// covers the design contract that even when the rollback activity itself
// returns errors (partial cleanup), the workflow surfaces the original
// failure — not the rollback error — so callers keep the root cause.
func TestEnsurePOICLifs_Rollback_RollbackActivityFailurePropagatesOriginal(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	stubICOntapNoOp(env)
	stubICAllocationSuccess(env)

	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.Anything).
		Return((*activities.POInterclusterLifResult)(nil), injectedErr("original lif failure")).Once()
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.Anything).
		Return(injectedErr("rollback also failed")).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "original lif failure",
		"the surfaced error must be the root cause, not the rollback error")
}

// TestEnsurePOICLifs_Rollback_VlanPortsFailure_RollbackFailsPropagatesOriginal
// pins the same non-masking contract at the newly-covered EnsurePOVlanPorts
// site: even when best-effort rollback surfaces its own error, the caller
// still sees the original ONTAP failure. Picking VlanPorts exercises the
// full mid-tier state (IPspace + broadcast-domain both succeeded before
// the failure) so scaffolding fields on the rollback input are populated
// while CreatedLifs stays empty.
func TestEnsurePOICLifs_Rollback_VlanPortsFailure_RollbackFailsPropagatesOriginal(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICPreOntapSuccess(env)
	stubICCVNSuccess(env)
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).
		Return(injectedErr("original vlan port failure")).Once()

	var capturedRB *activities.RollbackPOInterclusterLifsInput
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		capturedRB = &in
		return true
	})).Return(injectedErr("rollback also failed")).Once()

	runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "original vlan port failure",
		"the surfaced error must be the root cause, not the rollback error")
	assert.NotContains(t, env.GetWorkflowError().Error(), "rollback also failed",
		"rollback errors must not mask the original failure")

	require.NotNil(t, capturedRB, "rollback must still be invoked even though it will return an error")
	assert.Empty(t, capturedRB.CreatedLifs)
	assert.Contains(t, capturedRB.IPSpace, "4001")
	assert.Contains(t, capturedRB.BroadcastDomain, "4001")
	assert.ElementsMatch(t, []string{"node-1", "node-2"}, capturedRB.NodeNames)
}
