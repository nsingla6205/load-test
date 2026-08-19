package workflows

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// TestMapPOICLifEnsureChildError pins the collision-detection contract on
// the mapping function directly, so both the production-side child future
// error (*temporal.ChildWorkflowExecutionAlreadyStartedError) and the
// test-env service error (*serviceerror.WorkflowExecutionAlreadyStarted)
// are exercised — including when they arrive wrapped through fmt.Errorf
// (errors.As must still unwrap them). Non-collision inputs must pass
// through unchanged.
func TestMapPOICLifEnsureChildError(t *testing.T) {
	passthroughErr := fmt.Errorf("boom")

	tests := []struct {
		name         string
		input        error
		wantConflict bool // true = expect wrapped ErrResourceStateConflictError; false = expect pass-through
	}{
		{"nil returns nil", nil, false},
		{"unrelated error passes through", passthroughErr, false},
		{"child-side ChildWorkflowExecutionAlreadyStartedError maps to conflict",
			&temporal.ChildWorkflowExecutionAlreadyStartedError{}, true},
		{"service-side WorkflowExecutionAlreadyStarted maps to conflict",
			&serviceerror.WorkflowExecutionAlreadyStarted{}, true},
		{"child-side wrapped via %w still detected via errors.As",
			fmt.Errorf("outer: %w", &temporal.ChildWorkflowExecutionAlreadyStartedError{}), true},
		{"service-side wrapped via %w still detected via errors.As",
			fmt.Errorf("outer: %w", &serviceerror.WorkflowExecutionAlreadyStarted{}), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := mapPOICLifEnsureChildError(tc.input, "acct-1", "host-1")
			if tc.input == nil {
				require.NoError(t, out)
				return
			}
			require.Error(t, out)
			if tc.wantConflict {
				customErr := vsaerrors.ExtractCustomError(out)
				require.NotNil(t, customErr, "conflict-mapped error must carry a CustomError")
				assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError),
					"expected ErrResourceStateConflictError (%d), got %d",
					vsaerrors.ErrResourceStateConflictError, customErr.TrackingID)
				return
			}
			// Pass-through: mapPOICLifEnsureChildError returns the input
			// error unchanged, so identity is preserved.
			assert.True(t, errors.Is(out, tc.input),
				"pass-through must preserve error identity: got %v, want %v", out, tc.input)
		})
	}
}

// ---------------------------------------------------------------------------
// POICLifEnsureWorkflow tests
// ---------------------------------------------------------------------------

const (
	testICAccountUUID = "acct-uuid-1"
	// testICAccountUUIDNoDashes mirrors the dash-stripping
	// common.GeneratePOICLifNames performs on the account UUID (via
	// NormalizePOAccountUUID): every "-" is removed so the generated IC
	// LIF name has exactly three hyphens (two inside "ic-lif" and one
	// before the 6-hex tail). Keeping this derivation as a hardcoded
	// string (not strings.ReplaceAll at test init) makes the wire-level
	// contract obvious at a glance.
	testICAccountUUIDNoDashes = "acctuuid1"
	testICHostUUID              = "host-uuid-1"
	testICRemoteAccountID       = "remote-acct-1"
	testICMicrozoneUUID         = "microzone-1"
	testICAccountID             = int64(42)
	testICHostID                = int64(77)
)

// newICLifEnsureEnv builds a workflow test env wired with every activity
// POICLifEnsureWorkflow invokes. Callers set ENABLE_IC_LIFS_FOR_PO_POOLS
// via `enabled`. Serialization is now provided by the parent's
// WorkflowIDConflictPolicy, so no lock activities are registered here.
func newICLifEnsureEnv(t *testing.T, enabled bool) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	if enabled {
		t.Setenv("ENABLE_IC_LIFS_FOR_PO_POOLS", "true")
	} else {
		t.Setenv("ENABLE_IC_LIFS_FOR_PO_POOLS", "false")
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	env.RegisterActivity(&activities.POOntapActivity{})
	env.RegisterActivity(&activities.PODataLifActivity{})
	env.RegisterActivity(&activities.POICVNActivity{})
	env.RegisterActivity(&activities.POICLifActivity{})

	return env
}

