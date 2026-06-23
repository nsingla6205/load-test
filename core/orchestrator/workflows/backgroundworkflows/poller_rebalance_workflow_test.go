package backgroundworkflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/testsuite"
)

func TestSourceLeaseCleanupCandidatesFromStaged_DedupesAndSorts(t *testing.T) {
	out := sourceLeaseCleanupCandidatesFromStaged([]activities.RebalanceStagedNode{
		{SourceGroupID: 2, SourceLeaseName: "lease-b"},
		{SourceGroupID: 1, SourceLeaseName: "  "},
		{SourceGroupID: 2, SourceLeaseName: "ignored-dup"},
		{SourceGroupID: 1, SourceLeaseName: "lease-a"},
	})
	require.Len(t, out, 2)
	assert.Equal(t, int64(1), out[0].NodeGroupID)
	assert.Equal(t, "lease-a", out[0].LeaseName)
	assert.Equal(t, int64(2), out[1].NodeGroupID)
	assert.Equal(t, "lease-b", out[1].LeaseName)
}

func TestPollerRebalanceWorkflow_NoMovesRunsCleanupChild(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollerRebalanceWorkflow)

	plan := &PollerRebalanceSnapshotPlanResult{
		Groups: []datamodel.NodeGroupPollerCount{},
		Moves:  []activities.HarvestRebalanceMove{},
	}
	env.OnWorkflow(SnapshotAndPlanWorkflow, mock.Anything).Return(plan, nil)
	env.OnWorkflow(CleanupEmptyLeasesWorkflow, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PollerRebalanceWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPollerRebalanceWorkflow_SnapshotFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollerRebalanceWorkflow)
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)

	env.OnWorkflow(SnapshotAndPlanWorkflow, mock.Anything).Return(nil, errors.New("snapshot error"))

	env.ExecuteWorkflow(PollerRebalanceWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "snapshot error")
}

func TestPollerRebalanceWorkflow_UploadFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollerRebalanceWorkflow)
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)

	plan := &PollerRebalanceSnapshotPlanResult{
		Groups: []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, Count: 5}},
		Moves:  []activities.HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
	}
	env.OnWorkflow(SnapshotAndPlanWorkflow, mock.Anything).Return(plan, nil)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.UploadValidateCommitRebalanceActivity, mock.Anything, mock.Anything).Return(nil, errors.New("upload failed"))

	env.ExecuteWorkflow(PollerRebalanceWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "upload failed")
}

func TestPollerRebalanceWorkflow_CombinedActivityFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollerRebalanceWorkflow)
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)

	plan := &PollerRebalanceSnapshotPlanResult{
		Groups: []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, Count: 5}},
		Moves:  []activities.HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
	}
	env.OnWorkflow(SnapshotAndPlanWorkflow, mock.Anything).Return(plan, nil)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.UploadValidateCommitRebalanceActivity, mock.Anything, mock.Anything).Return(nil, errors.New("validate timeout"))

	env.ExecuteWorkflow(PollerRebalanceWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "validate timeout")
}

func TestPollerRebalanceWorkflow_CommitCapacityChanged(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollerRebalanceWorkflow)
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)

	plan := &PollerRebalanceSnapshotPlanResult{
		Groups: []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, Count: 5}},
		Moves:  []activities.HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
	}
	env.OnWorkflow(SnapshotAndPlanWorkflow, mock.Anything).Return(plan, nil)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.UploadValidateCommitRebalanceActivity, mock.Anything, mock.Anything).Return(nil, errors.New("HarvestRebalanceCapacityChanged: target full"))

	env.ExecuteWorkflow(PollerRebalanceWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "HarvestRebalanceCapacityChanged")
}

func TestPollerRebalanceWorkflow_FullSuccess(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollerRebalanceWorkflow)
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)
	env.RegisterWorkflow(CleanupEmptyLeasesWorkflow)

	plan := &PollerRebalanceSnapshotPlanResult{
		Groups: []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, Count: 5}, {NodeGroupID: 2, Count: 50}},
		Moves:  []activities.HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10, 11}}},
	}
	env.OnWorkflow(SnapshotAndPlanWorkflow, mock.Anything).Return(plan, nil)

	staged := activities.RebalanceUploadStageResult{
		Staged: []activities.RebalanceStagedNode{
			{NodeID: 10, SourceGroupID: 1, TargetGroupID: 2, SourceLeaseName: "l1", TargetLeaseName: "l2", Port: "13001"},
			{NodeID: 11, SourceGroupID: 1, TargetGroupID: 2, SourceLeaseName: "l1", TargetLeaseName: "l2", Port: "13002"},
		},
	}
	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.UploadValidateCommitRebalanceActivity, mock.Anything, mock.Anything).Return(&staged, nil)
	env.OnWorkflow(CleanupEmptyLeasesWorkflow, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(PollerRebalanceWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPollerRebalanceWorkflow_CleanupFailsAfterCommit(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(PollerRebalanceWorkflow)
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)
	env.RegisterWorkflow(CleanupEmptyLeasesWorkflow)

	plan := &PollerRebalanceSnapshotPlanResult{
		Groups: []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, Count: 5}},
		Moves:  []activities.HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
	}
	env.OnWorkflow(SnapshotAndPlanWorkflow, mock.Anything).Return(plan, nil)

	staged := activities.RebalanceUploadStageResult{
		Staged: []activities.RebalanceStagedNode{{NodeID: 10, SourceGroupID: 1, TargetGroupID: 2, SourceLeaseName: "l1", TargetLeaseName: "l2", Port: "13001"}},
	}
	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.UploadValidateCommitRebalanceActivity, mock.Anything, mock.Anything).Return(&staged, nil)
	env.OnWorkflow(CleanupEmptyLeasesWorkflow, mock.Anything, mock.Anything).Return(errors.New("cleanup failed"))

	env.ExecuteWorkflow(PollerRebalanceWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "cleanup failed")
}

