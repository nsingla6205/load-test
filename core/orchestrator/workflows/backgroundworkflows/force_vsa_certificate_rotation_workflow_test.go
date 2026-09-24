package backgroundworkflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestForceVsaCertificateRotationWorkflow_QueuesExplicitPools(t *testing.T) {
	t.Setenv("ENABLE_FORCED_CERTIFICATE_ROTATION", "true")
	t.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", "true")
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	for _, poolUUID := range []string{"pool-a", "pool-b"} {
		env.OnActivity(
			controlActivity.ExecutePoolForcedCertificateRotationSequentially,
			mock.Anything,
			poolUUID,
			CertificateRotationChildWorkflowTimeout,
		).Return(nil).Once()
	}

	env.ExecuteWorkflow(
		ForceVsaCertificateRotationWorkflow,
		commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenancePoolList,
			PoolUUIDs:     []string{"pool-a", "pool-b"},
		},
	)

	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestForceVsaCertificateRotationWorkflow_ListsAllCertificatePools(t *testing.T) {
	t.Setenv("ENABLE_FORCED_CERTIFICATE_ROTATION", "true")
	t.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", "true")
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	env.OnActivity(
		rotationActivity.ListPoolsWithCertificateAuth,
		mock.Anything,
		0,
		CertificateRotationBatchSize,
	).Return(&backgroundactivities.ListPoolsBatchResult{
		Pools: []*datamodel.Pool{{BaseModel: datamodel.BaseModel{UUID: "pool-a"}}},
	}, nil).Once()
	env.OnActivity(
		controlActivity.ExecutePoolForcedCertificateRotationSequentially,
		mock.Anything,
		"pool-a",
		CertificateRotationChildWorkflowTimeout,
	).Return(nil).Once()

	env.ExecuteWorkflow(
		ForceVsaCertificateRotationWorkflow,
		commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenanceAll,
		},
	)

	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestForceVsaCertificateRotationWorkflow_RejectsDisabledFeature(t *testing.T) {
	t.Setenv("ENABLE_FORCED_CERTIFICATE_ROTATION", "false")
	t.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", "true")
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(
		ForceVsaCertificateRotationWorkflow,
		commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenanceAll,
		},
	)

	assert.ErrorContains(t, env.GetWorkflowError(), "ENABLE_FORCED_CERTIFICATE_ROTATION=false")
}

func TestForceVsaCertificateRotationWorkflow_RejectsDisabledRotation(t *testing.T) {
	t.Setenv("ENABLE_FORCED_CERTIFICATE_ROTATION", "true")
	t.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", "false")
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(
		ForceVsaCertificateRotationWorkflow,
		commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenanceAll,
		},
	)

	assert.ErrorContains(t, env.GetWorkflowError(), "ENABLE_VSA_CERTIFICATE_ROTATION=false")
}

func TestForceVsaCertificateRotationWorkflow_ListGuardPaths(t *testing.T) {
	t.Setenv("ENABLE_FORCED_CERTIFICATE_ROTATION", "true")
	t.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", "true")
	activityErr := temporal.NewNonRetryableApplicationError(
		"activity failed",
		"NonRetryableErr",
		errors.New("failure"),
	)

	t.Run("list failure", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		env.OnActivity(
			rotationActivity.ListPoolsWithCertificateAuth,
			mock.Anything,
			0,
			CertificateRotationBatchSize,
		).Return(nil, activityErr).Once()

		env.ExecuteWorkflow(
			ForceVsaCertificateRotationWorkflow,
			commonparams.PoolCertificateMaintenanceParams{
				SelectionType: commonparams.PoolCertificateMaintenanceAll,
			},
		)

		assert.ErrorContains(t, env.GetWorkflowError(), "list certificate-auth pools")
	})

	t.Run("nil batch", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		env.OnActivity(
			rotationActivity.ListPoolsWithCertificateAuth,
			mock.Anything,
			0,
			CertificateRotationBatchSize,
		).Return(nil, nil).Once()

		env.ExecuteWorkflow(
			ForceVsaCertificateRotationWorkflow,
			commonparams.PoolCertificateMaintenanceParams{
				SelectionType: commonparams.PoolCertificateMaintenanceAll,
			},
		)

		assert.ErrorContains(t, env.GetWorkflowError(), "returned nil")
	})

	t.Run("skips nil pool and advances page", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
		controlActivity := &backgroundactivities.ControlWorkflowActivity{}
		env.OnActivity(
			rotationActivity.ListPoolsWithCertificateAuth,
			mock.Anything,
			0,
			CertificateRotationBatchSize,
		).Return(&backgroundactivities.ListPoolsBatchResult{
			Pools: []*datamodel.Pool{
				nil,
				{BaseModel: datamodel.BaseModel{UUID: "pool-a"}},
			},
			HasMore: true,
		}, nil).Once()
		env.OnActivity(
			controlActivity.ExecutePoolForcedCertificateRotationSequentially,
			mock.Anything,
			"pool-a",
			CertificateRotationChildWorkflowTimeout,
		).Return(nil).Once()
		env.OnActivity(
			rotationActivity.ListPoolsWithCertificateAuth,
			mock.Anything,
			CertificateRotationBatchSize,
			CertificateRotationBatchSize,
		).Return(&backgroundactivities.ListPoolsBatchResult{}, nil).Once()

		env.ExecuteWorkflow(
			ForceVsaCertificateRotationWorkflow,
			commonparams.PoolCertificateMaintenanceParams{
				SelectionType: commonparams.PoolCertificateMaintenanceAll,
			},
		)

		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestForceVsaCertificateRotationWorkflow_ReturnsQueueFailure(t *testing.T) {
	t.Setenv("ENABLE_FORCED_CERTIFICATE_ROTATION", "true")
	t.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", "true")
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	env.OnActivity(
		controlActivity.ExecutePoolForcedCertificateRotationSequentially,
		mock.Anything,
		"pool-a",
		CertificateRotationChildWorkflowTimeout,
	).Return(temporal.NewNonRetryableApplicationError(
		"queue failed",
		"NonRetryableErr",
		errors.New("failure"),
	)).Once()

	env.ExecuteWorkflow(
		ForceVsaCertificateRotationWorkflow,
		commonparams.PoolCertificateMaintenanceParams{
			SelectionType: commonparams.PoolCertificateMaintenancePool,
			PoolUUIDs:     []string{"pool-a"},
		},
	)

	assert.ErrorContains(t, env.GetWorkflowError(), "queue forced certificate rotation")
}