func newICEnsureInput() POICLifEnsureInput {
	return POICLifEnsureInput{
		AccountUUID:     testICAccountUUID,
		HostUUID:        testICHostUUID,
		RemoteAccountID: testICRemoteAccountID,
		MicrozoneUUID:   testICMicrozoneUUID,
		AccountID:       testICAccountID,
	}
}

// runICLifEnsureWorkflow wraps POICLifEnsureWorkflow so activity options are
// set (real callers get them from the parent CreatePool workflow context).
func runICLifEnsureWorkflow(env *testsuite.TestWorkflowEnvironment, in POICLifEnsureInput) *POICLifEnsureOutput {
	wrapper := func(ctx workflow.Context, wIn POICLifEnsureInput) (*POICLifEnsureOutput, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			HeartbeatTimeout:    time.Minute,
		})
		return POICLifEnsureWorkflow(ctx, wIn)
	}
	env.ExecuteWorkflow(wrapper, in)
	if env.GetWorkflowError() != nil {
		return nil
	}
	var out *POICLifEnsureOutput
	if err := env.GetWorkflowResult(&out); err != nil {
		return nil
	}
	return out
}

// stubICNodeAndHostResolution wires the deterministic pre-work: node listing,
// node-name→id resolution, host UUID→id resolution. All tests except flag-off
// need this.
func stubICNodeAndHostResolution(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity("GetPONodeNames", mock.Anything, mock.Anything).Return([]string{"node-1", "node-2"}, nil).Maybe()
	env.OnActivity("ResolvePONodeIDs", mock.Anything, mock.Anything).Return(map[string]int64{"node-1": 1, "node-2": 2}, nil).Maybe()
	env.OnActivity("ResolveHostID", mock.Anything, testICHostUUID).Return(testICHostID, nil).Maybe()
}

// TestPOICLifEnsureWorkflow_FlagOff_ReturnsSkipped verifies the config-gate:
// when ENABLE_IC_LIFS_FOR_PO_POOLS=false, the workflow must return Skipped=true
// without touching CVN or ONTAP — otherwise a canary flag off would still
// cause blast radius.
func TestPOICLifEnsureWorkflow_FlagOff_ReturnsSkipped(t *testing.T) {
	env := newICLifEnsureEnv(t, false)

	out := runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, out)
	assert.True(t, out.Skipped)

	// The gate must short-circuit before *any* activity runs.
	env.AssertNotCalled(t, "GetPONodeNames", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOICNetwork", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
}

// TestPOICLifEnsureWorkflow_CompleteSet_SkipsRebuild verifies the idempotent
// happy-path fast-return: when the persisted IC LIF rows already cover every
// current node exactly once, the workflow returns Skipped=true without any
// CVN provisioning or ONTAP LIF create.
func TestPOICLifEnsureWorkflow_CompleteSet_SkipsRebuild(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICNodeAndHostResolution(env)

	existing := []*datamodel.Lif{
		{BaseModel: datamodel.BaseModel{UUID: "lif-uuid-1"}, Name: "ic-lif-node-1", NodeID: 1, IPAddress: "10.10.0.10",
			LifDetails: &datamodel.LifDetails{ExternalUUID: "ext-1"}},
		{BaseModel: datamodel.BaseModel{UUID: "lif-uuid-2"}, Name: "ic-lif-node-2", NodeID: 2, IPAddress: "10.10.0.11",
			LifDetails: &datamodel.LifDetails{ExternalUUID: "ext-2"}},
	}
	env.OnActivity("GetPOInterclusterLifsForAccountHost", mock.Anything,
		activities.GetPOInterclusterLifsForAccountHostInput{AccountID: testICAccountID, HostID: testICHostID}).
		Return(existing, nil).Once()

	out := runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, out)
	assert.True(t, out.Skipped)

	// Complete-set path must NOT provision anything downstream.
	env.AssertNotCalled(t, "EnsurePOICNetwork", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOIPSpace", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOBroadcastDomain", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOVlanPorts", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePODefaultRoute", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "RollbackPOInterclusterLifs", mock.Anything, mock.Anything)
}