func TestSnapshotAndPlanWorkflow_GetCountsFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.GetNodeGroupsWithPollerCountsActivity, mock.Anything).Return(nil, errors.New("db error"))

	env.ExecuteWorkflow(SnapshotAndPlanWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "db error")
}

func TestSnapshotAndPlanWorkflow_BuildPlanFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)

	act := &activities.PollerRebalanceActivities{}
	snap := &activities.HarvestNodeGroupsSnapshotResult{
		Groups: []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, Count: 10}},
	}
	env.OnActivity(act.GetNodeGroupsWithPollerCountsActivity, mock.Anything).Return(snap, nil)
	env.OnActivity(act.BuildPollerRebalancePlanActivity, mock.Anything, mock.Anything).Return(nil, errors.New("plan error"))

	env.ExecuteWorkflow(SnapshotAndPlanWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "plan error")
}

func TestSnapshotAndPlanWorkflow_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(SnapshotAndPlanWorkflow)

	act := &activities.PollerRebalanceActivities{}
	snap := &activities.HarvestNodeGroupsSnapshotResult{
		Groups: []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, Count: 10}, {NodeGroupID: 2, Count: 50}},
	}
	planOutput := &activities.HarvestRebalancePlanOutput{
		Moves: []activities.HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{101}}},
	}
	env.OnActivity(act.GetNodeGroupsWithPollerCountsActivity, mock.Anything).Return(snap, nil)
	env.OnActivity(act.BuildPollerRebalancePlanActivity, mock.Anything, mock.Anything).Return(planOutput, nil)

	env.ExecuteWorkflow(SnapshotAndPlanWorkflow)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result PollerRebalanceSnapshotPlanResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Len(t, result.Groups, 2)
	assert.Len(t, result.Moves, 1)
}

func TestCleanupEmptyLeasesWorkflow_WithStagedCandidates(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(CleanupEmptyLeasesWorkflow)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.CleanupEmptyLeaseActivity, mock.Anything, mock.Anything).Return(nil)

	params := &CleanupEmptyLeasesWorkflowParams{
		Staged: []activities.RebalanceStagedNode{
			{SourceGroupID: 3, SourceLeaseName: "lease-3"},
			{SourceGroupID: 1, SourceLeaseName: "lease-1"},
		},
	}
	env.ExecuteWorkflow(CleanupEmptyLeasesWorkflow, params)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestCleanupEmptyLeasesWorkflow_NoCandidates(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(CleanupEmptyLeasesWorkflow)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.ListEmptyHarvestLeasesForCleanupActivity, mock.Anything).Return([]activities.EmptyHarvestLeaseCandidate{}, nil)

	env.ExecuteWorkflow(CleanupEmptyLeasesWorkflow, (*CleanupEmptyLeasesWorkflowParams)(nil))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestCleanupEmptyLeasesWorkflow_ListActivityFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(CleanupEmptyLeasesWorkflow)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.ListEmptyHarvestLeasesForCleanupActivity, mock.Anything).Return(nil, errors.New("list error"))

	env.ExecuteWorkflow(CleanupEmptyLeasesWorkflow, (*CleanupEmptyLeasesWorkflowParams)(nil))
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "list error")
}

func TestCleanupEmptyLeasesWorkflow_CleanupActivityFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(CleanupEmptyLeasesWorkflow)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.ListEmptyHarvestLeasesForCleanupActivity, mock.Anything).Return([]activities.EmptyHarvestLeaseCandidate{
		{NodeGroupID: 5, LeaseName: "harvest-l5"},
	}, nil)
	env.OnActivity(act.CleanupEmptyLeaseActivity, mock.Anything, mock.Anything).Return(errors.New("cleanup error"))

	env.ExecuteWorkflow(CleanupEmptyLeasesWorkflow, (*CleanupEmptyLeasesWorkflowParams)(nil))
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "cleanup error")
}

func TestPollerRebalanceUploadDestination(t *testing.T) {
	oldBucket := pollerRebalanceBucketName
	defer func() { pollerRebalanceBucketName = oldBucket }()

	pollerRebalanceBucketName = ""
	assert.Equal(t, pollerRebalanceHarvestUploadURL, pollerRebalanceUploadDestination())

	pollerRebalanceBucketName = "rebalance-bucket"
	assert.Equal(t, "gs://rebalance-bucket", pollerRebalanceUploadDestination())
}

func TestCleanupEmptyLeasesWorkflow_FromDBScan_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(CleanupEmptyLeasesWorkflow)

	act := &activities.PollerRebalanceActivities{}
	env.OnActivity(act.ListEmptyHarvestLeasesForCleanupActivity, mock.Anything).Return([]activities.EmptyHarvestLeaseCandidate{
		{NodeGroupID: 5, LeaseName: "harvest-l5"},
		{NodeGroupID: 3, LeaseName: "harvest-l3"},
	}, nil)
	env.OnActivity(act.CleanupEmptyLeaseActivity, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(CleanupEmptyLeasesWorkflow, (*CleanupEmptyLeasesWorkflowParams)(nil))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
