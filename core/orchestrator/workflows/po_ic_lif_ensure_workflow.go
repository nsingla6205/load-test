package workflows

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Child-workflow execution knobs for POICLifEnsureWorkflow. The IC ensure
// flow itself is a handful of ONTAP/CVN/DB activities whose own retry
// policies handle transient failures; the workflow-level retry is capped at
// one attempt so a hard failure surfaces to the parent immediately.
const (
	poICLifEnsureWorkflowExecutionTimeout = 30 * time.Minute
	poICLifEnsureWorkflowRunTimeout       = 20 * time.Minute
)

// executePOICLifEnsureChild is the parent-scope entry point for the IC LIF
// ensure flow. Runs three gates: feature flag (ENABLE_IC_LIFS_FOR_PO_POOLS),
// a POICLifPrecheck activity that short-circuits on an already-complete
// set, and finally a child workflow launch. The child's WorkflowID is
// derived from (accountUUID, hostUUID) with ALLOW_DUPLICATE reuse policy,
// so serial re-runs are permitted but a concurrent start on the same key
// is rejected by the server; mapPOICLifEnsureChildError converts that
// rejection to a 409-mapped VCP error.
func executePOICLifEnsureChild(ctx workflow.Context, in POICLifEnsureInput) (*POICLifEnsureOutput, error) {
	logger := util.GetLogger(ctx)

	cfg := common.DefaultPOICLifConfig()
	if !cfg.Enabled {
		logger.Info("PO IC LIF ensure disabled via config; skipping precheck and child workflow")
		return &POICLifEnsureOutput{Skipped: true}, nil
	}

	icLifActivity := &activities.POICLifActivity{}
	var precheck *activities.POICLifPrecheckOutput
	if err := workflow.ExecuteActivity(ctx, icLifActivity.POICLifPrecheck, activities.POICLifPrecheckInput{
		AccountID: in.AccountID,
		HostUUID:  in.HostUUID,
	}).Get(ctx, &precheck); err != nil {
		return nil, err
	}
	if precheck != nil && precheck.Complete {
		logger.Info("IC LIF set already complete at parent precheck; skipping child workflow",
			"accountUUID", in.AccountUUID, "hostUUID", in.HostUUID)
		return &POICLifEnsureOutput{Skipped: true}, nil
	}

	cwo := workflow.ChildWorkflowOptions{
		WorkflowID:               fmt.Sprintf("po-ic-lif-ensure-%s-%s", in.AccountUUID, in.HostUUID),
		TaskQueue:                workflowengine.CustomerTaskQueue,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		WorkflowExecutionTimeout: poICLifEnsureWorkflowExecutionTimeout,
		WorkflowRunTimeout:       poICLifEnsureWorkflowRunTimeout,
		RetryPolicy:              &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	childCtx := workflow.WithChildOptions(ctx, cwo)
	var out *POICLifEnsureOutput
	if err := workflow.ExecuteChildWorkflow(childCtx, POICLifEnsureWorkflow, in).Get(childCtx, &out); err != nil {
		return nil, mapPOICLifEnsureChildError(err, in.AccountUUID, in.HostUUID)
	}
	return out, nil
}

// mapPOICLifEnsureChildError converts a collision on the account+host IC
// LIF ensure WorkflowID into a non-retryable ErrResourceStateConflictError
// so the caller sees the 409 "another IC LIF ensure is already in progress
// for this account+host" semantics; all other errors pass through unchanged.
func mapPOICLifEnsureChildError(err error, accountUUID, hostUUID string) error {
	if err == nil {
		return nil
	}
	var childAlreadyStarted *temporal.ChildWorkflowExecutionAlreadyStartedError
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &childAlreadyStarted) || errors.As(err, &alreadyStarted) {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("another IC LIF ensure is already in progress for account %s host %s: %w",
					accountUUID, hostUUID, err)))
	}
	return err
}

// POICLifEnsureInput carries the identifiers the IC LIF ensure workflow needs
// to provision one intercluster LIF per cluster node.
type POICLifEnsureInput struct {
	AccountUUID     string
	HostUUID        string
	RemoteAccountID string
	MicrozoneUUID   string
	AccountID       int64
}

// POICLifEnsureOutput is the result of POICLifEnsureWorkflow. It reports
// success only via Skipped (true when a fast-path short-circuited, false
// on a completed build path); per-LIF metadata is not returned because
// readers live-query the lifs table directly.
type POICLifEnsureOutput struct {
	Skipped bool
}