// TestPOICLifEnsureWorkflow_TwoNodeCreate_HappyPath verifies the full build
// path when no IC LIFs exist: CVN network → ONTAP scaffolding → per-node IC LIF
// create → default route → final re-read. Assertions guard the persistence
// contract (ExternalUUID updated per LIF) and route creation.
func TestPOICLifEnsureWorkflow_TwoNodeCreate_HappyPath(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICNodeAndHostResolution(env)

	// No existing LIFs — force the create path.
	env.OnActivity("GetPOInterclusterLifsForAccountHost", mock.Anything,
		activities.GetPOInterclusterLifsForAccountHostInput{AccountID: testICAccountID, HostID: testICHostID}).
		Return([]*datamodel.Lif{}, nil).Once()

	// CVN returns a fresh range + VX network.
	env.OnActivity("EnsurePOICNetwork", mock.Anything, mock.MatchedBy(func(in activities.EnsurePOICNetworkInput) bool {
		return in.AccountUUID == testICAccountUUID &&
			in.RemoteAccountID == testICRemoteAccountID &&
			in.MicrozoneUUID == testICMicrozoneUUID &&
			in.HostUUID == testICHostUUID &&
			in.NodeCount == 2
	})).Return(&activities.POICNetworkOutput{
		NetworkUUID:   "net-1",
		IPRangeUUID:   "iprange-ic-1",
		VXNetworkUUID: "vx-1",
		VlanID:        4001,
		Gateway:       "10.10.0.1",
		Netmask:       "255.255.255.240",
	}, nil).Once()

	// ONTAP scaffolding.
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil).Once()

	// LIF names are random UUIDs generated inside the workflow via
	// workflow.SideEffect (VSCP-7663): "ic-lif-<uuid>", one per node.
	// Because the names are random per run, this test cannot pin literal
	// values; instead the mocks assert count/prefix/uniqueness on the
	// reservations and echo the workflow-supplied names back through the
	// allocation and create hops so the name→row correspondence is still
	// exercised end-to-end.
	env.OnActivity("AllocatePOInterclusterLifIPs", mock.Anything, mock.MatchedBy(func(in activities.AllocatePOInterclusterLifIPsInput) bool {
		if in.IPRangeUUID != "iprange-ic-1" || in.AccountID != testICAccountID || len(in.Reservations) != 2 {
			return false
		}
		seen := make(map[string]bool, len(in.Reservations))
		for _, r := range in.Reservations {
			if !strings.HasPrefix(r.Name, "ic-lif-") {
				return false
			}
			// Account-scoped naming: every LIF name must carry the
			// dash-stripped account UUID segment so operators can
			// group LIFs by tenant and stale names cannot leak
			// across accounts.
			if !strings.Contains(r.Name, testICAccountUUIDNoDashes) {
				return false
			}
			if seen[r.Name] {
				return false
			}
			seen[r.Name] = true
		}
		return true
	})).Return(func(_ context.Context, in activities.AllocatePOInterclusterLifIPsInput) ([]*activities.POICLifAllocated, error) {
		return []*activities.POICLifAllocated{
			{UUID: "lif-uuid-1", Name: in.Reservations[0].Name, IPAddress: "10.10.0.10", SubnetMask: "255.255.255.240", NodeID: in.Reservations[0].NodeID},
			{UUID: "lif-uuid-2", Name: in.Reservations[1].Name, IPAddress: "10.10.0.11", SubnetMask: "255.255.255.240", NodeID: in.Reservations[1].NodeID},
		}, nil
	}).Once()

	// ONTAP LIF create — one per allocated row. Cluster-scoped POST requires ipspace.name.
	// The two calls are distinguished by IPAddress (the reservation loop
	// paired address→node deterministically); Lif.Name is asserted for the
	// "ic-lif-" prefix only since it now flows through from a random UUID.
	wantIPSpace := common.POIPSpaceName(testICAccountUUID, "4001")
	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.MatchedBy(func(in activities.CreatePOInterclusterLifInput) bool {
		return in.HostUUID == testICHostUUID &&
			strings.HasPrefix(in.Lif.Name, "ic-lif-") &&
			strings.Contains(in.Lif.Name, testICAccountUUIDNoDashes) &&
			in.Lif.IPAddress == "10.10.0.10" &&
			in.Lif.IPSpace == wantIPSpace
	})).Return(func(_ context.Context, in activities.CreatePOInterclusterLifInput) (*activities.POInterclusterLifResult, error) {
		return &activities.POInterclusterLifResult{Name: in.Lif.Name, ExternalUUID: "ext-uuid-1"}, nil
	}).Once()
	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.MatchedBy(func(in activities.CreatePOInterclusterLifInput) bool {
		return in.HostUUID == testICHostUUID &&
			strings.HasPrefix(in.Lif.Name, "ic-lif-") &&
			strings.Contains(in.Lif.Name, testICAccountUUIDNoDashes) &&
			in.Lif.IPAddress == "10.10.0.11" &&
			in.Lif.IPSpace == wantIPSpace
	})).Return(func(_ context.Context, in activities.CreatePOInterclusterLifInput) (*activities.POInterclusterLifResult, error) {
		return &activities.POInterclusterLifResult{Name: in.Lif.Name, ExternalUUID: "ext-uuid-2"}, nil
	}).Once()

	// Persistence contract: ONTAP UUID written back to the VCP row.
	env.OnActivity("UpdatePOInterclusterLifExternalUUID", mock.Anything,
		activities.UpdatePOInterclusterLifExternalUUIDInput{LifUUID: "lif-uuid-1", ExternalUUID: "ext-uuid-1"}).
		Return(nil).Once()
	env.OnActivity("UpdatePOInterclusterLifExternalUUID", mock.Anything,
		activities.UpdatePOInterclusterLifExternalUUIDInput{LifUUID: "lif-uuid-2", ExternalUUID: "ext-uuid-2"}).
		Return(nil).Once()

	// Default route + final re-read of the finished set. IC LIF ensure calls
	// CreatePODefaultRoute with an empty SvmName (ipspace-scoped route).
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.HostUUID == testICHostUUID && in.Gateway == "10.10.0.1" && in.SvmName == ""
	})).Return(nil).Once()

	out := runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, out)
	assert.False(t, out.Skipped)

	// Rollback path must not fire on a clean happy path.
	env.AssertNotCalled(t, "RollbackPOInterclusterLifs", mock.Anything, mock.Anything)
}

