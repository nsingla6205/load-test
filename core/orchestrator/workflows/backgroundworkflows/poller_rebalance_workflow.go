package backgroundworkflows

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Harvest config upload endpoint (same defaults as register_node_to_harvest_farm_workflow).
var (
	pollerRebalanceHarvestHost         = env.GetString("HARVEST_HOST", "harvest-farm-service.vcp.svc.cluster.local:3000")
	pollerRebalanceHarvestRestProtocol = env.GetString("HARVEST_REST_PROTOCOL", "http")
	pollerRebalanceHarvestUploadURL    = fmt.Sprintf("%s://%s/config/upload", pollerRebalanceHarvestRestProtocol, pollerRebalanceHarvestHost)
	pollerRebalanceBucketName          = strings.TrimSpace(env.GetString("POLLER_REBALANCE_BUCKET_NAME", ""))
)

func pollerRebalanceUploadDestination() string {
	if pollerRebalanceBucketName != "" {
		return "gs://" + pollerRebalanceBucketName
	}
	return pollerRebalanceHarvestUploadURL
}

// Poller rebalance thresholds (read once at process init from env).
var (
	pollerRebalanceMaxNodesPerGroup = env.GetInt("MAX_NODES_PER_GROUP", 200)
	pollerRebalanceEvictThreshold   = env.GetInt("REBALANCE_EVICT_THRESHOLD", 20)
	pollerRebalanceSoftThreshold    = env.GetInt("REBALANCE_SOFT_THRESHOLD", 50)
	pollerRebalanceIncludeTier2     = env.GetBool("REBALANCE_INCLUDE_TIER2", false)
)

const harvestRebalanceCapacityChangedErrorType = "HarvestRebalanceCapacityChanged"

// PollerRebalanceSnapshotPlanResult is returned by SnapshotAndPlanWorkflow (groups + planned moves).
type PollerRebalanceSnapshotPlanResult struct {
	Groups []datamodel.NodeGroupPollerCount
	Moves  []activities.HarvestRebalanceMove
}

// CleanupEmptyLeasesWorkflowParams optionally passes rebalance staged rows so cleanup only targets source
// node groups from the verified move (distinct SourceGroupID + SourceLeaseName). When nil or Staged is empty,
// CleanupEmptyLeasesWorkflow lists all empty leases in the DB (used when this run had no moves).
type CleanupEmptyLeasesWorkflowParams struct {
	Staged []activities.RebalanceStagedNode
}

// PollerRebalanceWorkflow runs SnapshotAndPlanWorkflow (child 1), upload/validate/commit activities in-process (child 2),
// then CleanupEmptyLeasesWorkflow (child 3 — final cleanup) to drop Kubernetes leases for node groups with zero pollers.
// On validate or commit failure after a successful upload, RollbackRebalanceTargetHarvestActivity removes target-lease YAML.
// Partial upload failures are rolled back inside the upload activity (defer).
func PollerRebalanceWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	var result PollerRebalanceSnapshotPlanResult
	err := workflow.ExecuteChildWorkflow(ctx, SnapshotAndPlanWorkflow).Get(ctx, &result)
	if err != nil {
		logger.Error("SnapshotAndPlanWorkflow failed", "error", err)
		return err
	}
	// Build a simplified summary of moves (source -> target only)
	moveSummary := make([]string, len(result.Moves))
	for i, m := range result.Moves {
		moveSummary[i] = fmt.Sprintf("%d->%d", m.SourceGroupID, m.TargetGroupID)
	}
	logger.Info("PollerRebalanceWorkflow completed snapshot+plan",
		"groups", len(result.Groups), "atomicBatches", len(result.Moves), "moves", moveSummary)

	if len(result.Moves) == 0 {
		return runCleanupEmptyLeasesChildWorkflow(ctx, nil)
	}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{
				"PanicError",
				"HarvestRebalanceInvalidTargetGroup",
				"HarvestRebalancePrepareSourceMismatch",
				"HarvestRebalanceMissingHarvestConfig",
				"HarvestRebalanceCommitSourceMismatch",
				"HarvestRebalanceCommitDrift",
			},
		},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)
	act := &activities.PollerRebalanceActivities{}
	uploadDestination := pollerRebalanceUploadDestination()

	var uploadStage activities.RebalanceUploadStageResult
	err = workflow.ExecuteActivity(actCtx, act.UploadValidateCommitRebalanceActivity, &activities.UploadValidateCommitRebalanceParams{
		Moves:            result.Moves,
		UploadURL:        uploadDestination,
		MaxNodesPerGroup: pollerRebalanceMaxNodesPerGroup,
	}).Get(actCtx, &uploadStage)
	if err != nil {
		if strings.Contains(err.Error(), harvestRebalanceCapacityChangedErrorType) {
			logger.Warn("UploadValidateCommitRebalanceActivity detected stale capacity; plan is outdated and will retry",
				"errorType", harvestRebalanceCapacityChangedErrorType, "error", err)
		}
		logger.Error("UploadValidateCommitRebalanceActivity failed", "error", err)
		return err
	}

	logger.Info("PollerRebalanceWorkflow completed upload+validate+commit", "uploadDestination", uploadDestination)

	if err := runCleanupEmptyLeasesChildWorkflow(ctx, &CleanupEmptyLeasesWorkflowParams{Staged: uploadStage.Staged}); err != nil {
		logger.Error("CleanupEmptyLeasesWorkflow failed", "error", err)
		return err
	}
	return nil
}

