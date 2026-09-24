package backgroundworkflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestReconcilePoolCAWorkflow(t *testing.T) {
	const (
		poolUUID     = "pool-uuid"
		desiredCAURI = "project/pool/ca"
	)
	tests := []struct {
		name          string
		needsRotation bool
	}{
		{name: "same desired CA skips rotation", needsRotation: false},
		{name: "desired CA drift rotates with explicit context", needsRotation: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			activity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
			env.OnActivity(
				activity.CertificateNeedsRotationForDesiredCA,
				mock.Anything,
				poolUUID,
				desiredCAURI,
			).Return(tc.needsRotation, nil)
			if tc.needsRotation {
				poolContext := &backgroundactivities.PoolContext{
					Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}},
					PoolUUID:     poolUUID,
					DesiredCAURI: desiredCAURI,
				}
				env.OnActivity(
					activity.GetPoolContextForDesiredCA,
					mock.Anything,
					poolUUID,
					desiredCAURI,
				).Return(poolContext, nil)
				env.OnActivity(
					activity.RotatePoolCertificateForDesiredCAWithContext,
					mock.Anything,
					poolContext,
				).Return(nil)
			}

			env.ExecuteWorkflow(ReconcilePoolCAWorkflow, poolUUID, desiredCAURI)
			assert.True(t, env.IsWorkflowCompleted())
			assert.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

func TestTrackedReconcilePoolCAWorkflowUpdatesChildJob(t *testing.T) {
	const (
		poolUUID     = "pool-uuid"
		desiredCAURI = "project/pool/ca"
		childJobID   = "child-job"
	)
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	commonActivity := &activities.CommonActivities{}
	for _, state := range []datamodel.JobState{
		datamodel.JobsStatePROCESSING,
		datamodel.JobsStateDONE,
	} {
		expectedState := state
		env.OnActivity(
			commonActivity.UpdateJobStatus,
			mock.Anything,
			mock.MatchedBy(func(job *datamodel.Job) bool {
				return job.UUID == childJobID &&
					job.State == string(expectedState) &&
					job.ErrorDetails == ""
			}),
		).Return(nil).Once()
	}
	env.OnActivity(
		rotationActivity.CertificateNeedsRotationForDesiredCA,
		mock.Anything,
		poolUUID,
		desiredCAURI,
	).Return(false, nil).Once()

	env.ExecuteWorkflow(
		TrackedReconcilePoolCAWorkflow,
		poolUUID,
		desiredCAURI,
		childJobID,
	)

	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestReconcilePoolCAWorkflowReturnsEligibilityCheckFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	activity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	activityErr := temporal.NewNonRetryableApplicationError(
		"eligibility check failed",
		"NonRetryableErr",
		errors.New("failure"),
	)
	env.OnActivity(
		activity.CertificateNeedsRotationForDesiredCA,
		mock.Anything,
		"pool-1",
		"project/pool/ca",
	).Return(false, activityErr).Once()

	env.ExecuteWorkflow(ReconcilePoolCAWorkflow, "pool-1", "project/pool/ca")

	assert.ErrorContains(t, env.GetWorkflowError(), "eligibility check failed")
}

func TestReconcilePoolCAWorkflowReturnsRotationFailures(t *testing.T) {
	const (
		poolUUID     = "pool-1"
		desiredCAURI = "project/pool/ca"
	)
	activityErr := temporal.NewNonRetryableApplicationError(
		"activity failed",
		"NonRetryableErr",
		errors.New("failure"),
	)

	t.Run("pool context failure", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		activity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		env.OnActivity(
			activity.CertificateNeedsRotationForDesiredCA,
			mock.Anything,
			poolUUID,
			desiredCAURI,
		).Return(true, nil).Once()
		env.OnActivity(
			activity.GetPoolContextForDesiredCA,
			mock.Anything,
			poolUUID,
			desiredCAURI,
		).Return(nil, activityErr).Once()

		env.ExecuteWorkflow(ReconcilePoolCAWorkflow, poolUUID, desiredCAURI)

		assert.ErrorContains(t, env.GetWorkflowError(), "activity failed")
	})

	t.Run("certificate rotation failure", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		activity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		poolContext := &backgroundactivities.PoolContext{
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}},
			PoolUUID:     poolUUID,
			DesiredCAURI: desiredCAURI,
		}
		env.OnActivity(
			activity.CertificateNeedsRotationForDesiredCA,
			mock.Anything,
			poolUUID,
			desiredCAURI,
		).Return(true, nil).Once()
		env.OnActivity(
			activity.GetPoolContextForDesiredCA,
			mock.Anything,
			poolUUID,
			desiredCAURI,
		).Return(poolContext, nil).Once()
		env.OnActivity(
			activity.RotatePoolCertificateForDesiredCAWithContext,
			mock.Anything,
			poolContext,
		).Return(activityErr).Once()

		env.ExecuteWorkflow(ReconcilePoolCAWorkflow, poolUUID, desiredCAURI)

		assert.ErrorContains(t, env.GetWorkflowError(), "activity failed")
	})
}