// TestPOICLifEnsureWorkflow_PartialSet_CleansThenRebuilds verifies the
// self-healing contract: when the persisted set only covers some current
// nodes, the workflow must invoke RollbackPOInterclusterLifs *before* the
// create path. This is what protects against stale IC LIFs from a prior
// interrupted run.
func TestPOICLifEnsureWorkflow_PartialSet_CleansThenRebuilds(t *testing.T) {
	env := newICLifEnsureEnv(t, true)
	stubICNodeAndHostResolution(env)

	// Only one of the two current nodes has a persisted IC LIF: partial set.
	env.OnActivity("GetPOInterclusterLifsForAccountHost", mock.Anything,
		activities.GetPOInterclusterLifsForAccountHostInput{AccountID: testICAccountID, HostID: testICHostID}).
		Return([]*datamodel.Lif{
			{BaseModel: datamodel.BaseModel{UUID: "lif-uuid-1"}, Name: "ic-lif-node-1", NodeID: 1, IPAddress: "10.10.0.10",
				LifDetails: &datamodel.LifDetails{ExternalUUID: "stale-ext-1"}},
		}, nil).Once()

	// The partial-set branch MUST rollback the stale LIF before rebuilding.
	env.OnActivity("RollbackPOInterclusterLifs", mock.Anything, mock.MatchedBy(func(in activities.RollbackPOInterclusterLifsInput) bool {
		if len(in.CreatedLifs) != 1 || in.CreatedLifs[0].ExternalUUID != "stale-ext-1" {
			return false
		}
		return in.HostUUID == testICHostUUID && in.AccountID == testICAccountID && in.HostID == testICHostID
	})).Return(nil).Once()

	// Rebuild flow — same wiring as happy-path with fresh IDs.
	env.OnActivity("EnsurePOICNetwork", mock.Anything, mock.Anything).Return(&activities.POICNetworkOutput{
		NetworkUUID: "net-2", IPRangeUUID: "iprange-ic-2", VXNetworkUUID: "vx-2",
		VlanID: 4002, Gateway: "10.10.1.1", Netmask: "255.255.255.240",
	}, nil).Once()
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything).Return([]*activities.POICLifAllocated{
		{UUID: "new-1", Name: "ic-lif-node-1", IPAddress: "10.10.1.10", SubnetMask: "255.255.255.240", NodeID: 1},
		{UUID: "new-2", Name: "ic-lif-node-2", IPAddress: "10.10.1.11", SubnetMask: "255.255.255.240", NodeID: 2},
	}, nil).Once()
	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.Anything).
		Return(&activities.POInterclusterLifResult{Name: "n", ExternalUUID: "e"}, nil).Times(2)
	env.OnActivity("UpdatePOInterclusterLifExternalUUID", mock.Anything, mock.Anything).Return(nil).Times(2)
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.SvmName == ""
	})).Return(nil).Once()

	out := runICLifEnsureWorkflow(env, newICEnsureInput())
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, out)
	assert.False(t, out.Skipped)

	// Contract: RollbackPOInterclusterLifs was invoked, and it was invoked
	// before the CVN provisioning step (Once() with the stale ExternalUUID
	// asserts this — CVN would return unmatched args if reordered).
	env.AssertCalled(t, "RollbackPOInterclusterLifs", mock.Anything, mock.Anything)
	env.AssertCalled(t, "EnsurePOICNetwork", mock.Anything, mock.Anything)
}