// runCleanupEmptyLeasesChildWorkflow runs CleanupEmptyLeasesWorkflow as a child (final cleanup after rebalance or when there were no moves).
func runCleanupEmptyLeasesChildWorkflow(parentCtx workflow.Context, params *CleanupEmptyLeasesWorkflowParams) error {
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return err
	}
	info := workflow.GetInfo(parentCtx)
	childID := fmt.Sprintf("%s.CleanupEmptyLeases", info.WorkflowExecution.ID)
	childCtx := workflow.WithChildOptions(parentCtx, workflow.ChildWorkflowOptions{
		WorkflowID:         childID,
		WorkflowRunTimeout: retryPolicy.StartToCloseTimeout,
		TaskQueue:          info.TaskQueueName,
	})
	return workflow.ExecuteChildWorkflow(childCtx, CleanupEmptyLeasesWorkflow, params).Get(childCtx, nil)
}

// CleanupEmptyLeasesWorkflow runs CleanupEmptyLeaseActivity per candidate. When params.Staged is non-empty,
// candidates are derived from distinct source leases on those staged rows (post-validate move). Otherwise it lists
// all node groups with zero pollers and a non-empty lease from the DB.
func CleanupEmptyLeasesWorkflow(ctx workflow.Context, params *CleanupEmptyLeasesWorkflowParams) error {
	logger := workflow.GetLogger(ctx)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)
	act := &activities.PollerRebalanceActivities{}

	var candidates []activities.EmptyHarvestLeaseCandidate
	if params != nil && len(params.Staged) > 0 {
		candidates = sourceLeaseCleanupCandidatesFromStaged(params.Staged)
		logger.Info("CleanupEmptyLeasesWorkflow: candidates from rebalance staged sources", "count", len(candidates))
	} else {
		err = workflow.ExecuteActivity(actCtx, act.ListEmptyHarvestLeasesForCleanupActivity).Get(actCtx, &candidates)
		if err != nil {
			logger.Error("ListEmptyHarvestLeasesForCleanupActivity failed", "error", err)
			return err
		}
		logger.Info("CleanupEmptyLeasesWorkflow: candidates from DB scan", "count", len(candidates))
	}
	if len(candidates) == 0 {
		logger.Info("CleanupEmptyLeasesWorkflow: no empty leases to clean")
		return nil
	}

	// Stable order for reproducible behavior and heartbeats.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].NodeGroupID != candidates[j].NodeGroupID {
			return candidates[i].NodeGroupID < candidates[j].NodeGroupID
		}
		return candidates[i].LeaseName < candidates[j].LeaseName
	})

	for i, c := range candidates {
		workflow.GetLogger(ctx).Info("CleanupEmptyLeasesWorkflow cleaning lease",
			"index", i+1, "total", len(candidates), "nodeGroupID", c.NodeGroupID, "lease", c.LeaseName)
		err = workflow.ExecuteActivity(actCtx, act.CleanupEmptyLeaseActivity, &activities.CleanupEmptyLeaseParams{
			NodeGroupID: c.NodeGroupID,
			LeaseName:   c.LeaseName,
		}).Get(actCtx, nil)
		if err != nil {
			logger.Error("CleanupEmptyLeaseActivity failed", "error", err, "nodeGroupID", c.NodeGroupID, "lease", c.LeaseName)
			return err
		}
	}

	logger.Info("CleanupEmptyLeasesWorkflow completed", "processed", len(candidates))
	return nil
}

// sourceLeaseCleanupCandidatesFromStaged returns one candidate per distinct SourceGroupID with a non-empty SourceLeaseName.
func sourceLeaseCleanupCandidatesFromStaged(staged []activities.RebalanceStagedNode) []activities.EmptyHarvestLeaseCandidate {
	seen := make(map[int64]struct{})
	var out []activities.EmptyHarvestLeaseCandidate
	for _, s := range staged {
		lease := strings.TrimSpace(s.SourceLeaseName)
		if lease == "" {
			continue
		}
		if _, dup := seen[s.SourceGroupID]; dup {
			continue
		}
		seen[s.SourceGroupID] = struct{}{}
		out = append(out, activities.EmptyHarvestLeaseCandidate{
			NodeGroupID: s.SourceGroupID,
			LeaseName:   lease,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeGroupID != out[j].NodeGroupID {
			return out[i].NodeGroupID < out[j].NodeGroupID
		}
		return out[i].LeaseName < out[j].LeaseName
	})
	return out
}

// SnapshotAndPlanWorkflow loads harvest node-group counts, classifies tiers, and builds a fill-first rebalance plan.
func SnapshotAndPlanWorkflow(ctx workflow.Context) (*PollerRebalanceSnapshotPlanResult, error) {
	logger := workflow.GetLogger(ctx)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	act := &activities.PollerRebalanceActivities{}

	var snap activities.HarvestNodeGroupsSnapshotResult
	err = workflow.ExecuteActivity(ctx, act.GetNodeGroupsWithPollerCountsActivity).Get(ctx, &snap)
	if err != nil {
		logger.Error("GetNodeGroupsWithPollerCountsActivity failed", "error", err)
		return nil, err
	}

	planParams := &activities.BuildPollerRebalancePlanParams{
		NodeGroups:       snap.Groups,
		MaxNodesPerGroup: pollerRebalanceMaxNodesPerGroup,
		EvictThreshold:   pollerRebalanceEvictThreshold,
		SoftThreshold:    pollerRebalanceSoftThreshold,
		IncludeTier2:     pollerRebalanceIncludeTier2,
	}
	var plan activities.HarvestRebalancePlanOutput
	err = workflow.ExecuteActivity(ctx, act.BuildPollerRebalancePlanActivity, planParams).Get(ctx, &plan)
	if err != nil {
		logger.Error("BuildPollerRebalancePlanActivity failed", "error", err)
		return nil, err
	}

	return &PollerRebalanceSnapshotPlanResult{
		Groups: snap.Groups,
		Moves:  plan.Moves,
	}, nil
}
