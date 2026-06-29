package backgroundworkflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"go.temporal.io/sdk/testsuite"
)

func TestHarvestPollerDriftReconcileWorkflow_Success(t *testing.T) {
	orig := enableHarvestPollerDriftReconcile
	enableHarvestPollerDriftReconcile = true
	t.Cleanup(func() { enableHarvestPollerDriftReconcile = orig })

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	activity := &activities.HarvestPollerDriftReconcileActivity{}
	env.OnActivity(activity.ReconcileHarvestPollerDrift, mock.Anything).
		Return(&activities.HarvestPollerDriftReconcileResult{}, nil).
		Once()

	env.ExecuteWorkflow(HarvestPollerDriftReconcileWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestHarvestPollerDriftReconcileWorkflow_ActivityError(t *testing.T) {
	orig := enableHarvestPollerDriftReconcile
	enableHarvestPollerDriftReconcile = true
	t.Cleanup(func() { enableHarvestPollerDriftReconcile = orig })

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	activity := &activities.HarvestPollerDriftReconcileActivity{}
	env.OnActivity(activity.ReconcileHarvestPollerDrift, mock.Anything).
		Return((*activities.HarvestPollerDriftReconcileResult)(nil), assert.AnError).
		Once()

	env.ExecuteWorkflow(HarvestPollerDriftReconcileWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestHarvestPollerDriftReconcileWorkflow_FeatureDisabled(t *testing.T) {
	orig := enableHarvestPollerDriftReconcile
	enableHarvestPollerDriftReconcile = false
	t.Cleanup(func() { enableHarvestPollerDriftReconcile = orig })

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(HarvestPollerDriftReconcileWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}