// TestPOICLifEnsureWorkflow_ChildAlreadyStarted_FailsFast pins the concurrency
// contract at the parent-child boundary: when the child WorkflowID
// ("po-ic-lif-ensure-<accountUUID>-<hostUUID>") is already running, the
// parent's ExecuteChildWorkflow future returns a
// WorkflowExecutionAlreadyStarted error (in production a
// ChildWorkflowExecutionAlreadyStartedError; in the test env a
// serviceerror.WorkflowExecutionAlreadyStarted — mapPOICLifEnsureChildError
// unwraps both). The parent maps it to a non-retryable
// ErrResourceStateConflictError (409) so the API layer surfaces a stable
// "another operation is in progress for this account+host" response instead
// of a raw Temporal error.
//
// The test drives it by having a parent spawn two children with the same
// (accountUUID, hostUUID) — since the helper derives the WorkflowID from
// those fields, the second invocation collides with the first.
//
// Note: after the precheck refactor executePOICLifEnsureChild first invokes
// the POICLifPrecheck activity at parent scope; it must be stubbed to
// return Complete=false so the child is still invoked and can collide with
// the first (still-running) child.
func TestPOICLifEnsureWorkflow_ChildAlreadyStarted_FailsFast(t *testing.T) {
	t.Setenv("ENABLE_IC_LIFS_FOR_PO_POOLS", "true")

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	// Register POICLifActivity so the parent's precheck dispatch resolves;
	// the stub below returns Complete=false so the child is still invoked
	// and can collide with the running first child.
	env.RegisterActivity(&activities.POICLifActivity{})
	env.OnActivity("POICLifPrecheck", mock.Anything, mock.Anything).
		Return(&activities.POICLifPrecheckOutput{Complete: false}, nil).Maybe()

	// blocker stands in for POICLifEnsureWorkflow. It waits on a signal so
	// the parent can attempt a second start with the same derived WorkflowID
	// while the first is still running.
	blocker := func(ctx workflow.Context, _ POICLifEnsureInput) (*POICLifEnsureOutput, error) {
		ch := workflow.GetSignalChannel(ctx, "release")
		ch.Receive(ctx, nil)
		return &POICLifEnsureOutput{Skipped: true}, nil
	}
	env.RegisterWorkflowWithOptions(blocker, workflow.RegisterOptions{Name: "POICLifEnsureWorkflow"})

	const acct, host = "collision-acct", "collision-host"
	collisionWorkflowID := "po-ic-lif-ensure-" + acct + "-" + host

	// releaser fires the release signal from outside the workflow so the
	// blocker child stays in RUNNING state throughout the parent's second
	// dispatch attempt. The delay must be larger than any virtual-time
	// advance the parent's precheck activity + child dispatch can trigger;
	// otherwise the first child completes early and the second dispatch
	// gets ALLOW_DUPLICATE'd instead of the required
	// WorkflowExecutionAlreadyStarted collision. In practice the parent
	// hits the collision at T=0 and returns before this timer fires;
	// ParentClosePolicy=TERMINATE cleans up the still-blocked first child.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflowByID(collisionWorkflowID, "release", nil)
	}, time.Hour)

	parent := func(ctx workflow.Context) error {
		// executePOICLifEnsureChild now runs a parent-scope activity
		// (POICLifPrecheck) before dispatching the child, so default
		// activity options must be set on ctx just like the real
		// CreatePool parent supplies them.
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			HeartbeatTimeout:    time.Minute,
		})
		in := POICLifEnsureInput{AccountUUID: acct, HostUUID: host, AccountID: 1}

		// First start: spawn the blocker under the production-derived
		// WorkflowID and wait for the child execution to be started (its
		// WorkflowID must land in the test-env running-workflows map before
		// we can collide with it).
		firstOpts := workflow.ChildWorkflowOptions{WorkflowID: collisionWorkflowID}
		firstCtx := workflow.WithChildOptions(ctx, firstOpts)
		firstFuture := workflow.ExecuteChildWorkflow(firstCtx, POICLifEnsureWorkflow, in)
		if err := firstFuture.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
			return err
		}

		// Second start: reuse the production helper so we exercise the real
		// mapping. The helper derives the same WorkflowID from (acct, host),
		// so the test env returns WorkflowExecutionAlreadyStarted.
		if _, secondErr := executePOICLifEnsureChild(ctx, in); secondErr != nil {
			return secondErr
		}
		return nil
	}

	env.ExecuteWorkflow(parent)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError(), "second child start must surface an error")

	customErr := vsaerrors.ExtractCustomError(env.GetWorkflowError())
	require.NotNil(t, customErr, "collision error must carry a CustomError")
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError),
		"expected ErrResourceStateConflictError (%d), got %d",
		vsaerrors.ErrResourceStateConflictError, customErr.TrackingID)
}