// POICLifEnsureWorkflow ensures one intercluster LIF per cluster node exists
// for the given account+host pair. It is idempotent: a complete set of active
// IC LIF rows is returned immediately; a partial set is torn down and rebuilt.
//
// Concurrency is provided by the parent's ExecuteChildWorkflow contract: the
// WorkflowID is derived from (accountUUID, hostUUID) and while a run with
// that ID is still executing the server rejects a second start with
// WorkflowExecutionAlreadyStarted, which the parent maps to a 409-mapped
// VCP error.
//
// In production, executePOICLifEnsureChild already runs a parent-scope
// POICLifPrecheck activity that consolidates the same reads and returns
// Skipped when the persisted set is already complete, so this workflow's
// internal flag check and complete-set fast path are defense-in-depth: they
// protect against a precheck→child-start race where new LIFs land between
// the parent's activity and the child's first workflow task.
func POICLifEnsureWorkflow(ctx workflow.Context, in POICLifEnsureInput) (*POICLifEnsureOutput, error) {
	logger := util.GetLogger(ctx)

	cfg := common.DefaultPOICLifConfig()
	if !cfg.Enabled {
		logger.Info("PO IC LIF ensure disabled via config")
		return &POICLifEnsureOutput{Skipped: true}, nil
	}

	// The child workflow starts with a fresh context, so it must set its
	// own default activity options. Mirror the parent pool-workflow
	// timeouts (StartToClose, retry) so the ONTAP/CVN/DB activities inherit
	// the same envelope they had before this ran inline.
	retryPolicy, rpErr := PopulateRetryPolicyParams()
	if rpErr != nil {
		return nil, rpErr
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	})

	poActivity := &activities.POOntapActivity{}
	poDataLifActivity := &activities.PODataLifActivity{}
	icLifActivity := &activities.POICLifActivity{}
	icCVNActivity := &activities.POICVNActivity{}

	// Enumerate cluster nodes.
	var nodeNames []string
	if nErr := workflow.ExecuteActivity(ctx, poActivity.GetPONodeNames, activities.GetPONodeNamesInput{
		HostUUID: in.HostUUID,
	}).Get(ctx, &nodeNames); nErr != nil {
		return nil, nErr
	}

	// Resolve node name → node DB ID.
	var nodeIDs map[string]int64
	if rErr := workflow.ExecuteActivity(ctx, poDataLifActivity.ResolvePONodeIDs, activities.ResolvePONodeIDsInput{
		HostUUID:  in.HostUUID,
		NodeNames: nodeNames,
	}).Get(ctx, &nodeIDs); rErr != nil {
		return nil, rErr
	}

	// Resolve host UUID → host DB ID via the IC LIF activity.
	var hostID int64
	if hErr := workflow.ExecuteActivity(ctx, icLifActivity.ResolveHostID, in.HostUUID).Get(ctx, &hostID); hErr != nil {
		return nil, hErr
	}

	// Load existing IC LIF rows for this account+host.
	var existingLifs []*datamodel.Lif
	if gErr := workflow.ExecuteActivity(ctx, icLifActivity.GetPOInterclusterLifsForAccountHost,
		activities.GetPOInterclusterLifsForAccountHostInput{
			AccountID: in.AccountID,
			HostID:    hostID,
		}).Get(ctx, &existingLifs); gErr != nil {
		return nil, gErr
	}

	// Check completeness: exactly one active IC LIF per current node.
	if common.IsICLifSetComplete(existingLifs, nodeIDs) {
		logger.Info("IC LIF set already complete", "lifCount", len(existingLifs))
		return &POICLifEnsureOutput{Skipped: true}, nil
	}

	// Partial set: clean up existing rows and ONTAP LIFs before rebuilding.
	if len(existingLifs) > 0 {
		logger.Info("Partial IC LIF set detected, cleaning up before rebuild", "existing", len(existingLifs))
		createdLifs := make([]activities.POInterclusterLifResult, 0, len(existingLifs))
		for _, l := range existingLifs {
			extUUID := ""
			if l.LifDetails != nil {
				extUUID = l.LifDetails.ExternalUUID
			}
			if extUUID != "" {
				createdLifs = append(createdLifs, activities.POInterclusterLifResult{
					Name:         l.Name,
					ExternalUUID: extUUID,
				})
			}
		}
		rollbackIn := activities.RollbackPOInterclusterLifsInput{
			HostUUID:    in.HostUUID,
			AccountID:   in.AccountID,
			HostID:      hostID,
			CreatedLifs: createdLifs,
		}
		if rbErr := workflow.ExecuteActivity(ctx, poActivity.RollbackPOInterclusterLifs, rollbackIn).Get(ctx, nil); rbErr != nil {
			logger.Warn("Partial IC LIF cleanup failed (continuing)", "error", rbErr)
		}
	}

	// CVN: ensure secondary IP range + VX network for IC LIFs.
	icCVNAO := workflow.GetActivityOptions(ctx)
	icCVNAO.StartToCloseTimeout = poDatapathActivityTimeout
	icCVNAO.HeartbeatTimeout = poDatapathActivityTimeout
	icCVNCtx := workflow.WithActivityOptions(ctx, icCVNAO)

	var cvnOut activities.POICNetworkOutput
	if cErr := workflow.ExecuteActivity(icCVNCtx, icCVNActivity.EnsurePOICNetwork, activities.EnsurePOICNetworkInput{
		AccountUUID:     in.AccountUUID,
		RemoteAccountID: in.RemoteAccountID,
		MicrozoneUUID:   in.MicrozoneUUID,
		HostUUID:        in.HostUUID,
		NodeCount:       len(nodeNames),
	}).Get(icCVNCtx, &cvnOut); cErr != nil {
		return nil, cErr
	}

	// Derive ONTAP network names for the IC LIF path.
	vlanIDStr := strconv.Itoa(cvnOut.VlanID)
	ipSpace := common.POIPSpaceName(in.AccountUUID, vlanIDStr)
	broadcastDomain := common.POBroadcastDomainName(in.AccountUUID, vlanIDStr)
	lifHomePort := fmt.Sprintf("%s-%d", cfg.BasePort, cvnOut.VlanID)

	var createdLifs []activities.POInterclusterLifResult
	rollback := func(origErr error) (*POICLifEnsureOutput, error) {
		logger.Warn("IC LIF creation failed, rolling back", "error", origErr)
		rbIn := activities.RollbackPOInterclusterLifsInput{
			HostUUID:        in.HostUUID,
			AccountID:       in.AccountID,
			HostID:          hostID,
			IPSpace:         ipSpace,
			BroadcastDomain: broadcastDomain,
			BasePort:        cfg.BasePort,
			VlanID:          cvnOut.VlanID,
			Gateway:         cvnOut.Gateway,
			NodeNames:       nodeNames,
			CreatedLifs:     createdLifs,
		}
		rbCtx, rbCancel := workflow.NewDisconnectedContext(ctx)
		defer rbCancel()
		if rbErr := workflow.ExecuteActivity(rbCtx, poActivity.RollbackPOInterclusterLifs, rbIn).Get(rbCtx, nil); rbErr != nil {
			logger.Warn("IC LIF rollback had errors", "error", rbErr)
		}
		return nil, origErr
	}

	// Ensure ONTAP IPspace, broadcast domain, VLAN ports.
	if iErr := workflow.ExecuteActivity(ctx, poActivity.EnsurePOIPSpace, activities.EnsurePOIPSpaceInput{
		HostUUID: in.HostUUID,
		Name:     ipSpace,
	}).Get(ctx, nil); iErr != nil {
		return rollback(iErr)
	}

	if bErr := workflow.ExecuteActivity(ctx, poActivity.EnsurePOBroadcastDomain, activities.EnsurePOBroadcastDomainInput{
		HostUUID: in.HostUUID,
		IPSpace:  ipSpace,
		Name:     broadcastDomain,
	}).Get(ctx, nil); bErr != nil {
		return rollback(bErr)
	}

	if vErr := workflow.ExecuteActivity(ctx, poActivity.EnsurePOVlanPorts, activities.EnsurePOVlanPortsInput{
		HostUUID:        in.HostUUID,
		IPSpace:         ipSpace,
		BroadcastDomain: broadcastDomain,
		BasePort:        cfg.BasePort,
		VlanTag:         cvnOut.VlanID,
		NodeNames:       nodeNames,
	}).Get(ctx, nil); vErr != nil {
		return rollback(vErr)
	}

	// Allocate IC LIF IPs from the IP range. LIF names are account-scoped
	// (see common.GeneratePOICLifNames) and captured via SideEffect so
	// replay is deterministic and every retry reuses the same names.
	icLifNames, nameErr := generatePOICLifNames(ctx, in.AccountUUID, len(nodeNames))
	if nameErr != nil {
		return rollback(nameErr)
	}
	reservations := make([]activities.POICLifReservation, 0, len(nodeNames))
	for i, node := range nodeNames {
		nodeID, ok := nodeIDs[node]
		if !ok {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
					fmt.Errorf("missing node id for ONTAP node %q", node)))
		}
		reservations = append(reservations, activities.POICLifReservation{
			Name:   icLifNames[i],
			NodeID: nodeID,
		})
	}

	var allocated []*activities.POICLifAllocated
	if aErr := workflow.ExecuteActivity(ctx, icLifActivity.AllocatePOInterclusterLifIPs,
		activities.AllocatePOInterclusterLifIPsInput{
			IPRangeUUID:  cvnOut.IPRangeUUID,
			AccountID:    in.AccountID,
			Reservations: reservations,
		}).Get(ctx, &allocated); aErr != nil {
		return rollback(aErr)
	}

	// Build a nodeID→nodeName lookup for the create loop.
	nodeIDToName := make(map[int64]string, len(nodeIDs))
	for name, id := range nodeIDs {
		nodeIDToName[id] = name
	}

	// Create each IC LIF on ONTAP and record the external UUID.
	for _, alloc := range allocated {
		nodeName := nodeIDToName[alloc.NodeID]
		lifSpec := activities.POInterclusterLifSpec{
			Name:          alloc.Name,
			IPAddress:     alloc.IPAddress,
			Netmask:       alloc.SubnetMask,
			HomeNode:      nodeName,
			HomePort:      lifHomePort,
			ServicePolicy: common.POInterclusterServicePolicy,
			IPSpace:       ipSpace,
		}
		var result *activities.POInterclusterLifResult
		if cErr := workflow.ExecuteActivity(ctx, poActivity.CreatePOInterclusterLif,
			activities.CreatePOInterclusterLifInput{
				HostUUID: in.HostUUID,
				Lif:      lifSpec,
			}).Get(ctx, &result); cErr != nil {
			return rollback(cErr)
		}
		createdLifs = append(createdLifs, *result)

		// Persist the ONTAP external UUID back to the VCP row.
		if uErr := workflow.ExecuteActivity(ctx, icLifActivity.UpdatePOInterclusterLifExternalUUID,
			activities.UpdatePOInterclusterLifExternalUUIDInput{
				LifUUID:      alloc.UUID,
				ExternalUUID: result.ExternalUUID,
			}).Get(ctx, nil); uErr != nil {
			return rollback(uErr)
		}
	}

	// Ensure the IPspace-scoped default route. CreatePODefaultRoute with an
	// empty SvmName targets the ipspace itself and tolerates the "already
	// exists" case, so retries and re-entrant ensure runs stay idempotent.
	if rErr := workflow.ExecuteActivity(ctx, poActivity.CreatePODefaultRoute,
		activities.CreatePODefaultRouteInput{
			HostUUID: in.HostUUID,
			SvmName:  "",
			Gateway:  cvnOut.Gateway,
			IPSpace:  ipSpace,
		}).Get(ctx, nil); rErr != nil {
		return rollback(rErr)
	}

	return &POICLifEnsureOutput{}, nil
}

