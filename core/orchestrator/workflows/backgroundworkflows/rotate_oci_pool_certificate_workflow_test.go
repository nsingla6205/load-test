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
	s.env.RegisterActivity(rotateActivity.OciServiceCertificateNeedsRotation)
	s.env.RegisterActivity(rotateActivity.CleanupAndStageOciServiceCertificateVersion)
	s.env.RegisterActivity(rotateActivity.VerifyOciServiceCertificatePending)
	s.env.RegisterActivity(rotateActivity.PromoteAndCommitOciServiceCertificateVersion)
	s.env.RegisterActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric)
	s.env.RegisterActivity(backgroundactivities.EmitOciCertificateRotationSuccessMetric)

	// The per-pool parent now orchestrates two child workflows.
	s.env.RegisterWorkflow(RotateOciPoolClientCertificateWorkflow)
	s.env.RegisterWorkflow(RotateOciPoolServerCertificateWorkflow)
	s.env.RegisterWorkflow(RotateOciPoolServiceCertificateWorkflow)

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

// expectNoSuccessMetric guards that the success metric is never emitted (used for
// skip/"not due" paths). The mock is allowed-but-not-required, and fails the test
// if it is ever invoked.
func (s *RotateOciCertificateWorkflowTestSuite) expectNoSuccessMetric() {
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationSuccessMetric,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) {
			s.Fail("success metric must not be emitted on skip/not-due path")
		}).Return(nil).Maybe()
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
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
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
	// On successful completion the server-cert workflow emits the success metric
	// with cert_type "server".
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationSuccessMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "server").Return(nil).Once()

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_GetPoolContextFailsEmitsMetricWithUnknownPoolName() {
	poolUUID := "pool-a"
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(nil, errors.New("not found"))
	// The pool row is not resolved when GetPoolContext fails, so the metric is emitted
	// with the poolUUID and an "unknown" pool external identifier. error_type is the
	// bounded "pool_lookup" category, not the raw error string.
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "unknown", "oci_certificate_rotation", "pool_lookup").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_NeedsRotationFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(false, errors.New("needs-rotation check failed"))
	// The pool is resolved by this point, so the metric carries the real pool external
	// identifier. error_type is the bounded "rotation_check" category.
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "rotation_check").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_NoRotationNeeded() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(false, nil)
	// Rotation is not due, so no success metric should be emitted.
	s.expectNoSuccessMetric()

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_StagingFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciONTAPCertificateVersion, mock.Anything, poolContext).
		Return(int64(0), errors.New("staging failed"))
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "staging").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_InstallVerifyFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
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
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "install_verify").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_PromoteFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
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
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "promote_commit").Return(nil)

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
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
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
	// On successful completion the client-cert workflow emits the success metric
	// with cert_type "client".
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationSuccessMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "client").Return(nil).Once()

	s.env.ExecuteWorkflow(RotateOciPoolClientCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestClientCertWorkflow_NoRotationNeeded() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciVCPClientCertificateNeedsRotation, mock.Anything, poolContext).
		Return(false, nil)
	// Rotation is not due, so no success metric should be emitted.
	s.expectNoSuccessMetric()

	s.env.ExecuteWorkflow(RotateOciPoolClientCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestClientCertWorkflow_VerifyFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
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
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "client_verify").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolClientCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestServiceCertWorkflow_Success() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciServiceCertificateNeedsRotation, mock.Anything, poolContext).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciServiceCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).VerifyOciServiceCertificatePending, mock.Anything, poolContext, int64(4)).
		Return(nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).PromoteAndCommitOciServiceCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(nil)
	// On successful completion the service-cert workflow emits the success metric
	// with cert_type "service".
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationSuccessMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "service").Return(nil).Once()

	s.env.ExecuteWorkflow(RotateOciPoolServiceCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestServiceCertWorkflow_NoRotationNeeded() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciServiceCertificateNeedsRotation, mock.Anything, poolContext).
		Return(false, nil)
	// Rotation is not due, so no success metric should be emitted.
	s.expectNoSuccessMetric()

	s.env.ExecuteWorkflow(RotateOciPoolServiceCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestServiceCertWorkflow_VerifyFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciServiceCertificateNeedsRotation, mock.Anything, poolContext).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciServiceCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).VerifyOciServiceCertificatePending, mock.Anything, poolContext, int64(4)).
		Return(errors.New("verify failed"))
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "service_verify").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolServiceCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestServiceCertWorkflow_GetPoolContextFailure() {
	poolUUID := "pool-a"
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(nil, errors.New("pool lookup failed"))
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "unknown", "oci_certificate_rotation", "pool_lookup").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolServiceCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestServiceCertWorkflow_NeedsRotationFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciServiceCertificateNeedsRotation, mock.Anything, poolContext).
		Return(false, errors.New("rotation check failed"))
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "service_rotation_check").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolServiceCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestServiceCertWorkflow_StagingFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciServiceCertificateNeedsRotation, mock.Anything, poolContext).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciServiceCertificateVersion, mock.Anything, poolContext).
		Return(int64(0), errors.New("staging failed"))
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "service_staging").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolServiceCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestServiceCertWorkflow_PromoteFailureEmitsMetric() {
	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciServiceCertificateNeedsRotation, mock.Anything, poolContext).
		Return(true, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).CleanupAndStageOciServiceCertificateVersion, mock.Anything, poolContext).
		Return(int64(4), nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).VerifyOciServiceCertificatePending, mock.Anything, poolContext, int64(4)).
		Return(nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).PromoteAndCommitOciServiceCertificateVersion, mock.Anything, poolContext, int64(4)).
		Return(errors.New("promote failed"))
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "service_promote_commit").Return(nil)

	s.env.ExecuteWorkflow(RotateOciPoolServiceCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_LaunchesServiceCertificateChild() {
	origServiceCert := env.OCIEnableServiceONTAPCert
	env.OCIEnableServiceONTAPCert = true
	s.T().Cleanup(func() { env.OCIEnableServiceONTAPCert = origServiceCert })

	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciServiceCertificateNeedsRotation, mock.Anything, poolContext).
		Return(false, nil).Once()
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(false, nil).Once()

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestPerPool_ClientChildFailureDoesNotBlockServer asserts that with the separate
// client mTLS flag ON, a failing client child is best-effort (logged, not fatal) and
// the server child still runs to completion.
func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_ClientChildFailureDoesNotBlockServer() {
	env.OCISeparateMTLSClientCert = true

	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	// Client child fails at the rotation-check step; parent logs + continues.
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciVCPClientCertificateNeedsRotation, mock.Anything, poolContext).
		Return(false, errors.New("client rotation check failed"))
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "client_rotation_check").Return(nil)
	// Server child proceeds and finds nothing to rotate.
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(false, nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	// Client failure is best-effort; the server child succeeded, so the parent succeeds.
	s.NoError(s.env.GetWorkflowError())
}