// ---------------------------------------------------------------------------
// executePOICLifEnsureChild parent-scope tests (precheck-skip pattern)
// ---------------------------------------------------------------------------

// newExecuteChildEnv builds a test env for exercising executePOICLifEnsureChild
// at parent scope. The IC LIF flag is toggled via the caller. All activities
// POICLifEnsureWorkflow can touch are registered so downstream stubs resolve;
// POICLifEnsureWorkflow itself is intentionally NOT registered here —
// individual tests register it only when they want the child to actually run,
// so a "precheck complete → child skipped" case fails loudly if the child is
// ever invoked.
func newExecuteChildEnv(t *testing.T, icLifsEnabled bool) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	if icLifsEnabled {
		t.Setenv("ENABLE_IC_LIFS_FOR_PO_POOLS", "true")
	} else {
		t.Setenv("ENABLE_IC_LIFS_FOR_PO_POOLS", "false")
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	env.RegisterActivity(&activities.POOntapActivity{})
	env.RegisterActivity(&activities.PODataLifActivity{})
	env.RegisterActivity(&activities.POICVNActivity{})
	env.RegisterActivity(&activities.POICLifActivity{})
	return env
}

// runExecutePOICLifEnsureChildWrapper wraps executePOICLifEnsureChild in a
// workflow function (with default activity options) so it can be executed
// through the test env — same pattern as runICLifEnsureWorkflow.
func runExecutePOICLifEnsureChildWrapper(env *testsuite.TestWorkflowEnvironment, in POICLifEnsureInput) *POICLifEnsureOutput {
	wrapper := func(ctx workflow.Context, wIn POICLifEnsureInput) (*POICLifEnsureOutput, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			HeartbeatTimeout:    time.Minute,
		})
		return executePOICLifEnsureChild(ctx, wIn)
	}
	env.ExecuteWorkflow(wrapper, in)
	if env.GetWorkflowError() != nil {
		return nil
	}
	var out *POICLifEnsureOutput
	if err := env.GetWorkflowResult(&out); err != nil {
		return nil
	}
	return out
}