func TestReconcileVsaCAOnChangeWorkflow_QueuesStableBatch(t *testing.T) {
	const desiredCAURI = "project/pool/ca"
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	batch := &backgroundactivities.ListPoolsBatchResult{
		Pools: []*datamodel.Pool{
			{BaseModel: datamodel.BaseModel{UUID: "pool-a"}},
			{BaseModel: datamodel.BaseModel{UUID: "pool-b"}},
		},
	}
	env.OnActivity(
		rotationActivity.ListPoolsWithCertificateAuth,
		mock.Anything,
		0,
		vsaCAReconciliationBatchSize,
	).Return(batch, nil)
	env.OnActivity(
		controlActivity.ExecutePoolCAReconciliationSequentially,
		mock.Anything,
		"pool-a",
		desiredCAURI,
		mock.AnythingOfType("string"),
		vsaCAReconciliationWorkflowTimeout,
	).Return(nil)
	env.OnActivity(
		controlActivity.ExecutePoolCAReconciliationSequentially,
		mock.Anything,
		"pool-b",
		desiredCAURI,
		mock.AnythingOfType("string"),
		vsaCAReconciliationWorkflowTimeout,
	).Return(nil)

	env.ExecuteWorkflow(ReconcileVsaCAOnChangeWorkflow, allPoolCAReconciliationParams(desiredCAURI))
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestReconcileVsaCAOnChangeWorkflowGuardPaths(t *testing.T) {
	nonRetryable := temporal.NewNonRetryableApplicationError("activity failed", "NonRetryableErr", errors.New("failure"))

	t.Run("list failure", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		activity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		env.OnActivity(
			activity.ListPoolsWithCertificateAuth,
			mock.Anything,
			0,
			vsaCAReconciliationBatchSize,
		).Return(nil, nonRetryable).Once()

		env.ExecuteWorkflow(ReconcileVsaCAOnChangeWorkflow, allPoolCAReconciliationParams("project/pool/ca"))
		assert.ErrorContains(t, env.GetWorkflowError(), "list certificate-auth pools")
	})

	t.Run("nil batch", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		activity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		env.OnActivity(
			activity.ListPoolsWithCertificateAuth,
			mock.Anything,
			0,
			vsaCAReconciliationBatchSize,
		).Return(nil, nil).Once()

		env.ExecuteWorkflow(ReconcileVsaCAOnChangeWorkflow, allPoolCAReconciliationParams("project/pool/ca"))
		assert.ErrorContains(t, env.GetWorkflowError(), "returned nil")
	})

	t.Run("skips nil pool and advances page", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		activity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		env.OnActivity(
			activity.ListPoolsWithCertificateAuth,
			mock.Anything,
			0,
			vsaCAReconciliationBatchSize,
		).Return(&backgroundactivities.ListPoolsBatchResult{
			Pools:   []*datamodel.Pool{nil},
			HasMore: true,
		}, nil).Once()
		env.OnActivity(
			activity.ListPoolsWithCertificateAuth,
			mock.Anything,
			vsaCAReconciliationBatchSize,
			vsaCAReconciliationBatchSize,
		).Return(&backgroundactivities.ListPoolsBatchResult{}, nil).Once()

		env.ExecuteWorkflow(ReconcileVsaCAOnChangeWorkflow, allPoolCAReconciliationParams("project/pool/ca"))
		assert.NoError(t, env.GetWorkflowError())
	})

	t.Run("queue failure", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		controlActivity := &backgroundactivities.ControlWorkflowActivity{}
		env.OnActivity(
			rotationActivity.ListPoolsWithCertificateAuth,
			mock.Anything,
			0,
			vsaCAReconciliationBatchSize,
		).Return(&backgroundactivities.ListPoolsBatchResult{
			Pools: []*datamodel.Pool{{BaseModel: datamodel.BaseModel{UUID: "pool-1"}}},
		}, nil).Once()
		env.OnActivity(
			controlActivity.ExecutePoolCAReconciliationSequentially,
			mock.Anything,
			"pool-1",
			"project/pool/ca",
			mock.Anything,
			vsaCAReconciliationWorkflowTimeout,
		).Return(nonRetryable).Once()

		env.ExecuteWorkflow(ReconcileVsaCAOnChangeWorkflow, allPoolCAReconciliationParams("project/pool/ca"))
		assert.ErrorContains(t, env.GetWorkflowError(), "queue pool CA reconciliation")
	})
}

func TestReconcileVsaCAOnChangeWorkflow_QueuesExplicitPoolsWithoutListing(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	for _, poolUUID := range []string{"pool-a", "pool-b"} {
		env.OnActivity(
			controlActivity.ExecutePoolCAReconciliationSequentially,
			mock.Anything,
			poolUUID,
			"project/pool/ca",
			mock.AnythingOfType("string"),
			vsaCAReconciliationWorkflowTimeout,
		).Return(nil).Once()
	}

	env.ExecuteWorkflow(
		ReconcileVsaCAOnChangeWorkflow,
		commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenancePoolList,
			PoolUUIDs:     []string{"pool-a", "pool-b"},
			DesiredCAURI:  "project/pool/ca",
		},
	)

	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestReconcileVsaCAOnChangeWorkflow_ReturnsExplicitPoolQueueFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	env.OnActivity(
		controlActivity.ExecutePoolCAReconciliationSequentially,
		mock.Anything,
		"pool-a",
		"project/pool/ca",
		mock.AnythingOfType("string"),
		vsaCAReconciliationWorkflowTimeout,
	).Return(temporal.NewNonRetryableApplicationError(
		"queue failed",
		"NonRetryableErr",
		errors.New("failure"),
	)).Once()

	env.ExecuteWorkflow(
		ReconcileVsaCAOnChangeWorkflow,
		commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenancePool,
			PoolUUIDs:     []string{"pool-a"},
			DesiredCAURI:  "project/pool/ca",
		},
	)

	assert.ErrorContains(t, env.GetWorkflowError(), "queue pool CA reconciliation")
}

func allPoolCAReconciliationParams(desiredCAURI string) commonparams.PoolCertificateMaintenanceParams {
	return commonparams.PoolCertificateMaintenanceParams{
		SelectionType: commonparams.PoolCertificateMaintenanceAll,
		DesiredCAURI:  desiredCAURI,
	}
}