func (s *RotateOciCertificateWorkflowTestSuite) TestPerPool_ServiceChildFailureDoesNotBlockServer() {
	origServiceCert := env.OCIEnableServiceONTAPCert
	env.OCIEnableServiceONTAPCert = true
	s.T().Cleanup(func() { env.OCIEnableServiceONTAPCert = origServiceCert })

	poolUUID := "pool-a"
	poolContext := &backgroundactivities.PoolContext{
		Pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: poolUUID}, Name: "pool-a-name", PoolExternalIdentifier: "pool-a-ocid"},
		PoolUUID: poolUUID,
	}
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).GetPoolContext, mock.Anything, poolUUID).
		Return(poolContext, nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciServiceCertificateNeedsRotation, mock.Anything, poolContext).
		Return(false, errors.New("service rotation check failed"))
	s.env.OnActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric,
		mock.Anything, poolUUID, "pool-a-ocid", "oci_certificate_rotation", "service_rotation_check").Return(nil)
	s.env.OnActivity((&backgroundactivities.OciCertificateRotationActivity{}).OciONTAPCertificateNeedsRotation, mock.Anything, poolUUID).
		Return(false, nil)

	s.env.ExecuteWorkflow(RotateOciPoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func TestServiceCertRotateOciCertificateWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(RotateOciCertificateWorkflowTestSuite))
}