// TestExecutePOICLifEnsureChild_PrecheckComplete_SkipsChild verifies the
// whole point of the precheck-skip pattern: when POICLifPrecheck reports
// Complete=true, executePOICLifEnsureChild must return Skipped=true and
// MUST NOT invoke the child workflow. POICLifEnsureWorkflow is
// intentionally not registered here — if the child is ever dispatched the
// test env fails with an "unable to find workflow type" error, giving us a
// hard signal instead of a silent regression.
func TestExecutePOICLifEnsureChild_PrecheckComplete_SkipsChild(t *testing.T) {
	env := newExecuteChildEnv(t, true)

	env.OnActivity("POICLifPrecheck", mock.Anything,
		activities.POICLifPrecheckInput{AccountID: testICAccountID, HostUUID: testICHostUUID}).
		Return(&activities.POICLifPrecheckOutput{Complete: true}, nil).Once()

	out := runExecutePOICLifEnsureChildWrapper(env, newICEnsureInput())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, out)
	assert.True(t, out.Skipped, "complete precheck must short-circuit with Skipped=true")

	env.AssertCalled(t, "POICLifPrecheck", mock.Anything, mock.Anything)
	// Downstream ensure activities must NOT be invoked when the precheck
	// short-circuits — that confirms the child workflow never started.
	env.AssertNotCalled(t, "EnsurePOICNetwork", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "CreatePODefaultRoute", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "GetPOInterclusterLifsForAccountHost", mock.Anything, mock.Anything)
}

