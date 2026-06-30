package backgroundworkflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/testsuite"
)

type RotateOciCertificateWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *RotateOciCertificateWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	rotateActivity := &backgroundactivities.RotateOciVcpToVsaCertificateActivity{}
	s.env.RegisterActivity(rotateActivity.ListOciPoolsWithCertificateAuth)
	s.env.RegisterActivity(rotateActivity.DispatchOciPoolCertificateRotation)
	s.env.RegisterActivity(rotateActivity.GetPoolContext)
	s.env.RegisterActivity(rotateActivity.OciCertificateNeedsRotation)
	s.env.RegisterActivity(rotateActivity.CleanupAndStageOciCertificateVersion)
	s.env.RegisterActivity(rotateActivity.InstallAndVerifyOciCertificateVersion)
	s.env.RegisterActivity(rotateActivity.PromoteAndCommitOciCertificateVersion)
	s.env.RegisterActivity(backgroundactivities.EmitCertificateRotationFailureMetric)
}

func (s *RotateOciCertificateWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// withRotationEnabled flips the feature gate on for the duration of a test.
func (s *RotateOciCertificateWorkflowTestSuite) withRotationEnabled() {
	orig := env.EnableOCIVsaCertificateRotation
	env.EnableOCIVsaCertificateRotation = true
	s.T().Cleanup(func() { env.EnableOCIVsaCertificateRotation = orig })
}

// ---------------------------------------------------------------------------
// RotateOciVsaCertificateWorkflow (dispatcher)
// ---------------------------------------------------------------------------

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_Disabled_NoOp() {
	orig := env.EnableOCIVsaCertificateRotation
	env.EnableOCIVsaCertificateRotation = false
	s.T().Cleanup(func() { env.EnableOCIVsaCertificateRotation = orig })

	s.env.ExecuteWorkflow(RotateOciVsaCertificateWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_NoPools() {
	s.withRotationEnabled()

	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).ListOciPoolsWithCertificateAuth,
		mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: nil, HasMore: false}, nil).Once()

	s.env.ExecuteWorkflow(RotateOciVsaCertificateWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_DispatchesEachPool() {
	s.withRotationEnabled()

	pools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "pool-a"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-b"}},
	}
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).ListOciPoolsWithCertificateAuth,
		mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil).Once()
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).DispatchOciPoolCertificateRotation,
		mock.Anything, "pool-a", mock.Anything).Return(nil).Once()
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).DispatchOciPoolCertificateRotation,
		mock.Anything, "pool-b", mock.Anything).Return(nil).Once()

	s.env.ExecuteWorkflow(RotateOciVsaCertificateWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_ListFailureAborts() {
	s.withRotationEnabled()

	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).ListOciPoolsWithCertificateAuth,
		mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("list failed"))

	s.env.ExecuteWorkflow(RotateOciVsaCertificateWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_DispatchFailureDoesNotAbort() {
	s.withRotationEnabled()

	pools := []*datamodel.Pool{{BaseModel: datamodel.BaseModel{UUID: "pool-a"}}}
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).ListOciPoolsWithCertificateAuth,
		mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil).Once()
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).DispatchOciPoolCertificateRotation,
		mock.Anything, "pool-a", mock.Anything).Return(errors.New("dispatch failed")).Once()

	s.env.ExecuteWorkflow(RotateOciVsaCertificateWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	// A dispatch failure for one pool is logged but does not fail the sweep.
	s.NoError(s.env.GetWorkflowError())
}

// ---------------------------------------------------------------------------
// RotateOciPoolCertificateWorkflow (per-pool)
// ---------------------------------------------------------------------------

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_Success() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}

	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).OciCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).CleanupAndStageOciCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).InstallAndVerifyOciCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).PromoteAndCommitOciCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_GetPoolContextFailsEmitsMetricWithUnknownPoolName() {
	poolUUID := "pool-a"
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(nil, errors.New("not found"))
	// Pool name is not resolved when GetPoolContext fails, so the metric is emitted
	// with the poolUUID and an "unknown" pool name. error_type is the bounded
	// "pool_lookup" category, not the raw error string.
	s.env.OnActivity(backgroundactivities.EmitCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "unknown", "oci_certificate_rotation", "pool_lookup").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_NeedsRotationFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).OciCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(false, errors.New("needs-rotation check failed"))
	// Pool name is known by this point, so the metric carries the real pool name.
	// error_type is the bounded "rotation_check" category.
	s.env.OnActivity(backgroundactivities.EmitCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-name", "oci_certificate_rotation", "rotation_check").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_NoRotationNeeded() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).OciCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(false, nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_StagingFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).OciCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).CleanupAndStageOciCertificateVersion, mock.Anything, poolContext).
		Return(int64(0), errors.New("staging failed"))
	s.env.OnActivity(backgroundactivities.EmitCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-name", "oci_certificate_rotation", "staging").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_InstallVerifyFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).OciCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).CleanupAndStageOciCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).InstallAndVerifyOciCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(errors.New("install/verify failed"))
	s.env.OnActivity(backgroundactivities.EmitCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-name", "oci_certificate_rotation", "install_verify").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_PromoteFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).OciCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).CleanupAndStageOciCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).InstallAndVerifyOciCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(nil)
	s.env.OnActivity((&backgroundactivities.RotateOciVcpToVsaCertificateActivity{}).PromoteAndCommitOciCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(errors.New("promote failed"))
	s.env.OnActivity(backgroundactivities.EmitCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-name", "oci_certificate_rotation", "promote_commit").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func TestRotateOciCertificateWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(RotateOciCertificateWorkflowTestSuite))
}