// poICLifNamesResult is the SideEffect encoding envelope: it carries the
// generated names or a string form of the generator error so the whole
// value round-trips through Temporal's data converter without losing the
// error signal on replay.
type poICLifNamesResult struct {
	Names []string
	Err   string
}

// generatePOICLifNames returns count unique intercluster LIF names of the
// form "ic-lif-<accountUUID without dashes>-<6hex>".
// The account UUID is threaded in so the generated names are account-scoped
// and operators can group LIFs by tenant at a glance. Randomness is
// captured via workflow.SideEffect so the same names are produced on every
// replay of this workflow task — otherwise a worker retry would allocate
// fresh names and break the name→row correspondence the
// allocate/create/persist loop below relies on. Mirrors
// generatePODataLifPostfixes in pool_workflows.go.
func generatePOICLifNames(ctx workflow.Context, accountUUID string, count int) ([]string, error) {
	var encoded poICLifNamesResult
	if err := workflow.SideEffect(ctx, func(_ workflow.Context) interface{} {
		names, genErr := common.GeneratePOICLifNames(accountUUID, count)
		if genErr != nil {
			return poICLifNamesResult{Err: genErr.Error()}
		}
		return poICLifNamesResult{Names: names}
	}).Get(&encoded); err != nil {
		return nil, err
	}
	if encoded.Err != "" {
		return nil, errors.New(encoded.Err)
	}
	return encoded.Names, nil
}
