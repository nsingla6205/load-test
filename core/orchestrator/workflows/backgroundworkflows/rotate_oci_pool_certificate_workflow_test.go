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
	env              *testsuite.TestWorkflowEnvironment
	origSeparateMTLS bool
}

func (s *RotateOciCertificateWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	rotateActivity := &backgroundactivities.OciCertificateRotationActivity{}
	s.env.RegisterActivity(rotateActivity.ListOciPoolsWithCertificateAuth)
	s.env.RegisterActivity(rotateActivity.DispatchOciPoolCertificateRotation)
	s.env.RegisterActivity(rotateActivity.GetPoolContext)
	s.env.RegisterActivity(rotateActivity.OciONTAPCertificateNeedsRotation)
	s.env.RegisterActivity(rotateActivity.CleanupAndStageOciONTAPCertificateVersion)
	s.env.RegisterActivity(rotateActivity.InstallAndVerifyOciONTAPCertificateVersion)
	s.env.RegisterActivity(rotateActivity.PromoteAndCommitOciONTAPCertificateVersion)
	s.env.RegisterActivity(rotateActivity.OciVCPClientCertificateNeedsRotation)
	s.env.RegisterActivity(rotateActivity.CleanupAndStageOciVCPClientCertificateVersion)
	s.env.RegisterActivity(rotateActivity.PromoteAndCommitOciVCPClientCertificateVersion)
	s.env.RegisterActivity(rotateActivity.VerifyOciVCPClientCertificatePending)
	s.env.RegisterActivity(backgroundactivities.EmitCertificateRotationFailureMetric)

	// The per-pool parent now orchestrates two child workflows.
	s.env.RegisterWorkflow(RotateOciPoolClientCertificateWorkflow)
	s.env.RegisterWorkflow(RotateOciPoolServerCertificateWorkflow)

	// Default the separate-client-mTLS gate OFF so the server-rotation tests below
	// exercise only the server child. Client-child behaviour is covered by the
	// dedicated client-workflow tests, which flip it on explicitly.
	s.origSeparateMTLS = env.OCISeparateMTLSClientCert
	env.OCISeparateMTLSClientCert = false
}

func (s *RotateOciCertificateWorkflowTestSuite) AfterTest(suiteName, testName string) {
	env.OCISeparateMTLSClientCert = s.origSeparateMTLS
	s.env.AssertExpectations(s.T())
}

// withRotationEnabled flips the feature gate on for the duration of a test.
func (s *RotateOciCertificateWorkflowTestSuite) withRotationEnabled() {
	orig := env.EnableOCIVsaCertificateRotation
	env.EnableOCIVsaCertificateRotation = true
	s.T().Cleanup(func() { env.EnableOCIVsaCertificateRotation = orig })
}

// ---------------------------------------------------------------------------
// RotateOciCertificatesWorkflow (dispatcher)
// ---------------------------------------------------------------------------

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_Disabled_NoOp() {
	orig := env.EnableOCIVsaCertificateRotation
	env.EnableOCIVsaCertificateRotation = false
	s.T().Cleanup(func() { env.EnableOCIVsaCertificateRotation = orig })

	s.env.ExecuteWorkflow(RotateOciCertificatesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_NoPools() {
	s.withRotationEnabled()

	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).ListOciPoolsWithCertificateAuth,
		mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: nil, HasMore: false}, nil).Once()

	s.env.ExecuteWorkflow(RotateOciCertificatesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_DispatchesEachPool() {
	s.withRotationEnabled()

	pools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "pool-a"}},
		{BaseModel: datamodel.BaseModel{UUID: "pool-b"}},
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).ListOciPoolsWithCertificateAuth,
		mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil).Once()
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).DispatchOciPoolCertificateRotation,
		mock.Anything, "pool-a", mock.Anything).Return(nil).Once()
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).DispatchOciPoolCertificateRotation,
		mock.Anything, "pool-b", mock.Anything).Return(nil).Once()

	s.env.ExecuteWorkflow(RotateOciCertificatesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_ListFailureAborts() {
	s.withRotationEnabled()

	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).ListOciPoolsWithCertificateAuth,
		mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("list failed"))

	s.env.ExecuteWorkflow(RotateOciCertificatesWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestDispatcher_DispatchFailureDoesNotAbort() {
	s.withRotationEnabled()

	pools := []*datamodel.Pool{{BaseModel: datamodel.BaseModel{UUID: "pool-a"}}}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).ListOciPoolsWithCertificateAuth,
		mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil).Once()
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).DispatchOciPoolCertificateRotation,
		mock.Anything, "pool-a", mock.Anything).Return(errors.New("dispatch failed")).Once()

	s.env.ExecuteWorkflow(RotateOciCertificatesWorkflow)

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

	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciONTAPCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).InstallAndVerifyOciONTAPCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).PromoteAndCommitOciONTAPCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_GetPoolContextFailsEmitsMetricWithUnknownPoolName() {
	poolUUID := "pool-a"
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
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
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
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
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
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
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciONTAPCertificateVersion, mock.Anything, poolContext).
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
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciONTAPCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).InstallAndVerifyOciONTAPCertificateVersion, mock.Anything, poolContext, int64(4)).
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
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciONTAPCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).InstallAndVerifyOciONTAPCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).PromoteAndCommitOciONTAPCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(errors.New("promote failed"))
	s.env.OnActivity(backgroundactivities.EmitCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-name", "oci_certificate_rotation", "promote_commit").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// ---------------------------------------------------------------------------
// RotateOciPoolClientCertificateWorkflow / server + client orchestration
// ---------------------------------------------------------------------------

func (s *RotateOciCertificateWorkflowTestSuite) TestClientCertWorkflow_Success() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciVCPClientCertificateNeedsRotation, mock.Anything, poolContext).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciVCPClientCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).VerifyOciVCPClientCertificatePending, mock.Anything, poolContext, int64(4)).
		Return(nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).PromoteAndCommitOciVCPClientCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolClientCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestClientCertWorkflow_NoRotationNeeded() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciVCPClientCertificateNeedsRotation, mock.Anything, poolContext).
		Return(false, nil)

	s.env.ExecuteWorkflow(RotateOciPoolClientCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestClientCertWorkflow_VerifyFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciVCPClientCertificateNeedsRotation, mock.Anything, poolContext).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciVCPClientCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	// Verify runs before promote; on failure promote is never reached.
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).VerifyOciVCPClientCertificatePending, mock.Anything, poolContext, int64(4)).
		Return(errors.New("verify failed"))
	s.env.OnActivity(backgroundactivities.EmitCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-name", "oci_certificate_rotation", "client_verify").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolClientCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// TestPerPool_ClientChildFailureDoesNotBlockServer asserts that with the separate
// client mTLS flag ON, a failing client child is best-effort (logged, not fatal) and
// the server child still runs to completion.
func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_ClientChildFailureDoesNotBlockServer() {
	env.OCISeparateMTLSClientCert = true

	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	// Client child fails at the rotation-check step; parent logs + continues.
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciVCPClientCertificateNeedsRotation, mock.Anything, poolContext).
		Return(false, errors.New("client rotation check failed"))
	s.env.OnActivity(backgroundactivities.EmitCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-name", "oci_certificate_rotation", "client_rotation_check").Return(nil)
	// Server child proceeds and finds nothing to rotate.
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(false, nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	// Client failure is best-effort; the server child succeeded, so the parent succeeds.
	s.NoError(s.env.GetWorkflowError())
}

func TestRotateOciCertificateWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(RotateOciCertificateWorkflowTestSuite))
}