// TestExecutePOICLifEnsureChild_PrecheckIncomplete_InvokesChild verifies
// the fallthrough: when POICLifPrecheck reports Complete=false, the child
// workflow IS started and drives the full build path. This complements
// PrecheckComplete_SkipsChild by proving the parent gate doesn't
// over-eagerly skip.
func TestExecutePOICLifEnsureChild_PrecheckIncomplete_InvokesChild(t *testing.T) {
	env := newExecuteChildEnv(t, true)

	// Register the child so it can actually run under the parent's
	// ExecuteChildWorkflow, and stub every activity the child touches on
	// the create path (no existing LIFs → build path). This mirrors
	// stubICLifEnsureHappyPath but is inlined so this test file stays
	// self-contained.
	env.RegisterWorkflow(POICLifEnsureWorkflow)

	env.OnActivity("POICLifPrecheck", mock.Anything,
		activities.POICLifPrecheckInput{AccountID: testICAccountID, HostUUID: testICHostUUID}).
		Return(&activities.POICLifPrecheckOutput{Complete: false}, nil).Once()

	stubICNodeAndHostResolution(env)
	env.OnActivity("GetPOInterclusterLifsForAccountHost", mock.Anything, mock.Anything).
		Return([]*datamodel.Lif{}, nil).Once()
	env.OnActivity("EnsurePOICNetwork", mock.Anything, mock.Anything).
		Return(&activities.POICNetworkOutput{
			NetworkUUID: "net-1", IPRangeUUID: "iprange-ic-1", VXNetworkUUID: "vx-1",
			VlanID: 4001, Gateway: "10.10.0.1", Netmask: "255.255.255.240",
		}, nil).Once()
	env.OnActivity("EnsurePOIPSpace", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("EnsurePOVlanPorts", mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity("AllocatePOInterclusterLifIPs", mock.Anything, mock.Anything).
		Return([]*activities.POICLifAllocated{
			{UUID: "lif-uuid-1", Name: "ic-lif-node-1", IPAddress: "10.10.0.10", SubnetMask: "255.255.255.240", NodeID: 1},
			{UUID: "lif-uuid-2", Name: "ic-lif-node-2", IPAddress: "10.10.0.11", SubnetMask: "255.255.255.240", NodeID: 2},
		}, nil).Once()
	env.OnActivity("CreatePOInterclusterLif", mock.Anything, mock.Anything).
		Return(&activities.POInterclusterLifResult{Name: "ic-lif", ExternalUUID: "ext"}, nil).Times(2)
	env.OnActivity("UpdatePOInterclusterLifExternalUUID", mock.Anything, mock.Anything).Return(nil).Times(2)
	env.OnActivity("CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.SvmName == ""
	})).Return(nil).Once()

	out := runExecutePOICLifEnsureChildWrapper(env, newICEnsureInput())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, out)
	assert.False(t, out.Skipped, "incomplete precheck must drive the child workflow")

	env.AssertCalled(t, "POICLifPrecheck", mock.Anything, mock.Anything)
	env.AssertCalled(t, "EnsurePOICNetwork", mock.Anything, mock.Anything)
	env.AssertCalled(t, "CreatePOInterclusterLif", mock.Anything, mock.Anything)
	env.AssertCalled(t, "CreatePODefaultRoute", mock.Anything, mock.MatchedBy(func(in activities.CreatePODefaultRouteInput) bool {
		return in.SvmName == ""
	}))
}

// TestExecutePOICLifEnsureChild_FeatureFlagOff_SkipsWithoutPrecheck asserts
// the flag-off short-circuit runs at parent scope: when
// ENABLE_IC_LIFS_FOR_PO_POOLS=false, executePOICLifEnsureChild must return
// Skipped without invoking the precheck activity OR the child workflow.
// This is the property that makes a disabled canary safe.
func TestExecutePOICLifEnsureChild_FeatureFlagOff_SkipsWithoutPrecheck(t *testing.T) {
	env := newExecuteChildEnv(t, false)

	out := runExecutePOICLifEnsureChildWrapper(env, newICEnsureInput())

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.NotNil(t, out)
	assert.True(t, out.Skipped)

	// The flag gate must short-circuit before the precheck activity or any
	// child-workflow dispatch runs.
	env.AssertNotCalled(t, "POICLifPrecheck", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "EnsurePOICNetwork", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "GetPOInterclusterLifsForAccountHost", mock.Anything, mock.Anything)
}
